#!/usr/bin/env bash
# scripts/hybrid/home-worker-join.sh
# ─────────────────────────────────────────────────────────────────────────────
# Idempotent home WSL2 worker join for the hybrid provider cell (ADR-046 §WS3/F).
# Reads join credentials from the hub-operator-minted Secret
# (<spoke>-home-worker-join) via SSH to the Hub kubeconfig.
#
# Usage:
#   ./home-worker-join.sh <node-index> [--env ./home-lab.env]
#
# node-index: 1 or 2 (selects the HOME_WORKER_NODES entry)
# ─────────────────────────────────────────────────────────────────────────────
set -euo pipefail

NODE_INDEX=""
ENV_FILE="$(dirname "$0")/home-lab.env"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --env) ENV_FILE="$2"; shift 2 ;;
    -*)    echo "ERROR: unknown arg $1" >&2; exit 1 ;;
    *)     NODE_INDEX="$1"; shift ;;
  esac
done
NODE_INDEX="${NODE_INDEX:-1}"

# ── Load env ─────────────────────────────────────────────────────────────────
if [[ ! -f "$ENV_FILE" ]]; then
  echo "ERROR: $ENV_FILE not found. Copy home-lab.env.example → home-lab.env and fill in values." >&2
  exit 1
fi
# shellcheck source=/dev/null
source "$ENV_FILE"

# ── Parse node entry ──────────────────────────────────────────────────────────
NODE_LINE=$(echo "$HOME_WORKER_NODES" | sed -n "${NODE_INDEX}p")
if [[ -z "$NODE_LINE" ]]; then
  echo "ERROR: No node at index ${NODE_INDEX} in HOME_WORKER_NODES" >&2
  exit 1
fi

IFS='|' read -r HOSTNAME SSH_TARGET WSL_DISTRO TAILNET_HOST BOX_TAG <<< "$NODE_LINE"
echo "=== Home worker join: ${HOSTNAME} (${BOX_TAG}) ==="
echo "    SSH:     ${SSH_TARGET}"
echo "    Tailnet: ${TAILNET_HOST}"
echo "    Spoke:   ${HYBRID_SPOKE_NAME}"

# ── 1. Ensure Tailscale is up on this machine ────────────────────────────────
echo "[join] Verifying Tailscale connectivity..."
if ! tailscale status --peers=false &>/dev/null; then
  echo "ERROR: Tailscale is not running. Run 'sudo tailscale up' first." >&2
  exit 1
fi
TAILSCALE_IP=$(tailscale ip -4 2>/dev/null || echo "")
if [[ -z "$TAILSCALE_IP" ]]; then
  echo "ERROR: Could not get Tailscale IPv4 address. Is 'tailscale up' complete?" >&2
  exit 1
fi
echo "[join] ✓ Tailscale IP: ${TAILSCALE_IP}"

# ── 1b. Pin DNS to 1.1.1.1 (Tailscale MagicDNS IPv6 misbehaves on WSL2) ─────
# Tailscale's default /etc/resolv.conf includes fd7a:115c:a1e0::53 which causes
# "server misbehaving" for external DNS queries, breaking containerd image pulls.
# Disable Tailscale DNS management and lock resolv.conf to public resolvers.
if ! grep -q '^nameserver 1.1.1.1' /etc/resolv.conf 2>/dev/null; then
  echo "[join] Pinning DNS to 1.1.1.1 (disabling Tailscale DNS override)..."
  tailscale set --accept-dns=false 2>/dev/null || true
  chattr -i /etc/resolv.conf 2>/dev/null || true
  printf 'nameserver 1.1.1.1\nnameserver 8.8.8.8\n' > /etc/resolv.conf
  chattr +i /etc/resolv.conf
  echo "[join] ✓ DNS pinned to 1.1.1.1"
fi

echo "[join] Checking if ${HOSTNAME} is already in the cluster..."
SPOKE_KUBECONFIG=$(mktemp /tmp/hybrid-spoke-XXXXXX)
trap 'rm -f "$SPOKE_KUBECONFIG"' EXIT

# Fetch spoke kubeconfig from Hub
kubectl --kubeconfig="${HUB_KUBECONFIG}" \
  get secret "${HYBRID_SPOKE_NAME}-kubeconfig" \
  -n platform-capi \
  -o jsonpath='{.data.value}' | base64 -d > "$SPOKE_KUBECONFIG"

if kubectl --kubeconfig="$SPOKE_KUBECONFIG" get node "${HOSTNAME}" &>/dev/null; then
  NODE_READY=$(kubectl --kubeconfig="$SPOKE_KUBECONFIG" get node "${HOSTNAME}" \
    -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || echo "Unknown")
  echo "[join] Node ${HOSTNAME} already in cluster (Ready: ${NODE_READY})"

  # Ensure services are active and running
  systemctl start tailscaled containerd kubelet 2>/dev/null || true

  # Ensure labels and unmanaged providerID are present so Hetzner CCM ignores home workers
  kubectl --kubeconfig="$SPOKE_KUBECONFIG" label node "${HOSTNAME}" \
    "workload-location=home" \
    "node-role.kubernetes.io/home=" \
    "node.kubernetes.io/exclude-from-external-load-balancers=true" \
    --overwrite 2>/dev/null || true
  kubectl --kubeconfig="$SPOKE_KUBECONFIG" patch node "${HOSTNAME}" \
    -p "{\"spec\":{\"providerID\":\"unmanaged://${HOSTNAME}\"}}" 2>/dev/null || true
  echo "[join] ✓ Labels and providerID verified"

  if [[ -f /etc/kubernetes/kubelet.conf ]]; then
    echo "[join] ✓ Node is already provisioned — exiting cleanly (no reset needed)"
    exit 0
  fi
fi

# ── 3. Fetch join payload from hub-operator Secret ───────────────────────────
echo "[join] Fetching join payload from Hub..."
JOIN_SECRET_NAME="${HYBRID_SPOKE_NAME}-home-worker-join"

JOIN_TOKEN=$(kubectl --kubeconfig="${HUB_KUBECONFIG}" \
  get secret "${JOIN_SECRET_NAME}" \
  -n platform-capi \
  -o jsonpath="{.data.node-${NODE_INDEX}-token}" 2>/dev/null | base64 -d)

JOIN_CA_HASH=$(kubectl --kubeconfig="${HUB_KUBECONFIG}" \
  get secret "${JOIN_SECRET_NAME}" \
  -n platform-capi \
  -o jsonpath="{.data.ca-cert-hash}" 2>/dev/null | base64 -d)

CONTROL_PLANE_ENDPOINT=$(kubectl --kubeconfig="${HUB_KUBECONFIG}" \
  get secret "${JOIN_SECRET_NAME}" \
  -n platform-capi \
  -o jsonpath="{.data.control-plane-endpoint}" 2>/dev/null | base64 -d)

if [[ -z "$JOIN_TOKEN" || -z "$JOIN_CA_HASH" || -z "$CONTROL_PLANE_ENDPOINT" ]]; then
  echo "ERROR: Join payload not ready. Wait for hub-operator to mint tokens (WS4)." >&2
  echo "       Check: kubectl --kubeconfig=${HUB_KUBECONFIG} get secret ${JOIN_SECRET_NAME} -n platform-capi" >&2
  exit 1
fi

# kubeadm join needs a bare host:port — never a scheme or path. Defensive
# normalization in case the hub-operator wrote a full URL (legacy bug).
CONTROL_PLANE_ENDPOINT="${CONTROL_PLANE_ENDPOINT#https://}"
CONTROL_PLANE_ENDPOINT="${CONTROL_PLANE_ENDPOINT#http://}"
CONTROL_PLANE_ENDPOINT="${CONTROL_PLANE_ENDPOINT%%/*}"

echo "[join] ✓ Join payload received (endpoint: ${CONTROL_PLANE_ENDPOINT})"

# ── 4. kubeadm join ──────────────────────────────────────────────────────────
# Guard: only reset if kubelet.conf is broken/unusable
if [[ -f /etc/kubernetes/kubelet.conf || -f /etc/kubernetes/pki/ca.crt ]]; then
  echo "[join] ⚠ Cleaning up previous partial join state..."
  kubeadm reset -f --cleanup-tmp-dir 2>&1 || true
  echo "[join] ✓ Reset complete"
fi

# Guard: WSL2 enables swap by default (/dev/sdc). Kubelet refuses to start
# with swap on unless --fail-swap-on=false is set. Disable swap for this
# session and install a persistent kubelet drop-in so reboots stay clean.
# Node labels are set via kubelet --node-labels (NOT a post-join kubectl
# patch) so they are applied at registration and survive re-joins. The
# exclude-from-external-load-balancers label stops the Hetzner CCM from
# crash-looping on non-Hetzner home nodes and from trying to register them
# as LoadBalancer targets (only the spoke CP node becomes an LB target).
echo "[join] Disabling swap + setting --node-ip=${TAILSCALE_IP}..."
swapoff -a 2>/dev/null || true
mkdir -p /etc/systemd/system/kubelet.service.d
cat > /etc/systemd/system/kubelet.service.d/20-wsl2-node-config.conf <<EOF
[Service]
Environment="KUBELET_EXTRA_ARGS=--fail-swap-on=false --node-ip=${TAILSCALE_IP} --node-labels=workload-location=home,node-role.kubernetes.io/home=,topology.kubernetes.io/zone=home,node.kubernetes.io/exclude-from-external-load-balancers=true"
EOF
systemctl daemon-reload
echo "[join] ✓ Swap off + kubelet --node-ip=${TAILSCALE_IP} + home-node labels drop-in installed"

# Guard: containerd must be up before kubeadm can start kubelet successfully.
echo "[join] Waiting for containerd socket..."
for i in $(seq 1 30); do
  if [[ -S /run/containerd/containerd.sock ]]; then
    echo "[join] ✓ containerd socket ready"
    break
  fi
  if [[ "$i" -eq 30 ]]; then
    echo "ERROR: containerd not ready after 30s — is containerd.service enabled?" >&2
    exit 1
  fi
  sleep 1
done

echo "[join] Running kubeadm join..."
kubeadm join "${CONTROL_PLANE_ENDPOINT}" \
  --token "${JOIN_TOKEN}" \
  --discovery-token-ca-cert-hash "sha256:${JOIN_CA_HASH}" \
  --node-name "${HOSTNAME}"

# Force --node-ip=${TAILSCALE_IP} in /var/lib/kubelet/kubeadm-flags.env so kubelet registers
# its Tailscale IP as InternalIP rather than the unroutable WSL2 internal NAT IP.
if [[ -f /var/lib/kubelet/kubeadm-flags.env ]]; then
  if grep -q '--node-ip=' /var/lib/kubelet/kubeadm-flags.env; then
    sed -i "s/--node-ip=[^ \"]*/--node-ip=${TAILSCALE_IP}/g" /var/lib/kubelet/kubeadm-flags.env
  else
    sed -i "s/KUBELET_KUBEADM_ARGS=\"/KUBELET_KUBEADM_ARGS=\"--node-ip=${TAILSCALE_IP} /g" /var/lib/kubelet/kubeadm-flags.env
  fi
  systemctl restart kubelet
  echo "[join] ✓ Forced --node-ip=${TAILSCALE_IP} in kubeadm-flags.env"
fi

# ── 5. Apply labels + taints ─────────────────────────────────────────────────
echo "[join] Applying workload-location labels..."
# Wait up to 120s for node to appear
for i in $(seq 1 24); do
  if kubectl --kubeconfig="$SPOKE_KUBECONFIG" get node "${HOSTNAME}" &>/dev/null; then
    break
  fi
  echo "[join] Waiting for node ${HOSTNAME} to appear in cluster ($((i*5))s)..."
  sleep 5
done

# ── 6. Verify Ready ──────────────────────────────────────────────────────────
echo "[join] Waiting for node Ready..."
kubectl --kubeconfig="$SPOKE_KUBECONFIG" wait node "${HOSTNAME}" \
  --for="condition=Ready" \
  --timeout=120s

echo ""
echo "=== ✓ ${HOSTNAME} joined and Ready ==="
echo "    workload-location: home"
echo "    Control plane:     ${CONTROL_PLANE_ENDPOINT}"

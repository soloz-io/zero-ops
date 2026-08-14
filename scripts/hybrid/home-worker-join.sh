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

# ── 2. Idempotency check — already a member? ─────────────────────────────────
echo "[join] Checking if ${HOSTNAME} is already in the cluster..."
SPOKE_KUBECONFIG=$(mktemp /tmp/hybrid-spoke-XXXXXX.kubeconfig)
trap 'rm -f "$SPOKE_KUBECONFIG"' EXIT

# Fetch spoke kubeconfig from Hub
kubectl --kubeconfig="${HUB_KUBECONFIG}" \
  get secret "${HYBRID_SPOKE_NAME}-kubeconfig" \
  -n platform-capi \
  -o jsonpath='{.data.value}' | base64 -d > "$SPOKE_KUBECONFIG"

if kubectl --kubeconfig="$SPOKE_KUBECONFIG" get node "${HOSTNAME}" &>/dev/null; then
  NODE_READY=$(kubectl --kubeconfig="$SPOKE_KUBECONFIG" get node "${HOSTNAME}" \
    -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}')
  echo "[join] Node ${HOSTNAME} already in cluster (Ready: ${NODE_READY})"

  # Ensure labels are present
  kubectl --kubeconfig="$SPOKE_KUBECONFIG" label node "${HOSTNAME}" \
    "workload-location=home" \
    "node-role.kubernetes.io/home=" \
    --overwrite
  echo "[join] ✓ Labels verified"
  exit 0
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
echo "[join] Running kubeadm join..."
kubeadm join "${CONTROL_PLANE_ENDPOINT}" \
  --token "${JOIN_TOKEN}" \
  --discovery-token-ca-cert-hash "sha256:${JOIN_CA_HASH}" \
  --node-name "${HOSTNAME}"

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

kubectl --kubeconfig="$SPOKE_KUBECONFIG" label node "${HOSTNAME}" \
  "workload-location=home" \
  "node-role.kubernetes.io/home=" \
  "topology.kubernetes.io/zone=home" \
  --overwrite

# ── 6. Verify Ready ──────────────────────────────────────────────────────────
echo "[join] Waiting for node Ready..."
kubectl --kubeconfig="$SPOKE_KUBECONFIG" wait node "${HOSTNAME}" \
  --for="condition=Ready" \
  --timeout=120s

echo ""
echo "=== ✓ ${HOSTNAME} joined and Ready ==="
echo "    workload-location: home"
echo "    Control plane:     ${CONTROL_PLANE_ENDPOINT}"

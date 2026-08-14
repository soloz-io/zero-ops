#!/usr/bin/env bash
# scripts/hybrid/setup-wsl2-node.sh
# ─────────────────────────────────────────────────────────────────────────────
# Idempotent WSL2 node setup for the hybrid provider cell (ADR-046 §WS3/F).
# Installs: Tailscale, containerd 1.7.26, runc 1.2.5, kubelet/kubeadm/kubectl
# 1.31.6 — matching the versions pinned in _shared/spokepool-clusterclass-v1.yaml.
#
# Usage:
#   sudo ./setup-wsl2-node.sh [node-index]
#   node-index: 1 or 2 (used to set WSL2_HOSTNAME in home-lab.env)
#
# Must be run inside the WSL2 Ubuntu instance as root (or with sudo).
# ─────────────────────────────────────────────────────────────────────────────
set -euo pipefail

NODE_INDEX="${1:-1}"
CONTAINERD_VERSION="1.7.26"
RUNC_VERSION="1.2.5"
K8S_VERSION="1.31.6"
K8S_MINOR="1.31"

echo "=== Hybrid WSL2 node setup (node-${NODE_INDEX}) ==="
echo "    containerd: ${CONTAINERD_VERSION}"
echo "    runc:       ${RUNC_VERSION}"
echo "    kubernetes: ${K8S_VERSION}"

# ── 0. Prerequisites ────────────────────────────────────────────────────────
if [[ "$(id -u)" != "0" ]]; then
  echo "ERROR: Must be run as root inside WSL2" >&2
  exit 1
fi

# Enable systemd in WSL2 (requires WSL 0.67.6+)
if ! grep -q "systemd=true" /etc/wsl.conf 2>/dev/null; then
  echo "[setup] Enabling systemd in /etc/wsl.conf"
  cat >> /etc/wsl.conf <<'EOF'
[boot]
systemd=true
EOF
  echo "[setup] ⚠ Restart WSL2 after this script completes to activate systemd"
fi

# ── 0b. Static resolv.conf (fixes tailscaled NoState crash-loop on boot) ────
# WSL auto-generates /etc/resolv.conf with only 172.27.x.1 (WinNAT gateway).
# At WSL2 startup the WinNAT DNS relay is not yet ready, so tailscaled fails
# to resolve controlplane.tailscale.com → crashes → 87+ restarts/boot.
# Fix: pin to 1.1.1.1/8.8.8.8 (available as soon as eth0 is up), keep
# 172.27.x.1 as last-resort for LAN names. Disable WSL auto-overwrite.
echo "[setup] Pinning /etc/resolv.conf to static resolvers (stops auto-overwrite)..."
if ! grep -q "generateResolvConf" /etc/wsl.conf; then
  printf '\n[network]\ngenerateResolvConf = false\n' >> /etc/wsl.conf
fi
# Remove symlink if WSL installed one
[ -L /etc/resolv.conf ] && rm /etc/resolv.conf
cat > /etc/resolv.conf <<'EOF'
# Static — managed by setup-wsl2-node.sh (ADR-046).
# generateResolvConf = false in /etc/wsl.conf prevents WSL from overwriting.
#
# ORDERING: 172.27.32.1 (WinNAT relay) MUST be first.
# UDP 53 to external IPs (1.1.1.1, 8.8.8.8) is blocked by Windows Defender
# Firewall from the vEthernet (WSL) interface. The WinNAT DNS relay is the
# only resolver reachable from WSL2 — it forwards to the Windows host DNS.
# Keep 1.1.1.1/8.8.8.8 as last-resort only (e.g. WinNAT fully torn down).
nameserver 172.27.32.1
nameserver 1.1.1.1
nameserver 8.8.8.8
EOF
chmod 644 /etc/resolv.conf
echo "[setup] ✓ /etc/resolv.conf: 172.27.32.1 (WinNAT relay) first, 1.1.1.1/8.8.8.8 fallback"

# ── 1. Swap off ──────────────────────────────────────────────────────────────
echo "[setup] Disabling swap..."
swapoff -a
sed -i '/swap/d' /etc/fstab

# ── 2. Kernel modules + sysctl ──────────────────────────────────────────────
echo "[setup] Loading kernel modules..."
modprobe overlay
modprobe br_netfilter
cat > /etc/modules-load.d/k8s.conf <<'EOF'
overlay
br_netfilter
EOF
cat > /etc/sysctl.d/k8s.conf <<'EOF'
net.bridge.bridge-nf-call-iptables  = 1
net.bridge.bridge-nf-call-ip6tables = 1
net.ipv4.ip_forward                 = 1
EOF
sysctl --system

# ── 3. Base packages ─────────────────────────────────────────────────────────
echo "[setup] Installing base packages..."
export DEBIAN_FRONTEND=noninteractive
apt-get update -y
apt-get install -y --no-install-recommends \
  at jq unzip wget curl socat mtr logrotate apt-transport-https \
  ca-certificates gnupg lsb-release \
  dnsutils

# ── 4. runc ─────────────────────────────────────────────────────────────────
if ! /usr/local/sbin/runc --version 2>/dev/null | grep -q "${RUNC_VERSION}"; then
  echo "[setup] Installing runc ${RUNC_VERSION}..."
  ARCH="$(dpkg --print-architecture)"
  wget -q "https://github.com/opencontainers/runc/releases/download/v${RUNC_VERSION}/runc.${ARCH}"
  wget -q "https://github.com/opencontainers/runc/releases/download/v${RUNC_VERSION}/runc.sha256sum"
  sha256sum --check --ignore-missing runc.sha256sum
  install "runc.${ARCH}" /usr/local/sbin/runc
  rm -f "runc.${ARCH}" runc.sha256sum
fi
echo "[setup] ✓ runc $(/usr/local/sbin/runc --version | head -1)"

# ── 5. containerd ────────────────────────────────────────────────────────────
if ! /usr/local/bin/containerd --version 2>/dev/null | grep -q "${CONTAINERD_VERSION}"; then
  echo "[setup] Installing containerd ${CONTAINERD_VERSION}..."
  ARCH="$(dpkg --print-architecture)"
  wget -q "https://github.com/containerd/containerd/releases/download/v${CONTAINERD_VERSION}/containerd-${CONTAINERD_VERSION}-linux-${ARCH}.tar.gz"
  wget -q "https://github.com/containerd/containerd/releases/download/v${CONTAINERD_VERSION}/containerd-${CONTAINERD_VERSION}-linux-${ARCH}.tar.gz.sha256sum"
  sha256sum --check "containerd-${CONTAINERD_VERSION}-linux-${ARCH}.tar.gz.sha256sum"
  tar -zxf "containerd-${CONTAINERD_VERSION}-linux-${ARCH}.tar.gz" -C /usr/local
  rm -f "containerd-${CONTAINERD_VERSION}-linux-${ARCH}.tar.gz" "containerd-${CONTAINERD_VERSION}-linux-${ARCH}.tar.gz.sha256sum"
fi

# containerd config
mkdir -p /etc/containerd
containerd config default > /etc/containerd/config.toml
sed -i "s/SystemdCgroup = false/SystemdCgroup = true/" /etc/containerd/config.toml

# containerd systemd unit
if [[ ! -f /etc/systemd/system/containerd.service ]]; then
  wget -q https://raw.githubusercontent.com/containerd/containerd/main/containerd.service \
    -O /etc/systemd/system/containerd.service
fi
systemctl daemon-reload
systemctl enable containerd
systemctl start containerd
echo "[setup] ✓ containerd $(/usr/local/bin/containerd --version)"

# ── 6. kubelet + kubeadm + kubectl ──────────────────────────────────────────
if ! kubelet --version 2>/dev/null | grep -q "${K8S_VERSION}"; then
  echo "[setup] Installing Kubernetes ${K8S_VERSION}..."
  mkdir -p /etc/apt/keyrings/
  curl -fsSL "https://pkgs.k8s.io/core:/stable:/v${K8S_MINOR}/deb/Release.key" \
    | gpg --dearmor -o /etc/apt/keyrings/kubernetes-apt-keyring.gpg
  echo "deb [signed-by=/etc/apt/keyrings/kubernetes-apt-keyring.gpg] https://pkgs.k8s.io/core:/stable:/v${K8S_MINOR}/deb/ /" \
    | tee /etc/apt/sources.list.d/kubernetes.list
  apt-get update -y
  apt-get install -y \
    "kubelet=${K8S_VERSION}-*" \
    "kubeadm=${K8S_VERSION}-*" \
    "kubectl=${K8S_VERSION}-*" \
    bash-completion
  apt-mark hold kubelet kubectl kubeadm
  systemctl enable kubelet
fi
echo "[setup] ✓ kubelet $(kubelet --version)"

# kubelet → containerd ordering drop-in
# Prevents kubelet from starting before containerd.sock exists at boot,
# which causes a restart-loop until the socket appears.
echo "[setup] Installing kubelet/containerd ordering drop-in..."
mkdir -p /etc/systemd/system/kubelet.service.d
cat > /etc/systemd/system/kubelet.service.d/10-containerd-ordering.conf <<'EOF'
[Unit]
After=containerd.service
Requires=containerd.service
EOF
systemctl daemon-reload
echo "[setup] ✓ kubelet drop-in: After=containerd.service Requires=containerd.service"

# ── 7. Tailscale ─────────────────────────────────────────────────────────────
if ! command -v tailscale &>/dev/null; then
  echo "[setup] Installing Tailscale..."
  curl -fsSL https://tailscale.com/install.sh | sh
fi

# tailscaled boot drop-in: wait for DNS before launching + throttle restart loop.
# Prevents the "no DNS fallback candidates" → NoState loop caused by tailscaled
# starting before /etc/resolv.conf resolvers are reachable (race with WinNAT).
echo "[setup] Installing tailscaled DNS-wait + restart-throttle drop-in..."
mkdir -p /etc/systemd/system/tailscaled.service.d
cat > /etc/systemd/system/tailscaled.service.d/10-wsl2-dns-wait.conf <<'EOF'
# WSL2 boot fix (ADR-046): wait for WinNAT DNS relay before launching tailscaled.
# Prevents "failed to resolve controlplane.tailscale.com: no DNS fallback
# candidates remain" -> NoState crash-loop (was: 87 restarts/boot).
#
# Probe: dig UDP/53 @ 172.27.32.1. WinNAT relay is UDP-only; TCP/53 probe
# (/dev/tcp) always fails. Polls every 2s for up to 90s, starts anyway if
# the relay never responds (graceful degradation).
#
# TimeoutStartSec=150: ExecStartPre can run up to 90s + ~10s for daemon
# startup. The systemd default of 90s caused the service to be killed while
# still in start-pre, producing a misleading "timeout exceeded" failure.
[Service]
ExecStartPre=/bin/bash -c '\
  echo "tailscaled-pre: waiting for 172.27.32.1 UDP/53 (max 90s)..."; \
  for i in $(seq 1 45); do \
    if dig @172.27.32.1 +timeout=2 +tries=1 +short controlplane.tailscale.com \
        >/dev/null 2>&1; then \
      echo "tailscaled-pre: DNS relay ready after $((i*2))s -- starting tailscaled"; \
      exit 0; \
    fi; \
    sleep 2; \
  done; \
  echo "tailscaled-pre: DNS relay not ready after 90s -- starting tailscaled anyway"; \
  exit 0'
TimeoutStartSec=150
RestartSec=10
StartLimitIntervalSec=300
StartLimitBurst=3
EOF
systemctl daemon-reload
echo "[setup] ✓ tailscaled drop-in: ExecStartPre DNS-wait, RestartSec=10, burst cap=3"

systemctl enable tailscaled
systemctl start tailscaled
echo "[setup] ✓ Tailscale $(tailscale version | head -1)"

# Interactive login: `tailscale up` without --authkey prints a browser URL and
# waits for approval. The caller approves it in the browser. Idempotent: if the
# node is already authenticated, tailscale up is a no-op.
if ! tailscale status --peers=false &>/dev/null; then
  echo "[setup] Running interactive Tailscale login..."
  echo "[setup] → Approve the URL below in your browser (or 'tailscale login' on the node):"
  tailscale up --hostname="wsl2-node-${NODE_INDEX}" || echo "[setup] ⚠ tailscale up returned non-zero (login may be pending)"
else
  echo "[setup] ✓ Tailscale already authenticated"
fi

# ── 8. Cleanup ───────────────────────────────────────────────────────────────
apt-get -y autoremove
apt-get -y clean

echo ""
echo "=== WSL2 node setup complete ==="
echo "    Next: run ./home-worker-join.sh ${NODE_INDEX} after:"
echo "    1. Tailscale is authenticated (tailscale status shows Connected)"
echo "    2. Ensuring the hub-operator has minted the join payload Secret"

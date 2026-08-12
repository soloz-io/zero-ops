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
  ca-certificates gnupg lsb-release

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

# ── 7. Tailscale ─────────────────────────────────────────────────────────────
if ! command -v tailscale &>/dev/null; then
  echo "[setup] Installing Tailscale..."
  curl -fsSL https://tailscale.com/install.sh | sh
fi
systemctl enable tailscaled
systemctl start tailscaled
echo "[setup] ✓ Tailscale $(tailscale version | head -1)"

# ── 8. Cleanup ───────────────────────────────────────────────────────────────
apt-get -y autoremove
apt-get -y clean

echo ""
echo "=== WSL2 node setup complete ==="
echo "    Next: run ./home-worker-join.sh ${NODE_INDEX} after:"
echo "    1. Running 'tailscale up --authkey=<key> --hostname=wsl2-node-${NODE_INDEX}'"
echo "    2. Ensuring the hub-operator has minted the join payload Secret"

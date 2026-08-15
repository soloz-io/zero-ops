#!/usr/bin/env bash
# scripts/hybrid/prepare-wsl2-cgroup.sh
# ─────────────────────────────────────────────────────────────────────────────
# Host-level cgroup2 + BPF prep for Cilium on WSL2 home-worker nodes.
#
# Background (ADR-046 §Cilium CgroupManager):
#   Cilium's kube-proxy-replacement attaches socket BPF to the host's root
#   cgroup and needs a real cgroup2 mount at /run/cilium/cgroupv2 that is
#   marked SHARED so the agent container's mount propagations can see it.
#   On WSL2 the stock mount-cgroup nsenter mount never persists, leaving
#   /run/cilium/cgroupv2 an empty dir — Cilium then stacks a fresh empty
#   cgroup2 root and disables CgroupManager → transparent-DNS-proxy inactive.
#
# This script creates the host-level shared mounts Cilium requires:
#   1. /run/cilium/cgroupv2  → cgroup2, --make-shared
#   2. /sys/fs/bpf           → bpf,   --make-shared
# It also installs a systemd unit so the mounts survive WSL2 reboots
# (/run is tmpfs — mounts do not persist across reboots without this).
#
# Run before installing/rolling Cilium on the hybrid spoke. Idempotent.
#
# Usage:
#   ./prepare-wsl2-cgroup.sh [--env ./home-lab.env] [--apply-systemd]
# ─────────────────────────────────────────────────────────────────────────────
set -euo pipefail

ENV_FILE="$(dirname "$0")/home-lab.env"
APPLY_SYSTEMD=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --env) ENV_FILE="$2"; shift 2 ;;
    --apply-systemd) APPLY_SYSTEMD=1; shift ;;
    *) echo "ERROR: unknown arg $1" >&2; exit 1 ;;
  esac
done

if [[ ! -f "$ENV_FILE" ]]; then
  echo "ERROR: $ENV_FILE not found. Copy home-lab.env.example → home-lab.env." >&2
  exit 1
fi
# shellcheck source=/dev/null
source "$ENV_FILE"

# Remote setup script executed as root inside each WSL2 distro.
# Idempotent: mounts only if not already mounted; --make-shared is harmless
# when already shared.
# NOTE: shipped to the WSL2 distro as base64 so the Windows PowerShell layer
# cannot interpret the script's $(...) command substitutions.
REMOTE_SETUP_B64=$(base64 <<'SCRIPT'
set -euo pipefail
echo "==> cgroup2 mount at /run/cilium/cgroupv2"
mkdir -p /run/cilium/cgroupv2
if ! findmnt -n -t cgroup2 /run/cilium/cgroupv2 >/dev/null 2>&1; then
  mount -t cgroup2 none /run/cilium/cgroupv2
  echo "    mounted cgroup2"
else
  echo "    already cgroup2"
fi
mount --make-shared /run/cilium/cgroupv2
echo "    findmnt: $(findmnt -n -o TARGET,FSTYPE,OPTIONS /run/cilium/cgroupv2)"

echo "==> bpf mount at /sys/fs/bpf"
mkdir -p /sys/fs/bpf
if ! findmnt -n -t bpf /sys/fs/bpf >/dev/null 2>&1; then
  mount -t bpf bpf /sys/fs/bpf
  echo "    mounted bpf"
else
  echo "    already bpf"
fi
mount --make-shared /sys/fs/bpf
echo "    findmnt: $(findmnt -n -o TARGET,FSTYPE,OPTIONS /sys/fs/bpf)"
SCRIPT
)

# Optional systemd unit to re-create the mounts at boot (only with --apply-systemd).
SYSTEMD_UNIT="[Unit]
Description=Cilium host cgroup2+BPF prep (WSL2 home worker)
Before=containerd.service kubelet.service
After=local-fs.target

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/bin/bash -c '\''mkdir -p /run/cilium/cgroupv2 && mount -t cgroup2 none /run/cilium/cgroupv2 || true; mount --make-shared /run/cilium/cgroupv2 || true; mount -t bpf bpf /sys/fs/bpf || true; mount --make-shared /sys/fs/bpf || true'\''

[Install]
WantedBy=multi-user.target
"

echo "=== Cilium cgroup2/BPF host prep on WSL2 home-worker nodes ==="
echo "    Spoke: ${HYBRID_SPOKE_NAME}"
echo ""

FAILED=0
while IFS='|' read -r HOSTNAME SSH_TARGET WSL_DISTRO _TAILNET _TAG; do
  [[ -z "$HOSTNAME" ]] && continue
  echo "--- ${HOSTNAME} (${SSH_TARGET} / wsl:${WSL_DISTRO}) ---"

  # SSH lands on Windows OpenSSH (PowerShell). Every remote command ships
  # base64-encoded so PowerShell cannot interpret $()/substitutions, then is
  # decoded and executed inside the Linux distro as root.
  WSL_RUN() { ssh -o BatchMode=yes -o ConnectTimeout=20 -o StrictHostKeyChecking=no "${SSH_TARGET}" "wsl.exe -d ${WSL_DISTRO} -u root -e bash -lc 'echo $1 | base64 -d | bash'"; }

  echo "    running cgroup2/BPF prep..."
  if ! WSL_RUN "$REMOTE_SETUP_B64"; then
    echo "    ✗ ${HOSTNAME}: cgroup prep failed" >&2
    FAILED=1
    continue
  fi

  if [[ "$APPLY_SYSTEMD" == "1" ]]; then
    echo "    installing cilium-host-prep systemd unit..."
    SYSTEMD_B64=$(base64 <<SCRIPT
cat > /etc/systemd/system/cilium-host-prep.service <<'UNITEOF'
${SYSTEMD_UNIT}
UNITEOF
systemctl daemon-reload && systemctl enable cilium-host-prep.service
SCRIPT
)
    if ! WSL_RUN "$SYSTEMD_B64"; then
      echo "    ✗ ${HOSTNAME}: systemd unit install failed" >&2
      FAILED=1
    else
      echo "    ✓ ${HOSTNAME}: cilium-host-prep.service enabled"
    fi
  fi

  echo "    ✓ ${HOSTNAME}: host prep complete"
  echo ""
done <<< "$HOME_WORKER_NODES"

if [[ "$FAILED" -ne 0 ]]; then
  echo "ERROR: One or more nodes failed cgroup prep. Resolve before rolling Cilium." >&2
  exit 1
fi

echo ""
echo "=== ✓ All WSL2 nodes have shared cgroup2/BPF mounts ==="
echo "Next step:"
echo "  kubectl rollout restart daemonset/cilium -n kube-system --context <hybrid-spoke>"

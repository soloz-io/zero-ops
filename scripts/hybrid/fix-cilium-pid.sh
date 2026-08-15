#!/usr/bin/env bash
# scripts/hybrid/fix-cilium-pid.sh
# ─────────────────────────────────────────────────────────────────────────────
# Pre-rollout remediation: clear stale /var/run/cilium/cilium.pid on WSL2 nodes.
#
# Background: Cilium's clean-cilium-state init container reads the pidfile and
# signals the process. On WSL2 nodes the pidfile may contain PID 1 (always
# alive), causing the signal to be sent to the init process and clean-cilium-state
# to loop forever / crash, blocking the DaemonSet rollout.
#
# Run this before EVERY `kubectl rollout restart daemonset/cilium` on the
# hybrid spoke to pre-clear the pidfile on all WSL2 home-worker nodes.
# The Hetzner CP node does not require this (its pidfile is managed correctly).
#
# Usage:
#   ./fix-cilium-pid.sh [--env ./home-lab.env]
# ─────────────────────────────────────────────────────────────────────────────
set -euo pipefail

ENV_FILE="$(dirname "$0")/home-lab.env"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --env) ENV_FILE="$2"; shift 2 ;;
    *) echo "ERROR: unknown arg $1" >&2; exit 1 ;;
  esac
done

if [[ ! -f "$ENV_FILE" ]]; then
  echo "ERROR: $ENV_FILE not found. Copy home-lab.env.example → home-lab.env." >&2
  exit 1
fi
# shellcheck source=/dev/null
source "$ENV_FILE"

echo "=== Pre-rollout: clearing stale Cilium pidfile on WSL2 home-worker nodes ==="
echo "    Spoke: ${HYBRID_SPOKE_NAME}"
echo ""

FAILED=0
while IFS='|' read -r HOSTNAME SSH_TARGET WSL_DISTRO _TAILNET _TAG; do
  [[ -z "$HOSTNAME" ]] && continue
  echo "--- ${HOSTNAME} (${SSH_TARGET} / wsl:${WSL_DISTRO}) ---"

  # The SSH target lands on the Windows OpenSSH server (PowerShell). Wrap every
  # remote command through wsl.exe so it executes as root inside the Linux distro.
  WSL() { ssh -o BatchMode=yes -o ConnectTimeout=20 -o StrictHostKeyChecking=no "${SSH_TARGET}" "wsl.exe -d ${WSL_DISTRO} -u root -e bash -lc \"$*\""; }

  # Check if pidfile exists and what it contains
  PID_CONTENT=$(WSL 'cat /var/run/cilium/cilium.pid 2>/dev/null || echo NOT_FOUND')

  if [[ "$PID_CONTENT" == "NOT_FOUND" ]]; then
    echo "    ✓ /var/run/cilium/cilium.pid not present — nothing to do"
    continue
  fi

  echo "    pidfile contains: ${PID_CONTENT}"

  # A pidfile containing PID 1 (or a PID that resolves to the host init process)
  # is always stale — Cilium never legitimately runs as PID 1.
  if [[ "$PID_CONTENT" == "1" ]]; then
    echo "    ⚠ Stale pidfile detected (PID 1 = host init) — removing"
    if WSL 'rm -f /var/run/cilium/cilium.pid && echo REMOVED'; then
      echo "    ✓ ${HOSTNAME}: pidfile cleared"
    else
      echo "    ✗ ${HOSTNAME}: failed to remove pidfile" >&2
      FAILED=1
    fi
    continue
  fi

  # Check if the PID in the file is actually a running Cilium process.
  IS_CILIUM=$(WSL "cat /proc/${PID_CONTENT}/comm 2>/dev/null || echo NOT_FOUND")

  if [[ "$IS_CILIUM" == "cilium-agent" ]]; then
    echo "    ✓ pidfile references a live cilium-agent (PID ${PID_CONTENT}) — leaving intact"
  else
    echo "    ⚠ pidfile references stale PID ${PID_CONTENT} (comm: ${IS_CILIUM}) — removing"
    if WSL 'rm -f /var/run/cilium/cilium.pid && echo REMOVED'; then
      echo "    ✓ ${HOSTNAME}: pidfile cleared"
    else
      echo "    ✗ ${HOSTNAME}: failed to remove pidfile" >&2
      FAILED=1
    fi
  fi
  echo ""
done <<< "$HOME_WORKER_NODES"

if [[ "$FAILED" -ne 0 ]]; then
  echo "ERROR: One or more nodes failed pidfile cleanup. Resolve before rollout." >&2
  exit 1
fi

echo ""
echo "=== ✓ All WSL2 nodes cleared. Safe to rollout restart Cilium DaemonSet ==="
echo ""
echo "Next step:"
echo "  kubectl rollout restart daemonset/cilium -n kube-system --context <hybrid-spoke>"
echo "  kubectl rollout status  daemonset/cilium -n kube-system --context <hybrid-spoke>"

#!/usr/bin/env bash
# =============================================================================
# zero-ops/scripts/hybrid/wsl2/tailscale-watchdog.sh
#
# WSL2-side self-healing watchdog for hybrid Kubernetes nodes.
# Installed as a systemd service + timer by harden-windows-host.sh.
# Runs every 60 seconds INSIDE the WSL2 distro — no Mac dependency.
#
# Heals:
#   1. tailscaled dead / Disconnected / NoState
#   2. kubelet crashed (restart loop / OOM)
#   3. containerd socket gone
#
# Config:  /etc/soloz/node-config.env   (written by installer)
# Auth-key: /etc/soloz/tailscale-authkey (written by installer, chmod 600)
# Log:     journald (tag: tailscale-watchdog) — view with:
#            journalctl -t tailscale-watchdog -f
# =============================================================================
set -uo pipefail

CONFIG_FILE="/etc/soloz/node-config.env"
AUTHKEY_FILE="/etc/soloz/tailscale-authkey"
LOG_TAG="tailscale-watchdog"

log()  { logger -t "$LOG_TAG" -- "[INFO]  $*"; }
warn() { logger -t "$LOG_TAG" -- "[WARN]  $*"; }
err()  { logger -t "$LOG_TAG" -- "[ERROR] $*"; }

# Load node config (NODE_HOSTNAME, TAILSCALE_FLAGS)
if [[ ! -f "$CONFIG_FILE" ]]; then
    err "Config not found: $CONFIG_FILE — run harden-windows-host.sh first."
    exit 0  # Don't fail the timer — will retry next cycle
fi
# shellcheck source=/dev/null
source "$CONFIG_FILE"

# ── heal_tailscale ────────────────────────────────────────────────────────────
heal_tailscale() {
    # Check if we have a live Tailscale IP
    local ts_ip
    ts_ip=$(tailscale ip -4 2>/dev/null || true)

    if [[ "$ts_ip" =~ ^100\. ]]; then
        return 0  # Connected — nothing to do
    fi

    # Get state for finer-grained decision
    local ts_status
    ts_status=$(tailscale status --peers=false 2>&1 || true)
    warn "Tailscale not connected (ip='$ts_ip'). Status: $(echo "$ts_status" | head -2 | tr '\n' ' ')"

    # Ensure tailscaled itself is running
    if ! systemctl is-active --quiet tailscaled; then
        warn "tailscaled not active — restarting"
        systemctl restart tailscaled
        sleep 3
    fi

    # Decide whether we need auth-key (NoState/NeedsLogin) or just reconnect
    local needs_login=false
    if echo "$ts_status" | grep -qiE 'NoState|NeedsLogin|logged out|fetch control key'; then
        needs_login=true
    fi

    if [[ "$needs_login" == "true" ]]; then
        if [[ -f "$AUTHKEY_FILE" && -s "$AUTHKEY_FILE" ]]; then
            warn "Tailscale NeedsLogin — reconnecting with auth-key"
            # shellcheck disable=SC2086
            tailscale up ${TAILSCALE_FLAGS} --auth-key="file:${AUTHKEY_FILE}" 2>&1 | logger -t "$LOG_TAG" || true
        else
            err "Tailscale NeedsLogin but no auth-key at $AUTHKEY_FILE — manual re-auth required"
            return 1
        fi
    else
        warn "Tailscale Disconnected — reconnecting"
        # shellcheck disable=SC2086
        tailscale up ${TAILSCALE_FLAGS} 2>&1 | logger -t "$LOG_TAG" || true
    fi

    # Verify
    sleep 4
    ts_ip=$(tailscale ip -4 2>/dev/null || true)
    if [[ "$ts_ip" =~ ^100\. ]]; then
        log "Tailscale reconnected: $ts_ip"
    else
        err "Tailscale still not connected after recovery (ip='$ts_ip')"
    fi
}

# ── heal_containerd ───────────────────────────────────────────────────────────
heal_containerd() {
    if systemctl is-active --quiet containerd; then
        return 0
    fi
    warn "containerd not active — restarting"
    systemctl restart containerd
    sleep 3
    if systemctl is-active --quiet containerd; then
        log "containerd restarted successfully"
    else
        err "containerd failed to restart"
    fi
}

# ── heal_kubelet ──────────────────────────────────────────────────────────────
heal_kubelet() {
    if systemctl is-active --quiet kubelet; then
        return 0
    fi
    warn "kubelet not active — restarting"
    # Ensure containerd is up first (kubelet depends on it)
    heal_containerd
    systemctl restart kubelet
    sleep 5
    if systemctl is-active --quiet kubelet; then
        log "kubelet restarted successfully"
    else
        err "kubelet failed to restart — check: journalctl -u kubelet -n 50"
    fi
}

# ── heal_cilium_pid ───────────────────────────────────────────────────────────
heal_cilium_pid() {
    local pidfile="/var/run/cilium/cilium.pid"
    if [[ -f "$pidfile" ]]; then
        local pid_content
        pid_content=$(cat "$pidfile" 2>/dev/null || echo "")
        if [[ "$pid_content" == "1" ]]; then
            warn "Stale Cilium pidfile found (PID 1) — removing"
            rm -f "$pidfile"
        elif [[ -n "$pid_content" ]]; then
            local comm
            comm=$(cat "/proc/${pid_content}/comm" 2>/dev/null || echo "")
            if [[ "$comm" != "cilium-agent" ]]; then
                warn "Stale Cilium pidfile found (PID $pid_content, comm: '$comm') — removing"
                rm -f "$pidfile"
            fi
        fi
    fi
}

# ── main ─────────────────────────────────────────────────────────────────────
log "=== watchdog cycle (node=${NODE_HOSTNAME:-unknown}) ==="

heal_tailscale
heal_containerd
heal_kubelet
heal_cilium_pid

log "=== cycle done ==="

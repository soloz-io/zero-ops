#!/usr/bin/env bash
# scripts/hybrid/recover-wsl2-nat.sh
# ─────────────────────────────────────────────────────────────────────────────
# Mac-side recovery for a WSL2 node that has lost network after a Windows host
# reboot / sleep / NAT glitch (ADR-046 §WS3/F).
#
# Symptoms this fixes:
#   • No DNS inside WSL2 (nameserver 172.27.x.1 dead; resolv.conf stale)
#   • No raw TCP outbound (curl https://1.1.1.1 → 000 / connect timeout)
#   • Gateway ARP-resolves but connect times out (WinNAT stack torn down)
#   • Tailscale stuck NoState / logged-out ("no DNS fallback candidates remain")
#   • kubelet restart-loop (/run/containerd/containerd.sock missing at boot)
#
# Recovery sequence per node:
#   1. SSH → Windows: wsl --shutdown  (quiesce distro cleanly)
#   2. SSH → Windows: restart SharedAccess + HNS  (rebuild WinNAT/vEthernet WSL)
#   3. Wait for vEthernet (WSL) adapter to reappear
#   4. Start WSL distro + verify TCP (curl 1.1.1.1)
#   5. Re-authenticate Tailscale (prints URL; user approves in browser if needed)
#   6. Push kubelet drop-in (After=containerd.service) if not already present
#   7. Re-run join-home-workers.sh --node N
#
# Usage:
#   ./recover-wsl2-nat.sh [--node N] [--env ./home-lab.env] [--skip-join]
#     --node N      only recover node index N (1-based). Default: all nodes.
#     --env F       path to home-lab.env. Default: scripts/hybrid/home-lab.env
#     --skip-join   stop after network+Tailscale recovery; don't re-run join
# ─────────────────────────────────────────────────────────────────────────────
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
ENV_FILE="${HERE}/home-lab.env"
ONLY_NODE=""
SKIP_JOIN=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --node)      ONLY_NODE="$2"; shift 2 ;;
    --env)       ENV_FILE="$2"; shift 2 ;;
    --skip-join) SKIP_JOIN=1; shift ;;
    *) echo "ERROR: unknown arg $1" >&2; exit 1 ;;
  esac
done

if [[ ! -f "$ENV_FILE" ]]; then
  echo "ERROR: $ENV_FILE not found. Copy home-lab.env.example → home-lab.env." >&2
  exit 1
fi
# shellcheck source=/dev/null
source "$ENV_FILE"

echo "=== WSL2 NAT recovery driver ==="
echo "    Spoke:   ${HYBRID_SPOKE_NAME}"
echo "    Tailnet: ${TAILNET_NAME}"
echo ""

# ── win_ps ───────────────────────────────────────────────────────────────────
# Run a PowerShell snippet on the Windows host via SSH (base64-encoded to
# survive quoting). Returns the raw stdout of the PS invocation.
win_ps() {
  local SSH_TARGET="$1" SCRIPT="$2"
  local B64
  B64=$(printf '%s' "$SCRIPT" | iconv -f UTF-8 -t UTF-16LE | base64 | tr -d '\n')
  ssh -o BatchMode=yes -o ConnectTimeout=10 -o StrictHostKeyChecking=no \
    "$SSH_TARGET" "powershell -NoProfile -EncodedCommand $B64" 2>/dev/null
}

# ── wsl_exec ─────────────────────────────────────────────────────────────────
# Robustly executes a bash script snippet inside WSL via SSH to the Windows host.
# Bypasses Windows shell quoting/escaping issues by piping the command via stdin.
wsl_exec() {
  local SSH_TARGET="$1" WSL_DISTRO="$2" CMD="$3"
  printf '%s\n' "$CMD" | ssh -o BatchMode=yes -o ConnectTimeout=10 -o StrictHostKeyChecking=no \
    "$SSH_TARGET" "wsl.exe -d $WSL_DISTRO -u root -e bash -l"
}

# ── restart_winnat ───────────────────────────────────────────────────────────
# Shuts down WSL, restarts WinNAT (SharedAccess) and HNS so the vEthernet
# (WSL) adapter is fully rebuilt.  The sequence matters:
#   wsl --shutdown → stop services → start services
# SharedAccess = ICS (manages the DHCP/NAT for vEthernet (WSL)).
# HNS = Host Network Service (manages the virtual switch itself).
restart_winnat() {
  local SSH_TARGET="$1"
  echo "    → shutting down WSL distro (wsl --shutdown)..."
  local SHUTDOWN_OUT
  SHUTDOWN_OUT=$(win_ps "$SSH_TARGET" "wsl --shutdown; Start-Sleep -Seconds 3; Write-Output 'SHUTDOWN=OK'")
  if echo "$SHUTDOWN_OUT" | grep -q 'SHUTDOWN=OK'; then
    echo "    ✓ wsl --shutdown"
  else
    echo "    ⚠ wsl --shutdown may have timed out — continuing"
  fi

  echo "    → restarting HNS + SharedAccess (WinNAT)..."
  local PS_SVC
  PS_SVC='
try {
  Stop-Service -Name HNS          -Force -ErrorAction SilentlyContinue
  Stop-Service -Name SharedAccess -Force -ErrorAction SilentlyContinue
  Start-Sleep -Seconds 4
  Start-Service -Name HNS
  Start-Sleep -Seconds 2
  Start-Service -Name SharedAccess
  Start-Sleep -Seconds 3
  $hns = (Get-Service HNS).Status
  $ics = (Get-Service SharedAccess).Status
  Write-Output ("SVC-STATUS=HNS:$hns,ICS:$ics")
} catch {
  Write-Output ("SVC-ERR=" + $_.Exception.Message)
}
'
  local SVC_OUT
  SVC_OUT=$(win_ps "$SSH_TARGET" "$PS_SVC")
  echo "    $SVC_OUT"

  echo "    → waiting for vEthernet (WSL) adapter to come back (up to 90s)..."
  local ADAPTER_OK=0
  for _ in $(seq 1 18); do
    local ADAPTER_OUT
    ADAPTER_OUT=$(win_ps "$SSH_TARGET" \
      "Get-NetAdapter -Name 'vEthernet (WSL)' -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Status")
    if echo "$ADAPTER_OUT" | grep -qi "up"; then
      ADAPTER_OK=1
      echo "    ✓ vEthernet (WSL): Up"
      break
    fi
    sleep 5
  done
  if [[ "$ADAPTER_OK" != "1" ]]; then
    echo "    ✗ vEthernet (WSL) did not come back within 90s." >&2
    echo "      On the Windows host run: Get-NetAdapter -Name 'vEthernet (WSL)'" >&2
    return 1
  fi
}

# ── verify_wsl_tcp ───────────────────────────────────────────────────────────
# Verify basic TCP + DNS is working inside WSL by curling 1.1.1.1 over HTTPS.
verify_wsl_tcp() {
  local SSH_TARGET="$1" WSL_DISTRO="$2"
  echo "    → verifying WSL2 TCP (curl https://1.1.1.1, up to 30s)..."
  for _ in $(seq 1 6); do
    local HTTP_CODE
    HTTP_CODE=$(wsl_exec "$SSH_TARGET" "$WSL_DISTRO" 'curl -sk -o /dev/null -w "%{http_code}" https://1.1.1.1 --connect-timeout 8' 2>/dev/null || echo "000")
    case "$HTTP_CODE" in
      200|301|302) echo "    ✓ WSL2 TCP OK (HTTP $HTTP_CODE)"; return 0 ;;
    esac
    echo "    ⚠ got HTTP $HTTP_CODE — retrying..."
    sleep 5
  done
  echo "    ✗ WSL2 TCP still failing after 30s — WinNAT may need manual intervention." >&2
  return 1
}

# ── ensure_kubelet_dropin ─────────────────────────────────────────────────────
# Push a systemd drop-in that makes kubelet wait for containerd before starting.
# Fixes the restart-loop caused by kubelet starting before containerd.sock exists.
# Idempotent: no-op if the drop-in already exists with the correct content.
ensure_kubelet_dropin() {
  local SSH_TARGET="$1" WSL_DISTRO="$2"
  echo "    → ensuring kubelet After=containerd drop-in..."
  local DROPIN_DIR="/etc/systemd/system/kubelet.service.d"
  local DROPIN_FILE="${DROPIN_DIR}/10-containerd-ordering.conf"
  local DROPIN_CONTENT
  DROPIN_CONTENT=$(printf '[Unit]\nAfter=containerd.service\nRequires=containerd.service\n')
  local B64
  B64=$(printf '%s' "$DROPIN_CONTENT" | base64 | tr -d '\n')
  local OUT
  OUT=$(wsl_exec "$SSH_TARGET" "$WSL_DISTRO" \
    "mkdir -p ${DROPIN_DIR} && \
     echo ${B64} | base64 -d > ${DROPIN_FILE}.tmp && \
     if ! diff -q ${DROPIN_FILE}.tmp ${DROPIN_FILE} >/dev/null 2>&1; then \
       mv ${DROPIN_FILE}.tmp ${DROPIN_FILE} && \
       systemctl daemon-reload && \
       echo DROPIN=UPDATED; \
     else \
       rm -f ${DROPIN_FILE}.tmp && echo DROPIN=ALREADY-OK; \
     fi" 2>/dev/null || echo "DROPIN=ERR")
  echo "$OUT" | grep -E 'DROPIN=' | head -1 | sed 's/DROPIN=/    ✓ kubelet drop-in: /'
}

# ── recover_tailscale ─────────────────────────────────────────────────────────
# Re-authenticate Tailscale if it is in NoState / NeedsLogin / Stopped.
# Prints the login URL if interactive auth is required; user must approve it.
recover_tailscale() {
  local SSH_TARGET="$1" WSL_DISTRO="$2" HOSTNAME="$3"
  echo "    → checking Tailscale state..."
  local TS_STATE
  TS_STATE=$(wsl_exec "$SSH_TARGET" "$WSL_DISTRO" "tailscale status --peers=false 2>&1 || true" 2>/dev/null | head -3)

  if echo "$TS_STATE" | grep -q "100\."; then
    echo "    ✓ Tailscale already Connected"
    return 0
  fi

  echo "    ⚠ Tailscale not Connected (state: $(echo "$TS_STATE" | head -1))"
  echo "    → restarting tailscaled..."
  wsl_exec "$SSH_TARGET" "$WSL_DISTRO" "systemctl restart tailscaled; sleep 3" 2>/dev/null || true

  echo "    → running tailscale up --hostname=${HOSTNAME} ..."
  echo "    ─────────────────────────────────────────────────────────────"
  # Run with short timeout; prints login URL synchronously then exits non-zero if
  # waiting for approval. Capture the URL so the user sees it.
  wsl_exec "$SSH_TARGET" "$WSL_DISTRO" "timeout 20 tailscale up --hostname=${HOSTNAME} --accept-routes 2>&1 || true" 2>/dev/null || true
  echo "    ─────────────────────────────────────────────────────────────"
  echo "    ℹ If a URL appeared above, approve it in your browser now."
  read -r -p "    [Press ENTER when Tailscale is approved / already connected] " || true
}

# ── verify_tailscale ─────────────────────────────────────────────────────────
verify_tailscale() {
  local SSH_TARGET="$1" WSL_DISTRO="$2"
  echo "    → verifying Tailscale (up to 60s)..."
  for _ in $(seq 1 12); do
    local TS_IP
    TS_IP=$(wsl_exec "$SSH_TARGET" "$WSL_DISTRO" "tailscale ip -4 2>/dev/null || true" 2>/dev/null || echo "")
    if [[ "$TS_IP" =~ ^100\. ]]; then
      echo "    ✓ Tailscale Connected: ${TS_IP}"
      return 0
    fi
    sleep 5
  done
  echo "    ✗ Tailscale did not connect within 60s." >&2
  return 1
}

# ─────────────────────────────────────────────────────────────────────────────
NODE_IDX=0
RECOVER_FAILED=()

while IFS='|' read -r HOSTNAME SSH_TARGET WSL_DISTRO TAILNET_HOST BOX_TAG; do
  [[ -z "$HOSTNAME" ]] && continue
  NODE_IDX=$((NODE_IDX + 1))

  if [[ -n "$ONLY_NODE" && "$NODE_IDX" != "$ONLY_NODE" ]]; then
    continue
  fi

  echo "── node ${NODE_IDX}: ${HOSTNAME} (${BOX_TAG}) ─────────────────"
  echo "    SSH: ${SSH_TARGET}  WSL: ${WSL_DISTRO}"

  # Gate: SSH to Windows host must work
  if ! ssh -o BatchMode=yes -o ConnectTimeout=8 -o StrictHostKeyChecking=no \
        "${SSH_TARGET}" "echo ok" 2>/dev/null; then
    echo "    ✗ SSH to ${SSH_TARGET} failed — node unreachable over LAN. Skipping." >&2
    RECOVER_FAILED+=("${HOSTNAME}")
    continue
  fi
  echo "    ✓ SSH to Windows host"

  # Step 1+2+3: WinNAT restart + adapter verify
  if ! restart_winnat "$SSH_TARGET"; then
    RECOVER_FAILED+=("${HOSTNAME}")
    continue
  fi

  # Step 4: Verify TCP inside WSL
  if ! verify_wsl_tcp "$SSH_TARGET" "$WSL_DISTRO"; then
    RECOVER_FAILED+=("${HOSTNAME}")
    continue
  fi

  # Step 5: Fix kubelet/containerd ordering drop-in (idempotent)
  ensure_kubelet_dropin "$SSH_TARGET" "$WSL_DISTRO"

  # Step 6: Tailscale recovery + interactive re-auth
  recover_tailscale "$SSH_TARGET" "$WSL_DISTRO" "$HOSTNAME"
  if ! verify_tailscale "$SSH_TARGET" "$WSL_DISTRO"; then
    RECOVER_FAILED+=("${HOSTNAME}")
    continue
  fi

  echo ""
  echo "    ✓ ${HOSTNAME}: network + Tailscale restored"
  echo ""

  # Step 7: Re-run join (unless --skip-join)
  if [[ "$SKIP_JOIN" == "1" ]]; then
    echo "    (--skip-join: skipping kubeadm rejoin step)"
  else
    echo "    → re-running join-home-workers.sh --node ${NODE_IDX}"
    "${HERE}/join-home-workers.sh" --node "${NODE_IDX}" --env "${ENV_FILE}"
  fi

done <<< "$HOME_WORKER_NODES"

echo ""
echo "=== Recovery complete ==="
if [[ ${#RECOVER_FAILED[@]} -gt 0 ]]; then
  echo "    ✗ Failed nodes: ${RECOVER_FAILED[*]}"
  echo ""
  echo "    Manual recovery steps (on the Windows host):"
  echo "      wsl --shutdown"
  echo "      net stop hns && net start hns"
  echo "      net stop SharedAccess && net start SharedAccess"
  echo "      wsl -d Ubuntu-24.04"
  echo "      # inside WSL:"
  echo "      sudo tailscale up --hostname=<node>"
  echo "      # then from Mac:"
  echo "      ./join-home-workers.sh --node N"
  exit 1
else
  echo "    All targeted nodes recovered and re-joined."
fi

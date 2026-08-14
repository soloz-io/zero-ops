#!/usr/bin/env bash
# scripts/hybrid/join-home-workers.sh
# ─────────────────────────────────────────────────────────────────────────────
# Mac-side driver for hybrid home-worker (WSL2) join — THE single script to run
# after a Windows box reboots OR when checking membership after a network
# glitch. Idempotent: safe to re-run any time. Already-joined nodes are
# verified and re-labeled, not re-joined.
#
# What it does per node (node 1, 2, ... from home-lab.env):
#   1. Verifies SSH + Tailscale are up.
#   2. Pushes the hub kubeconfig and join tooling onto the node (/etc/hybrid).
#   3. Installs a Windows Task Scheduler entry so the box starts the distro +
#      rejoin timer automatically on Windows boot/logon (reboot resilience).
#   4. Installs a systemd oneshot+timer on the node so membership is
#      re-established automatically on boot / network return / periodic ticks.
#   5. Runs the join now (idempotent) and waits for the node Ready.
# Then prints a final per-node status summary.
#
# Usage:
#   ./join-home-workers.sh [--node 1] [--env ./home-lab.env]
#     --node N   only handle node index N (1-based). Default: all nodes.
#     --env F    path to home-lab.env. Default: scripts/hybrid/home-lab.env
# ─────────────────────────────────────────────────────────────────────────────
set -euo pipefail

ENV_FILE="$(dirname "$0")/home-lab.env"
ONLY_NODE=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --node) ONLY_NODE="$2"; shift 2 ;;
    --env)  ENV_FILE="$2"; shift 2 ;;
    *) echo "ERROR: unknown arg $1" >&2; exit 1 ;;
  esac
done

if [[ ! -f "$ENV_FILE" ]]; then
  echo "ERROR: $ENV_FILE not found. Copy home-lab.env.example → home-lab.env." >&2
  exit 1
fi
# shellcheck source=/dev/null
source "$ENV_FILE"

if [[ ! -f "$HUB_KUBECONFIG" ]]; then
  echo "ERROR: HUB_KUBECONFIG not found: $HUB_KUBECONFIG" >&2
  echo "       Check home-lab.env — the hub kubeconfig must exist on the Mac." >&2
  exit 1
fi

HERE="$(cd "$(dirname "$0")" && pwd)"
JOIN_SCRIPT="$HERE/home-worker-join.sh"
REMOTE_DIR="/etc/hybrid"
REMOTE_KUBECONFIG="$REMOTE_DIR/hub.kubeconfig"
REMOTE_ENV="$REMOTE_DIR/home-lab.env"
UNIT="hybrid-home-worker-join"

# Windows Task Scheduler task name (unique per node to support same-box nodes).
WINDOWS_TASK="HybridHomeWorkerJoin"

echo "=== Hybrid home-worker join driver ==="
echo "    Spoke:   ${HYBRID_SPOKE_NAME}"
echo "    Tailnet: ${TAILNET_NAME}"
echo "    Hub kc:  ${HUB_KUBECONFIG}"
echo ""

# ── install_windows_autostart ────────────────────────────────────────────────
# Registers a Windows Task Scheduler entry that starts the WSL distro + rejoin
# timer on Windows logon, so a rebooted box rejoins without the Mac running
# anything. Idempotent (Register-ScheduledTask -Force overwrites).
install_windows_autostart() {
  local SSH_TARGET="$1" WSL_DISTRO="$2" NODE_IDX="$3" HOSTNAME="$4"
  local TASK="HybridHomeWorkerJoin${NODE_IDX}"
  echo "    → installing Windows Task Scheduler auto-start (${TASK})"
  local PS
  PS="\$distro = '$WSL_DISTRO';
\$taskName = '$TASK';
try {
  \$action  = New-ScheduledTaskAction -Execute 'C:\Windows\System32\wsl.exe' -Argument ('-d ' + \$distro + ' -u root -e bash -lc \"\"systemctl start hybrid-home-worker-join.timer\"\"')
  \$trigger = New-ScheduledTaskTrigger -AtLogOn
  \$settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable
  Register-ScheduledTask -TaskName \$taskName -Action \$action -Trigger \$trigger -Settings \$settings -Description 'Hybrid home worker rejoin on Windows logon (node ${NODE_IDX}, ${HOSTNAME})' -Force | Out-Null
  Write-Output 'TASK-REGISTERED=OK'
} catch {
  Write-Output ('TASK-REGISTERED=ERR: ' + \$_.Exception.Message)
}
"
  local B64
  B64=$(printf '%s' "$PS" | iconv -f UTF-8 -t UTF-16LE | base64 | tr -d '\n')
  local OUT
  OUT=$(ssh -o BatchMode=yes -o ConnectTimeout=8 -o StrictHostKeyChecking=no \
        "$SSH_TARGET" "powershell -NoProfile -EncodedCommand $B64" 2>/dev/null)
  if echo "$OUT" | grep -q 'TASK-REGISTERED=OK'; then
    echo "    ✓ Windows Task Scheduler: ${TASK} registered (runs on logon)"
  else
    echo "    ⚠ Windows Task Scheduler install issue: $(echo "$OUT" | grep 'TASK-REGISTERED=' | head -1)" >&2
  fi
}

# ── check_member_status ───────────────────────────────────────────────────────
# Prints Ready/NotReady status for every node using the spoke kubeconfig.
check_member_status() {
  local SPOKE_KC
  SPOKE_KC=$(mktemp /tmp/hybrid-spoke-XXXXXX.kubeconfig)
  trap 'rm -f "$SPOKE_KC"' RETURN
  if ! kubectl --kubeconfig="${HUB_KUBECONFIG}" \
        get secret "${HYBRID_SPOKE_NAME}-kubeconfig" \
        -n platform-capi -o jsonpath='{.data.value}' 2>/dev/null \
        | base64 -d > "$SPOKE_KC"; then
    echo "    ⚠ could not fetch spoke kubeconfig — skipping status check" >&2
    return 0
  fi
  while IFS='|' read -r HOSTNAME SSH_TARGET WSL_DISTRO TAILNET_HOST BOX_TAG; do
    [[ -z "$HOSTNAME" ]] && continue
    if kubectl --kubeconfig="$SPOKE_KC" get node "${HOSTNAME}" &>/dev/null; then
      READY=$(kubectl --kubeconfig="$SPOKE_KC" get node "${HOSTNAME}" \
        -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}')
      echo "    ✓ ${HOSTNAME}: ${READY:-unknown}"
    else
      echo "    ✗ ${HOSTNAME}: not a member"
    fi
  done <<< "$HOME_WORKER_NODES"
}

NODE_IDX=0
while IFS='|' read -r HOSTNAME SSH_TARGET WSL_DISTRO TAILNET_HOST BOX_TAG; do
  [[ -z "$HOSTNAME" ]] && continue
  NODE_IDX=$((NODE_IDX + 1))

  if [[ -n "$ONLY_NODE" && "$NODE_IDX" != "$ONLY_NODE" ]]; then
    continue
  fi

  echo "── node ${NODE_IDX}: ${HOSTNAME} (${BOX_TAG}) ─────────────────"
  echo "    SSH: ${SSH_TARGET}  WSL: ${WSL_DISTRO}  Tailnet: ${TAILNET_HOST}"

  # 1. SSH must work (use a PowerShell-native command; default shell on the
  #    Windows host is PowerShell, where `true` is not a valid command)
  if ! ssh -o BatchMode=yes -o ConnectTimeout=6 -o StrictHostKeyChecking=no \
        "${SSH_TARGET}" "echo ok" 2>/dev/null; then
    echo "    ✗ SSH to ${SSH_TARGET} failed — skipping (node unreachable)." >&2
    continue
  fi

  # WSL entry point (Windows default shell is PowerShell; always invoke bash in WSL).
  WSL="wsl -d ${WSL_DISTRO} -u root -e bash -lc"

  # 2. Tailscale must be up + authenticated inside WSL. tailscaled can flap
  #    (transient "NoState" during restart), so retry for up to ~60s.
  echo "    → waiting for Tailscale in ${WSL_DISTRO}..."
  TS_OK=0
  for _ in $(seq 1 12); do
    if ssh -o BatchMode=yes -o ConnectTimeout=6 -o StrictHostKeyChecking=no \
          "${SSH_TARGET}" "${WSL} 'tailscale status --peers=false >/dev/null 2>&1'"; then
      TS_OK=1
      break
    fi
    sleep 5
  done
  if [[ "$TS_OK" != "1" ]]; then
    echo "    ✗ Tailscale not up in ${WSL_DISTRO}. Run 'sudo tailscale up' on the node first." >&2
    continue
  fi
  echo "    ✓ SSH + Tailscale"

  # 3. Push hub kubeconfig (base64 to dodge PowerShell quoting)
  echo "    → pushing hub kubeconfig → ${REMOTE_KUBECONFIG}"
  KCB64="$(base64 < "$HUB_KUBECONFIG" | tr -d '\n')"
  ssh -o BatchMode=yes -o ConnectTimeout=6 -o StrictHostKeyChecking=no \
    "${SSH_TARGET}" "${WSL} 'mkdir -p ${REMOTE_DIR} && echo ${KCB64} | base64 -d > ${REMOTE_KUBECONFIG} && chmod 600 ${REMOTE_KUBECONFIG}'"

  # 4. Push the join script + env
  echo "    → pushing join tooling → ${REMOTE_DIR}"
  SB64="$(base64 < "$JOIN_SCRIPT" | tr -d '\n')"
  ssh -o BatchMode=yes -o ConnectTimeout=6 -o StrictHostKeyChecking=no \
    "${SSH_TARGET}" "${WSL} 'echo ${SB64} | base64 -d > ${REMOTE_DIR}/home-worker-join.sh && chmod 700 ${REMOTE_DIR}/home-worker-join.sh'"

  # Node-local env: same registry, but HUB_KUBECONFIG is the pushed copy.
  {
    echo "# generated by join-home-workers.sh on $(date -u +%FT%TZ)"
    echo "HOME_WORKER_NODES=\"${HOME_WORKER_NODES}\""
    echo "TAILNET_NAME=\"${TAILNET_NAME}\""
    echo "HYBRID_SPOKE_NAME=\"${HYBRID_SPOKE_NAME}\""
    echo "HUB_KUBECONFIG=\"${REMOTE_KUBECONFIG}\""
    echo "NODE_INDEX=\"${NODE_IDX}\""
  } > /tmp/hybrid-home-lab.$$.env
  EB64="$(base64 < /tmp/hybrid-home-lab.$$.env | tr -d '\n')"
  rm -f /tmp/hybrid-home-lab.$$.env
  ssh -o BatchMode=yes -o ConnectTimeout=6 -o StrictHostKeyChecking=no \
    "${SSH_TARGET}" "${WSL} 'echo ${EB64} | base64 -d > ${REMOTE_ENV}'"

  # 5. Windows Task Scheduler auto-start (reboot resilience)
  install_windows_autostart "$SSH_TARGET" "$WSL_DISTRO" "$NODE_IDX" "$HOSTNAME"

  # 6. Install self-healing systemd unit + timer (oneshot join on boot/network,
  #    plus periodic re-verify every 10 min). Already-joined → join exits 0.
  echo "    → installing systemd auto-join unit + timer"
  SVC="/etc/systemd/system/${UNIT}.service"
  TIMER="/etc/systemd/system/${UNIT}.timer"
  cat > /tmp/hybrid-join.$$.service <<EOF
[Unit]
Description=Hybrid home worker auto-join (node ${NODE_IDX}, ${HOSTNAME})
Wants=network-online.target tailscaled.service
After=network-online.target tailscaled.service
ConditionPathExists=${REMOTE_KUBECONFIG}

[Service]
Type=oneshot
ExecStart=${REMOTE_DIR}/home-worker-join.sh ${NODE_IDX} --env ${REMOTE_ENV}
TimeoutStartSec=300
EOF
  cat > /tmp/hybrid-join.$$.timer <<EOF
[Unit]
Description=Periodic hybrid home worker membership check (node ${NODE_IDX})

[Timer]
OnBootSec=1min
OnUnitActiveSec=10min
Unit=${UNIT}.service

[Install]
WantedBy=timers.target
EOF
  SVCB64="$(base64 < /tmp/hybrid-join.$$.service | tr -d '\n')"
  TIMERB64="$(base64 < /tmp/hybrid-join.$$.timer | tr -d '\n')"
  rm -f /tmp/hybrid-join.$$.service /tmp/hybrid-join.$$.timer
  ssh -o BatchMode=yes -o ConnectTimeout=6 -o StrictHostKeyChecking=no \
    "${SSH_TARGET}" "${WSL} 'echo ${SVCB64} | base64 -d > ${SVC} && echo ${TIMERB64} | base64 -d > ${TIMER} && systemctl daemon-reload && systemctl enable --now ${UNIT}.timer && systemctl restart ${UNIT}.service'"

  # 7. Run the join now and report result
  echo "    → running join on node ${NODE_IDX}"
  if ssh -o BatchMode=yes -o ConnectTimeout=6 -o StrictHostKeyChecking=no \
        "${SSH_TARGET}" "${WSL} '${REMOTE_DIR}/home-worker-join.sh ${NODE_IDX} --env ${REMOTE_ENV}'"; then
    echo "    ✓ ${HOSTNAME} join completed (or already member)"
  else
    echo "    ✗ ${HOSTNAME} join returned non-zero — see node logs above." >&2
  fi
  echo ""
done <<< "$HOME_WORKER_NODES"

echo "=== Status summary ==="
check_member_status
echo "=== Done ==="
echo "    Verify: kubectl --kubeconfig=\$HUB_KUBECONFIG get nodes"

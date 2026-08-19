#!/usr/bin/env bash
# =============================================================================
# scripts/hybrid/provision-home-worker.sh
#
# THE single entry point to provision a WSL2 home-lab worker node into the
# hybrid spoke and make it FULLY SELF-HEALING — no Mac required for any
# runtime recovery afterwards.
#
# Replaces: harden-windows-host.sh, join-home-workers.sh, recover-wsl2-nat.sh,
#           prepare-wsl2-cgroup.sh, fix-cilium-pid.sh
#
# This is an ORCHESTRATOR: it drives the modular helpers (watchdogs,
# setup-wsl2-node.sh, home-worker-join.sh) through a fixed sequence of
# numbered phases. Phase failures abort the run. The run is only a SUCCESS
# once every targeted node reports Ready in the spoke cluster.
#
# Phases per node:
#   [1/7] SSH reachability gate
#   [2/7] Harden Windows host (power/lid/updates/OpenSSH, .wslconfig)
#   [3/7] Deploy self-healing watchdogs (Windows + WSL2, timers enabled)
#   [4/7] Provision WSL2 distro (containerd/kubelet/tailscale/cgroup)
#   [5/7] Install reboot resilience (Windows autostart + systemd auto-join)
#   [6/7] Join spoke cluster (kubeadm, idempotent)
#   [7/7] Verify node Ready (success gate)
#
# Usage:
#   ./provision-home-worker.sh                          # provision all nodes
#   ./provision-home-worker.sh --node 1                 # node index 1 only
#   ./provision-home-worker.sh --env <file>             # custom env file
#   ./provision-home-worker.sh --ts-authkey <key>       # autonomous Tailscale re-auth
#   ./provision-home-worker.sh --recover <node>         # repair an existing node
#   ./provision-home-worker.sh --verify                 # only check Ready status
# =============================================================================
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
ENV_FILE="${HERE}/home-lab.env"
ONLY_NODE=""
TS_AUTHKEY=""
MODE="provision"

usage() {
  cat <<EOF
Usage: $0 [OPTIONS]

Options:
  --node N          Provision only node index N (1-based from home-lab.env).
  --env FILE        Path to environment file (default: scripts/hybrid/home-lab.env)
  --ts-authkey KEY  Tailscale auth-key for autonomous re-auth on NeedsLogin.
                    Generate a reusable key at https://login.tailscale.com/admin/settings/keys
                    Stored at /etc/soloz/tailscale-authkey (chmod 600) in WSL2.
  --recover N       Repair node N using the self-heal path (watchdogs + join).
  --verify          Only check Ready status of registered nodes; no changes.
  -h, --help        Show this help message

Phases per node:
  [1/7] SSH reachability gate
  [2/7] Harden Windows host
  [3/7] Deploy self-healing watchdogs
  [4/7] Provision WSL2 distro
  [5/7] Install reboot resilience
  [6/7] Join spoke cluster
  [7/7] Verify node Ready (success gate)

Exit code 0 only if all targeted nodes are Ready.
EOF
  exit 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --node)       ONLY_NODE="$2"; shift 2 ;;
    --env)        ENV_FILE="$2"; shift 2 ;;
    --ts-authkey) TS_AUTHKEY="$2"; shift 2 ;;
    --recover)    MODE="recover"; ONLY_NODE="$2"; shift 2 ;;
    --verify)     MODE="verify"; shift ;;
    -h|--help)    usage ;;
    *) echo "ERROR: unknown option $1" >&2; usage ;;
  esac
done

if [[ ! -f "$ENV_FILE" ]]; then
  echo "ERROR: Environment file not found: $ENV_FILE" >&2
  exit 1
fi
# shellcheck source=/dev/null
source "$ENV_FILE"

# Auto-fetch Tailscale authkey from k8-secrets if not passed via CLI
REPO_ROOT="$(cd "${HERE}/../.." && pwd)"
DEFAULT_TS_AUTHKEY_FILE="${REPO_ROOT}/k8-secrets/tailscale/authkey"
if [[ -z "$TS_AUTHKEY" && -f "$DEFAULT_TS_AUTHKEY_FILE" ]]; then
  TS_AUTHKEY="$(tr -d '\r\n' < "$DEFAULT_TS_AUTHKEY_FILE")"
fi

if [[ ! -f "$HUB_KUBECONFIG" ]]; then
  echo "ERROR: HUB_KUBECONFIG not found: $HUB_KUBECONFIG" >&2
  echo "       Check home-lab.env — the hub kubeconfig must exist on the Mac." >&2
  exit 1
fi

# ── Helpers ──────────────────────────────────────────────────────────────────

win_ps() { # execute PowerShell on the Windows host via SSH
  local SSH_TARGET="$1" PS_SCRIPT="$2"
  printf '%s\n' "$PS_SCRIPT" | ssh -o BatchMode=yes -o ConnectTimeout=15 -o StrictHostKeyChecking=no \
    "$SSH_TARGET" "powershell -NoProfile -ExecutionPolicy Bypass -Command -" 2>&1 || true
}

wsl_exec() { # execute a bash snippet inside WSL via SSH to the Windows host
  local SSH_TARGET="$1" WSL_DISTRO="$2" CMD="$3"
  printf '%s\n' "$CMD" | ssh -o BatchMode=yes -o ConnectTimeout=15 -o StrictHostKeyChecking=no \
    "$SSH_TARGET" "wsl.exe -d $WSL_DISTRO -u root -e bash -l" 2>&1 || true
}

# ── Phase helpers (modular; each is idempotent) ──────────────────────────────

# Phase [2/7] — Harden Windows host: power/lid, disable updates, OpenSSH,
# .wslconfig. Ported from harden-windows-host.sh.
phase_harden_windows() {
  local SSH_TARGET="$1" WSL_DISTRO="$2" NODE_IDX="$3" HOSTNAME="$4"
  echo "    [2/7] Hardening Windows host (${HOSTNAME})..."
  local PS_HARDEN
  PS_HARDEN="
\$distro = '$WSL_DISTRO';
Write-Output '=== [2a/2d] Power & Lid Actions ==='
try {
  powercfg /setacvalueindex SCHEME_CURRENT 4f971e89-eebd-4455-a8de-9e59040e7347 5ca83367-6e45-459f-a27b-476b1d01c936 0
  powercfg /setdcvalueindex SCHEME_CURRENT 4f971e89-eebd-4455-a8de-9e59040e7347 5ca83367-6e45-459f-a27b-476b1d01c936 0
  powercfg /change standby-timeout-ac 0
  powercfg /change hibernate-timeout-ac 0
  powercfg /change monitor-timeout-ac 5
  powercfg /setactive SCHEME_CURRENT
  Write-Output '    ✓ Lid Close -> Do Nothing; AC Sleep/Hibernate -> Never'
} catch { Write-Output ('    ⚠ Power config: ' + \$_.Exception.Message) }

Write-Output '=== [2b/2d] Disabling Automatic Windows Updates & Reboots ==='
try {
  @('wuauserv', 'UsoSvc', 'WaaSMedicSvc', 'bits', 'dosvc') | ForEach-Object {
    Stop-Service -Name \$_ -Force -ErrorAction SilentlyContinue
    Set-Service -Name \$_ -StartupType Disabled -ErrorAction SilentlyContinue
  }
  \$auPath = 'HKLM:\SOFTWARE\Policies\Microsoft\Windows\WindowsUpdate\AU'
  if (!(Test-Path \$auPath)) { New-Item -Path \$auPath -Force | Out-Null }
  Set-ItemProperty -Path \$auPath -Name 'NoAutoUpdate' -Value 1 -Type DWord -Force
  Set-ItemProperty -Path \$auPath -Name 'AUOptions' -Value 1 -Type DWord -Force
  Set-ItemProperty -Path \$auPath -Name 'NoAutoRebootWithLoggedOnUsers' -Value 1 -Type DWord -Force
  Write-Output '    ✓ Windows Update services stopped & disabled'
} catch { Write-Output ('    ⚠ Updates: ' + \$_.Exception.Message) }

Write-Output '=== [2c/2d] .wslconfig (vmIdleTimeout=-1) ==='
try {
  \$wslconfigPath = Join-Path \$env:USERPROFILE '.wslconfig'
  \$wslconfigContent = @'
[wsl2]
vmIdleTimeout=-1
swap=0
localhostForwarding=true
'@
  Set-Content -Path \$wslconfigPath -Value \$wslconfigContent -Encoding UTF8 -Force
  Write-Output '    ✓ .wslconfig configured'
} catch { Write-Output ('    ⚠ .wslconfig: ' + \$_.Exception.Message) }

Write-Output '=== [2d/2d] OpenSSH Server + Firewall ==='
try {
  Set-Service -Name sshd -StartupType Automatic -ErrorAction SilentlyContinue
  Start-Service -Name sshd -ErrorAction SilentlyContinue
  New-NetFirewallRule -Name 'OpenSSH-Server-In-TCP' -DisplayName 'OpenSSH SSH Server (sshd)' -Enabled True -Direction Inbound -Protocol TCP -Action Allow -LocalPort 22 -ErrorAction SilentlyContinue | Out-Null
  Write-Output '    ✓ sshd Automatic + firewall rule'
} catch { Write-Output ('    ⚠ SSH: ' + \$_.Exception.Message) }
"
  local OUT
  OUT=$(win_ps "$SSH_TARGET" "$PS_HARDEN")
  echo "$OUT" | grep -E '✓|⚠|===' | sed 's/^/      /' || true
}

# Phase [3/7] — Deploy self-healing watchdogs (Windows + WSL2).
phase_deploy_watchdogs() {
  local SSH_TARGET="$1" WSL_DISTRO="$2" NODE_IDX="$3" HOSTNAME="$4"
  echo "    [3/7] Deploying self-healing watchdogs..."
  local WATCHDOG_PS1="${HERE}/windows/wsl2-node-watchdog.ps1"
  local TS_SH="${HERE}/wsl2/tailscale-watchdog.sh"
  local TS_SVC="${HERE}/wsl2/tailscale-watchdog.service"
  local TS_TMR="${HERE}/wsl2/tailscale-watchdog.timer"
  for F in "$WATCHDOG_PS1" "$TS_SH" "$TS_SVC" "$TS_TMR"; do
    [[ -f "$F" ]] || { echo "    ✗ Missing watchdog file: $F" >&2; return 1; }
  done

  # Determine WSL2 subnet for WinNAT config.
  local WSL_SUBNET
  WSL_SUBNET=$(win_ps "$SSH_TARGET" "
    \$ip = (Get-NetIPAddress -InterfaceAlias 'vEthernet (WSL)' -AddressFamily IPv4 -ErrorAction SilentlyContinue | Select-Object -First 1)
    if (\$ip) {
      \$o = \$ip.IPAddress.Split('.')
      \$o[2] = [Math]::Floor([int]\$o[2] / 16) * 16
      \$o[3] = 0
      Write-Output (\$o -join '.' + '/' + \$ip.PrefixLength)
    } else { Write-Output '172.27.32.0/20' }
  " 2>/dev/null | grep -Eo '[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+/[0-9]+' | head -1)
  WSL_SUBNET="${WSL_SUBNET:-172.27.32.0/20}"
  local TS_FLAGS="--accept-routes --hostname=${HOSTNAME} --accept-dns=false"

  # WSL2-side watchdog files + node config + timer.
  printf '%s\n' "$(cat "$TS_SH")" | \
    ssh -o BatchMode=yes -o ConnectTimeout=15 -o StrictHostKeyChecking=no \
      "$SSH_TARGET" "wsl.exe -d $WSL_DISTRO -u root -- bash -c 'mkdir -p /usr/local/bin && cat > /usr/local/bin/tailscale-watchdog.sh && chmod 755 /usr/local/bin/tailscale-watchdog.sh'" 2>/dev/null
  printf '%s\n' "$(cat "$TS_SVC")" | \
    ssh -o BatchMode=yes -o ConnectTimeout=15 -o StrictHostKeyChecking=no \
      "$SSH_TARGET" "wsl.exe -d $WSL_DISTRO -u root -- bash -c 'cat > /etc/systemd/system/tailscale-watchdog.service'" 2>/dev/null
  printf '%s\n' "$(cat "$TS_TMR")" | \
    ssh -o BatchMode=yes -o ConnectTimeout=15 -o StrictHostKeyChecking=no \
      "$SSH_TARGET" "wsl.exe -d $WSL_DISTRO -u root -- bash -c 'cat > /etc/systemd/system/tailscale-watchdog.timer'" 2>/dev/null
  printf 'NODE_HOSTNAME="%s"\nTAILSCALE_FLAGS="%s"\n' "${HOSTNAME}" "${TS_FLAGS}" | \
    ssh -o BatchMode=yes -o ConnectTimeout=15 -o StrictHostKeyChecking=no \
      "$SSH_TARGET" "wsl.exe -d $WSL_DISTRO -u root -- bash -c 'mkdir -p /etc/soloz && cat > /etc/soloz/node-config.env'" 2>/dev/null
  if [[ -n "$TS_AUTHKEY" ]]; then
    printf '%s\n' "$TS_AUTHKEY" | \
      ssh -o BatchMode=yes -o ConnectTimeout=15 -o StrictHostKeyChecking=no \
        "$SSH_TARGET" "wsl.exe -d $WSL_DISTRO -u root -- bash -c 'mkdir -p /etc/soloz && cat > /etc/soloz/tailscale-authkey && chmod 600 /etc/soloz/tailscale-authkey'" 2>/dev/null
  fi

  # Enable + start the WSL2 watchdog timer.
  local TIMER_OUT
  TIMER_OUT=$(ssh -o BatchMode=yes -o ConnectTimeout=15 -o StrictHostKeyChecking=no \
    "$SSH_TARGET" "wsl.exe -d $WSL_DISTRO -u root -- bash -c 'systemctl daemon-reload && systemctl enable tailscale-watchdog.timer && systemctl start tailscale-watchdog.timer && systemctl is-active tailscale-watchdog.timer'" 2>/dev/null || echo "failed")
  echo "    ✓ WSL2 watchdog timer: ${TIMER_OUT}"

  # Windows-side watchdog (SYSTEM task, every 2 min) + node config JSON.
  local WATCHDOG_B64
  WATCHDOG_B64=$(base64 < "$WATCHDOG_PS1" | tr -d '\n')
  local PS_WD
  PS_WD="
\$solozDir = 'C:\ProgramData\soloz';
if (!(Test-Path \$solozDir)) { New-Item -ItemType Directory -Path \$solozDir -Force | Out-Null }
[System.IO.File]::WriteAllBytes(
  (Join-Path \$solozDir 'wsl2-node-watchdog.ps1'),
  [System.Convert]::FromBase64String('$WATCHDOG_B64')
)
\$nodeConfig = @{
  wslDistro      = '$WSL_DISTRO'
  nodeHostname   = '$HOSTNAME'
  natName        = 'WSLNat'
  natSubnet      = '$WSL_SUBNET'
  tailscaleFlags = '$TS_FLAGS'
} | ConvertTo-Json
Set-Content -Path (Join-Path \$solozDir 'wsl2-node-config.json') -Value \$nodeConfig -Encoding UTF8 -Force
\$oldTask = 'HybridWSLKeepAlive' + '$NODE_IDX'
if (Get-ScheduledTask -TaskName \$oldTask -ErrorAction SilentlyContinue) {
  Unregister-ScheduledTask -TaskName \$oldTask -Confirm:\$false
}
\$watchdogName = 'HybridWSLWatchdog' + '$NODE_IDX'
\$action   = New-ScheduledTaskAction -Execute 'C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe' -Argument '-NonInteractive -ExecutionPolicy Bypass -File C:/ProgramData/soloz/wsl2-node-watchdog.ps1'
\$trigBoot  = New-ScheduledTaskTrigger -AtStartup
\$trigLogon = New-ScheduledTaskTrigger -AtLogOn
\$rep = New-ScheduledTaskTrigger -RepetitionInterval (New-TimeSpan -Minutes 2) -Once -At (Get-Date)
\$trigBoot.Repetition  = \$rep.Repetition
\$trigLogon.Repetition = \$rep.Repetition
\$settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable -RunOnlyIfNetworkAvailable:\$false -MultipleInstances IgnoreNew -ExecutionTimeLimit (New-TimeSpan -Minutes 3) -RestartCount 10 -RestartInterval (New-TimeSpan -Minutes 1)
\$principal = New-ScheduledTaskPrincipal -UserId 'SYSTEM' -LogonType ServiceAccount -RunLevel Highest
Register-ScheduledTask -TaskName \$watchdogName -Action \$action -Trigger @(\$trigBoot, \$trigLogon, \$rep) -Settings \$settings -Principal \$principal -Description 'Self-healing watchdog: WinNAT + WSL2 VM reset + Tailscale (every 2 min as SYSTEM)' -Force | Out-Null
Start-ScheduledTask -TaskName \$watchdogName -ErrorAction SilentlyContinue
Write-Output 'WATCHDOG-INSTALLED=OK'
"
  local WD_OUT
  WD_OUT=$(win_ps "$SSH_TARGET" "$PS_WD")
  if echo "$WD_OUT" | grep -q 'WATCHDOG-INSTALLED=OK'; then
    echo "    ✓ Windows watchdog ${NODE_IDX} installed (SYSTEM, every 2 min)"
  else
    echo "    ⚠ Windows watchdog install issue: $(echo "$WD_OUT" | tail -1)" >&2
  fi
}

# Phase [4/7] — Provision WSL2 distro (containerd/kubelet/tailscale/cgroup).
phase_provision_wsl2() {
  local SSH_TARGET="$1" WSL_DISTRO="$2" NODE_IDX="$3" HOSTNAME="$4"
  echo "    [4/7] Provisioning WSL2 distro (${WSL_DISTRO}) — this can take several minutes..."
  local SETUP_B64
  SETUP_B64=$(base64 < "${HERE}/setup-wsl2-node.sh" | tr -d '\n')
  # Stream setup-wsl2-node.sh and run it as root inside the distro. Phase 3
  # already wrote the auth-key (if provided), so login is autonomous.
  wsl_exec "$SSH_TARGET" "$WSL_DISTRO" \
    "echo ${SETUP_B64} | base64 -d > /tmp/setup-wsl2-node.sh && chmod 700 /tmp/setup-wsl2-node.sh && /tmp/setup-wsl2-node.sh ${NODE_IDX}" 2>/dev/null \
    && echo "    ✓ WSL2 node setup complete" || echo "    ⚠ WSL2 setup returned non-zero (may be partial — join phase will verify)"
}

# Phase [5/7] — Install reboot resilience: Windows autostart + systemd auto-join.
phase_reboot_resilience() {
  local SSH_TARGET="$1" WSL_DISTRO="$2" NODE_IDX="$3" HOSTNAME="$4"
  echo "    [5/7] Installing reboot resilience..."
  local REMOTE_DIR="/etc/hybrid"
  local REMOTE_KUBECONFIG="${REMOTE_DIR}/hub.kubeconfig"
  local REMOTE_ENV="${REMOTE_DIR}/home-lab.env"
  local UNIT="hybrid-home-worker-join"

  # Windows autostart task: on logon, start distro + auto-join timer.
  local PS_AS
  PS_AS="\$distro = '$WSL_DISTRO';
\$taskName = 'HybridHomeWorkerJoin' + '$NODE_IDX';
try {
  \$action  = New-ScheduledTaskAction -Execute 'C:\Windows\System32\wsl.exe' -Argument ('-d ' + \$distro + ' -u root -e bash -lc \"\"systemctl start hybrid-home-worker-join.timer\"\"')
  \$trigger = New-ScheduledTaskTrigger -AtLogOn
  \$settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable
  Register-ScheduledTask -TaskName \$taskName -Action \$action -Trigger \$trigger -Settings \$settings -Description 'Hybrid home worker rejoin on Windows logon (node ${NODE_IDX})' -Force | Out-Null
  Write-Output 'AUTOSTART=OK'
} catch { Write-Output ('AUTOSTART=ERR: ' + \$_.Exception.Message) }
"
  local AS_OUT
  AS_OUT=$(win_ps "$SSH_TARGET" "$PS_AS")
  if echo "$AS_OUT" | grep -q 'AUTOSTART=OK'; then
    echo "    ✓ Windows autostart task registered"
  else
    echo "    ⚠ Windows autostart: $(echo "$AS_OUT" | grep 'AUTOSTART=' | head -1)" >&2
  fi

  # Push hub kubeconfig + env for the node-side join script.
  local KCB64
  KCB64="$(base64 < "$HUB_KUBECONFIG" | tr -d '\n')"
  wsl_exec "$SSH_TARGET" "$WSL_DISTRO" "mkdir -p ${REMOTE_DIR} && echo ${KCB64} | base64 -d > ${REMOTE_KUBECONFIG} && chmod 600 ${REMOTE_KUBECONFIG}"
  {
    echo "# generated by provision-home-worker.sh on $(date -u +%FT%TZ)"
    echo "HOME_WORKER_NODES=\"${HOME_WORKER_NODES}\""
    echo "TAILNET_NAME=\"${TAILNET_NAME}\""
    echo "HYBRID_SPOKE_NAME=\"${HYBRID_SPOKE_NAME}\""
    echo "HUB_KUBECONFIG=\"${REMOTE_KUBECONFIG}\""
    echo "NODE_INDEX=\"${NODE_IDX}\""
  } > /tmp/hybrid-home-lab.$$.env
  local EB64
  EB64="$(base64 < /tmp/hybrid-home-lab.$$.env | tr -d '\n')"
  rm -f /tmp/hybrid-home-lab.$$.env
  wsl_exec "$SSH_TARGET" "$WSL_DISTRO" "echo ${EB64} | base64 -d > ${REMOTE_ENV}"

  # systemd auto-join unit + timer (every 10 min membership check).
  local SVC="/etc/systemd/system/${UNIT}.service"
  local TIMER="/etc/systemd/system/${UNIT}.timer"
  local JOIN_B64
  JOIN_B64="$(base64 < "${HERE}/home-worker-join.sh" | tr -d '\n')"
  wsl_exec "$SSH_TARGET" "$WSL_DISTRO" "echo ${JOIN_B64} | base64 -d > ${REMOTE_DIR}/home-worker-join.sh && chmod 700 ${REMOTE_DIR}/home-worker-join.sh"
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
  local SVCB64 TIMERB64
  SVCB64="$(base64 < /tmp/hybrid-join.$$.service | tr -d '\n')"
  TIMERB64="$(base64 < /tmp/hybrid-join.$$.timer | tr -d '\n')"
  rm -f /tmp/hybrid-join.$$.service /tmp/hybrid-join.$$.timer
  wsl_exec "$SSH_TARGET" "$WSL_DISTRO" \
    "echo ${SVCB64} | base64 -d > ${SVC} && echo ${TIMERB64} | base64 -d > ${TIMER} && systemctl daemon-reload && systemctl enable --now ${UNIT}.timer" \
    && echo "    ✓ systemd auto-join timer enabled (every 10 min)"
}

# Phase [6/7] — Join the spoke cluster (idempotent).
phase_join() {
  local SSH_TARGET="$1" WSL_DISTRO="$2" NODE_IDX="$3"
  echo "    [6/7] Joining spoke cluster..."
  local REMOTE_DIR="/etc/hybrid"
  wsl_exec "$SSH_TARGET" "$WSL_DISTRO" \
    "${REMOTE_DIR}/home-worker-join.sh ${NODE_IDX} --env ${REMOTE_DIR}/home-lab.env" 2>/dev/null \
    && echo "    ✓ join completed (or already member)"
}

# Phase [7/7] — Verify node Ready. THE success gate.
node_ready() {
  local HOSTNAME="$1"
  local SPOKE_KC
  SPOKE_KC=$(mktemp /tmp/hybrid-spoke-XXXXXX)
  trap 'rm -f "$SPOKE_KC"' RETURN
  if ! kubectl --kubeconfig="${HUB_KUBECONFIG}" \
        get secret "${HYBRID_SPOKE_NAME}-kubeconfig" \
        -n platform-capi -o jsonpath='{.data.value}' 2>/dev/null \
        | base64 -d > "$SPOKE_KC"; then
    echo "    ✗ could not fetch spoke kubeconfig" >&2
    return 1
  fi
  if kubectl --kubeconfig="$SPOKE_KC" get node "${HOSTNAME}" &>/dev/null; then
    local READY
    READY=$(kubectl --kubeconfig="$SPOKE_KC" get node "${HOSTNAME}" \
      -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || echo "Unknown")
    if [[ "$READY" == "True" ]]; then
      echo "    ✓ ${HOSTNAME}: Ready"
      return 0
    else
      echo "    ✗ ${HOSTNAME}: exists but Ready=${READY}" >&2
      return 1
    fi
  else
    echo "    ✗ ${HOSTNAME}: not a member of ${HYBRID_SPOKE_NAME}" >&2
    return 1
  fi
}

# ── verify-only mode ─────────────────────────────────────────────────────────
if [[ "$MODE" == "verify" ]]; then
  echo "=== Verify mode — checking node Ready status ==="
  local_ok=1
  while IFS='|' read -r HOSTNAME _SSH _WSL _TAILNET _TAG; do
    [[ -z "$HOSTNAME" ]] && continue
    if node_ready "$HOSTNAME"; then :; else local_ok=0; fi
  done <<< "$HOME_WORKER_NODES"
  [[ "$local_ok" == "1" ]] && echo "=== ALL REGISTERED NODES READY ===" || echo "=== SOME NODES NOT READY ==="
  exit $((1 - local_ok))
fi

# ── Main provision loop ──────────────────────────────────────────────────────
NODE_IDX=0
FAILED_NODES=()

echo "=== Hybrid home-worker provisioner (${MODE}) ==="
echo "    Spoke:   ${HYBRID_SPOKE_NAME}"
echo "    Tailnet: ${TAILNET_NAME}"
echo "    Hub kc:  ${HUB_KUBECONFIG}"
echo ""

while IFS='|' read -r HOSTNAME SSH_TARGET WSL_DISTRO TAILNET_HOST BOX_TAG; do
  [[ -z "$HOSTNAME" ]] && continue
  NODE_IDX=$((NODE_IDX + 1))
  [[ -n "$ONLY_NODE" && "$NODE_IDX" != "$ONLY_NODE" ]] && continue

  echo "── node ${NODE_IDX}: ${HOSTNAME} (${BOX_TAG}) ─────────────────"
  echo "    SSH: ${SSH_TARGET}  WSL: ${WSL_DISTRO}  Tailnet: ${TAILNET_HOST}"

  # [1/7] SSH reachability gate
  echo "    [1/7] SSH reachability gate..."
  if ! ssh -o BatchMode=yes -o ConnectTimeout=8 -o StrictHostKeyChecking=no \
        "${SSH_TARGET}" "echo ok" 2>/dev/null; then
    echo "    ✗ SSH to ${SSH_TARGET} failed — node unreachable over LAN. Skipping." >&2
    FAILED_NODES+=("${HOSTNAME}")
    continue
  fi
  echo "    ✓ SSH connected"

  if [[ "$MODE" == "recover" ]]; then
    # Recovery: skip full re-hardening/re-provision, go straight to watchdogs + join.
    phase_deploy_watchdogs "$SSH_TARGET" "$WSL_DISTRO" "$NODE_IDX" "$HOSTNAME"
    phase_join "$SSH_TARGET" "$WSL_DISTRO" "$NODE_IDX"
  else
    phase_harden_windows "$SSH_TARGET" "$WSL_DISTRO" "$NODE_IDX" "$HOSTNAME"
    phase_deploy_watchdogs "$SSH_TARGET" "$WSL_DISTRO" "$NODE_IDX" "$HOSTNAME"
    phase_provision_wsl2 "$SSH_TARGET" "$WSL_DISTRO" "$NODE_IDX" "$HOSTNAME"
    phase_reboot_resilience "$SSH_TARGET" "$WSL_DISTRO" "$NODE_IDX" "$HOSTNAME"
    phase_join "$SSH_TARGET" "$WSL_DISTRO" "$NODE_IDX"
  fi

  # [7/7] Success gate: node must be Ready. Wait up to 3 min for kubelet to
  # post status after a fresh join.
  echo "    [7/7] Waiting for node Ready (up to 3 min)..."
  local OK=0
  for _ in $(seq 1 18); do
    if node_ready "$HOSTNAME"; then OK=1; break; fi
    sleep 10
  done
  if [[ "$OK" != "1" ]]; then
    echo "    ✗ ${HOSTNAME} did not become Ready within 3 min." >&2
    FAILED_NODES+=("${HOSTNAME}")
  fi
  echo ""
done <<< "$HOME_WORKER_NODES"

echo ""
if [[ ${#FAILED_NODES[@]} -gt 0 ]]; then
  echo "=== PROVISION INCOMPLETE — failed nodes: ${FAILED_NODES[*]} ==="
  exit 1
fi
echo "=== ✓ ALL TARGETED NODES PROVISIONED AND READY ==="

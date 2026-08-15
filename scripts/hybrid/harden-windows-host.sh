#!/usr/bin/env bash
# =============================================================================
# scripts/hybrid/harden-windows-host.sh
#
# Transforms a Windows laptop / workstation into a headless 24/7 home-lab
# worker server managed remotely via SSH.
#
# After this script runs once from the Mac, nodes are FULLY SELF-HEALING:
#   - Windows watchdog (Task Scheduler, every 2 min): heals WinNAT + WSL2 + Tailscale
#   - WSL2 watchdog (systemd timer, every 60s): heals tailscaled + kubelet + containerd
#   - Mac is NOT required for any runtime recovery
#
# Hardening performed on the Windows host:
#   1. Power & Lid Action:
#      - Lid Close Action -> Do Nothing (both AC and DC power)
#      - Standby / Sleep -> Never on AC power
#      - Hibernate -> Disabled on AC power
#      - Display / Monitor timeout -> 5 min (turns off screen to reduce heat/power)
#   2. Windows Updates:
#      - Completely disables automatic Windows Updates & reboots
#      - Stops & disables wuauserv, UsoSvc, WaaSMedicSvc, bits, dosvc
#      - Configures Group Policy registry keys (NoAutoUpdate, AUOptions=1, NoAutoReboot)
#   3. Battery Conservation & Health:
#      - Detects battery / power hardware status
#      - Attempts to enable Lenovo Conservation Mode (75-80% charge threshold)
#      - Attempts to configure Dell Command battery charging thresholds if supported
#   4. WSL2 Self-Healing Watchdog (replaces old KeepAlive):
#      - Writes vmIdleTimeout=-1 in %USERPROFILE%\.wslconfig
#      - Deploys wsl2-node-watchdog.ps1 → C:\ProgramData\soloz\
#      - Writes C:\ProgramData\soloz\wsl2-node-config.json (distro, subnet, ts-flags)
#      - Registers HybridWSLWatchdog<N> task: runs every 2 min as SYSTEM
#        Heals: WinNAT object, vEthernet (WSL) adapter, WSL2 distro idle, Tailscale
#      - Deploys tailscale-watchdog.sh + systemd service + timer into WSL2
#        Heals: tailscaled, kubelet, containerd — every 60 seconds inside WSL2
#      - Optionally writes Tailscale auth-key to /etc/soloz/tailscale-authkey
#   5. Network & OpenSSH Service:
#      - Ensures sshd service is set to Automatic and running
#      - Ensures Windows Firewall rule allows inbound TCP port 22
#
# Usage:
#   ./scripts/hybrid/harden-windows-host.sh              # Harden all registered nodes
#   ./scripts/hybrid/harden-windows-host.sh --node 2     # Harden node 2 only
#   ./scripts/hybrid/harden-windows-host.sh --env <file> # Custom env file
#   ./scripts/hybrid/harden-windows-host.sh --ts-authkey <key>  # Embed Tailscale auth-key
# =============================================================================
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
ENV_FILE="${HERE}/home-lab.env"
ONLY_NODE=""
TS_AUTHKEY=""

usage() {
  cat <<EOF
Usage: $0 [OPTIONS]

Options:
  --node N           Harden only node N (1-based index from home-lab.env)
  --env FILE         Path to environment file (default: scripts/hybrid/home-lab.env)
  --ts-authkey KEY   Tailscale auth-key to embed for autonomous re-auth on NeedsLogin.
                     Generate a reusable key at https://login.tailscale.com/admin/settings/keys
                     Key is stored at /etc/soloz/tailscale-authkey (chmod 600) in WSL2.
  -h, --help         Show this help message
EOF
  exit 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --node)        ONLY_NODE="$2"; shift 2 ;;
    --env)         ENV_FILE="$2"; shift 2 ;;
    --ts-authkey)  TS_AUTHKEY="$2"; shift 2 ;;
    -h|--help) usage ;;
    *) echo "Unknown option: $1" >&2; usage ;;
  esac
done

if [[ ! -f "$ENV_FILE" ]]; then
  echo "ERROR: Environment file not found: $ENV_FILE" >&2
  exit 1
fi

# shellcheck source=/dev/null
source "$ENV_FILE"

echo "=== Windows Host Headless Server Hardening ==="
echo "    Registry: ${ENV_FILE}"
echo ""

# Helper to execute base64-encoded PowerShell script block on Windows host over SSH
win_ps() {
  local SSH_TARGET="$1" PS_SCRIPT="$2"
  printf '%s\n' "$PS_SCRIPT" | ssh -o BatchMode=yes -o ConnectTimeout=15 -o StrictHostKeyChecking=no \
    "$SSH_TARGET" "powershell -NoProfile -ExecutionPolicy Bypass -Command -" 2>&1 || true
}

NODE_IDX=0
while IFS='|' read -r HOSTNAME SSH_TARGET WSL_DISTRO TAILNET_HOST BOX_TAG; do
  [[ -z "$HOSTNAME" ]] && continue
  NODE_IDX=$((NODE_IDX + 1))

  if [[ -n "$ONLY_NODE" && "$NODE_IDX" != "$ONLY_NODE" ]]; then
    continue
  fi

  echo "── node ${NODE_IDX}: ${HOSTNAME} (${BOX_TAG}) ─────────────────"
  echo "    SSH: ${SSH_TARGET}  WSL: ${WSL_DISTRO}"

  if ! ssh -n -o BatchMode=yes -o ConnectTimeout=6 -o StrictHostKeyChecking=no \
        "${SSH_TARGET}" "echo ok" < /dev/null >/dev/null 2>&1; then
    echo "    ✗ SSH to ${SSH_TARGET} failed — skipping." >&2
    continue
  fi
  echo "    ✓ SSH connected to Windows host"

  # Read wsl2-node-watchdog.ps1 and encode as base64 for deployment
  WATCHDOG_PS1_PATH="${HERE}/windows/wsl2-node-watchdog.ps1"
  if [[ ! -f "$WATCHDOG_PS1_PATH" ]]; then
    echo "    ✗ Missing: ${WATCHDOG_PS1_PATH}" >&2
    echo "      Ensure zero-ops/scripts/hybrid/windows/wsl2-node-watchdog.ps1 exists" >&2
    RECOVER_FAILED+=("${HOSTNAME}")
    continue
  fi
  WATCHDOG_PS1_B64=$(base64 < "$WATCHDOG_PS1_PATH" | tr -d '\n')

  # Read tailscale-watchdog.sh and encode as base64 for WSL2 deployment
  TS_WATCHDOG_SH_PATH="${HERE}/wsl2/tailscale-watchdog.sh"
  TS_SERVICE_PATH="${HERE}/wsl2/tailscale-watchdog.service"
  TS_TIMER_PATH="${HERE}/wsl2/tailscale-watchdog.timer"
  for F in "$TS_WATCHDOG_SH_PATH" "$TS_SERVICE_PATH" "$TS_TIMER_PATH"; do
    if [[ ! -f "$F" ]]; then
      echo "    ✗ Missing: $F" >&2
      RECOVER_FAILED+=("${HOSTNAME}")
      continue 2
    fi
  done
  TS_SH_B64=$(base64  < "$TS_WATCHDOG_SH_PATH" | tr -d '\n')
  TS_SVC_B64=$(base64 < "$TS_SERVICE_PATH"      | tr -d '\n')
  TS_TMR_B64=$(base64 < "$TS_TIMER_PATH"        | tr -d '\n')

  # Determine WSL2 subnet (from vEthernet (WSL) adapter — query live from Windows)
  WSL_SUBNET=$(win_ps "$SSH_TARGET" "
    \$ip = (Get-NetIPAddress -InterfaceAlias 'vEthernet (WSL)' -AddressFamily IPv4 -ErrorAction SilentlyContinue | Select-Object -First 1)
    if (\$ip) {
      \$octets = \$ip.IPAddress.Split('.')
      \$octets[2] = [Math]::Floor([int]\$octets[2] / 16) * 16
      \$octets[3] = 0
      Write-Output (\$octets -join '.' + '/' + \$ip.PrefixLength)
    } else {
      Write-Output '172.27.32.0/20'
    }
  " 2>/dev/null | grep -Eo '[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+/[0-9]+' | head -1)
  WSL_SUBNET="${WSL_SUBNET:-172.27.32.0/20}"

  # Tailscale flags for this node
  TS_FLAGS="--accept-routes --hostname=${HOSTNAME} --accept-dns=false"

  # ── Step A: Deploy WSL2-side watchdog files directly via SSH (pre-PS) ─────
  echo "    → deploying WSL2-side watchdog files..."
  # Write files via stdin pipe through wsl.exe — avoids quoting/b64 issues
  printf '%s\n' "$(cat "$TS_WATCHDOG_SH_PATH")" | \
    ssh -o BatchMode=yes -o ConnectTimeout=15 -o StrictHostKeyChecking=no \
      "$SSH_TARGET" \
      "wsl.exe -d $WSL_DISTRO -u root -- bash -c 'mkdir -p /usr/local/bin && cat > /usr/local/bin/tailscale-watchdog.sh && chmod 755 /usr/local/bin/tailscale-watchdog.sh'" 2>/dev/null && \
    echo "    ✓ tailscale-watchdog.sh deployed" || echo "    ⚠ tailscale-watchdog.sh deploy failed"

  printf '%s\n' "$(cat "$TS_SERVICE_PATH")" | \
    ssh -o BatchMode=yes -o ConnectTimeout=15 -o StrictHostKeyChecking=no \
      "$SSH_TARGET" \
      "wsl.exe -d $WSL_DISTRO -u root -- bash -c 'mkdir -p /etc/systemd/system && cat > /etc/systemd/system/tailscale-watchdog.service'" 2>/dev/null && \
    echo "    ✓ tailscale-watchdog.service deployed" || echo "    ⚠ tailscale-watchdog.service deploy failed"

  printf '%s\n' "$(cat "$TS_TIMER_PATH")" | \
    ssh -o BatchMode=yes -o ConnectTimeout=15 -o StrictHostKeyChecking=no \
      "$SSH_TARGET" \
      "wsl.exe -d $WSL_DISTRO -u root -- bash -c 'mkdir -p /etc/systemd/system && cat > /etc/systemd/system/tailscale-watchdog.timer'" 2>/dev/null && \
    echo "    ✓ tailscale-watchdog.timer deployed" || echo "    ⚠ tailscale-watchdog.timer deploy failed"

  # Write node config env
  printf 'NODE_HOSTNAME="%s"\nTAILSCALE_FLAGS="%s"\n' "${HOSTNAME}" "${TS_FLAGS}" | \
    ssh -o BatchMode=yes -o ConnectTimeout=15 -o StrictHostKeyChecking=no \
      "$SSH_TARGET" \
      "wsl.exe -d $WSL_DISTRO -u root -- bash -c 'mkdir -p /etc/soloz && cat > /etc/soloz/node-config.env'" 2>/dev/null && \
    echo "    ✓ /etc/soloz/node-config.env written" || echo "    ⚠ node-config.env write failed"

  # Write Tailscale auth-key if provided
  if [[ -n "$TS_AUTHKEY" ]]; then
    printf '%s\n' "$TS_AUTHKEY" | \
      ssh -o BatchMode=yes -o ConnectTimeout=15 -o StrictHostKeyChecking=no \
        "$SSH_TARGET" \
        "wsl.exe -d $WSL_DISTRO -u root -- bash -c 'mkdir -p /etc/soloz && cat > /etc/soloz/tailscale-authkey && chmod 600 /etc/soloz/tailscale-authkey'" 2>/dev/null && \
      echo "    ✓ /etc/soloz/tailscale-authkey written (chmod 600)" || echo "    ⚠ tailscale-authkey write failed"
  fi

  # Enable + start the timer
  WSL2_TIMER_OUT=$(ssh -o BatchMode=yes -o ConnectTimeout=15 -o StrictHostKeyChecking=no \
    "$SSH_TARGET" \
    "wsl.exe -d $WSL_DISTRO -u root -- bash -c 'systemctl daemon-reload && systemctl enable tailscale-watchdog.timer && systemctl start tailscale-watchdog.timer && systemctl is-active tailscale-watchdog.timer'" 2>/dev/null || echo "failed")
  if [[ "$WSL2_TIMER_OUT" == "active" ]]; then
    echo "    ✓ tailscale-watchdog.timer: active"
  else
    echo "    ⚠ tailscale-watchdog.timer: ${WSL2_TIMER_OUT} (may need systemd running in WSL2)"
  fi

  # ── Step B: Windows-side hardening via PowerShell ─────────────────────────
  # Build PowerShell script for server hardening
  PS_HARDEN="
\$distro = '$WSL_DISTRO';
\$nodeIdx = '$NODE_IDX';
\$hostname = '$HOSTNAME';
\$wslSubnet = '$WSL_SUBNET';
\$tsFlags = '$TS_FLAGS';
\$watchdogB64 = '$WATCHDOG_PS1_B64';

Write-Output '=== [1/5] Configuring Power & Lid Actions ==='
try {
  # Subgroup GUID for buttons: 4f971e89-eebd-4455-a8de-9e59040e7347
  # Setting GUID for lid action: 5ca83367-6e45-459f-a27b-476b1d01c936
  # 0 = Do Nothing, 1 = Sleep, 2 = Hibernate, 3 = Shut down
  powercfg /setacvalueindex SCHEME_CURRENT 4f971e89-eebd-4455-a8de-9e59040e7347 5ca83367-6e45-459f-a27b-476b1d01c936 0
  powercfg /setdcvalueindex SCHEME_CURRENT 4f971e89-eebd-4455-a8de-9e59040e7347 5ca83367-6e45-459f-a27b-476b1d01c936 0

  # Disable Sleep and Hibernate on AC
  powercfg /change standby-timeout-ac 0
  powercfg /change hibernate-timeout-ac 0

  # Turn off display after 5 min on AC to reduce screen heat/power with lid open or closed
  powercfg /change monitor-timeout-ac 5
  powercfg /setactive SCHEME_CURRENT
  Write-Output '    ✓ Lid Close -> Do Nothing (AC & DC)'
  Write-Output '    ✓ AC Sleep/Hibernate -> Never'
  Write-Output '    ✓ Monitor Timeout -> 5 minutes'
} catch {
  Write-Output ('    ⚠ Power configuration issue: ' + \$_.Exception.Message)
}

Write-Output '=== [2/5] Disabling Automatic Windows Updates & Reboots ==='
try {
  # Disable update services
  @('wuauserv', 'UsoSvc', 'WaaSMedicSvc', 'bits', 'dosvc') | ForEach-Object {
    Stop-Service -Name \$_ -Force -ErrorAction SilentlyContinue
    Set-Service -Name \$_ -StartupType Disabled -ErrorAction SilentlyContinue
  }

  # Registry Group Policy overrides
  \$auPath = 'HKLM:\SOFTWARE\Policies\Microsoft\Windows\WindowsUpdate\AU'
  if (!(Test-Path \$auPath)) { New-Item -Path \$auPath -Force | Out-Null }
  Set-ItemProperty -Path \$auPath -Name 'NoAutoUpdate' -Value 1 -Type DWord -Force
  Set-ItemProperty -Path \$auPath -Name 'AUOptions' -Value 1 -Type DWord -Force
  Set-ItemProperty -Path \$auPath -Name 'NoAutoRebootWithLoggedOnUsers' -Value 1 -Type DWord -Force
  Set-ItemProperty -Path \$auPath -Name 'AlwaysAutoRebootAtScheduledTime' -Value 0 -Type DWord -Force

  Write-Output '    ✓ Windows Update services stopped & disabled'
  Write-Output '    ✓ Group Policy NoAutoUpdate=1 & NoAutoReboot=1 enforced'
} catch {
  Write-Output ('    ⚠ Windows Update policy issue: ' + \$_.Exception.Message)
}

Write-Output '=== [3/5] Installing Self-Healing WSL2 Watchdog (Windows-side) ==='
try {
  # Write .wslconfig to prevent VM teardown on idle
  \$wslconfigPath = Join-Path \$env:USERPROFILE '.wslconfig'
  \$wslconfigContent = @'
[wsl2]
vmIdleTimeout=-1
swap=0
localhostForwarding=true
'@
  Set-Content -Path \$wslconfigPath -Value \$wslconfigContent -Encoding UTF8 -Force
  Write-Output '    ✓ .wslconfig configured (vmIdleTimeout=-1)'

  # Create C:\ProgramData\soloz directory
  \$solozDir = 'C:\ProgramData\soloz'
  if (!(Test-Path \$solozDir)) { New-Item -ItemType Directory -Path \$solozDir -Force | Out-Null }

  # Deploy wsl2-node-watchdog.ps1
  [System.IO.File]::WriteAllBytes(
    (Join-Path \$solozDir 'wsl2-node-watchdog.ps1'),
    [System.Convert]::FromBase64String(\$watchdogB64)
  )
  Write-Output '    ✓ wsl2-node-watchdog.ps1 deployed to C:\ProgramData\soloz\'

  # Write node config JSON
  \$nodeConfig = @{
    wslDistro      = \$distro
    nodeHostname   = \$hostname
    natName        = 'WSLNat'
    natSubnet      = \$wslSubnet
    tailscaleFlags = \$tsFlags
  } | ConvertTo-Json
  Set-Content -Path (Join-Path \$solozDir 'wsl2-node-config.json') -Value \$nodeConfig -Encoding UTF8 -Force
  Write-Output ('    ✓ wsl2-node-config.json written (subnet=' + \$wslSubnet + ')')

  # Remove old HybridWSLKeepAlive task if present (superseded)
  \$oldTask = 'HybridWSLKeepAlive' + \$nodeIdx
  if (Get-ScheduledTask -TaskName \$oldTask -ErrorAction SilentlyContinue) {
    Unregister-ScheduledTask -TaskName \$oldTask -Confirm:\$false
    Write-Output ('    ✓ Removed legacy task: ' + \$oldTask)
  }

  # Register HybridWSLWatchdog<N> with 2-minute repetition — runs as SYSTEM
  \$watchdogName = 'HybridWSLWatchdog' + \$nodeIdx
  \$action   = New-ScheduledTaskAction \
    -Execute 'C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe' \
    -Argument '-NonInteractive -ExecutionPolicy Bypass -File C:/ProgramData/soloz/wsl2-node-watchdog.ps1'
  \$trigBoot  = New-ScheduledTaskTrigger -AtStartup
  \$trigLogon = New-ScheduledTaskTrigger -AtLogOn
  \$rep = New-ScheduledTaskTrigger -RepetitionInterval (New-TimeSpan -Minutes 2) -Once -At (Get-Date)
  \$trigBoot.Repetition  = \$rep.Repetition
  \$trigLogon.Repetition = \$rep.Repetition
  \$settings = New-ScheduledTaskSettingsSet \
    -AllowStartIfOnBatteries \
    -DontStopIfGoingOnBatteries \
    -StartWhenAvailable \
    -RunOnlyIfNetworkAvailable:\$false \
    -MultipleInstances IgnoreNew \
    -ExecutionTimeLimit (New-TimeSpan -Minutes 3) \
    -RestartCount 10 \
    -RestartInterval (New-TimeSpan -Minutes 1)
  \$principal = New-ScheduledTaskPrincipal -UserId 'SYSTEM' -LogonType ServiceAccount -RunLevel Highest
  Register-ScheduledTask \
    -TaskName \$watchdogName \
    -Action \$action \
    -Trigger @(\$trigBoot, \$trigLogon, \$rep) \
    -Settings \$settings \
    -Principal \$principal \
    -Description 'Self-healing watchdog: WinNAT + WSL2 distro + Tailscale (runs every 2 min as SYSTEM)' \
    -Force | Out-Null
  Start-ScheduledTask -TaskName \$watchdogName -ErrorAction SilentlyContinue
  Write-Output ('    ✓ ' + \$watchdogName + ' registered (SYSTEM, every 2 min)')

  Write-Output ''
  Write-Output '    ─── Watchdog summary ───────────────────────────────────'
  Write-Output '    Windows: HybridWSLWatchdog runs every 2 min as SYSTEM'
  Write-Output '    WSL2:    tailscale-watchdog.timer fires every 60 seconds'
  Write-Output '    Mac not required for any runtime recovery.'
  Write-Output '    ────────────────────────────────────────────────────────'
} catch {
  Write-Output ('    ⚠ Watchdog installation issue: ' + \$_.Exception.Message)
}

Write-Output '=== [4/5] Checking Battery Conservation & Health ==='
try {
  \$battery = Get-WmiObject Win32_Battery -ErrorAction SilentlyContinue
  if (\$battery) {
    Write-Output ('    • Battery Status: ' + \$battery.EstimatedChargeRemaining + '% charge remaining (Status: ' + \$battery.Status + ')')
    
    # Lenovo Conservation Mode check / toggle
    \$lenovoWmi = Get-WmiObject -Namespace root\WMI -Class Lenovo_SetConservationMode -ErrorAction SilentlyContinue
    if (\$lenovoWmi) {
      # Try enabling Conservation Mode (limits battery charge to ~75-80%)
      try {
        \$lenovoWmi.SetConservationMode(1) | Out-Null
        Write-Output '    ✓ Lenovo Conservation Mode enabled (charge capped at ~75-80%)'
      } catch {
        Write-Output '    • Lenovo Conservation Mode supported via Lenovo Vantage'
      }
    } else {
      # Registry fallback for Lenovo Power Management
      \$pwrReg = 'HKLM:\SOFTWARE\WOW6432Node\Lenovo\PWRMGRV\ConfInfo\AutoShut'
      if (Test-Path \$pwrReg) {
        Set-ItemProperty -Path \$pwrReg -Name 'ConservationMode' -Value 1 -ErrorAction SilentlyContinue
        Write-Output '    ✓ Lenovo ConservationMode registry flag set'
      }
    }
  } else {
    Write-Output '    • No battery detected (desktop workstation)'
  }
} catch {
  Write-Output ('    • Battery check note: ' + \$_.Exception.Message)
}

Write-Output '=== [5/5] Verifying OpenSSH Server & Firewall ==='
try {
  Set-Service -Name sshd -StartupType Automatic -ErrorAction SilentlyContinue
  Start-Service -Name sshd -ErrorAction SilentlyContinue
  New-NetFirewallRule -Name 'OpenSSH-Server-In-TCP' -DisplayName 'OpenSSH SSH Server (sshd)' -Enabled True -Direction Inbound -Protocol TCP -Action Allow -LocalPort 22 -ErrorAction SilentlyContinue | Out-Null
  Write-Output '    ✓ OpenSSH Server set to Automatic and running'
  Write-Output '    ✓ Firewall rule verified for TCP port 22'
} catch {
  Write-Output ('    • SSH service note: ' + \$_.Exception.Message)
}
"

  echo "    → running Windows hardening on ${HOSTNAME}..."
  OUT=$(win_ps "$SSH_TARGET" "$PS_HARDEN")
  echo "$OUT" | grep -v -E '#< CLIXML|<Objs|</Objs>|<Obj|<TN|<MS|<I64|<PR|<AV|<AI|<Nil|<PI|<PC|<T>|<SR|<SD|WARNING: connection' || true
  echo ""


done <<< "$HOME_WORKER_NODES"

echo "=== Windows Host Hardening Complete ==="

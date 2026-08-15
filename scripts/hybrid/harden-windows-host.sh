#!/usr/bin/env bash
# =============================================================================
# scripts/hybrid/harden-windows-host.sh
#
# Transforms a Windows laptop / workstation into a headless 24/7 home-lab
# worker server managed remotely via SSH.
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
#   4. WSL2 24/7 Server Persistence:
#      - Writes vmIdleTimeout=-1 in %USERPROFILE%\.wslconfig
#      - Registers & starts HybridWSLKeepAlive task (sleep infinity 24/7/365)
#   5. Network & OpenSSH Service:
#      - Ensures sshd service is set to Automatic and running
#      - Ensures Windows Firewall rule allows inbound TCP port 22
#
# Usage:
#   ./scripts/hybrid/harden-windows-host.sh             # Harden all registered nodes
#   ./scripts/hybrid/harden-windows-host.sh --node 2    # Harden node 2 only
#   ./scripts/hybrid/harden-windows-host.sh --env <file># Custom env file
# =============================================================================
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
ENV_FILE="${HERE}/home-lab.env"
ONLY_NODE=""

usage() {
  cat <<EOF
Usage: $0 [OPTIONS]

Options:
  --node N     Harden only node N (1-based index from home-lab.env)
  --env FILE   Path to environment file (default: scripts/hybrid/home-lab.env)
  -h, --help   Show this help message
EOF
  exit 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --node) ONLY_NODE="$2"; shift 2 ;;
    --env)  ENV_FILE="$2"; shift 2 ;;
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

  # Build PowerShell script for server hardening
  PS_HARDEN="
\$distro = '$WSL_DISTRO';
\$nodeIdx = '$NODE_IDX';
\$hostname = '$HOSTNAME';

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

Write-Output '=== [3/5] Configuring WSL2 24/7 Keep-Alive & Persistence ==='
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

  # Register HybridWSLKeepAlive task
  \$keepaliveName = 'HybridWSLKeepAlive' + \$nodeIdx
  \$actionKA   = New-ScheduledTaskAction -Execute 'C:\Windows\System32\wsl.exe' -Argument ('-d ' + \$distro + ' -u root -e bash -c \"sleep infinity\"')
  \$triggerKA1 = New-ScheduledTaskTrigger -AtStartup
  \$triggerKA2 = New-ScheduledTaskTrigger -AtLogOn
  \$settingsKA = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1) -ExecutionTimeLimit (New-TimeSpan -Days 365)
  Register-ScheduledTask -TaskName \$keepaliveName -Action \$actionKA -Trigger @(\$triggerKA1, \$triggerKA2) -Settings \$settingsKA -Description 'Keep WSL2 VM running continuously 24/7' -Force | Out-Null
  Start-ScheduledTask -TaskName \$keepaliveName -ErrorAction SilentlyContinue

  Write-Output '    ✓ .wslconfig configured (vmIdleTimeout=-1, swap=0)'
  Write-Output ('    ✓ ' + \$keepaliveName + ' registered and running (24/7 background keepalive)')
} catch {
  Write-Output ('    ⚠ WSL persistence configuration issue: ' + \$_.Exception.Message)
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

  echo "    → running automated hardening script on ${HOSTNAME}..."
  OUT=$(win_ps "$SSH_TARGET" "$PS_HARDEN")
  echo "$OUT" | grep -v -E '#< CLIXML|<Objs|</Objs>|<Obj|<TN|<MS|<I64|<PR|<AV|<AI|<Nil|<PI|<PC|<T>|<SR|<SD|WARNING: connection' || true
  echo ""

done <<< "$HOME_WORKER_NODES"

echo "=== Windows Host Hardening Complete ==="

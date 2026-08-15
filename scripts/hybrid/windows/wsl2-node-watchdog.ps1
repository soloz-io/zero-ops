# =============================================================================
# zero-ops/scripts/hybrid/windows/wsl2-node-watchdog.ps1
#
# Self-healing watchdog for a WSL2 hybrid Kubernetes node.
# Runs every 2 minutes via Windows Task Scheduler (SYSTEM account).
# NO external dependencies — no Mac, no SSH, no remote agents.
#
# Heals:
#   1. WSL2 distro WEDGED / unresponsive
#        -> FULL VM RESET: wsl --shutdown + distro restart + wait for the
#           WSL2-side watchdog to restore services. Cooldown-gated so a
#           broken host cannot thrash the VM.
#
# Detection — heartbeat file, NOT wsl.exe:
#   The WSL2 watchdog (tailscale-watchdog.sh, runs every 60s INSIDE the
#   distro) writes /mnt/c/ProgramData/soloz/wsl2-health-heartbeat.txt. The
#   Windows watchdog checks that file's LastWriteTime. A fresh heartbeat
#   means the distro is genuinely alive and services are being maintained.
#   A stale heartbeat means the distro is wedged (or its watchdog died).
#
# Why not probe via `wsl.exe ... echo ok`?
#   In mirrored-networking WSL2 (networkingMode=mirrored), invoking wsl.exe
#   from the SYSTEM account hangs/times out even when the distro is healthy,
#   causing false "wedged" detections and destructive resets of healthy
#   nodes. The heartbeat file avoids wsl.exe from SYSTEM entirely.
#
# No WinNAT logic: WSL2 here uses mirrored networking, so there is no
# vEthernet (WSL) adapter and no NetNat object to maintain.
#
# Config: C:\ProgramData\soloz\wsl2-node-config.json  (written by installer)
# State:  C:\ProgramData\soloz\wsl2-watchdog-state.json (reset cooldown tracking)
# Heartbeat: C:\ProgramData\soloz\wsl2-watchdog-heartbeat.txt
# Health input: C:\ProgramData\soloz\wsl2-health-heartbeat.txt (written by WSL2 watchdog)
# Log: C:\ProgramData\soloz\wsl2-watchdog.log  (last 500 lines kept)
# =============================================================================

$ErrorActionPreference = 'SilentlyContinue'

# ── Config ───────────────────────────────────────────────────────────────────
$SolozDir   = 'C:\ProgramData\soloz'
$ConfigFile = Join-Path $SolozDir 'wsl2-node-config.json'
$StateFile  = Join-Path $SolozDir 'wsl2-watchdog-state.json'
$LogFile    = Join-Path $SolozDir 'wsl2-watchdog.log'
$HBFile     = Join-Path $SolozDir 'wsl2-watchdog-heartbeat.txt'
$HealthFile = Join-Path $SolozDir 'wsl2-health-heartbeat.txt'

# Tunables
$HealthStaleSec  = 180   # heartbeat older than this (3 min) => distro wedged
$ResetCooldownMin = 15   # min between full VM resets
$AdapterWaitSec   = 90   # max wait for distro responsiveness after restart
$LogTrimLines     = 500

if (-not (Test-Path $ConfigFile)) {
    exit 1
}

$cfg = Get-Content $ConfigFile -Raw | ConvertFrom-Json
$distro        = $cfg.wslDistro
$nodeHostname  = $cfg.nodeHostname

# ── Logging ──────────────────────────────────────────────────────────────────
function Write-WatchdogLog {
    param(
        [Parameter(Mandatory=$true)][string]$Message,
        [Parameter(Mandatory=$false)][string]$Level = 'INFO'
    )
    $ts = (Get-Date).ToString('yyyy-MM-dd HH:mm:ss')
    $line = "[$ts][$Level] $Message"
    Add-Content -Path $LogFile -Value $line -Encoding UTF8
}

function TrimLog {
    if (Test-Path $LogFile) {
        $lines = Get-Content $LogFile
        if ($lines.Count -gt $LogTrimLines) {
            $lines | Select-Object -Last $LogTrimLines | Set-Content $LogFile -Encoding UTF8
        }
    }
}

# ── State (reset cooldown) ───────────────────────────────────────────────────
function Get-NodeState {
    $default = @{ lastReset = $null; resetCount = 0 }
    if (Test-Path $StateFile) {
        try {
            $s = Get-Content $StateFile -Raw | ConvertFrom-Json
            return @{
                lastReset  = $s.lastReset
                resetCount = [int]$s.resetCount
            }
        } catch {
            return $default
        }
    }
    return $default
}

function Save-NodeState {
    param(
        [AllowNull()]$LastReset,
        [int]$ResetCount
    )
    $s = @{
        lastReset  = $LastReset
        resetCount = $ResetCount
    } | ConvertTo-Json -Compress
    Set-Content -Path $StateFile -Value $s -Encoding UTF8
}

# ── Health probe ─────────────────────────────────────────────────────────────
# Returns $true if the WSL2-side watchdog heartbeat is fresh (distro alive).
function Test-DistroHealthy {
    if (-not (Test-Path $HealthFile)) {
        return $false
    }
    $age = (Get-Date) - (Get-Item $HealthFile).LastWriteTime
    return ($age.TotalSeconds -lt $HealthStaleSec)
}

# ── Heal: full VM reset ──────────────────────────────────────────────────────
# Shut the VM down, start the distro, and wait for the WSL2-side watchdog to
# resume (its heartbeat becomes fresh again once services are restored).
function Reset-Vm {
    Write-WatchdogLog "FULL VM RESET starting (distro=$distro) - wsl --shutdown + distro restart" -Level 'WARN'
    # Persist the reset timestamp BEFORE the long reset so a watchdog killed
    # mid-reset (Task Scheduler 3-min execution limit) still gates retries.
    Save-NodeState -LastReset (Get-Date -Format 'o') -ResetCount ((Get-NodeState).resetCount + 1)

    # 1. Shut down the WSL VM.
    Start-Process -FilePath 'wsl.exe' -ArgumentList '--shutdown' -WindowStyle Hidden | Out-Null
    Start-Sleep -Seconds 8

    # 2. Start the distro with a keepalive so the VM boots and systemd runs.
    Start-Process -FilePath 'wsl.exe' `
        -ArgumentList "-d $distro -u root -e bash -c `"sleep infinity`"" `
        -WindowStyle Hidden | Out-Null

    # 3. Wait (bounded) for the health heartbeat to come back — this confirms
    #    the VM rebooted, systemd started, and the WSL2 watchdog is running.
    $recovered = $false
    for ($i = 0; $i -lt [int]($AdapterWaitSec / 5); $i++) {
        Start-Sleep -Seconds 5
        if (Test-DistroHealthy) {
            $recovered = $true
            break
        }
    }
    if ($recovered) {
        Write-WatchdogLog "VM reset OK - distro heartbeat resumed" -Level 'INFO'
    } else {
        Write-WatchdogLog "VM reset: heartbeat still stale after $AdapterWaitSec s" -Level 'ERROR'
    }
}

# ── Main ─────────────────────────────────────────────────────────────────────
TrimLog
Write-WatchdogLog "=== watchdog cycle start (node=$nodeHostname, distro=$distro) ===" -Level 'INFO'

$state = Get-NodeState
$healthy = Test-DistroHealthy

if ($healthy) {
    Write-WatchdogLog "distro healthy (heartbeat fresh) - no action" -Level 'DEBUG'
} else {
    Write-WatchdogLog "distro heartbeat STALE (>$HealthStaleSec s) - distro may be wedged" -Level 'WARN'

    $cooldownOk = $true
    if ($state.lastReset) {
        $minsSince = ((Get-Date) - [datetime]$state.lastReset).TotalMinutes
        if ($minsSince -lt $ResetCooldownMin) {
            $cooldownOk = $false
            Write-WatchdogLog "reset cooldown active ($([math]::Round($minsSince,1))/$ResetCooldownMin min)" -Level 'WARN'
        }
    }

    if ($cooldownOk) {
        Reset-Vm
    } else {
        Save-NodeState -LastReset $state.lastReset -ResetCount $state.resetCount
    }
}

Set-Content -Path $HBFile -Value ((Get-Date -Format 'yyyy-MM-ddTHH:mm:ssZ')) -Encoding UTF8
Write-WatchdogLog "=== watchdog cycle done ===" -Level 'INFO'

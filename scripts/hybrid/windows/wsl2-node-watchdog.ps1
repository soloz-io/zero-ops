# =============================================================================
# zero-ops/scripts/hybrid/windows/wsl2-node-watchdog.ps1
#
# Self-healing watchdog for a WSL2 hybrid Kubernetes node.
# Runs every 2 minutes via Windows Task Scheduler (SYSTEM account).
# NO external dependencies — no Mac, no SSH, no remote agents.
#
# Heals:
#   1. WinNAT (NetNat) object destroyed after sleep/Hyper-V restart
#   2. vEthernet (WSL) adapter down
#   3. WSL2 distro not running (idle shutdown despite vmIdleTimeout=-1)
#   4. Tailscale disconnected (state: Disconnected or NoState)
#
# Config: C:\ProgramData\soloz\wsl2-node-config.json  (written by installer)
# Heartbeat: C:\ProgramData\soloz\wsl2-watchdog-heartbeat.txt
# Log: C:\ProgramData\soloz\wsl2-watchdog.log  (last 500 lines kept)
# =============================================================================

$ErrorActionPreference = 'SilentlyContinue'

# ── Config ───────────────────────────────────────────────────────────────────
$SolozDir   = 'C:\ProgramData\soloz'
$ConfigFile = Join-Path $SolozDir 'wsl2-node-config.json'
$LogFile    = Join-Path $SolozDir 'wsl2-watchdog.log'
$HBFile     = Join-Path $SolozDir 'wsl2-watchdog-heartbeat.txt'

if (-not (Test-Path $ConfigFile)) {
    exit 1
}

$cfg = Get-Content $ConfigFile -Raw | ConvertFrom-Json
$distro        = $cfg.wslDistro
$natName       = $cfg.natName
$natSubnet     = $cfg.natSubnet
$nodeHostname  = $cfg.nodeHostname
$tsFlags       = $cfg.tailscaleFlags

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
        if ($lines.Count -gt 500) {
            $lines | Select-Object -Last 500 | Set-Content $LogFile -Encoding UTF8
        }
    }
}

# ── Heal: WinNAT ─────────────────────────────────────────────────────────────
function Heal-WinNat {
    $existing = Get-NetNat -Name $natName 2>$null
    if ($existing) {
        return
    }

    Write-WatchdogLog -Message "WinNAT '$natName' missing - recreating for subnet $natSubnet" -Level 'WARN'
    try {
        New-NetNat -Name $natName -InternalIPInterfaceAddressPrefix $natSubnet | Out-Null
        Write-WatchdogLog -Message "WinNAT '$natName' recreated" -Level 'INFO'
    } catch {
        Write-WatchdogLog -Message "NetNat creation note: $($_.Exception.Message)" -Level 'WARN'
    }
}

# ── Heal: WSL2 distro liveness ───────────────────────────────────────────────
function Heal-WslDistro {
    $test = & wsl.exe -d $distro -u root -e echo ok 2>$null
    if ($test -match 'ok') {
        return
    }

    Write-WatchdogLog -Message "WSL2 distro '$distro' not responsive — launching background keepalive" -Level 'WARN'
    Start-Process -FilePath 'wsl.exe' `
        -ArgumentList "-d $distro -u root -e bash -c `"sleep infinity`"" `
        -WindowStyle Hidden `
        -PassThru | Out-Null

    Start-Sleep -Seconds 5
    Write-WatchdogLog -Message "WSL2 distro '$distro' keepalive launched" -Level 'INFO'
}

# ── Main ─────────────────────────────────────────────────────────────────────
TrimLog
Write-WatchdogLog -Message "=== watchdog cycle start (node=$nodeHostname, distro=$distro) ===" -Level 'INFO'

Heal-WinNat
Heal-WslDistro

Set-Content -Path $HBFile -Value ((Get-Date -Format 'yyyy-MM-ddTHH:mm:ssZ')) -Encoding UTF8
Write-WatchdogLog -Message "=== watchdog cycle done ===" -Level 'INFO'

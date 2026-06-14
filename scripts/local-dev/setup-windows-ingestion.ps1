<#
.SYNOPSIS
Establishes the SSHFS bridge to the Mac using the shared /mnt/wsl tmpfs via WSL2.

.DESCRIPTION
This script is intended to be run from native Windows PowerShell. It automatically invokes WSL 
to install sshfs (if missing) and mounts the Mac's /mnt/wsl/zero-ops directory into the Windows
shared /mnt/wsl filesystem. This allows the Windows Docker Desktop VM to natively read the Mac's
CAPD bootstrap files.
#>

$ErrorActionPreference = "Stop"

$MacUser = "arun_subramanian"
$MacIp = "192.168.1.2"

Write-Host "🌉 Setting up Synchronous SSHFS Locality Bridge on Windows via WSL2..." -ForegroundColor Cyan

# 1. Ensure WSL is available
if (-not (Get-Command "wsl.exe" -ErrorAction SilentlyContinue)) {
    Write-Warning "WSL is not installed or not in PATH. Please install WSL2 first."
    exit
}

# 2. Check and install sshfs inside WSL
Write-Host "Checking for sshfs in WSL..."
$sshfsCheck = wsl.exe bash -c "command -v sshfs"
if (-not $sshfsCheck) {
    Write-Host "Installing sshfs inside WSL. You may be prompted for your WSL sudo password." -ForegroundColor Yellow
    wsl.exe bash -c "sudo apt-get update && sudo apt-get install -y sshfs"
}

# 3. Create the shared /mnt/wsl mount point
Write-Host "Creating shared /mnt/wsl/zero-ops mount point..."
wsl.exe bash -c "sudo mkdir -p /mnt/wsl/zero-ops && sudo chown -R `$USER:`$USER /mnt/wsl/zero-ops"

# 4. Mount the Mac's directory using SSHFS
Write-Host "Mounting Mac's /mnt/wsl/zero-ops securely over SSHFS..."
$mountCheck = wsl.exe bash -c "mountpoint -q /mnt/wsl/zero-ops ; echo `$?"

if ($mountCheck -trim -ne "0") {
    Write-Host "Connecting to Mac ($MacIp)... Ensure your Mac is reachable." -ForegroundColor Cyan
    # Run the sshfs command. We use IdentityFile mapping to the WSL home directory.
    wsl.exe bash -c "sudo sshfs -o allow_other,default_permissions,IdentityFile=~/.ssh/id_ed25519 ${MacUser}@${MacIp}:/mnt/wsl/zero-ops /mnt/wsl/zero-ops"
    
    if ($LASTEXITCODE -eq 0) {
        Write-Host "✅ SSHFS bridge successfully established." -ForegroundColor Green
    } else {
        Write-Warning "Failed to establish SSHFS bridge. Check your SSH keys and Mac IP."
    }
} else {
    Write-Host "✅ SSHFS bridge is already active." -ForegroundColor Green
}

Write-Host "🚀 Windows is ready! Now run bootstrap-distributed.sh on your Mac!" -ForegroundColor Magenta

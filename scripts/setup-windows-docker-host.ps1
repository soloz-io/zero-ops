<#
.SYNOPSIS
Sets up a Windows machine as a Remote Docker Context Host for Zero-Ops local development.

.DESCRIPTION
This script installs OpenSSH Server, configures the service, opens the Windows firewall, 
sets the default SSH shell to PowerShell, and configures the authorized_keys file 
for the macOS orchestrator client.

.NOTES
Requires Administrator privileges to install OpenSSH and configure services.
#>

param (
    [string]$MacPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBjOm7BrNOj3J4O+s1G1i1C+omX3Wg9U88UOzffn1pKW test-cluster-capi"
)

# Ensure running as Administrator
if (-not ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Write-Warning "This script must be run as an Administrator. Please elevate your PowerShell session and try again."
    exit
}

Write-Host "Starting Zero-Ops Windows Docker Host Setup..." -ForegroundColor Cyan

# 1. Install OpenSSH Server
Write-Host "[1/5] Checking OpenSSH Server installation..."
$sshInstalled = (Get-WindowsCapability -Online -Name OpenSSH.Server*).State
if ($sshInstalled -ne 'Installed') {
    Write-Host "Installing OpenSSH Server..."
    Add-WindowsCapability -Online -Name OpenSSH.Server~~~~0.0.1.0
} else {
    Write-Host "OpenSSH Server is already installed." -ForegroundColor Green
}

# 2. Configure and Start sshd service
Write-Host "[2/5] Configuring sshd service to start automatically..."
Set-Service -Name sshd -StartupType 'Automatic'
Start-Service sshd
Write-Host "sshd service is running." -ForegroundColor Green

# 3. Configure Windows Firewall
Write-Host "[3/5] Checking Windows Firewall for OpenSSH..."
$firewallRule = Get-NetFirewallRule -Name *OpenSSH-Server* -ErrorAction SilentlyContinue
if (-not $firewallRule) {
    Write-Host "Adding Firewall Rule for OpenSSH (Port 22)..."
    New-NetFirewallRule -Name sshd -DisplayName 'OpenSSH Server (sshd)' -Enabled True -Direction Inbound -Protocol TCP -Action Allow -LocalPort 22
} else {
    Write-Host "Firewall rule for OpenSSH already exists." -ForegroundColor Green
}

# 4. Set OpenSSH Default Shell to PowerShell
Write-Host "[4/5] Setting OpenSSH Default Shell to PowerShell..."
New-ItemProperty -Path "HKLM:\SOFTWARE\OpenSSH" -Name DefaultShell -Value "C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe" -PropertyType String -Force | Out-Null
Write-Host "Default shell configured." -ForegroundColor Green

# 5. Configure SSH Keys
Write-Host "[5/5] Configuring SSH authorized_keys..."

function Add-SshKey ($KeyPath, $KeyContent, $IsAdminKey) {
    $dir = Split-Path $KeyPath
    if (-not (Test-Path $dir)) { New-Item -Path $dir -ItemType Directory | Out-Null }
    
    $keyExists = $false
    if (Test-Path $KeyPath) {
        $content = Get-Content $KeyPath -Raw
        if ($content -match [regex]::Escape($KeyContent)) {
            $keyExists = $true
        }
    }
    
    if (-not $keyExists) {
        Add-Content -Path $KeyPath -Value $KeyContent
        Write-Host "Mac public key successfully added to $KeyPath" -ForegroundColor Green
    } else {
        Write-Host "Mac public key already exists in $KeyPath" -ForegroundColor Green
    }

    # Set strict permissions on authorized_keys (Windows specific)
    if ($IsAdminKey) {
        icacls.exe $KeyPath /inheritance:r | Out-Null
        icacls.exe $KeyPath /grant:r "SYSTEM:(F)" | Out-Null
        icacls.exe $KeyPath /grant:r "Administrators:(F)" | Out-Null
    } else {
        icacls.exe $dir /inheritance:r | Out-Null
        icacls.exe $dir /grant:r "$($env:USERNAME):(OI)(CI)F" | Out-Null
        icacls.exe $dir /grant:r "SYSTEM:(OI)(CI)F" | Out-Null
        icacls.exe $dir /grant:r "Administrators:(OI)(CI)F" | Out-Null
    }
}

# Standard User Keys
$userSshKeyPath = Join-Path $env:USERPROFILE ".ssh\authorized_keys"
Add-SshKey -KeyPath $userSshKeyPath -KeyContent $MacPublicKey -IsAdminKey $false

# Administrator Keys (Required by OpenSSH for Admin users)
$adminSshKeyPath = "C:\ProgramData\ssh\administrators_authorized_keys"
Add-SshKey -KeyPath $adminSshKeyPath -KeyContent $MacPublicKey -IsAdminKey $true

Write-Host "Setup complete! The Mac orchestrator can now connect." -ForegroundColor Cyan
Write-Host "From the Mac, run:"
Write-Host "docker context create windows-box --docker `"host=ssh://${env:USERNAME}@<windows-ip>`""

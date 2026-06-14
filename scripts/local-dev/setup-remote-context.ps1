<#
.SYNOPSIS
Sets up a Windows PowerShell terminal to securely route Docker commands to the Mac.

.DESCRIPTION
This script generates an SSH key if one does not exist, copies it to the Mac for passwordless login,
and configures the Docker remote context to use the Mac's Docker Engine natively from Windows PowerShell.

$MacUser = "arun_subramanian"
$MacIp = "192.168.1.2"

$ErrorActionPreference = "Stop"
$ContextName = "mac-compute-pool"
$SshKeyPath = Join-Path $env:USERPROFILE ".ssh\id_ed25519"

Write-Host "🔑 Step 1: Ensuring SSH keys exist..." -ForegroundColor Cyan
if (-not (Test-Path $SshKeyPath)) {
    Write-Host "Generating new ED25519 SSH key..."
    # ssh-keygen is available natively in Windows 10/11
    ssh-keygen.exe -t ed25519 -N '""' -f $SshKeyPath
    Write-Host "`n✅ SSH key generated." -ForegroundColor Green
} else {
    Write-Host "✅ SSH key already exists." -ForegroundColor Green
}

Write-Host "`n🛡️ Step 2: Copying SSH key to Mac (You will be prompted for your Mac password)..." -ForegroundColor Cyan
$PublicKey = Get-Content "$SshKeyPath.pub" -Raw
# Windows doesn't have ssh-copy-id natively, so we manually append the key over SSH
$SshCommand = "mkdir -p ~/.ssh && chmod 700 ~/.ssh && echo '$PublicKey' >> ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys"
ssh.exe "${MacUser}@${MacIp}" $SshCommand

Write-Host "`n🐳 Step 3: Configuring Docker Remote Context..." -ForegroundColor Cyan
$Contexts = docker context ls --format '{{.Name}}'
if ($Contexts -contains $ContextName) {
    Write-Host "Context '$ContextName' already exists. Updating..."
    docker context update $ContextName --docker "host=ssh://${MacUser}@${MacIp}"
} else {
    docker context create $ContextName --docker "host=ssh://${MacUser}@${MacIp}"
}

Write-Host "`n🔄 Step 4: Activating the Remote Context..." -ForegroundColor Cyan
docker context use $ContextName

Write-Host "`n✅ Success! Your PowerShell terminal is now natively connected to the Mac's Docker Engine." -ForegroundColor Green
Write-Host "You can verify this by running: docker info" -ForegroundColor Yellow

#!/usr/bin/env pwsh

<#
.SYNOPSIS
    Windows Development Environment Setup Script
.DESCRIPTION
    This script installs and configures a complete development environment on Windows including:
    - Visual Studio Code
    - Git
    - Node.js
    - kubectl
    - SSH key generation
    - Git configuration
.AUTHOR
    Arun Subramanian
.VERSION
    1.0
#>

# Set execution policy to allow script to run
Set-ExecutionPolicy -ExecutionPolicy RemoteSigned -Scope CurrentUser -Force

# Function to check if command exists
function Test-Command {
    param ($Command)
    try {
        Get-Command $Command -ErrorAction Stop | Out-Null
        return $true
    }
    catch {
        return $false
    }
}

# Function to download and install from URL
function Install-FromUrl {
    param (
        [string]$Name,
        [string]$Url,
        [string]$InstallerArgs = "/S"
    )
    Write-Host "Installing $Name..." -ForegroundColor Green
    
    $tempPath = "$env:TEMP\$Name-installer.exe"
    
    try {
        Invoke-WebRequest -Uri $Url -OutFile $tempPath -UseBasicParsing
        Start-Process -FilePath $tempPath -ArgumentList $InstallerArgs -Wait -NoNewWindow
        Remove-Item $tempPath -Force
        Write-Host "$Name installed successfully!" -ForegroundColor Green
    }
    catch {
        Write-Host "Failed to install $Name. Error: $_" -ForegroundColor Red
    }
}

# Function to add to PATH
function Add-ToPath {
    param ([string]$Path)
    $currentPath = [Environment]::GetEnvironmentVariable("PATH", "User")
    if ($currentPath -notlike "*$Path*") {
        [Environment]::SetEnvironmentVariable("PATH", "$currentPath;$Path", "User")
        $env:PATH += ";$Path"
    }
}

Write-Host "Starting Windows Development Environment Setup..." -ForegroundColor Cyan
Write-Host "=================================================" -ForegroundColor Cyan

# 1. Install Visual Studio Code
Write-Host "`n[1/7] Installing Visual Studio Code..." -ForegroundColor Yellow
if (-not (Test-Command "code")) {
    $vscodeUrl = "https://code.visualstudio.com/sha/download?build=stable&os=win32-x64-user"
    Install-FromUrl -Name "VSCode" -Url $vscodeUrl -InstallerArgs "/S /mergetasks=!runcode"
} else {
    Write-Host "Visual Studio Code is already installed." -ForegroundColor Green
}

# 2. Install Git
Write-Host "`n[2/7] Installing Git..." -ForegroundColor Yellow
if (-not (Test-Command "git")) {
    $gitUrl = "https://git-scm.com/download/win"
    Write-Host "Downloading Git from $gitUrl..." -ForegroundColor Green
    
    try {
        # Get the latest Git for Windows download URL
        $response = Invoke-WebRequest -Uri $gitUrl -UseBasicParsing
        $downloadLink = ($response.Links | Where-Object { $_.href -like "*64-bit.exe" } | Select-Object -First 1).href
        if (-not $downloadLink) {
            $downloadLink = "https://github.com/git-for-windows/git/releases/latest/download/Git-2.43.0-64-bit.exe"
        }
        
        Install-FromUrl -Name "Git" -Url $downloadLink -InstallerArgs "/VERYSILENT /NORESTART"
    }
    catch {
        Write-Host "Using fallback Git installer..." -ForegroundColor Yellow
        $fallbackUrl = "https://github.com/git-for-windows/git/releases/latest/download/Git-2.43.0-64-bit.exe"
        Install-FromUrl -Name "Git" -Url $fallbackUrl -InstallerArgs "/VERYSILENT /NORESTART"
    }
} else {
    Write-Host "Git is already installed." -ForegroundColor Green
}

# 3. Install Node.js
Write-Host "`n[3/7] Installing Node.js..." -ForegroundColor Yellow
if (-not (Test-Command "node")) {
    $nodeUrl = "https://nodejs.org/dist/v20.12.2/node-v20.12.2-x64.msi"
    Install-FromUrl -Name "NodeJS" -Url $nodeUrl -InstallerArgs "/quiet /norestart"
} else {
    Write-Host "Node.js is already installed." -ForegroundColor Green
}

# 4. Install kubectl
Write-Host "`n[4/7] Installing kubectl..." -ForegroundColor Yellow
$kubectlPath = "$env:USERPROFILE\kubectl.exe"
if (-not (Test-Path $kubectlPath)) {
    try {
        Write-Host "Downloading kubectl v1.36.0 for Windows ARM64..." -ForegroundColor Green
        $kubectlUrl = "https://dl.k8s.io/release/v1.36.0/bin/windows/arm64/kubectl.exe"
        Invoke-WebRequest -Uri $kubectlUrl -OutFile $kubectlPath -UseBasicParsing
        
        # Add kubectl to PATH
        Add-ToPath -Path $env:USERPROFILE
        Write-Host "kubectl installed successfully!" -ForegroundColor Green
    }
    catch {
        Write-Host "Failed to download kubectl. Error: $_" -ForegroundColor Red
    }
} else {
    Write-Host "kubectl is already installed." -ForegroundColor Green
}

# 5. Generate SSH key
Write-Host "`n[5/7] Generating SSH key..." -ForegroundColor Yellow
$sshDir = "$env:USERPROFILE\.ssh"
$privateKeyPath = "$sshDir\id_ed25519"
$publicKeyPath = "$sshDir\id_ed25519.pub"

if (-not (Test-Path $privateKeyPath)) {
    # Create .ssh directory if it doesn't exist
    if (-not (Test-Path $sshDir)) {
        New-Item -ItemType Directory -Path $sshDir -Force | Out-Null
    }
    
    try {
        Write-Host "Generating SSH key (ed25519) for arun4infra@gmail.com..." -ForegroundColor Green
        ssh-keygen -t ed25519 -C "arun4infra@gmail.com" -f $privateKeyPath -N ""
        
        if (Test-Path $publicKeyPath) {
            Write-Host "`nSSH key generated successfully!" -ForegroundColor Green
            Write-Host "Public key content:" -ForegroundColor Cyan
            Write-Host "-------------------" -ForegroundColor Cyan
            Get-Content $publicKeyPath | Write-Host
            Write-Host "-------------------" -ForegroundColor Cyan
            Write-Host "Please add this public key to your GitHub/GitLab account." -ForegroundColor Yellow
        }
    }
    catch {
        Write-Host "Failed to generate SSH key. Error: $_" -ForegroundColor Red
    }
} else {
    Write-Host "SSH key already exists." -ForegroundColor Green
    Write-Host "Public key content:" -ForegroundColor Cyan
    Write-Host "-------------------" -ForegroundColor Cyan
    Get-Content $publicKeyPath | Write-Host
    Write-Host "-------------------" -ForegroundColor Cyan
}

# 6. Configure Git
Write-Host "`n[6/7] Configuring Git..." -ForegroundColor Yellow
try {
    git config --global user.name "Arun Subramanian"
    git config --global user.email "arun4infra@gmail.com"
    git config --global init.defaultBranch main
    git config --global pull.rebase false
    
    Write-Host "Git configuration completed!" -ForegroundColor Green
    Write-Host "User name: $(git config --global user.name)" -ForegroundColor White
    Write-Host "User email: $(git config --global user.email)" -ForegroundColor White
}
catch {
    Write-Host "Failed to configure Git. Error: $_" -ForegroundColor Red
}

# 7. Verify installations
Write-Host "`n[7/7] Verifying installations..." -ForegroundColor Yellow
Write-Host "Installation Summary:" -ForegroundColor Cyan
Write-Host "====================" -ForegroundColor Cyan

$tools = @{
    "VS Code" = "code --version"
    "Git" = "git --version"
    "Node.js" = "node --version"
    "npm" = "npm --version"
    "kubectl" = "kubectl version --client"
}

foreach ($tool in $tools.GetEnumerator()) {
    try {
        $version = Invoke-Expression $tool.Value 2>$null
        if ($LASTEXITCODE -eq 0) {
            Write-Host "✓ $($tool.Key): $version" -ForegroundColor Green
        } else {
            Write-Host "✗ $($tool.Key): Not found or error" -ForegroundColor Red
        }
    }
    catch {
        Write-Host "✗ $($tool.Key): Not found" -ForegroundColor Red
    }
}

Write-Host "`nSetup completed!" -ForegroundColor Green
Write-Host "Please restart your terminal or PowerShell session to use all installed tools." -ForegroundColor Yellow
Write-Host "=================================================" -ForegroundColor Cyan

# Pause to keep window open if running interactively
if ($Host.Name -eq "ConsoleHost") {
    Write-Host "`nPress any key to exit..." -ForegroundColor Yellow
    $null = $Host.UI.RawUI.ReadKey("NoEcho,IncludeKeyDown")
}

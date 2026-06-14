#!/usr/bin/env bash
# scripts/local-dev/setup-windows-ingestion.sh
# Runs on Windows WSL2 over SSH to establish the synchronous SSHFS bridge to the Mac.

set -euo pipefail

MAC_USER="arun_subramanian"
MAC_IP="192.168.1.2"

echo "🌉 Setting up Synchronous SSHFS Locality Bridge on Windows..."

# 1. Ensure sshfs is installed in Windows WSL2
if ! command -v sshfs >/dev/null 2>&1; then
    echo "Installing sshfs..."
    sudo apt-get update && sudo apt-get install -y sshfs
fi

# 2. Create the target mount point identically matching the Mac
sudo mkdir -p /var/folders
sudo chown -R "$USER:$USER" /var/folders

# 3. Mount the Mac's /var/folders into WSL2 natively
if ! mountpoint -q /var/folders; then
    echo "Mounting Mac's /var/folders securely over SSHFS..."
    sudo sshfs -o allow_other,default_permissions,IdentityFile=~/.ssh/id_ed25519 "${MAC_USER}@${MAC_IP}:/var/folders" /var/folders
    echo "✅ SSHFS bridge successfully established."
else
    echo "✅ SSHFS bridge is already active."
fi

echo "🚀 Windows is ready to ingest workloads from the Mac!"
#!/usr/bin/env bash
# scripts/local-dev/setup-windows-node.sh
# Runs on Windows WSL2 to establish the synchronous SSHFS bridge to the Mac.

set -euo pipefail

MAC_USER="arun_subramanian"
MAC_IP="192.168.1.2"

echo "🌉 Setting up Synchronous SSHFS Locality Bridge..."

# 1. Ensure sshfs is installed
if ! command -v sshfs >/dev/null 2>&1; then
    echo "Installing sshfs..."
    sudo apt-get update && sudo apt-get install -y sshfs
fi

# 2. Create the target mount point identically matching the Mac
sudo mkdir -p /var/folders
sudo chown -R "$USER:$USER" /var/folders

# 3. Mount the Mac's /var/folders into WSL2
# We check if it's already mounted to prevent overlapping mounts
if ! mountpoint -q /var/folders; then
    echo "Mounting Mac's /var/folders securely over SSHFS..."
    # -o allow_other ensures the Docker daemon can read the mount
    sudo sshfs -o allow_other,default_permissions,IdentityFile=~/.ssh/id_ed25519 "${MAC_USER}@${MAC_IP}:/var/folders" /var/folders
    echo "✅ SSHFS bridge successfully established."
else
    echo "✅ SSHFS bridge is already active."
fi

echo "🚀 Windows Node is ready. Run your hub bootstrap on the Mac!"

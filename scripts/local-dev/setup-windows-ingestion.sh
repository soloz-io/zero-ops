#!/usr/bin/env bash
# scripts/local-dev/setup-windows-ingestion.sh
# RUN THIS ON YOUR WINDOWS WSL2 UBUNTU TERMINAL
# Establishes the SSHFS bridge to the Mac using the shared /mnt/wsl tmpfs

set -euo pipefail

MAC_USER="arun_subramanian"
MAC_IP="192.168.1.2"

echo "🌉 Setting up Synchronous SSHFS Locality Bridge on Windows..."

if ! command -v sshfs >/dev/null 2>&1; then
    echo "Installing sshfs..."
    sudo apt-get update && sudo apt-get install -y sshfs
fi

# We use /mnt/wsl because Windows 10/11 shares this directory across ALL
# running WSL2 distributions, including the hidden Docker Desktop VM!
sudo mkdir -p /mnt/wsl/zero-ops
sudo chown -R "$USER:$USER" /mnt/wsl/zero-ops

if ! mountpoint -q /mnt/wsl/zero-ops; then
    echo "Mounting Mac's /mnt/wsl/zero-ops securely over SSHFS..."
    sudo sshfs -o allow_other,default_permissions,IdentityFile=~/.ssh/id_ed25519 "${MAC_USER}@${MAC_IP}:/mnt/wsl/zero-ops" /mnt/wsl/zero-ops
    echo "✅ SSHFS bridge successfully established."
else
    echo "✅ SSHFS bridge is already active."
fi

echo "🚀 Windows is ready! Now run bootstrap-distributed.sh on your Mac!"
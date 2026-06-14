#!/usr/bin/env bash

# scripts/local-dev/setup-remote-context.sh
# This script configures a Windows WSL2 terminal to securely route Docker commands to the Mac.

set -euo pipefail

MAC_USER="arun_subramanian"
MAC_IP="192.168.1.2"
CONTEXT_NAME="mac-compute-pool"

echo "🔑 Step 1: Ensuring SSH keys exist..."
if [ ! -f ~/.ssh/id_ed25519 ]; then
    ssh-keygen -t ed25519 -N "" -f ~/.ssh/id_ed25519
    echo "✅ SSH key generated."
else
    echo "✅ SSH key already exists."
fi

echo "🛡️ Step 2: Copying SSH key to Mac (You will be prompted for your Mac password)..."
# We use ssh-copy-id to enable passwordless login
ssh-copy-id "${MAC_USER}@${MAC_IP}"

echo "🐳 Step 3: Configuring Docker Remote Context..."
if docker context ls --format '{{.Name}}' | grep -q "^${CONTEXT_NAME}$"; then
    echo "Context '${CONTEXT_NAME}' already exists. Updating..."
    docker context update "${CONTEXT_NAME}" --docker "host=ssh://${MAC_USER}@${MAC_IP}"
else
    docker context create "${CONTEXT_NAME}" --docker "host=ssh://${MAC_USER}@${MAC_IP}"
fi

echo "🔄 Step 4: Activating the Remote Context..."
docker context use "${CONTEXT_NAME}"

echo "✅ Success! Your Windows terminal is now securely paired to the Mac's Docker Engine."
echo "You can verify this by running: docker info"

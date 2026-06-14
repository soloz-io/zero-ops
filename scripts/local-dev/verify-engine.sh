#!/usr/bin/env bash

# scripts/local-dev/verify-engine.sh
set -euo pipefail

MAC_IP="192.168.1.5" # Note: Update this to match your actual Mac IP

echo "🔍 Running pre-flight port verification checks..."
if ! nc -z -w3 "${MAC_IP}" 22; then
    echo "❌ Error: Cannot reach Mac on SSH port 22. Check Mac Remote Login settings."
    exit 1
fi

echo "📡 Checking active Docker context mapping..."
CURRENT_OS=$(docker info --format '{{.Operating System}}')
if [[ "$CURRENT_OS" == *"WSL"* ]] || [[ "$CURRENT_OS" == *"Windows"* ]]; then
    echo "❌ Error: Docker is still pointing to local Windows storage."
    echo "💡 Please run: docker context use mac-compute-pool"
    exit 1
else
    echo "✅ Success! Windows terminal is safely paired with Mac's 32GB engine."
    echo "🚀 Operating System Verified: ${CURRENT_OS}"
fi

#!/usr/bin/env bash
# scripts/local-dev/export-mac-kubeconfig.sh
set -euo pipefail

# 1. Setup variables
TARGET_CONTEXT="kind-zero-ops-mac-engine"  # Adjust to match your Mac's cluster context
OUTPUT_FILE="${HOME}/.kube/mac-distributed-spec.yaml"
WINDOWS_USER="Dell"                     # The user on the Windows laptop we SSH'd into earlier
WINDOWS_IP="192.168.43.147"             # The newly assigned IP of the Windows laptop

# 2. Extract your physical Mac LAN IP for the remote network route
MAC_IP=$(ipconfig getifaddr en0 || ipconfig getifaddr en1)

echo "📡 Extracting context [${TARGET_CONTEXT}] with host route IP: ${MAC_IP}..."
# 3. Export clean kubeconfig view
kubectl config view --context="${TARGET_CONTEXT}" --flatten --minify > "${OUTPUT_FILE}"

# 4. Patch loopback binding to use the physical network IP address
# This ensures the Windows Hub routes traffic directly to the Mac network interface
if [[ "$OSTYPE" == "darwin"* ]]; then
  sed -i '' "s/127.0.0.1/${MAC_IP}/g" "${OUTPUT_FILE}"
  sed -i '' "s/localhost/${MAC_IP}/g" "${OUTPUT_FILE}"
else
  sed -i "s/127.0.0.1/${MAC_IP}/g" "${OUTPUT_FILE}"
  sed -i "s/localhost/${MAC_IP}/g" "${OUTPUT_FILE}"
fi

echo "🔐 Securing and transferring config payload to Windows Hub..."
# 5. Push file directly into Windows host system via secure channel
scp -o StrictHostKeyChecking=accept-new "${OUTPUT_FILE}" "${WINDOWS_USER}@${WINDOWS_IP}:~/.kube/mac-target-engine.yaml"

echo "✅ Handoff Complete! File copied to Windows host."

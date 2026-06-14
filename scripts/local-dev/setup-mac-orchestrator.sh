#!/usr/bin/env bash
# scripts/local-dev/setup-mac-orchestrator.sh
# Runs on the Mac to set up the Windows kind cluster as the compute engine,
# and pulls its kubeconfig for Crossplane to ingest.

set -euo pipefail

MAC_USER="arun_subramanian"
MAC_IP="192.168.1.2"
WINDOWS_USER="Dell"
WINDOWS_IP="192.168.1.18"

echo "🎯 Orchestrating Windows Node Engine Setup from Mac..."

# 1. Create a Kind cluster on Windows to host the CAPD controller natively
echo "Creating Windows compute engine..."
ssh "${WINDOWS_USER}@${WINDOWS_IP}" <<EOF
    kind create cluster --name windows-engine --kubeconfig ~/.kube/windows-engine-config 2>/dev/null || true
    # We must patch the kubeconfig to use the external Windows IP so the Mac can reach it
    sed -i 's/0.0.0.0/${WINDOWS_IP}/g' ~/.kube/windows-engine-config
    sed -i 's/127.0.0.1/${WINDOWS_IP}/g' ~/.kube/windows-engine-config
EOF

# 2. Ingest the kubeconfig into the Mac
echo "Ingesting Windows kubeconfig into Mac..."
scp "${WINDOWS_USER}@${WINDOWS_IP}:~/.kube/windows-engine-config" "$HOME/.kube/windows-target-engine.yaml"

echo "✅ Windows Engine Config ingested successfully!"
echo "You can now run 'hub bootstrap' on the Mac. Crossplane will automatically route to the Windows engine."

#!/bin/bash

# Zero-Ops: Enable Windows Ingestion Engine Routing
# This script dynamically patches the local Crossplane compositions to route 
# all local Spoke workloads to the Windows Ingestion Engine instead of the Mac Hub.
#
# Use this when you want to utilize the Asymmetric Split architecture (multi-machine).
# To revert to single-machine mode, simply run: git restore manifests/providers/local/

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$(dirname "$SCRIPT_DIR")")"

echo "🌉 Patching local Crossplane Compositions for Windows Ingestion Engine..."

COMPOSITION_FILE="${PROJECT_ROOT}/manifests/providers/local/k8s/spokepool-capd-composition.yaml"

PROVIDER_FILE="${PROJECT_ROOT}/manifests/providers/local/crossplane/provider-config-windows.yaml"

if [[ ! -f "$COMPOSITION_FILE" ]] || [[ ! -f "$PROVIDER_FILE" ]]; then
    echo "❌ Error: Could not find required manifests"
    exit 1
fi

# Patch all ProviderConfig references in the Composition to use the Windows engine
sed -i '' 's/kubernetes-provider/windows-cluster-engine/g' "$COMPOSITION_FILE"
# Ensure the ProviderConfig object itself is named windows-cluster-engine
sed -i '' 's/name: kubernetes-provider/name: windows-cluster-engine/g' "$PROVIDER_FILE"

echo "✅ Successfully patched $COMPOSITION_FILE"
echo "✅ Successfully patched $PROVIDER_FILE"
echo ""
echo "🚀 Injecting Windows Ingestion Engine credentials into your Hub natively..."
WINDOWS_KUBECONFIG="$HOME/.kube/windows-target-engine.yaml"
if [[ -f "$WINDOWS_KUBECONFIG" ]]; then
    # Start the Mac Host bridge proxy if it's not already running
    if ! pgrep -f "mac-windows-proxy.py" > /dev/null; then
        echo "🌉 Starting Docker-to-LAN bridge proxy on Mac host (port 6445)..."
        nohup python3 "${SCRIPT_DIR}/mac-windows-proxy.py" 6445 192.168.1.18 6444 > /tmp/mac-windows-proxy.log 2>&1 &
    fi

    # Patch the kubeconfig to use the Mac Host bridge instead of direct LAN (bypasses Docker for Mac LAN routing bug)
    sed -i '' 's/192.168.1.18:6444/host.docker.internal:6445/g' "$WINDOWS_KUBECONFIG"
    # Skip TLS verification since the hostname host.docker.internal is not in the Windows API server's SAN list
    sed -i '' 's/certificate-authority-data:.*/insecure-skip-tls-verify: true/g' "$WINDOWS_KUBECONFIG"

    kubectl create namespace zero-ops-system --dry-run=client -o yaml | kubectl apply -f - 2>/dev/null || true
    kubectl create secret generic windows-engine-credentials --namespace=zero-ops-system --from-file=kubeconfig="$WINDOWS_KUBECONFIG" --dry-run=client -o yaml | kubectl apply -f - 2>/dev/null || true
    kubectl apply -f "$PROVIDER_FILE" 2>/dev/null || true
    echo "✅ Windows Engine Credentials injected successfully using Bridge Proxy!"
else
    echo "⚠️  Warning: $WINDOWS_KUBECONFIG not found. Please run setup-windows-ingestion.ps1 on Windows and bootstrap-distributed.sh on Mac to fetch the credentials."
fi

echo ""
echo "⚠️  IMPORTANT: Since ArgoCD pulls from your Git repository, you must commit and push"
echo "these changes for the routing to take effect on the Hub:"
echo ""
echo "    git add manifests/providers/local/k8s/spokepool-capd-composition.yaml"
echo "    git add manifests/providers/local/crossplane/provider-config-windows.yaml"
echo "    git commit -m \"chore: enable windows ingestion engine routing\""
echo "    git push"
echo ""
echo "After ArgoCD syncs, if you already have Spoke nodes running on the Mac, delete them:"
echo "    kubectl delete cluster local-dev -n platform-capi"
echo "Crossplane will instantly recreate them natively on Windows!"

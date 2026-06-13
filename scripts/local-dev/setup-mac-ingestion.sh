#!/usr/bin/env bash
# scripts/local-dev/setup-mac-ingestion.sh
set -euo pipefail

CLUSTER_NAME="zero-ops-mac-engine"
MAC_IP=$(ipconfig getifaddr en0 || ipconfig getifaddr en1)

echo "🍏 Creating lightweight Mac ingestion cluster via Kind..."
# 1. Generate Kind configuration exposing the API server over the physical LAN IP
cat <<EOF > kind-mac-config.yaml
apiVersion: kind.x-k8s.io/v1alpha4
kind: Cluster
networking:
  apiServerAddress: "0.0.0.0"
  apiServerPort: 6444
nodes:
- role: control-plane
  kubeadmConfigPatches:
  - |
    kind: ClusterConfiguration
    apiServer:
      certSANs:
      - "${MAC_IP}"
      - "localhost"
      - "127.0.0.1"
EOF

kind create cluster --name "${CLUSTER_NAME}" --config kind-mac-config.yaml
rm kind-mac-config.yaml

echo "🚀 Initializing bare Cluster API and CAPD controllers on Mac..."
# 2. Use standard clusterctl to prime the Mac with CAPD structures
# This enables the Mac to interpret incoming CAPI resources from Crossplane
export EXP_CLUSTER_RESOURCE_SET=true
clusterctl init --infrastructure docker

echo "✅ Mac engine is ready to receive remote Crossplane payloads!"

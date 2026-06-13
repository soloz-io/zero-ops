#!/usr/bin/env bash
# scripts/local-dev/setup-windows-ingestion.sh
set -euo pipefail

CLUSTER_NAME="zero-ops-windows-engine"

# Extract the IP from the docker context
HOST_URL=$(docker context inspect windows-box | jq -r '.[0].Endpoints.docker.Host')
WINDOWS_IP=$(echo "$HOST_URL" | grep -oE '[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+' || echo "192.168.43.147")

echo "💻 Creating lightweight Windows ingestion cluster via Kind over SSH..."

cat <<EOF > kind-windows-config.yaml
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
      - "${WINDOWS_IP}"
      - "localhost"
      - "127.0.0.1"
EOF

# Ensure we are operating over the remote context
export DOCKER_CONTEXT="windows-box"

# Create cluster remotely
kind create cluster --name "${CLUSTER_NAME}" --config kind-windows-config.yaml
rm kind-windows-config.yaml

echo "🔧 Extracting and patching remote credentials locally..."
# The kubeconfig is generated locally by kind, but might point to 127.0.0.1 (Windows loopback)
# We rewrite it to point to the physical LAN IP of the Windows machine
kind get kubeconfig --name "${CLUSTER_NAME}" > ~/.kube/windows-target-engine.yaml
sed -i '' "s/0.0.0.0/${WINDOWS_IP}/g" ~/.kube/windows-target-engine.yaml
sed -i '' "s/127.0.0.1/${WINDOWS_IP}/g" ~/.kube/windows-target-engine.yaml
sed -i '' "s/localhost/${WINDOWS_IP}/g" ~/.kube/windows-target-engine.yaml

echo "🚀 Initializing bare Cluster API and CAPD controllers on Windows..."
export KUBECONFIG=~/.kube/windows-target-engine.yaml
export EXP_CLUSTER_RESOURCE_SET=true
clusterctl init --infrastructure docker

echo "✅ Windows engine is ready to receive remote Crossplane payloads from the Mac Hub!"

#!/bin/bash

# Zero-Ops Distributed Local Bootstrap (Synchronous Hybrid Split via SSHFS)
# This script orchestrates the full setup across two machines natively from the Mac terminal.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$(dirname "$SCRIPT_DIR")")"
WINDOWS_USER="Dell"
WINDOWS_IP="192.168.1.18"

# Colors for output
GREEN='\032[0;32m'
BLUE='\032[0;34m'
YELLOW='\032[1;33m'
NC='\032[0m' # No Color

log() {
    echo -e "${BLUE}[Distributed-Bootstrap]${NC} $1"
}

warn() {
    echo -e "${YELLOW}[Distributed-Bootstrap] WARNING:${NC} $1"
}

log "Starting Synchronous Hybrid Split Bootstrap (Mac = Hub, Windows = Spoke)..."

# Phase 1: Ingest Windows credentials
log "Phase 1: Ingesting Windows Ingestion Engine credentials..."
# The user ran setup-windows-ingestion.ps1 on Windows natively.
# We pull the kubeconfig securely over SSH.
scp "${WINDOWS_USER}@${WINDOWS_IP}:.kube/config" ~/.kube/windows-target-engine.yaml

# Patch the IP from 127.0.0.1 to the actual Windows LAN IP
sed -i '' "s/0.0.0.0/${WINDOWS_IP}/g" ~/.kube/windows-target-engine.yaml
sed -i '' "s/127.0.0.1/${WINDOWS_IP}/g" ~/.kube/windows-target-engine.yaml
sed -i '' "s/localhost/${WINDOWS_IP}/g" ~/.kube/windows-target-engine.yaml

# Phase 2: Bootstrap Mac Hub
log "Phase 2: Bootstrapping Hub locally on Mac..."
# Run the standard hub-bootstrap script on the Mac's native docker daemon
bash "${PROJECT_ROOT}/scripts/hub-bootstrap.sh" --provider local --topology multi

log "Phase 3: Deploying Spoke Clusters to Windows..."
kubectl apply -f "${PROJECT_ROOT}/manifests/spoke/spoke-pools/dev/local/multi/local-dev.yaml"

log "${GREEN}Distributed Local Setup Complete!${NC}"
log "Hub is running on your Mac. Spoke workloads will provision seamlessly on Windows."
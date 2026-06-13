#!/bin/bash

# Zero-Ops Distributed Local Bootstrap (Asymmetric Split)
# This script orchestrates the full setup across two machines from the Mac terminal.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$(dirname "$SCRIPT_DIR")")"
MAC_ENGINE_NAME="zero-ops-mac-engine"

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

# Ensure prerequisites
if ! docker context ls | grep -q "windows-box"; then
    warn "Docker context 'windows-box' not found. Please create it first:"
    warn "docker context create windows-box --docker \"host=ssh://<user>@<windows_ip>\""
    exit 1
fi

log "Starting Asymmetric Split Bootstrap..."

# Phase 1: Clean and build the Mac Ingestion Layer locally
log "Phase 1: Initializing Mac Ingestion Layer..."
if kind get clusters | grep -q "^${MAC_ENGINE_NAME}$"; then
    log "Cleaning up existing Mac engine cluster..."
    kind delete cluster --name "${MAC_ENGINE_NAME}"
fi
bash "${SCRIPT_DIR}/setup-mac-ingestion.sh"

# Phase 2: Export Credentials
log "Phase 2: Extracting Mac Engine Credentials..."
bash "${SCRIPT_DIR}/export-mac-kubeconfig.sh"
# Ensure the exported credentials are also available in the default location so hub-bootstrap.sh finds them
mkdir -p ~/.kube
cp "${SCRIPT_DIR}/mac-target-engine.yaml" ~/.kube/mac-target-engine.yaml
log "Mac credentials staged for Crossplane injection."

# Phase 3: Bootstrap Windows Hub
log "Phase 3: Bootstrapping Hub on Windows Machine (via windows-box context)..."
# We wrap this in a subshell so we don't permanently alter the user's terminal environment
(
    export DOCKER_CONTEXT="windows-box"
    log "Switched to Docker context: $DOCKER_CONTEXT"
    
    # Run the standard hub-bootstrap script
    # Our injected hook inside hub-bootstrap.sh will automatically find ~/.kube/mac-target-engine.yaml
    # and configure Crossplane before deploying the local-dev SpokePool!
    bash "${PROJECT_ROOT}/scripts/hub-bootstrap.sh" --provider docker
)

log "${GREEN}Distributed Local Setup Complete!${NC}"
log "Hub is running on Windows. Spoke workloads will provision on this Mac."

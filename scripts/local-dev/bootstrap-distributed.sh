#!/bin/bash

# Zero-Ops Distributed Local Bootstrap (Asymmetric Split - Mac Brain, Windows Muscle)
# This script orchestrates the full setup across two machines natively from the Mac terminal.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$(dirname "$SCRIPT_DIR")")"
WINDOWS_ENGINE_NAME="zero-ops-windows-engine"

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

log "Starting Asymmetric Split Bootstrap (Mac = Hub, Windows = Spoke)..."

# Phase 1: Clean and build the Windows Ingestion Layer remotely
log "Phase 1: Initializing Windows Ingestion Layer via SSH context..."
if DOCKER_CONTEXT="windows-box" kind get clusters | grep -q "^${WINDOWS_ENGINE_NAME}$"; then
    log "Cleaning up existing Windows engine cluster..."
    DOCKER_CONTEXT="windows-box" kind delete cluster --name "${WINDOWS_ENGINE_NAME}"
fi
bash "${SCRIPT_DIR}/setup-windows-ingestion.sh"

log "Windows Engine credentials successfully generated and staged."

# Phase 2: Bootstrap Mac Hub
log "Phase 2: Bootstrapping Hub locally on Mac..."
# Run the standard hub-bootstrap script on the Mac's native docker daemon
# Our injected hook inside hub-bootstrap.sh will automatically find ~/.kube/windows-target-engine.yaml
# and configure Crossplane before deploying the local-dev SpokePool!

bash "${PROJECT_ROOT}/scripts/hub-bootstrap.sh" --provider docker

log "${GREEN}Distributed Local Setup Complete!${NC}"
log "Hub is running on your Mac. Spoke workloads will provision seamlessly on Windows."

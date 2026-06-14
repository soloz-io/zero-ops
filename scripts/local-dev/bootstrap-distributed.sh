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

# Phase 1: Establish SSHFS bridge on Windows remotely
log "Phase 1: Initializing Windows SSHFS Bridge via SSH..."
# Copy the script to the Windows native temp directory
scp "${SCRIPT_DIR}/setup-windows-ingestion.sh" "${WINDOWS_USER}@${WINDOWS_IP}:C:\\Windows\\Temp\\setup-windows-ingestion.sh"
# Execute it inside WSL2 using the /mnt/c path translation
ssh "${WINDOWS_USER}@${WINDOWS_IP}" "wsl bash /mnt/c/Windows/Temp/setup-windows-ingestion.sh"

# Phase 2: Bootstrap Mac Hub
log "Phase 2: Bootstrapping Hub locally on Mac..."
# Run the standard hub-bootstrap script on the Mac's native docker daemon
# Ensure DOCKER_HOST is set to route Spoke provisioning to Windows Docker Engine
export DOCKER_HOST="ssh://${WINDOWS_USER}@${WINDOWS_IP}"

bash "${PROJECT_ROOT}/scripts/hub-bootstrap.sh" --provider docker

log "Phase 3: Deploying Spoke Clusters to Windows..."
kubectl apply -f "${PROJECT_ROOT}/manifests/spoke/spoke-pools/dev/local/local-dev.yaml"

log "${GREEN}Distributed Local Setup Complete!${NC}"
log "Hub is running on your Mac. Spoke workloads will provision seamlessly on Windows."
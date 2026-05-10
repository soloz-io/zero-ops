#!/bin/bash

# Zero-Ops Hub Bootstrap Script
# This script performs the hub-bootstrap steps one by one with proper intervals
# Based on zero-ops/README.md hub bootstrap process

set -euo pipefail

# Configuration
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
ZERO_OPS_DIR="$PROJECT_ROOT"
LOG_DIR="$ZERO_OPS_DIR/.zero-ops"
HUB_BINARY="$ZERO_OPS_DIR/bin/hub"

# Create log directory
mkdir -p "$LOG_DIR"

# Logging function
log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $1" | tee -a "$LOG_DIR/bootstrap.log"
}

# Error handling
error_exit() {
    log "ERROR: $1"
    exit 1
}

# Check prerequisites
check_prerequisites() {
    log "Checking prerequisites..."
    
    # Check if hub binary exists
    if [[ ! -f "$HUB_BINARY" ]]; then
        error_exit "Hub binary not found at $HUB_BINARY. Please run 'make build-hub' first."
    fi
    
    # Check required environment variables and files
    if [[ ! -f "$ZERO_OPS_DIR/k8-secrets/hetzner/token" ]]; then
        error_exit "Hetzner token file not found at k8-secrets/hetzner/token"
    fi
    
    if [[ ! -f "$ZERO_OPS_DIR/k8-secrets/github/github-pat-token" ]]; then
        error_exit "GitHub token file not found at k8-secrets/github/github-pat-token"
    fi
    
    # Check if AWS profile is configured
    if ! aws configure list --profile zerotouch-platform-admin >/dev/null 2>&1; then
        error_exit "AWS profile 'zerotouch-platform-admin' not configured. Please run 'aws configure --profile zerotouch-platform-admin'"
    fi
    
    log "Prerequisites check passed"
}

# Step 1: Bootstrap Hub Cluster
step1_bootstrap_hub() {
    log "Step 1: Bootstrapping Hub Cluster..."
    
    export HCLOUD_TOKEN=$(cat "$ZERO_OPS_DIR/k8-secrets/hetzner/token")
    
    log "Running: $HUB_BINARY bootstrap --name=hub --region=fsn1 --debug"
    "$HUB_BINARY" bootstrap \
        --name=hub \
        --region=fsn1 \
        --debug 2>&1 | tee "$LOG_DIR/bootstrap-hub.log"
    
    log "Hub cluster bootstrap completed"
}

# Step 2: Configure AWS Secrets Manager
step2_configure_aws_secrets() {
    log "Step 2: Configuring AWS Secrets Manager for disaster recovery..."
    
    export AWS_PROFILE=zerotouch-platform-admin
    
    log "Running: $HUB_BINARY configure-aws-secrets-manager --environment=development --aws-region=ap-south-1"
    "$HUB_BINARY" configure-aws-secrets-manager \
        --environment=development \
        --aws-region=ap-south-1 \
        --kubeconfig="$ZERO_OPS_DIR/k8-secrets/kubeconfig/hub.kubeconfig"
    
    log "AWS Secrets Manager configuration completed"
}

# Step 3: Configure GitHub Access
step3_configure_github() {
    log "Step 3: Configuring GitHub Access..."
    
    export GITHUB_TOKEN=$(cat "$ZERO_OPS_DIR/k8-secrets/github/github-pat-token")
    
    log "Running: $HUB_BINARY configure-github-access --ghcr-pat=\$GITHUB_TOKEN"
    "$HUB_BINARY" configure-github-access \
        --ghcr-pat="$GITHUB_TOKEN" \
        --kubeconfig="$ZERO_OPS_DIR/k8-secrets/kubeconfig/hub.kubeconfig"
    
    log "GitHub access configuration completed"
}

# Step 4: Wait for ArgoCD to sync and create namespaces
step4_wait_namespaces() {
    log "Step 4: Waiting for ArgoCD to sync and create namespaces (timeout: 300s)..."
    
    log "Running: kubectl wait --for=jsonpath='{.status.phase}'=Active namespace/platform-data --timeout=300s"
    kubectl wait --for=jsonpath='{.status.phase}'=Active namespace/platform-data \
        --timeout=300s \
        --kubeconfig="$ZERO_OPS_DIR/k8-secrets/kubeconfig/hub.kubeconfig"
    
    log "Namespaces created successfully"
}

# Step 5: Initialize bootstrap secrets
step5_init_secrets() {
    log "Step 5: Initializing bootstrap secrets..."
    
    # Start port-forward for Infisical in background
    log "Starting port-forward for Infisical..."
    kubectl port-forward -n platform-security svc/platform-infisical-infisical-standalone-infisical 8080:8080 \
        --kubeconfig="$ZERO_OPS_DIR/k8-secrets/kubeconfig/hub.kubeconfig" &
    PORT_FORWARD_PID=$!
    
    # Wait a bit for port-forward to be ready
    sleep 10
    
    # Check if port-forward is still running
    if ! kill -0 $PORT_FORWARD_PID 2>/dev/null; then
        error_exit "Port-forward failed to start"
    fi
    
    # Initialize secrets
    log "Running: INFISICAL_API_URL=http://localhost:8080 $HUB_BINARY init-secrets"
    INFISICAL_API_URL=http://localhost:8080 "$HUB_BINARY" init-secrets \
        --kubeconfig="$ZERO_OPS_DIR/k8-secrets/kubeconfig/hub.kubeconfig"
    
    # Clean up port-forward
    kill $PORT_FORWARD_PID 2>/dev/null || true
    
    log "Bootstrap secrets initialization completed"
}

# Step 6: Wait for Infisical to be ready
step6_wait_infisical() {
    log "Step 6: Waiting for Infisical to be ready..."
    
    # Wait for Infisical pods to be running
    log "Checking Infisical pods status..."
    local max_attempts=30
    local attempt=1
    
    while [[ $attempt -le $max_attempts ]]; do
        local pod_status
        pod_status=$(kubectl get pods -n platform-security \
            --kubeconfig="$ZERO_OPS_DIR/k8-secrets/kubeconfig/hub.kubeconfig" \
            -l app.kubernetes.io/instance=platform-infisical \
            -o jsonpath='{.items[0].status.phase}' 2>/dev/null || echo "NotFound")
        
        if [[ "$pod_status" == "Running" ]]; then
            log "Infisical is ready"
            break
        fi
        
        log "Waiting for Infisical to be ready (attempt $attempt/$max_attempts, status: $pod_status)"
        sleep 10
        ((attempt++))
    done
    
    if [[ $attempt -gt $max_attempts ]]; then
        error_exit "Infisical did not become ready within expected time"
    fi
    
    log "Infisical pods are running"
}

# Step 7-8: Create Machine Identity and configure ESO (automated in hub-operator)
step7_8_configure_eso() {
    log "Step 7-8: Machine Identity creation and ESO configuration (automated in hub-operator)..."
    log "These steps are automated in hub-operator. Skipping manual configuration."
}

# Step 9: Wait for database deployment
step9_wait_database() {
    log "Step 9: Waiting for database deployment (timeout: 600s)..."
    
    log "Running: kubectl wait --for=condition=ready pod -l cnpg.io/cluster=platform-db --timeout=600s"
    kubectl wait --for=condition=ready pod -l cnpg.io/cluster=platform-db \
        -n platform-data --timeout=600s \
        --kubeconfig="$ZERO_OPS_DIR/k8-secrets/kubeconfig/hub.kubeconfig"
    
    log "Database deployment completed"
}

# Main execution
main() {
    log "Starting Zero-Ops Hub Bootstrap Process"
    log "Project root: $ZERO_OPS_DIR"
    log "Log directory: $LOG_DIR"
    
    # Check prerequisites
    check_prerequisites
    
    # Execute steps in order
    step1_bootstrap_hub
    sleep 30  # Wait between steps
    
    step2_configure_aws_secrets
    sleep 15  # Wait between steps
    
    step3_configure_github
    sleep 15  # Wait between steps
    
    step4_wait_namespaces
    sleep 10  # Wait between steps
    
    step5_init_secrets
    sleep 15  # Wait between steps
    
    step6_wait_infisical
    sleep 10  # Wait between steps
    
    step7_8_configure_eso
    sleep 5   # Wait between steps
    
    step9_wait_database
    
    log "Zero-Ops Hub Bootstrap Process completed successfully!"
    log "You can now access your hub cluster using: kubectl --kubeconfig=k8-secrets/kubeconfig/hub.kubeconfig"
}

# Handle script interruption
trap 'log "Script interrupted"; exit 1' INT TERM

# Run main function
main "$@"
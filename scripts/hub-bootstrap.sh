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
BOOTSTRAP_STATE_FILE="$LOG_DIR/bootstrap-state.json"

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

# Generic function to check if a step is completed based on bootstrap state file
is_step_completed() {
    local step="$1"
    
    if [[ ! -f "$BOOTSTRAP_STATE_FILE" ]]; then
        return 1
    fi
    
    if command -v jq >/dev/null 2>&1; then
        # Use any() to check if any step matches, returns true/false with proper exit codes
        jq -e --arg step "$step" '.completedSteps | any(. == $step)' "$BOOTSTRAP_STATE_FILE" >/dev/null
    else
        # Fallback: simple grep check
        grep -q "\"$step\"" "$BOOTSTRAP_STATE_FILE"
    fi
}

# Generic function to mark a step as completed in bootstrap state file
mark_step_completed() {
    local step="$1"
    
    mkdir -p "$(dirname "$BOOTSTRAP_STATE_FILE")"
    
    # Read existing completed steps or create new array
    local existing_steps=""
    if [[ -f "$BOOTSTRAP_STATE_FILE" ]]; then
        if command -v jq >/dev/null 2>&1; then
            existing_steps=$(jq -r '.completedSteps[]?' "$BOOTSTRAP_STATE_FILE" 2>/dev/null || echo "")
        else
            # Fallback: parse simple JSON array
            existing_steps=$(grep '"completedSteps":' "$BOOTSTRAP_STATE_FILE" | sed 's/.*"completedSteps": \[\(.*\)\]/\1/' | sed 's/[","]//g' | tr ',' '\n')
        fi
    fi
    
    # Remove quotes and convert to array
    local steps_array=()
    if [[ -n "$existing_steps" ]]; then
        for existing_step in $existing_steps; do
            if [[ -n "$existing_step" ]]; then
                steps_array+=("$existing_step")
            fi
        done
    fi
    
    # Add new step if not already present
    local step_already_exists=false
    if [[ ${#steps_array[@]} -gt 0 ]]; then
        for existing_step in "${steps_array[@]}"; do
            if [[ "$existing_step" == "$step" ]]; then
                step_already_exists=true
                break
            fi
        done
    fi
    
    if [[ "$step_already_exists" == "false" ]]; then
        steps_array+=("$step")
    fi
    
    # Create JSON array string
    local completed_steps_json="["
    for i in "${!steps_array[@]}"; do
        if [[ $i -gt 0 ]]; then
            completed_steps_json+=","
        fi
        completed_steps_json+="\"${steps_array[$i]}\""
    done
    completed_steps_json+="]"
    
    # Create or update bootstrap state file
    echo "{\"completedSteps\": $completed_steps_json}" > "$BOOTSTRAP_STATE_FILE.tmp"
    mv "$BOOTSTRAP_STATE_FILE.tmp" "$BOOTSTRAP_STATE_FILE"
    
    log "Step '$step' marked as completed in bootstrap state file"
}

# Function to delete all access keys for an IAM user
delete_iam_access_keys() {
    local iam_user="$1"
    
    log "Deleting all access keys for $iam_user"
    
    # Debug: Show what we're getting from AWS
    local all_keys_debug=$(aws iam list-access-keys --user-name "$iam_user" --output json 2>/dev/null || echo "")
    log "Debug: Raw AWS response: $all_keys_debug"
    
    # Get all access keys (both active and inactive)
    local all_keys=$(aws iam list-access-keys --user-name "$iam_user" --query 'AccessKeyMetadata[?Status==`Active`].AccessKeyId' --output text 2>/dev/null || echo "")
    log "Debug: Active keys found: '$all_keys'"
    
    if [[ -z "$all_keys" ]] || [[ "$all_keys" == "None" ]]; then
        # Try getting all keys regardless of status
        all_keys=$(aws iam list-access-keys --user-name "$iam_user" --query 'AccessKeyMetadata[].AccessKeyId' --output text 2>/dev/null || echo "")
        log "Debug: All keys found: '$all_keys'"
    fi
    
    if [[ -n "$all_keys" ]] && [[ "$all_keys" != "None" ]]; then
        for key_id in $all_keys; do
            if [[ -n "$key_id" ]] && [[ "$key_id" != "None" ]] && [[ ${#key_id} -ge 16 ]]; then
                log "Deleting access key: $key_id"
                aws iam delete-access-key --user-name "$iam_user" --access-key-id "$key_id" 2>/dev/null || true
            fi
        done
        log "All existing access keys deleted"
    else
        log "No access keys found to delete"
    fi
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
    
    # Check for jq
    if ! command -v jq >/dev/null 2>&1; then
        log "Warning: jq not found. State management will be limited."
    fi
    
    log "Prerequisites check passed"
}

# Step 1: Bootstrap Hub Cluster
step1_bootstrap_hub() {
    # First check if step is already completed
    if is_step_completed "bootstrap_hub"; then
        log "Step 1: Hub bootstrap already completed, skipping"
        return
    fi
    
    # Check if cluster already exists and is working
    local kubeconfig="$ZERO_OPS_DIR/k8-secrets/kubeconfig/hub.kubeconfig"
    if [[ -f "$kubeconfig" ]] && kubectl --kubeconfig="$kubeconfig" cluster-info >/dev/null 2>&1; then
        log "Step 1: Hub cluster already exists and is accessible, marking as completed"
        mark_step_completed "bootstrap_hub"
        return
    fi
    
    log "Step 1: Bootstrapping Hub Cluster..."
    
    export HCLOUD_TOKEN=$(cat "$ZERO_OPS_DIR/k8-secrets/hetzner/token")
    
    log "Running: $HUB_BINARY bootstrap --name=hub --region=fsn1 --debug"
    "$HUB_BINARY" bootstrap \
        --name=hub \
        --region=fsn1 \
        --debug 2>&1 | tee "$LOG_DIR/bootstrap-hub.log"
    
    mark_step_completed "bootstrap_hub"
    log "Hub cluster bootstrap completed"
}

# Step 2: Configure AWS Secrets Manager
step2_configure_aws_secrets() {
    if is_step_completed "configure_aws_secrets"; then
        log "Step 2: AWS Secrets Manager already configured, skipping"
        return
    fi
    
    log "Step 2: Configuring AWS Secrets Manager for disaster recovery..."
    
    export AWS_PROFILE=zerotouch-platform-admin
    
    # Check if IAM user already exists and has access keys
    local iam_user="hub-operator-secrets-manager-development"
    log "Checking for existing IAM user: $iam_user"
    
    if aws iam get-user --user-name "$iam_user" >/dev/null 2>&1; then
        log "IAM user $iam_user already exists"
        
        # Check for existing access keys
        local access_keys=$(aws iam list-access-keys --user-name "$iam_user" --query 'AccessKeys[?Status==`Active`].AccessKeyId' --output text 2>/dev/null || echo "")
        
        if [[ -n "$access_keys" ]]; then
            log "Found existing active access keys for $iam_user"
            
            # Try to use existing keys first
            log "Attempting to use existing access keys..."
            if "$HUB_BINARY" configure-aws-secrets-manager \
                --environment=development \
                --aws-region=ap-south-1 \
                --kubeconfig="$ZERO_OPS_DIR/k8-secrets/kubeconfig/hub.kubeconfig" 2>&1 | grep -q "already exists"; then
                log "Existing keys work, using them"
                mark_step_completed "configure_aws_secrets"
                log "AWS Secrets Manager configuration completed"
                return
            else
                log "Existing keys may be invalid, attempting to recreate..."
                delete_iam_access_keys "$iam_user"
                
                # Retry configuration after deleting keys
                log "Retrying AWS Secrets Manager configuration after deleting keys..."
                "$HUB_BINARY" configure-aws-secrets-manager \
                    --environment=development \
                    --aws-region=ap-south-1 \
                    --kubeconfig="$ZERO_OPS_DIR/k8-secrets/kubeconfig/hub.kubeconfig"
                
                mark_step_completed "configure_aws_secrets"
                log "AWS Secrets Manager configuration completed"
                return
            fi
        fi
    fi
    
    # If no existing keys or user doesn't exist, proceed normally
    log "Running: $HUB_BINARY configure-aws-secrets-manager --environment=development --aws-region=ap-south-1"
    "$HUB_BINARY" configure-aws-secrets-manager \
        --environment=development \
        --aws-region=ap-south-1 \
        --kubeconfig="$ZERO_OPS_DIR/k8-secrets/kubeconfig/hub.kubeconfig"
    
    mark_step_completed "configure_aws_secrets"
    log "AWS Secrets Manager configuration completed"
}

# Step 3: Configure GitHub Access
step3_configure_github() {
    if is_step_completed "configure_github"; then
        log "Step 3: GitHub access already configured, skipping"
        return
    fi
    
    log "Step 3: Configuring GitHub Access..."
    
    export GITHUB_TOKEN=$(cat "$ZERO_OPS_DIR/k8-secrets/github/github-pat-token")
    
    log "Running: $HUB_BINARY configure-github-access --ghcr-pat=\$GITHUB_TOKEN"
    "$HUB_BINARY" configure-github-access \
        --ghcr-pat="$GITHUB_TOKEN" \
        --kubeconfig="$ZERO_OPS_DIR/k8-secrets/kubeconfig/hub.kubeconfig"
    
    mark_step_completed "configure_github"
    log "GitHub access configuration completed"
}

# Step 4: Wait for ArgoCD to sync and create namespaces
step4_wait_namespaces() {
    if is_step_completed "wait_namespaces"; then
        log "Step 4: Namespaces already created, skipping"
        return
    fi
    
    log "Step 4: Waiting for ArgoCD to sync and create namespaces (timeout: 300s)..."
    
    # Correct polling logic for a resource that doesn't exist yet
    # kubectl wait fails if namespace doesn't exist, so we must poll first
    timeout=300
    elapsed=0
    while ! kubectl get namespace platform-data --kubeconfig="$ZERO_OPS_DIR/k8-secrets/kubeconfig/hub.kubeconfig" >/dev/null 2>&1; do
        if [ $elapsed -ge $timeout ]; then
            log "❌ Timeout waiting for platform-data namespace to be created by ArgoCD"
            error_exit "ArgoCD failed to create platform-data namespace within ${timeout}s"
        fi
        sleep 5
        elapsed=$((elapsed+5))
    done
    
    # Now wait for it to be active since we know it exists
    log "Running: kubectl wait --for=jsonpath='{.status.phase}'=Active namespace/platform-data --timeout=60s"
    kubectl wait --for=jsonpath='{.status.phase}'=Active namespace/platform-data \
        --timeout=60s \
        --kubeconfig="$ZERO_OPS_DIR/k8-secrets/kubeconfig/hub.kubeconfig"
    
    mark_step_completed "wait_namespaces"
    log "✅ platform-data namespace created by ArgoCD"
}

# Step 5: Initialize bootstrap secrets
step5_init_secrets() {
    if is_step_completed "init_secrets"; then
        log "Step 5: Bootstrap secrets already initialized, skipping"
        return
    fi
    
    log "Step 5: Initializing bootstrap secrets..."
    
    # Initialize secrets first (this will create the infisical-secrets secret)
    log "Running: $HUB_BINARY init-secrets"
    "$HUB_BINARY" init-secrets \
        --kubeconfig="$ZERO_OPS_DIR/k8-secrets/kubeconfig/hub.kubeconfig"
    
    # Wait for Infisical pod to be ready after secrets are created
    log "Waiting for Infisical pod to be ready after secrets initialization..."
    local max_attempts=180  # 30 minutes (180 × 10 seconds)
    local attempt=1
    
    while [[ $attempt -le $max_attempts ]]; do
        local pod_ready
        pod_ready=$(kubectl get pods -n platform-security \
            --kubeconfig="$ZERO_OPS_DIR/k8-secrets/kubeconfig/hub.kubeconfig" \
            -l app=infisical-standalone -o jsonpath='{.items[0].status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || echo "NotFound")
        
        if [[ "$pod_ready" == "True" ]]; then
            log "Infisical pod is ready"
            break
        fi
        
        log "Waiting for Infisical pod to be ready (attempt $attempt/$max_attempts, ready: $pod_ready)"
        sleep 10
        ((attempt++))
    done
    
    if [[ $attempt -gt $max_attempts ]]; then
        error_exit "Infisical pod did not become ready within expected time"
    fi
    
    # Now start port-forward for any additional operations if needed
    log "Starting port-forward for Infisical..."
    kubectl port-forward -n platform-security svc/platform-infisical-infisical-standalone-infisical 8080:8080 \
        --kubeconfig="$ZERO_OPS_DIR/k8-secrets/kubeconfig/hub.kubeconfig" &
    PORT_FORWARD_PID=$!
    
    # Wait a bit for port-forward to be ready
    sleep 5
    
    # Clean up port-forward (not needed for init-secrets but good practice)
    kill $PORT_FORWARD_PID 2>/dev/null || true
    
    mark_step_completed "init_secrets"
    log "Bootstrap secrets initialization completed"
}

# Step 6: Wait for Infisical to be ready
step6_wait_infisical() {
    if is_step_completed "wait_infisical"; then
        log "Step 6: Infisical already ready, skipping"
        return
    fi
    
    log "Step 6: Waiting for Infisical to be ready..."
    
    # Wait for Infisical pods to be ready (using same logic as Step 5)
    log "Checking Infisical pods status..."
    local max_attempts=180  # 30 minutes to match Step 5
    local attempt=1
    
    while [[ $attempt -le $max_attempts ]]; do
        local pod_ready
        pod_ready=$(kubectl get pods -n platform-security \
            --kubeconfig="$ZERO_OPS_DIR/k8-secrets/kubeconfig/hub.kubeconfig" \
            -l app=infisical-standalone \
            -o jsonpath='{.items[0].status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || echo "NotFound")
        
        if [[ "$pod_ready" == "True" ]]; then
            log "Infisical is ready"
            break
        fi
        
        log "Waiting for Infisical to be ready (attempt $attempt/$max_attempts, ready: $pod_ready)"
        sleep 10
        ((attempt++))
    done
    
    if [[ $attempt -gt $max_attempts ]]; then
        error_exit "Infisical did not become ready within expected time"
    fi
    
    mark_step_completed "wait_infisical"
    log "Infisical pods are running"
}

# Step 7-8: Create Machine Identity and configure ESO (automated in hub-operator)
step7_8_configure_eso() {
    if is_step_completed "configure_eso"; then
        log "Step 7-8: ESO configuration already completed, skipping"
        return
    fi
    
    log "Step 7-8: Machine Identity creation and ESO configuration (automated in hub-operator)..."
    log "These steps are automated in hub-operator. Skipping manual configuration."
    
    mark_step_completed "configure_eso"
}

# Step 9: Wait for database deployment
step9_wait_database() {
    if is_step_completed "wait_database"; then
        log "Step 9: Database already ready, skipping"
        return
    fi
    
    log "Step 9: Waiting for database deployment (timeout: 600s)..."
    
    log "Running: kubectl wait --for=condition=ready pod -l cnpg.io/cluster=platform-db --timeout=600s"
    kubectl wait --for=condition=ready pod -l cnpg.io/cluster=platform-db \
        -n platform-data --timeout=600s \
        --kubeconfig="$ZERO_OPS_DIR/k8-secrets/kubeconfig/hub.kubeconfig"
    
    mark_step_completed "wait_database"
    log "Database deployment completed"
}

# Main execution
main() {
    log "Starting Zero-Ops Hub Bootstrap Process"
    log "Project root: $ZERO_OPS_DIR"
    log "Log directory: $LOG_DIR"
    
    # Check prerequisites
    check_prerequisites
    
    # Execute steps in order (each step is now independently idempotent)
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
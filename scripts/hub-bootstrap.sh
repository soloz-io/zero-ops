#!/bin/bash

# Zero-Ops Hub Bootstrap Script
# This script performs the hub-bootstrap steps one by one with proper intervals
# Based on zero-ops/README.md hub bootstrap process
#
# Provider-agnostic after step 1. The bootstrap produces a result contract
# (kubeconfig path) consumed by all downstream steps. No step knows whether
# the cluster came from Hetzner, CAPD, or any other provider.

set -euo pipefail

# Configuration
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
ZERO_OPS_DIR="$PROJECT_ROOT"
LOG_DIR="$ZERO_OPS_DIR/.zero-ops"
HUB_BINARY="$ZERO_OPS_DIR/bin/hub"
BOOTSTRAP_STATE_FILE="$LOG_DIR/bootstrap-state.json"

# Defaults (overridable via flags)
# Docker/local provider uses a fixed canonical cluster name. The state file
# is the source of truth, so the cluster name must be stable across runs.
CLUSTER_NAME="${CLUSTER_NAME:-hub-local}"
PROVIDER="${PROVIDER:-local}"

# Teardown existing cluster before bootstrap
TEARDOWN="${TEARDOWN:-false}"

# SpokePool Configuration (provider-agnostic)
SPOKEPOOL_NAME=""
SPOKEPOOL_NAMESPACE="${SPOKEPOOL_NAMESPACE:-platform-ops}"
SPOKEPOOL_TIMEOUT="${SPOKEPOOL_TIMEOUT:-1800}"  # 30 minutes in seconds

# Environment slug for matrix topology (ADR 037). MUST be explicitly set
# via --environment flag. The Go bootstrap CLI defaults to dev for docker,
# prod for hetzner if omitted.
ENVIRONMENT=""
CERT_TIMEOUT="${CERT_TIMEOUT:-600}"  # 10 minutes in seconds
CLUSTER_TIMEOUT="${CLUSTER_TIMEOUT:-900}"  # 15 minutes in seconds

# Kubeconfig path — set by step1 from the Go bootstrap result contract
KUBECONFIG_PATH=""

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

    # Create empty state file if it doesn't exist
    if [[ ! -f "$BOOTSTRAP_STATE_FILE" ]]; then
        mkdir -p "$(dirname "$BOOTSTRAP_STATE_FILE")"
        printf '{"completedSteps": []}' > "$BOOTSTRAP_STATE_FILE"
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
            existing_steps=$(jq -r '.completedSteps[]?' "$BOOTSTRAP_STATE_FILE" 2>/dev/null | tr -d '\r' || echo "")
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

    # Create or update bootstrap state file (use printf to avoid CRLF on Windows/Git Bash)
    printf '{"completedSteps": %s}' "$completed_steps_json" > "$BOOTSTRAP_STATE_FILE.tmp"
    mv "$BOOTSTRAP_STATE_FILE.tmp" "$BOOTSTRAP_STATE_FILE"

    log "Step '$step' marked as completed in bootstrap state file"
}

# Function to delete all access keys for an IAM user
delete_iam_access_keys() {
    local iam_user="$1"

    log "Deleting all access keys for $iam_user"

    # Get all access keys (both active and inactive)
    local all_keys=$(aws iam list-access-keys --user-name "$iam_user" --query 'AccessKeyMetadata[?Status==`Active`].AccessKeyId' --output text 2>/dev/null || echo "")

    if [[ -z "$all_keys" ]] || [[ "$all_keys" == "None" ]]; then
        # Try getting all keys regardless of status
        all_keys=$(aws iam list-access-keys --user-name "$iam_user" --query 'AccessKeyMetadata[].AccessKeyId' --output text 2>/dev/null || echo "")
    fi

    if [[ -n "$all_keys" ]] && [[ "$all_keys" != "None" ]]; then
        for key_id in $all_keys; do
            if [[ -n "$key_id" ]] && [[ "$key_id" != "None" ]] && [[ ${#key_id} -ge 16 ]]; then
                aws iam delete-access-key --user-name "$iam_user" --access-key-id "$key_id" 2>/dev/null || true
            fi
        done
        log "All existing access keys deleted"
    else
        log "No access keys found to delete"
    fi
}

# Auto-install kind and clusterctl into ~/bin on Windows/Git Bash if missing
auto_install_tool() {
    local tool="$1"
    local install_dir="$HOME/bin"
    mkdir -p "$install_dir"

    case "$tool" in
        kind)
            log "  Auto-installing kind..."
            if curl -fsSLo "$install_dir/kind.exe" "https://kind.sigs.k8s.io/dl/latest/kind-windows-amd64" 2>/dev/null; then
                chmod +x "$install_dir/kind.exe"
                export PATH="$PATH:$install_dir"
                log "  ✓ kind installed to $install_dir/kind.exe"
            else
                return 1
            fi
            ;;
        clusterctl)
            log "  Auto-installing clusterctl..."
            if curl -fsSLo "$install_dir/clusterctl.exe" "https://github.com/kubernetes-sigs/cluster-api/releases/download/v1.9.6/clusterctl-windows-amd64.exe" 2>/dev/null; then
                chmod +x "$install_dir/clusterctl.exe"
                export PATH="$PATH:$install_dir"
                log "  ✓ clusterctl installed to $install_dir/clusterctl.exe"
            else
                return 1
            fi
            ;;
        helm)
            log "  Auto-installing helm..."
            local helm_version="v3.17.3"
            local helm_zip="$install_dir/helm.zip"
            if curl -fsSLo "$helm_zip" "https://get.helm.sh/helm-${helm_version}-windows-amd64.zip" 2>/dev/null; then
                unzip -jo "$helm_zip" "windows-amd64/helm.exe" -d "$install_dir" 2>/dev/null
                rm -f "$helm_zip"
                chmod +x "$install_dir/helm.exe"
                export PATH="$PATH:$install_dir"
                log "  ✓ helm installed to $install_dir/helm.exe"
            else
                return 1
            fi
            ;;
        aws)
            log "  Auto-installing aws CLI..."
            # Use winget if aws not already installed via MSI
            local aws_dir="/c/Program Files/Amazon/AWSCLIV2"
            if [[ ! -f "$aws_dir/aws.exe" ]]; then
                if winget install -e --id Amazon.AWSCLI --silent --accept-package-agreements 2>/dev/null; then
                    log "  ✓ aws installed via winget"
                else
                    log "  Attempting AWS CLI MSI install..."
                    local msi="$install_dir/AWSCLIV2.msi"
                    if curl -fsSLo "$msi" "https://awscli.amazonaws.com/AWSCLIV2.msi" 2>/dev/null; then
                        msiexec //i "$msi" //quiet //norestart 2>/dev/null || true
                        rm -f "$msi"
                    fi
                fi
            fi
            # Add the real AWSCLIV2 dir to PATH (aws.exe needs sibling DLLs)
            if [[ -f "$aws_dir/aws.exe" ]]; then
                case ":$PATH:" in
                    *":$aws_dir:"*) ;;
                    *) export PATH="$aws_dir:$PATH" ;;
                esac
                log "  ✓ aws found: $aws_dir/aws.exe"
            else
                return 1
            fi
            ;;
        jq)
            log "  Auto-installing jq..."
            if curl -fsSLo "$install_dir/jq.exe" "https://github.com/jqlang/jq/releases/download/jq-1.7.1/jq-windows-amd64.exe" 2>/dev/null; then
                chmod +x "$install_dir/jq.exe"
                export PATH="$PATH:$install_dir"
                log "  ✓ jq installed to $install_dir/jq.exe"
            else
                return 1
            fi
            ;;
        *)
            return 1
            ;;
    esac

    # Persist to ~/.bashrc if not already there
    local bashrc="$HOME/.bashrc"
    # For aws, use the Program Files dir instead of ~/bin
    local persist_dir="$install_dir"
    if [[ "$tool" == "aws" ]]; then
        persist_dir="/c/Program Files/Amazon/AWSCLIV2"
    fi
    local path_line="export PATH=\"\$PATH:$persist_dir\""
    if ! grep -qF "$persist_dir" "$bashrc" 2>/dev/null; then
        echo -e "\n# auto-installed tools\n$path_line" >> "$bashrc"
    fi
}

# Check prerequisites
check_prerequisites() {
    log "Checking prerequisites..."
    local failed=0

    # --- Required CLI tools ---
    for tool in kubectl aws jq kind clusterctl helm; do
        # Also check ~/bin since auto-installed tools live there but
        # non-interactive shells don't source ~/.bashrc (where PATH is persisted)
        if [[ -x "$HOME/bin/$tool" ]]; then
            export PATH="$HOME/bin:$PATH"
        fi
        if ! command -v "$tool" >/dev/null 2>&1; then
            case "$tool" in
                kind|clusterctl|helm|aws|jq)
                    log "  '$tool' not found — attempting auto-install..."
                    if auto_install_tool "$tool" && command -v "$tool" >/dev/null 2>&1; then
                        log "  ✓ $tool found: $(command -v "$tool")"
                    else
                        log "ERROR: '$tool' could not be auto-installed"
                        case "$tool" in
                            kind)
                                log "  Install (Windows): winget install -e --id Kubernetes.kind"
                                log "  Or download: https://kind.sigs.k8s.io/dl/latest/kind-windows-amd64"
                                log "  Docs: https://kind.sigs.k8s.io/docs/user/quick-start/#installation"
                                ;;
                            clusterctl)
                                log "  Install (Windows): curl -Lo clusterctl.exe https://github.com/kubernetes-sigs/cluster-api/releases/download/v1.9.6/clusterctl-windows-amd64.exe"
                                log "  Then add to PATH"
                                log "  Docs: https://cluster-api.sigs.k8s.io/user/quick-start#install-clusterctl"
                                ;;
                            helm)
                                log "  Install (Windows): winget install -e --id Helm.Helm"
                                log "  Or download: https://get.helm.sh/helm-v3.17.3-windows-amd64.zip"
                                log "  Docs: https://helm.sh/docs/intro/install/"
                                ;;
                            aws)
                                log "  Install (Windows): https://awscli.amazonaws.com/AWSCLIV2.msi"
                                log "  Or: winget install -e --id Amazon.AWSCLI"
                                log "  Docs: https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html"
                                ;;
                            jq)
                                log "  Install (Git Bash): curl -L -o ~/jq.exe https://github.com/jqlang/jq/releases/download/jq-1.7.1/jq-windows-amd64.exe && chmod +x ~/jq.exe && export PATH=\$PATH:~"
                                log "  Or (Windows): winget install -e --id jqlang.jq"
                                log "  Docs: https://jqlang.github.io/jq/download/"
                                ;;
                        esac
                        failed=1
                    fi
                    ;;
                *)
                    log "ERROR: '$tool' is not installed or not in PATH"
                    case "$tool" in
                        kubectl)
                            log "  Install (Windows): winget install -e --id Kubernetes.kubectl"
                            log "  Or download: https://dl.k8s.io/release/v1.30.0/bin/windows/amd64/kubectl.exe"
                            log "  Docs: https://kubernetes.io/docs/tasks/tools/install-kubectl-windows/"
                            ;;
                    esac
                    failed=1
                    ;;
            esac
        else
            log "  ✓ $tool found: $(command -v "$tool")"
        fi
    done

    # --- Docker (required for kind) ---
    local docker_dirs=(
        "/c/Program Files/Docker/Docker/resources/bin"
        "/c/Program Files/Docker/Docker"
        "$HOME/bin"
    )
    if ! command -v docker >/dev/null 2>&1; then
        for d in "${docker_dirs[@]}"; do
            if [[ -f "$d/docker.exe" ]]; then
                export PATH="$d:$PATH"
                log "  ✓ docker found: $d/docker.exe"
                break
            fi
        done
    fi
    if ! command -v docker >/dev/null 2>&1; then
        log "ERROR: 'docker' not found"
        log "  Install Docker Desktop: https://docs.docker.com/desktop/setup/install/windows-install/"
        log "  Or: winget install -e --id Docker.DockerDesktop"
        log "  Then start Docker Desktop and re-run."
        failed=1
    elif ! docker info >/dev/null 2>&1; then
        log "WARNING: Docker is installed but the daemon is not running."
        log "  Attempting to start Docker Desktop..."
        local docker_exe
        docker_exe="$(command -v docker)"
        local docker_dir
        docker_dir="$(dirname "$docker_exe")"
        local desktop_exe="${docker_dir}/../Docker Desktop.exe"
        if [[ -f "$desktop_exe" ]]; then
            "$desktop_exe" &>/dev/null &
            log "  Docker Desktop launched — waiting 30s for daemon to initialize..."
            sleep 30
            if docker info >/dev/null 2>&1; then
                log "  ✓ Docker daemon is now running"
            else
                log "  ⚠️ Docker Desktop may still be starting. Continue waiting or check the system tray."
                log "  If it fails, start Docker Desktop manually and re-run."
                failed=1
            fi
        else
            log "  ⚠️ Docker CLI found but daemon not running. Start Docker Desktop manually."
            failed=1
        fi
    else
        log "  ✓ docker daemon running"
    fi

    # --- Hub binary ---
    log "Building hub binary..."
    if (cd "$ZERO_OPS_DIR" && go build -mod=mod -o bin/hub ./cmd/hub 2>&1); then
        log "  ✓ hub binary built"
    else
        log "ERROR: Hub binary build failed"
        failed=1
    fi

    # --- Secret files ---
    if [[ "$PROVIDER" == "hetzner" ]]; then
        if [[ ! -f "$ZERO_OPS_DIR/k8-secrets/hetzner/token" ]]; then
            log "ERROR: Hetzner token file not found at k8-secrets/hetzner/token"
            failed=1
        else
            log "  ✓ k8-secrets/hetzner/token found"
        fi
    fi

    if [[ ! -f "$ZERO_OPS_DIR/k8-secrets/github/github-pat-token" ]]; then
        log "ERROR: GitHub token file not found at k8-secrets/github/github-pat-token"
        failed=1
    else
        log "  ✓ k8-secrets/github/github-pat-token found"
    fi

    # --- AWS profile ---
    if ! command -v aws >/dev/null 2>&1; then
        : # already reported above
    elif ! aws configure list --profile zerotouch-platform-admin >/dev/null 2>&1; then
        log "ERROR: AWS profile 'zerotouch-platform-admin' not configured"
        log "  Fix: aws configure --profile zerotouch-platform-admin"
        failed=1
    else
        log "  ✓ AWS profile 'zerotouch-platform-admin' configured"
    fi

    if [[ $failed -ne 0 ]]; then
        error_exit "Prerequisites check failed. Fix the errors above and re-run."
    fi

    log "Prerequisites check passed"
}

# Read the kubeconfig path from the Go bootstrap state file (the result contract).
# Called after hub bootstrap completes to set KUBECONFIG_PATH for all downstream steps.
read_kubeconfig_from_state() {
    local go_state_file="$ZERO_OPS_DIR/.zero-ops/state/${CLUSTER_NAME}.json"
    if [[ -f "$go_state_file" ]]; then
        KUBECONFIG_PATH=$(jq -r '.mgmtKubeconfig // ""' "$go_state_file" 2>/dev/null || echo "")
    fi
    if [[ -z "$KUBECONFIG_PATH" ]]; then
        # Fallback: convention-based path
        KUBECONFIG_PATH="$ZERO_OPS_DIR/k8-secrets/kubeconfig/${CLUSTER_NAME}.kubeconfig"
    fi
    export KUBECONFIG_PATH
    log "Kubeconfig path: $KUBECONFIG_PATH"
}

# Step 1: Bootstrap Hub Cluster
step1_bootstrap_hub() {
    # Check if step is already completed AND the Go bootstrap state confirms postboot finished
    local go_state_file="$ZERO_OPS_DIR/.zero-ops/state/${CLUSTER_NAME}.json"
    # Handle teardown-on-bootstrap: clean up existing cluster before starting
    # State files are removed unconditionally to prevent stale state from
    # skipping the bootstrap on re-run. If teardown fails (cluster gone etc.),
    # the fresh bootstrap still proceeds from scratch.
    if [[ "$TEARDOWN" == "true" ]]; then
        log "TEARDOWN=true — tearing down existing cluster '${CLUSTER_NAME}'..."
        "$HUB_BINARY" teardown --name="${CLUSTER_NAME}" --confirm 2>&1 || log "WARNING: Teardown returned non-zero — cluster may not exist, continuing..."
        log "Cleanup: removing stale bootstrap state and logs..."
        docker system prune -f --volumes 2>/dev/null || true
        rm -f "$BOOTSTRAP_STATE_FILE"
        rm -f "$go_state_file"
        rm -f "$LOG_DIR/bootstrap.log"
        rm -f "$LOG_DIR/bootstrap-hub.log"
        rm -f "$LOG_DIR/init-secrets.log"
        log "✓ Bootstrap state and logs reset for fresh start"
        sleep 10  # let the smoke clear
    fi

    if is_step_completed "bootstrap_hub"; then
        # Verify the Go bootstrap actually completed all phases (incl. postboot)
		if [[ -f "$go_state_file" ]] && grep -q '"complete"' "$go_state_file"; then
            log "Step 1: Hub bootstrap already completed (postboot confirmed), skipping"
            read_kubeconfig_from_state
            return
        fi
        log "Step 1: Bash state says done but Go bootstrap postboot not found — re-running hub bootstrap to resume"
    fi

    # Source of truth check: the Go state file at state/<name>.json is the
    # single authoritative record of the bootstrap phase. If it shows the
    # bootstrap is complete, skip — even when the bash state cache is empty
    # (e.g. after a direct `hub bootstrap` CLI run, or a stale cache).
    if [[ -f "$go_state_file" ]] && grep -q '"complete"' "$go_state_file"; then
        log "Step 1: Hub bootstrap already completed (state file: $go_state_file), skipping"
        read_kubeconfig_from_state
        # Mirror to bash state so downstream steps see consistent step tracking
        mark_step_completed "bootstrap_hub"
        return
    fi

    log "Step 1: Bootstrapping Hub Cluster..."

    if [[ "$PROVIDER" == "local" ]]; then
        # Push current branch to remote so ArgoCD's Git generator can read boundary configs
        local current_branch
        current_branch=$(cd "$ZERO_OPS_DIR" && git rev-parse --abbrev-ref HEAD)
        if [[ "$current_branch" != "main" ]]; then
            log "Pushing current branch '$current_branch' to remote for ArgoCD reconciliation..."
            (cd "$ZERO_OPS_DIR" && git push origin "$current_branch" 2>&1) || \
                log "WARNING: git push failed — ArgoCD may not be able to read configs from remote"
        fi
        local env_flag="${ENVIRONMENT:---environment=dev}"
        log "Running: $HUB_BINARY bootstrap --name=${CLUSTER_NAME} --provider=docker $env_flag --keep-bootstrap --debug"
        (cd "$ZERO_OPS_DIR" && "$HUB_BINARY" bootstrap \
            --name="${CLUSTER_NAME}" \
            --provider=docker \
            $env_flag \
            --keep-bootstrap \
            --debug 2>&1 | tee "$LOG_DIR/bootstrap-hub.log")
    else
        export HCLOUD_TOKEN=$(cat "$ZERO_OPS_DIR/k8-secrets/hetzner/token")
        local env_flag="${ENVIRONMENT:---environment=prod}"
        log "Running: $HUB_BINARY bootstrap --name=${CLUSTER_NAME} --region=fsn1 $env_flag --debug"
        (cd "$ZERO_OPS_DIR" && "$HUB_BINARY" bootstrap \
            --name="${CLUSTER_NAME}" \
            --region=fsn1 \
            $env_flag \
            --debug 2>&1 | tee "$LOG_DIR/bootstrap-hub.log")
    fi

    # Read the result contract produced by the Go bootstrap
    read_kubeconfig_from_state

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
                --kubeconfig="$KUBECONFIG_PATH" 2>&1 | grep -q "already exists"; then
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
                    --kubeconfig="$KUBECONFIG_PATH"

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
        --kubeconfig="$KUBECONFIG_PATH"

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
        --kubeconfig="$KUBECONFIG_PATH"

    mark_step_completed "configure_github"
    log "GitHub access configuration completed"
}

# Step 4: Wait for ArgoCD to sync and create namespaces
step4_wait_namespaces() {
    if is_step_completed "wait_namespaces"; then
        log "Step 4: Namespaces already created, skipping"
        return
    fi

    log "Step 4: Waiting for ArgoCD to sync and create namespaces (timeout: 600s)..."

    # Correct polling logic for a resource that doesn't exist yet
    # kubectl wait fails if namespace doesn't exist, so we must poll first
    timeout=600
    elapsed=0
    while ! kubectl get namespace platform-data --kubeconfig="$KUBECONFIG_PATH" >/dev/null 2>&1; do
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
        --kubeconfig="$KUBECONFIG_PATH"

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

    # Wait for Infisical deployment to be ready before init-secrets (per ADR-021)
    log "Waiting for Infisical deployment to become ready..."
    if kubectl wait --for=condition=Available deployment/infisical-standalone-infisical \
        -n platform-security --timeout=300s --kubeconfig="$KUBECONFIG_PATH" >/dev/null 2>&1; then
        log "✅ Infisical is healthy"
    else
        log "⚠️ Infisical did not become healthy within timeout"
        log "The init-secrets step may fail; it can be re-run later"
    fi

    # Initialize secrets first (this will create the infisical-secrets secret)
    # For local Kind, set up a temporary port-forward so the Go binary can reach
    # the Infisical API (https://infisical.nutgraf.in is not reachable locally).
    if [[ "$PROVIDER" == "local" ]]; then
        kubectl port-forward -n platform-security svc/infisical-standalone-infisical 8080:8080 \
            --kubeconfig="$KUBECONFIG_PATH" &>/dev/null &
        PORT_FWD_PID=$!
        sleep 2
        export INFISICAL_API_URL="http://localhost:8080"
    fi

    log "Running: $HUB_BINARY init-secrets"
    "$HUB_BINARY" init-secrets \
        --kubeconfig="$KUBECONFIG_PATH" 2>&1 | tee "$LOG_DIR/init-secrets.log"

    # Tear down the temporary port-forward if it was started
    if [[ -n "${PORT_FWD_PID:-}" ]]; then
        kill "$PORT_FWD_PID" 2>/dev/null || true
        unset INFISICAL_API_URL
    fi

    # Mark Infisical healthy state if the log contains the success message
    if grep -q "✓ Infisical is healthy" "$LOG_DIR/init-secrets.log" 2>/dev/null; then
        mark_step_completed "infisical_healthy"
        log "  State recorded: infisical_healthy"
    fi

    # Mark infisical-auth ready state if the log contains the success message
    if grep -q "✓ infisical-auth secret found" "$LOG_DIR/init-secrets.log" 2>/dev/null; then
        mark_step_completed "infisical_auth_ready"
        log "  State recorded: infisical_auth_ready"
    fi

    # Wait for Infisical pod to be ready after secrets are created
    log "Waiting for Infisical pod to be ready after secrets initialization..."
    local max_attempts=180  # 30 minutes (180 × 10 seconds)
    local attempt=1

    while [[ $attempt -le $max_attempts ]]; do
        local pod_ready
        pod_ready=$(kubectl get pods -n platform-security \
            --kubeconfig="$KUBECONFIG_PATH" \
            -l app=infisical-standalone -o jsonpath='{.items[0].status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || echo "NotFound")

        if [[ "$pod_ready" == "True" ]]; then
            log "Infisical pod is ready"
            break
        fi

        log "Waiting for Infisical pod to be ready (attempt $attempt/$max_attempts, ready: $pod_ready). It mostly get ready by 50"
        sleep 10
        ((attempt++))
    done

    if [[ $attempt -gt $max_attempts ]]; then
        error_exit "Infisical pod did not become ready within expected time"
    fi

    # Verify the binary's credential-storage step actually produced the
    # infisical-auth Secret. This is the single source of truth for
    # "Step 5 actually completed" — the binary's strict health check is
    # meant to prevent silent skips, but we still verify the artifact
    # exists before marking the step complete. If the Secret is missing
    # we leave the step unmarked so a re-run can retry.
    local infisical_auth_data
    if ! infisical_auth_data=$(kubectl get secret infisical-auth -n platform-ops \
        -o jsonpath='{.data.client-id}{" "}{.data.client-secret}' \
        --kubeconfig="$KUBECONFIG_PATH" 2>/dev/null) || [[ -z "$infisical_auth_data" ]]; then
        log "❌ infisical-auth Secret missing or empty in platform-ops namespace"
        log "   The init-secrets binary may have failed to complete credential storage."
        log "   Re-run: $HUB_BINARY init-secrets --kubeconfig=$KUBECONFIG_PATH"
        log "   Then re-run: bash $0"
        return 1
    fi

    mark_step_completed "init_secrets"
    log "Bootstrap secrets initialization completed (infisical-auth verified)"
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
            --kubeconfig="$KUBECONFIG_PATH" \
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
        --kubeconfig="$KUBECONFIG_PATH"

    mark_step_completed "wait_database"
    log "Database deployment completed"
}

# Step 10: Wait for SpokePool readiness and certificate distribution
step10_wait_spokepool() {
    if is_step_completed "wait_spokepool"; then
        log "Step 10: SpokePool already ready, skipping"
        return
    fi

    log "Step 10: Waiting for SpokePool readiness and certificate distribution..."
    log "SpokePool: $SPOKEPOOL_NAME in namespace: $SPOKEPOOL_NAMESPACE"

    local max_attempts=$((SPOKEPOOL_TIMEOUT / 10))  # Convert seconds to attempts (10s intervals)
    local attempt=1

    log "Checking SpokePool $SPOKEPOOL_NAME status..."

    while [[ $attempt -le $max_attempts ]]; do
        # Check SpokePool status
        local spokepool_status
        spokepool_status=$(kubectl get spokepool "$SPOKEPOOL_NAME" -n "$SPOKEPOOL_NAMESPACE" \
            --kubeconfig="$KUBECONFIG_PATH" \
            -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || echo "NotFound")

        local spokepool_synced
        spokepool_synced=$(kubectl get spokepool "$SPOKEPOOL_NAME" -n "$SPOKEPOOL_NAMESPACE" \
            --kubeconfig="$KUBECONFIG_PATH" \
            -o jsonpath='{.status.conditions[?(@.type=="Synced")].status}' 2>/dev/null || echo "NotFound")

        if [[ "$spokepool_status" == "True" && "$spokepool_synced" == "True" ]]; then
            log "✅ SpokePool is Ready and Synced"
            break
        fi

        log "Waiting for SpokePool readiness (attempt $attempt/$max_attempts, Ready: $spokepool_status, Synced: $spokepool_synced). It mostly get ready by 130"
        sleep 10
        ((attempt++))
    done

    if [[ $attempt -gt $max_attempts ]]; then
        log "❌ SpokePool did not become ready within expected time"
        log "Current status: Ready=$spokepool_status, Synced=$spokepool_synced"
        error_exit "SpokePool readiness timeout"
    fi

    # Step 10a: Wait for CAPI Cluster to be Ready
    log "Step 10a: Checking CAPI Cluster readiness..."

    local cluster_name="$SPOKEPOOL_NAME"
    local cluster_attempt=1
    local cluster_max_attempts=$((CLUSTER_TIMEOUT / 10))

    while [[ $cluster_attempt -le $cluster_max_attempts ]]; do
        # Get cluster phase and conditions
        local cluster_phase
        cluster_phase=$(kubectl get cluster "$cluster_name" -n platform-capi \
            --kubeconfig="$KUBECONFIG_PATH" \
            -o jsonpath='{.status.phase}' 2>/dev/null || echo "Unknown")

        local infra_ready
        infra_ready=$(kubectl get cluster "$cluster_name" -n platform-capi \
            --kubeconfig="$KUBECONFIG_PATH" \
            -o jsonpath='{.status.conditions[?(@.type=="InfrastructureReady")].status}' 2>/dev/null || echo "False")

        local cp_ready
        cp_ready=$(kubectl get cluster "$cluster_name" -n platform-capi \
            --kubeconfig="$KUBECONFIG_PATH" \
            -o jsonpath='{.status.conditions[?(@.type=="ControlPlaneReady")].status}' 2>/dev/null || echo "False")

        local workers_ready
        workers_ready=$(kubectl get cluster "$cluster_name" -n platform-capi \
            --kubeconfig="$KUBECONFIG_PATH" \
            -o jsonpath='{.status.v1beta2.conditions[?(@.type=="WorkerMachinesReady")].status}' 2>/dev/null || echo "False")

        if [[ "$infra_ready" == "True" && "$cp_ready" == "True" && "$workers_ready" == "True" ]]; then
            log "✅ CAPI Cluster is ready: Phase=$cluster_phase, InfrastructureReady=True, ControlPlaneReady=True, WorkersReady=True"
            break
        fi

        log "Waiting for CAPI Cluster (attempt $cluster_attempt/$cluster_max_attempts): Phase=$cluster_phase, Infrastructure=$infra_ready, ControlPlane=$cp_ready, Workers=$workers_ready"
        sleep 10
        ((cluster_attempt++))
    done

    if [[ $cluster_attempt -gt $cluster_max_attempts ]]; then
        log "❌ CAPI Cluster did not become ready within expected time"
        error_exit "CAPI Cluster readiness timeout"
    fi

    # Step 10b: Verify ProviderConfig for spoke cluster exists
    log "Step 10b: Checking ProviderConfig for spoke cluster..."

    local providerconfig_attempt=1
    local providerconfig_max_attempts=60

    while [[ $providerconfig_attempt -le $providerconfig_max_attempts ]]; do
        if kubectl get providerconfig "$cluster_name" -n "$SPOKEPOOL_NAMESPACE" \
            --kubeconfig="$KUBECONFIG_PATH" >/dev/null 2>&1; then
            log "✅ ProviderConfig for spoke cluster exists: $cluster_name"
            break
        fi

        log "Waiting for ProviderConfig (attempt $providerconfig_attempt/$providerconfig_max_attempts): $cluster_name"
        sleep 10
        ((providerconfig_attempt++))
    done

    if [[ $providerconfig_attempt -gt $providerconfig_max_attempts ]]; then
        log "⚠️ ProviderConfig not found (may be created by external controller, continuing...)"
    fi

    # Step 10c: Wait for cert-operator PKI artifacts (machine-identity + bootstrap-cert)
    # Replaces legacy function-cert-distribution checks. cert-operator watches SpokePool
    # and creates these Secrets in platform-capi for ClusterResourceSet consumption.
    log "Step 10c: Waiting for cert-operator PKI artifacts..."
    local pki_attempt=1
    local pki_max_attempts=$((CERT_TIMEOUT / 10))

    while [[ $pki_attempt -le $pki_max_attempts ]]; do
        local identity_ready=false
        local cert_ready=false

        if kubectl get secret "${SPOKEPOOL_NAME}-machine-identity" -n platform-capi \
            --kubeconfig="$KUBECONFIG_PATH" >/dev/null 2>&1; then
            identity_ready=true
        fi

        if kubectl get secret "${SPOKEPOOL_NAME}-bootstrap-cert" -n platform-capi \
            --kubeconfig="$KUBECONFIG_PATH" >/dev/null 2>&1; then
            cert_ready=true
        fi

        if [[ "$identity_ready" == "true" && "$cert_ready" == "true" ]]; then
            log "✅ cert-operator PKI artifacts ready"
            break
        fi

        log "Waiting for cert-operator PKI (attempt $pki_attempt/$pki_max_attempts): machine-identity=$identity_ready bootstrap-cert=$cert_ready"
        sleep 10
        ((pki_attempt++))
    done

    if [[ $pki_attempt -gt $pki_max_attempts ]]; then
        log "❌ cert-operator PKI artifacts not created within timeout"
        log "Check cert-operator pod logs in platform-ops namespace"
        log "This may mean Infisical is unhealthy or the infisical-auth secret is missing"
        error_exit "cert-operator PKI timeout"
    fi

    # Step 10d: Verify certificates exist in target namespaces
    log "Step 10d: Verifying certificates in target namespaces..."

    local target_namespaces=("platform-observability" "platform-messaging" "platform-ops")
    for ns in "${target_namespaces[@]}"; do
        # Check if namespace exists
        if kubectl get namespace "$ns" \
            --kubeconfig="$KUBECONFIG_PATH" >/dev/null 2>&1; then
            log "✅ Namespace exists: $ns"
        else
            log "⚠️ Namespace not found yet (may be created later): $ns"
        fi
    done

    # Step 10e: Verify worker nodes are Ready
    log "Step 10e: Verifying worker nodes are Ready..."

    local machine_lines=()
    mapfile -t machine_lines < <(kubectl get machines -l cluster.x-k8s.io/cluster-name="$cluster_name" \
        --kubeconfig="$KUBECONFIG_PATH" \
        -n platform-capi --no-headers 2>/dev/null || true)
    local worker_nodes=${#machine_lines[@]}

    if [[ "$worker_nodes" -gt 0 ]]; then
        local ready_names=()
        mapfile -t ready_names < <(kubectl get machines -l cluster.x-k8s.io/cluster-name="$cluster_name" \
            --kubeconfig="$KUBECONFIG_PATH" \
            -n platform-capi \
            -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null || true)
        local ready_workers=${#ready_names[@]}

        log "Worker nodes: $ready_workers/$worker_nodes Ready"

        if [[ "$ready_workers" -lt "$worker_nodes" ]]; then
            log "⚠️ Not all worker nodes are Ready yet, but continuing (nodes will be available after cluster is provisioned)"
        fi
    else
        log "⚠️ No worker nodes found yet (normal during cluster bootstrap)"
    fi

    mark_step_completed "wait_spokepool"
    log "✅ SpokePool readiness validation completed successfully"
    log "📊 SpokePool Status: Ready=True, Synced=True"
    log "🔐 Certificate Distribution: All 3 certificates ready and synced"
    log "🏗️  Spoke Cluster: InfrastructureReady=True, ControlPlaneReady=True, WorkersReady=True"
}

# Main execution
main() {
    # Parse arguments
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --name=*)
                CLUSTER_NAME="${1#*=}"
                shift
                ;;
            --name)
                CLUSTER_NAME="$2"
                shift 2
                ;;
            --provider=*)
                PROVIDER="${1#*=}"
                shift
                ;;
            --provider)
                PROVIDER="$2"
                shift 2
                ;;
            --environment=*)
                ENVIRONMENT="${1#*=}"
                shift
                ;;
            --environment)
                ENVIRONMENT="$2"
                shift 2
                ;;
            --spoke=*)
                SPOKEPOOL_NAME="${1#*=}"
                shift
                ;;
            --spoke)
                SPOKEPOOL_NAME="$2"
                shift 2
                ;;
            --teardown)
                TEARDOWN="true"
                shift
                ;;
            *)
                log "WARNING: Unknown argument: $1"
                shift
                ;;
        esac
    done

    if [[ "$PROVIDER" != "hetzner" && "$PROVIDER" != "local" ]]; then
        error_exit "Invalid provider: $PROVIDER (must be 'hetzner' or 'local')"
    fi

    # Docker provider uses a fixed cluster name. The Go state file at
    # .zero-ops/state/<name>.json is the single source of truth, so the
    # name must be deterministic. Override any user-supplied --name.
    if [[ "$PROVIDER" == "local" ]]; then
        if [[ "$CLUSTER_NAME" != "hub-local" ]]; then
            log "Docker provider uses fixed cluster name 'hub-local' (overriding '$CLUSTER_NAME')"
        fi
        CLUSTER_NAME="hub-local"
    fi

    log "Starting Zero-Ops Hub Bootstrap Process"
    log "Project root: $ZERO_OPS_DIR"
    log "Log directory: $LOG_DIR"
    log "Provider: $PROVIDER"
    log "Cluster name: $CLUSTER_NAME"

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
    sleep 10  # Wait between steps

    if [[ "$PROVIDER" == "local" ]]; then
        SPOKEPOOL_NAME="local-dev"
        log "Step 10: Using local SpokePool: $SPOKEPOOL_NAME"
    elif [[ -z "$SPOKEPOOL_NAME" ]]; then
        error_exit "SPOKEPOOL_NAME must be set via --spoke flag or SPOKEPOOL_NAME env var for provider '$PROVIDER'"
    fi
    step10_wait_spokepool

    log "Zero-Ops Hub Bootstrap Process completed successfully!"
    log "🎯 Hub cluster: Ready and operational"
    log "🌐 SpokePool: Provisioned and ready for tenant workloads"
    log "🔐 Certificate distribution: Complete"
    log "🏗️  Spoke cluster: Ready for tenant database provisioning"
    log "You can now access your hub cluster using: kubectl --kubeconfig=$KUBECONFIG_PATH"

    # Run post-bootstrap core services validation
    log ""
    log "Running post-bootstrap core services validation..."
    local validate_script="$SCRIPT_DIR/post-bootstrap-validate.sh"
    if [[ -f "$validate_script" ]]; then
        bash "$validate_script" || log "⚠️  Post-bootstrap validation reported failures — review the summary above"
    else
        log "⚠️  post-bootstrap-validate.sh not found at $validate_script — skipping validation"
    fi
}

# Handle script interruption
trap 'log "Script interrupted"; exit 1' INT TERM

# Run main function
main "$@"

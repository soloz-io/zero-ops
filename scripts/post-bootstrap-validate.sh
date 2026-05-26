#!/bin/bash
# Zero-Ops Hub Post-Bootstrap Core Services Validation
# Run after hub-bootstrap.sh completes to validate all platform services are healthy.
# Reports a full pass/fail summary and exits non-zero if any critical check fails.

set -uo pipefail

# ─── Configuration ───────────────────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
ZERO_OPS_DIR="$PROJECT_ROOT"
LOG_DIR="$ZERO_OPS_DIR/.zero-ops"
LOG_FILE="$LOG_DIR/post-bootstrap-validate.log"
KUBECONFIG="${KUBECONFIG:-$ZERO_OPS_DIR/k8-secrets/kubeconfig/hub.kubeconfig}"
SPOKEPOOL_NAME="${SPOKEPOOL_NAME:-spoke-pool-eu-prod-01}"

# Timeout for individual checks (seconds)
DEPLOY_READY_TIMEOUT="${DEPLOY_READY_TIMEOUT:-120}"
ARGOCD_SYNC_TIMEOUT="${ARGOCD_SYNC_TIMEOUT:-60}"

mkdir -p "$LOG_DIR"
# Clear log on each run (do not append to stale output from previous runs)
> "$LOG_FILE"

# ─── Counters ─────────────────────────────────────────────────────────────────
PASS=0
FAIL=0
WARN=0
declare -a FAILURES=()
declare -a WARNINGS=()

# ─── Logging ──────────────────────────────────────────────────────────────────
log() { echo "[$(date '+%Y-%m-%d %H:%M:%S')] $1" | tee -a "$LOG_FILE"; }
log_pass()  { log "  ✅ $1"; ((PASS++)); }
log_fail()  { log "  ❌ $1"; FAILURES+=("$1"); ((FAIL++)); }
log_warn()  { log "  ⚠️  $1"; WARNINGS+=("$1"); ((WARN++)); }
log_section() { log ""; log "══════════════════════════════════════════"; log "  $1"; log "══════════════════════════════════════════"; }

# ─── kubectl wrapper ──────────────────────────────────────────────────────────
kc() { kubectl --kubeconfig="$KUBECONFIG" "$@" 2>/dev/null; }

# ─── Helpers ──────────────────────────────────────────────────────────────────

# Check all pods in a namespace are Running/Completed (no CrashLoop, Pending, etc.)
check_namespace_pods() {
    local ns="$1"
    local label_selector="${2:-}"
    local label_arg=""
    [[ -n "$label_selector" ]] && label_arg="-l $label_selector"

    local not_ready
    # shellcheck disable=SC2086
    not_ready=$(kc get pods -n "$ns" $label_arg --no-headers 2>/dev/null \
        | grep -v -E '([0-9]+/[0-9]+\s+(Running|Completed))' \
        | grep -v "^$" || true)

    if [[ -z "$not_ready" ]]; then
        local count
        # shellcheck disable=SC2086
        count=$(kc get pods -n "$ns" $label_arg --no-headers 2>/dev/null | grep -c '' || echo 0)
        if [[ "$count" -eq 0 ]]; then
            log_warn "Namespace $ns: no pods found (nothing deployed yet?)"
        else
            log_pass "Namespace $ns: all $count pod(s) healthy"
        fi
    else
        local crashloop_count
        crashloop_count=$(echo "$not_ready" | grep -c "CrashLoopBackOff" || true)
        local pending_count
        pending_count=$(echo "$not_ready" | grep -c "Pending" || true)
        local error_count
        error_count=$(echo "$not_ready" | grep -c "Error" || true)
        log_fail "Namespace $ns: unhealthy pods (CrashLoop=$crashloop_count, Pending=$pending_count, Error=$error_count)"
        echo "$not_ready" | while IFS= read -r line; do
            [[ -n "$line" ]] && log "     → $line"
        done
    fi
}

# Check a specific deployment is available
check_deployment() {
    local ns="$1"
    local name="$2"
    local desired
    desired=$(kc get deployment "$name" -n "$ns" -o jsonpath='{.spec.replicas}' 2>/dev/null || echo "0")
    local available
    available=$(kc get deployment "$name" -n "$ns" -o jsonpath='{.status.availableReplicas}' 2>/dev/null || echo "0")
    desired="${desired:-0}"; available="${available:-0}"

    if [[ "$available" -ge "$desired" && "$desired" -gt 0 ]]; then
        log_pass "Deployment $ns/$name: $available/$desired available"
    else
        log_fail "Deployment $ns/$name: $available/$desired available"
    fi
}

# Check ArgoCD application sync + health
check_argocd_app() {
    local app="$1"
    local severity="${2:-FAIL}"   # FAIL or WARN
    local sync
    sync=$(kc get application "$app" -n platform-ops -o jsonpath='{.status.sync.status}' 2>/dev/null || echo "Unknown")
    local health
    health=$(kc get application "$app" -n platform-ops -o jsonpath='{.status.health.status}' 2>/dev/null || echo "Unknown")

    if [[ "$sync" == "Synced" && "$health" == "Healthy" ]]; then
        log_pass "ArgoCD app $app: Sync=$sync Health=$health"
    elif [[ "$sync" == "Synced" && "$health" == "Progressing" ]]; then
        # Progressing is transitional — warn regardless of severity so it is visible
        log_warn "ArgoCD app $app: Sync=$sync Health=$health (still converging)"
    else
        local out_of_sync_resources
        out_of_sync_resources=$(kc get application "$app" -n platform-ops \
            -o jsonpath='{range .status.resources[?(@.status=="OutOfSync")]}{.kind}/{.name} {end}' 2>/dev/null || echo "")
        local msg="ArgoCD app $app: Sync=$sync Health=$health"
        [[ -n "$out_of_sync_resources" ]] && msg="$msg (OutOfSync: $out_of_sync_resources)"
        if [[ "$severity" == "WARN" ]]; then
            log_warn "$msg"
        else
            log_fail "$msg"
        fi
    fi
}

# Check ExternalSecret is synced
check_external_secret() {
    local ns="$1"
    local name="$2"
    local ready
    ready=$(kc get externalsecret "$name" -n "$ns" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || echo "Unknown")
    if [[ "$ready" == "True" ]]; then
        log_pass "ExternalSecret $ns/$name: Ready"
    else
        local msg
        msg=$(kc get externalsecret "$name" -n "$ns" -o jsonpath='{.status.conditions[?(@.type=="Ready")].message}' 2>/dev/null || echo "unknown error")
        log_fail "ExternalSecret $ns/$name: Not ready ($msg)"
    fi
}

# Check a Crossplane object Ready+Synced
check_crossplane_object() {
    local ns="$1"
    local name="$2"
    local ready
    ready=$(kc get object "$name" -n "$ns" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || echo "Unknown")
    local synced
    synced=$(kc get object "$name" -n "$ns" -o jsonpath='{.status.conditions[?(@.type=="Synced")].status}' 2>/dev/null || echo "Unknown")
    if [[ "$ready" == "True" && "$synced" == "True" ]]; then
        log_pass "Crossplane object $ns/$name: Ready+Synced"
    else
        log_fail "Crossplane object $ns/$name: Ready=$ready Synced=$synced"
    fi
}

# ─── Prerequisite: kubeconfig ─────────────────────────────────────────────────
preflight_check() {
    log_section "PREFLIGHT"
    if [[ ! -f "$KUBECONFIG" ]]; then
        log "ERROR: kubeconfig not found at $KUBECONFIG"
        log "       Set KUBECONFIG env var or run hub bootstrap first."
        exit 1
    fi
    if ! kc cluster-info >/dev/null 2>&1; then
        log "ERROR: Cannot connect to cluster using $KUBECONFIG"
        exit 1
    fi
    local server
    server=$(kc cluster-info 2>/dev/null | head -1 | sed 's/.*at //' || echo "unknown")
    log_pass "Hub cluster reachable: $server"

    if ! kc get namespace platform-ops >/dev/null 2>&1; then
        log "ERROR: platform-ops namespace not found. Bootstrap may not have completed."
        exit 1
    fi
    log_pass "Core namespaces present"
}

# ─── 1. PLATFORM NAMESPACES ───────────────────────────────────────────────────
check_namespaces() {
    log_section "1. PLATFORM NAMESPACES"
    local required_ns=(
        platform-ops
        platform-capi
        platform-data
        platform-identity
        platform-billing
        platform-messaging
        platform-observability
        platform-security
        platform-edge
        platform-controlplane
        cert-manager
        cnpg-system
        kube-system
    )
    for ns in "${required_ns[@]}"; do
        if kc get namespace "$ns" >/dev/null 2>&1; then
            log_pass "Namespace $ns exists"
        else
            log_fail "Namespace $ns missing"
        fi
    done
}

# ─── 2. ARGOCD ────────────────────────────────────────────────────────────────
check_argocd() {
    log_section "2. ARGOCD"
    check_namespace_pods "platform-ops" "app.kubernetes.io/name=argocd-server"
    check_deployment "platform-ops" "argocd-server"
    check_deployment "platform-ops" "argocd-repo-server"
    check_deployment "platform-ops" "argocd-applicationset-controller"

    # Kyverno (platform-ops per ADR-015)
    check_deployment "platform-ops" "kyverno-admission-controller"
    check_deployment "platform-ops" "kyverno-background-controller"
}

# ─── 3. CROSSPLANE ────────────────────────────────────────────────────────────
check_crossplane() {
    log_section "3. CROSSPLANE"
    check_namespace_pods "platform-ops" "app=crossplane"
    check_deployment "platform-ops" "crossplane"
    check_deployment "platform-ops" "crossplane-rbac-manager"

    # Crossplane providers
    log "  Checking Crossplane providers..."
    local providers
    providers=$(kc get providers.pkg.crossplane.io -o jsonpath='{range .items[*]}{.metadata.name}: {.status.conditions[?(@.type=="Healthy")].status}{"\n"}{end}' 2>/dev/null || echo "")
    if [[ -n "$providers" ]]; then
        while IFS= read -r line; do
            [[ -z "$line" ]] && continue
            if echo "$line" | grep -q ": True"; then
                log_pass "Provider $line"
            else
                log_fail "Provider $line (not Healthy)"
            fi
        done <<< "$providers"
    else
        log_warn "No Crossplane providers found"
    fi
}

# ─── 4. EXTERNAL SECRETS OPERATOR ─────────────────────────────────────────────
check_eso() {
    log_section "4. EXTERNAL SECRETS OPERATOR"
    check_namespace_pods "platform-ops" "app.kubernetes.io/name=external-secrets"

    # ClusterSecretStore
    local css_ready
    css_ready=$(kc get clustersecretstore infisical-backend -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || echo "Unknown")
    if [[ "$css_ready" == "True" ]]; then
        log_pass "ClusterSecretStore infisical-backend: Ready"
    else
        log_fail "ClusterSecretStore infisical-backend: Not ready (status=$css_ready)"
    fi
}

# ─── 5. INFISICAL ─────────────────────────────────────────────────────────────
check_infisical() {
    log_section "5. INFISICAL"
    check_namespace_pods "platform-security"

    # Key ExternalSecrets
    local key_secrets=(
        "platform-data:hub-db-credentials"
        "platform-data:platform-db-app-credentials"
        "platform-data:control-plane-db-credentials"
        "platform-billing:openmeter-postgresql"
        "platform-billing:openmeter-clickhouse"
        "platform-billing:openmeter-svix"
    )
    for entry in "${key_secrets[@]}"; do
        local ns="${entry%%:*}"
        local name="${entry##*:}"
        check_external_secret "$ns" "$name"
    done
}

# ─── 6. DATABASES (CNPG) ──────────────────────────────────────────────────────
check_databases() {
    log_section "6. DATABASES (CloudNative-PG)"
    # cnpg-system = upstream operator namespace (per ADR-015 Upstream Namespaces)
    # Operator health is checked here; database workloads live in platform-data
    # Deployment name from cloudnative-pg Helm chart is cloudnative-pg.
    # Fall back to legacy name cnpg-controller-manager if the primary is not found.
    local cnpg_deploy
    cnpg_deploy=$(kc get deployment -n cnpg-system --no-headers 2>/dev/null \
        | awk '{print $1}' | grep -E '^(cloudnative-pg|cnpg-controller-manager)$' | head -1 || echo "cloudnative-pg")
    check_deployment "cnpg-system" "$cnpg_deploy"

    # CNPG clusters (platform-data per ADR-015)
    local clusters
    clusters=$(kc get clusters.postgresql.cnpg.io -n platform-data \
        -o jsonpath='{range .items[*]}{.metadata.name}: {.status.phase}{"\n"}{end}' 2>/dev/null || echo "")
    if [[ -n "$clusters" ]]; then
        while IFS= read -r line; do
            [[ -z "$line" ]] && continue
            if echo "$line" | grep -q ": Cluster in healthy state"; then
                log_pass "CNPG Cluster $line"
            else
                log_fail "CNPG Cluster $line (not Ready)"
            fi
        done <<< "$clusters"
    else
        log_fail "No CNPG clusters found in platform-data"
    fi

    # Redis
    local redis_total redis_running
    redis_total=$(kc get pods -n platform-data -l app=platform-redis --no-headers 2>/dev/null | grep -c '' || echo "0")
    redis_running=$(kc get pods -n platform-data -l app=platform-redis --no-headers 2>/dev/null | grep -c "Running" || echo "0")
    if [[ "$redis_total" -gt 0 && "$redis_running" -eq "$redis_total" ]]; then
        log_pass "Redis (platform-data): $redis_running/$redis_total Running"
    elif [[ "$redis_running" -gt 0 ]]; then
        log_warn "Redis (platform-data): $redis_running/$redis_total Running (partial)"
    else
        log_fail "Redis (platform-data): not found or not running"
    fi

    # All data-layer pods (postgres, pooler, redis)
    check_namespace_pods "platform-data"
}

# ─── 7. CLICKHOUSE ────────────────────────────────────────────────────────────
check_clickhouse() {
    log_section "7. CLICKHOUSE"
    check_argocd_app "platform-clickhouse" "FAIL"

    # ClickHouseInstallation CR
    local chi_status
    chi_status=$(kc get clickhouseinstallation platform-clickhouse -n platform-data \
        -o jsonpath='{.status.status}' 2>/dev/null || echo "NotFound")
    if [[ "$chi_status" == "Completed" ]]; then
        log_pass "ClickHouseInstallation platform-clickhouse: $chi_status"
    elif [[ "$chi_status" == "NotFound" ]]; then
        log_fail "ClickHouseInstallation platform-clickhouse: Not deployed (ArgoCD sync failed?)"
    else
        log_warn "ClickHouseInstallation platform-clickhouse: status=$chi_status (may still be provisioning)"
    fi

    # ClickHouse service reachable
    local ch_svc
    ch_svc=$(kc get svc -n platform-data -l "clickhouse.altinity.com/chi=platform-clickhouse" \
        --no-headers 2>/dev/null | grep -c '' || echo "0")
    if [[ "${ch_svc:-0}" -gt 0 ]]; then
        log_pass "ClickHouse services present ($ch_svc found)"
    else
        log_fail "ClickHouse services not found in platform-data"
    fi
}

# ─── 8. OPENMETER ─────────────────────────────────────────────────────────────
check_openmeter() {
    log_section "8. OPENMETER"
    check_argocd_app "openmeter" "FAIL"

    local om_deployments=(
        "openmeter-api"
        "openmeter-balance-worker"
        "openmeter-billing-worker"
        "openmeter-notification-service"
        "openmeter-sink-worker"
        "openmeter-svix"
    )

    for deploy in "${om_deployments[@]}"; do
        local available
        available=$(kc get deployment "$deploy" -n platform-billing \
            -o jsonpath='{.status.availableReplicas}' 2>/dev/null || echo "0")
        local desired
        desired=$(kc get deployment "$deploy" -n platform-billing \
            -o jsonpath='{.spec.replicas}' 2>/dev/null || echo "1")
        available="${available:-0}"; desired="${desired:-1}"
        if [[ "$available" -ge "$desired" && "$desired" -gt 0 ]]; then
            log_pass "OpenMeter $deploy: $available/$desired"
        else
            # Show crash reason if CrashLoopBackOff
            local crash_reason
            crash_reason=$(kc get pods -n platform-billing -l "app.kubernetes.io/name=$deploy" \
                --no-headers 2>/dev/null | grep -v "Running" | head -1 || true)
            log_fail "OpenMeter $deploy: $available/$desired available${crash_reason:+ → $crash_reason}"
        fi
    done

    # Kafka (StatefulSet)
    local kafka_ready_raw
    kafka_ready_raw=$(kc get statefulset -n platform-billing -l "app.kubernetes.io/name=kafka" \
        --no-headers 2>/dev/null | awk '{print $2}' | head -1 | tr -d '[:space:]' || echo "0/0")
    local kafka_ready="${kafka_ready_raw:-0/0}"
    if echo "$kafka_ready" | grep -qE '^[1-9][0-9]*/[1-9][0-9]*$'; then
        log_pass "OpenMeter Kafka: $kafka_ready ready"
    else
        log_fail "OpenMeter Kafka: ready=$kafka_ready (StatefulSet not ready)"
    fi

    # Recurring job errors (billing/subscription sync)
    local failed_jobs
    failed_jobs=$(kc get jobs -n platform-billing --no-headers 2>/dev/null \
        | awk '$2 ~ /^0\// {print $1}' | grep -c '' || echo "0")
    if [[ "$failed_jobs" -gt 5 ]]; then
        log_warn "OpenMeter: $failed_jobs failed recurring jobs (billing/subscription cronjobs) — may indicate backend not ready"
    fi
}

# ─── 9. ORY IDENTITY STACK ────────────────────────────────────────────────────
check_ory() {
    log_section "9. ORY IDENTITY STACK"
    check_argocd_app "ory-kratos" "FAIL"
    check_argocd_app "ory-hydra" "FAIL"
    check_argocd_app "ory-keto" "FAIL"

    local ory_deployments=(
        "platform-identity:ory-kratos"
        "platform-identity:ory-hydra"
        "platform-identity:ory-keto"
    )
    for entry in "${ory_deployments[@]}"; do
        local ns="${entry%%:*}"
        local name="${entry##*:}"
        # Use word-boundary anchored match: first column (name) starts with $name
        local found
        found=$(kc get deployment -n "$ns" --no-headers 2>/dev/null | awk -v n="$name" '$1 ~ "^" n' | awk '{print $1}' | head -1 || true)
        if [[ -n "$found" ]]; then
            check_deployment "$ns" "$found"
        else
            log_warn "Ory deployment not found: $ns/$name (may use different name)"
        fi
    done
}

# ─── 10. NATS MESSAGING ───────────────────────────────────────────────────────
check_nats() {
    log_section "10. NATS MESSAGING"
    check_argocd_app "platform-nats" "FAIL"

    local nats_ready
    nats_ready=$(kc get pods -n platform-messaging --no-headers 2>/dev/null \
        | grep -c "Running" || echo "0")
    if [[ "$nats_ready" -gt 0 ]]; then
        log_pass "NATS pods running: $nats_ready"
    else
        log_warn "NATS: no running pods in platform-messaging"
    fi
}

# ─── 11. HUB OPERATOR ─────────────────────────────────────────────────────────
check_hub_operator() {
    log_section "11. HUB-OPERATOR"
    check_argocd_app "hub-operator" "FAIL"
    check_deployment "platform-ops" "hub-operator"

    # Billing CRDs installed (ops.nutgraf.in group — added in hub-operator kustomization.yaml)
    local billing_crds=(
        "meters.ops.nutgraf.in"
        "features.ops.nutgraf.in"
        "plans.ops.nutgraf.in"
    )
    for crd in "${billing_crds[@]}"; do
        if kc get crd "$crd" >/dev/null 2>&1; then
            log_pass "Billing CRD $crd: installed"
        else
            log_fail "Billing CRD $crd: NOT installed (check hub-operator kustomization)"
        fi
    done

    # HubEnvironment CR status
    local hub_env_name
    hub_env_name=$(kc get hubenvironment -A \
        -o jsonpath='{range .items[*]}{.metadata.namespace}/{.metadata.name}{"\n"}{end}' 2>/dev/null | head -1 || echo "")
    if [[ -n "$hub_env_name" ]]; then
        local he_ns="${hub_env_name%%/*}"
        local he_name="${hub_env_name##*/}"
        local he_ready
        he_ready=$(kc get hubenvironment "$he_name" -n "$he_ns" \
            -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || echo "Unknown")
        if [[ "$he_ready" == "True" ]]; then
            log_pass "HubEnvironment $he_name: Ready"
        else
            log_warn "HubEnvironment $he_name: Ready=$he_ready (may still reconciling)"
        fi
    else
        log_warn "No HubEnvironment CR found"
    fi
}

# ─── 12. KUBE-SBT API ─────────────────────────────────────────────────────────
check_kube_sbt_api() {
    log_section "12. KUBE-SBT API"
    check_argocd_app "kube-sbt-api" "FAIL"
    check_namespace_pods "platform-ops" "app=kube-sbt-api"

    local svc_ip
    svc_ip=$(kc get svc kube-sbt-api -n platform-ops -o jsonpath='{.spec.clusterIP}' 2>/dev/null || echo "")
    if [[ -n "$svc_ip" && "$svc_ip" != "None" ]]; then
        log_pass "kube-sbt-api Service: ClusterIP=$svc_ip"
    else
        log_warn "kube-sbt-api Service: not found or no ClusterIP"
    fi
}

# ─── 13. INGRESS + CERT-MANAGER ───────────────────────────────────────────────
check_ingress() {
    log_section "13. INGRESS & CERT-MANAGER"
    check_argocd_app "ingress-nginx" "FAIL"
    check_namespace_pods "cert-manager"
    check_deployment "cert-manager" "cert-manager"
    check_deployment "cert-manager" "cert-manager-webhook"

    local ingress_ready
    ingress_ready=$(kc get pods -n platform-edge --no-headers 2>/dev/null \
        | grep -c "Running" || echo "0")
    if [[ "$ingress_ready" -gt 0 ]]; then
        log_pass "Ingress pods (platform-edge): $ingress_ready running"
    else
        log_warn "No ingress pods running in platform-edge"
    fi
}

# ─── 14. SPOKE POOL + CAPI ────────────────────────────────────────────────────
check_spoke() {
    log_section "14. SPOKE POOL & CAPI"

    # CAPI operator deployment (migrated to platform-capi per ADR-015)
    check_deployment "platform-capi" "capi-operator-controller-manager"

    # CAPI Provider CRs (all in platform-capi per ADR-015)
    local providers=("CoreProvider/cluster-api" "BootstrapProvider/kubeadm" "ControlPlaneProvider/kubeadm" "InfrastructureProvider/hetzner")
    for p in "${providers[@]}"; do
        local kind="${p%%/*}"
        local name="${p##*/}"
        local ready
        ready=$(kc get "$kind" "$name" -n platform-capi \
            -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || echo "Unknown")
        if [[ "$ready" == "True" ]]; then
            log_pass "CAPI $kind/$name: Ready"
        else
            log_fail "CAPI $kind/$name: Ready=$ready"
        fi
    done

    local spokepool_ready
    spokepool_ready=$(kc get spokepool "$SPOKEPOOL_NAME" -n platform-ops \
        -o jsonpath='{.status.conditions[?(@.type=="CrossplaneAdminSecretGenerated")].status}' 2>/dev/null || echo "Unknown")
    local spokepool_synced
    spokepool_synced=$(kc get spokepool "$SPOKEPOOL_NAME" -n platform-ops \
        -o jsonpath='{.status.conditions[?(@.type=="CertificatesMinted")].status}' 2>/dev/null || echo "Unknown")
    if [[ "$spokepool_ready" == "True" && "$spokepool_synced" == "True" ]]; then
        log_pass "SpokePool $SPOKEPOOL_NAME: Ready+Synced"
    else
        log_fail "SpokePool $SPOKEPOOL_NAME: CrossplaneAdminSecretGenerated=$spokepool_ready CertificatesMinted=$spokepool_synced"
    fi

    local capi_cluster_phase
    capi_cluster_phase=$(kc get cluster "$SPOKEPOOL_NAME" -n platform-capi \
        -o jsonpath='{.status.phase}' 2>/dev/null || echo "Unknown")
    if [[ "$capi_cluster_phase" == "Provisioned" ]]; then
        log_pass "CAPI Cluster $SPOKEPOOL_NAME: Provisioned"
    else
        log_fail "CAPI Cluster $SPOKEPOOL_NAME: phase=$capi_cluster_phase"
    fi

    # Certificate distributions
    local cert_resources=(
        "${SPOKEPOOL_NAME}-alloy-cert-dist"
        "${SPOKEPOOL_NAME}-nats-cert-dist"
        "${SPOKEPOOL_NAME}-argocd-cert-dist"
    )
    for cert in "${cert_resources[@]}"; do
        check_crossplane_object "platform-ops" "$cert"
    done
}

# ─── 15. CRITICAL ARGOCD APPS (PLATFORM INFRA) ───────────────────────────────
check_platform_argocd_apps() {
    log_section "15. CRITICAL ARGOCD APPS"

    # Critical — failure blocks operations
    local critical_apps=(
        "platform-namespaces"
        "02-platform-data"
        "platform-external-secrets"
        "platform-crossplane"
        "platform-crossplane-providers"
        "platform-cluster-secret-store"
        "platform-cloudnative-pg"
        "platform-database"
        "platform-identity"
        "hub-environment"
        "hub-operator"
        "platform-nats"
        "platform-redis"
        "platform-security-certificates"
    )
    for app in "${critical_apps[@]}"; do
        check_argocd_app "$app" "FAIL"
    done

    # Warning only — degraded but not blocking
    local warn_apps=(
        "01-platform-infra"
        "03-platform-services"
        "platform-kyverno"
        "platform-clickhouse"
        "clickhouse-operator"
        "openmeter"
        "ory-kratos"
        "ory-hydra"
        "ory-keto"
        "platform-spire"
        "platform-infisical-prerequisites"
    )
    for app in "${warn_apps[@]}"; do
        check_argocd_app "$app" "WARN"
    done
}

# ─── 16. OBSERVABILITY ────────────────────────────────────────────────────────
check_observability() {
    log_section "16. OBSERVABILITY"
    check_argocd_app "victoria-metrics-cluster" "WARN"
    check_argocd_app "victoria-metrics-alerts" "WARN"
    check_argocd_app "grafana-alloy" "WARN"

    local obs_ready
    obs_ready=$(kc get pods -n platform-observability --no-headers 2>/dev/null \
        | grep -c "Running" || echo "0")
    if [[ "$obs_ready" -gt 0 ]]; then
        log_pass "Observability pods (platform-observability): $obs_ready running"
    else
        log_warn "No observability pods running in platform-observability"
    fi
}

# ─── SUMMARY ──────────────────────────────────────────────────────────────────
print_summary() {
    log ""
    log "╔══════════════════════════════════════════╗"
    log "║    POST-BOOTSTRAP VALIDATION SUMMARY     ║"
    log "╠══════════════════════════════════════════╣"
    log "║  ✅ PASSED : $PASS"
    log "║  ❌ FAILED : $FAIL"
    log "║  ⚠️  WARNED : $WARN"
    log "╠══════════════════════════════════════════╣"

    if [[ ${#FAILURES[@]} -gt 0 ]]; then
        log "║  CRITICAL FAILURES:"
        for f in "${FAILURES[@]}"; do
            log "║    ✗ $f"
        done
        log "╠══════════════════════════════════════════╣"
    fi

    if [[ ${#WARNINGS[@]} -gt 0 ]]; then
        log "║  WARNINGS:"
        for w in "${WARNINGS[@]}"; do
            log "║    ! $w"
        done
        log "╠══════════════════════════════════════════╣"
    fi

    if [[ "$FAIL" -eq 0 ]]; then
        log "║  🎉 ALL CRITICAL CHECKS PASSED           ║"
    else
        log "║  💥 $FAIL CRITICAL CHECK(S) FAILED        ║"
        log "║  Review failures above before proceeding  ║"
    fi
    log "╚══════════════════════════════════════════╝"
    log "  Full log: $LOG_FILE"
}

# ─── MAIN ─────────────────────────────────────────────────────────────────────
main() {
    log "═══════════════════════════════════════════════════════"
    log " Zero-Ops Post-Bootstrap Core Services Validation"
    log " Kubeconfig: $KUBECONFIG"
    log " SpokePool:  $SPOKEPOOL_NAME"
    log "═══════════════════════════════════════════════════════"

    preflight_check
    check_namespaces
    check_argocd
    check_crossplane
    check_eso
    check_infisical
    check_databases
    check_clickhouse
    check_openmeter
    check_ory
    check_nats
    check_hub_operator
    check_kube_sbt_api
    check_ingress
    check_spoke
    check_platform_argocd_apps
    check_observability

    print_summary

    if [[ "$FAIL" -gt 0 ]]; then
        exit 1
    fi
    exit 0
}

trap 'log "Script interrupted"; print_summary; exit 1' INT TERM

main "$@"

#!/usr/bin/env bash
# A kustomization or chart that does not build takes ArgoCD down a
# ComparisonError path which reports as "Unknown" rather than "Degraded" — easy
# to miss in a wall of sync output, and it needs no cluster to detect.
validate_manifest_renders() {
    section "Manifest renders"

    local targets=(
        "manifests/environments/dev"
        "manifests/environments/stg"
        "manifests/environments/prod"
        "manifests/providers/hybrid"
        "manifests/providers/hetzner"
        "manifests/providers/_shared"
        "manifests/spoke/spoke-catalog/infra"
        "manifests/hub-core-services/identity/hydra-maester"
        "manifests/hub-core-services/cluster-secret-store"
    )
    local t err
    for t in "${targets[@]}"; do
        if [[ ! -d "$VALIDATE_ROOT/$t" ]]; then
            hard_fail "kustomize target missing: $t"
            continue
        fi
        if err=$(kubectl kustomize "$VALIDATE_ROOT/$t" 2>&1 >/dev/null); then
            pass "kustomize builds: $t"
        else
            hard_fail "kustomize fails: $t — $(head -1 <<< "$err")"
        fi
    done

    local chart
    for chart in manifests/argocd/environment-manager manifests/tenants/charts/universal-tenant internal/opensbt/providers/gitops/helm-chart; do
        if err=$(helm template "$VALIDATE_ROOT/$chart" 2>&1 >/dev/null); then
            pass "helm renders: $chart"
        else
            hard_fail "helm fails: $chart — $(head -1 <<< "$err")"
        fi
    done
}

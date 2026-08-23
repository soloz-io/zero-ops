#!/usr/bin/env bash
# Hetzner project capacity, checked before anything is created (ADR-046 §23).
#
# A hub consumes one load balancer for its control plane, plus one more for every
# `type: LoadBalancer` Service still in the platform. Each spoke consumes another.
# The project ceiling is small and shared by every cluster in the account, so a run
# can be refused by resources that belong to a cluster nobody is looking at.
#
# When the ceiling is hit CAPH reports LoadBalancerCreateFailed on the HetznerCluster
# and the CAPI Cluster stays at Provisioning forever. That is indistinguishable from
# a slow provision unless you read the condition reason, which is why on 2026-08-23
# it was polled to timeout and diagnosed only from a live cluster.
#
# The zero-target detector below is the part that earns its place. A CCM-created
# load balancer is named a<service-uid> and carries exactly one label,
# hcloud-ccm/service-uid — nothing that ties it to a cluster. Teardown could not
# match it, and the CCM that alone could delete it dies with the cluster. Four such
# accumulated silently over four rebuilds and reported nothing wrong anywhere until
# they blocked the *next* provision.
#
# A CCM load balancer with no targets is one of two defects, and preflight runs
# without a cluster so it cannot tell them apart — deliberately, because both need
# action and neither is visible from inside Kubernetes:
#
#   leaked  — its Service is gone with a destroyed cluster. Delete it; it consumes
#             quota and is billed forever.
#   broken  — its Service is live but the CCM registered no backends, so the load
#             balancer accepts TCP and forwards nothing. Deleting it achieves
#             nothing; the CCM recreates it. Fix the cause (addendum 2: kubeadm's
#             exclude-from-external-load-balancers label on the control plane, and
#             home workers whose unmanaged:// providerID the CCM cannot target).
#
# Once §23 items 1–2 land, no platform Service creates a CCM load balancer at all
# and only the leaked case can occur.
validate_hetzner_capacity() {
    section "Hetzner project capacity (ADR-046 §23)"

    local token
    token=$(cat "$VALIDATE_ROOT/k8-secrets/hetzner/token" 2>/dev/null | tr -d '[:space:]')
    if [[ -z "$token" ]]; then
        warn "no k8-secrets/hetzner/token — skipping capacity check"
        return 0
    fi

    local body
    if ! body=$(curl -sf --max-time 20 -H "Authorization: Bearer $token" \
        'https://api.hetzner.cloud/v1/load_balancers' 2>/dev/null); then
        warn "hcloud API unreachable — skipping capacity check"
        return 0
    fi

    # Sole label hcloud-ccm/service-uid identifies a CCM-owned load balancer; zero
    # targets means it is serving nothing. Either way it consumes quota.
    local dead dead_count
    dead=$(jq -r '
        [ .load_balancers[]
          | select((.labels | keys | length) == 1)
          | select(.labels["hcloud-ccm/service-uid"] != null)
          | select((.targets | length) == 0)
          | "\(.name) (id \(.id), \(.location.name), created \(.created))"
        ] | .[]' <<<"$body" 2>/dev/null)
    dead_count=$(grep -c . <<<"$dead" || true)
    [[ -z "$dead" ]] && dead_count=0

    if (( dead_count > 0 )); then
        hard_fail "$dead_count CCM load balancer(s) with zero targets — leaked, or live with no backends"
        while IFS= read -r d; do [[ -n "$d" ]] && note "$d"; done <<<"$dead"
        note "check each against a live Service before deleting: a live one is recreated by the CCM"
        note "see ADR-046 §23 (leak) and addendum 2 (exclude-from-external-load-balancers)"
    else
        pass "no CCM load balancers with zero targets"
    fi

    local total
    total=$(jq '.load_balancers | length' <<<"$body")

    # HETZNER_LB_BUDGET is what this run will ask for: a hub bootstrap needs its own
    # control-plane LB, and provisioning a spoke needs one more. Callers that know
    # better override it.
    local budget="${HETZNER_LB_BUDGET:-2}"
    if (( total + budget > HETZNER_LB_QUOTA )); then
        hard_fail "load balancer quota: $total/${HETZNER_LB_QUOTA} in use, this run needs $budget more"
        note "CAPH will report LoadBalancerCreateFailed and the CAPI Cluster will never leave Provisioning"
    else
        pass "load balancer headroom: $total/${HETZNER_LB_QUOTA} used, $budget needed by this run"
    fi
}

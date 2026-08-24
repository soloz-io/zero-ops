#!/usr/bin/env bash
# Gateway API startup dependency contract (ADR-046 §24 class).
#
# cilium-operator checks for the Gateway API CRDs EXACTLY ONCE at start and
# disables Gateway API for the process lifetime if they are absent. Observed on a
# cold boot: operator up at 11:08:01, CRDs landed 11:20:53 via ArgoCD, and
# GatewayClass/cilium stayed Accepted=Unknown for hours — no listener served
# traffic, HTTPRoutes could not attach, AgentGateway never became ready. Nothing
# self-heals, because the operator never re-checks.
#
# The fix is a dependency gate on the operator (initContainer), not a delivery
# ordering trick. These checks assert the CONTRACT, not the mechanism, so they stay
# valid if delivery is recomposed later.
#
# This is a COLD-BOOT check. Run against a freshly bootstrapped cluster: on an
# already-running cluster criterion 2 passes trivially, because the CRDs have long
# since been established and the ordering it exists to prove is unobservable.

validate_gateway_api_startup_contract() {
    section "Gateway API startup dependency contract"

    if ! ensure_spoke_kubeconfig; then
        soft_fail "spoke unreachable — Gateway API startup contract not verified"
        return 0
    fi

    local required=(
        gatewayclasses gateways httproutes grpcroutes referencegrants
    )

    # 1. All five required CRDs Established=True.
    local missing=0
    for crd in "${required[@]}"; do
        local est
        est=$(kc_spoke get crd "${crd}.gateway.networking.k8s.io" \
                -o jsonpath='{.status.conditions[?(@.type=="Established")].status}' 2>/dev/null)
        if [[ "$est" != "True" ]]; then
            hard_fail "Gateway API CRD ${crd}.gateway.networking.k8s.io is not Established (got '${est:-absent}') — cilium-operator disables Gateway API when these are missing at start"
            missing=1
        fi
    done
    [[ $missing -eq 0 ]] && pass "all five required Gateway API CRDs are Established"

    # 2. The gate exists on the operator. Asserted on the workload rather than by
    #    inferring from timestamps: a cluster that has been up for hours cannot
    #    demonstrate the ordering, but it can demonstrate that the dependency is
    #    still encoded and would hold on the next cold boot.
    local gate
    gate=$(kc_spoke get deployment cilium-operator -n kube-system \
              -o jsonpath='{.spec.template.spec.initContainers[?(@.name=="wait-for-gateway-api-crds")].name}' 2>/dev/null)
    if [[ "$gate" == "wait-for-gateway-api-crds" ]]; then
        pass "cilium-operator carries the Gateway API dependency gate"
    else
        hard_fail "cilium-operator has no wait-for-gateway-api-crds initContainer — the startup race is unguarded and will recur on the next cold boot"
    fi

    # 2b. Ordering, when it is still observable. The operator container's start time
    #     must be at or after CRD establishment. On a long-running cluster the CRD
    #     timestamps predate everything and this is vacuous, so it is reported as a
    #     note rather than a pass to avoid manufacturing false confidence.
    local crd_est op_start
    crd_est=$(kc_spoke get crd gatewayclasses.gateway.networking.k8s.io \
                -o jsonpath='{.metadata.creationTimestamp}' 2>/dev/null)
    op_start=$(kc_spoke get pods -n kube-system -l io.cilium/app=operator \
                -o jsonpath='{.items[0].status.startTime}' 2>/dev/null)
    if [[ -n "$crd_est" && -n "$op_start" ]]; then
        if [[ "$op_start" < "$crd_est" ]]; then
            hard_fail "cilium-operator started ($op_start) BEFORE the Gateway API CRDs existed ($crd_est) — this is the exact race the gate exists to prevent"
        else
            note "operator start $op_start is at/after CRD creation $crd_est (only meaningful on a cold boot)"
        fi
    fi

    # 3 + 4. GatewayClass exists, is Accepted, and is owned by Cilium's controller.
    #        Accepted=Unknown is the signature of the disabled-at-startup state and
    #        is treated as failure, not as "still settling".
    local gc_accepted gc_controller
    gc_accepted=$(kc_spoke get gatewayclass cilium \
                     -o jsonpath='{.status.conditions[?(@.type=="Accepted")].status}' 2>/dev/null)
    gc_controller=$(kc_spoke get gatewayclass cilium \
                     -o jsonpath='{.spec.controllerName}' 2>/dev/null)
    if [[ -z "$gc_controller" ]]; then
        hard_fail "GatewayClass/cilium does not exist — no Gateway API implementation is registered"
    elif [[ "$gc_controller" != "io.cilium/gateway-controller" ]]; then
        hard_fail "GatewayClass/cilium controllerName is '$gc_controller', expected io.cilium/gateway-controller"
    elif [[ "$gc_accepted" != "True" ]]; then
        hard_fail "GatewayClass/cilium Accepted=${gc_accepted:-Unknown} — the controller is not reconciling it, which is what a missed CRD check looks like"
    else
        pass "GatewayClass/cilium Accepted=True, controller io.cilium/gateway-controller"
    fi

    # 5. A representative route actually attaches. The preceding checks can all pass
    #    while nothing routes, so this closes the loop on the user-visible outcome.
    local routes
    routes=$(kc_spoke get httproute -A -o json 2>/dev/null)
    if [[ -z "$routes" ]] || [[ "$(echo "$routes" | jq -r '.items | length' 2>/dev/null)" == "0" ]]; then
        note "no HTTPRoute present to verify attachment; skipping the route half of the contract"
        return
    fi
    local unaccepted
    unaccepted=$(echo "$routes" | jq -r '
        .items[]
        | select(([.status.parents[]?.conditions[]? | select(.type=="Accepted") | .status] | index("True")) | not)
        | "\(.metadata.namespace)/\(.metadata.name)"' 2>/dev/null)
    if [[ -n "$unaccepted" ]]; then
        hard_fail "HTTPRoute(s) not Accepted by any parent: $(echo "$unaccepted" | tr '\n' ' ')— the Gateway exists but routes nothing"
    else
        pass "every HTTPRoute is Accepted by a parent Gateway"
    fi
}

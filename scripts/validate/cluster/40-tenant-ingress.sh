#!/usr/bin/env bash
# The public tenant path under ADR-051 is:
#   browser -> Hetzner LB -> spoke Gateway -> AgentGateway (OIDC) -> workload
#
# Every hop below has a failure mode that reports Healthy while serving nothing,
# so each is asserted directly rather than inferred from pod status.
validate_tenant_ingress() {
    section "Tenant ingress path (ADR-050/051)"

    if ! ensure_spoke_kubeconfig; then
        soft_fail "spoke unreachable — tenant ingress path not verified"
        return 0
    fi

    # AgentGateway is the only thing enforcing OIDC in front of tenant workloads.
    local ag
    ag=$(kc_spoke get deployment agentgateway -n platform-ops -o jsonpath='{.status.readyReplicas}')
    if [[ "${ag:-0}" -ge 1 ]]; then
        pass "spoke AgentGateway has $ag ready replica(s)"
    else
        soft_fail "spoke AgentGateway not ready (readyReplicas=${ag:-0}) — nothing enforces OIDC in front of tenant workloads"
    fi

    _validate_external_dns_runtime
    _validate_gateway_programmed
    _validate_routes_accepted
    _validate_spoke_certificates
}

# external-dns publishes the tenant hostname. Its args are patched BY INDEX by the
# spoke-catalog ApplicationSet; an unapplied patch leaves PLACEHOLDER, and
# external-dns then runs healthy and publishes nothing at all — the hostname
# simply never resolves, with no unhealthy resource anywhere.
_validate_external_dns_runtime() {
    local args
    args=$(kc_spoke get deployment external-dns -n platform-ops \
        -o jsonpath='{.spec.template.spec.containers[0].args[0]} {.spec.template.spec.containers[0].args[1]}')

    if [[ -z "$args" ]]; then
        soft_fail "external-dns not deployed on the spoke — tenant hostnames are never published"
        return 0
    fi
    if [[ "$args" == *PLACEHOLDER* ]]; then
        hard_fail "external-dns args still contain PLACEHOLDER ($args) — the spoke-catalog index patch did not apply; it runs healthy and writes no records"
        return 0
    fi
    pass "external-dns args substituted per-spoke: $args"

    if [[ "$args" == *"--domain-filter=${ENV_ZONE}"* ]]; then
        pass "external-dns scoped to ${ENV_ZONE}"
    else
        hard_fail "external-dns --domain-filter is not ${ENV_ZONE} (got: $args) — this spoke can write another environment's DNS records"
    fi
}

# Accepted alone only means the config parsed. Programmed means an address was
# assigned and the listeners are actually live.
_validate_gateway_programmed() {
    local st
    st=$(kc_spoke get gateway -n platform-ops \
        -o jsonpath='{.items[0].status.conditions[?(@.type=="Programmed")].status}')
    if [[ "$st" == "True" ]]; then
        pass "spoke Gateway is Programmed (address assigned, listeners live)"
    else
        soft_fail "spoke Gateway not Programmed (status=${st:-none}) — no listener is serving tenant traffic"
    fi

    # ADR-050 hardening 5: a Gateway outside platform-ops lets a tenant publish a
    # route that reaches its workload without passing AgentGateway.
    local rogue
    rogue=$(kc_spoke get gateway -A \
        -o jsonpath='{range .items[*]}{.metadata.namespace}/{.metadata.name}{"\n"}{end}' \
        | grep -v '^platform-ops/' | grep -v '^$' || true)
    if [[ -z "$rogue" ]]; then
        pass "no Gateway outside platform-ops (OIDC policy cannot be bypassed)"
    else
        local g
        while IFS= read -r g; do
            hard_fail "Gateway $g is outside platform-ops — it bypasses AgentGateway's OIDC policy (ADR-050 hardening 5)"
        done <<< "$rogue"
    fi
}

# An HTTPRoute whose parent never accepted it is inert: it exists, ArgoCD reports
# it Synced, and it routes nothing.
_validate_routes_accepted() {
    local bad
    bad=$(kc_spoke get httproute -A -o json 2>/dev/null \
        | jq -r '.items[] | select([.status.parents[]?.conditions[]? | select(.type=="Accepted" and .status=="True")] | length == 0) | "\(.metadata.namespace)/\(.metadata.name)"' 2>/dev/null || true)
    if [[ -z "$bad" ]]; then
        pass "all HTTPRoutes accepted by their parent Gateway"
    else
        local r
        while IFS= read -r r; do
            [[ -z "$r" ]] && continue
            soft_fail "HTTPRoute $r not Accepted by any parent — it exists but routes nothing"
        done <<< "$bad"
    fi
}

# Without a Ready certificate the browser gets a TLS error, which no pod-level
# check ever surfaces.
_validate_spoke_certificates() {
    local bad
    bad=$(kc_spoke get certificate -A --no-headers 2>/dev/null | awk '$3 != "True" {print $1"/"$2}' || true)
    if [[ -z "$bad" ]]; then
        pass "all spoke certificates Ready"
    else
        local c
        while IFS= read -r c; do
            [[ -z "$c" ]] && continue
            soft_fail "spoke certificate not Ready: $c — the browser gets a TLS error"
        done <<< "$bad"
    fi
}

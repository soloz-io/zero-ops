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
    #
    # ONE PER TENANT, in the tenant's own namespace. This asserted a single
    # deployment named "agentgateway" in platform-ops until 2026-09-02, which
    # d3b7303d had already replaced: that commit moved the gateway to one
    # instance per tenant rendered by the universal-tenant chart, deleted the
    # shared one, and updated 80-ingress-topology.sh and the tenant-identifier
    # validator — but not this. So it read readyReplicas of a deployment that no
    # longer exists, reported 0, and failed a fleet whose gateway was running
    # fine (agentgateway-waypoint, 1/1, for two and a half hours).
    #
    # Enumerating by label rather than by name means adding a tenant does not
    # need another edit here, and a tenant whose gateway is genuinely down is
    # still caught.
    local ag_json total ready
    ag_json=$(kc_spoke get deployment -A -l app.kubernetes.io/name=agentgateway -o json 2>/dev/null)
    total=$(printf '%s' "$ag_json" | jq -r '[.items[]?] | length' 2>/dev/null)
    if [[ "${total:-0}" -eq 0 ]]; then
        # Fall back to a name prefix: the chart may not carry the label.
        ag_json=$(kc_spoke get deployment -A -o json 2>/dev/null \
            | jq '{items: [.items[]? | select(.metadata.name|startswith("agentgateway"))]}')
        total=$(printf '%s' "$ag_json" | jq -r '[.items[]?] | length' 2>/dev/null)
    fi

    if [[ "${total:-0}" -eq 0 ]]; then
        soft_fail "no AgentGateway deployment found on the spoke — nothing enforces OIDC in front of tenant workloads"
    else
        local notready
        notready=$(printf '%s' "$ag_json" | jq -r '[.items[]? | select((.status.readyReplicas // 0) < 1) | .metadata.namespace + "/" + .metadata.name] | join(", ")')
        if [[ -z "$notready" ]]; then
            pass "all $total tenant AgentGateway(s) ready"
        else
            soft_fail "AgentGateway not ready: $notready — nothing enforces OIDC in front of those tenants' workloads"
        fi
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

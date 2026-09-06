#!/usr/bin/env bash
# ADR-051: public tenant traffic terminates on the SPOKE, not the hub.
#
# Two gateways claiming one hostname is a split-brain that resolves by whichever
# DNS record happens to win — untestable, and it fails differently on each
# lookup. The hub must therefore claim no tenant hostname at all.
#
# ADR-050 hardening 5: the Gateway and the hostname HTTPRoute are platform-owned,
# so a tenant cannot author a route that reaches its workload without passing
# AgentGateway's OIDC policy.
validate_ingress_topology() {
    section "ADR-051 spoke-terminated ingress"

    local hub_gw="$VALIDATE_ROOT/manifests/hub-core-services/api-gateway/agentgateway-config.yaml"
    if grep -qE "waypoint(\.[a-z]+)*\.nutgraf\.in" "$hub_gw"; then
        hard_fail "hub agentgateway-config still claims a waypoint hostname — ADR-051 terminates tenant ingress on the spoke"
    else
        pass "hub agentgateway-config claims no tenant hostnames"
    fi

    local kust="$VALIDATE_ROOT/manifests/spoke/spoke-catalog/infra/kustomization.yaml"
    local f
    # agentgateway.yaml / agentgateway-config.yaml are deliberately ABSENT here.
    # d3b7303d replaced the single spoke-wide gateway with one per tenant,
    # rendered by manifests/tenants/charts/universal-tenant (asserted below), so
    # requiring the spoke-catalog to deliver them asserted the pre-d3b7303d
    # topology against a tree that no longer has the files.
    for f in tenant-gateway.yaml external-dns.yaml; do
        if grep -q "  - $f" "$kust"; then
            pass "spoke-catalog delivers $f"
        else
            hard_fail "spoke-catalog kustomization.yaml does not list $f — it is never applied"
        fi
    done

    local tenant_gw="$VALIDATE_ROOT/manifests/tenants/charts/universal-tenant/templates/agentgateway.yaml"
    if [[ -f "$tenant_gw" ]]; then
        pass "universal-tenant chart renders the per-tenant AgentGateway"
    else
        hard_fail "universal-tenant chart does not render agentgateway.yaml — no tenant gets a gateway"
    fi

    if (cd "$VALIDATE_ROOT" && python3 - <<'PY'
import sys, yaml
found = False
for doc in yaml.safe_load_all(open("manifests/spoke/spoke-catalog/infra/tenant-gateway.yaml")):
    if not doc or doc.get("kind") != "Gateway":
        continue
    ns = doc["metadata"].get("namespace")
    for l in doc["spec"].get("listeners", []):
        frm = l.get("allowedRoutes", {}).get("namespaces", {}).get("from")
        if ns != "platform-ops" or frm != "Same":
            sys.exit(1)
        found = True
sys.exit(0 if found else 1)
PY
    ); then
        pass "tenant Gateway is platform-owned (platform-ops) with allowedRoutes.from=Same"
    else
        hard_fail "tenant Gateway must be in platform-ops with allowedRoutes.namespaces.from=Same, else a tenant can bind a route that bypasses the OIDC policy"
    fi
}

# ADR-051: every public hostname carries the environment label. A bare-zone
# hostname in a dev manifest points dev traffic at prod DNS.
#
# In-cluster *.svc.cluster.local URLs are deliberately NOT zoned — each
# environment is a separate cluster, so the service DNS name is identical in all
# of them. Only public hostnames are checked.
validate_hostname_zoning() {
    section "Public hostnames carry the environment label"

    [[ "$ENVIRONMENT" == "prod" ]] && { pass "prod uses the un-prefixed zone — nothing to check"; return 0; }

    local double
    double=$(cd "$VALIDATE_ROOT" && grep -rn "${ENVIRONMENT}\.${ENVIRONMENT}\.nutgraf\.in" manifests/ 2>/dev/null || true)
    if [[ -z "$double" ]]; then
        pass "no double-zoned hostnames (${ENVIRONMENT}.${ENVIRONMENT}.nutgraf.in)"
    else
        while IFS= read -r line; do hard_fail "double-zoned hostname — $line"; done <<< "$double"
    fi

    local unzoned
    unzoned=$(cd "$VALIDATE_ROOT" && grep -rnoE "https?://[a-z0-9-]+\.nutgraf\.in" \
        manifests/spoke/spoke-catalog/infra/agentgateway-config.yaml 2>/dev/null || true)
    if [[ -z "$unzoned" ]]; then
        pass "identity + gateway hostnames are all environment-zoned"
    else
        while IFS= read -r line; do hard_fail "un-zoned public hostname — $line"; done <<< "$unzoned"
    fi
}

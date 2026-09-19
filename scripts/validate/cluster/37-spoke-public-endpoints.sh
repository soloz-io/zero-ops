#!/usr/bin/env bash
# Every workload cluster's query endpoint answers over its own public hostname.
#
# ADR-083 decision 3 gives each spoke ONE authenticated public endpoint pair --
# the query APIs of its metrics and logs stores -- and the ADR says plainly that
# this is "the one new network surface the topology creates". A surface nobody
# probes is a surface nobody knows the state of.
#
# SEPARATE FROM 35-public-api-endpoints.sh, deliberately. That module's hosts all
# derive from the zone alone, because every one of them belongs to the box. These
# belong to a CLUSTER: ADR-051's 2026-09-19 amendment makes them
# <service>.<cluster>.<subdomain>.<domain>, so the list cannot be written down in
# advance -- it is one entry per workload cluster the box has declared, and the
# cluster names are the tenant's (ADR-082). Folding them into the static table
# would mean either hard-coding a tenant's cluster names into the platform, which
# ADR-063 forbids, or a table that is silently wrong for every box but one.
#
# What is asserted, and what is deliberately NOT:
#
#   * The name resolves, TLS terminates on a chain a browser would accept, and
#     the endpoint answers. Those are properties of the platform.
#   * An UNAUTHENTICATED request is REFUSED. This is the security property the
#     whole design rests on: ADR-083 addendum 1 §1 records that mTLS is
#     unavailable at every layer this platform can reach and that the Gateway
#     accepts the field while Cilium ignores it -- "a Gateway carrying
#     AllowValidOnly reports Accepted=True, raises no condition, and serves the
#     store to the internet with no certificate required". A check that only
#     confirmed the endpoint answers would pass on exactly that hole.
#   * NOT whether an authenticated request succeeds. That needs a token from the
#     box's issuer, which is a credential this module has no business holding.

validate_spoke_public_endpoints() {
    section "Each workload cluster's query endpoint is published and closed (ADR-083)"
    require_zone || return 0

    local zone="$ENV_ZONE"

    # The clusters this box has declared, from the ArgoCD cluster registry --
    # the same inventory the fleet ApplicationSets generate from, so this check
    # and the delivery it verifies cannot disagree about which clusters exist.
    local -a cells=()
    while IFS= read -r c; do [[ -n "$c" ]] && cells+=("$c"); done < <(
        kc get secrets -n platform-ops -l argocd.argoproj.io/secret-type=cluster \
            -o jsonpath='{range .items[*]}{.metadata.labels.spoke-type}{"|"}{.data.name}{"\n"}{end}' 2>/dev/null \
        | awk -F'|' '$1=="pool"{print $2}' \
        | while read -r b64; do echo "$b64" | base64 -d 2>/dev/null; echo; done
    )

    if (( ${#cells[@]} == 0 )); then
        note "this box declares no workload cluster; nothing to probe"
        return 0
    fi

    local cell host code
    for cell in "${cells[@]}"; do
        host="victoriametrics.${cell}.${zone}"

        # 1. Does the name resolve at all? external-dns publishes it from the
        #    spoke's own Gateway, so an NXDOMAIN means the record was never
        #    written -- not that the endpoint is down.
        if ! dig +short +time=3 +tries=1 A "$host" 2>/dev/null | grep -qE '^[0-9]'; then
            warn "$cell: $host does not resolve"
            note "the spoke publishes this from its own Gateway; check external-dns on that cluster"
            continue
        fi

        # 2. Does it answer over TLS a browser would accept? No -k: the
        #    certificate chain is part of what is being checked, and this
        #    endpoint is reachable from the internet.
        code=$(curl -s -o /dev/null -m 15 -w '%{http_code}' "https://${host}/prometheus/api/v1/query?query=up" 2>/dev/null)
        if [[ "$code" == "000" ]]; then
            warn "$cell: nothing accepts TLS at https://${host}"
            note "the name resolves, so DNS is published and the listener is not serving"
            continue
        fi

        # 3. And is it CLOSED? An unauthenticated query must not return data.
        #    401 or 403 is the endpoint working. 200 means this cluster's entire
        #    telemetry store is readable by anyone who knows the hostname.
        case "$code" in
            401|403)
                pass "$cell: $host answers and refuses an unauthenticated query (HTTP $code)"
                ;;
            200)
                hard_fail "$cell: $host served an UNAUTHENTICATED query (HTTP 200)"
                note "ADR-083 decision 3: this endpoint is internet-reachable and authenticates by"
                note "OIDC against the box's own issuer. A 200 here means vmauth is not verifying,"
                note "and every series this cluster holds is public."
                ;;
            *)
                warn "$cell: $host returned HTTP $code; expected 401 or 403 unauthenticated"
                ;;
        esac
    done
}

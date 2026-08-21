#!/usr/bin/env bash
# hydra-maester reconciles OAuth2Client CRs into Hydra. A failed registration is
# reported only in the CR status; the resulting login failure appears in the
# browser with nothing unhealthy anywhere in the cluster.
#
# Preflight already proved the redirect URIs are internally consistent. This
# proves hydra-maester actually got them into Hydra.
validate_oauth_clients() {
    section "OAuth client registration (hydra-maester)"

    if ! kc get crd oauth2clients.hydra.ory.sh >/dev/null 2>&1; then
        soft_fail "OAuth2Client CRD absent — hydra-maester is not installed, so no OAuth client is registered"
        return 0
    fi

    local clients
    clients=$(kc get oauth2clients -A -o jsonpath='{range .items[*]}{.metadata.namespace}/{.metadata.name}{"\n"}{end}')
    if [[ -z "$clients" ]]; then
        soft_fail "no OAuth2Client resources present — login has no registered client"
        return 0
    fi

    local c ns name err
    while IFS= read -r c; do
        [[ -z "$c" ]] && continue
        ns="${c%%/*}"; name="${c##*/}"
        err=$(kc get oauth2client "$name" -n "$ns" -o jsonpath='{.status.reconciliationError.description}')
        if [[ -z "$err" ]]; then
            pass "OAuth2Client $c registered in Hydra"
        else
            hard_fail "OAuth2Client $c not registered: $err — login via this client fails in the browser only"
        fi
    done <<< "$clients"
}

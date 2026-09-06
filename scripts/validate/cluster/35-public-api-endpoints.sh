#!/usr/bin/env bash
# The platform's public APIs answer, over the path a real client takes.
#
# Everything else in this suite reads Kubernetes objects, and a workload can be
# Running with its Service, HTTPRoute and Certificate all Healthy while the
# hostname a user types answers nothing. Every hop between them is a separate
# thing that breaks on its own: public DNS, the Gateway listener, the ACME
# certificate, the HTTPRoute's hostname match, and the backend itself.
#
# So these checks resolve the public name and speak the protocol, rather than
# asking the cluster whether it believes it is healthy. Two failures observed on
# this platform were invisible to every object-level check:
#
#   - Zitadel is MULTI-INSTANCE and resolves which instance serves a request from
#     the request's origin. Addressed by its in-cluster Service name it answers
#     404 with a plain-text body, so a JWKS consumer fails while PARSING
#     ("expected value at line 1 column 1") rather than while connecting. Only a
#     request carrying the public host proves the issuer is usable.
#
#   - An OIDC discovery document must state an issuer that equals, byte for byte,
#     the URL relying parties were configured with. A mismatch is rejected as a
#     spoofed provider at login, long after every pod reports Ready.
#
# Endpoints are derived from ENV_ZONE, the same way internal/hub-cli/bootstrap/
# hubdomain.go derives them, so this cannot drift from what the platform deploys.

# _probe URL [HOST_HEADER] -> body, then a final line holding the HTTP code.
#
# The body is NOT truncated. An earlier version capped it with `tail -c 2048`,
# which cut an OIDC discovery document (~2.4KB) mid-JSON: the parse then yielded
# an empty issuer and the check reported a byte-for-byte mismatch against a
# document that was in fact correct. A validator that manufactures its own
# failures is worse than no validator.
#
# --resolve is deliberately NOT used: resolving the name through public DNS is
# part of what is being checked.
_probe() {
    local url="$1" host="${2:-}" args=(-s -m 12 -o /dev/stdout -w '\n%{http_code}')
    [[ -n "$host" ]] && args+=(-H "Host: $host")
    curl "${args[@]}" "$url" 2>/dev/null
}

_code() { tail -1 <<< "$1"; }
_body() { sed '$d' <<< "$1"; }

# A public hostname resolves in DNS at all. Split out because "no A record" and
# "nothing listening" need different remedies, and a bare connection failure does
# not distinguish them.
_resolves() {
    [[ -n "$(dig +short +time=3 +tries=1 "$1" 2>/dev/null | head -1)" ]]
}

validate_public_api_endpoints() {
    section "Public APIs answer over their published hostnames"

    local id_host="id.${ENV_ZONE}"
    local auth_host="auth.${ENV_ZONE}"
    local infisical_host="infisical.${ENV_ZONE}"

    # ── The issuer's discovery document ──────────────────────────────────────
    #
    # Checked first because everything else in the identity path is derived from
    # it, and because its `issuer` field is the one value a relying party
    # compares byte-for-byte.
    if ! _resolves "$id_host"; then
        soft_fail "$id_host does not resolve — external-dns has not published the issuer's record"
    else
        local disc code
        disc=$(_probe "https://${id_host}/.well-known/openid-configuration")
        code=$(_code "$disc")
        if [[ "$code" != "200" ]]; then
            soft_fail "OIDC discovery at https://${id_host}/.well-known/openid-configuration returned HTTP ${code:-no-response} — the issuer is not serving"
        else
            local stated
            stated=$(_body "$disc" | python3 -c "import json,sys; print(json.load(sys.stdin).get('issuer',''))" 2>/dev/null)
            if [[ "$stated" == "https://${id_host}" ]]; then
                pass "OIDC discovery served, issuer states https://${id_host}"
            else
                # Not soft: this cannot converge on its own, and it breaks every
                # login while the cluster reports itself entirely healthy.
                hard_fail "OIDC discovery states issuer ${stated:-<empty>} but is served at https://${id_host} — relying parties compare this byte-for-byte and reject the mismatch as a spoofed provider"
            fi
        fi

        # ── The signing keys, at the path the issuer actually publishes ──────
        local jwks jcode nkeys
        jwks=$(_probe "https://${id_host}/oauth/v2/keys")
        jcode=$(_code "$jwks")
        nkeys=$(_body "$jwks" | python3 -c "import json,sys; print(len(json.load(sys.stdin).get('keys',[])))" 2>/dev/null || echo 0)
        if [[ "$jcode" == "200" && "${nkeys:-0}" -gt 0 ]]; then
            pass "JWKS served at https://${id_host}/oauth/v2/keys ($nkeys key(s))"
        else
            soft_fail "JWKS at https://${id_host}/oauth/v2/keys returned HTTP ${jcode:-no-response} with ${nkeys:-0} key(s) — tokens cannot be validated by any consumer"
        fi

        # The conventional path MUST NOT be assumed to work. A consumer that
        # appends /.well-known/jwks.json to the issuer gets a 404 whose body is
        # not JSON, and fails while parsing rather than while fetching.
        local conv
        conv=$(_code "$(_probe "https://${id_host}/.well-known/jwks.json")")
        if [[ "$conv" == "200" ]]; then
            note "the conventional /.well-known/jwks.json also answers on this issuer"
        else
            note "conventional /.well-known/jwks.json returns HTTP ${conv:-no-response}, as expected — consumers must be given the JWKS URL explicitly, never assemble it"
        fi
    fi

    # ── auth.<zone>: the machine surfaces that stay on auth-proxy ────────────
    #
    # The Gateway splits this hostname: /.well-known/ and /internal/ are served,
    # everything else redirects to the issuer. The split is the thing being
    # verified — a redirect on a machine surface breaks OAuth clients silently.
    if ! _resolves "$auth_host"; then
        soft_fail "$auth_host does not resolve — the OAuth metadata surface is unreachable"
    else
        local meta mcode
        meta=$(_probe "https://${auth_host}/.well-known/oauth-authorization-server")
        mcode=$(_code "$meta")
        if [[ "$mcode" == "200" ]]; then
            local adv
            adv=$(_body "$meta" | python3 -c "import json,sys; print(json.load(sys.stdin).get('issuer',''))" 2>/dev/null)
            if [[ "$adv" == "https://${id_host}" ]]; then
                pass "authorization-server metadata served on ${auth_host}, advertising the issuer"
            else
                hard_fail "authorization-server metadata on ${auth_host} advertises issuer ${adv:-<empty>}, not https://${id_host} — an MCP client discovering this rejects every token the issuer mints"
            fi
        else
            soft_fail "authorization-server metadata at https://${auth_host}/.well-known/oauth-authorization-server returned HTTP ${mcode:-no-response}"
        fi

        # A machine surface must be SERVED, never redirected. 3xx here means the
        # catch-all rule is matching a path it should not.
        local wk
        wk=$(_code "$(_probe "https://${auth_host}/.well-known/openid-configuration")")
        case "$wk" in
            200) pass "auth.${ENV_ZONE}/.well-known/ is served, not redirected" ;;
            3??) hard_fail "auth.${ENV_ZONE}/.well-known/openid-configuration returned HTTP $wk — a machine surface is being redirected; the browser catch-all is matching it and OAuth clients cannot follow" ;;
            *)   soft_fail "auth.${ENV_ZONE}/.well-known/openid-configuration returned HTTP ${wk:-no-response}" ;;
        esac
    fi

    # ── Infisical's API ─────────────────────────────────────────────────────
    #
    # Every ExternalSecret in the platform resolves through it, and the issuers
    # that sign internal TLS authenticate to it. When it is unreachable publicly
    # the symptom appears as unrelated components failing to start.
    if ! _resolves "$infisical_host"; then
        soft_fail "$infisical_host does not resolve — the secret backend has no public record"
    else
        local ist icode
        ist=$(_probe "https://${infisical_host}/api/status")
        icode=$(_code "$ist")
        if [[ "$icode" == "200" ]]; then
            local ok
            ok=$(_body "$ist" | python3 -c "
import json,sys
d=json.load(sys.stdin)
print(d.get('date') or d.get('message') or 'ok')
" 2>/dev/null)
            pass "Infisical API answers at https://${infisical_host}/api/status (${ok:-ok})"
        else
            soft_fail "Infisical API at https://${infisical_host}/api/status returned HTTP ${icode:-no-response} — ExternalSecrets and the internal issuers depend on it"
        fi
    fi
}

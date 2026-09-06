#!/usr/bin/env bash

PUBLIC_ENDPOINTS=(
  # host                 | path                        | expect              | plaintext | label
  "id.{zone}             | /.well-known/openid-configuration | 200           | yes       | issuer discovery"
  "id.{zone}             | /oauth/v2/keys              | 200                 | no        | issuer JWKS"
  "id.{zone}             | /ui/v2/login/loginname      | 200 302 303 400     | no        | issuer login UI"
  "auth.{zone}           | /.well-known/oauth-authorization-server | 200      | yes       | auth metadata"
  "auth.{zone}           | /.well-known/openid-configuration | 200           | no        | auth discovery passthrough"
  "api.{zone}            | /                           | 200 401 403 404     | yes       | api gateway"
  "infisical.{zone}      | /api/status                 | 200                 | yes       | infisical api"
  "console.{zone}        | /                           | 200 302 303 404     | yes       | console"
  "argocd.{zone}         | /healthz                    | 200                 | yes       | argocd"
  "dashboard.{zone}      | /                           | 200 302 401         | yes       | headlamp"
  "mail.{apex}           | /                           | 200 302 401 404     | yes       | mail"
  "smtp.{apex}           | /                           | 200 302 401 404     | yes       | smtp"
)

# ─── Probes ──────────────────────────────────────────────────────────────────

# _probe URL -> body, then a final line holding the HTTP code.
#
# The body is NOT truncated. An earlier version capped it with `tail -c 2048`,
# which cut an OIDC discovery document (~2.4KB) mid-JSON: the parse then yielded
# an empty issuer and the check reported a byte-for-byte mismatch against a
# document that was in fact correct. A validator that manufactures its own
# failures is worse than no validator.
#
# --resolve is deliberately NOT used, and neither is -k: resolving the name
# through public DNS and validating the certificate chain are both part of what
# is being checked.
_probe() { curl -s -m 15 -o /dev/stdout -w '\n%{http_code}' "$1" 2>/dev/null; }
_probe_final() { curl -s -L -m 20 -o /dev/null -w '%{http_code} %{url_effective}' "$1" 2>/dev/null; }
_code() { tail -1 <<< "$1"; }
_body() { sed '$d' <<< "$1"; }
_json_field() { python3 -c "import json,sys; print(json.load(sys.stdin).get('$1',''))" 2>/dev/null; }
_field() { awk -F'|' -v n="$2" '{gsub(/^[ \t]+|[ \t]+$/,"",$n); print $n}' <<< "$1"; }

validate_public_api_endpoints() {
    section "Public endpoints answer over their published hostnames"

    local zone="$ENV_ZONE"
    local apex="${zone#*.}"
    [[ "$zone" != *.*.* ]] && apex="$zone"

    local row host path expect plaintext label url code seen_hosts=""
    for row in "${PUBLIC_ENDPOINTS[@]}"; do
        host=$(_field "$row" 1); path=$(_field "$row" 2)
        expect=$(_field "$row" 3); plaintext=$(_field "$row" 4); label=$(_field "$row" 5)
        host="${host//\{zone\}/$zone}"; host="${host//\{apex\}/$apex}"
        url="https://${host}${path}"

        # DNS and TLS are asserted ONCE per host, not once per row, so a broken
        # hostname is reported as one fault rather than as many.
        if [[ " $seen_hosts " != *" $host "* ]]; then
            seen_hosts="$seen_hosts $host"

            if [[ -z "$(dig +short +time=3 +tries=1 "$host" 2>/dev/null | head -1)" ]]; then
                hard_fail "$label: $host does not resolve — external-dns has not published its record"
                continue
            fi

            # TLS separately from HTTP, so an expired or wrong-SAN certificate is
            # named as such rather than surfacing as a bare connection failure.
            if [[ "$(echo | openssl s_client -servername "$host" -connect "$host:443" -verify_return_error 2>&1 | grep -cE 'Verify return code: 0 \(ok\)')" -eq 0 ]]; then
                hard_fail "$label: TLS to https://$host failed certificate verification — the ACME certificate is missing, expired, or does not cover this name"
                continue
            fi

            # Plaintext must never serve content. Every listener is fronted by an
            # http-to-https redirect; a non-3xx here means one hostname answers
            # unencrypted, which no object-level check sees.
            if [[ "$plaintext" == "yes" ]]; then
                code=$(_code "$(_probe "http://${host}/")")
                case "$code" in
                    3??) pass "$label: http://${host} redirects to HTTPS (HTTP $code)" ;;
                    *)   hard_fail "$label: http://${host} returned HTTP ${code:-no-response}, expected a 3xx redirect — plaintext must never serve content" ;;
                esac
            fi
        fi

        code=$(_code "$(_probe "$url")")
        if [[ " $expect " == *" $code "* ]]; then
            pass "$label: $url → HTTP $code"
        else
            hard_fail "$label: $url returned HTTP ${code:-no-response}, expected one of [$expect]"
        fi
    done

    # ─── Contracts the status code alone cannot express ──────────────────────

    local id_host="id.${zone}" auth_host="auth.${zone}"

    # The issuer must NAME itself as the URL it is served at. Relying parties
    # compare this byte-for-byte and reject a mismatch as a spoofed provider —
    # at login, long after every pod reports Ready.
    local disc stated advertised jwks nkeys
    disc=$(_probe "https://${id_host}/.well-known/openid-configuration")
    if [[ "$(_code "$disc")" == "200" ]]; then
        stated=$(_body "$disc" | _json_field issuer)
        if [[ "$stated" == "https://${id_host}" ]]; then
            pass "issuer identity: discovery states https://${id_host}"
        else
            hard_fail "issuer identity: discovery states ${stated:-<empty>} but is served at https://${id_host} — relying parties reject the mismatch as a spoofed provider"
        fi

        # The advertised jwks_uri must itself work: a consumer takes this value
        # verbatim, so advertising an unreachable path is broken even though the
        # document parsed.
        advertised=$(_body "$disc" | _json_field jwks_uri)
        jwks=$(_probe "$advertised")
        nkeys=$(_body "$jwks" | python3 -c "import json,sys; print(len(json.load(sys.stdin).get('keys',[])))" 2>/dev/null || echo 0)
        if [[ "$(_code "$jwks")" == "200" && "${nkeys:-0}" -gt 0 ]]; then
            pass "issuer keys: advertised jwks_uri serves $nkeys key(s)"
        else
            hard_fail "issuer keys: advertised jwks_uri ${advertised:-<empty>} returned HTTP $(_code "$jwks") with ${nkeys:-0} key(s) — no consumer can validate a token"
        fi
    fi

    # auth.<zone> is SPLIT: /.well-known/ and /internal/ are served by
    # auth-proxy, everything else redirects to the issuer. Both halves are
    # asserted — a redirect on a machine surface breaks OAuth clients, which
    # cannot follow one on a metadata or token endpoint.
    local meta adv browser
    meta=$(_probe "https://${auth_host}/.well-known/oauth-authorization-server")
    if [[ "$(_code "$meta")" == "200" ]]; then
        adv=$(_body "$meta" | _json_field issuer)
        if [[ "$adv" == "https://${id_host}" ]]; then
            pass "auth metadata: advertises the issuer https://${id_host}"
        else
            hard_fail "auth metadata: advertises issuer ${adv:-<empty>}, not https://${id_host} — an MCP client discovering this rejects every token the issuer mints"
        fi
    fi

    browser=$(_probe_final "https://${auth_host}/")
    if [[ "$browser" == *"${id_host}"* ]]; then
        pass "auth split: browser traffic redirects to the issuer ($browser)"
    else
        hard_fail "auth split: browser traffic on ${auth_host} ended at [$browser], not ${id_host} — the machine/browser split is not in effect"
    fi
}

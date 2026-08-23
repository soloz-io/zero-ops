#!/usr/bin/env bash
# The kube-apiserver's LB listen port must not collide with a declared
# extraServices listenPort (ADR-046 §25.1).
#
# CAPH publishes the apiserver on the control-plane load balancer at
# `controlPlaneEndpoint.port`. A Hetzner LB cannot carry two services on one
# listen_port, so an extraServices entry claiming that same port is silently
# dropped: no error, no event, no condition — `LoadBalancerReady=True` and
# `HetznerCluster Ready=True` throughout.
#
# This is not hypothetical. The spoke ClusterClass declared 443->443 for tenant
# HTTPS while `controlPlaneEndpoint.port` was also 443. The LB served
# `listen 443 -> dest 6443` (the apiserver) and tenant HTTPS was dead from §17.4
# until §25 — `openssl s_client` against the LB returned CN=kube-apiserver.
# Every status object read healthy for the entire period, which is precisely why
# this needs a static check rather than a runtime one.
#
# Logic lives in the sibling .py: the repo has already hit bash quoting failures
# embedding YAML-parsing Python in heredocs (see 96-spoke-secret-authz.sh).
validate_lb_listen_port_collision() {
    section "Control-plane LB listen ports do not collide (ADR-046 §25.1)"

    local out
    out=$(cd "$VALIDATE_ROOT" && python3 "$VALIDATE_DIR/preflight/26-lb-listen-port-collision.py") || true

    if [[ -z "$out" ]]; then
        hard_fail "LB listen-port collision check produced no output"
        return 0
    fi

    local line
    while IFS= read -r line; do
        [[ -z "$line" ]] && continue
        case "$line" in
            OK*)  pass "$(printf '%s' "$line" | cut -f2-)" ;;
            BAD*) hard_fail "$(printf '%s' "$line" | cut -f2-)" ;;
            *)    note "$line" ;;
        esac
    done <<< "$out"
}

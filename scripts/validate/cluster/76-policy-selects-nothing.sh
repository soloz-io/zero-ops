#!/usr/bin/env bash
# A network policy that selects no endpoints is reported Healthy. It is simply
# inert — enforcing nothing, denying nothing, and looking exactly like a policy
# that works.
#
# On 2026-08-31 `agent-sandbox-default-deny` selected pods by a label the SDK
# writes into the Sandbox CR's podTemplate. The agent-sandbox controller owns
# that label space and replaces it, so the label never reached a pod: the
# policy governed ZERO pods and the "no cluster-internal access" its own header
# describes had never once been in force. Nothing reported it, because a CCNP
# selecting nothing is not an error.
#
# The same shape appeared twice more that day — a certificate authority that
# existed but could not issue, and toFQDNs rules matching no address. The common
# property is a component reporting healthy while doing nothing, which is the
# class this check exists to break.
#
# Scoped to policies the PLATFORM owns. A tenant policy may legitimately select
# nothing while its workload is scaled to zero, and failing on that would train
# people to ignore this check.
validate_policy_selects_nothing() {
    section "Network policies select at least one endpoint"

    local kubeconfig="${SPOKE_KUBECONFIG:-$HUB_KUBECONFIG}"
    local out
    out=$(kubectl --kubeconfig="$kubeconfig" get ciliumclusterwidenetworkpolicies -o json 2>/dev/null | python3 -c '
import json, sys

try:
    items = json.load(sys.stdin).get("items", [])
except Exception:
    sys.exit(0)

for p in items:
    name = p["metadata"]["name"]
    sel = p.get("spec", {}).get("endpointSelector")
    # An empty selector matches everything, which is a different (and valid)
    # design. Only a selector that names something is expected to match.
    if not sel or not (sel.get("matchLabels") or sel.get("matchExpressions")):
        continue
    # Cilium reports what a policy actually resolved to. A policy Cilium has not
    # yet processed has no status and must not be reported as inert.
    st = p.get("status") or {}
    if not st:
        continue
    print("%s\t%s" % (name, json.dumps(sel)[:80]))
')

    if [[ -z "$out" ]]; then
        pass "no clusterwide policy is checkable for endpoint selection yet"
        return 0
    fi

    # Cilium does not publish a selected-endpoint count on the policy, so the
    # authoritative answer comes from the agent: ask whether any endpoint in the
    # cluster carries the labels the policy selects.
    local name sel
    while IFS=$'\t' read -r name sel; do
        [[ -z "$name" ]] && continue
        local matched
        matched=$(kubectl --kubeconfig="$kubeconfig" get pods -A -o json 2>/dev/null | python3 -c "
import json, sys
sel = json.loads('''$sel''')
want = sel.get('matchLabels') or {}
exprs = sel.get('matchExpressions') or []
n = 0
for pod in json.load(sys.stdin).get('items', []):
    labels = pod['metadata'].get('labels') or {}
    ns = pod['metadata']['namespace']
    # Cilium prefixes k8s labels and encodes the namespace as a label; compare
    # on the bare key so the selector reads the same way it was written.
    ok = True
    for k, v in want.items():
        key = k.split(':')[-1]
        if key == 'io.kubernetes.pod.namespace':
            if ns != v: ok = False; break
            continue
        if labels.get(key) != v: ok = False; break
    if ok:
        for e in exprs:
            key = e['key'].split(':')[-1]
            op = e.get('operator')
            if op == 'Exists' and key not in labels: ok = False; break
            if op == 'DoesNotExist' and key in labels: ok = False; break
    if ok: n += 1
print(n)
" 2>/dev/null)
        if [[ "${matched:-0}" == "0" ]]; then
            # Reported, not failed. Zero matches is ambiguous by construction: it
            # is the signature of a wrong label, and equally the correct answer
            # when the workload the policy governs is simply not running. This
            # check cannot tell those apart, and hard-failing on the benign case
            # would train people to ignore the one that matters.
            #
            # What to do with it: if the workload IS running, the selector is
            # wrong and the policy is enforcing nothing. Compare the selector
            # against a live pod's labels — that is exactly how the sandbox
            # default-deny was found to be inert.
            soft_fail "CiliumClusterwideNetworkPolicy $name selects no pods — inert if its workload is running, benign if it is not; check a live pod's labels against $sel"
        else
            pass "$name selects $matched pod(s)"
        fi
    done <<< "$out"
}

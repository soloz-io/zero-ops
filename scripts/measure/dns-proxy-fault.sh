#!/usr/bin/env bash
# Reproduce Cilium's DNS-proxy fault in a scratch namespace, off any tenant.
#
# THE FAULT
#
# `toFQDNs` rules are enforced against a name-to-IP cache Cilium builds by
# proxying the workload's own DNS queries. On this fleet that proxy receives
# nothing, so the cache stays empty and every FQDN rule matches no address --
# a silent egress denial that surfaces as whatever timeout the application
# happens to have. It has blocked three features (.agents/spec/enhancements/
# 2026-08-31.md items 15 and 20) and is why every workload chart still carries
# `toEntities: world` on 443.
#
# Every step up to local delivery is correct and measured. Item 10 records:
#
#   BPF policy map    53/UDP -> PROXY PORT <p>, PACKETS n   redirect programmed and hit
#   proxy mark        0x...0200 = TO_PROXY | (<p><<16)
#   iptables TPROXY   n pkts -> 127.0.0.1:<p>                matched
#   ip rule 9         fwmark 0x200/0xf00 -> table 2004       local dev lo
#   proxy socket      127.0.0.1:<p> UDP+TCP, cilium-agent    listening
#   proxy received    0                                       <-- the only failure
#
# Ruled out already, do not re-test: rp_filter (0 everywhere), the TPROXY kernel
# modules (loaded), filter INPUT (ACCEPT), Tailscale's ts-input DROP (0 packets),
# NOTRACK (applied and matching), encryption (disabled). Two candidate fixes were
# falsified and are recorded in item 10 -- addendum 28's `ip rule`, and
# `dnsproxy-enable-transparent-mode: false`. Neither is worth retrying.
#
# WHY THIS SCRIPT EXISTS
#
# The fault was diagnosed on a live tenant and the diagnosis cost that tenant
# its DNS: enabling the `rules.dns` visibility block redirects ALL of a selected
# workload's queries into a proxy nothing can reach, so DNS stops entirely --
# internal names included. That is strictly worse than not redirecting, and it
# is the one thing this fault needs you to do to observe it.
#
# So it is done here instead, on a pod that exists for ninety seconds and that
# nothing depends on. The policy selects that pod ALONE, by a label no workload
# carries. It is never fleet-wide and never touches a tenant.
#
# WHAT TO DO WITH THE OUTPUT
#
# The evidence block it prints is the upstream bug report. The fault is narrow:
# a marked, TPROXY-matched UDP packet that never reaches a listening transparent
# socket on the same node -- Cilium 1.17.18, tunnel/vxlan, legacy host routing,
# kernel 6.12-flatcar, kube-proxy-replacement=true.
#
#   usage: dns-proxy-fault.sh [--keep]
#
#   --keep   leave the namespace and policy in place for manual poking. The
#            teardown is otherwise unconditional, including on failure: a
#            leftover visibility policy is a pod with no DNS, and the next
#            person would find it as a mystery rather than as this script.
set -uo pipefail

NS="dnsproxy-repro"
POD="repro"
# A label no tenant workload carries, so the policy below cannot select one even
# if the namespace were wrong.
SEL="dns-proxy-reproducer"
KEEP=""
[[ "${1:-}" == "--keep" ]] && KEEP=1

cleanup() {
  [[ -n "$KEEP" ]] && { echo; echo "--keep: leaving namespace/$NS in place. Remove with:"; echo "  kubectl delete ns $NS"; return; }
  echo; echo "── teardown ─────────────────────────────────────────────"
  kubectl delete ns "$NS" --wait=false >/dev/null 2>&1
  echo "  namespace/$NS deleting"
}
trap cleanup EXIT

say() { printf '\n── %s ─────────────────────────────────────────\n' "$1"; }

say "scratch namespace"
kubectl create ns "$NS" >/dev/null 2>&1
kubectl -n "$NS" run "$POD" --image=busybox:1.36 --labels="app=$SEL" \
  --restart=Never --command -- sleep 600 >/dev/null 2>&1
kubectl -n "$NS" wait --for=condition=Ready "pod/$POD" --timeout=90s >/dev/null 2>&1 || {
  echo "  pod never became Ready"; exit 1; }
NODE=$(kubectl -n "$NS" get pod "$POD" -o jsonpath='{.spec.nodeName}')
echo "  pod/$POD on $NODE"

resolve() {
  # 10 lookups, reported as a count. getent exits non-zero on failure and waits
  # out the resolver timeout, which is itself the symptom.
  kubectl -n "$NS" exec "$POD" --request-timeout=120s -- sh -c \
    'ok=0; for i in $(seq 1 10); do nslookup kubernetes.default.svc.cluster.local >/dev/null 2>&1 && ok=$((ok+1)); done; echo "$ok/10"' 2>/dev/null
}

say "baseline: DNS with no policy selecting this pod"
echo "  resolved: $(resolve)"

say "applying the visibility policy (THIS is what breaks it)"
# rules.dns is what makes Cilium proxy the query rather than let it pass. The
# policy allows everything it could otherwise deny, so anything that fails here
# failed in the proxy and not in the allowlist.
kubectl apply -f - >/dev/null 2>&1 <<EOF
apiVersion: cilium.io/v2
kind: CiliumNetworkPolicy
metadata:
  name: dns-proxy-reproducer
  namespace: $NS
spec:
  endpointSelector:
    matchLabels:
      app: $SEL
  egress:
    - toEndpoints:
        - matchLabels:
            k8s:io.kubernetes.pod.namespace: kube-system
      toPorts:
        - ports:
            - port: "53"
              protocol: UDP
            - port: "53"
              protocol: TCP
          rules:
            dns:
              - matchPattern: "*"
    - toEntities: [world, cluster]
EOF
sleep 5
echo "  resolved: $(resolve)    <-- 0/10 here is the fault"

say "datapath evidence from cilium-agent on $NODE"
AGENT=$(kubectl -n kube-system get pods -l k8s-app=cilium \
        --field-selector "spec.nodeName=$NODE" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
if [[ -z "$AGENT" ]]; then
  echo "  no cilium agent found on $NODE"
else
  EP=$(kubectl -n kube-system exec "$AGENT" -c cilium-agent --request-timeout=60s -- \
       cilium-dbg endpoint list -o json 2>/dev/null \
       | python3 -c "
import json,sys
for e in json.load(sys.stdin):
    lbls=(e.get('status',{}).get('identity',{}) or {}).get('labels',[]) or []
    if any('$SEL' in l for l in lbls): print(e['id']); break
" 2>/dev/null)
  echo "  endpoint id: ${EP:-<not found>}"

  echo "  -- fqdn cache (empty cache is the consequence) --"
  kubectl -n kube-system exec "$AGENT" -c cilium-agent --request-timeout=60s -- \
    cilium-dbg fqdn cache list 2>/dev/null | head -5 | sed 's/^/    /'

  if [[ -n "$EP" ]]; then
    echo "  -- BFP policy map: is the 53/UDP redirect programmed and hit? --"
    kubectl -n kube-system exec "$AGENT" -c cilium-agent --request-timeout=60s -- \
      cilium-dbg bpf policy get "$EP" 2>/dev/null | grep -E "PROXY|53" | head -6 | sed 's/^/    /'
  fi

  echo "  -- proxy sockets held by cilium-agent --"
  kubectl -n kube-system exec "$AGENT" -c cilium-agent --request-timeout=60s -- \
    sh -c 'ss -lnpu 2>/dev/null | head -8' 2>/dev/null | sed 's/^/    /'

  echo "  -- iptables TPROXY counters (matched but not delivered) --"
  kubectl -n kube-system exec "$AGENT" -c cilium-agent --request-timeout=60s -- \
    sh -c 'iptables -t mangle -L -n -v 2>/dev/null | grep -i tproxy' 2>/dev/null | sed 's/^/    /'

  echo "  -- ip rules (9 sends the mark to table 2004 = local dev lo) --"
  kubectl -n kube-system exec "$AGENT" -c cilium-agent --request-timeout=60s -- \
    sh -c 'ip rule list' 2>/dev/null | sed 's/^/    /'

  echo "  -- agent log, DNS proxy lines --"
  kubectl -n kube-system logs "$AGENT" -c cilium-agent --tail=300 --request-timeout=60s 2>/dev/null \
    | grep -iE "dnsproxy|dns proxy|tproxy" | tail -6 | sed 's/^/    /'
fi

say "reading the result"
cat <<'TXT'
  baseline 10/10 and policy 0/10 reproduces the fault: the redirect is
  programmed and matched, the socket is listening, and the proxy receives
  nothing. That pair of numbers plus the evidence above is the bug report.

  baseline 10/10 and policy 10/10 means the proxy delivered -- the fault is
  FIXED on this node. Check what changed (kernel, Cilium, host routing), then
  delete the toEntities:world rules the workload charts carry and let the
  toFQDNs rules become operative again. That is the whole point of this.
TXT

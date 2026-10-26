#!/usr/bin/env bash
# Which component answers cluster DNS is the platform's to know, and exactly one
# file is allowed to know it.
#
# THE INCIDENT THIS ENCODES
#
# Ten separate policies -- five tenant workload charts, the stateless-web base,
# the sandbox default-deny, two support-agent policies -- each declared their own
# DNS egress, and each named the backend by label:
#
#     toEndpoints: [{k8s:io.kubernetes.pod.namespace: kube-system,
#                    k8s:k8s-app: kube-dns}]
#
# `kube-proxy-replacement: true` is an ADR-046 datapath invariant, and under it
# Cilium enforces egress against the service ENDPOINT rather than the ClusterIP
# (Documentation/security/policy/language.rst). Those rules therefore worked only
# because the endpoint behind the kube-dns ClusterIP happened to carry that
# label. On 2026-09-24 a CiliumLocalRedirectPolicy moved pod DNS to a node-local
# cache (ADR-046 §34), the endpoint became a pod labelled `k8s-app:
# node-local-dns`, and all ten stopped matching at the same instant. Resolution
# from a tenant pod, same pod, before and after:
#
#     sdk-workload.tenant-waypoint     48/50  ->  0/60
#     shared-cnpg-rw.platform-data     50/50  ->  0/60
#
# It presented as `fetch failed` in application logs -- a JWKS fetch and an
# inter-service call -- and cost a day before the cause was named, because no
# policy reports that a selector matches nothing.
#
# WHAT IS ENFORCED
#
# 1. A policy that allows DNS must name EVERY backend the platform runs, or
#    none. Naming kube-dns alone is the defect above, and it is silent.
# 2. The platform's own grant exists, so that a tenant workload declaring no DNS
#    rule still resolves. Without this the first check would pass a fleet that
#    cannot resolve at all.
#
# Tenant workload charts live in their own repositories and cannot be checked
# from here. What makes their side safe is that they declare no DNS rule at all:
# the platform grants it, so there is nothing for them to get wrong. This gate
# holds that property for everything this repository owns, the stateless-web
# base -- their reference model -- included.

# Backends that answer cluster DNS. Adding one to the fleet means adding it
# here, which is the point: the list is the contract and the gate is how it
# stays true.
DNS_BACKENDS="kube-dns node-local-dns"

validate_dns_backend_contract() {
    section "cluster DNS backend contract"
    local out
    out=$(cd "$VALIDATE_ROOT" && DNS_BACKENDS="$DNS_BACKENDS" python3 - <<'PY'
import os, sys, yaml, pathlib

backends = set(os.environ["DNS_BACKENDS"].split())
POLICY_KINDS = {"NetworkPolicy", "CiliumNetworkPolicy", "CiliumClusterwideNetworkPolicy"}

def labels_in(peer):
    """Every label value a peer selector names, Cilium or Kubernetes shaped."""
    found = set()
    if not isinstance(peer, dict):
        return found
    for key in ("matchLabels",):
        for k, v in (peer.get(key) or {}).items():
            found.add(str(v))
    for sel in ("podSelector", "namespaceSelector"):
        for k, v in ((peer.get(sel) or {}).get("matchLabels") or {}).items():
            found.add(str(v))
    return found

def allows_dns(rule):
    """True if this egress rule opens port 53."""
    for block in (rule.get("toPorts") or []):
        for p in (block.get("ports") or []):
            if str(p.get("port")) == "53":
                return True
    for p in (rule.get("ports") or []):          # Kubernetes NetworkPolicy
        if str(p.get("port")) == "53":
            return True
    return False

def peers_of(rule):
    out = []
    for key in ("toEndpoints", "to"):
        for peer in (rule.get(key) or []):
            out.append(peer)
    return out

def policies_in(node):
    """Every policy object anywhere in a document, however deeply nested.

    Top-level documents are not enough. A Crossplane Composition carries its
    objects under spec.resources[].base, and the tenant cache's policy lives
    there -- it was missed by a top-level scan and was still naming kube-dns
    alone after every visible policy had been corrected.
    """
    if isinstance(node, dict):
        # `kind` is not always a string: a CRD's openAPI schema has a `kind`
        # property whose value is a mapping, and testing that for membership
        # raises "unhashable type: dict" rather than simply not matching.
        kind = node.get("kind")
        if isinstance(kind, str) and kind in POLICY_KINDS and isinstance(node.get("spec"), dict):
            yield node
        for v in node.values():
            yield from policies_in(v)
    elif isinstance(node, list):
        for v in node:
            yield from policies_in(v)

offenders, grant_found = [], False

for path in sorted(pathlib.Path("manifests").rglob("*.yaml")):
    try:
        docs = list(yaml.safe_load_all(open(path)))
    except Exception:
        continue                                  # Helm templates and vendored CRDs
    for top in docs:
        for doc in policies_in(top):
            name = (doc.get("metadata") or {}).get("name", "<unnamed>")
            spec = doc.get("spec") or {}
            rules = list(spec.get("egress") or [])
            for extra in (doc.get("specs") or []):
                rules.extend(extra.get("egress") or [])
            for rule in rules:
                if not allows_dns(rule):
                    continue
                named = set()
                for peer in peers_of(rule):
                    named |= (labels_in(peer) & backends)
                if not named:
                    continue                      # DNS allowed some other way
                missing = backends - named
                if missing:
                    offenders.append(
                        "%s: %s allows DNS to %s but not %s"
                        % (path, name, ", ".join(sorted(named)), ", ".join(sorted(missing))))
                elif name == "tenant-dns-egress":
                    grant_found = True

# Both findings are reported, not the first one. Stopping at the missing grant
# would hide every policy that also needs correcting, and they are fixed in the
# same change -- one run should name all the work.
problems = []
if not grant_found:
    problems.append("no tenant-dns-egress policy grants DNS to every backend; "
                    "tenant workloads declare none of their own and would not resolve")
problems.extend(offenders)

if problems:
    for p in problems:
        print("BAD " + p)
else:
    print("OK")
PY
)
    if [[ "$out" == OK* ]]; then
        pass "every DNS rule names all backends ($DNS_BACKENDS); platform grant present"
    else
        hard_fail "cluster DNS backend contract violated:
${out//BAD /  }"
    fi
}

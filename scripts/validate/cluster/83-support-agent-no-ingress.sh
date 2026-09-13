#!/usr/bin/env bash
# Nothing reaches inward to the Support Agent.
#
# ADR-077: no platform component opens a connection into a tenant's cluster,
# holds a credential for one, or queries it. The agent is the one component that
# talks to the platform at all, so if anything were ever going to grow an inbound
# path it would be this one.
#
# Checked as a property of the manifests rather than of the agent's code. A
# process that never listens is a process that might listen after the next
# change; a component with no Service, no Ingress and no ingress rule cannot be
# reached however its code behaves. That is the difference between a boundary and
# an intention.
#
# Three things are asserted, because each can be defeated without the others:
#
#   1. the NetworkPolicy declares the Ingress policyType, so the default is deny
#   2. it carries no ingress RULES -- `ingress: []` is fine, an entry is not
#   3. the component ships no Service, Ingress, HTTPRoute or Gateway at all
#
# Check 3 is not redundant. A Service with no NetworkPolicy permitting it is
# unreachable today and reachable the moment someone relaxes the policy for an
# unrelated reason, and the review that relaxes it will not be looking for a
# Service nobody remembers adding.
set -euo pipefail

ROOT="${1:-.}"
# Both agents (ADR-077: one per cluster). The spoke's is a single multi-document
# file rather than a directory, so the checker takes paths and globs them itself.
DIR="$ROOT/manifests/hub-core-services/support-agent"
SPOKE="$ROOT/manifests/spoke/spoke-catalog/infra/support-agent.yaml"

[ -d "$DIR" ]   || { echo "83: no support-agent component at $DIR" >&2; exit 1; }
[ -f "$SPOKE" ] || { echo "83: no spoke support agent at $SPOKE" >&2; exit 1; }

checker=$(cat <<'PY'
import sys, glob, os, yaml

paths = []
for arg in sys.argv[1:]:
    paths.extend(sorted(glob.glob(os.path.join(arg, "*.yaml"))) if os.path.isdir(arg) else [arg])
problems = []
policies = 0
# A Service is what makes a pod addressable; the rest are what make it reachable
# from outside the cluster. None of them belong to an egress-only component.
INBOUND_KINDS = {"Service", "Ingress", "HTTPRoute", "GRPCRoute", "TCPRoute", "Gateway"}

for path in paths:
    with open(path) as fh:
        try:
            docs = list(yaml.safe_load_all(fh))
        except yaml.YAMLError as e:
            problems.append(f"{os.path.basename(path)} does not parse: {e}")
            continue

    for doc in docs:
        if not isinstance(doc, dict):
            continue
        kind = doc.get("kind")

        if kind in INBOUND_KINDS:
            problems.append(
                f"{os.path.basename(path)} ships a {kind} "
                f"({(doc.get('metadata') or {}).get('name')}). The Support Agent "
                f"is egress-only; anything that makes it addressable is a change "
                f"to ADR-077's boundary.")

        if kind != "NetworkPolicy":
            continue
        policies += 1
        spec = doc.get("spec") or {}
        name = (doc.get("metadata") or {}).get("name")

        types = spec.get("policyTypes") or []
        if "Ingress" not in types:
            # Without the policyType the API applies no inbound restriction at
            # all, and an absent `ingress` key then means "unrestricted" rather
            # than "denied" -- the opposite of what the manifest reads like.
            problems.append(
                f"NetworkPolicy {name} does not declare the Ingress policyType, "
                f"so nothing inbound is denied. An absent `ingress` key without "
                f"it means unrestricted, not closed.")

        rules = spec.get("ingress")
        if rules:
            problems.append(
                f"NetworkPolicy {name} carries {len(rules)} ingress rule(s). "
                f"An egress-only component admits none.")

if policies == 0:
    problems.append(
        "the component ships no NetworkPolicy at all, so this check would pass "
        "on a component with no network restriction whatsoever")

if problems:
    print("the Support Agent has an inbound path:")
    for p in problems:
        print("  " + p)
    sys.exit(1)

print(f"83: both support agents are egress-only ({policies} policies, no ingress "
      f"rules, nothing addressable)")
PY
)

python3 -c "$checker" "$DIR" "$SPOKE"

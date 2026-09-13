#!/usr/bin/env bash

# What the Support Agent may emit, asserted against ADR-077's categories.
#
# The allowlist is the privacy boundary, and a boundary nothing checks is a
# preference. This reads the shipped ConfigMap rather than the source file, so it
# asserts what a cluster actually received.
#
# Three properties, each of which has a way of quietly failing:
#
#   every emitted field is in its collector's schema
#       -- an emit list that drifts from the schema is emitting something whose
#          shape nobody declared, which is the unbounded-payload failure in
#          miniature.
#   no collector runs a caller-supplied query
#       -- ADR-077: collectors run defined queries whose result schema is known.
#          A templated or parameterised query turns the allowlist into a label
#          filter over whatever the cluster happens to contain.
#   no emitted field names tenant-identifying material
#       -- the categories ADR-077 makes normative. Resource names, namespaces and
#          node names carry a tenant's naming and often their business.

# Field names that identify a tenant's resources rather than the platform's
# state. Emitting any of them crosses the ADR-066 boundary between a capability
# and a workload.
_identifying_fields=(resource_name resource_namespace name namespace node dest_server cluster pod container image)

validate_support_agent_allowlist() {
    section "The Support Agent emits only allowlisted, non-identifying fields (ADR-077)"

    local raw
    if ! raw=$(kc get configmap support-agent-allowlist -n platform-ops \
               -o jsonpath='{.data.allowlist\.yaml}'); then
        note "support-agent allowlist not installed on this hub; nothing to check"
        return 0
    fi
    if [[ -z "$raw" ]]; then
        hard_fail "the support-agent allowlist is present but empty"
        return 0
    fi

    # The check is a program passed with -c, not read from stdin: `python3 -`
    # takes its PROGRAM from stdin, so a heredoc for the script and a herestring
    # for the data both target fd 0 and the last one wins -- the YAML would have
    # been parsed as Python. Passing the program as an argument leaves stdin for
    # the data, which is what it is for.
    local checker
    read -r -d '' checker <<'PYEOF' || true
import sys, yaml

IDENTIFYING = {"resource_name", "resource_namespace", "name", "namespace",
               "node", "dest_server", "cluster", "pod", "container", "image"}

entries = yaml.safe_load(sys.stdin.read()) or []
problems = []
if not entries:
    problems.append("the allowlist parses to nothing; nothing bounds what leaves")

for e in entries:
    name = e.get("collector", "<unnamed>")
    schema = set(e.get("schema") or [])
    emit = set(e.get("emit") or [])

    for f in sorted(emit - schema):
        problems.append(f"{name}: emits {f!r}, which is not in its declared schema")
    for f in sorted(emit & IDENTIFYING):
        problems.append(f"{name}: emits {f!r}, which identifies a tenant's resources")

    q = str((e.get("source") or {}).get("query", ""))
    if any(t in q for t in ("{{", "$", "%s")):
        problems.append(f"{name}: query {q!r} is templated; collectors run defined queries (ADR-077)")
    if not schema:
        problems.append(f"{name}: declares no schema, so nothing bounds what its response may contain")

print(f"COLLECTORS={len(entries)}")
for p in problems:
    print(f"PROBLEM={p}")
PYEOF

    local report
    report=$(python3 -c "$checker" <<<"$raw") \
        || { hard_fail "could not parse the support-agent allowlist"; return 0; }

    local count
    count=$(sed -n 's/^COLLECTORS=//p' <<<"$report")

    local failed=0 line
    while IFS= read -r line; do
        [[ -z "$line" ]] && continue
        hard_fail "${line#PROBLEM=}"
        failed=1
    done < <(grep '^PROBLEM=' <<<"$report")

    (( failed )) && return 0
    pass "$count collector(s): every emitted field is declared and none identifies a tenant"
}

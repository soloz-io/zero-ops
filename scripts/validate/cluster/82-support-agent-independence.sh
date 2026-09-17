#!/usr/bin/env bash
# The Support Agent must not read the observability backend.
#
# ADR-078 §8: shared platform evidence sources, independent collection and
# export paths. The agent's collectors name component metrics endpoints
# directly -- the same /metrics surfaces Alloy scrapes -- and never the store
# Alloy writes to.
#
# Gated rather than reviewed, because the coupling is the convenient thing to
# introduce. Once a metrics store exists in the box, pointing the agent at it is
# one line and looks like a simplification. It is not:
#
#   * support telemetry would then depend on a component the tenant may scale
#     down, so a box with observability disabled becomes an unsupported box --
#     the licence check ADR-077 exists to prevent, arriving by a different door;
#   * the store holds whatever the tenant opted in under ADR-078 §7, so the
#     allowlist would be filtering an unbounded payload instead of bounding a
#     known one, which is a preference and not a boundary.
#
# Checked against the shipped allowlist, which is the contract.
#
# A module, not a standalone script. run.sh SOURCES each file and then calls the
# validate_* functions it defined, so top-level code here ran in the runner's own
# shell. Three things followed from that, and all three were live:
#
#   * `$1` read the RUNNER's first argument, which is --mode=final. This looked
#     for its allowlist at "--mode=final/manifests/.../allowlist.yaml" and
#     reported it missing -- while the file sat in the repository, present and
#     correct.
#   * a top-level `exit 1` ended the whole validation run, not one check, so a
#     single failure here took every check after it with it.
#   * `set -euo pipefail` changed the runner's shell options for everything that
#     sourced afterwards.
validate_support_agent_independence() {
    section "The Support Agent collects from components, not the observability store (ADR-078 §8)"

    # ADR-077 puts one agent on every cluster, so there are two allowlists: the
    # hub's component and the spoke catalogue's. Both are checked, and they are
    # checked against each other -- a spoke reporting under a different contract
    # would make "the allowlist is what leaves" true of only half the fleet, and
    # nothing else in the repository compares them.
    local hub="$VALIDATE_ROOT/manifests/hub-core-services/support-agent/allowlist.yaml"
    local spoke="$VALIDATE_ROOT/manifests/spoke/spoke-catalog/infra/support-agent.yaml"

    [ -f "$hub" ]   || { hard_fail "no hub allowlist at $hub"; return 0; }
    [ -f "$spoke" ] || { hard_fail "no spoke agent at $spoke"; return 0; }

    local extract
    extract=$(cat <<'PY'
import sys, yaml
for doc in yaml.safe_load_all(open(sys.argv[1])):
    if isinstance(doc, dict) and doc.get("kind") == "ConfigMap" \
            and "allowlist.yaml" in (doc.get("data") or {}):
        sys.stdout.write(doc["data"]["allowlist.yaml"])
        sys.exit(0)
sys.exit(f"no allowlist ConfigMap in {sys.argv[1]}")
PY
)

    local hub_raw spoke_raw
    hub_raw=$(python3 -c "$extract" "$hub") \
        || { hard_fail "the hub allowlist could not be read from $hub"; return 0; }
    spoke_raw=$(python3 -c "$extract" "$spoke") \
        || { hard_fail "the spoke agent's allowlist could not be read from $spoke"; return 0; }

    # The spoke's allowlist may be a SUBSET of the hub's -- a spoke has no
    # database to back up -- but never a superset, and never a different emit set
    # for a collector both carry. Either would mean a field leaves a spoke that a
    # review of the hub's contract would not have shown.
    local contract
    contract=$(python3 - "$hub_raw" "$spoke_raw" <<'PY'
import sys, yaml
hub = {c["collector"]: c for c in yaml.safe_load(sys.argv[1]) or []}
spoke = {c["collector"]: c for c in yaml.safe_load(sys.argv[2]) or []}
for name, c in spoke.items():
    h = hub.get(name)
    if h is None:
        print(f"PROBLEM=spoke collector {name!r} is in no hub allowlist")
        continue
    extra = set(c.get("emit") or []) - set(h.get("emit") or [])
    if extra:
        print(f"PROBLEM=spoke collector {name!r} emits {sorted(extra)}, which the hub's does not")
    if (c.get("source") or {}).get("query") != (h.get("source") or {}).get("query"):
        print(f"PROBLEM=spoke collector {name!r} queries something different from the hub's")
PY
) || { hard_fail "the hub and spoke allowlists could not be compared"; return 0; }

    local failed=0 line
    while IFS= read -r line; do
        [[ -z "$line" ]] && continue
        hard_fail "${line#PROBLEM=}"
        failed=1
    done < <(grep '^PROBLEM=' <<<"$contract")

    local raw
    raw=$(printf '%s\n%s\n' "$hub_raw" "$spoke_raw")

    # python3 -c with the YAML on stdin. `python3 - <<'PY' <<<"$raw"` gives stdin
    # to the LAST redirection, so python would read the YAML as its own program --
    # the way check 81 silently could never run.
    local checker
    checker=$(cat <<'PY'
import sys, yaml

# The observability capability's components (ADR-078 §4) plus the names their
# Services carry. A collector naming any of these is reading the store rather
# than the source.
BACKENDS = (
    "victoriametrics", "victoria-metrics", "vmsingle", "vmcluster", "vmselect",
    "vminsert", "vmstorage", "vmagent", "vmalert", "victorialogs", "victoria-logs",
    "grafana", "loki", "tempo", "mimir", "prometheus", "thanos", "alloy",
)

count = 0
for c in yaml.safe_load(sys.stdin) or []:
    count += 1
    name = c.get("collector", "?")
    src = c.get("source") or {}
    haystack = " ".join(str(src.get(k, "")) for k in ("service", "namespace", "query", "path")).lower()
    for b in BACKENDS:
        if b in haystack:
            print(f"PROBLEM=collector {name!r} reads {b!r} ({haystack.strip()}); ADR-078 §8 "
                  f"keeps the paths independent -- collect from the component's own /metrics, "
                  f"which is the source the store also reads")
print(f"COLLECTORS={count}")
PY
)

    local report
    report=$(python3 -c "$checker" <<<"$raw") \
        || { hard_fail "the support-agent allowlists could not be inspected"; return 0; }

    while IFS= read -r line; do
        [[ -z "$line" ]] && continue
        hard_fail "${line#PROBLEM=}"
        failed=1
    done < <(grep '^PROBLEM=' <<<"$report")

    (( failed )) && return 0
    pass "$(sed -n 's/^COLLECTORS=//p' <<<"$report") collector(s): support agents read no observability component, and the spoke's contract matches the hub's"
}

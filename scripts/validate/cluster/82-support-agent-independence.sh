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
set -euo pipefail

ROOT="${1:-.}"
# ADR-077 puts one agent on every cluster, so there are two allowlists: the
# hub's component and the spoke catalogue's. Both are checked, and they are
# checked against each other -- a spoke reporting under a different contract
# would make "the allowlist is what leaves" true of only half the fleet, and
# nothing else in the repository compares them.
HUB="$ROOT/manifests/hub-core-services/support-agent/allowlist.yaml"
SPOKE="$ROOT/manifests/spoke/spoke-catalog/infra/support-agent.yaml"

[ -f "$HUB" ]   || { echo "82: no hub allowlist at $HUB" >&2; exit 1; }
[ -f "$SPOKE" ] || { echo "82: no spoke agent at $SPOKE" >&2; exit 1; }

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

hub_raw=$(python3 -c "$extract" "$HUB")
spoke_raw=$(python3 -c "$extract" "$SPOKE")

# The spoke's allowlist may be a SUBSET of the hub's -- a spoke has no database
# to back up -- but never a superset, and never a different emit set for a
# collector both carry. Either would mean a field leaves a spoke that a review
# of the hub's contract would not have shown.
python3 - <<'PY' "$hub_raw" "$spoke_raw"
import sys, yaml
hub = {c["collector"]: c for c in yaml.safe_load(sys.argv[1]) or []}
spoke = {c["collector"]: c for c in yaml.safe_load(sys.argv[2]) or []}
bad = []
for name, c in spoke.items():
    h = hub.get(name)
    if h is None:
        bad.append(f"spoke collector {name!r} is in no hub allowlist")
        continue
    extra = set(c.get("emit") or []) - set(h.get("emit") or [])
    if extra:
        bad.append(f"spoke collector {name!r} emits {sorted(extra)}, which the hub's does not")
    if (c.get("source") or {}).get("query") != (h.get("source") or {}).get("query"):
        bad.append(f"spoke collector {name!r} queries something different from the hub's")
if bad:
    print("the spoke agent reports under a different contract from the hub's:")
    for b in bad:
        print("  " + b)
    sys.exit(1)
PY

raw=$(printf '%s\n%s\n' "$hub_raw" "$spoke_raw")

# python3 -c with the YAML on stdin. `python3 - <<'PY' <<<"$raw"` gives stdin to
# the LAST redirection, so python would read the YAML as its own program -- the
# way check 81 silently could never run.
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

bad = []
for c in yaml.safe_load(sys.stdin) or []:
    name = c.get("collector", "?")
    src = c.get("source") or {}
    haystack = " ".join(str(src.get(k, "")) for k in ("service", "namespace", "query", "path")).lower()
    for b in BACKENDS:
        if b in haystack:
            bad.append(f"collector {name!r} reads {b!r} ({haystack.strip()})")

if bad:
    print("the Support Agent reads the observability backend:")
    for b in bad:
        print("  " + b)
    print()
    print("ADR-078 s8 keeps the paths independent. Collect from the component's")
    print("own /metrics, which is the source the store also reads.")
    sys.exit(1)
print("82: support agents read no observability component, and the spoke's contract matches the hub's")
PY
)

python3 -c "$checker" <<<"$raw"

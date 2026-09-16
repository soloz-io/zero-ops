#!/usr/bin/env bash
# Day-0 generates artifacts, commits them, and something must apply them.
#
# ADR-045 covers the first two. The third had no check, and it was not happening:
# the root Application filtered its directory with an allowlist naming
# bundle.yaml, so generated.yaml -- the Application that OWNS generated/ -- was
# never applied and that Application was never created. Every artifact Day-0
# wrote sat in git, correct and reconciled by nothing.
#
# Nothing reported it, and nothing could have. The root was Synced and Healthy
# because it was correctly applying the one file it had been told about; the
# missing Application cannot be unhealthy, because it does not exist. This is the
# false-green shape that recurs: a component managing nothing is indistinguishable
# from a component with nothing to do, unless something asserts it should exist.
#
# What it cost: a box ran on the Infisical project ids the platform bundle ships
# while its own sat in git, so hub-operator answered every SpokePool reconcile
# with "Project ... not found" and no spoke PKI was ever created. The cluster
# reported healthy throughout.
#
# The render-time test (TestRender_RootAppliesEveryObjectInTheClusterDirectory)
# stops a NEW box shipping with the wrong filter. It cannot help a box already
# built: root.yaml lives in the tenant's own repository, and a bundle upgrade
# does not rewrite it. So the same property is asserted here, against the cluster.
validate_generated_artifacts_reconciled() {
    section "Day-0 generated artifacts have a reconciler (ADR-045)"

    local apps
    apps=$(kc get applications -n platform-ops -o json 2>/dev/null) || {
        soft_fail "cannot list Applications, so whether generated artifacts are reconciled is unknown"
        return
    }

    local out
    out=$(printf '%s' "$apps" | python3 -c '
import json, sys
try:
    items = json.load(sys.stdin).get("items", [])
except Exception:
    sys.exit(0)

# The Application whose source path ends in /generated is the one ADR-045
# artifacts belong to. Matched by PATH rather than by name, because the name
# carries the cluster and this check should not have to know it.
owners = []
for a in items:
    src = a.get("spec", {}).get("source", {}) or {}
    path = (src.get("path") or "").rstrip("/")
    if path.endswith("/generated"):
        st = a.get("status", {})
        owners.append((
            a["metadata"]["name"],
            path,
            st.get("sync", {}).get("status", "Unknown"),
            st.get("health", {}).get("status", "Unknown"),
            len(st.get("resources", []) or []),
        ))

if not owners:
    print("ABSENT\t-\t-\t-\t0")
for name, path, sync, health, n in owners:
    print(f"PRESENT\t{name}\t{path}\t{sync}\t{n}")
') || { soft_fail "could not read Applications"; return; }

    local kind name path sync count
    while IFS=$'\t' read -r kind name path sync count; do
        [[ -z "$kind" ]] && continue
        case "$kind" in
            ABSENT)
                hard_fail "no Application reconciles clusters/<cluster>/generated — every artifact Day-0 wrote there is in git and applied to nothing. Check the root Application's directory filter: it must not exclude generated.yaml" ;;
            PRESENT)
                if [[ "$count" -eq 0 ]]; then
                    # Healthy and owning nothing is the shape this check exists
                    # for: the Application resolves, reports fine, and manages no
                    # object at all.
                    hard_fail "$name reconciles $path and manages 0 resources — the artifacts are not reaching the cluster"
                elif [[ "$sync" != "Synced" ]]; then
                    soft_fail "$name ($path) is $sync — Day-0's generated artifacts are not applied"
                else
                    pass "$name reconciles $path ($count resource(s))"
                fi ;;
        esac
    done <<< "$out"
}

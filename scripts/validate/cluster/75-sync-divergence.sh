#!/usr/bin/env bash
# An ArgoCD sync that fails retries forever against the revision it STARTED on.
# It never re-resolves HEAD, so a fix pushed afterwards is never picked up — and
# throughout, the Application reports Running and often Healthy. Nothing alerts.
# The only symptom is that a pushed change does not arrive, which reads as a
# build or caching problem and sends the reader anywhere but here.
#
# This was cleared by hand eight times on 2026-08-31 across four Applications,
# and cost more time than any single code defect that day. Every underlying
# cause is now codified; the detection gap was not, and it is the part that
# generalises — the causes will differ next time.
#
# Two states are queryable and neither was watched:
#
#   1. An operation pinned to a revision older than the Application's target.
#      This is precisely the state in which a fix cannot land.
#   2. An operation retrying well past the point where retrying helps.
#
# Recovery is to clear .operation and let the controller re-resolve, which this
# check deliberately does NOT do: a check that repairs hides how often it fires.
validate_sync_divergence() {
    section "ArgoCD sync divergence (operation pinned to a stale revision)"

    local out
    out=$(kc get applications -n platform-ops -o json | python3 -c '
import json, sys

try:
    apps = json.load(sys.stdin).get("items", [])
except Exception:
    sys.exit(0)

for a in apps:
    name = a["metadata"]["name"]
    st = a.get("status", {})
    op = st.get("operationState", {})
    if op.get("phase") != "Running":
        continue

    op_rev = (op.get("operation", {}).get("sync", {}) or {}).get("revision") or ""
    target = st.get("sync", {}).get("revision") or ""

    # A revision-less operation is an auto-sync that resolves HEAD itself, which
    # is the healthy case and must not be reported.
    if op_rev and target and op_rev != target:
        print("STALE\t%s\t%s\t%s" % (name, op_rev[:10], target[:10]))
        continue

    msg = op.get("message") or ""
    # ArgoCD counts its own attempts in the message. Past a handful, the thing
    # being retried is not going to start working on its own.
    import re
    m = re.search(r"Retrying attempt #(\d+)", msg)
    if m and int(m.group(1)) >= 3:
        print("RETRY\t%s\t%s\t%s" % (name, m.group(1), msg[:60]))
')

    if [[ -z "$out" ]]; then
        pass "no Application is syncing against a stale revision"
        return 0
    fi

    local kind app a b
    while IFS=$'\t' read -r kind app a b; do
        [[ -z "$kind" ]] && continue
        case "$kind" in
            STALE)
                hard_fail "$app is syncing revision $a while its target is $b — a fix pushed after that operation began cannot land until .operation is cleared" ;;
            RETRY)
                soft_fail "$app has retried its sync $a times ($b) — it is unlikely to recover without intervention" ;;
        esac
    done <<< "$out"
}

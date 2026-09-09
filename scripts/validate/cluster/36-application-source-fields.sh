#!/usr/bin/env bash
# An Application must not carry both spec.source and spec.sources.
#
# ArgoCD does not treat this as a conflict. GetSources() returns spec.sources
# whenever it is non-empty and never consults spec.source
# (application/v1alpha1/types.go, HasMultipleSources). So an Application holding
# both reconciles the sources array and silently ignores the single source that
# was meant to replace it.
#
# The ApplicationSet controller patches generated Applications rather than
# replacing them, so a template changed from `sources:` to `source:` leaves the
# old array in place on every Application it had already generated. Nothing
# reports a problem: the ApplicationSet is correct, the released bundle is
# correct, the Application is Synced and Healthy, and it is reconciling the
# source the platform stopped shipping.
#
# Observed on the dev hub: zitadel carried a correct single OCI source alongside
# a stale sources array naming the platform's git repository twice. The bundle
# had been clean since 0.1.5 and the cluster had been resolving platform git the
# whole time -- an ADR-063 custody violation invisible to the release gates,
# because the release gates read the bundle and this lives on the cluster.
#
# The check is here rather than in the release gates for that reason: it is a
# property of a running cluster, and no rendering of a bundle can show it.

validate_application_source_fields() {
    section "Applications declare one source shape"

    # The filter is a heredoc rather than an inline -c string: escaping the
    # dictionary lookups through two levels of quoting produced a Python that did
    # not parse, and with stderr discarded the check reported success on a cluster
    # that had the fault. Errors are surfaced for the same reason.
    local conflicted
    conflicted=$(kc get application -n platform-ops -o json | python3 -c "$(cat <<'PYEOF'
import sys, json
for a in json.load(sys.stdin).get("items", []):
    spec = a.get("spec", {})
    if spec.get("source") and spec.get("sources"):
        urls = ",".join(s.get("repoURL", "?") for s in spec["sources"])
        print(a["metadata"]["name"] + "\t" + urls)
PYEOF
)")

    if [[ -z "$conflicted" ]]; then
        pass "no Application carries both spec.source and spec.sources"
        return 0
    fi

    while IFS=$'\t' read -r name urls; do
        [[ -n "$name" ]] || continue
        hard_fail "Application ${name} carries both spec.source and spec.sources; ArgoCD reconciles the sources array (${urls}) and ignores spec.source, so this cluster is running content the bundle no longer ships"
    done <<< "$conflicted"

    return 0
}

#!/usr/bin/env bash

# The Support Agent's access, asserted rather than reviewed.
#
# ADR-077 makes the agent a bounded control-plane evidence collector with no
# Kubernetes read access, and says why that form was chosen: "the agent is not a control-plane backdoor" is a
# sentence in a document, while a ClusterRole granting nothing is a fact a
# tenant's security review confirms in ten seconds.
#
# A claim like that survives exactly as long as something checks it. An agent
# whose ClusterRole has quietly acquired `list pods` is a backdoor regardless of
# what its code does, and the commit that adds it will not say so -- it will say
# it needed a field. This runs against the live cluster, where the answer
# includes any rule bound by any other means.

_agent_sa="system:serviceaccount:platform-ops:support-agent"

# Verbs that make the agent a reader or a writer of cluster state. `create` is
# absent deliberately: the agent's proxy creates tokenreviews and
# subjectaccessreviews to authenticate callers to its OWN endpoint, which grants
# visibility into nothing.
# The wildcard is first and is the one that matters most: a ClusterRole granting
# `*` is every verb below and every verb invented later, and it renders as [*]
# rather than as any of their names.
_forbidden_verbs=('*' get list watch update patch delete deletecollection impersonate escalate bind)

validate_support_agent_rbac() {
    section "The Support Agent can read nothing (ADR-077)"

    # Absent is a valid state: ADR-077 ships the agent inert, and a hub that has
    # not installed it has nothing to over-permit. Noted rather than passed, so
    # a run where the manifest silently stopped being applied does not read as
    # the check succeeding.
    if ! kc get serviceaccount support-agent -n platform-ops >/dev/null 2>&1; then
        note "support-agent not installed on this hub; nothing to check"
        return 0
    fi

    # `auth can-i --list` is the effective answer, not the declared one: it
    # includes every rule reaching this identity through any binding, including
    # ones this component did not ship. That is the question a security review
    # asks, so it is the question asked here.
    local listing
    if ! listing=$(kc auth can-i --list --as="$_agent_sa"); then
        hard_fail "could not enumerate what $_agent_sa may do"
        return 0
    fi

    # The verbs are the LAST bracketed group on each line, and they are bracket-
    # delimited rather than space-delimited: a line reading `[get]` has no
    # whitespace around the verb, so matching on word boundaries missed exactly
    # the single-verb rules worth catching. Parsed by taking the final [...] and
    # splitting its contents.
    #
    # Self-subject rules (selfsubjectaccessreviews, selfsubjectrulesreviews) are
    # what every identity in the cluster can do about itself. They are not access
    # to cluster state and are excluded by name.
    local verbs
    verbs=$(awk '
        # Resource rows only. Every authenticated identity in any cluster holds
        # get on the discovery URLs -- /healthz, /version, /api, /openapi -- and
        # those rows carry an empty Resources column and are indented. Counting
        # them made a service account with no bindings at all look like a reader,
        # which would have failed this check on a correctly-scoped agent.
        NR > 1 && /^[^[:space:]]/ && $0 !~ /selfsubject/ {
            if (match($0, /\[[^][]*\][[:space:]]*$/)) {
                v = substr($0, RSTART + 1, RLENGTH - 2)
                gsub(/,/, " ", v)
                print v
            }
        }
    ' <<<"$listing" | tr ' ' '\n' | sort -u)

    local found=() verb
    for verb in "${_forbidden_verbs[@]}"; do
        if grep -qxF -- "$verb" <<<"$verbs"; then
            found+=("$verb")
        fi
    done

    if (( ${#found[@]} > 0 )); then
        hard_fail "the Support Agent holds ${found[*]} on cluster resources"
        echo "     ADR-077: the agent collects bounded control-plane evidence through" >&2
        echo "     defined collectors, and reads nothing through the Kubernetes API." >&2
        echo "     Widening this is a change to the platform's boundary and belongs" >&2
        echo "     in an amendment to ADR-077, not in a commit that needed a field." >&2
        echo "$listing" | sed 's/^/       /' >&2
        return 0
    fi

    pass "no get/list/watch or write verb on any resource"
}

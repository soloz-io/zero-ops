#!/usr/bin/env bash
# ADR-047 addendum: a platform manifest or binary must not name a tenant.
#
# Tenant runtime state lives in fleet-registry (ADR-004), and the platform renders
# tenant resources parameterised by fleetId. A literal tenant name in platform code
# inverts that: the platform starts depending on something that only exists after
# onboarding.
#
# That is not cosmetic. waypoint-bff-client-secret is an ExternalSecret in
# platform-edge whose key is only produced once that tenant's OAuth client is
# registered — so on a fresh hub, where no tenant has onboarded, it can never
# resolve. It was the single ExternalSecret still failing at 21/22 after every other
# secret converged, and it fails that way on every rebuild. It was repeatedly
# mistaken for a seeding gap; the dependency simply should not exist yet.
#
# The known offenders are baselined so the rule is enforceable today without
# blocking on the refactor. Anything NEW fails: a tenth file, or a second tenant
# hardcoded the same way.
validate_tenant_identifiers() {
    section "No tenant identifiers in platform code (ADR-047 addendum)"

    local out
    out=$(cd "$VALIDATE_ROOT" && python3 - <<'PY'
import os, re, subprocess

# Paths that constitute "the platform". Tenant material belongs under
# manifests/tenants/ and the fleet provisioning ApplicationSets.
PLATFORM = ["manifests/hub-core-services", "manifests/argocd", "manifests/spoke",
            "internal", "cmd", "operators"]

# Tenant identifiers observed in this repo. There is no canonical local list —
# tenants live in fleet-registry — so this enumerates the names that have actually
# leaked. The rule is "no tenant names at all"; this is how it is detected.
TENANTS = ["waypoint", "oranger"]

# Files that already violate the rule, recorded so the debt is visible and any NEW
# leak still fails. Shrinks as the ADR-047 remediation lands.
#
# The auth-proxy coupling is CLOSED: the platform binary no longer registers a
# tenant's OAuth clients, and its Deployment no longer mounts a tenant secret.
# What remains is hostname-level.
# EMPTY as of 2026-09-02. Both former entries are remediated: d3b7303d moved
# AgentGateway to one instance per tenant configured from the fleet registry, so
# agentgateway-config.yaml no longer defines tenant routing, and the appset is
# fleet-parameterised. What each still contains is a tenant name in a COMMENT,
# which this check no longer counts.
BASELINE = set()

pattern = r"\b(" + "|".join(re.escape(t) for t in TENANTS) + r")\b"


def strip_comments(line, ext):
    """Remove the comment tail of a line, ignoring comment markers inside strings.

    Naive stripping is not safe here. A Go line may carry
    "postgresql://tenant:..." — a real violation whose "//" is part of a URL, not
    a comment — so the marker only counts when it falls OUTSIDE a quoted string.
    Conversely a tenant name in prose (an example query, an incident note, a
    comment explaining why a label IS fleet-agnostic) is documentation, and
    flagging it penalises writing down the reasoning.
    """
    # A YAML file may embed another language whose comment marker differs — the
    # Alloy/River config in grafana-alloy.yaml is written with "//" inside a YAML
    # block scalar. Honour both markers there rather than only the outer one.
    markers = ("//",) if ext == ".go" else ("#", "//")
    quote = None
    i = 0
    while i < len(line):
        c = line[i]
        if quote:
            if c == "\\" and ext == ".go":
                i += 2
                continue
            if c == quote:
                quote = None
        elif c == '"' or c == "'":
            # Deliberately not treating a Go raw-string delimiter as a quote: a
            # tenant name inside one is still hardcoded, and skipping it would
            # be the unsafe direction.
            quote = c
        elif any(line.startswith(m, i) for m in markers):
            m = next(m for m in markers if line.startswith(m, i))
            # '#' opens a comment only at line start or after space; '//' must not
            # swallow a URL scheme ("postgresql://tenant:..."), which is a real
            # violation rather than a comment.
            if m == "#" and i > 0 and line[i - 1] not in " \t":
                i += 1
                continue
            if m == "//" and i > 0 and line[i - 1] == ":":
                i += 2
                continue
            return line[:i]
        i += 1
    return line


def count_code_hits(path):
    ext = os.path.splitext(path)[1]
    n = 0
    try:
        with open(path, errors="replace") as fh:
            for line in fh:
                n += len(re.findall(pattern, strip_comments(line, ext)))
    except OSError:
        return 0
    return n


hits = {}
for root in PLATFORM:
    if not os.path.isdir(root):
        continue
    r = subprocess.run(["grep", "-rlE", pattern, root], capture_output=True, text=True)
    for f in r.stdout.split("\n"):
        f = f.strip()
        if not f:
            continue
        # Vendored or generated trees are not authored platform code.
        if "/vendor/" in f or "/reference-projects/" in f:
            continue
        n = count_code_hits(f)
        if n:
            hits[f] = n

new = {f: n for f, n in hits.items() if f not in BASELINE}
known = {f: n for f, n in hits.items() if f in BASELINE}

for f in sorted(new):
    print("\t".join(["BAD", f, str(new[f])]))
for f in sorted(known):
    print("\t".join(["DEBT", f, str(known[f])]))

# A baseline entry that no longer matches means the refactor landed — prompt for
# its removal so the list cannot rot into permanent noise.
for f in sorted(BASELINE):
    if f not in hits:
        print("\t".join(["CLEAN", f, "0"]))
PY
)

    local tag file count bad=0 debt=0
    while IFS=$'\t' read -r tag file count; do
        case "$tag" in
            BAD)
                bad=1
                hard_fail "$file names a tenant ($count occurrence(s)) — platform code must be fleet-parameterised"
                ;;
            DEBT) ((debt++)) ;;
            CLEAN)
                warn "$file is in the baseline but no longer names a tenant — remove it from BASELINE"
                ;;
        esac
    done <<< "$out"

    if (( ! bad )); then
        pass "no new tenant identifiers in platform code"
    fi
    if (( debt )); then
        note "$debt file(s) still name a tenant (ADR-047 remediation outstanding):"
        while IFS=$'\t' read -r tag file count; do
            [[ "$tag" == "DEBT" ]] || continue
            note "  $file ($count)"
        done <<< "$out"
    fi
}

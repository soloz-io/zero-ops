#!/usr/bin/env python3
"""A fleet may not declare RBAC that escalates out of its namespace.

WHY THIS EXISTS ALONGSIDE THE CHART GUARD

`workloadRbac[].rules` is rendered verbatim into a Role the platform creates, so
whatever a fleet declares, the platform grants. The chart refuses the dangerous
declarations at render time; this refuses them at the repository, and neither
replaces the other:

  the chart guard   protects the cluster. Nothing that reaches a sync can carry an
                    escalating rule, whatever its source.
  this preflight    protects the review. A chart failure arrives as an ArgoCD sync
                    error naming a Helm template, at which point the declaration is
                    already merged and someone is debugging a deployment rather
                    than reading a diff.

WHAT COUNTS AS ESCALATION, AND WHAT DOES NOT

Each pattern below crosses out of the namespace it was granted in, or grants what
nobody decided. Broad-but-bounded privilege is not the concern -- a fleet that needs
to create workloads says so and gets it.

Authority to create a workload is deliberately NOT refused. A pod specification may
mount any Secret in its namespace, so that authority already implies reading them and
cannot be withheld from a fleet that must run work. It is bounded by the namespace,
which is why tenant isolation rests on namespace scoping (ADR-003 section 6).

Emits tab-separated lines the shell wrapper classifies:
    OK<TAB><message>
    BAD<TAB><message>
    NONE
"""
import os
import sys
from pathlib import Path

import yaml

HERE = Path(__file__).resolve()
ZERO_OPS = HERE.parents[3]
REPO = Path(os.environ.get("GITOPS_DIR", ZERO_OPS / ".local-e2e/nutgraf-gitops"))

# resource -> why it escalates. Kept as prose because the message is what an
# operator acts on; a rule name tells them nothing.
DENIED_RESOURCES = {
    "secrets": "reads every credential the platform delivers to the namespace (ADR-003); "
    "a workload receives its secrets as env vars and volumes, not from the API",
    "serviceaccounts/token": "mints an identity for any ServiceAccount in the namespace, "
    "so the grant becomes whatever those hold",
}
DENIED_VERBS = {
    "escalate": "creates Roles carrying privileges the creator does not hold, which defeats "
    "RBAC containment outright",
    "bind": "binds Roles the creator could not have created",
    "impersonate": "acts as another subject, making every other limit advisory",
}


def fleet_values():
    """Every fleet values file that declares workloadRbac."""
    if not REPO.is_dir():
        return
    for f in sorted(REPO.rglob("values.yaml")):
        if ".state" in f.parts:
            continue
        try:
            doc = yaml.safe_load(f.read_text())
        except Exception:
            continue
        if isinstance(doc, dict) and doc.get("workloadRbac"):
            yield f, doc["workloadRbac"]


def main() -> int:
    checked = 0
    bad = 0

    for path, entries in fleet_values():
        rel = path.relative_to(REPO)
        for entry in entries or []:
            sa = (entry or {}).get("serviceAccount", "<unnamed>")
            for rule in (entry or {}).get("rules") or []:
                resources = rule.get("resources") or []
                verbs = rule.get("verbs") or []
                checked += 1

                for r in resources:
                    if r == "*":
                        print(f"BAD\t{rel}: {sa} declares resources: [*] — a wildcard grants what "
                              f"nobody decided, including resources that do not exist yet")
                        bad += 1
                    elif r in DENIED_RESOURCES:
                        print(f"BAD\t{rel}: {sa} declares `{r}` — {DENIED_RESOURCES[r]}")
                        bad += 1
                for v in verbs:
                    if v == "*":
                        print(f"BAD\t{rel}: {sa} declares verbs: [*] — name the verbs the workload uses")
                        bad += 1
                    elif v in DENIED_VERBS:
                        print(f"BAD\t{rel}: {sa} declares the `{v}` verb — {DENIED_VERBS[v]}")
                        bad += 1

    if checked == 0:
        if not REPO.is_dir():
            # The tenant repository is a separate checkout and the platform must
            # validate without it, as the cross-application check already assumes.
            # Reported as unverified rather than passed.
            print("NONE")
            return 0
        # The repository IS here and declares no RBAC at all. That is a real state --
        # a fleet may need none -- so it passes, but it says so rather than going
        # quiet, because "no rules found" and "no rules checked" look identical in a
        # log and only one of them is fine.
        print("OK\tthe tenant repository declares no workloadRbac at all; nothing to escalate with")
        return 0
    if bad == 0:
        print(f"OK\t{checked} fleet RBAC rule(s) declare no escalation out of their namespace")
    return 0


if __name__ == "__main__":
    sys.exit(main())

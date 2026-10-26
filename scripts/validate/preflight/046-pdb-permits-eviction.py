#!/usr/bin/env python3
"""A PodDisruptionBudget must not forbid every eviction.

WHY THIS EXISTS

`minAvailable: 1` beside `replicas: 1` means the only pod can never be evicted:
removing it would take availability below minAvailable, so the eviction API refuses
with 429. `kubectl drain` does not fail on that -- it retries, indefinitely -- so the
symptom is not an error but a node that never finishes draining.

On this platform that is a control-plane roll that cannot complete. Observed on
nutgraf-01 on 2026-10-05: the v3 roll replaced the control-plane node correctly, the
new node came up Ready and self-assigned its providerID, and the OLD Machine then sat
in `Deleting` draining kube-system/ccm-ccm-hetzner with nothing to time out. The roll
had already succeeded; it simply could not finish. Every future change to that
control plane was blocked behind it, including the v4 encryption argument.

It was not a decision. The upstream ccm-hetzner chart ships that PDB as its default
and it was rendered verbatim into a ClusterResourceSet addon.

WHY A PLAIN YAML SCAN WOULD NOT HAVE FOUND IT

The PDB is not a document in a manifest file. It is a line inside the `stringData`
of a Secret of type `addons.cluster.x-k8s.io/resource-set` -- the ClusterResourceSet
delivery mechanism -- so `kind: PodDisruptionBudget` appears nowhere a document-level
walk would look. This check parses embedded manifests out of Secret/ConfigMap data
and judges those too, which is the only reason it sees the case it was written for.

WHAT IS JUDGED

A PDB is paired with the workloads its selector matches, within the same document
set, and failed when no eviction is possible:

    minAvailable: N   with replicas <= N          -> no eviction ever
    minAvailable: 100%                            -> no eviction ever
    minAvailable: P%  with no allowed disruption  -> no eviction ever

DaemonSets are NOT judged: their replica count is the node count, which is not in the
manifest, so `minAvailable: 1` on one is only a trap on a single-node cluster and
cannot be decided from here. A PDB matching only DaemonSets is reported as unjudged
rather than silently passed.

Emits tab-separated lines the shell wrapper classifies:
    OK<TAB><message>
    BAD<TAB><message>
    WARN<TAB><message>
    NONE
"""
import functools
import math
import subprocess
import sys
from pathlib import Path

import yaml

HERE = Path(__file__).resolve()
ZERO_OPS = HERE.parents[3]

# `internal/platform/embedded` is a generated copy of `manifests`, so judging it
# would double every finding at a path nobody edits.
TREES = [ZERO_OPS / "manifests"]

WORKLOAD_KINDS = {"Deployment", "StatefulSet", "ReplicaSet"}
PER_NODE_KINDS = {"DaemonSet"}


def embedded_docs(doc):
    """Manifests carried inside a Secret/ConfigMap, which is how addons ship here."""
    out = []
    if not isinstance(doc, dict):
        return out
    if doc.get("kind") not in ("Secret", "ConfigMap"):
        return out
    for field in ("stringData", "data"):
        blob = doc.get(field)
        if not isinstance(blob, dict):
            continue
        for key, val in blob.items():
            if not isinstance(val, str) or "kind:" not in val:
                continue
            try:
                for d in yaml.safe_load_all(val):
                    if isinstance(d, dict):
                        out.append(d)
            except yaml.YAMLError:
                # Not YAML, or base64 under `data`. Not this check's business.
                continue
    return out


def all_docs(path):
    """Every document in a file, plus every manifest embedded in one."""
    try:
        top = [d for d in yaml.safe_load_all(path.read_text(encoding="utf-8")) if isinstance(d, dict)]
    except (yaml.YAMLError, UnicodeDecodeError):
        return []
    out = list(top)
    for d in top:
        out.extend(embedded_docs(d))
    return out


@functools.lru_cache(maxsize=64)
def kustomized_docs(directory):
    """Documents as kustomize renders them, for a directory that has a kustomization.

    NEEDED BECAUSE A REPLICA COUNT IS OFTEN A PATCH, NOT A FIELD. The
    cert-manager-webhook PDB carries minAvailable: 1 and would read as un-evictable
    against the vendored chart's `replicas: 1` -- but a kustomize patch sets it to 2,
    deliberately and with its reasoning recorded, so one eviction IS allowed.

    Judging the raw file would make that a permanent finding on correct
    configuration, and a check that is permanently wrong about something gets
    switched off. Reporting it unjudged instead would be honest and still noise.
    So the rendered form is consulted before giving up on a PDB whose workload is
    not in the same file.
    """
    if not (Path(directory) / "kustomization.yaml").is_file():
        return ()
    try:
        out = subprocess.run(
            ["kustomize", "build", str(directory)],
            capture_output=True, text=True, timeout=120,
        )
    except (OSError, subprocess.SubprocessError):
        return ()
    if out.returncode != 0:
        return ()
    try:
        return tuple(d for d in yaml.safe_load_all(out.stdout) if isinstance(d, dict))
    except yaml.YAMLError:
        return ()


def matches(selector, labels):
    if not selector or not labels:
        return False
    for k, v in selector.items():
        if labels.get(k) != v:
            return False
    return True


def allowed_disruptions(min_avail, replicas):
    """Pods that may be evicted, or None when minAvailable is unparseable."""
    if isinstance(min_avail, int):
        return replicas - min_avail
    if isinstance(min_avail, str) and min_avail.endswith("%"):
        try:
            pct = float(min_avail[:-1])
        except ValueError:
            return None
        # Kubernetes rounds minAvailable percentages UP.
        return replicas - math.ceil(replicas * pct / 100.0)
    return None


def main():
    findings = []
    judged = 0

    for tree in TREES:
        if not tree.exists():
            print(f"BAD\t{tree.relative_to(ZERO_OPS)} does not exist; this check is looking in "
                  f"the wrong place and would pass an unrollable control plane")
            return 0
        for path in sorted(tree.rglob("*.yaml")):
            docs = all_docs(path)
            if not docs:
                continue
            pdbs = [d for d in docs if d.get("kind") == "PodDisruptionBudget"]
            if not pdbs:
                continue
            rel = path.relative_to(ZERO_OPS)

            for pdb in pdbs:
                spec = pdb.get("spec") or {}
                min_avail = spec.get("minAvailable")
                if min_avail is None:
                    # maxUnavailable always permits at least one eviction.
                    continue
                name = (pdb.get("metadata") or {}).get("name", "<unnamed>")
                sel = ((spec.get("selector") or {}).get("matchLabels")) or {}

                hits, per_node = [], []
                for d in docs:
                    kind = d.get("kind")
                    if kind not in WORKLOAD_KINDS and kind not in PER_NODE_KINDS:
                        continue
                    labels = (((d.get("spec") or {}).get("template") or {})
                              .get("metadata") or {}).get("labels") or {}
                    if not matches(sel, labels):
                        continue
                    if kind in PER_NODE_KINDS:
                        per_node.append(d)
                    else:
                        hits.append(d)

                # Before concluding the workload is not here, ask kustomize. A
                # replica count set by a patch is invisible in the raw file.
                if not hits:
                    for d in kustomized_docs(str(path.parent)):
                        if d.get("kind") not in WORKLOAD_KINDS:
                            continue
                        labels = (((d.get("spec") or {}).get("template") or {})
                                  .get("metadata") or {}).get("labels") or {}
                        if matches(sel, labels):
                            hits.append(d)

                if not hits and per_node:
                    findings.append(
                        f"WARN\t{rel}: PDB {name} has minAvailable={min_avail!r} and matches only "
                        f"a DaemonSet, whose replica count is the node count and is not in the "
                        f"manifest; on a single-node cluster this forbids every eviction, and that "
                        f"cannot be decided from here"
                    )
                    continue
                if not hits:
                    findings.append(
                        f"WARN\t{rel}: PDB {name} has minAvailable={min_avail!r} and its selector "
                        f"{sel} matches no workload in the same manifest, so what it protects "
                        f"cannot be checked here"
                    )
                    continue

                for d in hits:
                    judged += 1
                    wname = (d.get("metadata") or {}).get("name", "<unnamed>")
                    replicas = (d.get("spec") or {}).get("replicas")
                    if replicas is None:
                        replicas = 1  # the Kubernetes default
                    allowed = allowed_disruptions(min_avail, replicas)
                    if allowed is None:
                        findings.append(
                            f"WARN\t{rel}: PDB {name} has an unparseable minAvailable "
                            f"{min_avail!r}; it cannot be judged"
                        )
                        continue
                    if allowed <= 0:
                        findings.append(
                            f"BAD\t{rel}: PDB {name} has minAvailable={min_avail!r} and "
                            f"{d.get('kind')} {wname} has replicas={replicas}, so NO pod can ever "
                            f"be evicted -- the eviction API refuses with 429 and `kubectl drain` "
                            f"retries forever rather than failing. A node draining this never "
                            f"finishes, which on a control plane is a roll that cannot complete. "
                            f"Use maxUnavailable: 1 to bound simultaneous disruption without "
                            f"forbidding the single eviction a drain needs"
                        )

    if judged == 0 and not findings:
        print("NONE")
        return 0

    for line in findings:
        print(line)
    if not any(l.startswith("BAD") for l in findings):
        print(f"OK\t{judged} PDB/workload pairing(s) permit at least one eviction")
    return 0


if __name__ == "__main__":
    sys.exit(main())

#!/usr/bin/env bash
# ADR-030 makes two requirements that are invisible when broken, because a rotated
# credential does not fail at rotation time:
#
#   #1 Every ExternalSecret carries a non-zero refreshInterval. Without it ESO syncs
#      once and never reconciles again, so drift and accidental deletion of the
#      target Secret are never noticed.
#   #3 A workload consuming a rotating Secret must roll when it changes. Processes
#      read credentials at startup; ESO rewrites the Secret in place; nothing
#      restarts the pod. The old value keeps working until it is revoked or expires,
#      so the outage lands long after the rotation that caused it and looks
#      unrelated to it.
#
# Both were violated platform-wide: one ExternalSecret had no refreshInterval, and
# FIVE of five ESO-secret consumers had no rolling-update trigger — Stakater Reloader
# was deployed and annotated nothing at all.
validate_secret_rotation_contract() {
    section "Secret rotation contract (ADR-030 #1, #3)"

    local out
    out=$(cd "$VALIDATE_ROOT" && python3 - <<'PY'
import glob, yaml

es_targets = {}
for f in glob.glob("manifests/**/*.yaml", recursive=True):
    try:
        docs = [d for d in yaml.safe_load_all(open(f)) if d]
    except Exception:
        continue
    for d in docs:
        if d.get("kind") != "ExternalSecret":
            continue
        ri = d["spec"].get("refreshInterval")
        name = (d["spec"].get("target") or {}).get("name") or d["metadata"]["name"]
        es_targets[name] = f
        if ri in (None, "0", "0s"):
            print("\t".join(["BAD", "#1 " + d["metadata"]["name"],
                             "refreshInterval is %r — ESO syncs once and never reconciles" % ri]))

def secrets_used(spec):
    used = set()
    for v in spec.get("volumes") or []:
        s = (v.get("secret") or {}).get("secretName")
        if s:
            used.add(s)
    for c in (spec.get("containers") or []) + (spec.get("initContainers") or []):
        for e in c.get("env") or []:
            r = ((e.get("valueFrom") or {}).get("secretKeyRef") or {}).get("name")
            if r:
                used.add(r)
        for ef in c.get("envFrom") or []:
            r = (ef.get("secretRef") or {}).get("name")
            if r:
                used.add(r)
    return used

for f in glob.glob("manifests/**/*.yaml", recursive=True):
    try:
        docs = [d for d in yaml.safe_load_all(open(f)) if d]
    except Exception:
        continue
    for d in docs:
        if d.get("kind") not in ("Deployment", "StatefulSet", "DaemonSet"):
            continue
        spec = (d.get("spec") or {}).get("template", {}).get("spec", {}) or {}
        rotating = secrets_used(spec) & set(es_targets)
        if not rotating:
            continue
        ann = d["metadata"].get("annotations") or {}
        if any(k.startswith(("reloader.stakater.com", "secret.reloader.stakater.com")) for k in ann):
            continue
        print("\t".join(["BAD", "#3 " + d["metadata"]["name"],
                         "consumes rotating secret(s) %s with no rolling-update trigger"
                         % ",".join(sorted(rotating))]))
PY
)

    if [[ -z "$out" ]]; then
        pass "every ExternalSecret reconciles, and every consumer rolls on rotation"
        return 0
    fi

    local tag what msg
    while IFS=$'\t' read -r tag what msg; do
        [[ "$tag" == "BAD" ]] || continue
        hard_fail "ADR-030 $what — $msg"
    done <<< "$out"
}

#!/usr/bin/env bash
# ADR-021 amendment (2026-10-29): the GitOps composition contract, asserted statically.
#
# The amendment exists because rc.177 shipped an observability stack that disagreed
# with four accepted ADRs and nothing caught it: 2.8 MB of CRDs converged in one sync
# with the stack that needed them, dead sync-wave fields declared an ordering no
# mechanism provided, and required collection sat behind a capability gate that would
# have removed it. None of that is visible in a green sync, which is why it is checked
# here rather than trusted.
#
# Five invariants, each traceable to a clause:
#
#   1. No Application carries a sync wave. ArgoCD orders resources within ONE
#      synchronisation; an ApplicationSet-generated Application is synced by no
#      parent, so the annotation ordered nothing while reading as a guarantee.
#   2. No descriptor declares a field no boundary template dereferences. A dead
#      field is indistinguishable from a live one by reading it.
#   3. Required machinery (ADR-066 add.4) is declared outside every capability gate.
#   4. An Application shipping a CRD and the operator that owns it establishes the
#      CRD first (ADR-029 §4 tier -5) -- the one ordering that is real here.
#   5. Vendored CRD artefacts carry provenance OUTSIDE themselves, and the recorded
#      digest matches the committed bytes (ADR-029 §1).
#
# Invariants 4 and 5 are asserted for the components this amendment composes. Earlier
# vendored CRD sets (ESO, gateway, external-dns, platform-alerts, the spoke catalogue)
# predate it and are listed rather than failed: retrofitting them is its own change
# with its own evidence, and a check that fails thirty components on the day it lands
# is a check somebody disables.

ADR021_SCOPE="victoriametrics-operator platform-dashboards grafana"

validate_adr021_no_application_sync_waves() {
    section "ADR-021 amendment: no Application declares a sync wave"

    local hits
    hits=$(grep -rln "argocd.argoproj.io/sync-wave" \
        "$VALIDATE_ROOT/manifests/argocd/environment-manager/templates" 2>/dev/null || true)
    if [[ -z "$hits" ]]; then
        pass "no boundary template annotates a generated Application with a sync wave"
    else
        hard_fail "sync-wave annotation on a generated Application in: $(tr '\n' ' ' <<<"$hits") -- no parent Application syncs these, so it orders nothing while stating a guarantee"
    fi

    hits=$(grep -rln "^syncWave:" "$VALIDATE_ROOT/manifests/argocd/components" 2>/dev/null || true)
    if [[ -z "$hits" ]]; then
        pass "no component descriptor declares a syncWave"
    else
        hard_fail "syncWave declared in: $(tr '\n' ' ' <<<"$hits") -- no boundary template consumes it"
    fi
}

validate_adr061_no_dead_descriptor_fields() {
    section "ADR-061 amendment: a descriptor declares only fields that are consumed"

    local out line
    out=$(cd "$VALIDATE_ROOT" && python3 - <<'PY'
import glob, re

# A field is live if ANY template or helper dereferences it: boundary 04 is served by
# two templates and _distribution.tpl dereferences fields on every component's behalf,
# so a per-template view reports live fields as dead.
consumed = set()
for t in glob.glob("manifests/argocd/environment-manager/templates/*"):
    body = open(t).read()
    consumed |= set(re.findall(r'\}\} \.([A-Za-z][A-Za-z0-9]*)', body))
    consumed |= set(re.findall(r'\.descriptor\.([A-Za-z][A-Za-z0-9]*)', body))
    consumed |= set(re.findall(r'\$d\.([A-Za-z][A-Za-z0-9]*)', body))
    consumed |= set(re.findall(r'\.([A-Za-z][A-Za-z0-9]*) \|', body))
    consumed |= set(re.findall(r'eq \.([A-Za-z][A-Za-z0-9]*)', body))
    consumed |= set(re.findall(r'ne \.([A-Za-z][A-Za-z0-9]*)', body))
consumed |= {"boundary", "appName", "helmValues"}

for d in sorted(glob.glob("manifests/argocd/components/*/*.yaml")):
    declared = set(re.findall(r'^([A-Za-z][A-Za-z0-9]*):', open(d).read(), re.M))
    dead = sorted(declared - consumed)
    if dead:
        print("FAIL %s declares %s, which no boundary template dereferences"
              % (d, ", ".join(dead)))
print("DONE")
PY
    )
    if [[ "$out" == "DONE" ]]; then
        pass "every descriptor field is dereferenced by a boundary template"
        return 0
    fi
    while IFS= read -r line; do
        [[ -z "$line" || "$line" == "DONE" ]] && continue
        hard_fail "${line#FAIL }"
    done <<< "$out"
}

validate_adr066_required_machinery_is_ungated() {
    section "ADR-066 add.4: required collection machinery is not behind a capability gate"

    local appset="$VALIDATE_ROOT/manifests/argocd/environment-manager/templates/03-platform-services-appset.yaml"
    local component
    for component in grafana-alloy kube-state-metrics node-exporter; do
        if [[ -f "$VALIDATE_ROOT/manifests/argocd/components/03/$component.yaml" ]]; then
            pass "$component is an unconditional descriptor"
        elif grep -q "appName: '$component'" "$appset" 2>/dev/null; then
            hard_fail "$component is declared inline in boundary 03, which is reserved for selectable presence (ADR-066 add.2) -- required machinery declared there is removed whenever the capability above it is deselected, which add.4 forbids"
        else
            note "$component is not a component of its own in boundary 03 (node-exporter ships inside the Alloy DaemonSet on some boxes)"
        fi
    done
}

validate_adr029_crds_establish_before_their_operator() {
    section "ADR-029 §4: a CRD is established before the operator that owns it"

    local out line
    out=$(cd "$VALIDATE_ROOT" && ADR021_SCOPE="$ADR021_SCOPE" python3 - <<'PY'
import glob, os, yaml

WAVE = "argocd.argoproj.io/sync-wave"
scope = set(os.environ["ADR021_SCOPE"].split())
others = []

for comp in sorted(glob.glob("manifests/hub-core-services/*")):
    if not os.path.isdir(comp):
        continue
    crds, workloads, crd_kinds, cr_kinds = [], [], set(), set()
    for f in sorted(glob.glob(comp + "/*.yaml")):
        try:
            docs = list(yaml.safe_load_all(open(f)))
        except yaml.YAMLError:
            continue   # a templated manifest, not a CRD carrier
        for d in docs:
            if not isinstance(d, dict) or "kind" not in d:
                continue
            ann = (d.get("metadata") or {}).get("annotations") or {}
            wave = ann.get(WAVE)
            if d["kind"] == "CustomResourceDefinition":
                crds.append((f, wave))
                crd_kinds.add(((d.get("spec") or {}).get("names") or {}).get("kind"))
            elif d["kind"] in ("Deployment", "StatefulSet", "DaemonSet"):
                workloads.append((f, wave))
            else:
                cr_kinds.add(d["kind"])

    if not crds:
        continue
    name = os.path.basename(comp)
    if name not in scope:
        others.append(name)
        continue

    bad = sorted({f for f, w in crds if w != "-5"})
    if bad:
        print("FAIL %s: CRDs without sync-wave -5 in %s -- nothing establishes the API "
              "before the controller that serves it" % (name, ", ".join(bad)))
    else:
        print("PASS %s: %d CRD(s) at wave -5" % (name, len(crds)))

    late = sorted({f for f, w in workloads if w in (None, "-5")})
    if late:
        print("FAIL %s: operator workload in %s carries no wave after the CRDs"
              % (name, ", ".join(late)))
    elif workloads:
        print("PASS %s: %d operator workload(s) ordered after its CRDs"
              % (name, len(workloads)))

    served = sorted(k for k in (crd_kinds & cr_kinds) if k)
    if served:
        print("FAIL %s: ships both the CRD and instances of %s -- their readiness "
              "depends on an operator in the same sync, which ArgoCD treats as one "
              "unit (ADR-021 addendum 2026-08-22)" % (name, ", ".join(served)))

if others:
    print("NOTE %d CRD carrier(s) predate this amendment and are not asserted here: %s"
          % (len(others), ", ".join(others)))
PY
    )
    while IFS= read -r line; do
        [[ -z "$line" ]] && continue
        case "$line" in
            PASS\ *) pass "${line#PASS }" ;;
            FAIL\ *) hard_fail "${line#FAIL }" ;;
            NOTE\ *) note "${line#NOTE }" ;;
        esac
    done <<< "$out"
}

validate_adr029_vendored_crd_provenance() {
    section "ADR-029 §1: vendored CRD provenance is external and verifiable"

    local artifacts=(
        "manifests/hub-core-services/victoriametrics-operator/crds.yaml"
        "manifests/hub-core-services/platform-dashboards/grafana-operator-crds.yaml"
    )
    local artifact comp base prov sum
    for artifact in "${artifacts[@]}"; do
        comp=$(basename "$(dirname "$artifact")")
        base=$(basename "$artifact")
        prov="manifests/provenance/$comp/$base.provenance.yaml"
        sum="manifests/provenance/$comp/$base.sha256"

        if grep -q "^# Source chart sha256:" "$VALIDATE_ROOT/$artifact" 2>/dev/null; then
            hard_fail "$artifact records a digest inside itself; writing it changes the bytes it claims to cover, so it can never verify"
        fi
        if [[ ! -f "$VALIDATE_ROOT/$prov" || ! -f "$VALIDATE_ROOT/$sum" ]]; then
            hard_fail "$artifact has no external provenance at $prov (ADR-029 §1: source, version and digest)"
            continue
        fi
        if (cd "$VALIDATE_ROOT" && shasum -a 256 -c "$sum" >/dev/null 2>&1); then
            pass "$comp/$base matches its recorded digest"
        else
            hard_fail "$comp/$base does not match $sum -- the artefact changed and the digest was not re-recorded"
        fi
        if grep -q "repository:" "$VALIDATE_ROOT/$prov" && grep -q "version:" "$VALIDATE_ROOT/$prov"; then
            pass "$comp/$base records its upstream repository and version"
        else
            hard_fail "$prov omits the upstream repository or version"
        fi
    done
}

#!/usr/bin/env bash
# ADR-046 §11 defines exactly two placement classes for stateful workloads, and
# each pairs a location with the only storage its nodes can attach:
#
#   home     workload-location: home     -> local-path      (home-lab Flatcar nodes)
#   hetzner  workload-location: hetzner  -> hcloud-volumes  (Hetzner nodes)
#
# Mixing them is BANNED. hcloud-volumes cannot be attached by a home VM, so the PVC
# binds nowhere and the pod sits Pending; §11 calls that "the correct failure mode",
# which is precisely why it is invisible — nothing errors, the bootstrap just stops.
#
# This drifted once already: platform-db declared its placement while redis and nats
# did not, and each failed in turn on separate days. The check exists so the third
# case is caught here rather than 40 minutes into a bootstrap.
validate_placement_class() {
    section "ADR-046 §11 placement classes (rendered per provider)"

    local out
    out=$(cd "$VALIDATE_ROOT" && python3 - <<'PY'
import glob, os, subprocess, sys, yaml

# The location a provider's workers live in, and the only class they can bind.
EXPECTED = {"hybrid": ("home", "local-path"), "hetzner": ("hetzner", "hcloud-volumes")}

# Hub provider overlays AND spoke-catalog environment overlays. The spoke tree was
# missing here, and that is the whole reason this check passed while every hybrid
# spoke CNPG asked for hcloud-volumes with no nodeSelector at all — the exact
# condition §11 was written to ban, in the exact file it was written about.
#   manifests/hub-core-services/providers/<provider>/<component>
#   manifests/spoke/spoke-catalog/environments/<env>/<provider>
roots = sorted(glob.glob("manifests/hub-core-services/providers/*/*")) \
      + sorted(glob.glob("manifests/spoke/spoke-catalog/environments/*/*"))
if not roots:
    print("NONE")
    raise SystemExit

for root in roots:
    # provider is the parent dir for hub overlays and the LEAF for spoke overlays.
    parts = root.split(os.sep)
    provider = parts[-1] if "spoke-catalog" in root else parts[-2]
    want = EXPECTED.get(provider)
    if want is None:
        print(f"BAD\t{root}\tunknown provider directory {provider!r}")
        continue
    want_loc, want_sc = want

    r = subprocess.run(["kubectl", "kustomize", root], capture_output=True, text=True)
    if r.returncode != 0:
        print(f"BAD\t{root}\tdoes not build: {r.stderr.strip().splitlines()[0] if r.stderr.strip() else 'unknown'}")
        continue

    for doc in yaml.safe_load_all(r.stdout):
        if not doc:
            continue
        kind, name = doc.get("kind"), doc.get("metadata", {}).get("name", "?")
        spec = doc.get("spec", {})

        if kind == "StatefulSet":
            vcts = spec.get("volumeClaimTemplates") or []
            if not vcts:
                continue
            sel = (spec.get("template", {}).get("spec", {}) or {}).get("nodeSelector") or {}
            scs = [v.get("spec", {}).get("storageClassName") for v in vcts]
        elif kind == "Cluster" and "storage" in spec:   # CNPG
            sel = (spec.get("affinity", {}) or {}).get("nodeSelector") or {}
            scs = [spec.get("storage", {}).get("storageClass")]
        else:
            continue

        # ADR-014 applies to both classes and is not optional.
        if sel.get("node-role.kubernetes.io/worker") is None:
            print(f"BAD\t{root}\t{kind}/{name} has no node-role.kubernetes.io/worker selector (ADR-014)")

        loc = sel.get("workload-location")
        if loc != want_loc:
            print(f"BAD\t{root}\t{kind}/{name} workload-location={loc!r}, expected {want_loc!r}")

        for sc in scs:
            if sc != want_sc:
                print(f"BAD\t{root}\t{kind}/{name} storageClass={sc!r}, expected {want_sc!r} for a {want_loc} workload")
PY
)

    if [[ "$out" == "NONE" ]]; then
        hard_fail "no provider overlays found under manifests/hub-core-services/providers/"
        return 0
    fi

    if [[ -z "$out" ]]; then
        pass "every provider overlay renders a consistent §11 placement class"
    else
        local tag root msg
        while IFS=$'\t' read -r tag root msg; do
            [[ -z "$tag" ]] && continue
            hard_fail "$root — $msg"
        done <<< "$out"
    fi
}

# A base that still carries location-specific settings defeats the overlays: it
# would apply to every provider, which is how the mixed class arises in the first
# place. The bases must declare the ADR-014 worker selector and nothing more.
validate_bases_are_provider_neutral() {
    section "Placement bases carry no provider-specific settings"

    local out
    out=$(cd "$VALIDATE_ROOT" && python3 - <<'PY'
import glob, yaml

for comp in ("database", "redis", "nats"):
    for f in glob.glob(f"manifests/hub-core-services/{comp}/*.yaml"):
        try:
            docs = [d for d in yaml.safe_load_all(open(f)) if d]
        except Exception:
            continue
        for d in docs:
            kind, spec = d.get("kind"), d.get("spec", {})
            name = d.get("metadata", {}).get("name", "?")
            if kind == "StatefulSet":
                sel = (spec.get("template", {}).get("spec", {}) or {}).get("nodeSelector") or {}
                scs = [v.get("spec", {}).get("storageClassName")
                       for v in (spec.get("volumeClaimTemplates") or [])]
            elif kind == "Cluster" and "storage" in spec:
                sel = (spec.get("affinity", {}) or {}).get("nodeSelector") or {}
                scs = [spec.get("storage", {}).get("storageClass")]
            else:
                continue
            if "workload-location" in sel:
                print(f"{f}: {kind}/{name} pins workload-location in the BASE")
            for sc in scs:
                if sc:
                    print(f"{f}: {kind}/{name} pins storageClass={sc!r} in the BASE")
PY
)

    if [[ -z "$out" ]]; then
        pass "bases declare only the ADR-014 worker selector"
    else
        while IFS= read -r line; do
            [[ -n "$line" ]] && hard_fail "$line"
        done <<< "$out"
    fi
}

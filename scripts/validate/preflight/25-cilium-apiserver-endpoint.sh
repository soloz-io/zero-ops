#!/usr/bin/env bash
# Cilium must be able to reach the API server on a cold boot (ADR-046 §24).
#
# The failure this encodes: the shared ClusterClass deletes kube-proxy right after
# `kubeadm init`, because its REJECT chains for endpoint-less services break Gateway
# API under Cilium's full kube-proxy-replacement. Cilium is the intended replacement
# — but with no `k8s-service-host` it discovers the API server through the in-cluster
# ClusterIP (10.96.0.1), the address kube-proxy used to route and that Cilium has not
# yet programmed. A fresh spoke deadlocks: the `config` init container times out, the
# CNI never initialises, the node stays NotReady, and every workload sits Pending.
#
# It stayed latent for weeks because the kube-proxy deletion was applied to a RUNNING
# spoke, where Cilium was already up and deleting kube-proxy is harmless. Only a cold
# boot reproduces it, and ADR-046 §17.3 made spokes reprovision-only — so the first
# cold boot after that change is where it fired. Nothing static caught it, which is
# why this check is static.
#
# The invariant, in one line: if kube-proxy is deleted and Cilium is the replacement,
# something must supply the API endpoint before the first node boots.
validate_cilium_apiserver_endpoint() {
    section "Cilium reaches the API server on a cold boot (ADR-046 §24)"

    local cc="$VALIDATE_ROOT/manifests/providers/_shared/spokepool-clusterclass-v1.yaml"
    local kube_proxy_deleted=0
    if grep -q 'delete daemonset -n kube-system kube-proxy' "$cc" 2>/dev/null; then
        kube_proxy_deleted=1
    fi

    if (( ! kube_proxy_deleted )); then
        pass "spoke ClusterClass keeps kube-proxy — the ClusterIP path still works"
        return 0
    fi
    note "spoke ClusterClass deletes kube-proxy at init; Cilium must have a direct endpoint"

    # 1. Exactly one owner. An addon Secret that still ships a cilium-config document
    #    would race the rendered one, and which wins would depend on CRS apply order.
    local addon
    for addon in "manifests/providers/hybrid/k8s/cilium-addon-hybrid.yaml" \
                 "manifests/spoke/spoke-bootstrap/cilium-addon-template.yaml"; do
        [[ -f "$VALIDATE_ROOT/$addon" ]] || continue
        if (cd "$VALIDATE_ROOT" && python3 - "$addon" <<'PY'
import sys, yaml
doc = yaml.safe_load(open(sys.argv[1]))
payload = (doc.get("stringData") or doc.get("data") or {}).get("cilium.yaml", "")
found = any(d and d.get("kind") == "ConfigMap" and d["metadata"]["name"] == "cilium-config"
            for d in yaml.safe_load_all(payload))
sys.exit(1 if found else 0)
PY
        ); then
            pass "$(basename "$addon") ships no cilium-config — single owner preserved"
        else
            hard_fail "$addon still ships a cilium-config ConfigMap; hub-operator also renders one — two owners for one object"
        fi
    done

    # 2. The renderer exists. If it is deleted while the split stays, no spoke gets a
    #    cilium-config at all — a worse failure than the one this replaced.
    local ctrl="$VALIDATE_ROOT/operators/hub-operator/internal/controller/spokepool_controller.go"
    if grep -q 'func (r \*SpokePoolReconciler) ensureCiliumConfigCRSWrapper' "$ctrl" 2>/dev/null &&
       grep -q 'r.ensureCiliumConfigCRSWrapper(ctx, spokePool)' "$ctrl" 2>/dev/null; then
        pass "hub-operator renders the cilium-config CRS wrapper and calls it from Reconcile"
    else
        hard_fail "hub-operator no longer renders cilium-config, but the addons were split on the assumption that it does — spokes would boot with no CNI config"
    fi

    # 3. The base carries the settings and NOT the endpoint. A pinned endpoint in Git
    #    cannot be right for more than one spoke, and a stale one is worse than none.
    local base provider found_base=0
    for base in "manifests/providers/hybrid/k8s/cilium-config-base.yaml" \
                "manifests/providers/hetzner/k8s/cilium-config-base.yaml"; do
        [[ -f "$VALIDATE_ROOT/$base" ]] || { hard_fail "missing cilium-config base: $base"; continue; }
        found_base=1
        provider=$(basename "$(dirname "$(dirname "$base")")")
        if (cd "$VALIDATE_ROOT" && python3 - "$base" <<'PY'
import sys, yaml
d = yaml.safe_load(open(sys.argv[1]))
data = d.get("data") or {}
bad = [k for k in ("k8s-service-host", "k8s-service-port") if k in data]
if bad:
    print(",".join(bad)); sys.exit(1)
if data.get("kube-proxy-replacement") != "true":
    print("kube-proxy-replacement not true"); sys.exit(2)
sys.exit(0)
PY
        ); then
            pass "$provider base defines the settings, leaves the endpoint to the renderer"
        else
            hard_fail "$base pins k8s-service-host/port, or does not set kube-proxy-replacement — the endpoint is per-spoke and belongs to hub-operator alone"
        fi
    done
    (( found_base )) || hard_fail "no cilium-config base found; the split is half-applied"

    # 4. Both compositions must deliver the wrapper, or that provider's spokes get a
    #    DaemonSet with no config.
    local comp
    for comp in "manifests/providers/hybrid/k8s/spokepool-hybrid-composition.yaml" \
                "manifests/providers/hetzner/k8s/spokepool-hetzner-composition.yaml"; do
        if grep -q '%s-cilium-config' "$VALIDATE_ROOT/$comp" 2>/dev/null; then
            pass "$(basename "$comp" | cut -d- -f2) composition registers the cilium-config wrapper"
        else
            hard_fail "$comp does not register {spoke}-cilium-config in its ClusterResourceSet"
        fi
    done

    # 5. The endpoint's only source. CAPH populates controlPlaneEndpoint from the
    #    load balancer it creates; with the LB disabled there is no endpoint, and the
    #    renderer would defer forever with no visible cause on the spoke.
    # Parsed, not grepped: the default sits under a nested openAPIV3Schema, so a
    # line-window grep reports a false failure (it did, on the first run of this
    # check — the manifest was correct).
    if (cd "$VALIDATE_ROOT" && python3 - "manifests/providers/_shared/spokepool-clusterclass-v1.yaml" <<'PY'
import sys, yaml
for doc in yaml.safe_load_all(open(sys.argv[1])):
    if not doc or doc.get("kind") != "ClusterClass":
        continue
    for v in doc["spec"].get("variables", []):
        if v["name"] == "controlPlaneLoadBalancerEnabled":
            sys.exit(0 if v["schema"]["openAPIV3Schema"].get("default") is True else 1)
sys.exit(1)
PY
    ); then
        pass "controlPlaneLoadBalancerEnabled defaults true — the endpoint has a source"
    else
        hard_fail "spoke ClusterClass does not default controlPlaneLoadBalancerEnabled=true; without the LB there is no controlPlaneEndpoint and cilium-config can never render"
    fi

    # 6. The hub is not a spoke: it has no SpokePool and no renderer, and it keeps
    #    kube-proxy. It therefore installs the base verbatim, joined back on at Day-0.
    #    If that concatenation is dropped the hub boots with no cilium-config at all.
    local day0="$VALIDATE_ROOT/internal/hub-cli/bootstrap/provider_cloud.go"
    if grep -q 'cilium-config-base.yaml' "$day0" 2>/dev/null; then
        pass "hub Day-0 install recomposes the addon with the cilium-config base"
    else
        hard_fail "hub Day-0 CNI install no longer reads cilium-config-base.yaml — the hub would boot with no cilium-config"
    fi
}

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

    local cc="$VALIDATE_ROOT/manifests/providers/hetzner/base/spokepool-clusterclass-v1.yaml"
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

    # 1b. The ConfigMap value alone is NOT sufficient, and this arm exists because
    #     shipping it that way did not work.
    #
    #     The `config` init container's job is to READ cilium-config from the API.
    #     It cannot use a value inside that ConfigMap to find the API — circular —
    #     so it uses KUBERNETES_SERVICE_HOST, which kubelet defaults to the
    #     ClusterIP. Upstream's chart injects that env var on the containers when
    #     k8sServiceHost is set; a split that populates only the ConfigMap leaves
    #     the agent still dialling 10.96.0.1 and still deadlocked.
    #
    #     configMapKeyRef keeps one owner: the DaemonSet names a KEY, the rendered
    #     ConfigMap supplies the VALUE, and kubelet resolves it at pod creation over
    #     its own kubeconfig. optional:true is required — the hub's base has no such
    #     key and must keep using the ClusterIP, which is correct there because the
    #     hub retains kube-proxy.
    for addon in "manifests/providers/hybrid/k8s/cilium-addon-hybrid.yaml" \
                 "manifests/spoke/spoke-bootstrap/cilium-addon-template.yaml"; do
        [[ -f "$VALIDATE_ROOT/$addon" ]] || continue
        if (cd "$VALIDATE_ROOT" && python3 - "$addon" <<'PY'
import sys, yaml
doc = yaml.safe_load(open(sys.argv[1]))
need = {("DaemonSet", "cilium"): {"config", "cilium-agent"},
        ("Deployment", "cilium-operator"): {"cilium-operator"}}
seen = set()
for d in yaml.safe_load_all((doc.get("stringData") or {}).get("cilium.yaml", "")):
    if not d:
        continue
    key = (d.get("kind"), d.get("metadata", {}).get("name"))
    if key not in need:
        continue
    spec = d["spec"]["template"]["spec"]
    for grp in ("initContainers", "containers"):
        for c in spec.get(grp, []):
            if c["name"] not in need[key]:
                continue
            envs = {e["name"]: e for e in c.get("env", [])}
            for var, k in (("KUBERNETES_SERVICE_HOST", "k8s-service-host"),
                           ("KUBERNETES_SERVICE_PORT", "k8s-service-port")):
                ref = (envs.get(var) or {}).get("valueFrom", {}).get("configMapKeyRef")
                if not ref or ref.get("name") != "cilium-config" \
                   or ref.get("key") != k or ref.get("optional") is not True:
                    print(f"{key[1]}/{c['name']} missing optional configMapKeyRef for {var}")
                    sys.exit(1)
            seen.add((key[1], c["name"]))
missing = {(k[1], c) for k, cs in need.items() for c in cs} - seen
if missing:
    print("containers not found: " + ", ".join(f"{a}/{b}" for a, b in sorted(missing)))
    sys.exit(1)
sys.exit(0)
PY
        ); then
            pass "$(basename "$addon") wires KUBERNETES_SERVICE_HOST/PORT from cilium-config"
        else
            hard_fail "$addon does not wire KUBERNETES_SERVICE_HOST/PORT via optional configMapKeyRef — the config init container will keep dialling the ClusterIP and deadlock, even with a correct ConfigMap"
        fi
    done

    # 1c. cilium-operator must fit on one node (ADR-046 §24.3).
    #
    #     It declares hostPort 9963 with hostNetwork, so two replicas can never share
    #     a node — and a hybrid spoke is single-node AT BOOT by construction: burst
    #     workers are replicas:0 and home workers join only after the control plane
    #     is Ready. Two replicas deadlock the cold boot in every environment:
    #     operator Unschedulable -> CRDs never registered -> agent never Ready ->
    #     Node never Ready -> the home-worker join never runs -> still one node.
    #
    #     Checked here rather than trusted to a comment: the manifest previously
    #     carried one asserting this was handled by a spoke-catalog overlay that did
    #     not exist and could not have worked (cilium is CRS-delivered; an ArgoCD
    #     overlay would be a second owner, which ADR-048 forbids).
    if (cd "$VALIDATE_ROOT" && python3 - "manifests/providers/hybrid/k8s/cilium-addon-hybrid.yaml" <<'PY'
import sys, yaml
doc = yaml.safe_load(open(sys.argv[1]))
for d in yaml.safe_load_all((doc.get("stringData") or {}).get("cilium.yaml", "")):
    if d and d.get("kind") == "Deployment" and d["metadata"]["name"] == "cilium-operator":
        ports = [p.get("hostPort") for c in d["spec"]["template"]["spec"]["containers"]
                 for p in c.get("ports", []) if p.get("hostPort")]
        replicas = d["spec"].get("replicas", 1)
        # Only a hostPort-bound operator is constrained; if upstream ever drops the
        # hostPort, more replicas become legitimate and this check should not block.
        if ports and replicas != 1:
            print("replicas>1 with a hostPort"); sys.exit(1)
        # Scaling to 1 is not enough on its own: a surge-based rollout keeps the old
        # pod (which holds the hostPort) until the new one is Ready, so the new one
        # can never schedule. With replicas:1 the percentage defaults resolve to
        # maxSurge=1 / maxUnavailable=0 — the deadlock. Recreate, or an explicit
        # zero surge, is required. Observed live before it was fixed.
        strat = d["spec"].get("strategy", {})
        if ports and strat.get("type") != "Recreate":
            surge = str(strat.get("rollingUpdate", {}).get("maxSurge", "25%"))
            if surge not in ("0", "0%"):
                print(f"hostPort workload uses surge rollout (type={strat.get('type')}, maxSurge={surge})")
                sys.exit(1)
        sys.exit(0)
sys.exit(1)
PY
    ); then
        pass "hybrid cilium-operator fits a single-node spoke and can roll without deadlocking"
    else
        hard_fail "hybrid cilium-operator cannot converge on a single-node spoke — either >1 replica with a hostPort, or a surge-based rollout whose old pod holds the port until the new one is Ready (which it never can be)"
    fi

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
    if (cd "$VALIDATE_ROOT" && python3 - "manifests/providers/hetzner/base/spokepool-clusterclass-v1.yaml" <<'PY'
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

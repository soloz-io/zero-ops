#!/usr/bin/env bash
# The spoke cold-boot causal chain (ADR-046 §24).
#
#   controlPlaneEndpoint populated
#        -> {spoke}-cilium-config CRS wrapper exists
#        -> wrapper carries THAT endpoint
#        -> kube-system/cilium-config on the spoke carries it too
#        -> Cilium agent Ready
#        -> control-plane Node Ready
#        -> home worker Node Ready with the placement contract
#        -> argocd-agent Ready
#        -> spoke catalog reconciled (CRDs present)
#        -> CNPG installed
#        -> CNPG has an eligible placement target
#
# Every link is asserted against live state. None is inferred from the one before,
# because the failure this encodes broke a link in the middle while both ends looked
# fine: the SpokePool XR reported Ready=True/Available and the CAPI Cluster reported
# Provisioned for five hours while the spoke had no CNI, no worker, no CRDs and no
# workloads. Anything that reports health without observing it is how that happened.
#
# A link that cannot be checked FAILS. It does not pass, and it does not skip.
validate_spoke_cilium_chain() {
    section "Spoke cold-boot chain (ADR-046 §24)"

    if [[ -z "$SPOKEPOOL_NAME" ]]; then
        soft_fail "SPOKEPOOL_NAME unset — the spoke cold-boot chain was not verified"
        return 0
    fi

    # ── Link 1: controlPlaneEndpoint — the sole source for the endpoint ──────
    local host port
    host=$(kc get clusters.cluster.x-k8s.io "$SPOKEPOOL_NAME" -n platform-capi -o jsonpath='{.spec.controlPlaneEndpoint.host}')
    port=$(kc get clusters.cluster.x-k8s.io "$SPOKEPOOL_NAME" -n platform-capi -o jsonpath='{.spec.controlPlaneEndpoint.port}')
    if [[ -n "$host" && -n "$port" && "$port" != "0" ]]; then
        pass "controlPlaneEndpoint populated: ${host}:${port}"
    else
        hard_fail "controlPlaneEndpoint is empty — CAPH has not created the load balancer, so cilium-config can never render (check HetznerCluster for LoadBalancerCreateFailed)"
        return 0
    fi

    # ── Link 2: the CRS wrapper exists and is the rendered one ───────────────
    local wrapper="${SPOKEPOOL_NAME}-cilium-config" wtype
    wtype=$(kc get secret "$wrapper" -n platform-capi -o jsonpath='{.type}')
    if [[ "$wtype" == "addons.cluster.x-k8s.io/resource-set" ]]; then
        pass "CRS wrapper $wrapper exists"
    else
        hard_fail "CRS wrapper $wrapper missing — hub-operator has not rendered it (old operator image, or it is deferring; check its logs)"
        return 0
    fi

    # ── Link 3: the wrapper carries THAT endpoint, not a stale one ───────────
    local wrapped_host wrapped_port payload
    payload=$(kc get secret "$wrapper" -n platform-capi -o jsonpath='{.data.cilium-config\.yaml}' | base64 -d 2>/dev/null)
    wrapped_host=$(sed -n 's/^ *k8s-service-host: *"\{0,1\}\([^"]*\)"\{0,1\} *$/\1/p' <<<"$payload" | head -1)
    wrapped_port=$(sed -n 's/^ *k8s-service-port: *"\{0,1\}\([^"]*\)"\{0,1\} *$/\1/p' <<<"$payload" | head -1)
    if [[ "$wrapped_host" == "$host" && "$wrapped_port" == "$port" ]]; then
        pass "wrapper carries the live endpoint (${wrapped_host}:${wrapped_port})"
    else
        hard_fail "wrapper endpoint ${wrapped_host:-<empty>}:${wrapped_port:-<empty>} does not match the cluster's ${host}:${port} — the spoke would look for the API server at the wrong address"
    fi

    if ! ensure_spoke_kubeconfig; then
        hard_fail "spoke API unreachable — the rest of the cold-boot chain cannot be verified, and an unverified chain is exactly what ADR-046 §24 forbids claiming"
        return 0
    fi

    # ── Link 4: the delivered ConfigMap carries it too ───────────────────────
    # CRS guarantees creation, not steady state (ADR-048 amendment): a payload
    # deleted after a successful apply stays deleted, and the binding still reads
    # applied: true. So the live object is checked, never the binding.
    local live_host live_port kpr
    live_host=$(kc_spoke get cm cilium-config -n kube-system -o jsonpath='{.data.k8s-service-host}')
    live_port=$(kc_spoke get cm cilium-config -n kube-system -o jsonpath='{.data.k8s-service-port}')
    kpr=$(kc_spoke get cm cilium-config -n kube-system -o jsonpath='{.data.kube-proxy-replacement}')
    if [[ "$live_host" == "$host" && "$live_port" == "$port" ]]; then
        pass "spoke cilium-config carries the endpoint (${live_host}:${live_port})"
    else
        hard_fail "spoke cilium-config endpoint is ${live_host:-<empty>}:${live_port:-<empty>}, expected ${host}:${port} — this is the exact state that deadlocked the cold boot"
    fi

    # kube-proxy is deleted by the ClusterClass; with KPR on and no endpoint there is
    # no route to the API at all. Asserted together because either alone is survivable.
    if [[ "$kpr" == "true" ]] && kc_spoke get ds kube-proxy -n kube-system >/dev/null 2>&1; then
        warn "both kube-proxy and Cilium kube-proxy-replacement are active — addendum 1 removes kube-proxy for a reason"
    elif [[ "$kpr" == "true" && -z "$live_host" ]]; then
        hard_fail "kube-proxy-replacement=true with no k8s-service-host and no kube-proxy — nothing can route the API ClusterIP"
    fi

    # ── Link 5: Cilium actually Ready (not merely scheduled) ─────────────────
    local cil_ready cil_desired
    cil_ready=$(kc_spoke get ds cilium -n kube-system -o jsonpath='{.status.numberReady}')
    cil_desired=$(kc_spoke get ds cilium -n kube-system -o jsonpath='{.status.desiredNumberScheduled}')
    if [[ -n "$cil_ready" && "${cil_ready:-0}" -ge 1 && "$cil_ready" == "$cil_desired" ]]; then
        pass "Cilium agent Ready on all $cil_ready node(s)"
    else
        hard_fail "Cilium agent ${cil_ready:-0}/${cil_desired:-0} Ready — check the 'config' init container; a timeout on 10.96.0.1 means the endpoint never reached the agent"
    fi

    # ── Link 6: control-plane Node Ready ─────────────────────────────────────
    _chain_nodes_ready "control plane" 'node-role.kubernetes.io/control-plane' \
        "control-plane Node not Ready — 'cni plugin not initialized' means link 5 has not taken effect"

    # ── Link 7: home worker Ready AND carrying the placement contract ────────
    # Both labels, per ADR-014 + §11. A Ready node without workload-location=home
    # satisfies nothing that is scheduled against it.
    _chain_nodes_ready "home worker" 'workload-location=home,node-role.kubernetes.io/worker' \
        "no Ready node carries workload-location=home + node-role.kubernetes.io/worker — Step 10e did not converge, and every stateful workload will sit Pending"

    # ── Link 8: argocd-agent Ready — the spoke's only GitOps path ────────────
    local agent
    agent=$(kc_spoke get deployment argocd-agent -n argocd -o jsonpath='{.status.readyReplicas}')
    if [[ "${agent:-0}" -ge 1 ]]; then
        pass "argocd-agent Ready ($agent replica(s))"
    else
        hard_fail "argocd-agent not Ready — nothing delivers the spoke catalog, so every CRD and controller below is unreachable"
    fi

    # ── Link 9: the catalog actually reconciled ──────────────────────────────
    local crd missing=()
    for crd in clusters.postgresql.cnpg.io externalsecrets.external-secrets.io \
               certificates.cert-manager.io clusterpolicies.kyverno.io; do
        kc_spoke get crd "$crd" >/dev/null 2>&1 || missing+=("$crd")
    done
    if (( ${#missing[@]} == 0 )); then
        pass "spoke catalog reconciled (platform CRDs established)"
    else
        hard_fail "spoke catalog has not reconciled — missing CRDs: ${missing[*]}"
    fi

    # ── Link 10 + 11: CNPG installed, and its placement is satisfiable ───────
    local cnpg
    cnpg=$(kc_spoke get deployment cnpg-cloudnative-pg -n cnpg-system -o jsonpath='{.status.readyReplicas}')
    if [[ "${cnpg:-0}" -ge 1 ]]; then
        pass "CNPG operator Ready"
    else
        hard_fail "CNPG operator not Ready — no database can be provisioned on this spoke"
    fi

    # The placement rule is only satisfiable if a schedulable node matches BOTH
    # selectors. A Pending CNPG PVC is the correct failure mode for a missing home
    # worker (REMEMBER.md), so the eligibility is asserted rather than the PVC.
    # Filter in the shell, not in jsonpath. `?(@.spec.unschedulable!=true)` does NOT
    # match objects where the field is absent — and it is absent on every schedulable
    # node — so that filter reports zero eligible nodes while a healthy one is sitting
    # right there. This check produced exactly that false alarm on 2026-08-23.
    local eligible=0 line
    while IFS= read -r line; do
        [[ -z "$line" ]] && continue
        [[ "${line#*=}" == "true" ]] && continue    # cordoned
        eligible=$((eligible + 1))
    done < <(kc_spoke get nodes -l 'workload-location=home,node-role.kubernetes.io/worker' \
        -o jsonpath='{range .items[*]}{.metadata.name}{"="}{.spec.unschedulable}{"\n"}{end}')
    if [[ "${eligible:-0}" -ge 1 ]]; then
        pass "CNPG placement satisfiable ($eligible schedulable home worker(s))"
    else
        hard_fail "no schedulable node satisfies the ADR-014 + §11 placement class — CNPG will stay Pending with a healthy-looking cluster around it"
    fi
}

# Ready is asserted on the Node condition. Never on a CAPI Machine: a home worker
# has no Machine at all, so a Machine count is structurally blind to it — which is
# how the previous Step 10e passed on a cluster with zero workers.
_chain_nodes_ready() {
    local what="$1" selector="$2" failure="$3"
    local line ready=0 total=0
    while IFS= read -r line; do
        [[ -z "$line" ]] && continue
        total=$((total + 1))
        [[ "${line#*=}" == "True" ]] && ready=$((ready + 1))
    done < <(kc_spoke get nodes -l "$selector" \
        -o jsonpath='{range .items[*]}{.metadata.name}{"="}{.status.conditions[?(@.type=="Ready")].status}{"\n"}{end}')

    if (( ready >= 1 )); then
        pass "$what Node Ready ($ready/$total)"
    else
        hard_fail "$failure (matched $total node(s), $ready Ready)"
    fi
}

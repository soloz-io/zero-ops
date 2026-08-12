You can run a **hybrid Kubernetes cluster** where:

```text
                    Kubernetes
                        │
          ┌─────────────┴─────────────┐
          │                           │
   Home Windows machine          Hetzner Cloud
      ~16 GB RAM                 additional nodes
          │                           │
          └───────────┬───────────────┘
                      │
                   Mac Mini
                  pure client
```

The important distinction is that **the local Windows machine should be a real Kubernetes node**, not a k3d/CAPD node pretending to be infrastructure.

### What I'd recommend

Run **K3s**:

```text
Windows/WSL2
  └── K3s server
        │
        ├── local workloads
        │
        └── Hetzner K3s agents
              ├── cloud workload 1
              ├── cloud workload 2
              └── ...
```

Kubernetes will schedule pods across the local and Hetzner nodes according to available resources and your scheduling rules.

You can also control placement:

```yaml
nodeSelector:
  workload: local
```

or:

```yaml
nodeSelector:
  workload: cloud
```

So you could reserve your ~16 GB home capacity for lightweight services and burst larger workloads into Hetzner.

### But there is one major networking issue

Your Windows machine is **behind your home NAT**, while Hetzner nodes are on the public Internet.

You therefore need a private network between them. I would use **WireGuard** or **Tailscale** rather than exposing Kubernetes node ports directly to the Internet.

For example:

```text
Mac
 │
 │ SSH
 ▼
Windows/WSL2
 │
 │ WireGuard/Tailscale
 │
 ├─────────────── Hetzner node 1
 ├─────────────── Hetzner node 2
 └─────────────── Hetzner node 3
```

Then K3s can communicate over the private overlay network.

### And this fits your existing project better

Instead of:

```text
CAPI → Hetzner → VMs
```

or:

```text
CAPI → Docker → containers
```

I'd move toward:

```text
CAPI/bootstrap
      ↓
K3s cluster
   ├── Home node
   └── Hetzner nodes
```

Your **Mac remains completely disposable as a Kubernetes client**.

One caveat: because this is a hybrid home/cloud cluster, **losing your home Internet connection takes the local node away**, so I'd keep critical control-plane components on Hetzner rather than making the home Windows machine the only server.

I can sketch the exact topology for **1 Windows + 2 Hetzner nodes, including WireGuard, K3s server/agents, and how your existing Hub/Spoke CAPI model would fit into it**.


My recommendation:

1. **CAPI vs K3s:** Keep **CAPI**. Don't replace the platform with hand-managed K3s. Use CAPI to manage the clusters, but for the hybrid cluster use an infrastructure approach that can genuinely span home + Hetzner. I would **not use CAPD/k3d for the workload nodes**; CAPD is primarily a dev/test provider.

2. **Goal:** Primarily **save money on dev/staging** by using the ~16 GB home capacity, while retaining Hetzner for overflow/heavier workloads.

3. **Control plane:** Put the **control plane in Hetzner**. The home Windows machine should be a worker. That way a home Internet outage removes one worker, but the cluster remains manageable.

4. **Networking:** **Tailscale** is my preference initially. It is much easier to get the home↔Hetzner connectivity working and maintain it. Once everything is stable, WireGuard is an option if you want a more minimal setup.

5. **Hub/Spoke:** Keep the existing **Hub/Spoke isolation**. Don't turn the Hub into a special hybrid provider. Instead, make the infrastructure/provider configuration explicit for a hybrid environment.

So the target architecture I'd pursue is:

```text
                         Mac Mini
                    kubectl / CAPI CLI
                            │
                            │ SSH / Tailscale
                            ▼
                 ┌─────────────────────┐
                 │     Hetzner Cloud   │
                 │                     │
                 │  K8s control plane  │
                 │       + workers     │
                 └──────────┬──────────┘
                            │
                         Tailscale
                            │
                 ┌──────────▼──────────┐
                 │ Windows Home Lab    │
                 │                     │
                 │ K8s worker          │
                 │ ~16 GB RAM          │
                 └─────────────────────┘
```

The key design decision is therefore:

**CAPI stays → Hetzner remains the control-plane side → home Windows becomes a real Kubernetes worker → Tailscale provides the private network.**

I would change your earlier migration plan substantially rather than proceeding with **CAPD/k3d as the production workload infrastructure**.


Yes — based on the codebase you mapped, I would lock in the following decisions.

### 1. Topology: choose (a)

Use **one spoke cluster with Hetzner control plane + Hetzner workers + one home worker over Tailscale**.

```text
             Hetzner
        ┌─────────────────┐
        │ Control plane   │
        │ Worker(s)       │
        └────────┬────────┘
                 │
              Tailscale
                 │
        ┌────────▼────────┐
        │ Home Windows    │
        │ WSL2 worker     │
        │ ~16 GB RAM      │
        └─────────────────┘
```

The existing CAPD/kind-on-Windows model should remain available for local development, but **it should not be the hybrid production/staging mechanism**.

### 2. Matrix: use a distinct `hybrid` provider cell

I agree with your lean toward:

```text
manifests/providers/hybrid/
```

rather than putting special behavior inside `hetzner`.

The reason is architectural: your ADRs say a provider cell is a real isolation boundary. A hybrid deployment has different networking, lifecycle, and worker semantics, so pretending it's ordinary Hetzner will eventually leak exceptions throughout the code.

I'd name it something like:

```text
hybrid
```

with an explicit relationship:

```text
hybrid = Hetzner infrastructure + home worker integration
```

The **Hetzner CAPI provider remains the actual infrastructure provider** for CAPI-managed machines. `hybrid` is your platform/provider cell, not a brand-new upstream CAPI infrastructure provider.

### 3. Home runtime: choose WSL2 Linux

Definitely **WSL2 Linux**, unless you explicitly need Windows containers/workloads.

Use:

```text
Windows
└── WSL2 Ubuntu
    ├── kubelet
    ├── containerd
    ├── kube-proxy
    └── Tailscale
```

This aligns with your existing SSH-based tooling and avoids introducing Windows-node Kubernetes complexity.

### 4. Home worker lifecycle: unmanaged convenience worker

I agree with your conclusion.

Don't try to manufacture a fake CAPI `Machine` without an infrastructure provider.

Instead:

```text
CAPI
  └── manages Hetzner control plane/workers

bootstrap script
  └── joins home WSL2 node with kubeadm
```

The join should be **idempotent**:

```text
1. Ensure Tailscale is connected
2. Check whether node is already part of cluster
3. If not, obtain/refresh kubeadm token
4. kubeadm join
5. Apply labels/taints
6. Verify Ready
```

I'd label it something explicit like:

```text
workload-location=home
node-role.kubernetes.io/home=
```

rather than calling it a normal worker role.

And I'd make scheduling intentional, e.g.:

```yaml
nodeSelector:
  workload-location: home
```

for things you specifically want on the home machine.

### 5. Control plane: Hetzner for both Hub and hybrid spoke control planes

I'd interpret your goal as:

```text
Hub
  → Hetzner control plane

Hybrid Spoke
  → Hetzner control plane
  → Hetzner workers
  → optional home worker
```

But **don't make every spoke hybrid automatically**.

Make hybrid an explicit **dev/staging topology** in the matrix:

```text
provider/environment
──────────────────────
local     → CAPD/k3d development
hetzner   → normal cloud
hybrid    → Hetzner + optional home capacity
```

That gives you a very clean escape hatch: production stays entirely Hetzner, while dev/staging can consume the 16 GB home capacity.

---

## One important design adjustment

I would **not make the home worker part of the ClusterClass itself**.

Keep ClusterClass describing the CAPI-managed cluster:

```text
ClusterClass
 ├── Hetzner control plane
 ├── Hetzner MachineDeployment(s)
 └── normal CAPI lifecycle
```

Then add a **hybrid worker integration layer** outside the MachineDeployment:

```text
Hybrid provider
 ├── Tailscale connectivity
 ├── kubeadm token provisioning
 ├── home-worker join
 ├── labeling
 └── rejoin/recovery
```

That preserves your CAPI model instead of trying to bend CAPI into managing something it doesn't own.

### Final target

```text
                         Mac Mini
                    pure client / CLI
                           │
                           ▼
                    Hetzner Hub
                   ┌──────────────┐
                   │ CAPI         │
                   │ Crossplane   │
                   │ ArgoCD       │
                   └──────┬───────┘
                          │
                    creates Hybrid Spoke
                          │
            ┌─────────────┴─────────────┐
            │                           │
      Hetzner nodes                Home worker
      CAPI-managed                 WSL2 + Tailscale
```

This is the direction I'd implement. The next design work should therefore focus on **how the hybrid cell mints/rotates the kubeadm join credentials and reconciles the home worker**, because that's the genuinely new piece in your codebase.

Yes. I’d lock the revised design as follows:

1. **Home workers:** 2 WSL2 Ubuntu nodes initially, scalable to 3. Treat each WSL2 instance as one Kubernetes worker with a unique hostname.

2. **Hetzner control plane:** **1 replica for dev**, **3 for staging/production**. Keep the control plane entirely in Hetzner.

3. **Hetzner worker pool:** Keep a CAPI `MachineDeployment` at **replicas: 0**. This preserves the burst path without ongoing cost.

4. **Scheduling:** Default workloads to home using the scheduling contract you proposed:

```yaml
nodeSelector:
  workload-location: home
```

Cloud workers, when scaled up, require explicit `workload-location: hetzner`.

5. **Networking:** I strongly prefer **the entire cluster network over Tailscale**, including the Kubernetes API, rather than exposing the API publicly and using Tailscale only for pod traffic.

That gives you:

```text
Mac
 └── kubectl / CAPI / Argo access
         │
      Tailscale
         │
Hetzner
 ├── K8s control plane
 │    └── API server
 │
 └──── Tailscale ──── Home
                       ├── WSL2 worker 1
                       └── WSL2 worker 2
```

This is cleaner and avoids maintaining two separate trust/network paths.

### One important correction

I would **not use a Tailscale subnet router as the primary Kubernetes data path**. Each WSL2 worker should run Tailscale itself and have a stable tailnet address. Then Kubernetes node-to-node/control-plane communication can use those addresses directly.

So the intended model is:

```text
CP node ─────── Tailscale ─────── Home worker 1
   │
   └─────────── Tailscale ─────── Home worker 2
```

rather than:

```text
CP → subnet router → home LAN → workers
```

### Resulting architecture

```text
                    Hetzner
             ┌──────────────────┐
             │ K8s Control Plane│
             │ 1 dev / 3 stg+   │
             └────────┬─────────┘
                      │
                   Tailscale
                      │
          ┌───────────┴───────────┐
          │                       │
      Home WSL2 #1            Home WSL2 #2
      worker-location=home    worker-location=home

          Optional burst:
             Hetzner workers
             replicas = 0
```

This preserves your **CAPI + provider matrix**, makes home capacity the default, and gives you a zero-cost cloud worker pool you can scale when the 16 GB home budget isn't enough.

I would now revise the implementation plan around exactly this topology.

All five locked. Here's the revised, implementation-ready plan for the "spoke = home lab, control plane = Hetzner, entire network on Tailscale" topology.
Revised topology
                Mac (kubectl/ArgoCD client) — Tailscale
                              │
                    ┌─────────▼─────────┐
                    │   Hetzner (Hub)   │  CAPI/Crossplane/ArgoCD already live here
                    └────────┬──────────┘
                             │
                        Tailscale (spoke API on tailnet — NOT public)
                             │
   ┌─────────────────────────┼───────────────────────────────┐
   │                         │                                │
Hetzner CP node          WSL2 worker 1                  WSL2 worker 2
  (1 dev / 3 stg)        workload-location=home        workload-location=home
- Hetzner: control plane only via CAPI (KubeadmControlPlane). Burst worker MachineDeployment remains at replicas: 0.
- Home: 2 WSL2 Ubuntu workers, each its own Tailscale node + stable tailnet address, joined via kubeadm.
- Networking: API server reachable only over Tailscale (MagicDNS, no public LB). Node-to-node + CP-to-worker data path runs over tailnet addresses. No subnet router.
Per-area changes
1. Shared provider base (moved up — required by the tailnet switch)
- New manifests/providers/_shared/hetzner-spoke-pool-v1.yaml — the HetznerClusterTemplate gains two variables: controlPlaneLoadBalancer.enabled (default true, keeps existing hetzner behavior) and controlPlaneEndpointHost (empty default; used to inject tailnet hostname).
- New manifests/providers/_shared/spoke-addons/{ccm,csi}-addon-template.yaml.
- Hetzner kustomization updated to reference shared files (kustomize forbids ../; shared base is the reuse vehicle, ADR-036 §7).
2. New provider cell manifests/providers/hybrid/
- kustomization.yaml → shared ClusterClass + shared addons + hetzner-credentials-es.yaml + new composition + tailscale-node additions + home-worker integration manifests.
- k8s/spokepool-hybrid-composition.yaml — clone of the hetzner composition with:
- label provider: hybrid;
- capi-cluster patched: controlPlaneLoadBalancer.enabled: false, controlPlaneEndpoint.host = tailnet MagicDNS name (e.g. zero-ops-hybrid-<cell>.tailnet.ts.net), control-plane replicas patched from claim (1 dev claim / 3 stg claim);
- topology.workers.machineDeployments[0].replicas patched from spec.nodePool.count — default 0 on the claims;
- a tailscale-node DaemonSet added to the spoke ClusterResourceSet so CAPI-managed CP nodes join the tailnet (per-node identity via node-name-derived tags);
- a home-worker-join-config ConfigMap carrying the home-node list (hostname, tailnet host, count) from claim annotations.
- k8s/home-worker-integration.yaml — RBAC for hub-operator token minting; tailscale pre-shared-key Secret consumed by the DaemonSet.
3. SpokePool claims
- manifests/spoke/spoke-pools/dev/hybrid/hybrid-dev.yaml — compositionSelector.matchLabels.provider: hybrid, region: fsn1, nodePool.count: 0, control-plane replicas 1, annotations: home-workers: [{hostname: wsl2-node-1, tailnet-host: ...}, {hostname: wsl2-node-2}].
- Same for stg/hybrid/ (control-plane replicas 3).
- Scheduling contract distributable from the claim: home is default, burst (hetser workers) require nodeSelector: workload-location: hetzner.
4. Bootstrap CLI + driver
- cmd/hub/bootstrap.go — add hybrid to validation + switch; new flags --home-worker-enabled, --home-worker-ttl, --tailnet-name.
- internal/hub-cli/bootstrap/driver_hybrid.go (new) — HybridDriver implementing CloudDriver, composing HetznerDriver (reuses Preflight, Day-0, CAPIProviders, ClusterClass paths; returns hybrid for helm provider). Adds write of the home-worker join config Secret into platform-capi.
- orchestrator.go:579 — hybrid passes through unchanged (dir = manifests/providers/hybrid).
5. Home worker token minting (extend hub-operator)
operators/hub-operator/internal/controller/spokepool_controller.go — add reconcileHomeWorkerJoin gated on provider=hybrid:
1. Wait for CAPI Cluster Ready (status.phase == Provisioned).
2. Read <spoke>-kubeconfig secret (existing observer/creator pattern in spokepool-capd-single-composition.yaml:439).
3. Since home workers join after the CRS DaemonSet brings CP nodes into the tailnet, mint a kubeadm bootstrap-token-<id> Secret (type bootstrap.kubernetes.io/token, TTL --home-worker-ttl) in the spoke kube-system via pure API — no node exec.
4. Compute discovery-token-ca-cert-hash from the kubeconfig CA cert.
5. Write payload K8s Secret {spoke}-home-worker-join (per-node entries) + push to Infisical via existing InfisicalClient (ADR-031 pattern).
6. Rotate token before expiry; idempotent reconcile loop.
6. WSL2 side
- scripts/hybrid/setup-wsl2-node.sh — install Tailscale + containerd/kubelet/kubeadm pinned to the ClusterClass versions (containerd 1.7.26, runc 1.2.5, k8s 1.31.6 per spokepool-hetzner-v1.yaml:162-201); enable systemd; swapoff; CNI sysctls.
- scripts/hybrid/home-worker-join.sh <node-index> — idempotent per node: tailscale up → if kubectl get node <hostname> exists, label+verify and exit → fetch join payload → kubeadm join <tailnet host>:443 --discovery-token-ca-cert-hash → apply workload-location=home, node-role.kubernetes.io/home= → verify Ready.
7. Burst workers label (additive to shared ClusterClass)
spokepool-hetzner-v1.yaml worker KubeadmConfigTemplate gets kubelet --node-labels=workload-location=hetzner so scaled-up burst workers are self-labeled.
8. Default scheduling
Documented contract + applied to new/updated spoke workload templates as nodeSelector: workload-location: home. Existing spoke-catalog manifests are touched only where behaviorally safe (I'd scope this as a follow-up pass, not blocking the hybrid cell).
9. ADR-046
docs/adr/046-hybrid-provider-home-worker.md — matrix row (local/hetzner/hybrid), tailnet-only networking decision, token lifecycle, home-as-default scheduling, burst semantics, and the "ClusterClass stays CAPI-managed; home workers are unmanaged" boundary.
Two remaining design decisions
1. stg/prod 3-CP tailnet front. kubeadm multi-replica control planes need a load balancer at controlPlaneEndpoint, and with the API tailnet-only that LB must live on the tailnet. My recommendation: a lightweight Tailscale-enrolled TCP front (the Hub node, or a small tailnet node running haproxy→CP nodes) whose MagicDNS name is the stable controlPlaneEndpoint. For dev (1 CP) the endpoint can be the single CP node's tailnet address directly. Do you want the Hub as the spoke-API tailnet front for both, or a dedicated tailnet node?
2. Burst-worker prerequisites. When you later scale the 0-replica hetzner workers to burst, they also need Tailscale (CRS DaemonSet already covers this) — but they'll join the same 24h-token lifecycle. Acceptable that burst workers share the home-worker token/payload flow, or do you want them to go back to CAPI's native kubeadm join (separate path)?
Answer those two and the plan is final and ready to execute.

1. **Use a dedicated tailnet node for the spoke API front**, not the Hub. That keeps Hub responsibilities separate and avoids coupling spoke control-plane availability to the Hub. For dev, direct single-CP endpoint is fine; for stg/prod, use a small HAProxy TCP frontend enrolled in Tailscale with a stable MagicDNS name.

2. **Keep burst Hetzner workers on native CAPI/kubeadm join.** Don't reuse the home-worker token flow. CAPI already owns their lifecycle, so let `KubeadmConfigTemplate` handle bootstrap, while the shared Tailscale DaemonSet adds network participation and the `workload-location=hetzner` label.

So the final split is:

```text
Home workers
  → custom unmanaged kubeadm join/token lifecycle

Hetzner burst workers
  → normal CAPI/KubeadmConfigTemplate lifecycle

Spoke API
  → dedicated Tailscale HAProxy frontend (stg/prod)
```

That keeps the two worker lifecycles clean and minimizes custom control-plane logic.


All decisions locked. Here is the final, execution-ready plan.
Final Plan: hybrid provider — Hetzner CP + home WSL2 workers on a Tailnet-only spoke
1. Locked architecture
                Mac (client) ── Tailscale
                         │
                 ┌───────▼───────┐
                 │ Hetzner Hub   │   CAPI / Crossplane / ArgoCD
                 └───────┬───────┘
                         │ Tailscale (spoke API is tailnet-only, no public LB)
       ┌─────────────────┼───────────────────────┐
       │                 │                        │
 Taigw API front    HaProxy TCP front      HaProxy stg/prod
 (stg/prod only)          │                        │
                         │                        │
        ┌────────────────┼────────────────────────┼──────────────────────┐
        │                │                        │                      │
  Hetzner CP #1     Hetzner CP x3           WSL2 worker 1          WSL2 worker 2
  (dev: 1, stg: 3)  (stg/prod)             workload-location=home  workload-location=home
Worker lifecycle split (final):
- Home workers → unmanaged, custom kubeadm join + hub-operator-minted token lifecycle (2 now, →3).
- Hetzner burst workers → native CAPI KubeadmConfigTemplate bootstrap, replicas 0 by default; shared Tailscale DaemonSet + workload-location=hetzner kubelet label.
- Spoke API → dev: direct single-CP tailnet endpoint; stg/prod: dedicated Tailscale-enrolled HAProxy TCP front with stable MagicDNS name.
Scheduling contract: default workloads to nodeSelector: workload-location: home; burst cloud workers require explicit workload-location: hetzner.
2. Workstream A — Shared provider base (zero behavior change)
- manifests/providers/_shared/hetzner-spoke-pool-v1.yaml — move of manifests/providers/hetzner/k8s/spokepool-hetzner-v1.yaml, with two new variables:
- controlPlaneLoadBalancer.enabled (default true — hetzner unchanged; hybrid sets false)
- controlPlaneEndpointHost (default "" — hybrid injects tailnet MagicDNS name)
- manifests/providers/_shared/spoke-addons/{ccm,csi}-addon-template.yaml — moves of hetzner's copies.
- Hetzner worker KubeadmConfigTemplate gains --node-labels=workload-location=hetzner (burst workers self-label; the base stays CAPI-native).
- manifests/providers/hetzner/kustomization.yaml updated to point at _shared/ (kustomize resources: forbids ../, so shared base is the reuse vehicle per ADR-036 §7).
3. Workstream B — manifests/providers/hybrid/ (new cell)
- kustomization.yaml: shared ClusterClass + shared addons + k8s/hetzner-credentials-es.yaml + k8s/spokepool-hybrid-composition.yaml + k8s/home-worker-integration.yaml.
- k8s/spokepool-hybrid-composition.yaml — clone of hetzner composition (manifests/providers/hetzner/k8s/spokepool-hetzner-composition.yaml), label provider: hybrid, with:
- capi-cluster patched: controlPlaneLoadBalancer.enabled=false, controlPlaneEndpoint.host from controlPlaneEndpointHost variable (tailnet MagicDNS), control-plane replicas patched from claim annotation (1 dev / 3 stg).
- workers.machineDeployments[0].replicas patched from spec.nodePool.count (claims set 0).
- tailscale-node DaemonSet added to the spoke ClusterResourceSet so all CAPI-managed nodes (CP + future burst workers) join the tailnet with per-node identities (node-name-derived tags).
- home-worker-join-config ConfigMap step carrying {hostname, tailnetHost} list from claim annotations.
- k8s/home-worker-integration.yaml: RBAC for hub-operator (read platform-capi secrets, mint tokens), tailscale pre-shared-key Secret for the DaemonSet.
- Matrix wiring needs no AppSet changes: 03-platform-services-appset.yaml:97 already resolves manifests/providers/{{ .Values.provider }}, and :370 already resolves manifests/spoke/spoke-pools/{{ .Values.environmentSlug }}/{{ .Values.provider }}.
4. Workstream C — SpokePool claims
- manifests/spoke/spoke-pools/dev/hybrid/hybrid-dev.yaml — compositionSelector.matchLabels.provider: hybrid, region: fsn1, nodePool.count: 0, annotations: control-plane-replicas: "1", home-workers: [{hostname: wsl2-node-1,…},{hostname: wsl2-node-2,…}], home-worker-enabled: "true".
- manifests/spoke/spoke-pools/stg/hybrid/hybrid-stg.yaml — same with control-plane-replicas: "3" and a spoke-api-front annotation (ha-proxy.tailnet.ts.net).
- Optional: namesapce.yaml in prod not touched — hybrid is dev/stg only; prod stays pure hetzner (escape hatch preserved).
5. Workstream D — Bootstrap CLI + driver
- cmd/hub/bootstrap.go: add hybrid to validProviders (:81) and switch (:150); new flags --home-worker-enabled, --home-worker-ttl (default 24h), --tailnet-name; reuse hetzner validation (region/OS/token) for hybrid.
- internal/hub-cli/bootstrap/driver_hybrid.go (new): HybridDriver implementing CloudDriver, composing HetznerDriver — reuses PreflightValidators, ProvisionDayZero, CAPIProviders, OnCAPIInit, ClusterClassPaths, PopulateClusterConfig; Name() returns hybrid (helm passthrough; orchestrator.go:579 needs no mapping since only docker→local is special-cased). Adds writing the home-worker join-config Secret into platform-capi during Day-0.
- internal/hub-cli/cluster/config.go: extend cluster.Config with HomeWorker struct{Enabled bool; TTL string} (+ any SpokeAPIFront string) so the provisioner can surface these to hub-operator.
6. Workstream E — Token minting & rotation (hub-operator)
operators/hub-operator/internal/controller/spokepool_controller.go (already reconciles SpokePools, :39) — add reconcileHomeWorkerJoin(ctx, spokePool), gated on compositionSelector.matchLabels.provider == hybrid:
1. Wait for CAPI Cluster Ready (status.phase == Provisioned).
2. Read spoke kubeconfig from <spoke>-kubeconfig secret in platform-capi (existing observer/creator pattern, spokepool-capd-single-composition.yaml:439).
3. Mint kubeadm bootstrap-token-<id> Secret (type: bootstrap.kubernetes.io/token) in spoke kube-system via pure API — kube-controller-manager signs discovery; no node exec.
4. Compute discovery-token-ca-cert-hash from CA cert in the kubeconfig.
5. Emit join payloads (one per home node) → K8s Secret {spoke}-home-worker-join + push to Infisical via existing InfisicalClient (ADR-031 pattern).
6. Rotate token before expiration (TTL 24h); idempotent RequeueAfter loop, structured logs, r.Get-then-r.Update.
- RBAC markers + config/rbac/role.yaml regenerated via make manifests generate (per hub-operator AGENTS.md).
- Unit test in suite_test.go following existing Ginkgo/Gomega patterns.
7. Workstream F — WSL2 side scripts
- scripts/hybrid/setup-wsl2-node.sh <node> — idempotent: tailscale up, install containerd 1.7.26 / runc 1.2.5 / kubelet-kubeadm-kubectl 1.31.6 (match spokepool-hetzner-v1.yaml:162-201), systemd enable, swapoff, CNI sysctls.
- scripts/hybrid/home-worker-join.sh <node-index> — idempotent: ensure Tailscale up → if kubectl get node <hostname> already exists, verify labels + Ready, exit → fetch {spoke}-home-worker-join → kubeadm join <tailnet host>:443 --token … --discovery-token-ca-cert-hash sha256:… → label workload-location=home, node-role.kubernetes.io/home=, optional topology.kubernetes.io/zone=home → verify Ready.
8. Workstream G — Spoke API front (stg/prod)
- manifests/providers/hybrid/k8s/spoke-api-front.yaml (stg/prod): config for a dedicated Tailscale-enrolled HAProxy TCP front (MagicDNS ha-proxy.tailnet.ts.net, forwards :443 → CP node tailnet addresses :6443), provisioned outside the Hub design per your correction. Dev uses the single CP node's tailnet address directly — no front node. (Front infra provisioning — VM or container — is a small standalone item; I'll flag it as the one piece with a bootstrap-time manual step unless you want it as a CAPI provisioned VM too.)
9. Workstream H — Docs
- docs/adr/046-hybrid-provider-home-worker.md — matrix row, tailnet-only networking (API + data path), the two worker lifecycles, token lifecycle, home-default scheduling, burst semantics, and the "ClusterClass stays CAPI-managed; home workers unmanaged" boundary.
- Optional: manifests/argocd/environment-manager/values.yaml:9 comment noting hybrid; skill file provider list.
10. Sequencing & validation
1. A shared base (pure refactor — verify kubectl kustomize + hetzner bootstrap still byte-identical).
2. B + C hybrid cell + claims (render helm template environment-manager --set provider=hybrid,… and inspect AppSet paths).
3. D CLI/driver (hub bootstrap --provider hybrid --dry-run).
4. E hub-operator token minting (unit tests + a throwaway hybrid dev pool on the existing dev Hub).
5. F WSL2 scripts (join node 1, then node 2; simulate reboot/rejoin).
6. G stg/prod HAProxy front.
7. H ADR.
- Build/lint: go build ./..., make test in operators/hub-operator/, kubectl kustomize on all touched provider dirs.
This plan has no remaining open questions — the two lifecycle paths, the API front, the scheduling contract, and the matrix cell are all pinned. Ready to execute on your go-ahead.

My choices:

1. **Box B SSH target:** leave a placeholder in `home-lab.env.example`; put the real `user@ip` only in the gitignored `.env`.

2. **Box B role:** both boxes join the **same dev hybrid spoke pool**. Start with 2 workers and scale toward 4.

3. **Tailnet scope:** **one shared Tailscale tailnet** across Mac, both home boxes, Hetzner control-plane/burst nodes, and the dedicated spoke API HAProxy front.

4. **CI:** **remove `e2e-capd`** and leave a documented TBD for a future hybrid E2E harness. Don't add paid Hetzner provisioning to every PR.

That gives you one clean hybrid environment with home capacity as the default and Hetzner as control-plane/burst infrastructure.

All four decisions locked. The plan is now complete and executable. Here's the final version:
Final Plan: hybrid provider + CAPD removal
Registry defaults — home-lab.env.example ships box B as a placeholder; real user@ip only in gitignored home-lab.env. Both boxes join one dev hybrid pool (2→4). One shared tailnet for Mac, both boxes, Hetzner CP/burst, and the HAProxy front.
WS0 — Remove CAPD/local
- Delete scripts/local-dev/, manifests/providers/local/, manifests/spoke/spoke-pools/dev/local/, pre-req-local.sh.
- Remove provider_local.go, embedded classes/capd-spoke-pool-v1.yaml, --provider docker in cmd/hub/bootstrap.go, docker→local branch in orchestrator.go:579-580, docker provider option in capi/setup.go:44 (keep kind + DockerValidator for pivot).
- Strip local branch + windows-engine hook from hub-bootstrap.sh.
- Scrub local/capd wording in spokepool-v1.yaml XRD.
- Remove e2e-capd job from e2e-preview.yaml; add TBD note for future hybrid E2E (no paid Hetzner in every PR).
- Verify: go build ./...; kubectl kustomize manifests/providers/hetzner byte-identical.
WS1 — Shared base (manifests/providers/_shared/)
Move ClusterClass + CCM/CSI templates from hetzner. Add variables controlPlaneLoadBalancer.enabled (default true) and controlPlaneEndpointHost; kubelet workload-location label. Hetzner kustomization repoints.
WS2 — Hybrid provider cell
- manifests/providers/hybrid/: composition (Hetzner CP + home workers via claim annotations + burst MD replicas:0), clusterclass (LB disabled, tailnet-only endpoint), home-worker-integration.yaml (Tailscale DaemonSet, RBAC, PSK).
- Claims: dev/hybrid (CP=1) and stg/hybrid (CP=3).
WS3 — Home-lab node registry (scripts/hybrid/)
- home-lab.env.example + gitignored home-lab.env with HOME_WORKER_NODES = <hostname>|<ssh-target>|<wsl-distro>|<tailnet-host>|<box-tag>; line 1 = box A (Dell@10.57.194.147), line 2 = box B placeholder.
- setup-wsl2-node.sh, home-worker-join.sh, render-home-workers.sh.
WS4 — CLI + controller
--provider hybrid; driver_hybrid.go; cluster.Config.HomeWorker; reconcileHomeWorkerJoin in spokepool_controller.go (bootstrap-token mint, 24h TTL, rotation).
WS5 — Spoke API front + ADR-046
Tailscale HAProxy TCP front; docs/adr/046-hybrid-provider-home-worker.md; supersede ADR-036 §3 + ADR-044.

This is a brilliantly structured, exhaustive plan. You have perfectly captured the architectural shift and the transition sequence. 

### Answer to your residual question:
**Remove `docker system prune -f --volumes` entirely.** 
The original justification for that command was that CAPD (Docker-in-Docker) leaves behind orphaned docker volumes and massive amounts of dangling images on the host during failures. Since the Mac is now returning to being a well-behaved, pure GitOps client (and the Hub is moving to Hetzner), running a global, destructive `docker system prune --volumes` is a violation of the developer's local environment. `kind delete cluster` is more than sufficient for cleaning up the Hub.

---

Let's begin executing **WS0: Remove CAPD/local**. I will provide the exact commands and code changes for the surface area available in my context. 

### 1. Delete Artifacts (Run from Repo Root)

Run the following block to purge the local/docker provider artifacts from the filesystem:

```bash
# 1. Manifests & Scripts
rm -rf manifests/providers/local/
rm -rf scripts/local-dev/
rm -rf manifests/spoke/spoke-pools/dev/local/
rm -f scripts/pre-req-local.sh

# 2. Go files & Embedded Assets
rm -f internal/hub-cli/bootstrap/provider_local.go
rm -f internal/assets/manifests/classes/capd-spoke-pool-v1.yaml
rm -f internal/assets/manifests/core/capi-operator/providers/infrastructure-provider-docker.yaml
```

### 2. Modify Scripts

#### Update `scripts/hub-bootstrap.sh`
Remove the Docker/CAPD specific branches, cleanup logic, and the Windows hook.

*   **Lines ~474-477 (Teardown Cleanup):** Remove the destructive docker prune.
    ```diff
    -        docker system prune -f --volumes 2>/dev/null || true
             rm -f "$BOOTSTRAP_STATE_FILE"
    ```
*   **Lines ~533-560 (Bootstrap Execution):** Remove the `if [[ "$PROVIDER" == "local" ]]` branch completely and make it strictly Hetzner (we will add `hybrid` in WS4).
    ```bash
    # REPLACING THE ENTIRE IF/ELSE BLOCK FOR PROVIDER WITH THIS:
    
    export HCLOUD_TOKEN=$(cat "$ZERO_OPS_DIR/k8-secrets/hetzner/token")
    local env_flag="${ENVIRONMENT:---environment=prod}"
    local topo_flag=""
    if [[ -n "${TOPOLOGY:-}" ]]; then
        topo_flag="--topology=$TOPOLOGY"
    fi
    
    log "Running: $HUB_BINARY bootstrap --name=${CLUSTER_NAME} --region=fsn1 $env_flag --debug"
    (cd "$ZERO_OPS_DIR" && "$HUB_BINARY" bootstrap \
        --name="${CLUSTER_NAME}" \
        --region=fsn1 \
        $env_flag \
        $topo_flag \
        --debug 2>&1 | tee "$LOG_DIR/bootstrap-hub.log")
    ```
*   **Lines ~565-578 (Windows Ingestion Hook):** Delete the entire `[ZERO-OPS ASYMMETRIC SPLIT HOOK]` block.
*   **Lines ~1162-1166 (SpokePool Defaulting):** Remove the local default.
    ```diff
    -    if [[ "$PROVIDER" == "local" ]]; then
    -        SPOKEPOOL_NAME="local-dev"
    -        log "Step 10: Using local SpokePool: $SPOKEPOOL_NAME"
    -    elif [[ -z "$SPOKEPOOL_NAME" ]]; then
    +    if [[ -z "$SPOKEPOOL_NAME" ]]; then
             error_exit "SPOKEPOOL_NAME must be set via --spoke flag or SPOKEPOOL_NAME env var for provider '$PROVIDER'"
         fi
    ```

#### Update `scripts/post-bootstrap-validate.sh`
Remove the local/docker auto-detection logic.

*   **Lines 14-26:**
    ```bash
    # REPLACE AUTO-DETECT LOGIC WITH:
    if [[ -z "${KUBECONFIG:-}" ]]; then
        KUBECONFIG="$ZERO_OPS_DIR/k8-secrets/kubeconfig/hub.kubeconfig"
    fi
    CAPI_INFRA_PROVIDER="hetzner"
    SPOKEPOOL_NAME="${SPOKEPOOL_NAME:-spoke-pool-eu-prod-01}"
    ```

### 3. Modify Manifests

#### Update `manifests/hub-core-services/crossplane/tenant-platform/xrds/spokepool-v1.yaml`
```yaml
# Update Lines 35 and 41
                provider:
                  type: string
                  minLength: 1
                  description: >
                    Infrastructure provider for this spoke cell (e.g., hetzner, hybrid, aws).
                    ...
                region:
                  type: string
                  description: "Provider region (e.g., fsn1, nbg1, hel1) or 'hybrid' for home-lab"
```

#### Update `manifests/argocd/environment-manager/values.yaml`
```yaml
# Update Line 9
# environmentSlug: "dev"        # dev | stg | prod | ephemeral
# provider: "hetzner"           # hetzner | hybrid | aws
```

### 4. Modify Go Source Files & CI (Action Required)
Since the raw Go source files and the `.github/workflows/e2e-preview.yaml` aren't fully present in my current context window, please apply the following cleanups to them on your end:

1.  **`cmd/hub/bootstrap.go`**: 
    *   Remove `"docker"` from `validProviders`.
    *   Delete the `--docker-socket` flag logic and variable entirely.
    *   Remove the `case "docker":` block in the switch statement.
2.  **`internal/hub-cli/bootstrap/provider.go`** & **`provider_cloud.go`**:
    *   Remove the `IsLocal() bool` method from the interface and its implementations.
3.  **`internal/hub-cli/bootstrap/orchestrator.go`**:
    *   Remove `if !o.Provider.IsLocal()` checks (just execute the teardown blocks directly).
    *   Remove the docker -> local string mapping logic at the bottom where Helm values are passed.
4.  **`internal/hub-cli/versions/versions.go`**:
    *   Delete `DockerInfraProviderVersion`.
5.  **`.github/workflows/e2e-preview.yaml`**:
    *   Delete the `e2e-capd` job completely.
    *   Leave a comment `TODO: Implement hybrid provider E2E harness once Tailscale matrix is established`.

---

Once you have executed these deletions and modifications, run:
```bash
go build ./...
helm template manifests/argocd/environment-manager --set provider=hetzner
```

Let me know if the build passes cleanly. Once confirmed, we will move immediately into **WS1 (Shared base)** and **WS2 (Hybrid provider cell)**!


Locked — provider-registry.yaml becomes exactly hetzner: "spokepool-hetzner" + hybrid: "spokepool-hybrid". Future providers (CIVO, etc.) get added to the registry when their cells land; no placeholders.
Plan is now fully settled. Summary of the locked registry change (in operators/hub-operator/config/manager/provider-registry.yaml):
data:
  hetzner: "spokepool-hetzner"
  hybrid: "spokepool-hybrid"
Removed: capd (dead after WS0) and aws (dangling, no composition). Sole-dev, permanent-clean state.
Everything else in the WS1–WS5 plan stands as presented (shared base refactor → hybrid cell → home-lab registry → CLI/token controller → spoke API front + ADR-046, plus the hetzner kustomize remote-URL permanent fix in WS1).
Ready to exit plan mode and execute WS1 → WS5.

Here's what's been completed so far.
✅ WS0 — CAPD/local removal (COMPLETE, verified)
Deleted (29 artifacts):
- manifests/providers/local/** (17 files), scripts/local-dev/** (10), manifests/spoke/spoke-pools/dev/local/** (2), scripts/pre-req-local.sh
- provider_local.go, embedded classes/capd-spoke-pool-v1.yaml, core/capi-operator/providers/infrastructure-provider-docker.yaml
Go changes:
- Dropped IsLocal() from Provider interface + CloudProvider
- Removed docker→local helm map in orchestrator.go; cleanup now !KeepBootstrap
- Removed DockerInfraProviderVersion from versions.go
- CLI: removed docker provider case, --docker-socket, docker envslug default; added hybrid case + HybridDriver (minimal wrapper, WS4 enriches)
- Verified: go build ./cmd/hub ./internal/hub-cli/... + go vet clean
Scripts/manifests/CI:
- hub-bootstrap.sh: removed docker prune, local branch, Windows asymmetric-split hook; defaults → provider=hetzner, CLUSTER_NAME=hub
- post-bootstrap-validate.sh: always hetzner, spoke-pool-eu-prod-01 default
- XRD + env-manager values scrubbed; removed provider=local ingress-nginx hostNetwork block
- e2e-preview.yaml: removed e2e-capd job, added TBD note
🔄 WS1 — Shared base (IN PROGRESS)
Done:
- Moved ClusterClass → _shared/spokepool-clusterclass-v1.yaml, renamed hetzner-spoke-pool-v1 → spokepool-v1
- Moved CCM/CSI addon templates → _shared/spoke-addons/
- Created _shared/kustomization.yaml
- Added ClusterClass controlPlaneEndpoint patch + controlPlaneLoadBalancerEnabled (default true) + controlPlaneEndpointHost (default "") variables
- Worker KubeadmConfigTemplate carries node-labels: workload-location=hetzner (pre-existing)
- Updated hetzner composition class: ref → spokepool-v1
- Rewrote hetzner kustomization.yaml → references ../_shared, dropped the broken remote crossplane-contrib URL (permanent fix)
- Key discovery: ../ in kustomize resources actually works (earlier assumption was wrong)
Verified: hetzner kustomize renders (3553 lines, EXIT 0); ClusterClass + both addons + both new variables present; YAML valid.
Not yet done in WS1: enrich driver_hybrid.go (ClusterClassPaths → hybrid, LoadBalancer=false capability), update driver_hetzner.go:178 CCM path if it reads from manifests/providers/hetzner/spoke-addons/.
⏭️ Remaining
- WS1 finish (driver paths/CLI)
- WS2 hybrid cell: composition, claims, home-worker-integration.yaml, provider-registry.yaml → hetzner+hybrid only (locked: remove capd + aws, add hybrid: spokepool-hybrid)
- WS3 home-lab registry scripts + env example + .gitignore
- WS4 CLI flags + cluster.Config.HomeWorker + hub-operator reconcileHomeWorkerJoin
- WS5 spoke-api-front + ADR-046



----------------------

Choose **(b)** — derive the endpoint automatically from the claim name + tailnet name.

That is the more robust design:

```text
claim:       spoke-pool-hybrid-dev-01
tailnet:     zero-ops.ts.net

                ↓

endpoint:
spoke-pool-hybrid-dev-01-cp.zero-ops.ts.net
```

Why I prefer it:

* No duplicated configuration that can drift.
* Every hybrid spoke gets a deterministic endpoint.
* The DaemonSet and CAPI endpoint use the same naming convention.
* Adding another dev/stg/prod hybrid claim doesn't require manually inventing the hostname.

I'd make the naming function explicit and centralized, e.g.:

```text
<claim-name>-cp.<tailnet-name>
```

Then the implementation should ensure the **Tailscale node hostname/tag generated for the control-plane node exactly matches that convention**.

The three pieces should therefore move together:

```text
Hybrid Composition
 ├── derive controlPlaneEndpoint.host
 ├── create tailscale-node DaemonSet
 └── create home-worker-join-config
```

One caution: **don't assume a Tailscale tag automatically produces the exact MagicDNS hostname you want**. Make the node's advertised hostname explicit in the Tailscale configuration and verify it matches the derived endpoint before considering the spoke ready.

---------------

Yes. Proceed on that basis.

Use:

```text
<claim-name>-cp.<tailnet-name>
```

as the **single source of truth** for the spoke API endpoint. Remove the now-redundant `control-plane-endpoint-host` annotation from the dev claim.

One implementation detail I'd insist on: **don't assume the DaemonSet can safely assign the same `--hostname=<claim>-cp` to every control-plane node**. If you have 3 CP replicas, that would create duplicate Tailscale identities/hostnames. The endpoint needs a stable frontend for 3-CP HA, while individual CP nodes need unique Tailscale hostnames.

So:

* **Dev, 1 CP:** `<claim>-cp.<tailnet>` can point directly at that CP.
* **STG/prod, 3 CP:** `<claim>-cp.<tailnet>` should resolve to the dedicated Tailscale HAProxy frontend, with CP nodes having unique names such as `<claim>-cp-1`, `<claim>-cp-2`, `<claim>-cp-3`.

Everything else in the plan looks consistent.

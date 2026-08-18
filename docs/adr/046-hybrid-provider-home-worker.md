# ADR-046: Hybrid Provider Cell (Hetzner Control Plane + Home-Lab Workers)

**Date:** 2026-08-12
**Status:** Accepted

## Context

The platform historically used CAPD (Cluster API Provider Docker) for local
development — a Windows-hosted Docker engine acting as the control plane with a
macOS node as worker. This model was architecturally flawed (the Mac was the
worker by design, the Windows box ran the ingestion engine) and never worked.
Production runs entirely on Hetzner.

We need a local development and staging environment that:
- Reuses the tested Hetzner CAPI control-plane path as-is.
- Provides real, useful worker capacity from home-lab hardware (two WSL2
  Windows boxes, ~16GB RAM each, scalable to more).
- Costs nothing to keep running (no idle Hetzner worker nodes).
- Reaches home-lab workers over Tailscale while keeping the control plane on
  Hetzner's public network (CP advertised via a CAPH-managed Hetzner Load
  Balancer, exactly like the Hub and the pure-hetzner spokes).

The CAPD/local provider is removed entirely; home-lab is the new local/staging
ground.

## Decision

Introduce a new **hybrid** provider cell in the provider matrix
(ADR-036/037/038). It is a platform/provider cell — a directory of GitOps
manifests and a Hub CLI driver — NOT a new CAPI infrastructure provider.
Hetzner CAPI remains the infrastructure provider.

### Topology

| Layer | Placement |
|-------|-----------|
| Hub (management) cluster | Hetzner |
| Spoke control plane | Hetzner (1 replica dev, 3 stg/prod) |
| Spoke burst worker pool | Hetzner CAPI `MachineDeployment` at `replicas: 0` (escape hatch) |
| Spoke default workers | Home-lab WSL2 nodes, unmanaged kubeadm join over Tailscale |
| Spoke API access | Public Hetzner Load Balancer (CAPH-managed, `controlPlaneLoadBalancer.enabled=true`) |
| Tailscale | Home-lab WSL2 workers only; neither the Hub nor the spoke control plane runs Tailscale |

### Provider Cell Layout

- `manifests/providers/_shared/` — ClusterClass (`spokepool-v1`), CCM/CSI addon
  templates. Shared by hetzner and hybrid cells (kustomize `../` works within
  the repo).
- `manifests/providers/hybrid/` — hybrid Composition, home-worker integration,
  credentials.
- `manifests/providers/hetzner/` — unchanged hetzner composition + credentials,
  referencing `../_shared`.

### ClusterClass Variables

The shared ClusterClass exposes two variables:
- `controlPlaneLoadBalancer.enabled` (default `true`) — both hetzner and hybrid
  keep the Hetzner LB; the LB IP becomes the spoke API endpoint.
- `controlPlaneEndpointHost` (default `""`) — left empty for both providers;
  CAPH auto-fills `controlPlaneEndpoint.host` from the LB's IPv4
  (`ControlPlaneEndpointSet` condition).

Burst workers carry kubelet label `workload-location=hetzner`; home workers are
labeled `workload-location=home` by the join script.

### Home-Worker Lifecycle (Unmanaged)

Home workers are convenience nodes, not CAPI Machines:

1. hub-operator `reconcileHomeWorkerJoin` (WS4) gates on
   `spec.provider == hybrid` + `home-worker-enabled: "true"`.
2. It reads the CAPI-generated spoke kubeconfig (`<spoke>-kubeconfig`,
   `platform-capi`), connects to the spoke, and mints a pure-API kubeadm
   bootstrap token Secret (`bootstrap.kubernetes.io/token`) in the spoke's
   `kube-system`.
3. It writes the join payload Secret (`<spoke>-home-worker-join`,
   `platform-capi`) with per-node tokens, the discovery CA hash, and the
   control-plane endpoint.
4. `scripts/hybrid/home-worker-join.sh <idx>` fetches the payload and runs an
   idempotent `kubeadm join` from the WSL2 node.
5. Tokens rotate before expiry (rotation window = TTL/2); expired tokens are
   pruned.

Home workers never appear in the ClusterClass topology.

### Spoke API Endpoint

- CAPH creates a Hetzner Load Balancer per spoke (`controlPlaneLoadBalancer`
  `port: 6443`, `type: lb11`) and publishes the kube-apiserver on the LB IPv4,
  TCP 443 (LB `ListenPort` = `controlPlaneEndpoint.port`, forwarding to CP
  nodes on 6443). The endpoint is derived automatically; no tailnet name is
  involved.
- The segment is identical to the pure-hetzner provider; hybrid adds nothing
  for the API path. The endpoint is stable across control-plane node rotation
  because the LB IP does not change.
- Home workers reach the spoke API through this same public endpoint (the
  kubeconfig server used by the join flow); they do not depend on the CP being
  on the tailnet.

### Scheduling Contract

- Default workloads: `nodeSelector: workload-location: home`.
- Burst workloads: explicit `nodeSelector: workload-location: hetzner`.

### Cilium CgroupManager — WSL2 Cgroup Namespace Isolation

WSL2 worker nodes run containerd with OCI cgroup namespaces enabled
(`/proc/1/cgroup = 0::/` inside every container). The stock Cilium DaemonSet
initialises cgroup2 visibility via `mount-cgroup`, which uses `nsenter
--cgroup --mount` into the host mount namespace. On WSL2 the mount lands in
the host namespace but **does not propagate** into the agent container's
private mount namespace. Cilium's `CheckOrMountCgrpFS` then detects
`/run/cilium/cgroupv2` as a plain directory and stacks a fresh empty `cgroup2`
root on top, hiding the host `kubepods*.slice`. This disables `CgroupManager`,
which disables transparent-DNS-proxy, causing `toEndpoints kube-dns` identity
resolution to fall back to `world` and the `strict-egress-contract` CNP to
deny DNS.

**Fix**: the hybrid provider uses a separate `cilium-addon-hybrid` Secret
(registered in `manifests/providers/hybrid/k8s/cilium-addon-hybrid.yaml`,
referenced only from `spokepool-hybrid-composition.yaml`) instead of the
shared `cilium-addon-template`. It adds:

1. **Host-level shared cgroup2/BPF mounts** (the prerequisite). Cilium's
   socket-based kube-proxy-replacement needs a real cgroup2 mount at
   `/run/cilium/cgroupv2` marked **shared** (`mount -t cgroup2 none
   /run/cilium/cgroupv2 && mount --make-shared`) so the agent container's
   mount propagations can see it. On WSL2 the stock `mount-cgroup` nsenter
   mount never persists. Established by `scripts/hybrid/prepare-wsl2-cgroup.sh`
   and a `cilium-host-prep.service` systemd unit at boot.
2. **`fix-cgroup-mount` init container** (runs **after** `clean-cilium-state`,
   immediately before the agent) — privileged, executes inside the pod's own
   mount namespace, bind-mounts `/sys/fs/cgroup` (host real cgroup2 tree,
   exposed via a `host-cgroup` hostPath volume) onto `/run/cilium/cgroupv2`
   with Bidirectional propagation. Ordering is critical: `clean-cilium-state`
   runs `cilium-dbg cleanup -f --all-state`, which unmounts the host cgroup2
   mount; `fix-cgroup-mount` must therefore re-establish it last, right before
   the agent (whose `cilium-cgroup` volumeMount uses HostToContainer
   propagation). Enforces three sequential invariants and **fails closed**
   (exit 1) if any gate breaks:
   - Gate 1: bind-mount succeeds.
   - Gate 2: `/run/cilium/cgroupv2` is a real cgroup2 superblock (`findmnt
     -t cgroup2`).
   - Gate 3: at least one `kubepods*` directory exists at depth 1 (glob
     tolerates `kubepods/`, `kubepods.slice/`, etc.).
3. **`mount-cgroup` replaced with a no-op** echo command. The nsenter path
   is silently broken on WSL2; the no-op makes this explicit.
4. **`host-cgroup` hostPath volume** — `path: /sys/fs/cgroup, type: Directory`.
5. **Agent `cilium-cgroup` volumeMount** (`/run/cilium/cgroupv2`,
   `mountPropagation: HostToContainer`) — the agent must mount the cgroup
   volume itself; init containers run in their own mount namespace and cannot
   pass mounts to the agent otherwise.

The shared `cilium-addon-template` used by Hetzner spokes is left unchanged.

**Pre-rollout requirement**: before every `kubectl rollout restart daemonset/cilium`
on the hybrid spoke, run `scripts/hybrid/fix-cilium-pid.sh` to clear any
stale `/var/run/cilium/cilium.pid` (may contain PID 1) on WSL2 nodes, which
would otherwise block the `clean-cilium-state` init container.

**Control-plane durability**: the Hetzner CP node also needs the host shared
cgroup2 mount (the hybrid manifest no-ops the stock `mount-cgroup` init, so
the CP host must establish it itself). Codified in the shared ClusterClass
(`_shared/spokepool-clusterclass-v1.yaml`): a `cilium-host-prep.service`
systemd unit is written via `files[]` and enabled/started in
`preKubeadmCommands`, re-creating the shared cgroup2/BPF mounts at every boot
(`/run` is tmpfs), with a fail-closed `findmnt` gate. Idempotent and harmless
on pure-hetzner spokes (it pre-creates what stock `mount-cgroup` would
establish anyway). The WSL2 home workers use the same unit via
`setup-wsl2-node.sh`.

### Provider Registry

`operators/hub-operator/config/manager/provider-registry.yaml` maps exactly:
`hetzner → spokepool-hetzner`, `hybrid → spokepool-hybrid`. `capd` and `aws`
entries were removed (no compositions exist; sole-developer clean cut).

## Ownership

Defined by this ADR: hybrid cell manifests and the home-worker join flow, and
the provider-registry map. ClusterClass and addon templates are owned jointly
with the hetzner cell (shared base, ADR-036).

## Consequences

### Positive

- Zero idle Hetzner worker cost (burst pool at `replicas: 0`).
- Reuses the tested Hetzner CAPI control-plane path verbatim, including the
  CAPH-managed Load Balancer (same as the Hub and pure-hetzner spokes).
- Real hardware capacity (~32GB across two boxes, scalable to 3+ nodes).
- Stable spoke API endpoint (LB IP survives control-plane rotation); home
  workers reach the API over the public LB, Tailscale is used only for the
  worker nodes themselves.

### Negative

- Home workers are unmanaged — no CAPI-driven rollouts, upgrades, or
  remediation for them.
- kubeadm join depends on the spoke API being reachable (public LB) and on the
  hub-operator token being current (24h TTL, rotated by the controller).
- The spoke API is publicly exposed on a Hetzner Load Balancer (TCP 443) rather
  than kept tailnet-only; exposure is scoped by Hetzner firewall rules.

## Addenda (2026-08-17)

Operational corrections applied live on `spoke-pool-hybrid-dev-01` (Aug 2026)
and codified so re-provisioned spokes work out of the box:

1. **kube-proxy addon removal (Cilium full KPR).** kubeadm's stock
   `addon/kube-proxy` conflicts with Cilium full kube-proxy-replacement
   (`kube-proxy-replacement: "true"`): its iptables REJECT chains for
   endpoint-less services (incl. the Cilium Gateway LB VIP) break Gateway API
   TLS and in-cluster service networking. Codified as
   `spokepool-control-plane-v3` = v2 + `postKubeadmCommands` that delete the
   kube-proxy DaemonSet + ConfigMap post-init (this CAPI version has no
   `skipPhases` on `KubeadmControlPlaneTemplate`; `postKubeadmCommands` run
   after kubeadm's addon phase, so deletion is effective)
   (`manifests/providers/_shared/spokepool-clusterclass-v1.yaml`). The
   ClusterClass ref rotation to v3 is a deliberate, separate step: it
   propagates to existing clusters and rolls the control-plane machine.
2. **LB exclude-label removal.** kubeadm sets
   `node.kubernetes.io/exclude-from-external-load-balancers` on control-plane
   nodes by default; the Hetzner CCM honors it and registers ZERO backends for
   Cilium Gateway-API LoadBalancer services, so the external VIP accepts TCP
   but forwards nothing and TLS handshakes fail externally (they work
   in-cluster). Codified in v3 `postKubeadmCommands`:
   `kubectl ... label nodes --all node.kubernetes.io/exclude-from-external-load-balancers- || true`.
3. **clean-cilium-state=false.** Must stay `false` in steady state: with
   `true`, an agent restart wipes all endpoint/BPF state while existing pods
   keep their netns/veth but lose their Cilium endpoint (kubelet does not
   re-run CNI for existing sandboxes) → probes fail → CrashLoopBackOff. The
   flag was a KPR-deadlock workaround while the conflicting kube-proxy addon
   ran; that conflict is gone (kube-proxy removal in v3).
4. **Firewall gap (not codified).** LB→node traffic for Cilium Gateway-API
   service nodePorts (e.g. 30657/31888) is NOT codified anywhere in this repo —
   no hcloud `Firewall` manifests, no `HetznerCluster.spec.firewall` /
   `HCloudMachine.spec.firewall` config (firewalls are only deleted by the
   hub-cli teardown). Hetzner-side rules for spoke load balancers are
   provisioned out-of-band; re-provisioning a spoke requires re-adding the
   LB→node port rules manually.
5. **spoke-api-front removed (stale pre-ADR artifact).** `spoke-api-front.yaml`
   was a Tailscale-enrolled HAProxy TCP front for the spoke control-plane API —
   a design that predates and contradicts this ADR: the Hub and spoke control
   planes do NOT run Tailscale (topology table above), and the spoke API path
   is the CAPH-managed Hetzner LB ("hybrid adds nothing for the API path"). It
   referenced nonexistent ADR sections (§WS2/§WS5) and its Deployment was
   permanently Pending (nodeSelector `workload-location: home` matches no hub
   node). Removed from `manifests/providers/hybrid/kustomization.yaml` and the
   stg claim comments corrected; home workers join via the spoke kubeconfig
   server (LB endpoint) per the join flow (hub-operator WS4), which also
   supports the `control-plane-endpoint-host` claim annotation when a
   tailnet-resolvable endpoint is ever needed.
6. **Hybrid Cilium VXLAN MTU (1200) & Tailscale Overlay Invariant.**
   Tailscale's WireGuard underlay operates at MTU 1280 (`tailscale0`). The stock
   Cilium VXLAN overlay MTU (1230) produces outer encapsulated UDP packets of
   ~1308 bytes, causing IP fragmentation on `tailscale0` and packet drops in the
   Cilium BPF datapath (`First logical datagram fragment not found`).
   **Codified**: `mtu: "1200"` in `manifests/providers/hybrid/cilium-values.yaml`
   and `manifests/providers/hybrid/k8s/cilium-addon-hybrid.yaml`.
   Additionally, Cilium cross-node overlay routing between Hetzner CP and WSL2
   workers requires the CP's routable Tailscale IP (`100.71.186.51`) to be
   recognized for VXLAN tunneling while preserving the Kubernetes Node `InternalIP`
   (`10.0.0.4`) for Hetzner Load Balancer and K8s API traffic. When Envoy (running
   on CP host) routes traffic to WSL2 frontend pods, the Linux kernel uses the CP's
   Cilium host router IP (`10.244.28.9`) as source; WSL2 remote workers look up
   `10.244.28.9` in BPF ipcache and route return SYN-ACK packets back to the CP's
   Tailscale tunnel endpoint.
   **Codified**: `cilium-node-ip-reconciler` DaemonSet in
   `manifests/providers/hybrid/k8s/cilium-addon-hybrid.yaml` automatically maintains
   the CP node's Tailscale IP in `CiliumNode.spec.addresses` and `CiliumEndpoint.status.networking.node`.
   Tested and verified up to 1MB payloads across both directions with 0 packet drops or fragmentation.
7. **Standalone `cilium-envoy` DaemonSet removed (embedded Envoy only).** The
   rendered addon had BOTH `external-envoy-proxy: "false"` (agent runs Envoy
   embedded, serving Gateway-API L7 on 127.0.0.1:10515) AND the standalone
   `cilium-envoy` DaemonSet (`--base-id 0`). Both bind the same
   `/var/run/cilium/envoy/sockets` abstract domain sockets; a stale host
   `cilium-envoy` process (orphaned after pod restarts) left
   `@envoy_domain_socket_parent_0` behind, and every new standalone Envoy
   instance crashed in ~35ms with `errno=98 (EADDRINUSE)`, taking Gateway
   traffic down. **Codified**: `envoy.enabled: false` in
   `manifests/providers/hybrid/cilium-values.yaml` and the rendered Secret
   `manifests/providers/hybrid/k8s/cilium-addon-hybrid.yaml` no longer contains
   the cilium-envoy ServiceAccount / ConfigMap / Service / DaemonSet. Gateway-API
   proxying is unaffected — the agent serves it in-process. Pure-hetzner spokes
   keep the standalone DS until re-rendered consistently.
8. **Gateway API hostNetwork mode + Hetzner LB (external provision).**
   NodePort+TPROXY (L7LB) silently drops genuinely-external SYNs (no SYN-ACK,
   no RST, no BPF trace) on ALL interfaces — a known Cilium class of bugs
   (#8971/PR#12434, #35012, #43819) — while loopback/self-originated tests
   pass (false positives). **Codified**: `gatewayAPI.hostNetwork.enabled: true`
   with `nodes.matchLabels: node-role.kubernetes.io/control-plane=` — the
   embedded Envoy binds `0.0.0.0:80/443` directly on the CP node; the gateway
   Service becomes ClusterIP so the Hetzner CCM no longer provisions an LB.
   Two prerequisites (both codified):
   - `envoy.securityContext.capabilities.keepCapNetBindService: true` +
     `NET_BIND_SERVICE` capability on the cilium-agent container, otherwise
     Envoy fails `cannot bind '0.0.0.0:80': Permission denied`.
   - Cilium's stock `CILIUM_PRE_mangle` rule
     `-m socket --transparent ... -j MARK --set-xmark 0x200` matches
     post-handshake packets destined to the transparent host-bound listener
     and reroutes them via table 2004 (local lo), breaking the TCP handshake
     for external traffic (loopback exempt via Cilium's `! -o lo` exclusion).
     Since Cilium wipes foreign rules on every chain re-sync, a guard
     DaemonSet (`cilium-hostnetwork-mangle-guard`, codified in the rendered
     addon) continuously re-asserts
     `iptables -t mangle -I CILIUM_PRE_mangle 1 -p tcp -m multiport --dports 80,443 -j RETURN`.
   The public-facing Hetzner LB (`waypoint-gateway-lb`, lb11/hel1, public
   `77.42.12.176`, private `10.0.0.5` on network 12543641) is an EXTERNAL
   resource — out of scope for in-cluster GitOps and unmanageable by the CCM
   in hostNetwork mode (ClusterIP Service, no NodePorts). **Codified**:
   `scripts/hybrid/ensure-waypoint-lb.sh` (idempotent hcloud CLI: services
   80/443 with PROXY protocol, HTTP health checks with relaxed timeouts, CP
   server target over the private network `use_private_ip=true` — the
   embedded Envoy binds IPv4-only, so public-IPv4 health probes must be
   avoided).    DNS `waypoint.nutgraf.in`/`api.waypoint.nutgraf.in` → LB public
   IPv4 (Hetzner Cloud DNS).
9. **Known limitation: Envoy stuck-drain on Gateway changes.** Modifying
   `Gateway` annotations or listeners triggers an Envoy hot-restart in
   Cilium. In hostNetwork mode this frequently results in a stuck drain
   state (parent shuts down after drain, child fails to bind, traffic drops
   to `000` on every port while the sockets remain bound by the draining
   parent — observed twice). **Workaround**: after applying Gateway changes,
   manually restart the Cilium agent on the control-plane node:
   `kubectl delete pod -n kube-system -l k8s-app=cilium --field-selector spec.nodeName=<cp-node>`.
   Deliberately NOT mitigated with a watchdog: Gateway topology is a Day-1
   operation; tracked as upstream technical debt (embedded-Envoy hot-restart
   in hostNetwork mode). **Superseded by addendum 10** (the same failure
   class reproduces on plain agent restarts, without any Gateway change).

10. **Confirmed restart-safety defect in the Cilium v1.17.18 hostNetwork
    embedded-Envoy lifecycle under overlapping agent replacement — decision:
    decouple Envoy from the agent (draft for sign-off, NOT yet applied).**

    **Observed failure.** A plain Cilium agent restart on the control-plane
    node — no Gateway configuration change, no CNP change, no certificate
    change — repeatedly produces an HTTPS availability loss on
    `waypoint.nutgraf.in`. `kubectl delete` of the agent pod triggers an
    immediate DaemonSet replacement that starts **before** the old
    container's grace-period termination releases the host abstract sockets
    (`@envoy_domain_socket_parent_0` / `@envoy_domain_socket_child_0`) and
    the `:80`/`:443` listeners. The new Envoy finds the previous hot-restart
    participant still present, engages the hot-restart drain handoff at
    startup, and the parent stays in drain until the 900s parent-shutdown
    boundary. Gateway HTTPS goes `000` during/after that transition. Severity
    is variable: one run stayed down until the agent pod was deleted; another
    recovered by itself after ~4 minutes.

    **Evidence (two controlled replays, 2026-08-17, all times UTC).**

    | Generation | Pod start | Envoy start | Parent-shutdown (+900s) | HTTPS |
    |---|---|---|---|---|
    | A (wedged) | 12:19:18Z | 12:19:27Z | 12:34:27Z (+900.2s) | `000` observed 12:48:05Z–12:53:45Z (probe logs), no self-recovery; restored ~12:59:31Z only after agent pod deletion |
    | B (self-healing) | 12:51:51Z | 12:51:52Z | 13:06:52Z (+900.0s) | `200` 12:59:31Z–13:13:45Z, `000` 13:14:01Z–13:17:44Z, self-recovered 13:18:56Z, no intervention |

    Both runs share the same conditions: clean socket ownership at start
    (single Envoy generation, 4× SO_REUSEPORT `:80` + 1× `:443`, no stale
    external process); LDS/SDS fully converged before the failure
    (`tenant-waypoint/cilium-gateway-waypoint-gateway/listener` added,
    TLS cert from the `cilium-secrets` SDS sync served, continuous `200`);
    no Gateway object mutation (Gateway 42h old, PROGRAMMED); agent log
    silent at the boundary except the `[shutting down parent after drain`
    line — no listener reprogramming. At the boundary the Envoy re-bound
    its `:443` listener set 1→4 internally (no agent LDS involved) and the
    process never exited; socket ownership stayed within the expected
    generation throughout. The overlap window between the new pod start
    (12:51:51Z) and the old container's termination (~12:52:30Z) explains
    why the handoff engages on every restart, and why severity varies with
    the race.

    **Root-cause classification.** Embedded Envoy + hot-restart handoff
    engaging during the overlapping agent replacement window, in Cilium
    v1.17.18 hostNetwork Gateway mode. We do **not** claim a deeper upstream
    Envoy bug: the mechanism is fully consistent with the observed data, but
    the upstream defect boundary is not established (no source-level
    evidence). Conclusion wording: **confirmed restart-safety defect in the
    Cilium 1.17.18 hostNetwork embedded-Envoy lifecycle under overlapping
    agent replacement.**

    **Decision.** Move to **decoupled Envoy**; do **not** add a watchdog.
    A watchdog that deletes agent pods on HTTPS `000` turns a deterministic
    lifecycle defect into an availability-control loop (delete → restart →
    re-wedge) and was rejected. Decoupling gives Envoy an independent
    lifecycle: agent restarts (including the overlap window) never restart
    Envoy, and Gateway `:80`/`:443` stays served. Mechanics: `envoy.enabled:
    true` in `manifests/providers/hybrid/cilium-values.yaml` renders the
    standalone `cilium-envoy` DaemonSet; the chart flips
    `external-envoy-proxy: "true"` atomically with `envoy.enabled=true`
    (templates/cilium-configmap.yaml:1508), so no dual-mode intermediate
    state exists (the addendum-7 EADDRINUSE collision class). The DS
    supports hostNetwork, `nodeSelector`, and
    `keepCapNetBindService`/`NET_BIND_SERVICE` (envoy.enabled DS +
    `gatewayAPI.hostNetwork` semantics preserved).

    **Migration / rollback.** Status: draft for sign-off — nothing applied
    yet; the addon is still the addendum-7 embedded configuration.
    Migration sequence:
    1. Pre-flight: verify current ingress stays serving (LB health check
       continuity gate); identify and record current Envoy socket holders.
    2. Render the addon with `envoy.enabled: true` (single rendered
       manifest, `external-envoy-proxy` flips in the same sync — no
       dual-mode window by construction).
    3. Stale-holder handling: the flipped agents restart and release the
       embedded Envoy's sockets **before** the DS pods bind; additionally,
       a fail-closed init container on the DS (via the chart's
       `envoy.initContainers` hook) verifies no foreign process holds
       `@envoy_domain_socket_parent_0`/`:80`/`:443` and exits non-zero with
       a clear message otherwise — the DS pod crash-loops loudly instead of
       silently wedging.
    4. Verify: `:80`/`:443` bound by the DS pod only (not the agent);
       LDS listener `tenant-waypoint/cilium-gateway-waypoint-gateway/
       listener` present; TLS cert served (SDS via `cilium-secrets`);
       LB backend healthy; external HTTPS `200`.
    5. Rollback boundary: git revert of the values flip + re-render
       (`envoy.enabled: false`); the DS pods are removed and the agent
       flip back happens **after** DS termination (the init-gate ordering
       below also applies in reverse), then the same verification gate.
       Rollback is exercised only as a deliberate operation, never as the
       recovery mechanism.

    **DS restart-overlap mitigation (concrete, enforced by the controller
    + the init gate, not by operational care).** The embedded-mode failure
    is fundamentally the overlap of two Envoy generations competing for
    host sockets. In decoupled mode this is prevented structurally:
    - Socket safety is guaranteed by the `wait-for-envoy-release` init
      container: a new DS Envoy never binds until the host sockets
      (`:80`/`:443`, `@envoy_domain_socket_parent_0`) are free AND the
      agent's xDS socket is up — regardless of how the new pod was created
      (rollout, replacement, out-of-band). 600s fail-closed deadline.
    - `envoy.updateStrategy.type: RollingUpdate` with
      `rollingUpdate.maxUnavailable: 1` (default is `2`). NOTE: the
      DaemonSet API rejects `maxUnavailable: 0` ("cannot be 0 when
      maxSurge is 0"), and surge (`maxSurge: 1`) would deadlock the
      release gate (the old pod holds the sockets until the new pod is
      ready — the init gate would wait forever). With `maxUnavailable: 1`
      the per-node update is delete-old-then-create-new (grace 1s), and
      the init gate then serializes socket ownership: no old/new Envoy
      generation can coexist on the host sockets at any rollout.
    - Agent restarts are decoupled entirely: the agent DaemonSet rollout no
      longer restarts Envoy, so the failure trigger (overlapping agent
      replacement) cannot engage the Envoy drain state machine.
    - The DS pod's own drain on kubelet termination is safe by construction:
      the replacement's init gate waits for the sockets the terminating pod
      releases, and kubelet enforces the final kill if the drain stalls.

    **Out of scope** (explicitly unchanged by this amendment): BFF
    cross-node packet-loss investigation remains a separate Phase 2 effort;
    no CiliumNetworkPolicy weakening; no certificate/issuer changes; no
    watchdog that deletes Cilium agents when HTTPS becomes unhealthy.

    **Codified**: executed 2026-08-17 on `spoke-pool-hybrid-dev-01`.
    `manifests/providers/hybrid/cilium-values.yaml` now sets `envoy.enabled:
    true`, `envoy.updateStrategy.rollingUpdate.maxUnavailable: 1` (see the
    mitigation note: `0` is invalid for DaemonSets without surge), and
    `envoy.securityContext.capabilities.envoy: [NET_ADMIN, SYS_ADMIN,
    NET_BIND_SERVICE]` (the chart default envoy cap list lacks
    NET_BIND_SERVICE; without it the DS cannot bind `:80`/`:443`).
    `manifests/providers/hybrid/k8s/cilium-addon-hybrid.yaml` carries the
    chart-rendered cilium-envoy ServiceAccount / ConfigMap / Service /
    DaemonSet (exactly four resources), `external-envoy-proxy: "true"` in
    the cilium-config data, and the DS post-render additions:
    `wait-for-envoy-release` init container (busybox; polls for the agent
    xDS socket on the shared hostPath AND absence of LISTEN `:80`/`:443`
    and `@envoy_domain_socket_parent_0` in the host netns; 600s fail-closed
    deadline) and `updateStrategy.maxUnavailable: 1`. The section above
    ("Migration / rollback") is the operator reference for this rollout.

## Addenda (2026-08-18)

### 11. Hybrid Topology Database Model (placement + storage per location)

**Foundation.** ADR-014's Platform-Wide Placement Rule applies to hybrid cells
unchanged: every stateful workload runs on **worker nodes only**
(`nodeSelector: node-role.kubernetes.io/worker: ""` — key-only label, never
`"true"`), control-plane nodes keep `control-plane:NoSchedule`, and placement is
declared, never inferred from storage binding. Hybrid adds one orthogonal axis:
`workload-location` (home vs hetzner), which determines the storage class a
stateful workload may use.

**Failure that motivated this model.** On `spoke-pool-hybrid-dev-01`,
`shared-cnpg` (then at `manifests/spoke/spoke-catalog/infra/cnpg-cluster.yaml`;
deployed from per-environment overlays since Wave 1, see Codified) was
created with `storage.storageClass: hcloud-volumes` and **no nodeSelector**.
With placement undeclared, CNPG scheduled the sole instance onto the
control-plane node. Storage binding did not save it: `hcloud-volumes` is
attachable on the Hetzner CP host, so the PVC bound and the cluster ran "fine"
on the CP — placement by accident of storage topology, exactly the failure
class ADR-014 now bans.

#### Placement classes (one per location)

| Class | nodeSelector (BOTH required) | StorageClass | Nodes |
|---|---|---|---|
| home | `node-role.kubernetes.io/worker: ""` + `workload-location: home` | `local-path` | WSL2 home workers |
| hetzner | `node-role.kubernetes.io/worker: ""` + `workload-location: hetzner` | `hcloud-volumes` | burst pool (`replicas: 0` default in dev) |

- A stateful workload is **bound to exactly one location at creation**; the
  location never changes afterwards (storage is node-local in the home class).
- `workload-location` is **never sufficient on its own** — the platform-wide
  worker selector is mandatory in both classes (ADR-014).
- The home-located dev cluster keeps the name `shared-cnpg`; a hetzner-located
  twin is named `shared-cnpg-hetzner`. In dev/stg only the home cluster is
  provisioned; the hetzner twin is provisioned only when burst capacity is
  actually scaled.

#### Storage class contract

| StorageClass | Provisioner | Binds on | Snapshots |
|---|---|---|---|
| `hcloud-volumes` | csi.hetzner.cloud | Hetzner nodes only (WaitForFirstConsumer) | yes |
| `local-path` | rancher.io/local-path | home workers only (node-local) | **no** |

- `hcloud-volumes` remains the **default** class in hybrid cells; `local-path`
  is referenced explicitly by name. A PVC with no class on a home worker gets
  `hcloud-volumes`, cannot bind (no Hetzner topology), and stays Pending — this
  is the correct failure mode, not an invitation to remove the class.
- `local-path` PVCs are **node-pinned**: data lives on the exact home worker
  that bound the claim. Node affinity on the PVC is a *consequence* of declared
  placement, never the placement mechanism itself (ADR-014).
- **BANNED**: `hcloud-volumes` on a home worker (hcloud CSI cannot provision
  for a WSL2 node) and `local-path` on a Hetzner worker (provisioner runs on
  home nodes only via nodeAffinity).
- Durability for home-located clusters: recovery is **barman restore only**
  (ADR-014 §Backup and restore contract — Hetzner Object Storage,
  `spoke-pool-backups` @ `https://hel1.your-objectstorage.com`) — local-path
  has no volume-snapshot support, so CNPG WAL archiving + S3 restore is the
  sole restore mechanism; see "Backup and restore isolation" below.
  Restoration requires a base backup **plus** WALs. Retention stays `30d`.

#### Backup and restore isolation

- Each spoke's backup data lives in a **per-spoke prefix**,
  `s3://spoke-pool-backups/<spoke-name>/` (bucket on Hetzner Object Storage per
  ADR-014 §Backup and restore contract), set via the
  `platform-spoke-catalog` ApplicationSet kustomize patch on
  `Cluster.spec.backup.barmanObjectStore.destinationPath` (keyed on the ArgoCD
  cluster name `{{ .name }}`). Previously every spoke shared the single
  `shared-cnpg` folder — a dev/stg collision that would cross-contaminate
  recoveries. The isolated prefix is established and proven **before** any
  destructive cutover.
- Base backups are declared per spoke via a static `ScheduledBackup`
  (`spec.cluster.name: shared-cnpg`, `backupOwnerReference: cluster`, daily
  schedule); none existed before Wave 1. `destinationPath` is **not** set on
  the ScheduledBackup — its CRD (CNPG 1.29) carries no destination fields; the
  backups inherit the Cluster's barman configuration.
- `spec.immediate: true` fires the first base backup at resource creation —
  the declarative **G1 gate**: the backup must reach `completed` (Backup CR)
  and appear in the new prefix before Wave A of any environment.

#### CSI daemonset boundary

The shared `csi-addon-template.yaml` runs `hcloud-csi-node` on **every** node
(its affinity excludes only Hetzner robot/root servers; a label-less WSL2 home
worker matches and gets a driver that cannot reach block devices). Codified
in a new hybrid-cell addon `manifests/providers/hybrid/k8s/csi-addon-hybrid.yaml`
(referenced from `spokepool-hybrid-composition.yaml`, analogous to
`cilium-addon-hybrid`):

1. `hcloud-csi-node` DaemonSet: add `nodeAffinity required NotIn
   workload-location: home` (exclude home workers).
2. `local-path-provisioner` DaemonSet: `nodeAffinity required In
   workload-location: home`, hostPath `/opt/local-path-provisioner`.
3. `local-path` StorageClass (`WaitForFirstConsumer`, `reclaimPolicy: Delete`,
   **not** default).

#### CNPG pattern for hybrid spokes

The CNPG Cluster manifest for the home class (`shared-cnpg`, per-environment
overlay `manifests/spoke/spoke-catalog/environments/{dev,stg,prod}/hybrid/cnpg-cluster.yaml`)
declares:

```yaml
spec:
  storage:
    size: 100Gi
    storageClass: local-path
  affinity:
    enablePodAntiAffinity: true
    topologyKey: kubernetes.io/hostname
    nodeSelector:
      node-role.kubernetes.io/worker: ""
      workload-location: home
```

- Instances, the Pooler, barman WAL-archiver jobs, and Atlas migration jobs all
  inherit this placement. The Pooler's own `affinity` must **mirror** the
  cluster selector — the CP-pinning bug reproduces in poolers.
- With `instances: 1` on local-path, pod anti-affinity is moot but retained so
  a later `instances: 2` spreads replicas across two home workers (each replica
  node-pinned via its PVC).
- Crossplane `tenant-db` flow unchanged: provider-sql over
  `shared-cnpg-rw.platform-data.svc.cluster.local`, credentials via ESO from
  Infisical (ADR-003); DB/user creation is storage-agnostic.

#### Migration of existing spokes (codified procedure)

Storage class cannot change in place and the recovery source requires an
isolated, proven prefix. The cutover is therefore a destructive-first GitOps
state machine (per-environment overlay state; dev and stg progress
independently because their authoritative state is directory-scoped):

1. **Wave A — prune.** Remove both `cnpg-cluster.yaml` and
   `scheduled-backup.yaml` from the environment overlay; ArgoCD prunes the
   Cluster **and its ScheduledBackup**. No resource references the pruned
   cluster during the WAL-only window — the `ScheduledBackup → Cluster`
   dependency is pruned along with its root. The barman store is untouched.
2. **Wave B — recover.** Re-add `cnpg-cluster.yaml` with
   `bootstrap.recovery` (method `object_store`, source = per-spoke prefix),
   the home-class placement + `local-path` storage, and re-add
   `scheduled-backup.yaml` with `immediate: true`. The name `shared-cnpg` is
   kept, so tenant connections (`shared-cnpg-rw.platform-data...`) are
   unchanged. Recovery is proven by: restored data + ADR-014/§11 placement
   checks + a completed post-recovery base backup (CNPG fires no backup while
   the cluster is still restoring).
3. **Wave C — steady state.** Return `bootstrap` to `initdb` in the manifest.
   `.spec.bootstrap` is creation-time state (CNPG does not re-reconcile it);
   the spoke-catalog ApplicationSet permanently ignores `/spec/bootstrap` —
   the declarative steady-state manifest cannot represent the historical
   bootstrap mechanism of an already-initialized cluster.
4. **Hetzner-only spokes skip A→B→C:** `hcloud-volumes` block devices attach
   anywhere in the zone, so changing `nodeSelector` rolls instances onto
   workers with their existing PVCs (placement-only rotation).
5. barman `retentionPolicy: "30d"` remains the safety net; WALs under the old
   shared prefix become orphaned garbage once the per-spoke prefix is live.

#### Enforcement / verification (hybrid-specific)

Beyond the ADR-014 post-bootstrap validation, in every hybrid spoke:

- every stateful workload's nodeSelector contains **both**
  `node-role.kubernetes.io/worker: ""` and exactly one `workload-location`;
- storageClass and location agree (`local-path` ↔ home, `hcloud-volumes` ↔
  hetzner);
- zero `hcloud-csi-node` pods on home workers; `local-path-provisioner` pods
  on all home workers;
- every `ScheduledBackup` references an existing Cluster; per-spoke barman
  prefixes `s3://spoke-pool-backups/<spoke-name>/` are unique across spokes.

**Codified:** the model above is the spec. Manifest changes required by the
Wave-1 merge (per §11 review):

1. CNPG moves out of `manifests/spoke/spoke-catalog/infra` (the base stays
   provider-neutral): `cnpg-cluster.yaml` + `scheduled-backup.yaml` land in
   `manifests/spoke/spoke-catalog/environments/{dev,stg,prod}/{hybrid,hetzner}/`.
2. `take-along-label.capi-to-argocd.provider` added to both provider
   compositions (`hybrid` / `hetzner`); the `platform-spoke-catalog`
   ApplicationSet path resolves `{{ .Values.environmentSlug }}/{{ provider }}`
   and hard-fails with `{{ fail }}` on any other provider value
   (`missingkey=error` on the label).
3. ApplicationSet kustomize patch targets **`Cluster/shared-cnpg` only**:
   `/spec/backup/barmanObjectStore/destinationPath` →
   `s3://spoke-pool-backups/{{ .name }}/`; every overlay manifest declares
   `endpointURL: https://hel1.your-objectstorage.com` per the ADR-014 backup
   contract (the pre-contract manifest shipped without it).
4. `scheduled-backup.yaml` per overlay — static (no per-spoke fields):
   `spec.immediate: true` at creation (fires the G1 base backup), daily
   schedule in steady state, `backupOwnerReference: cluster`.
5. spoke-catalog syncOptions gain `RespectIgnoreDifferences=true`, together
   with `ignoreDifferences: Cluster/shared-cnpg → /spec/bootstrap`.
6. `csi-addon-hybrid.yaml` (new, wired into `spokepool-hybrid-composition.yaml`):
   hcloud-csi-node excludes home workers, `local-path-provisioner` DaemonSet
   on home workers, `local-path` StorageClass (`WaitForFirstConsumer`,
   `reclaimPolicy: Delete`, **not** default).
7. post-bootstrap-validate.sh gains the §11 enforcement checks (Wave-8
   enforcement pass).

## References

- ADR-036 (pluggable providers) — §3 superseded.
- ADR-037 / ADR-038 (environment matrix).
- ADR-044 (local provider abstraction) — superseded; CAPD removed.
- ADR-014 (Platform-Wide Placement Rule) — §11 builds on its worker-only/taint/selector contract.
- PRD: hybrid home-lab cluster integration using Hetzner and Tailscale.

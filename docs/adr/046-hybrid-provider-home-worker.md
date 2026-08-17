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
   in hostNetwork mode).

## References

- ADR-036 (pluggable providers) — §3 superseded.
- ADR-037 / ADR-038 (environment matrix).
- ADR-044 (local provider abstraction) — superseded; CAPD removed.
- PRD: hybrid home-lab cluster integration using Hetzner and Tailscale.

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
- Works on a Tailscale-only network (no public IPs, no subnet router).

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
| Spoke default workers | Home-lab WSL2 nodes, unmanaged kubeadm join |
| Spoke API access | Tailnet-only |

### Provider Cell Layout

- `manifests/providers/_shared/` — ClusterClass (`spokepool-v1`), CCM/CSI addon
  templates. Shared by hetzner and hybrid cells (kustomize `../` works within
  the repo).
- `manifests/providers/hybrid/` — hybrid Composition, home-worker integration,
  spoke API front, credentials.
- `manifests/providers/hetzner/` — unchanged hetzner composition + credentials,
  referencing `../_shared`.

### ClusterClass Variables

The shared ClusterClass gains two variables:
- `controlPlaneLoadBalancer.enabled` (default `true`) — hetzner keeps the
  Hetzner LB; hybrid disables it.
- `controlPlaneEndpointHost` (default `""`) — hybrid supplies the Tailscale
  MagicDNS endpoint; hetzner leaves empty (LB advertises).

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

- **dev**: direct single-CP tailnet address (`control-plane-endpoint-host`
  annotation).
- **stg/prod**: `spoke-api-front.yaml` — a Tailscale-enrolled HAProxy TCP front
  exposing one MagicDNS name (`<spoke>-api-front`) over TCP 6443 to the Hetzner
  CP nodes.

### Scheduling Contract

- Default workloads: `nodeSelector: workload-location: home`.
- Burst workloads: explicit `nodeSelector: workload-location: hetzner`.

### Provider Registry

`operators/hub-operator/config/manager/provider-registry.yaml` maps exactly:
`hetzner → spokepool-hetzner`, `hybrid → spokepool-hybrid`. `capd` and `aws`
entries were removed (no compositions exist; sole-developer clean cut).

## Ownership

Defined by this ADR: hybrid cell manifests, home-worker join flow, spoke API
front, and the provider-registry map. ClusterClass and addon templates are
owned jointly with the hetzner cell (shared base, ADR-036).

## Consequences

### Positive

- Zero idle Hetzner worker cost (burst pool at `replicas: 0`).
- Reuses the tested Hetzner CAPI control-plane path verbatim.
- Real hardware capacity (~32GB across two boxes, scalable to 3+ nodes).
- Tailnet-only network: no public API exposure, no firewall/LB management for
  the spoke API.

### Negative

- Home workers are unmanaged — no CAPI-driven rollouts, upgrades, or
  remediation for them.
- kubeadm join depends on the spoke being reachable over the tailnet and on the
  hub-operator token being current (24h TTL, rotated by the controller).
- No public-LB path for the spoke API in hybrid (by design).

## References

- ADR-036 (pluggable providers) — §3 superseded.
- ADR-037 / ADR-038 (environment matrix).
- ADR-044 (local provider abstraction) — superseded; CAPD removed.
- PRD: hybrid home-lab cluster integration using Hetzner and Tailscale.

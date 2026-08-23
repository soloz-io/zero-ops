# ADR-052: Elastic Burst Capacity for Tenant Workloads

**Date:** 2026-08-23
**Status:** Proposed
**Relates to:** ADR-005 (unified abstraction layers in Crossplane), ADR-011 (declarative over imperative), ADR-012 (billing/metering), ADR-014 (platform-owned stateful infrastructure), ADR-033 (fleet scale targets), ADR-034 (control-plane failure domains), ADR-036 (pluggable provider architecture), ADR-039 (ownership model), ADR-041 (controller responsibility matrix), ADR-043 (control plane authority), ADR-046 (placement classes, burst worker pool), ADR-047 (fleet tenant deployment contract), `crossplane-capi-ownership-pattern`

---

## Context

Spoke worker capacity is a platform asset with a hard ceiling. Under ADR-046 the
default worker pool is home-lab hardware and the cloud `MachineDeployment` sits
at `replicas: 0` as an escape hatch. Every node added to serve a tenant workload
becomes capacity the platform must own, size, patch and pay for permanently — to
serve demand that is bursty, tenant-driven and unpredictable.

Two workload shapes drive that demand and neither belongs in a fixed pool:

| Shape | Consumer API | Profile |
|---|---|---|
| Batch execution | ephemeral job | Fire-and-forget, minutes, high resource envelope |
| Interactive sandbox | `agents.x-k8s.io/Sandbox` | Session-scoped, idle-terminated, reachable in-cluster, tenant-supplied image |

The platform needs both to run on capacity that appears when work exists and
disappears when it does not, while the cluster keeps serving control-plane and
infrastructure workloads on capacity the platform sizes for itself.

Four existing constraints bound the answer:

1. **The `Sandbox` CRD is vendored upstream and must not be forked** (ADR-019:
   OSS consumed unmodified). Its schema is `podTemplate`-shaped and already
   carries `nodeSelector`, `tolerations` and `runtimeClassName`.
2. **Sandboxes are reached over the cluster network** at
   `http://<service>.<namespace>.svc.cluster.local:<port>`, and their egress is
   policed by a platform-owned `CiliumClusterwideNetworkPolicy`. Compute that
   cannot be reached this way, or policed this way, cannot serve them.
3. **Crossplane composes only Managed Resources.** Per
   `crossplane-capi-ownership-pattern` and ADR-005, CAPI and core Kubernetes
   objects must be wrapped in `provider-kubernetes` `Object` resources.
4. **Infrastructure authority is Crossplane** (ADR-043), and CAPI/CAPH is the
   platform's established mechanism for cloud machines. A controller that calls
   a cloud provider API directly is a second infrastructure provisioner — the
   responsibility creep ADR-041 exists to prevent.

---

## Decision

**Home-lab capacity is reserved for platform infrastructure and fleet
control-plane services. Tenant execution runs on elastic cloud nodes,
provisioned through the platform's existing CAPI/CAPH path and scaled by
unschedulable demand.**

The only new component is the ephemeral job reconciliation logic. No new
infrastructure controller is introduced.

### 0. Invariant: burst nodes join the spoke, they do not form a cluster

Burst nodes are members of **the same spoke cluster** that already runs the
fleet's workloads. They are the ADR-046 burst `MachineDeployment` for that spoke,
scaled off zero — not a separate cluster, not a separate control plane, not an
external execution target.

The distinction this ADR draws is **placement, not membership**: a workload moves
from a home-lab node to a cloud node within one cluster, distinguished only by
the `workload-location` label (ADR-046 §11 placement classes).

Everything else in this decision depends on that invariant. Constraint 2 is
satisfied — `ClusterIP` Services resolve, cluster DNS answers, the platform
`CiliumClusterwideNetworkPolicy` enforces, `kubectl logs` streams — because the
burst node is in the *same* cluster as the consumer, not merely because it is in
*a* cluster. A design that placed burst nodes in a second cluster would
reintroduce every problem §Alternatives rejects, with a cross-cluster hop added.

Consequently: no burst node ever carries a control-plane role, and the burst pool
scales only within one spoke. A cell that needs more burst capacity than one
spoke's pool allows raises `maxNodes`; it does not gain a second cluster.

### 1. Four layers, four questions

Each layer answers exactly one question and owns nothing the layer below owns:

```
EphemeralJob / Sandbox
        │  "this workload must run on cloud capacity"
        ▼
EphemeralJob controller  ·  Kyverno placement policy
        │  emits a Pod carrying burst placement intent
        ▼
Scheduler + cluster-autoscaler
        │  "I need N more nodes"
        ▼
BurstCapacity XR → provider-kubernetes Object → burst MachineDeployment
        │  "those N nodes should exist, within the declared envelope"
        ▼
CAPI / CAPH
        │
        ▼
Hetzner nodes  (workload-location: hetzner)
        │
        ▼
Normal Kubernetes scheduling → workload Pod
```

**The controller expresses capacity intent; it never expresses capacity
quantity.** It does not create, patch or compute `replicas`, and it does not own
the `BurstCapacity` XR. Its relationship to that XR is a *dependency* — the job
requires capacity the XR governs — not ownership. If every job scaled
infrastructure independently, N jobs would produce N uncoordinated scaling
decisions; deciding how many nodes serve a set of pending pods is the
autoscaler's single responsibility.

### 2. `BurstCapacity` XR — the envelope, not the count

`BurstCapacity` is a **platform-owned, per-cell, Git-declared** composite
resource. It is reconciled by ArgoCD → Crossplane → `provider-kubernetes`
`Object` → the burst `MachineDeployment` that ADR-046 already defines at
`replicas: 0`. One XR per cell per size class; never one per job.

```yaml
apiVersion: compute.nutgraf.in/v1alpha1
kind: BurstCapacity
metadata:
  name: burst-standard
spec:
  cellId: <cell>
  placementClass: hetzner        # ADR-046 placement class
  machineType: <provider size class>
  minNodes: 0                    # >0 keeps warm capacity (§6)
  maxNodes: 8                    # the cell's burst spend ceiling
  scaleDownUnneededAfter: 10m
```

The XR composes the `MachineDeployment` and the autoscaler bounds annotations
(`cluster.x-k8s.io/cluster-api-autoscaler-node-group-{min,max}-size`) that
derive from `minNodes`/`maxNodes`. **`maxNodes` is the cell's cloud spend
ceiling**, declared in Git, reviewed like any other infrastructure change, and
enforced by a component that cannot exceed it.

Crossplane owns whether the pool exists and what shape it has. It does not own
how many nodes are running right now.

### 3. Scaling is unschedulable demand, not a control loop we write

`cluster-autoscaler` with `--cloud-provider=clusterapi` scales the burst
`MachineDeployment` from unschedulable pods, within the annotated bounds. It is
deployed **one instance per spoke, running on the hub**, using the
CAPI-generated spoke kubeconfig the hub already holds: the `MachineDeployment`
it scales lives on the hub, so this placement keeps the management credential on
the management cluster and adds no cross-cluster credential to a
tenant-serving spoke.

Scaling is therefore hub-dependent. Per ADR-034 this is a degradation, not an
outage: during a hub outage running workloads continue and the existing burst
pool keeps serving; only *new* capacity is unavailable. The dependency is
inherent to the `MachineDeployment` living on the hub and is not introduced by
this placement.

The platform writes no scaling logic. Node group discovery, scale-down,
draining, PodDisruptionBudget awareness, unready-node handling and expander
policy are all inherited.

### 4. Placement is platform-mutated, never fleet-declared

A platform-owned Kyverno mutating policy adds, to pods that match a
platform-owned selector — sandbox pods, and pods owned by an `EphemeralJob` — in
namespaces whose fleet has the capability enabled:

- `nodeSelector: workload-location: <placementClass>`
- the matching toleration for the burst pool taint
- the platform `PriorityClass` for burst work (§5)

A companion validating policy rejects fleet-authored burst tolerations and
`priorityClassName` values. This preserves ADR-047: a fleet can neither place
itself onto burst capacity it was not granted, nor escape burst placement to
consume home-lab nodes.

### 5. Per-fleet bounds are native Kubernetes, not a new admission path

Burst pods carry a platform-owned `PriorityClass` (`burst-tenant`) with a value
below every platform workload, so burst work can never preempt infrastructure.

Per-fleet burst capacity is then a `ResourceQuota` with a
`scopeSelector` on that priority class:

```yaml
spec:
  hard: { requests.cpu: "16", requests.memory: 32Gi, pods: "8" }
  scopeSelector:
    matchExpressions:
      - operator: In
        scopeName: PriorityClass
        values: ["burst-tenant"]
```

This bounds a fleet's burst footprint without bounding its in-cluster
footprint, is enforced by the API server, and requires no custom webhook. It
composes with `maxNodes` (§2): the XR caps the cell, the quota caps the fleet.
The quota is platform-rendered from `fleet-registry` values at ADR-047 Tier 2 —
a fleet declares its request, the platform authors the object.

### 6. Warm capacity and scale-down are settings, not features

`minNodes > 0` keeps that many nodes permanently available, so the first burst
workload of the day does not pay node-join latency. This is the warm pool,
expressed natively; the platform writes no pre-warming code.

`scaleDownUnneededAfter` governs reclamation. One consequence must be stated
plainly: **an idle interactive sandbox pins its node for as long as its own
idle timeout allows**, so burst cost tracks sandbox idle timeout rather than
sandbox usage. Sandbox `shutdownTime`/`shutdownPolicy` and
`scaleDownUnneededAfter` must be tuned together, and the sandbox idle timeout is
the dominant cost lever.

### 7. `EphemeralJob` owns the job state machine and nothing else

`compute.nutgraf.in/v1alpha1 EphemeralJob` is a fleet-authored, namespaced
resource. Its controller materialises a `batch/v1` Job and owns exactly one
thing: the job lifecycle.

```
Pending → Provisioning → Running → Succeeded | Failed | TimedOut → Cleanup
```

`Provisioning` is the interval in which the pod is unschedulable and capacity is
being created. The controller surfaces *why* — unschedulable, scaling, node
joining, image pulling — by projecting pod events and node conditions into
`status`, so a submitter sees infrastructure progress without holding
infrastructure permissions.

Beyond the state machine the CRD adds what a bare `batch/v1` Job lacks: a
single-use completion token, an input/output object contract, a terminal result
the submitter can await, and TTL cleanup. It provisions nothing.

### 8. `Sandbox` is unmodified

The upstream controller creates a pod; Kyverno places it on burst capacity; the
autoscaler provides the node. `shutdownPolicy`/`shutdownTime` continue to drive
teardown, and when the last pod leaves a node the autoscaler reclaims it.

Because a burst node is an ordinary node of the same spoke cluster (§0),
everything constraint 2 requires keeps working unchanged: `ClusterIP` Services,
cluster DNS, the platform `CiliumClusterwideNetworkPolicy` egress allowlist,
`kubectl logs`, events, CSI and quota. The sandbox client's
`http://<svc>.<ns>.svc.cluster.local:<port>` address is unaffected by placement.

### 9. Isolation, and the one workload class that needs more

Burst workloads are isolated at the pod boundary — the same boundary they have
today, relocated to cloud nodes. This is not a regression.

Workloads requiring host-level privilege (nested containers, a container
runtime socket) are the exception: as pods they would need privileged
`securityContext`, which is worse on a shared node than on a disposable
machine. The remedy is **node dedication, not a second execution mechanism**: a
separate placement class whose `BurstCapacity` provisions a dedicated tainted
pool sized so one pod occupies one node. Machine-level isolation, same four
layers, no new component.

### 10. Capacity rule

> **May consume home-lab nodes:** platform infrastructure and controllers, and
> fleet control-plane services whose resource envelope is set by the platform —
> API, BFF, frontend, coordinating workers.
>
> **Must burst:** any workload whose resource envelope is set by tenant demand —
> sandboxes, batch jobs, renders, inference, fine-tuning.

Fleet quota (ADR-047) bounds the first. `BurstCapacity.maxNodes` and the
priority-scoped `ResourceQuota` bound the second.

---

## Alternatives considered and rejected

**Dedicated machine per workload (Virtual Kubelet + a new Crossplane provider).**
Register virtual nodes whose pod lifecycle handler creates one cloud machine per
pod, provisioned through a purpose-built `provider-hetzner`.

Rejected as unnecessary complexity. It requires building a Crossplane provider
(none exists for this cloud today), a Virtual Kubelet provider, and a guest
agent, and — because a machine-backed pod is not a CNI endpoint — reconstructing
pod networking, `EndpointSlice` publication, `Service` reachability, NetworkPolicy
enforcement, FQDN egress control, log streaming and a warm pool. It would also
split Network Policy authority away from the CNI, requiring an ADR-043
amendment. The requirement is that tenant workloads run **outside the home
cluster**, not outside Kubernetes; a cloud node satisfies it while a dedicated
machine per pod satisfies only a stricter isolation requirement that §9 meets
with node dedication.

**A controller calling the cloud provider API directly.** Rejected under ADR-043
(Infrastructure authority is Crossplane) and ADR-041 (a second infrastructure
provisioner is responsibility creep). It would additionally reintroduce
credential handling, orphan reclamation and provider-specific logic into a
workload controller.

**The job controller scaling `MachineDeployment` replicas.** Rejected: N jobs
would produce N uncoordinated scaling decisions, and the controller would own a
capacity-planning responsibility that `cluster-autoscaler` already discharges
correctly, including scale-down and draining.

---

## Ownership

### Ownership under this ADR

Registered against the ADR-039 matrix in its canonical seven-column form:

| Resource Class | Generator | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|---|
| Burst Capacity Envelope | Platform | Git (`zero-ops`) | ArgoCD | Crossplane | cluster-autoscaler | Day-1+ |
| Burst Node Count | cluster-autoscaler | Kubernetes API (`MachineDeployment.spec.replicas`) | cluster-autoscaler | CAPI | Scheduler | Day-1+ |
| Burst Machine | CAPH | Provider API | CAPI | CAPH | Burst pods | Day-1+ |
| Burst Placement Policy | Platform | Git (`zero-ops`) | ArgoCD | Kyverno | Burst pods | Day-1+ |
| Per-Fleet Burst Quota | Platform | Git (`fleet-registry`) | ArgoCD | kube-apiserver | Fleets | Day-1+ |
| Ephemeral Job Request | Fleet workload | Kubernetes API (fleet namespace) | Fleet | ephemeral-job controller | Fleet workloads | Day-1+ |
| Burst Compute Usage | Observability Stack | OpenMeter | Observability Stack | Alloy / OTel Collector | Billing, SRE | Day-1+ |

**The deliberate split.** A fleet is Lifecycle Owner of the *request* — a
`Sandbox` or an `EphemeralJob`. The platform owns every layer the request
depends on. This mirrors `TenantDatabase` (fleet declares, CNPG and Crossplane
own) and `Certificate` (fleet declares, cert-manager owns), and satisfies
ADR-039 constraint 3: Lifecycle Owner and Reconciler are distinct on the request
row.

**Metering** derives from pod resource-seconds already collected by the
observability stack and attributed by namespace. This covers both consumer APIs
uniformly and adds no emission path to the job controller.

### Repository ownership

| Artefact | Owner | Repository |
|---|---|---|
| `EphemeralJob` CRD and controller | Platform | `zero-ops` |
| `BurstCapacity` XRD and composition | Platform | `zero-ops` |
| cluster-autoscaler deployment, Kyverno placement policies, `PriorityClass` | Platform | `zero-ops` |
| Capability opt-in and per-fleet burst quota values | Fleet | `fleet-registry` |
| Workload images and submission call sites | Fleet | fleet's own repository |

The falsifiable test of conformance: **a conforming fleet contains no
compute-provisioning code and no provider credential.**

### ADR-041 matrix entries

**Ephemeral Job Controller**

| Allowed | Forbidden |
|---|---|
| Reconciling `EphemeralJob` CRs and owning the job state machine | Infrastructure provisioning of any kind |
| Creating and owning the `batch/v1` Job for a request | Creating, patching or computing `MachineDeployment` replicas |
| Projecting pod and node conditions into job status | Holding or reading provider credentials |
| Minting single-use, job-scoped completion tokens | Authoring placement policy, quota or `PriorityClass` |
| Enforcing job timeout and TTL cleanup | Secret generation, PKI operations |

**cluster-autoscaler**

| Allowed | Forbidden |
|---|---|
| Scaling annotated `MachineDeployment` replicas within declared bounds | Creating or modifying machine templates or `MachineDeployment` shape |
| Cordoning and draining nodes it scaled down | Scaling outside the annotated min/max bounds |
| Reading pods, nodes and PodDisruptionBudgets to make scaling decisions | Provisioning infrastructure directly |
| | Acting on node groups it did not discover through CAPI |

### ADR-043 authority

**No amendment is required.** Infrastructure remains Crossplane's domain and is
exercised through the established Crossplane → `provider-kubernetes` → CAPI path.
Network Policy remains the CNI's domain, because burst nodes are ordinary CNI
nodes. Kubernetes State remains ArgoCD's domain, because the burst envelope is
declared in Git.

---

## Consequences

### Positive

- Home-lab node count is sized by platform need alone; tenant demand no longer
  drives it. This is the goal the ADR exists to serve.
- The only new component is the ephemeral job state machine. No new
  infrastructure controller, no new Crossplane provider, no guest agent.
- `Sandbox` runs on burst capacity with the upstream controller unmodified, and
  every Kubernetes semantic it depends on — Services, DNS, NetworkPolicy, logs,
  CSI — continues to work because a burst node is an ordinary node.
- Spend is bounded twice, declaratively: `maxNodes` per cell in Git, and a
  priority-scoped `ResourceQuota` per fleet, both enforced by components that
  cannot exceed them.
- Warm capacity and scale-down are settings on an existing component rather than
  features to build and operate.
- ADR-043 needs no amendment and no authority is split.

### Negative / Trade-offs

- **Cold-start latency is a node join**, materially slower than starting a pod
  on existing capacity. `minNodes > 0` trades money for latency; there is no
  setting that is both cheap and fast.
- **Idle interactive sandboxes pin nodes**, so burst cost tracks idle timeout
  rather than usage (§6). This is the dominant cost risk and it is a tuning
  problem, not a design flaw.
- **Scaling depends on hub reachability** (§3). Degraded rather than broken, but
  it is a real dependency for a tenant-facing capability.
- **Isolation is pod-level by default.** Workloads needing host privilege
  require a dedicated pool (§9), which is less efficient than shared nodes.
- Node churn adds CAPI/CAPH reconciliation load and cloud API calls that scale
  with burst frequency rather than with fleet count.

---

## Impact

- **ADR-046** gains a purpose for the burst `MachineDeployment` it defines at
  `replicas: 0`: it becomes the elastic tenant-execution pool, bounded by a
  `BurstCapacity` XR. The escape hatch becomes a product.
- **ADR-047** gains a platform capability consumed through Tier-2 RBAC and
  platform-rendered quota, and the §10 capacity rule constrains what a fleet may
  run on home-lab capacity.
- **ADR-039** gains seven resource-class rows; **ADR-041** gains two controller
  entries (§Ownership).
- **ADR-036** gains burst size classes and warm-capacity settings in the
  provider capability contract.
- **ADR-043**, **ADR-014** and **ADR-005** are unaffected.
- Migration of any existing fleet-built provisioning implementation is tracked
  separately as an alignment plan, not in this ADR.

## References

- ADR-046 §Topology — the burst `MachineDeployment` at `replicas: 0`
- ADR-046 §11 — placement classes
- `crossplane-capi-ownership-pattern` — why CAPI objects are wrapped in `provider-kubernetes` `Object`
- ADR-047 §Sandbox Workloads — the platform-capability contract this ADR extends to elastic capacity
- `manifests/spoke/spoke-catalog/infra/agent-sandbox/` — the upstream CRD that must not be forked
- `manifests/spoke/spoke-catalog/infra/sandbox-network-policy.yaml` — the egress control that keeps working unchanged

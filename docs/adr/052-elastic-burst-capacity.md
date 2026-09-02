# ADR-052: Elastic Burst Capacity for Tenant Workloads

**Date:** 2026-08-23
**Status:** Accepted (amended 2026-08-31, 2026-09-02)
**Relates to:** ADR-005 (unified abstraction layers in Crossplane), ADR-011 (declarative over imperative), ADR-012 (billing/metering), ADR-014 (platform-owned stateful infrastructure), ADR-033 (fleet scale targets), ADR-034 (control-plane failure domains), ADR-036 (pluggable provider architecture), ADR-039 (ownership model), ADR-041 (controller responsibility matrix), ADR-043 (control plane authority), ADR-046 (placement classes, burst worker pool), ADR-047 (fleet tenant deployment contract), `crossplane-capi-ownership-pattern`

---

> **Amendment 2026-09-02 — Authorship replaces admission; the count gets an owner.**
> Two gaps, both found by running the path end to end. Neither changes the four
> layers. §8 is withdrawn and rewritten: placing a sandbox by admission policy is
> a weaker guarantee than authoring its pod, and on the dev spoke the policy was
> absent from the catalog entirely, so every sandbox ran unplaced on home-lab
> capacity — the outcome this ADR exists to prevent. `EphemeralJob` now carries a
> lifecycle discriminator and the operator authors the pod for both shapes.
> Separately, the component §3 and the Ownership table name as owner of Burst
> Node Count — cluster-autoscaler — had never been deployed. The burst
> MachineDeployment is rendered with autoscaler bounds and deliberately no
> replicas field, so with nothing filling that role the pool sat at zero
> permanently and every burst pod pended against capacity that could not arrive.
> An ADR naming an owner does not create one.

---

> **Amendment 2026-08-31 — Cold-start budget and sandbox provisioning visibility.**
> Two gaps, both on the interactive-sandbox path, neither of which changes the
> decision. The four layers stand and no consumer gains an infrastructure
> permission. What is added is a *stated*
> cold-start budget (§11) — this ADR names node-join latency as a cost but never
> as a number, and the first consumer to reach burst capacity holds a
> 120-second readiness deadline that a node join cannot meet — and a
> provisioning-visibility contract for `Sandbox` (§12), which §7 grants
> `EphemeralJob` and §8 withholds from the workload where a human is actually
> waiting.

> **Amendment 2026-08-31 (2) — Placement authorship, and why the job path stays
> a controller.**
> Two clarifications to the Decision, neither changing it.
>
> **Placement is supplied by whichever component authors the pod (§4).** The job
> controller authors the `batch/v1` Job and therefore writes `nodeSelector`, the
> burst toleration and `priorityClassName` into it directly. Kyverno mutation is
> scoped to `Sandbox`, the one pod the platform does not author because its CRD
> is vendored (constraint 1). An arrangement in which the controller marks the
> Job for classification and admission policy then adds placement to that same
> object is rejected: it holds the weaker guarantee while the platform has
> already paid for the stronger one.
>
> **`EphemeralJob` is deliberately not an XRD and composition (§7).** ADR-005
> would normally place a tenant-facing abstraction there, and it was considered.
> It does not hold for this resource class: submission rate follows a fleet's
> concurrent users, so job objects are numerous and short-lived, while
> Crossplane's costs are proportional to object lifetime. The exception is
> recorded in §Impact and does not generalise.
>
> A consequence of that volume is stated in §Consequences and is not resolved
> here: at the expected rate, admission rejection under the §5 quota is the
> common case rather than an error path, and this ADR provides no queue.

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

The abstraction, end to end. Each boundary below is a place where a decision
stops being the fleet's and becomes the platform's, and nothing crosses back:

```text
  FLEET                    │ PLATFORM
  ─────────────────────────┼──────────────────────────────────────────────────
                           │
  EphemeralJob             │   ephemeral-job-operator          §7, §8
    image, command         │     authors the Pod
    capacity (envelope)    │     writes nodeSelector,          §4
    mode: Job | Service    │       tolerations, priorityClass
    timeout / idle         │     never provisions a node
                           │            │
  ── declares what ────────┼────────────┼── decides where ────────────────────
                           │            ▼
                           │   Pod: unschedulable
                           │            │
                           │            │ demand, not a control loop     §3
                           │            ▼
                           │   cluster-autoscaler
                           │     reads MachineDeployment bounds
                           │     min 0 .. max N                          §2
                           │            │
  ── asks for capacity ────┼────────────┼── owns the count ───────────────────
                           │            ▼
                           │   MachineDeployment.replicas
                           │            │
                           │            ▼
                           │   CAPI + provider → burst node joins spoke  §0
                           │            │
                           │            ▼
                           │   Pod scheduled, bounded by ResourceQuota   §5
                           │            │
  ◄── terminal result ─────┼────────────┘
      (callback)           │
```

Four objects, four owners, one direction. A fleet expresses demand and never a
node property; the autoscaler owns the count and never the placement; the
operator owns the pod and never the infrastructure. §11 keeps the two clocks —
waiting for a node, and running — separate, so neither is charged for the other.


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

```
            ONE spoke cluster — one control plane, one CNI, one DNS, one quota
 ┌────────────────────────────────────────────────────────────────────────────────┐
 │                                                                                │
 │   workload-location: home                     workload-location: hetzner       │
 │   ┌──────────────────────────────┐            ┌──────────────────────────────┐ │
 │   │  home-lab nodes — FIXED      │            │  burst nodes — ELASTIC       │ │
 │   │  (capacity you already own)  │            │  MachineDeployment 0 ──► N   │ │
 │   │                              │            │  (ADR-046 burst pool)        │ │
 │   │  · platform infrastructure   │            │                              │ │
 │   │  · platform controllers      │            │  · Sandbox pods              │ │
 │   │  · fleet control plane:      │            │  · EphemeralJob pods         │ │
 │   │      API, BFF, frontend,     │            │  · renders, inference,       │ │
 │   │      coordinating workers    │            │    fine-tuning, batch        │ │
 │   │                              │            │                              │ │
 │   │  envelope set by PLATFORM    │            │  envelope set by TENANT      │ │
 │   └──────────────────────────────┘            └──────────────────────────────┘ │
 │                  ▲                                           ▲                 │
 │                  │                                           │                 │
 │                  └───────────────── same cluster ────────────┘                 │
 │                    ClusterIP resolves · cluster DNS answers                    │
 │                    CiliumClusterwideNetworkPolicy enforces                     │
 │                    kubectl logs streams · events, CSI, quota apply             │
 └────────────────────────────────────────────────────────────────────────────────┘

   NOT a second cluster · NOT a second control plane · NOT an external executor.
   A burst node is an ordinary node of this spoke that happens to be disposable.
```

```
EphemeralJob → Job → unschedulable Pod → autoscaler → CAPI/CAPH → Hetzner → scheduler → Pod
```

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
EphemeralJob controller (jobs)  ·  Kyverno mutation (Sandbox only)
        │  emits a Pod already carrying burst placement
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

**This layer expresses capacity intent; it never expresses capacity quantity.**
Neither the controller nor the mutation creates, patches or computes `replicas`,
and neither owns the `BurstCapacity` XR. The relationship to that XR is a
*dependency* — the job requires capacity the XR governs — not ownership. If every job scaled
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

### 4. Placement is platform-supplied, never fleet-declared

Placement is set by whichever component authors the pod, and the platform authors
it on both paths — but by different means, because it authors the two pods to a
different degree.

**Jobs: authored.** The platform writes the entire `batch/v1` Job (§7), so
`nodeSelector`, the burst toleration and `priorityClassName` are written into it
directly. The fleet's `EphemeralJob` has no field in which to express placement,
so on this path there is nothing to mutate and nothing to reject.

**Sandboxes: mutated.** The `Sandbox` CRD is vendored (constraint 1) and the
upstream controller authors the pod from `spec.podTemplate`, so the platform
cannot compose it. Here a platform-owned Kyverno mutating policy adds, to pods
matching the platform-owned `agents.x-k8s.io/sandbox` selector, in namespaces
whose fleet has the capability enabled:

- `nodeSelector: workload-location: <placementClass>`
- the matching toleration for the burst pool taint
- the platform `PriorityClass` for burst work (§5)

A companion validating policy rejects fleet-authored burst tolerations and
`priorityClassName` values on that path. This preserves ADR-047: a fleet can
neither place itself onto burst capacity it was not granted, nor escape burst
placement to consume home-lab nodes.

The two mechanisms are not equally strong, and the difference is worth stating.
Mutation is a guarantee that holds because a policy matches correctly and a
companion policy rejects correctly. Authorship is a guarantee that holds because
the fleet-facing resource has no field to carry the value. **Unrepresentable is
a stronger property than rejected** — it survives a policy being disabled,
mis-scoped, or lost in a refactor.

The test is therefore not which component supplies placement in the abstract,
but whether the platform authors the pod. Where it does, it writes placement and
no mutation is required. Where an unmodifiable upstream CRD means it does not,
mutation is correct and the validating policy carries the guarantee. A design in
which the platform authors the pod and *then* has a second component add
placement to it satisfies neither test: it holds the weaker guarantee while
paying for the stronger one.

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

Two bounds, two authors, two enforcement points — neither raisable by a fleet:

```
   PLATFORM Git                                FLEET REGISTRY (ADR-047 Tier 2)
   BurstCapacity.maxNodes: 8                   fleet declares a burst request,
        │                                      platform AUTHORS the object
        │                                           │
        │  caps the CELL's spend                    │  caps the FLEET's share
        ▼                                           ▼
 ┌───────────────────────────────┐       ┌────────────────────────────────────┐
 │ node-group-max-size           │       │ ResourceQuota                      │
 │   annotation on the burst     │       │   scopeSelector: PriorityClass     │
 │   MachineDeployment           │       │     In [burst-tenant]              │
 │                               │       │   hard: cpu / memory / pods        │
 │ enforced by the autoscaler    │       │ enforced by the API server         │
 └───────────────────────────────┘       └────────────────────────────────────┘
        │                                           │
        └──────────────────┬────────────────────────┘
                           ▼
              a fleet cannot exceed EITHER bound:
              quota rejects the pod before it is ever pending,
              maxNodes refuses the node even if the pod is pending
```

The `burst-tenant` PriorityClass sits below every platform workload, so burst
work can never preempt infrastructure — the quota scope and the preemption order
come from the same object.
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

**Why a controller and not a composition** (Amendment 2). A tenant-facing
abstraction on this platform is normally an XRD and a composition, and ADR-005
would point that way. It does not hold here, because job objects are not
infrastructure. Submission rate scales with the number of concurrent users of a
fleet's product rather than with how often infrastructure changes, so the
population is large, short-lived and continuously turning over. Crossplane
reconciles a composite and its managed resources for as long as they exist,
polls their observed state, and holds finalizers on both — costs proportional to
object lifetime, which is the correct trade for a database and the wrong one for
a workload measured in seconds. Deletion is where this concentrates: TTL cleanup
is the most frequently executed path in the system, and it is the path on which
finalizer-bound objects accumulate. A controller reconciles on watch rather than
on a timer and adds one object per request rather than three.

**Placement is written by the controller, not stamped for another component**
(Amendment 2). Because the controller authors the Job, it supplies the §4
placement fields directly. An arrangement in which the controller marks the Job
with a burst classification and admission policy then adds placement to the same
object is rejected: it holds the weaker guarantee — placement present only while
a policy is loaded and correctly scoped — while the platform has already paid
for the stronger one by authoring the object. The validating policy remains, as
defence in depth rather than as the mechanism.

This does not reopen the ADR-041 boundary. The controller writes placement into
a workload it owns; it does not author placement *policy*, and it holds no
authority over nodes, machine templates or replica counts.

### 8. One authored pod, two lifecycles (Amendment 2026-09-02)

This section previously read "`Sandbox` is unmodified": the upstream controller
created the pod, Kyverno placed it, the autoscaler provided the node. That is
withdrawn. It rested on admission being a sufficient insertion point for
placement, and it is not.

A mutating policy places a pod only while it is loaded, enabled and correctly
scoped. When it is not, the pod is admitted anyway — unplaced, on whatever node
has room, with nothing in error. On the dev spoke the policy was absent from the
catalog entirely and every sandbox ran on home-lab capacity, which is the outcome
this ADR exists to prevent. §4 already states that authorship is the stronger
property; §8 was the one place the platform did not hold it.

`EphemeralJob` therefore carries a lifecycle discriminator, and the operator
authors the pod in both cases:

```
mode: Job      Pending → Provisioning → Running → Succeeded | Failed | TimedOut
               bounded by its own exit; reaped by TTL after a terminal phase

mode: Service  Pending → Provisioning → Running → Succeeded
               no completion to wait for; reaped by an idle clock
```

`Service` adds a Pod rather than a `batch/v1` Job, because `batch/v1` exists to
drive something to completion and an interactive sandbox has none; a Service in
front of it, owned by the same request, gives the stable in-cluster address.

The idle clock is required in `Service` mode, not defaulted. A long-lived
workload with no idle bound never terminates, and a bound guessed on a session's
behalf is how burst capacity leaks. It is refreshed through the status
subresource, so a client saying "still in use" cannot also rewrite the request's
image, capacity or placement class.

Because a burst node is an ordinary node of the same spoke cluster (§0),
everything constraint 2 requires keeps working unchanged: `ClusterIP` Services,
cluster DNS, the platform `CiliumClusterwideNetworkPolicy` egress allowlist,
`kubectl logs`, events, CSI and quota. The sandbox client's
`http://<svc>.<ns>.svc.cluster.local:<port>` address is unaffected by placement.

The vendored `Sandbox` CRD is no longer the provisioning path. Constraint 1 — do
not fork upstream — is satisfied more completely than before: the platform does
not modify the upstream controller, it stops depending on it for placement.

The validating policy that rejects fleet-supplied placement remains, and the
mutating policy is now redundant on this path. Defence in depth, not the
mechanism.

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

### 11. Cold start is a budget, not an acknowledgement (Amendment 2026-08-31)

§Consequences records that "cold-start latency is a node join, materially slower
than starting a pod on existing capacity." That is true and it is not actionable.
It names no number, and it says nothing about what the latency demands of the
consumers this ADR moves onto burst capacity — which is where the cost is
actually paid.

The first such consumer already contradicts it. `@waypoint/sandbox-k8s` — the
library that creates every `Sandbox` CR under §8 — holds `POLL_TIMEOUT_MS` and
`HTTP_READY_TIMEOUT_MS` at 120 seconds. A cold burst start is the serial sum of
the autoscaler's scan interval, the CAPI/CAPH machine create, Hetzner
provisioning and boot, kubeadm join, CNI readiness, image pull and container
start. Two minutes does not cover it, and no tuning of the consumer's polling
interval changes that.

**The resulting failure is not a clean timeout.** In-cluster, the readiness wait
polls the `ClusterIP` URL and never reads pod phase, so an unschedulable pod and
a crashed sandbox are the same observation — connection refused until the
deadline — and the error distinguishes neither. Worse, the registry row is
written only *after* readiness succeeds, so a timeout leaves a `Sandbox` CR with
no registry entry. The next message finds no active mapping, takes the recreate
branch, and deletes the deterministically-named CR — destroying the pending pod
whose node was still being provisioned. The autoscaler observes the pending pod
disappear and may reverse the scale-up it had already triggered; a new pod with
the same name appears moments later and the cycle repeats. The system does not
converge, it thrashes, and it pays for node joins that never serve a request.

This is a contract gap, not a bug in one consumer. Any consumer written against
existing capacity carries a deadline calibrated to pod start, and this ADR moves
such consumers onto capacity measured in machine start without telling them.
Therefore:

1. **Every placement class MUST declare a cold-start budget** — p50 and p95
   seconds from unschedulable pod to `Running` — as a measured, documented
   property of the `BurstCapacity` XR. This ADR deliberately declines to name a
   number: it depends on machine type, image size and registry locality, and an
   invented default would be adopted as though it had been measured.

2. **No consumer may hold a readiness deadline shorter than the p95 budget of
   the placement class it targets.** A consumer that cannot wait that long has
   not been made burst-ready by relabelling its pods; the remedy is `minNodes > 0`
   (§6), which is what warm capacity exists for.

3. **A consumer MUST NOT delete a workload object solely because a readiness
   deadline expired.** To the autoscaler, deletion during provisioning is
   indistinguishable from deletion of a healthy workload, and is the thrash
   described above. Provisioning-timeout and provisioning-failure are different
   terminal conditions and must be distinguished before any cleanup runs.

4. **Where an interactive path cannot tolerate the budget, warm capacity is a
   required cost, not a tuning preference.** `minNodes > 0` is the only setting
   under which cold start does not apply, and §6's trade — money for latency —
   is then already decided by the workload, not open to the operator.

The migration consequence is explicit: **a fleet is not burst-ready when its
pods carry burst placement. It is burst-ready when its timeouts and its
failure handling match the budget of the class it was placed in.**

### 12. `Sandbox` provisioning visibility (Amendment 2026-08-31)

§7 gives `EphemeralJob` a `Provisioning` state that surfaces *why* —
unschedulable, scaling, node joining, image pulling — "so a submitter sees
infrastructure progress without holding infrastructure permissions." §8 says
`Sandbox` is unmodified. Both are right, and together they left the asymmetry
backwards: batch work, where no human is waiting, got the progress signal;
the interactive session, where a human is waiting, got none.

The asymmetry is not in the CRD and the remedy must not be. Constraint 1 stands
— `Sandbox` is vendored and is not forked — and nothing here amends it. The gap
is that this ADR never said where a sandbox consumer is *permitted* to read
provisioning state from, so the only obvious answers are the ones ADR-047
forbids it: `Node` conditions, the `MachineDeployment`, the autoscaler's own
status, all cluster-scoped and all outside a Tier-2 namespaced grant.

**Burst placement MUST be observable from namespaced objects alone.** The
pending Pod's `PodScheduled` condition carries `reason: Unschedulable` and its
message, and `cluster-autoscaler` writes its `TriggeredScaleUp` /
`NotTriggerScaleUp` events onto that same Pod — in the tenant's own namespace.
Between them, "waiting on capacity" is already distinguishable from "failed to
start" without reading a single cluster-scoped object. That this signal exists
where a fleet may read it is now a **requirement on the platform**, not an
incidental property of the components chosen: any future change to the scaling
component must preserve a namespaced provisioning signal on the pending Pod.

Two obligations follow.

**On the platform:** the burst placement path must not require a fleet consumer
to read `Nodes`, `MachineDeployments`, or autoscaler state to learn that its pod
is waiting for capacity. The ADR-047 Tier-2 grant for a sandbox-consuming fleet
must therefore include `pods` and `events` read in its own namespace — which the
existing example grant, scoped to `agents.x-k8s.io/sandboxes` verbs alone, does
not cover.

**On the consumer:** a wait on a burst-placed pod MUST read that signal before
concluding failure, and MUST distinguish three outcomes that are not the same
condition and must not share a code path:

| Observation | Meaning | Correct response |
|---|---|---|
| Create rejected at admission | Fleet's priority-scoped `ResourceQuota` (§5) is full | Terminal. Do not retry, do not create. Surface the quota. |
| Pod exists, `PodScheduled=false`, scale-up event present | Capacity is being provisioned | Wait, up to the §11 p95 budget. Report progress. |
| Pod exists, `PodScheduled=false`, `NotTriggerScaleUp` | `maxNodes` reached, or no node group fits | Terminal for now. Surface the cell ceiling; do not delete and recreate. |

The first case is easy to mistake for the second and is the opposite of it: a
quota rejection means the pod was never created, so a consumer that treats it as
"still pending" waits out its full budget before reporting a condition that was
knowable at the create call.

This adds no component. It states where the signal lives, that the platform must
keep it there, and that a consumer must read it — which is what §7 already
promised the batch path and §8 left unsaid for the interactive one.

**Scope.** The two paths reach the same guarantee by different routes. On the
**job path** the controller already holds the projection responsibility §7
assigns it, so a fleet reads its own `EphemeralJob` status and needs no access
to the Pod at all. On the **`Sandbox` path** no such component exists — the
upstream controller projects nothing about capacity — so the consumer reads the
Pod and its Events directly, and the namespaced-signal requirement above is what
makes that possible without a cluster-scoped grant. Both learn that they are
waiting on capacity rather than broken, which is the guarantee; only the route
differs, and only the `Sandbox` path constrains the platform's choice of scaling
component.

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

**`EphemeralJob` as an XRD and composition** (Amendment 2). Attractive, and
rejected on volume. It would place the job path on the platform's established
tenant-abstraction pattern and introduce no new component, and for a job
population comparable to the tenant population it would be the right answer.
The population is not comparable: submission rate follows a fleet's concurrent
users, so job objects are numerous, short-lived and continuously turning over,
while Crossplane's costs — continuous reconciliation of composite and managed
resource, observed-state polling, finalizers on both — are proportional to
object lifetime. The pattern is correct for resources that outlive their
reconcile interval and inverted for a workload that does not.

**Fleet-created `batch/v1` Jobs placed by admission policy alone.** Rejected.
It introduces no component and no reconciliation cost, but requires the fleet to
hold `create` on Jobs in its own namespace, which is the authority that makes
burst placement escapable: a fleet holding it can author a Job without burst
placement and consume home-lab capacity. The guarantee would then rest entirely
on a validating policy being present and correctly scoped, which §4 establishes
as the weaker of the two available guarantees.

---

## Ownership

### Ownership under this ADR

Registered against the ADR-039 matrix in its canonical seven-column form:

| Resource Class | Generator | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|---|
| Burst Capacity Envelope | Platform | Git (`zero-ops`) | ArgoCD | Crossplane | cluster-autoscaler | Day-1+ |
| Burst Node Count | cluster-autoscaler | Kubernetes API (`MachineDeployment.spec.replicas`) | cluster-autoscaler | CAPI | Scheduler | Day-1+ |
| Burst Machine | CAPH | Provider API | CAPI | CAPH | Burst pods | Day-1+ |
| Burst Placement | Platform | Git (`zero-ops`) | ArgoCD | ephemeral-job-operator | Burst pods | Day-1+ |
| Burst Placement Policy (defence in depth) | Platform | Git (`zero-ops`) | ArgoCD | Kyverno | Burst pods | Day-1+ |
| Per-Fleet Burst Quota | Platform | Git (`fleet-registry`) | ArgoCD | kube-apiserver | Fleets | Day-1+ |
| Ephemeral Job Request | Fleet workload | Kubernetes API (fleet namespace) | Fleet | ephemeral-job-operator | Fleet workloads | Day-1+ |
| Sandbox Workload Pod | ephemeral-job-operator | Kubernetes API (fleet namespace) | Fleet | ephemeral-job-operator | Chat runtime | Day-1+ |
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
| Creating and owning the `batch/v1` Job for a request, including its placement fields | Creating, patching or computing `MachineDeployment` replicas |
| Projecting pod and node conditions into job status | Holding or reading provider credentials |
| Minting single-use, job-scoped completion tokens | Authoring placement *policy*, quota or `PriorityClass` |
| Enforcing job timeout and TTL cleanup | Secret generation, PKI operations |

Writing placement fields into a Job the controller owns is distinct from
authoring placement policy, which remains platform-declared in Git (§4).

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
- Placement on the job path is written by the component that owns the workload,
  so it does not depend on an admission policy being loaded and correctly scoped
  (§4).
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
  setting that is both cheap and fast. The obligation this places on consumers —
  a declared budget, deadlines matched to it, and no deletion on timeout — is
  normative in §11.
- **Idle interactive sandboxes pin nodes**, so burst cost tracks idle timeout
  rather than usage (§6). This is the dominant cost risk and it is a tuning
  problem, not a design flaw.
- **Scaling depends on hub reachability** (§3). Degraded rather than broken, but
  it is a real dependency for a tenant-facing capability.
- **Isolation is pod-level by default.** Workloads needing host privilege
  require a dedicated pool (§9), which is less efficient than shared nodes.
- Node churn adds CAPI/CAPH reconciliation load and cloud API calls that scale
  with burst frequency rather than with fleet count.
- **Admission rejection is the expected steady state, and this ADR provides no
  queue** (Amendment 2). Because submission rate follows a fleet's concurrent
  users while the priority-scoped `ResourceQuota` (§5) and `maxNodes` (§2) are
  fixed, demand routinely exceeds both. Both bounds reject rather than defer:
  the quota refuses the pod at admission and `maxNodes` refuses the node. At low
  volume a rejection is an error a submitter can surface; at the expected volume
  it is the common case, and a batch API whose common case is rejection is
  unusable without a queue and a submitter-visible position in it. Whether
  admission is synchronous or a submission may be accepted-and-deferred is a
  contract question this ADR does not settle and must be decided before the job
  path carries production load.

---

## Impact

- **ADR-046** gains a purpose for the burst `MachineDeployment` it defines at
  `replicas: 0`: it becomes the elastic tenant-execution pool, bounded by a
  `BurstCapacity` XR. The escape hatch becomes a product.
- **ADR-047** gains a platform capability consumed through Tier-2 RBAC and
  platform-rendered quota, and the §10 capacity rule constrains what a fleet may
  run on home-lab capacity. Per §12 the Tier-2 grant for a sandbox-consuming
  fleet must also carry namespaced `pods` and `events` read, without which burst
  placement is unobservable to the consumer that has to wait for it.
- **ADR-039** gains seven resource-class rows; **ADR-041** gains two controller
  entries (§Ownership).
- **ADR-005** records a bounded exception: `EphemeralJob` is a tenant-facing
  abstraction that is deliberately not an XRD and composition, because its
  object population is workload-shaped rather than infrastructure-shaped (§7,
  Amendment 2). The exception is scoped to this resource class and does not
  generalise.
- **ADR-036** gains burst size classes and warm-capacity settings in the
  provider capability contract.
- **ADR-043**, **ADR-014** and **ADR-005** are unaffected.
- Migration of any existing fleet-built provisioning implementation is tracked
  separately as an alignment plan, not in this ADR.
- **Amendment 2026-09-02.** The vendored `Sandbox` CRD is no longer the
  provisioning path for burst compute, so constraint 1 is satisfied more
  completely: the platform does not modify the upstream controller, it stops
  depending on it for placement. The mutating placement policy is retained as
  defence in depth and is no longer load-bearing. **waypoint ADR-031** is amended
  to submit through this ADR's `EphemeralJob` for both job and sandbox shapes,
  and no longer names a machine type — capacity is stated, the machine is
  derived.

## References

- ADR-046 §Topology — the burst `MachineDeployment` at `replicas: 0`
- ADR-046 §11 — placement classes
- `crossplane-capi-ownership-pattern` — why CAPI objects are wrapped in `provider-kubernetes` `Object`
- ADR-047 §Sandbox Workloads — the platform-capability contract this ADR extends to elastic capacity
- `manifests/spoke/spoke-catalog/infra/agent-sandbox/` — the upstream CRD that must not be forked
- `manifests/spoke/spoke-catalog/infra/sandbox-network-policy.yaml` — the egress control that keeps working unchanged

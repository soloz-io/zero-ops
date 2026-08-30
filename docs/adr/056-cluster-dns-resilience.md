# ADR-056: Cluster DNS Resilience

**Date:** 2026-08-31
**Status:** Accepted

## Context

Every workload in the platform depends on cluster DNS, and most depend on it before they can start. A resolver that answers unreliably does not present as a DNS fault: a query lost in transit is reported to the caller as a name that does not exist, which is indistinguishable from a missing Service.

Two properties of the platform make that reporting especially costly. Several components — the GitOps controller, the secret store, the certificate issuer, the identity tier — resolve a dependency exactly once at startup and treat failure as fatal, so an unlucky query costs a pod restart rather than a retry. And those same components are the ones everything else depends on, so their failures propagate rather than remaining local.

The default installation places the resolver poorly on any topology. The cluster bootstrap expresses resolver anti-affinity as a scheduling *preference* and tolerates the control-plane taint. A preference is a hint the scheduler may disregard, and placement is not re-evaluated for running pods, so an entire resolver tier can be co-located on one node and remain there indefinitely. Every pod on every other node then resolves across the network, and the loss of that one node removes name resolution cluster-wide.

Running a caching resolver on each node is established practice for clusters of any size or topology, for reasons that have nothing to do with any particular network: it removes per-query connection tracking pressure, it bounds resolution latency, and it decouples a pod's ability to resolve from the health of another node.

The hybrid topology (ADR-046) does not create these problems; it makes them acute and measurable. There the control plane sits in Hetzner and the workers on a home LAN, joined by a VXLAN overlay carried over Tailscale, and that link loses packets: resolving an in-cluster name from a workload pod succeeded on seven of eight attempts and failed on the eighth. With both resolver replicas co-located on the control-plane node, every query crossed that link.

The observable result was a manifest generation failure naming a Service whose pod was running, whose endpoint was ready, and whose ClusterIP was valid. The Application that depended on it stopped synchronising and remained so for thirty-five hours. During the same period, secret-store lookups timed out, an ACME DNS-01 challenge did not complete, an identity service exited fatally on a startup fetch, and database connections from init containers failed to resolve their host. Each was attributed to timing or to the component in which it surfaced. All are consistent with one lossy resolver path.

That the fault was diagnosable only on the hybrid topology is a property of the measurement, not of the design. A single-site cluster with a co-located resolver tier has the same structural exposure and merely a lower failure rate.

The platform already accommodates the hybrid link's constraints elsewhere: ADR-046 records an MTU reduction for the same overlay. What was missing is any equivalent accommodation for name resolution.

## Decision

### Name resolution does not depend on the network, on any topology

A DNS query issued by a pod is resolved on the node that pod runs on, without leaving the host, in the common case. This is a property the platform provides, not a behaviour it hopes for, and it holds for every provider and topology.

A caching resolver runs on every node and holds the node's own copy of cluster and external records. Pods resolve against it over a link-local address, which by definition cannot leave the host. The network is involved only when that cache misses.

This is applied uniformly rather than where a network is known to be poor. Conditioning it on measured loss would mean the platform behaves differently on the topology where the fault is hardest to observe, and would leave a single-site cluster carrying the same structural exposure with no indication of it.

### Cache misses reach the cluster resolver over TCP

Where a query must leave the node, it is forwarded over TCP rather than UDP. This is the difference between an imperfect network degrading latency and one fabricating negative answers: TCP retransmits a lost segment, whereas a lost UDP datagram is surfaced to the caller as a nonexistent name.

The cost is a connection per miss. It is accepted because the miss rate is low by construction — a cache exists to make it so — and because a wrong answer delivered quickly is worse than a correct answer delivered slowly.

### Resolver placement is a requirement, not a preference

Cluster resolver replicas may not share a node, expressed as a scheduling requirement rather than a preference. A preference permitted the entire resolver tier to be co-located on one node, and nothing subsequently moved it.

The replica count is not committed to configuration at all. It is a function of cluster size, and cluster size is an observed property that differs per cluster and changes as nodes are added, rebuilt or consolidated — so any number written down is wrong somewhere, and becomes wrong everywhere else over time.

Under a required placement constraint this is not merely untidy. A replica with no free node does not double up; it remains unschedulable. A count chosen for a large cluster therefore leaves permanently pending pods on a small one, which is a worse condition than the co-location that motivated constraining placement.

The count is instead derived at runtime from live node and core count, with a floor that keeps more than one replica whenever the cluster has more than one node, so that no single node's loss removes name resolution. Per-node coverage is not part of this calculation: that is the node-local cache's responsibility and it follows the cluster automatically.

This is a scheduling property only. The resolver's image and configuration remain owned by the cluster bootstrap that created them; this platform contributes placement and does not take ownership of what it does not need to change.

### The node-local cache is introduced before anything depends on it

The cache is deployed and confirmed answering on every node before any node is configured to use it. Ordering matters and is not incidental: a node told to resolve against an address where nothing listens cannot resolve anything at all, including the names required to diagnose the condition. Introduced in this order, the cache is inert until adopted and reversible at any point before that.

Adoption is a node-level configuration change, made per node, and therefore outside the reconciliation loop that manages workloads. It is a deliberate operation with its own verification, not a consequence of applying a manifest.

### Tolerating a network fault does not resolve it

The measures above make name resolution independent of the network's reliability. On the hybrid topology they do not repair the link that prompted their discovery. Every other flow crossing it — pod-to-pod traffic between sites, and any data-tier replication that later spans them — remains exposed, and the packet loss is a fault to be remediated in its own right.

Recording this explicitly prevents a predictable misreading: that because the symptom disappeared, the cause was addressed.

### Alternatives considered

Increasing the resolver replica count without constraining placement was rejected. The failure was not insufficient capacity; it was that pods reached a resolver through a virtual address that may select a replica on another node. More replicas leave that path unchanged.

Distributing resolver replicas without a node-local cache was rejected as insufficient for the same reason. It guarantees a resolver on each node but not that a pod reaches the one beside it.

Forwarding all cluster DNS over TCP without a cache was rejected as addressing the symptom at unacceptable cost. It would make every query, not merely a miss, pay for a connection across the link the design is trying to avoid.

Configuring each workload to retry name resolution was rejected as unenforceable. Retry behaviour would have to be correct in every component, including third-party ones the platform does not control, and several already treat a startup lookup failure as fatal.

## Ownership

Per ADR-039.

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Node-local DNS cache | Git | platform | ArgoCD | every pod | Day-1+ |
| Cluster resolver placement | Git | platform | ArgoCD | every pod | Day-1+ |
| Cluster resolver image and configuration | cluster bootstrap | cluster bootstrap | cluster bootstrap | every pod | Day-0 |
| Node resolver configuration | node configuration | platform operator | none — applied per node | kubelet | Day-1+ |

## Consequences

### Positive

- Name resolution no longer depends on the network being reliable, on any topology, so a class of failures that presented as missing Services, unresolvable secrets and unreachable databases is removed rather than reduced.
- Cache misses degrade latency instead of returning incorrect answers, which matters disproportionately for components that resolve once at startup and treat failure as fatal.
- Resolver placement is guaranteed by a scheduling requirement rather than left to a hint, so the co-location that caused this cannot recur.
- Query volume leaving each node falls to cache misses, which reduces connection-tracking pressure everywhere and, on the hybrid topology, load on a constrained link.
- The cluster resolver's image and configuration remain with the bootstrap that owns them; only placement is contributed here.

### Negative

- A cache introduces staleness bounded by its TTL, so a record that changes is observed late by up to that interval.
- A resolver now runs on every node, adding a small fixed cost per node and one more component in the critical path of every pod.
- Adoption requires a node-level change outside the reconciliation loop, so a node added later does not inherit it automatically and must be configured as part of provisioning.
- The platform becomes tolerant of a network fault, which reduces the pressure to fix it. On the hybrid topology the packet loss remains, and everything else crossing that link stays exposed.
- Forwarding misses over TCP costs a connection per miss, which is only acceptable while the miss rate stays low.

## Impact

- **Applies to every provider and topology.** The resolver runs on all nodes of all clusters, not only where a network is known to be imperfect. Single-site clusters carry the same structural exposure at a lower failure rate.
- **Extends ADR-046.** The hybrid topology already accommodates this link's constraints through an MTU reduction; name resolution now has an equivalent accommodation. The hybrid topology is where this fault became measurable, not where it is confined.
- **Introduces a Day-1 platform component** deployed to every node, and a corresponding node-level configuration step that provisioning must apply to any node joined subsequently.
- **Requires ordering during rollout.** The cache is confirmed answering on every node before any node is configured to use it.
- **Leaves the hybrid topology's packet loss open.** Remediating that path remains outstanding and is not superseded by this decision.
- **Reframes prior incident findings.** Intermittent failures previously attributed to timing in the secret store, the certificate issuer, the identity tier and database connectivity are consistent with this single cause, and should be re-examined against it rather than treated as independent.

## References

- ADR-035: Universal PKI Rule
- ADR-039: Platform Ownership Model
- ADR-040: Day-0 vs Day-1 Lifecycle Boundary
- ADR-041: Controller Responsibility Matrix
- ADR-043: Control Plane Authority Model
- ADR-046: Hybrid Topology

# ADR-085: The Cilium and Gateway API Upgrade

**Date:** 2026-09-21

**Status:** Proposed

*Constrained by: ADR-039 (Platform Ownership Model), ADR-041 (Controller Responsibility Matrix), ADR-046 (Hybrid Provider and Home Worker), ADR-048 (Two-Stage Cluster Lifecycle), ADR-051 (Environment DNS Naming and Public Gateway TLS), ADR-063 (A Released Bundle Reaches Into No Repository), ADR-071 (No Catalog), ADR-083 (Cross-Cluster Observability Federation), ADR-084 (Tenant Workload Deployment)*

> Shared infrastructure, no shared object ownership.

## Topology

A spoke, once this lands. Every workload cluster is identical.

```
                         the tenant's box
  ┌──────────────────────────────────────────────────────────────────────┐
  │                                                                      │
  │   SPOKE                                     Cilium 1.20              │
  │                                             Gateway API 1.6          │
  │   platform-ops                                                       │
  │   ┌────────────────────────────────────────────────────────────┐     │
  │   │  tenant-tls-gateway            :443   ← platform owns       │     │
  │   │    gatewayClassName: cilium                                 │     │
  │   │    allowedListeners: <namespace selector>                   │     │
  │   │                                                             │     │
  │   │  tenant-gateway                :80    ← ACME solver,        │     │
  │   │                                         plaintext redirect  │     │
  │   └───────────▲──────────────▲──────────────▲──────────────────┘     │
  │               │ attaches     │ attaches     │ attaches               │
  │   ┌───────────┴──┐  ┌────────┴─────┐  ┌─────┴──────────┐             │
  │   │ ListenerSet  │  │ ListenerSet  │  │ ListenerSet    │             │
  │   │ fleet A      │  │ fleet B      │  │ observability  │             │
  │   │  hostname    │  │  hostname    │  │  hostname      │             │
  │   │  certificate │  │  certificate │  │  certificate   │             │
  │   └──────┬───────┘  └──────┬───────┘  └──────┬─────────┘             │
  │          │                 │                 │                       │
  │     HTTPRoute         HTTPRoute         HTTPRoute                    │
  │          │                 │                 │                       │
  │          ▼                 ▼                 ▼                       │
  │   AgentGateway       AgentGateway       query endpoint               │
  │   (OIDC)             (OIDC)                                          │
  │          │                 │                                         │
  │          ▼                 ▼                                         │
  │   tenant-A workloads  tenant-B workloads                             │
  │                                                                      │
  │   CNI addon (ClusterResourceSet)                                     │
  │     Gateway API CRDs incl. ListenerSet  ──┐ delivered ahead of       │
  │     Cilium agent, operator, Envoy       ──┘ the operator             │
  │                                                                      │
  └──────────────────────────────────────────────────────────────────────┘
```

The platform owns the Gateway, the GatewayClass, the load balancer and the
policy admitting attachments. Each fleet and each platform capability owns one
ListenerSet carrying its own hostname and certificate. No object has two
writers, and a spoke serves as many fleets as it hosts.

## Context

ADR-051 requires that several independent consumers contribute listeners to one
shared Gateway without sharing write ownership of the Gateway object. Gateway
API defines ListenerSet for that delegation and has carried it in the standard
channel since v1.5.0; Cilium implements it from 1.20.

This platform runs Cilium 1.17.18 and delivers Gateway API 1.5.1. Cilium v1.20.2
pins Gateway API v1.6.1, so the CNI and the API bundle are one version decision.
The upgrade is therefore a prerequisite for an ownership model already decided
elsewhere, rather than a decision about ownership itself.

Two properties of this platform shape how the upgrade is performed.

Cilium supports upgrades only between consecutive minor releases, and states
that traffic through its user-space proxy — which is all Gateway API traffic —
is disrupted while one happens.

ADR-046 holds datapath decisions that take the form of values in a ConfigMap and
structural deltas in a rendered chart: an MTU that fits a VXLAN header inside
the tailnet's, tunnel routing rather than native, a host-network Envoy with a
hand-written bootstrap, and a certificate authority that must not be
regenerated. Upstream defaults differ from every one of them, and a chart
rendered from stock values restores those defaults silently.

## Decision

**The CNI and the Gateway API bundle are one versioned unit.** Cilium pins the
Gateway API version it supports; the two are rendered, delivered and validated
together, and neither moves alone.

**The platform runs Cilium 1.20 with Gateway API 1.6 and the ListenerSet type
delivered.** It is reached one minor release at a time, each hop validated on a
running cluster before the next begins.

**The CNI addon is generated from a values file.** That file is the source of
truth for the version and for every value this platform sets. A render procedure
produces the delivered artefact from it: render at the pinned version, apply the
structural deltas as declared patches, preserve the secrets that must not be
regenerated, and record a digest a gate compares against a fresh render. What is
committed is reproducible from what is declared.

**The structural deltas are declared, and the set is closed.** Each is a
decision the values file cannot express, or a hazard the render must guard:

| Delta | Why it is not a value |
|---|---|
| Host-network mangle guard DaemonSet and its ServiceAccount | Keeps Cilium's transparent socket filter from diverting load-balancer traffic into a loopback table (ADR-046 §8, §15) |
| Operator initContainer waiting on Gateway API CRDs | The operator blocks on CRDs delivered in the same ClusterResourceSet; ordering the chart cannot express |
| Operator replica count and recreate strategy | A single-node control plane cannot roll two operators |
| Agent DaemonSet without the Envoy socket volume | The Envoy is decoupled (ADR-046 addendum 10), so the agent does not host its socket |
| Envoy Service target port given numerically | A named port does not resolve against the decoupled DaemonSet |
| Envoy bootstrap configuration | Written for the discovery services the running Cilium serves |
| Hubble relay objects | Rendered by the chart from defaults this platform does not deliver |
| CNI certificate authority and Hubble server certificates | Regenerated by any render; preserved from the artefact being replaced, never produced |

**Every ADR-046 invariant is asserted before delivery.** Each datapath value is
checked by name and by value, per provider, with the ADR section cited in the
failure. A regenerated artefact is accepted because the assertion holds, not
because a diff was read.

**A key the running version ignores is resolved at the hop that begins reading
it.** Configuration keys that a newer Cilium introduces are decided together
with the artefacts they affect, at the hop where they become live, so an inert
line never becomes an instruction that contradicts what ships beside it.

**The ListenerSet type is delivered ahead of the operator.** Cilium detects it at
startup, so it travels in the ClusterResourceSet with the Gateway API CRDs,
before the operator that reads it.

### Alternatives considered

**A single upgrade to the target version.** Rejected: upstream tests and
supports only consecutive-minor upgrades, and this platform's datapath depends
on behaviour outside that testing.

**A hand-maintained rendered addon.** Rejected: an artefact of this size cannot
be reviewed as a diff, and the drift it hides is silent.

**Deferring the upgrade and delegating listeners another way.** Rejected in
ADR-051. Separate ports with a second load-balancer service encode a version gap
into the topology; a single Application rendering every listener centralises
what Gateway API distributes; an operator assembling every fleet's listeners
resolves the write conflict by making one component know every fleet's hostname
and certificate, which is the ownership model ADR-051 exists to prevent.

## Sequence

Four changes, each complete on its own, each leaving a cluster that serves.

| | Delivers | Established before the next begins |
|---|---|---|
| **0** | The addon is generated from the values file, at the version already running | The generated artefact differs from the committed one only where the upstream diff accounts for it, and the cluster is unchanged by adopting it |
| **1** | Cilium 1.18 | The datapath properties below, on a running cluster |
| **2** | Cilium 1.19 | The same properties |
| **3** | Cilium 1.20, Gateway API 1.6, the ListenerSet type | The same properties, and a ListenerSet attaching to the shared Gateway |

Step 0 carries no version change, which is what makes it the first step: the
render procedure is proven against a known-good artefact before it is trusted to
produce a new one. A generated artefact that reproduces what is already running
is evidence the procedure is faithful; one produced at a new version at the same
time proves nothing about either.

The ownership migration is not a fifth step in this ADR. Once the ListenerSet
type is served, ADR-051 governs how listeners move to it.

## Alignment with the reference model

kubefirst is this platform's reference for GitOps structure (ADR-071, ADR-084),
so both where this follows it and where it cannot are stated.

**The upgrade has no counterpart there.** kubefirst manages no CNI: its clusters
come from managed services and the provider owns the network plugin and its
lifecycle. This platform runs CAPI on Hetzner, where no managed CNI exists, so
it owns Cilium and owns upgrading it. The divergence is a property of the
infrastructure.

**The resulting ownership model is the same one.** What kubefirst obtains from a
single ingress controller aggregating many Ingress objects is that
infrastructure is shared while ownership is not — each application declares its
own hostname and TLS in its own object, and no two applications write to one
another's. ListenerSet expresses that in Gateway API terms: the parent Gateway
is infrastructure, each ListenerSet is one owner's object. Reaching it is the
reason the upgrade is worth three disruptive hops.

**One divergence is retained.** kubefirst places the Ingress in the
application's own chart, so an application declares the route reaching it.
ADR-051 assigns the hostname route to the platform, because where routing is a
security control ownership belongs with the control, and a tenant must not reach
its own backends without traversing the authenticating gateway. A fleet owns its
listener and its certificate; the route remains the platform's.

## Live-cluster validation

Each hop is proven on a running cluster before the next begins. Manifest
assertions are necessary and insufficient: the properties below are properties
of a datapath, and the failure modes they guard are ones where every Kubernetes
status reports success.

| Property demonstrated | Why a manifest check does not establish it |
|---|---|
| The delivered configuration carries the ADR-046 invariants | The ConfigMap is rendered per spoke at runtime; a correct base is not a correct delivery |
| Cross-node pod traffic passes at the MTU ceiling | Fragmentation is a property of packet size on a live path |
| The CNI certificate authority is unchanged | A rotated authority presents as working until a component reconnects |
| Each node's proxy serves the configuration it holds | A proxy holding stale state is indistinguishable through the API from one that is current |
| Each ListenerSet's hostname terminates TLS and reaches its backend | Every layer above the one that fails reports healthy |
| Two fleets serve public hostnames from one spoke simultaneously | This is the property the upgrade exists to obtain, and only a second fleet exercises it |
| Nodes join and reach Ready from cold | The cold-boot path is where the CNI deadlocks, and a running cluster never exercises it |

The first hop additionally establishes the render procedure: the artefact it
generates differs from the one it replaces only by the version and by what the
upstream diff accounts for.

## Ownership

Reference ADR-039 (Platform Ownership Model).

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Cilium version and values | Provider values file in Git | Platform | Render procedure | CNI addon artefact | Day-0 |
| Rendered CNI addon | Generated from the values file | Platform | Render procedure and drift gate | ClusterResourceSet | Day-0 |
| Gateway API CRD bundle | Vendored at the version Cilium pins | Platform | Render procedure | ClusterResourceSet, ArgoCD | Day-0, Day-1+ |
| ADR-046 datapath settings | Provider base ConfigMap in Git | Platform | hub-operator, per spoke | Cilium agent | Day-1+ |
| CNI certificate authority and Hubble server certificates | The running cluster | Platform | Preserved across renders | Cilium, Hubble | Day-0 |
| ListenerSet type | Gateway API bundle | Platform | ClusterResourceSet, ahead of the operator | Cilium operator | Day-0 |
| Gateway, GatewayClass, attachment policy | Platform manifests | Platform | ArgoCD | Fleets, platform capabilities | Day-1+ |
| A fleet's listener and certificate | The fleet's own ListenerSet | Fleet | ArgoCD | That fleet's routes | Day-1+ |

## Consequences

### Positive

A spoke serves as many fleets as it hosts, each owning its own hostname and
certificate, with the platform owning the Gateway and the policy that admits
them.

An upgrade is a version change and a review of an upstream diff, against an
artefact reproducible from what is declared.

The datapath decisions ADR-046 records are enforced mechanically.

### Negative

Three sequential upgrades, each disrupting Gateway API traffic while the proxy
restarts.

The structural deltas are carried as patches, and a patch that no longer applies
to a newer chart is a change requiring understanding rather than reapplication.

Until the upgrade completes, a spoke serves the public hostnames of one fleet,
and a spoke serving a fleet does not serve its observability query endpoint.

### Neutral

Generating the artefact removes transcription risk, not upgrade risk. The
datapath properties are properties of a running cluster and are established
there at each hop.

## Impact

Prerequisite for ADR-051, whose listener ownership model this makes
implementable.

Amends ADR-046 in mechanism, not in decision: its values are unchanged and are
now asserted by a validator named for it, and its addon artefact is generated
rather than hand-maintained.

Affects ADR-083, whose spoke query endpoint owns a ListenerSet on the same
footing as a fleet.

Does not alter ADR-063: the Gateway API bundle continues to be delivered in the
CNI's ClusterResourceSet, ahead of the operator that blocks on it.

Does not alter ADR-051's routing authority: listener and certificate ownership
sit with the fleet, the hostname route with the platform.

## References
- ADR-039: Platform Ownership Model
- ADR-040: Day-0 vs Day-1 Lifecycle Boundary
- ADR-041: Controller Responsibility Matrix
- ADR-043: Control Plane Authority Model
- ADR-046: Hybrid Provider and Home Worker
- ADR-048: Two-Stage Cluster Lifecycle
- ADR-051: Environment DNS Naming and Public Gateway TLS
- ADR-063: A Released Bundle Reaches Into No Repository
- ADR-071: No Catalog
- ADR-083: Cross-Cluster Observability Federation
- ADR-084: Tenant Workload Deployment

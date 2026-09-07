# ADR-066: The Control Plane Boundary

**Date:** 2026-09-07
**Status:** Proposed

## Context

ADR-065 places a control plane inside every tenant's box. It does not say what a control plane contains, and the platform currently draws no line: `manifests/hub-core-services/` holds thirty-one components reconciled uniformly, spanning cluster machinery, databases, an identity provider, a message bus, an analytics store, a mail server and a metering service.

Three facts bear on the boundary.

**The floor price of a box is the cost of running whatever the control plane is.** ADR-065 records that every tenant carries this cost and that reducing it is a standing constraint. A control plane defined as all thirty-one components could cost a startup more than the application it exists to run, which inverts the affordability the platform is for.

**A comparable platform draws the line at cluster machinery.** kubefirst's management cluster carries a GitOps engine, a secret store, certificate management, DNS, ingress, and Crossplane. It carries no database for its customers, no identity provider for their end users, and no analytics store. What it runs is what is required to declare and reconcile clusters.

**On-premises capacity is already part of the topology.** ADR-046 provisions home workers on an immutable operating system and ADR-052 bursts to cloud capacity above them. Maintenance effort on those nodes is a cost the platform bears or passes on, and it scales with how much state they are asked to hold.

## Decision

**The control plane is the machinery that declares, provisions, reconciles and secures. Everything else is a workload.**

### The test is functional, not organisational

A component belongs to the control plane if removing it stops clusters or workloads from being declared, provisioned, reconciled, secured or reached. A component that continues to serve its purpose while none of those activities occur is a workload.

By that test the control plane is the GitOps engine, the composition and cluster-lifecycle machinery, secret delivery, certificate issuance, DNS, ingress and gateway, admission policy, and node and capacity lifecycle. Databases, identity for a tenant's end users, messaging, analytics, mail, metering and a tenant's own applications are workloads.

**A workload authored by the platform is still a workload.** That the platform builds, tests and ships a component does not place it in the control plane, and the components most likely to be misclassified are the ones the platform happens to have written.

### Workloads are optional; the control plane is not

Every box runs a control plane, and it is the same one. Workloads are selected in the tenant's infrastructure repository ADR-062 establishes: a tenant runs the database it needs and does not carry the ones it does not.

This is what bounds the floor price. A box costs a control plane plus what the tenant chose, rather than a fixed thirty-one components regardless of use.

### The boundary is also the support boundary

Maintenance of the control plane is the product, and it is uniform across tenants because the control plane is uniform. Maintenance of a workload is a separate undertaking, because a workload's version, configuration and data are the tenant's.

Drawing the line once means a maintenance obligation is derivable rather than negotiated per component.

### On-premises capacity carries workloads, not control-plane state

Control-plane state is not placed on on-premises nodes. Those nodes carry workloads and burst capacity, so a node can be replaced without the ceremony that reconciling control-plane state would require, which is the property ADR-046's immutable operating system already assumes.

This bounds the maintenance a tenant's own hardware imposes: replacing a node is a capacity operation, not a control-plane operation.

### Alternatives considered

**Treat every platform-shipped component as control plane, as the tree does today.** Rejected. It sets the floor price of a box at the cost of thirty-one components, which contradicts the affordability ADR-065 records as a standing constraint, and it obliges the platform to maintain a tenant's mail server on the same terms as its GitOps engine.

**Let each tenant draw its own boundary.** Rejected. A support obligation that varies per tenant cannot be stated once, and the uniformity of the control plane is what makes a single tested bundle meaningful under ADR-063.

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Control plane composition | `zero-ops` | Platform | ArgoCD | Tenant control plane | Day-1+ |
| Workload selection | tenant infrastructure repository | Tenant | ArgoCD | Tenant | Day-1+ |
| Workload configuration and data | tenant | Tenant | ArgoCD | Tenant | Day-1+ |

See ADR-039 for the complete ownership matrix.

## Consequences

### Positive

The floor price of a box falls to the cost of cluster machinery, which is what makes a control plane per tenant affordable and removes the strongest argument for sharing one.

What maintenance covers is derivable from the boundary rather than negotiated per component and per tenant.

A tenant pays for the database it runs and not for six it does not.

On-premises nodes hold no control-plane state, so replacing one is a capacity operation. The maintenance burden of a tenant's own hardware stays bounded as that hardware grows.

### Negative

Components that serve both sides sit awkwardly. Observability watches the control plane and the workloads, and secret delivery is control plane while the store behind it need not be; each such component needs its side named rather than inferred.

A tenant that wants a database now selects and pays for one, where previously it appeared to arrive with the platform. This is a truthful accounting of a cost that already existed, but it will read as a reduction in what a box includes.

Workloads the platform ships but does not maintain by default are a support expectation that will be misread unless stated plainly at the boundary.

The classification will be argued for individual components, and the argument will recur whenever one is added.

## Impact

- **Amends ADR-065.** What ships into a box is the control plane as defined here, plus the workloads a tenant selects.
- **Amends ADR-063.** A bundle is the tested set of the control plane; workloads are versioned within it but are not what every box necessarily runs.
- **Confirms ADR-046 and ADR-052.** On-premises nodes carry workloads and burst capacity, which is the arrangement those ADRs already build.
- **Constrains ADR-013.** Observability spans the boundary, so its components are classified explicitly rather than by their location.

## References

- ADR-013: Hub-Spoke Observability Architecture with Dual Collection Patterns
- ADR-039: Platform Ownership Model
- ADR-046: Hybrid Provider Home Worker
- ADR-052: Elastic Burst Capacity for Tenant Workloads
- ADR-062: Repository Separation of Types, Instances and Workloads
- ADR-063: The Platform Bundle and its Version
- ADR-065: The Control Plane Ships Into the Box

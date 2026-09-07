# ADR-065: The Control Plane Ships Into the Box

**Date:** 2026-09-07
**Status:** Proposed

## Context

ADR-062 establishes that a tenant is a customer organisation holding one box, and that a spoke cluster belongs to exactly one tenant. It does not say where the control plane that reconciles those clusters runs, or who operates it.

Two arrangements are possible and the decision record does not choose between them. In the first, the platform operates a control plane and tenants are reconciled from it. In the second, each tenant's control plane runs inside that tenant's own box.

Three facts bear on the choice.

**The first arrangement creates a cross-tenant surface that does not otherwise exist.** ADR-062 removes cross-tenant isolation from the interior of a cluster by giving each cluster one tenant. A control plane serving several tenants reintroduces it at a single point holding every tenant's identity, secret material, cloud credentials and reconciliation authority. Nothing else in the platform would carry that property.

**A comparable platform resolves this by shipping the control plane.** kubefirst installs its API and console into each customer's own management cluster, with cluster-scoped authority granted there, and hosts only a stateless console that relays requests to whichever API the operator points it at. The vendor holds no customer credentials and operates no customer infrastructure. Its published upgrade path is to reinstall the CLI, so it obtains this property at the cost of having no propagation mechanism at all.

**The Day-0 path already produces a self-contained control plane.** ADR-040 defines Day-0 as a CLI-executed sequence run exactly once that establishes trust, identity and configuration foundations, after which controllers reconcile continuously. It already produces a control plane that does not depend on the platform to run.

## Decision

**Each tenant's control plane runs inside that tenant's own box, and the platform operates nothing.**

### The control plane is delivered, not hosted

A control plane is part of what a tenant receives. It runs in the tenant's cloud account, under credentials the tenant holds, with the authority it needs over that tenant's clusters and no authority anywhere else. The platform has no standing access to it.

The single authority per domain that ADR-043 requires is unchanged; what changes is that the authority for a tenant's domain resides in that tenant's box.

This is a statement about custody rather than about capability. What a tenant runs is the same bundle the platform tests and publishes, and it is not a reduced edition.

### Cloud credentials never leave the box

The credentials that provision a tenant's clusters are held by that tenant's control plane. No platform-held credential provisions tenant infrastructure, and no tenant credential is legible to the platform or to another tenant.

This makes the property ADR-062 asserts about clusters true of the material that creates them, and it removes the class of failure in which one compromised credential reaches more than one tenant's cloud account.

### The platform publishes; it does not reach in

The platform's output is a bundle at a tagged revision, as ADR-063 defines it. Delivery is publication. A tenant's control plane consumes what the platform publishes, and nothing the platform runs holds authority over a tenant's cluster.

Propagation therefore cannot be a push. ADR-064 records the mechanism by which a published bundle reaches a tenant's clusters without the platform holding access to them.

### A hosted interface, if offered, is a relay

An interface the platform hosts addresses a tenant's own control plane and holds no authoritative state. It is interchangeable with an interface the tenant runs, and losing it degrades presentation rather than reconciliation.

An interface that accumulated state, or held credentials for the control planes it addressed, would reintroduce the surface this ADR exists to remove.

### Alternatives considered

**A platform-operated control plane serving several tenants.** Deferred rather than rejected. It would concentrate every tenant's identity, secrets, cloud credentials and reconciliation authority in one place, which is a property nothing else in the platform has, and it is the only reason a cross-tenant isolation model would need to exist. It remains the arrangement that makes a control plane affordable to a tenant that cannot yet justify its own, and that trade is a separate decision from this one.

**Shipping the control plane and abandoning propagation, as kubefirst does.** Rejected. Maintaining the stack across a tenant's estate is the service the platform provides, and an arrangement in which a published fix never reaches a running cluster removes it.

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Tenant control plane | tenant's cloud account | Tenant | Day-0 CLI, then itself | Tenant | Day-0 |
| Cloud provider credentials | tenant's control plane | Tenant | ESO | Crossplane / CAPI | Day-1+ |
| Published bundle | `zero-ops` | Platform | Release workflow | Tenant control planes | Day-1+ |
| Hosted interface state | none | Platform | — | Tenant | Day-1+ |

See ADR-039 for the complete ownership matrix.

## Consequences

### Positive

The platform holds no tenant credential and no standing access to tenant infrastructure, so a compromise of the platform does not reach a tenant's cloud account.

Cross-tenant isolation ceases to be a property the platform must implement, because no component serves more than one tenant. The isolation model becomes an assertion about topology rather than a set of controls.

A tenant's box continues to reconcile when the platform is unreachable, which makes the independence the platform claims verifiable rather than contractual.

Exit is not a migration. A tenant that stops buying maintenance keeps a running control plane and a bundle at a known tag.

Day-0's existing sequence becomes a product surface rather than internal bootstrap tooling, and its quality bounds what a tenant can do unaided.

### Negative

Every tenant carries the cost of a full control plane, so the floor price of a box is the cost of running one. Reducing that floor becomes a standing engineering constraint rather than an optimisation.

The platform cannot observe a tenant's cluster directly, so support, incident response and defect reproduction all depend on what a tenant reports or chooses to share.

A defect in a published bundle cannot be corrected in place. Withdrawal is a publication and a promotion, bounded by each tenant's approval.

Control planes will run different bundle versions, and the platform supports the set of versions in the field rather than one.

## Impact

- **Amends ADR-062.** A tenant's control plane is part of its box and runs in its own cloud account. The repository model is unchanged in principle and changes in ownership, which that ADR records.
- **Constrains ADR-064.** Promotion cannot be a push from the platform, because the platform holds no access to the clusters being promoted.
- **Confirms ADR-063.** The bundle is what the platform delivers, and publication is the whole of delivery.
- **Confirms ADR-040.** Day-0 already produces a self-contained control plane; this ADR names that property as load-bearing rather than incidental.
- **Confirms ADR-031.** Secret isolation is bounded by a box, and no secret store serves more than one tenant.
- A platform-operated control plane shared by several tenants is out of scope here and is left to a separate ADR.

## References

- ADR-031: Tenant Secret Isolation and Identity Topology
- ADR-039: Platform Ownership Model
- ADR-040: Day-0 vs Day-1 Lifecycle Boundary
- ADR-043: Control Plane Authority Model
- ADR-062: Repository Separation of Types, Instances and Workloads
- ADR-063: The Platform Bundle and its Version
- ADR-064: Bundle Promotion and Cell-Scoped Policy

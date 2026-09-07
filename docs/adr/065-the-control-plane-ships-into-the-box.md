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

**Each tenant's control plane runs inside that tenant's own box, and the platform operates no tenant infrastructure.**

### The control plane is delivered, not hosted

A control plane is part of what a tenant receives. It runs in the tenant's cloud account, under credentials the tenant holds, with the authority it needs over that tenant's clusters and no authority anywhere else. The platform has no standing access to it.

The single authority per domain that ADR-043 requires is unchanged; what changes is that the authority for a tenant's domain resides in that tenant's box.

This is a statement about custody rather than about capability. What a tenant runs is the same control plane the platform tests and publishes, defined by ADR-066, and it is not a reduced edition.

### Cloud credentials never leave the box

The credentials that provision a tenant's clusters are held by that tenant's control plane. No platform-held credential provisions tenant infrastructure, and no tenant credential is legible to the platform or to another tenant.

This makes the property ADR-062 asserts about clusters true of the material that creates them, and it removes the class of failure in which one compromised credential reaches more than one tenant's cloud account.

### The platform's authority ends at a pull request

Maintaining the stack across a tenant's estate is the service the platform provides, so the platform does not stop at publishing. It proposes changes to a tenant's declarations, in the manner of a dependency-update bot: it reads the tenant's `<tenant>-gitops` repository, opens branches and pull requests against it, and does nothing else. Under ADR-063 what it proposes is a version, so a proposal carries no content of the platform's and touches no field the tenant writes.

That access is delegated by the tenant through the App installation ADR-062 already establishes, is scoped to repositories, and is revocable by the tenant at any time. Revoking it stops proposals arriving; it does not stop anything running.

The boundary is between proposing a change to a declaration and applying a change to infrastructure. The platform does the first and never the second. It holds no cloud credential, no cluster credential, and no secret material belonging to a tenant, and it cannot merge its own proposal. Every change reaches running infrastructure by the tenant's own control plane reconciling the tenant's own repository after the tenant has accepted it.

A tenant may configure its repository to accept qualifying proposals automatically. That is the tenant's automation acting under the tenant's rules, and the platform's authority is unchanged by it.

### A hosted interface, if offered, is a relay

An interface the platform hosts addresses a tenant's own control plane and holds no authoritative state. It is interchangeable with an interface the tenant runs, and losing it degrades presentation rather than reconciliation.

An interface that accumulated state, or held credentials for the control planes it addressed, would reintroduce the surface this ADR exists to remove.

### Alternatives considered

**A platform-operated control plane serving several tenants.** Deferred rather than rejected. It would concentrate every tenant's identity, secrets, cloud credentials and reconciliation authority in one place, which is a property nothing else in the platform has, and it is the only reason a cross-tenant isolation model would need to exist. It remains the arrangement that makes a control plane affordable to a tenant that cannot yet justify its own, and that trade is a separate decision from this one.

**Shipping the control plane and abandoning propagation, as kubefirst does.** Rejected. Maintaining the stack across a tenant's estate is the service the platform provides, and an arrangement in which a published fix never reaches a running cluster removes it.

**Having each tenant's control plane watch for published versions and propose its own upgrades.** Rejected. It would place the maintenance mechanism in every box, where a defect in it is present across the field and correctable only by the upgrade path it is itself responsible for. Proposing from outside keeps that mechanism in one place under the platform's control, while leaving acceptance with the tenant.

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Tenant control plane | tenant's cloud account | Tenant | Day-0 CLI, then itself | Tenant | Day-0 |
| Cloud provider credentials | tenant's control plane | Tenant | ESO | Crossplane / CAPI | Day-1+ |
| Published bundle | OCI registry | Platform | Release pipeline | Tenant control planes | Day-1+ |
| Hosted interface state | none | Platform | — | Tenant | Day-1+ |
| Maintenance proposals | `<tenant>-gitops` | Platform | Platform automation | Tenant | Day-1+ |
| Repository access grant | App installation | Tenant | — | Platform | Day-1+ |

See ADR-039 for the complete ownership matrix.

## Consequences

### Positive

The platform holds no cloud, cluster or secret credential belonging to a tenant, so a compromise of the platform reaches proposals in a repository and no running infrastructure anywhere.

Cross-tenant isolation ceases to be a property the platform must implement, because no component serves more than one tenant. The isolation model becomes an assertion about topology rather than a set of controls.

A tenant's box continues to reconcile when the platform is unreachable, which makes the independence the platform claims verifiable rather than contractual.

Exit is not a migration. A tenant that stops buying maintenance revokes an App installation and keeps a running control plane and a bundle at a known version, which ADR-063 requires it to be able to mirror. Nothing is withdrawn from it, because nothing was ever held on its behalf.

Day-0's existing sequence becomes a product surface rather than internal bootstrap tooling, and its quality bounds what a tenant can do unaided.

### Negative

Every tenant carries the cost of a full control plane, so the floor price of a box is the cost of running one. Reducing that floor becomes a standing engineering constraint rather than an optimisation.

The platform cannot observe a tenant's cluster directly, so support, incident response and defect reproduction depend on what the box exports. A proposal can be correct against the declarations and still fail against a cluster the platform cannot see. ADR-067 records the egress-only telemetry this requires and bounds a maintenance claim by it.

A defect in a published bundle cannot be corrected in place. Withdrawal is a publication and a proposal, bounded by each tenant's approval, and a tenant that has revoked access receives neither.

Control planes will run different bundle versions, and the platform supports the set of versions in the field rather than one.

## Impact

- **Amends ADR-062.** A tenant's control plane is part of its box and runs in its own cloud account. The repository model is unchanged in principle and changes in ownership, which that ADR records.
- **Constrains ADR-064.** A promotion is a proposal the platform raises against a tenant's repository, never an action against a tenant's cluster.
- **Confirms ADR-063.** The bundle is what the platform delivers. Publishing a tag begins a release; proposing it to each tenant completes one.
- **Confirms ADR-040.** Day-0 already produces a self-contained control plane; this ADR names that property as load-bearing rather than incidental.
- **Confirms ADR-031.** Secret isolation is bounded by a box, and no secret store serves more than one tenant.
- **Constrains ADR-067.** Evidence for support crosses outward under a tenant's grant; no platform component initiates a connection into a box.
- A platform-operated control plane shared by several tenants is out of scope here and is left to a separate ADR.

## References

- ADR-031: Tenant Secret Isolation and Identity Topology
- ADR-039: Platform Ownership Model
- ADR-040: Day-0 vs Day-1 Lifecycle Boundary
- ADR-043: Control Plane Authority Model
- ADR-062: Onboarding and Scaffolding a Tenant
- ADR-063: The Platform Bundle and its Version
- ADR-064: Bundle Promotion and Cell-Scoped Policy
- ADR-066: The Control Plane Boundary
- ADR-067: Support Telemetry and the Basis of Maintenance

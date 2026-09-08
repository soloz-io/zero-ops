# ADR-070: The Minimum Supported Box

**Date:** 2026-09-08
**Status:** Proposed

## Context

ADR-065 gives every tenant its own control plane, and ADR-066 places the capabilities a tenant selects on the platform's side of the boundary. Together they decide what a box costs to run, and neither states what that cost may be.

Three facts make it a decision rather than an observation.

**The tenant is chosen for not being able to staff a platform team.** The arrangement exists for organisations that want a golden path and cannot justify the people to build one. An architecture whose smallest instance costs more than the engineer it replaces has no one to sell to, and the failure is commercial rather than technical.

**An earlier version of ADR-066 claimed the cost was bounded and was wrong.** It said the floor price was cluster machinery plus what a tenant chose, which was only true because it had placed databases and identity outside the platform. With those inside, the bound is gone and nothing replaces it.

**The evidence to date is insufficient.** Declared resource requests across `manifests/hub-core-services/` total under one CPU and 1.3 GiB, which undercounts badly: most components arrive as Helm charts whose requests are in chart defaults rather than in this repository. No measurement of a running box has been taken.

## Decision

**A production-capable minimum box must fit within €150–400 per month, and the support contract is not weakened to make it fit.**

### The topology is decided first, and the cost is measured against it

What a capability requires to be supportable is settled on its own terms: whether a database can be operated responsibly with one replica, whether an identity provider can lose its node without losing sessions, what a backup and a restore actually need. That answer comes from the obligation ADR-066 and ADR-069 create, not from a target.

The cost of the resulting topology is then measured. The target above is what that measurement is checked against; it is not an input to the topology.

### Reducing replicas to reach the target is refused

A capability whose supported topology is HA is not shipped single-replica to fit a number. The platform would then be promising maintenance for a configuration it knows cannot meet the promise, and the failure would arrive as a tenant's outage rather than as a missed target.

Where a capability can be supported in more than one topology, offering both is legitimate and the tenant selects. Offering only the cheaper one, while promising what the more expensive one delivers, is not.

### The number is a constraint to validate, not an established fact

The range above is a target the architecture must satisfy and has not yet been shown to satisfy. Until a box is built and measured it stays a constraint, and no other decision may rest on it as though it were measured.

The measurement is: bootstrap one box at minimum supported topology, and record the cost of its cluster machinery, its mandatory capabilities, storage, observability, artefact mirroring, and whatever HA the topology requires.

### Exceeding the target is a business decision, not an architectural one

If a supportable minimum box costs materially more than this range, the architecture is not what changes. What changes is who the platform is for, or what it charges, or which capabilities are mandatory.

Recording it this way is the point of a separate decision: an economic target inside the boundary ADR would eventually be met by moving the boundary, which is how the earlier version of ADR-066 came to place databases outside the platform.

### Alternatives considered

**Put the target in ADR-066.** Rejected. A cost target in the same decision that defines what the platform maintains invites the boundary to move whenever the target is missed, and that is exactly what happened once already.

**Set no target.** Rejected. The floor price is the most likely reason the arrangement fails for its intended tenant, and an architecture with no stated economic constraint cannot be checked against the customer it is for.

**Commit to a number now.** Rejected. The available evidence undercounts the footprint by a wide margin, and a number asserted from it would be quoted back as measured.

## Ownership

This ADR defines an economic constraint on architectural choices and does not own platform resources. For resource ownership, see ADR-039.

## Consequences

### Positive

The cost of the arrangement is a stated constraint that can be checked, rather than an assumption discovered when a prospect declines.

Support obligations are settled before cost, so a topology is never chosen by a target and then promised as though it were chosen by the obligation.

A missed target is answered as a business question, and the boundary ADR-066 draws is insulated from the pressure that already moved it once.

### Negative

The target is unvalidated, and every plan resting on the arrangement being affordable rests on something not yet measured.

Refusing to weaken topology for cost means some capabilities will be expensive, and a tenant may face a floor set by a capability it barely uses. Making that capability optional is the available answer and is not always the right one.

A per-tenant control plane is structurally more expensive than a shared one. This decision constrains that cost; it does not remove the difference, and a shared control plane remains the alternative deliberately deferred in ADR-065.

## Impact

- **Constrains ADR-065 and ADR-066.** The per-tenant control plane and the capabilities inside the boundary are what the cost is measured over.
- **Confirms ADR-069.** Support obligations decide the supported topology; this decision does not permit a cheaper one to be promised as though it were supported.
- No change to the bundle, its promotion, the evidence behind support, or how a build declares its version.

## References

- ADR-039: Platform Ownership Model
- ADR-065: The Control Plane Ships Into the Box
- ADR-066: The Platform Boundary
- ADR-069: The Maintenance Promise

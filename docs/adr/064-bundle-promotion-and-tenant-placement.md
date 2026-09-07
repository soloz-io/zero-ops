# ADR-064: Bundle Promotion and Tenant Placement

**Date:** 2026-09-07
**Status:** Proposed

## Context

ADR-063 defines the bundle and states that its version is a tag. It does not say where that tag is recorded, how it reaches a running cluster, or how it is withdrawn when the bundle is defective. A version that names a tested set but has no sanctioned path onto a cluster is a label, not a release mechanism.

Five facts about the current arrangement bear on this decision.

**The bundle version is not in Git.** `environmentRevision` is a parameter of the seed Application, which `internal/hub-cli/bootstrap/seed.go` renders and applies during Day-0. No manifest declares that Application. There is therefore no file to raise a pull request against, no reviewer, and nothing to revert. Advancing the version on a running cluster means re-running the CLI, which ADR-040 lists among the Day-1+ **Forbidden** operations as "CLI execution for any operational purpose", and it contradicts ADR-055, which requires that "no environment value is supplied at render time from outside Git."

**One value selects every cell.** `environmentRevision` is a single chart value consumed at eighteen references across nine boundary templates. Every cell a Hub reconciles moves together, so there is no way to advance one cell, observe it, and then advance the rest.

**Placement is already a secret-path segment.** `cellId` reaches Infisical directly — `/spoke-pool/{{ .Values.cellId }}/tenants/{{ $t }}/OIDC_CLIENT_ID` in the tenant chart — and ADR-031 scopes each cell's Machine Identity to exactly `/spoke-pool/<cell-id>/tenants/*`. A tenant-writable `cellId` is therefore a tenant-writable secret path, which is the redirection ADR-047 prohibits and the isolation ADR-031 depends on.

**The per-cell selection mechanism already exists.** `cell-id` is a label on the ArgoCD cluster Secret, and boundaries 05 and 06 already resolve a tenant to its cell through a matrix generator that matches on it. Boundary 03 selects `spoke-type: pool`, so a silo cell would today receive no platform services at all.

**Topology is already non-discretionary for some tenants.** ADR-031's amendment makes the silo model mandatory for PCI, HIPAA and SOC2 tenants and forbids operators from scheduling regulated workloads onto pools. Topology is a field whose permitted value is constrained by the tenant that occupies the cell.

## Decision

**The cluster instance is the unit of maintenance.**

A cell is declared once, in `fleet-registry`, and that declaration carries everything that distinguishes it: its region, its topology, the bundle version it runs, and who may approve a change to it. Provisioning a cell and upgrading a cell are the same act performed against the same record.

### The bundle version is pinned per cell

Each cluster instance declares the bundle version its cell runs. That declaration is the System of Record for what the cell is running, in place of the render-time parameter.

Day-0 seeds a cell's initial version from the default carried in `zero-ops`, which preserves ADR-040 exactly: Day-0 still selects the first bundle, and thereafter never acts. What changes is that the value is written into Git as part of declaring the cell, rather than surviving only as a field of a cluster object nothing declares.

The version is published to the cluster Secret as a label and read by the same generators that already read `cell-id`. No new resolution mechanism is introduced; the value that is hub-wide today becomes per-cell through the selector that already exists.

### Promotion is a pull request

ADR-037 governs promotion and is adopted without modification. A bundle version advances through ephemeral, dev, stg and prod in order; a promotion workflow verifies that the version reconciled and passed validation in the preceding environment before it may be proposed for the next; production carries Environment Protection Rules and CODEOWNERS; and rollback is a `git revert` of the field, never a cluster mutation.

The platform opens every promotion pull request. Central propagation is preserved in full: a platform fix becomes one automated pull request per cell, authored by the platform, not one action per tenant.

### Approval is a property of the cell

Each cluster instance declares whether promotion to it is automatically approved. The gate belongs to whoever bears the blast radius.

On a silo the occupant is one tenant, so the tenant may hold the gate, and a tenant that opts into automatic approval gets an end-to-end automated path with no human step. On a pool the cell carries many tenants — the hybrid dev pool declares `maxTenantCapacity: 20` — so no single occupant can hold it, and the platform approves. This is a value on the record, not a difference in procedure.

Automatic approval waives review. It does not waive validation: the checks below run on every promotion regardless of who approves it or whether anyone does.

### Region is declared, placement is derived

A tenant declares the region it requires and the compliance class it falls under. Both are business facts about the tenant and belong with the tenant, in `tenant-registry`.

The platform derives `cellId` from them. This is the pattern the platform already applies to burst capacity under ADR-052 — a request, not a grant, where the platform decides what the request means — and it is what ADR-062 already records when it calls the tenant's cell **assigned**.

The distinction is load-bearing rather than procedural. If cells are regional because law differs by region, a tenant-writable `cellId` would let a tenant bound by one jurisdiction place itself under another, through a change indistinguishable from ordinary configuration. The control that enforces residency cannot be a field held by the party residency binds.

### Topology is a field, not a fork

Pool and silo are values of `topology` on one declaration format, reconciled by one provisioning path and advanced by one promotion path. They differ in what the platform derives from them — the scope of the cell's Machine Identity under ADR-031, its tenant capacity, and its default approver — and in nothing else.

Boundary 03's `spoke-type: pool` selector is a consequence of pool being the only topology yet instantiated. Platform services are owed to every cell, and the selector is narrowed to pools only where a Composition genuinely differs.

### The constraints are validators

Every rule above is asserted mechanically, in the style ADR-063 establishes for version couplings, and a promotion that violates one fails its pull request rather than reaching a cell:

- A tenant in a regulated compliance class resolves only to a cell whose topology is `silo` (ADR-031).
- A tenant's declared region equals the region of its assigned cell.
- A declared bundle version exists as a tag of `zero-ops`.
- A cell's version advances only to one that has reconciled and passed validation in the preceding environment (ADR-037).

### Alternatives considered

**Retain one hub-wide `environmentRevision`.** Rejected. It makes staged rollout inexpressible: the blast radius of any bundle change is every cell the Hub reconciles, and the first cluster to run a bundle is also the last. It is also the arrangement that has no promotion path at all, for the reasons in the Context.

**Let tenants write `cellId` directly.** Rejected. It is a tenant-writable secret path under ADR-031's path IAM, and under a regional cell model it would make data residency a tenant-editable field. The requirement behind the proposal — that placement follows from something the tenant declares — is met by deriving the cell from a declared region and compliance class.

**Copy the platform template into each tenant's own organisation, as kubefirst does.** Rejected, on the ground that distinguishes the two systems. kubefirst's recipient operates the cluster it receives, so a copy it owns is correct. Here the platform operates it, and a copy in each tenant's organisation would end central propagation: a platform fix would land only when each tenant acted on it. Per-cell pinning delivers the independent version this ADR needs without moving the declaration out of the platform's control.

**Separate provisioning and upgrade paths for pool and silo.** Rejected. Two paths would diverge, and the topology that diverges least tested is the one carrying regulated tenants.

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Bundle version per cell | `fleet-registry` cluster instance | Platform | ArgoCD | Boundary ApplicationSets | Day-1+ |
| Initial bundle version | `zero-ops` default | Platform | Day-0 CLI | Cluster instance | Day-0 |
| Cell topology, region, approval mode | `fleet-registry` cluster instance | Platform | Crossplane / CAPI | Platform | Day-1+ |
| Declared region and compliance class | `tenant-registry` | Platform | — | Placement derivation | Day-1+ |
| Derived placement (`cellId`) | `tenant-registry` | Platform | Crossplane | Tenant ApplicationSets | Day-1+ |

See ADR-039 for the complete ownership matrix.

## Consequences

### Positive

A bundle reaches one cell before it reaches the rest, so a defect is observed on a cluster that was chosen to observe it rather than on all of them at once.

The bundle version acquires the properties ADR-037 already provides to every other environment-differentiated value: review, a verification gate, an audit trail, and rollback by revert. None of it is newly invented.

Advancing a cell no longer requires the CLI, which removes the last operational reason to run it after Day-0 and closes the ADR-040 conflict rather than tolerating it.

Placement is derived from what a tenant declares, so a tenant states a jurisdiction and a compliance class — facts it actually holds — instead of naming infrastructure, and neither statement grants it access to another cell's secrets.

Pool and silo stop being two systems. A tenant moving from pool to silo for compliance changes a field.

### Negative

Cells will run different bundle versions simultaneously, and the set of versions in production is a support matrix the platform did not previously have to reason about. A defect reproduces only against the cells that reached the version.

Propagation of a security fix is bounded by the slowest approver, which is the cost of the gate. Cells on automatic approval are unaffected; a cell that holds the gate holds the exposure with it, and that is the trade the gate exists to offer.

One pull request per cell means promotion volume scales with the fleet, and the workflow that opens them becomes platform infrastructure with its own failure modes.

Deriving placement means the platform must resolve a region and a compliance class to a cell that exists, and must fail an onboarding intelligibly when no such cell does. Declaring the cell is a prerequisite of onboarding a tenant into a new region.

Boundary 03's cluster selector and the tenant-facing values that carry `cellId` today are both written for the current arrangement and must be reworked before a silo can be provisioned.

## Impact

- **Amends ADR-063.** The bundle version is recorded on the cluster instance in `fleet-registry` and pinned per cell, rather than being a single value selected for a Hub at Day-0. The definition of a bundle, the requirement that component versions are declared once, and the rejection of latest-as-a-target are unchanged.
- **Amends ADR-055.** `environmentRevision` becomes an environment value resident in Git, which is what that ADR already requires of every other value the seed Application consumes.
- **Confirms ADR-037.** Its promotion path, verification gate, protection rules and revert-based rollback are adopted for the bundle version without modification.
- **Confirms ADR-062.** The cluster instance is the record this ADR extends, and placement is the assignment that ADR already locates in `tenant-registry`.
- **Confirms ADR-031.** The pool and silo models and the path-scoped Machine Identity are unchanged; this ADR states how a tenant reaches the correct one and asserts it mechanically.
- **Confirms ADR-040.** Day-0 still selects the first bundle version and still acts exactly once. Removing the CLI from the upgrade path narrows Day-1+ CLI execution to nothing, which is what that ADR already requires.
- No change to ADR-021, ADR-042, ADR-047, ADR-052 or ADR-061. Boundary composition, bootstrap phases, the tenant contract and the request-not-a-grant pattern are all applied here as they stand.

## References

- ADR-021: Boundary-Driven GitOps and Day-0 Choreography
- ADR-031: Tenant Secret Isolation and Identity Topology
- ADR-037: Directory-Based Environment Promotion and Gating
- ADR-039: Platform Ownership Model
- ADR-040: Day-0 vs Day-1 Lifecycle Boundary
- ADR-042: Bootstrap State Machine
- ADR-047: Fleet Tenant Deployment Contract
- ADR-052: Elastic Burst Capacity for Tenant Workloads
- ADR-055: Boundary Activation as the Day-0 Gating Mechanism
- ADR-061: Component Descriptors for Boundary Composition
- ADR-062: Repository Separation of Types, Instances and Workloads
- ADR-063: The Platform Bundle and its Version

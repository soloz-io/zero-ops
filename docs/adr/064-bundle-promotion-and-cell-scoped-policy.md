# ADR-064: Bundle Promotion and Cell-Scoped Policy

**Date:** 2026-09-07
**Status:** Proposed

## Context

ADR-063 defines the bundle and states that its version is a tag. It does not say where that tag is recorded, how it reaches a running cluster, or how it is withdrawn when the bundle is defective. A version that names a tested set but has no sanctioned path onto a cluster is a label, not a release mechanism.

ADR-062 establishes that a spoke cluster belongs to exactly one tenant, that a cluster instance is the record of it, and that a cell is a dynamic grouping of one tenant's clusters for attaching policy. Four facts about the current arrangement bear on this decision.

**The bundle version is not in Git.** `environmentRevision` is a parameter of the seed Application, which the Day-0 CLI renders and applies. No manifest declares that Application. There is therefore no file to raise a pull request against, no reviewer, and nothing to revert. Advancing the version on a running cluster means re-running the CLI, which ADR-040 lists among the Day-1+ forbidden operations, and it contradicts ADR-055, which requires that no environment value be supplied at render time from outside Git.

**The platform holds no access to the clusters it maintains.** ADR-065 places each tenant's control plane inside that tenant's box, so any promotion mechanism that assumes the platform can act on a tenant's cluster or repository is unavailable.

**One value selects every cluster.** `environmentRevision` is a single chart value consumed at eighteen references across nine boundary templates. Every cluster a control plane reconciles moves together, so there is no way to advance one, observe it, and then advance the rest. Under ADR-062 those clusters belong to different tenants.

**Policy reaches every cluster uniformly.** The spoke catalogue, including its admission policies, is delivered by a cluster generator selecting a single label common to all spokes. Nothing expresses a policy that applies to some of a tenant's clusters and not others, which is what a cell exists to express.

**The selection mechanism already exists.** `cell-id` is a label on the ArgoCD cluster Secret, and two boundaries already resolve clusters through a matrix generator that matches on it. Cell-scoped delivery is a selector, not new machinery.

## Decision

**The cluster instance is the unit of maintenance. The cell is the unit of policy.**

### The bundle version is pinned per cluster

Each cluster instance declares the published bundle version it runs, and that declaration is the System of Record for what the cluster is running, in place of the render-time parameter. A cluster consumes the bundle by naming a version and supplying values, so the declaration is a version and the values beside it, and never the bundle's content.

Day-0 seeds a cluster's initial version from the version its own build carries (ADR-068), which preserves ADR-040: Day-0 still selects the first bundle and thereafter never acts. What changes is that the value is written into Git as part of declaring the cluster, rather than surviving only as a field of a cluster object nothing declares.

The version is published to the ArgoCD cluster Secret as a label and read by the same generators that already read `cell-id`. The value that is control-plane-wide today becomes per-cluster through a selector that already exists.

### Policy is attached to cells, not to clusters

A tenant declares cells and which of its clusters belong to them. Policy is bound to a cell and delivered to the clusters that match it. A cluster matching several cells receives the union.

A cell holds no compute, pins no version, and owns no cluster. It is a selector over clusters a tenant already owns, so grouping is tenant-authored configuration with no cross-tenant consequence. Cluster identity — region, provider, node pool, bundle version — is not tenant-authored, because ADR-031 scopes secret paths by cluster identity.

Residency follows from this without a special case. Region is a property of a cluster. A cell may carry a residency policy, and a validator asserts that every cluster matching the cell satisfies it.

### Promotion is a pull request the platform raises against the tenant's repository

ADR-037 governs promotion and is adopted without modification. A bundle version advances through the environments in order; a promotion workflow verifies that the version reconciled and passed validation in the preceding environment before it may be proposed for the next; production carries protection rules and code ownership; and rollback is a revert of the field rather than a cluster mutation.

Under ADR-065 the platform proposes and does not apply. When a bundle version is published, the platform opens a pull request against each subscribing tenant's `<tenant>-gitops` repository changing the pinned version on the clusters that are due it. Because ADR-063 publishes the bundle as a chart, that change is one field per cluster and carries none of the bundle's content. The change reaches infrastructure only when the pull request is accepted and the tenant's own control plane reconciles the result.

A promotion never writes a values file. Values are the tenant's, defaults belong to the chart, and a version that required a values change to be usable would be a version that could not be proposed without arbitration.

This is the shape of a dependency-update bot, and the properties that make that arrangement work carry over: the proposal is legible before it is accepted, it is declined by closing it, the access that produces it is granted by the tenant and revocable, and a tenant may configure its repository to accept qualifying proposals automatically. The platform maintains the estate without holding a credential that can change anything running in it.

### A proposal resolves the versions it carries

A promotion pull request records the component versions the bundle version resolves to, alongside the version itself. The version remains the unit of change and the thing that is promoted; the resolved set is written beside it so that a proposal states what moves.

Two properties follow. A promotion becomes reviewable on its own terms: the reviewer sees which components move and by how much, rather than one version advancing to another with no visible content. And a tenant's repository becomes legible without resolving a chart, which is the property kubefirst obtains by copying content into each instance — obtained here without the copy, so the platform and the tenant still write to disjoint fields.

The resolved set is an output of the promotion, not an input to reconciliation. What a cluster runs is determined by the version; a recorded set that disagrees with the version it accompanies is a defect a validator rejects, not an override.

### Approval is a property of the cluster instance

Each cluster instance declares whether promotion to it is automatically approved. Because a cluster belongs to exactly one tenant and the pull request is raised in that tenant's own repository, the gate is the tenant's throughout; a tenant that opts into automatic approval obtains an end-to-end automated path with no human step.

Automatic approval waives review. It does not waive validation.

### The constraints are validators

Each rule is asserted mechanically, in the style ADR-063 establishes for version couplings, and a promotion or grouping that violates one fails its pull request rather than reaching a cluster:

- Every cluster matching a cell satisfies that cell's policy, including residency.
- A declared bundle version exists as a published chart.
- A cluster advances only to a version that has reconciled and passed validation in the preceding environment.
- A cluster instance names exactly one owning tenant.
- A proposal carries a pre-flight verdict from the cluster it targets, or is raised as unverified (ADR-067).
- The component versions a proposal records resolve from the bundle version it proposes.
- A proposed bundle version exists as a published chart before the proposal is opened.

### Alternatives considered

**Retain one control-plane-wide `environmentRevision`.** Rejected. Staged rollout becomes inexpressible and the first cluster to run a bundle is also the last. It is also the arrangement that has no promotion path at all.

**Have the platform apply promotions directly to tenant clusters.** Rejected under ADR-065. It would require the platform to hold a credential that changes running infrastructure, where opening a pull request requires only repository access the tenant grants and can revoke, and it would remove the tenant's opportunity to decline a change before it takes effect.

**Have each tenant's control plane propose its own promotions.** Rejected. It places the maintenance mechanism inside every box, so a defect in it is present across the field and correctable only through the path it is itself responsible for.

**Pin the bundle version on the cell rather than the cluster.** Rejected. A cell is a dynamic grouping and a cluster may match several, so a version pinned there is either ambiguous or forces cells to be disjoint, which would make them a partition rather than a selector and remove the property that motivates them.

**Attach policy to clusters directly.** Rejected. It reproduces on every cluster what a tenant states once, and a policy that must hold across a set becomes a rule with no single place to assert it.

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Bundle version per cluster | `<tenant>-gitops` | Platform | ArgoCD | Tenant control plane | Day-1+ |
| Initial bundle version | `zero-ops` default | Platform | Day-0 CLI | Cluster instance | Day-0 |
| Cluster values | `<tenant>-gitops` | Tenant | ArgoCD | Bundle chart | Day-1+ |
| Cluster identity and approval mode | `<tenant>-gitops` | Tenant | Crossplane / CAPI | Tenant control plane | Day-1+ |
| Cell definitions and policy binding | `<tenant>-gitops` | Tenant | ArgoCD | Spoke clusters | Day-1+ |
| Published bundle tags | `zero-ops` | Platform | Release workflow | Tenant control planes | Day-1+ |
| Promotion proposals | `<tenant>-gitops` | Platform | Platform automation | Tenant | Day-1+ |
| Resolved component versions | `<tenant>-gitops` | Platform | Platform automation | Tenant | Day-1+ |

See ADR-039 for the complete ownership matrix.

## Consequences

### Positive

A bundle reaches one cluster before the rest, so a defect is observed where it was chosen to be observed rather than everywhere at once. Because clusters belong to one tenant each, a defective promotion is bounded to that tenant.

The platform maintains a tenant's estate while holding no credential that can change anything running in it. Propagation and custody stop being in tension: a proposal is the whole of the platform's reach.

The bundle version acquires the properties ADR-037 already provides to every environment-differentiated value: review, a verification gate, an audit trail, and rollback by revert.

Advancing a cluster no longer requires the CLI, which removes the last operational reason to run it after Day-0.

A tenant expresses a policy boundary once and applies it to any subset of its clusters, including subsets that change, without the platform holding a list.

A promotion is legible as a set of component moves rather than as one opaque tag, and a tenant's repository states what its estate runs without reference to anything the platform hosts.

Residency stops being a special case. It is a cell policy asserted against a cluster property.

### Negative

Clusters will run different bundle versions simultaneously, and the set in production becomes a support matrix the platform did not previously have to reason about.

Propagation of a security fix is bounded by the slowest approver. Clusters on automatic approval are unaffected; a tenant that holds the gate holds the exposure with it.

One pull request per cluster means promotion volume scales with the estate, and the automation that opens them becomes platform infrastructure with its own failure modes. A tenant that accepts proposals automatically has delegated review to a validator set, so a defect that validators do not catch reaches its clusters without a human seeing it.

A cluster matching several cells receives a union of policy, so conflicting policy across two cells is a state a tenant can express and a validator must reject.

Recording resolved versions duplicates into every tenant's repository what the tag already determines, and a stale or hand-edited record is a new way for a repository to disagree with itself.

## Impact

- **Amends ADR-063.** The bundle version is recorded on the cluster instance and pinned per cluster, rather than being a single value selected for a control plane at Day-0. The definition of a bundle and the requirement that component versions are declared once are unchanged.
- **Amends ADR-055.** `environmentRevision` becomes an environment value resident in Git, which is what that ADR already requires of every other value the seed Application consumes.
- **Amends ADR-031.** Policy and secret scope follow cluster identity, and a cluster serves one tenant, so isolation between tenants is not a property of a cluster's interior.
- **Confirms ADR-037.** Its promotion path, verification gate, protection rules and revert-based rollback are adopted for the bundle version without modification.
- **Confirms ADR-062.** The cluster instance and the cell are the records this ADR extends and binds policy to.
- **Confirms ADR-065.** A promotion is a proposal raised against a tenant's repository under access the tenant grants, and never an action against a tenant's cluster.
- **Confirms ADR-040.** Day-0 still selects the first bundle version and acts exactly once. Removing the CLI from the upgrade path narrows Day-1+ CLI execution to nothing.
- No change to ADR-021, ADR-042, ADR-047, ADR-052 or ADR-061.

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
- ADR-062: Onboarding and Scaffolding a Tenant
- ADR-063: The Platform Bundle and its Version
- ADR-065: The Control Plane Ships Into the Box
- ADR-067: Support Telemetry and the Basis of Maintenance
- ADR-068: The Build Declares the Bundle Version

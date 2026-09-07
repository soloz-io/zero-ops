# ADR-062: Repository Separation of Types, Instances and Workloads

**Date:** 2026-09-06
**Status:** Proposed

## Context

ADR-004 establishes a dual-repository contract: platform code in `zero-ops`, tenant runtime state in `fleet-registry`, which is the ArgoCD source of truth for tenant workloads. ADR-007 defines the fleet-registry spoke flow. ADR-047 defines the tenant deployment contract and rejects fleets authoring external workload repositories, on the ground that it contradicts ADR-004 and the workload separation of ADR-021 and ADR-027.

Four facts about the current arrangement bear on this decision.

**The word "fleet" names two different things in the decision record.** ADR-033 uses it for the cluster estate: exceeding its scale limits requires a new Hub cluster, with tenants and spokes pinned to a Hub. ADR-047 uses it for a single tenant's deployment boundary. The implementation follows neither consistently — the tenant provisioning ApplicationSets are written in terms of `tenantId`, while ADR-047 specifies `fleetId`, and both appear in the same file. Outside the platform the word is unambiguous and matches ADR-033: it denotes a set of clusters.

**`fleet-registry` holds two kinds of content with different owners and change rates.** Per-tenant, per-environment values, which the tenant provisioning ApplicationSets discover by directory glob and which change when a tenant is onboarded or reassigned; and rendered workload state, whose image digests change on every build of every tenant.

**Those writes share a branch, and contend.** The publishing workflow resolves the contention with a bounded rebase-and-push retry loop with jitter, which is the correct mitigation and works at present volume. The contention scales with tenants multiplied by build frequency. An exhausted retry leaves a built image unpinned, and that presents as a deployment which silently lags its build rather than as a repository conflict.

**Cluster instances and cluster definitions share commit rights.** SpokePool declarations are stored in `zero-ops` beside the Compositions and XRDs that define what a spoke pool is, so declaring a cluster and changing the meaning of a cluster are the same privilege.

Tenants are to be connected through a GitHub App. The App installation is therefore the platform's record of which repositories belong to which tenant, which makes a per-tenant workload repository addressable without a tenant naming it.

## Decision

Repositories are separated by what their content **is** — a type, an instance, or a workload — rather than by who authored it.

**`zero-ops` holds types.** XRDs, Compositions, ClusterClasses, boundary charts and platform manifests. It defines what a spoke pool is, what a tenant is, and what a boundary is. It holds no instance of any of them.

**A per-tenant infrastructure repository holds cluster instances.** One per tenant, in the tenant's own organisation, declaring each of that tenant's spoke clusters — environment, provider, region, node pool, the bundle version it runs, and who may approve a change to it — together with the cell definitions that group them for policy. Under ADR-065 the tenant's own control plane reconciles it, so the repository sits with the control plane that reads it.

`fleet-registry` is this repository for the platform's own box. The name is reclaimed for its ADR-033 meaning and is not used for a tenant boundary anywhere.

**`tenant-registry` holds tenant instances.** The record that a tenant exists: its identity, its environments, and the location of its repositories. It is the System of Record for tenant existence, and it is a commercial record rather than a reconciliation input — no tenant's control plane reads it, and a tenant continues to reconcile when it is unreachable.

**A per-tenant workload repository holds each tenant's workloads.** One per tenant, in the tenant's own organisation, holding overlays and image pins, and owned by that tenant. It is separate from the infrastructure repository above because its change rate is every build, where a cluster declaration changes when the estate changes.

### Vocabulary

One word per concept, used unchanged by ADR-063 and ADR-064.

**Platform** is the golden path: the types, the bundle, and the engineering that maintains them.

**Tenant** is a customer organisation, and holds exactly one **box** — control plane access together with that tenant's own spoke clusters. A tenant's control plane is either its own or one shared at cost; capability does not differ between them, and moving from one to the other is a supported operation rather than a migration. `fleetId` in ADR-047 is `tenantId`, which is what the implementation has used throughout.

**Spoke cluster** is the unit of everything measurable. It declares its region, provider and node pool, pins a bundle version, and is where compute is metered. It belongs to exactly one tenant, and is a **cluster instance** when referred to as a record in this registry.

**Cell** is a dynamic grouping of one tenant's spoke clusters, by label, for the purpose of attaching policy. A cluster may belong to several cells and receives the union of their policy. A cell is a selector: it holds no compute, pins no version, and owns nothing.

**Fleet** is the cluster estate, as ADR-033 already uses it.

**Bundle** is the complete set of platform content at one revision of `zero-ops`; a **bundle version** is the tag naming it. **Promotion** is advancing a cluster instance from one bundle version to the next.

**Workload** is what a tenant runs on its own clusters.

### The workload repository location is registration, not configuration

ADR-047 rejected fleets authoring external workload repositories because a value a tenant writes could redirect which code runs in that tenant's namespace. That reasoning is untouched by this ADR and the rejection stands.

The location of a tenant's workload repository is held in the tenant registry, established when the tenant is onboarded and derived from the App installation. It is platform-owned state that a tenant cannot alter through anything it controls. A repository named by tenant-authored values remains prohibited.

### A tenant authors its own instances

A tenant declares its clusters, and declares the cells that group them for policy. Both are held in the tenant's own infrastructure repository and reconciled by the tenant's own control plane.

This is a change of owner rather than a relaxation. The reasoning that made cluster identity platform-held was cross-tenant: a tenant able to author region, provider or secret path could name something outside its own box. Under ADR-062's single-tenant cluster and ADR-065's delivered control plane there is nothing outside the box to name — the credentials, the secret store and the clusters are all the tenant's. What remains is the platform's interest in a cluster being *well-formed*, which is asserted by the validators ADR-064 records rather than by withholding the field.

### Tenant discovery is explicit

Tenant discovery is currently implicit in a directory layout: the set of tenants is whatever a glob over `fleet-registry` returns. With each tenant holding its own repositories there is no such directory, and under ADR-065 no platform-run component reconciles tenants in the first place. The tenant registry records that a tenant exists for commercial purposes; discovery as a reconciliation input ceases to exist.

### Repository identity is declared once

Every repository the platform reads is named in exactly one place in the environment chart. ArgoCD keys repository credentials and its repository-server cache by the literal URL string, and AppProject `sourceRepos` matches it by exact string, so a repository named in several places is one that will eventually be named several ways. The failure is silent in both directions: an unmatched credential degrades to anonymous access, and an unmatched `sourceRepos` entry rejects the Application.

### Alternatives considered

**Retain one repository for all tenants, with directory-level ownership.** Rejected. It does not remove the write contention, because the contention is on the branch rather than on the paths. It also leaves history shared, so a tenant cannot be granted write access to its own state without being granted every other tenant's history.

**Retain cluster instances in `zero-ops` under CODEOWNERS.** Considered and not taken. Directory-level review delivers most of the separation, and ADR-037 already requires Platform Engineering review on production overlays. The deciding factor is separation of duties rather than review: declaring a cluster should not require commit rights over the Compositions that define what a cluster is.

**Hold the tenant registry in a database rather than a repository.** Rejected. It would make the cluster the System of Record for tenant existence, in conflict with ADR-039 and with the ownership ADR-021 assigns to Git, and the registry would then be reachable only through a component rather than through the same reconciliation path as everything else.

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Platform type definitions | `zero-ops` | Platform | ArgoCD | Platform | Day-1+ |
| Cluster instance declarations | tenant infrastructure repository | Tenant | Crossplane / CAPI | Tenant control plane | Day-1+ |
| Bundle version per cluster | tenant infrastructure repository | Tenant | ArgoCD | Boundary ApplicationSets | Day-1+ |
| Cell definitions and policy binding | tenant infrastructure repository | Tenant | ArgoCD | Spoke clusters | Day-1+ |
| Tenant records | `tenant-registry` | Platform | — | Platform | Day-1+ |
| Tenant workload state | tenant workload repository | Tenant | ArgoCD | Tenant | Day-1+ |

In every row Git is the System of Record and the corresponding in-cluster resource is its reconciled projection, not an independent authority. See ADR-039 for the complete ownership matrix.

## Consequences

### Positive

Each word in the decision record names one thing, and the implementation's use of `tenantId` stops contradicting the ADR that governs it.

Write contention disappears rather than being mitigated, because per-tenant repositories share no branch. The failure mode it produces at scale — a built image that never lands its pin, presenting as a stale deployment — becomes unreachable.

A tenant can be granted write access to its own desired state without being granted any other tenant's history.

Declaring a cluster and defining what a cluster is carry different commit rights.

A tenant groups its own clusters for policy without holding commit rights over what a cluster is, because a cell is a selector over clusters it already owns.

Each repository has one owner, one audience and one change rate, so its review rules can match its blast radius instead of averaging across unrelated content.

### Negative

One platform repository plus two per tenant, each requiring a credential, a webhook, ownership rules and continuous integration. The credentials are the tenant's own and the count is theirs to carry, but the URL-identity failure described above now occurs inside every tenant's box, where the platform cannot see it — which is why repository identity is declared once rather than per reference.

Tenant discovery becomes a mechanism that must be maintained, where it was previously a free consequence of the directory layout.

Onboarding a tenant becomes a repository-provisioning step rather than a directory-creation step, and offboarding must reckon with a repository the platform does not own.

A change spanning a type and its instances now spans repositories and cannot be atomic. Ordering that was previously guaranteed by a single commit has to be reasoned about.

## Impact

Supersedes ADR-004. The dual-repository contract is replaced by the separation above; every ADR that cites ADR-004 for the location of tenant runtime state now resolves to the per-tenant repository, and to the tenant registry for the record that the tenant exists.

Amends ADR-047. The rejection of fleets authoring external workload repositories stands on its original ground. The location of a tenant's workload repository is platform-held registration derived from the App installation, and is not tenant-authored configuration. `fleetId` is `tenantId` throughout.

Amends ADR-007. The spoke flow is unchanged in mechanism, but the repository it names now holds cluster instances rather than tenant state.

Confirms ADR-033. Its use of "fleet" for the cluster estate is adopted as the platform's definition.

Constrains ADR-063 and ADR-064. A fifth repository holding the platform's component versions — a `gitops-template` equivalent — was considered in ADR-063 and rejected on this ADR's test: it would hold the same content, at the same revision, as `zero-ops`, and so is not a distinct type, instance or workload. The per-cluster independence such a repository would have provided is obtained instead from the cluster instances held here, which ADR-064 extends to carry the bundle version each cluster runs.

Amends ADR-031. A spoke cluster belongs to exactly one tenant, so the shared-cell blast radius that ADR's topology was written to contain does not arise between tenants. What remains within a cluster is isolation between one tenant's own workloads.

Amended by ADR-065. A tenant's control plane runs in that tenant's box, which places the infrastructure repository with the control plane that reconciles it and makes the tenant its lifecycle owner.

No change to ADR-021, ADR-037, ADR-039, ADR-043 or ADR-055. No change to ADR-061: a component declares its own source, so a cluster-instance component naming a different repository is already expressible.

## References

- ADR-004: Dual Repository GitOps Pattern (superseded by this ADR)
- ADR-007: Fleet Registry Spoke Flow
- ADR-021: Boundary-Driven GitOps and Day-0 Choreography
- ADR-027: GitOps Workload Separation via Remote Bases
- ADR-031: Tenant Secret Isolation and Identity Topology
- ADR-033: Fleet Scale Targets & SLOs
- ADR-037: Directory-Based Environment Promotion and Gating
- ADR-039: Platform Ownership Model
- ADR-043: Control Plane Authority Model
- ADR-047: Fleet Tenant Deployment Contract
- ADR-055: Boundary Activation as the Day-0 Gating Mechanism
- ADR-061: Component Descriptors for Boundary Composition
- ADR-063: The Platform Bundle and its Version
- ADR-064: Bundle Promotion and Cell-Scoped Policy
- ADR-065: The Control Plane Ships Into the Box

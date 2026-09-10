# ADR-073: Workload Delivery is Version Pinning

**Date:** 2026-09-10

**Status:** Proposed

## Context

ADR-062 separates repositories by what their content is, and assigns workloads to tenant-owned repositories. ADR-063 establishes that platform content is published as a versioned artefact and consumed by a repository naming a version. ADR-047 rejected fleets authoring external workload repositories.

How a workload's new build reaches a running cluster is settled by none of them. Five facts bear on it.

**The mechanism in use copies rendered state into a platform repository.** Seven workflows in the `waypoint` repository build an image and then clone `soloz-io/fleet-registry`, substitute a digest into `tenants/waypoint/workloads/base/<component>/kustomization.yaml`, and push to the default branch behind a five-attempt rebase loop. The loop exists because every tenant's every build lands on one branch. The token those workflows carry has write access to a repository the platform owns.

**ADR-062 states two things that do not hold together.** It places workloads in "tenant-owned repositories ... separate from `<tenant>-gitops`", and it restates ADR-047's rejection of fleets authoring external workload repositories as standing "on its original ground". If workload state lives in a repository other than the one the box reconciles, something must resolve that repository; resolving one a fleet names is the practice ADR-047 rejected. One of the two has to give.

**A field records an intention nobody honours.** Every fleet values file declares `workloads.gitRepo`, and `05-tenant-fleet-appset.yaml` deliberately does not read it — the comment beside the generator says the repository is platform-owned and not taken from the fleet's values. A declared field that no reader consumes is worse than an absent one: it reads as a supported capability and is not.

**The comparable system pins a version rather than copying state.** kubefirst's application repository carries the application's own Helm chart. Its pipeline reads that chart's version, writes `<environment>/<application>/Chart.yaml` in the gitops repository as a Helm dependency at `<version>-rc.<short-sha>`, and commits. What crosses the boundary is a version string resolved from a chart registry, and the shipped template carries the placeholder `version: 0.0.1-rc.awaiting-ci` to make that explicit. The ArgoCD Application's source is the gitops repository at a path within it, never a repository the application declares.

**This platform already made that argument, about itself.** ADR-063 publishes the bundle and has a cluster name a version, and gives the reason directly: content copied into a tenant's repository is content the platform can no longer correct, and two writers on one file make every upgrade a merge to arbitrate. Nothing in that reasoning is specific to platform content.

## Decision

**A workload is delivered the way the platform is: published as a versioned artefact, and consumed by a repository naming that version.**

### The workload's chart is published, not copied

A workload's Helm chart lives in the repository that holds its source, is versioned with the build that produced it, and is published to a chart registry. The image reference is resolved inside that chart, at the version, by the party that built it.

What reaches the tenant's repository is a version string. No image digest, no kustomize overlay, and no rendered manifest is written there by a build.

### The tenant's repository holds a version and values

The tenant's `<tenant>-gitops` repository holds, per workload and per environment, the version it runs and the values it runs with. This is the same division ADR-062 established for the bundle, applied to the workloads that rest on it: one file a build writes, one file a human writes, and no field written by both.

### The workload's build writes the version and holds no platform credential

A build's last step is a commit to the tenant's own repository, changing the version of the workload it just published. It is the tenant's automation writing to the tenant's own repository, which ADR-065 permits directly, so it remains a push rather than a proposal. The platform's own restriction to branches and pull requests is unchanged, because the platform is not the party writing.

No build anywhere holds a credential to a repository the platform owns. That is the load-bearing part: the arrangement it replaces required one.

### The reconciled source is the tenant's own repository

An Application generated for a workload sources the tenant's own repository at a path within it. It never resolves a repository named in a fleet's values file.

ADR-047's rejection is retained and its ground is replaced. The original reason was cross-tenant containment: in a box holding many tenants, a tenant able to name a repository could name one outside its own boundary. ADR-065 removed that boundary — a box holds one tenant, and there is nothing outside it to name. The reason it survives is different and narrower: a repository named in a values file is a second thing to resolve, credential and revision included, in order to learn something a version already says. Publishing removes the need to name a repository at all.

`workloads.gitRepo` is therefore withdrawn rather than honoured. `workloads.gitPath` and `workloads.gitRevision` are withdrawn with it: a version identifies the content, and a path into a repository identifies only where a copy of it was put.

### Contention is removed rather than retried

Per-tenant repositories end contention between tenants; version pinning ends most of what remains within one. A build changes one line in the file for the workload it built, and two workloads in one repository no longer edit the same file. The retry loop stays, because two builds of the same workload still race, but it stops being the mechanism that makes the system work.

## Alternatives considered

**Retarget the existing mechanism at `<tenant>-gitops` and change nothing else.** Rejected. It is the smallest change and it moves the defect rather than fixing it: rendered state and image digests would then be written by builds into the same repository the platform proposes against, which is the two-writers-one-file problem ADR-062 opens by naming and ADR-063 rejects for the bundle. It also leaves every build holding a token for a repository it should not be able to write.

**Keep workload state in a separate tenant-owned repository and have the ApplicationSet resolve it.** Rejected, though it is the reading ADR-062's own wording invites. It requires the generator to honour a repository a values file names, which reintroduces the resolution ADR-047 refused, and it obliges the box to hold a credential for a repository whose contents a version would have described. It also makes a workload's identity a repository URL rather than a version, so nothing records what a cluster is running without fetching it.

**Argo CD Image Updater, or an equivalent controller watching the registry.** Rejected. It moves the write from a build into the cluster, which makes the running state the author of the desired state and removes the record of what asked for a change. It is also a component in every box, which ADR-065 already rejected for the maintenance mechanism on the same ground: a defect in it is present across the field.

**Publish workload charts to the same registry as the bundle, or to one the tenant runs.** Not settled here, and deliberately. Either satisfies this decision. A tenant running its own registry keeps custody complete in the sense ADR-063 requires; using the platform's is simpler and makes the platform a runtime dependency of the tenant's own applications, which is a heavier claim than it appears. The choice is per tenant and is recorded with the tenant, not here.

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Workload source and chart | tenant workload repository | Tenant | Tenant CI | chart registry | Day-1+ |
| Published workload chart | chart registry | Tenant | Tenant CI | tenant control plane | Day-1+ |
| Workload version per environment | `<tenant>-gitops` | Tenant CI | ArgoCD | tenant control plane | Day-1+ |
| Workload values per environment | `<tenant>-gitops` | Tenant | ArgoCD | workload chart | Day-1+ |

The platform appears in no row. See ADR-039 for the complete ownership matrix.

## Consequences

### Positive

No build holds a credential to a platform-owned repository, which makes ADR-065's claim true of the path a tenant's own code takes to its own cluster and not only of the path the platform's content takes.

What a cluster runs is legible from the tenant's repository without fetching anything: a version names it. Under the arrangement this replaces, reading the repository gave a digest, and a digest says what is running only to whoever can resolve it.

A rollback is an edit to one line, and the artefact it names still exists. Reverting a digest substitution required the overlay it was substituted into to still be correct.

The platform's content and a tenant's workloads are delivered by one mechanism, so the reasoning behind ADR-063 is tested twice rather than asserted once.

### Negative

Every workload now needs a published chart and somewhere to publish it. That is real work per application and a running dependency the current arrangement does not have — an application with no chart cannot be delivered at all, where before any kustomize overlay would do.

A registry outage stops deployments that a git push would have completed. The bundle already carries this exposure; this extends it to a tenant's own applications, and a tenant that judges its applications more critical than the platform may reasonably object.

Seven workflows in one repository change, and every workload repository onboarded since will have to change with them. The migration is not mechanical: it adds a chart and a publish step, not just a different URL.

A chart version per environment is a third place a version lives, alongside the bundle version and the component version matrix. Nothing yet asserts that a workload chart a tenant pins was built against a bundle version that tenant runs.

## Impact

- **Amends ADR-062.** Workload *state* is published from a tenant-owned repository; the tenant's `<tenant>-gitops` repository holds the version and values. The separation the ADR asserts is retained and the contradiction with ADR-047 is removed.
- **Amends ADR-047.** The rejection of fleets authoring external workload repositories stands, on the ground stated above rather than on cross-tenant containment. `workloads.gitRepo`, `workloads.gitPath` and `workloads.gitRevision` are withdrawn from the fleet values schema.
- **Amends ADR-027.** Tenant workload overlays are not held in a platform-owned registry repository. Remote bases remain available to a workload's own chart.
- **Confirms ADR-063.** The publish-and-pin argument is adopted for workloads on its own terms.
- **Confirms ADR-065.** A tenant's automation writing to a tenant's repository is the tenant acting, and is unaffected by the platform's restriction to proposals.
- No change to ADR-066: a workload remains the tenant's, and the platform gains no mechanism that writes one.

## References

- ADR-027: GitOps Workload Separation and Remote Bases
- ADR-039: Platform Ownership Model
- ADR-047: Fleet Tenant Deployment Contract
- ADR-062: Onboarding and Scaffolding a Tenant
- ADR-063: The Platform Bundle and its Version
- ADR-065: The Control Plane Ships Into the Box
- ADR-066: The Platform Boundary
- ADR-071: How This Platform Differs from kubefirst

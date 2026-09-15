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

Reading that source properly (`reference-projects/civo/`, 2026-09-16) shows four mechanics the summary above flattens, and each one carries a decision:

- **Publishing and deploying are separate workflows.** `.argo/publish.yaml` builds the image, sets the chart version to `<base>-rc.<shortSha>`, and pushes the chart. `.argo/deploy.yaml` takes `environment` as a parameter and writes that version into `registry/environments/<environment>/<app>/Chart.yaml`. Making an artefact and choosing where it runs are different acts, done at different times, and promoting is re-running the second with the same version against a different environment.
- **One chart per application, not per repository.** The gitops path is per application, and the umbrella chart at it declares exactly one dependency. Two applications never touch one file.
- **`appVersion` carries the image tag.** `set-chart-versions` stamps `appVersion` with the short SHA inside the app's own chart, and the chart's deployment renders `image: "{{ .Values.image.repository }}:{{ .Chart.AppVersion }}"`. The gitops repository names no image at all; resolving the chart version resolves the image transitively.
- **The chart registry is a component of the management cluster.** Charts are published to an in-cluster registry and dependencies name it by its `.svc.cluster.local` address. This one is observed and not adopted; the registry is settled below.

**This platform already made that argument, about itself.** ADR-063 publishes the bundle and has a cluster name a version, and gives the reason directly: content copied into a tenant's repository is content the platform can no longer correct, and two writers on one file make every upgrade a merge to arbitrate. Nothing in that reasoning is specific to platform content.

## Decision

**A workload is delivered the way the platform is: published as a versioned artefact, and consumed by a repository naming that version.**

### The workload's chart is published, not copied

A workload's Helm chart lives in the repository that holds its source, is versioned with the build that produced it, and is published to a chart registry. The image reference is resolved inside that chart, at the version, by the party that built it.

What reaches the tenant's repository is a version string. No image digest, no kustomize overlay, and no rendered manifest is written there by a build.

**The image tag is the chart's `appVersion`.** A build stamps `appVersion` with the commit it built and the chart renders `image: "{{ .Values.image.repository }}:{{ .Chart.AppVersion }}"`, so the one version string in the tenant's repository pins the image transitively. This is what makes the previous paragraph enforceable rather than aspirational: there is no field in the tenant's repository in which an image *could* be written, so a build cannot drift back to writing one. Taken from kubefirst unchanged.

### One chart per application

The tenant's repository holds `environments/<environment>/<tenant>/workloads/<application>/Chart.yaml` — a directory per application, whose umbrella chart declares exactly one dependency.

Not one chart per fleet listing every application as a dependency. That arrangement was written here first and does not work: every build of every application edits one file, which is the contention this decision claims to remove and would not have. It also collapses a tenant's applications into a single Application, so they share a sync, a health state and a blast radius — one unresolvable chart version leaves every other workload in the tenant unreconciled, and ArgoCD reports the whole set degraded without naming which member caused it.

Per-application directories give each workload its own Application, its own health, and a rollback that touches one line in one file that no other build writes. Taken from kubefirst, whose `registry/environments/<env>/<app>/Chart.yaml` has this shape for these reasons.

### Publishing an artefact and deploying it are separate acts

A build publishes a chart. A deployment pins a published version into one environment. They are separate steps, and the second names the environment it is acting on.

Collapsing them into "the build's last step is a commit" — as an earlier draft of this decision did — makes every build a deployment to whatever environment the build happens to target, and leaves promotion undescribed. Separating them makes promotion the same step run again with the same version and a different environment, which is what makes "the artefact that was tested is the artefact that ships" a property of the mechanism rather than a convention. ADR-064's promotion-is-a-proposal rule governs which environments require a pull request to make that change; it does not change what is being changed.

### Charts are published to the tenant's OCI registry

A workload chart is published to `oci://ghcr.io/<tenant-org>/charts`, in the git organisation that already holds the tenant's repositories.

This settles a question an earlier draft left open per tenant, and settles it for the same reason the rest of this ADR exists: an unanswered question in a delivery path is answered differently by every tenant that meets it, and the platform then supports every answer.

The organisation is already the tenant's. ADR-062 creates the tenant's repositories there and ADR-065 makes writes to them the tenant acting rather than the platform acting, so a chart published beside them needs no new custody argument and no new credential — the same installation that writes the version publishes the chart.

It is also the mechanism already in use rather than a second one. ADR-063 publishes the bundle as OCI artefacts to a registry of this kind, so the authentication, the tooling and the failure modes are ones the platform already has. A tenant reading this runs one kind of artefact store, not two.

**An in-cluster chart registry is rejected.** It is a stateful component in every box, with charts to persist, back up and restore, added to a platform whose maintenance burden is the thing being sold. Its one real advantage — that a registry sharing a failure domain with its cluster cannot fail independently of it — does not apply here: ArgoCD runs on the hub and every spoke deployment already depends on the hub being reachable, so an in-box registry removes no dependency that deployment does not already have. It would buy a failure domain that is already shared, and pay for it with a second artefact store to operate.

`ghcr.io` is named because it is where the platform already publishes and where a tenant scaffolded by ADR-062 already has an organisation. A tenant whose git organisation is elsewhere publishes to that provider's OCI registry instead; what is settled is that it is an OCI registry in the tenant's own organisation, not which vendor hosts it.

### A pinned version is present and wrong, never absent

Every application directory ships a real dependency carrying the placeholder version `0.0.1-rc.awaiting-ci`, which resolves to nothing until a build replaces it.

The alternative, shipping the dependency commented out, was written here first and is worse in a specific way: an umbrella chart with no dependencies renders zero resources, and ArgoCD reports zero resources as Synced and Healthy. A workload that was never deployed is then indistinguishable from one deployed successfully. A placeholder that cannot resolve fails loudly and names itself. Taken from kubefirst.

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

Per-tenant repositories end contention between tenants; version pinning and per-application charts end most of what remains within one. A build changes one line in one file, and that file is written by no other application's build.

This holds only because of the per-application granularity above. Under a single chart per fleet the claim is false — every build edits the shared `Chart.yaml`, and the contention is exactly what it was. The property is a consequence of the file layout, not of version pinning on its own.

The retry loop stays, because two builds of the same application still race, but it stops being the mechanism that makes the system work.

## Alternatives considered

**Retarget the existing mechanism at `<tenant>-gitops` and change nothing else.** Rejected. It is the smallest change and it moves the defect rather than fixing it: rendered state and image digests would then be written by builds into the same repository the platform proposes against, which is the two-writers-one-file problem ADR-062 opens by naming and ADR-063 rejects for the bundle. It also leaves every build holding a token for a repository it should not be able to write.

**Keep workload state in a separate tenant-owned repository and have the ApplicationSet resolve it.** Rejected, though it is the reading ADR-062's own wording invites. It requires the generator to honour a repository a values file names, which reintroduces the resolution ADR-047 refused, and it obliges the box to hold a credential for a repository whose contents a version would have described. It also makes a workload's identity a repository URL rather than a version, so nothing records what a cluster is running without fetching it.

**Argo CD Image Updater, or an equivalent controller watching the registry.** Rejected. It moves the write from a build into the cluster, which makes the running state the author of the desired state and removes the record of what asked for a change. It is also a component in every box, which ADR-065 already rejected for the maintenance mechanism on the same ground: a defect in it is present across the field.

**Publish workload charts to the platform's own registry.** Rejected. It is simpler by one line and it makes the platform a runtime dependency of the tenant's own applications — a heavier claim than it appears, and one ADR-065 spends the rest of its argument avoiding. A tenant's application should not stop deploying because the platform's registry is unavailable.

**Run a chart registry inside the box.** Rejected, with the reasoning in the decision above. It is kubefirst's answer and it is right there: their customer operates its own management cluster and a component in it is a component that customer runs. Here it is a component the platform maintains in every box, and the failure-domain advantage that justifies it is already provided by the hub.

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

A failing workload is named. Per-application Applications mean ArgoCD reports which workload is unresolvable, degraded or out of sync, rather than reporting one aggregate over all of a tenant's applications -- and a workload that was never deployed reports as failed rather than as an empty success.

### Negative

Every workload now needs a published chart and somewhere to publish it. That is real work per application and a running dependency the current arrangement does not have — an application with no chart cannot be delivered at all, where before any kustomize overlay would do.

A registry outage stops deployments that a git push would have completed. The bundle already carries this exposure; this extends it to a tenant's own applications, and a tenant that judges its applications more critical than the platform may reasonably object.

Seven workflows in one repository change, and every workload repository onboarded since will have to change with them. The migration is not mechanical: it adds a chart and a publish step, not just a different URL.

A chart version per environment is a third place a version lives, alongside the bundle version and the component version matrix. Nothing yet asserts that a workload chart a tenant pins was built against a bundle version that tenant runs.

A directory and an Application per application is more objects than one per fleet. A tenant with twenty workloads across three environments has sixty Applications where the rejected shape had three. That is the cost of per-workload health and rollback, and it is paid in ArgoCD's object count and UI, not in anything a tenant maintains by hand -- the directories are generated and the Applications are derived.

## Impact

- **Amends ADR-062.** Workload *state* is published from a tenant-owned repository; the tenant's `<tenant>-gitops` repository holds the version and values. The separation the ADR asserts is retained and the contradiction with ADR-047 is removed.
- **Amends ADR-047.** The rejection of fleets authoring external workload repositories stands, on the ground stated above rather than on cross-tenant containment. `workloads.gitRepo`, `workloads.gitPath` and `workloads.gitRevision` are withdrawn from the fleet values schema.
- **Amends ADR-027.** Tenant workload overlays are not held in a platform-owned registry repository. Remote bases remain available to a workload's own chart.
- **Confirms ADR-063.** The publish-and-pin argument is adopted for workloads on its own terms.
- **Confirms ADR-065.** A tenant's automation writing to a tenant's repository is the tenant acting, and is unaffected by the platform's restriction to proposals.
- No change to ADR-066: a workload remains the tenant's, and the platform gains no mechanism that writes one. The platform provides the path and derives the Application; the tenant's own automation writes the version.
- **Amends ADR-071.** Four further patterns are adopted from kubefirst -- the publish/deploy split, one chart per application, `appVersion` as the image tag, and a placeholder version that fails loudly. ADR-071's "What was taken from it" section is the register of such adoptions and records them.
- **Supersedes the shape this ADR was first implemented in.** `environments/<env>/<tenant>/workloads/Chart.yaml`, a single chart listing every application as a dependency, is replaced by a directory per application. The ApplicationSet generating one Application per fleet is replaced by one generating an Application per application directory.

## References

- ADR-027: GitOps Workload Separation and Remote Bases
- ADR-039: Platform Ownership Model
- ADR-047: Fleet Tenant Deployment Contract
- ADR-062: Onboarding and Scaffolding a Tenant
- ADR-063: The Platform Bundle and its Version
- ADR-065: The Control Plane Ships Into the Box
- ADR-066: The Platform Boundary
- ADR-064: Bundle Promotion and Cell-Scoped Policy
- ADR-071: How This Platform Differs from kubefirst

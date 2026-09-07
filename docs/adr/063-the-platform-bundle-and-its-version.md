# ADR-063: The Platform Bundle and its Version

**Date:** 2026-09-07
**Status:** Proposed

## Context

The platform is delivered as a set of third-party components — Crossplane, ArgoCD, Kyverno, External Secrets, CloudNativePG, cert-manager, Cilium, two Crossplane providers and several charts besides. Nothing in the decision record says how their versions are chosen, where they are recorded, or what it means to upgrade them.

In practice the versions are scattered across five kinds of location: component descriptors under `manifests/argocd/components/`, inline elements in the boundary ApplicationSets, a standalone `application.yaml`, Crossplane `Provider` packages, and a Go constant in `internal/hub-cli/versions/versions.go`. Nothing states that they are related, so nothing prevents them being changed one at a time.

Three facts about the current arrangement bear on this decision.

**A version pair already exists that nothing enforces.** `versions.go` pins the ArgoCD chart the Day-0 seed installs, and a descriptor pins the chart GitOps reconciles. They must be equal, and they drifted — 7.7.12 against 7.8.0 — until a validator was written to compare them. That validator exists because the coupling was discovered by failure, not because the coupling was recorded.

**A second such pair was found the same way.** `provider-kubernetes` v1.x runs only on Crossplane v2: v1.0.0 moved it to `crossplane-runtime/v2` and added a namespaced API tree. The package declares no core-version constraint, so pairing it with a v1 core installs cleanly and then misbehaves. The absence of a declared constraint was briefly taken as evidence of compatibility, which it is not.

**A bundle selector already exists.** `environmentRevision` is set once during Day-0 and flows into every boundary ApplicationSet — all nine templates, eighteen references — supplying `targetRevision` for every platform-owned Application and the revision for every descriptor-reading git generator. One value already selects the revision of the entire platform. It resolves to `"main"`.

### What a comparable platform does

kubefirst was examined because it solves the same problem for the same class of system.

Its CLI version is injected at build time (`-X configs.K1Version=v{{.Version}}`) and used directly as the tag of a separate `gitops-template` repository (`GitopsTemplateBranch = configs.K1Version`). Inside that template, every component pins an explicit chart version at the leaf, while the intermediate Applications point at the consuming repository at `HEAD`. The CLI version therefore names a complete, fixed component set.

Its current release ships Crossplane 1.17.0, External Secrets 0.8.1, cert-manager v1.14.4 and external-dns 1.14.4 — every one substantially behind the latest of its line, at a time when Crossplane 2.4.0, External Secrets 2.10.0 and cert-manager v1.21.1 are all available. That is not neglect. It is the observable consequence of shipping a set that was tested together rather than the newest of each component.

The codebase contains no upgrade machinery of any kind. The only occurrence of "upgrade" is a message telling the operator to update the CLI. The unit of change is the bundle.

**Two separable ideas are bundled in their design.** `gitops-template` carries 52 distinct placeholder tokens — `<CLUSTER_NAME>`, `<GITOPS_REPO_URL>` and others — because the CLI clones it, substitutes them, and pushes the result into each customer's own git organisation. That copy exists because each customer ends up **operating** the cluster it describes, and so owns the copy; it solves a hand-over problem. Separately, and independently of the hand-over, each cluster ends up with its own pinned version that can be advanced on its own. The second idea does not require the first. ADR-064 adopts the per-cluster pin and does not adopt the copy, for the reason recorded there: here the platform operates the infrastructure.

## Decision

**The platform is a bundle, and its version is a tag.**

A bundle is the complete set of platform content at one revision of `zero-ops`: every boundary, every component descriptor, every chart version, and the Day-0 constants that must agree with them. It is delivered as a unit because it is only ever tested as a unit.

### The version is a tag

A bundle version is a tag of `zero-ops` rather than a branch. A tag names a set that was tested together; a branch names whatever was merged most recently, which is a different thing and cannot be reproduced.

Day-0 accepts an override, and development continues to use it — a working branch is a legitimate bundle for a cluster that is being built rather than run. What changes is the default: a cluster that does not say otherwise runs a tag.

Where that tag is recorded, and how a cluster moves from one to the next, is ADR-064. This ADR settles what a version names; that one settles how it travels.

### Component versions are declared once

Every third-party version the platform installs is declared in one place. The existing parity validators assert that the scattered pins — descriptors, inline elements, provider packages, Go constants — agree with that declaration.

This is the mechanism that already exists, generalised. `scripts/validate-argocd-seed-parity.sh` compares one Go constant against one descriptor and would have caught the drift that produced it. The same comparison applied to the whole matrix turns every coupling from something discovered by failure into something a pre-commit hook refuses.

A coupling that cannot be expressed as equal version strings — `provider-kubernetes` v1.x requiring a Crossplane v2 core — is recorded as a constraint beside the versions it constrains, and asserted the same way.

### Upgrades move the bundle, not a component

A component is not upgraded on its own. The bundle is advanced to a new set, that set is exercised, and the result is tagged. What reaches a cluster is a version that existed as a whole before it was deployed.

Under ADR-065 the tag is the whole of delivery: the platform publishes it and holds no access to the clusters that consume it.

This is a statement about what is *released*, not about how work is done. Bumping one component while integrating it is ordinary; shipping that bump alone, to a cluster, as a change in its own right, is what this forbids.

### The bundle includes the tenant template

A bundle version selects more than the platform's own components. The tenant
ApplicationSets source the universal tenant chart at that same revision,
supplying per-tenant values from the tenant registry, so the chart that renders
a tenant's namespace, RBAC, secret bindings, gateway and TLS moves with the
bundle.

That chart is the platform's equivalent of a gitops-template, separated from
per-tenant values exactly as kubefirst separates template from substituted
tokens, but rendered continuously rather than copied once.

One consequence follows, and it is the reason the arrangement is worth keeping:
advancing a cluster's bundle advances the platform-rendered infrastructure of
every workload on that cluster, on the next reconcile, without touching any
tenant's repository. A bundle version is therefore also a statement about what
those workloads are running, and tagging it makes tenant infrastructure
reproducible on the same terms as the platform's. Because the version is pinned
per cluster under ADR-064, that statement is made one cluster at a time.

### Latest is not a target

The bundle pins versions that work together. That a newer release of a component exists is not a reason to move, and a platform whose components are each individually newest is a platform whose combination nobody has run — including its own upstreams.

The worked example is this repository on 2026-09-07: nine components moved to their respective latest releases in a single change, producing a combination that had no upstream precedent and no local test history. That is the state this ADR exists to make visible, not to permit.

### Alternatives considered

**Per-component upgrades, as now.** Rejected as the release model. It is how the current version matrix was reached, and it produces combinations that were never tested together. Its failures are not confined to the component moved: a stale observation in one provider presented as a spoke that would not become ready, and a diff strategy enabled in one component presented as unrelated Applications unable to render.

**Pinning nothing and tracking latest.** Rejected. It makes every reconcile a potential upgrade and removes the ability to reproduce a cluster, which ADR-042's bootstrap state machine and ADR-045's generated artifacts both assume.

**A fifth repository holding the component versions.** Rejected on ADR-062's test. Such a repository would hold the same content, at the same revision, as `zero-ops`, and so is not a distinct type, instance or workload. The per-cluster independence that motivates a separate repository elsewhere is obtained instead from the cluster instances that ADR-062 places in `fleet-registry`.

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Bundle definition (tag on `zero-ops`) | `zero-ops` | Platform | Release workflow | Cluster instances | Day-1+ |
| Component version matrix | `zero-ops` | Platform | pre-commit validators | Boundary ApplicationSets | Day-1+ |
| Cross-component constraints | `zero-ops` | Platform | pre-commit validators | Platform | Day-1+ |

The version each cluster runs is a separate resource class, owned by the cluster instance and recorded in ADR-064. See ADR-039 for the complete ownership matrix.

## Consequences

### Positive

A cluster can be rebuilt at the version it was built at, because that version names a fixed set rather than a moving branch.

A version coupling is stated once and asserted mechanically. The two that have been found so far were both discovered by a cluster failing; further ones are refused at commit.

The set that reaches a cluster is one that existed as a whole beforehand. A combination that nobody has run is visible as such before it is deployed rather than after.

Upgrading becomes a bounded, reviewable act with one artefact — a tag — rather than a diff spread across five kinds of file.

### Negative

Component versions will lag their upstreams, sometimes considerably, and that lag is deliberate rather than a defect to be corrected. A security fix in a single component still moves the whole bundle, which is more work than moving the component.

Declaring versions once means a second place to change when adding a component, and a validator that will refuse the addition until both agree. That cost is the point, but it is a cost.

Tagging is a release step the platform does not have today, and a bundle that is never tagged is a branch with extra ceremony.

Because the tenant template moves with the bundle, a defective bundle reaches the platform-rendered infrastructure of every workload on a cluster at once. The blast radius of a bundle is therefore a whole cluster, which is what makes per-cluster promotion under ADR-064 the mechanism that bounds it, and what argues for exercising a bundle before tagging it rather than for decoupling the template.

## Impact

- **Amends ADR-040.** Day-0's selection of a bundle version is named as such. Its CLI-only authority is unchanged.
- **Amends ADR-045.** Generated artifacts are scoped to the bundle that produced them; a bundle version and the artifacts committed under it are read together.
- **Confirms ADR-062.** A fifth repository was considered for this purpose and rejected on that ADR's own test.
- **Extends the mechanism of `scripts/validate-argocd-seed-parity.sh`** from one pair to the whole matrix.
- **Deferred to ADR-064.** Where a bundle version is recorded, how it advances, who approves it, and how it is withdrawn.
- No change to ADR-021, ADR-042, ADR-055 or ADR-061. Boundaries, bootstrap phases, activation gating and descriptor composition are all *within* a bundle and are unaffected by how it is versioned.

## References

- ADR-021: Boundary-Driven GitOps and Day-0 Choreography
- ADR-039: Platform Ownership Model
- ADR-040: Day-0 vs Day-1 Lifecycle Boundary
- ADR-042: Bootstrap State Machine
- ADR-045: Bootstrap-Generated GitOps Artifacts
- ADR-055: Boundary Activation as the Day-0 Gating Mechanism
- ADR-061: Component Descriptors for Boundary Composition
- ADR-062: Repository Separation of Types, Instances and Workloads
- ADR-065: The Control Plane Ships Into the Box
- ADR-064: Bundle Promotion and Tenant Placement

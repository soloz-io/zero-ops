# ADR 038: Continuous Revision Tracking for Ephemeral Environments

## Status

Accepted

## Context

Ephemeral preview environments and local development environments must continuously track the Git branch that created the environment for the entire lifecycle of the environment.

The existing platform deploys child ArgoCD Applications from static manifests. Those Applications reference `HEAD` and therefore reconcile against the repository default branch. This prevents environments from continuously tracking feature branches and causes ArgoCD reconciliation to eventually return environments to the default branch after bootstrap.

The platform organizes deployments into four operational boundaries:

* 01-platform-infra
* 02-platform-data
* 03-platform-services
* 04-tenant-services

These boundaries provide deployment sequencing, operational isolation, and visibility across the platform lifecycle.

## Decision

The platform shall implement continuous revision tracking using boundary ApplicationSets with a list generator. The `environment-manager` Helm chart renders four ApplicationSets (`01-platform-infra`, `02-platform-data`, `03-platform-services`, `04-tenant-services`) into the cluster. Each ApplicationSet contains a list generator with inline elements. The orchestrator calls `helm template --set environmentRevision=<branch>` at bootstrap time and applies the rendered output. ArgoCD's ApplicationSet controller generates child Applications with the revision injected into platform-owned apps.

The deployment hierarchy varies by environment type:

```text
# Local CAPD (orchestrator-driven)
Hub CLI (helm template --set)
        ↓
Environment Manager Helm Chart
        ↓
01-platform-infra ApplicationSet
02-platform-data ApplicationSet
03-platform-services ApplicationSet
04-tenant-services ApplicationSet
        ↓
Generated Child Applications

# Ephemeral Preview (PR-driven)
GitHub PR labeled preview-environment
        ↓
Pull Request Generator ApplicationSet ({{.head_sha}})
        ↓
Environment Manager Helm Chart (environmentRevision={{.head_sha}})
        ↓
01-platform-infra ApplicationSet
02-platform-data ApplicationSet
03-platform-services ApplicationSet
04-tenant-services ApplicationSet
        ↓
Generated Child Applications
```

Both paths converge at the `environment-manager` Helm chart — the orchestrator calls `helm template` directly for local CAPD, while the PR Generator delegates to the chart for GitHub PRs.

The Environment Application establishes the deployment boundaries and propagates the environment revision into those boundaries.

Each boundary is implemented as an ApplicationSet. The boundary ApplicationSet generates the child Applications belonging to that operational domain and applies the environment revision during generation.

Platform-owned child Applications are generated resources managed by boundary ApplicationSets rather than independently authored static Application manifests.

Generated child Applications continuously reconcile against the revision associated with the environment for its entire lifetime.

Third-party dependencies and version-pinned components remain independently versioned and are excluded from environment revision propagation.

The branch associated with an environment becomes part of the environment definition and remains authoritative until the environment is destroyed.

## Decision Drivers

The selected architecture satisfies the following requirements:

* Continuous tracking of feature branches throughout the environment lifecycle.
* Declarative ownership of revision state through ArgoCD reconciliation.
* Preservation of the existing operational boundary model.
* Elimination of bootstrap-time mutation as a source of truth.
* Elimination of repository mutations, ephemeral commits, and generated overlay directories.
* Explicit propagation of revision information without relying on implicit inheritance behavior.
* Isolation of revision propagation to platform-owned resources while preserving pinned versions for third-party dependencies.

## Impact

### Platform Architecture

The four existing deployment boundaries remain in place:

* 01-platform-infra
* 02-platform-data
* 03-platform-services
* 04-tenant-services

Boundary responsibilities expand from deploying static child Applications to generating child Applications through ApplicationSets.

### Application Ownership

Platform-owned child Applications transition from static authored manifests to generated resources managed by boundary ApplicationSets.

Application lifecycle ownership moves from Git-authored Application manifests to ApplicationSet reconciliation.

### Environment Lifecycle

Environment revision becomes a persistent attribute of the environment definition.

ArgoCD continuously reconciles the environment against the selected feature branch until the environment is destroyed.

### Operational Model

The existing boundary sequencing, health visibility, and deployment isolation model remains unchanged.

Operational workflows continue to use the existing boundary structure while gaining continuous feature-branch tracking.

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Kubernetes Resources | Git | ArgoCD | ArgoCD | Platform, Tenants | Day-1+ |

See ADR-039 for the complete ownership matrix.

## Consequences

### Positive

* Continuous synchronization to the tracked branch is maintained for the lifetime of the environment.
* Revision ownership is fully declarative and managed by ArgoCD.
* Environment lifecycle and revision lifecycle are aligned under a single control mechanism.
* Operational boundaries remain intact.
* No environment-specific repository artifacts are required.
* No bootstrap-time revision mutation is required.
* Adding a new platform Application requires adding one list element to the appropriate boundary template.

### Negative

* Platform-owned Applications become generated resources rather than standalone authored manifests.
* Boundary ApplicationSets become responsible for Application generation.
* Environment state depends on multiple reconciliation layers within the ApplicationSet hierarchy.
* Application generation logic becomes part of the platform control plane architecture.

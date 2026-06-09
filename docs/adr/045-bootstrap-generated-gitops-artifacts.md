# ADR-045: Bootstrap-Generated GitOps Artifacts

**Date:** 2026-06-09
**Status:** Proposed

## Context

The platform's GitOps model requires all resources to be defined in manifests committed to Git. However, some resources require values that are only known at Day-0 runtime — after the CLI has bootstrapped Infisical, created Machine Identities, and established the PKI hierarchy.

Today, these values reach GitOps resources through two inconsistent mechanisms:

1. **CLI patches ConfigMaps directly** (e.g., `hub-bootstrap-config` with OrgID/ProjectID) — requires ArgoCD `ignoreDifferences` because Git is no longer the source of truth.

2. **Empty strings committed to Git** (e.g., `clientId: ""` in `infisical-fleet-issuer.yaml`) — deployed with invalid values, producing delayed failures that are harder to diagnose.

Neither is acceptable for a zero-ops platform where Git is the sole source of truth.

The platform requires a formal pattern for bootstrap-generated GitOps artifacts: non-secret values that the CLI must produce during Day-0 but that ArgoCD must reconcile from Git.

## Decision

### Bootstrap-Generated GitOps Artifact Pattern

A Bootstrap-Generated GitOps Artifact is a Git-tracked manifest written by the Day-0 CLI and reconciled by ArgoCD. It bridges the gap between runtime-known values and declarative GitOps.

#### Location

All artifacts reside under `manifests/generated/`. Each artifact follows the naming convention `<resource-type>-<name>-patch.yaml`.

```
manifests/generated/
├── .gitkeep
├── README.md
├── infisical-fleet-issuer-patch.yaml
```

#### Lifecycle

1. **Generation (Day-0 CLI):** During the appropriate bootstrap phase defined by the Bootstrap State Machine (ADR-042), the CLI writes the artifact to `manifests/generated/`. Artifact generation is part of bootstrap completion.

2. **Commit + Push (Operator / CI):** Generated artifacts MUST be committed and pushed before platform readiness is achieved. The CLI validates the presence and correctness of required artifacts but does not require direct access to the Git remote. Platform readiness checks MUST fail until required generated artifacts have been committed, pushed, reconciled, and validated.

3. **Reconciliation (ArgoCD, Day-1+):** ArgoCD syncs the artifact as part of the owning kustomization. The artifact is consumed via `patches:` in a `kustomization.yaml`.

4. **Rotation (Operator):** To rotate a generated value, the operator regenerates the artifact (re-running the relevant CLI phase or editing the file) and commits the change. ArgoCD reconciles the new value.

#### Content Rules

| Permitted | Forbidden |
|-----------|-----------|
| Infisical project IDs | Passwords |
| Machine Identity client IDs | Private keys |
| Domain names and slugs | Certificates |
| Environment labels | Tokens |
| Non-secret configuration references | Any value stored in Infisical Secrets |

Forbidden content MUST NOT appear in generated artifacts. Secrets follow ADR-003 (ESO → Infisical → Kubernetes Secret). Certificates follow ADR-035 (cert-manager only).

Generated artifacts MUST be deterministic. Re-running the same bootstrap phase with identical inputs MUST produce byte-for-byte equivalent artifacts. Generated artifacts MUST NOT contain timestamps, random values, generated UUIDs, or ephemeral metadata unless those values are the explicit purpose of the artifact.

#### Skeleton and Fast Failure

Base manifests under `manifests/hub-core-services/*/` do NOT retain empty placeholder values. Instead, they use `spec: {}` or a minimal valid structure. A missing patch fails validation immediately — ArgoCD cannot reconcile a resource with `spec: {}` if the patch is required.

A missing generated artifact is a hard bootstrap failure, not a degraded runtime state.

#### Consumption

The consuming kustomization references the artifact via `patches:`:

```yaml
patches:
- path: ../../generated/infisical-fleet-issuer-patch.yaml
```

The base manifest uses `spec: {}` or a minimal valid structure. The patch supplies the real values at deploy time.

#### First Consumer

`infisical-fleet-issuer-patch.yaml` patches the `infisical-fleet-issuer` ClusterIssuer with `projectId` and `clientId` known only after Day-0 bootstrap completes.

### Ownership

This ADR defines a pattern and does not own platform resources. For resource ownership, see ADR-039.

## Consequences

### Positive

- Single, documented mechanism for Day-0 → GitOps value injection.
- Git remains the sole source of truth for all reconciled resources.
- Fast failure on missing artifacts — no stale empty values reaching production.
- Deterministic artifacts prevent GitOps drift and unnecessary commits.
- Pattern is reusable for OIDC client registrations, JWKS metadata, SPIRE bootstrap, SaaS identifiers, and tenant onboarding metadata.
- No dual ownership: CLI generates the file, ArgoCD owns the resource.

### Negative

- Adds a generation + commit + push step to the bootstrap workflow.
- Generated files inflate Git history unless managed.
- If the CLI is invoked in an environment without Git write access, the pattern fails — bootstrap blocks until artifacts are committed and pushed.

## Impact

No existing ADR is superseded. This ADR establishes a new pattern that future Day-0 → GitOps bridges should reference instead of inventing ad-hoc mechanisms.

## References

- ADR-003: Secret Management Architecture
- ADR-035: Enterprise PKI and Delegated Trust via Infisical OSS
- ADR-039: Platform Ownership Model
- ADR-040: Day-0 vs Day-1 Lifecycle Boundary
- ADR-042: Bootstrap State Machine
- ADR-044: Local Provider Runtime Abstraction

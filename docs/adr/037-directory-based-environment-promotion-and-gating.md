# ADR 037: Directory-Based Environment Promotion and Gating

## Status
Accepted

## Context
The platform requires a structured mechanism to promote infrastructure changes and tenant configurations through isolated lifecycle stages (ephemeral preview, dev, staging, production). Relying on a flat manifest structure or branch-based promotion risks configuration drift, obscures cross-environment visibility, and allows unverified changes to impact production workloads.

## Decision
We will enforce environment progression and access gating using a Directory-Based Overlay pattern combined with Git repository controls:

1. **Directory-Based Overlays:** Environments will be defined by strictly segregated Kustomize overlay directories (e.g., `manifests/environments/dev`, `stg`, `prod`). We explicitly reject branch-based promotion; all environment states will reside in the `main` branch to maintain a single, auditable source of truth.

2. **Environment-Bound Hubs:** Each environment maps to a physically isolated Hub cluster. Each Hub cluster SHALL be configured with an immutable environment-specific ArgoCD Application definition that reconciles only its designated overlay path. A production Hub physically cannot reconcile non-production manifests.

3. **Automated Promotion Workflow:** Promotion between environments SHALL be performed through automated repository workflows that create promotion pull requests. Direct manual duplication of configuration between environment overlay directories is discouraged. This prevents configuration drift caused by human error during file copying.

4. **Sequential Promotion:** Infrastructure and application changes must be promoted sequentially by mutating the overlays from lower environments to higher environments via Pull Requests. The promotion path is ephemeral through dev through stg to prod. Promotion workflows SHALL verify that the target revision has successfully reconciled and passed all required validation checks in the preceding environment before promotion is permitted. This prevents promotion from bypassing verification gates and provides an auditable compliance trail.

5. **Gated Deployment Approval:** Production deployment workflows SHALL use GitHub Environment Protection Rules requiring explicit approval before deployment execution. This creates a two-gate model where both change approval (PR review) and deployment approval (environment protection) are independently enforced.

6. **Cryptographic Gating:** GitHub `CODEOWNERS` SHALL mandate explicit Platform Engineering review and approval for any modifications targeting the `prod` overlay directories.

7. **Differentiated CI Validation:** Continuous Integration pipelines will execute lightweight, fast-fail validation (manifest linting, policy dry-runs) on all Pull Requests. Heavy end-to-end deployments (spinning up local Hub and CAPD Spoke clusters) will be decoupled and triggered via explicit PR labels to optimize pipeline velocity and compute costs.

8. **GitOps-Driven Rollback:** Rollback SHALL be performed through Git revert operations and GitOps reconciliation rather than direct cluster mutations. This preserves the integrity of Git as the single source of truth and ensures rollbacks are fully auditable.

## Consequences
### Positive
- Cross-environment differences and configuration drift are explicitly visible within a single repository commit history.
- Production modifications are securely gated by mandatory reviews and deployment approvals without requiring complex Git branching strategies.
- Blast radius is mathematically isolated; a malformed commit in the `dev` directory cannot be applied by the `prod` Hub cluster.
- Rollbacks are fully auditable through Git history rather than imperative cluster operations.

### Negative
- Promotion depends on automated workflow coordination across environment overlays, introducing additional pipeline complexity and operational ownership requirements.

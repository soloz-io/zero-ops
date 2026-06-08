# ADR-042: Bootstrap State Machine

**Date:** 2026-06-08
**Status:** Accepted

## Context

Bootstrap logic is currently distributed across ADRs 003 (secret injection), 021 (Day-0 CLI choreography), 025 (phased operator lifecycle), and 035 (PKI bootstrap exception). Each ADR describes a fragment of the bootstrap sequence but no single document defines the complete state machine, the owner of each state, the criteria for state transitions, or the recovery path for failures.

Without a state machine, bootstrap failures produce ambiguous diagnostics. When the platform is stuck between `SECRETS_READY` and `GITOPS_READY`, it is unclear which component is responsible for recovery or whether manual intervention is required.

This ADR consolidates bootstrap logic into a single state machine with explicit ownership, entry/exit criteria, and failure recovery instructions.

## Decision

### State Machine

```
  NEW
    │
    ▼
  INFISICAL_READY
    │
    ▼
  PKI_READY
    │
    ▼
  SECRETS_READY
    │
    ▼
  GITOPS_READY
    │
    ▼
  PLATFORM_READY
```

### State Definitions

#### NEW

The Hub cluster is provisioned (KinD or CAPH) but no platform components have been installed.

- **Owner:** CLI
- **Entry Criteria:** Hub cluster exists and `kubectl` is configured.
- **Exit Criteria:** `hub-platform` and `hub-secrets` Infisical projects exist. Machine Identity with admin access to both projects exists. Infisical access token is stored in Hub cluster.
- **Failure Recovery:** Re-run CLI bootstrap from start. State is fully idempotent.

#### INFISICAL_READY

Infisical projects and Machine Identities are created. Bootstrap secrets are generated and stored.

- **Owner:** CLI
- **Entry Criteria:** `NEW` exit criteria satisfied.
- **Operations:**
  - Generate bootstrap secrets (`platform-db-app`, `infisical-db-credentials`, `platform-db-ca`, `infisical-secrets`, `infisical-redis-credentials`).
  - Upload bootstrap secrets to Infisical `hub-secrets` project.
  - Create bootstrap secrets as Kubernetes Secrets in the Hub cluster.
- **Exit Criteria:** All bootstrap secrets exist in Infisical and as Kubernetes Secrets. Infisical API returns 200 for all secret paths.
- **Failure Recovery:** If Infisical is unreachable, retry with exponential backoff (max 5 minutes). If secret generation fails, re-run — generation is idempotent. If Kubernetes Secret creation fails, check RBAC permissions and re-run.

#### PKI_READY

The PKI trust hierarchy is established and the Fleet Intermediate CA is hosted in Infisical.

- **Owner:** CLI
- **Entry Criteria:** `INFISICAL_READY` exit criteria satisfied.
- **Operations:**
  - Generate Offline Root CA (outside Infisical, outside Kubernetes).
  - Upload Fleet Intermediate CA (signed by Offline Root CA) to Infisical `hub-platform` project.
  - Create certificate profiles for infrastructure services, database clients, service mesh, and human access.
  - Store Offline Root CA public certificate as a ConfigMap in the Hub cluster.
- **Exit Criteria:** Fleet Intermediate CA exists in Infisical PKI. Certificate profiles exist. Root CA ConfigMap exists in Hub cluster.
- **Failure Recovery:** If Root CA generation fails, re-generate — no downstream state depends on this operation. If Fleet Intermediate CA upload fails, verify Infisical Machine Identity PKI permissions and retry. Root CA private key must never be uploaded to Infisical or Kubernetes.

#### SECRETS_READY

All bootstrap secrets are synchronized to Kubernetes and infrastructure components can bootstrap.

- **Owner:** CLI (initiates), then ArgoCD (completes)
- **Entry Criteria:** `PKI_READY` exit criteria satisfied. `01-platform-infra` ArgoCD boundary is applied.
- **Operations:**
  - CLI applies `01-platform-infra` boundary and waits for Healthy/Synced.
  - CLI injects Secret Zero (trust anchors, bootstrap credentials).
  - CLI applies `02-platform-data` and `03-platform-services` boundaries.
  - CNPG bootstraps using `platform-db-app` bootstrap secret.
  - ESO syncs application secrets from Infisical.
  - Hub Operator creates database roles from ESO-synced credentials.
- **Exit Criteria:** All three ArgoCD boundaries (`01-platform-infra`, `02-platform-data`, `03-platform-services`) report `Healthy` and `Synced`. All ExternalSecrets report `SecretSynced`. CNPG clusters are ready. Hub Operator has completed initial reconciliation.
- **Failure Recovery:** If any ArgoCD boundary fails, inspect the specific Application health in ArgoCD. If ESO ExternalSecrets fail, verify Infisical connectivity and SecretStore configuration. If CNPG fails to bootstrap, verify `platform-db-app` secret content. If Hub Operator fails, check operator logs — database roles cannot be created until application secrets exist.

#### GITOPS_READY

ArgoCD is autonomously reconciling all boundaries. The CLI ownership handoff to Day-1 controllers is in progress.

- **Owner:** ArgoCD (platform-infra boundary), Hub Operator (Spoke orchestration), Crossplane (infrastructure)
- **Entry Criteria:** `SECRETS_READY` exit criteria satisfied.
- **Operations:**
  - All three ArgoCD boundaries continue reconciling autonomously.
  - Hub Operator begins SpokePool reconciliation.
  - Crossplane begins infrastructure XR reconciliation.
  - cert-manager begins certificate lifecycle.
  - Atlas Operator begins schema migration reconciliation.
- **Exit Criteria:** All Day-1 controllers have completed at least one successful reconciliation loop. The Hub Operator has reconciled all existing SpokePool CRs. Crossplane XRs report `Ready: True`. cert-manager Certificate resources report `Ready: True`.
- **Failure Recovery:** Controller-specific. Each controller must emit Kubernetes Events and status conditions. Operators diagnose failures using controller logs and Crossplane/ArgoCD status conditions. See ADR-040 for ownership handoff failure rules.

#### PLATFORM_READY

All Day-1 controllers are operating normally. The platform is fully provisioned and accepting tenant workloads.

- **Owner:** All Day-1 controllers (per ADR-039 ownership matrix)
- **Entry Criteria:** `GITOPS_READY` exit criteria satisfied. All Day-1 controllers have completed at least one successful reconciliation.
- **Exit Criteria:** Terminal state. No further transitions.
- **Failure Recovery:** Not applicable — this is the operational steady state. Failures are handled by individual controller reconciliation loops.

### State Transition Governance

1. A state transition may only be initiated by the state's designated Owner.
2. A state is entered only when all Entry Criteria are satisfied.
3. A state is exited only when all Exit Criteria are satisfied.
4. If a state's Exit Criteria cannot be satisfied after exhausting the documented Failure Recovery path, the bootstrap is blocked and requires manual intervention.
5. The CLI may initiate transitions through `SECRETS_READY`. After `SECRETS_READY` is achieved, the CLI exits permanently and controllers manage all subsequent transitions.

## Consequences

### Positive

- Bootstrap failures produce unambiguous diagnostics — the current state indicates which component is responsible.
- Consolidated bootstrap logic eliminates the need to cross-reference four separate ADRs to understand the bootstrap sequence.
- Explicit ownership per state prevents "who fixes this" ambiguity during bootstrap incidents.
- Failure recovery instructions per state reduce mean time to recovery.

### Negative

- The state machine is procedural; a state transition failure blocks all subsequent states. There is no partial progression.
- If a new infrastructure dependency is added to the bootstrap, this state machine must be updated.
- The state machine documents the ideal bootstrap path; edge cases (partial state, controller crashes during transition) require operator judgment beyond the documented recovery paths.

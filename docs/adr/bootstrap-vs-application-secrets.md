# ADR: Bootstrap vs Application Secrets Pattern

## Status
Proposed

## Context

The platform has two competing systems trying to create the same secrets:

1. **hub-operator** (Phase 1): Generates all database credential secrets including bootstrap and application secrets
2. **External Secrets Operator (ESO)**: Syncs secrets from Infisical with `creationPolicy: Owner`

This creates a conflict where ESO fails with "failed to take ownership of target secret" because the operator already created them.

### The Chicken-and-Egg Problem

```
CNPG needs platform-db-app → to bootstrap database
Database needs to exist → for operator to create roles  
Infisical needs database → to store secrets
ESO needs Infisical → to sync secrets
ESO wants to create platform-db-app → but CNPG already needs it!
```

### Current Architecture Issues

**Design Document Says:**
> "Operator generates Secret Zero → uploads to Infisical → ESO takes ownership"

**ESO Reality:**
- `creationPolicy: Owner` means ESO **creates** secrets from scratch
- ESO cannot "take ownership" of operator-created secrets
- ESO can only **update** existing secrets with `creationPolicy: Merge`

**Current Behavior:**
- Operator creates ALL secrets (bootstrap + application) in Phase 1
- CNPG uses `platform-db-app` to bootstrap (Wave 2)
- Operator uploads secrets to Infisical (Phase 3)
- ESO tries to create secrets from Infisical → FAILS (secrets already exist)

## Decision

Implement a **Hybrid Pattern** that separates Bootstrap Secrets from Application Secrets:

### Bootstrap Secrets (Operator Creates)
Secrets required for infrastructure components to start. These MUST exist before dependent services can bootstrap.

**Created by:** hub-operator (Phase 1)  
**Managed by:** ESO with `creationPolicy: Merge` (operator creates, ESO updates from Infisical)  
**Purpose:** Break circular dependencies in bootstrap sequence

**List:**
- `platform-db-app` - CNPG bootstrap superuser
- `infisical-db-credentials` - Infisical database user
- `platform-db-ca` - TLS certificate authority
- `infisical-secrets` - Infisical encryption keys
- `infisical-redis-credentials` - Redis authentication
- `infisical-postgres-connection` - Infisical DB connection string

### Application Secrets (ESO Creates)
Secrets for application workloads. These can be created AFTER infrastructure is bootstrapped.

**Created by:** ESO (synced from Infisical)  
**Managed by:** ESO with `creationPolicy: Owner`  
**Purpose:** Infisical is source of truth for application credentials

**List:**
- `control-plane-db-credentials` (mcp_server role)
- `hub-db-credentials` (spoke_controller role)
- `spire-server-db-credentials` (spire_server role)
- `hydra-db-credentials` (hydra role)
- `kratos-db-credentials` (kratos role)
- `keto-db-credentials` (keto role)
- `ghcr-pull-secret` (container registry)
- `hetzner-dns` (DNS provider)
- `platform-git-secret` (GitHub credentials)
- `victoriametrics-basic-auth` (metrics auth)

## Consequences

### Positive

1. **Respects Bootstrap Dependencies:** CNPG gets `platform-db-app` immediately in Wave 1
2. **Infisical is Source of Truth:** All application secrets originate from Infisical
3. **No ESO Conflicts:** Clear ownership boundaries prevent "failed to take ownership" errors
4. **Clean Separation:** Bootstrap vs Application secrets have different lifecycles
5. **Rotation Support:** ESO handles password rotation for application secrets
6. **Disaster Recovery:** Bootstrap secrets recreated by operator, application secrets synced from Infisical

### Negative

1. **Dual Secret Management:** Two systems manage secrets (operator for bootstrap, ESO for application)
2. **Complexity:** Developers must understand which secrets are bootstrap vs application
3. **Phase Dependency:** Operator Phase 2 must wait for ESO to create application secrets before creating database roles

### Neutral

1. **Bootstrap secrets uploaded to Infisical:** Operator still uploads bootstrap secrets to Infisical for backup/audit
2. **ESO syncs bootstrap secrets:** ESO keeps bootstrap secrets in sync with Infisical (using `creationPolicy: Merge`)

## Implementation

### Deployment Sequence

```
Wave 0: CRDs
  ↓
Wave 1: hub-operator + HubEnvironment CR
  ↓
Phase 1: Operator generates Bootstrap Secrets
  - platform-db-app
  - infisical-db-credentials  
  - platform-db-ca
  ↓
Operator uploads to Infisical
  ↓
Wave 2: CNPG Cluster + ESO
  ↓
CNPG bootstraps using platform-db-app
  ↓
ESO syncs from Infisical to create Application Secrets
  - control-plane-db-credentials
  - hub-db-credentials
  - spire-server-db-credentials
  - hydra-db-credentials
  - kratos-db-credentials
  - keto-db-credentials
  ↓
Phase 1b: Operator waits for Application Secrets to exist
  ↓
Phase 2: Operator creates database roles
  (uses ESO-created credentials)
  ↓
Wave 3: Infisical, Ory Stack, SPIRE
  ↓
Wave 4+: Application workloads
```

### Controller Changes

**Phase 1: Generate Bootstrap Secrets Only**
- Remove Ory credentials generation (hydra, kratos, keto)
- Remove role credentials generation loop
- Keep only bootstrap secrets

**Phase 1b: Wait for ESO Application Secrets (NEW)**
- Check for existence of ESO-managed secrets
- Requeue every 10s until all application secrets exist
- Set `ApplicationSecretsReady` condition

**Phase 2: Create Database Roles**
- Proceeds only after `ApplicationSecretsReady=True`
- Reads credentials from ESO-created secrets

### ExternalSecret Changes

**Bootstrap Secrets:**
```yaml
creationPolicy: Merge  # Operator creates, ESO updates
```

**Application Secrets:**
```yaml
creationPolicy: Owner  # ESO creates and owns
```

### Secret Mappings Changes

**Remove from operator upload:**
- control-plane-db-credentials
- hub-db-credentials
- spire-server-db-credentials
- hydra-db-credentials
- kratos-db-credentials
- keto-db-credentials

**Keep in operator upload:**
- platform-db-app (bootstrap)
- infisical-db-credentials (bootstrap)
- hcloud-token (CLI-injected)
- hetzner-dns (CLI-injected)
- ghcr-pull-secret (CLI-injected)
- platform-git-secret (CLI-injected)

## Alternatives Considered

### Option A: Operator Creates Everything
- Change all ExternalSecrets to `creationPolicy: Merge`
- Operator remains source of initial secrets
- **Rejected:** Violates "Infisical is source of truth" principle

### Option B: ESO Creates Everything
- Remove Phase 1 secret generation from operator
- Create placeholder secrets manually via CLI
- **Rejected:** Cannot solve CNPG bootstrap dependency (needs platform-db-app before Wave 2)

### Option C: Hybrid (SELECTED)
- Operator creates bootstrap secrets
- ESO creates application secrets
- **Selected:** Balances bootstrap dependencies with Infisical-as-source-of-truth

## References

- [ESO Infisical Pattern](./eso-infisical-pattern.md)
- [Sync Wave Order](./sync-wave-order.md)
- [Hub Operator Design](../.kiro/specs/hub-operator/design.md)
- [Secret Zero Generation Spec](../.kiro/specs/hub-operator/tasks.md#task-3-secret-zero-generation-implementation)

## Related Issues

- ESO ExternalSecrets failing with "failed to take ownership of target secret"
- mcp-server ImagePullBackOff due to missing ghcr-pull-secret
- control-plane-db-credentials SecretSyncedError
- Circular dependency between CNPG bootstrap and ESO secret creation

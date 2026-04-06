# ESO-Infisical Integration Pattern

## Status
Active - Updated to reflect hub-operator pattern

See also: [Bootstrap vs Application Secrets](./bootstrap-vs-application-secrets.md)

## Architecture Overview

Infisical is the **SOURCE OF TRUTH** for all secrets. Two patterns exist based on secret type:

### Pattern A: Bootstrap Secrets (Operator → Infisical → ESO)

```
[ hub-operator Phase 1 ]
   ↓
Generate bootstrap credentials
   ↓
Create K8s Secrets (platform-db-app, infisical-db-credentials, etc.)
   ↓
Upload to Infisical  ← SOURCE OF TRUTH
   ↓
----------------------------------------
   ↓
[ ESO with creationPolicy: Merge ]
   ↓
Sync from Infisical to update K8s Secrets
   ↓
[ CNPG / Infisical / Infrastructure ]
   ↓
Consume secrets to bootstrap
```

**Purpose:** Break circular dependencies. CNPG needs `platform-db-app` to bootstrap before Infisical can start.

**Secrets:** platform-db-app, infisical-db-credentials, platform-db-ca, infisical-secrets, infisical-redis-credentials

### Pattern B: Application Secrets (Infisical → ESO → Apps)

```
[ Infisical UI / API ]
   ↓
Create/Update credentials
   ↓
Store in Infisical  ← SOURCE OF TRUTH
   ↓
----------------------------------------
   ↓
[ ESO with creationPolicy: Owner ]
   ↓
Sync to K8s Secrets (Hub + Spokes)
   ↓
[ Application Workloads ]
   ↓
Consume secrets
   ↓
[ PostgreSQL / Services ]
```

**Purpose:** Infisical is the source of truth. ESO creates and owns the K8s secrets.

**Secrets:** control-plane-db-credentials, hub-db-credentials, spire-server-db-credentials, hydra-db-credentials, kratos-db-credentials, keto-db-credentials, ghcr-pull-secret, hetzner-dns, hub-platform-git-secret

## Password Lifecycle

### Bootstrap Secrets Lifecycle

1. **Initial Creation:** hub-operator generates secure random password
2. **K8s Secret Created:** Operator creates secret in cluster
3. **Upload to Infisical:** Operator uploads to Infisical (backup/audit)
4. **ESO Sync:** ESO keeps K8s secret in sync with Infisical (Merge mode)
5. **Rotation:** Update in Infisical → ESO syncs → Operator detects change → Updates database

### Application Secrets Lifecycle

1. **Creation in Infisical:** Secret created via Infisical UI/API/CLI
2. **ESO Sync:** ESO pulls from Infisical and creates K8s secret
3. **App Consumption:** Application reads from K8s secret
4. **Rotation:** Update in Infisical → ESO syncs → Operator detects change → Updates database

## Spoke Cluster Pattern

```
[ Hub Infisical ]
     ↓
Tenant credentials stored
     ↓
-----------------------------------
     ↓
[ Spoke Cluster ESO ]
     ↓
Pulls from Hub Infisical
     ↓
Creates K8s Secrets locally
     ↓
[ Spoke Applications ]
     ↓
Consume secrets
```

## Boundary Rules

- ✅ Use Infisical for secrets (passwords, tokens, API keys)
- ❌ Do NOT use Infisical for service discovery
- ❌ Do NOT use Infisical for configuration management
- ✅ Use ConfigMaps for non-sensitive configuration
- ✅ Use Service DNS for service discovery

## ESO Configuration Patterns

### Bootstrap Secrets (Operator Creates)

```yaml
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: platform-db-app-credentials
spec:
  target:
    name: platform-db-app
    creationPolicy: Merge  # Operator creates, ESO updates
```

### Application Secrets (ESO Creates)

```yaml
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: control-plane-db-credentials
spec:
  target:
    name: control-plane-db-credentials
    creationPolicy: Owner  # ESO creates and owns
```

## References

- [Bootstrap vs Application Secrets ADR](./bootstrap-vs-application-secrets.md)
- [Sync Wave Order ADR](./sync-wave-order.md)
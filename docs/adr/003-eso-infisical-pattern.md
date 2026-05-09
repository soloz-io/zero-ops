# ESO-Infisical Integration Pattern

## Status
Active - Updated to reflect hub-operator pattern and dual-phase rotation

See also: [Bootstrap vs Application Secrets](./bootstrap-vs-application-secrets.md)

## Architecture Overview

Infisical is the **SOURCE OF TRUTH** for all secrets. Two patterns exist based on secret type:

**Critical Rule:** The ESO `PushSecret` resource is strictly banned due to lack of provider support (Infisical ESO provider does not support PushSecret) and architectural fragility. Secrets must flow from upstream controllers to Infisical (push-left), not from cluster operators back to Infisical.

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

### Pattern A2: Dynamic Tenant/Spoke Secrets

This pattern has two sub-patterns based on the resource type:

#### Pattern A2a: Tenant Resources (Kube-SBT → Infisical Only → ESO)

```
[ Kube-SBT / open-sbt Application Plane ]
   ↓
Check Infisical: does password exist?
   ↓
If exists → Skip (idempotent)
   ↓
If missing AND first-time → Generate password
   ↓
If missing AND NOT first-time → FAIL (manual intervention required)
   ↓
Upload to Infisical ONLY  ← SOURCE OF TRUTH (no Hub K8s secret)
   ↓
----------------------------------------
   ↓
[ Spoke ESO with creationPolicy: Owner ]
   ↓
Sync from Infisical to create K8s Secret on Spoke
   ↓
[ Spoke Crossplane provider-sql OR Application ]
   ↓
Consume secret to provision tenant databases or connect to applications
```

**Purpose:** Generate tenant resource credentials (databases, apps) via Kube-SBT, deliver to Spoke via Infisical. No Hub K8s secret duplication.

**Use Cases:**
- Tenant database credentials
- Tenant application secrets
- Per-tenant service credentials

**Secrets:** 
- Tenant database credentials: `/spoke-pool/<cell-id>/tenants/<tenant-id>/db-credentials`
- Properties stored: `username`, `password`

**Key Characteristics:**
- Kube-SBT/open-sbt does NOT create K8s secret on Hub
- Infisical is queried directly for idempotency (via API)
- Password generated ONLY on first-time tenant onboarding
- If password missing later → controller FAILS (prevents accidental regeneration)

**Key Naming Convention:**
- Pattern: `/spoke-pool/<cell-id>/tenants/<tenant-id>/db-credentials`
- Example: Tenant `app-creator` in cell `spoke-pool-eu-prod-01` → `/spoke-pool/spoke-pool-eu-prod-01/tenants/app-creator/db-credentials`
- Implementation: `infisicalPath := fmt.Sprintf("/spoke-pool/%s/tenants/%s/db-credentials", cellId, tenantId)`

#### Pattern A2b: Spoke Infrastructure (Infrastructure Controller Generation → Infisical → ESO)

```
[ Hub Operator (SpokePoolReconciler) ]
   ↓
Generate password in Go (secure random)
   ↓
Upload directly to Infisical via REST API  ← SOURCE OF TRUTH
   ↓
----------------------------------------
   ↓
[ Spoke ESO with creationPolicy: Owner ]
   ↓
Sync from Infisical to create K8s Secret on Spoke
   ↓
[ Spoke Crossplane provider-sql ]
   ↓
Consume secret to provision spoke infrastructure
```

**Purpose:** Generate spoke infrastructure credentials via upstream control plane (Hub Operator), store in Infisical as source of truth, deliver to Spoke via ESO.

**Use Cases:**
- Spoke crossplane-admin database credentials
- Spoke infrastructure service accounts
- Per-spoke system credentials

**Secrets:**
- SpokePool credentials: `<spokepool-cr-name>-crossplane-admin-password`

**Key Characteristics:**
- Hub Operator (e.g., `SpokePoolReconciler` in `operators/hub-operator/internal/controller/spokepool_controller.go`) generates password in Go
- Password uploaded directly to Infisical via REST API (no K8s Secret on Hub)
- Spoke ESO pulls from Infisical to create spoke-local secret
- Idempotency via Hub Operator's reconciliation logic (checks Infisical before generation)

**Critical Rule:** The ESO `PushSecret` resource is strictly banned due to lack of provider support and architectural fragility. Secrets must flow from upstream controllers to Infisical (push-left), not from cluster operators back to Infisical.

**Key Naming Convention:**
- Pattern: `<spokepool-cr-name>-crossplane-admin-password`
- Example: SpokePool CR `spoke-pool-eu-prod-01` → Infisical key `spoke-pool-eu-prod-01-crossplane-admin-password`
- Implementation: `infisicalKey := fmt.Sprintf("%s-crossplane-admin-password", spokeName)`

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
Consume secrets at runtime
   ↓
[ PostgreSQL / Services ]
```

**Purpose:** Infisical is the source of truth. ESO creates and owns the K8s secrets.
**ESO role:** Runtime delivery only — syncing the current active credential to K8s for application consumption. ESO does NOT drive rotation.

**Secrets:** control-plane-db-credentials, hub-db-credentials, spire-server-db-credentials, hydra-db-credentials, kratos-db-credentials, keto-db-credentials, ghcr-pull-secret, hetzner-dns, platform-git-secret

---

## Password Lifecycle

### Bootstrap Secrets Lifecycle (Pattern A)

1. **Initial Creation:** hub-operator generates secure random password
2. **K8s Secret Created:** Operator creates secret in cluster
3. **Upload to Infisical:** Operator uploads to Infisical (backup/audit)
4. **ESO Sync:** ESO keeps K8s secret in sync with Infisical (Merge mode)
5. **Rotation:** See Rotation section below

### Per-Spoke/Tenant Secrets Lifecycle (Pattern A2)

#### Pattern A2a: Tenant Resources (Kube-SBT)

1. **CR Reconciliation (First-Time):** 
   - Tenant CR created (via Kube-SBT API)
   - Kube-SBT Application Plane controller reconciles
   - Controller queries Infisical API: does password exist?
   - If missing AND first-time → Generate secure random password and upload to Infisical
   - **Controller does NOT create Hub K8s secret**
2. **ESO Sync (Spoke):** Spoke ESO pulls from Infisical and creates K8s secret on Spoke (Owner mode)
3. **Subsequent Reconciles:** 
   - Controller checks Infisical API for password existence
   - If password exists → Skip generation (idempotent)
   - If password missing → FAIL reconciliation (manual intervention required)
4. **Rotation:** Requires manual password update in Infisical + Spoke database role update

**Critical:** Password is generated ONLY during first-time tenant onboarding. Controller does not manage secret lifecycle post-creation.

#### Pattern A2b: Spoke Infrastructure (Hub Operator)

1. **Initial Creation:**
   - Hub Operator (SpokePoolReconciler) reconciles SpokePool CR
   - Operator generates secure random password in Go
   - Operator uploads password directly to Infisical via REST API
   - **Operator does NOT create K8s Secret on Hub**
2. **ESO Sync (Spoke):** Spoke ESO pulls from Infisical and creates K8s secret on Spoke (Owner mode)
3. **Subsequent Reconciles:**
   - Hub Operator checks Infisical for password existence
   - If password exists → Skip generation (idempotent)
   - If password missing → FAIL reconciliation (manual intervention required)
4. **Rotation:** See Rotation section below

**Critical:** Infisical is the absolute source of truth. No K8s Secret exists on Hub. Password generation happens imperatively by the upstream controller before any GitOps reconciliation.

### Application Secrets Lifecycle (Pattern B)

1. **Creation in Infisical:** Secret created via Infisical UI/API/CLI
2. **ESO Sync:** ESO pulls from Infisical and creates K8s secret
3. **App Consumption:** Application reads from K8s secret at runtime
4. **Rotation:** See Rotation section below

---

## Password Rotation

### Critical Distinction

**ESO sync is for runtime consumption, NOT rotation.**

ESO's job is to deliver the current active credential from Infisical to K8s so applications can consume it. It does not initiate or drive rotation. Rotation is a separate, ordered process that must alter the database credential first before updating Infisical.

Doing it the other way (update Infisical first → ESO syncs → app picks up new password → DB still has old password) causes an authentication failure window.

### Dual-Phase Rotation Pattern

Rotation follows Infisical's dual-phase (secret rotation) model:
- Reference: https://infisical.com/docs/documentation/platform/secret-rotation/overview

```
Phase 1 — Prepare new credential (zero-downtime window opens)
   ↓
ALTER ROLE <user> WITH PASSWORD '<new-password>'  ← DB altered FIRST
   ↓
Both old and new passwords valid simultaneously (overlap period)
   ↓
Update Infisical with new password  ← SOURCE OF TRUTH updated
   ↓
ESO detects change, syncs new password to K8s Secret
   ↓
Applications pick up new credential (via pod restart or secret reload)
   ↓
Phase 2 — Expire old credential (overlap period ends)
   ↓
Confirm all connections using new password
   ↓
Invalidate old password in DB
   ↓
Zero-downtime rotation complete
```

### Why DB is altered first

The database is the authoritative source for whether a credential works. Updating Infisical before altering the DB creates a window where:
- ESO has synced the new password to K8s
- Applications attempt to connect with the new password
- DB still has the old password → authentication failure

Altering the DB first ensures the new password is valid before any application attempts to use it.

### Overlap period

During the overlap period both the old and new passwords are valid in the DB. This gives:
- Time for ESO to sync the new password to K8s
- Time for applications to pick up the new credential (pod restart / secret reload)
- A safe rollback window if the new credential has issues

### Rotation trigger

Rotation is triggered externally (not by ESO):
- Infisical's built-in secret rotation scheduler
- Manual trigger via Infisical UI/API
- Platform operator annotation (e.g., `rotation.nutgraf.in/trigger`)

ESO is only involved in the final step: delivering the updated credential to K8s after Infisical has been updated.

---

## Spoke Cluster Pattern

```
[ Hub Infisical ]
     ↓
Tenant credentials stored  ← SOURCE OF TRUTH
     ↓
-----------------------------------
     ↓
[ Spoke Cluster ESO ]
     ↓
Pulls from Hub Infisical
     ↓
Creates K8s Secrets locally (runtime delivery)
     ↓
[ Spoke Applications ]
     ↓
Consume secrets
```

---

## Boundary Rules

- ✅ Use Infisical for secrets (passwords, tokens, API keys)
- ✅ ESO syncs Infisical → K8s for runtime consumption only
- ✅ Rotation always alters the DB credential first, then updates Infisical
- ✅ Dual-phase rotation (overlap period) for zero-downtime
- ✅ **Tenant resources**: Kube-SBT generates and uploads to Infisical ONLY (Pattern A2a)
- ✅ **Spoke infrastructure**: Crossplane generates K8s Secret + PushSecret to Infisical (Pattern A2b)
- ✅ Query Infisical API directly for idempotency when Hub K8s secret not needed (Pattern A2a)
- ❌ Do NOT update Infisical before altering the DB (causes auth failure window)
- ❌ Do NOT use ESO sync as the rotation trigger
- ❌ Do NOT regenerate tenant passwords if missing from Infisical (requires manual intervention)
- ❌ Do NOT use PushSecret for tenant resources (Kube-SBT uploads directly)
- ❌ Do NOT use Infisical for service discovery
- ❌ Do NOT use Infisical for configuration management
- ✅ Use ConfigMaps for non-sensitive configuration
- ✅ Use Service DNS for service discovery

---

## ESO Configuration Patterns

### Bootstrap Secrets (Operator Creates - Pattern A)

```yaml
apiVersion: external-secrets.io/v1
kind: ExternalSecret
metadata:
  name: platform-db-app-credentials
spec:
  target:
    name: platform-db-app
    creationPolicy: Merge  # Operator creates, ESO updates
```

### Tenant Resource Secrets (Kube-SBT Uploads, ESO Creates - Pattern A2a)

```yaml
apiVersion: external-secrets.io/v1
kind: ExternalSecret
metadata:
  name: tenant-app-creator-db-credentials-restore
  namespace: tenant-app-creator
spec:
  secretStoreRef:
    name: infisical-backend
    kind: ClusterSecretStore
  target:
    name: tenant-app-creator-db-credentials
    creationPolicy: Owner  # ESO creates and owns (no Hub K8s secret)
  data:
  - secretKey: username
    remoteRef:
      key: /spoke-pool/${CELL_ID}/tenants/${TENANT_ID}/db-credentials
      property: username
  - secretKey: password
    remoteRef:
      key: /spoke-pool/${CELL_ID}/tenants/${TENANT_ID}/db-credentials
      property: password
```

**Note:** Kube-SBT uploads to Infisical only. Spoke ESO creates the K8s secret.

### Spoke Infrastructure Secrets (Crossplane + PushSecret - Pattern A2b)

**Hub K8s Secret (created by Crossplane):**
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: spoke-pool-eu-prod-01-crossplane-admin
  namespace: platform-ops
type: Opaque
stringData:
  password: "<generated-from-metadata-uid>"
```

**PushSecret (pushes to Infisical):**
```yaml
apiVersion: external-secrets.io/v1alpha1
kind: PushSecret
metadata:
  name: spoke-pool-eu-prod-01-crossplane-admin-push
  namespace: platform-ops
spec:
  secretStoreRefs:
    - name: infisical-backend
      kind: ClusterSecretStore
  selector:
    secret:
      name: spoke-pool-eu-prod-01-crossplane-admin
  data:
    - match:
        secretKey: password
        remoteRef:
          remoteKey: spoke-pool-eu-prod-01-crossplane-admin-password
```

**Spoke ExternalSecret (pulls from Infisical):**
```yaml
apiVersion: external-secrets.io/v1
kind: ExternalSecret
metadata:
  name: crossplane-admin-credentials
  namespace: platform-ops
spec:
  secretStoreRef:
    name: infisical-secret-store
    kind: SecretStore
  target:
    name: crossplane-admin-credentials
    creationPolicy: Owner  # ESO creates and owns on Spoke
  data:
  - secretKey: password
    remoteRef:
      key: spoke-pool-eu-prod-01-crossplane-admin-password
```

**Note:** Crossplane creates Hub K8s secret, PushSecret backs up to Infisical, Spoke ESO creates spoke secret.

### Application Secrets (ESO Creates - Pattern B)

```yaml
apiVersion: external-secrets.io/v1
kind: ExternalSecret
metadata:
  name: control-plane-db-credentials
spec:
  target:
    name: control-plane-db-credentials
    creationPolicy: Owner  # ESO creates and owns
```

---

## References

- [Bootstrap vs Application Secrets ADR](./bootstrap-vs-application-secrets.md)
- [Sync Wave Order ADR](./sync-wave-order.md)
- [Infisical Secret Rotation](https://infisical.com/docs/documentation/platform/secret-rotation/overview)

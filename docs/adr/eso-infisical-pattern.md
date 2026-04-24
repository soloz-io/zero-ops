# ESO-Infisical Integration Pattern

## Status
Active - Updated to reflect hub-operator pattern and dual-phase rotation

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

### Pattern A2: Per-Spoke Secrets (Operator → Infisical Only → ESO)

```
[ hub-operator SpokePool controller ]
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
[ Spoke Crossplane provider-sql ]
   ↓
Consume secret to provision tenant databases
```

**Purpose:** Generate per-spoke credentials on Hub, deliver to Spoke via Infisical. No Hub K8s secret duplication.

**Key Difference from Pattern A:** 
- Hub Operator does NOT create K8s secret on Hub
- Infisical is queried directly for idempotency (via API)
- Password generated ONLY on first-time SpokePool creation
- If password missing later → controller FAILS (prevents accidental regeneration that would break Spoke CNPG)

**Secrets:** `<spokepool-cr-name>-crossplane-admin-password` (per SpokePool)

**Key Naming Convention:** The Infisical key MUST be derived from the SpokePool CR name to ensure uniqueness and traceability:
- Pattern: `<spokepool-cr-name>-crossplane-admin-password`
- Example: SpokePool CR `spoke-pool-eu-prod-01` → Infisical key `spoke-pool-eu-prod-01-crossplane-admin-password`
- Implementation: `infisicalKey := fmt.Sprintf("%s-crossplane-admin-password", spokeName)`

**Idempotency Logic:**
1. Check status condition: `CrossplaneAdminSecretGenerated=True`?
2. Query Infisical API: does secret exist?
3. Decision matrix:
   - Exists in Infisical → Skip generation (idempotent)
   - Missing + first-time (status=False) → Generate and upload
   - Missing + NOT first-time (status=True) → FAIL with error

**Manual Intervention Required:** If password is accidentally deleted from Infisical after initial provisioning, the controller will fail reconciliation with error: `"Password missing from Infisical for already-provisioned SpokePool - manual recovery required"`. This prevents breaking the Spoke's CNPG connection by generating a new password that doesn't match the database.

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

**Secrets:** control-plane-db-credentials, hub-db-credentials, spire-server-db-credentials, hydra-db-credentials, kratos-db-credentials, keto-db-credentials, ghcr-pull-secret, hetzner-dns, hub-platform-git-secret

---

## Password Lifecycle

### Bootstrap Secrets Lifecycle (Pattern A)

1. **Initial Creation:** hub-operator generates secure random password
2. **K8s Secret Created:** Operator creates secret in cluster
3. **Upload to Infisical:** Operator uploads to Infisical (backup/audit)
4. **ESO Sync:** ESO keeps K8s secret in sync with Infisical (Merge mode)
5. **Rotation:** See Rotation section below

### Per-Spoke Secrets Lifecycle (Pattern A2)

1. **CR Reconciliation (First-Time):** 
   - SpokePool CR created on Hub
   - hub-operator SpokePool controller reconciles
   - Controller checks status condition: `CrossplaneAdminSecretGenerated=True`?
   - Controller queries Infisical API: does password exist?
   - If missing AND first-time → Generate secure random password and upload to Infisical
   - Controller sets status condition to True
   - **Operator does NOT create Hub K8s secret**
2. **ESO Sync (Spoke):** Spoke ESO pulls from Infisical and creates K8s secret on Spoke (Owner mode)
3. **Subsequent Reconciles:** 
   - Controller checks Infisical API for password existence
   - If password exists → Skip generation (idempotent)
   - If password missing AND status=True → FAIL reconciliation (manual intervention required)
4. **Rotation:** Not supported - requires manual password update in Infisical + Spoke CNPG role update

**Critical:** Password is generated ONLY during first-time CR reconciliation. Operator does not manage secret lifecycle post-creation. If password is accidentally deleted from Infisical after initial provisioning, controller will fail to prevent breaking Spoke's CNPG connection.

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
- ✅ Per-spoke secrets: generate ONLY on first-time creation, fail if missing later
- ✅ Query Infisical API directly for idempotency when Hub K8s secret not needed
- ❌ Do NOT update Infisical before altering the DB (causes auth failure window)
- ❌ Do NOT use ESO sync as the rotation trigger
- ❌ Do NOT regenerate per-spoke passwords if missing from Infisical (requires manual intervention)
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

### Per-Spoke Secrets (Operator Uploads, ESO Creates - Pattern A2)

```yaml
apiVersion: external-secrets.io/v1
kind: ExternalSecret
metadata:
  name: crossplane-admin-credentials
  namespace: spoke-platform-ops
spec:
  secretStoreRef:
    name: infisical-secret-store
    kind: SecretStore
  target:
    name: crossplane-admin-credentials
    creationPolicy: Owner  # ESO creates and owns (no Hub K8s secret)
  data:
  - secretKey: password
    remoteRef:
      key: ${SPOKE_NAME}-crossplane-admin-password
```

**Note:** Hub Operator uploads to Infisical only. Spoke ESO creates the K8s secret.

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

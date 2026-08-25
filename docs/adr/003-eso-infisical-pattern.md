# ADR-003: Secret Management Architecture

**Date:** 2026-06-08 (rewritten)
**Status:** Active (rewritten — supersedes original ADR-003 and `bootstrap-vs-application-secrets.md`)

## Context

The platform manages secrets across multiple resource classes: bootstrap secrets for infrastructure initialization, tenant passwords for database access, spoke infrastructure credentials for cross-cluster connectivity, and application secrets for workload consumption. Each follows the same fundamental lifecycle: generation, storage, delivery, consumption, and rotation.

The original ADR-003 organized secret management around "who uploads what" (Pattern A/A2/A2a/A2b/B) rather than the underlying concerns that govern all secrets. The `bootstrap-vs-application-secrets.md` document addressed a specific ESO ownership conflict but accepted dual ownership as a necessary compromise. This rewrite consolidates both documents and restructures secret management around the five universal concerns.

All ownership assignments reference ADR-039. All controller responsibilities reference ADR-041. Day-0 vs Day-1 classification references ADR-040.

## Decision

### 1. Generation

Secret material is generated once by the authorized Generator defined in ADR-039.

| Secret Class | Generator | Phase | Idempotency |
|---|---|---|---|
| Bootstrap Secrets | CLI | Day-0 | CLI checks Infisical before generation; skips if exists |
| Application Secrets | Human / Infisical UI | Day-1+ | Manual — Infisical UI rejects duplicates |
| Tenant Passwords | Kube-SBT | Day-1+ | Tenant provisioning controller queries Infisical; generates only if missing and first-time |
| Spoke Infrastructure Credentials | Hub Operator | Day-1+ | Hub Operator queries Infisical; generates only if missing |
| Database Passwords (ESO-generated) | ESO (via Generator resources) | Day-1+ | ESO Generator resources handle idempotency |

**Constraints:**
- Generation is a one-time event. The Generator is distinct from the Lifecycle Owner — see ADR-039.
- All generated secrets use full-entropy cryptographically random values. No entropy weakening for URI formatting. Consumers apply URL-encoding at injection time.
- If a previously generated secret is missing from the System of Record, the Generator must fail reconciliation — it must never silently regenerate a secret that may be in use.

### 2. Storage

All secrets are stored in Infisical. Infisical is the System of Record for all secret material in the platform.

| Infisical Project | Type | Purpose |
|---|---|---|
| `hub-secrets` | `secret-manager` | Application secrets, bootstrap secrets, database credentials, infrastructure tokens |
| `hub-platform` | `cert-manager` | PKI infrastructure: Fleet Intermediate CA, certificate profiles |

**Constraints:**
- No Kubernetes Secret may serve as a secret's System of Record. Kubernetes Secrets are a delivery cache, not an authoritative store.
- Secrets must never be stored in Git, ConfigMaps, environment variables, or container images.

**PushSecret is banned. No exception.**

Two distinct layers enforce this ban:

1. **Technical (hard block):** ESO's Infisical provider throws `not implemented` for PushSecret. Infisical's API requires `workspaceId` + `environmentSlug` that ESO's generic PushSecret interface cannot dynamically map. The `InfisicalPushSecret` CRD and `infisical-operator` exist but are architecturally excluded for value authoring (see below).

2. **Architectural (ownership inversion):** Even if PushSecret were technically supported, the pattern inverts the ownership model: Kubernetes becomes the origin, Infisical becomes the mirror. This violates the invariant that Infisical is the System of Record. The correct flow is: generation authority writes directly to Infisical (API), ESO pulls to all consumers.

### 3. Delivery

ESO is the sole delivery mechanism for secrets from Infisical to Kubernetes. All ExternalSecrets reference the `hub-secrets` ClusterSecretStore.

| Secret Class | ExternalSecret `creationPolicy` | Rationale |
|---|---|---|
| Bootstrap Secrets | `Merge` | The CLI creates the initial Kubernetes Secret during Day-0. ESO keeps it synchronized with Infisical without taking ownership. |
| Application Secrets | `Owner` | ESO creates and owns the Kubernetes Secret. Infisical is the only source. |
| Tenant Passwords | `Owner` | ESO creates the Kubernetes Secret on the Spoke cluster. No Hub Kubernetes Secret exists. |
| Spoke Infrastructure Credentials | `Owner` | ESO creates the Kubernetes Secret on the Spoke cluster. Credentials are stored in Infisical (System of Record). Lifecycle owned by Hub Operator per ADR-039. |

**Spoke Delivery Path:**

Spoke clusters pull secrets from Hub Infisical via their own ESO instance and a namespaced SecretStore referencing the Hub Infisical backend. No secrets traverse Crossplane Composition Functions. No secrets are embedded in ClusterResourceSet payloads. See ADR-032 and ADR-035 for certificate and trust distribution (which follow different paths).

### 4. Consumption

Consumers read secrets from Kubernetes Secrets mounted as volumes or environment variables. They never access Infisical directly.

| Consumer | Secret Class | Consumption Method |
|---|---|---|
| CNPG | Bootstrap Secrets | Kubernetes Secret reference in Cluster spec |
| Infisical (self-hosted) | Bootstrap Secrets | Kubernetes Secret reference in Deployment |
| Crossplane provider-sql | Application / Tenant / Spoke Secrets | Kubernetes Secret reference in ProviderConfig or Role CR |
| Workloads (Ory, SPIRE, etc.) | Application Secrets | Kubernetes Secret mounted as volume or env |
| Tenant Applications | Tenant Passwords | Kubernetes Secret mounted as volume |
| Operators | Application Secrets | Kubernetes Secret reference |

**Constraints:**
- Consumers must not cache credentials indefinitely. Workloads must support rolling updates triggered by secret changes (via Stakater Reloader annotations or equivalent).
- Consumers of password values in DSN URLs must apply URL-encoding at injection time (`urlquery`). The stored secret must never be pre-encoded.
- Where a consumer requires a DSN URL, the Kubernetes Secret must also project individual components (`host`, `port`, `username`, `password`, `dbname`) alongside the opaque URL.

### 5. Rotation

Rotation follows a dual-phase model: the managed system (database) is updated before the System of Record (Infisical). ESO delivers the updated credential to Kubernetes as a downstream step.

```
Phase 1 — Update managed system first (zero-downtime window opens)
  ↓
ALTER ROLE <user> WITH PASSWORD '<new-password>'  ← Database altered FIRST
  ↓
Both old and new passwords valid simultaneously (overlap period)
  ↓
Update Infisical with new password  ← System of Record updated
  ↓
ESO detects change, syncs new password to Kubernetes Secret
  ↓
Applications pick up new credential (rolling restart or secret reload)
  ↓
Phase 2 — Expire old credential (overlap period ends)
  ↓
Confirm all connections using new password
  ↓
Invalidate old password in database
  ↓
Zero-downtime rotation complete
```

**Why the database is updated first:** The database is the authoritative system for whether a credential works. Updating Infisical before the database creates an authentication failure window where ESO has synced the new password but the database still has the old one.

**Rotation triggers:**
- Infisical's built-in secret rotation scheduler.
- Manual trigger via Infisical UI/API.
- Continuous reconciliation via ESO `refreshInterval`.

**ESO's role in rotation:** ESO delivers the current active credential from Infisical to Kubernetes. It does not initiate or drive rotation. ESO's `refreshInterval` ensures delivery of an already-rotated credential; it is not a rotation trigger.

**Provider reconciliation:** Crossplane `provider-sql` continuously monitors the referenced Kubernetes Secret. When the password changes, provider-sql issues `ALTER ROLE ... PASSWORD` without dropping the role. See ADR-024 and ADR-030 for the closed-loop rotation lifecycle.

## Boundary Rules

- Infisical is the System of Record for all secrets. No exception.
- ESO is the sole delivery mechanism from Infisical to Kubernetes. No exception.
- The dual-phase rotation pattern (database first, Infisical second) applies to all database credential rotation. No exception.
- PushSecret is banned. No exception. ESO's Infisical provider does not support PushSecret (`not implemented`), and the `InfisicalPushSecret` CRD is architecturally excluded for value authoring.
- Secrets must never be generated by a component that does not own the secret class (per ADR-039).
- Secrets must never be delivered by any mechanism other than ESO (per ADR-041).
- Kubernetes Secrets are a delivery cache. No component may use a Kubernetes Secret as the System of Record for secret material.
- ConfigMaps must never contain sensitive data. Service DNS must be used for service discovery.

## ESO Configuration Reference

### Bootstrap Secrets (CLI creates, ESO syncs)

```yaml
apiVersion: external-secrets.io/v1
kind: ExternalSecret
spec:
  target:
    creationPolicy: Merge  # CLI creates, ESO updates from Infisical
```

### Application, Tenant, and Spoke Secrets (ESO creates)

```yaml
apiVersion: external-secrets.io/v1
kind: ExternalSecret
spec:
  target:
    creationPolicy: Owner  # ESO creates and owns the Kubernetes Secret
```

### Spoke Delivery (ESO on Spoke, no Hub Kubernetes Secret)

Spoke ESO pulls from Hub Infisical using a namespaced SecretStore. The Hub Operator stores the credential in Infisical directly. No Kubernetes Secret exists on the Hub for this credential class.

```yaml
apiVersion: external-secrets.io/v1
kind: ExternalSecret
metadata:
  namespace: platform-ops
spec:
  secretStoreRef:
    name: infisical-secret-store
    kind: SecretStore
  target:
    creationPolicy: Owner
```

## Consequences

### Positive

- Single concern-based model replaces four named patterns (A, A2a, A2b, B) with universal rules.
- Every secret class in the platform can be described by the same five concerns, regardless of the Generator or Consumer.
- Dual ownership is eliminated: each secret class has exactly one Generator, one System of Record, and one Lifecycle Owner.
- Rotation is a first-class concern with a single, consistent dual-phase pattern.

### Negative

- The model is more abstract than the original pattern-based approach. Developers implementing secret flows must map their secret class to the five concerns rather than selecting a named pattern.
- The dual-phase rotation pattern requires every database credential consumer to implement connection retry logic during the overlap period.

## References

- ADR-039: Platform Ownership Model
- ADR-040: Day-0 vs Day-1 Lifecycle Boundary
- ADR-041: Controller Responsibility Matrix
- ADR-024: Crossplane Password Rotation
- ADR-030: Autonomous Credential Rotation Lifecycle
- ADR-035: Enterprise PKI and Delegated Trust

## Impact

This rewrite supersedes the original ADR-003 (ESO-Infisical Integration Pattern) and the `bootstrap-vs-application-secrets.md` document. Both are retained for historical reference but must not be treated as current architecture.

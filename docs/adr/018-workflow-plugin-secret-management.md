# ADR-0018: Workflow Plugin Secret Management with Infisical Agent Injector

**Date:** 2026-05-06  
**Status:** Superseded by [ADR-019: Waypoint Shared SaaS Platform Service](./019-waypoint-shared-saas-platform-service.md)

> **Note:** The init-mode injection pattern described here was designed for a per-tenant pod topology. With Waypoint operating as a shared SaaS service (one SDK pod per cell), static pod-startup injection cannot support per-tenant credential isolation. Plugin credential resolution has moved to runtime context resolution via Infisical SDK, as specified in ADR-019. The Infisical path convention (`/spoke-pool/{cellId}/tenants/{tenantId}/plugins/*`) and the Machine Identity per-tenant model remain valid and are carried forward in ADR-019.

See also: [ADR-003: ESO-Infisical Pattern](./003-eso-infisical-pattern.md), [ADR-009: Platform Security Architecture](./009-platform-security-architecture.md), [ADR-019: Waypoint Shared SaaS Platform Service](./019-waypoint-shared-saas-platform-service.md)

## Context

The Waypoint Builder workflow engine executes tenant-defined workflows that invoke third-party plugins (OpenAI, Slack, Stripe, etc.). These plugins require API credentials to function. The platform must deliver plugin secrets to workflow execution pods while maintaining:

1. **Zero application changes:** OSS plugins expect `process.env.OPENAI_API_KEY` without modification
2. **Per-tenant isolation:** Tenant A cannot access Tenant B's plugin credentials
3. **Zero etcd persistence:** Secrets must not be stored in Kubernetes Secrets (etcd)
4. **Workload identity:** Leverage existing SPIFFE/SPIRE infrastructure
5. **Secret rotation:** Support credential updates without platform downtime
6. **Auditability:** Track which tenant accessed which secrets

### Challenge

The Vercel Workflows SDK propagates all `process.env` variables from host Node.js process into VM sandbox (`creator/archived/workflow/packages/core/src/vm/index.ts` lines 95-97). This creates a security risk: exposing all container environment variables to workflow code violates least privilege principles.

## Decision

We adopt **Infisical Agent Injector** in init mode to deliver plugin secrets to Waypoint SDK pods.

### Architecture Pattern

```
[ Infisical ] ← SOURCE OF TRUTH
   ↓ (SPIFFE workload identity auth)
[ Agent Injector Webhook ] ← Mutating admission controller
   ↓ (patches pod spec on CREATE events)
[ Waypoint SDK Pod ]
   ├─ Init Container: Infisical Agent
   │    ↓ (authenticates via service account token)
   │    ↓ (fetches secrets from /tenants/{tenant-id}/plugins/*)
   │    ↓ (renders to /infisical/secrets file)
   └─ Main Container: Workflow Runtime
        ↓ (export $(cat /infisical/secrets | xargs))
        ↓ (process.env.OPENAI_API_KEY populated)
[ OSS Plugin ] ← Reads process.env unchanged
```

### Pattern: Agent Injector Init Mode

**Infisical Agent Injector** is a Kubernetes mutating webhook that:
- Intercepts pod CREATE events
- Patches pod spec to add init container when `org.infisical.com/inject: "true"` annotation present
- Init container authenticates to Infisical using pod's service account token
- Fetches secrets and renders to shared volume mount
- Main container sources secrets into `process.env` at startup

**Init mode** (vs sidecar mode):
- Agent runs once as init container, not long-lived sidecar
- Secrets fetched at pod startup only
- Lower resource overhead (no continuous polling)
- Suitable for ephemeral workflow execution model

### Per-Tenant Isolation

**Enforcement Layers:**

1. **Namespace Isolation:** Each tenant runs in dedicated namespace (`tenant-{id}`) with dedicated Waypoint SDK Deployment (one Graphile Worker pod per tenant)
2. **Infisical Machine Identity:** One per tenant, configured with Kubernetes Auth:
   - Allowed Namespace: `tenant-{id}`
   - Allowed Service Account: `spoke-worker`
   - Allowed Secret Path: `/spoke-pool/{cell-id}/tenants/{tenant-id}/plugins/*`
3. **ConfigMap Scoping:** One ConfigMap per tenant in tenant namespace, defines secret template
4. **SPIFFE Validation:** Workload identity cryptographically validates namespace + service account

**Result:** Tenant A's Waypoint SDK pod cannot authenticate to fetch Tenant B's secrets due to Machine Identity path restrictions and namespace isolation.

**Critical:** One Graphile Worker pod per tenant ensures `process.env` isolation. Tenant A and Tenant B never share a pod, preventing cross-tenant secret leakage.

### Secret Path Convention

**Infisical Structure:**
```
/spoke-pool/{cell-id}/tenants/{tenant-id}/plugins/
  OPENAI_API_KEY: sk-proj-...
  SLACK_API_TOKEN: xoxb-...
  STRIPE_SECRET_KEY: sk_live_...
```

**Example:** Tenant `app-creator` in cell `spoke-pool-eu-prod-01`:
```
/spoke-pool/spoke-pool-eu-prod-01/tenants/app-creator/plugins/OPENAI_API_KEY
```

### Secret Rotation

**Rotation Model:** Pod restart-based rotation with Stakater Reloader

**Architecture Constraint:** Waypoint SDKs run Graphile Worker as long-running daemon (not ephemeral pods per workflow). The Vercel VM freezes `process.env` at context creation. Secrets loaded at pod startup cannot be hot-reloaded during pod lifetime.

**Flow:**
```
Secret updated in Infisical
   ↓
Infisical Agent Injector syncs to K8s Secret (temporary mount)
   ↓
Stakater Reloader detects Secret change
   ↓
Reloader triggers rolling restart of Waypoint SDK Deployment
   ↓
New pod starts → Init container fetches updated secrets
   ↓
New pod exports secrets to process.env
   ↓
Graphile Worker daemon starts with fresh secrets
   ↓
In-flight workflows on old pod complete gracefully
   ↓
Old pod terminates after drain period
```

**Dual-Phase Rotation:** Follows Infisical's dual-phase rotation pattern (ADR-003):
- Phase 1: Update secret in Infisical, overlap period where both old and new credentials valid
- Phase 2: Rolling restart ensures zero downtime, old workflows complete on old pods
- Phase 3: Expire old credential after all pods restarted

**Stakater Reloader Configuration:**
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: spoke-worker
  namespace: tenant-app-creator
  annotations:
    reloader.stakater.com/auto: "true"  # Auto-restart on Secret change
```

**Rationale:** Graphile Worker is long-running daemon, not ephemeral. Pod restarts are required for secret rotation. Reloader automates this with zero downtime via rolling updates.

### Pod Configuration

**Waypoint SDK Deployment:**
```yaml
metadata:
  annotations:
    org.infisical.com/inject: "true"
    org.infisical.com/inject-mode: "init"
    org.infisical.com/agent-config-map: "tenant-{id}-plugin-secrets"
spec:
  serviceAccountName: spoke-worker
  containers:
    - name: workflow-runtime
      command: ["/bin/sh", "-c"]
      args:
        - |
          export $(cat /infisical/secrets | xargs)
          exec node spoke-worker.js
```

**ConfigMap Template (per tenant):**
```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: tenant-app-creator-plugin-secrets
  namespace: tenant-app-creator
data:
  config.yaml: |
    infisical:
      address: "https://app.infisical.com"
      auth:
        type: "kubernetes"
        config:
          identity-id: "machine-identity-for-app-creator"
    templates:
      - destination-path: "/infisical/secrets"
        template-content: |
          {{- with secret "project-app-creator" "prod" "/plugins" }}
          {{- range . }}
          {{ .Key }}={{ .Value }}
          {{- end }}
          {{- end }}
```

## Consequences

### Positive

**Security:**
- **Zero etcd persistence:** Secrets never stored in Kubernetes Secrets, only in Infisical and pod memory
- **Workload identity:** SPIFFE-based authentication, no static credentials
- **Per-tenant isolation:** Machine Identity path restrictions + namespace isolation
- **Least privilege:** Each tenant receives only their plugin secrets
- **Auditability:** Infisical logs all secret access with SPIFFE workload identity

**Operational:**
- **Zero application changes:** OSS plugins work unchanged, read `process.env` as-is
- **OOB solution:** No custom secret management code, uses industry-standard pattern
- **Scalable:** 1 ConfigMap per tenant (not per plugin), 1 Machine Identity per tenant
- **Rotation-ready:** Dual-phase rotation via pod lifecycle ensures zero downtime
- **Low overhead:** Init mode (not sidecar) minimizes resource consumption

**Industry Alignment:**
- **Standard pattern:** Same approach as Vault Agent Injector (HashiCorp), AWS Secrets Manager CSI
- **CNCF ecosystem:** Integrates with existing SPIFFE/SPIRE infrastructure
- **Modern 2026 practice:** Workload identity + secret injection is enterprise standard

### Trade-offs

- **Pod restart required for rotation:** Secrets not updated during pod lifetime, requires Stakater Reloader to trigger rolling restart (acceptable for long-running Graphile Worker model)
- **Webhook dependency:** Requires Infisical Agent Injector deployed to cluster
- **ConfigMap proliferation:** One ConfigMap per tenant (manageable via tenant provisioning automation)
- **Infisical dependency:** Waypoint SDK pods cannot start if Infisical unavailable (mitigated by Infisical HA deployment)
- **Reloader dependency:** Requires Stakater Reloader for automated pod restarts on secret changes

## Boundary Rules

- ✅ Use Infisical Agent Injector for plugin secrets delivery
- ✅ Use init mode (not sidecar mode) for Waypoint SDK pods
- ✅ One Machine Identity per tenant with path-scoped access
- ✅ One ConfigMap per tenant in tenant namespace
- ✅ One Graphile Worker pod per tenant (dedicated Deployment per tenant namespace)
- ✅ Secret rotation via Stakater Reloader triggering rolling pod restarts
- ✅ Leverage existing SPIFFE/SPIRE workload identity infrastructure
- ✅ Sync secrets to temporary K8s Secret for Reloader detection
- ❌ Do NOT store plugin secrets in Kubernetes Secrets permanently (only temporary mount for Reloader)
- ❌ Do NOT use sidecar mode (unnecessary overhead, init mode sufficient)
- ❌ Do NOT create per-plugin Machine Identities (does not scale)
- ❌ Do NOT modify OSS plugin code to read secrets differently
- ❌ Do NOT share Waypoint SDK pods across tenants (breaks process.env isolation)

## References

- [Infisical Kubernetes Agent Injector Documentation](https://infisical.com/docs/integrations/platforms/kubernetes-injector)
- [Infisical Kubernetes Auth](https://infisical.com/docs/documentation/platform/identities/kubernetes-auth)
- [Stakater Reloader](https://github.com/stakater/Reloader)
- [Vault Agent Injector vs CSI Provider (HashiCorp)](https://developer.hashicorp.com/vault/docs/deploy/kubernetes/injector-csi)
- [ADR-003: ESO-Infisical Pattern](./003-eso-infisical-pattern.md)
- [ADR-009: Platform Security Architecture](./009-platform-security-architecture.md)
- [ADR-017: Workflow Engine Tenant Isolation](./017-workflow-engine-tenant-isolation.md)
- [Vercel Workflows SDK VM Context](creator/archived/workflow/packages/core/src/vm/index.ts)
- [Graphile Worker Architecture](creator/archived/workflow/packages/world-postgres/src/queue.ts)

# ADR-0018: Workflow Plugin Secret Management with Infisical Agent Injector

**Date:** 2026-05-06  
**Status:** Accepted  
**Context:** Workflow Engine Plugin Secret Management

See also: [ADR-003: ESO-Infisical Pattern](./003-eso-infisical-pattern.md), [ADR-009: Platform Security Architecture](./009-platform-security-architecture.md), [ADR-017: Workflow Engine Tenant Isolation](./017-workflow-engine-tenant-isolation.md)

## Context

The Workhorse Blueprint Builder workflow engine executes tenant-defined workflows that invoke third-party plugins (OpenAI, Slack, Stripe, etc.). These plugins require API credentials to function. The platform must deliver plugin secrets to workflow execution pods while maintaining:

1. **Zero application changes:** OSS plugins expect `process.env.OPENAI_API_KEY` without modification
2. **Per-tenant isolation:** Tenant A cannot access Tenant B's plugin credentials
3. **Zero etcd persistence:** Secrets must not be stored in Kubernetes Secrets (etcd)
4. **Workload identity:** Leverage existing SPIFFE/SPIRE infrastructure
5. **Secret rotation:** Support credential updates without platform downtime
6. **Auditability:** Track which tenant accessed which secrets

### Challenge

The Vercel Workflows SDK propagates all `process.env` variables from host Node.js process into VM sandbox (`creator/archived/workflow/packages/core/src/vm/index.ts` lines 95-97). This creates a security risk: exposing all container environment variables to workflow code violates least privilege principles.

## Decision

We adopt **Infisical Agent Injector** in init mode to deliver plugin secrets to Spoke Worker pods.

### Architecture Pattern

```
[ Infisical ] ← SOURCE OF TRUTH
   ↓ (SPIFFE workload identity auth)
[ Agent Injector Webhook ] ← Mutating admission controller
   ↓ (patches pod spec on CREATE events)
[ Spoke Worker Pod ]
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

1. **Namespace Isolation:** Each tenant runs in dedicated namespace (`tenant-{id}`)
2. **Infisical Machine Identity:** One per tenant, configured with Kubernetes Auth:
   - Allowed Namespace: `tenant-{id}`
   - Allowed Service Account: `spoke-worker`
   - Allowed Secret Path: `/spoke-pool/{cell-id}/tenants/{tenant-id}/plugins/*`
3. **ConfigMap Scoping:** One ConfigMap per tenant in tenant namespace, defines secret template
4. **SPIFFE Validation:** Workload identity cryptographically validates namespace + service account

**Result:** Tenant A's Spoke Worker pod cannot authenticate to fetch Tenant B's secrets due to Machine Identity path restrictions and namespace isolation.

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

**Rotation Model:** Pod lifecycle-based rotation

**Flow:**
```
Secret updated in Infisical
   ↓
Existing workflow runs continue with old secret (in-flight pods)
   ↓
New workflow run triggered → New pod created
   ↓
Init container fetches updated secret from Infisical
   ↓
New pod uses rotated secret
   ↓
Old pods complete and terminate
```

**Dual-Phase Rotation:** Follows Infisical's dual-phase rotation pattern (ADR-003):
- Phase 1: Update secret in Infisical, overlap period where both old and new credentials valid
- Phase 2: Expire old credential after all in-flight workflows complete

**Rationale:** Workflow executions are ephemeral (minutes to hours). Each workflow run spawns fresh Spoke Worker pod. Secret rotation happens naturally via pod lifecycle, not runtime updates.

### Pod Configuration

**Spoke Worker Deployment:**
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

- **Init-only rotation:** Secrets not updated during pod lifetime, requires new pod for rotated secrets (acceptable for ephemeral workflow model)
- **Webhook dependency:** Requires Infisical Agent Injector deployed to cluster
- **ConfigMap proliferation:** One ConfigMap per tenant (manageable via tenant provisioning automation)
- **Infisical dependency:** Spoke Worker pods cannot start if Infisical unavailable (mitigated by Infisical HA deployment)

## Boundary Rules

- ✅ Use Infisical Agent Injector for plugin secrets delivery
- ✅ Use init mode (not sidecar mode) for workflow execution pods
- ✅ One Machine Identity per tenant with path-scoped access
- ✅ One ConfigMap per tenant in tenant namespace
- ✅ Secret rotation via pod lifecycle (new workflow run = new pod = fresh secrets)
- ✅ Leverage existing SPIFFE/SPIRE workload identity infrastructure
- ❌ Do NOT store plugin secrets in Kubernetes Secrets (etcd)
- ❌ Do NOT use sidecar mode (unnecessary overhead for ephemeral workflows)
- ❌ Do NOT create per-plugin Machine Identities (does not scale)
- ❌ Do NOT modify OSS plugin code to read secrets differently

## References

- [Infisical Kubernetes Agent Injector Documentation](https://infisical.com/docs/integrations/platforms/kubernetes-injector)
- [Infisical Kubernetes Auth](https://infisical.com/docs/documentation/platform/identities/kubernetes-auth)
- [Vault Agent Injector vs CSI Provider (HashiCorp)](https://developer.hashicorp.com/vault/docs/deploy/kubernetes/injector-csi)
- [ADR-003: ESO-Infisical Pattern](./003-eso-infisical-pattern.md)
- [ADR-009: Platform Security Architecture](./009-platform-security-architecture.md)
- [ADR-017: Workflow Engine Tenant Isolation](./017-workflow-engine-tenant-isolation.md)
- [Vercel Workflows SDK VM Context](creator/archived/workflow/packages/core/src/vm/index.ts)

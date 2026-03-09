## Phase 2 — K8s Provisioning Integration
**Duration: 1 week | Risk: Medium**

Add the Kubernetes side-effects to the API handlers you built in Phase 1. This is the highest-risk integration because K8s SSA, Informer caches, and RBAC are all non-trivial.

Deliverables:
- `SharedInformerFactory` setup — namespace cache, never sync `Get()` on request path
- On `POST /api/v1/tenants` success: SSA apply of `Namespace`, `ResourceQuota`, `RoleBinding`, `ServiceAccount` — in that order, each idempotent
- Plan → quota mapping (Free/Pro/Enterprise hardcoded defaults, overridable via `quotas` object in payload)
- On `DELETE`: add finalizer to namespace, set `status=deleting` in DB
- K8s integration tests using `envtest` (controller-runtime's local API server) — no real cluster needed in CI
- The `kubectl` verification commands from PRD section 7 pass against a local `kind` cluster

**Critical note:** The `ServiceAccount` token is **not** returned in this response. You provision the SA here. Token issuance is Phase 3. This keeps provisioning idempotent and secrets out of logs.

**Exit criteria:** Full agentic onboarding works end-to-end (minus real auth and minus token in response) against a local `kind` cluster. The three `kubectl` verification commands from the PRD pass.
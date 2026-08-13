# ADR 004: Favor Declarative Operator State Management over Imperative Initialization Jobs

**Date:** 2023-10-26 *(Update to current date)*  
**Status:** Accepted  
**Authors:** Platform Engineering Team  

## Context

During the rollout of the Phase 3 Spoke Cluster database infrastructure, the GitOps pipeline (ArgoCD) entered a hard deadlock. 

The architecture relied on an imperative Kubernetes `Job` (using an ArgoCD Sync Hook) to run `psql` commands to bootstrap the `crossplane_admin` database role. The Job failed due to idempotency and permission issues (`ALTER ROLE` on a role it didn't own). Because it was a Sync Hook, it blocked ArgoCD from syncing the rest of the application, including the `ExternalSecret` required to authenticate Crossplane. The missing secret caused Crossplane to fail, resulting in a circular dependency and total cluster deployment failure.

This highlighted a broader anti-pattern: **mixing imperative, script-based initialization with declarative GitOps reconciliation.**

Kubernetes and GitOps inherently expect **eventual consistency**. When we use scripts (Bash, Python, `psql`, `kubectl`) inside initialization `Jobs` to manage state, we bypass the Kubernetes control loop. These scripts are rarely fully idempotent, handle edge cases poorly, and create brittle deployment ordering dependencies (Sync Waves/Hooks) that break the self-healing nature of the platform.

## Decision

Moving forward, the Platform team will adhere to the following architectural rules for infrastructure provisioning:

### 1. Ban Imperative State Management via Jobs
We will no longer use Kubernetes `Jobs` running CLI tools (`psql`, `curl`, `kubectl`, `aws`) to initialize or mutate state inside the cluster. Any required state (roles, permissions, schemas, API tokens) must be managed declaratively via Kubernetes Custom Resources (CRs).

### 2. Utilize Native Operator Capabilities
Before writing custom logic to configure a service, engineers must exhaust the declarative API capabilities of the underlying Operator. 
*   *Example:* Instead of a Job running `CREATE ROLE`, use CloudNativePG's `spec.managed.roles`.
*   *Example:* Instead of a Job running `CREATE DATABASE`, use Crossplane's `Provider-SQL` Database Managed Resource.

### 3. Design for Eventual Consistency, not Imperative Ordering
We will minimize the use of ArgoCD Sync Waves and Hooks. Resources should be deployed simultaneously. If Resource A depends on Resource B (e.g., an Operator depends on a Secret created by External Secrets), Resource A must be allowed to fail gracefully and continuously retry in its control loop until Resource B exists. 

### 4. Prefer Native Ownership over Complex Grants
When provisioning multi-tenant systems, prefer using the system's native ownership models over complex, finely-grained permission scripts.
*   *Example:* For PostgreSQL, make the tenant user the native `Owner` of the database via the CNPG `Database` CR, rather than maintaining a brittle list of `GRANT ALL ON TABLES...` statements. This ensures future schemas and tables automatically inherit the correct permissions.

## Ownership

This ADR defines a design principle (declarative over imperative) and does not own specific platform resources. For resource ownership, see ADR-039.

## Consequences

### Positive
*   **Self-Healing Infrastructure:** If a database role is deleted manually, the CNPG operator will recreate it. An initialization job would not.
*   **Elimination of Deadlocks:** Removing sync-wave dependencies allows ArgoCD to apply all resources immediately, eliminating the risk of a single failing script blocking secret generation or networking updates.
*   **Idempotency Guarantee:** Kubernetes controllers are strictly idempotent by design. We no longer need to write `IF NOT EXISTS` bash logic.
*   **Cleaner Codebase:** Deletion of fragile shell scripts embedded inside YAML strings.

### Negative
*   **Learning Curve:** Engineers cannot fall back to familiar bash scripts to solve quick problems. They must spend time reading Operator documentation to find the "declarative way" to achieve their goals.
*   **Feature Gaps:** Occasionally, an Operator may not support a specific niche configuration. In these rare cases, a Custom Crossplane Composition Function or a lightweight custom controller should be evaluated before falling back to an imperative Job.

## Code Examples: Good vs. Bad

### ❌ Anti-Pattern (Rejected)
Imperative state management relying on sync ordering and scripts.
```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: db-init
  annotations:
    argocd.argoproj.io/sync-wave: "1" # Forces ordering, creates deadlocks
spec:
  template:
    spec:
      containers:
        - name: psql
          image: postgres:16
          command: ["/bin/sh", "-c"]
          # Brittle, hard to make idempotent, ignores GitOps drift
          args: ["psql -c \"ALTER ROLE admin CREATEDB;\" || true"] 
```

### ✅ Best Practice (Accepted)
Declarative state managed by a continuously reconciling control loop.
```yaml
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: shared-cnpg
spec:
  # Declarative, idempotent, self-healing, handles retry logic internally
  managed:
    roles:
      - name: admin
        ensure: present
        createdb: true
        passwordSecret:
          name: admin-credentials 
```

## Implementation Note: Crossplane Delivery (See ADR 005)

While this ADR mandates the use of declarative Custom Resources (like `provider-sql` Roles and Grants) over imperative Jobs, care must be taken in how these resources are delivered from the Hub to the Spoke.

As per **ADR 005 (Unified Abstraction Layers in Crossplane)**, when composing these declarative resources via Crossplane Pipeline mode for remote Spoke clusters, they MUST be wrapped in `kubernetes.crossplane.io/v1alpha2/Object` Managed Resources to prevent Hub-side schema resolution race conditions.

**Key Principle:** Declarative CRs are the correct pattern (ADR 004), but their delivery mechanism must account for Hub-Spoke topology constraints (ADR 005).
# ADR 008: Federated API Boundary Pattern for Crossplane Hub-and-Spoke

## Status
Accepted

## Date
2026-04-27

**Last Updated:** 2026-04-28 (Added Matryoshka Pattern clarification)

## Context

In [ADR 005: Unified Abstraction Layers with Crossplane](005-unified-abstraction-layers-crossplane.md), we established Crossplane as the primary abstraction layer for our control plane. This allowed us to shift from imperative scripts to declarative KRM (Kubernetes Resource Model) definitions.

Our initial implementation of the Hub-and-Spoke multi-cluster architecture used the Hub's Crossplane instance (via `provider-kubernetes`) to push all tenant resources down to the Spoke clusters. Currently, the Hub's `AINativeSaaS` Composition pushes over 10 raw Kubernetes primitives (Namespaces, CNPG `Pooler`s, `AtlasMigration` CRs, `Deployment`s, `Role`s, and `RoleBinding`s) directly to the remote Spoke.

This approach has revealed several architectural leaks and scaling bottlenecks:

1. **Security & RBAC Overreach:** The Hub's `provider-kubernetes` requires `cluster-admin` privileges on all Spoke clusters to manage these diverse primitive types.

2. **Coupled Blast Radius:** Upgrading a tenant component (e.g., bumping the PostgREST image version) requires modifying the Hub's global Composition, which instantly affects all tenants across all Spokes globally. We cannot do progressive, per-cell rollouts.

3. **Control Plane Bloat:** The Hub's ETCD and Crossplane memory footprints grow linearly with every low-level resource it tracks on remote clusters.

4. **Complex Status Aggregation:** The Hub must use complex JSON paths to parse readiness states from disparate remote objects to calculate if a tenant is truly "Ready."

5. **Race Conditions:** When the Hub pushes multiple XRs to the Spoke in parallel (e.g., `TenantDatabase` and `SpokeTenantEnvironment`), there is no guaranteed ordering. Resources may attempt to create objects in namespaces that don't exist yet, causing intermittent failures.

## Decision

We will implement the **Federated API Boundary Pattern** (also known as the Remote XR Pattern) combined with the **Matryoshka (Nested XR) Pattern** to decouple Intent (Hub) from Implementation (Spoke) and enforce dependency ordering.

### Core Principles

1. **Single API Boundary:** We will define a new Crossplane XRD named `SpokeTenantEnvironment` and deploy it to the **Spoke clusters**. This acts as the API contract between the Hub and the Spoke.

2. **Strict Separation of Concerns:** ArgoCD handles KRM (Kubernetes Resource Model) primitives; Crossplane handles Infrastructure.
   - **ArgoCD exclusively owns:** Namespaces, RBAC (ServiceAccounts, Roles, RoleBindings), ConfigMaps, ResourceQuotas, Services, Deployments
   - **Crossplane exclusively owns:** External infrastructure (Databases, Storage, IAM), Custom Resources (CNPG, Atlas), Composite Resources (XRs)
   - **Why:** Prevents "split-brain" race conditions between GitOps and Control Plane systems. Each system has a clear, non-overlapping domain.

3. **Spoke-Local Composition:** We will migrate all low-level infrastructure manifests (CNPG Poolers, Deployments) from the Hub's `AINativeSaaS` composition into a Spoke-local `SpokeTenantEnvironment` Composition. Note: Namespaces and RBAC are excluded from this Composition as they are managed by ArgoCD.

4. **Hub Simplification (Matryoshka Pattern):** The Hub's `AINativeSaaS` composition will be refactored to use `provider-kubernetes` to push exactly **ONE** object to the Spoke: the `SpokeTenantEnvironment` Custom Resource.

   **Critical:** The Hub will **NOT** push multiple XRs (e.g., `TenantDatabase` + `SpokeTenantEnvironment`) to avoid race conditions. Instead, the `SpokeTenantEnvironment` Composition on the Spoke will **compose nested XRs** (like `TenantDatabase`) internally, guaranteeing dependency ordering.

5. **Nested XR Composition:** The Spoke's `SpokeTenantEnvironment` Composition will compose other XRs (not just managed resources), following the Matryoshka pattern:
   ```
   SpokeTenantEnvironment (XR)
   ├── TenantDatabase (nested XR) ← composed inside
   ├── AtlasMigration (managed resource)
   └── PostgREST Deployment (managed resource)
   ```

   **Critical:** Namespace is NOT managed by this Composition. Namespaces are created exclusively by ArgoCD via the Universal Tenant Helm Chart before the XR is created.

6. **Status Reflection:** The Spoke-local composition will aggregate the health of its internal resources (including nested XRs) and expose a single `status.ready` boolean. The Hub will observe only this field.

This decision *Amends* ADR-005 by clarifying that:
- Crossplane abstractions must be distributed and scoped to their respective clusters
- XRs should compose other XRs (not just managed resources) to enforce dependency ordering
- The Hub should push exactly one XR per tenant to each Spoke

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Infrastructure XRs | Kubernetes API | Crossplane | Crossplane | Spokes, Tenants | Day-1+ |
| Kubernetes Resources | Git | ArgoCD | ArgoCD | Platform, Tenants | Day-1+ |

See ADR-039 for the complete ownership matrix.

## Consequences

### Positive

* **Principle of Least Privilege:** The Hub's `provider-kubernetes` will only require RBAC permissions to `CREATE/UPDATE/PATCH` the `spoketenantenvironments.nutgraf.in` resource on the Spokes, completely eliminating the need for remote `cluster-admin` access.

* **Progressive Rollouts:** Platform engineering can update the `SpokeTenantEnvironment` Composition on a single Spoke (Cell) via ArgoCD, test it, and then roll the infrastructure upgrade out progressively.

* **Hub Scalability:** Hub ETCD footprint and memory usage will drop by ~90% for tenant workloads, as the Hub only watches one object per tenant instead of 10+.

* **Simplified Compositions:** Crossplane Compositions become highly readable and maintainable.

* **Race Condition Prevention:** By composing nested XRs (like `TenantDatabase`) inside the `SpokeTenantEnvironment` Composition, we guarantee that prerequisite resources (like Namespaces) are created before dependent resources attempt to use them. Crossplane's reconciliation loop ensures parent resources are ready before child resources are created.

* **Atomic Operations:** Deleting a `SpokeTenantEnvironment` XR automatically cascades to all nested XRs and managed resources, ensuring clean teardown without orphaned resources.

* **Single Status Field:** The Hub observes a single `status.ready` field on the `SpokeTenantEnvironment` XR, simplifying monitoring and alerting. No need to aggregate status from multiple disparate resources.

* **Alignment with Crossplane Philosophy:** The Matryoshka pattern (composing XRs within XRs) is the canonical Crossplane design pattern, used by AWS EKS Blueprints, Azure Landing Zones, GCP Foundations, and Red Hat Hosted Control Planes.

### Negative / Risks

* **Distribution Complexity:** We must now ensure Spoke-local XRDs and Compositions are reliably distributed to Spokes before the Hub attempts to create a tenant there.
  
  *Mitigation: We already utilize ArgoCD ApplicationSets (`platform-spoke-catalog-appsets.yaml`) which can safely orchestrate the delivery of these XRs to Spoke pools. The edge catalog deployment (Wave 0-4) ensures all XRDs are installed before tenant provisioning begins.*

* **Refactoring Effort:** Requires rewriting the existing `ainativesaas-starter-hetzner.yaml` Composition and migrating its contents to new manifests in the `spoke-catalog`.

  *Mitigation: This is a one-time refactoring effort that pays dividends in operational simplicity and scalability. The refactoring can be done incrementally, starting with a single Spoke Pool for testing.*

* **Nested XR Debugging:** When a nested XR (like `TenantDatabase`) fails, operators must inspect both the parent `SpokeTenantEnvironment` and the child `TenantDatabase` to diagnose issues.

  *Mitigation: Crossplane's status conditions propagate from child to parent. The `SpokeTenantEnvironment` status will reflect "TenantDatabase not ready" with a reference to the failing child XR. Observability tooling (Prometheus metrics, ArgoCD health checks) will surface these failures.*
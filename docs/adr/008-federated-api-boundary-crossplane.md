# ADR 008: Federated API Boundary Pattern for Crossplane Hub-and-Spoke

## Status
Accepted

## Date
2026-04-27

## Context

In [ADR 005: Unified Abstraction Layers with Crossplane](005-unified-abstraction-layers-crossplane.md), we established Crossplane as the primary abstraction layer for our control plane. This allowed us to shift from imperative scripts to declarative KRM (Kubernetes Resource Model) definitions.

Our initial implementation of the Hub-and-Spoke multi-cluster architecture used the Hub's Crossplane instance (via `provider-kubernetes`) to push all tenant resources down to the Spoke clusters. Currently, the Hub's `AINativeSaaS` Composition pushes over 10 raw Kubernetes primitives (Namespaces, CNPG `Pooler`s, `AtlasMigration` CRs, `Deployment`s, `Role`s, and `RoleBinding`s) directly to the remote Spoke.

This approach has revealed several architectural leaks and scaling bottlenecks:

1. **Security & RBAC Overreach:** The Hub's `provider-kubernetes` requires `cluster-admin` privileges on all Spoke clusters to manage these diverse primitive types.

2. **Coupled Blast Radius:** Upgrading a tenant component (e.g., bumping the PostgREST image version) requires modifying the Hub's global Composition, which instantly affects all tenants across all Spokes globally. We cannot do progressive, per-cell rollouts.

3. **Control Plane Bloat:** The Hub's ETCD and Crossplane memory footprints grow linearly with every low-level resource it tracks on remote clusters.

4. **Complex Status Aggregation:** The Hub must use complex JSON paths to parse readiness states from disparate remote objects to calculate if a tenant is truly "Ready."

## Decision

We will implement the **Federated API Boundary Pattern** (also known as the Remote XR Pattern) to decouple Intent (Hub) from Implementation (Spoke).

1. **New API Boundary:** We will define a new Crossplane XRD named `SpokeTenantEnvironment` (or similar) and deploy it to the **Spoke clusters**. This acts as the API contract between the Hub and the Spoke.

2. **Spoke-Local Composition:** We will migrate all low-level infrastructure manifests (Namespaces, CNPG Poolers, Deployments, RBAC) from the Hub's `AINativeSaaS` composition into a Spoke-local `SpokeTenantEnvironment` Composition.

3. **Hub Simplification:** The Hub's `AINativeSaaS` composition will be refactored to use `provider-kubernetes` to push exactly **one** object to the Spoke: the `SpokeTenantEnvironment` Custom Resource.

4. **Status Reflection:** The Spoke-local composition will aggregate the health of its internal resources and expose a single `status.ready` boolean. The Hub will observe only this field.

This decision *Amends* ADR-005 by clarifying that Crossplane abstractions must be distributed and scoped to their respective clusters.

## Consequences

### Positive

* **Principle of Least Privilege:** The Hub's `provider-kubernetes` will only require RBAC permissions to `CREATE/UPDATE/PATCH` the `spoketenantenvironments.nutgraf.in` resource on the Spokes, completely eliminating the need for remote `cluster-admin` access.

* **Progressive Rollouts:** Platform engineering can update the `SpokeTenantEnvironment` Composition on a single Spoke (Cell) via ArgoCD, test it, and then roll the infrastructure upgrade out progressively.

* **Hub Scalability:** Hub ETCD footprint and memory usage will drop by ~90% for tenant workloads, as the Hub only watches one object per tenant instead of 10+.

* **Simplified Compositions:** Crossplane Compositions become highly readable and maintainable.

### Negative / Risks

* **Distribution Complexity:** We must now ensure Spoke-local XRDs and Compositions are reliably distributed to Spokes before the Hub attempts to create a tenant there.
  
  *Mitigation: We already utilize ArgoCD ApplicationSets (`platform-spoke-catalog-appsets.yaml`) which can safely orchestrate the delivery of these XRs to Spoke pools.*

* **Refactoring Effort:** Requires rewriting the existing `ainativesaas-starter-hetzner.yaml` Composition and migrating its contents to new manifests in the `spoke-catalog`.

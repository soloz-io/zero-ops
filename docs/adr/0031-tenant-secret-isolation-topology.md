# ADR 031: Tenant Secret Isolation and Identity Topology

## Status

Accepted

## Context

As the Zero-Ops platform scales, managing secret distribution across the Hub and Spoke architecture presents two competing challenges:
1. **API Limits & Management Overhead**: Generating a unique Infisical Machine Identity for every single SaaS tenant (which could be tens of thousands) exhausts Infisical API limits and creates an unmanageable explosion of IAM policies.
2. **Tenant Isolation (Blast Radius)**: Relying solely on a single global `ClusterSecretStore` on each Spoke cluster violates the principle of least privilege. If the global store is used, any misconfigured `ExternalSecret` could accidentally pull credentials belonging to another tenant.

We need a topology that minimizes the number of Machine Identities while maximizing Kubernetes namespace isolation, aligning with our existing GitOps governance (ADR-021).

## Decision

We will implement a **Cell-Based Identity Topology with Namespace-Scoped Secret Stores**:

1. **Spoke-Scoped Machine Identities**: The Hub Operator will generate exactly *one* Infisical Machine Identity per Spoke Pool (Cell). 
2. **Path-Based IAM**: This identity will be granted read access exclusively to its specific cell paths:
   - `/spoke-pool/<cell-id>/shared/*` (Platform/Shared credentials)
   - `/spoke-pool/<cell-id>/tenants/*` (Tenant-specific credentials)
3. **Identity Projection**: The Hub will project this Machine Identity's credentials into the Spoke's `tenant-<id>` namespaces using the platform's bootstrap `ClusterSecretStore`.
4. **Namespace-Scoped SecretStores**: Inside each tenant namespace, the platform will provision a local `SecretStore` that consumes the projected Machine Identity. All tenant-specific `ExternalSecret` resources (e.g., database credentials, JWT keys) will strictly reference this local `SecretStore`.

### Security Boundary Enforcement

Because the Spoke-scoped Machine Identity has access to *all* tenants within that cell, isolation relies on Kubernetes RBAC and GitOps boundaries:
- **Platform Control**: Only the AINativeSaaS Crossplane XR (governed by the platform team) can create `ExternalSecret` and `SecretStore` resources.
- **Tenant Restriction**: Per **ADR-021** (Tenant ABI), the `tenant-workloads` ArgoCD AppProject explicitly **whitelists** the resources tenants can deploy (Deployments, Rollouts, Services, etc.). Tenants are **denied** the ability to create `ExternalSecret` objects, making it impossible for a malicious tenant workload to query Infisical for another tenant's paths.

## Consequences

### Positive
* **Scalability**: Reduces Infisical Machine Identities from $O(Tenants)$ to $O(Spokes)$. Dramatically reduces API pressure on the secret backend.
* **Blast Radius Reduction**: A compromised Spoke cluster's Machine Identity can only read secrets for tenants residing on that specific Spoke, rather than the entire global fleet.
* **Explicit Boundaries**: Using `SecretStore` instead of `ClusterSecretStore` for tenant resources prevents platform engineers from accidentally cross-wiring secrets between namespaces during composition development.

### Negative
* **Shared Cell Risk**: A complete compromise of the Kubernetes API on a Spoke cluster would expose all tenant secrets on that Spoke. (Mitigated by the fact that a K8s API compromise already implies full cluster compromise).
* **Two-Hop Resolution**: Bootstrapping a tenant requires a two-hop secret resolution: ESO must first use the `ClusterSecretStore` to fetch the Machine Identity, and then use the local `SecretStore` to fetch the actual application secrets.

## Amendment (2026-05-28): Tenant Classification and Spoke Silo Model

### Context
The shared blast radius of Spoke Pools—where one Machine Identity accesses all tenant secrets within a cell—is acceptable for standard workloads but violates compliance requirements for highly regulated data.

### Decision
Tenant workloads are strictly categorized and placed into distinct Spoke topologies based on compliance requirements.

**Spoke Pool Model:**
Utilized exclusively for non-regulated, standard commercial tenants. A single Spoke-scoped Machine Identity manages secrets for all tenants within the shared cluster.

**Spoke Silo Model:**
Mandatory for tenants requiring PCI, HIPAA, or SOC2 strict compliance. These tenants are provisioned into dedicated, single-tenant Spoke clusters. The Machine Identity is scoped exclusively to the single tenant, ensuring total physical and cryptographic isolation. Platform operators are forbidden from scheduling regulated workloads onto Spoke Pools.

### Consequences
- **Positive:** Provides clear regulatory isolation for compliance-bound tenants.
- **Negative:** Increases operational cost for siloed Spoke clusters.
- **Negative:** Requires tenant classification at provisioning time.

## References
* **ADR-003**: Infisical as Single Source of Truth
* **ADR-019**: Runtime Plugin Credential Resolution
* **ADR-021**: GitOps Governance and Tenant ABI Enforcement

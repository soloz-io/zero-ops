# ADR-041: Controller Responsibility Matrix

**Date:** 2026-06-08
**Status:** Accepted

## Context

The platform includes multiple controllers (Hub Operator, Crossplane, cert-manager, ESO, ArgoCD, Atlas, CNPG, Kyverno, Kube-SBT) that operate across overlapping domains. Without explicit per-controller boundaries, responsibility creep is inevitable: an operator begins managing certificates, a GitOps tool begins provisioning infrastructure, a secret delivery tool begins generating secrets.

Each controller must operate within a bounded scope. Responsibilities must be explicitly assigned. Violations must be unambiguous.

This ADR defines the allowed and forbidden operations for every controller in the platform. It references the ownership matrix defined in ADR-039 and the lifecycle boundary defined in ADR-040.

## Decision

### CLI

The CLI is the sole Day-0 execution environment. It exists only during bootstrap and must never be used for Day-1+ operations.

| Allowed | Forbidden |
|---|---|
| Bootstrap secret generation and upload | Runtime reconciliation |
| PKI trust anchor creation | Certificate issuance or renewal |
| Machine Identity creation in Infisical | Tenant or Spoke provisioning |
| Initial GitOps bootstrap (boundary ApplicationSets) | Infrastructure provisioning |
| Injection of Secret Zero and trust anchors | Post-bootstrap CLI execution for any operational purpose |
| Gating ArgoCD boundary activation | Modifying Day-1 controller state |

### Hub Operator

The Hub Operator orchestrates Spoke and tenant lifecycle at the control-plane level. It generates spoke infrastructure credentials. It does not participate in Day-0 bootstrap beyond consuming Day-0 artifacts as inputs.

| Allowed | Forbidden |
|---|---|
| Spoke provisioning and lifecycle management | PKI operations of any kind |
| Tenant environment provisioning | Secret generation for resources it does not own |
| API routing and database setup | Certificate issuance, renewal, or management |
| Credential generation only for resources it owns (spoke infrastructure) | Private key handling |
| Reading Day-0 artifacts from their Systems of Record | Modifying Day-0 artifacts |
| | Acting as a PKI control plane |
| | Acting as a secret manager for tenant or application secrets |

### Crossplane

Crossplane provisions and reconciles infrastructure through XRDs, Compositions, and Claims. It delivers infrastructure declarations received from ArgoCD. It must never handle secret material.

| Allowed | Forbidden |
|---|---|
| Infrastructure provisioning and reconciliation | Secret generation |
| XR composition (including nested XRs) | Certificate generation, signing, or renewal |
| ClusterResourceSet attachment to Spokes | Secret copying or traversal |
| Database role and grant management (via provider-sql) | Private key reading or handling |
| Status reflection from managed resources | PKI lifecycle management |
| | Acting as a secret distribution plane |

### cert-manager

cert-manager is the sole authority for X.509 certificate lifecycle. No other component may issue, renew, revoke, or manage certificates.

| Allowed | Forbidden |
|---|---|
| Certificate issuance, renewal, and revocation | Infrastructure provisioning |
| Certificate lifecycle management at or below the Issuer/ClusterIssuer boundary | Secret management (beyond certificate key material) |
| Automatic renewal based on TTL thresholds | Database management |
| Private key generation for certificates | Non-PKI secret operations |
| CA lifecycle within the Issuer/ClusterIssuer boundary | Spoke or tenant lifecycle management |

### Spoke Identity Operator

The Spoke Identity Operator manages Machine Identity lifecycle for Spoke clusters by reconciling SpokeMachineIdentity CRs against the Infisical API. It is the initial implementation of the Platform Identity Domain and may evolve to support full identity lifecycle (rotation, revocation, attestation, audit) as defined in a future ADR.

| Allowed | Forbidden |
|---|---|
| SpokeMachineIdentity CR reconciliation | PKI operations of any kind |
| Machine Identity creation via Infisical API | Certificate issuance, renewal, or management |
| Identity drift detection and reconciliation | Private key handling |
| Client secret rotation (create new + revoke old) | Secret generation for non-identity resources |
| Identity revocation on CR deletion | Acting as a general-purpose identity provider |
| Status.conditions, status.identityID, and status.lastRotated updates | Modifying cert-manager resources |
| | Acting as a PKI control plane |

NOTE: The Spoke Identity Operator is the initial implementation of the Platform Identity Domain. Future ADRs may extend the platform with attestation, policy enforcement, credential provenance, and audit capabilities.

### External Secrets Operator (ESO)

ESO delivers secrets from Infisical to Kubernetes. It is a delivery mechanism, not a management mechanism.

| Allowed | Forbidden |
|---|---|
| Syncing secrets from Infisical to Kubernetes (runtime delivery) | Secret generation |
| ExternalSecret and ClusterSecretStore management | PKI operations |
| Secret refresh based on `refreshInterval` | Rotation triggering or driving |
| `creationPolicy: Owner` for ESO-owned secrets | PushSecret usage (provider lacks support) |
| `creationPolicy: Merge` for bootstrap secrets | Acting as a source of truth for any secret |
| | Non-Infisical secret store backends |

### ArgoCD

ArgoCD delivers desired state for Kubernetes resources from Git. It delivers infrastructure declarations (XRDs, Compositions, Providers). It must not reconcile infrastructure.

| Allowed | Forbidden |
|---|---|
| Kubernetes resource delivery (Namespaces, RBAC, ConfigMaps, Deployments, Services) | Infrastructure provisioning or reconciliation |
| Infrastructure declaration delivery (XRD, Composition, Provider objects) | Secret generation |
| ApplicationSet management across Spoke Pools | Certificate lifecycle management |
| Sync-wave annotations for CRD → operator ordering (within `01-platform-infra` only) | Database management |
| Boundary ApplicationSet lifecycle | Acting as an infrastructure controller |
| | Sync-wave annotations outside CRD ordering |

### Atlas Operator

Atlas Operator manages database schema lifecycle through declarative migrations.

| Allowed | Forbidden |
|---|---|
| Schema migration planning and execution | Database provisioning (CNPG domain) |
| Declarative DDL lifecycle | Role or grant management (provider-sql domain) |
| Drift detection and reporting | Secret management |
| Migration checksum validation | Infrastructure provisioning |
| | Physical database lifecycle |

### CloudNativePG (CNPG)

CNPG manages the physical PostgreSQL cluster lifecycle.

| Allowed | Forbidden |
|---|---|
| PostgreSQL cluster provisioning and lifecycle | Schema management (Atlas domain) |
| High availability and failover | Role or grant management (provider-sql domain) |
| Backup and recovery | Secret generation (consumes secrets from ESO) |
| Physical resource management (PVCs, Pods) | Application-level database operations |
| | Certificate lifecycle (consumes TLS secrets) |

### Kube-SBT (Tenant Identity Service)

Kube-SBT manages tenant identity and generates tenant passwords at provisioning time.

The abstraction is the responsibility; the product behind it is an implementation
detail. This row named one product until 2026-09-03, which made the platform's own
responsibility matrix read as a commitment to a vendor — see ADR-059. Kube-SBT
selects its provider by configuration and provisions a tenant's identity resources
through whichever one is configured.

| Allowed | Forbidden |
|---|---|
| Tenant identity lifecycle | PKI operations |
| Initial tenant password generation (one-time) | Runtime credential rotation |
| Tenant-to-database credential mapping | Infrastructure provisioning |
| Infisical upload for tenant credentials | Spoke lifecycle management |
| Identity provider API abstraction | Certificate management |

### Kyverno

Kyverno enforces admission policies across the platform.

| Allowed | Forbidden |
|---|---|
| Policy enforcement and mutation | Secret generation or management |
| RBAC aggregation via ClusterRole labels | Infrastructure provisioning |
| Resource validation | Certificate lifecycle |
| Dynamic policy updates | Application-level business logic |
| | Acting as a workload controller |

### SPIRE

SPIRE managed workload identity through SPIFFE. **Decommissioned (2026-08-13):**
removed from the platform for the home-lab; its workload identity responsibilities
were assumed by cert-manager-issued certificates. No new controller may claim
SPIRE's former scope.

| Allowed | Forbidden |
|---|---|
| Workload identity issuance (SVIDs) | Infrastructure certificate issuance |
| SPIFFE federation | Secret management |
| Node attestation | Infrastructure provisioning |
| Workload-to-workload authentication | Database management |
| | Acting as an infrastructure PKI |

### Billing Operator

The Billing Operator manages the billing catalog declaratively.

| Allowed | Forbidden |
|---|---|
| Billing catalog lifecycle (Meters, Features, Plans) | Runtime usage data management |
| Catalog reconciliation from Git | Tenant subscription lifecycle |
| Catalog validation and versioning | Secret management |
| | Infrastructure provisioning |

## Universal Prohibitions

The following operations are universally prohibited and no exception ADR may override them without amending this ADR:

1. **Secret traversal:** No component may copy secret material from one cluster to another. (Exception: ESO's prescribed delivery path from Infisical → Kubernetes.)
2. **PKI operations:** Only cert-manager may issue, renew, revoke, or manage X.509 certificates. All other components are prohibited from PKI operations regardless of their individual ADR permissions.
3. **System of Record bypass:** No component may write resource state directly to a System of Record unless it is the Lifecycle Owner defined in ADR-039.
4. **Day-0 execution by Day-1 controllers:** No Day-1 controller may perform an operation classified as Day-0 in ADR-040.

## Consequences

### Positive

- Every controller's scope is explicitly bounded. Responsibility creep is detectable in code review.
- Universal prohibitions prevent fragmentation of critical domains (PKI, secrets).
- The matrix resolves conflicts between ADRs — where two ADRs describe overlapping controller responsibilities, this ADR controls.
- Onboarding documentation for new controllers is reduced to pointing at this matrix.

### Negative

- Adding a new controller requires defining its complete matrix entry and reviewing for conflicts with existing controllers.
- The matrix is authoritative; if a legitimate exception is needed, this ADR must be amended — there is no per-controller carve-out mechanism.

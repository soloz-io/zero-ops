# ADR-035: Enterprise PKI and Delegated Trust via Infisical OSS

**Date:** 2026-05-31
**Status:** Accepted

---

# Context

The platform requires a scalable PKI architecture capable of supporting hundreds of Spoke clusters while respecting the architectural boundaries established by:

* ADR-005 (Ownership Boundaries)
* ADR-025 (Deterministic Bootstrap)
* ADR-031 (Security Isolation)
* ADR-034 (Runtime vs Lifecycle Autonomy)

Historically, certificate issuance was performed on the Hub and distributed to Spokes through Crossplane Composition Functions and Kubernetes Secret traversal.

This approach created several problems:

1. Crossplane became a secret distribution plane.
2. Private key material traversed Hub control-plane components.
3. Certificate lifecycle logic became tightly coupled to provisioning.
4. Certificate distribution violated the ownership boundaries established by ADR-005.
5. Blast radius increased because certificate issuance and distribution were centralized inside provisioning workflows.

The platform requires a model where:

* Crossplane never handles private keys.
* Spokes manage their own certificate lifecycle.
* PKI remains centrally governed.
* Certificate issuance follows the same lifecycle dependency model already used by GitOps, provisioning, and secret management.

---

# Alternatives Considered

## Alternative A: ESO Synchronizes Certificates

External Secrets Operator retrieves certificates directly from Infisical and distributes them into clusters.

### Rejected

The ESO Infisical provider only supports secret retrieval.

The provider exposes:

* SecretStoreReadOnly capability
* `/api/v3/secrets/*` endpoints

It does not support:

* `/api/v1/pki/*`
* `/api/v1/cert-manager/*`

Certificate issuance cannot be implemented through ESO.

---

## Alternative B: Hub Operator Performs Certificate Distribution

The Hub Operator issues and distributes leaf certificates to Spokes.

### Rejected

This turns the Hub Operator into a PKI control plane.

Consequences:

* Violates ADR-005.
* Requires private key handling on the Hub.
* Recreates the same secret traversal problem ADR-035 is intended to eliminate.

---

## Alternative C: Dedicated Intermediate CA Per Spoke

Each Spoke receives its own Intermediate CA.

### Rejected

Investigation of the Infisical OSS codebase revealed:

* CA-level authorization is not available in OSS.
* Machine Identities cannot be restricted to a specific Intermediate CA.
* Authorization enforcement is implemented in Enterprise-only permission services.
* PKI permissions are effectively project scoped.

As a result:

* Multiple Intermediate CAs do not provide enforceable isolation.
* A Machine Identity with PKI issuance permissions can issue certificates against any CA available within the project.

The additional operational complexity provides no measurable security benefit.

---

## Alternative D: Single Fleet Intermediate CA + Delegated Issuance

Spokes perform leaf certificate issuance through `cert-manager` and `infisical-issuer`.

A single Fleet Intermediate CA remains inside Infisical.

### Selected

This model:

* Eliminates secret traversal.
* Eliminates distributed Intermediate CA private keys.
* Preserves centralized governance.
* Aligns with ADR-034 lifecycle dependency rules.
* Matches Infisical OSS authorization capabilities.

---

# Decision

## Trust Hierarchy

The platform implements a three-tier trust hierarchy:

1. Offline Root CA
2. Fleet Intermediate CA
3. Leaf Certificates

Infisical OSS cannot enforce CA-scoped authorization boundaries for Machine Identities. Therefore Intermediate CA delegation is not treated as a security boundary. The platform uses a single Fleet Intermediate CA.

### Offline Root CA

The Offline Root CA:

* Is generated outside Infisical.
* Is stored offline.
* Is never connected to production systems.
* Signs the Fleet Intermediate CA.

The Offline Root private key is never stored in Kubernetes.

### Fleet Intermediate CA

The Fleet Intermediate CA:

* Is hosted within Infisical PKI.
* Is the sole issuing authority for platform certificates.
* Signs all infrastructure certificates.
* Signs all Spoke-issued certificates.

The Fleet Intermediate private key never leaves Infisical.

### Leaf Certificates

Leaf certificates are issued through:

* cert-manager
* infisical-issuer
* Infisical PKI

All certificates chain through:

```
Offline Root CA
    ↓
Fleet Intermediate CA
    ↓
Leaf Certificate
```

---

# Certificate Profiles

All certificate issuance SHALL occur through centrally managed certificate profiles. Direct issuance against Certificate Authorities by ID is not supported by the platform.

A certificate profile defines:

| Field | Description |
|---|---|
| `slug` | Human-readable identifier (survives backup/restore) |
| `caId` | The issuing Certificate Authority (the Fleet Intermediate CA) |
| `enrollmentType` | Issuance method: `api`, `acme`, `scep`, `est` |
| `defaults` | Default TTL, key algorithm, subject fields |
| `issuerType` | `ca` or `self_signed` |

The effective issuance boundary is:

```text
Machine Identity
     ↓
Project-level PKI permissions
     ↓
Certificate Profile (profileId)
     ↓
Fleet Intermediate CA
```

Profiles are identified by UUID internally but resolved by `slug` for operational use. The Hub Operator resolves the bootstrap profile slug to UUID at runtime via `GET /api/v1/cert-manager/certificate-profiles/slug/:slug`.

---

# Certificate Issuance Model

Each Spoke cluster deploys:

* cert-manager
* infisical-issuer

Leaf certificates are requested locally through:

```text
Certificate
   ↓
cert-manager
   ↓
infisical-issuer
   ↓
Infisical /api/v1/cert-manager/* API
```

The Fleet Intermediate CA signs the certificate.

The private key is generated and stored locally in the Spoke cluster.

Crossplane is never involved.

The implementation depends on the Infisical certificate-management API family (`/api/v1/cert-manager/*`). The platform does not rely on any `/api/v1/pki/*` endpoints (those are deprecated in the Infisical OSS codebase).

---

# Bootstrap Exception

ADR-025 requires deterministic cluster bootstrap.

The ArgoCD Agent must establish an mTLS connection before GitOps becomes available.

Therefore a controlled exception exists.

## Bootstrap Certificate

The Cert Operator SHALL issue a temporary bootstrap certificate.

Characteristics:

* TTL: 72 hours
* Single purpose
* ArgoCD Agent only
* Delivered through ClusterResourceSet
* Replaced after GitOps initialization

Workflow:

```text
SpokePool Created
       ↓
Cert Operator
       ↓
Issue 72h Bootstrap Certificate
       ↓
ClusterResourceSet
       ↓
Spoke Cluster
       ↓
ArgoCD Agent Connects
       ↓
GitOps Starts
       ↓
cert-manager Takes Ownership
       ↓
Bootstrap Certificate Replaced
```

## Bootstrap Private Key Handling

The Cert Operator temporarily receives bootstrap certificate private key material during Day-0 provisioning.

This is a controlled exception:

* The private key is received in-memory from the `POST /api/v1/cert-manager/certificates/` API response.
* It is embedded directly into a ClusterResourceSet payload and written to the Kubernetes API Server.
* It exists in Cert Operator process memory only during the reconcile loop.
* It is never stored in operator-side persistent storage, Secrets, ConfigMaps, or the Infisical API.

After the Spoke cluster receives the ClusterResourceSet payload:

* The private key exists only on the Spoke cluster.
* cert-manager takes ownership and replaces the certificate within 72 hours.
* The Cert Operator never reads, rotates, renews, or reconciles this private key.

**This exception is limited exclusively to the 72-hour ArgoCD bootstrap certificate and does not extend to any Day-1+ lifecycle operations.**

The bootstrap certificate is the only exception to delegated issuance.

---

# Trust Distribution

The Offline Root CA public certificate is injected during Hub bootstrap.

The Root CA public certificate is distributed to:

* ArgoCD Principal
* NATS
* VictoriaMetrics
* Spoke bootstrap payloads

No per-Spoke trust bundles exist.

No Intermediate CA bundles are distributed.

Trust is established through the common Root CA hierarchy.

## Trust Artifact Type

The Offline Root CA public certificate is stored as a **ConfigMap**, not a Secret.

Rationale:

* The root certificate is public information — no sensitivity requires Secret RBAC.
* A ConfigMap communicates intent more clearly to operators.
* `cert-manager` and the ArgoCD Agent consume it as a PEM file, not as sensitive credential material.
* Avoids unnecessary RBAS restrictions for read-only trust consumers.

ClusterResourceSet payloads deliver the root CA ConfigMap to Spoke clusters using `kind: ConfigMap`.

---

# Operator Responsibility Split

PKI and identity bootstrapping are extracted from the Hub Operator into a dedicated **Cert Operator** to maintain modularity and single-responsibility boundaries.

## Cert Operator Responsibilities

The Cert Operator participates only in Day-0 bootstrap.

Responsibilities:

* Watch `SpokePool` provisioning events.
* Create Machine Identities in Infisical.
* Generate the 72h bootstrap certificate via Infisical PKI.
* Store bootstrap artifacts as ClusterResourceSet payloads in the `platform-capi` namespace.

The Cert Operator SHALL NOT:

* Rotate, renew, or reconcile Spoke leaf certificates (Day-1+).
* Act as an active PKI proxy for workloads.

## Hub Operator Responsibilities

The Hub Operator is completely decoupled from PKI and Machine Identity management.

The Hub Operator remains responsible for:

* SaaS control plane management.
* Tenant environment provisioning.
* API routing and database setup.

The Hub Operator SHALL NOT:

* Create or manage Machine Identities.
* Issue, rotate, renew, or reconcile any certificate.
* Handle private key material.
* Store PKI artifacts in ClusterResourceSet payloads.
* Act as a PKI control plane.

---

# Crossplane Responsibilities

Crossplane SHALL NOT:

* Generate certificates.
* Copy certificates.
* Copy secrets.
* Read private keys.
* Traverse secret material.

Crossplane remains responsible only for:

* Infrastructure provisioning.
* Cluster lifecycle management.
* ClusterResourceSet attachment.

---

# Identity Separation

Infrastructure identity and workload identity remain separated.

## Infrastructure Identity

Managed through:

* cert-manager
* infisical-issuer
* Infisical PKI

Examples:

* ArgoCD Agent
* NATS Leaf Nodes
* Alloy
* Admission Webhooks
* Controllers

## Workload Identity

Managed through SPIRE.

Examples:

* Tenant workloads
* Service mesh identities
* Application-to-application authentication

SPIRE SHALL NOT be used for infrastructure certificates.

---

# PKI Profile TTLs

Certificate validity is governed centrally through certificate profiles.

| Profile                 | TTL |
| ----------------------- | --- |
| Infrastructure Services | 24h |
| Database Clients        | 4h  |
| Service Mesh Components | 1h  |
| Human Access            | 15m |
| Bootstrap Certificate   | 72h |

Each profile maps to a named PKI policy in Infisical. TTLs are enforced server-side by the certificate profile configuration.

---

# Revocation Strategy

The platform does not implement:

* CRLs
* OCSP
* Online revocation infrastructure

Compromise mitigation relies on:

1. Short certificate TTLs.
2. Machine Identity rotation.
3. Fleet Intermediate replacement.
4. Trust anchor replacement when required.

Compromised certificates expire naturally.

---

# Infisical OSS Authorization Limitation

Investigation of the Infisical OSS codebase revealed a critical limitation.

Machine Identity permissions are project scoped.

Infisical OSS does not provide:

* CA-level authorization
* Intermediate-specific authorization
* CA-ID-based permission constraints

Therefore:

* PKI authorization boundaries cannot be enforced per Intermediate CA.
* Multiple Intermediate CAs do not provide enforceable isolation in OSS.
* Security isolation relies on:

  * Project boundaries
  * Machine Identity protection
  * Short certificate TTLs
  * SPIRE workload identity separation

This limitation is accepted by the platform architecture.

---

# Decision

## Infisical Project Separation

The platform maintains two Infisical projects with distinct types to separate PKI and secret management concerns:

- **`hub-platform`** (type `cert-manager`): Hosts the Fleet Intermediate CA, certificate profiles, and all PKI infrastructure. Machine Identities with PKI permissions are scoped here.
- **`hub-secrets`** (type `secret-manager`): Stores all application secrets, database credentials, and infrastructure tokens. The ESO ClusterSecretStore references this project.

**Context:** Infisical OSS Machine Identity permissions are project scoped — a single project cannot mix cert-manager and secret-manager operations because the ESO provider only supports the secret-manager type. Separating into two projects is required, not optional.

Both projects are created during Day-0 bootstrap by the CLI. The Machine Identity is granted admin role on both.

**Impact:** PKI Machine Identities cannot read application secrets. ESO syncs from the secrets project only. Operators must use the correct project slug when configuring Infisical operations.

---

# Consequences

## Positive

* Crossplane secret traversal is eliminated.
* Certificate lifecycle ownership moves to Spokes.
* No Intermediate CA private keys leave Infisical.
* Simpler operational model.
* Centralized issuance audit trail.
* Consistent lifecycle dependency model.
* Reduced platform complexity.
* Eliminates false security assumptions around per-Spoke Intermediate CAs.

## Negative

* Certificate issuance depends on Infisical availability.
* Lifecycle autonomy is not provided.
* Machine Identity PKI permissions remain project scoped in Infisical OSS.
* A compromised Machine Identity could issue additional certificates within the same Infisical project.

This trade-off is accepted because it aligns with ADR-034's distinction between Runtime Autonomy and Lifecycle Autonomy.

# ADR-035: Enterprise PKI and Delegated Trust via Infisical OSS

**Date:** 2026-05-31
**Status:** Accepted
**Supersedes:** ADR-032 (Partial: Implementation of Delegated Spoke Issuance)

## Context

The Zero-Ops platform requires a horizontally scalable, cryptographically isolated Public Key Infrastructure (PKI) to support mutual TLS (mTLS) across hundreds of Spoke clusters.

Crossplane is explicitly prohibited from traversing application secret material per ADR-032. The PKI architecture must support:

* Delegated certificate issuance
* Strong blast-radius isolation
* Centralized lifecycle governance
* Explicit ownership boundaries
* Enterprise-scale operation at ADR-033 fleet limits

The platform operates under ADR-034's Runtime Autonomy model:

* Runtime Autonomy is mandatory.
* Lifecycle Autonomy is not required.

A Hub outage must not impact already-running tenant workloads, active database connections, existing secrets, or already-issued certificates.

However, lifecycle operations may pause during Hub outages, including:

* Secret synchronization
* Certificate issuance and renewal
* GitOps synchronization
* Infrastructure provisioning

Certificate issuance is therefore classified as a lifecycle operation rather than a runtime dependency.

---

## Alternatives Considered

### A. ESO Synchronizes PKI Objects

The original design proposed synchronizing Intermediate CA material through ESO.

Analysis of the Infisical OSS provider implementation demonstrated that the ESO provider supports only Secrets Management APIs (`/api/v3/secrets/*`) and does not support PKI APIs (`/api/v1/cert-manager/*`).

The provider exposes only `SecretStoreReadOnly` capabilities and cannot retrieve PKI certificate objects or private keys.

**Decision:** Rejected as technically infeasible.

---

### B. Hub Operator as Continuous PKI Distributor

The Hub Operator retrieves Intermediate CA private keys from Infisical and distributes them to Spokes.

This approach would:

* Violate ADR-035 ownership boundaries
* Turn the Hub Operator into a PKI control plane
* Require continuous handling of private key material
* Reintroduce secret distribution patterns prohibited by ADR-032

**Decision:** Rejected.

---

### C. cert-manager + infisical-issuer (Selected)

The platform deploys cert-manager and infisical-issuer on every Spoke cluster.

Leaf certificates are requested through the Infisical PKI API.

Intermediate CA private keys remain exclusively within Infisical.

Certificate issuance depends on Infisical availability.

This aligns with ADR-034 because certificate issuance is a lifecycle operation.

This model mirrors the platform's existing operational pattern:

| Capability      | Authority     |
| --------------- | ------------- |
| Secrets         | Infisical     |
| Certificates    | Infisical PKI |
| GitOps          | ArgoCD        |
| Provisioning    | Crossplane    |
| Runtime Traffic | Spoke         |

**Decision:** Accepted.

---

### D. Spoke-Owned Intermediate CA

Each Spoke possesses its own Intermediate CA private key and performs fully autonomous local issuance using cert-manager CA Issuers.

Advantages:

* No dependency on Infisical for issuance
* Full certificate lifecycle autonomy

Disadvantages:

* Distribution of hundreds of Intermediate CA private keys
* Increased attack surface
* Per-Spoke trust-anchor rotation complexity
* Significant operational burden at ADR-033 fleet scale

Following ADR-034 clarification, lifecycle autonomy is not a platform requirement.

**Decision:** Rejected.

---

## Decision

### Trust Hierarchy

The platform implements a strict three-tier trust hierarchy.

1. Offline Root CA
2. Fleet Intermediate CA
3. Spoke Intermediate CA

```text
Offline Root CA
        │
        ▼
Fleet Intermediate CA
        │
        ▼
Spoke Intermediate CA
        │
        ▼
Leaf Certificates
```

The Offline Root CA is:

* Air-gapped
* Irrecoverable
* Created outside Infisical
* Used exclusively to sign Fleet Intermediates

The Fleet Intermediate CA is managed inside Infisical.

Every SpokePool and SpokeSilo owns a dedicated Spoke Intermediate CA.

---

### Intermediate Isolation

A dedicated Spoke Intermediate CA SHALL exist for every:

* SpokePool
* SpokeSilo

The mapping is:

```text
1 Cell
=
1 Intermediate CA
```

Cross-cell issuance is prohibited.

A certificate issued for one cell MUST NOT be valid for any other cell.

This enforces ADR-031 blast-radius boundaries.

---

### Intermediate Key Ownership

Intermediate CA private keys SHALL remain exclusively within Infisical.

The following systems SHALL NEVER possess Intermediate CA private keys:

* Hub Operator
* Crossplane
* ArgoCD
* ESO
* Spoke Clusters

Infisical is the sole owner of Intermediate key material.

---

### Certificate Issuance

Each Spoke cluster deploys:

* cert-manager
* infisical-issuer

Leaf certificates are issued through the Infisical PKI API.

The issuer authenticates using the Spoke's dedicated Machine Identity.

Machine Identities MUST be scoped exclusively to the Intermediate CA associated with the owning cell.

Cross-cell issuance permissions are forbidden.

---

### Trust Anchor Distribution

Hub-side trust stores SHALL trust:

* Offline Root CA
* Fleet Intermediate CA

Hub-side trust stores SHALL NOT trust individual Spoke Intermediate CAs directly.

Every certificate chain SHALL validate through:

```text
Leaf
  ↓
Spoke Intermediate
  ↓
Fleet Intermediate
  ↓
Offline Root
```

This allows any Hub service to validate certificates from any authorized Spoke without maintaining per-Spoke trust stores.

---

### Hub Operator Boundary

The Hub Operator participates exclusively in Day-0 provisioning.

Responsibilities:

* Ensure Spoke Intermediate CA exists
* Create Spoke Intermediate CA if absent
* Provision PKI metadata required by the Spoke

The Hub Operator SHALL NOT:

* Reconcile Intermediate CA lifecycle
* Rotate Intermediate CAs
* Renew Intermediate CAs
* Issue runtime leaf certificates
* Operate as a PKI control plane

PKI lifecycle governance remains within Infisical.

This preserves ADR-005 ownership boundaries.

---

### Identity Separation

Infrastructure identity and workload identity remain separate.

Infrastructure Identity:

* cert-manager
* infisical-issuer
* ingress controllers
* admission webhooks
* platform infrastructure

Workload Identity:

* SPIRE
* SPIFFE SVIDs
* service mesh workloads
* tenant applications

Infrastructure certificates SHALL NOT be used for workload identity.

SPIRE identities SHALL NOT be used for infrastructure trust.

---

### PKI Profiles

Leaf certificate lifecycle is governed centrally through Infisical PKI Profiles.

| Profile                     | TTL |
| --------------------------- | --- |
| Infrastructure/Webhooks     | 24h |
| Database Clients            | 4h  |
| Service Mesh Infrastructure | 1h  |
| Human Access                | 15m |

Profile governance SHALL be centralized within Infisical.

Application teams SHALL NOT override platform PKI policies.

---

### Bootstrap Certificate Exception

The ArgoCD Agent requires mutual TLS before GitOps becomes available.

A bootstrap certificate may therefore be issued during Day-0 provisioning.

The bootstrap certificate:

* Exists solely to establish initial connectivity
* Is injected through the trusted bootstrap channel
* Is temporary
* Is replaced by cert-manager-managed certificates after GitOps activation

This exception is permitted under ADR-032's bootstrap trust-anchor allowance.

---

### Compromise Mitigation

The platform SHALL NOT maintain:

* Certificate Revocation Lists (CRLs)
* OCSP Responders

Compromise mitigation is performed through Trust Anchor Replacement.

When a Spoke Intermediate is compromised:

1. Intermediate CA is revoked operationally.
2. A replacement Intermediate CA is created.
3. New issuance begins immediately.
4. Existing leaf certificates expire naturally according to profile TTLs.

Short certificate lifetimes are the primary revocation mechanism.

---

## Consequences

### Positive

* Crossplane certificate distribution is completely eliminated.
* Intermediate CA private keys never leave Infisical.
* The Hub Operator is not a PKI control plane.
* Per-cell blast-radius isolation is enforced.
* Centralized issuance provides a complete audit trail.
* PKI governance is centralized.
* Operational complexity is significantly reduced compared to distributed Intermediate ownership.
* The architecture scales cleanly to ADR-033 fleet limits.

### Negative

* Certificate issuance depends on Infisical availability.
* Certificate renewal depends on Infisical availability.
* Lifecycle operations pause during Infisical outages.
* Runtime autonomy is preserved, but certificate lifecycle autonomy is intentionally not provided.

These tradeoffs are accepted and align with ADR-034's Runtime-versus-Lifecycle autonomy model.

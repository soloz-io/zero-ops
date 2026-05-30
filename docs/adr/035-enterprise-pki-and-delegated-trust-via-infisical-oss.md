# ADR-035: Enterprise PKI and Delegated Trust via Infisical OSS

**Date:** 2026-05-31
**Status:** Accepted
**Supersedes:** ADR-032 (Partial: Implementation of Delegated Spoke Issuance)

---

# Context

The platform requires a scalable Public Key Infrastructure (PKI) to support mTLS across hundreds of Spoke clusters while maintaining strict separation between provisioning responsibilities, runtime operations, and workload identity systems.

Crossplane is explicitly prohibited from traversing, distributing, or managing certificate private keys. The PKI architecture must support:

* Secure infrastructure mTLS
* Centralized lifecycle governance
* Short-lived certificates
* Auditability
* Separation between infrastructure identity and workload identity
* Consistency with ADR-034's Runtime Autonomy vs Lifecycle Autonomy model

Per ADR-034:

* **Runtime Autonomy** is guaranteed.
* **Lifecycle Autonomy** is not guaranteed.

Existing workloads, certificates, and secrets must continue operating during Hub outages. However, certificate issuance, renewal, secret rotation, GitOps reconciliation, and provisioning are lifecycle operations and may pause when centralized systems are unavailable.

The original ADR-035 design proposed a dedicated Intermediate CA per Spoke cluster to provide cryptographic blast-radius isolation.

Subsequent validation of the Infisical OSS permission model demonstrated that this isolation cannot be enforced in OSS.

Specifically:

* PKI authorization enforcement relies on Enterprise-only permission services.
* Certificate Authority permissions support name-based conditions only.
* OSS Machine Identities cannot be restricted to a specific Intermediate CA.
* A Machine Identity with PKI issuance permissions may issue certificates against any accessible CA within the same Infisical project.

As a result, a "one Intermediate per Spoke" hierarchy introduces substantial operational complexity without providing the intended security boundary.

The architecture therefore converges on a single Fleet Intermediate CA model.

---

# Alternatives Considered

## A. ESO Synchronizes PKI Objects

The ESO Infisical provider wraps the Infisical Secrets API and supports secret retrieval only.

It does not support:

* PKI issuance
* CA management
* Certificate retrieval
* PKI lifecycle operations

This approach is infeasible.

**Decision:** Rejected.

---

## B. Hub Operator Fetches and Distributes Private Keys

The Hub Operator would retrieve certificate private keys from Infisical and distribute them to Spokes.

This violates multiple architectural principles:

* ADR-005 ownership boundaries
* ADR-032 secret traversal restrictions
* ADR-035 private key custody requirements

It would effectively transform the Hub Operator into a PKI control plane.

**Decision:** Rejected.

---

## C. Dedicated Intermediate CA Per Spoke

Each Spoke receives its own Intermediate CA.

Advantages:

* Theoretical blast-radius isolation
* Independent trust branches

Disadvantages:

* Infisical OSS cannot enforce CA-specific authorization boundaries.
* Machine Identities cannot be scoped to individual Intermediate CAs.
* Intermediate lifecycle management becomes operationally expensive at ADR-033 scale.
* Hundreds of Intermediate CAs provide little practical security benefit when authorization isolation does not exist.

**Decision:** Rejected.

---

## D. cert-manager + infisical-issuer + Fleet Intermediate CA (Selected)

Spoke-local cert-manager instances use the Infisical PKI API through `infisical-issuer`.

All infrastructure certificates are issued from a centrally managed Fleet Intermediate CA.

No Intermediate CA private keys leave Infisical.

Certificate issuance depends on Infisical availability, which is acceptable under ADR-034 because issuance and renewal are lifecycle operations.

This model:

* Eliminates private key distribution.
* Simplifies PKI governance.
* Aligns with ADR-034.
* Aligns with Infisical OSS capabilities.
* Removes unenforceable isolation assumptions.

**Decision:** Accepted.

---

# Decision

## Trust Hierarchy

The platform implements a two-tier operational trust hierarchy:

1. **Offline Root CA**

   * Air-gapped
   * Irrecoverable
   * Created and stored outside Infisical
   * Used solely to sign the Fleet Intermediate CA

2. **Fleet Intermediate CA**

   * Managed within Infisical OSS PKI
   * Sole issuer for platform infrastructure certificates
   * Private key never leaves Infisical

All infrastructure certificates issued across all Spokes chain through:

```text
Offline Root CA
        |
Fleet Intermediate CA
        |
Infrastructure Leaf Certificates
```

---

## Certificate Issuance

Spoke clusters deploy:

* cert-manager
* infisical-issuer

Certificate requests are processed through the Infisical PKI API.

The issuance flow is:

```text
Certificate CR
      |
cert-manager
      |
infisical-issuer
      |
Infisical PKI API
      |
Fleet Intermediate CA
      |
Issued Certificate
```

The Fleet Intermediate CA private key remains exclusively within Infisical.

No CA private keys are distributed to Spokes.

---

## Hub Operator Boundary

The Hub Operator is not a PKI control plane.

The Hub Operator SHALL:

* Provision Infisical Machine Identities during Day-0 bootstrap.
* Inject Machine Identity credentials through the bootstrap trust channel.
* Provision temporary bootstrap certificates required for deterministic cluster bootstrap.

The Hub Operator SHALL NOT:

* Generate certificates continuously.
* Manage Intermediate CAs.
* Reconcile certificate lifecycles.
* Rotate trust anchors.
* Distribute private keys.

All ongoing certificate lifecycle management remains between:

* cert-manager
* infisical-issuer
* Infisical PKI

---

## Bootstrap Certificate Exception

A bootstrap exception is required for ArgoCD Agent initialization.

The ArgoCD Agent must establish mTLS connectivity before:

* cert-manager exists
* infisical-issuer exists
* GitOps reconciliation begins

Therefore:

* The Hub Operator SHALL issue a temporary bootstrap certificate.
* The certificate SHALL have a maximum TTL of 72 hours.
* The certificate SHALL be delivered through the trusted bootstrap channel and ClusterResourceSet mechanism.

After GitOps becomes operational:

* cert-manager assumes ownership of the same Secret.
* The bootstrap certificate is automatically replaced.
* All subsequent renewals use infisical-issuer.

This exception is limited exclusively to deterministic cluster bootstrap.

---

## Trust Anchor Distribution

The Offline Root CA public certificate SHALL be distributed during Hub bootstrap.

Hub services SHALL trust:

* Offline Root CA

Spoke-issued certificates chain through:

```text
Leaf Certificate
       |
Fleet Intermediate
       |
Offline Root
```

No per-Spoke trust bundles exist.

No Intermediate CA distribution is required.

No dual-bundle Intermediate rotation process exists.

---

## Identity Separation

The platform maintains strict separation between infrastructure identity and workload identity.

### Infrastructure Identity

Managed through:

* cert-manager
* infisical-issuer
* Infisical PKI

Examples:

* ArgoCD Agent
* NATS Leaf Nodes
* Grafana Alloy
* Admission Webhooks
* Platform Infrastructure Components

### Workload Identity

Managed through SPIRE.

Examples:

* Tenant workloads
* Service mesh identities
* Internal service-to-service authentication

SPIRE and cert-manager serve distinct trust domains and SHALL NOT overlap responsibilities.

---

## PKI Profiles

Certificate TTLs are centrally governed through Infisical PKI profiles.

| Certificate Type            | TTL |
| --------------------------- | --- |
| Infrastructure/Webhooks     | 24h |
| Database Clients            | 4h  |
| Service Mesh Infrastructure | 1h  |
| Human Access                | 15m |

Short-lived certificates replace traditional revocation mechanisms.

---

## Compromise Mitigation

The platform does not operate:

* CRLs
* OCSP responders

Compromise mitigation relies on:

1. Short certificate lifetimes.
2. Machine Identity rotation.
3. Fleet Intermediate replacement when required.
4. Offline Root trust anchor replacement during catastrophic compromise.

Compromised certificates expire naturally according to their TTL profile.

---

# Consequences

## Positive

* Crossplane Composition Functions are completely removed from certificate distribution.
* Crossplane never traverses private keys or certificate material.
* No CA private keys are distributed to Spoke clusters.
* PKI architecture aligns with actual Infisical OSS security boundaries.
* Simplified operational model with a single Fleet Intermediate CA.
* Eliminates management of hundreds of Intermediate CAs.
* Eliminates Intermediate CA rotation complexity.
* Centralized issuance audit trail through Infisical.
* Consistent lifecycle model across:

  * Secrets
  * Certificates
  * GitOps
  * Provisioning
* Reduced attack surface compared to distributed Intermediate ownership.
* Disaster recovery and trust governance remain centralized.

## Negative

* Certificate issuance and renewal depend on Infisical availability.
* Certificate lifecycle operations pause during Infisical outages.
* Bootstrap requires a temporary certificate exception for ArgoCD Agent initialization.
* The Fleet Intermediate CA becomes a higher-value target than a distributed hierarchy.
* Runtime autonomy is preserved, but lifecycle autonomy is intentionally not provided.

---

# Architectural Rationale

The platform intentionally prioritizes:

* Simplicity
* Auditability
* Operational scalability
* Alignment with Infisical OSS capabilities

over theoretical isolation properties that cannot be enforced by the underlying PKI platform.

At ADR-033 scale, a single Fleet Intermediate CA provides a simpler and more honest security model than maintaining hundreds of Intermediate CAs whose authorization boundaries cannot be guaranteed.

The resulting architecture aligns with ADR-034's Runtime Autonomy model while preserving strong infrastructure identity controls through short-lived certificates, centralized issuance governance, and strict separation between infrastructure and workload identity systems.

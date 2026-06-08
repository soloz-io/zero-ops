# ADR-035: Enterprise PKI and Delegated Trust via Infisical OSS

**Date:** 2026-06-08 (rewritten)
**Status:** Accepted (rewritten — supersedes original ADR-035)

## Context

The platform requires a scalable PKI architecture capable of supporting hundreds of Spoke clusters. Historically, certificate issuance was performed on the Hub and distributed to Spokes through Crossplane Composition Functions and Kubernetes Secret traversal.

This approach created several problems:
1. Crossplane became a secret distribution plane.
2. Private key material traversed Hub control-plane components.
3. Certificate lifecycle logic became tightly coupled to provisioning.
4. Certificate distribution violated ownership boundaries.

The platform requires a model where:
- Only cert-manager may issue, renew, revoke, or manage X.509 certificates.
- Spokes manage their own certificate lifecycle.
- PKI remains centrally governed.
- Certificate issuance follows the same lifecycle dependency model as provisioning and secret management.

This rewrite broadens the PKI ban from component-specific scopes to a universal architectural rule, adds a PKI Decision Hierarchy to disambiguate certificate authority delegation, and references the ownership and authority models defined in ADRs 039, 041, and 043.

## Decision

### Universal PKI Rule

**Only cert-manager may issue, renew, revoke, or manage X.509 certificates.** This is a universal prohibition. No component — present or future — may perform PKI operations unless explicitly authorized by an amendment to this ADR.

#### Forbidden from ALL PKI operations

The following components must never generate, sign, renew, manage CA lifecycle, or handle private key material for X.509 certificates:

- Hub Operator
- Spoke Operator (any spoke-local operator)
- CLI (post Day-0 bootstrap — see Bootstrap Exception)
- Crossplane and all Composition Functions
- Kube-SBT
- External Secrets Operator (ESO)
- ArgoCD
- Atlas Operator
- CNPG
- Billing Operator
- Kyverno

#### Allowed

The following components may create `Certificate`, `Issuer`, and `ClusterIssuer` Custom Resources, delegating actual PKI operations to cert-manager:

- ArgoCD (delivers Certificate manifests from Git)
- Hub Operator (if a Spoke requires a specific Certificate resource as part of provisioning)
- Any component that requires a certificate at runtime — by declaring a Certificate resource, not by generating one.

Creating a Certificate resource is not a PKI operation. It is a Kubernetes resource declaration. cert-manager processes the resource and performs the PKI operation. This distinction must be preserved.

### PKI Decision Hierarchy

```
  Root CA (offline, CLI-generated)
        │
        ▼
  Intermediate CA (Infisical PKI)
        │
        ▼
  Issuer / ClusterIssuer (cert-manager, referencing infisical-issuer)
        │
        ▼
  Certificate (cert-manager, requested by any authorized component)
```

- **Above the Issuer boundary:** Only the CLI (Day-0) may operate. Root CA generation and Intermediate CA upload are immutable Day-0 operations.
- **At and below the Issuer boundary:** Only cert-manager may reconcile. No other component may manage `Issuer`, `ClusterIssuer`, `CertificateRequest`, or `Certificate` reconciliation.
- **Any component may create a Certificate CR.** The act of declaring intent (creating a Certificate resource) is distinct from the act of reconciliation (issuing the certificate), which only cert-manager performs.

### Trust Hierarchy

#### Offline Root CA

- Generated outside Infisical during Day-0.
- Stored offline. Never connected to production systems.
- Signs the Fleet Intermediate CA.
- Private key is never stored in Kubernetes.
- Public certificate distributed as a ConfigMap (not a Secret) to Hub and Spoke clusters.

#### Fleet Intermediate CA

- Hosted within Infisical PKI (`hub-platform` project, type `cert-manager`).
- Sole issuing authority for all platform certificates.
- Signs all infrastructure certificates and all Spoke-issued leaf certificates.
- Private key never leaves Infisical.

#### Leaf Certificates

Issued through cert-manager → infisical-issuer → Infisical PKI. All certificates chain through:

```
Offline Root CA → Fleet Intermediate CA → Leaf Certificate
```

### Certificate Issuance Model

Each Spoke cluster deploys cert-manager and infisical-issuer. Leaf certificates are requested locally:

```
Certificate CR → cert-manager → infisical-issuer → Infisical /api/v1/cert-manager/* API
```

The Fleet Intermediate CA signs the certificate. The private key is generated and stored locally in the Spoke cluster. Crossplane is never involved. No certificate material traverses the Hub.

This model depends on the Infisical certificate-management API family (`/api/v1/cert-manager/*`). The platform does not rely on deprecated `/api/v1/pki/*` endpoints.

### Certificate Profiles

All certificate issuance occurs through centrally managed certificate profiles. Direct issuance against Certificate Authorities by ID is not supported.

A certificate profile defines:
- `slug`: Human-readable identifier (survives backup/restore).
- `caId`: The issuing Certificate Authority (Fleet Intermediate CA).
- `enrollmentType`: Issuance method (`api`, `acme`, `scep`, `est`).
- `defaults`: Default TTL, key algorithm, subject fields.
- `issuerType`: `ca` or `self_signed`.

The issuance boundary is:

```
Machine Identity → Project-level PKI permissions → Certificate Profile → Fleet Intermediate CA
```

Profiles are identified by UUID internally and resolved by `slug` operationally.

### PKI Profile TTLs

| Profile | TTL |
|---|---|
| Infrastructure Services | 24h |
| Database Clients | 4h |
| Service Mesh Components | 1h |
| Human Access | 15m |
| Bootstrap Certificate | 72h |

TTLs are enforced server-side by the certificate profile configuration in Infisical.

### Bootstrap Exception

The ArgoCD Agent must establish an mTLS connection before GitOps becomes available. A controlled exception exists for a single 72-hour bootstrap certificate.

The Cert Operator issues the bootstrap certificate during Day-0. This is the only exception to the delegated issuance model and is strictly limited to Day-0.

After the Spoke cluster receives the bootstrap certificate payload:
- cert-manager takes ownership and replaces the certificate within 72 hours.
- The Cert Operator never reads, rotates, renews, or reconciles this certificate post-Day-0.
- The private key exists only on the Spoke cluster after ClusterResourceSet delivery.

**This exception does not extend to any Day-1+ lifecycle operations.**

### Operator Responsibilities

#### Cert Operator

The Cert Operator participates only in Day-0 bootstrap:
- Watch SpokePool provisioning events.
- Create Machine Identities in Infisical.
- Generate the 72h bootstrap certificate via Infisical PKI.
- Store bootstrap artifacts as ClusterResourceSet payloads.

The Cert Operator must never rotate, renew, or reconcile Spoke leaf certificates during Day-1+.

#### Hub Operator

The Hub Operator is completely decoupled from PKI:
- Must never create or manage Machine Identities.
- Must never issue, rotate, renew, or reconcile any certificate.
- Must never handle private key material.
- Must never store PKI artifacts in ClusterResourceSet payloads.
- Must never act as a PKI control plane.

### Infisical Project Separation

The platform maintains two Infisical projects:
- **`hub-platform`** (type `cert-manager`): Fleet Intermediate CA, certificate profiles, PKI infrastructure.
- **`hub-secrets`** (type `secret-manager`): Application secrets, database credentials, infrastructure tokens.

Machine Identity permissions are project scoped in Infisical OSS. Separation into two projects ensures PKI Machine Identities cannot read application secrets and ESO only references the secrets project.

### Revocation Strategy

The platform does not implement CRLs, OCSP, or online revocation infrastructure. Compromise mitigation relies on:
1. Short certificate TTLs.
2. Machine Identity rotation.
3. Fleet Intermediate replacement.
4. Trust anchor replacement when required.

Compromised certificates expire naturally.

### Infisical OSS Authorization Limitation

Machine Identity permissions are project scoped in Infisical OSS. CA-level authorization, Intermediate-specific authorization, and CA-ID-based permission constraints are not available.

Therefore:
- PKI authorization boundaries cannot be enforced per Intermediate CA.
- Security isolation relies on project boundaries, Machine Identity protection, short certificate TTLs, and SPIRE workload identity separation.

This limitation is accepted by the platform architecture.

## Consequences

### Positive

- Universal PKI rule eliminates ambiguity: no future discussion about "can component X generate a certificate" is necessary.
- PKI Decision Hierarchy disambiguates certificate authority delegation — any component may declare a Certificate, only cert-manager may reconcile one.
- Certificate lifecycle ownership moves to Spokes. No private key material traverses the Hub.
- Centralized issuance audit trail via Infisical PKI.
- Consistent lifecycle dependency model with ADR-034 (Runtime vs Lifecycle Autonomy).

### Negative

- Certificate issuance depends on Infisical availability.
- Lifecycle autonomy is not provided for certificate issuance.
- Machine Identity PKI permissions remain project scoped in Infisical OSS.
- A compromised Machine Identity could issue additional certificates within the same Infisical project.

This trade-off is accepted because it aligns with ADR-034's distinction between Runtime Autonomy and Lifecycle Autonomy.

## Impact

This rewrite supersedes the original ADR-035. The broadened PKI ban, PKI Decision Hierarchy, and explicit component prohibition list replace the original component-specific scope. All component-level PKI prohibitions in other ADRs must reference this ADR as the single authority for PKI boundaries.

## References

- ADR-039: Platform Ownership Model
- ADR-040: Day-0 vs Day-1 Lifecycle Boundary
- ADR-041: Controller Responsibility Matrix
- ADR-043: Control Plane Authority Model
- ADR-032: PKI Architecture, Revocation, and Secret Traversal
- ADR-034: Control Plane Failure Domains and Data Plane Autonomy

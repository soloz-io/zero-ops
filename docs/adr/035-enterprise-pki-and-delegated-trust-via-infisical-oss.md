# ADR-035: Enterprise PKI and Delegated Trust via Infisical OSS

**Date:** 2026-05-31
**Status:** Accepted
**Supersedes:** ADR-032 (Partial: Implementation of Delegated Spoke Issuance)

## Context

The platform requires a horizontally scalable, cryptographically isolated Public Key Infrastructure (PKI) to support mTLS across hundreds of Spoke clusters. Crossplane is explicitly banned from traversing secret material. The PKI architecture must support delegated spoke issuance, strict workload identity separation, defined compromise mitigation, and explicit lifecycle governance for all trust anchors.

Per ADR-034's clarified autonomy model, the platform guarantees Runtime Autonomy (existing workloads, secrets, and certificates continue serving traffic) but not Lifecycle Autonomy (secret rotation, certificate issuance, provisioning may pause during Hub outages). This ADR is evaluated against that clarified boundary: certificate issuance is a lifecycle operation, not a runtime requirement.

## Alternatives Considered

The following implementation approaches were evaluated against the Infisical OSS codebase (`backend/src/` vs `backend/src/ee/`) and ESO provider capabilities (`providers/v1/infisical/`):

**A. ESO Synchronizes PKI Objects (Original ADR)**
The ESO Infisical provider (`providers/v1/infisical/client.go`) wraps the Infisical Go SDK which calls secrets management endpoints only (`GET /api/v3/secrets/raw/*`). It has no support for PKI certificate operations (`/api/v1/cert-manager/*`). The provider's `Capabilities()` returns `SecretStoreReadOnly` only. This path is infeasible as written.

**B. Hub Operator Fetches Private Key via PKI API (Per-Renewal)**
The hub-operator calls `GET /api/v1/cert-manager/certificates/{certId}/private-key` on every rotation. This violates ADR-035's own constraint that "the hub-operator never handles raw private keys" and turns the hub-operator into a PKI distribution plane (contradicting ADR-005). Rejected.

**C. cert-manager + infisical-issuer (External Issuer Pattern) — Selected**
The [infisical-issuer](https://github.com/Infisical/infisical-issuer) is a cert-manager external issuer that calls the Infisical PKI API for leaf issuance. The Spoke never possesses the Intermediate CA private key. Leaf issuance depends on Infisical availability, which ADR-034 now explicitly categorizes as a lifecycle operation (may pause during outages). This is architecturally consistent: secrets, certificates, GitOps, and provisioning all follow the same Hub-dependency model.

**D. Day-0 Bootstrap Intermediate Distribution + Local cert-manager**
The Spoke Intermediate CA is provisioned during Day-0 bootstrap through the trusted bootstrap channel (per ADR-032's bootstrap trust anchor exception). Leaf certificate issuance is entirely Spoke-local thereafter via cert-manager CA issuer. Rejected after ADR-034's clarification: the platform does not require lifecycle autonomy for certificates, making the additional attack surface (500 Intermediate CA private keys at ADR-033 scale) and operational complexity (90-day dual-bundle rotation per Spoke) unjustified.

## Decision

**Trust Hierarchy:**
The platform implements a strict three-tier enterprise trust hierarchy:
1. **Offline Root CA**: An irrecoverable, air-gapped trust anchor created outside Infisical.
2. **Fleet Intermediate CA**: Managed within Infisical OSS PKI.
3. **Spoke Intermediate CA**: Dedicated per Spoke cluster, managed within Infisical OSS PKI.

Every SpokePool and SpokeSilo SHALL own a dedicated Intermediate CA, mapped one-to-one with its `cellId`. Every InfisicalIssuer SHALL be bound to the Intermediate CA associated with its cellId. Cross-cell issuance is forbidden — a compromised Spoke MUST NOT be able to issue certificates valid for another cell.

The Intermediate CA private keys never leave Infisical. The Spoke never possesses Intermediate CA key material at any point.

**Certificate Issuance:**
Spoke-local `cert-manager` instances use [infisical-issuer](https://github.com/Infisical/infisical-issuer) — a cert-manager external issuer — to request leaf certificates from Infisical PKI via the `POST /api/v1/cert-manager/certificates` API. The issuer authenticates via an Infisical Machine Identity provisioned to each Spoke, scoped to the Spoke's dedicated Intermediate CA.

This pattern is consistent with how the platform handles all other lifecycle operations: secrets (ESO + Infisical), GitOps (ArgoCD + Hub), and provisioning (Crossplane + Hub) all depend on a centralized authority. Certificate issuance follows the same model.

**Trust Anchor Distribution:**
Hub-side trust stores SHALL trust the Offline Root CA and Fleet Intermediate CA only. Individual Spoke Intermediate certificates SHALL NOT be distributed as trust anchors. Every Spoke-issued leaf certificate chains through its Spoke Intermediate → Fleet Intermediate → Offline Root, so Hub services validate any Spoke leaf naturally without managing per-Spoke trust anchors.

**Hub Operator Boundary:**
The Hub Operator participates only in Day-0 PKI provisioning: it ensures a Spoke Intermediate CA exists in Infisical at provisioning time and creates it if absent. The Hub Operator SHALL NOT continuously reconcile, rotate, renew, or otherwise manage Intermediate CA lifecycle after Day-0. Intermediate lifecycle governance (rotation, renewal, revocation) remains within Infisical and is driven by its native TTL and policy engine. This preserves the ADR-005 ownership boundary: the Hub Operator is a provisioning orchestrator, not a PKI control plane.

**Security Boundary:**
A Spoke never possesses Intermediate CA private keys. Compromise of a Spoke cluster permits misuse of issued leaf certificates only. Compromise of the Intermediate CA requires compromise of Infisical itself. Attack surface is reduced from 500 distributed Intermediate CA keys (at ADR-033 scale) to a single Fleet Intermediate within Infisical.

**Identity Separation:**
- `cert-manager` handles infrastructure identity (ingress controllers, admission webhooks, platform mTLS).
- SPIRE handles application workload identity (SVIDs for tenant workloads and service mesh).

**PKI Profiles:**
Leaf TTL is governed by centrally managed PKI profiles in Infisical:
- Infrastructure/Webhooks: 24h
- Database Clients: 4h
- Service Mesh: 1h
- Human Access: 15m

**Compromise Mitigation:**
No CRLs or OCSP responders for leaf certificates. Compromise is handled via Trust Anchor Replacement at the Infisical level: the compromised CA path is deleted and reissued centrally. All associated leaf certificates expire naturally per their TTL.

## Consequences

### Positive
- Crossplane Composition Functions are completely removed from certificate distribution.
- No Intermediate CA private keys are distributed to the fleet; attack surface is limited to Infisical alone.
- Simplified rotation: Intermediate CAs remain in Infisical and are managed centrally, eliminating per-Spoke dual-bundle rotation.
- Centralized audit trail: every issuance passes through the Infisical PKI API, providing a single issuance record for SOC2 and ISO27001.
- Consistent operational model: certificates, secrets, GitOps, and provisioning all follow the same Hub-dependency pattern.
- Disaster recovery and intermediate key generation are centralized in Infisical without risking the global Offline Root CA.

### Negative
- Certificate issuance and renewal depend on Infisical availability; leaf certificate lifecycle operations pause during Infisical outages.
- This is consistent with ADR-034's Runtime-vs-Lifecycle autonomy model: lifecycle operations depend on centralized authorities.

# ADR-035: Enterprise PKI and Delegated Trust via Infisical OSS

**Date:** 2026-05-31
**Status:** Accepted
**Supersedes:** ADR-032 (Partial: Implementation of Delegated Spoke Issuance)

## Context
The platform requires a horizontally scalable, cryptographically isolated Public Key Infrastructure (PKI) to support mTLS across hundreds of Spoke clusters. Crossplane is explicitly banned from traversing secret material. The PKI architecture must support delegated spoke issuance, strict workload identity separation, defined compromise mitigation, and explicit lifecycle governance for all trust anchors without relying on continuous Hub reconciliation.

## Decision

**Trust Hierarchy**
The platform implements a strict three-tier enterprise trust hierarchy:
1. **Offline Root CA**: An irrecoverable, air-gapped trust anchor.
2. **Fleet Intermediate CA**: Managed securely within Infisical OSS PKI.
3. **Spoke Intermediate CA**: Dedicated per Spoke cluster.

**Key Generation and Ownership**
The Infisical PKI engine exclusively generates, stores, and owns all intermediate key material. The `hub-operator` acts solely as an orchestrator that requests the creation of Spoke Intermediates via the Infisical API and provisions External Secrets Operator (ESO) references. The `hub-operator` never generates or handles raw private keys in memory.

**Declarative Delivery and Local Minting**
ESO on the target Spoke cluster synchronizes the Spoke Intermediate CA payload from Infisical into a localized Kubernetes Secret. A Spoke-local `cert-manager` `ClusterIssuer` (of type `CA`) mounts this Secret to mint local leaf certificates.

**Security Boundary and Private Key Custody**
A Spoke cluster is explicitly trusted with custody of its own Intermediate CA private key. Compromise of a Spoke cluster is equivalent to compromise of that specific Spoke Intermediate CA. The blast radius is mathematically bounded to the individual Spoke through this per-Spoke Intermediate isolation.

**Intermediate CA Lifecycle Governance**
Spoke Intermediate CAs are governed by a strict 90-day validity period (TTL). 
- **Rotation Triggers**: Scheduled expiry, trust anchor replacement events, or suspected compromise.
- **Rotation Method**: Executed via dual trust bundle distribution. A new Intermediate is issued, both new and old trust bundles are distributed to trust stores, leaf certificates are renewed against the new Intermediate, and the old Intermediate is removed after a defined overlap period to ensure zero downtime.

**Identity Separation (SPIRE Coexistence)**
The platform enforces a strict boundary between infrastructure identity and workload identity:
- `cert-manager` handles infrastructure identity (ingress controllers, admission webhooks, and foundational platform mTLS).
- SPIRE handles application workload identity (SVIDs for tenant workloads and service mesh integration).

**PKI Profiles for Leaf Lifecycle**
Leaf certificate Time-To-Live (TTL) is governed by centrally managed PKI profiles in Infisical, eliminating blanket TTL policies:
- Infrastructure/Webhooks: 24h
- Database Clients: 4h
- Service Mesh: 1h
- Human Access: 15m

**Compromise Mitigation (Trust Anchor Replacement)**
The platform does not maintain Certificate Revocation Lists (CRLs) or OCSP responders for leaf certificates. Compromise mitigation is handled exclusively via Trust Anchor Replacement. A compromised Spoke cluster requires the explicit deletion and rotation of its Spoke Intermediate CA in Infisical, followed by an automated fleet rollout of the new trust bundle.

## Consequences

- Crossplane Composition Functions are completely removed from certificate distribution.
- The Hub API server and cert-manager are relieved of leaf-certificate reconciliation traffic.
- Spoke clusters achieve total cryptographic autonomy for Day-2 runtime infrastructure operations.
- The Hub Operator is prevented from becoming a PKI control plane or key-management system.
- Blast radius isolation is mathematically enforced; a compromised Spoke cluster only possesses its own Intermediate CA.
- Disaster recovery, auditability, and intermediate key generation are centralized in Infisical without risking the global Offline Root CA.
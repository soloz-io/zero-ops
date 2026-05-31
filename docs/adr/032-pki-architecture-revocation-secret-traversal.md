# ADR 032: PKI Architecture, Revocation, and Secret Traversal

## Status

Accepted

## Supersedes

Centralized Leaf Issuance

## Context

Centralized Hub leaf certificate issuance creates a massive reconciliation bottleneck at scale. Furthermore, allowing raw secret material to traverse Crossplane Composition Functions risks security exposure. The platform requires a scalable issuance and revocation model.

## Decision

**Delegated Spoke Issuance:**
The Hub cluster will solely generate and securely distribute Intermediate CA certificates to Spoke clusters. Spoke clusters will run local `cert-manager` instances to mint, rotate, and manage their own local leaf certificates for workloads (e.g., SPIRE, PostgREST).

**Revocation Strategy:**
Leaf certificates are strictly bounded to a 1-hour Time-To-Live (TTL). No Certificate Revocation Lists (CRLs) or OCSP responders will be maintained for leaf certificates. A compromised Spoke cluster requires manual intervention to replace and redistribute trust anchors for the Hub-issued Intermediate CA specific to that Spoke, isolating the blast radius to a single cluster.

**Secret Traversal Constraint:**
Crossplane Composition Functions are explicitly prohibited from parsing, manipulating, or transporting application-level secret material. Function payload traversal is strictly limited to Day-0 bootstrap trust anchors (Intermediate CAs). All application secrets must utilize the Infisical-to-ESO runtime resolution pattern.

## Consequences

### Positive
- Eliminates Hub-side certificate issuance bottleneck.
- Short-lived leaf certificates (1h TTL) eliminate need for CRLs or OCSP.
- Secret traversal constraint prevents exposure in Composition Functions.

### Negative
- Compromised Spoke requires manual Intermediate CA trust anchor replacement.

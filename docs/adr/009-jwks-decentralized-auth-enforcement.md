# ADR-0009: Centralized Auth with Decentralized Enforcement via JWKS in Spoke Clusters

**Date:** 2025-01-20  
**Status:** Accepted  
**Context:** Authentication & Security Architecture  

## Context

The Zero-Ops platform utilizes a Hub-Spoke architecture to provision isolated, multi-tenant SaaS environments. 
*   **The Hub cluster** centrally manages identity, access, and token issuance using the Ory stack (Hydra, Kratos, Keto).
*   **The Spoke clusters (Cells)** run tenant-specific data planes, primarily leveraging PostgREST to expose PostgreSQL databases as REST APIs to the tenants' applications.

To secure the tenant APIs, PostgREST must validate JSON Web Tokens (JWTs) attached to incoming requests. The initial design implicitly relied on a **Symmetric Key Strategy**, which involved synchronizing the Hub’s JWT signing secret (`hub-ory-jwt-secret`) to every tenant namespace across all Spoke clusters via the External Secrets Operator (ESO) or Crossplane.

This approach presented several critical architectural flaws:
1.  **Secret Sprawl:** Highly sensitive cryptographic secrets (signing keys) bleed out of the secure Hub control plane into loosely-trusted Spoke clusters, massively increasing the blast radius of a compromised Spoke node.
2.  **Rotation Nightmare:** Rotating the JWT signing key in Ory Hydra would require synchronous updates across hundreds of tenant namespaces in external clusters. Any latency or failure in this sync process results in API downtime for tenants (token validation failures).
3.  **Tight Coupling:** It violates the autonomous nature of Spoke clusters by strictly coupling their bootstrap sequence to secret-replication controllers.

## Decision

We will adopt a **Centralized Authentication + Decentralized Enforcement** pattern utilizing **Asymmetric Cryptography (RS256)** and the **JSON Web Key Set (JWKS)** protocol.

1.  **Centralized Issuance:** Ory Hydra on the Hub cluster will be the sole issuer of JWTs, signing them with a private RSA key that never leaves the Hub.
2.  **Public Key Distribution:** Hydra will expose its public keys via the standard OIDC endpoint (`https://auth.nutgraf.in/.well-known/jwks.json`).
3.  **Decentralized Stateless Validation:** PostgREST instances in the Spoke clusters will be configured with a `jwks_uri` pointing to the Hub's public JWKS endpoint. 
4.  **Local Caching:** PostgREST will natively fetch and cache these public keys in memory (configured via `PGRST_JWT_CACHE_MAX_LIFETIME=3600`) to validate incoming request signatures locally.
5.  **Audience Enforcement:** PostgREST will be configured to strictly enforce the `aud` (Audience) claim (`PGRST_JWT_AUD="https://api.nutgraf.in/mcp"`) to prevent token misuse across different contexts.

## Consequences

### Positive
*   **Zero Secret Replication (Highest Security):** No identity secrets or signing keys are replicated to the Spoke clusters. Spoke clusters only need access to public, non-sensitive keys.
*   **Hitless Key Rotation:** Ory Hydra can seamlessly rotate signing keys. PostgREST will automatically fetch the new keys from the JWKS endpoint upon encountering an unknown `kid` (Key ID) or when the cache expires, resulting in zero downtime.
*   **High Performance:** After the initial JWKS fetch, token validation happens locally in memory on the Spoke cluster. There is no network round-trip to the Hub for every API request.
*   **Loose Coupling:** Spoke clusters can be provisioned, destroyed, and scaled instantly without waiting for secret-syncing operators to populate auth credentials.

### Negative / Trade-offs
*   **Egress Dependency:** Spoke clusters must maintain egress network connectivity to the Hub's public auth URL (`https://auth.nutgraf.in`). If this URL is unreachable during a cache miss or pod startup, new token validation will temporarily fail.
*   **Cold Start Latency:** The very first API request hitting a newly spun-up PostgREST pod will incur a slight latency penalty (typically <50ms) while the pod fetches the JWKS payload from the Hub.
*   **Stateless Revocation Limits:** Because validation is fully decentralized and stateless, revoked tokens will remain valid at the Spoke level until their standard expiration time (TTL). We mitigate this by keeping JWT lifespans relatively short (e.g., 10-15 minutes) and relying on the Hub for refresh-token rotation.
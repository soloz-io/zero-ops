# ADR-0009: Zero-Trust Multi-Layer Authentication with JWKS and Service Mesh

**Date:** 2026-04-28  
**Status:** Accepted  
**Context:** Zero-Trust Security Architecture

## Context

The Zero-Ops platform utilizes a Hub-Spoke architecture to provision isolated, multi-tenant SaaS environments with **zero-trust security principles**.

*   **The Hub cluster** centrally manages identity, access, and token issuance using the Ory stack (Hydra, Kratos, Keto).
*   **The Spoke clusters (Cells)** run tenant-specific data planes, primarily leveraging PostgREST to expose PostgreSQL databases as REST APIs.

### Security Requirements

Modern zero-trust architecture requires **defense-in-depth** with multiple independent security layers:

1. **Layer 1 (Edge):** User authentication and authorization at the gateway
2. **Layer 2 (Network):** Cryptographic workload identity and mTLS between services
3. **Layer 3 (Service):** Application-level identity validation
4. **Layer 4 (Data):** Row-level security and tenant isolation

### Initial Design Challenges

**Challenge 1: Symmetric Key Strategy**
- Synchronizing Hub's JWT signing secret to every Spoke cluster
- **Secret Sprawl:** Cryptographic secrets leaked to loosely-trusted Spoke clusters
- **Rotation Complexity:** Key rotation required synchronous updates across hundreds of namespaces
- **Tight Coupling:** Spoke clusters dependent on secret-replication controllers

**Challenge 2: Incomplete Security Model**
- No cryptographic workload identity (SPIFFE/SPIRE)
- No mTLS enforcement between services
- Vulnerable to header spoofing if network compromised
- Single validation point (gateway only) - not defense-in-depth
- No protection against lateral movement after compromise

## Decision

We adopt a **4-Layer Zero-Trust Architecture** combining asymmetric cryptography, service mesh, and defense-in-depth validation.

### Layer 1: Edge Gateway (AgentGateway)

**Primary User Authentication**

1.  **Centralized Issuance:** Ory Hydra on the Hub cluster is the sole issuer of JWTs, signing them with a private RSA key that never leaves the Hub.
2.  **Public Key Distribution:** Hydra exposes public keys via standard OIDC endpoint (`https://auth.nutgraf.in/.well-known/jwks.json`).
3.  **JWT Validation:** AgentGateway validates JWT signature, audience, expiration, and issuer.
4.  **Claims Extraction:** Extracts `tenant_id`, `sub` (user), and `aud` (audience) from JWT claims.
5.  **Header Injection:** Injects `X-Tenant-ID`, `X-User-ID`, `X-Audience` headers.
6.  **JWT Forwarding:** Forwards JWT to downstream services for defense-in-depth validation.

### Layer 2: Service Mesh (Istio + SPIFFE/SPIRE)

**Cryptographic Workload Identity**

1.  **SPIFFE Identity:** Every pod receives a SPIFFE SVID (X.509 certificate) with format:
    ```
    spiffe://cluster.local/ns/{namespace}/sa/{serviceaccount}
    ```
2.  **Auto-Rotation:** SVIDs auto-rotate every 60 minutes (short-lived certificates).
3.  **mTLS Enforcement:** Istio `PeerAuthentication` in STRICT mode - all traffic must use mTLS.
4.  **Authorization Policy:** Istio `AuthorizationPolicy` restricts PostgREST access:
    ```yaml
    principals:
      - "cluster.local/ns/platform-edge/sa/agentgateway"
    ```
5.  **Prevents Spoofing:** Cryptographic enforcement prevents header injection attacks.

**Why This Matters:**
- Headers alone can be spoofed if network is compromised
- mTLS + SPIFFE provides cryptographic proof of workload identity
- Even if attacker compromises a pod, they cannot impersonate AgentGateway without its private key

### Layer 3: Service (PostgREST)

**Secondary JWT Validation (Defense-in-Depth)**

1.  **JWKS Validation:** PostgREST configured with `jwks_uri` pointing to Hub's JWKS endpoint.
2.  **Local Caching:** PostgREST caches public keys in memory (`PGRST_JWT_CACHE_MAX_LIFETIME=3600`).
3.  **Audience Enforcement:** Strictly enforces `aud` claim (`PGRST_JWT_AUD="urn:zero-ops:tenant:*:api"`).
4.  **Tenant Extraction:** Extracts `tenant_id` from JWT claims.
5.  **Session Variable:** Sets PostgreSQL session variable `app.current_tenant`.

**Why JWT Validation Here?**
- **Not for trust boundary** (Layer 2 mTLS handles that)
- **Defense-in-depth:** Independent validation layer
- **Application-level identity:** Validates user/tenant claims, not just network identity
- **Native support:** PostgREST has built-in JWT validation (no custom code)
- **Performance:** JWT caching provides ~20% throughput improvement

### Layer 4: Data (PostgreSQL RLS)

**Final Enforcement at Data Level**

1.  **Row Level Security:** RLS policies enforce tenant isolation:
    ```sql
    CREATE POLICY tenant_isolation ON resources
      USING (tenant_id = current_setting('app.current_tenant'));
    ```
2.  **Defense-in-Depth:** Protects data even if upper layers compromised.

## Architecture Diagram

```
User → [JWT] → AgentGateway (Layer 1: JWT validation)
                     ↓
              mTLS + SPIFFE (Layer 2: Workload identity)
                     ↓
              PostgREST (Layer 3: JWT validation)
                     ↓
              PostgreSQL RLS (Layer 4: Data isolation)
```

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Certificates | Kubernetes API | cert-manager | cert-manager | Workloads, ArgoCD Agent | Day-1+ |
| Workload Identity | SPIRE Server | SPIRE | SPIRE | Istio, Tenant Apps | Day-1+ |

See ADR-039 for the complete ownership matrix.

## Consequences

### Positive

**Security**
*   **Zero Secret Replication:** No signing keys replicated to Spoke clusters (only public keys).
*   **Defense-in-Depth:** 4 independent security layers - compromise of one layer doesn't break security.
*   **Cryptographic Identity:** SPIFFE SVIDs provide unforgeable workload identity.
*   **Prevents Header Spoofing:** mTLS enforcement prevents network-level attacks.
*   **Prevents Lateral Movement:** AuthorizationPolicy restricts service-to-service access.
*   **Zero-Trust Compliant:** "Never trust, always verify" at every layer.

**Operational**
*   **Hitless Key Rotation:** Ory Hydra can rotate signing keys with zero downtime.
*   **Auto-Rotating Certificates:** SPIFFE SVIDs rotate every 60 minutes automatically.
*   **High Performance:** JWT caching + mTLS hardware acceleration.
*   **Loose Coupling:** Spoke clusters can be provisioned instantly without secret sync.
*   **Distributed Policy:** Istio AuthorizationPolicy scales with services.

**Industry Alignment**
*   **CNCF Standards:** Istio, SPIFFE/SPIRE (used by Google, AWS, Netflix, Uber).
*   **Modern Pattern:** Matches 2025-2026 zero-trust best practices.
*   **Well-Documented:** Extensive community resources and production examples.

### Trade-offs

*   **Additional Components:** Requires Istio + SPIRE deployment and management.
*   **Learning Curve:** Team must understand service mesh concepts and SPIFFE.
*   **Debugging Complexity:** mTLS can complicate troubleshooting (requires cert inspection tools).
*   **Egress Dependency:** Spoke clusters must reach Hub's JWKS endpoint (`https://auth.nutgraf.in`).
*   **Cold Start Latency:** First request to new PostgREST pod incurs JWKS fetch (~50ms).
*   **Mesh Overhead:** Envoy sidecars add ~5-10ms latency and memory overhead per pod.
*   **Stateless Revocation:** Revoked JWTs remain valid until expiration (mitigated by short TTL: 10-15 min).
*   **Certificate Management:** SPIRE server must be highly available (single point of failure for cert issuance).

## Implementation Notes

### PostgREST Configuration

```bash
# Enable JWT validation (secondary defense-in-depth)
PGRST_JWT_SECRET=""  # Empty - use JWKS instead
PGRST_JWT_SECRET_IS_BASE64=false
PGRST_JWT_AUD="urn:zero-ops:tenant:*:api"
PGRST_JWT_ROLE_CLAIM_KEY=".tenant_id"

# JWKS endpoint (Hub)
PGRST_OPENAPI_SERVER_PROXY_URI="https://auth.nutgraf.in"

# JWT caching for performance
PGRST_JWT_CACHE_MAX_ENTRIES=1000
PGRST_JWT_CACHE_MAX_LIFETIME=3600
```

### Istio Configuration

```yaml
# mTLS enforcement (STRICT mode)
apiVersion: security.istio.io/v1
kind: PeerAuthentication
metadata:
  name: default
  namespace: tenant-app-creator
spec:
  mtls:
    mode: STRICT

---
# Authorization policy (only gateway can reach PostgREST)
apiVersion: security.istio.io/v1
kind: AuthorizationPolicy
metadata:
  name: postgrest-only-from-gateway
  namespace: tenant-app-creator
spec:
  selector:
    matchLabels:
      app: postgrest
  action: ALLOW
  rules:
    - from:
        - source:
            principals:
              - "cluster.local/ns/platform-edge/sa/agentgateway"
```

## References

- [Istio Security Best Practices (2025-2026)](https://istio.io/latest/docs/ops/configuration/security/)
- [SPIFFE/SPIRE Workload Identity](https://spiffe.io/)
- [PostgREST JWT Authentication](https://postgrest.org/en/stable/references/auth.html)
- [Zero-Trust Architecture (NIST SP 800-207)](https://csrc.nist.gov/publications/detail/sp/800-207/final)
- [Google Cloud GKE Multi-Tenancy with Workload Identity](https://cloud.google.com/service-mesh/docs/security/security-overview)

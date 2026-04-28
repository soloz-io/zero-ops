# Zero-Trust Hub-Spoke Multi-Tenant Architecture (v2)

**Pattern Source**: Istio Service Mesh Security (2025-2026), SPIFFE/SPIRE Workload Identity, AWS Multi-Tenant SaaS, Google Cloud GKE Multi-Tenancy

**Key Principles**:
1. **Layered Defense-in-Depth** - Multiple validation layers (Gateway → Mesh → Service → Data)
2. **Cryptographic Identity** - SPIFFE SVIDs for workload authentication, not just headers
3. **Zero-Trust Networking** - mTLS everywhere, "never trust, always verify"
4. **Single Gateway at Hub** - Centralized entry point for north-south traffic
5. **Distributed Policy Enforcement** - Service mesh for east-west traffic authorization

---

## Architecture Diagram

```
┌─────────────────────────────────────────────────────────────────────────────────────────────────┐
│                                    HUB CLUSTER (Control Plane)                                   │
│                                                                                                  │
│  ┌──────────────────────────────────────────────────────────────────────────────────────────┐  │
│  │                         Identity Provider (Ory Hydra + Kratos)                            │  │
│  │                                                                                            │  │
│  │   ┌─────────────┐         ┌──────────────┐                                               │  │
│  │   │ Ory Kratos  │────────▶│  Ory Hydra   │                                               │  │
│  │   │  (Identity) │         │ (OAuth/OIDC) │                                               │  │
│  │   └─────────────┘         └──────┬───────┘                                               │  │
│  │                                  │                                                         │  │
│  │                                  │ Issues JWT with claims:                                │  │
│  │                                  │ {                                                      │  │
│  │                                  │   "sub": "user-123",                                   │  │
│  │                                  │   "aud": "urn:zero-ops:tenant:app-creator:api",        │  │
│  │                                  │   "tenant_id": "app-creator",                          │  │
│  │                                  │   "exp": 1734567890                                    │  │
│  │                                  │ }                                                      │  │
│  │                                  │                                                         │  │
│  │                                  │ Exposes JWKS:                                          │  │
│  │                                  │ https://auth.nutgraf.in/.well-known/jwks.json         │  │
│  └──────────────────────────────────┼─────────────────────────────────────────────────────────┘  │
│                                     │                                                            │
│  ┌──────────────────────────────────▼─────────────────────────────────────────────────────────┐  │
│  │                    LAYER 1: AgentGateway (Edge - North-South Traffic)                      │  │
│  │                                                                                             │  │
│  │  ┌──────────────────────────────────────────────────────────────────────────────────┐     │  │
│  │  │ JWT Validation (PRIMARY Enforcement)                                              │     │  │
│  │  │ ────────────────────────────────────                                              │     │  │
│  │  │ • Validates JWT signature via JWKS                                                │     │  │
│  │  │ • Checks audience, expiration, issuer                                             │     │  │
│  │  │ • Extracts claims (tenant_id, sub, aud)                                           │     │  │
│  │  │ • Injects headers: X-Tenant-ID, X-User-ID, X-Audience                             │     │  │
│  │  │ • Forwards JWT to downstream (for defense-in-depth)                               │     │  │
│  │  └──────────────────────────────────────────────────────────────────────────────────┘     │  │
│  │                                                                                             │  │
│  │  ┌──────────────────────────────────────────────────────────────────────────────────┐     │  │
│  │  │ Telemetry & Observability (OpenTelemetry Integration)                             │     │  │
│  │  │ ──────────────────────────────────────────────────────                             │     │  │
│  │  │ • OTLP Traces: Distributed tracing across Hub-Spoke requests                      │     │  │
│  │  │ • OTLP Metrics: Request rates, latencies, error rates                             │     │  │
│  │  │ • Usage Events: tenant_id, user_id, method, path, status, duration                │     │  │
│  │  │ • Exports to: Jaeger (traces), VictoriaMetrics (metrics), OpenMeter (billing)          │     │  │
│  │  │ • Business Events: Authentication, authorization, tenant operations               │     │  │
│  │  └──────────────────────────────────────────────────────────────────────────────────┘     │  │
│  │                                                                                             │  │
│  │  ┌──────────────────────────────────────────────────────────────────────────────────┐     │  │
│  │  │ Routing                                                                            │     │  │
│  │  │ ───────                                                                            │     │  │
│  │  │ • /mcp/* → MCP Server (Hub-local)                                                 │     │  │
│  │  │ • /tenant/{id}/api/* → Spoke PostgREST (via mTLS)                                 │     │  │
│  │  └──────────────────────────────────────────────────────────────────────────────────┘     │  │
│  └─────────────────────────────────────────────────────────────────────────────────────────────┘  │
│                                                                                                  │
│  ┌──────────────────────────────────────────────────────────────────────────────────────────┐  │
│  │                    Telemetry & Observability (OpenTelemetry Stack)                        │  │
│  │                                                                                            │  │
│  │   ┌──────────────┐   ┌──────────────┐   ┌──────────────┐   ┌──────────────┐            │  │
│  │   │   OTLP       │──▶│   Jaeger     │   │VictoriaMetrics│   │  OpenMeter   │            │  │
│  │   │ Collector    │   │  (Traces)    │   │  (Metrics)   │   │  (Billing)   │            │  │
│  │   └──────▲───────┘   └──────────────┘   └──────────────┘   └──────▲───────┘            │  │
│  │          │                                                         │                     │  │
│  │          │ ◀─── AgentGateway OTLP traces + metrics                 │                     │  │
│  │          │                                                         │                     │  │
│  │          │                                                         │ ◀─── Usage events  │  │
│  │          │                                                                               │  │
│  │   ┌──────▼──────┐   ┌──────────────┐   ┌──────────────┐   ┌──────────────┐            │  │
│  │   │ Distributed │   │  OpenSearch  │   │  ClickHouse  │   │   Billing    │            │  │
│  │   │   Tracing   │   │ (Log Search) │   │ (Log Aggr.)  │   │   Engine     │            │  │
│  │   └─────────────┘   └──────────────┘   └──────────────┘   └──────────────┘            │  │
│  │                                                                                          │  │
│  │   ┌──────────────┐   ┌──────────────┐                                                  │  │
│  │   │Grafana Alloy │   │    K8sGPT    │                                                  │  │
│  │   │(Collection)  │   │(AI Diagnostics)│                                                │  │
│  │   └──────────────┘   └──────────────┘                                                  │  │
│  └──────────────────────────────────────────────────────────────────────────────────────────┘  │
│             │                                                                                    │
│  ┌──────────┼────────────────────────────────────────────────────────────────────────────────┐  │
│  │          │                    GitOps & Orchestration                                       │  │
│  │          │                                                                                 │  │
│  │   ┌──────▼──────┐         ┌──────────────┐         ┌─────────────────┐                  │  │
│  │   │   ArgoCD    │────────▶│  Crossplane  │────────▶│  Spoke Clusters │                  │  │
│  │   │  (GitOps)   │         │     (XRD)    │         │  Provisioning   │                  │  │
│  │   └─────────────┘         └──────────────┘         └─────────────────┘                  │  │
│  └──────────────────────────────────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────────────────────────────────────┘
                  │
                  │ mTLS Connection (Istio Service Mesh)
                  │ Cryptographic Identity: SPIFFE SVIDs
                  │ Forwards: JWT + Headers (X-Tenant-ID, X-User-ID, X-Audience)
                  │
┌─────────────────▼──────────────────────────────────────────────────────────────────────────────┐
│              SPOKE CLUSTER (Tenant: app-creator) - Zero-Trust Architecture                      │
│                                                                                                  │
│  ┌──────────────────────────────────────────────────────────────────────────────────────────┐  │
│  │                    LAYER 2: Service Mesh (Istio + SPIFFE/SPIRE)                           │  │
│  │                                                                                            │  │
│  │  ┌──────────────────────────────────────────────────────────────────────────────────┐    │  │
│  │  │ Workload Identity (SPIFFE)                                                        │    │  │
│  │  │ ──────────────────────────                                                        │    │  │
│  │  │ • Every pod gets SPIFFE SVID (X.509 certificate)                                 │    │  │
│  │  │ • Format: spiffe://cluster.local/ns/{namespace}/sa/{serviceaccount}              │    │  │
│  │  │ • Auto-rotated every 60 minutes                                                   │    │  │
│  │  │ • Example: spiffe://cluster.local/ns/tenant-app-creator/sa/postgrest             │    │  │
│  │  └──────────────────────────────────────────────────────────────────────────────────┘    │  │
│  │                                                                                            │  │
│  │  ┌──────────────────────────────────────────────────────────────────────────────────┐    │  │
│  │  │ mTLS Enforcement (Istio PeerAuthentication)                                       │    │  │
│  │  │ ───────────────────────────────────────────                                       │    │  │
│  │  │ • STRICT mode - all traffic must use mTLS                                         │    │  │
│  │  │ • Prevents header spoofing via cryptographic enforcement                          │    │  │
│  │  │ • Both client and server authenticate via certificates                            │    │  │
│  │  └──────────────────────────────────────────────────────────────────────────────────┘    │  │
│  │                                                                                            │  │
│  │  ┌──────────────────────────────────────────────────────────────────────────────────┐    │  │
│  │  │ Authorization Policy (Istio)                                                      │    │  │
│  │  │ ────────────────────────────                                                      │    │  │
│  │  │ • ONLY AgentGateway SPIFFE ID can reach PostgREST                                 │    │  │
│  │  │ • Blocks all other traffic (default-deny)                                         │    │  │
│  │  │ • Validates source principal cryptographically                                    │    │  │
│  │  │                                                                                    │    │  │
│  │  │ Example:                                                                           │    │  │
│  │  │   principals:                                                                      │    │  │
│  │  │     - "cluster.local/ns/hub-platform-edge/sa/agentgateway"                        │    │  │
│  │  └──────────────────────────────────────────────────────────────────────────────────┘    │  │
│  └──────────────────────────────────────────────────────────────────────────────────────────┘  │
│                                                                                                  │
│  ┌──────────────────────────────────────────────────────────────────────────────────────────┐  │
│  │              Tenant Namespace: tenant-app-creator                                         │  │
│  │                                                                                            │  │
│  │   ┌────────────────────────────────────────────────────────────────────────────────┐     │  │
│  │   │                    LAYER 3: PostgREST (Service)                                 │     │  │
│  │   │                                                                                  │     │  │
│  │   │  JWT Validation (SECONDARY Defense-in-Depth)                                    │     │  │
│  │   │  ────────────────────────────────────────────                                   │     │  │
│  │   │  • Validates JWT signature via JWKS (same as Gateway)                           │     │  │
│  │   │  • Extracts tenant_id from JWT claims                                           │     │  │
│  │   │  • Sets PostgreSQL session variable                                             │     │  │
│  │   │  • Provides defense-in-depth (not sole security layer)                          │     │  │
│  │   │                                                                                  │     │  │
│  │   │  Why JWT validation here?                                                       │     │  │
│  │   │  • Layer 2 (mTLS) prevents network-level attacks                                │     │  │
│  │   │  • Layer 3 (JWT) validates application-level identity                           │     │  │
│  │   │  • Defense-in-depth: multiple independent security layers                       │     │  │
│  │   └──────────────────────────────────┬───────────────────────────────────────────────┘     │  │
│  │                                      │                                                     │  │
│  │   ┌──────────────────────────────────▼───────────────────────────────────────────────┐     │  │
│  │   │                     LAYER 4: PostgreSQL (Data)                                   │     │  │
│  │   │                                                                                   │     │  │
│  │   │  Row Level Security (RLS) Policies:                                              │     │  │
│  │   │  ──────────────────────────────────                                              │     │  │
│  │   │  CREATE POLICY tenant_isolation ON resources                                     │     │  │
│  │   │    USING (tenant_id = current_setting('app.current_tenant'));                    │     │  │
│  │   │                                                                                   │     │  │
│  │   │  • Final enforcement layer at data level                                         │     │  │
│  │   │  • Ensures tenant isolation even if upper layers compromised                     │     │  │
│  │   └───────────────────────────────────────────────────────────────────────────────────┘     │  │
│  └──────────────────────────────────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────────────────────────────────────┘


┌─────────────────────────────────────────────────────────────────────────────────────────────────┐
│                         DATA FLOW: Tenant API Request (Zero-Trust)                              │
└─────────────────────────────────────────────────────────────────────────────────────────────────┘

Step 1: Authentication (Hub)
────────────────────────────
User → Ory Kratos → Ory Hydra → JWT Token Issued
                                 {
                                   "sub": "user-123",
                                   "aud": "urn:zero-ops:tenant:app-creator:api",
                                   "tenant_id": "app-creator",
                                   "exp": 1734567890
                                 }

Step 2: Layer 1 - Edge Gateway (Hub)
─────────────────────────────────────
User → [JWT] → AgentGateway
                    │
                    ├─▶ Validate JWT (PRIMARY)
                    │   ✓ Signature, audience, expiration, issuer
                    │
                    ├─▶ Extract Claims → Inject Headers
                    │   • X-Tenant-ID: "app-creator"
                    │   • X-User-ID: "user-123"
                    │   • X-Audience: "urn:zero-ops:tenant:app-creator:api"
                    │
                    ├─▶ Forward JWT + Headers to Spoke
                    │
                    ├─▶ Capture Telemetry → OTLP Collector
                    │   • Distributed traces (request flow Hub→Spoke)
                    │   • Metrics (latency, throughput, errors)
                    │   • Usage events → OpenMeter (billing)
                    │
                    ▼
                Route to Spoke via mTLS

Step 3: Layer 2 - Service Mesh (Spoke)
───────────────────────────────────────
AgentGateway Envoy Sidecar
        │
        ├─▶ Present SPIFFE SVID (X.509 certificate)
        │   spiffe://cluster.local/ns/hub-platform-edge/sa/agentgateway
        │
        ▼
    mTLS Handshake
        │
        ├─▶ Mutual certificate validation
        │   ✓ AgentGateway proves identity
        │   ✓ PostgREST proves identity
        │
        ▼
PostgREST Envoy Sidecar
        │
        ├─▶ Validate source principal (Istio AuthorizationPolicy)
        │   ✓ ALLOW if principal == "cluster.local/ns/hub-platform-edge/sa/agentgateway"
        │   ✗ DENY all other principals
        │
        ├─▶ Forward request to PostgREST container
        │   (JWT + Headers passed through)
        │
        ▼

Step 4: Layer 3 - Service (PostgREST)
──────────────────────────────────────
PostgREST Container
        │
        ├─▶ Validate JWT (SECONDARY defense-in-depth)
        │   ✓ Signature via JWKS
        │   ✓ Audience, expiration
        │
        ├─▶ Extract tenant_id from JWT claims
        │   tenant_id = "app-creator"
        │
        ├─▶ Set PostgreSQL session variable
        │   SET LOCAL app.current_tenant = 'app-creator'
        │
        ▼

Step 5: Layer 4 - Data (PostgreSQL)
────────────────────────────────────
PostgreSQL
        │
        ├─▶ Apply RLS policies
        │   USING (tenant_id = current_setting('app.current_tenant'))
        │
        ├─▶ Execute query (tenant-isolated)
        │
        ▼
    Response → PostgREST → Envoy → mTLS → AgentGateway → User

Step 6: Telemetry Processing (Hub)
───────────────────────────────────
AgentGateway OpenTelemetry Integration
        │
        ├─▶ Export OTLP traces → Jaeger
        │   • End-to-end request tracing (Hub→Spoke)
        │   • Span context propagation
        │   • Performance analysis
        │
        ├─▶ Export OTLP metrics → VictoriaMetrics
        │   • Request rates, latencies, error rates
        │   • Gateway performance metrics
        │   • Service health indicators
        │
        ├─▶ Transform usage events → OpenMeter
        │   • CloudEvents format
        │   • Billing and metering data
        │
        ▼
OTLP Collector → {Jaeger, VictoriaMetrics, OpenMeter}


┌─────────────────────────────────────────────────────────────────────────────────────────────────┐
│                              SECURITY LAYERS COMPARISON                                          │
└─────────────────────────────────────────────────────────────────────────────────────────────────┘

Layer          Purpose                      Technology           Validates
─────────────────────────────────────────────────────────────────────────────────────────────────
Layer 1        Edge Authentication          AgentGateway         JWT signature, claims
(Gateway)      Primary enforcement          (Envoy)              User identity
               Usage tracking                                    Tenant identity

Layer 2        Network Security             Istio + SPIFFE       Workload identity (X.509)
(Mesh)         Prevents spoofing            mTLS                 Source principal
               Cryptographic enforcement    AuthorizationPolicy  Service-to-service auth

Layer 3        Application Security         PostgREST            JWT signature, claims
(Service)      Defense-in-depth             (Secondary)          Tenant identity
               Business logic auth                               User permissions

Layer 4        Data Security                PostgreSQL RLS       Tenant isolation
(Data)         Final enforcement            Row-level policies   Data access control
─────────────────────────────────────────────────────────────────────────────────────────────────


┌─────────────────────────────────────────────────────────────────────────────────────────────────┐
│                                  KEY DESIGN DECISIONS                                            │
└─────────────────────────────────────────────────────────────────────────────────────────────────┘

1. Why Forward JWT to Spoke?
   ──────────────────────────
   ✓ Defense-in-depth: Multiple independent validation layers
   ✓ PostgREST natively supports JWT validation (no custom code)
   ✓ Application-level identity verification (not just network)
   ✓ JWT caching mitigates performance overhead (~20% improvement)
   ✗ NOT for trust boundary (Layer 2 mTLS handles that)

2. Why mTLS + SPIFFE/SPIRE?
   ────────────────────────
   ✓ Cryptographic workload identity (not spoofable headers)
   ✓ Prevents lateral movement after compromise
   ✓ Industry standard (CNCF, used by Google, AWS, Netflix)
   ✓ Auto-rotating certificates (short-lived, 60min)
   ✓ Zero-trust networking ("never trust, always verify")

3. Why Istio AuthorizationPolicy?
   ───────────────────────────────
   ✓ Enforces "only gateway can reach PostgREST" at network layer
   ✓ Validates source principal cryptographically (SPIFFE SVID)
   ✓ Default-deny security model
   ✓ Distributed policy enforcement (scales with services)
   ✓ Handles east-west traffic (service-to-service)

4. Why Keep PostgreSQL RLS?
   ─────────────────────────
   ✓ Final defense layer at data level
   ✓ Protects against compromised application layer
   ✓ Ensures tenant isolation even if JWT/mTLS bypassed
   ✓ Database-native security (no application code)

5. Why OpenTelemetry Integration?
   ──────────────────────────────
   ✓ Complete observability stack (traces, metrics, logs)
   ✓ Industry standard (CNCF, vendor-neutral)
   ✓ Both AgentGateway and OpenMeter support OTLP natively
   ✓ Distributed tracing across Hub-Spoke architecture
   ✓ Business events + technical metrics in single pipeline
   ✓ Vendor-agnostic (works with Jaeger, VictoriaMetrics, DataDog, etc.)
   ✓ Integrates with zero-ops observability stack (VictoriaMetrics, OpenSearch, ClickHouse)

6. Why OpenMeter for Usage Billing?
   ─────────────────────────────────
   ✓ Native OpenTelemetry support (OTLP traces, metrics, logs)
   ✓ Real-time usage metering and billing engine
   ✓ CloudEvents ingestion for usage tracking
   ✓ Complements zero-ops observability stack
   ⚠️ Note: OpenMeter is additional to core tech stack for billing use case

7. Why Usage Tracking at Gateway?
   ───────────────────────────────
   ✓ Single source of truth for all traffic
   ✓ Captures north-south and east-west traffic
   ✓ No per-service instrumentation needed
   ✓ Consistent telemetry format (OTLP + CloudEvents)
   ✓ Async, non-blocking (no request latency impact)


┌─────────────────────────────────────────────────────────────────────────────────────────────────┐
│                              PATTERN JUSTIFICATION                                               │
└─────────────────────────────────────────────────────────────────────────────────────────────────┘

This architecture implements the **modern 3-layer zero-trust pattern** documented in:
- Istio Service Mesh Security Best Practices (2025-2026)
- SPIFFE/SPIRE Workload Identity Standard (CNCF)
- Google Cloud GKE Multi-Tenancy with Workload Identity
- AWS Multi-Tenant SaaS with IAM Roles (cryptographic enforcement)

Compared to v1 (2-layer pattern):

v1 Pattern (INCOMPLETE)                    v2 Pattern (COMPLETE)
─────────────────────────────────────────────────────────────────────────────
Layer 1: Gateway (JWT)          ✓          Layer 1: Gateway (JWT)          ✓
Layer 2: Network (mTLS)         ✗          Layer 2: Mesh (mTLS + SPIFFE)   ✓
Layer 3: Service (Headers)      ⚠️          Layer 3: Service (JWT)          ✓
Layer 4: Data (RLS)             ✓          Layer 4: Data (RLS)             ✓

Security Model:
- v1: Trust headers after gateway validation (weak)
- v2: Cryptographic identity at every layer (strong)

Attack Resistance:
- v1: Header spoofing possible if network compromised
- v2: Requires breaking mTLS + JWT + RLS (defense-in-depth)

Industry Alignment:
- v1: Simplified pattern, not production-grade
- v2: Standard enterprise zero-trust architecture


┌─────────────────────────────────────────────────────────────────────────────────────────────────┐
│                                  WHAT THIS ACHIEVES                                              │
└─────────────────────────────────────────────────────────────────────────────────────────────────┘

✅ Zero-Trust Architecture
   • "Never trust, always verify" at every layer
   • Cryptographic identity (SPIFFE SVIDs)
   • Defense-in-depth (4 independent security layers)

✅ Production-Grade Security
   • Prevents header spoofing (mTLS enforcement)
   • Prevents lateral movement (AuthorizationPolicy)
   • Prevents tenant data leakage (RLS)

✅ Industry Standard Pattern
   • Istio + SPIFFE/SPIRE (CNCF standards)
   • Used by Google, AWS, Netflix, Uber
   • Well-documented, proven at scale

✅ Operational Simplicity
   • Auto-rotating certificates (no manual rotation)
   • Distributed policy enforcement (scales with services)
   • Centralized observability (Istio telemetry)

✅ Performance Optimized
   • JWT caching (~20% throughput improvement)
   • mTLS hardware acceleration
   • Async telemetry export (no request latency impact)
   • OTLP batching for efficient data transfer

✅ Complete Observability Stack
   • OTLP traces for distributed request tracing (Jaeger)
   • OTLP metrics for performance monitoring (VictoriaMetrics)
   • Log aggregation and search (OpenSearch + ClickHouse)
   • Usage events for billing and business intelligence (OpenMeter)
   • Business events (auth, tenant ops) for audit trails
   • AI-powered cluster diagnostics (K8sGPT)

✅ Production-Grade Telemetry
   • Native OpenTelemetry support in AgentGateway
   • Vendor-neutral OTLP protocol (works with any backend)
   • Distributed tracing across Hub-Spoke architecture
   • Performance monitoring with minimal overhead
   • Integrates with zero-ops observability stack


┌─────────────────────────────────────────────────────────────────────────────────────────────────┐
│                                  WHAT THIS ELIMINATES                                            │
└─────────────────────────────────────────────────────────────────────────────────────────────────┘

❌ Header Spoofing Risk
   • v1: Headers could be forged if network compromised
   • v2: mTLS + SPIFFE prevents header injection

❌ Single Point of Failure
   • v1: Gateway-only validation (if bypassed, no defense)
   • v2: Multiple independent layers (defense-in-depth)

❌ Network Trust Assumption
   • v1: Assumes internal network is trusted
   • v2: Zero-trust (every hop validated cryptographically)

❌ Operational Complexity
   • v1: Manual certificate management
   • v2: Auto-rotating SPIFFE SVIDs (60min lifetime)

❌ Scalability Bottleneck
   • v1: Gateway-only policy enforcement
   • v2: Distributed policy (Istio AuthorizationPolicy per service)

❌ Incomplete Telemetry Stack
   • v1: Only usage events (access logs)
   • v2: Complete OTLP integration (traces + metrics + events)

❌ Limited Observability
   • v1: Basic request logging
   • v2: Distributed tracing, performance metrics, business events

❌ Vendor Lock-in Risk
   • v1: Custom telemetry format
   • v2: Standard OTLP protocol (vendor-neutral)

Let me create a comprehensive ASCII diagram showing your Hub-Spoke architecture with OpenMeter integration for tenant usage tracking:

```
┌─────────────────────────────────────────────────────────────────────────────────────────────────┐
│                                    HUB CLUSTER (Control Plane)                                   │
│                                                                                                  │
│  ┌──────────────────────────────────────────────────────────────────────────────────────────┐  │
│  │                              Identity & Auth Layer                                        │  │
│  │                                                                                            │  │
│  │   ┌─────────────┐         ┌──────────────┐         ┌─────────────────┐                  │  │
│  │   │ Ory Kratos  │────────▶│  Ory Hydra   │────────▶│   Auth Proxy    │                  │  │
│  │   │  (Identity) │         │ (OAuth/OIDC) │         │ (Consent Flow)  │                  │  │
│  │   └─────────────┘         └──────┬───────┘         └─────────────────┘                  │  │
│  │                                  │                                                         │  │
│  │                                  │ Issues JWT with claims:                                │  │
│  │                                  │ - sub: user-id                                         │  │
│  │                                  │ - aud: urn:zero-ops:tenant:{tenantId}:api             │  │
│  │                                  │ - tenant_id: {tenantId}                                │  │
│  │                                  │                                                         │  │
│  │                                  │ Exposes JWKS:                                          │  │
│  │                                  │ https://auth.nutgraf.in/.well-known/jwks.json         │  │
│  └──────────────────────────────────┼─────────────────────────────────────────────────────────┘  │
│                                     │                                                            │
│  ┌──────────────────────────────────┼─────────────────────────────────────────────────────────┐  │
│  │                              GitOps & Orchestration                                        │  │
│  │                                  │                                                         │  │
│  │   ┌─────────────┐         ┌──────▼──────┐         ┌─────────────────┐                   │  │
│  │   │   ArgoCD    │────────▶│ Crossplane  │────────▶│  Spoke Clusters │                   │  │
│  │   │  (GitOps)   │         │    (XRD)    │         │  Provisioning   │                   │  │
│  │   └─────────────┘         └─────────────┘         └─────────────────┘                   │  │
│  └──────────────────────────────────────────────────────────────────────────────────────────┘  │
│                                                                                                  │
│  ┌──────────────────────────────────────────────────────────────────────────────────────────┐  │
│  │                         Usage Tracking & Billing (NEW)                                    │  │
│  │                                                                                            │  │
│  │   ┌─────────────────────────────────────────────────────────────────────────────────┐    │  │
│  │   │                          OpenMeter (Hub Instance)                                │    │  │
│  │   │                                                                                   │    │  │
│  │   │  ┌──────────────┐   ┌──────────────┐   ┌──────────────┐   ┌──────────────┐    │    │  │
│  │   │  │   Kafka      │──▶│  ClickHouse  │──▶│  PostgreSQL  │──▶│   Billing    │    │    │  │
│  │   │  │  (Events)    │   │ (Aggregation)│   │   (Catalog)  │   │   Engine     │    │    │  │
│  │   │  └──────▲───────┘   └──────────────┘   └──────────────┘   └──────────────┘    │    │  │
│  │   │         │                                                                        │    │  │
│  │   │         │ CloudEvents Ingestion API                                             │    │  │
│  │   │         │ POST /api/v1/events                                                   │    │  │
│  │   └─────────┼───────────────────────────────────────────────────────────────────────┘    │  │
│  │             │                                                                              │  │
│  │             │                                                                              │  │
│  │   ┌─────────┼───────────────────────────────────────────────────────────────────────┐    │  │
│  │   │         │              NATS JetStream (Hub-Spoke Bridge)                        │    │  │
│  │   │         │                                                                        │    │  │
│  │   │  ┌──────▼──────────┐         Topic: tenant.usage.events                         │    │  │
│  │   │  │ NATS Consumer   │         - Consumes from all Spokes                         │    │  │
│  │   │  │ (Usage Bridge)  │         - Transforms to CloudEvents                        │    │  │
│  │   │  │                 │         - Publishes to OpenMeter                           │    │  │
│  │   │  └─────────────────┘                                                            │    │  │
│  │   └──────────────────────────────────────────────────────────────────────────────────────┘    │  │
│  │             ▲                                                                              │  │
│  │             │ Usage events from all tenant Spokes                                         │  │
└─────────────────┼──────────────────────────────────────────────────────────────────────────────┘
                  │
                  │ NATS Cross-Cluster Connection
                  │
┌─────────────────┼──────────────────────────────────────────────────────────────────────────────┐
│                 │           SPOKE CLUSTER (Tenant: app-creator)                                │
│                 │                                                                              │
│  ┌──────────────┼──────────────────────────────────────────────────────────────────────────┐  │
│  │              │              Tenant Namespace: tenant-app-creator                        │  │
│  │              │                                                                           │  │
│  │   ┌──────────┴──────────────────────────────────────────────────────────────────────┐  │  │
│  │   │                    Usage Tracking Middleware (NEW)                               │  │  │
│  │   │                                                                                   │  │  │
│  │   │  ┌────────────────────────────────────────────────────────────────────────┐     │  │  │
│  │   │  │  Envoy Sidecar / Custom Go Proxy                                       │     │  │  │
│  │   │  │                                                                         │     │  │  │
│  │   │  │  1. Intercepts HTTP Request                                            │     │  │  │
│  │   │  │  2. Validates JWT via JWKS (https://auth.nutgraf.in/.well-known/...)  │     │  │  │
│  │   │  │  3. Extracts Claims:                                                   │     │  │  │
│  │   │  │     - tenant_id: "app-creator"                                         │     │  │  │
│  │   │  │     - sub: "user-123"                                                  │     │  │  │
│  │   │  │     - aud: "urn:zero-ops:tenant:app-creator:api"                       │     │  │  │
│  │   │  │  4. Captures Metrics:                                                  │     │  │  │
│  │   │  │     - method, path, status_code                                        │     │  │  │
│  │   │  │     - response_time_ms, request/response bytes                         │     │  │  │
│  │   │  │  5. Publishes to NATS (async, non-blocking)                            │     │  │  │
│  │   │  │  6. Forwards request to PostgREST                                      │     │  │  │
│  │   │  └────────────┬───────────────────────────────────────────────────────────┘     │  │  │
│  │   └───────────────┼───────────────────────────────────────────────────────────────────┘  │  │
│  │                   │                                                                      │  │
│  │                   │ Proxied Request (with validated JWT)                                │  │
│  │                   │                                                                      │  │
│  │   ┌───────────────▼───────────────────────────────────────────────────────────────────┐  │  │
│  │   │                          PostgREST (Data API)                                      │  │  │
│  │   │                                                                                     │  │  │
│  │   │  Environment Variables:                                                            │  │  │
│  │   │  - PGRST_JWT_SECRET: '{"jwks_uri": "https://auth.nutgraf.in/...jwks.json"}'      │  │  │
│  │   │  - PGRST_JWT_AUD: "urn:zero-ops:tenant:app-creator:api"                           │  │  │
│  │   │  - PGRST_JWT_CACHE_MAX_LIFETIME: "3600"                                           │  │  │
│  │   │                                                                                     │  │  │
│  │   │  Validates JWT → Extracts tenant_id claim → Sets PostgreSQL session variable      │  │  │
│  │   └─────────────────────────────────┬───────────────────────────────────────────────────┘  │  │
│  │                                     │                                                      │  │
│  │                                     │ SQL Query with RLS context                           │  │
│  │                                     │                                                      │  │
│  │   ┌─────────────────────────────────▼───────────────────────────────────────────────────┐  │  │
│  │   │                     PostgreSQL (Tenant Database)                                     │  │  │
│  │   │                                                                                       │  │  │
│  │   │  Row Level Security (RLS) Policies:                                                 │  │  │
│  │   │  - SET LOCAL app.current_tenant = 'app-creator'                                     │  │  │
│  │   │  - WHERE tenant_id = current_setting('app.current_tenant')                          │  │  │
│  │   │                                                                                       │  │  │
│  │   │  Tables:                                                                             │  │  │
│  │   │  - resources (tenant_id, data, ...)                                                 │  │  │
│  │   │  - workflows (tenant_id, definition, ...)                                           │  │  │
│  │   └───────────────────────────────────────────────────────────────────────────────────────┘  │  │
│  └──────────────────────────────────────────────────────────────────────────────────────────────┘  │
│                                                                                                  │
│  ┌──────────────────────────────────────────────────────────────────────────────────────────┐  │
│  │                              NATS (Spoke Instance)                                        │  │
│  │                                                                                            │  │
│  │   Topic: tenant.usage.events                                                              │  │
│  │   Publisher: Usage Tracking Middleware                                                    │  │
│  │   Consumer: Hub NATS Bridge (cross-cluster)                                               │  │
│  │                                                                                            │  │
│  │   CloudEvent Message Format:                                                              │  │
│  │   {                                                                                        │  │
│  │     "specversion": "1.0",                                                                  │  │
│  │     "type": "tenant.api.request",                                                          │  │
│  │     "source": "spoke-cluster/tenant-app-creator",                                          │  │
│  │     "subject": "app-creator",                                                              │  │
│  │     "data": {                                                                              │  │
│  │       "tenant_id": "app-creator",                                                          │  │
│  │       "user_id": "user-123",                                                               │  │
│  │       "audience": "urn:zero-ops:tenant:app-creator:api",                                   │  │
│  │       "method": "POST",                                                                    │  │
│  │       "path": "/api/v1/resources",                                                         │  │
│  │       "status_code": 201,                                                                  │  │
│  │       "response_time_ms": 45,                                                              │  │
│  │       "request_size_bytes": 1024,                                                          │  │
│  │       "response_size_bytes": 512                                                           │  │
│  │     }                                                                                      │  │
│  │   }                                                                                        │  │
│  └──────────────────────────────────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────────────────────────────────────┘


┌─────────────────────────────────────────────────────────────────────────────────────────────────┐
│                         DATA FLOW: Tenant API Request with Usage Tracking                       │
└─────────────────────────────────────────────────────────────────────────────────────────────────┘

Step 1: Authentication (Hub)
────────────────────────────
User → Ory Kratos (login) → Ory Hydra (OAuth) → Auth Proxy (consent)
                                                      │
                                                      ▼
                                            JWT Token Issued:
                                            {
                                              "sub": "user-123",
                                              "aud": "urn:zero-ops:tenant:app-creator:api",
                                              "tenant_id": "app-creator",
                                              "exp": 1234567890
                                            }

Step 2: API Request (Spoke)
────────────────────────────
User → [JWT Token] → Spoke Ingress → Usage Tracking Middleware
                                              │
                                              ├─▶ Validate JWT via JWKS (Hub)
                                              ├─▶ Extract Claims (tenant_id, sub, aud)
                                              ├─▶ Capture Metrics (method, path, timing)
                                              ├─▶ Publish to NATS (async) ──┐
                                              │                              │
                                              ▼                              │
                                         PostgREST                           │
                                              │                              │
                                              ├─▶ Validate JWT (JWKS)       │
                                              ├─▶ Extract tenant_id          │
                                              ├─▶ Set RLS context            │
                                              │                              │
                                              ▼                              │
                                         PostgreSQL                          │
                                              │                              │
                                              ├─▶ Apply RLS policies         │
                                              ├─▶ Execute query              │
                                              │                              │
                                              ▼                              │
                                         Response ──────────────────────────┘
                                                                             │
Step 3: Usage Event Processing (Hub)                                        │
─────────────────────────────────────                                       │
                                                                             │
NATS (Spoke) ──cross-cluster──▶ NATS (Hub) ──▶ Usage Bridge ◀──────────────┘
                                                      │
                                                      ├─▶ Transform to CloudEvents
                                                      ├─▶ Enrich with metadata
                                                      │
                                                      ▼
                                                 OpenMeter API
                                                      │
                                                      ├─▶ Kafka (buffer)
                                                      ├─▶ ClickHouse (aggregate)
                                                      ├─▶ PostgreSQL (billing)
                                                      │
                                                      ▼
                                              Usage Metrics Available:
                                              - Real-time dashboards
                                              - Billing calculations
                                              - Quota enforcement
                                              - Tenant analytics


┌─────────────────────────────────────────────────────────────────────────────────────────────────┐
│                                  KEY INTEGRATION POINTS                                          │
└─────────────────────────────────────────────────────────────────────────────────────────────────┘

1. JWT Claims → Usage Events
   ─────────────────────────
   - tenant_id from JWT → CloudEvents "subject" field
   - sub (user) → CloudEvents "data.user_id" field
   - aud (audience) → CloudEvents "data.audience" field
   - Ensures usage is correctly attributed to tenant

2. NATS as Event Bus
   ──────────────────
   - Decouples Spoke (data plane) from Hub (control plane)
   - Async, non-blocking usage tracking
   - Resilient to OpenMeter downtime
   - Leverages existing NATS infrastructure

3. RLS Consistency
   ────────────────
   - Same tenant_id used for:
     * PostgreSQL RLS policies (data isolation)
     * OpenMeter billing records (usage isolation)
     * JWT audience claims (API isolation)
   - Defense-in-depth security model

4. JWKS Validation
   ────────────────
   - Both PostgREST and Usage Middleware validate JWT
   - No secret sharing between Hub and Spoke
   - Automatic key rotation support
   - Stateless, scalable architecture
```

This diagram shows:

1. **Hub Cluster** with Ory Hydra issuing JWTs with per-tenant audience claims
2. **Spoke Cluster** with a new Usage Tracking Middleware that:
   - Intercepts requests before PostgREST
   - Validates JWT and extracts tenant identity
   - Publishes usage events to NATS asynchronously
   - Forwards requests to PostgREST (existing flow unchanged)
3. **OpenMeter Integration** in the Hub that:
   - Consumes usage events from NATS
   - Aggregates metrics in ClickHouse
   - Manages billing in PostgreSQL
4. **Data Flow** showing the complete request lifecycle with usage tracking

The key insight is that the Usage Tracking Middleware sits **between the ingress and PostgREST**, extracting the already-validated JWT claims to tag usage events with tenant identity, then publishing to NATS for async processing by OpenMeter in the Hub.

Would you like me to create a spec for implementing this architecture?
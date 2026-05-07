# PostgREST + AgentGateway Integration Analysis

**Spec**: spoke-pool-provisioner  
**Component**: PostgREST (Auto-Generated REST API) + AgentGateway (Authentication Gateway)  
**Purpose**: Provide authenticated, tenant-isolated REST API access to PostgreSQL schemas  
**Status**: PostgREST not implemented, AgentGateway already implemented in Hub

---

## 1. Position in Spoke Pool Provisioning Flow

```
Cell Provisioning (FR-1.x)
    ↓
Edge Catalog Deployment (FR-2.x)
    ↓
    Wave 0: Database extensions (if needed)
    Wave 1: CNPG Cluster + PgBouncer (FR-2.2)
    Wave 2: Atlas Operator + AtlasMigration CRs (FR-4.4)
    Wave 3: PostgREST + AgentGateway (FR-2.6) ← THIS INTEGRATION
    Wave 4: Tenant workloads
    ↓
Tenant Schema Provisioning (FR-4.1)
    ↓
Centralized Identity via Hub Ory (FR-4.5)
```

**Critical Constraint**: PostgREST MUST deploy in Wave 3 (after schemas exist) and is NEVER directly exposed. All traffic flows through AgentGateway.

---

## 2. Architecture Overview

### 2.1 Hub-Spoke Authentication Flow

```
Developer/Agent (IDE Client)
    ↓ [JWT from Hub Ory Hydra]
AgentGateway (Spoke Pool)
    ↓ [Validates JWT using Hub Ory JWKS]
    ↓ [Extracts tenant_id from JWT claims]
    ↓ [Forwards: JWT + X-Tenant-ID header]
PostgREST (Spoke Pool, Internal Service)
    ↓ [Caches validated JWT (10000 entries)]
    ↓ [Sets search_path=tenant_<id>]
    ↓ [Executes query via PgBouncer]
CNPG Cluster (Spoke Pool, Shared Database)
```

**Key Principle**: AgentGateway is the ONLY entry point. PostgREST is an internal service with no external exposure.

### 2.2 Dual Deployment Pattern

The platform uses TWO distinct PostgREST deployments:

1. **Hub-side PostgREST**: Exposes Hub Centralised DB for Spoke Controller status writes (FR-5.x)
2. **Spoke-side PostgREST**: Exposes Control Plane Shared DB (Pool) or Tenant Control Plane DB (Silo) for tenant data access (FR-2.6, FR-4.2)

**This document covers Spoke-side PostgREST only.**

---

## 3. Component Responsibilities

### 3.1 AgentGateway (Already Implemented)

**Current Implementation**: `archived/identity-auth/agentgateway/`

**Responsibilities**:
- Validate JWT signatures using Hub Ory JWKS (RS256)
- Extract `tenant_id` claim from validated JWT
- Forward authenticated requests to PostgREST with:
  * Original JWT (for PostgREST caching)
  * `X-Tenant-ID: <tenant_id>` header (for schema routing)
- Enforce rate limits per tenant
- Log all requests with `tenant_id` for audit

**Configuration Requirements**:
```yaml
# AgentGateway ConfigMap (per Spoke Pool)
apiVersion: v1
kind: ConfigMap
metadata:
  name: agentgateway-config
  namespace: spoke-pool
data:
  config.yaml: |
    auth:
      jwks_url: "https://auth.nutgraf.in/.well-known/jwks.json"
      issuer: "https://auth.nutgraf.in"
      audience: "https://api.nutgraf.in"  # Must match auth-proxy EXPECTED_JWT_AUDIENCE
      cache_ttl: 3600  # 1 hour JWKS cache
    
    routing:
      postgrest:
        url: "http://postgrest.spoke-pool.svc:3000"
        timeout: 30s
        headers:
          - name: "X-Tenant-ID"
            source: "jwt.claims.tenant_id"
    
    rate_limiting:
      per_tenant: 1000  # requests per minute
```

**Existing MCP Authentication Pattern** (from `archived/identity-auth/agentgateway/examples/mcp-authentication/`):
- AgentGateway already validates JWTs for MCP tools in Hub
- Same JWKS validation logic applies to Spoke Pool PostgREST routing
- Reuse existing JWT validation, add PostgREST routing backend

### 3.2 PostgREST (Not Implemented)

**Codebase Reference**: `archived/supabase/postgrest/`

**Responsibilities**:
- Auto-generate REST API from PostgreSQL schema introspection
- Cache validated JWTs in-memory (10000 entries) for performance
- Set `search_path=tenant_<id>` based on `X-Tenant-ID` header
- Execute queries via PgBouncer (transaction pooling)
- Respect PostgreSQL RLS policies for end-user isolation
- Provide OpenAPI schema for tenant applications

**Configuration Requirements**:
```yaml
# PostgREST Deployment (per Spoke Pool)
apiVersion: apps/v1
kind: Deployment
metadata:
  name: postgrest
  namespace: spoke-pool
spec:
  replicas: 3  # HA deployment
  template:
    spec:
      containers:
      - name: postgrest
        image: postgrest/postgrest:v12.0.2
        env:
        - name: PGRST_DB_URI
          value: "postgres://authenticator@pgbouncer.spoke-pool.svc:5432/shared_cnpg"
        - name: PGRST_DB_SCHEMAS
          value: "tenant_acme,tenant_xyz,tenant_foo"  # Dynamically updated
        - name: PGRST_DB_ANON_ROLE
          value: "web_anon"  # Fallback role (no permissions)
        - name: PGRST_JWT_SECRET
          valueFrom:
            secretKeyRef:
              name: postgrest-jwt-secret
              key: jwt-secret  # RS256 public key from Hub Ory
        - name: PGRST_JWT_AUD
          value: "https://api.nutgraf.in"  # Must match auth-proxy EXPECTED_JWT_AUDIENCE
        - name: PGRST_JWT_CACHE_MAX_LIFETIME
          value: "3600"  # 1 hour
        - name: PGRST_JWT_CACHE_SIZE
          value: "10000"  # 10k cached tokens
        - name: PGRST_DB_POOL
          value: "10"  # Connection pool size
        - name: PGRST_DB_POOL_TIMEOUT
          value: "10"  # Seconds
        - name: PGRST_SERVER_PROXY_URI
          value: "https://api.nutgraf.in"  # AgentGateway URL (Hub domain)
        ports:
        - containerPort: 3000
        livenessProbe:
          httpGet:
            path: /
            port: 3000
          initialDelaySeconds: 10
          periodSeconds: 10
        readinessProbe:
          httpGet:
            path: /
            port: 3000
          initialDelaySeconds: 5
          periodSeconds: 5
---
apiVersion: v1
kind: Service
metadata:
  name: postgrest
  namespace: spoke-pool
spec:
  type: ClusterIP  # Internal service only
  ports:
  - port: 3000
    targetPort: 3000
  selector:
    app: postgrest
```

**Critical Configuration Notes**:
- `PGRST_DB_SCHEMAS`: Must be dynamically updated when new tenant schemas are created (Atlas Operator reconciliation)
- `PGRST_JWT_SECRET`: RS256 public key from Hub Ory (not symmetric HS256)
- `PGRST_DB_ANON_ROLE`: Fallback role with zero permissions (security default)
- `PGRST_SERVER_PROXY_URI`: AgentGateway URL for OpenAPI schema generation

---

## 4. Tenant Isolation Mechanism

### 4.1 Schema-Level Isolation (Platform-Level)

**Mechanism**: Deterministic schema naming + `search_path` routing

```sql
-- Each tenant gets a dedicated schema
CREATE SCHEMA IF NOT EXISTS tenant_acme;
CREATE SCHEMA IF NOT EXISTS tenant_xyz;

-- Schema owner role
CREATE ROLE tenant_acme_role;
GRANT ALL ON SCHEMA tenant_acme TO tenant_acme_role;
```

**PostgREST Schema Routing**:
```
AgentGateway extracts tenant_id from JWT
    ↓
Forwards: X-Tenant-ID: acme
    ↓
PostgREST receives request
    ↓
Sets: search_path=tenant_acme
    ↓
All queries execute within tenant_acme schema
    ↓
No cross-tenant access possible
```

**PgBouncer Transaction Pooling** (NFR-2.4, NFR-2.5):
- MUST use transaction pooling mode (not session pooling)
- Ensures `search_path` is reset between transactions
- Prevents session state leakage between tenants
- Configuration: `pool_mode = transaction` in PgBouncer

### 4.2 Row-Level Security (End-User Isolation)

**Mechanism**: PostgreSQL RLS policies within tenant schema

```sql
-- Within tenant_acme schema
CREATE TABLE tenant_acme.documents (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL,  -- From JWT claims
    content TEXT
);

-- Enable RLS
ALTER TABLE tenant_acme.documents ENABLE ROW LEVEL SECURITY;

-- Policy: Users can only access their own documents
CREATE POLICY user_isolation ON tenant_acme.documents
    USING (user_id = current_setting('request.jwt.claims')::json->>'user_id');
```

**JWT Claims Flow**:
```
JWT contains: {"tenant_id": "acme", "user_id": "user-123", "role": "developer"}
    ↓
PostgREST sets: request.jwt.claims = '{"tenant_id": "acme", "user_id": "user-123"}'
    ↓
RLS policy evaluates: user_id = 'user-123'
    ↓
Query returns only user-123's documents
```

---

## 5. JWT Validation and Caching

### 5.1 AgentGateway JWT Validation (Primary)

**Validation Steps**:
1. Extract JWT from `Authorization: Bearer <token>` header
2. Fetch JWKS from Hub Ory on startup (cache for 1 hour)
3. Validate JWT signature using cached public key (RS256)
4. Validate issuer: `https://auth.nutgraf.in`
5. Validate audience: `https://api.nutgraf.in`
6. Validate expiration: `exp` claim
7. Extract `tenant_id` claim
8. Forward to PostgREST with `X-Tenant-ID` header

**Performance** (NFR-1.6):
- Cached JWKS lookup: < 1ms (in-memory)
- Signature validation: < 10ms (RS256)
- Total validation: < 50ms (P95)

### 5.2 PostgREST JWT Caching (Secondary)

**Caching Strategy**:
- PostgREST maintains in-memory JWT cache (10000 entries)
- Cache key: JWT signature
- Cache value: Validated claims + expiration
- Cache TTL: Token expiration time (max 1 hour)
- Cache eviction: LRU (Least Recently Used)

**Performance** (NFR-1.5):
- Cached token lookup: < 1ms (in-memory)
- Uncached token validation: < 50ms (AgentGateway already validated)

**Security** (NFR-4.6):
- Cache invalidation on token expiry (automatic)
- No token replay attacks (expiration enforced)
- Cache size limit prevents memory exhaustion

---

## 6. Database Connection Management

### 6.1 PgBouncer Transaction Pooling

**Configuration** (NFR-2.3, NFR-2.4):
```ini
[databases]
shared_cnpg = host=cnpg-rw.spoke-pool.svc port=5432 dbname=shared_cnpg

[pgbouncer]
pool_mode = transaction  # CRITICAL: Must be transaction mode
max_client_conn = 500    # NFR-2.3: 500 concurrent connections
default_pool_size = 20   # Connections per tenant schema
reserve_pool_size = 5    # Emergency connections
reserve_pool_timeout = 3
max_db_connections = 100 # Total connections to CNPG
```

**Why Transaction Pooling**:
- Session pooling would leak `search_path` between tenants
- Transaction pooling resets session state after each transaction
- Enables high tenant density (100 tenants * 5 connections = 500 via 100 DB connections)

### 6.2 Connection Lifecycle

```
PostgREST receives request
    ↓
Acquires connection from PgBouncer pool
    ↓
Executes: SET search_path=tenant_<id>
    ↓
Executes: SET request.jwt.claims='<claims>'
    ↓
Executes tenant query
    ↓
Commits transaction
    ↓
Returns connection to pool (search_path reset)
```

---

## 7. Schema Discovery and Dynamic Updates

### 7.1 Initial Schema Configuration

**At Cell Provisioning** (FR-2.6):
```yaml
# PostgREST initial deployment
env:
- name: PGRST_DB_SCHEMAS
  value: "public"  # Empty cell, no tenant schemas yet
```

### 7.2 Schema Addition (Tenant Onboarding)

**Flow** (FR-4.1, FR-5.2):
```
MCP API commits tenant values to Git
    ↓
ArgoCD ApplicationSet creates Helm Application
    ↓
Helm generates AtlasMigration CR
    ↓
Atlas Operator creates schema: tenant_<id>
    ↓
Atlas Operator updates PostgREST ConfigMap
    ↓
ConfigMap change triggers PostgREST rolling restart
    ↓
PostgREST introspects new schema
    ↓
New tenant can access their schema via AgentGateway
```

**ConfigMap Update Pattern**:
```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: postgrest-schemas
  namespace: spoke-pool
data:
  schemas: "tenant_acme,tenant_xyz,tenant_foo"  # Comma-separated list
```

**PostgREST Deployment Patch**:
```yaml
env:
- name: PGRST_DB_SCHEMAS
  valueFrom:
    configMapKeyRef:
      name: postgrest-schemas
      key: schemas
```

### 7.3 Schema Removal (Tenant Offboarding)

**Flow**:
```
Tenant deletion request
    ↓
Atlas Operator removes schema
    ↓
Atlas Operator updates PostgREST ConfigMap
    ↓
PostgREST rolling restart
    ↓
Schema no longer accessible
```

---

## 8. Observability and Monitoring

### 8.1 Metrics

**AgentGateway Metrics**:
```
agentgateway_jwt_validation_duration_seconds{result="success|failure"}
agentgateway_postgrest_requests_total{tenant_id="<id>", status="<code>"}
agentgateway_postgrest_request_duration_seconds{tenant_id="<id>"}
agentgateway_rate_limit_exceeded_total{tenant_id="<id>"}
```

**PostgREST Metrics** (via Prometheus exporter):
```
postgrest_requests_total{schema="<tenant_id>", method="<GET|POST>", status="<code>"}
postgrest_request_duration_seconds{schema="<tenant_id>"}
postgrest_db_pool_connections{state="<active|idle>"}
postgrest_jwt_cache_hits_total
postgrest_jwt_cache_misses_total
```

### 8.2 Logging

**AgentGateway Logs**:
```json
{
  "timestamp": "2026-04-08T10:30:00Z",
  "level": "info",
  "msg": "JWT validated",
  "tenant_id": "acme",
  "user_id": "user-123",
  "request_id": "req-abc123",
  "duration_ms": 15
}
```

**PostgREST Logs**:
```json
{
  "timestamp": "2026-04-08T10:30:00Z",
  "level": "info",
  "msg": "Query executed",
  "tenant_id": "acme",
  "schema": "tenant_acme",
  "method": "GET",
  "path": "/documents",
  "status": 200,
  "duration_ms": 25
}
```

### 8.3 Health Checks

**AgentGateway Health**:
```
GET /health
Response: 200 OK
{
  "status": "healthy",
  "jwks_last_refresh": "2026-04-08T10:00:00Z",
  "postgrest_reachable": true
}
```

**PostgREST Health**:
```
GET /
Response: 200 OK
{
  "swagger": "2.0",
  "info": {
    "title": "PostgREST API",
    "version": "12.0.2"
  },
  "schemas": ["tenant_acme", "tenant_xyz"]
}
```

---

## 9. Spec Alignment

### 9.1 Functional Requirements

| Requirement | Implementation | Validation |
|-------------|----------------|------------|
| FR-2.6 | PostgREST deployed as internal service in Wave 3 | ArgoCD Application health check |
| FR-2.6 | AgentGateway validates JWT using Hub Ory JWKS | JWT validation metrics |
| FR-2.6 | AgentGateway forwards with X-Tenant-ID header | Request logs |
| FR-4.2 | Schema isolation via search_path | PgBouncer transaction pooling |
| FR-4.2 | RLS policies for end-user isolation | SQL policy verification |
| FR-4.5 | Hub Ory Kratos/Hydra for authentication | JWKS endpoint reachability |
| FR-4.5 | AgentGateway validates JWT signatures | Signature validation metrics |

### 9.2 Non-Functional Requirements

| Requirement | Implementation | Target | Validation |
|-------------|----------------|--------|------------|
| NFR-1.5 | PostgREST JWT cache (in-memory) | < 1ms (P95) | Metrics: postgrest_jwt_cache_hits |
| NFR-1.6 | AgentGateway JWT validation | < 50ms (P95) | Metrics: agentgateway_jwt_validation_duration |
| NFR-1.7 | PostgREST request latency | < 50ms (P95) | Metrics: postgrest_request_duration |
| NFR-2.3 | PgBouncer connection pooling | 500 concurrent | PgBouncer stats |
| NFR-2.4 | Transaction pooling mode | Required | PgBouncer config |
| NFR-2.6 | PostgREST JWT cache size | 10000 entries | PGRST_JWT_CACHE_SIZE |
| NFR-2.8 | PostgREST NOT directly exposed | Internal service | Kubernetes Service type=ClusterIP |
| NFR-4.5 | JWT validation via JWKS | RS256 | AgentGateway config |
| NFR-4.6 | JWT cache invalidation | Automatic | Cache TTL = token expiry |
| NFR-4.7 | Schema isolation enforcement | search_path | SQL query logs |
| NFR-4.8 | PostgREST internal only | No external exposure | Network policies |

### 9.3 Acceptance Criteria

| Criteria | Implementation | Validation |
|----------|----------------|------------|
| AC-4 | PostgREST deployed as internal service | Service type=ClusterIP |
| AC-4 | AgentGateway deployed and configured | Deployment health check |
| AC-4 | AgentGateway routes to PostgREST | Request logs |
| AC-5 | AgentGateway validates JWT | JWT validation metrics |
| AC-5 | AgentGateway forwards X-Tenant-ID | Request headers |
| AC-6 | Tenant can access schema via AgentGateway | E2E test |
| AC-6 | AgentGateway validates JWT | E2E test |
| AC-6 | PostgREST sets search_path | SQL query logs |
| AC-6 | PgBouncer transaction pooling | PgBouncer config |

---

## 10. Implementation Checklist

### 10.1 Hub Prerequisites

- [ ] Hub Ory Kratos deployed and configured (platform-identity namespace)
- [ ] Hub Ory Hydra deployed and configured (platform-identity namespace)
- [ ] JWKS endpoint exposed: `https://auth.nutgraf.in/.well-known/jwks.json`
- [ ] JWT tokens include claims: `tenant_id`, `user_id`, `role`, `tenant_tier`
- [ ] RS256 public key available for PostgREST configuration

### 10.2 Spoke Pool Deployment

- [ ] AgentGateway Helm chart created
- [ ] AgentGateway ConfigMap with Hub Ory JWKS URL
- [ ] AgentGateway Deployment with HA (3 replicas)
- [ ] AgentGateway Service (LoadBalancer or Ingress)
- [ ] PostgREST Helm chart created
- [ ] PostgREST ConfigMap for dynamic schema list
- [ ] PostgREST Secret with RS256 public key
- [ ] PostgREST Deployment with HA (3 replicas)
- [ ] PostgREST Service (ClusterIP, internal only)
- [ ] PgBouncer configured with transaction pooling
- [ ] NetworkPolicy: PostgREST only accessible from AgentGateway

### 10.3 ArgoCD Integration

- [ ] ApplicationSet includes AgentGateway in Wave 3
- [ ] ApplicationSet includes PostgREST in Wave 3
- [ ] Health checks: AgentGateway Deployment ready
- [ ] Health checks: PostgREST Deployment ready
- [ ] Health checks: PostgREST schema introspection successful

### 10.4 Atlas Operator Integration

- [ ] Atlas Operator updates PostgREST ConfigMap on schema creation
- [ ] PostgREST rolling restart triggered on ConfigMap change
- [ ] PostgREST health check verifies schema exists before accepting requests

### 10.5 Observability

- [ ] AgentGateway metrics exposed (Prometheus)
- [ ] PostgREST metrics exposed (Prometheus)
- [ ] Grafana dashboard: JWT validation latency
- [ ] Grafana dashboard: PostgREST request latency
- [ ] Grafana dashboard: PgBouncer connection pool usage
- [ ] Alerts: JWT validation failures
- [ ] Alerts: PostgREST connection pool exhaustion

---

## 11. Security Considerations

### 11.1 JWT Validation

**Threat**: Forged JWT tokens
**Mitigation**: RS256 signature validation using Hub Ory JWKS (asymmetric cryptography)

**Threat**: Expired JWT tokens
**Mitigation**: AgentGateway validates `exp` claim, PostgREST cache respects expiration

**Threat**: Token replay attacks
**Mitigation**: Short-lived tokens (1 hour), cache invalidation on expiry

### 11.2 Network Isolation

**Threat**: Direct PostgREST access bypassing AgentGateway
**Mitigation**: PostgREST Service type=ClusterIP (internal only), NetworkPolicy enforcement

**Threat**: Cross-tenant access
**Mitigation**: Schema isolation via `search_path`, PgBouncer transaction pooling

### 11.3 Database Security

**Threat**: SQL injection
**Mitigation**: PostgREST uses parameterized queries, PostgreSQL prepared statements

**Threat**: Privilege escalation
**Mitigation**: PostgREST uses `web_anon` role with zero permissions, RLS policies enforce access control

---

## 12. Design Patterns (from sbt-patterns)

### 12.1 Multi-Tenant Security (Defense-in-Depth)

**Pattern**: Tenant isolation at every layer

**Layers**:
1. **API Layer**: AgentGateway validates JWT, extracts tenant_id
2. **Authorization Layer**: JWT claims enforce tenant context
3. **Database Layer**: PostgreSQL RLS + schema isolation

**Reference**: `docs/sbt-design-principles.md` - Section 8

### 12.2 Status Controller Pattern

**Pattern**: Database is the source of truth for status

**Application**: PostgREST provides read-only access to tenant data, status is written by Spoke Controller to Hub DB

**Reference**: `docs/opensbt-architecture-guide.md` - Section 4

### 12.3 GitOps-First Operations

**Pattern**: All configuration changes via Git commits

**Application**: PostgREST schema list updated via ConfigMap in Git, ArgoCD syncs changes

**Reference**: `docs/saas-architecture-principles.md` - Operational Excellence Pillar

---

## 13. Testing Strategy

### 13.1 Unit Tests

**AgentGateway**:
- JWT validation with valid/invalid signatures
- JWT validation with expired tokens
- Tenant ID extraction from JWT claims
- Header forwarding to PostgREST

**PostgREST**:
- Schema introspection
- JWT cache hit/miss scenarios
- search_path setting based on X-Tenant-ID header

### 13.2 Integration Tests

**AgentGateway + PostgREST**:
- End-to-end request flow with valid JWT
- Request rejection with invalid JWT
- Tenant isolation verification (cross-tenant access blocked)
- PgBouncer transaction pooling verification

### 13.3 E2E Tests (AC-6)

```bash
# 1. Provision Spoke Pool cell
kubectl apply -f spokepool-01.yaml

# 2. Wait for PostgREST ready
kubectl wait --for=condition=Ready deployment/postgrest -n spoke-pool --timeout=5m

# 3. Create tenant schema
# (via MCP API, Atlas Operator creates schema)

# 4. Obtain JWT from Hub Ory
JWT=$(curl -X POST https://auth.nutgraf.in/oauth2/token \
  -d "grant_type=client_credentials" \
  -d "client_id=test-client" \
  -d "client_secret=test-secret" | jq -r .access_token)

# 5. Make authenticated request via AgentGateway
curl -H "Authorization: Bearer $JWT" \
  https://api.nutgraf.in/documents

# 6. Verify response contains only tenant's documents
# 7. Verify PostgREST logs show search_path=tenant_<id>
# 8. Verify PgBouncer stats show transaction pooling
```

---

## 14. Troubleshooting Guide

### 14.1 JWT Validation Failures

**Symptom**: 401 Unauthorized from AgentGateway

**Diagnosis**:
```bash
# Check AgentGateway logs
kubectl logs -n spoke-pool deployment/agentgateway | grep "JWT validation failed"

# Verify JWKS endpoint reachable
curl https://auth.nutgraf.in/.well-known/jwks.json

# Verify JWT claims
echo $JWT | jwt decode -
```

**Resolution**:
- Verify Hub Ory Kratos is running
- Verify JWKS URL in AgentGateway ConfigMap
- Verify JWT issuer and audience match configuration

### 14.2 Schema Not Found

**Symptom**: 404 Not Found from PostgREST

**Diagnosis**:
```bash
# Check PostgREST schema list
kubectl exec -n spoke-pool deployment/postgrest -- \
  curl localhost:3000/ | jq .schemas

# Check CNPG cluster for schema
kubectl exec -n spoke-pool cnpg-rw-0 -- \
  psql -U postgres -c "\dn tenant_*"
```

**Resolution**:
- Verify Atlas Operator created schema
- Verify PostgREST ConfigMap includes schema
- Trigger PostgREST rolling restart

### 14.3 Connection Pool Exhaustion

**Symptom**: 503 Service Unavailable from PostgREST

**Diagnosis**:
```bash
# Check PgBouncer stats
kubectl exec -n spoke-pool deployment/pgbouncer -- \
  psql -p 6432 pgbouncer -c "SHOW POOLS"

# Check PostgREST connection pool
kubectl logs -n spoke-pool deployment/postgrest | grep "connection pool"
```

**Resolution**:
- Increase PgBouncer `default_pool_size`
- Increase PostgREST `PGRST_DB_POOL`
- Scale PostgREST replicas

---

**Document Version**: 1.0  
**Last Updated**: 2026-04-08  
**Next Review**: After implementation completion

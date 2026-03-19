---
title: SaaS Architecture Principles
description: Multi-tenant SaaS design principles adapted from AWS Well-Architected SaaS Lens
inclusion: always
---

# SaaS Architecture Principles

This document provides architectural guidance for building multi-tenant SaaS systems, adapted from the AWS Well-Architected SaaS Lens and optimized for the Kiro platform's technology stack.

## Core SaaS Design Principles

### 1. Tenant Isolation is Foundational
Every layer of the architecture must prevent cross-tenant access. Tenant isolation is non-negotiable.

**Implementation Guidelines:**
- Use Ory Keto relationship-based authorization for tenant boundary enforcement
- Implement tenant context in JWT tokens (bind user identity to tenant identity)
- Apply PostgreSQL Row-Level Security (RLS) policies for data isolation
- Validate tenant context at API gateway (AgentGateway) before routing
- Use namespace isolation in Kubernetes for tenant workloads when applicable
- Implement tenant-aware logging and metrics (tenant ID in all log entries)

**Critical Rule:** Never rely on application-level checks alone. Use database-level isolation (RLS) as defense-in-depth.


### 2. SaaS Identity: Bind User to Tenant
Authentication must produce a SaaS identity that includes both user and tenant context.

**Implementation Guidelines:**
- Ory Kratos authentication returns user identity
- Enrich identity with tenant context during token generation (Ory Hydra)
- Include tenant_id, tier, and permissions in JWT claims
- Pass tenant context through all service layers without additional lookups
- Use tenant context for: data access, metrics, logging, rate limiting, feature flags

**Token Structure Example:**
```json
{
  "sub": "user_id",
  "tenant_id": "tenant_uuid",
  "tenant_tier": "premium|standard|basic",
  "roles": ["admin", "developer"],
  "permissions": ["agents:execute", "clusters:create"]
}
```

### 3. Design for Unpredictable Growth
The architecture must scale without operational overhead as tenants are added.

**Implementation Guidelines:**
- Use KEDA for pod autoscaling based on tenant workload metrics
- Use Karpenter for node autoscaling to handle cluster capacity
- Implement connection pooling (PgBouncer via CNPG) to handle database connections
- Design stateless services that can scale horizontally
- Use NATS for async communication to decouple services
- Avoid per-tenant infrastructure provisioning (use shared pools with isolation)

**Anti-Pattern:** Creating dedicated infrastructure per tenant (breaks SaaS economics).


### 4. Automated, Frictionless Tenant Onboarding
Tenant provisioning must be fully automated through a single, repeatable process.

**Implementation Guidelines:**
- Use Crossplane compositions for tenant infrastructure provisioning
- Use ArgoCD ApplicationSets for tenant-specific deployments
- Implement GitOps workflow: tenant creation → Git commit → ArgoCD sync
- Provision tenant database schema via sqlc migrations
- Create tenant identity in Ory Kratos/Keto automatically
- Generate tenant-specific credentials and store in KSOPS-encrypted secrets
- Emit tenant lifecycle events to NATS for downstream processing

**Onboarding Flow:**
1. API receives tenant creation request (validated by JWT)
2. Crossplane provisions tenant resources (namespace, database, S3 bucket)
3. ArgoCD deploys tenant-specific configurations
4. Ory Keto creates tenant authorization relationships
5. NATS event triggers downstream setup (monitoring, billing)
6. Return tenant credentials and endpoints


### 5. Tenant-Aware Observability
Operations teams need visibility into system health globally and per-tenant.

**Implementation Guidelines:**
- Tag all metrics with tenant_id label (VictoriaMetrics)
- Include tenant_id in all log entries (OpenSearch)
- Create Grafana dashboards with tenant filtering
- Use cnpg2monitor for tenant-specific database metrics
- Implement tenant-aware alerting (noisy neighbor detection)
- Track per-tenant resource consumption (CPU, memory, storage, API calls)
- Use K8sGPT for tenant-specific cluster diagnostics

**Key Metrics to Track:**
- Tenant request rate and latency (p50, p95, p99)
- Tenant error rates and types
- Tenant resource consumption (cost attribution)
- Tenant feature usage patterns
- Tenant database query performance
- Tenant agent execution metrics


### 6. Measure Tenant Cost Impact
Understand the cost profile of each tenant to inform pricing and architecture decisions.

**Implementation Guidelines:**
- Track compute costs: pod CPU/memory usage per tenant (KEDA metrics)
- Track storage costs: PostgreSQL storage, S3 usage per tenant
- Track network costs: ingress/egress per tenant
- Track agent execution costs: LiteLLM token usage per tenant
- Aggregate costs in VictoriaMetrics with tenant_id labels
- Create cost dashboards showing: cost per tenant, cost per tier, cost trends
- Use cost data to identify: unprofitable tenants, tier misalignment, optimization opportunities

**BYOC Model Consideration:**
Since Kiro uses BYOC (tenant owns compute), focus on measuring platform overhead and shared service costs.


### 7. Support Multiple Tenant Tiers
Design a single environment that supports different tenant experiences without one-off versions.

**Implementation Guidelines:**
- Define tiers in configuration (basic, standard, premium, enterprise)
- Use feature flags to enable/disable capabilities per tier
- Implement tier-based rate limiting at AgentGateway
- Apply tier-based resource quotas (Kubernetes ResourceQuotas)
- Use tier-based isolation strategies (pool vs. silo)
- Store tier information in tenant JWT claims for fast access
- Allow tier upgrades/downgrades through automated workflows

**Tier Strategy Example:**
- **Basic**: Shared pool, rate limits, basic features
- **Standard**: Shared pool, higher limits, standard features
- **Premium**: Dedicated namespace, priority scheduling, advanced features
- **Enterprise**: Dedicated cluster (BYOC), custom SLAs, all features


### 8. Align Infrastructure with Tenant Activity
Avoid over-provisioning by dynamically scaling infrastructure based on real-time tenant workload.

**Implementation Guidelines:**
- Use KEDA to scale pods based on tenant-specific metrics (queue depth, request rate)
- Use Karpenter to scale nodes based on actual pod requirements
- Implement idle timeout for tenant workloads (scale to zero when inactive)
- Use CNPG connection pooling to optimize database resource usage
- Monitor tenant activity patterns to predict scaling needs
- Set appropriate resource requests/limits per tenant tier
- Use Kubernetes HPA for CPU/memory-based scaling

**Scaling Strategy:**
- **Reactive**: KEDA scales based on current metrics (NATS queue, HTTP requests)
- **Predictive**: Analyze tenant patterns to pre-scale before peak usage
- **Cost-Aware**: Balance performance with cost (scale down aggressively for basic tier)


### 9. Limit Developer Awareness of Multi-Tenancy
Provide libraries and abstractions that hide tenancy complexity from developers.

**Implementation Guidelines:**
- Create Go middleware that extracts tenant context from JWT automatically
- Provide tenant-aware database query helpers (auto-inject tenant_id filters)
- Use sqlc with tenant-scoped queries (WHERE tenant_id = $1)
- Create tenant-aware logging wrappers (auto-include tenant_id)
- Provide tenant-aware metrics helpers (auto-tag with tenant_id)
- Abstract tenant isolation logic into reusable packages
- Use code generation to enforce tenant-aware patterns

**Example Go Middleware:**
```go
// Middleware extracts tenant context and adds to request context
func TenantContextMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        claims := jwt.ExtractClaims(c)
        tenantID := claims["tenant_id"].(string)
        tenantTier := claims["tenant_tier"].(string)
        
        ctx := context.WithValue(c.Request.Context(), "tenant_id", tenantID)
        ctx = context.WithValue(ctx, "tenant_tier", tenantTier)
        c.Request = c.Request.WithContext(ctx)
        c.Next()
    }
}
```


### 10. Global Customization Over One-Off Solutions
Support tenant-specific needs through configuration available to all tenants, not custom code.

**Implementation Guidelines:**
- Use feature flags stored in PostgreSQL (tenant-specific toggles)
- Implement configurable workflows (tenant can customize agent pipelines)
- Provide tenant-specific configuration via MCP interface
- Store tenant preferences in database, not code
- Use Crossplane compositions with tenant-specific parameters
- Allow tenant-specific resource limits via Kubernetes quotas
- Maintain single codebase serving all tenants

**Anti-Pattern:** Creating branches or forks for specific tenants.


### 11. Decompose Services by Multi-Tenant Profile
Service boundaries should consider tenant load, isolation requirements, and tiering strategies.

**Implementation Guidelines:**
- Separate services with different isolation needs (e.g., agent execution vs. API)
- Use AgentSandbox for tenant agent execution (strong isolation)
- Pool shared services (authentication, billing, monitoring)
- Silo high-security services for premium tiers
- Consider noisy neighbor impact when designing service boundaries
- Use NATS for async communication between services
- Deploy services via ArgoCD with tenant-aware configurations

**Service Isolation Patterns:**
- **Pooled**: Shared infrastructure, tenant isolation via RLS/RBAC (API, database)
- **Bridged**: Shared control plane, isolated data plane (agent execution)
- **Siloed**: Dedicated infrastructure per tenant (enterprise tier only)


### 12. No One-Size-Fits-All Architecture
Adapt architecture to business needs, compliance, market segments, and domain requirements.

**Implementation Guidelines:**
- Support multiple deployment models (shared cluster, dedicated cluster, BYOC)
- Implement flexible isolation strategies (pool, bridge, silo)
- Design for compliance requirements (data residency, encryption, audit)
- Support different tenant personas (developer, enterprise, ISV)
- Use Crossplane to abstract infrastructure differences
- Maintain operational consistency across deployment models
- Use GitOps to manage configuration across environments

**Kiro-Specific Considerations:**
- BYOC model: Tenant owns Hetzner infrastructure
- Edge deployments: Argo Agents for edge GitOps
- Hybrid deployments: Some tenants on-prem, some cloud


## Security Pillar: Multi-Tenant Security

### Tenant Isolation Strategies

**Database Isolation (PostgreSQL + RLS):**
```sql
-- Enable RLS on all tenant tables
ALTER TABLE agents ENABLE ROW LEVEL SECURITY;

-- Policy: Users can only access their tenant's data
CREATE POLICY tenant_isolation ON agents
    USING (tenant_id = current_setting('app.tenant_id')::uuid);

-- Set tenant context in connection
SET app.tenant_id = 'tenant-uuid-here';
```

**API Gateway Isolation (AgentGateway):**
- Validate JWT signature using JWKS from Ory Hydra
- Extract tenant_id from JWT claims
- Enforce rate limits per tenant_id
- Route requests to tenant-specific backends if needed
- Log all requests with tenant_id for audit


**Kubernetes Isolation:**
- Use namespaces for tenant workload isolation (premium/enterprise tiers)
- Apply NetworkPolicies to restrict cross-tenant communication
- Use PodSecurityPolicies/PodSecurityStandards
- Implement ResourceQuotas per tenant namespace
- Use AgentSandbox (runsc) for agent execution sandboxing
- Apply RBAC to prevent cross-tenant access

**Authorization (Ory Keto):**
```
// Define tenant relationships
tenant:tenant-123#member@user:user-456
tenant:tenant-123#admin@user:user-789

// Check permissions
Can user:user-456 execute agents on tenant:tenant-123?
```

### Data Protection

**Encryption:**
- TLS 1.2+ for all endpoints (cert-manager)
- Encrypt secrets in Git (KSOPS + Age)
- PostgreSQL encryption at rest (CNPG configuration)
- Encrypt S3 objects (Hetzner S3 server-side encryption)


**Audit Logging:**
- Log all tenant operations to OpenSearch
- Include: tenant_id, user_id, action, resource, timestamp, result
- Retain logs per compliance requirements
- Provide tenant-accessible audit logs via MCP interface
- Alert on suspicious cross-tenant access attempts

## Reliability Pillar: Multi-Tenant Resilience

### Fault Isolation
Prevent one tenant's failures from impacting others.

**Implementation:**
- Use circuit breakers for tenant-specific operations
- Implement per-tenant rate limiting (prevent resource exhaustion)
- Use separate connection pools per tenant tier (PgBouncer)
- Deploy critical services across multiple availability zones
- Use NATS for async processing (isolate failures)
- Implement tenant-specific health checks


### Disaster Recovery

**Backup Strategy:**
- CNPG automated PostgreSQL backups to Hetzner S3
- Point-in-time recovery capability per tenant
- GitOps repository backups (ArgoCD configurations)
- Tenant data export via MCP interface
- Test restore procedures regularly

**High Availability:**
- CNPG PostgreSQL cluster with replicas
- Kubernetes multi-node cluster (control plane HA)
- Load balancing across pods (Kubernetes Service)
- Health checks and automatic pod restart
- Zero-downtime deployments (rolling updates via ArgoCD)

### Noisy Neighbor Mitigation

**Resource Limits:**
- Set Kubernetes resource requests/limits per tenant tier
- Implement database connection limits per tenant
- Apply API rate limits per tenant (AgentGateway)
- Monitor tenant resource usage (VictoriaMetrics)
- Alert on tenants exceeding quotas


## Performance Efficiency Pillar: Multi-Tenant Performance

### Tenant-Aware Performance Optimization

**Database Performance:**
- Use pgvector indexes for AI feature similarity search
- Partition large tables by tenant_id (PostgreSQL partitioning)
- Use CNPG read replicas for read-heavy tenants
- Monitor slow queries per tenant (postgresai)
- Implement query timeouts per tenant tier
- Use materialized views for tenant analytics

**Database Access Patterns:**
- **sqlc for Go APIs**: Type-safe queries for Control Plane, Application Plane, MCP servers
- **PostgREST for Dashboards**: Auto-generated REST API for admin interfaces and reporting
- **Consistent RLS**: Both sqlc and PostgREST respect PostgreSQL Row-Level Security policies
- **Performance**: sqlc generates optimized queries, PostgREST provides efficient REST endpoints

**Caching Strategy:**
- Cache tenant configuration in memory (reduce database load)
- Use Redis for tenant session data (if needed)
- Cache tenant JWT public keys (JWKS)
- Implement tenant-aware CDN for static assets
- Cache tenant-specific API responses


**API Performance:**
- Use connection pooling (PgBouncer via CNPG)
- Implement request batching for bulk operations
- Use async processing via NATS for long-running tasks
- Apply compression for API responses
- Use HTTP/2 for multiplexing
- Implement GraphQL or gRPC for efficient data fetching

**Agent Execution Performance:**
- Use LiteLLM for model routing and caching
- Implement agent result caching (avoid redundant executions)
- Use AgentSandbox with optimized runtime configuration
- Pre-warm agent execution environments
- Use KEDA to scale agent workers based on queue depth

### Performance Monitoring

**Tenant-Specific Metrics:**
- API latency per tenant (p50, p95, p99)
- Database query performance per tenant
- Agent execution time per tenant
- Resource utilization per tenant (CPU, memory, storage)
- LiteLLM token usage and latency per tenant


## Cost Optimization Pillar: Multi-Tenant Economics

### Tenant Cost Attribution

**Measurement Strategy:**
- Track compute costs: Kubernetes pod CPU/memory usage per tenant
- Track storage costs: PostgreSQL storage, S3 usage per tenant
- Track network costs: Ingress/egress bandwidth per tenant
- Track AI costs: LiteLLM token usage per tenant
- Aggregate costs in VictoriaMetrics with tenant_id labels

**Cost Allocation Model:**
```
Tenant Cost = 
  (Compute Cost × Tenant CPU/Memory Usage) +
  (Storage Cost × Tenant Data Size) +
  (Network Cost × Tenant Bandwidth) +
  (AI Cost × Tenant Token Usage) +
  (Shared Platform Cost / Total Tenants)
```

**BYOC Considerations:**
- Tenant owns Hetzner infrastructure (direct billing)
- Platform charges for: control plane, monitoring, support
- Measure platform overhead separately from tenant workload costs


### Cost Optimization Strategies

**Right-Sizing:**
- Use KEDA to scale pods based on actual demand
- Use Karpenter to optimize node utilization
- Implement idle timeout for inactive tenant workloads
- Use spot instances for non-critical workloads (if applicable)
- Optimize database connection pooling (PgBouncer)

**Resource Efficiency:**
- Use efficient container images (distroless, Alpine)
- Implement lazy loading for tenant resources
- Use shared services where isolation permits
- Optimize database queries (sqlc generates efficient SQL)
- Use compression for storage and network

**Tier-Based Pricing:**
- Basic tier: Shared resources, best-effort performance
- Standard tier: Guaranteed resources, SLA-backed performance
- Premium tier: Dedicated resources, priority support
- Enterprise tier: Custom infrastructure, dedicated support


## Operational Excellence Pillar: Multi-Tenant Operations

### GitOps-First Operations

**Infrastructure as Code:**
- All infrastructure defined in Git (Crossplane, ArgoCD)
- Tenant provisioning via Git commits (no manual kubectl)
- Configuration changes via pull requests (review + approval)
- Rollback via Git revert
- Audit trail via Git history

**Deployment Strategy:**
- Use ArgoCD for continuous deployment
- Implement progressive rollouts (canary, blue-green)
- Test changes in staging environment first
- Use ArgoCD ApplicationSets for tenant-specific deployments
- Automate rollback on failure detection

### Tenant Lifecycle Management

**Onboarding:**
1. API receives tenant creation request
2. Validate request (authentication, authorization, quota)
3. Create Git commit with tenant configuration
4. Crossplane provisions infrastructure
5. ArgoCD deploys tenant resources
6. Initialize tenant database schema (sqlc migrations)
7. Create tenant identity (Ory Kratos/Keto)
8. Emit opensbt_tenantCreated event to NATS
9. Return tenant credentials and endpoints


**Offboarding:**
1. API receives tenant deletion request
2. Validate request (authentication, authorization)
3. Mark tenant as deleted (soft delete)
4. Backup tenant data to S3
5. Emit opensbt_tenantDeleted event to NATS
6. Deactivate tenant identity (Ory Kratos/Keto)
7. Schedule resource cleanup (grace period)
8. Delete tenant resources via GitOps
9. Remove tenant data after retention period

**Tier Changes:**
1. API receives tier change request
2. Validate request and new tier
3. Update tenant configuration in Git
4. ArgoCD applies new resource quotas
5. Update tenant JWT claims (new tier)
6. Emit opensbt_tenantTierChanged event to NATS
7. Apply new rate limits and feature flags

### Monitoring and Alerting

**Global Monitoring:**
- Cluster health (nodes, pods, services)
- Platform services health (PostgreSQL, NATS, ArgoCD)
- Overall system performance and capacity
- Security events and anomalies


**Tenant-Specific Monitoring:**
- Tenant resource usage and quotas
- Tenant API performance and errors
- Tenant database performance
- Tenant agent execution metrics
- Tenant cost trends

**Alerting Strategy:**
- Critical: Platform outage, security breach, data loss
- High: Tenant SLA violation, resource exhaustion, service degradation
- Medium: Tenant quota exceeded, performance degradation
- Low: Tenant usage anomalies, cost spikes

**Incident Response:**
1. Alert triggered (VictoriaMetrics → Alertmanager)
2. On-call engineer notified
3. Assess impact (global vs. tenant-specific)
4. Isolate affected tenants if needed
5. Investigate root cause (logs, metrics, traces)
6. Apply fix (hotfix or rollback)
7. Verify resolution
8. Post-mortem and prevention measures


## Testing Strategy for Multi-Tenant Systems

### E2E Testing (TDD Approach)

**Tenant Isolation Tests:**
- Verify tenant A cannot access tenant B's data
- Test cross-tenant API access attempts (should fail)
- Validate RLS policies in PostgreSQL
- Test Kubernetes namespace isolation
- Verify Ory Keto authorization boundaries

**Multi-Tenant Load Tests:**
- Simulate multiple tenants with concurrent requests
- Test noisy neighbor scenarios (one tenant high load)
- Validate resource limits and quotas
- Test autoscaling behavior (KEDA, Karpenter)
- Measure performance degradation under load

**Tenant Lifecycle Tests:**
- Test tenant onboarding flow (end-to-end)
- Test tenant tier upgrades/downgrades
- Test tenant offboarding and data deletion
- Verify GitOps workflows (ArgoCD sync)
- Test rollback scenarios


**Integration Tests (Testcontainers-Go):**
```go
// Example: Test tenant isolation in PostgreSQL
func TestTenantIsolation(t *testing.T) {
    // Start PostgreSQL with Testcontainers
    ctx := context.Background()
    pgContainer, err := postgres.RunContainer(ctx)
    require.NoError(t, err)
    defer pgContainer.Terminate(ctx)
    
    // Setup: Create two tenants
    tenant1 := createTenant(t, "tenant-1")
    tenant2 := createTenant(t, "tenant-2")
    
    // Test: Tenant 1 creates data
    agent1 := createAgent(t, tenant1, "agent-1")
    
    // Test: Tenant 2 cannot access tenant 1's data
    _, err = getAgent(t, tenant2, agent1.ID)
    assert.Error(t, err, "Tenant 2 should not access tenant 1's agent")
    
    // Test: Tenant 1 can access their own data
    agent, err := getAgent(t, tenant1, agent1.ID)
    assert.NoError(t, err)
    assert.Equal(t, agent1.ID, agent.ID)
}
```


## Implementation Checklist

When implementing a new multi-tenant feature, ensure:

### Security
- [ ] Tenant context extracted from JWT and validated
- [ ] Database queries include tenant_id filter (RLS enabled)
- [ ] Authorization checked via Ory Keto
- [ ] Audit logging includes tenant_id
- [ ] No hardcoded tenant identifiers in code
- [ ] Cross-tenant access attempts logged and alerted

### Observability
- [ ] Metrics tagged with tenant_id
- [ ] Logs include tenant_id field
- [ ] Tenant-specific dashboards created
- [ ] Alerts configured for tenant SLA violations
- [ ] Cost attribution metrics implemented

### Performance
- [ ] Resource limits defined per tenant tier
- [ ] Autoscaling configured (KEDA/Karpenter)
- [ ] Database indexes optimized for tenant queries
- [ ] Connection pooling configured
- [ ] Caching strategy implemented


### Reliability
- [ ] Fault isolation implemented (circuit breakers, rate limits)
- [ ] Backup and restore tested
- [ ] High availability configured (replicas, health checks)
- [ ] Disaster recovery plan documented
- [ ] Rollback procedure tested

### Operations
- [ ] GitOps workflow implemented (no manual kubectl)
- [ ] Tenant lifecycle automation complete
- [ ] Monitoring dashboards created
- [ ] Runbooks documented
- [ ] E2E tests passing (Testcontainers-Go)

### Cost
- [ ] Cost attribution metrics implemented
- [ ] Resource quotas defined per tier
- [ ] Autoscaling configured to minimize waste
- [ ] Cost dashboards created
- [ ] Pricing model validated


## Anti-Patterns to Avoid

### Architecture Anti-Patterns
- **Per-Tenant Infrastructure**: Creating dedicated infrastructure for each tenant breaks SaaS economics
- **Manual Tenant Provisioning**: Requires human intervention, doesn't scale
- **Hardcoded Tenant Logic**: Tenant-specific code branches instead of configuration
- **Missing Tenant Context**: Services that don't know which tenant they're serving
- **Shared Credentials**: Multiple tenants using same database credentials
- **No Resource Limits**: Allowing unlimited resource consumption per tenant

### Security Anti-Patterns
- **Application-Only Isolation**: Not using database-level isolation (RLS)
- **Trusting Client Input**: Not validating tenant_id from JWT
- **Missing Audit Logs**: No record of tenant operations
- **Weak Tenant Boundaries**: Allowing cross-tenant API calls
- **Shared Secrets**: Using same encryption keys for all tenants


### Operations Anti-Patterns
- **Manual Configuration**: Using kubectl instead of GitOps
- **No Tenant Metrics**: Unable to see per-tenant health and performance
- **Reactive Scaling**: Waiting for performance issues before scaling
- **No Cost Tracking**: Unable to attribute costs to tenants
- **Missing Rollback Plan**: No way to undo failed deployments
- **Insufficient Testing**: Not testing multi-tenant scenarios

### Data Anti-Patterns
- **Tenant Data Mixing**: Storing multiple tenants' data without clear boundaries
- **No Data Residency**: Ignoring compliance requirements for data location
- **Missing Backups**: No tenant-specific backup and restore capability
- **Inefficient Queries**: Not optimizing for multi-tenant query patterns
- **No Data Lifecycle**: Not handling tenant data retention and deletion


## Kiro Platform-Specific Patterns

### MCP-First Architecture
All platform capabilities exposed via MCP interface for agent consumption.

**Pattern:**
```
Agent → MCP Client → MCP Server → Platform API → PostgreSQL
```

**Benefits:**
- Standardized interface for all operations
- Easy to add new capabilities (new MCP tools)
- Agents can discover available operations
- Consistent authentication and authorization

**Implementation:**
- MCP servers written in Go
- Use Gin for HTTP endpoints
- Use sqlc for database queries (Go APIs)
- Use PostgREST for dashboard APIs
- Include tenant context in all operations
- Expose via AgentGateway with rate limiting


### BYOC Model Considerations

**Tenant-Owned Infrastructure:**
- Tenant provisions Hetzner account
- Tenant owns compute, storage, network costs
- Platform manages via Crossplane
- Platform provides control plane and monitoring

**Implications:**
- Cost attribution simpler (tenant pays directly)
- Data residency controlled by tenant
- Compliance easier (tenant's infrastructure)
- Platform charges for management, not resources

**Implementation:**
- Crossplane provider credentials per tenant
- CAPI/CAPH for tenant cluster provisioning
- Argo Agents for edge GitOps
- Tenant-specific ArgoCD applications
- Monitoring aggregated to central platform


### Agent Execution Isolation

**AgentSandbox Sandboxing:**
- Each agent execution runs in AgentSandbox sandbox
- Prevents agent code from accessing host system
- Limits syscalls and resource access
- Provides strong isolation between tenant agents

**Pattern:**
```
Tenant Request → NATS Queue → Kagents Worker → AgentSandbox → Agent Code
```

**Security Benefits:**
- Tenant agents cannot escape sandbox
- Malicious code contained
- Resource limits enforced by AgentSandbox
- Audit trail of agent operations

**Performance Considerations:**
- AgentSandbox adds ~10-20% overhead
- Pre-warm sandboxes for faster startup
- Use KEDA to scale workers based on queue depth
- Monitor sandbox resource usage per tenant


### GitOps Tenant Management

**Tenant as Code:**
```yaml
# tenants/acme-corp/tenant.yaml
apiVersion: platform.kiro.dev/v1
kind: Tenant
metadata:
  name: acme-corp
spec:
  tier: premium
  quotas:
    cpu: "10"
    memory: "20Gi"
    storage: "100Gi"
  features:
    - agents
    - clusters
    - monitoring
  credentials:
    hetzner:
      secretRef: acme-corp-hetzner-creds
```

**Workflow:**
1. Create tenant YAML in Git
2. Commit and push to repository
3. ArgoCD detects change
4. Crossplane provisions resources
5. CNPG creates tenant database
6. Ory Keto creates relationships
7. Tenant ready for use


## Quick Reference

### Tenant Context Flow
```
User Login → Ory Kratos (auth) → Ory Hydra (token) → JWT with tenant_id
→ AgentGateway (validate) → API (extract context) → PostgreSQL (RLS filter)
```

### Database Query Pattern
```go
// Always include tenant context
func GetAgent(ctx context.Context, tenantID uuid.UUID, agentID uuid.UUID) (*Agent, error) {
    // sqlc generates this with tenant_id filter
    return q.GetAgentByTenant(ctx, GetAgentByTenantParams{
        TenantID: tenantID,
        AgentID:  agentID,
    })
}
```

### Metrics Pattern
```go
// Always tag with tenant_id
requestDuration.WithLabelValues(
    tenantID,
    method,
    path,
    statusCode,
).Observe(duration)
```


### Logging Pattern
```go
// Always include tenant_id field
log.WithFields(log.Fields{
    "tenant_id": tenantID,
    "user_id":   userID,
    "action":    "create_agent",
    "agent_id":  agentID,
}).Info("Agent created successfully")
```

### Authorization Pattern
```go
// Check permission via Ory Keto
allowed, err := keto.Check(ctx, &ketoapi.RelationQuery{
    Namespace: "tenants",
    Object:    tenantID,
    Relation:  "member",
    Subject:   &ketoapi.Subject{ID: userID},
})
if err != nil || !allowed {
    return ErrUnauthorized
}
```

## References

- [AWS Well-Architected SaaS Lens](https://docs.aws.amazon.com/wellarchitected/latest/saas-lens/saas-lens.html)
- Content adapted for Kiro platform architecture
- Optimized for Kubernetes, PostgreSQL, Go stack
- Aligned with BYOC and GitOps-first principles


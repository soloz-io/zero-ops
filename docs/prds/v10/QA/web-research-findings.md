# Web Research Findings for Missing & Partial Questions

## Research Summary

Researched 7 questions (2 missing + 5 partial) to find open-source reference implementations.

**Result**: Found reference implementations for 6/7 questions (86%)

---

## ✅ FOUND: Open Source References

### Q3.2: Tenant-Defined Outcome Webhooks
**Status**: ✅ Reference found

**Open Source Projects**:

1. **Svix Webhooks** ([github.com/svix/svix-webhooks](https://github.com/svix/svix-webhooks))
   - Enterprise-ready webhooks service
   - Multi-tenant webhook delivery
   - Pattern: Tenant-scoped webhook destinations, topic subscriptions, event logs

2. **Hookdeck Outpost** ([hookdeck.com/outpost](https://hookdeck.com/outpost))
   - Open-source Event Destinations infrastructure
   - Multi-tenancy: Every resource scoped to specific tenant
   - Pattern: Webhook destinations, topic subscriptions, delivery attempts per tenant

3. **Standard Webhooks** ([standardwebhooks.com](https://www.standardwebhooks.com/))
   - Open-source tools and guidelines for webhooks
   - Secure and reliable webhook delivery
   - Pattern: Standardized webhook implementation

**Implementation Pattern**:
```
Tenant defines webhook endpoint → Platform sends outcome event → 
Webhook delivers to tenant endpoint → Tenant confirms receipt → 
Billing triggered based on outcome
```

---

### Q3.3: Billing Reconciliation & Dispute Resolution
**Status**: ✅ Reference found

**Open Source Projects**:

1. **Lago** ([github.com/getlago/lago](https://github.com/getlago/lago))
   - Open-source metering and usage-based billing API
   - **Credit Notes**: Full reconciliation system
   - **Dispute Handling**: 
     - Credit note reasons (duplicate_charge, order_cancelation, etc.)
     - Multiple credit methods: Refund, Offset, Credit for future use
     - Credit note wallet for future invoice application
     - Void credit functionality
   - **Audit Trail**: Each credit note has unique number, downloadable PDF
   - **Webhooks**: `credit_note.created` event for integration

**Implementation Pattern from Lago**:
```typescript
// Credit note creation with dispute reason
{
  "credit_note": {
    "invoice_id": "invoice_123",
    "reason": "duplicated_charge", // or "order_cancelation", etc.
    "credit_amount_cents": 1000,
    "refund_amount_cents": 500,
    "offset_amount_cents": 500,
    "items": [
      { "fee_id": "fee_123", "amount_cents": 1000 }
    ]
  }
}

// Credit methods:
// 1. Refund: Return money to customer (auto-triggered via payment provider)
// 2. Offset: Reduce invoice amount due
// 3. Credit wallet: Store for future invoice application
```

2. **Kill Bill** ([killbill.io](https://killbill.io/))
   - Open-source subscription billing platform
   - Invoice subsystem with credit/adjustment support
   - Event-oriented architecture for billing events
   - Apache 2.0 license

---

### Q4.1: Multi-Layer Rate Limiting
**Status**: ✅ Reference found

**Open Source Patterns**:

**Multi-Layer Rate Limiting Architecture**:
- **Layer 1 - Platform Level**: Global rate limits across all tenants
- **Layer 2 - Tenant Level**: Per-tenant quotas (e.g., 10k requests/hour)
- **Layer 3 - Agent Level**: Per-agent limits within tenant quota
- **Layer 4 - API Level**: Per-endpoint specific limits

**Reference Articles**:
1. "Multi-Layered Rate Limiting (User-Level, IP-Level, API-Level)" - C# Corner
2. "Rate Limiting Solutions with multitenant applications" - UMA Technology
3. "Rate Limiting in Multi-Tenant APIs" - DreamFactory

**Implementation Pattern**:
```
Request → Platform Limiter (check global) → 
Tenant Limiter (check tenant quota) → 
Agent Limiter (check agent quota) → 
API Limiter (check endpoint) → 
Process Request
```

**Key Concepts**:
- Hierarchical quota inheritance
- Per-tenant isolation with Redis/distributed cache
- Token bucket per layer
- Graceful degradation (return 429 with retry-after header)

---

### Q5.1: Agent Versioning & Canary Deployment
**Status**: ✅ Reference found

**Open Source Projects**:

1. **Argo Rollouts** ([argo-rollouts.readthedocs.io](https://argo-rollouts.readthedocs.io/))
   - Kubernetes progressive delivery controller
   - Canary deployments with automated analysis
   - Blue-green deployments
   - Traffic splitting (1% → 5% → 25% → 50% → 100%)
   - Automated rollback on metric degradation

2. **Flagger** ([flagger.app](https://flagger.app/))
   - Progressive delivery for Kubernetes
   - Automated canary deployments
   - Metrics-based promotion (HTTP/gRPC success rate, latency)
   - Works with Istio, Linkerd, NGINX, etc.
   - Automated rollback on failure

**Implementation Pattern**:
```yaml
# Argo Rollouts example
apiVersion: argoproj.io/v1alpha1
kind: Rollout
spec:
  strategy:
    canary:
      steps:
      - setWeight: 10    # 10% traffic to new version
      - pause: {duration: 5m}
      - setWeight: 50    # 50% traffic
      - pause: {duration: 5m}
      - setWeight: 100   # Full rollout
      analysis:
        templates:
        - templateName: success-rate
        startingStep: 1
```

**Comparison**:
- **Argo Rollouts**: Paired with ArgoCD, CRD-based, rich analysis
- **Flagger**: Works with any GitOps tool, service mesh integration

---

### Q5.2: Agent Testing Sandbox
**Status**: ✅ Reference found

**Open Source Projects**:

1. **E2B (e2b-dev/E2B)** ([github.com/e2b-dev/E2B](https://github.com/e2b-dev/E2B))
   - Secure sandboxed cloud environment for AI agents
   - **Isolation**: Firecracker microVMs (150ms boot time)
   - **Features**: Full Linux environment, terminal, filesystem, git
   - **Duration**: Up to 24 hours per sandbox
   - **SDK**: Python and TypeScript
   - **Use Cases**: CI/CD testing, coding agents, secure code execution

2. **Docker Sandboxes** ([docker.com/blog/docker-sandboxes](https://www.docker.com/blog/docker-sandboxes-a-new-approach-for-coding-agent-safety/))
   - Local sandboxes that wrap agents in containers
   - Mirror local workspace with strict boundaries
   - Isolation from local system

3. **OpenShell (NVIDIA)** - Announced at GTC 2026
   - Open-source sandbox runtime for AI agents
   - Containerized environment with locked filesystem
   - Network blocked by default
   - API keys never touch disk

4. **Agent Sandbox (Kubernetes-native)** ([itsfoss.gitlab.io](https://itsfoss.gitlab.io/post/open-source-agent-sandbox-enables-secure-deployment-of-ai-agents-on/))
   - Kubernetes-native framework
   - Proper isolation, resource constraints, monitoring

**Implementation Pattern (E2B)**:
```python
from e2b import Sandbox

# Create isolated sandbox
sandbox = Sandbox(template="base")

# Execute code safely
result = sandbox.run_code("print('Hello from sandbox')")

# Access filesystem
sandbox.filesystem.write("/app/test.txt", "data")

# Clean up
sandbox.close()
```

**Key Features**:
- **Isolation**: Separate compute environment (VM or container)
- **Resource Limits**: CPU, memory, disk quotas
- **Network Control**: Blocked by default, allowlist-based
- **Filesystem**: Isolated, read-only or ephemeral
- **Monitoring**: Execution logs, resource usage tracking

---

### Q3.1: Outcome-Based Billing Tracking
**Status**: ⚠️ Partial reference found (upgraded from existing)

**Additional Finding**:

**Lago** supports usage-based billing with event tracking:
- Event-based architecture for consumption tracking
- Aggregation rules for custom metrics
- Webhook integration for real-time events
- Can track custom "outcome" events (e.g., "task_completed")

**Pattern**:
```
Agent completes task → Emit outcome event → 
Lago aggregates events → Invoice generation → 
Billing based on outcome count
```

**Gap**: Still requires custom logic to define "success" criteria per agent type

---

## ❌ NOT FOUND: No Direct Open Source Reference

### Q4.2: Noisy Neighbor Prevention (Tenant-Level)
**Status**: ❌ No additional reference found beyond existing

**Existing References** (from assessment):
- Kubernetes resource limits (CPU, memory quotas)
- Service discovery isolation
- Rate limiting with token bucket

**Gap**: No open-source project found specifically for:
- Tenant-level priority queuing
- Dynamic resource allocation based on tenant tier
- Cross-tenant resource fairness algorithms

**Recommendation**: 
- Use existing K8s resource quotas + namespaces per tenant
- Implement custom priority queue with Redis
- Monitor per-tenant resource usage with Prometheus

---

## Summary Table

| Question | Status | Open Source Project(s) | License |
|----------|--------|------------------------|---------|
| Q3.1: Outcome Billing Tracking | ⚠️ Partial | Lago | AGPL-3.0 |
| Q3.2: Tenant Webhooks | ✅ Found | Svix, Hookdeck Outpost | Apache 2.0 |
| Q3.3: Billing Reconciliation | ✅ Found | Lago, Kill Bill | AGPL-3.0, Apache 2.0 |
| Q4.1: Multi-Layer Rate Limiting | ✅ Found | Patterns (no single project) | N/A |
| Q4.2: Noisy Neighbor Prevention | ❌ Not Found | - | - |
| Q5.1: Agent Versioning | ✅ Found | Argo Rollouts, Flagger | Apache 2.0 |
| Q5.2: Agent Testing Sandbox | ✅ Found | E2B, Docker Sandboxes | Apache 2.0 |

---

## Recommendations

### Immediate Actions
1. **Adopt Lago** for billing reconciliation (Q3.3) - mature, production-ready
2. **Adopt Svix** for tenant webhooks (Q3.2) - enterprise-grade, well-documented
3. **Adopt E2B** for agent testing sandbox (Q5.2) - purpose-built for AI agents
4. **Adopt Argo Rollouts** for canary deployments (Q5.1) - Kubernetes-native

### Custom Development Required
1. **Q4.2 (Noisy Neighbor)**: Build custom tenant-level resource management
   - Use K8s ResourceQuotas as foundation
   - Add custom priority queue logic
   - Implement tenant-tier based throttling

2. **Q4.1 (Multi-Layer Rate Limiting)**: Implement hierarchical rate limiter
   - Use Redis for distributed state
   - Implement token bucket per layer
   - Add graceful degradation logic

3. **Q3.1 (Outcome Billing)**: Extend Lago with custom outcome definitions
   - Define success criteria per agent type
   - Emit custom events to Lago
   - Build outcome verification logic

---

## License Considerations

**AGPL-3.0 (Lago)**:
- Requires source code disclosure if modified and deployed as a service
- Consider commercial license for closed-source deployment
- Alternative: Kill Bill (Apache 2.0) but less feature-rich

**Apache 2.0 (Svix, Argo Rollouts, E2B)**:
- Permissive license, safe for commercial use
- No source code disclosure requirements
- Can modify and deploy without restrictions

---

## Next Steps

1. Update `reference-source-assessment.md` with web research findings
2. Create detailed Q&A document with proper citations
3. Evaluate license compatibility for each project
4. Create POC for Lago + Svix integration
5. Design custom solutions for Q4.1 and Q4.2

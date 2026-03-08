**API-driven, DB-backed architecture for zero-ops at scale:**

**Current (Phase 1-12): CLI-only**
```
Admin → CLI → Management Cluster → CAPI → Tenant Clusters
```

**Future (Scale): API + DB**
```
Tenant/Admin → API Server → Database → Management Cluster → Tenant Clusters
                    ↓
                  Queue (async jobs)
```

**How it works:**

**1. Database stores:**
- Tenant metadata (org name, billing, contacts)
- Cluster inventory (name, version, status, cost)
- Desired state (K8s version, node count, ClusterClass)
- Audit logs (who requested what, when)

**2. API Server provides:**
```
POST /api/v1/clusters
{
  "tenant": "acme",
  "name": "acme-prod",
  "class": "hetzner-prod-talos-v1",
  "k8sVersion": "v1.31.6",
  "workers": 5
}
```

**3. Workflow:**
```
API request → Validate → Save to DB → Queue job → 
Worker reconciles → Create CAPI Cluster resource → 
Update DB status → Notify tenant
```

**4. Benefits:**
- Tenants self-service via API/UI
- Async provisioning (don't wait 15min)
- Query cluster inventory: `GET /api/v1/clusters?tenant=acme`
- Bulk operations: upgrade all clusters to v1.32
- Webhooks: notify tenant when cluster ready

**Example flow:**
1. Tenant clicks "Create Cluster" in UI
2. UI calls API: `POST /api/v1/clusters`
3. API saves to DB, returns job ID
4. Background worker reads DB, creates Cluster resource on management cluster
5. CAPI provisions infrastructure
6. Worker polls status, updates DB
7. Tenant gets webhook: "Cluster ready"

**Zero-ops would need:** API server (Go), DB (PostgreSQL), job queue (Redis), worker (reconciler).
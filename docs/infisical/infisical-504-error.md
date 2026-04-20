I'll investigate the ArgoCD controller health to understand the API server connectivity issues.

The controller pod is not ready (0/1). Let me check its logs and events:

The controller is failing readiness probes. Let me check the actual logs:

The ArgoCD controller is experiencing widespread API server connectivity issues - TLS handshake timeouts, connection losses, and i/o timeouts. Let me check the API server health:

API server reports healthy. Let me check if there are network issues or resource constraints:

Nodes are healthy. Let me check if this is a known ArgoCD issue and restart the controller:

I'll wait for the ArgoCD controller to restart and then monitor the sync progress.

Excellent! The controller is now ready (1/1) and the application shows OutOfSync. Let me trigger a sync:

Now we have a different issue - the CNPG webhook is not reachable. Let me check the webhook service:

The webhook service has no endpoints. Let me check the CNPG operator pod:

CNPG operator is not ready (0/1) with 44 restarts. Let me check its logs:

I'll restart the CNPG operator to fix the webhook issue:

## Summary: Infisical 504 Error - Root Cause & Fix Attempt

### Original Problem
Infisical API returning 504 Gateway Timeout due to database connection pool exhaustion. Infisical has hardcoded pool limit (max 10 connections), but 16 idle connections were stuck, preventing new requests.

### Approved Solution
Enable PgBouncer (CNPG's built-in connection pooler) to handle connection multiplexing at infrastructure layer. This is the production-grade, idiomatic Kubernetes solution.

### Implementation Status

**✅ Completed (GitOps Compliant):**
1. Updated `manifests/hub-core-services/platform-database/platform-db.yaml` with pooler config
2. Updated `internal/hub/components/installer.go` to use pooler endpoint
3. Committed and pushed changes (commit 7ea5bc2)
4. Documentation approved for Day 2 operations

**❌ Blocked by Infrastructure Issues:**
Cannot complete deployment due to cascading cluster problems:

1. **ArgoCD Controller**: Was experiencing API server connectivity issues (TLS handshake timeouts) for 1.5+ hours
   - **Action Taken**: Restarted controller pod
   - **Result**: Controller now healthy (1/1 Ready)

2. **CNPG Operator**: Not ready (0/1) with 44 restarts, webhook service has no endpoints
   - **Impact**: ArgoCD cannot apply Cluster CR changes because webhook validation fails
   - **Error**: `Post "https://cnpg-webhook-service.cnpg-system.svc:443/mutate-postgresql-cnpg-io-v1-cluster?timeout=10s": dial tcp 10.96.150.189:443: connect: operation not permitted`

### Current Blocker
CNPG operator is unhealthy, preventing the pooler configuration from being applied to the database cluster. The changes are ready in Git, but cannot be reconciled until CNPG operator recovers.

### Next Steps (Pending)
1. Investigate CNPG operator health (44 restarts indicates persistent issue)
2. Once CNPG operator is healthy, ArgoCD will sync pooler config
3. Run `./hub init-secrets` to regenerate connection secret with pooler endpoint
4. Restart Infisical pods
5. Validate API returns 200

**All work has been GitOps compliant - no manual kubectl apply used for infrastructure changes.**
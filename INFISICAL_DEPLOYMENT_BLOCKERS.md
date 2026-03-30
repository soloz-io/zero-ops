# Infisical Deployment Blockers - Status Report

## Current Situation

Infisical pods are stuck in Pending/CrashLoopBackOff due to multiple cascading issues.

## Root Cause Analysis

### 1. CRITICAL: Insufficient Worker Node Capacity (PRIMARY BLOCKER)

**Cluster Configuration:**
- All nodes: Hetzner cx23 (2 vCPU, 3.8GB RAM)
- 3 control-plane nodes
- 2 worker nodes

**Worker Node Utilization:**
```
Worker 1 (p7kzf): CPU 83% (1675m/2000m), Memory 74% (2776Mi/3809Mi)
Worker 2 (pnxhv): CPU 100% (2000m/2000m), Memory 98% (3670Mi/3809Mi) ← FULL
```

**Infisical Requirements:**
- CPU: 350m request
- Memory: 1000Mi request

**Result:** Infisical pods cannot be scheduled. Events show:
```
0/5 nodes are available: 2 Insufficient cpu, 2 Insufficient memory, 
3 node(s) had untolerated taint {node-role.kubernetes.io/control-plane: }
```

### 2. Encryption Key Mismatch (SECONDARY BLOCKER)

**Issue:** Infisical database contains encrypted data from previous ENCRYPTION_KEY. When `hub init-secrets` regenerated keys, new key cannot decrypt old data.

**Error:** `Unsupported state or unable to authenticate data` in KMS service

**Solution Required:** Drop and recreate infisical database before Infisical boots

### 3. Secret Zero Pattern (RESOLVED)

**Issue:** ArgoCD selfHeal was overwriting hub-managed secrets with "changeme" from Git

**Solution Applied:**
- Added `argocd.argoproj.io/sync-options: Replace=false` to all credential secrets
- Removed `stringData` from Git manifests
- Hub CLI now owns secret data, Git owns metadata only

## Resolution Path

### Immediate (Required to Unblock)

1. **Scale up worker nodes** to larger instance type (e.g., cx33: 4 vCPU, 8GB RAM)
   - This requires CAPI MachineDeployment update
   - Cannot be done via GitOps alone - requires cluster API changes

2. **Wipe Infisical database** to clear encrypted data:
   ```bash
   # Scale down Infisical
   kubectl patch application platform-infisical -n argocd --type merge \
     -p '{"spec":{"source":{"helm":{"valuesObject":{"infisical":{"replicaCount":0}}}}}}'
   
   # Drop database
   kubectl run -n zero-ops-system psql-drop --rm -i --restart=Never \
     --image=postgres:16-alpine --command -- \
     env PGPASSWORD=<superuser-password> psql -h platform-db-rw.zero-ops-system.svc \
     -U postgres -c "DROP DATABASE infisical; CREATE DATABASE infisical OWNER infisical;"
   
   # Scale up Infisical
   kubectl patch application platform-infisical -n argocd --type merge \
     -p '{"spec":{"source":{"helm":{"valuesObject":{"infisical":{"replicaCount":1}}}}}}'
   ```

### Long-term (Production Readiness)

1. **Right-size hub cluster** per PRD v9.0 requirements:
   - Control plane: 3x cx23 (acceptable for control plane)
   - Workers: 2x cx33 or larger (4 vCPU, 8GB RAM minimum)

2. **Implement resource quotas** to prevent overcommitment

3. **Add cluster autoscaling** via Karpenter (already in tech stack)

## Files Modified (GitOps Compliant)

1. `manifests/platform-infisical/values.yaml` - Added pod restart annotation
2. `manifests/platform-infisical/infisical-redis-credentials.yaml` - Added Replace=false
3. `manifests/platform-database/migrations/infisical-migrations.yaml` - Added database drop logic
4. `internal/hub/components/installer.go` - Consolidated secret generation

## Next Steps

**User Decision Required:**
1. Scale up worker nodes to cx33 or larger?
2. Or reduce Infisical resource requests (not recommended for production)?
3. Or add a 3rd worker node with current size?

Once capacity is available, the remaining fixes are automated via GitOps.

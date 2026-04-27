# Issue: Crossplane provider-sql Role Not Created After Phase 0 Fix

**Date:** 2026-04-27  
**Status:** BLOCKED  
**Related Spec:** `.kiro/specs/ongoing/adr-005-architectural-violations-fix/`  
**Related ADR:** `docs/adr/006-multi-tenant-database-pattern.md`

## Current Status

Phase 0 (ADR 006 fix) has been implemented but the provider-sql Role is not being created by Crossplane, blocking the entire TenantDatabase reconciliation.

### What Was Fixed (Phase 0)

1. ✅ **Deleted Resource 3** (`db-credentials-secret`) from composition - removed Crossplane-generated secret
2. ✅ **Updated Resource 1** (`db-credentials-restore`) to use ESO `creationPolicy: Owner` with explicit `data` array
3. ✅ **ESO Secret Created**: `tenant-app-creator-db-credentials` exists with all 5 fields (username, password, database, host, port)
4. ✅ **Composition Updated**: Latest revision `tenantdatabase-spoke-2f386b2` applied
5. ✅ **Old Object MR Deleted**: Removed conflicting `app-creator-db-credentials-secret` Object MR with finalizer patch

### Current State

**TenantDatabase Status:**
```json
{
  "type": "Synced",
  "status": "False",
  "reason": "ReconcileError",
  "message": "Unsynced resources: db-user"
}
```

**Observations:**
- TenantDatabase claim shows `db-user` in `resourceRefs` list
- provider-sql Role MR (`tenant_app-creator_user`) does NOT exist in cluster
- ESO secret exists and is Ready with correct ownerReference
- Database Object MR is Ready and Synced
- All prerequisites for Role creation are met

**Cluster State:**
```bash
# Secret exists with correct fields
kubectl get secret tenant-app-creator-db-credentials -n tenant-app-creator
# NAME                                TYPE     DATA   AGE
# tenant-app-creator-db-credentials   Opaque   5      3h

# Database is Ready
kubectl get object tenant-app-creator-db-database -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}'
# True

# Role does NOT exist
kubectl get role.postgresql.sql.crossplane.io -A
# No resources found
```

## Attempted Fixes

### Fix 1: Remove Old Object MR Finalizer
**Action:** Patched `app-creator-db-credentials-secret` to remove finalizer blocking deletion
```bash
kubectl patch object app-creator-db-credentials-secret --type json -p='[{"op": "remove", "path": "/metadata/finalizers"}]'
```
**Result:** ✅ Old Object MR deleted, but Role still not created

### Fix 2: Force Crossplane Reconciliation
**Action:** Annotated TenantDatabase to trigger reconciliation
```bash
kubectl annotate tenantdatabase app-creator crossplane.io/paused=false --overwrite
```
**Result:** ❌ No change, still shows "Unsynced resources: db-user"

### Fix 3: Update Composition Revision
**Action:** Patched TenantDatabase to use latest composition revision
```bash
kubectl patch tenantdatabase app-creator --type merge -p '{"spec":{"compositionRevisionRef":{"name":"tenantdatabase-spoke-2f386b2"}}}'
```
**Result:** ❌ No change, Role MR never created

### Fix 4: Delete and Recreate TenantDatabase
**Action:** Deleted and recreated TenantDatabase claim for clean state
```bash
kubectl delete tenantdatabase app-creator --wait=false
kubectl apply -f tenantdatabase-claim.yaml
```
**Result:** ❌ Same error persists, "Unsynced resources: db-user"

## Root Cause Analysis

### Hypothesis 1: Crossplane Composition Function Bug
The composition uses `mode: Pipeline` with `function-patch-and-transform`. The function may not be correctly expanding native MRs (Role, Grant, DefaultPrivileges) alongside Object MRs.

**Evidence:**
- Object MRs (db-credentials-restore, database, pooler-app-secret) are created successfully
- Native MRs (Role, Grant, DefaultPrivileges) are NOT created
- Composition revision shows both types of resources in the same `resources` array

### Hypothesis 2: Missing ProviderConfig
The Role resource references `providerConfigRef.name: default` but the ProviderConfig may not exist or may not be configured correctly.

**Evidence:**
- provider-sql pod is running: `provider-sql-d14af3510c40-856fbbf4f5-b85jr`
- No errors in provider-sql logs related to app-creator
- Other provider-sql resources (Grant, DefaultPrivileges) also not created

### Hypothesis 3: Secret Reference Timing Issue
Crossplane may be checking for the secret before ESO creates it, then never retrying.

**Evidence:**
- Secret exists and is Ready
- Secret has correct ownerReference to ESO ExternalSecret
- Database is Ready (no timing issue there)
- Crossplane shows "Unsynced" not "Waiting" or "Creating"

## Potential Fixes

### Option 1: Check ProviderConfig Exists (RECOMMENDED)
**Action:**
```bash
# Check if default ProviderConfig exists
kubectl get providerconfig.postgresql.sql.crossplane.io default -o yaml

# If missing, check what ProviderConfigs exist
kubectl get providerconfig.postgresql.sql.crossplane.io -A

# Check provider-sql logs for ProviderConfig errors
kubectl logs -n spoke-platform-ops provider-sql-d14af3510c40-856fbbf4f5-b85jr --tail=500 | grep -i "providerconfig\|error"
```

**Why This Might Work:**
- If ProviderConfig is missing, Crossplane cannot create the Role
- Error message "Unsynced resources" is generic and doesn't reveal the actual cause
- provider-sql requires a ProviderConfig with PostgreSQL connection details

### Option 2: Wrap Role in Object MR
**Action:** Change Resource 4 (db-user) from native Role MR to Object MR wrapping the Role
```yaml
- name: db-user
  base:
    apiVersion: kubernetes.crossplane.io/v1alpha2
    kind: Object
    spec:
      forProvider:
        manifest:
          apiVersion: postgresql.sql.crossplane.io/v1alpha1
          kind: Role
          spec:
            forProvider:
              passwordSecretRef:
                namespace: ""
                name: ""
                key: password
```

**Why This Might Work:**
- Object MRs are working (db-credentials-restore, database, pooler-app-secret all created)
- Native MRs are NOT working (Role, Grant, DefaultPrivileges all missing)
- This suggests the composition function may have a bug with native MRs

**Trade-off:**
- ❌ Violates ADR 005 (native MRs for Hub-local resources)
- ✅ Unblocks Phase 0 validation
- ⚠️ Can be reverted later if root cause is found

### Option 3: Check Crossplane Function Logs
**Action:**
```bash
# Find function-patch-and-transform pod
kubectl get pods -n spoke-platform-ops | grep function

# Check function logs for errors
kubectl logs -n spoke-platform-ops <function-pod-name> --tail=500 | grep -i "app-creator\|error\|role"
```

**Why This Might Work:**
- The composition uses `mode: Pipeline` with `function-patch-and-transform`
- Function may be failing to expand native MRs
- Logs will show if function is rejecting the Role resource

### Option 4: Manually Create Role MR
**Action:** Manually create the Role MR to test if provider-sql can create the user
```bash
kubectl apply -f - <<EOF
apiVersion: postgresql.sql.crossplane.io/v1alpha1
kind: Role
metadata:
  name: tenant_app-creator_user
spec:
  forProvider:
    privileges:
      login: true
      createDb: false
      superUser: false
    passwordSecretRef:
      namespace: tenant-app-creator
      name: tenant-app-creator-db-credentials
      key: password
  providerConfigRef:
    name: default
  deletionPolicy: Delete
EOF
```

**Why This Might Work:**
- Tests if provider-sql can create the user when Role MR exists
- Isolates the issue: Crossplane composition vs provider-sql functionality
- If this works, the issue is in the composition function

**Trade-off:**
- ⚠️ Manual resource, not managed by Crossplane composition
- ⚠️ Will be deleted if TenantDatabase is deleted
- ✅ Unblocks Phase 0 validation immediately

## Investigation Results

### ProviderConfig Status (Option 1) ✅
**Result:** ProviderConfig EXISTS and is configured correctly

```yaml
apiVersion: postgresql.sql.crossplane.io/v1alpha1
kind: ProviderConfig
metadata:
  name: default
spec:
  credentials:
    source: PostgreSQLConnectionSecret
    connectionSecretRef:
      namespace: spoke-platform-data
      name: crossplane-admin-credentials
  defaultDatabase: postgres
  sslMode: require
status:
  users: 3  # ProviderConfig is active and has 3 users
```

**Secret Status:**
- ✅ `crossplane-admin-credentials` secret exists in `spoke-platform-data` namespace
- ✅ Contains all required fields: username, password, host, port, database, connectionString
- ✅ Managed by ESO ExternalSecret

**Conclusion:** ProviderConfig is NOT the issue.

### Function Logs Status (Option 3) ✅
**Result:** Function is processing successfully, NO errors

**Key Findings:**
```json
{
  "msg": "Successfully processed patch-and-transform resources",
  "xr-kind": "TenantDatabase",
  "xr-name": "app-creator",
  "resource-templates": 8,
  "existing-resources": 7,
  "warnings": 0
}
```

**Analysis:**
- ✅ Function processes 8 resource templates (matches composition: 8 resources)
- ❌ Only 7 existing resources created (should be 8)
- ✅ No warnings or errors in function logs
- ✅ No errors in Crossplane controller logs

**Conclusion:** Function is working correctly. The issue is that Crossplane is NOT creating the Role MR even though the function successfully processes it.

### Root Cause: Crossplane Silently Skipping Native MRs

**Evidence:**
1. Function logs show 8 resource templates processed
2. Only 7 resources exist in cluster (missing Role)
3. TenantDatabase shows Role in `resourceRefs` but MR doesn't exist
4. No errors in any logs

**Hypothesis:** Crossplane may be silently skipping native MRs (Role, Grant, DefaultPrivileges) when they are mixed with Object MRs in the same composition. This could be a bug in Crossplane's composition reconciliation logic.

## Next Steps

1. **Manual Role Creation** (Option 4) - Test if provider-sql can create user when Role MR exists manually
2. **If Manual Works:** Wrap Role in Object MR (Option 2) as workaround

## Impact on Phase 1-5

Phase 0 must be validated before proceeding to Phase 1-5 (ADR 005 fixes). The current blocker prevents:
- ❌ PostgreSQL user creation
- ❌ AtlasMigration execution (requires user to exist)
- ❌ Application database access
- ❌ End-to-end tenant provisioning validation

**Recommendation:** Resolve this issue before proceeding to Phase 1.

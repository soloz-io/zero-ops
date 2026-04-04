# Hub Operator Troubleshooting Guide

## Fix Status Summary (Latest)

### ✅ Database Connection Fix (CNPG Wildcard)
**Status:** ALREADY APPLIED

Both files already handle the CNPG wildcard `*` dbname:
- `operators/hub-operator/internal/database/migrator.go` (lines 56-60)
- `operators/hub-operator/internal/database/roles.go` (lines 40-44)

Code correctly defaults to `postgres` database when encountering `*` or empty string.

### ✅ ArgoCD Sync-Wave Annotations
**Status:** ALREADY APPLIED

Both applications already have sync-wave "4":
- `manifests/platform-identity/argocd/mcp-server.yaml` (line 6)
- `manifests/platform-identity/argocd/kratos-ui.yaml` (line 6)

### ✅ OutOfSync Fix (ServerSideApply)
**Status:** ALREADY APPLIED

`manifests/argocd/apps/platform-external-secrets.yaml` already has:
- `ServerSideApply=true` (line 68)
- `RespectIgnoreDifferences=true` (line 69)

## Current Issue: Role Name Mismatch

**Problem:** HubEnvironment CR uses underscores, secrets use hyphens.

**CR:** `mcp_server`, `spoke_controller`, `spire_server`
**Secrets:** `mcp-server-db-credentials`, `spoke-controller-db-credentials`

**Fix:** Secret generation must match CR naming (underscores).

# Hub Operator Troubleshooting Guide

## Fix Status Summary

### ✅ Database Connection Fix (CNPG Wildcard)
**Status:** ALREADY APPLIED

Both files handle CNPG wildcard `*` dbname correctly.

### ✅ ArgoCD Sync-Wave Annotations
**Status:** ALREADY APPLIED

Both mcp-server and kratos-ui have sync-wave "4".

### ✅ OutOfSync Fix (ServerSideApply)
**Status:** ALREADY APPLIED

platform-external-secrets has ServerSideApply=true.

### ✅ Role Name Mismatch Fix
**Status:** FIXED

**Problem:** Operator wasn't generating credential secrets for CR-defined roles.

**Fix:** Added dynamic role credential generation in Phase 1.

**Next Steps:**
```bash
# 1. Rebuild operator
make docker-build docker-push IMG=ghcr.io/soloz-io/zero-ops/hub-operator:latest

# 2. Restart operator
kubectl rollout restart deployment hub-operator -n hub-platform-ops

# 3. Trigger reconciliation
kubectl annotate hubenvironment hub-production -n hub-platform-ops \
  ops.zero-ops.io/reconcile-trigger="$(date -u +%Y-%m-%dT%H:%M:%SZ)" --overwrite
```

## ROOT CAUSE & PERMANENT FIX

### The Core Problem

**Lines 371-376 in `generator.go`:**
```go
if _, ok := existingSecrets["infisical-secrets"]; ok {
    result.InfisicalSecrets = nil
    // Reuse existing redis credentials
    result.InfisicalRedisCredentials = nil
} else {
    // Generate NEW secrets with NEW passwords
}
```

**The Flaw:**
- If `infisical-secrets` is deleted (manually or by accident), the operator regenerates it with NEW keys
- But it doesn't restart dependent pods (Redis, Infisical)
- This creates the mismatch we experienced

### Why This Happened

On April 14, someone or something deleted the `infisical-secrets` secret, causing:
1. Operator regenerated with NEW ENCRYPTION_KEY and NEW Redis password
2. Redis kept running with OLD password
3. Infisical database encrypted with OLD ENCRYPTION_KEY
4. Result: Complete failure

### The Permanent GitOps Fix

**Three-Part Solution:**

## 1. Prevent Secret Deletion (Immediate)

Add immutability protection to critical secrets:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: infisical-secrets
  namespace: hub-platform-security
  annotations:
    argocd.argoproj.io/sync-options: Prune=false,Replace=false
    # Prevent accidental deletion
    kiro.io/protected: "true"
  finalizers:
    - kubernetes.io/pvc-protection  # Prevents deletion
```

## 2. Add Pod Restart Logic to Operator (Code Fix)

Update the operator to restart dependent pods when secrets are regenerated:

```go
// After regenerating infisical-secrets
if result.InfisicalSecrets != nil {
    // NEW secrets were generated - restart dependent pods
    logger.Info("New infisical-secrets generated, restarting dependent pods")
    
    // Restart Redis StatefulSet
    if err := r.restartStatefulSet(ctx, "redis-master", dataNamespace); err != nil {
        logger.Error(err, "Failed to restart Redis")
    }
    
    // Restart Infisical Deployment
    if err := r.restartDeployment(ctx, "platform-infisical-infisical-standalone-infisical", securityNamespace); err != nil {
        logger.Error(err, "Failed to restart Infisical")
    }
}
```

## 3. Add Backup/Restore for ENCRYPTION_KEY (Long-term)

Store the ENCRYPTION_KEY in a secure external location:

```go
// Before generating new ENCRYPTION_KEY, check if backup exists
if backupKey := r.getBackupEncryptionKey(ctx); backupKey != "" {
    // Restore from backup instead of generating new
    encryptionKey = backupKey
} else {
    // Generate new and backup
    encryptionKey, _ = GenerateSecurePassword()
    r.backupEncryptionKey(ctx, encryptionKey)
}
```

### Recommended Implementation Priority

**Immediate (Today):**
1. Add finalizers to `infisical-secrets` and `infisical-redis-credentials` to prevent deletion
2. Document the recovery procedure

**Short-term (This Week):**
1. Update operator code to restart pods when secrets regenerate
2. Add validation to detect secret/pod mismatches

**Long-term (Next Sprint):**
1. Implement ENCRYPTION_KEY backup to external vault
2. Add automated recovery from backup

---

## Immediate Action Items

**Should I:**
1. Drop the Infisical database to resolve the current issue?
2. Create a GitOps commit to add finalizers to prevent future deletions?
3. Document this incident as an ADR for the team?
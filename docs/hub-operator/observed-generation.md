# ObservedGeneration Pattern in Hub Operator

## What is Generation?

In Kubernetes, every resource has two generation-related fields:

- **`metadata.generation`**: Auto-incremented by the API server every time `.spec` is modified
- **`status.conditions[].observedGeneration`**: Manually set by the controller to track which generation it processed

## Why ObservedGeneration Matters

Without checking `observedGeneration`, a controller can't distinguish between:
- "I already processed this" (skip work)
- "The spec changed, I need to reprocess" (do work)

## The Pattern

### Setting Conditions (Controller Code)

Always set `observedGeneration` when updating a condition:

```go
meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{
    Type:               "InfisicalBootstrapped",
    Status:             metav1.ConditionTrue,
    Reason:             "Bootstrapped",
    Message:            "Infisical bootstrapped successfully",
    ObservedGeneration: hubEnv.Generation,  // CRITICAL: Always set this
})
```

### Checking Conditions (Controller Code)

Always verify the generation matches:

```go
// ❌ WRONG: Only checks if condition is True
if !meta.IsStatusConditionTrue(hubEnv.Status.Conditions, "InfisicalBootstrapped") {
    // This skips work even if spec changed!
}

// ✅ CORRECT: Checks condition exists, is True, AND matches current generation
condition := meta.FindStatusCondition(hubEnv.Status.Conditions, "InfisicalBootstrapped")
needsWork := condition == nil || 
    condition.Status != metav1.ConditionTrue || 
    condition.ObservedGeneration != hubEnv.Generation

if needsWork {
    // Do the work
}
```

## Real-World Example

### Scenario: User Updates Database Roles

1. **Initial State (Generation 1)**
   ```yaml
   metadata:
     generation: 1
   spec:
     database:
       roles:
       - name: mcp_server
   status:
     conditions:
     - type: DatabaseRolesConfigured
       status: "True"
       observedGeneration: 1  # Matches generation 1
   ```
   Controller sees: `1 == 1` → Skip Phase 2 (roles already configured)

2. **User Adds New Role (Generation 2)**
   ```yaml
   metadata:
     generation: 2  # Auto-incremented by API server
   spec:
     database:
       roles:
       - name: mcp_server
       - name: spoke_controller  # NEW ROLE
   status:
     conditions:
     - type: DatabaseRolesConfigured
       status: "True"
       observedGeneration: 1  # Still 1, doesn't match!
   ```
   Controller sees: `1 != 2` → Run Phase 2 (create new role)

3. **After Reconciliation**
   ```yaml
   metadata:
     generation: 2
   status:
     conditions:
     - type: DatabaseRolesConfigured
       status: "True"
       observedGeneration: 2  # Updated to match
   ```
   Controller sees: `2 == 2` → Skip Phase 2 (roles up to date)

## When to Use This Pattern

### Always Check ObservedGeneration For:

- **Idempotent Operations**: Bootstrap, migrations, role creation
- **Expensive Operations**: API calls, database queries, service restarts
- **Spec-Dependent Work**: Anything that changes based on `.spec` fields

### Don't Check ObservedGeneration For:

- **Continuous Monitoring**: Certificate rotation, password drift detection
- **External Event Triggers**: Watching secrets, deployments, CNPG clusters
- **Status-Only Updates**: Updating Ready condition after all phases complete

## Hub Operator Phase Mapping

| Phase | Condition | Checks ObservedGeneration? | Why |
|-------|-----------|---------------------------|-----|
| Phase 0 | InfisicalBootstrapped | ✅ Yes | Spec changes might require re-bootstrap |
| Phase 1 | SecretZeroGenerated | ✅ Yes | New roles in spec need new secrets |
| Phase 2a | MigrationsComplete | ✅ Yes | Schema changes tied to spec version |
| Phase 2b | DatabaseRolesConfigured | ✅ Yes | Roles list comes from spec |
| Phase 3a | SecretsBackedUp | ✅ Yes | New roles need backup to Infisical |
| Phase 3b | OAuthClientsRegistered | ✅ Yes | OAuth clients defined in spec |
| Phase 3c | NATSStreamsConfigured | ✅ Yes | NATS streams defined in spec |
| Ready | Ready | ✅ Yes | Overall readiness tied to spec |
| Rotation | CertificateRotationFailed | ❌ No | Triggered by external secret changes |
| Rotation | PasswordRotation | ❌ No | Triggered by ESO secret updates |

## Common Mistakes

### Mistake 1: Forgetting to Set ObservedGeneration
```go
// ❌ BAD: Missing observedGeneration
meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{
    Type:    "DatabaseRolesConfigured",
    Status:  metav1.ConditionTrue,
    Reason:  "Configured",
    Message: "Roles configured",
    // ObservedGeneration missing!
})
```

**Impact**: Condition will never match future generations, causing infinite reprocessing.

### Mistake 2: Using IsStatusConditionTrue Without Generation Check
```go
// ❌ BAD: Doesn't detect spec changes
if !meta.IsStatusConditionTrue(hubEnv.Status.Conditions, "DatabaseRolesConfigured") {
    createRoles()
}
```

**Impact**: Spec changes (new roles) won't trigger reprocessing.

### Mistake 3: Checking ObservedGeneration for External Triggers
```go
// ❌ BAD: Certificate rotation shouldn't check generation
condition := meta.FindStatusCondition(hubEnv.Status.Conditions, "CertificateRotationFailed")
if condition != nil && condition.ObservedGeneration == hubEnv.Generation {
    return // Skip rotation
}
```

**Impact**: Certificate rotation won't happen even when platform-db-ca changes.

## Debugging Generation Mismatches

### Check Current State
```bash
kubectl get hubenvironment hub-production -o yaml | grep -A 20 status
```

Look for:
```yaml
metadata:
  generation: 3  # Current spec version
status:
  conditions:
  - type: InfisicalBootstrapped
    observedGeneration: 1  # Stale! Should be 3
```

### Force Reprocessing
If a condition is stale and blocking progress:

```bash
# Option 1: Delete the stale condition (forces reprocessing)
kubectl patch hubenvironment hub-production --type=json \
  -p='[{"op": "remove", "path": "/status/conditions/3"}]'

# Option 2: Trigger manual reconciliation (clears all conditions)
kubectl annotate hubenvironment hub-production \
  ops.nutgraf.in/reconcile-trigger="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
```

### Check Operator Logs
```bash
kubectl logs -n hub-platform-ops -l app.kubernetes.io/name=hub-operator --tail=100
```

Look for:
```
Phase 0: Bootstrapping Infisical  # Should appear if generation mismatch detected
```

## Best Practices

1. **Always set `ObservedGeneration: hubEnv.Generation`** when updating conditions
2. **Always check generation match** for spec-dependent phases
3. **Never check generation** for external event-driven operations
4. **Use `meta.FindStatusCondition()`** instead of `meta.IsStatusConditionTrue()` when you need generation awareness
5. **Log generation mismatches** to help debugging:
   ```go
   if condition.ObservedGeneration != hubEnv.Generation {
       logger.Info("Condition stale, reprocessing",
           "condition", "InfisicalBootstrapped",
           "observedGeneration", condition.ObservedGeneration,
           "currentGeneration", hubEnv.Generation)
   }
   ```

## References

- [Kubernetes API Conventions - Generation](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#metadata)
- [controller-runtime meta package](https://pkg.go.dev/k8s.io/apimachinery/pkg/api/meta)

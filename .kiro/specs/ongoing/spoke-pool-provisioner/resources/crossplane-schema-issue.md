# Crossplane XRD Schema Validation Issue

**Date**: 2026-04-11  
**Status**: ✅ RESOLVED  
**Crossplane Version**: v1.20.5  
**Solution**: Use `provider-kubernetes` `Object` resource to wrap namespaced resources

## Resolution Summary

**Root Cause**: Direct composition of namespaced resources (ConfigMap, ClusterResourceSet) causes Crossplane to add `namespace` field to `spec.resourceRefs`, but the hardcoded Crossplane schema only allows `apiVersion`, `kind`, `name`.

**Solution**: Use `kubernetes.crossplane.io/v1alpha2` `Object` Managed Resource from `provider-kubernetes`. The `Object` resource is cluster-scoped, so Crossplane doesn't add namespace to resourceRefs, bypassing the schema validation bug entirely.

**Implementation**: Replaced direct composition (`spokepool-hetzner.yaml`) with Object-wrapped composition (`spokepool-hetzner-v2.yaml` → renamed to `spokepool-hetzner.yaml`).

## Issue Summary

SpokePool XR fails to sync with error:
```
cannot compose resources: cannot update composite resource spec.resourceRefs: 
failed to create typed patch object: errors:
  .spec.resourceRefs[0].namespace: field not declared in schema
  .spec.resourceRefs[2].namespace: field not declared in schema
  .spec.resourceRefs[3].namespace: field not declared in schema
  .spec.resourceRefs[4].namespace: field not declared in schema
```

## Root Cause

The composition creates **namespaced resources** (ConfigMaps in `platform-ops` namespace):
- `argocd-agent-config-percluster` - ConfigMap with per-cluster agent configuration
- `argocd-namespace-configmap` - ConfigMap with namespace manifest
- `cluster-resource-set` - ClusterResourceSet (namespaced)
- `hetzner-credentials-externalsecret` - ExternalSecret (namespaced)

Crossplane tracks these resources in `spec.resourceRefs` with namespace field, but the auto-generated CRD schema for `resourceRefs` only includes:
- `apiVersion` (string)
- `kind` (string)  
- `name` (string)

**Missing**: `namespace` field

## Architecture Context

This is a **valid and powerful pattern** for ClusterResourceSet-based spoke provisioning:
1. Composition creates per-cluster ConfigMaps in hub cluster
2. ConfigMaps contain YAML manifests to be applied to spoke clusters
3. ClusterResourceSet references these ConfigMaps
4. CAPI injects the manifests into spoke clusters during bootstrap

The composition design is correct. The issue is Crossplane's schema validation.

## Attempted Fixes

### Fix 1: Add `x-kubernetes-preserve-unknown-fields: true` to XRD spec
**File**: `xrds/definitions/spokepool-v1.yaml`
```yaml
spec:
  properties:
    spec:
      type: object
      x-kubernetes-preserve-unknown-fields: true
```
**Result**: ❌ Failed - Crossplane still generates strict CRD schema

### Fix 2: Add `additionalProperties: true` to XRD spec
```yaml
spec:
  properties:
    spec:
      type: object
      additionalProperties: true
      properties:
        region: ...
        nodePool: ...
```
**Result**: ❌ Failed - CRD generation ignores this setting

### Fix 3: Remove all spec properties (full permissive mode)
```yaml
spec:
  properties:
    spec:
      type: object
      x-kubernetes-preserve-unknown-fields: true
      # No properties defined
```
**Result**: ❌ Failed - User spec validation broken, can't create SpokePool instances

### Fix 4: Upgrade Crossplane v1.14.5 → v1.20.5
**File**: `manifests/argocd/apps/platform-crossplane.yaml`
```yaml
targetRevision: 1.20.5  # was 1.14.5
```
**Result**: ❌ Failed - Same schema generation behavior in v1.20.5

### Fix 5: Manually patch generated CRD
```bash
# Add additionalProperties to spec
kubectl patch crd spokepools.nutgraf.in --type=json \
  -p='[{"op": "add", "path": "/spec/versions/0/schema/openAPIV3Schema/properties/spec/additionalProperties", "value": true}]'

# Add additionalProperties to resourceRefs items
kubectl patch crd spokepools.nutgraf.in --type=json \
  -p='[{"op": "add", "path": "/spec/versions/0/schema/openAPIV3Schema/properties/spec/properties/resourceRefs/items/additionalProperties", "value": true}]'
```
**Result**: ❌ Failed - Crossplane ignores CRD patches, validation still fails

## Technical Analysis

### Crossplane CRD Generation Behavior

Crossplane automatically generates CRDs from XRDs with hardcoded schemas for internal fields:

**Generated `resourceRefs` schema** (from `internal/xcrd/schemas.go`):
```go
"resourceRefs": {
    Type: "array",
    Items: &extv1.JSONSchemaPropsOrArray{
        Schema: &extv1.JSONSchemaProps{
            Type: "object",
            Properties: map[string]extv1.JSONSchemaProps{
                "apiVersion": {Type: "string"},
                "name":       {Type: "string"},
                "kind":       {Type: "string"},
            },
            Required: []string{"apiVersion", "kind"},
        },
    },
    XListType: ptr.To("atomic"),
}
```

**Key observations**:
1. No `namespace` field in schema
2. `type: object` without `additionalProperties: true` = strict validation
3. This schema is hardcoded in Crossplane source code
4. XRD settings (`additionalProperties`, `x-kubernetes-preserve-unknown-fields`) are ignored for Crossplane-managed fields

### Why This Happens

When Crossplane reconciles a composition that creates namespaced resources:
1. Composition creates ConfigMap in `platform-ops` namespace
2. Crossplane tries to add reference to `spec.resourceRefs`:
   ```yaml
   resourceRefs:
     - apiVersion: v1
       kind: ConfigMap
       name: spoke-pool-eu-prod-01-argocd-agent-config
       namespace: platform-ops  # ← This field causes validation error
   ```
3. CRD schema validation rejects the update because `namespace` is not in the schema
4. Composition reconciliation fails

## Incorrect Hypotheses (Lessons Learned)

❌ **"Crossplane doesn't support namespaced composed resources"**  
→ FALSE: Crossplane fully supports namespaced resources

❌ **"Need to upgrade Crossplane for namespace support"**  
→ FALSE: v1.20.5 has same behavior as v1.14.5

❌ **"Composition design is wrong"**  
→ FALSE: Creating ConfigMaps for ClusterResourceSet is a valid pattern

❌ **"Need to move ConfigMaps to ArgoCD"**  
→ FALSE: Per-cluster dynamic ConfigMaps must be created by Crossplane

✅ **"XRD schema is too strict and blocks Crossplane internal fields"**  
→ TRUE: But fixing the XRD doesn't fix the CRD generation

## Current Status

**Blocked**: Cannot provision spoke clusters because SpokePool XR fails to sync.

**Workarounds considered**:
1. ❌ Remove namespaced resources from composition - breaks ClusterResourceSet pattern
2. ❌ Use cluster-scoped resources - ConfigMaps must be namespaced
3. ❌ Manually create ConfigMaps via ArgoCD - loses per-cluster dynamic generation
4. ❓ Patch Crossplane source code to add namespace to resourceRefs schema
5. ❓ Use Crossplane Functions to bypass composition engine
6. ❓ File upstream Crossplane issue/PR

## Files Modified

### Updated
- `xrds/definitions/spokepool-v1.yaml` - Added `additionalProperties: true`
- `manifests/argocd/apps/platform-crossplane.yaml` - Upgraded to v1.20.5
- `xrds/compositions/spokepool-hetzner.yaml` - Fixed ArgoCD Agent ConfigMap keys

### Fixed (Unrelated)
- ArgoCD Agent ConfigMap keys: `hub-url` → `agent.server.address`, `mode` → `agent.mode`
- ArgoCD Agent image: `quay.io/argoproj-labs/argocd-agent:v0.1.0` → `ghcr.io/argoproj-labs/argocd-agent/argocd-agent:latest`
- ArgoCD Agent configuration: CLI flags → environment variables from ConfigMap

## Next Steps

1. **Research Crossplane Functions** - Check if Functions can bypass this limitation
2. **File Crossplane Issue** - Report schema generation bug upstream
3. **Alternative Approach** - Investigate if ClusterResourceSet can reference cluster-scoped resources
4. **Patch Crossplane** - Fork and modify `internal/xcrd/schemas.go` to add namespace field
5. **Wait for Fix** - Monitor Crossplane releases for schema flexibility improvements

## References

- Crossplane source: `archived/cloud-native/crossplane/internal/xcrd/schemas.go`
- Official XRD examples: `archived/cloud-native/crossplane/cluster/composition/*.yaml`
- Issue tracking: `.kiro/specs/spoke-pool-provisioner/issues.md` (Issue #6)


## Final Solution (WORKING)

### What Was Done

1. **Deleted direct composition**: Removed `xrds/compositions/spokepool-hetzner.yaml` (v1) that directly composed namespaced resources
2. **Fixed namespace mismatch**: Changed CAPI Cluster namespace from `platform-capi` to `platform-ops` to match ClusterResourceSet
3. **Renamed v2 to v1**: Renamed `spokepool-hetzner-v2.yaml` → `spokepool-hetzner.yaml` to become the default composition
4. **Applied changes**: Deleted old composition, applied new one, recreated SpokePool

### Why It Works

The `provider-kubernetes` `Object` resource is **cluster-scoped** but manages **namespaced** resources:

```yaml
- name: capi-cluster
  base:
    apiVersion: kubernetes.crossplane.io/v1alpha2  # ← Cluster-scoped Managed Resource
    kind: Object
    spec:
      forProvider:
        manifest:
          apiVersion: cluster.x-k8s.io/v1beta1
          kind: Cluster
          metadata:
            namespace: platform-ops  # ← Creates namespaced resource
```

**Key insight**: Crossplane tracks the `Object` resource (cluster-scoped) in `resourceRefs`, not the underlying `Cluster` (namespaced). This bypasses the schema bug entirely.

### Verification

```bash
$ kubectl get spokepool spoke-pool-eu-prod-01 -n platform-ops
NAME                    SYNCED   READY   COMPOSITION         AGE
spoke-pool-eu-prod-01   True     False   spokepool-hetzner   38s
```

- ✅ **SYNCED: True** - Schema validation error resolved
- ⏳ **READY: False** - Resources still provisioning (normal)

Resource refs now show `Object` resources (no namespace field):
```yaml
resourceRefs:
  - apiVersion: kubernetes.crossplane.io/v1alpha2
    kind: Object
    name: spoke-pool-eu-prod-01
  - apiVersion: kubernetes.crossplane.io/v1alpha2
    kind: Object
    name: spoke-pool-eu-prod-01-argocd-agent-config
```

### Files Changed

- ❌ Deleted: `xrds/compositions/spokepool-hetzner.yaml` (direct composition)
- ✅ Updated: `xrds/compositions/spokepool-hetzner-v2.yaml` → `xrds/compositions/spokepool-hetzner.yaml`
  - Fixed: Cluster namespace `platform-capi` → `platform-ops`
  - Renamed: Removed `-v2` suffix to become default composition
- ✅ Updated: `manifests/argocd/apps/platform-crossplane.yaml` (v1.14.5 → v1.20.5)
- ✅ Fixed: ArgoCD Agent ConfigMap keys and image (unrelated issue)

## Lessons Learned

### Incorrect Assumptions
- ❌ "Crossplane doesn't support namespaced composed resources" - FALSE
- ❌ "Need to upgrade Crossplane" - FALSE (but we did anyway)
- ❌ "Composition design is wrong" - FALSE
- ❌ "XRD schema needs x-kubernetes-preserve-unknown-fields" - INSUFFICIENT

### Correct Understanding
- ✅ Crossplane hardcodes `resourceRefs` schema without namespace field
- ✅ Direct composition of namespaced resources triggers the bug
- ✅ `provider-kubernetes` `Object` resource is the idiomatic solution
- ✅ `Object` is cluster-scoped but manages namespaced resources
- ✅ This pattern is Crossplane best practice for managing native Kubernetes resources

### Key Takeaway

**When composing native Kubernetes resources (especially namespaced ones), always use `provider-kubernetes` `Object` resource instead of direct composition.** This is not a workaround - it's the intended Crossplane pattern.

## References

- Crossplane Issue: https://github.com/crossplane/crossplane/issues/6491
- Provider Kubernetes: https://github.com/crossplane-contrib/provider-kubernetes
- Crossplane Composition Docs: https://docs.crossplane.io/latest/concepts/compositions/

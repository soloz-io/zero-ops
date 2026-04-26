# Bugfix Requirements Document

## Introduction

The current Crossplane implementation violates ADR 005 (Hub-Spoke Crossplane Composition Pattern) by splitting tenant abstraction across ArgoCD and Crossplane, placing cluster-scoped resources in tenant-scoped compositions, and using ConfigMap-based migrations instead of Git-native pulls. This causes operational failures including duplicate resource provisioning attempts, garbage collection breaking shared infrastructure, and massive ConfigMap pushes to Spokes.

**Impact:** When multiple tenants share a Spoke Pool cluster, deleting one tenant garbage-collects cluster-wide mTLS certificates, breaking observability and GitOps for ALL remaining tenants on that Spoke.

## Bug Analysis

### Current Behavior (Defect)

1.1 WHEN a tenant is provisioned THEN ArgoCD deploys BOTH the `AINativeSaaS` XR to Hub AND raw Kubernetes primitives (Namespace, ResourceQuota, NetworkPolicy, RBAC) directly to Spoke via a second ApplicationSet (`tenant-spoke-provisioning`)

1.2 WHEN `TenantDatabase` composition executes THEN cluster-wide mTLS certificates (`alloy-client-cert`, `nats-leafnode-cert`, `argocd-agent-cert`) are distributed from Hub to Spoke within the tenant-scoped composition

1.3 WHEN 100 tenants are deployed to the same Spoke Pool cluster THEN Crossplane attempts to provision cluster-wide certificates 100 times (once per tenant)

1.4 WHEN one tenant is deleted from a Spoke Pool cluster THEN Crossplane garbage-collects the cluster-wide mTLS certificates because they are owned by the deleted tenant's `TenantDatabase` XR

1.5 WHEN cluster-wide certificates are garbage-collected THEN observability (Grafana Alloy), messaging (NATS), and GitOps (ArgoCD Agent) break for ALL other tenants on that Spoke cluster

1.6 WHEN the `universal-tenant` Helm chart is deployed THEN it globs raw SQL migration files into a ConfigMap requiring ArgoCD to push massive ConfigMaps to Spokes

1.7 WHEN `AtlasMigration` CR is provisioned THEN it reads migrations from ConfigMap instead of pulling natively from Git repository URL

### Expected Behavior (Correct)

2.1 WHEN a tenant is provisioned THEN ArgoCD SHALL deploy EXACTLY ONE resource to Hub: the `AINativeSaaS` XR with complete tenant specification in values.yaml

2.2 WHEN Hub Crossplane reconciles the `AINativeSaaS` XR THEN the `ainativesaas-starter-hetzner` Composition SHALL provision ALL Spoke resources (Namespace, ResourceQuota, NetworkPolicy, RBAC, ArgoCD Agent) via `provider-kubernetes` Object MRs

2.3 WHEN a Spoke Pool cluster is provisioned THEN the `SpokePool` composition SHALL provision cluster-wide mTLS certificates (`alloy-client-cert`, `nats-leafnode-cert`, `argocd-agent-cert`) ONCE per cluster

2.4 WHEN a tenant is deleted from a Spoke Pool cluster THEN cluster-wide certificates SHALL remain intact because they are owned by the `SpokePool` XR, not the tenant's XR

2.5 WHEN the `universal-tenant` Helm chart is deployed THEN it SHALL contain ONLY the `AINativeSaaS` XR manifest with tenant values mapped to XR spec fields

2.6 WHEN `AtlasMigration` CR is provisioned THEN it SHALL pull migrations natively from Git repository URL using `spec.dir.url` instead of reading from ConfigMap

2.7 WHEN the `AINativeSaaS` XRD is updated THEN it SHALL include fields for `resourceQuota`, `ownerEmail`, and `argocdAgent` configuration to support Composition-based provisioning

### Unchanged Behavior (Regression Prevention)

3.1 WHEN a tenant is provisioned to a Spoke Silo cluster (Enterprise tier) THEN the system SHALL CONTINUE TO provision a dedicated cluster with full isolation

3.2 WHEN Crossplane reconciles existing `TenantDatabase` XRs THEN the system SHALL CONTINUE TO provision PostgreSQL clusters with CNPG, pgvector, and PgBouncer

3.3 WHEN ArgoCD syncs tenant applications THEN the system SHALL CONTINUE TO pull from the OCI catalog registry

3.4 WHEN Spoke Controller watches Crossplane claim conditions THEN the system SHALL CONTINUE TO write status to Hub Centralised DB via PostgREST

3.5 WHEN Grafana Alloy collects metrics THEN the system SHALL CONTINUE TO remote_write to VictoriaMetrics on Hub

3.6 WHEN NATS Leaf Node buffers events THEN the system SHALL CONTINUE TO forward billing and lifecycle events to Hub JetStream

3.7 WHEN existing tenants use database migrations THEN the system SHALL CONTINUE TO execute migrations successfully during the transition period

## Bug Condition Derivation

### Bug Condition Function

```pascal
FUNCTION isBugCondition(X)
  INPUT: X of type TenantProvisioningRequest
  OUTPUT: boolean
  
  // Returns true when architectural violations are present
  RETURN (
    X.hasSecondApplicationSet = true OR
    X.tenantCompositionContainsClusterScopedResources = true OR
    X.migrationsDeliveredViaConfigMap = true
  )
END FUNCTION
```

### Property Specification (Fix Checking)

```pascal
// Property: ADR 005 Compliance - Unified Abstraction Layer
FOR ALL X WHERE isBugCondition(X) DO
  result ← provisionTenant'(X)
  ASSERT (
    result.argocdDeploysOnlyXR = true AND
    result.crossplaneProvisionsSpokeResources = true AND
    result.clusterScopedResourcesInSpokePoolComposition = true AND
    result.migrationsFromGitURL = true AND
    no_duplicate_cert_provisioning(result) AND
    no_garbage_collection_of_shared_resources(result)
  )
END FOR
```

### Preservation Property (Regression Prevention)

```pascal
// Property: Preservation Checking - Existing Tenant Functionality
FOR ALL X WHERE NOT isBugCondition(X) DO
  ASSERT provisionTenant(X) = provisionTenant'(X)
END FOR

// Specifically preserve:
// - Spoke Silo dedicated cluster provisioning
// - CNPG database provisioning with pgvector
// - OCI catalog pull mechanism
// - Status sync via Spoke Controller
// - Observability pipeline (Alloy → VictoriaMetrics)
// - Event forwarding (NATS → Hub JetStream)
```

### Counterexample (Concrete Bug Demonstration)

```yaml
# Scenario: Deploy 2 tenants to Spoke Pool "spoke-pool-eu-1"
# Tenant A: acme-corp
# Tenant B: widgets-inc

# Step 1: Deploy Tenant A
# Result: alloy-client-cert created, owned by acme-corp TenantDatabase XR

# Step 2: Deploy Tenant B  
# Result: Crossplane attempts to create alloy-client-cert again (conflict)

# Step 3: Delete Tenant A
# Result: Crossplane garbage-collects alloy-client-cert
# Impact: Tenant B's Grafana Alloy loses mTLS cert, observability breaks

# Expected (Fixed):
# - alloy-client-cert owned by SpokePool XR "spoke-pool-eu-1"
# - Deleting Tenant A does NOT affect cluster-scoped resources
# - Tenant B observability continues uninterrupted
```

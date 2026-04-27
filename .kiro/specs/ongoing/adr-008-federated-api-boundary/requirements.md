# ADR 008: Federated API Boundary Pattern - Requirements

## Introduction

Implement the Federated API Boundary Pattern to decouple Hub intent from Spoke implementation, enabling progressive rollouts, reducing Hub RBAC requirements, and improving scalability by introducing a Spoke-local XRD as the API contract between Hub and Spoke clusters.

## Requirements

### R1: Spoke API Boundary

**R1.1** WHEN a Spoke cluster is provisioned THEN a `SpokeTenantEnvironment` XRD SHALL be deployed to the Spoke cluster

**R1.2** WHEN the `SpokeTenantEnvironment` XRD is defined THEN it SHALL include spec fields for: tenantId, tier, databaseName, postgrestImage, resourceQuota

**R1.3** WHEN the `SpokeTenantEnvironment` XRD is defined THEN it SHALL include status fields for: ready (boolean), message (string), conditions (array)

**R1.4** WHEN the `SpokeTenantEnvironment` XRD is deployed THEN it SHALL be distributed via ArgoCD ApplicationSet to all Spoke Pool clusters

### R2: Spoke-Local Composition

**R2.1** WHEN a `SpokeTenantEnvironment` Composition is created THEN it SHALL provision the following resources locally on the Spoke:
- Namespace
- ServiceAccount  
- Role
- RoleBinding
- ResourceQuota
- CNPG Pooler
- PostgREST Deployment
- PostgREST Service
- AtlasMigration

**R2.2** WHEN the Spoke Composition reconciles THEN it SHALL aggregate the health of all composed resources into a single `status.ready` boolean

**R2.3** WHEN the Spoke Composition reconciles THEN it SHALL set `status.conditions[type=Ready]` to `True` only when ALL composed resources are ready

**R2.4** WHEN the Spoke Composition reconciles THEN it SHALL use Native Kubernetes resources (not wrapped in Object MRs) since resources are local to the Spoke

### R3: Hub Composition Simplification

**R3.1** WHEN the Hub `AINativeSaaS` Composition is refactored THEN it SHALL push exactly TWO resources to the Spoke:
- `TenantDatabase` XR (already exists)
- `SpokeTenantEnvironment` XR (new)

**R3.2** WHEN the Hub Composition pushes `SpokeTenantEnvironment` THEN it SHALL use `provider-kubernetes` Object MR with `providerConfigRef` pointing to the target Spoke cluster

**R3.3** WHEN the Hub Composition pushes `SpokeTenantEnvironment` THEN it SHALL include `readinessChecks` to wait for the remote XR's `Ready` condition to be `True`

**R3.4** WHEN the Hub Composition is refactored THEN it SHALL remove the following Object MRs (migrated to Spoke Composition):
- namespace
- service-account
- role
- role-binding
- resource-quota
- pooler
- postgrest-deployment
- postgrest-service
- atlasmigration

### R4: Status Propagation

**R4.1** WHEN the Spoke `SpokeTenantEnvironment` XR becomes ready THEN the Hub's Object MR SHALL observe this via `readinessChecks`

**R4.2** WHEN the Hub's Object MR observes the Spoke XR as ready THEN the Hub's `AINativeSaaS` XR SHALL transition to ready

**R4.3** WHEN the Spoke Composition fails to provision a resource THEN the failure SHALL be reflected in the Spoke XR's `status.message` field

**R4.4** WHEN the Hub observes a Spoke XR failure THEN the Hub XR SHALL reflect this in its own status conditions

### R5: Progressive Rollout Capability

**R5.1** WHEN the `SpokeTenantEnvironment` Composition is updated THEN it SHALL be possible to deploy the update to a single Spoke cluster first

**R5.2** WHEN a Composition update is deployed to one Spoke THEN tenants on other Spokes SHALL continue using the previous Composition version

**R5.3** WHEN a Composition update is validated on one Spoke THEN it SHALL be possible to roll out to additional Spokes incrementally

### R6: RBAC Reduction

**R6.1** WHEN the Hub's `provider-kubernetes` is configured THEN it SHALL only require RBAC permissions to `CREATE/UPDATE/PATCH/DELETE` the following resource types on Spokes:
- `tenantdatabases.nutgraf.in`
- `spoketenantenvironments.nutgraf.in`

**R6.2** WHEN the Hub's `provider-kubernetes` is configured THEN it SHALL NOT require `cluster-admin` privileges on Spoke clusters

**R6.3** WHEN the Hub's `provider-kubernetes` is configured THEN it SHALL NOT require permissions for Namespace, ServiceAccount, Role, RoleBinding, ResourceQuota, Deployment, Service, or other primitive types

### R7: Scalability Improvement

**R7.1** WHEN 1000 tenants are provisioned THEN the Hub SHALL track exactly 2000 remote resources (2 per tenant: TenantDatabase + SpokeTenantEnvironment)

**R7.2** WHEN the Hub tracks remote resources THEN the Hub ETCD footprint SHALL be reduced by at least 80% compared to the current implementation

**R7.3** WHEN the Hub Crossplane reconciles THEN the reconciliation loop SHALL complete faster due to fewer resources tracked

### R8: Preservation of Existing Functionality

**R8.1** WHEN a tenant is provisioned via the new pattern THEN database provisioning SHALL continue to work via the existing `TenantDatabase` XR

**R8.2** WHEN a tenant is provisioned via the new pattern THEN PostgREST SHALL continue to connect to the database via pooler credentials

**R8.3** WHEN a tenant is provisioned via the new pattern THEN AtlasMigration SHALL continue to execute migrations successfully

**R8.4** WHEN a tenant is provisioned via the new pattern THEN observability (Alloy), messaging (NATS), and GitOps (ArgoCD Agent) SHALL continue to function

**R8.5** WHEN a tenant is deleted THEN garbage collection SHALL only affect tenant-scoped resources, not cluster-scoped resources

## Success Criteria

**SC1:** `SpokeTenantEnvironment` XRD deployed to all Spoke clusters via ArgoCD

**SC2:** Spoke Composition provisions all 9 tenant resources locally

**SC3:** Hub Composition reduced from 11 Object MRs to 2 Object MRs

**SC4:** Hub `provider-kubernetes` RBAC reduced to 2 XRD types only

**SC5:** Test tenant provisioned successfully via new pattern

**SC6:** Progressive rollout demonstrated (update Composition on one Spoke, verify isolation)

**SC7:** Hub ETCD footprint reduced by >80% (measured via `kubectl get object` count)

**SC8:** All existing tenant functionality preserved (database, PostgREST, migrations, observability)

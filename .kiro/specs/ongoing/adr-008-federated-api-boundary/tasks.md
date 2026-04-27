# ADR 008: Federated API Boundary Pattern - Implementation Tasks

## Phase 1: Spoke XRD Creation

- [ ] 1.1 Create SpokeTenantEnvironment XRD
  - Create file `manifests/spoke/xrds/spoketenantenvironment.yaml`
  - Define XRD with spec fields: tenantId, tier, databaseName, postgrestImage, resourceQuota
  - Define status fields: ready (boolean), message (string), conditions (array)
  - Add validation patterns for tenantId (RFC 1123 compliant)
  - Add enum validation for tier (starter, enterprise)
  - Set default value for postgrestImage: "postgrest/postgrest:v12.0.2"
  - Commit changes to feature branch
  - _Requirements: R1.1, R1.2, R1.3_

- [ ] 1.2 Add SpokeTenantEnvironment XRD to ArgoCD ApplicationSet
  - Open `manifests/argocd/apps/platform-spoke-catalog-appsets.yaml`
  - Add new ApplicationSet `platform-spoketenantenvironment-xrds`
  - Configure generator to target all Spoke Pool clusters
  - Set source path to `manifests/spoke/xrds`
  - Set directory include filter to `spoketenantenvironment.yaml`
  - Configure automated sync with prune and selfHeal
  - Commit changes to feature branch
  - _Requirements: R1.4_

- [ ] 1.3 Manual Validation - XRD Deployment
  - **What to verify**: SpokeTenantEnvironment XRD deployed to all Spoke clusters
  - **How to verify**:
    - Push changes to feature branch
    - Wait for ArgoCD sync
    - For each Spoke cluster:
      - `kubectl get xrd spoketenantenvironments.nutgraf.in --context spoke-pool-eu-prod-01`
      - Verify XRD exists and shows Established=True
      - `kubectl get xrd spoketenantenvironments.nutgraf.in -o yaml --context spoke-pool-eu-prod-01`
      - Verify spec fields match design (tenantId, tier, databaseName, postgrestImage, resourceQuota)
      - Verify status fields match design (ready, message, conditions)
  - **Expected outcome**: XRD deployed to all Spokes, schema validated, ready for Composition
  - **Approval gate**: Proceed to Phase 2 only after approval

---

## Phase 2: Spoke Composition Creation

- [ ] 2.1 Create SpokeTenantEnvironment Composition
  - Create file `manifests/spoke/compositions/spoketenantenvironment-default.yaml`
  - Set compositeTypeRef to `spoketenantenvironments.nutgraf.in`
  - Add Resource 1: Namespace (native resource, not Object MR)
  - Add Resource 2: ServiceAccount (native resource)
  - Add Resource 3: Role with RBAC rules (native resource)
  - Add Resource 4: RoleBinding (native resource)
  - Add Resource 5: ResourceQuota (native resource)
  - Add Resource 6: CNPG Pooler (native resource)
  - Add Resource 7: PostgREST Deployment (native resource)
  - Add Resource 8: PostgREST Service (native resource)
  - Add Resource 9: AtlasMigration (native resource)
  - Configure patches for all resources (tenantId → name/namespace transformations)
  - Configure secret references (Pooler and PostgREST reference TenantDatabase secrets)
  - Commit changes to feature branch
  - _Requirements: R2.1, R2.4_

- [ ] 2.2 Add SpokeTenantEnvironment Composition to ArgoCD ApplicationSet
  - Open `manifests/argocd/apps/platform-spoke-catalog-appsets.yaml`
  - Add new ApplicationSet `platform-spoketenantenvironment-compositions`
  - Configure generator to target all Spoke Pool clusters
  - Set source path to `manifests/spoke/compositions`
  - Set directory include filter to `spoketenantenvironment-*.yaml`
  - Configure automated sync with prune and selfHeal
  - Commit changes to feature branch
  - _Requirements: R2.1_

- [ ] 2.3 Manual Validation - Composition Deployment
  - **What to verify**: SpokeTenantEnvironment Composition deployed to all Spoke clusters
  - **How to verify**:
    - Push changes to feature branch
    - Wait for ArgoCD sync
    - For each Spoke cluster:
      - `kubectl get composition spoketenantenvironment-default --context spoke-pool-eu-prod-01`
      - Verify Composition exists
      - `kubectl get composition spoketenantenvironment-default -o yaml --context spoke-pool-eu-prod-01`
      - Verify compositeTypeRef points to `spoketenantenvironments.nutgraf.in`
      - Verify 9 resources defined (Namespace, ServiceAccount, Role, RoleBinding, ResourceQuota, Pooler, PostgREST Deployment, PostgREST Service, AtlasMigration)
      - Verify all resources are native (not wrapped in Object MRs)
  - **Expected outcome**: Composition deployed to all Spokes, 9 resources defined, ready for testing
  - **Approval gate**: Proceed to Phase 3 only after approval

---

## Phase 3: Hub Composition Refactoring

- [ ] 3.1 Add SpokeTenantEnvironment Object MR to Hub Composition
  - Open `manifests/hub-core-services/crossplane/tenant-platform/compositions/ainativesaas-starter-hetzner.yaml`
  - Add new resource after `tenant-database-remote` (Resource 2)
  - Name: `spoke-tenant-environment`
  - Base: `kubernetes.crossplane.io/v1alpha2/Object`
  - Configure forProvider.manifest with `SpokeTenantEnvironment` XR
  - Add readinessChecks: `type: MatchCondition`, `matchCondition.type: Ready`, `matchCondition.status: "True"`
  - Configure patches:
    - cellId → providerConfigRef.name
    - tenantId → metadata.name (with transformation)
    - tenantId → spec.tenantId
    - tier → spec.tier
    - database.name → spec.databaseName
    - postgrest.image → spec.postgrestImage
    - resourceQuota.cpu → spec.resourceQuota.cpu
    - resourceQuota.memory → spec.resourceQuota.memory
    - resourceQuota.storage → spec.resourceQuota.storage
    - resourceQuota.pods → spec.resourceQuota.pods
  - Commit changes to feature branch
  - _Requirements: R3.1, R3.2, R3.3_

- [ ] 3.2 Manual Validation - Hub Composition with Both Patterns
  - **What to verify**: Hub Composition provisions both TenantDatabase and SpokeTenantEnvironment XRs
  - **How to verify**:
    - Push changes to feature branch
    - Wait for ArgoCD sync
    - Deploy test tenant XR to Hub: `kubectl apply -f test-tenant-xr.yaml`
    - Wait for Crossplane reconciliation
    - **Verify Hub XR**:
      - `kubectl get ainativesaas test-tenant-001 -o yaml`
      - Verify status shows both `databaseReady` and `environmentReady` fields
      - Verify overall Ready condition is True
    - **Verify Spoke XRs**:
      - `kubectl get tenantdatabase test-tenant-001 --context spoke-pool-eu-prod-01`
      - `kubectl get spoketenantenvironment test-tenant-001 --context spoke-pool-eu-prod-01`
      - Both should exist and show Ready=True
    - **Verify Spoke Resources**:
      - `kubectl get namespace tenant-test-tenant-001 --context spoke-pool-eu-prod-01`
      - `kubectl get deployment postgrest-test-tenant-001 -n tenant-test-tenant-001 --context spoke-pool-eu-prod-01`
      - All 9 resources from SpokeTenantEnvironment Composition should exist
    - **Verify Duplicate Resources**:
      - Check if resources exist twice (once from old Hub Object MRs, once from new Spoke Composition)
      - This is expected during transition - both patterns active
  - **Expected outcome**: Both patterns work simultaneously, test tenant fully provisioned, ready for cleanup
  - **Approval gate**: Proceed to Phase 4 only after approval

---

## Phase 4: Hub Composition Cleanup

- [ ] 4.1 Remove 9 Object MRs from Hub Composition
  - Open `manifests/hub-core-services/crossplane/tenant-platform/compositions/ainativesaas-starter-hetzner.yaml`
  - Delete Resource: `namespace` (Object MR)
  - Delete Resource: `service-account` (Object MR)
  - Delete Resource: `role` (Object MR)
  - Delete Resource: `role-binding` (Object MR)
  - Delete Resource: `resource-quota` (Object MR)
  - Delete Resource: `pooler` (Object MR)
  - Delete Resource: `postgrest-deployment` (Object MR)
  - Delete Resource: `postgrest-service` (Object MR)
  - Delete Resource: `atlasmigration` (Object MR)
  - Verify only 2 Object MRs remain: `tenant-database-remote` and `spoke-tenant-environment`
  - Commit changes to feature branch
  - _Requirements: R3.4_

- [ ] 4.2 Manual Validation - Hub Composition Simplified
  - **What to verify**: Hub Composition only provisions 2 remote XRs, no duplicate resources
  - **How to verify**:
    - Push changes to feature branch
    - Wait for ArgoCD sync
    - Deploy new test tenant XR to Hub: `kubectl apply -f test-tenant-002-xr.yaml`
    - Wait for Crossplane reconciliation
    - **Verify Hub Composition**:
      - `kubectl get composition ainativesaas-starter-hetzner -o yaml`
      - Count resources in spec.resources array (should be exactly 2)
      - Verify only `tenant-database-remote` and `spoke-tenant-environment` exist
    - **Verify Hub Object MRs**:
      - `kubectl get object -l crossplane.io/claim-name=test-tenant-002`
      - Should show exactly 2 Object MRs (TenantDatabase + SpokeTenantEnvironment)
    - **Verify Spoke Resources**:
      - `kubectl get namespace tenant-test-tenant-002 --context spoke-pool-eu-prod-01`
      - `kubectl get deployment postgrest-test-tenant-002 -n tenant-test-tenant-002 --context spoke-pool-eu-prod-01`
      - All 9 resources should exist (created by Spoke Composition only)
    - **Verify No Duplicates**:
      - Check resource counts match expected (1 Namespace, 1 ServiceAccount, etc.)
      - No duplicate resources from old Hub Object MRs
  - **Expected outcome**: Hub simplified to 2 Object MRs, Spoke Composition provisions all resources, no duplicates
  - **Approval gate**: Proceed to Phase 5 only after approval

---

## Phase 5: RBAC Reduction

- [ ] 5.1 Update Hub provider-kubernetes RBAC
  - Open `manifests/hub-core-services/crossplane/provider-kubernetes-rbac.yaml`
  - Replace existing ClusterRole rules with minimal rules:
    - apiGroups: ["nutgraf.in"]
    - resources: ["tenantdatabases", "spoketenantenvironments"]
    - verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
  - Add status subresource rules:
    - apiGroups: ["nutgraf.in"]
    - resources: ["tenantdatabases/status", "spoketenantenvironments/status"]
    - verbs: ["get", "update", "patch"]
  - Remove all rules for primitive types (Namespace, ServiceAccount, Role, RoleBinding, ResourceQuota, Deployment, Service, etc.)
  - Commit changes to feature branch
  - _Requirements: R6.1, R6.2, R6.3_

- [ ] 5.2 Manual Validation - RBAC Reduction
  - **What to verify**: Hub provider-kubernetes only has permissions for 2 XRD types
  - **How to verify**:
    - Push changes to feature branch
    - Wait for ArgoCD sync
    - **Verify ClusterRole**:
      - `kubectl get clusterrole crossplane-provider-kubernetes-spoke -o yaml`
      - Count rules (should be exactly 2: XR resources + XR status)
      - Verify apiGroups only contains "nutgraf.in"
      - Verify resources only contains "tenantdatabases", "spoketenantenvironments", and status subresources
      - Verify NO rules for "", "apps", "rbac.authorization.k8s.io" apiGroups
    - **Verify Tenant Provisioning Still Works**:
      - Deploy new test tenant: `kubectl apply -f test-tenant-003-xr.yaml`
      - Wait for Crossplane reconciliation
      - Verify tenant provisions successfully
      - Verify no RBAC errors in Crossplane logs: `kubectl logs -n crossplane-system -l app=crossplane`
  - **Expected outcome**: RBAC reduced to 2 XRD types only, tenant provisioning works, no permission errors
  - **Approval gate**: Proceed to Phase 6 only after approval

---

## Phase 6: End-to-End Validation

- [ ] 6.1 Verify preservation of existing functionality
  - **What to verify**: All tenant functionality works with new pattern
  - **How to verify**:
    - Deploy test tenant using new pattern
    - **Verify Database Provisioning**:
      - `kubectl get tenantdatabase test-tenant-004 --context spoke-pool-eu-prod-01`
      - Verify Ready=True
      - `kubectl exec -n spoke-platform-data shared-cnpg-1 -- psql -U postgres -c "\du tenant_test-tenant-004_user" --context spoke-pool-eu-prod-01`
      - Verify user exists
    - **Verify Pooler Connection**:
      - `kubectl get pooler test-tenant-004-pooler -n spoke-platform-data --context spoke-pool-eu-prod-01`
      - Verify Ready=True
      - Test connection via pooler
    - **Verify PostgREST API**:
      - `curl https://postgrest.test-tenant-004.example.com/health`
      - Verify returns 200
      - Test authenticated API call
    - **Verify AtlasMigration**:
      - `kubectl get atlasmigration test-tenant-004-migrations -n tenant-test-tenant-004 --context spoke-pool-eu-prod-01`
      - Verify Ready=True
      - Verify migrations applied successfully
    - **Verify Observability**:
      - Query VictoriaMetrics for tenant metrics
      - Verify Alloy collecting metrics
      - Verify no gaps in metric collection
    - **Verify Status Sync**:
      - Query Hub Centralised DB for tenant status
      - Verify Spoke Controller writing status
      - Verify status shows "ready"
  - **Expected outcome**: All functionality works, no regressions, tenant fully operational
  - **Approval gate**: Proceed to Phase 6.2 only after approval

- [ ] 6.2 Verify garbage collection isolation
  - **What to verify**: Deleting tenant does NOT affect other tenants on same Spoke
  - **How to verify**:
    - **Setup**: Deploy 2 test tenants to same Spoke:
      - test-tenant-005
      - test-tenant-006
    - **Verify Both Operational**:
      - Test database connections for both
      - Test PostgREST APIs for both
      - Verify both show Ready=True
    - **Delete First Tenant**:
      - `kubectl delete ainativesaas test-tenant-005`
      - Wait for Crossplane garbage collection
    - **Verify First Tenant Deleted**:
      - `kubectl get namespace tenant-test-tenant-005 --context spoke-pool-eu-prod-01`
      - Should NOT exist
      - `kubectl get spoketenantenvironment test-tenant-005 --context spoke-pool-eu-prod-01`
      - Should NOT exist
      - `kubectl get tenantdatabase test-tenant-005 --context spoke-pool-eu-prod-01`
      - Should NOT exist
    - **Verify Second Tenant Unaffected**:
      - `kubectl get namespace tenant-test-tenant-006 --context spoke-pool-eu-prod-01`
      - Should exist
      - Test database connection for test-tenant-006
      - Should succeed
      - Test PostgREST API for test-tenant-006
      - Should return 200
      - Query metrics for test-tenant-006
      - Should continue collecting
    - **Verify No Cluster-Wide Impact**:
      - Verify cluster-scoped resources (mTLS certs, NATS, ArgoCD Agent) still exist
      - Verify other tenants on Spoke unaffected
  - **Expected outcome**: First tenant fully deleted, second tenant unaffected, garbage collection isolated
  - **Approval gate**: Proceed to Phase 6.3 only after approval

- [ ] 6.3 Final validation and documentation
  - **What to verify**: Complete ADR 008 compliance, all requirements met
  - **How to verify**:
    - Review all previous validation checkpoints
    - Confirm SpokeTenantEnvironment XRD deployed to all Spokes (R1)
    - Confirm Spoke Composition provisions 9 resources (R2)
    - Confirm Hub Composition simplified to 2 Object MRs (R3)
    - Confirm status propagation works via readinessChecks (R4)
    - Confirm RBAC reduced to 2 XRD types (R6)
    - Confirm all existing functionality preserved (R8)
    - **Update Documentation**:
      - Update ADR 008 status to "Implemented"
      - Document migration path for existing tenants
    - **Create Runbook**:
      - Document how to update Spoke Compositions
      - Document how to target specific Spokes for rollout
      - Document troubleshooting procedures
  - **Expected outcome**: All requirements met, ADR 008 fully implemented, documentation complete
  - **Approval gate**: Mark ADR 008 implementation complete after approval

---

## Notes

- **NO automated tests**: All validation is manual as per requirements
- **Phase ordering**: Each phase must complete manual validation and receive approval before proceeding to next phase
- **GitOps compliance**: All changes via Git commits, ArgoCD reconciles (no `kubectl apply` for infrastructure)
- **Approval gates**: Explicit approval required at end of each phase before picking next phase tasks

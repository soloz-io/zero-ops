# ADR 008: Federated API Boundary Pattern - Implementation Tasks

## Phase 1: Spoke XRD Creation

- [ ] 1.1 Create SpokeTenantEnvironment XRD
  - Create file `manifests/spoke/xrds/spoketenantenvironment.yaml`
  - Define XRD with spec fields: tenantId, tier, databaseName, cellId, postgrestImage, resourceQuota
  - Add cellId field (required for nested TenantDatabase XR - Russian Doll pattern)
  - Define status fields: ready (boolean), databaseReady (boolean), message (string), conditions (array)
  - Add validation patterns for tenantId (RFC 1123 compliant)
  - Add enum validation for tier (starter, enterprise)
  - Set default value for postgrestImage: "postgrest/postgrest:v12.0.2"
  - Commit changes to feature branch
  - _Requirements: R1.1, R1.2, R1.3_

- [ ] 1.2 Add SpokeTenantEnvironment XRD to ArgoCD ApplicationSet
  - Open `manifests/argocd/apps/platform-spoke-catalog-appsets.yaml`
  - Add new ApplicationSet `platform-spoketenantenvironment-xrds`
  - Configure Cluster Generator to target all Spoke Pool clusters (matchLabels: spoke-type: pool)
  - Set source path to `manifests/spoke/xrds`
  - Set directory include filter to `spoketenantenvironment.yaml`
  - Add sync-wave annotation to Application template metadata: `argocd.argoproj.io/sync-wave: "1"` (XRDs must sync first)
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

- [ ] 2.1 Create SpokeTenantEnvironment Composition (Russian Doll Pattern)
  - Create file `manifests/spoke/compositions/spoketenantenvironment-default.yaml`
  - Use `mode: Pipeline` with `function-patch-and-transform` (consistent with existing compositions)
  - Set compositeTypeRef to `spoketenantenvironments.nutgraf.in`
  - Configure pipeline step with `apiVersion: pt.fn.crossplane.io/v1beta1` and `kind: Resources`
  - Add Resource 1: TenantDatabase XR (Russian Doll - nested inside SpokeTenantEnvironment)
  - Add readinessChecks to TenantDatabase XR (blocks subsequent resources until database ready)
  - Add ToCompositeFieldPath patch to propagate TenantDatabase status to SpokeTenantEnvironment status.databaseReady
  - Add Resource 2: Namespace (native resource, not Object MR)
  - Add Resource 3: ServiceAccount (native resource)
  - Add Resource 4: Role with RBAC rules (native resource)
  - Add Resource 5: RoleBinding (native resource)
  - Add Resource 6: ResourceQuota (native resource)
  - Add Resource 7: CNPG Pooler (native resource) with initContainer to wait for secret and startupProbe configuration
  - Add Resource 8: PostgREST Deployment (native resource) with initContainer to wait for secret and startupProbe configuration
  - Add Resource 9: PostgREST Service (native resource)
  - Add Resource 10: AtlasMigration (native resource)
  - Configure patches for all resources (tenantId → name/namespace transformations)
  - Configure secret references (Pooler and PostgREST reference TenantDatabase secrets)
  - Configure initContainers for Pooler and PostgREST to wait for secrets (prevents CrashLoopBackOff)
  - Configure startupProbe with initialDelaySeconds: 30, failureThreshold: 30 (5 minute timeout)
  - Commit changes to feature branch
  - _Requirements: R2.1, R2.4_

- [ ] 2.2 Add SpokeTenantEnvironment Composition to ArgoCD ApplicationSet
  - Open `manifests/argocd/apps/platform-spoke-catalog-appsets.yaml`
  - Add new ApplicationSet `platform-spoketenantenvironment-compositions`
  - Configure Cluster Generator to target all Spoke Pool clusters (matchLabels: spoke-type: pool)
  - Set source path to `manifests/spoke/compositions`
  - Set directory include filter to `spoketenantenvironment-*.yaml`
  - Add sync-wave annotation to Application template metadata: `argocd.argoproj.io/sync-wave: "2"` (Compositions sync after XRDs)
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
      - Verify `mode: Pipeline` is set
      - Verify pipeline step uses `function-patch-and-transform`
      - Verify 10 resources defined in pipeline input (TenantDatabase XR, Namespace, ServiceAccount, Role, RoleBinding, ResourceQuota, Pooler, PostgREST Deployment, PostgREST Service, AtlasMigration)
      - Verify Resource 1 is TenantDatabase XR (Russian Doll pattern)
      - Verify TenantDatabase XR has readinessChecks configured
      - Verify Resources 2-10 are native Kubernetes resources (not wrapped in Object MRs)
      - Verify Pooler and PostgREST have initContainers configured
      - Verify startupProbe configuration (initialDelaySeconds: 30, failureThreshold: 30)
  - **Expected outcome**: Composition deployed to all Spokes, Pipeline mode configured, 10 resources defined (TenantDatabase XR + 9 native resources), Russian Doll pattern implemented, initContainers and probes configured, ready for testing
  - **Approval gate**: Proceed to Phase 3 only after approval

- [ ] 2.4 Configure monitoring alert tuning for tenant provisioning
  - Open VictoriaMetrics alert rules configuration
  - Update `PodCrashLooping` alert rule to suppress for tenant namespaces during first 5 minutes
  - Update `PodNotReady` alert rule to suppress for tenant namespaces during first 5 minutes
  - Add alert rule condition: `namespace=~"tenant-.*" AND age < 5m → suppress`
  - Commit changes to feature branch
  - Push and verify alert rules updated in VictoriaMetrics
  - _Requirements: R2.4_

- [ ] 2.5 Manual Validation - Alert Tuning
  - **What to verify**: Monitoring alerts properly tuned for tenant provisioning
  - **How to verify**:
    - Query VictoriaMetrics alert rules: `curl https://victoria.nutgraf.in/api/v1/rules`
    - Verify `PodCrashLooping` rule has 5-minute grace period for tenant namespaces
    - Verify `PodNotReady` rule has 5-minute grace period for tenant namespaces
    - Deploy test tenant and verify no false alerts during first 5 minutes
    - Verify alerts fire correctly after 5-minute grace period if pod still failing
  - **Expected outcome**: Alert rules tuned, no false alerts during normal provisioning, real failures still detected
  - **Approval gate**: Proceed to Phase 3 only after approval

---

## Phase 3: Hub Composition Refactoring (Russian Doll Pattern)

- [ ] 3.1 Add SpokeTenantEnvironment Object MR to Hub Composition (Russian Doll - Single XR)
  - Open `manifests/hub-core-services/crossplane/tenant-platform/compositions/ainativesaas-starter-hetzner.yaml`
  - Add new resource: `spoke-tenant-environment` (Resource 1 - only resource needed)
  - Base: `kubernetes.crossplane.io/v1alpha2/Object`
  - Configure forProvider.manifest with `SpokeTenantEnvironment` XR
  - Add readinessChecks: `type: MatchCondition`, `matchCondition.type: Ready`, `matchCondition.status: "True"`
  - Configure patches:
    - cellId → providerConfigRef.name
    - tenantId → metadata.name (with transformation)
    - tenantId → spec.tenantId
    - cellId → spec.cellId (NEW - required for nested TenantDatabase XR)
    - tier → spec.tier
    - database.name → spec.databaseName
    - postgrest.image → spec.postgrestImage
    - resourceQuota.cpu → spec.resourceQuota.cpu
    - resourceQuota.memory → spec.resourceQuota.memory
    - resourceQuota.storage → spec.resourceQuota.storage
    - resourceQuota.pods → spec.resourceQuota.pods
  - Commit changes to feature branch
  - _Requirements: R3.1, R3.2, R3.3_

- [ ] 3.2 Manual Validation - Hub Composition with Russian Doll Pattern
  - **What to verify**: Hub Composition provisions SpokeTenantEnvironment XR only (Russian Doll contains TenantDatabase)
  - **How to verify**:
    - Push changes to feature branch
    - Wait for ArgoCD sync
    - Deploy test tenant XR to Hub: `kubectl apply -f test-tenant-xr.yaml`
    - Wait for Crossplane reconciliation
    - **Verify Hub XR**:
      - `kubectl get ainativesaas test-tenant-001 -o yaml`
      - Verify status shows `environmentReady` field (includes database readiness)
      - Verify overall Ready condition is True
    - **Verify Hub Object MRs**:
      - `kubectl get object -l crossplane.io/claim-name=test-tenant-001`
      - Should show exactly 1 Object MR (SpokeTenantEnvironment only)
    - **Verify Spoke XRs**:
      - `kubectl get spoketenantenvironment test-tenant-001 --context spoke-pool-eu-prod-01`
      - Should exist and show Ready=True
      - `kubectl get spoketenantenvironment test-tenant-001 -o yaml --context spoke-pool-eu-prod-01`
      - Verify status.databaseReady=True (propagated from nested TenantDatabase)
      - `kubectl get tenantdatabase test-tenant-001 --context spoke-pool-eu-prod-01`
      - Should exist (created by SpokeTenantEnvironment Composition - Russian Doll)
    - **Verify Spoke Resources**:
      - `kubectl get namespace tenant-test-tenant-001 --context spoke-pool-eu-prod-01`
      - `kubectl get deployment postgrest-test-tenant-001 -n tenant-test-tenant-001 --context spoke-pool-eu-prod-01`
      - All 10 resources from SpokeTenantEnvironment Composition should exist (TenantDatabase XR + 9 native resources)
    - **Verify No Duplicate Resources**:
      - Verify TenantDatabase XR created by Spoke Composition (not Hub)
      - Verify no duplicate resources
  - **Expected outcome**: Hub pushes 1 XR only, Spoke Composition creates TenantDatabase + 9 resources, Russian Doll pattern working, test tenant fully provisioned
  - **Approval gate**: Proceed to Phase 4 only after approval

---

## Phase 4: Hub Composition Cleanup (Remove Old Pattern)

- [ ] 4.1 Remove 10 Object MRs from Hub Composition (Keep Only SpokeTenantEnvironment)
  - Open `manifests/hub-core-services/crossplane/tenant-platform/compositions/ainativesaas-starter-hetzner.yaml`
  - Delete Resource: `tenant-database-remote` (Object MR - now nested in Spoke Composition)
  - Delete Resource: `namespace` (Object MR)
  - Delete Resource: `service-account` (Object MR)
  - Delete Resource: `role` (Object MR)
  - Delete Resource: `role-binding` (Object MR)
  - Delete Resource: `resource-quota` (Object MR)
  - Delete Resource: `pooler` (Object MR)
  - Delete Resource: `postgrest-deployment` (Object MR)
  - Delete Resource: `postgrest-service` (Object MR)
  - Delete Resource: `atlasmigration` (Object MR)
  - Verify only 1 Object MR remains: `spoke-tenant-environment`
  - Commit changes to feature branch
  - _Requirements: R3.4_

- [ ] 4.2 Manual Validation - Hub Composition Simplified (Russian Doll Complete)
  - **What to verify**: Hub Composition only provisions 1 remote XR, all resources created by Spoke Composition
  - **How to verify**:
    - Push changes to feature branch
    - Wait for ArgoCD sync
    - Deploy new test tenant XR to Hub: `kubectl apply -f test-tenant-002-xr.yaml`
    - Wait for Crossplane reconciliation
    - **Verify Hub Composition**:
      - `kubectl get composition ainativesaas-starter-hetzner -o yaml`
      - Count resources in spec.resources array (should be exactly 1)
      - Verify only `spoke-tenant-environment` exists
    - **Verify Hub Object MRs**:
      - `kubectl get object -l crossplane.io/claim-name=test-tenant-002`
      - Should show exactly 1 Object MR (SpokeTenantEnvironment only)
    - **Verify Spoke XRs**:
      - `kubectl get spoketenantenvironment test-tenant-002 --context spoke-pool-eu-prod-01`
      - Should exist and show Ready=True
      - `kubectl get tenantdatabase test-tenant-002 --context spoke-pool-eu-prod-01`
      - Should exist (created by SpokeTenantEnvironment Composition - Russian Doll)
    - **Verify Spoke Resources**:
      - `kubectl get namespace tenant-test-tenant-002 --context spoke-pool-eu-prod-01`
      - `kubectl get deployment postgrest-test-tenant-002 -n tenant-test-tenant-002 --context spoke-pool-eu-prod-01`
      - All 10 resources should exist (TenantDatabase XR + 9 native resources, all created by Spoke Composition)
    - **Verify No Duplicates**:
      - Check resource counts match expected (1 TenantDatabase XR, 1 Namespace, 1 ServiceAccount, etc.)
      - No duplicate resources from old Hub Object MRs
  - **Expected outcome**: Hub simplified to 1 Object MR, Spoke Composition provisions all resources (Russian Doll), no duplicates
  - **Approval gate**: Proceed to Phase 5 only after approval

---

## Phase 5: RBAC Reduction (Simplified to 1 XRD Type)

- [ ] 5.1 Update Hub provider-kubernetes RBAC (Russian Doll - 1 XRD Type Only)
  - Open `manifests/hub-core-services/crossplane/provider-kubernetes-rbac.yaml`
  - Replace existing ClusterRole rules with minimal rules:
    - apiGroups: ["nutgraf.in"]
    - resources: ["spoketenantenvironments"]
    - verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
  - Add status subresource rules:
    - apiGroups: ["nutgraf.in"]
    - resources: ["spoketenantenvironments/status"]
    - verbs: ["get", "update", "patch"]
  - Remove all rules for `tenantdatabases` and `tenantdatabases/status` (no longer needed - Russian Doll)
  - Remove all rules for primitive types (Namespace, ServiceAccount, Role, RoleBinding, ResourceQuota, Deployment, Service, etc.)
  - Commit changes to feature branch
  - _Requirements: R6.1, R6.2, R6.3_

- [ ] 5.2 Manual Validation - RBAC Reduction (1 XRD Type Only)
  - **What to verify**: Hub provider-kubernetes only has permissions for 1 XRD type (SpokeTenantEnvironment)
  - **How to verify**:
    - Push changes to feature branch
    - Wait for ArgoCD sync
    - **Verify ClusterRole**:
      - `kubectl get clusterrole crossplane-provider-kubernetes-spoke -o yaml`
      - Count rules (should be exactly 2: XR resources + XR status)
      - Verify apiGroups only contains "nutgraf.in"
      - Verify resources only contains "spoketenantenvironments" and "spoketenantenvironments/status"
      - Verify NO rules for "tenantdatabases" (Russian Doll - nested in Spoke)
      - Verify NO rules for "", "apps", "rbac.authorization.k8s.io" apiGroups
    - **Verify Tenant Provisioning Still Works**:
      - Deploy new test tenant: `kubectl apply -f test-tenant-003-xr.yaml`
      - Wait for Crossplane reconciliation
      - Verify tenant provisions successfully
      - Verify no RBAC errors in Crossplane logs: `kubectl logs -n crossplane-system -l app=crossplane`
  - **Expected outcome**: RBAC reduced to 1 XRD type only (SpokeTenantEnvironment), tenant provisioning works, no permission errors
  - **Approval gate**: Proceed to Phase 6 only after approval

---

## Phase 6: End-to-End Validation

- [ ] 6.1 Verify preservation of existing functionality
  - **What to verify**: All tenant functionality works with new pattern, no CrashLoopBackOff during provisioning
  - **How to verify**:
    - Deploy test tenant using new pattern
    - **Verify No CrashLoopBackOff**:
      - `kubectl get pods -n tenant-test-tenant-004 --context spoke-pool-eu-prod-01 -w`
      - Verify Pooler and PostgREST pods show `Init:0/1` (initContainer running) instead of `CrashLoopBackOff`
      - Verify pods transition to `Running` once TenantDatabase secret appears
      - Verify no pod restarts during provisioning
    - **Verify Database Provisioning**:
      - `kubectl get tenantdatabase test-tenant-004 --context spoke-pool-eu-prod-01`
      - Verify Ready=True
      - `kubectl exec -n platform-data shared-cnpg-1 -- psql -U postgres -c "\du tenant_test-tenant-004_user" --context spoke-pool-eu-prod-01`
      - Verify user exists
    - **Verify Pooler Connection**:
      - `kubectl get pooler test-tenant-004-pooler -n platform-data --context spoke-pool-eu-prod-01`
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
      - Verify no `PodCrashLooping` alerts fired during provisioning
    - **Verify Status Sync**:
      - Query Hub Centralised DB for tenant status
      - Verify Spoke Controller writing status
      - Verify status shows "ready"
  - **Expected outcome**: All functionality works, no CrashLoopBackOff, no false alerts, tenant fully operational
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
    - Confirm Spoke Composition provisions 10 resources: TenantDatabase XR + 9 native resources (R2)
    - Confirm Hub Composition simplified to 1 Object MR (R3)
    - Confirm status propagation works via readinessChecks (R4)
    - Confirm RBAC reduced to 1 XRD type (R6)
    - Confirm all existing functionality preserved (R8)
    - Confirm Russian Doll pattern eliminates race conditions
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

### ⚠️ Issue #44: AtlasMigration Authentication - Shared Cluster User Violates Isolation

FIX: zero-ops/.kiro/specs/spoke-pool-provisioner/resources/per-tenant-user-design.md

**STATUS**: IN PROGRESS — provider-sql approach blocked (Hub cannot reach Spoke CNPG)
**ROOT CAUSE**: All tenants share cluster-wide `app` user, violating security isolation  
**DISCOVERY**: Phase 3 validation - AtlasMigration password authentication failed

**IMPLEMENTATION ATTEMPT** (commit 854258c, 2026-04-22):
- provider-sql v0.9.0 installed on Hub: `INSTALLED=True, HEALTHY=True` ✅
- Composition updated with db-credentials-secret, db-user, grant-connect/tables/sequences ✅
- **BLOCKED**: provider-sql runs on Hub, Spoke CNPG is ClusterIP-only (not reachable cross-cluster)
- Error on Role CR: `cannot get ProviderConfig: ProviderConfig.postgresql.sql.crossplane.io "spoke-pool-eu-prod-01" not found`
- Root cause: Even with ProviderConfig, Hub cannot reach `shared-cnpg-rw.spoke-pool-system:5432`

**OPTIONS TO EVALUATE**:
1. **CNPG managed roles** — add `spec.managed.roles` to shared CNPG Cluster CR per tenant (Hub-side via provider-kubernetes, no network issue)
2. **Atlas migration SQL** — add `CREATE USER` + `GRANT` to tenant baseline migrations (Atlas runs on Spoke, has direct DB access)
3. **Expose CNPG via LoadBalancer** — security concern, adds external surface

**BLOCKED TASKS**: 3.6.9-3.6.15, 3.6.23-3.6.24

---
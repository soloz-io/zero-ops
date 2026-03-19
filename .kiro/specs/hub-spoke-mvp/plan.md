Perfect! Now I understand the architecture. Here's the **Hub Management MCP Feature List** for your SaaS Factory platform:

---

## **Hub Management MCP - Feature List**

### **Core Value Proposition**
"One-click control plane + application plane provisioning for tenants"

---

## **1. Tenant Lifecycle Management**

### 1.1 Tenant Onboarding
- **Tenant registration** - Capture tenant metadata (tenant_id, org_name, plan, features)
- **GitHub repo creation** - Fork/template tenant monorepo (control plane + app plane) into tenant's GitHub account
- **Fleet registry update** - Add tenant descriptor to global fleet-registry repo
- **Cluster provisioning** - Trigger CAPI to create dedicated or assign shared spoke cluster
- **ArgoCD agent deployment** - Install agent on spoke cluster, connect to hub
- **Vault namespace creation** - Provision tenant-specific secrets namespace
- **Initial credentials** - Generate and store tenant admin credentials

### 1.2 Tenant Offboarding
- **Cluster teardown** - Delete spoke cluster via CAPI (dedicated) or namespace cleanup (shared)
- **Fleet registry cleanup** - Remove tenant descriptor
- **Repo archival** - Archive tenant GitHub repo
- **Vault cleanup** - Revoke secrets and delete namespace
- **Billing finalization** - Export usage data, close billing account

### 1.3 Tenant Plan Management
- **Plan upgrades/downgrades** - Migrate between free/shared → dedicated spoke
- **Feature toggles** - Enable/disable platform features per tenant
- **Resource quota updates** - Adjust CPU/memory/storage limits

---

## **2. Spoke Cluster Provisioning (CAPI Integration)**

### 2.1 Dedicated Spoke Clusters
- **HA control plane** - 3 control plane nodes across 3 AZs (Hetzner regions)
- **Worker nodes** - Auto-scaling worker pools spread across AZs
- **ClusterClass selection** - Use `hetzner-prod-ubuntu-v1` or `hetzner-staging-ubuntu-v1`
- **Network isolation** - Private network per tenant
- **Load balancer** - Dedicated LB for tenant API endpoint

### 2.2 Shared Spoke Clusters
- **Multi-tenant control plane** - Single cluster for multiple free-tier tenants
- **Namespace isolation** - Per-tenant namespaces with RBAC
- **Resource quotas** - Enforce limits per tenant namespace
- **Network policies** - Isolate tenant workloads

### 2.3 Cluster Health Monitoring
- **Node health checks** - Monitor control plane and worker node status
- **Cluster readiness** - Verify cluster reaches Ready state before onboarding
- **Auto-healing** - Trigger CAPI reconciliation on failures

---

## **3. GitOps Orchestration (ArgoCD + ApplicationSets)**

### 3.1 Fleet Registry Management
- **Tenant descriptor schema** - Define tenant_id, repo_url, cluster_ref, plan, features
- **Git webhook triggers** - Detect new tenant descriptors in fleet-registry
- **ApplicationSet generation** - Auto-create ApplicationSets for new tenants

### 3.2 ArgoCD Agent Management
- **Agent deployment** - Install argocd-agent on spoke clusters
- **mTLS certificate provisioning** - Generate and distribute agent certificates
- **Agent health monitoring** - Track agent connectivity and sync status
- **Agent upgrades** - Rolling updates for agent versions

### 3.3 Control Plane Deployment
- **Tenant control plane repo** - Watch tenant's private repo for control plane manifests
- **Helm chart rendering** - Render control plane Helm templates with tenant-specific values
- **Sync policies** - Auto-sync or manual approval per tenant preference
- **Rollback support** - Revert to previous control plane versions

### 3.4 Application Plane Deployment
- **App plane repo** - Watch tenant's app plane directory
- **Multi-environment support** - Deploy to dev/staging/prod namespaces
- **Progressive delivery** - Canary/blue-green deployments via Argo Rollouts
- **Sync waves** - Ordered deployment of dependencies

---

## **4. Secrets Management (Vault Integration)**

### 4.1 Tenant Secret Provisioning
- **Vault namespace per tenant** - Isolated secret storage
- **Initial secrets** - Database credentials, API keys, certificates
- **Secret rotation** - Automated rotation policies
- **External Secrets Operator** - Sync Vault secrets to Kubernetes

### 4.2 Platform Secrets
- **Hetzner API tokens** - Stored in Vault, injected into CAPI
- **GitHub tokens** - For repo creation and fleet-registry updates
- **ArgoCD credentials** - Agent mTLS certificates
- **Billing API keys** - For usage tracking integrations

---

## **5. Tenant Customization & SDK**

### 5.1 Tenant Monorepo Template
- **Control plane starter** - Pre-configured Helm charts for common services (DB, cache, queue)
- **App plane starter** - Sample microservices with CI/CD pipelines
- **Built-in SaaS tools** - Identity (Keycloak), metrics (Prometheus), logging (Loki)
- **Customization guide** - Documentation for extending templates

### 5.2 open-sbt SDK
- **CLI tool** - `open-sbt init`, `open-sbt deploy`, `open-sbt scale`
- **Terraform modules** - Infrastructure-as-code for tenant resources
- **Helm library charts** - Reusable charts for common patterns
- **API client libraries** - SDKs for platform APIs (Go, Python, Node.js)

---

## **6. Observability & Monitoring**

### 6.1 Hub Metrics
- **Tenant count** - Active tenants by plan (free/paid)
- **Cluster health** - Spoke cluster status (Ready/Degraded/Failed)
- **ArgoCD sync status** - Applications in sync/out-of-sync/degraded
- **Resource utilization** - CPU/memory/storage across all spokes

### 6.2 Tenant Metrics (Exposed to Tenants)
- **Application health** - Pod status, restart counts
- **Sync history** - Deployment timeline and rollback events
- **Resource usage** - Per-namespace quotas and consumption
- **Cost attribution** - Billing breakdown by resource type

### 6.3 Alerting
- **Cluster failures** - Notify ops team on spoke cluster issues
- **Sync failures** - Alert on ArgoCD sync errors
- **Quota breaches** - Warn tenants approaching limits
- **Agent disconnections** - Detect offline agents

---

## **7. Billing & Usage Tracking**

### 7.1 Metering
- **Resource consumption** - Track CPU-hours, memory-hours, storage-GB
- **API usage** - Count control plane API calls
- **Data transfer** - Measure ingress/egress bandwidth
- **Feature usage** - Track enabled features per tenant

### 7.2 Billing Integration
- **Stripe/Chargebee integration** - Sync usage data to billing provider
- **Invoice generation** - Monthly billing reports
- **Payment status** - Track paid/unpaid accounts
- **Suspension logic** - Pause tenants with overdue payments

---

## **8. Multi-Tenancy & Isolation**

### 8.1 Network Isolation
- **Private networks** - Dedicated VPCs for paid tenants
- **Network policies** - Enforce namespace isolation in shared clusters
- **Ingress controllers** - Per-tenant ingress with TLS

### 8.2 RBAC & Access Control
- **Tenant admin roles** - Full access to tenant namespaces
- **Developer roles** - Read-only or deploy-only access
- **Platform admin roles** - Hub cluster management
- **Service accounts** - For CI/CD pipelines

---

## **9. Disaster Recovery & Backup**

### 9.1 Cluster Backups
- **Velero integration** - Scheduled backups of spoke clusters
- **Backup retention** - 30-day retention for paid tenants
- **Restore workflows** - One-click cluster restoration

### 9.2 GitOps State Recovery
- **Fleet registry backups** - Version-controlled in Git
- **ArgoCD state export** - Backup ApplicationSets and sync history
- **Vault snapshots** - Encrypted secret backups

---

## **10. Platform Operations**

### 10.1 Hub Cluster Management
- **CAPI self-hosting** - Hub manages its own lifecycle
- **ClusterClass library** - Versioned spoke cluster templates
- **Provider upgrades** - Update CAPI/CAPH/Kubeadm providers
- **Component upgrades** - ArgoCD, Vault, CloudNativePG updates

### 10.2 Spoke Cluster Upgrades
- **Kubernetes version upgrades** - Rolling upgrades via CAPI
- **Node image updates** - Ubuntu security patches
- **Zero-downtime upgrades** - Blue-green control plane upgrades

---

## **Priority Roadmap**

**Phase 1 (MVP):**
1. Tenant onboarding (manual GitHub repo creation)
2. Dedicated spoke provisioning (CAPI)
3. ArgoCD agent deployment
4. Fleet registry + ApplicationSets
5. Basic observability

**Phase 2:**
6. Automated GitHub repo templating
7. Shared spoke clusters (multi-tenant)
8. Vault integration
9. Billing/metering
10. Tenant self-service portal

**Phase 3:**
11. open-sbt SDK
12. Advanced observability (Grafana dashboards)
13. Disaster recovery
14. Multi-cloud support (AWS/GCP)

---

**Next Steps:**
Should I create a NEW spec for **"Tenant Onboarding & Spoke Provisioning"** (Journey B) with these features?
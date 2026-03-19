# Requirements Document

## Introduction

This document defines the requirements for Phase 1 MVP of the Hub-Spoke SaaS platform tenant onboarding system. The system enables multi-tenant SaaS operations using a Hub-Spoke Kubernetes architecture where the hub cluster manages all control-plane services (VictoriaMetrics, Grafana, identity services, ArgoCD, Crossplane) and each spoke cluster hosts isolated tenant workloads. The architecture follows a GitOps-first approach where all provisioning is declarative via Git commits that ArgoCD reconciles.

**Architecture Principles:**
- **Crossplane + CAPI Pattern**: Crossplane provides the declarative API layer (AINativeSaaS CRs) while CAPI handles the actual cluster provisioning underneath. This separation allows tenant-facing abstractions while leveraging battle-tested CAPI for infrastructure.
- **Secrets Management**: Self-hosted Infisical for secrets storage and Teleport for privileged access management (PAM). Credentials are stored encrypted in Infisical and injected into clusters via External Secrets Operator.
- **GitOps-First**: All infrastructure changes are committed to Git. Standard ArgoCD with ApplicationSets reconciles from Git repositories.
- **Observability**: VictoriaMetrics for centralized metrics, Grafana for dashboards, and Grafana Alloy agents on spoke clusters pushing telemetry to the hub. Tracks tenant count, cluster health, ArgoCD sync status, and resource utilization.
- **MCP-First Interaction**: All tenant operations are performed via MCP tools (no CLI or UI in Phase 1-2). The existing MCP server integrates with the hub identity stack.

Phase 1 MVP focuses on the core tenant onboarding workflow: capturing tenant metadata via MCP, provisioning dedicated spoke clusters via Crossplane + CAPI, deploying standard ArgoCD for GitOps, implementing fleet registry with ApplicationSets, and establishing basic observability with VictoriaMetrics + Grafana.

## Glossary

- **Hub_Cluster**: The central Kubernetes cluster that hosts all control-plane services (VictoriaMetrics, Grafana, ArgoCD, Crossplane, identity services, MCP server)
- **Spoke_Cluster**: A Kubernetes cluster dedicated to hosting tenant workloads, provisioned via Crossplane + CAPI
- **Tenant**: An organization or customer using the SaaS platform with dedicated or shared infrastructure
- **Fleet_Registry**: A Git repository containing tenant descriptor files that trigger ArgoCD ApplicationSets to watch tenant repositories
- **ArgoCD**: Standard ArgoCD installation (not ArgoCD Agent labs project) for GitOps reconciliation
- **ApplicationSet**: An ArgoCD resource that automatically generates Application resources based on templates and generators
- **CAPI**: Cluster API, the Kubernetes-native declarative API for cluster lifecycle management (used by Crossplane underneath)
- **Crossplane**: Declarative infrastructure orchestration layer that uses CAPI providers to provision spoke clusters
- **ClusterClass**: A CAPI template defining the configuration for provisioning clusters (e.g., hetzner-spoke-prod-v1)
- **Control_Plane_Repo**: A Git repository containing Kubernetes manifests and Helm charts for a tenant's control plane services
- **App_Plane_Repo**: A Git repository containing Kubernetes manifests and application code for a tenant's workloads
- **Tenant_Descriptor**: A YAML file in the fleet registry defining tenant metadata, repository URLs, cluster references, and configuration
- **mTLS**: Mutual TLS authentication where both client and server verify each other's certificates
- **HA_Control_Plane**: High-availability Kubernetes control plane with 3 master nodes distributed across 3 availability zones
- **Platform_Admin**: An operator or administrator managing the Hub-Spoke platform infrastructure via MCP tools
- **Tenant_Admin**: An administrator within a tenant organization managing their applications and services
- **VictoriaMetrics**: Time-series database for centralized metrics storage on the hub cluster
- **Grafana**: Visualization and dashboarding platform for observability data
- **Grafana Alloy**: Lightweight telemetry agent deployed on spoke clusters that pushes metrics to the hub
- **Infisical**: Self-hosted secrets management platform for storing encrypted cloud provider credentials
- **Teleport**: Privileged access management (PAM) platform for audited, time-limited access to clusters
- **External Secrets Operator (ESO)**: Kubernetes operator that syncs secrets from Infisical to cluster namespaces
- **MCP_Server**: Model Context Protocol server that exposes tenant management tools, integrated with hub identity stack
- **AINativeSaaS_CR**: Crossplane custom resource defining tenant environment configuration
- **ClusterResourceSet (CRS)**: CAPI feature for automatic addon installation (CNI, CCM, CSI, Grafana Alloy, Teleport agent) during cluster bootstrap

## Requirements


### Requirement 1: Tenant Metadata Capture

**User Story:** As a Platform_Admin, I want to capture tenant metadata during onboarding, so that I can track tenant configuration and provision appropriate infrastructure.

#### Acceptance Criteria

1. THE Onboarding_System SHALL capture tenant_id as a unique identifier
2. THE Onboarding_System SHALL capture org_name as the tenant organization name
3. THE Onboarding_System SHALL capture plan as one of (free, shared, dedicated)
4. THE Onboarding_System SHALL capture features as a list of enabled platform capabilities
5. THE Onboarding_System SHALL validate that tenant_id contains only lowercase alphanumeric characters and hyphens
6. THE Onboarding_System SHALL validate that tenant_id is between 3 and 63 characters in length
7. WHEN tenant metadata is captured, THE Onboarding_System SHALL store the tenant record in the database
8. WHEN a duplicate tenant_id is provided, THE Onboarding_System SHALL return an error indicating the tenant already exists

### Requirement 2: Automated Tenant Repository Creation via MCP

**User Story:** As a Platform_Admin, I want tenant Git repositories created automatically via MCP tools, so that I can establish GitOps infrastructure without manual Git operations.

**CRITICAL:** This operation is triggered via MCP tools (tenant_create), not manual CLI or UI operations. The MCP server integrates with the hub identity stack for authentication.

#### Acceptance Criteria

1. WHEN the tenant_create MCP tool is invoked, THE MCP_Server SHALL authenticate the request via the hub identity stack (Ory Hydra/Kratos/Keto)
2. THE MCP_Server SHALL invoke zero_ops_api to create a Control_Plane_Repo for the tenant in GitHub using a GitHub App Installation Token
3. THE MCP_Server SHALL invoke zero_ops_api to create an App_Plane_Repo for the tenant in GitHub using a GitHub App Installation Token
4. THE Control_Plane_Repo SHALL be initialized with a default Kustomize structure:
   - base/kustomization.yaml (base manifests)
   - overlays/starter/ (destination for Starter tier CRs)
   - overlays/enterprise/ (destination for Enterprise tier CRs)
   - README.md (tenant onboarding documentation)
5. THE App_Plane_Repo SHALL be initialized with sample application manifests and README
6. THE zero_ops_api SHALL record the Control_Plane_Repo URL in the tenant record
7. THE zero_ops_api SHALL record the App_Plane_Repo URL in the tenant record
8. IF Git repository creation fails, THE zero_ops_api SHALL persist the tenant record with status: INCOMPLETE_GIT_SETUP and return HTTP 500
9. WHEN tenant_create is retried for a tenant in INCOMPLETE_GIT_SETUP state, THE zero_ops_api SHALL idempotently retry the Git provisioning steps
10. THE GitHub App Private Key SHALL be stored in Infisical and synced to the zero_ops_api namespace via External Secrets Operator

### Requirement 3: Fleet Registry Tenant Registration

**User Story:** As a Platform_Admin, I want to register tenants in the fleet registry, so that ArgoCD ApplicationSets can discover and deploy tenant applications.

#### Acceptance Criteria

1. WHEN a tenant is onboarded, THE Onboarding_System SHALL create a Tenant_Descriptor file in the Fleet_Registry
2. THE Tenant_Descriptor SHALL include tenant_id field
3. THE Tenant_Descriptor SHALL include control_plane_repo_url field
4. THE Tenant_Descriptor SHALL include app_plane_repo_url field
5. THE Tenant_Descriptor SHALL include cluster_ref field referencing the target Spoke_Cluster
6. THE Tenant_Descriptor SHALL include plan field indicating tenant tier
7. THE Tenant_Descriptor SHALL include features field listing enabled capabilities
8. THE Tenant_Descriptor SHALL be committed to the Fleet_Registry Git repository
9. THE Tenant_Descriptor file SHALL be named using the pattern `{tenant_id}.yaml`
10. WHEN the Tenant_Descriptor is committed, THE Git_System SHALL trigger a webhook notification


### Requirement 4: Dedicated Spoke Cluster Provisioning via Crossplane

**User Story:** As a Platform_Admin, I want to provision dedicated spoke clusters using Crossplane + CAPI, so that tenants have isolated infrastructure with a declarative API layer.

**CRITICAL:** Crossplane provides the tenant-facing API (AINativeSaaS CR) while CAPI handles the actual cluster provisioning underneath. This follows the industry best practice of separating abstraction (Crossplane) from implementation (CAPI).

#### Acceptance Criteria

1. WHEN a tenant with plan=dedicated is registered, THE Provisioning_System SHALL create an AINativeSaaS_CR in the hub cluster
2. THE AINativeSaaS_CR SHALL specify tier (starter or enterprise), cloud provider (hetzner), and region
3. THE Crossplane Composition SHALL select the appropriate ClusterClass template based on tier:
   - Starter tier → hetzner-spoke-staging-v1
   - Enterprise tier → hetzner-spoke-prod-v1
4. THE Crossplane Composition SHALL create CAPI Cluster resources using the selected ClusterClass
5. THE ClusterClass SHALL provision an HA_Control_Plane with 3 master nodes distributed across 3 availability zones (Hetzner regions: fsn1, nbg1, hel1)
6. THE ClusterClass SHALL provision worker node pools with auto-scaling enabled (2-10 nodes for prod, 1-5 for staging)
7. THE Crossplane Composition SHALL create an ExternalSecret resource that fetches Hetzner API credentials from Infisical path: /tenants/{tenant_id}/credentials/hetzner
8. THE External Secrets Operator SHALL sync the credentials from Infisical to a Kubernetes Secret in the spoke cluster namespace
9. THE CAPI provider SHALL use the synced credentials to provision Hetzner infrastructure (VMs, networks, load balancers)
10. THE Crossplane Composition SHALL assign a unique cluster name using the pattern `spoke-{tenant_id}-{environment_suffix}`
11. WHEN the CAPI Cluster resource reaches Ready state, THE Crossplane SHALL update the AINativeSaaS_CR status to Ready: True
12. WHEN cluster provisioning encounters errors, THE Crossplane SHALL update the AINativeSaaS_CR status to Degraded with error details
13. THE Crossplane SHALL continuously reconcile and retry provisioning (no terminal failure states)
14. WHEN the cluster reaches Ready state, THE Provisioning_System SHALL update the tenant status to ClusterReady

### Requirement 5: Spoke Cluster Health Monitoring

**User Story:** As a Platform_Admin, I want to monitor spoke cluster health, so that I can detect and respond to infrastructure failures.

#### Acceptance Criteria

1. THE Monitoring_System SHALL track the status of each Spoke_Cluster as one of (Provisioning, Ready, Degraded, Failed)
2. WHEN a Spoke_Cluster status changes, THE Monitoring_System SHALL update the cluster health metric
3. THE Monitoring_System SHALL monitor control plane node health for each Spoke_Cluster
4. THE Monitoring_System SHALL monitor worker node health for each Spoke_Cluster
5. WHEN a control plane node becomes NotReady, THE Monitoring_System SHALL set cluster status to Degraded
6. WHEN all control plane nodes are Ready, THE Monitoring_System SHALL set cluster status to Ready
7. THE Monitoring_System SHALL expose cluster health metrics to VictoriaMetrics on the Hub_Cluster
8. THE Monitoring_System SHALL check cluster health at intervals not exceeding 60 seconds

### Requirement 6: ArgoCD Installation on Spoke Clusters

**User Story:** As a Platform_Admin, I want ArgoCD installed on spoke clusters, so that applications can be synchronized from tenant Git repositories.

**CRITICAL:** This uses standard ArgoCD (not ArgoCD Agent labs project). ArgoCD is installed via ClusterResourceSet during CAPI cluster bootstrap.

#### Acceptance Criteria

1. WHEN a Spoke_Cluster reaches Ready state, THE ClusterResourceSet SHALL automatically install ArgoCD via Helm chart
2. THE ArgoCD installation SHALL be deployed in the argocd namespace on the spoke cluster
3. THE ArgoCD installation SHALL be configured to watch the tenant's Control_Plane_Repo and App_Plane_Repo
4. THE ArgoCD installation SHALL use ApplicationSets for auto-discovery of applications from the tenant repositories
5. THE ArgoCD installation SHALL authenticate to Git repositories using GitHub App Installation Tokens (synced from Infisical via External Secrets Operator)
6. WHEN ArgoCD is deployed, THE Provisioning_System SHALL verify ArgoCD server is healthy within 120 seconds
7. WHEN ArgoCD health check fails, THE Provisioning_System SHALL retry verification up to 3 times
8. WHEN ArgoCD is verified healthy, THE Provisioning_System SHALL update the tenant status to ArgoCDReady


### Requirement 7: ArgoCD Health Monitoring

**User Story:** As a Platform_Admin, I want to monitor ArgoCD health on spoke clusters, so that I can detect and troubleshoot synchronization issues.

#### Acceptance Criteria

1. THE Monitoring_System SHALL track ArgoCD health status as one of (Healthy, Progressing, Degraded, Suspended)
2. THE Monitoring_System SHALL scrape ArgoCD metrics from the argocd-metrics service on spoke clusters
3. THE Grafana Alloy agent on spoke clusters SHALL push ArgoCD metrics to VictoriaMetrics on the hub
4. WHEN ArgoCD server pod is not Running, THE Monitoring_System SHALL set status to Degraded
5. WHEN ArgoCD server is Running and healthy, THE Monitoring_System SHALL set status to Healthy
6. THE Monitoring_System SHALL expose ArgoCD health metrics to VictoriaMetrics on the Hub_Cluster
7. WHEN ArgoCD status changes to Degraded, THE Monitoring_System SHALL emit an alert
8. THE Monitoring_System SHALL record the last successful health check timestamp for each spoke ArgoCD instance

### Requirement 8: Fleet Registry ApplicationSet Implementation

**User Story:** As a Platform_Admin, I want ApplicationSets to watch the fleet registry, so that tenant applications are automatically discovered and deployed.

#### Acceptance Criteria

1. THE ApplicationSet_System SHALL create an ApplicationSet resource on the Hub_Cluster
2. THE ApplicationSet SHALL use a Git generator to watch the Fleet_Registry repository
3. THE ApplicationSet SHALL discover Tenant_Descriptor files matching the pattern `*.yaml`
4. WHEN a new Tenant_Descriptor is committed, THE ApplicationSet SHALL generate an Application resource within 60 seconds
5. THE Application resource SHALL reference the tenant's Control_Plane_Repo
6. THE Application resource SHALL target the tenant's assigned Spoke_Cluster
7. THE Application resource SHALL be configured with auto-sync enabled
8. WHEN a Tenant_Descriptor is removed, THE ApplicationSet SHALL delete the corresponding Application resource within 60 seconds
9. WHEN a Tenant_Descriptor is modified, THE ApplicationSet SHALL update the corresponding Application resource within 60 seconds

### Requirement 9: Control Plane Application Deployment

**User Story:** As a Tenant_Admin, I want my control plane services deployed to the spoke cluster, so that I can run tenant-specific infrastructure.

#### Acceptance Criteria

1. WHEN an Application resource is created by ApplicationSet, THE ArgoCD on the spoke cluster SHALL synchronize manifests from the Control_Plane_Repo
2. THE ArgoCD SHALL apply Kubernetes manifests to the Spoke_Cluster
3. THE ArgoCD SHALL render Helm charts with tenant-specific values
4. WHEN synchronization completes successfully, THE ArgoCD SHALL report sync status as Synced
5. WHEN synchronization fails, THE ArgoCD SHALL report sync status as OutOfSync
6. THE ArgoCD SHALL report application health as one of (Healthy, Progressing, Degraded, Suspended, Missing, Unknown)
7. WHEN all application resources are healthy, THE ArgoCD SHALL report health status as Healthy
8. THE ArgoCD SHALL retry failed synchronizations with exponential backoff up to 300 seconds


### Requirement 10: Application Plane Deployment

**User Story:** As a Tenant_Admin, I want my application workloads deployed to the spoke cluster, so that I can serve my end users.

#### Acceptance Criteria

1. THE ApplicationSet on the spoke cluster SHALL watch the tenant's App_Plane_Repo for application manifests
2. WHEN new applications are detected in the App_Plane_Repo, THE ApplicationSet SHALL generate Application resources
3. THE Application resources SHALL reference the tenant's App_Plane_Repo
4. THE Application resources SHALL target namespaces on the Spoke_Cluster
5. THE ArgoCD SHALL synchronize manifests from the App_Plane_Repo
6. THE ArgoCD SHALL apply application workload manifests to the Spoke_Cluster
7. WHEN synchronization completes successfully, THE ArgoCD SHALL report sync status as Synced
8. WHEN all application resources are healthy, THE ArgoCD SHALL report health status as Healthy

### Requirement 11: ArgoCD Sync Status Tracking

**User Story:** As a Platform_Admin, I want to track ArgoCD sync status for all tenants, so that I can monitor deployment health across the platform.

#### Acceptance Criteria

1. THE Monitoring_System SHALL collect sync status from all Application resources
2. THE Monitoring_System SHALL track sync status as one of (Synced, OutOfSync, Unknown)
3. THE Monitoring_System SHALL expose sync status metrics to VictoriaMetrics on the Hub_Cluster
4. THE Monitoring_System SHALL aggregate sync status by tenant
5. THE Monitoring_System SHALL aggregate sync status by cluster
6. WHEN an Application sync status changes to OutOfSync, THE Monitoring_System SHALL emit an alert
7. THE Monitoring_System SHALL record the last successful sync timestamp for each Application
8. THE Monitoring_System SHALL update sync status metrics at intervals not exceeding 60 seconds

### Requirement 12: Tenant Count Metrics

**User Story:** As a Platform_Admin, I want to track the number of active tenants, so that I can monitor platform growth and capacity.

#### Acceptance Criteria

1. THE Monitoring_System SHALL count the total number of tenants
2. THE Monitoring_System SHALL count tenants by plan (free, shared, dedicated)
3. THE Monitoring_System SHALL count tenants by status (Provisioning, Ready, Degraded, Failed)
4. THE Monitoring_System SHALL expose tenant count metrics to VictoriaMetrics on the Hub_Cluster
5. THE Monitoring_System SHALL update tenant count metrics at intervals not exceeding 300 seconds
6. THE Monitoring_System SHALL include tenant count in platform health dashboards


### Requirement 13: Resource Utilization Metrics

**User Story:** As a Platform_Admin, I want to track resource utilization across spoke clusters, so that I can optimize capacity and identify resource constraints.

#### Acceptance Criteria

1. THE Monitoring_System SHALL collect CPU utilization metrics from each Spoke_Cluster
2. THE Monitoring_System SHALL collect memory utilization metrics from each Spoke_Cluster
3. THE Monitoring_System SHALL collect storage utilization metrics from each Spoke_Cluster
4. THE Monitoring_System SHALL aggregate resource utilization by tenant
5. THE Monitoring_System SHALL aggregate resource utilization by cluster
6. THE Monitoring_System SHALL expose resource utilization metrics to VictoriaMetrics on the Hub_Cluster
7. THE Monitoring_System SHALL update resource utilization metrics at intervals not exceeding 60 seconds
8. WHEN CPU utilization exceeds 80 percent for more than 300 seconds, THE Monitoring_System SHALL emit an alert
9. WHEN memory utilization exceeds 80 percent for more than 300 seconds, THE Monitoring_System SHALL emit an alert
10. WHEN storage utilization exceeds 80 percent, THE Monitoring_System SHALL emit an alert

### Requirement 14: Secrets Management with Infisical

**User Story:** As a Platform_Admin, I want cloud provider credentials stored securely in Infisical, so that secrets are encrypted at rest and access is audited.

**CRITICAL:** Infisical replaces traditional GitOps secret management (KSOPS/Age, Sealed Secrets) with a centralized secrets platform. External Secrets Operator syncs secrets from Infisical to clusters.

#### Acceptance Criteria

1. THE hub cluster SHALL have Infisical server installed in the secrets-management namespace
2. THE Infisical installation SHALL use CloudNativePG for its PostgreSQL backend
3. THE hub cluster SHALL have External Secrets Operator (ESO) installed in the secrets-management namespace
4. WHEN a tenant is created, THE zero_ops_api SHALL create an Infisical project at path: /tenants/{tenant_id}/
5. WHEN cloud provider credentials are submitted, THE zero_ops_api SHALL store them in Infisical at path: /tenants/{tenant_id}/credentials/hetzner with AES-256-GCM encryption
6. WHEN a spoke cluster is provisioned, THE Crossplane Composition SHALL create an ExternalSecret resource that references the Infisical SecretStore
7. THE ExternalSecret SHALL fetch credentials from /tenants/{tenant_id}/credentials/hetzner and create a Kubernetes Secret in the spoke cluster namespace
8. THE ExternalSecret SHALL refresh credentials every 5 minutes to detect rotation
9. THE Infisical SHALL maintain an audit log of all secret access operations (timestamp, identity, secret path, operation)
10. THE Infisical audit logs SHALL be exported to the hub observability stack for security monitoring

### Requirement 15: Tenant Status Lifecycle

**User Story:** As a Platform_Admin, I want to track tenant status through the onboarding lifecycle, so that I can monitor provisioning progress and troubleshoot failures.

#### Acceptance Criteria

1. WHEN a tenant is created, THE Onboarding_System SHALL set tenant status to Pending
2. WHEN cluster provisioning begins, THE Onboarding_System SHALL set tenant status to Provisioning
3. WHEN the Spoke_Cluster reaches Ready state, THE Onboarding_System SHALL set tenant status to ClusterReady
4. WHEN the ArgoCD_Agent is deployed and connected, THE Onboarding_System SHALL set tenant status to AgentReady
5. WHEN control plane applications are synced and healthy, THE Onboarding_System SHALL set tenant status to ControlPlaneReady
6. WHEN app plane applications are synced and healthy, THE Onboarding_System SHALL set tenant status to Ready
7. WHEN any provisioning step fails, THE Onboarding_System SHALL set tenant status to Failed
8. THE Onboarding_System SHALL record the timestamp of each status transition
9. THE Onboarding_System SHALL expose tenant status as a metric in VictoriaMetrics


### Requirement 16: GitOps Declarative Provisioning

**User Story:** As a Platform_Admin, I want all provisioning to be declarative via Git commits, so that infrastructure changes are auditable and reversible.

#### Acceptance Criteria

1. THE Onboarding_System SHALL NOT execute imperative kubectl commands for cluster provisioning
2. THE Onboarding_System SHALL NOT execute imperative kubectl commands for agent deployment
3. WHEN a tenant is onboarded, THE Onboarding_System SHALL commit a CAPI Cluster resource to the infrastructure Git repository
4. WHEN a tenant is onboarded, THE Onboarding_System SHALL commit an ArgoCD Application resource to the infrastructure Git repository
5. THE ArgoCD_Controller on the Hub_Cluster SHALL reconcile Git commits to the cluster state
6. WHEN a Git commit is reverted, THE ArgoCD_Controller SHALL revert the corresponding cluster resources
7. THE Onboarding_System SHALL record the Git commit SHA for each provisioning action
8. THE Onboarding_System SHALL expose Git commit references in tenant metadata

### Requirement 17: ClusterClass Template Selection

**User Story:** As a Platform_Admin, I want to select appropriate ClusterClass templates for spoke clusters, so that clusters are provisioned with correct configurations for their environment.

#### Acceptance Criteria

1. THE Provisioning_System SHALL support ClusterClass hetzner-prod-ubuntu-v1 for production environments
2. THE Provisioning_System SHALL support ClusterClass hetzner-staging-ubuntu-v1 for staging environments
3. WHEN a tenant is created with environment=production, THE Provisioning_System SHALL use hetzner-prod-ubuntu-v1
4. WHEN a tenant is created with environment=staging, THE Provisioning_System SHALL use hetzner-staging-ubuntu-v1
5. THE ClusterClass hetzner-prod-ubuntu-v1 SHALL define 3 control plane nodes
6. THE ClusterClass hetzner-prod-ubuntu-v1 SHALL define worker node auto-scaling with minimum 2 nodes
7. THE ClusterClass hetzner-staging-ubuntu-v1 SHALL define 3 control plane nodes
8. THE ClusterClass hetzner-staging-ubuntu-v1 SHALL define worker node auto-scaling with minimum 1 node

### Requirement 18: Tenant Descriptor Schema Validation

**User Story:** As a Platform_Admin, I want tenant descriptors validated against a schema, so that invalid configurations are rejected before deployment.

#### Acceptance Criteria

1. THE Fleet_Registry SHALL define a JSON schema for Tenant_Descriptor files
2. THE schema SHALL require tenant_id field as a string
3. THE schema SHALL require control_plane_repo_url field as a valid URL
4. THE schema SHALL require app_plane_repo_url field as a valid URL
5. THE schema SHALL require cluster_ref field as a string
6. THE schema SHALL require plan field as one of (free, shared, dedicated)
7. THE schema SHALL require features field as an array of strings
8. WHEN a Tenant_Descriptor is committed, THE Validation_System SHALL validate it against the schema
9. WHEN validation fails, THE Validation_System SHALL reject the commit with a descriptive error message
10. WHEN validation succeeds, THE Validation_System SHALL allow the commit to proceed


### Requirement 19: Platform Health Dashboard

**User Story:** As a Platform_Admin, I want a dashboard showing platform health, so that I can monitor the overall system status at a glance.

#### Acceptance Criteria

1. THE Dashboard_System SHALL display total tenant count
2. THE Dashboard_System SHALL display tenant count by plan (free, shared, dedicated)
3. THE Dashboard_System SHALL display tenant count by status (Provisioning, Ready, Degraded, Failed)
4. THE Dashboard_System SHALL display total spoke cluster count
5. THE Dashboard_System SHALL display spoke cluster count by status (Provisioning, Ready, Degraded, Failed)
6. THE Dashboard_System SHALL display ArgoCD agent connectivity status for all spokes
7. THE Dashboard_System SHALL display ArgoCD sync status for all applications
8. THE Dashboard_System SHALL display aggregate resource utilization across all spokes
9. THE Dashboard_System SHALL refresh metrics at intervals not exceeding 60 seconds
10. THE Dashboard_System SHALL be accessible via Grafana on the Hub_Cluster

### Requirement 20: Error Handling and Retry Logic

**User Story:** As a Platform_Admin, I want automatic retry logic for transient failures, so that temporary issues do not require manual intervention.

#### Acceptance Criteria

1. WHEN cluster provisioning fails with a transient error, THE Provisioning_System SHALL retry up to 3 times
2. WHEN agent deployment fails with a transient error, THE Agent_Deployment_System SHALL retry up to 3 times
3. WHEN application synchronization fails with a transient error, THE ArgoCD_Agent SHALL retry with exponential backoff
4. THE retry backoff SHALL start at 10 seconds and double with each retry up to 300 seconds
5. WHEN all retries are exhausted, THE system SHALL set the tenant status to Failed
6. WHEN a retry succeeds, THE system SHALL continue to the next provisioning step
7. THE system SHALL log each retry attempt with timestamp and error details
8. WHEN a permanent error is detected, THE system SHALL NOT retry and SHALL immediately set status to Failed

### Requirement 21: Tenant Onboarding Idempotency

**User Story:** As a Platform_Admin, I want tenant onboarding operations to be idempotent, so that retrying operations does not create duplicate resources.

#### Acceptance Criteria

1. WHEN a tenant with an existing tenant_id is onboarded, THE Onboarding_System SHALL return an error
2. WHEN a Tenant_Descriptor with an existing tenant_id is committed, THE Fleet_Registry SHALL reject the commit
3. WHEN a CAPI Cluster resource with an existing name is created, THE CAPI_Controller SHALL return an error
4. WHEN an ArgoCD Application resource with an existing name is created, THE ArgoCD_Controller SHALL update the existing resource
5. THE Onboarding_System SHALL check for existing resources before creating new ones
6. WHEN a provisioning operation is retried, THE system SHALL NOT create duplicate resources
7. THE system SHALL use unique identifiers derived from tenant_id for all resources


### Requirement 22: Audit Logging

**User Story:** As a Platform_Admin, I want all tenant onboarding actions logged, so that I can audit system operations and troubleshoot issues.

#### Acceptance Criteria

1. WHEN a tenant is created, THE Onboarding_System SHALL log the action with tenant_id and timestamp
2. WHEN a Tenant_Descriptor is committed, THE Fleet_Registry SHALL log the commit SHA and timestamp
3. WHEN a Spoke_Cluster is provisioned, THE Provisioning_System SHALL log the cluster name and timestamp
4. WHEN an ArgoCD_Agent is deployed, THE Agent_Deployment_System SHALL log the agent name and timestamp
5. WHEN a tenant status changes, THE Onboarding_System SHALL log the old status, new status, and timestamp
6. WHEN an error occurs, THE system SHALL log the error message, stack trace, and timestamp
7. THE system SHALL store audit logs in a centralized logging system on the Hub_Cluster
8. THE audit logs SHALL be retained for at least 90 days
9. THE audit logs SHALL include the Platform_Admin identity for manual operations

### Requirement 23: Spoke Cluster Network Isolation

**User Story:** As a Platform_Admin, I want spoke clusters to have isolated networks, so that tenant workloads cannot interfere with each other.

#### Acceptance Criteria

1. WHEN a Spoke_Cluster is provisioned, THE Provisioning_System SHALL create a dedicated private network
2. THE private network SHALL be isolated from other tenant networks
3. THE Spoke_Cluster SHALL use the private network for all node communication
4. THE Spoke_Cluster SHALL expose a public load balancer endpoint for external access
5. THE load balancer SHALL be dedicated to the tenant
6. THE Provisioning_System SHALL configure network policies to prevent cross-tenant traffic
7. THE Provisioning_System SHALL configure firewall rules to allow only necessary traffic

### Requirement 24: Hub-to-Spoke Communication Security

**User Story:** As a Platform_Admin, I want secure communication between hub and spoke clusters, so that tenant data and credentials are protected.

#### Acceptance Criteria

1. THE ArgoCD_Agent SHALL use mTLS for all connections to the Hub_Cluster
2. THE ArgoCD_Agent SHALL verify the Hub_Cluster certificate authority
3. THE Hub_Cluster ArgoCD server SHALL verify the ArgoCD_Agent certificate
4. THE system SHALL NOT allow unencrypted connections between hub and spoke
5. THE system SHALL NOT allow connections with invalid or expired certificates
6. WHEN a certificate verification fails, THE ArgoCD_Agent SHALL log the error and retry
7. THE system SHALL use TLS version 1.2 or higher for all connections


### Requirement 25: Metrics Collection from Spoke Clusters

**User Story:** As a Platform_Admin, I want metrics collected from spoke clusters and sent to the hub, so that I can monitor tenant resource usage centrally.

#### Acceptance Criteria

1. THE Monitoring_System SHALL deploy a metrics collection agent on each Spoke_Cluster
2. THE metrics collection agent SHALL collect CPU, memory, and storage metrics
3. THE metrics collection agent SHALL push metrics to VictoriaMetrics on the Hub_Cluster
4. THE metrics collection agent SHALL push metrics at intervals not exceeding 60 seconds
5. THE metrics collection agent SHALL buffer metrics when the Hub_Cluster is unreachable
6. WHEN the Hub_Cluster becomes reachable, THE metrics collection agent SHALL flush buffered metrics
7. THE metrics collection agent SHALL use mTLS for connections to the Hub_Cluster
8. THE metrics SHALL include tenant_id and cluster_ref labels for aggregation

### Requirement 26: ApplicationSet Auto-Discovery

**User Story:** As a Platform_Admin, I want ApplicationSets to automatically discover new tenants, so that I do not need to manually create Application resources.

#### Acceptance Criteria

1. THE ApplicationSet SHALL use a Git generator to watch the Fleet_Registry repository
2. THE ApplicationSet SHALL poll the Fleet_Registry at intervals not exceeding 180 seconds
3. WHEN a new Tenant_Descriptor file is detected, THE ApplicationSet SHALL generate an Application resource
4. THE Application resource SHALL be created within 60 seconds of the Tenant_Descriptor commit
5. THE Application resource name SHALL be derived from the tenant_id
6. THE Application resource SHALL use the control_plane_repo_url from the Tenant_Descriptor
7. THE Application resource SHALL target the cluster_ref from the Tenant_Descriptor
8. WHEN the Tenant_Descriptor is deleted, THE ApplicationSet SHALL delete the Application resource within 60 seconds

### Requirement 27: Cluster Readiness Verification

**User Story:** As a Platform_Admin, I want to verify cluster readiness before deploying applications, so that deployments do not fail due to incomplete infrastructure.

#### Acceptance Criteria

1. WHEN a Spoke_Cluster is provisioned, THE Provisioning_System SHALL wait for the cluster to reach Ready state
2. THE Provisioning_System SHALL verify that all control plane nodes are Ready
3. THE Provisioning_System SHALL verify that at least one worker node is Ready
4. THE Provisioning_System SHALL verify that the Kubernetes API server is accessible
5. THE Provisioning_System SHALL verify that core system pods are running (kube-proxy, CNI, CoreDNS)
6. WHEN all readiness checks pass, THE Provisioning_System SHALL proceed with agent deployment
7. WHEN any readiness check fails after 600 seconds, THE Provisioning_System SHALL set cluster status to Failed
8. THE Provisioning_System SHALL log each readiness check result with timestamp


### Requirement 28: Application Health Reporting

**User Story:** As a Platform_Admin, I want application health reported from spoke clusters, so that I can detect and respond to application failures.

#### Acceptance Criteria

1. THE ArgoCD_Agent SHALL report application health status to the Hub_Cluster
2. THE application health status SHALL be one of (Healthy, Progressing, Degraded, Suspended, Missing, Unknown)
3. THE ArgoCD_Agent SHALL determine health by evaluating Kubernetes resource status
4. WHEN all pods are Running and ready, THE ArgoCD_Agent SHALL report health as Healthy
5. WHEN pods are being created or updated, THE ArgoCD_Agent SHALL report health as Progressing
6. WHEN pods are CrashLooping or failing, THE ArgoCD_Agent SHALL report health as Degraded
7. WHEN resources are missing, THE ArgoCD_Agent SHALL report health as Missing
8. THE ArgoCD_Agent SHALL update health status at intervals not exceeding 60 seconds
9. THE Hub_Cluster SHALL expose application health metrics in VictoriaMetrics

### Requirement 29: Tenant Metadata Persistence

**User Story:** As a Platform_Admin, I want tenant metadata persisted in a database, so that tenant information survives system restarts.

#### Acceptance Criteria

1. THE Onboarding_System SHALL store tenant metadata in a PostgreSQL database on the Hub_Cluster
2. THE database SHALL store tenant_id as the primary key
3. THE database SHALL store org_name, plan, features, control_plane_repo_url, app_plane_repo_url, cluster_ref
4. THE database SHALL store tenant status and status transition timestamps
5. THE database SHALL store created_at and updated_at timestamps
6. WHEN a tenant is created, THE Onboarding_System SHALL insert a record in the database
7. WHEN tenant metadata is updated, THE Onboarding_System SHALL update the database record
8. THE database SHALL enforce unique constraints on tenant_id
9. THE database SHALL be backed up daily with 30-day retention

### Requirement 30: Configuration Validation

**User Story:** As a Platform_Admin, I want configuration validated before provisioning, so that invalid configurations are detected early.

#### Acceptance Criteria

1. WHEN a tenant is onboarded, THE Onboarding_System SHALL validate that control_plane_repo_url is accessible
2. WHEN a tenant is onboarded, THE Onboarding_System SHALL validate that app_plane_repo_url is accessible
3. WHEN a tenant is onboarded, THE Onboarding_System SHALL validate that the ClusterClass exists
4. WHEN a tenant is onboarded, THE Onboarding_System SHALL validate that the plan is one of (free, shared, dedicated)
5. WHEN validation fails, THE Onboarding_System SHALL return a descriptive error message
6. WHEN validation succeeds, THE Onboarding_System SHALL proceed with provisioning
7. THE Onboarding_System SHALL validate tenant_id format before creating resources
8. THE Onboarding_System SHALL validate that features list contains only supported feature names

# Requirements Document

## Introduction

This feature implements the deployment of Infisical (primary secret storage) and External Secrets Operator (ESO) following GitOps workflow to resolve ArgoCD authentication errors that are blocking the observability pipeline. The architecture follows the recommended pattern from the ArgoCD team: ArgoCD deploys secret REFERENCES (ExternalSecret CRDs), ESO fetches secrets from Infisical and creates Kubernetes secrets, and applications consume secrets from files with auto-reload capability.

## Glossary

- **Infisical**: Primary secret storage system for the platform
- **External_Secrets_Operator (ESO)**: Kubernetes operator that fetches secrets from external secret stores and creates native Kubernetes secrets
- **ClusterSecretStore**: ESO custom resource that defines connection to Infisical backend (cluster-scoped)
- **ExternalSecret**: ESO custom resource that references secrets in Infisical and creates corresponding Kubernetes secrets
- **ArgoCD**: GitOps continuous deployment tool
- **Hub_Cluster**: Management cluster named "mothership" running control plane services
- **GitOps_Workflow**: All infrastructure changes via Git commits, ArgoCD reconciles
- **Sync_Wave**: ArgoCD annotation controlling deployment order (lower numbers deploy first)
- **Secret_Reference**: ExternalSecret CRD manifest stored in Git (not actual secret values)
- **VictoriaMetrics**: Metrics storage and querying system
- **Grafana_Alloy**: Metrics collection and forwarding agent
- **Prometheus_Operator**: Kubernetes operator for Prometheus monitoring stack

## Requirements

### Requirement 1: Deploy Infisical as Primary Secret Storage

**User Story:** As a platform operator, I want Infisical deployed to the Hub cluster, so that I have a centralized secret management system for all platform secrets.

#### Acceptance Criteria

1. THE Infisical_Deployment SHALL use the Helm chart from archived/references/identity-auth/infisical/helm-charts/infisical-standalone-postgres
2. THE Infisical_Deployment SHALL be deployed to the zero-ops-system namespace
3. THE Infisical_Deployment SHALL use CloudNativePG for PostgreSQL backend storage
4. THE Infisical_ArgoCD_Application SHALL have sync-wave annotation "3" (after platform-database at wave 2)
5. THE Infisical_Deployment SHALL expose an HTTPS endpoint for API access
6. THE Infisical_Deployment SHALL persist data using PersistentVolumeClaims
7. WHEN Infisical pods restart, THE Infisical_Deployment SHALL retain all stored secrets

### Requirement 2: Deploy External Secrets Operator

**User Story:** As a platform operator, I want External Secrets Operator deployed to the Hub cluster, so that Kubernetes secrets can be automatically synchronized from Infisical.

#### Acceptance Criteria

1. THE ESO_Deployment SHALL use the Helm chart from archived/references/identity-auth/external-secrets/deploy/charts/external-secrets
2. THE ESO_Deployment SHALL be deployed to the external-secrets-system namespace
3. THE ESO_ArgoCD_Application SHALL have sync-wave annotation "4" (after Infisical at wave 3)
4. THE ESO_Deployment SHALL install CustomResourceDefinitions for ExternalSecret, SecretStore, and ClusterSecretStore
5. THE ESO_Deployment SHALL run with minimal resource requests (cpu: 10m, memory: 64Mi)
6. THE ESO_Deployment SHALL have automated sync policy with prune and selfHeal enabled
7. WHEN ESO detects changes in Infisical, THE ESO_Controller SHALL update corresponding Kubernetes secrets within 60 seconds

### Requirement 3: Create ClusterSecretStore for Infisical Backend

**User Story:** As a platform operator, I want a ClusterSecretStore configured for Infisical, so that ExternalSecret resources can fetch secrets from Infisical across all namespaces.

#### Acceptance Criteria

1. THE ClusterSecretStore_Resource SHALL be named "infisical-backend"
2. THE ClusterSecretStore_Resource SHALL reference Infisical API endpoint
3. THE ClusterSecretStore_Resource SHALL use a Kubernetes secret containing Infisical API credentials
4. THE ClusterSecretStore_ArgoCD_Application SHALL have sync-wave annotation "5" (after ESO at wave 4)
5. THE ClusterSecretStore_Resource SHALL be cluster-scoped (accessible from all namespaces)
6. WHEN an ExternalSecret references "infisical-backend", THE ESO_Controller SHALL successfully authenticate to Infisical
7. IF Infisical API credentials are invalid, THEN THE ClusterSecretStore_Status SHALL report authentication failure

### Requirement 4: Fix ArgoCD GitHub Authentication

**User Story:** As a platform operator, I want ArgoCD GitHub authentication configured correctly, so that ArgoCD applications can sync manifests from the GitHub repository without authentication errors.

#### Acceptance Criteria

1. THE ArgoCD_GitHub_Credentials SHALL be stored in Infisical
2. THE ArgoCD_GitHub_ExternalSecret SHALL fetch credentials from Infisical and create a Kubernetes secret
3. THE ArgoCD_GitHub_ExternalSecret SHALL create a Kubernetes Secret containing the `argocd.argoproj.io/secret-type: repository` label so ArgoCD auto-discovers it
4. THE ArgoCD_GitHub_ExternalSecret SHALL have sync-wave annotation "6" (after ClusterSecretStore at wave 5)
5. WHEN ArgoCD syncs applications, THE ArgoCD_Controller SHALL successfully authenticate to GitHub
6. WHEN GitHub credentials are rotated in Infisical, THE ArgoCD_GitHub_Secret SHALL be updated within 60 seconds
7. THE Prometheus_Operator_Application SHALL sync successfully after GitHub authentication is fixed
8. THE Grafana_Alloy_Application SHALL sync successfully after GitHub authentication is fixed
9. THE VictoriaMetrics_Ingress_Application SHALL sync successfully after GitHub authentication is fixed

### Requirement 5: Create ArgoCD Applications for All Components

**User Story:** As a platform operator, I want ArgoCD Applications defined for all secret management components, so that the entire secret management stack is deployed via GitOps workflow.

#### Acceptance Criteria

1. THE Platform_Operator SHALL create ArgoCD Application manifests in manifests/argocd/apps/ directory
2. THE ArgoCD_Applications SHALL include: platform-infisical.yaml, platform-external-secrets.yaml, platform-cluster-secret-store.yaml, platform-argocd-github-auth.yaml
3. THE ArgoCD_Applications SHALL reference manifests in the zero-ops GitHub repository
4. THE ArgoCD_Applications SHALL use sync-wave annotations to enforce dependency ordering
5. THE ArgoCD_Applications SHALL have automated sync policy with prune enabled
6. THE ArgoCD_Applications SHALL have retry policy with exponential backoff (duration: 30s, maxDuration: 10m, limit: 15)
7. WHEN a manifest is committed to the main branch, THE ArgoCD_Controller SHALL detect and sync the change within 3 minutes

### Requirement 6: Validation Script for Secret Flow

**User Story:** As a platform operator, I want a validation script that verifies the secret flow works end-to-end, so that I can confirm Infisical → ESO → Kubernetes secrets → Application consumption is functioning correctly.

#### Acceptance Criteria

1. THE Validation_Script SHALL create a test secret in Infisical via API
2. THE Validation_Script SHALL create an ExternalSecret resource referencing the test secret
3. THE Validation_Script SHALL verify the Kubernetes secret is created within 60 seconds
4. THE Validation_Script SHALL verify the secret value matches the value stored in Infisical
5. THE Validation_Script SHALL deploy a test pod that mounts the secret as a file
6. THE Validation_Script SHALL verify the test pod can read the secret file
7. THE Validation_Script SHALL clean up all test resources after validation
8. IF any validation step fails, THEN THE Validation_Script SHALL exit with non-zero status and descriptive error message

### Requirement 7: Follow Approved Architecture Pattern

**User Story:** As a platform operator, I want the secret management implementation to follow the approved architecture pattern, so that the system is maintainable and aligns with ArgoCD team recommendations.

#### Acceptance Criteria

1. THE Secret_Management_Architecture SHALL follow the pattern: Infisical → ESO → Kubernetes_Secrets → Applications
2. THE ArgoCD_Manifests SHALL contain only ExternalSecret CRDs (secret references), not actual secret values
3. THE Applications SHALL consume secrets from mounted files, not environment variables
4. THE Applications SHALL implement file-based secret auto-reload mechanisms
5. THE GitOps_Repository SHALL never contain plaintext secret values
6. THE Platform_Operator SHALL never use kubectl apply for secret management components
7. WHEN secrets are rotated in Infisical, THE Applications SHALL reload secrets without pod restarts

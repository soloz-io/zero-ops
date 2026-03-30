# Implementation Plan: Infisical and External Secrets Deployment

## Overview

This implementation follows GitOps-first principles with ArgoCD sync-wave ordering to deploy Infisical (secret storage), External Secrets Operator (secret synchronization), and ArgoCD GitHub authentication fix. All changes are committed to Git and synced via ArgoCD - no kubectl apply commands. The validation script provides end-to-end testing of the secret flow.

## Tasks

- [x] 0. Break-glass bootstrap (one-time imperative)
  - [x] 0.1 Add InstallInfisicalAuth method to installer.go
    - Method signature: `InstallInfisicalAuth(ctx context.Context, clientID, clientSecret string) error`
    - Create `infisical-auth` secret in `external-secrets-system` namespace
    - Secret contains `client-id` and `client-secret` keys
    - Use client-go kubernetes clientset (PRODUCTION READY)
    - _Requirements: 3.3, 3.6_

  - [x] 0.2 Add FixArgoCDGitHubAuth method to installer.go
    - Method signature: `FixArgoCDGitHubAuth(ctx context.Context, githubToken string) error`
    - Create `repo-soloz-io-zero-ops` secret in `argocd` namespace
    - Add label: `argocd.argoproj.io/secret-type: repository`
    - Include fields: `type: git`, `url`, `username`, `password`
    - Use client-go kubernetes clientset (PRODUCTION READY)
    - _Requirements: 4.1, 4.3, 4.5_

  - [x] 0.3 Document bootstrap workflow
    - Add comments explaining chicken-and-egg problem
    - Document that bootstrap secrets enable GitOps flow
    - Note that ESO takes over after initial sync
    - _Requirements: 7.1, 7.2_

  - [x] 0.4 Add InstallInfisicalSecrets method to installer.go
    - Method signature: `InstallInfisicalSecrets(ctx context.Context) error`
    - Generate ENCRYPTION_KEY (32-byte hex) using crypto/rand
    - Generate AUTH_SECRET (32-byte hex) using crypto/rand
    - Create `infisical-secrets` in `zero-ops-system` namespace
    - Use client-go kubernetes clientset (PRODUCTION READY)
    - _Requirements: 1.2, 7.1_

  - [x] 0.5 Add InstallPostgresConnectionSecret method to installer.go
    - Method signature: `InstallPostgresConnectionSecret(ctx context.Context) error`
    - Generate secure random password using crypto/rand
    - Build PostgreSQL connection string for CNPG
    - Create `infisical-postgres-connection` in `zero-ops-system` namespace
    - Use client-go kubernetes clientset (PRODUCTION READY)
    - _Requirements: 1.3, 7.1_

  - [x] 0.6 Create configure-eso CLI command
    - Create `cmd/hub/configure_eso.go`
    - Accept flags: `--infisical-client-id`, `--infisical-client-secret`, `--github-token`, `--kubeconfig`
    - Call InstallInfisicalAuth() and FixArgoCDGitHubAuth()
    - Add command to main CLI in `cmd/hub/main.go`
    - _Requirements: 3.6, 4.5_

- [x] 1. Create ArgoCD Application manifests
  - [x] 1.1 Create platform-infisical ArgoCD Application
    - Create `manifests/argocd/apps/platform-infisical.yaml`
    - Set sync-wave annotation to "3"
    - Configure automated sync with prune and selfHeal
    - Set retry policy (duration: 30s, maxDuration: 10m, limit: 15)
    - Point to `manifests/platform-infisical` directory
    - Target namespace: zero-ops-system
    - _Requirements: 5.1, 5.2, 5.4, 5.5, 5.6_

  - [x] 1.2 Create platform-external-secrets ArgoCD Application
    - Create `manifests/argocd/apps/platform-external-secrets.yaml`
    - Set sync-wave annotation to "4"
    - Configure automated sync with prune and selfHeal
    - Set retry policy (duration: 30s, maxDuration: 10m, limit: 15)
    - Point to `manifests/platform-external-secrets` directory
    - Target namespace: external-secrets-system
    - _Requirements: 5.1, 5.2, 5.4, 5.5, 5.6_

  - [x] 1.3 Create platform-cluster-secret-store ArgoCD Application
    - Create `manifests/argocd/apps/platform-cluster-secret-store.yaml`
    - Set sync-wave annotation to "5"
    - Configure automated sync with prune and selfHeal
    - Set retry policy (duration: 30s, maxDuration: 10m, limit: 15)
    - Point to `manifests/platform-cluster-secret-store` directory
    - Target namespace: external-secrets-system
    - _Requirements: 5.1, 5.2, 5.4, 5.5, 5.6_

  - [x] 1.4 Create platform-argocd-github-auth ArgoCD Application
    - Create `manifests/argocd/apps/platform-argocd-github-auth.yaml`
    - Set sync-wave annotation to "6"
    - Configure automated sync with prune and selfHeal
    - Set retry policy (duration: 30s, maxDuration: 10m, limit: 15)
    - Point to `manifests/platform-argocd-github-auth` directory
    - Target namespace: argocd
    - _Requirements: 5.1, 5.2, 5.4, 5.5, 5.6_

- [x] 2. Create Infisical deployment manifests
  - [x] 2.1 Create Infisical namespace and PostgreSQL connection secret
    - Create `manifests/platform-infisical/namespace.yaml` for zero-ops-system
    - Create `manifests/platform-infisical/postgres-connection-secret.yaml`
    - Secret should reference CloudNativePG connection string
    - Use placeholder for password (to be replaced via KSOPS/Age)
    - _Requirements: 1.2, 1.3_

  - [x] 2.2 Create Infisical Helm values configuration
    - Create `manifests/platform-infisical/values.yaml`
    - Set chart version to v1.7.5
    - Disable built-in PostgreSQL (postgresql.enabled: false)
    - Disable built-in Redis (redis.enabled: false)
    - Configure external PostgreSQL secret reference
    - Set replica count to 2
    - Configure resource requests (cpu: 350m, memory: 1000Mi)
    - _Requirements: 1.1, 1.3, 1.6_

  - [x] 2.3 Create Infisical HelmRelease and ingress
    - Create `manifests/platform-infisical/helm-release.yaml`
    - Reference Helm chart from archived/references/identity-auth/infisical/helm-charts/infisical-standalone-postgres
    - Create `manifests/platform-infisical/ingress.yaml`
    - Configure HTTPS ingress with cert-manager annotation
    - Set hostname to infisical.zero-ops.local
    - _Requirements: 1.1, 1.4, 1.5_

- [x] 3. Create External Secrets Operator deployment manifests
  - [x] 3.1 Create ESO namespace and Helm values
    - Create `manifests/platform-external-secrets/namespace.yaml` for external-secrets-system
    - Create `manifests/platform-external-secrets/values.yaml`
    - Set chart version to v2.2.0
    - Enable CRD installation (installCRDs: true)
    - Set minimal resource requests (cpu: 10m, memory: 64Mi)
    - Enable ServiceMonitor for metrics
    - _Requirements: 2.1, 2.2, 2.4, 2.5_

  - [x] 3.2 Create ESO HelmRelease
    - Create `manifests/platform-external-secrets/helm-release.yaml`
    - Reference Helm chart from archived/references/identity-auth/external-secrets/deploy/charts/external-secrets
    - Configure automated sync policy
    - _Requirements: 2.1, 2.3, 2.6_

- [x] 4. Create ClusterSecretStore manifests
  - [x] 4.1 Create ClusterSecretStore resource
    - Create `manifests/platform-cluster-secret-store/cluster-secret-store.yaml`
    - Name: infisical-backend
    - Configure Infisical provider with API endpoint
    - Reference infisical-auth secret for authentication (created by bootstrap)
    - Set cluster scope (ClusterSecretStore, not SecretStore)
    - _Requirements: 3.1, 3.2, 3.4, 3.5, 3.7_

- [x] 5. Create ArgoCD GitHub authentication manifests
  - [x] 5.1 Create ExternalSecret for GitHub credentials
    - Create `manifests/platform-argocd-github-auth/external-secret.yaml`
    - Name: argocd-github-creds
    - Reference ClusterSecretStore "infisical-backend"
    - Fetch github-username and github-token from Infisical
    - Create target secret: repo-soloz-io-zero-ops (replaces bootstrap secret)
    - Add label: `argocd.argoproj.io/secret-type: repository`
    - Include template fields: `type: git`, `url`, `username`, `password`
    - Set refresh interval to 1h
    - _Requirements: 4.1, 4.2, 4.3, 4.4, 4.5, 4.6_

- [x] 6. Checkpoint - Verify ArgoCD Applications sync successfully
  - Run break-glass bootstrap methods first (task 0)
  - Commit all manifests to feature branch
  - Verify ArgoCD detects and syncs all four applications
  - Verify sync-wave ordering is respected (3→4→5→6)
  - Verify bootstrap secret replaced by ESO-managed secret
  - Ensure all tests pass, ask the user if questions arise.

- [x] 7. Create validation scripts for secret flow
  - [x] 7.1 Implement health check validation script
    - Create `test/e2e/check-secret-health.sh`
    - Add shebang and set error handling (set -euo pipefail)
    - Verify Infisical API is accessible
    - Verify External Secrets Operator is running
    - Verify ClusterSecretStore is ready
    - Verify ArgoCD GitHub secret exists and is synced
    - Verify ExternalSecret for GitHub credentials is synced
    - Verify ArgoCD applications can sync (GitHub auth working)
    - _Requirements: 6.1, 6.7, 6.8_

  - [x] 7.2 Implement E2E secret flow test script
    - Create `test/e2e/e2e-secret-flow.sh`
    - Add shebang and set error handling (set -euo pipefail)
    - Define configuration variables (API endpoint, timeout)
    - Add cleanup trap to ensure resources are deleted on exit
    - _Requirements: 6.1, 6.7, 6.8_

  - [x] 7.3 Implement Infisical secret creation step
    - Add function to create test secret in Infisical via API
    - Use curl with Bearer token authentication
    - Generate unique secret key and random value
    - Add error handling for API failures
    - _Requirements: 6.1, 6.4_

  - [x] 7.4 Implement ExternalSecret creation and verification
    - Add function to create ExternalSecret resource
    - Reference ClusterSecretStore "infisical-backend"
    - Wait for Kubernetes secret creation (max 60 seconds)
    - Verify secret value matches Infisical value
    - _Requirements: 6.2, 6.3, 6.4_

  - [x] 7.5 Implement pod deployment and secret consumption test
    - Add function to deploy test pod with secret mount
    - Wait for pod to be ready
    - Execute command in pod to read secret file
    - Verify pod can read secret value correctly
    - _Requirements: 6.5, 6.6_

  - [x] 7.6 Implement cleanup function
    - Add function to delete test pod, ExternalSecret, and Kubernetes secret
    - Delete test secret from Infisical via API
    - Ensure cleanup runs even on script failure
    - _Requirements: 6.7_

- [x] 8. Final checkpoint - End-to-end validation
  - Run health check script to verify component deployment
  - Run E2E secret flow test to verify complete secret lifecycle
  - Verify Prometheus Operator, Grafana Alloy, and VictoriaMetrics Ingress applications sync successfully
  - Ensure all tests pass, ask the user if questions arise.

## Notes

- All infrastructure changes must be committed to Git and synced via ArgoCD (no kubectl apply)
- Health check script is read-only and verifies deployed resources
- E2E test script uses kubectl apply for ephemeral test resources only (acceptable for CI/CD testing)
- Sync-wave ordering ensures correct dependency chain: Database (2) → Infisical (3) → ESO (4) → ClusterSecretStore (5) → GitHub Auth (6)
- Two validation scripts serve different purposes:
  - `test/e2e/check-secret-health.sh`: Read-only health checks after deployment
  - `test/e2e/e2e-secret-flow.sh`: Active E2E testing for CI/CD pipelines

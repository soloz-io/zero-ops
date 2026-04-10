# Requirements Document: Hub Operator

## Introduction

The hub-operator is a Kubernetes Operator that manages Day-2 operations for the Zero-Ops Hub cluster. It replaces imperative CLI-driven bootstrap logic with declarative, GitOps-native reconciliation. The operator watches the HubEnvironment Custom Resource and orchestrates Secret Zero generation, database migrations, role provisioning, OAuth client registration, NATS stream creation, and Infisical backup operations.

## Glossary

- **Hub_Operator**: Kubernetes Operator managing Day-2 Hub cluster operations
- **HubEnvironment_CR**: Custom Resource defining Hub cluster configuration
- **Secret_Zero**: Bootstrap secrets required before ArgoCD syncs dependent services
- **CLI**: Command-line interface handling CAPI bootstrap and ArgoCD installation
- **CNPG_Cluster**: CloudNativePG PostgreSQL cluster Custom Resource
- **Infisical**: Secret management service acting as Source of Truth for credentials
- **Hydra**: Ory OAuth2/OIDC server
- **NATS_JetStream**: NATS messaging system with persistent streams
- **PgBouncer_Pooler**: Connection pooler provided by CNPG
- **ArgoCD**: GitOps continuous deployment tool
- **Sync_Wave**: ArgoCD annotation controlling deployment order
- **Kubebuilder**: Kubernetes Operator SDK framework
- **KUTTL**: Kubernetes Test Tool for declarative E2E testing
- **Envtest**: Controller-runtime testing framework with fake API server

## Requirements

### Requirement 1: CLI Scope Boundary

**User Story:** As a platform operator, I want the CLI to handle only infrastructure provisioning, so that Day-2 operations are managed declaratively by the operator.

#### Acceptance Criteria

1. THE CLI SHALL create a kind bootstrap cluster
2. THE CLI SHALL install CAPI and CAPH operators
3. THE CLI SHALL provision the Hetzner management cluster
4. THE CLI SHALL pivot CAPI state to the Hetzner cluster
5. THE CLI SHALL install ArgoCD
6. THE CLI SHALL apply app-of-apps.yaml
7. THE CLI SHALL save kubeconfig to ~/.zero-ops/clusters/{name}.kubeconfig
8. THE CLI SHALL exit after completing ArgoCD installation
9. THE CLI SHALL NOT generate Secret Zero
10. THE CLI SHALL NOT execute database migrations
11. THE CLI SHALL NOT create database roles
12. THE CLI SHALL NOT register OAuth clients
13. THE CLI SHALL NOT create NATS streams

### Requirement 2: Operator Scope Boundary

**User Story:** As a platform operator, I want the operator to handle all Day-2 operations declaratively, so that the Hub cluster is fully GitOps-compliant.

#### Acceptance Criteria

1. THE Hub_Operator SHALL generate Secret_Zero for infisical-secrets
2. THE Hub_Operator SHALL generate Secret_Zero for platform-db-app
3. THE Hub_Operator SHALL generate Secret_Zero for infisical-db-credentials
4. THE Hub_Operator SHALL execute database migrations using golang-migrate/migrate library
5. THE Hub_Operator SHALL create the mcp_server database role via database/sql
6. THE Hub_Operator SHALL create the infisical database role via database/sql
7. THE Hub_Operator SHALL create the spoke_controller database role via database/sql
8. THE Hub_Operator SHALL create the spire_server database role via database/sql
9. THE Hub_Operator SHALL register OAuth clients with Hydra using the official Go SDK
10. THE Hub_Operator SHALL create NATS_JetStream streams using the NATS Go SDK
11. THE Hub_Operator SHALL upload all generated secrets to Infisical API
12. WHEN a CNPG_Cluster becomes ready, THE Hub_Operator SHALL reconcile database roles
13. WHEN Hydra becomes ready, THE Hub_Operator SHALL reconcile OAuth clients
14. WHEN Infisical becomes ready, THE Hub_Operator SHALL upload secrets

### Requirement 3: HubEnvironment CRD Schema

**User Story:** As a platform operator, I want a single declarative resource to configure the entire Hub, so that all configuration is version-controlled in Git.

#### Acceptance Criteria

1. THE HubEnvironment_CR SHALL include a domain field for networking configuration
2. THE HubEnvironment_CR SHALL include TLS configuration fields
3. THE HubEnvironment_CR SHALL include VictoriaMetrics retention policy fields
4. THE HubEnvironment_CR SHALL include Grafana Alloy scrape interval fields
5. THE HubEnvironment_CR SHALL include NATS stream definitions with name and subjects
6. THE HubEnvironment_CR SHALL include OAuth client definitions with redirect URIs
7. THE HubEnvironment_CR SHALL include secret generation specifications
8. THE HubEnvironment_CR SHALL include CNPG_Cluster reference for database configuration
9. THE HubEnvironment_CR SHALL include managed database role specifications
10. THE HubEnvironment_CR SHALL be cluster-scoped

### Requirement 4: Secret Zero Generation

**User Story:** As a platform operator, I want the operator to generate cryptographically secure bootstrap secrets, so that dependent services can start without manual intervention.

#### Acceptance Criteria

1. WHEN the HubEnvironment_CR is created, THE Hub_Operator SHALL generate a 32-character ENCRYPTION_KEY for Infisical
2. WHEN the HubEnvironment_CR is created, THE Hub_Operator SHALL generate a 32-character AUTH_SECRET for Infisical
3. WHEN the HubEnvironment_CR is created, THE Hub_Operator SHALL generate a Redis password
4. WHEN the HubEnvironment_CR is created, THE Hub_Operator SHALL construct a REDIS_URL connection string
5. WHEN the HubEnvironment_CR is created, THE Hub_Operator SHALL generate a self-signed CA Certificate for the database using Go's crypto/x509 package
6. WHEN the HubEnvironment_CR is created, THE Hub_Operator SHALL store the generated CA in platform-db-ca secret so Infisical can boot with TLS in Wave 1
7. THE CNPG Cluster SHALL be configured to import the pre-generated platform-db-ca secret rather than generating its own CA in Wave 2
8. WHEN the HubEnvironment_CR is created, THE Hub_Operator SHALL base64-encode the CA certificate as DB_ROOT_CERT
9. WHEN the HubEnvironment_CR is created, THE Hub_Operator SHALL create the infisical-secrets Kubernetes secret
10. WHEN the HubEnvironment_CR is created, THE Hub_Operator SHALL generate a 32-character password for platform-db-app
11. WHEN the HubEnvironment_CR is created, THE Hub_Operator SHALL create the platform-db-app Kubernetes secret with type BasicAuth
12. WHEN the HubEnvironment_CR is created, THE Hub_Operator SHALL generate a 32-character password for infisical-db-credentials
13. WHEN the HubEnvironment_CR is created, THE Hub_Operator SHALL create the infisical-db-credentials Kubernetes secret
14. WHEN the HubEnvironment_CR is created, THE Hub_Operator SHALL create the infisical-postgres-connection secret with DB_SSL_MODE set to require
15. WHEN the HubEnvironment_CR is created, THE Hub_Operator SHALL generate a 32-character password for hydra-db-credentials
16. WHEN the HubEnvironment_CR is created, THE Hub_Operator SHALL create the hydra-db-credentials Kubernetes secret
17. WHEN the HubEnvironment_CR is created, THE Hub_Operator SHALL generate a 32-character password for kratos-db-credentials
18. WHEN the HubEnvironment_CR is created, THE Hub_Operator SHALL create the kratos-db-credentials Kubernetes secret
19. WHEN the HubEnvironment_CR is created, THE Hub_Operator SHALL generate a 32-character password for keto-db-credentials
20. WHEN the HubEnvironment_CR is created, THE Hub_Operator SHALL create the keto-db-credentials Kubernetes secret
21. IF a secret already exists, THE Hub_Operator SHALL reuse existing passwords to maintain data integrity
22. THE Hub_Operator SHALL set ownerReferences on all created secrets pointing to the HubEnvironment_CR

### Requirement 5: Database Migration Execution

**User Story:** As a platform operator, I want database migrations to run natively in the operator, so that bash jobs are eliminated and error handling is improved.

#### Acceptance Criteria

1. WHEN the CNPG_Cluster status is Ready, THE Hub_Operator SHALL execute database migrations using golang-migrate/migrate
2. THE Hub_Operator SHALL connect directly to the CNPG primary service (platform-db-rw) for migrations, NOT the PgBouncer pooler
3. THE Hub_Operator SHALL use TLS for database connections with sslmode=require
4. THE Hub_Operator SHALL read migration files from an embedded filesystem
5. IF a migration fails with a dirty database state, THE Hub_Operator SHALL set MigrationsComplete condition to False with reason "DirtyDatabase" and SHALL NOT requeue automatically
6. IF a migration fails with a transient error (connection timeout, network failure), THE Hub_Operator SHALL requeue with exponential backoff
7. WHEN all migrations complete successfully, THE Hub_Operator SHALL update the HubEnvironment_CR status condition MigrationsComplete to True

### Requirement 6: Database Role Provisioning

**User Story:** As a platform operator, I want database roles to be created declaratively, so that application services have proper database access.

#### Acceptance Criteria

1. WHEN migrations complete, THE Hub_Operator SHALL create the mcp_server role via database/sql
2. WHEN migrations complete, THE Hub_Operator SHALL create the infisical role via database/sql
3. WHEN migrations complete, THE Hub_Operator SHALL create the spoke_controller role via database/sql
4. WHEN migrations complete, THE Hub_Operator SHALL create the spire_server role via database/sql
5. WHEN migrations complete, THE Hub_Operator SHALL create the hydra role via database/sql
6. WHEN migrations complete, THE Hub_Operator SHALL create the kratos role via database/sql
7. WHEN migrations complete, THE Hub_Operator SHALL create the keto role via database/sql
8. THE Hub_Operator SHALL grant appropriate permissions to each role based on HubEnvironment_CR specifications
9. THE Hub_Operator SHALL use idempotent CREATE ROLE IF NOT EXISTS statements
10. WHEN a role password changes in the Kubernetes secret, THE Hub_Operator SHALL execute ALTER ROLE to update the PostgreSQL password
11. THE Hub_Operator SHALL compare the secret hash with pg_authid to detect password drift
12. IF role creation fails, THE Hub_Operator SHALL update the HubEnvironment_CR status with the error message
13. IF role creation fails, THE Hub_Operator SHALL requeue with exponential backoff
14. WHEN all roles are created, THE Hub_Operator SHALL update the HubEnvironment_CR status condition DatabaseRolesConfigured to True
15. THE Hub_Operator SHALL DELETE database roles from PostgreSQL that exist in the database (with the zero-ops managed prefix/comment) but are no longer defined in the HubEnvironment_CR spec

### Requirement 7: OAuth Client Registration

**User Story:** As a platform operator, I want OAuth clients to be registered with Hydra declaratively, so that identity services are configured automatically.

#### Acceptance Criteria

1. WHEN Hydra becomes ready, THE Hub_Operator SHALL register OAuth clients using the Ory Hydra Go SDK
2. THE Hub_Operator SHALL read OAuth client specifications from the HubEnvironment_CR
3. THE Hub_Operator SHALL configure redirect URIs from the HubEnvironment_CR
4. THE Hub_Operator SHALL use idempotent client registration with client_id as the unique key
5. IF Hydra API returns 404, THE Hub_Operator SHALL requeue after 10 seconds
6. IF Hydra API returns 5xx, THE Hub_Operator SHALL requeue with exponential backoff
7. WHEN all OAuth clients are registered, THE Hub_Operator SHALL update the HubEnvironment_CR status condition OAuthClientsRegistered to True
8. THE Hub_Operator SHALL DELETE OAuth clients from Hydra that exist in Hydra but are no longer defined in the HubEnvironment_CR spec

### Requirement 8: NATS Stream Creation

**User Story:** As a platform operator, I want NATS streams to be created declaratively, so that messaging infrastructure is configured automatically.

#### Acceptance Criteria

1. WHEN NATS becomes ready, THE Hub_Operator SHALL create JetStream streams using the NATS Go SDK
2. THE Hub_Operator SHALL read stream definitions from the HubEnvironment_CR
3. THE Hub_Operator SHALL configure stream subjects from the HubEnvironment_CR
4. THE Hub_Operator SHALL use idempotent stream creation with stream name as the unique key
5. THE Hub_Operator SHALL compare existing NATS stream configuration against the HubEnvironment_CR spec
6. IF stream configuration drift is detected (subjects, retention, storage), THE Hub_Operator SHALL execute UpdateStream API call
7. IF NATS API is unreachable, THE Hub_Operator SHALL requeue after 10 seconds
8. IF NATS API returns an error, THE Hub_Operator SHALL requeue with exponential backoff
9. WHEN all streams are created and synchronized, THE Hub_Operator SHALL update the HubEnvironment_CR status condition NATSStreamsConfigured to True
10. THE Hub_Operator SHALL DELETE NATS streams that exist in NATS but are no longer defined in the HubEnvironment_CR spec

### Requirement 9: Infisical Secret Backup

**User Story:** As a platform operator, I want all generated secrets uploaded to Infisical, so that credentials are backed up and recoverable.

#### Acceptance Criteria

1. WHEN Infisical becomes ready AND SecretsBackedUp condition is False, THE Hub_Operator SHALL upload infisical-db-username to Infisical API
2. WHEN Infisical becomes ready AND SecretsBackedUp condition is False, THE Hub_Operator SHALL upload infisical-db-password to Infisical API
3. WHEN Infisical becomes ready AND SecretsBackedUp condition is False, THE Hub_Operator SHALL upload platform-db-app-username to Infisical API
4. WHEN Infisical becomes ready AND SecretsBackedUp condition is False, THE Hub_Operator SHALL upload platform-db-app-password to Infisical API
5. WHEN Infisical becomes ready AND SecretsBackedUp condition is False, THE Hub_Operator SHALL upload control-plane-db-username to Infisical API
6. WHEN Infisical becomes ready AND SecretsBackedUp condition is False, THE Hub_Operator SHALL upload control-plane-db-password to Infisical API
7. WHEN Infisical becomes ready AND SecretsBackedUp condition is False, THE Hub_Operator SHALL upload hub-db-username to Infisical API
8. WHEN Infisical becomes ready AND SecretsBackedUp condition is False, THE Hub_Operator SHALL upload hub-db-password to Infisical API
9. WHEN Infisical becomes ready AND SecretsBackedUp condition is False, THE Hub_Operator SHALL upload spire-server-db-username to Infisical API
10. WHEN Infisical becomes ready AND SecretsBackedUp condition is False, THE Hub_Operator SHALL upload spire-server-db-password to Infisical API
11. WHEN Infisical becomes ready AND SecretsBackedUp condition is False, THE Hub_Operator SHALL upload hydra-db-username to Infisical API
12. WHEN Infisical becomes ready AND SecretsBackedUp condition is False, THE Hub_Operator SHALL upload hydra-db-password to Infisical API
13. WHEN Infisical becomes ready AND SecretsBackedUp condition is False, THE Hub_Operator SHALL upload kratos-db-username to Infisical API
14. WHEN Infisical becomes ready AND SecretsBackedUp condition is False, THE Hub_Operator SHALL upload kratos-db-password to Infisical API
15. WHEN Infisical becomes ready AND SecretsBackedUp condition is False, THE Hub_Operator SHALL upload keto-db-username to Infisical API
16. WHEN Infisical becomes ready AND SecretsBackedUp condition is False, THE Hub_Operator SHALL upload keto-db-password to Infisical API
17. THE Hub_Operator SHALL track uploaded secrets individually in the HubEnvironment_CR Status via an UploadedSecrets array
18. WHEN a new database role or secret requirement is added to the CR spec, THE Hub_Operator SHALL upload the new secret to Infisical and append its name to the UploadedSecrets array
19. THE Hub_Operator SHALL NOT overwrite secrets whose names already exist in the UploadedSecrets array
20. THE External Secrets Operator SHALL own the lifecycle of syncing secrets from Infisical to Kubernetes after bootstrap
21. IF Infisical API returns 404, THE Hub_Operator SHALL requeue after 10 seconds
22. IF Infisical API returns 5xx, THE Hub_Operator SHALL requeue with exponential backoff
23. THE Hub_Operator SHALL NOT upload configuration data (hostnames, ports, URLs, database names, or TLS certificates) to Infisical
24. THE Hub_Operator SHALL strictly limit Infisical payloads to cryptographic keys, passwords, and highly sensitive tokens
25. THE External Secrets Operator SHALL use template fields to combine Infisical passwords with Kubernetes service discovery (DNS names, ports)
26. WHEN all secrets are uploaded, THE Hub_Operator SHALL update the HubEnvironment_CR status condition SecretsBackedUp to True

### Requirement 10: Idempotent Reconciliation

**User Story:** As a platform operator, I want reconciliation to be safe to retry infinitely, so that transient failures do not corrupt state.

#### Acceptance Criteria

1. THE Hub_Operator SHALL check if a secret exists before creating it
2. THE Hub_Operator SHALL reuse existing secret values when secrets already exist
3. THE Hub_Operator SHALL use CREATE ROLE IF NOT EXISTS for database role creation
4. THE Hub_Operator SHALL check if an OAuth client exists before registering it
5. THE Hub_Operator SHALL check if a NATS stream exists before creating it
6. THE Hub_Operator SHALL use CreateOrUpdate pattern for Infisical secret uploads
7. THE Hub_Operator SHALL compare desired state with actual state using DeepEqual before updating resources
8. THE Hub_Operator SHALL update resources only when changes are detected

### Requirement 11: Status Reporting

**User Story:** As a platform operator, I want detailed status updates after each reconciliation step, so that I can monitor operator progress.

#### Acceptance Criteria

1. WHEN Secret_Zero is generated, THE Hub_Operator SHALL set the SecretZeroGenerated condition to True
2. WHEN migrations complete, THE Hub_Operator SHALL set the MigrationsComplete condition to True
3. WHEN database roles are configured, THE Hub_Operator SHALL set the DatabaseRolesConfigured condition to True
4. WHEN OAuth clients are registered, THE Hub_Operator SHALL set the OAuthClientsRegistered condition to True
5. WHEN NATS streams are configured, THE Hub_Operator SHALL set the NATSStreamsConfigured condition to True
6. WHEN secrets are backed up, THE Hub_Operator SHALL set the SecretsBackedUp condition to True
7. WHEN all conditions are True, THE Hub_Operator SHALL set the Ready condition to True
8. IF any step fails, THE Hub_Operator SHALL set the corresponding condition to False with a descriptive message
9. THE Hub_Operator SHALL update the HubEnvironment_CR status after each reconciliation step

### Requirement 12: Dependency Watching

**User Story:** As a platform operator, I want the operator to watch external dependencies, so that reconciliation proceeds when dependencies become ready.

#### Acceptance Criteria

1. THE Hub_Operator SHALL watch CNPG_Cluster resources with ResourceVersionChangedPredicate
2. THE Hub_Operator SHALL watch Hydra Deployment resources with ResourceVersionChangedPredicate
3. THE Hub_Operator SHALL watch Infisical Deployment resources with ResourceVersionChangedPredicate
4. THE Hub_Operator SHALL watch NATS StatefulSet resources with ResourceVersionChangedPredicate
5. WHEN a watched resource status changes to Ready, THE Hub_Operator SHALL trigger reconciliation
6. THE Hub_Operator SHALL own all Kubernetes secrets it creates
7. WHEN an owned secret is deleted, THE Hub_Operator SHALL trigger reconciliation to recreate it
8. THE Hub_Operator SHALL watch Secrets possessing the label "ops.nutgraf.in/db-credentials=true", regardless of ownership
9. WHEN a watched non-owned secret changes (ESO-driven password rotation), THE Hub_Operator SHALL trigger reconciliation to execute ALTER ROLE

### Requirement 13: Sync Wave Architecture

**User Story:** As a platform operator, I want resources deployed in the correct order, so that dependencies are satisfied before dependent services start.

#### Acceptance Criteria

1. THE HubEnvironment_CRD SHALL be deployed in sync wave 0
2. THE Hub_Operator Deployment SHALL be deployed in sync wave 1
3. THE HubEnvironment_CR SHALL be deployed in sync wave 1
4. THE CNPG_Cluster SHALL be deployed in sync wave 2
5. THE NATS StatefulSet SHALL be deployed in sync wave 2
6. THE Redis StatefulSet SHALL be deployed in sync wave 2
7. THE Infisical Deployment SHALL be deployed in sync wave 3
8. THE SPIRE Server SHALL be deployed in sync wave 3
9. THE Hydra Deployment SHALL be deployed in sync wave 3
10. THE AgentGateway SHALL be deployed in sync wave 4 or higher
11. THE MCP Server SHALL be deployed in sync wave 4 or higher

### Requirement 14: High Availability Configuration

**User Story:** As a platform operator, I want the operator to run in HA mode, so that reconciliation continues during pod failures.

#### Acceptance Criteria

1. THE Hub_Operator SHALL enable leader election
2. THE Hub_Operator SHALL run with at least 2 replicas in production
3. WHEN the leader pod fails, THE Hub_Operator SHALL elect a new leader within 15 seconds
4. THE Hub_Operator SHALL use a cluster-scoped ClusterRole for RBAC
5. THE Hub_Operator SHALL have permissions to read and write HubEnvironment CRs
6. THE Hub_Operator SHALL have permissions to read CNPG_Cluster resources
7. THE Hub_Operator SHALL have permissions to read Deployment resources
8. THE Hub_Operator SHALL have permissions to read StatefulSet resources
9. THE Hub_Operator SHALL have permissions to create and update Secret resources
10. THE Hub_Operator SHALL have permissions to create and update ConfigMap resources

### Requirement 15: Packaging and Deployment

**User Story:** As a platform operator, I want the operator packaged with Kustomize, so that it integrates with the existing GitOps workflow.

#### Acceptance Criteria

1. THE Hub_Operator SHALL be scaffolded using Kubebuilder
2. THE Hub_Operator SHALL use the native config/ directory structure from Kubebuilder
3. THE Hub_Operator SHALL be packaged with Kustomize
4. THE Hub_Operator SHALL be deployed via ArgoCD
5. THE Hub_Operator SHALL NOT be deployed via kubectl apply
6. THE Hub_Operator SHALL include a kustomization.yaml in the config/default directory
7. THE Hub_Operator SHALL include RBAC manifests in the config/rbac directory
8. THE Hub_Operator SHALL include CRD manifests in the config/crd directory
9. THE Hub_Operator SHALL include manager manifests in the config/manager directory

### Requirement 16: Testing Strategy

**User Story:** As a platform developer, I want comprehensive tests for the operator, so that regressions are caught before production deployment.

#### Acceptance Criteria

1. THE Hub_Operator SHALL include unit tests using envtest
2. THE Hub_Operator SHALL include E2E tests using KUTTL
3. THE Hub_Operator unit tests SHALL use the fake Kubernetes client from controller-runtime
4. THE Hub_Operator E2E tests SHALL deploy real CNPG clusters
5. THE Hub_Operator E2E tests SHALL deploy real Hydra instances
6. THE Hub_Operator E2E tests SHALL deploy real Infisical instances
7. THE Hub_Operator E2E tests SHALL deploy real NATS clusters
8. THE Hub_Operator E2E tests SHALL assert that database roles exist via read-only SQL queries
9. THE Hub_Operator E2E tests SHALL assert that OAuth clients exist via Hydra API queries
10. THE Hub_Operator E2E tests SHALL assert that NATS streams exist via NATS API queries
11. THE Hub_Operator E2E tests SHALL assert that secrets exist in Infisical via API queries

### Requirement 17: Migration from CLI to Operator

**User Story:** As a platform developer, I want clear guidance on migrating CLI logic to the operator, so that no functionality is lost during the transition.

#### Acceptance Criteria

1. THE InstallInfisicalSecrets function logic SHALL be migrated to the Hub_Operator reconciler
2. THE InstallPostgresConnectionSecret function logic SHALL be migrated to the Hub_Operator reconciler
3. THE InstallPlatformDatabaseCredentials function logic SHALL be migrated to the Hub_Operator reconciler
4. THE InstallSPIREServerCredentials function logic SHALL be migrated to the Hub_Operator reconciler
5. THE setup-platform-roles-job.yaml bash job SHALL be deleted
6. THE password-rotation-job.yaml bash job SHALL be deleted
7. THE init-streams-job.yaml bash job SHALL be deleted
8. THE spire-registration-job.yaml bash job SHALL be deleted
9. THE internal/hub/components/installer.go file SHALL be refactored to remove Day-2 logic
10. THE CLI SHALL retain only CAPI bootstrap and ArgoCD installation logic

### Requirement 18: Backward Compatibility

**User Story:** As a platform operator, I want to break compatibility with existing CLI state files, so that the v9.0 architecture starts with a clean slate.

#### Acceptance Criteria

1. THE Hub_Operator SHALL NOT read CLI state files from ~/.zero-ops/state/
2. THE Hub_Operator SHALL use only the HubEnvironment_CR status field for state tracking
3. THE CLI state file SHALL be used only for recovering from broken kind-to-Cloud CAPI pivots
4. WHEN ArgoCD is running, THE CLI state file SHALL be irrelevant to operator reconciliation
5. THE Hub_Operator SHALL NOT depend on any CLI-generated state beyond Secret_Zero

### Requirement 19: Memory Optimization

**User Story:** As a platform operator, I want the operator to use minimal memory, so that it can run efficiently in resource-constrained environments.

#### Acceptance Criteria

1. THE Hub_Operator SHALL use cache transformers to strip Secret data payloads from the cache for secrets it does not own
2. THE Hub_Operator SHALL use an uncached client.Reader for reading operational secrets (platform-db-superuser, platform-db-ca, infisical-auth)
3. THE Hub_Operator SHALL apply label-based cache bypass for secrets with label "app.kubernetes.io/managed-by=zero-ops-hub-cli"
4. THE Hub_Operator SHALL use the StripDataFromSecretOrConfigMapTransform pattern from controller-runtime for non-operational secrets
5. THE Hub_Operator SHALL configure cache options in the main.go file
6. THE Hub_Operator SHALL reduce memory usage by at least 90% compared to caching full secret payloads for non-operational secrets

### Requirement 20: Error Handling and Retry Logic

**User Story:** As a platform operator, I want robust error handling with exponential backoff, so that transient failures do not cause operator crashes.

#### Acceptance Criteria

1. IF a database connection fails, THE Hub_Operator SHALL requeue after 10 seconds
2. IF a Hydra API call fails with 5xx, THE Hub_Operator SHALL requeue with exponential backoff
3. IF an Infisical API call fails with 5xx, THE Hub_Operator SHALL requeue with exponential backoff
4. IF a NATS API call fails, THE Hub_Operator SHALL requeue with exponential backoff
5. THE Hub_Operator SHALL NOT use custom retry loops
6. THE Hub_Operator SHALL return ctrl.Result with RequeueAfter for transient failures
7. THE Hub_Operator SHALL return ctrl.Result with Requeue=false for permanent errors requiring manual intervention
8. THE Hub_Operator SHALL NOT panic or crash on API failures
9. THE Hub_Operator SHALL log errors with structured logging using controller-runtime logger
10. THE Hub_Operator SHALL watch for an annotation "ops.nutgraf.in/reconcile-trigger" on the HubEnvironment_CR
11. WHEN the "ops.nutgraf.in/reconcile-trigger" annotation is added or updated, THE Hub_Operator SHALL resume reconciliation from a Permanent Error state
12. THE annotation value SHALL be a timestamp in RFC3339 format to ensure uniqueness

### Requirement 22: Operator Authentication to External Services

**User Story:** As a platform operator, I want the operator to authenticate securely to external services, so that API interactions are authorized and auditable.

#### Acceptance Criteria

1. THE Hub_Operator SHALL read the infisical-auth secret (client-id, client-secret) from hub-platform-ops namespace to authenticate to Infisical API
2. THE Hub_Operator SHALL use Universal Auth (client credentials grant) to obtain a bearer token from Infisical
3. THE Hub_Operator SHALL cache the Infisical bearer token and refresh it before expiration
4. THE Hub_Operator SHALL connect to NATS using the nats://nats.hub-platform-core.svc:4222 URL without authentication for JetStream management
5. THE Hub_Operator SHALL use the Ory Hydra Admin API at https://hydra-admin.ory-system.svc for OAuth client registration
6. THE Hub_Operator SHALL NOT require authentication for Hydra Admin API (internal cluster service)
7. IF authentication to Infisical fails, THE Hub_Operator SHALL requeue after 10 seconds
8. IF the infisical-auth secret is missing, THE Hub_Operator SHALL set the SecretsBackedUp condition to False with reason "AuthenticationFailed"
9. IF the infisical-auth secret is missing, THE Hub_Operator SHALL continue executing Phase 1 (Secret Zero Generation) and Phase 2 (Database Setup)
10. IF the infisical-auth secret is missing, THE Hub_Operator SHALL suspend Phase 3 (Upload Secrets to Infisical) until the secret becomes available
11. THE Hub_Operator SHALL allow Infisical to boot successfully even when infisical-auth is not yet configured

### Requirement 23: Certificate Rotation and Pod Restart Handling

**User Story:** As a platform operator, I want the operator to handle certificate rotation gracefully, so that services continue operating when certificates are renewed.

#### Acceptance Criteria

1. WHEN the platform-db-ca secret is updated by CNPG, THE Hub_Operator SHALL detect the change via watch
2. WHEN the platform-db-ca secret changes, THE Hub_Operator SHALL update the DB_ROOT_CERT field in infisical-secrets
3. WHEN infisical-secrets is updated with a new DB_ROOT_CERT, THE Hub_Operator SHALL patch the Infisical Deployment with a restartedAt annotation
4. THE Hub_Operator SHALL use the annotation pattern: kubectl.kubernetes.io/restartedAt: <RFC3339 timestamp>
5. THE Infisical Deployment SHALL perform a rolling restart to pick up the new certificate
6. THE Hub_Operator SHALL watch for the Infisical Deployment to become ready again after restart
7. IF the Infisical restart fails, THE Hub_Operator SHALL update the status condition with the error message
8. THE Hub_Operator SHALL apply the same restart pattern to Redis StatefulSet when redis credentials in infisical-secrets change
9. WHEN the platform-db-ca secret changes, THE Hub_Operator SHALL patch the Hydra Deployment with a restartedAt annotation
10. WHEN the platform-db-ca secret changes, THE Hub_Operator SHALL patch the Kratos Deployment with a restartedAt annotation
11. WHEN the platform-db-ca secret changes, THE Hub_Operator SHALL patch the Keto Deployment with a restartedAt annotation
12. WHEN the platform-db-ca secret changes, THE Hub_Operator SHALL patch the SPIRE Server StatefulSet with a restartedAt annotation
13. WHEN the platform-db-ca secret changes, THE Hub_Operator SHALL patch the MCP Server Deployment with a restartedAt annotation
14. THE Hub_Operator SHALL ensure all database-connected services reload the CA certificate to prevent x509 certificate errors
15. WHEN a watched database credential secret is updated by ESO, THE Hub_Operator SHALL execute ALTER ROLE to update the PostgreSQL password
16. WHEN a watched database credential secret is updated by ESO, THE Hub_Operator SHALL patch the consuming Deployment/StatefulSet with a restartedAt annotation to force a rolling restart

**User Story:** As a platform developer, I want robust parsing and serialization of HubEnvironment CRs, so that configuration is validated and formatted correctly.

#### Acceptance Criteria

1. WHEN a valid HubEnvironment_CR is provided, THE Parser SHALL parse it into a HubEnvironment object
2. WHEN an invalid HubEnvironment_CR is provided, THE Parser SHALL return a descriptive validation error
3. THE Pretty_Printer SHALL format HubEnvironment objects back into valid YAML
4. FOR ALL valid HubEnvironment objects, parsing then printing then parsing SHALL produce an equivalent object

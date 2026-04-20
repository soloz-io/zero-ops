# Implementation Tasks: Hub Operator

## Task 1: Project Scaffolding and Setup

**Objective:** Initialize the Kubebuilder project structure and configure the development environment.

### Subtasks:

- [x] 1.1 Scaffold operator using Kubebuilder: `kubebuilder init --domain nutgraf.in --repo github.com/soloz-io/zero-ops/operators/hub-operator`
- [x] 1.2 Create HubEnvironment CRD: `kubebuilder create api --group ops --version v1alpha1 --kind HubEnvironment`
- [x] 1.3 Configure Go module dependencies (golang-migrate, NATS SDK, Hydra SDK, CNPG API)
- [x] 1.4 Set up project directory structure following operators/spoke-controller pattern
- [x] 1.5 Create internal package structure (controller/, client/, secrets/, database/, embed/)
- [x] 1.6 Configure Makefile for build, test, and deployment targets
- [x] 1.7 Set up .gitignore for operator-specific artifacts

**Acceptance Criteria:**
- Kubebuilder project structure exists in operators/hub-operator/
- All required Go dependencies are in go.mod
- `make build` compiles successfully
- Directory structure matches design.md specification

---

## Task 2: HubEnvironment CRD Schema Implementation

**Objective:** Define the complete HubEnvironment Custom Resource Definition with all required fields.

### Subtasks:

- [x] 2.1 Define spec.domain field for networking configuration
- [x] 2.2 Define spec.tls fields (issuer, email)
- [x] 2.3 Define spec.observability fields (VictoriaMetrics, Grafana Alloy)
- [x] 2.4 Define spec.database fields (clusterRef, namespace, roles array)
- [x] 2.5 Define spec.nats.streams array (name, subjects, retention, storage)
- [x] 2.6 Define spec.oauth.clients array (clientId, clientName, redirectUris, grantTypes, responseTypes)
- [x] 2.7 Define spec.secrets.infisical fields (projectSlug, environmentSlug)
- [x] 2.8 Define status.conditions array with standard condition types
- [x] 2.9 Define status.uploadedSecrets array for tracking Infisical uploads
- [x] 2.10 Define status.observedGeneration field
- [x] 2.11 Add validation rules (required fields, enum values, format constraints)
- [x] 2.12 Generate CRD manifests: `make manifests`
- [x] 2.13 Add example HubEnvironment CR in config/samples/

**Acceptance Criteria:**
- CRD YAML generated in config/crd/bases/
- All fields from design.md are present
- Validation rules enforce constraints
- Example CR validates successfully

---

## Task 3: Secret Zero Generation Implementation

**Objective:** Implement cryptographically secure bootstrap secret generation including self-signed CA.

### Subtasks:

- [x] 3.1 Create internal/secrets/generator.go package
- [x] 3.2 Implement generateSecurePassword() utility function (32-character hex)
- [x] 3.3 Implement generateSelfSignedCA() using crypto/x509 (4096-bit RSA, 10-year validity)
- [x] 3.4 Implement generateInfisicalSecrets() (ENCRYPTION_KEY, AUTH_SECRET, REDIS_URL, DB_ROOT_CERT)
- [x] 3.5 Implement generatePlatformDBApp() (BasicAuth secret with username/password)
- [x] 3.6 Implement generateInfisicalDBCredentials() (username/password)
- [x] 3.7 Implement generateInfisicalPostgresConnection() (DB_HOST, DB_PORT, DB_USER, DB_PASSWORD, DB_NAME, DB_SSL_MODE)
- [x] 3.8 Implement generateOryDBCredentials() for hydra, kratos, keto
- [x] 3.9 Add idempotency logic (reuse existing passwords if secrets exist)
- [x] 3.10 Add ownerReference to all created secrets
- [x] 3.11 Add label "ops.nutgraf.in/db-credentials=true" to credential secrets
- [x] 3.12 Implement GenerateSecretZero() orchestration function

**Acceptance Criteria:**
- All secrets generated with correct structure
- Passwords are exactly 32 characters
- Self-signed CA has valid X.509 structure
- Idempotent: running twice produces same passwords
- OwnerReferences set correctly

---

## Task 4: Database Migration Execution

**Objective:** Implement database migration execution using golang-migrate with embedded SQL files.

### Subtasks:

- [x] 4.1 Create internal/database/migrator.go package
- [x] 4.2 Create internal/embed/migrations.go with embedded filesystem
- [x] 4.3 Copy SQL migration files from manifests/hub-core-services/platform-database/migrations/ to internal/embed/migrations/
- [x] 4.4 Implement NewMigrator() constructor (connects to platform-db-rw, not pooler)
- [x] 4.5 Implement RunMigrations() using golang-migrate/migrate
- [x] 4.6 Implement DirtyDatabaseError custom error type
- [x] 4.7 Add dirty database detection logic
- [x] 4.8 Add transient error detection (connection timeout, network failure)
- [x] 4.9 Configure TLS connection with sslmode=require
- [x] 4.10 Add structured logging for migration progress

**Acceptance Criteria:**
- Migrations run successfully on clean database
- Dirty database state returns DirtyDatabaseError
- Transient errors are distinguishable from permanent errors
- Connects to primary service, not pooler
- All SQL files embedded in binary

---

## Task 5: Database Role Provisioning ✅

**Objective:** Implement database role creation, password rotation, and pruning.

### Subtasks:

- [x] 5.1 Create internal/database/roles.go package
- [x] 5.2 Implement NewRoleManager() constructor
- [x] 5.3 Implement CreateOrUpdateRole() with CREATE ROLE IF NOT EXISTS
- [x] 5.4 Implement passwordNeedsUpdate() for drift detection
- [x] 5.5 Implement ALTER ROLE logic for password rotation
- [x] 5.6 Implement permission granting (SELECT, INSERT, UPDATE, DELETE)
- [x] 5.7 Implement pruneOrphanedRoles() to delete roles not in CR spec
- [x] 5.8 Add role identification logic (zero-ops prefix or managed-by comment)
- [x] 5.9 Add structured logging for role operations
- [x] 5.10 Implement Close() for connection cleanup

**Acceptance Criteria:**
- ✅ Roles created with correct permissions
- ✅ Password rotation updates PostgreSQL via ALTER ROLE
- ✅ Orphaned roles are deleted
- ✅ Idempotent: running twice produces same state
- ✅ All operations use parameterized queries

---

## Task 6: Infisical Client Implementation ✅

**Objective:** Implement Infisical API client with Universal Auth and additive sync.

### Subtasks:

- [x] 6.1 Create internal/client/infisical.go package
- [x] 6.2 Migrate Client struct from internal/hub/infisical/client.go
- [x] 6.3 Implement NewInfisicalClient() with controller-runtime client
- [x] 6.4 Implement authenticate() using Universal Auth (client credentials grant)
- [x] 6.5 Implement token caching and refresh logic
- [x] 6.6 Implement CreateOrUpdateSecret() for Infisical API
- [x] 6.7 Add retry logic for 5xx errors
- [x] 6.8 Add timeout configuration (30 seconds)
- [x] 6.9 Read infisical-auth secret from hub-platform-ops namespace
- [x] 6.10 Add structured logging for API operations

**Acceptance Criteria:**
- ✅ Authenticates successfully with Universal Auth
- ✅ Uploads secrets to correct project/environment
- ✅ Retries on transient failures
- ✅ Token refreshes before expiration
- ✅ Only uploads passwords/keys (no URLs/hostnames)

---

## Task 7: Hydra Client Implementation ✅

**Objective:** Implement Ory Hydra client for OAuth client registration and pruning.

### Subtasks:

- [x] 7.1 Create internal/client/hydra.go package
- [x] 7.2 Implement NewHydraClient() using Ory Hydra Go SDK
- [x] 7.3 Configure client for https://hydra-admin.ory-system.svc
- [x] 7.4 Implement RegisterOAuthClient() with idempotent client_id check
- [x] 7.5 Implement client update logic for existing clients
- [x] 7.6 Implement pruneOrphanedOAuthClients() to delete clients not in CR spec
- [x] 7.7 Add retry logic for 404 and 5xx errors
- [x] 7.8 Add structured logging for OAuth operations

**Acceptance Criteria:**
- ✅ OAuth clients registered successfully
- ✅ Idempotent: running twice produces same state
- ✅ Orphaned clients are deleted
- ✅ Retries on transient failures
- ✅ No authentication required (internal service)

---

## Task 8: NATS Client Implementation ✅

**Objective:** Implement NATS JetStream client for stream creation, drift detection, and pruning.

### Subtasks:

- [x] 8.1 Create internal/client/nats.go package
- [x] 8.2 Implement NewNATSClient() using NATS Go SDK
- [x] 8.3 Configure connection to nats://nats.hub-platform-core.svc:4222
- [x] 8.4 Implement CreateStream() with idempotent stream name check
- [x] 8.5 Implement configuration drift detection (subjects, retention, storage)
- [x] 8.6 Implement UpdateStream() for drift correction
- [x] 8.7 Implement pruneOrphanedStreams() to delete streams not in CR spec
- [x] 8.8 Implement isNATSTransientError() for error classification
- [x] 8.9 Add retry logic for transient errors
- [x] 8.10 Implement Close() for connection cleanup

**Acceptance Criteria:**
- ✅ Streams created successfully
- ✅ Configuration drift detected and corrected
- ✅ Orphaned streams are deleted
- ✅ Transient errors (connection timeout) trigger retry
- ✅ Permanent errors (invalid config) update status

---

## Task 9: Main Reconciler Implementation ✅

**Objective:** Implement the core HubEnvironment reconciliation logic with all phases.

### Subtasks:

- [x] 9.1 Create internal/controller/hubenvironment_controller.go
- [x] 9.2 Define HubEnvironmentReconciler struct with all clients
- [x] 9.3 Implement Reconcile() main entry point
- [x] 9.4 Implement reconcile-trigger annotation handling
- [x] 9.5 Implement Phase 1: generateSecretZero()
- [x] 9.6 Implement Phase 2: runMigrations()
- [x] 9.7 Implement Phase 2: createDatabaseRoles()
- [x] 9.8 Implement Phase 3: uploadSecretsToInfisical() with UploadedSecrets tracking
- [x] 9.9 Implement Phase 3: registerOAuthClients()
- [x] 9.10 Implement Phase 3: createNATSStreams()
- [x] 9.11 Implement dependency readiness checks (isCNPGReady, isInfisicalReady, isHydraReady, isNATSReady)
- [x] 9.12 Implement status condition updates after each phase
- [x] 9.13 Implement error classification (transient vs permanent)
- [x] 9.14 Implement exponential backoff for transient errors
- [x] 9.15 Add structured logging for all operations
- [x] 9.16 Create controller README documentation

**Acceptance Criteria:**
- ✅ All phases execute in correct order
- ✅ Status conditions updated after each phase
- ✅ Transient errors trigger requeue with backoff
- ✅ Permanent errors (dirty database) do not requeue
- ✅ Ready condition set when all phases complete
- ✅ Build passes successfully

---

## Task 10: Certificate Rotation Handling ✅

**Objective:** Implement platform-db-ca rotation detection and service restart logic.

### Subtasks:

- [x] 10.1 Implement handleCertificateRotation() in reconciler
- [x] 10.2 Read platform-db-ca using UncachedClient
- [x] 10.3 Compare CA certificate with DB_ROOT_CERT in infisical-secrets
- [x] 10.4 Update DB_ROOT_CERT when CA changes
- [x] 10.5 Implement restartDeployment() helper (kubectl.kubernetes.io/restartedAt annotation)
- [x] 10.6 Implement restartStatefulSet() helper
- [x] 10.7 Restart Infisical Deployment on CA change
- [x] 10.8 Restart Redis StatefulSet on CA change
- [x] 10.9 Restart Hydra Deployment on CA change
- [x] 10.10 Restart Kratos Deployment on CA change
- [x] 10.11 Restart Keto Deployment on CA change
- [x] 10.12 Restart SPIRE Server StatefulSet on CA change
- [x] 10.13 Restart MCP Server Deployment on CA change
- [x] 10.14 Watch for Infisical Deployment readiness after restart (AC 23.6)
- [x] 10.15 Update status condition if restart fails (AC 23.7)
- [x] 10.16 Restart Redis on redis credentials change (AC 23.8)

**Acceptance Criteria:**
- ✅ CA rotation detected automatically
- ✅ All database-connected services restarted
- ✅ Rolling restart preserves availability
- ✅ No x509 certificate errors after rotation
- ✅ Status condition updated on restart failure
- ✅ Redis restarted on credentials change
- ✅ Build passes successfully

**Note:** Uses regular client for now. TODO comment added for UncachedClient (Task 13).

---

## Task 11: Password Rotation Handling ✅

**Objective:** Implement ESO-driven password rotation detection and service restart logic.

**Status:** Completed - All functionality implemented.

### Subtasks:

- [x] 11.1 Implement handlePasswordRotation() in reconciler
- [x] 11.2 Detect password changes in watched secrets (label: ops.nutgraf.in/db-credentials=true)
- [x] 11.3 Extract service name from secret labels
- [x] 11.4 Execute ALTER ROLE in PostgreSQL
- [x] 11.5 Restart consuming Deployment/StatefulSet based on service name
- [x] 11.6 Add structured logging for password rotation events

**Acceptance Criteria:**
- ✅ Password changes detected via watch
- ✅ ALTER ROLE executed successfully
- ✅ Consuming services restarted
- ✅ Services pick up new credentials
- ✅ Build passes successfully

**Dependencies:**
- Task 12: Watch configuration for secrets with label ops.nutgraf.in/db-credentials=true

---

## Task 12: Watch Configuration ✅

**Objective:** Configure controller watches for all external dependencies and secrets.

### Subtasks:

- [x] 12.1 Implement SetupWithManager() in reconciler
- [x] 12.2 Configure watch for HubEnvironment CR (primary resource)
- [x] 12.3 Configure Owns() for operator-created secrets
- [x] 12.4 Configure watch for CNPG Cluster with ResourceVersionChangedPredicate
- [x] 12.5 Configure watch for platform-db-ca secret (certificate rotation)
- [x] 12.6 Configure watch for secrets with label ops.nutgraf.in/db-credentials=true (password rotation)
- [x] 12.7 Configure watch for Hydra Deployment
- [x] 12.8 Configure watch for Infisical Deployment
- [x] 12.9 Configure watch for NATS StatefulSet
- [x] 12.10 Implement findHubEnvironmentForCNPG() mapper function
- [x] 12.11 Implement findHubEnvironmentForSecret() mapper function
- [x] 12.12 Implement findHubEnvironmentForDeployment() mapper function
- [x] 12.13 Implement findHubEnvironmentForStatefulSet() mapper function

**Acceptance Criteria:**
- ✅ Reconciliation triggered when dependencies become ready
- ✅ Certificate rotation triggers reconciliation
- ✅ Password rotation triggers reconciliation
- ✅ Owned secrets trigger reconciliation when deleted
- ✅ Build passes successfully

---

## Task 13: Memory Optimization ✅

**Objective:** Implement cache transformers and uncached client for memory efficiency.

### Subtasks:

- [x] 13.1 Configure cache options in cmd/main.go
- [x] 13.2 Apply StripManagedFields cache transformer to Secret cache
- [x] 13.3 Create uncached client.Client for operational secrets
- [x] 13.4 Pass both cached and uncached clients to reconciler
- [x] 13.5 Update all operational secret reads to use UncachedClient (NewMigrator, NewRoleManager, NewInfisicalClient, handleCertificateRotation, handlePasswordRotation)
- [x] 13.6 Update non-operational secret reads to use cached Client
- [x] 13.7 Add comments documenting when to use each client

**Acceptance Criteria:**
- ✅ Cache transformer applied to Secret cache
- ✅ Operational secrets read via UncachedClient (bypass cache)
- ✅ UncachedClient passed to all constructors that read platform-db-superuser, platform-db-ca, infisical-auth
- ✅ Build passes successfully

---

## Task 14: RBAC Configuration ✅

**Objective:** Define ClusterRole and ClusterRoleBinding with minimal required permissions.

### Subtasks:

- [x] 14.1 Create config/rbac/role.yaml with ClusterRole
- [x] 14.2 Add permissions for HubEnvironment CRD (get, list, watch, update, patch)
- [x] 14.3 Add permissions for HubEnvironment status (get, update, patch)
- [x] 14.4 Add permissions for Secrets (get, list, watch, create, update, patch, delete)
- [x] 14.5 Add permissions for ConfigMaps (get, list, watch)
- [x] 14.6 Add permissions for CNPG Cluster (get, list, watch)
- [x] 14.7 Add permissions for Deployments (get, list, watch, patch)
- [x] 14.8 Add permissions for StatefulSets (get, list, watch, patch)
- [x] 14.9 Add permissions for Leases (leader election)
- [x] 14.10 Add permissions for Events (create, patch)
- [x] 14.11 Create config/rbac/role_binding.yaml
- [x] 14.12 Generate RBAC manifests: `make manifests`

**Acceptance Criteria:**
- ✅ ClusterRole has minimal required permissions
- ✅ ClusterRoleBinding references correct ServiceAccount
- ✅ Leader election permissions included (generated by Kubebuilder)
- ✅ Patch permissions for Deployments/StatefulSets (restart annotation)
- ✅ Build passes successfully

---

## Task 15: Deployment Manifests ✅

**Objective:** Create production-ready deployment manifests with HA configuration.

### Subtasks:

- [x] 15.1 Create manifests/deployment.yaml with 2 replicas
- [x] 15.2 Configure leader election (--leader-elect=true)
- [x] 15.3 Add sync-wave annotation (argocd.argoproj.io/sync-wave: "1")
- [x] 15.4 Configure resource requests (100m CPU, 128Mi memory)
- [x] 15.5 Configure resource limits (500m CPU, 512Mi memory)
- [x] 15.6 Add liveness probe (/healthz on port 8081)
- [x] 15.7 Add readiness probe (/readyz on port 8081)
- [x] 15.8 Configure environment variables (INFISICAL_BASE_URL, HYDRA_BASE_URL, NATS_URL)
- [x] 15.9 Add security context (runAsNonRoot, drop ALL capabilities)
- [x] 15.10 Create manifests/service_account.yaml
- [x] 15.11 Create manifests/rbac.yaml
- [x] 15.12 Create manifests/kustomization.yaml

**Acceptance Criteria:**
- ✅ Deployment runs 2 replicas for HA
- ✅ Leader election configured
- ✅ Health probes configured
- ✅ Security context enforces least privilege
- ✅ All manifests follow GitOps structure

---

## Task 16: E2E Test Suite - Secret Zero Generation ✅

**Objective:** Create KUTTL E2E test for Secret Zero generation phase.

### Subtasks:

- [x] 16.1 Create tests/e2e/01-secret-zero-generation/ directory
- [x] 16.2 Create 00-install.yaml with ArgoCD Application for operator
- [x] 16.3 Create 00-install.yaml with HubEnvironment CR
- [x] 16.4 Create 00-assert.yaml asserting infisical-secrets exists
- [x] 16.5 Create 00-assert.yaml asserting platform-db-app exists (BasicAuth type)
- [x] 16.6 Create 00-assert.yaml asserting infisical-db-credentials exists
- [x] 16.7 Create 00-assert.yaml asserting platform-db-ca exists (self-signed CA)
- [x] 16.8 Create 00-assert.yaml asserting hydra-db-credentials exists
- [x] 16.9 Create 00-assert.yaml asserting kratos-db-credentials exists
- [x] 16.10 Create 00-assert.yaml asserting keto-db-credentials exists
- [x] 16.11 Create 00-assert.yaml asserting SecretZeroGenerated condition is True
- [x] 16.12 Create verify.sh bash script (read-only assertions)
- [x] 16.13 Verify ENCRYPTION_KEY is 32 characters
- [x] 16.14 Verify AUTH_SECRET is 32 characters
- [x] 16.15 Verify REDIS_URL format
- [x] 16.16 Verify DB_ROOT_CERT contains "BEGIN CERTIFICATE"
- [x] 16.17 Verify platform-db-ca contains valid X.509 certificate
- [x] 16.18 Verify ownerReferences are set correctly

**Acceptance Criteria:**
- ✅ Test deploys via ArgoCD (no kubectl apply)
- ✅ All secrets asserted with correct structure
- ✅ verify.sh validates secret contents (read-only)
- ✅ ownerReferences and db-credentials labels verified

---

## Task 17: E2E Test Suite - Database Setup ✅

**Objective:** Create KUTTL E2E test for database migrations and role provisioning.

### Subtasks:

- [x] 17.1 Create tests/e2e/02-database-setup/ directory
- [x] 17.2 Create 00-install.yaml with CNPG Cluster via ArgoCD
- [x] 17.3 Create 01-assert.yaml asserting MigrationsComplete condition is True
- [x] 17.4 Create 02-assert.yaml asserting DatabaseRolesConfigured condition is True
- [x] 17.5 Create verify.sh bash script (read-only SQL queries)
- [x] 17.6 Verify migrations ran (check schema_migrations table)
- [x] 17.7 Verify mcp_server role exists
- [x] 17.8 Verify infisical role exists
- [x] 17.9 Verify spoke_controller role exists
- [x] 17.10 Verify spire_server role exists
- [x] 17.11 Verify hydra role exists
- [x] 17.12 Verify kratos role exists
- [x] 17.13 Verify keto role exists
- [x] 17.14 Verify role permissions (has_schema_privilege checks)

**Acceptance Criteria:**
- ✅ Test deploys CNPG via ArgoCD (no kubectl apply)
- ✅ MigrationsComplete condition asserted
- ✅ DatabaseRolesConfigured condition asserted
- ✅ All 7 roles verified via read-only SQL
- ✅ verify.sh is read-only (kubectl exec psql only)

---

## Task 18: E2E Test Suite - External Services Configuration ✅

**Objective:** Create KUTTL E2E test for Infisical upload, OAuth registration, and NATS streams.

### Subtasks:

- [x] 18.1 Create tests/e2e/03-external-services/ directory
- [x] 18.2 Create 00-install.yaml with Infisical Deployment via ArgoCD
- [x] 18.3 Create 00-install.yaml with Hydra Deployment via ArgoCD
- [x] 18.4 Create 00-install.yaml with NATS StatefulSet via ArgoCD
- [x] 18.5 Create 01-assert.yaml asserting SecretsBackedUp condition is True
- [x] 18.6 Create 02-assert.yaml asserting OAuthClientsRegistered condition is True
- [x] 18.7 Create 03-assert.yaml asserting NATSStreamsConfigured condition is True
- [x] 18.8 Create 04-assert.yaml asserting Ready condition is True
- [x] 18.9 Create verify.sh bash script (read-only API queries)
- [x] 18.10 Verify secrets exist in Infisical via API
- [x] 18.11 Verify OAuth clients exist in Hydra via API
- [x] 18.12 Verify NATS streams exist via NATS CLI
- [x] 18.13 Verify UploadedSecrets array populated in status

**Acceptance Criteria:**
- ✅ Test deploys all services via ArgoCD (no kubectl apply)
- ✅ All 4 conditions asserted sequentially
- ✅ verify.sh is read-only (curl + kubectl exec only)
- ✅ UploadedSecrets status verified

---

## Task 19: E2E Test Suite - Certificate Rotation ✅

**Objective:** Create KUTTL E2E test for platform-db-ca rotation and service restart.

### Subtasks:

- [x] 19.1 Create tests/e2e/04-certificate-rotation/ directory
- [x] 19.2 Create 00-install.yaml that triggers rotation via ArgoCD sync from main (GitOps-compliant)
- [x] 19.3 Create 01-assert.yaml asserting DB_ROOT_CERT updated in infisical-secrets
- [x] 19.4 Create 02-assert.yaml asserting Infisical Deployment has restartedAt annotation
- [x] 19.5 Create 02-assert.yaml asserting Hydra Deployment has restartedAt annotation
- [x] 19.6 Create 02-assert.yaml asserting Kratos Deployment has restartedAt annotation
- [x] 19.7 Create 02-assert.yaml asserting Keto Deployment has restartedAt annotation
- [x] 19.8 Create 02-assert.yaml asserting SPIRE Server StatefulSet has restartedAt annotation
- [x] 19.9 Create 02-assert.yaml asserting MCP Server Deployment has restartedAt annotation
- [x] 19.10 Create verify.sh bash script (check pod restart times)

**Acceptance Criteria:**
- ✅ Rotation triggered via ArgoCD sync from main (no direct secret mutation)
- ✅ Real X.509 CA fixture committed to repo (fixtures/platform-db-ca-rotated.yaml)
- ✅ DB_ROOT_CERT update asserted
- ✅ All 6 services asserted with restartedAt annotation
- ✅ verify.sh is read-only (kubectl get/logs only)

---

## Task 20: E2E Test Suite - Password Rotation ✅

**Objective:** Create KUTTL E2E test for ESO-driven password rotation and service restart.

### Subtasks:

- [x] 20.1 Create tests/e2e/05-password-rotation/ directory
- [x] 20.2 Create 00-install.yaml that triggers rotation via ArgoCD sync from main (GitOps-compliant)
- [x] 20.3 Create 01-assert.yaml asserting consuming Deployment has restartedAt annotation
- [x] 20.4 Create verify.sh bash script (verify ALTER ROLE executed)
- [x] 20.5 Verify new password works for database connection
- [x] 20.6 Verify service restarted and is healthy

**Acceptance Criteria:**
- ✅ Rotation triggered via ArgoCD sync from main (no direct secret mutation)
- ✅ Real credential fixture committed to repo (fixtures/infisical-db-credentials-rotated.yaml)
- ✅ restartedAt annotation asserted on Infisical Deployment
- ✅ verify.sh is read-only (psql authentication test, kubectl get only)

---

## Task 21: E2E Test Suite - Resource Pruning

**Objective:** Create KUTTL E2E test for orphaned resource deletion.

### Subtasks:

- [ ] 21.1 Create tests/e2e/06-resource-pruning/ directory
- [ ] 21.2 Create 00-install.yaml with HubEnvironment CR containing 3 roles
- [ ] 21.3 Create 01-assert.yaml asserting 3 roles exist
- [ ] 21.4 Create 02-install.yaml updating CR to remove 1 role
- [ ] 21.5 Create 03-assert.yaml asserting only 2 roles exist
- [ ] 21.6 Repeat for OAuth clients (add 2, remove 1)
- [ ] 21.7 Repeat for NATS streams (add 2, remove 1)
- [ ] 21.8 Create verify.sh bash script (verify orphaned resources deleted)

**Acceptance Criteria:**
- Orphaned database roles deleted
- Orphaned OAuth clients deleted
- Orphaned NATS streams deleted
- Only resources in CR spec remain

---

## Task 22: E2E Test Suite - Dirty Database Recovery

**Objective:** Create KUTTL E2E test for dirty database state and manual recovery.

### Subtasks:

- [ ] 22.1 Create tests/e2e/07-dirty-database-recovery/ directory
- [ ] 22.2 Create 00-install.yaml that causes dirty database state
- [ ] 22.3 Create 01-assert.yaml asserting MigrationsComplete condition is False with reason "DirtyDatabase"
- [ ] 22.4 Create 02-install.yaml that adds reconcile-trigger annotation
- [ ] 22.5 Create 03-assert.yaml asserting reconciliation resumed
- [ ] 22.6 Create verify.sh bash script (verify manual recovery procedure)

**Acceptance Criteria:**
- Dirty database state detected
- Operator does not requeue automatically
- reconcile-trigger annotation resumes reconciliation
- Manual recovery procedure documented

---

## Task 23: CLI Refactoring

**Objective:** Remove Day-2 logic from CLI and update to use operator.

### Subtasks:

- [ ] 23.1 Delete InstallInfisicalSecrets() from internal/hub/components/installer.go
- [ ] 23.2 Delete InstallPostgresConnectionSecret() from internal/hub/components/installer.go
- [ ] 23.3 Delete InstallPlatformDatabaseCredentials() from internal/hub/components/installer.go
- [ ] 23.4 Delete InstallSPIREServerCredentials() from internal/hub/components/installer.go
- [ ] 23.5 Delete RestartPlatformWorkloads() from internal/hub/components/installer.go
- [ ] 23.6 Update CLI to only handle CAPI bootstrap and ArgoCD installation
- [ ] 23.7 Update CLI to exit after ArgoCD installation
- [ ] 23.8 Update CLI documentation to reflect operator-driven Day-2 operations

**Acceptance Criteria:**
- CLI no longer generates Secret Zero
- CLI no longer executes database migrations
- CLI no longer creates database roles
- CLI exits after ArgoCD installation
- CLI documentation updated

---

## Task 24: Manifest Cleanup

**Objective:** Delete bash jobs replaced by operator.

### Subtasks:

- [ ] 24.1 Delete manifests/hub-core-services/platform-database/setup-platform-roles-job.yaml
- [ ] 24.2 Delete manifests/hub-core-services/platform-database/password-rotation-job.yaml
- [ ] 24.3 Delete manifests/hub-core-services/platform-database/setup-infisical-role-job.yaml
- [ ] 24.4 Delete manifests/hub-core-services/nats/init-streams-job.yaml
- [ ] 24.5 Update ArgoCD Applications to remove deleted jobs
- [ ] 24.6 Update sync-wave documentation

**Acceptance Criteria:**
- All bash jobs deleted
- ArgoCD Applications updated
- No references to deleted jobs remain

---

## Task 25: ArgoCD Integration

**Objective:** Integrate hub-operator into ArgoCD app-of-apps.

### Subtasks:

- [ ] 25.1 Create ArgoCD Application manifest for hub-operator
- [ ] 25.2 Set sync-wave to 1 (after CRDs, before infrastructure)
- [ ] 25.3 Configure automated sync policy
- [ ] 25.4 Add to app-of-apps.yaml
- [ ] 25.5 Create ArgoCD Application manifest for HubEnvironment CR
- [ ] 25.6 Set sync-wave to 1 (same as operator)
- [ ] 25.7 Test deployment via ArgoCD on dev cluster

**Acceptance Criteria:**
- Operator deploys via ArgoCD
- HubEnvironment CR deploys via ArgoCD
- Sync waves enforce correct order
- No kubectl apply used

---

## Task 26: Documentation

**Objective:** Create comprehensive operator documentation.

### Subtasks:

- [ ] 26.1 Create operators/hub-operator/README.md
- [ ] 26.2 Document operator architecture and design
- [ ] 26.3 Document HubEnvironment CR schema and examples
- [ ] 26.4 Document manual recovery procedures (dirty database)
- [ ] 26.5 Document troubleshooting guide
- [ ] 26.6 Document KUTTL test execution
- [ ] 26.7 Document development workflow (GitOps-first)
- [ ] 26.8 Document RBAC requirements
- [ ] 26.9 Document memory optimization patterns
- [ ] 26.10 Create architecture diagrams

**Acceptance Criteria:**
- README.md covers all operator features
- Manual recovery procedures documented
- Troubleshooting guide includes common issues
- Development workflow enforces GitOps-first

---

## Task 27: CI/CD Pipeline

**Objective:** Set up GitHub Actions for operator build, test, and release.

### Subtasks:

- [ ] 27.1 Create .github/workflows/hub-operator-ci.yaml
- [ ] 27.2 Configure Go build and test jobs
- [ ] 27.3 Configure KUTTL E2E test job
- [ ] 27.4 Configure Docker image build and push
- [ ] 27.5 Configure semantic versioning and releases
- [ ] 27.6 Add status badges to README.md

**Acceptance Criteria:**
- CI runs on every PR
- E2E tests run in CI
- Docker images published to ghcr.io
- Releases automated with semantic versioning

---

## Task 28: Production Deployment

**Objective:** Deploy hub-operator to production Hub cluster.

### Subtasks:

- [ ] 28.1 Review and approve all E2E test results
- [ ] 28.2 Create production HubEnvironment CR
- [ ] 28.3 Deploy operator via ArgoCD to production
- [ ] 28.4 Monitor operator logs for errors
- [ ] 28.5 Verify Secret Zero generation
- [ ] 28.6 Verify database migrations
- [ ] 28.7 Verify database roles
- [ ] 28.8 Verify Infisical uploads
- [ ] 28.9 Verify OAuth clients
- [ ] 28.10 Verify NATS streams
- [ ] 28.11 Verify Ready condition
- [ ] 28.12 Monitor for 24 hours

**Acceptance Criteria:**
- Operator deployed successfully
- All phases complete without errors
- Ready condition is True
- No manual intervention required
- Platform services healthy


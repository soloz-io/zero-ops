# Web Research Topics for Design Phase

This document contains verified information from the current hub-operator codebase that will inform the design.md creation. Only topics with 100% clarity from the codebase are included.

## 1. Current ENCRYPTION_KEY Generation

### Current Implementation
- **Function**: `GenerateSecurePassword()` in `internal/secrets/generator.go`
- **Format**: 32-character hexadecimal string (16 random bytes encoded as hex)
- **Generation**: Uses `crypto/rand.Read()` for cryptographically secure random bytes
- **Encoding**: `hex.EncodeToString(bytes)` - produces lowercase hex characters [0-9a-f]

### Current Problem
- `GenerateInfisicalSecrets()` always generates a NEW key on every invocation
- No backup mechanism exists
- No restore mechanism exists
- No validation of key format before use

### Secret Structure
The `infisical-secrets` secret contains 4 fields:
1. `ENCRYPTION_KEY` - 32 hex characters
2. `AUTH_SECRET` - 32 hex characters
3. `REDIS_URL` - Constructed from Redis password
4. `DB_ROOT_CERT` - Base64-encoded CA certificate

## 2. HubEnvironment Status Conditions

### Current Implementation
- **API Type**: `metav1.Condition` from `k8s.io/apimachinery/pkg/apis/meta/v1`
- **Storage**: `HubEnvironment.Status.Conditions` (array of conditions)
- **Operations**:
  - Check: `meta.IsStatusConditionTrue(hubEnv.Status.Conditions, "ConditionType")`
  - Set: `meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{...})`

### Existing Condition Types
The operator currently uses these condition types:
- `BootstrapSecretsGenerated` - Bootstrap secrets created
- `ApplicationSecretsReady` - ESO-created secrets exist
- `MigrationsComplete` - Database migrations executed
- `DatabaseRolesConfigured` - Database roles created
- `SecretsBackedUp` - Secrets uploaded to Infisical
- `OAuthClientsRegistered` - OAuth clients registered
- `NATSStreamsConfigured` - NATS streams created
- `Ready` - All phases complete
- `CertificateRotationFailed` - Certificate rotation failed
- `CertificateRotationInProgress` - Certificate rotation in progress

### Condition Structure
```go
metav1.Condition{
    Type:               "ConditionType",
    Status:             metav1.ConditionTrue,  // or ConditionFalse
    Reason:             "ReasonCode",
    Message:            "Human-readable message",
    ObservedGeneration: hubEnv.Generation,
}
```

## 3. Bootstrap Idempotency Pattern

### Current Implementation
The operator implements idempotency through `GenerateBootstrapSecrets()`:

**Pattern**:
1. Accept `existingSecrets map[string]*corev1.Secret` parameter
2. For each secret to generate:
   - Check if it exists in `existingSecrets` map
   - If exists: Reuse the existing secret data, return `nil` for that secret
   - If not exists: Generate new secret
3. Return only secrets that need to be created

**Example from code**:
```go
if existing, ok := existingSecrets["platform-db-ca"]; ok {
    // Reuse existing CA certificate
    result.PlatformDBCA = nil
    caCert = existing.Data["ca.crt"]
} else {
    // Generate new CA certificate
    platformDBCA, cert, err := GeneratePlatformDBCA(dataNamespace, owner)
    result.PlatformDBCA = platformDBCA
    caCert = cert
}
```

### Reading Existing Secrets
The controller uses `UncachedClient` to read secret data:
```go
existingSecrets := make(map[string]*corev1.Secret)
for _, name := range secretNames {
    secret := &corev1.Secret{}
    if err := r.UncachedClient.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, secret); err == nil {
        existingSecrets[name] = secret
    }
}
```

## 4. Day 0 vs Day 2 Operations in Current Codebase

### Day 0 Operations (Currently in Operator)
The operator currently handles these bootstrap operations:
- Generate bootstrap secrets (Phase 1)
- Run database migrations (Phase 2)
- Create database roles (Phase 2)
- Upload secrets to Infisical (Phase 3)
- Register OAuth clients (Phase 3)
- Create NATS streams (Phase 3)

### Day 2 Operations (Currently in Operator - SHOULD BE REMOVED)
The operator currently handles these Day 2 operations that should be delegated:

**Certificate Rotation** (`handleCertificateRotation()`):
- Monitors `platform-db-ca` secret for changes
- Updates `DB_ROOT_CERT` in `infisical-secrets`
- Restarts 7 services (Infisical, Redis, Hydra, Kratos, Keto, SPIRE, MCP)
- Uses `restartDeployment()` and `restartStatefulSet()` functions

**Password Rotation** (`handlePasswordRotation()`):
- Monitors ESO-managed database credential secrets
- Executes `ALTER ROLE` in PostgreSQL
- Restarts consuming services
- Maps role names to service deployments

**Pod Restart Functions**:
- `restartDeployment()` - Patches deployment with restart annotation
- `restartStatefulSet()` - Patches statefulset with restart annotation

### Scope Violation
The requirements document states the operator should ONLY handle Day 0 operations. The current implementation violates this by handling certificate rotation, password rotation, and pod restarts.

## 5. Kubernetes Finalizers

### Current State
- **RBAC Permission**: `//+kubebuilder:rbac:groups=ops.nutgraf.in,resources=hubenvironments/finalizers,verbs=update`
- **Implementation**: No finalizer logic currently exists in the codebase
- **DeletionTimestamp Handling**: Not currently implemented

### Standard Pattern (Not Yet Implemented)
Finalizers prevent resource deletion until cleanup is complete:
1. Add finalizer to resource metadata
2. On deletion, Kubernetes sets `DeletionTimestamp` but doesn't delete
3. Controller performs cleanup
4. Controller removes finalizer
5. Kubernetes deletes the resource

## 6. Operator Reconciliation Flow

### Current Phase Sequence
1. **Phase 0**: Bootstrap Infisical (check readiness, create admin/project/machine identity)
2. **Phase 1**: Generate bootstrap secrets (if `BootstrapSecretsGenerated` not true)
3. **Phase 1b**: Wait for ESO to create application secrets (if `ApplicationSecretsReady` not true)
4. **Phase 2**: Run database migrations (if `MigrationsComplete` not true)
5. **Phase 2**: Create database roles (if `DatabaseRolesConfigured` not true)
6. **Phase 3**: Upload secrets to Infisical (if `SecretsBackedUp` not true)
7. **Phase 3**: Register OAuth clients (if `OAuthClientsRegistered` not true)
8. **Phase 3**: Create NATS streams (if `NATSStreamsConfigured` not true)
9. **Ready**: Set Ready condition (if `Ready` not true)
10. **Continuous**: Handle certificate rotation (runs after Ready)
11. **Continuous**: Handle password rotation (runs after Ready)

### Reconciliation Trigger
The operator supports manual reconciliation via annotation:
```go
if triggerTime, ok := hubEnv.Annotations["ops.nutgraf.in/reconcile-trigger"]; ok {
    // Clear ALL conditions and uploaded secrets to force full Phase 1 re-run
    hubEnv.Status.Conditions = []string{}
    hubEnv.Status.UploadedSecrets = []string{}
    delete(hubEnv.Annotations, "ops.nutgraf.in/reconcile-trigger")
}
```

## 7. Secret Namespace Organization

### Current Namespaces
- **Data Namespace**: `hubEnv.Spec.Database.Namespace` (e.g., `hub-platform-data`)
  - `platform-db-ca` - CA certificate
  - `platform-db-app` - CNPG superuser credentials
  - `infisical-db-credentials` - Infisical database user
  - `infisical-redis-credentials` - Redis authentication
  
- **Security Namespace**: `hub-platform-security` (hardcoded)
  - `infisical-secrets` - Infisical encryption keys
  - `infisical-postgres-connection` - Infisical DB connection string

- **Identity Namespace**: `hub-platform-identity`
  - `hydra-db-credentials` - Hydra database credentials (ESO-managed)

## 8. Owner References

### Current Implementation
All generated secrets have owner references to the HubEnvironment CR:
```go
owner := metav1.OwnerReference{
    APIVersion: hubEnv.APIVersion,
    Kind:       hubEnv.Kind,
    Name:       hubEnv.Name,
    UID:        hubEnv.UID,
    Controller: func() *bool { b := true; return &b }(),
}
```

This ensures secrets are deleted when the HubEnvironment is deleted (garbage collection).

## 9. UncachedClient Usage

### Current Pattern
The operator uses two clients:
- **Cached Client** (`r.Client`): For metadata-only reads
- **Uncached Client** (`r.UncachedClient`): For reading secret data

**Rule**: Always use `UncachedClient` when reading secret `.Data` or `.StringData` fields to avoid cache stripping sensitive data.

Example:
```go
secret := &corev1.Secret{}
if err := r.UncachedClient.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, secret); err == nil {
    password := secret.Data["password"]  // Only works with UncachedClient
}
```

## External Research Findings

### 10. AWS Authentication Strategy (CRITICAL ARCHITECTURAL DECISION)

**DECISION: Use Static IAM Credentials (Option B), NOT IRSA (Option A)**

**Rationale from Codebase Analysis**:

1. **Hetzner Cluster NOT Configured for IRSA**:
   - `xrds/clusterclass/spokepool-hetzner-v1.yaml` shows NO OIDC flags in `apiServer.extraArgs`
   - Missing required flags:
     - `--service-account-issuer`
     - `--service-account-jwks-uri`
     - `ServiceAccountIssuerDiscovery` feature gate
   - Kubernetes API server's OIDC discovery endpoint is NOT publicly exposed

2. **Ory Hydra is NOT the Solution**:
   - Hydra URL: `https://auth.nutgraf.in` (from `manifests/platform-identity/ory-hydra/values.yaml`)
   - Hydra JWKS: `https://auth.nutgraf.in/.well-known/jwks.json` (publicly accessible)
   - **CRITICAL GOTCHA**: AWS IRSA requires the **Kubernetes API Server's** ServiceAccount issuer, NOT Ory Hydra
   - Hydra issues OAuth tokens for users/clients, NOT Kubernetes ServiceAccount tokens
   - Projected ServiceAccount tokens are signed by K8s API server, not Hydra

3. **No Existing AWS Infrastructure**:
   - Zero AWS dependencies in codebase (no Terraform, ARNs, or regions)
   - No AWS account ID, region, or IAM roles configured
   - Platform is 100% Hetzner/Kubernetes-native

4. **IRSA Would Require Massive Changes**:
   - Reconfigure Hetzner cluster API server (requires cluster restart)
   - Expose Kubernetes OIDC discovery endpoint publicly (security risk)
   - Create AWS IAM OIDC provider and roles (out-of-band manual work)
   - Test and validate OIDC federation (significant effort)

**Chosen Approach: Static IAM User Credentials via CLI Injection**

**Implementation Pattern** (Following Existing GitHub Secrets Pattern):

1. **CLI Command**: `hub configure-aws-secrets-manager`
   - Accepts AWS credentials as flags
   - Creates K8s secret via client-go (Secret Zero)
   - Secret name: `hub-operator-aws-credentials`
   - Namespace: `hub-platform-ops`

2. **Hub-Operator Uploads to Infisical**:
   - Reads `hub-operator-aws-credentials` secret
   - Uploads to Infisical (makes Infisical Source of Truth)
   - Keys: `aws-access-key-id`, `aws-secret-access-key`, `aws-region`

3. **ExternalSecret Syncs from Infisical**:
   - Recreates `hub-operator-aws-credentials` from Infisical
   - GitOps-managed thereafter
   - Future credential rotations happen in Infisical

4. **Hub-Operator Deployment References Secret**:
   - Environment variables injected via `secretKeyRef`
   - AWS SDK automatically picks up credentials
   - No code changes needed for authentication

**IAM Policy** (Least Privilege):
```json
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Action": [
      "secretsmanager:GetSecretValue",
      "secretsmanager:PutSecretValue",
      "secretsmanager:CreateSecret",
      "secretsmanager:DescribeSecret"
    ],
    "Resource": "arn:aws:secretsmanager:REGION:ACCOUNT_ID:secret:/hub-operator/*"
  }]
}
```

**Kubernetes Secret** (Created by CLI):
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: hub-operator-aws-credentials
  namespace: hub-platform-ops
  labels:
    app.kubernetes.io/managed-by: zero-ops-hub-cli
    app.kubernetes.io/component: secret-zero
    app.kubernetes.io/part-of: hub-operator
type: Opaque
stringData:
  AWS_ACCESS_KEY_ID: "AKIA..."
  AWS_SECRET_ACCESS_KEY: "..."
  AWS_REGION: "ap-south-1"
```

**Deployment Environment Variables**:
```yaml
env:
- name: AWS_ACCESS_KEY_ID
  valueFrom:
    secretKeyRef:
      name: hub-operator-aws-credentials
      key: AWS_ACCESS_KEY_ID
- name: AWS_SECRET_ACCESS_KEY
  valueFrom:
    secretKeyRef:
      name: hub-operator-aws-credentials
      key: AWS_SECRET_ACCESS_KEY
- name: AWS_REGION
  valueFrom:
    secretKeyRef:
      name: hub-operator-aws-credentials
      key: AWS_REGION
```

**Security Considerations**:
- Credentials stored in etcd (encrypted at rest)
- Limit IAM user to Secrets Manager only (no other AWS services)
- Use resource-based policy to restrict secret paths
- Rotate credentials via Infisical (GitOps workflow)
- Monitor CloudTrail for unauthorized access

**Consistency with Existing Patterns**:
- Follows exact same pattern as GitHub secrets (`hub configure-eso`)
- Uses Secret Zero pattern (CLI → K8s → Infisical → ExternalSecret)
- Operator-driven, no bash scripts in production
- GitOps compliant after initial bootstrap

**Future Enhancement**:
- If IRSA becomes a requirement, it can be added later after:
  - Hetzner cluster API server reconfiguration
  - Public OIDC discovery endpoint setup
  - AWS IAM OIDC provider creation

**Sources**: 
- Codebase analysis: `xrds/clusterclass/spokepool-hetzner-v1.yaml`
- Codebase analysis: `manifests/platform-identity/ory-hydra/values.yaml`
- Codebase analysis: `cmd/hub/configure_eso.go` (pattern reference)
- Codebase analysis: `internal/hub/components/secrets.go` (pattern reference)
- Archived project analysis: `scripts/bootstrap/helpers/s3-helpers.sh` (static keys confirmation)

### 11. AWS SDK for Go v2 - Retry and Error Handling

**Default Retry Behavior**:
- AWS SDK for Go v2 uses `retry.Standard` retryer by default
- Default max attempts: 3
- Default max backoff delay: 20 seconds
- Uses exponential backoff with jitter automatically

**Configuring Retries**:
```go
import (
    "github.com/aws/aws-sdk-go-v2/aws/retry"
    "github.com/aws/aws-sdk-go-v2/config"
)

// Custom max attempts
cfg, err := config.LoadDefaultConfig(context.TODO(), 
    config.WithRetryer(func() aws.Retryer {
        return retry.AddWithMaxAttempts(retry.NewStandard(), 5)
    }))

// Custom max backoff delay
cfg, err := config.LoadDefaultConfig(context.TODO(), 
    config.WithRetryer(func() aws.Retryer {
        return retry.AddWithMaxBackoffDelay(retry.NewStandard(), 5*time.Second)
    }))
```

**Retryable Errors**:
- Network errors (connection timeouts, DNS failures)
- HTTP 5xx errors (server-side errors)
- Throttling errors (rate limiting)
- Specific AWS error codes (configurable)

**Client-Side Rate Limiting**:
- Token bucket with 500 capacity (default)
- Timeout errors cost 10 tokens
- Other retryable errors cost 5 tokens
- Successful first attempts add 1 token back
- Can be disabled with `ratelimit.None`

**Timeouts**:
- Use `context.WithTimeout()` or `context.WithDeadline()`
- SDK respects context cancellation
- No automatic retry if context is cancelled

**Best Practices**:
- Set reasonable max attempts (3-5 for most cases)
- Use context timeouts for overall operation deadlines
- Monitor retry metrics in production
- Consider exponential backoff for custom retry logic

**Sources**:
- [AWS SDK for Go v2 - Retries and Timeouts](https://docs.aws.amazon.com/sdk-for-go/v2/developer-guide/configure-retries-timeouts.html)
- [Collection of retry patterns for SDK calls in AWS SDK for Go v2](https://dev.to/aws-builders/collection-of-retry-patterns-for-sdk-calls-in-aws-sdk-for-go-v2-14p4)

### 12. Infisical ENCRYPTION_KEY Specifications

**Official Requirements** (from Infisical documentation):
- **Format**: Random 16-byte hex string (32 hex characters)
- **Generation**: `openssl rand -hex 16`
- **Purpose**: Encrypts the KMS root key in the database
- **Immutability**: Cannot be rotated without re-encrypting all data

**Technical Details**:
- 16 bytes = 128 bits of entropy
- Hex encoding produces 32 characters (0-9, a-f)
- Used for AES-128-GCM encryption (standard encryption strategy)
- Protects KMS keys stored in PostgreSQL database

**Validation Requirements**:
1. Must be exactly 32 characters long
2. Must be valid hexadecimal (characters 0-9, a-f)
3. Must not be all zeros (invalid key)
4. Must be cryptographically random (not predictable)

**Impact of Key Mismatch**:
- Database contains data encrypted with old key
- New key cannot decrypt existing data
- Results in complete data loss for encrypted secrets
- Requires database wipe and re-initialization
- All secrets must be re-uploaded after recovery

**Critical Constraint**:
- ENCRYPTION_KEY is a master encryption key, NOT a rotatable password
- Must be preserved across pod restarts and secret deletions
- Backup and restore are essential for disaster recovery

**Sources**:
- [Infisical Environment Variables](https://infisical.com/docs/self-hosting/configuration/envars)
- [Infisical HSM Integration](https://infisical.com/docs/documentation/platform/kms/hsm-integration)

### 13. Infisical AUTH_SECRET Specifications

**Official Requirements** (from Infisical documentation):
- **Format**: Random 32-byte base64 string
- **Generation**: `openssl rand -base64 32`
- **Purpose**: Signs and verifies JWT tokens for authentication

**Technical Details**:
- 32 bytes = 256 bits of entropy
- Base64 encoding produces ~44 characters
- Used for HMAC-based JWT signing (likely HS256 or HS512)
- Signs tokens for user sessions and machine identities

**JWT Signing Context**:
- JWT secret keys use HMAC algorithms (HS256, HS384, HS512)
- HS256 requires 32 bytes (256 bits) minimum
- HS512 requires 64 bytes (512 bits) for optimal security
- Secret is combined with header and payload to produce signature

**Impact of AUTH_SECRET Regeneration**:
- All active JWT tokens become invalid immediately
- User sessions are terminated (forced logout)
- Machine Identity tokens (ESO) stop working
- Causes cluster-wide authentication outage
- All services using Infisical authentication fail

**Critical Constraint**:
- AUTH_SECRET must be preserved alongside ENCRYPTION_KEY
- Regeneration breaks all active sessions and integrations
- Backup and restore are essential to prevent auth outages

**Validation Requirements**:
1. Must be valid base64 string
2. Should be at least 32 bytes when decoded
3. Must not be empty or all zeros
4. Must be cryptographically random

**Note**: The requirements document specifies 32 hex characters for AUTH_SECRET, but Infisical documentation specifies 32-byte base64. This discrepancy needs clarification during design phase.

**Sources**:
- [Infisical Environment Variables](https://infisical.com/docs/self-hosting/configuration/envars)
- [JWT Secret Key Generator Guide](https://selfdevkit.com/blog/jwt-secret-key-generator/)
- [JWT Secret Key Best Practices](https://zerowp.com/jwt-secret-generator)


## 14. Critical Codebase Findings

### AUTH_SECRET Format (RESOLVED)

**Current Implementation**: 32 hex characters (NOT base64)
- `internal/secrets/generator.go` uses `GenerateSecurePassword()`
- Generates 16 random bytes, encodes as hex → 32 characters
- Current deployment uses this format successfully
- **DO NOT change to base64** - would break existing deployment

### Bootstrap Status Condition (CORRECTION NEEDED)

**Spec Says**: `SecretZeroGenerated`
**Codebase Uses**: `BootstrapSecretsGenerated`
- Found in `internal/controller/hubenvironment_controller.go` (Line 126)
- **Action Required**: Update requirements.md to use correct condition name

### Partial Secret Deletion Bug (CRITICAL)

**Current Behavior** (`internal/secrets/generator.go` Lines 275-290):
- If `infisical-secrets` is deleted, code generates NEW Redis password
- Creates NEW `infisical-redis-credentials` secret
- **Breaks Redis StatefulSet** until restart

**Required Fix**:
- Check if `infisical-redis-credentials` exists before generating new password
- When restoring `infisical-secrets`, read EXISTING Redis password
- Reconstruct `REDIS_URL` using existing password, not new one

### Secret Reconstruction Order

**Current Logic** (`internal/secrets/generator.go` Lines 249-307):
1. Check if secret exists in `existingSecrets` map
2. If missing, generate new secret
3. No validation of dependencies

**Required Logic for Restore**:
1. Check AWS Secrets Manager for backup
2. If backup exists, restore ENCRYPTION_KEY and AUTH_SECRET
3. Read EXISTING `infisical-redis-credentials` for Redis password
4. Read EXISTING `platform-db-ca` for DB_ROOT_CERT
5. Reconstruct `infisical-secrets` with all 4 fields:
   - ENCRYPTION_KEY (from AWS backup)
   - AUTH_SECRET (from AWS backup)
   - REDIS_URL (from existing Redis credentials)
   - DB_ROOT_CERT (from existing CA cert)

### Projected ServiceAccount Tokens

**Already Supported**: Yes
- Used by SPIRE deployment (`manifests/platform-core-services/spire/agent-daemonset.yaml` Lines 84-88)
- Kubernetes supports projected volumes
- Can be used for AWS SDK authentication (if IRSA were configured)

### No Existing AWS Infrastructure

**Finding**: Zero AWS dependencies in codebase
- No Terraform modules for AWS
- No hardcoded ARNs or regions
- No existing IAM roles or policies
- Platform is 100% Hetzner/Kubernetes-native

**Implication**: All AWS integration must be built from scratch

**Sources**:
- Codebase analysis: `internal/secrets/generator.go`
- Codebase analysis: `internal/controller/hubenvironment_controller.go`
- Codebase analysis: `xrds/clusterclass/spokepool-hetzner-v1.yaml`
- Codebase analysis: `manifests/platform-core-services/spire/agent-daemonset.yaml`

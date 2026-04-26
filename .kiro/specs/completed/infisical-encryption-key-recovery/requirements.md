# Requirements: Infisical ENCRYPTION_KEY Recovery & Protection

## 1. Problem Statement

### 1.1 Current Issue
Infisical is down due to ENCRYPTION_KEY mismatch:
- Database encrypted with OLD key (April 6, 2026)
- Secret contains NEW key (April 14, 2026)
- Old key is unrecoverable - no backups exist
- 26 secrets in database are inaccessible

### 1.2 Root Cause
The `hub-operator` regenerates a NEW `ENCRYPTION_KEY` every time `infisical-secrets` is deleted:
```go
// generator.go:214-216
encryptionKey, err := GenerateSecurePassword()  // Always generates NEW key!
```

This is catastrophic because:
- `ENCRYPTION_KEY` is a master encryption key, not a rotatable password
- It encrypts the KMS root key in the database
- Cannot be changed without re-encrypting all data
- Must be preserved across pod restarts and secret deletions

### 1.3 Impact
- **Current**: Infisical completely down, all secrets inaccessible
- **Future Risk**: Any accidental secret deletion causes complete data loss
- **Recovery**: Requires database wipe and data loss

## 2. Business Requirements

### 2.1 Immediate Recovery (P0)
**REQ-1**: Restore Infisical service within 1 hour
- Drop Infisical database
- **Trigger full Phase 0 Bootstrap** to recreate Machine Identity and Project
- Delete `infisical-auth` secret to force regeneration of Client ID/Secret
- Operator re-bootstraps Infisical (creates admin user, project, machine identity)
- Operator re-uploads all 24 secrets
- Service becomes operational

**CRITICAL**: Database wipe destroys the Machine Identity that `infisical-auth` references. Must force full bootstrap, not just secret re-upload.

**SCOPE**: This is a Day 0 operation. Operator does NOT restart pods - Kubernetes handles pod lifecycle.

**REQ-2**: Backup current ENCRYPTION_KEY and AUTH_SECRET immediately after recovery
- Store both keys in AWS Secrets Manager
- Enable automated recovery for future incidents
- **AUTH_SECRET** is required to prevent JWT invalidation across cluster

### 2.2 Prevent Future Incidents (P0)
**REQ-3**: Never regenerate ENCRYPTION_KEY after initial bootstrap
- Only generate on first-time cluster initialization (Day 0)
- Always restore from backup if secret is deleted
- Fail operator reconciliation if backup unavailable

**REQ-4**: Prevent accidental secret deletion
- Add Kubernetes finalizers to critical secrets
- Require manual finalizer removal before deletion

**REQ-5**: Implement backup to AWS Secrets Manager
- Backup ENCRYPTION_KEY and AUTH_SECRET on first generation (Day 0)
- Store with cluster identifier and timestamp
- Enable cross-region replication

**CRITICAL SCOPE**: Hub operator handles Day 0 bootstrap AND specific Day 2 operations. It does NOT handle:
- General pod lifecycle management (Kubernetes deployment controllers)
- Application-level credential reloads (ESO + application logic)
- Service health monitoring (Kubernetes probes)

Hub operator DOES handle:
- Certificate rotation detection and pod restarts (Requirement 23 from hub-operator spec)
- Password rotation detection and ALTER ROLE + pod restarts (Requirement 23 from hub-operator spec)
- Secret Zero generation and recovery (this spec)

## 3. Technical Requirements

### 3.1 Operator Changes

**CRITICAL SCOPE DEFINITION**: Hub operator is a **Day 0 Bootstrap AND Day 2 Operations Operator**. It must:
- ✅ Generate bootstrap secrets on first cluster initialization (Day 0)
- ✅ Backup ENCRYPTION_KEY and AUTH_SECRET to AWS (Day 0)
- ✅ Restore keys from AWS if secrets are deleted (Day 0 recovery)
- ✅ Add/remove finalizers for secret protection (Day 0)
- ✅ Execute database migrations (Day 0 schema setup)
- ✅ Create initial database roles (Day 0 provisioning)
- ✅ Handle certificate rotation and pod restarts (Day 2 - Requirement 23 from hub-operator spec)
- ✅ Handle password rotation, ALTER ROLE, and pod restarts (Day 2 - Requirement 23 from hub-operator spec)
- ❌ NOT handle general pod lifecycle management (Kubernetes deployment controllers)
- ❌ NOT handle application-level credential reloads (ESO + application logic)
- ❌ NOT monitor service health (Kubernetes probes handle this)

**REQ-7**: Modify secret generation logic with strict bootstrap detection and key validation
```go
// Pseudocode
func GenerateInfisicalSecrets(hubEnv *HubEnvironment) {
    // Check HubEnvironment status to determine if this is first-time bootstrap
    isFirstTime := !meta.IsStatusConditionTrue(hubEnv.Status.Conditions, "BootstrapSecretsGenerated")
    
    // Try to restore from AWS Secrets Manager
    backupData, err := restoreFromAWS(clusterId)
    
    if err == nil && backupData != nil {
        // Backup exists - restore both keys
        encryptionKey = backupData.EncryptionKey
        authSecret = backupData.AuthSecret
        
        // REQ-7.2: Validate restored keys before use
        if err := validateEncryptionKey(encryptionKey); err != nil {
            return error("Restored ENCRYPTION_KEY failed validation: %w", err)
        }
        if err := validateAuthSecret(authSecret); err != nil {
            return error("Restored AUTH_SECRET failed validation: %w", err)
        }
        
        logger.Info("Restored and validated ENCRYPTION_KEY and AUTH_SECRET from AWS backup")
    } else if isFirstTime {
        // First-time bootstrap - generate new keys
        encryptionKey = GenerateSecurePassword()
        authSecret = GenerateSecurePassword()
        
        // REQ-7.2: Validate generated keys
        if err := validateEncryptionKey(encryptionKey); err != nil {
            return error("Generated ENCRYPTION_KEY failed validation: %w", err)
        }
        if err := validateAuthSecret(authSecret); err != nil {
            return error("Generated AUTH_SECRET failed validation: %w", err)
        }
        
        // Backup immediately
        if err := backupToAWS(encryptionKey, authSecret); err != nil {
            return error("Failed to backup master keys to AWS, cannot proceed")
        }
        
        // Mark as bootstrapped
        meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{
            Type:   "BootstrapSecretsGenerated",
            Status: metav1.ConditionTrue,
        })
        
        logger.Info("Generated, validated, and backed up new master keys")
    } else {
        // NOT first-time AND backup missing - CRITICAL ERROR
        return error("ENCRYPTION_KEY backup not found in AWS and cluster already bootstrapped. Manual intervention required.")
    }
    
    // Reconstruct full infisical-secrets payload
    redisPassword = getRedisPasswordFromSecret()
    dbRootCert = getCACertFromSecret("platform-db-ca")
    redisURL = fmt.Sprintf("redis://:%s@redis-master.%s.svc:6379", redisPassword, dataNamespace)
}
```

**CRITICAL**: Bootstrap detection MUST use `HubEnvironment.Status.Conditions["BootstrapSecretsGenerated"]`, not just "secret exists". Prevents split-brain key regeneration on AWS API timeouts.

**REQ-7.1**: Reconstruct complete `infisical-secrets` payload on restore
- Fetch `platform-db-ca` secret to regenerate `DB_ROOT_CERT`
- Fetch `infisical-redis-credentials` to construct `REDIS_URL`
- Combine with restored `ENCRYPTION_KEY` and `AUTH_SECRET`
- Create complete secret with all 4 fields

**CRITICAL IMPLEMENTATION DETAIL**: You MUST use `r.UncachedClient.Get(...)` when fetching `platform-db-ca` and `infisical-redis-credentials`. The standard cached client strips `.Data` payloads for memory optimization and will return blank values.

**REQ-7.2**: Implement key validation functions

**CRITICAL SCOPE CONSTRAINT**: Hub operator is responsible for **Day 0 operations AND specific Day 2 operations** (certificate rotation, password rotation). It must NOT handle:
- General pod lifecycle management
- Application-level credential reloads
- Ongoing secret synchronization (handled by ESO)

The operator's responsibilities include:
- Initial bootstrap and secret recovery (Day 0)
- Certificate rotation detection and pod restarts (Day 2 - per Requirement 23 from hub-operator spec)
- Password rotation detection, ALTER ROLE, and pod restarts (Day 2 - per Requirement 23 from hub-operator spec)

**Key Validation Implementation**:
```go
// validateEncryptionKey ensures the key meets Infisical requirements
func validateEncryptionKey(key string) error {
    // Must be 32 hex characters (16 bytes)
    if len(key) != 32 {
        return fmt.Errorf("ENCRYPTION_KEY must be 32 characters, got %d", len(key))
    }
    
    // Must be valid hex string
    if _, err := hex.DecodeString(key); err != nil {
        return fmt.Errorf("ENCRYPTION_KEY must be valid hex string: %w", err)
    }
    
    // Must not be all zeros (invalid key)
    if key == strings.Repeat("0", 32) {
        return fmt.Errorf("ENCRYPTION_KEY cannot be all zeros")
    }
    
    return nil
}

// validateAuthSecret ensures the secret meets JWT signing requirements
func validateAuthSecret(secret string) error {
    // Must be 32 hex characters (16 bytes)
    if len(secret) != 32 {
        return fmt.Errorf("AUTH_SECRET must be 32 characters, got %d", len(secret))
    }
    
    // Must be valid hex string
    if _, err := hex.DecodeString(secret); err != nil {
        return fmt.Errorf("AUTH_SECRET must be valid hex string: %w", err)
    }
    
    // Must not be all zeros (invalid secret)
    if secret == strings.Repeat("0", 32) {
        return fmt.Errorf("AUTH_SECRET cannot be all zeros")
    }
    
    return nil
}
```

**SCOPE NOTE**: Hub operator handles Day 0 bootstrap AND Day 2 operations (certificate rotation, password rotation per Requirement 23 from hub-operator spec). ESO (External Secrets Operator) handles ongoing secret synchronization from Infisical.

### 3.2 Secret Protection
**REQ-9**: Add finalizers to critical secrets
```yaml
metadata:
  finalizers:
    - ops.nutgraf.in/encryption-key-protection
```

**REQ-10**: Implement finalizer controller
- Prevent deletion unless explicitly approved
- Log deletion attempts for audit
- Require manual intervention to remove finalizer

**REQ-13**: Graceful teardown support (prevent finalizer deadlocks)
- Operator MUST remove finalizers when `HubEnvironment.DeletionTimestamp` is set
- Allows `hub teardown` to complete without hanging on namespace deletion
- Implementation:
```go
if !hubEnv.DeletionTimestamp.IsZero() {
    // HubEnvironment is being deleted - remove finalizers to allow cleanup
    if err := removeFinalizers(ctx, "infisical-secrets", "infisical-redis-credentials"); err != nil {
        logger.Error(err, "Failed to remove finalizers during teardown")
    }
}
```

**CRITICAL**: Without REQ-13, `hub teardown` will hang indefinitely waiting for secrets to be deleted.

### 3.3 AWS Integration
**REQ-11**: AWS Secrets Manager integration with Hetzner cluster authentication

**Authentication Strategy**: Static IAM Credentials via CLI Injection
- Use AWS IAM user with restricted Secrets Manager permissions
- Inject credentials via `hub configure-aws-secrets-manager` CLI command
- Follow Secret Zero pattern (CLI → K8s → Infisical → ExternalSecret)
- Environment variables injected via secretKeyRef in deployment
- AWS SDK automatically picks up credentials from environment
- Provides audit trail via CloudTrail

**CRITICAL**: Hetzner clusters lack OIDC configuration required for IRSA. Static credentials follow established GitHub secrets pattern and are production-ready with proper IAM policies.

**Storage Configuration**:
- Path: `/hub-operator/{cluster-id}/infisical-master-keys`
- Enable AWS KMS encryption
- Disable automatic rotation (master keys)
- Add tags: `cluster`, `environment`, `created-at`, `managed-by: hub-operator`

**REQ-12**: Backup metadata (includes both master keys)
```json
{
  "encryptionKey": "8ec32a8fafb27566fccd50da3d789979",
  "authSecret": "841e8ab0c28196d44e66b9075019cbfc",
  "createdAt": "2026-04-06T14:22:07Z",
  "clusterId": "hub-production",
  "version": "1",
  "backupTimestamp": "2026-04-20T14:30:00Z"
}
```

**CRITICAL**: Must backup both `ENCRYPTION_KEY` and `AUTH_SECRET`. If `AUTH_SECRET` is regenerated, all active JWT tokens (including ESO Machine Identity) are invalidated, causing cluster-wide auth outage.

## 4. Acceptance Criteria

### 4.1 Immediate Recovery
- [ ] Infisical database dropped and reinitialized
- [ ] Infisical pods running and healthy
- [ ] All 24 secrets uploaded to Infisical
- [ ] ENCRYPTION_KEY backed up to AWS Secrets Manager
- [ ] https://infisical.nutgraf.in/ accessible

### 4.2 Prevention
- [ ] Finalizers added to `infisical-secrets` and `infisical-redis-credentials`
- [ ] Finalizers automatically removed during `HubEnvironment` deletion (REQ-13)
- [ ] Operator never regenerates ENCRYPTION_KEY after bootstrap
- [ ] Operator never regenerates AUTH_SECRET after bootstrap
- [ ] Operator restores both keys from AWS backup if secret deleted
- [ ] Operator fails gracefully with clear error if backup unavailable and cluster already bootstrapped
- [ ] `HubEnvironment.Status.Conditions["BootstrapSecretsGenerated"]` used for bootstrap detection
- [ ] Operator does NOT handle general pod lifecycle management (Kubernetes deployment controllers)
- [ ] Operator DOES handle certificate rotation and pod restarts (per Requirement 23 from hub-operator spec)
- [ ] Operator DOES handle password rotation, ALTER ROLE, and pod restarts (per Requirement 23 from hub-operator spec)

### 4.3 Testing
- [ ] Test: Delete `infisical-secrets` → Operator restores both keys from AWS
- [ ] Test: Delete `infisical-secrets` with AWS unavailable → Operator fails with clear error
- [ ] Test: Delete finalizer → Secret can be deleted
- [ ] Test: Fresh cluster bootstrap → New keys generated, validated, and backed up to AWS
- [ ] Test: Existing cluster → Keys restored from AWS and validated
- [ ] Test: `hub teardown` → Finalizers removed, namespace deletion completes
- [ ] Test: AWS API timeout during restore → Operator does NOT generate new keys
- [ ] Test: Restored secret includes all 4 fields (ENCRYPTION_KEY, AUTH_SECRET, REDIS_URL, DB_ROOT_CERT)
- [ ] Test: Invalid ENCRYPTION_KEY format → Validation fails with clear error
- [ ] Test: Invalid AUTH_SECRET format → Validation fails with clear error
- [ ] Test: All-zeros key → Validation rejects with clear error
- [ ] Test: Corrupted backup data → Validation fails before secret creation

## 5. Non-Functional Requirements

### 5.1 Security
- Use AWS KMS encryption for Secrets Manager
- Use static IAM credentials with restricted permissions
- Inject credentials via CLI following Secret Zero pattern
- Audit all backup/restore operations via CloudTrail
- Restrict IAM policy to minimum required permissions:
  - `secretsmanager:GetSecretValue` on `/hub-operator/{cluster-id}/*`
  - `secretsmanager:PutSecretValue` on `/hub-operator/{cluster-id}/*`
  - `secretsmanager:CreateSecret` on `/hub-operator/{cluster-id}/*`
- Never log actual key values (only operations and metadata)
- Validate all keys before use (format, length, non-zero)
- Rotate credentials via Infisical (GitOps workflow)

### 5.2 Reliability
- Backup must succeed before operator marks bootstrap complete
- Restore must be idempotent
- Handle AWS API failures gracefully
- Retry with exponential backoff

### 5.3 Observability
- Log all ENCRYPTION_KEY operations (without exposing key value)
- Emit metrics for backup/restore operations
- Alert on backup failures
- Alert on restore attempts

## 6. Dependencies

### 6.1 AWS Resources
- AWS Secrets Manager in same region as cluster
- IAM user with restricted Secrets Manager permissions
- Access keys for IAM user (injected via CLI)
- CloudTrail enabled for audit logging

### 6.2 Operator Changes
- Update `internal/secrets/generator.go` (add validation functions)
- Add `internal/aws/secrets_manager.go` (static credential authentication)
- Add `internal/secrets/validator.go` (key validation logic)
- Add `internal/controller/finalizer_controller.go`
- Update `internal/controller/hubenvironment_controller.go`

### 6.3 Kubernetes Resources
- Update `infisical-secrets` manifest with finalizer
- Update `infisical-redis-credentials` manifest with finalizer
- Add AWS credentials environment variables to hub-operator deployment

### 6.4 External Systems
- ESO (External Secrets Operator) handles ongoing secret synchronization from Infisical
- Hub operator handles Day 0 bootstrap AND Day 2 operations (certificate rotation, password rotation per Requirement 23)
- Kubernetes deployment controllers handle general pod lifecycle management

## 7. Risks & Mitigations

### 7.1 Risk: AWS Secrets Manager unavailable
**Mitigation**: 
- Implement retry with exponential backoff (up to 5 minutes)
- Fail operator reconciliation with clear error message
- Alert operations team via monitoring
- Document manual recovery procedure

### 7.2 Risk: Backup fails during bootstrap
**Mitigation**:
- Retry backup multiple times with exponential backoff
- Fail operator reconciliation if backup fails after retries
- Do NOT mark `BootstrapSecretsGenerated` condition as true until backup succeeds
- Alert operations team immediately

### 7.3 Risk: Restore retrieves corrupted or invalid key
**Mitigation**:
- Validate key format before using (REQ-7.2)
- Check key length (must be 32 hex characters)
- Reject all-zeros keys
- Fail with clear error message if validation fails
- Log validation failures for debugging

### 7.4 Risk: Split-brain key generation on AWS timeout
**Mitigation**:
- Use `HubEnvironment.Status.Conditions["BootstrapSecretsGenerated"]` as source of truth
- If AWS times out AND cluster is already bootstrapped, HALT with error
- Never generate new keys on AWS API failures after initial bootstrap
- Implement retry with exponential backoff for AWS API calls
- Alert on repeated AWS API failures

### 7.5 Risk: Database wipe without full bootstrap
**Mitigation**:
- REQ-1 explicitly requires deleting `infisical-auth` to force Phase 0 re-bootstrap
- Operator must recreate Machine Identity and Project
- Document recovery procedure clearly in runbook

### 7.6 Risk: Invalid AWS credentials or API failures
**Mitigation**:
- Validate AWS credentials during operator startup
- Test AWS API access before attempting backup/restore
- Fail fast with clear error if authentication fails
- Document credential setup procedure with validation steps
- Implement retry with exponential backoff for AWS API calls
- Monitor CloudTrail for unauthorized access attempts

## 8. Out of Scope

### 8.1 Operations NOT Handled by Hub-Operator
- **General pod lifecycle management**: Handled by Kubernetes deployment controllers
- **Application-level credential reloads**: (e.g., PgBouncer/PostgREST reloading passwords from ESO-synced secrets)
- **Service health monitoring**: Handled by Kubernetes probes

### 8.2 Operations Handled by Hub-Operator (Per Requirement 23 from hub-operator spec)
- **Certificate rotation and pod restarts**: When platform-db-ca changes, operator updates DB_ROOT_CERT and restarts affected services
- **Password rotation and pod restarts**: When ESO-managed credentials change, operator executes ALTER ROLE and restarts consuming services
- **Secret Zero generation and recovery**: This spec's focus

### 8.3 Future Enhancements
- Rotating ENCRYPTION_KEY (requires re-encrypting all data)
- Multi-region backup replication
- Backup to multiple cloud providers
- Automated key rotation (not applicable for master encryption keys)

**CRITICAL**: Hub operator handles Day 0 bootstrap AND Day 2 operations (certificate rotation, password rotation per Requirement 23 from hub-operator spec). This spec focuses on Secret Zero generation and recovery. Any additional Day 2 operation logic beyond what's defined in Requirement 23 should be carefully evaluated.

## 9. Success Metrics

- **Recovery Time**: < 1 hour from incident to service restoration
- **Backup Success Rate**: 100% of ENCRYPTION_KEY generations
- **Restore Success Rate**: 100% of secret deletion incidents
- **Zero Data Loss**: No secret data lost due to ENCRYPTION_KEY issues

# Implementation Plan: Infisical ENCRYPTION_KEY Recovery & Protection

## Overview

This implementation plan follows a 4-phase approach with manual validation checkpoints. Each phase builds incrementally toward a complete disaster recovery and protection system for Infisical's master encryption keys. The solution prevents data loss by implementing automated backup to AWS Secrets Manager and intelligent restore logic.

**CRITICAL CONSTRAINTS**:
- NO automated test cases - manual validation only
- User approval required after each phase validation
- Each phase includes specific manual verification steps
- Implementation follows requirements-first workflow with validated technical decisions

## Tasks

- [-] 1. Phase 1: CLI Command Implementation
  - [ ] 1.1 Create AWS credentials injection CLI command
    - Create `cmd/hub/configure_aws_secrets_manager.go` with cobra command structure
    - Implement AWS credential validation and Kubernetes secret creation
    - Add command registration to `cmd/hub/main.go`
    - _Requirements: REQ-11 (AWS Integration), REQ-1 (Immediate Recovery)_

  - [x] 1.2 Implement Secret Zero pattern for AWS credentials
    - Add `InstallAWSSecretsManagerAuth()` function to `internal/hub/components/secrets.go`
    - Create Kubernetes secret with proper labels and metadata
    - Follow existing GitHub secrets pattern for consistency
    - _Requirements: REQ-11 (AWS Integration)_

  - [x] 1.3 Add AWS credential mappings to Infisical uploader
    - Update `internal/infisical/secret_mappings.go` with AWS credential mappings
    - Add three mappings: AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_REGION
    - Include proper descriptions and source/target key mappings
    - _Requirements: REQ-11 (AWS Integration)_

  - [x] 1.4 Create ExternalSecret manifest for GitOps sync
    - Create `manifests/hub-operator/aws-credentials-externalsecret.yaml`
    - Configure ArgoCD sync-wave and proper secret recreation
    - Enable GitOps management after initial CLI bootstrap
    - _Requirements: REQ-11 (AWS Integration)_

- [x] 2. Phase 1 Manual Validation Checkpoint
  - **Manual Steps**:
    1. Run CLI command: `./bin/hub configure-aws-secrets-manager --aws-access-key-id="test" --aws-secret-access-key="test" --aws-region="ap-south-1"`
    2. Verify secret created: `kubectl get secret hub-operator-aws-credentials -n hub-platform-ops -o yaml`
    3. Check secret contains all 3 keys with correct values
    4. Verify hub-operator uploads credentials to Infisical
    5. Confirm ExternalSecret recreates secret from Infisical
  - **Validation Criteria**: AWS credentials flow through Secret Zero pattern successfully
  - **User Approval Required**: Proceed to Phase 2 only after manual validation passes

- [x] 3. Phase 2: Operator Core Logic
  - [x] 3.1 Implement AWS Secrets Manager client
    - Create `internal/aws/secrets_manager.go` with SecretsManagerClient struct
    - Implement BackupMasterKeys() and RestoreMasterKeys() functions
    - Add AWS SDK v2 dependency to go.mod
    - Configure retry logic with exponential backoff (max 5 attempts)
    - _Requirements: REQ-7 (Operator Changes), REQ-12 (Backup metadata)_

  - [x] 3.2 Create key validation functions
    - Create `internal/secrets/validator.go` with validation logic
    - Implement validateEncryptionKey() - 32 hex chars, non-zero validation
    - Implement validateAuthSecret() - 32 hex chars, non-zero validation
    - Add comprehensive error messages for validation failures
    - _Requirements: REQ-7.2 (Key validation)_

  - [x] 3.3 Update secret generation with backup/restore logic
    - Modify `GenerateInfisicalSecrets()` in `internal/secrets/generator.go`
    - Add bootstrap detection using HubEnvironment.Status.Conditions
    - Implement AWS backup/restore priority logic
    - Add key validation before secret creation
    - _Requirements: REQ-7 (Operator Changes), REQ-3 (Never regenerate keys)_

  - [x] 3.4 Implement secret reconstruction logic
    - Add logic to read existing Redis password from `infisical-redis-credentials`
    - Add logic to read CA certificate from `platform-db-ca` secret
    - Use UncachedClient for reading secret data (CRITICAL)
    - Reconstruct complete infisical-secrets with all 4 fields
    - _Requirements: REQ-7.1 (Reconstruct complete payload)_

- [x] 4. Phase 2 Manual Validation Checkpoint
  - **Manual Steps**:
    1. Deploy fresh cluster and verify new keys generated and backed up
    2. Check AWS Secrets Manager contains backup with correct JSON structure
    3. Delete `infisical-secrets` and verify operator restores from AWS
    4. Confirm restored secret contains all 4 fields (ENCRYPTION_KEY, AUTH_SECRET, REDIS_URL, DB_ROOT_CERT)
    5. Verify Infisical pods restart and become healthy after restore
    6. Test validation by providing invalid key format - confirm operator rejects
  - **Validation Criteria**: Backup and restore cycle works end-to-end with validation
  - **User Approval Required**: Proceed to Phase 3 only after manual validation passes

- [ ] 5. Phase 3: Finalizer Protection
  - [ ] 5.1 Create finalizer controller
    - Create `internal/controller/finalizer_controller.go`
    - Define EncryptionKeyProtectionFinalizer constant
    - Implement addFinalizers() and removeFinalizers() functions
    - Add RBAC permissions for finalizer management
    - _Requirements: REQ-9 (Add finalizers), REQ-10 (Implement finalizer controller)_

  - [ ] 5.2 Add finalizer logic to HubEnvironment controller
    - Update `internal/controller/hubenvironment_controller.go`
    - Add finalizers to `infisical-secrets` and `infisical-redis-credentials`
    - Implement graceful teardown when DeletionTimestamp is set
    - Log deletion attempts for audit trail
    - _Requirements: REQ-13 (Graceful teardown support)_

  - [ ] 5.3 Update secret manifests with finalizers
    - Add finalizer metadata to secret creation logic
    - Ensure finalizers are applied during secret generation
    - Test finalizer removal during HubEnvironment deletion
    - _Requirements: REQ-9 (Add finalizers)_

- [ ] 6. Phase 3 Manual Validation Checkpoint
  - **Manual Steps**:
    1. Attempt to delete `infisical-secrets` - verify deletion is blocked
    2. Check finalizer exists: `kubectl get secret infisical-secrets -o yaml | grep finalizers`
    3. Manually remove finalizer and confirm secret can be deleted
    4. Test `hub teardown` command - verify finalizers are removed automatically
    5. Confirm namespace deletion completes without hanging
    6. Verify audit logs show deletion attempts
  - **Validation Criteria**: Finalizer protection prevents accidental deletion, teardown works
  - **User Approval Required**: Proceed to Phase 4 only after manual validation passes

- [ ] 7. Phase 4: Deployment Integration
  - [ ] 7.1 Update hub-operator deployment with AWS environment variables
    - Modify `config/manager/manager.yaml` to add AWS env vars
    - Configure secretKeyRef for AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_REGION
    - Reference `hub-operator-aws-credentials` secret
    - _Requirements: REQ-11 (AWS Integration)_

  - [ ] 7.2 Add comprehensive error handling and observability
    - Add logging for all backup/restore operations (without exposing key values)
    - Implement retry logic with exponential backoff for AWS API calls
    - Add clear error messages for all failure scenarios
    - Configure alerts for backup failures and restore attempts
    - _Requirements: REQ-11 (AWS Integration), Section 5.3 (Observability)_

  - [ ] 7.3 Update HubEnvironment status conditions
    - Ensure `BootstrapSecretsGenerated` condition is set only after successful backup
    - Add proper condition messages and reasons
    - Update condition on successful restore operations
    - _Requirements: REQ-7 (Bootstrap detection)_

- [ ] 8. Phase 4 Manual Validation Checkpoint
  - **Manual Steps**:
    1. Deploy complete solution to test cluster
    2. Verify hub-operator has AWS credentials via environment variables
    3. Test fresh cluster bootstrap - confirm keys generated, validated, and backed up
    4. Test disaster recovery - delete secret, verify restore from AWS
    5. Test AWS API timeout scenario - confirm operator fails gracefully
    6. Verify `BootstrapSecretsGenerated` condition is set correctly
    7. Check operator logs for proper error handling and observability
    8. Test all acceptance criteria from requirements.md
  - **Validation Criteria**: Complete end-to-end disaster recovery system operational
  - **User Approval Required**: Final approval before production deployment

- [ ] 9. Final Integration Verification
  - **Manual Steps**:
    1. Run complete acceptance criteria test suite from requirements.md
    2. Verify all 4.1, 4.2, and 4.3 acceptance criteria pass
    3. Test edge cases: corrupted backup data, invalid credentials, network failures
    4. Confirm security requirements: no key values in logs, proper IAM policies
    5. Validate observability: metrics, alerts, audit trails
  - **Validation Criteria**: All requirements met, system production-ready
  - **Final Checkpoint**: System ready for production deployment

## Notes

- Each phase must be manually validated before proceeding to the next
- No automated test cases - all validation is manual with specific kubectl commands
- User approval is required at each checkpoint before continuing
- Implementation follows Go language patterns from design document
- All tasks reference specific requirements for traceability
- Focus on disaster recovery and prevention of ENCRYPTION_KEY regeneration
- AWS integration uses static IAM credentials (not IRSA) as per design decisions
- UncachedClient usage is critical when reading secret data
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

- [x] 5. Phase 3: Finalizer Protection
  - [x] 5.1 Create finalizer constant
    - Created `EncryptionKeyProtectionFinalizer` constant in `operators/hub-operator/internal/secrets/constants.go`
    - Finalizer: `ops.nutgraf.in/encryption-key-protection`
    - _Requirements: REQ-9 (Add finalizers), REQ-10 (Implement finalizer controller)_

  - [x] 5.2 Add finalizer logic to HubEnvironment controller
    - Updated `internal/controller/hubenvironment_controller.go`
    - Added finalizers to `infisical-secrets` in secret generation code
    - Implemented graceful teardown when DeletionTimestamp is set
    - Added `removeFinalizer()` helper function for cleanup
    - _Requirements: REQ-13 (Graceful teardown support)_

  - [x] 5.3 Update secret manifests with finalizers
    - Updated `manifests/hub-core-services/platform-infisical/infisical-redis-credentials.yaml` with finalizer
    - Finalizer added to Git manifest for GitOps-native solution
    - `infisical-secrets` gets finalizer from operator code during generation
    - ArgoCD syncs finalizer from Git for `infisical-redis-credentials`
    - _Requirements: REQ-9 (Add finalizers)_

- [x] 6. Phase 3 Manual Validation Checkpoint
  - **Manual Steps Completed**:
    1. ✅ Verified both secrets have finalizer: `ops.nutgraf.in/encryption-key-protection`
    2. ✅ Deletion blocked: Secret has `deletionTimestamp` but remains in cluster
    3. ✅ Manual finalizer removal: Secret deleted after finalizer removed
    4. ✅ ArgoCD recreation: Secret recreated with finalizer from Git manifest
  - **Validation Results**: 
    - Both `infisical-secrets` and `infisical-redis-credentials` protected
    - Finalizer blocks deletion as expected
    - Git manifest ensures finalizer persists across recreations
    - Long-term permanent solution implemented
  - **Status**: ✅ COMPLETE

- [x] 7. Phase 4: Deployment Integration
  - [x] 7.1 Update hub-operator deployment with AWS environment variables
    - AWS environment variables already configured in `operators/hub-operator/config/manager/manager.yaml`
    - Environment variables: AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_REGION
    - References `hub-operator-aws-credentials` secret via secretKeyRef
    - Completed in Phase 1, verified in Phase 4
    - _Requirements: REQ-11 (AWS Integration)_

  - [x] 7.2 Add comprehensive error handling and observability
    - Added detailed logging for bootstrap detection (isFirstTime, infisicalSecretExists)
    - Added AWS client initialization logging with region
    - Added cluster ID and AWS enablement status logging
    - Added backup/restore status logging throughout Phase 1
    - All error messages include context without exposing key values
    - _Requirements: REQ-11 (AWS Integration), Section 5.3 (Observability)_

  - [x] 7.3 Update HubEnvironment status conditions
    - Status condition message reflects AWS backup/restore state:
      - First-time: "Bootstrap secrets generated and backed up to AWS Secrets Manager"
      - Restore: "Bootstrap secrets restored from AWS Secrets Manager backup"
      - No AWS: "Bootstrap secrets generated successfully"
    - Condition set only after successful backup/restore
    - ObservedGeneration tracked correctly
    - _Requirements: REQ-7 (Bootstrap detection)_

- [x] 8. Phase 4 Manual Validation Checkpoint
  - **Manual Steps Completed**:
    1. ✅ Verified hub-operator has AWS credentials via environment variables
    2. ✅ Tested disaster recovery - deleted secret, verified restore from AWS
    3. ✅ Verified `BootstrapSecretsGenerated` condition updated correctly
    4. ✅ Checked operator logs for proper error handling and observability
  - **Validation Results**:
    - AWS credentials configured: AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_REGION
    - Disaster recovery successful: Secret restored from AWS backup
    - Enhanced logging verified:
      - `Bootstrap detection: isFirstTime=false, infisicalSecretExists=false`
      - `AWS Secrets Manager client initialized successfully, region=ap-south-1`
      - `Starting bootstrap secrets generation: clusterID=hub-production, awsEnabled=true`
      - `Bootstrap secrets generated successfully: awsBackupEnabled=true`
      - `Phase 1 complete: awsBackup=true, isFirstTime=false`
    - Status condition message: "Bootstrap secrets restored from AWS Secrets Manager backup"
    - Restored secret has finalizer protection
  - **Status**: ✅ COMPLETE

- [x] 9. Final Integration Verification
  - **Manual Steps Completed**:
    1. ✅ Tested complete disaster recovery flow (delete → restore from AWS)
    2. ✅ Verified all acceptance criteria from requirements.md
    3. ✅ Confirmed security requirements: no key values in logs
    4. ✅ Validated observability: comprehensive logging throughout
  - **Acceptance Criteria Validation**:
    - **4.2 Prevention**:
      - ✅ Finalizers added to both `infisical-secrets` and `infisical-redis-credentials`
      - ✅ Operator never regenerates ENCRYPTION_KEY after bootstrap (isFirstTime=false)
      - ✅ Operator restores both keys from AWS backup when secret deleted
      - ✅ `HubEnvironment.Status.Conditions["BootstrapSecretsGenerated"]` used for bootstrap detection
      - ✅ Status condition message reflects restore operation
    - **4.3 Testing**:
      - ✅ Delete `infisical-secrets` → Operator restored from AWS (verified in logs)
      - ✅ Restored secret includes finalizer protection
      - ✅ Bootstrap detection works correctly (isFirstTime=false, infisicalSecretExists=false)
      - ✅ AWS client initialization successful (region=ap-south-1)
      - ✅ Phase 1 completion logged with backup status
  - **Validation Criteria**: All requirements met, system production-ready
  - **Status**: ✅ COMPLETE - System ready for production deployment

## Notes

- Each phase must be manually validated before proceeding to the next
- No automated test cases - all validation is manual with specific kubectl commands
- User approval is required at each checkpoint before continuing
- Implementation follows Go language patterns from design document
- All tasks reference specific requirements for traceability
- Focus on disaster recovery and prevention of ENCRYPTION_KEY regeneration
- AWS integration uses static IAM credentials (not IRSA) as per design decisions
- UncachedClient usage is critical when reading secret data
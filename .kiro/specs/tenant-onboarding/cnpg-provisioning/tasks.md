# Implementation Plan: CNPG Provisioning

## Overview

This implementation plan covers Phase 2 CNPG provisioning: deploying the Platform Database and implementing the cnpg2monitor operator for zero-touch observability integration. The implementation follows 34 documented reference patterns and includes comprehensive BDD test scenarios.

## Tasks

- [x] 1. Platform Database deployment and configuration
  - [x] 1.1 Create Platform Database CNPG Cluster manifest
    - Create CNPG Cluster CR with 3-node HA configuration
    - Configure storage, monitoring, and bootstrap settings
    - Set up database owner and post-init SQL
    - _Requirements: FR1.1, FR1.2, FR1.3_
  
  - [x] 1.2 Deploy Platform Database to Management Cluster
    - Apply manifest to zero-ops-system namespace
    - Verify cluster reaches ClusterPhaseHealthy status
    - Validate connection secret generation
    - _Requirements: FR1.1, FR1.2, FR1.3_
  
  - [x] 1.3 Write BDD tests for Platform Database Bootstrap (Suite 1)
    - **BDD Suite 1: Platform Database Bootstrap**
    - **Validates: Requirements FR1.1, FR1.2, FR1.3**

- [x] 2. cnpg2monitor operator core implementation (CORRECTED)
  - [x] 2.1 Set up Go project structure and dependencies
    - Initialize Go module with Kubernetes controller dependencies
    - Set up kubebuilder project structure
    - Configure build and deployment manifests
    - _Requirements: FR2.1, FR2.2_
  
  - [x] 2.2 Implement controller struct and configuration
    - Create Cnpg2Monitor controller struct with embedded client
    - Implement configuration loading from environment variables
    - Set up structured logging and metrics registration
    - _Requirements: FR2.1, NFR3.1, NFR3.2_
  
  - [x] 2.3 Implement watch configuration and predicates
    - Configure watches for CNPG Clusters, Namespaces, and PodMonitors
    - Implement predicate-based event filtering
    - Set up EnqueueRequestsFromMapFunc for cross-resource mapping
    - _Requirements: FR3.1, FR3.2_
  
  - [x] 2.4 Controller setup validation
    - Test configuration loading and validation
    - Test watch setup and predicate filtering
    - _Requirements: TR1.1_
  
  - [x] 2.5 CNPG scheme registration (CRITICAL FIX)
    - Register CloudNativePG API v1 scheme in main.go
    - Add CNPG dependency to go.mod
    - Ensure proper scheme initialization
    - _Requirements: FR3.1_

- [x] 3. Reconciliation logic implementation (CORRECTED)
  - [x] 3.1 Implement observe-analyze-act reconciliation pattern
    - Create observe phase for state gathering
    - Implement analyze phase for action determination
    - Build act phase for state changes
    - _Requirements: FR3.1, NFR2.1_
  
  - [x] 3.2 Implement PodMonitor discovery and validation
    - Find CNPG-generated PodMonitors using label selectors
    - Validate cluster monitoring configuration
    - Handle race conditions with retry logic
    - _Requirements: FR3.1_
  
  - [x] 3.3 Implement topology label reading from namespaces
    - Read zero-ops.io/* labels from parent namespace
    - Handle missing topology labels with warning events
    - Support dynamic namespace label updates
    - _Requirements: FR3.2_
  
  - [x] 3.4 Reconciliation logic validation
    - Test observe-analyze-act pattern
    - Test PodMonitor discovery edge cases
    - Test topology label handling
    - _Requirements: TR1.1_
  
  - [x] 3.5 CNPG Cluster CR integration (CRITICAL FIX)
    - Watch CNPG Cluster CR instead of Pod resources
    - Use correct postgresql.cnpg.io/cluster label
    - Import CloudNativePG API v1
    - Update mapping functions for CNPG Clusters
    - _Requirements: FR3.1_

- [x] 4. Server-Side Apply PodMonitor patching (CORRECTED)
  - [x] 4.1 Implement SSA patch strategy for PodMonitors
    - Build relabelings array from topology labels
    - Implement field-level ownership with SSA
    - Handle missing topology labels by removing monitored label
    - _Requirements: FR3.1, FR3.2, NFR2.2_
  
  - [x] 4.2 Implement PodMonitor lifecycle management
    - Patch existing PodMonitors with topology relabelings
    - Restore monitored label when topology labels are added
    - Handle PodMonitor deletion gracefully
    - _Requirements: FR3.1, FR3.2_
  
  - [x] 4.3 SSA patching validation
    - Test relabelings generation from topology labels
    - Test field ownership and conflict resolution
    - Test missing topology label handling
    - _Requirements: TR1.1_
  
  - [x] 4.4 Target correct field per NFR2.2 (CRITICAL FIX)
    - Target podMetricsEndpoints[port=metrics].relabelings
    - Use "metrics" port as merge key for SSA
    - Fix from MetricRelabelConfigs to RelabelConfigs
    - _Requirements: NFR2.2_

- [x] 5. Event emission system implementation
  - [x] 5.1 Implement annotation-based state tracking
    - Create annotation constants for state tracking
    - Implement annotation update helpers
    - Build level-based event detection logic
    - _Requirements: FR3.3_
  
  - [x] 5.2 Implement lifecycle event emission
    - Detect and emit CNPGScaled events
    - Detect and emit CNPGConfigChanged events
    - Detect and emit CNPGStorageExpanded events
    - _Requirements: FR3.3_
  
  - [x] 5.3 Event emission validation
    - Test annotation-based state tracking
    - Test event deduplication logic
    - Test various lifecycle event scenarios
    - _Requirements: TR1.1_

- [x] 6. RBAC and deployment configuration (CORRECTED)
  - [x] 6.1 Generate RBAC manifests with minimal permissions
    - Add RBAC for cross-namespace CNPG cluster access
    - Generate ServiceAccount, Role, and RoleBinding (not Cluster*)
    - Validate minimal required permissions
    - _Requirements: FR2.2, SR1.1, DR1.2_
  
  - [x] 6.2 Create operator deployment manifests
    - Build Deployment with resource limits and health probes
    - Configure environment variables and command-line flags
    - Set up namespace restriction and API rate limiting
    - _Requirements: FR2.1, NFR1.1, NFR1.2, DR1.1_
  
  - [x] 6.3 Fix RBAC and deployment per requirements (CRITICAL FIX)
    - Use Role/RoleBinding instead of ClusterRole/ClusterRoleBinding
    - Deploy to cnpg2monitor-system namespace per FR2.1
    - Set CPU limit to 100m per FR2.1
    - Add patch permission for cluster annotations
    - _Requirements: DR1.2, FR2.1, SR1.1_

- [ ] 7. Checkpoint - Core operator functionality complete
  - Ensure all tests pass, ask the user if questions arise.

- [ ] 8. BDD test implementation
  - [ ] 8.1 Implement BDD Suite 1: Platform Database Bootstrap
    - Scenario 1.1: High Availability Cluster Provisioning
    - Scenario 1.2: Post-Init SQL and Database Owner Verification
    - Scenario 1.3: Secret Generation for API Consumption
    - _Requirements: TR2.1, FR1.1, FR1.2, FR1.3_
  
  - [ ] 8.2 Implement BDD Suite 2: cnpg2monitor Auto-Wiring
    - Scenario 2.1: Topology Label Injection (The Golden Path)
    - Scenario 2.2: Ignoring Unmonitored Databases
    - Scenario 2.3: Dynamic Namespace Label Updates
    - _Requirements: TR2.1, FR3.1, FR3.2_
  
  - [ ] 8.3 Implement BDD Suite 3: AI Correlation Event Emission
    - Scenario 3.1: Emitting Scale Events
    - Scenario 3.2: Emitting PostgreSQL Config Change Events
    - _Requirements: TR2.1, FR3.3_
  
  - [ ] 8.4 Set up BDD test framework and helpers
    - Configure Ginkgo/Gomega test framework
    - Create test helper functions for CNPG clusters and namespaces
    - Set up test environment with envtest
    - _Requirements: TR2.1_

- [ ] 9. Error handling and observability
  - [ ] 9.1 Implement comprehensive error handling
    - Add race condition handling with retry logic
    - Implement graceful degradation patterns
    - Handle operator restart scenarios
    - _Requirements: NFR2.1, NFR2.2_
  
  - [ ] 9.2 Implement Prometheus metrics and structured logging
    - Register Prometheus metrics for monitoring
    - Set up structured logging with context propagation
    - Add health and readiness probes
    - _Requirements: NFR3.1, NFR3.2_
  
  - [ ] 9.3 Write tests for error handling and metrics
    - Test retry logic and backoff strategies
    - Test metrics collection and emission
    - Test logging output and context
    - _Requirements: TR1.1_

- [ ] 10. Integration testing and validation
  - [ ] 10.1 Set up integration test environment
    - Configure test cluster with CNPG operator
    - Set up test namespaces with topology labels
    - Deploy cnpg2monitor operator for testing
    - _Requirements: TR1.2_
  
  - [ ] 10.2 Run end-to-end integration tests
    - Execute all BDD test scenarios
    - Validate PodMonitor creation and patching
    - Test event emission and lifecycle management
    - _Requirements: TR1.2, TR2.1_
  
  - [ ] 10.3 Perform chaos testing
    - Test operator restart during reconciliation
    - Test CNPG cluster deletion during PodMonitor creation
    - Test namespace label changes during reconciliation
    - _Requirements: TR2.2_

- [ ] 11. Final checkpoint and deployment preparation
  - [ ] 11.1 Validate all success criteria
    - Verify Platform Database is healthy and accessible
    - Confirm cnpg2monitor operator is running and functional
    - Validate PodMonitors have correct topology labels
    - _Requirements: SC1.1, SC1.2, SC1.3_
  
  - [ ] 11.2 Prepare deployment documentation
    - Create operator README with deployment instructions
    - Document troubleshooting guide for common issues
    - Document metrics and monitoring setup
    - _Requirements: SC2.2_
  
  - [ ] 11.3 Final integration validation
    - Run complete test suite and verify all tests pass
    - Perform static analysis and security scanning
    - Validate Phase 4 integration readiness
    - _Requirements: SC2.1, SC1.3_

- [ ] 12. Final checkpoint - Ensure all tests pass
  - Ensure all tests pass, ask the user if questions arise.

## Notes

- Tasks marked with `*` are optional and can be skipped for faster MVP
- Each task references specific requirements for traceability
- BDD test scenarios are based on e2e-bdd.md specification
- Implementation follows 34 documented reference patterns from production operators
- Checkpoints ensure incremental validation and user approval
- Phase organization allows for phase-by-phase approval as requested
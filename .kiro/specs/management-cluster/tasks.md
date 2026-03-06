# Implementation Tasks: Management Cluster Bootstrap

**Feature:** Platform Bootstrap (Journey A)  
**Version:** 1.0  
**Status:** NOT STARTED

---

## Phase 1: Project Setup & CLI Foundation

### 1.1 Repository Structure
- [ ] Create Go monorepo structure following Syself model
- [ ] Set up `cmd/zero-ops/` for CLI entrypoint
- [ ] Set up `pkg/` for library code
- [ ] Set up `internal/assets/` for embedded manifests
- [ ] Set up `manifests/` and `catalog/` directories
- [ ] Create Makefile with build targets

### 1.2 CLI Framework
- [ ] Initialize Go module with Cobra framework
- [ ] Implement `zero-ops mgmt bootstrap` command skeleton
- [ ] Implement flag parsing (--name, --region, --talos-image-id, etc.)
- [ ] Implement environment variable reading (HCLOUD_TOKEN)
- [ ] Add --debug flag for verbose logging

### 1.3 Binary Management
- [ ] Implement clusterctl binary manager (download, verify checksum, OS/arch detection)
- [ ] Implement talosctl binary manager (download, verify checksum, OS/arch detection)
- [ ] Store binaries in `~/.zero-ops/bin/`
- [ ] Handle unsupported architectures gracefully

### 1.4 State Management
- [ ] Implement BootstrapState struct (phase tracking, kubeconfig paths, timestamps)
- [ ] Implement state persistence to `~/.zero-ops/state/<cluster-name>.json`
- [ ] Implement state recovery on failure

### Manual Testing (Phase 1)
```bash
# Verify CLI builds and runs
make build
./bin/zero-ops mgmt bootstrap --help

# Verify binary management
./bin/zero-ops mgmt bootstrap --name=test --region=fsn1 --talos-image-id=12345 --dry-run
# Expected: clusterctl and talosctl downloaded to ~/.zero-ops/bin/

# Verify flag validation
./bin/zero-ops mgmt bootstrap --name=invalid_name --region=fsn1
# Expected: Error about invalid cluster name format
```

**Approval Gate:** User confirms CLI structure, flag parsing, and binary management work correctly.

---

## Phase 2: Preflight Validation

### 2.1 Docker Validation
- [ ] Implement DockerValidator (check daemon running via `docker ps`)
- [ ] Add error message with remediation steps

### 2.2 Kind Validation
- [ ] Implement KindValidator (check Kind available via `kind version`)
- [ ] Skip if --bootstrap-context provided

### 2.3 Hetzner Token Validation
- [ ] Implement HetznerTokenValidator (read-only API call to validate token)
- [ ] Add exponential backoff for rate limits (HTTP 429)
- [ ] Add error messages for invalid/read-only tokens

### 2.4 Talos Image Validation
- [ ] Implement TalosImageValidator (verify snapshot exists in Hetzner)
- [ ] Add support for --build-talos-image flag (trigger Packer build)
- [ ] Add error message if neither --talos-image-id nor --build-talos-image provided

### 2.5 SSH Key Validation (Optional)
- [ ] Implement SSHKeyValidator (verify key exists in HCloud if --ssh-key provided)
- [ ] Add error message if key not found

### 2.6 Idempotency Check
- [ ] Check for existing cluster with same name
- [ ] Exit with error if exists and --upgrade not set
- [ ] Proceed to reconciliation if --upgrade set

### Manual Testing (Phase 2)
```bash
# Test Docker validation
systemctl stop docker
./bin/zero-ops mgmt bootstrap --name=test --region=fsn1 --talos-image-id=12345
# Expected: Error about Docker not running

# Test Hetzner token validation
export HCLOUD_TOKEN=invalid
./bin/zero-ops mgmt bootstrap --name=test --region=fsn1 --talos-image-id=12345
# Expected: Error about invalid token

# Test Talos image validation
export HCLOUD_TOKEN=<valid-token>
./bin/zero-ops mgmt bootstrap --name=test --region=fsn1 --talos-image-id=99999
# Expected: Error about Talos image not found

# Test idempotency check (run twice)
./bin/zero-ops mgmt bootstrap --name=test --region=fsn1 --talos-image-id=<valid-id>
./bin/zero-ops mgmt bootstrap --name=test --region=fsn1 --talos-image-id=<valid-id>
# Expected: Second run exits with "cluster already exists" error
```

**Approval Gate:** User confirms all preflight validations work correctly with appropriate error messages.

---

## Phase 3: Bootstrap Cluster Creation

### 3.1 Kind Cluster Management
- [ ] Implement KindManager.Create() (execute `kind create cluster`)
- [ ] Set cluster name to `bootstrap-zero-ops`
- [ ] Add 2-minute timeout
- [ ] Implement KindManager.Delete() for cleanup

### 3.2 Bootstrap Context Support
- [ ] Support --bootstrap-context flag (use existing cluster)
- [ ] Validate context exists in kubeconfig
- [ ] Verify cluster is reachable

### 3.3 Namespace Creation
- [ ] Create `zero-ops-system` namespace in bootstrap cluster
- [ ] Add labels for tracking

### Manual Testing (Phase 3)
```bash
# Test Kind cluster creation
./bin/zero-ops mgmt bootstrap --name=test --region=fsn1 --talos-image-id=<valid-id> --keep-bootstrap
# Expected: Kind cluster created, visible via `kind get clusters`

# Verify namespace created
kubectl --context kind-bootstrap-zero-ops get namespace zero-ops-system
# Expected: Namespace exists

# Test cleanup
kind delete cluster --name=bootstrap-zero-ops
```

**Approval Gate:** User confirms Kind cluster creation, namespace creation, and cleanup work correctly.

---

## Phase 4: CAPI Initialization (Declarative Operator)

### 4.1 Manifest Embedding
- [ ] Embed `manifests/core/capi-operator/install.yaml` using go:embed
- [ ] Embed Provider CRD manifests (CoreProvider, BootstrapProvider, ControlPlaneProvider, InfrastructureProvider)
- [ ] Implement assets.ReadManifest() helper

### 4.2 Operator Installation
- [ ] Implement CAPIOperatorInstaller.installOperator() (apply operator manifest)
- [ ] Implement CAPIOperatorInstaller.waitForOperator() (wait for deployment Ready)
- [ ] Add 3-minute timeout

### 4.3 Provider CRD Application
- [ ] Implement CAPIOperatorInstaller.applyProviders() (apply all Provider CRDs)
- [ ] Apply CoreProvider (cluster-api v1.10.0)
- [ ] Apply BootstrapProvider (talos v0.6.5)
- [ ] Apply ControlPlaneProvider (talos v0.5.6)
- [ ] Apply InfrastructureProvider (hetzner v1.0.7)

### 4.4 Provider Verification
- [ ] Implement CAPIOperatorInstaller.waitForProviders() (poll Provider CRD status)
- [ ] Check Ready condition for each Provider
- [ ] Add 5-minute timeout with progress display

### 4.5 Secret Creation
- [ ] Create Hetzner credentials secret in `zero-ops-system` namespace
- [ ] Add `clusterctl.cluster.x-k8s.io/move` label for pivot

### Manual Testing (Phase 4)
```bash
# Run bootstrap up to CAPI initialization
./bin/zero-ops mgmt bootstrap --name=test --region=fsn1 --talos-image-id=<valid-id> --keep-bootstrap

# Verify operator installed
kubectl --context kind-bootstrap-zero-ops get deployment -n capi-operator-system
# Expected: capi-operator-controller-manager deployment Ready

# Verify Provider CRDs applied
kubectl --context kind-bootstrap-zero-ops get coreprovider,bootstrapprovider,controlplaneprovider,infrastructureprovider
# Expected: All 4 providers with Ready=True

# Verify secret created
kubectl --context kind-bootstrap-zero-ops get secret hetzner-credentials -n zero-ops-system
# Expected: Secret exists with hcloud key
```

**Approval Gate:** User confirms cluster-api-operator and Provider CRDs are installed and Ready.

---

## Phase 5: Management Cluster Provisioning

### 5.1 ClusterClass Embedding
- [ ] Embed `manifests/classes/hetzner-mgmt-talos-v1.yaml` using go:embed
- [ ] Implement ClusterClass template rendering (substitute variables)

### 5.2 Cluster Resource Generation
- [ ] Implement Cluster manifest generation with topology reference
- [ ] Set `spec.topology.class: hetzner-mgmt-talos-v1`
- [ ] Set `spec.topology.version: v1.31.6`
- [ ] Set control plane replicas: 3, worker replicas: 2
- [ ] Populate variables (region, talosVersion, talosImageId, network CIDR, etc.)

### 5.3 Resource Application
- [ ] Apply ClusterClass to bootstrap cluster
- [ ] Apply Cluster resource to bootstrap cluster
- [ ] Verify resources created successfully

### 5.4 Provisioning Monitor
- [ ] Implement ClusterProvisioner.WaitForReady() (poll cluster status)
- [ ] Monitor `status.phase` transitions (Pending → Provisioning → Provisioned)
- [ ] Check Ready condition (not just Provisioned phase)
- [ ] Add 15-minute timeout with progress display

### 5.5 Infrastructure Verification
- [ ] Query CAPH status for infrastructure resources
- [ ] Verify 3 control plane VMs, 2 worker VMs, 1 LB, 1 network, 2 placement groups

### Manual Testing (Phase 5)
```bash
# Run bootstrap through provisioning
./bin/zero-ops mgmt bootstrap --name=mothership --region=fsn1 --talos-image-id=<valid-id> --keep-bootstrap

# Verify ClusterClass applied
kubectl --context kind-bootstrap-zero-ops get clusterclass hetzner-mgmt-talos-v1 -n zero-ops-system
# Expected: ClusterClass exists

# Verify Cluster resource
kubectl --context kind-bootstrap-zero-ops get cluster mothership -n zero-ops-system
# Expected: Cluster with phase=Provisioned or Provisioning

# Verify Hetzner infrastructure (via Hetzner Console or API)
# Expected: 3 control plane VMs (CPX31), 2 worker VMs (CPX31), 1 LB, 1 network

# Wait for cluster Ready
kubectl --context kind-bootstrap-zero-ops get cluster mothership -n zero-ops-system -w
# Expected: Eventually shows Ready=True condition
```

**Approval Gate:** User confirms Management Cluster is provisioned on Hetzner and reaches Ready state.

---

## Phase 6: CAPI Pivot

### 6.1 Kubeconfig Retrieval
- [ ] Implement PivotOrchestrator.getKubeconfig() (extract from secret)
- [ ] Decode base64 kubeconfig
- [ ] Save to temporary file

### 6.2 Operator Installation on Management Cluster
- [ ] Install cluster-api-operator on Management Cluster
- [ ] Wait for operator Ready (3 minutes)

### 6.3 Resource Counting
- [ ] Implement resource counting before pivot (Cluster, Machine, HetznerCluster, Secret)
- [ ] Store counts for verification

### 6.4 Pivot Execution
- [ ] Execute `clusterctl move --to-kubeconfig=<mgmt> --namespace=zero-ops-system`
- [ ] Add 10-minute timeout

### 6.5 Post-Pivot Verification
- [ ] Count resources after pivot (existence check only)
- [ ] Verify counts match
- [ ] Implement waitForProvidersReady() (wait for Provider CRDs to reconcile)
- [ ] Implement waitForClusterReady() (wait for Cluster Ready condition)
- [ ] Add 10-minute timeout for reconciliation

### 6.6 Bootstrap Cleanup
- [ ] Delete Kind cluster (if --keep-bootstrap not set)
- [ ] Preserve Kind cluster on failure

### Manual Testing (Phase 6)
```bash
# Run full bootstrap including pivot
./bin/zero-ops mgmt bootstrap --name=mothership --region=fsn1 --talos-image-id=<valid-id>

# Verify kubeconfig retrieved
ls mothership.kubeconfig
# Expected: File exists

# Verify operator on Management Cluster
kubectl --kubeconfig=mothership.kubeconfig get deployment -n capi-operator-system
# Expected: Operator deployment Ready

# Verify resources moved
kubectl --kubeconfig=mothership.kubeconfig get cluster,machine,hetznercluster -n zero-ops-system
# Expected: All resources present

# Verify Provider CRDs reconciled
kubectl --kubeconfig=mothership.kubeconfig get coreprovider,bootstrapprovider,controlplaneprovider,infrastructureprovider
# Expected: All providers Ready=True

# Verify Kind cluster deleted
kind get clusters
# Expected: bootstrap-zero-ops not listed (unless --keep-bootstrap used)
```

**Approval Gate:** User confirms pivot completed successfully, resources moved, and bootstrap cluster cleaned up.

---

## Phase 7: ClusterClass Library Deployment

### 7.1 Library Embedding
- [ ] Embed `manifests/classes/hetzner-prod-talos-v1.yaml`
- [ ] Embed `manifests/classes/hetzner-dev-talos-v1.yaml`
- [ ] Embed `manifests/classes/hetzner-staging-talos-v1.yaml`

### 7.2 Library Application
- [ ] Apply all ClusterClass definitions to Management Cluster
- [ ] Verify all ClusterClasses created successfully

### Manual Testing (Phase 7)
```bash
# Verify ClusterClass library deployed
kubectl --kubeconfig=mothership.kubeconfig get clusterclass -n zero-ops-system
# Expected: hetzner-mgmt-talos-v1, hetzner-prod-talos-v1, hetzner-dev-talos-v1, hetzner-staging-talos-v1

# Inspect a ClusterClass
kubectl --kubeconfig=mothership.kubeconfig get clusterclass hetzner-prod-talos-v1 -n zero-ops-system -o yaml
# Expected: Valid ClusterClass with TalosControlPlane and TalosConfigTemplate
```

**Approval Gate:** User confirms ClusterClass library is deployed and accessible.

---

## Phase 8: Post-Bootstrap Components

### 8.1 Component Manifest Embedding
- [ ] Embed ArgoCD manifests in `catalog/gitops/argocd/install.yaml`
- [ ] Embed capi2argo manifests in `catalog/gitops/capi2argo/install.yaml`
- [ ] Embed CloudNativePG manifests in `catalog/databases/cloudnative-pg/install.yaml`
- [ ] Embed Hetzner CCM manifests (if not in ClusterClass)
- [ ] Embed Hetzner CSI manifests (if not in ClusterClass)

### 8.2 Component Installer Implementation
- [ ] Implement ManagementClusterInstaller.InstallAll() (sequential installation)
- [ ] Implement installHetznerCCM() + verifyHetznerCCM()
- [ ] Implement installHetznerCSI() + verifyHetznerCSI()
- [ ] Implement installArgoCD() + verifyArgoCD()
- [ ] Implement installCapi2Argo() + verifyCapi2Argo()
- [ ] Implement installCloudNativePG() + verifyCloudNativePG()

### 8.3 Verification Helpers
- [ ] Implement waitForDeployment() (check deployment Available condition)
- [ ] Add timeouts per component (3-5 minutes)
- [ ] Add progress logging

### 8.4 ArgoCD Password Retrieval
- [ ] Extract admin password from `argocd-initial-admin-secret`
- [ ] Store for output message

### Manual Testing (Phase 8)
```bash
# Run full bootstrap
./bin/zero-ops mgmt bootstrap --name=mothership --region=fsn1 --talos-image-id=<valid-id>

# Verify Hetzner CCM
kubectl --kubeconfig=mothership.kubeconfig get deployment -n kube-system hcloud-cloud-controller-manager
# Expected: Deployment Ready

# Verify Hetzner CSI
kubectl --kubeconfig=mothership.kubeconfig get deployment -n kube-system hcloud-csi-controller
# Expected: Deployment Ready

# Verify ArgoCD
kubectl --kubeconfig=mothership.kubeconfig get pods -n argocd
# Expected: All ArgoCD pods Running

# Verify capi2argo
kubectl --kubeconfig=mothership.kubeconfig get deployment -n capi2argo-system capi2argo-controller-manager
# Expected: Deployment Ready

# Verify CloudNativePG
kubectl --kubeconfig=mothership.kubeconfig get deployment -n cnpg-system cnpg-controller-manager
# Expected: Deployment Ready

# Test ArgoCD access
kubectl --kubeconfig=mothership.kubeconfig get secret argocd-initial-admin-secret -n argocd -o jsonpath='{.data.password}' | base64 -d
# Expected: Admin password displayed
```

**Approval Gate:** User confirms all platform components are installed and healthy.

---

## Phase 9: Kubeconfig & Talosconfig Management

### 9.1 Kubeconfig Management
- [ ] Save kubeconfig to `<cluster-name>.kubeconfig` in current directory
- [ ] Set file permissions to 0600
- [ ] Implement --merge-kubeconfig flag (merge into ~/.kube/config with backup)
- [ ] Set context name to cluster name

### 9.2 Talosconfig Management
- [ ] Retrieve talosconfig from secret `<cluster-name>-talosconfig`
- [ ] Save to `<cluster-name>.talosconfig` in current directory
- [ ] Set file permissions to 0600

### 9.3 Success Output
- [ ] Display success message with cluster details
- [ ] Show kubeconfig and talosconfig paths
- [ ] Show ArgoCD UI URL and admin password
- [ ] Show next steps (verify cluster, access nodes, onboard tenant)

### Manual Testing (Phase 9)
```bash
# Run full bootstrap
./bin/zero-ops mgmt bootstrap --name=mothership --region=fsn1 --talos-image-id=<valid-id>

# Verify kubeconfig saved
ls -la mothership.kubeconfig
# Expected: File exists with 0600 permissions

# Verify talosconfig saved
ls -la mothership.talosconfig
# Expected: File exists with 0600 permissions

# Test kubeconfig
kubectl --kubeconfig=mothership.kubeconfig get nodes
# Expected: 3 control plane + 2 worker nodes listed

# Test talosconfig
talosctl --talosconfig=mothership.talosconfig -n <node-ip> version
# Expected: Talos version displayed

# Verify success message displayed
# Expected: Message shows cluster details, kubeconfig path, ArgoCD credentials, next steps
```

**Approval Gate:** User confirms kubeconfig and talosconfig are saved correctly and functional.

---

## Phase 10: Error Handling & Recovery

### 10.1 Retry Logic
- [ ] Implement exponential backoff for Hetzner API calls (HTTP 429, 5xx)
- [ ] Implement retry for Kubernetes API calls (connection errors)
- [ ] Add max retry limit (5 attempts)

### 10.2 Failure Preservation
- [ ] Preserve Kind cluster on any failure
- [ ] Save state to `~/.zero-ops/state/<cluster-name>.json`
- [ ] Output diagnostic information (logs, resource status)

### 10.3 Error Messages
- [ ] Implement actionable error messages with remediation steps
- [ ] Add error codes for common failures

### 10.4 Debug Mode
- [ ] Implement --debug flag for verbose logging
- [ ] Log all API calls and kubectl commands

### Manual Testing (Phase 10)
```bash
# Test retry logic (simulate rate limit)
# Manually trigger rate limit by making many Hetzner API calls, then run bootstrap
# Expected: CLI retries with exponential backoff

# Test failure preservation
# Kill bootstrap process mid-execution (Ctrl+C during provisioning)
kind get clusters
# Expected: bootstrap-zero-ops still exists

# Test error messages
export HCLOUD_TOKEN=invalid
./bin/zero-ops mgmt bootstrap --name=test --region=fsn1 --talos-image-id=12345
# Expected: Clear error message with remediation steps

# Test debug mode
./bin/zero-ops mgmt bootstrap --name=test --region=fsn1 --talos-image-id=<valid-id> --debug
# Expected: Verbose logging showing all API calls and commands
```

**Approval Gate:** User confirms error handling, retry logic, and debug mode work correctly.

---

## Phase 11: Teardown Command

### 11.1 Teardown Implementation
- [ ] Implement `zero-ops mgmt teardown` command
- [ ] Add --name flag (required)
- [ ] Add --force flag (direct Hetzner API deletion)
- [ ] Add --confirm flag (safety check for force deletion)

### 11.2 Graceful Deletion
- [ ] Delete Cluster resource from Management Cluster
- [ ] Wait for CAPI deletion cascade (15-minute timeout)
- [ ] Verify all Hetzner resources deleted via API

### 11.3 Force Deletion
- [ ] Query Hetzner API for resources with cluster tag
- [ ] Delete all matching resources directly
- [ ] Verify deletion via API

### 11.4 Local Cleanup
- [ ] Remove kubeconfig file
- [ ] Remove talosconfig file
- [ ] Remove context from ~/.kube/config (if merged)
- [ ] Remove CLI cache in ~/.zero-ops/clusters/<cluster-name>/

### Manual Testing (Phase 11)
```bash
# Test graceful teardown
./bin/zero-ops mgmt teardown --name=mothership
# Expected: Cluster deleted via CAPI, Hetzner resources removed

# Verify Hetzner resources deleted (via Hetzner Console)
# Expected: No VMs, LBs, networks for cluster

# Test force teardown
./bin/zero-ops mgmt teardown --name=mothership --force --confirm
# Expected: Resources deleted directly via Hetzner API

# Verify local cleanup
ls mothership.kubeconfig mothership.talosconfig
# Expected: Files not found
```

**Approval Gate:** User confirms teardown command works for both graceful and force deletion.

---

## Phase 12: Upgrade & Reconciliation

### 12.1 Upgrade Command
- [ ] Add --upgrade flag to bootstrap command
- [ ] Detect existing cluster
- [ ] Skip provisioning if cluster exists

### 12.2 Component Reconciliation
- [ ] Re-apply ClusterClass definitions (declarative update)
- [ ] Re-apply component manifests (ArgoCD, capi2argo, CNPG)
- [ ] Wait for all components to reach Ready state

### 12.3 Version Compatibility Check
- [ ] Verify CAPI API version compatibility (v1beta1)
- [ ] Check Kubernetes version compatibility
- [ ] Detect breaking changes in ClusterClass schema

### Manual Testing (Phase 12)
```bash
# Create initial cluster
./bin/zero-ops mgmt bootstrap --name=mothership --region=fsn1 --talos-image-id=<valid-id>

# Run upgrade (with newer CLI version)
./bin/zero-ops mgmt bootstrap --name=mothership --region=fsn1 --talos-image-id=<valid-id> --upgrade

# Verify ClusterClasses updated
kubectl --kubeconfig=mothership.kubeconfig get clusterclass -n zero-ops-system -o yaml
# Expected: ClusterClasses match embedded versions in CLI

# Verify components updated
kubectl --kubeconfig=mothership.kubeconfig get deployment -n argocd
# Expected: ArgoCD version matches CLI embedded version
```

**Approval Gate:** User confirms upgrade/reconciliation works without disrupting existing cluster.

---

## Final Validation

### End-to-End Test
```bash
# Full bootstrap from scratch
./bin/zero-ops mgmt bootstrap --name=mothership --region=fsn1 --talos-image-id=<valid-id>

# Verify cluster accessible
kubectl --kubeconfig=mothership.kubeconfig get nodes
# Expected: 5 nodes (3 control plane + 2 workers) Ready

# Verify CAPI self-hosted
kubectl --kubeconfig=mothership.kubeconfig get pods -n capi-system
# Expected: CAPI controllers running on Management Cluster

# Verify ClusterClass library
kubectl --kubeconfig=mothership.kubeconfig get clusterclass -n zero-ops-system
# Expected: 4 ClusterClasses available

# Verify platform components
kubectl --kubeconfig=mothership.kubeconfig get pods -n argocd
kubectl --kubeconfig=mothership.kubeconfig get pods -n capi2argo-system
kubectl --kubeconfig=mothership.kubeconfig get pods -n cnpg-system
# Expected: All pods Running

# Verify Talos access
talosctl --talosconfig=mothership.talosconfig -n <node-ip> version
# Expected: Talos version displayed (no SSH required)

# Verify ArgoCD access
# Open ArgoCD UI with credentials from success message
# Expected: ArgoCD UI accessible, can login with admin credentials

# Teardown
./bin/zero-ops mgmt teardown --name=mothership --confirm
# Expected: Clean deletion, no orphaned resources
```

**Final Approval:** User confirms complete bootstrap flow works end-to-end with all components functional.

---

## Implementation Notes

**Development Order:**
- Phases 1-3: Foundation (CLI, validation, bootstrap cluster)
- Phases 4-6: Core CAPI (operator, provisioning, pivot)
- Phases 7-9: Platform services (ClusterClass, components, configs)
- Phases 10-12: Operations (error handling, teardown, upgrade)

**Testing Strategy:**
- Manual testing after each phase (no automated tests required)
- User approval gate before proceeding to next phase
- Final end-to-end validation before marking complete

**Dependencies:**
- Hetzner Cloud account with API token
- Valid Talos Linux snapshot ID in Hetzner
- Docker and Kind installed locally
- Internet access for binary downloads

**Estimated Timeline:**
- Phase 1-3: 2-3 days (foundation)
- Phase 4-6: 3-4 days (CAPI core)
- Phase 7-9: 2-3 days (platform services)
- Phase 10-12: 2-3 days (operations)
- Total: ~10-13 days for complete implementation

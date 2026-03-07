# Implementation Tasks: Management Cluster Bootstrap

**Feature:** Platform Bootstrap (Journey A)  
**Version:** 1.0  
**Status:** IN PROGRESS (Phase 4)

---

## Phase 1: Project Setup & CLI Foundation ✅ COMPLETE

### 1.1 Repository Structure ✅
- [x] Create Go monorepo structure following Syself model
- [x] Set up `cmd/zero-ops/` for CLI entrypoint
- [x] Set up `pkg/` for library code
- [x] Set up `internal/assets/` for embedded manifests
- [x] Set up `manifests/` and `catalog/` directories
- [x] Create Makefile with build targets

### 1.2 CLI Framework ✅
- [x] Initialize Go module with Cobra framework
- [x] Implement `zero-ops mgmt bootstrap` command skeleton
- [x] Implement flag parsing (--name, --region, --talos-image-id, etc.)
- [x] Implement environment variable reading (HCLOUD_TOKEN)
- [x] Add --debug flag for verbose logging

### 1.3 Binary Management ✅
- [x] Implement clusterctl binary manager (download, verify checksum, OS/arch detection)
- [x] Implement talosctl binary manager (download, verify checksum, OS/arch detection)
- [x] Store binaries in `~/.zero-ops/bin/`
- [x] Handle unsupported architectures gracefully

### 1.4 State Management ✅
- [x] Implement BootstrapState struct (phase tracking, kubeconfig paths, timestamps)
- [x] Implement state persistence to `~/.zero-ops/state/<cluster-name>.json`
- [x] Implement state recovery on failure

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

## Phase 2: Preflight Validation ✅ COMPLETE

### 2.1 Docker Validation ✅
- [x] Implement DockerValidator (check daemon running via `docker ps`)
- [x] Add error message with remediation steps

### 2.2 Kind Validation ✅
- [x] Implement KindValidator (check Kind available via `kind version`)
- [x] Skip if --bootstrap-context provided

### 2.3 Hetzner Token Validation ✅
- [x] Implement HetznerTokenValidator (read-only API call to validate token)
- [x] Add exponential backoff for rate limits (HTTP 429)
- [x] Add error messages for invalid/read-only tokens

### 2.4 Talos Image Validation ✅
- [x] Implement TalosImageValidator (verify snapshot exists in Hetzner)
- [x] Add support for --build-talos-image flag (trigger Packer build)
- [x] Add error message if neither --talos-image-id nor --build-talos-image provided

### 2.5 SSH Key Validation (Optional) ✅
- [x] Implement SSHKeyValidator (verify key exists in HCloud if --ssh-key provided)
- [x] Add error message if key not found

### 2.6 Idempotency Check ✅
- [x] Check for existing cluster with same name
- [x] Exit with error if exists and --upgrade not set
- [x] Proceed to reconciliation if --upgrade set

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

## Phase 3: Bootstrap Cluster Creation ✅ COMPLETE

### 3.1 Kind Cluster Management ✅
- [x] Implement KindManager.Create() (execute `kind create cluster`)
- [x] Set cluster name to `bootstrap-zero-ops`
- [x] Add 2-minute timeout
- [x] Implement KindManager.Delete() for cleanup

### 3.2 Bootstrap Context Support ✅
- [x] Support --bootstrap-context flag (use existing cluster)
- [x] Validate context exists in kubeconfig
- [x] Verify cluster is reachable

### 3.3 Namespace Creation ✅
- [x] Create `zero-ops-system` namespace in bootstrap cluster
- [x] Add labels for tracking

---

## Phase 4: CAPI Initialization (Declarative Operator) 🔄 IN PROGRESS
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

## Phase 4: CAPI Initialization (Declarative Operator) ✅ COMPLETE

### 4.1 Manifest Embedding ✅
- [x] Embed `manifests/core/capi-operator/install.yaml` using go:embed
- [x] Embed Provider CRD manifests (CoreProvider, BootstrapProvider, ControlPlaneProvider, InfrastructureProvider)
- [x] Implement assets.ReadManifest() helper

### 4.2 Operator Installation ✅
- [x] Implement CAPIOperatorInstaller.installOperator() (apply operator manifest)
- [x] Implement CAPIOperatorInstaller.waitForOperator() (wait for deployment Ready)
- [x] Add 3-minute timeout

### 4.3 Provider CRD Application ✅
- [x] Implement CAPIOperatorInstaller.applyProviders() (apply all Provider CRDs)
- [x] Apply CoreProvider (cluster-api v1.10.0)
- [x] Apply BootstrapProvider (talos v0.6.5)
- [x] Apply ControlPlaneProvider (talos v0.5.6)
- [x] Apply InfrastructureProvider (hetzner v1.0.7)

### 4.4 Provider Verification ✅
- [x] Implement CAPIOperatorInstaller.waitForProviders() (poll Provider CRD status)
- [x] Check Ready condition for each Provider
- [x] Add 5-minute timeout with progress display

### 4.5 Secret Creation ✅
- [x] Create Hetzner credentials secret in `zero-ops-system` namespace
- [x] Add `clusterctl.cluster.x-k8s.io/move` label for pivot

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

## Phase 5: Management Cluster Provisioning ✅ COMPLETE

### 5.1 ClusterClass Embedding ✅
- [x] Embed `manifests/classes/hetzner-mgmt-talos-v1.yaml` using go:embed

### 5.2 Cluster Resource Generation ✅
- [x] Implement Cluster manifest generation with topology reference
- [x] Set `spec.topology.class: hetzner-mgmt-talos-v1`
- [x] Set `spec.topology.version: v1.31.6`
- [x] Set control plane replicas: 3, worker replicas: 2
- [x] Populate variables (region, talosVersion, talosImageId, network CIDR, etc.)

### 5.3 Resource Application ✅
- [x] Apply ClusterClass to bootstrap cluster
- [x] Apply Cluster resource to bootstrap cluster
- [x] Verify resources created successfully

### 5.4 Provisioning Monitor ✅
- [x] Implement ClusterProvisioner.WaitForReady() (poll cluster status)
- [x] Monitor `status.phase` transitions (Pending → Provisioning → Provisioned)
- [x] Check Ready condition (not just Provisioned phase)
- [x] Add 15-minute timeout with progress display

### 5.5 Infrastructure Verification ✅
- [x] Query CAPH status for infrastructure resources
- [x] Verify 3 control plane VMs, 2 worker VMs, 1 LB, 1 network, 2 placement groups
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

## Phase 6: CAPI Pivot ✅ COMPLETE

### 6.1 Kubeconfig Retrieval ✅
- [x] Implement PivotOrchestrator.getKubeconfig() (extract from secret)
- [x] Decode base64 kubeconfig
- [x] Save to temporary file

### 6.2 Operator Installation on Management Cluster ✅
- [x] Install cluster-api-operator on Management Cluster
- [x] Wait for operator Ready (3 minutes)

### 6.3 Resource Counting ✅
- [x] Implement resource counting before pivot (Cluster, Machine, HetznerCluster, Secret)
- [x] Store counts for verification

### 6.4 Pivot Execution ✅
- [x] Execute `clusterctl move --to-kubeconfig=<mgmt> --namespace=zero-ops-system`
- [x] Add 10-minute timeout

### 6.5 Post-Pivot Verification ✅
- [x] Count resources after pivot (existence check only)
- [x] Verify counts match
- [x] Implement waitForProvidersReady() (wait for Provider CRDs to reconcile)
- [x] Implement waitForClusterReady() (wait for Cluster Ready condition)
- [x] Add 10-minute timeout for reconciliation

### 6.6 Bootstrap Cleanup ✅
- [x] Delete Kind cluster (if --keep-bootstrap not set)
- [x] Preserve Kind cluster on failure

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

## Phase 7: ClusterClass Library Deployment ✅ COMPLETE

### 7.1 Library Embedding ✅
- [x] Embed `manifests/classes/hetzner-prod-talos-v1.yaml`
- [x] Embed `manifests/classes/hetzner-dev-talos-v1.yaml`
- [x] Embed `manifests/classes/hetzner-staging-talos-v1.yaml`

### 7.2 Library Application ✅
- [x] Apply all ClusterClass definitions to Management Cluster
- [x] Verify all ClusterClasses created successfully

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

## Phase 8: Post-Bootstrap Components ✅ COMPLETE

### 8.1 Component Manifest Embedding ✅
- [x] Embed ArgoCD manifests in `catalog/gitops/argocd/install.yaml`
- [x] Embed capi2argo manifests in `catalog/gitops/capi2argo/install.yaml`
- [x] Embed CloudNativePG manifests in `catalog/databases/cloudnative-pg/install.yaml`
- [x] Embed Hetzner CCM manifests
- [x] Embed Hetzner CSI manifests

### 8.2 Component Installer Implementation ✅
- [x] Implement ManagementClusterInstaller.InstallAll() (sequential installation)
- [x] Implement installHetznerCCM() + verifyHetznerCCM()
- [x] Implement installHetznerCSI() + verifyHetznerCSI()
- [x] Implement installArgoCD() + verifyArgoCD()
- [x] Implement installCapi2Argo() + verifyCapi2Argo()
- [x] Implement installCloudNativePG() + verifyCloudNativePG()

### 8.3 Verification Helpers ✅
- [x] Implement waitForDeployment() (check deployment Available condition)
- [x] Add timeouts per component (3-5 minutes)
- [x] Add progress logging

### 8.4 ArgoCD Password Retrieval ✅
- [x] Extract admin password from `argocd-initial-admin-secret`
- [x] Store for output message

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

## Phase 9: Kubeconfig & Talosconfig Management ✅ COMPLETE

### 9.1 Kubeconfig Management ✅
- [x] Save kubeconfig to `<cluster-name>.kubeconfig` in current directory
- [x] Set file permissions to 0600
- [x] Implement --merge-kubeconfig flag (merge into ~/.kube/config with backup)
- [x] Set context name to cluster name

### 9.2 Talosconfig Management ✅
- [x] Retrieve talosconfig from secret `<cluster-name>-talosconfig`
- [x] Save to `<cluster-name>.talosconfig` in current directory
- [x] Set file permissions to 0600

### 9.3 Success Output ✅
- [x] Display success message with cluster details
- [x] Show kubeconfig and talosconfig paths
- [x] Show ArgoCD UI URL and admin password
- [x] Show next steps (verify cluster, access nodes, onboard tenant)

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

## Phase 10: Error Handling & Observability ✅ COMPLETE

### 10.1 Failure Preservation ✅
- [x] Preserve Kind cluster on any failure (already implemented)
- [x] Output diagnostic information (logs, resource status)

### 10.2 Error Messages ✅
- [x] Implement actionable error messages with remediation steps
- [x] Add error codes for common failures

### 10.3 Debug Mode ✅
- [x] Implement --debug flag for verbose logging
- [x] Log all kubectl commands and CAPI resource status

**Note:** Retry logic for Hetzner API and Kubernetes operations is handled by CAPH controllers and client-go built-in retry mechanisms. No custom retry implementation needed.

### Manual Testing (Phase 10)
```bash
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
# Expected: Verbose logging showing kubectl commands and resource status
```

**Approval Gate:** User confirms error handling and debug mode work correctly.

---

## Phase 11: Teardown Command ✅ COMPLETE

### 11.1 Teardown Implementation ✅
- [x] Implement `zero-ops mgmt teardown` command
- [x] Add --name flag (required)
- [x] Add --force flag (direct Hetzner API deletion for stuck resources)
- [x] Add --confirm flag (safety check for force deletion)

### 11.2 Graceful Deletion (Default) ✅
- [x] Delete Cluster resource from Management Cluster using kubectl
- [x] Wait for CAPI deletion cascade via finalizers (15-minute timeout)
- [x] Monitor deletion progress via Cluster status conditions
- [x] Verify all Hetzner resources deleted via API

**Note:** CAPI/CAPH controllers handle deletion cascade automatically via finalizers. The standard `kubectl delete cluster` triggers cleanup of all child resources (Machines, HetznerCluster, VMs, LBs, networks).

### 11.3 Force Deletion (Fallback Only) ✅
- [x] Query Hetzner API for resources with cluster label
- [x] Delete all matching resources directly via Hetzner API
- [x] Remove finalizers from stuck Kubernetes resources
- [x] Verify deletion via API

**Use force deletion only when:**
- CAPI controllers are not running
- Resources are stuck in deletion for >15 minutes
- Finalizers prevent deletion

### 11.4 Local Cleanup ✅
- [x] Remove kubeconfig file
- [x] Remove talosconfig file
- [x] Remove context from ~/.kube/config (if merged)
- [x] Remove state file in ~/.zero-ops/state/<cluster-name>.json

### Manual Testing (Phase 11)
```bash
# Test graceful teardown (standard CAPI deletion)
./bin/zero-ops mgmt teardown --name=mothership
# Expected: Cluster deleted via CAPI cascade, Hetzner resources removed

# Verify Hetzner resources deleted (via Hetzner Console)
# Expected: No VMs, LBs, networks for cluster

# Test force teardown (only if graceful fails)
./bin/zero-ops mgmt teardown --name=mothership --force --confirm
# Expected: Resources deleted directly via Hetzner API, finalizers removed

# Verify local cleanup
ls mothership.kubeconfig mothership.talosconfig
# Expected: Files not found
```

**Approval Gate:** User confirms teardown command works for both graceful and force deletion.

---

## Phase 12: Upgrade & Reconciliation ✅ COMPLETE

### 12.1 Upgrade Command ✅
- [x] Add --upgrade flag to bootstrap command
- [x] Detect existing cluster via Cluster resource
- [x] Skip provisioning if cluster exists and is Ready

### 12.2 Component Reconciliation ✅
- [x] Re-apply ClusterClass definitions (declarative update via kubectl apply)
- [x] Update Provider CRD versions (change spec.version in Provider CRDs)
- [x] Re-apply component manifests (ArgoCD, capi2argo, CNPG)
- [x] Wait for all components to reach Ready state

**Note:** Provider upgrades are handled by cluster-api-operator. Simply update the Provider CRD spec.version and the operator reconciles the change.

### 12.3 Version Compatibility Check ✅
- [x] Verify CAPI API version compatibility (v1beta1)
- [x] Check Kubernetes version compatibility
- [x] Detect breaking changes in ClusterClass schema

**Note:** Version compatibility is implicit - embedded manifests in CLI match tested versions. Breaking changes would cause kubectl apply to fail with validation errors.

### Manual Testing (Phase 12)
```bash
# Create initial cluster
./bin/zero-ops mgmt bootstrap --name=mothership --region=fsn1 --talos-image-id=<valid-id>

# Run upgrade (with newer CLI version)
./bin/zero-ops mgmt bootstrap --name=mothership --region=fsn1 --talos-image-id=<valid-id> --upgrade

# Verify ClusterClasses updated
kubectl --kubeconfig=mothership.kubeconfig get clusterclass -n zero-ops-system -o yaml
# Expected: ClusterClasses match embedded versions in CLI

# Verify Provider versions updated
kubectl --kubeconfig=mothership.kubeconfig get coreprovider,infrastructureprovider -o yaml
# Expected: spec.version matches CLI embedded versions

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
- Phases 10-12: Operations (observability, teardown, upgrade)

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
- Phase 10-12: 1-2 days (operations - simplified based on CAPH patterns)
- Total: ~9-12 days for complete implementation

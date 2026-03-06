# Requirements Specification: Management Cluster Bootstrap

**Feature:** Platform Bootstrap (Journey A)  
**Version:** 2.0 (Talos Linux)  
**Status:** APPROVED  
**Last Updated:** 2025-01-XX

---

## 1. Overview

### 1.1 Purpose

This specification defines the requirements for bootstrapping a self-hosted Management Cluster ("Mothership") on Hetzner Cloud infrastructure using Cluster API (CAPI), Cluster API Provider Hetzner (CAPH), and Talos Linux. The Management Cluster serves as the control plane for the Zero-Ops Platform, hosting CAPI controllers, ArgoCD, and platform services that manage tenant Kubernetes clusters. All clusters (Management and tenant workload clusters) run Talos Linux for immutable, API-driven, zero-touch operations. Uses declarative Kubernetes Operators that natively integrates with CAPI.

### 1.2 Scope

**In Scope:**
- Ephemeral bootstrap cluster creation (Kind)
- CAPI/CAPH/Talos providers installation and initialization
- Talos Linux image management (Packer-based or pre-built)
- Management Cluster provisioning on Hetzner Cloud with Talos Linux
- CAPI state pivot from bootstrap to Management Cluster
- ClusterClass library deployment (Talos-based topologies)
- Post-bootstrap component installation (ArgoCD, capi2argo, CloudNativePG)
- Kubeconfig and talosconfig generation and local storage
- Idempotency and error recovery

**Out of Scope:**
- Tenant cluster provisioning (Journey B)
- Service catalog deployment (Journey D)
- Web UI integration
- Multi-cloud support (AWS/GCP)
- Bare metal Hetzner servers (Phase 1)
- Ubuntu/kubeadm-based clusters (deferred to future)

### 1.3 Success Criteria

The bootstrap process is considered successful when:
1. Management Cluster is running on Hetzner Cloud with 3 Talos control plane nodes and 2+ Talos worker nodes
2. CAPI/CAPH/Talos providers are operational on the Management Cluster (self-hosted)
3. ClusterClass library (Talos-based topologies) is available in `zero-ops-system` namespace
4. ArgoCD, capi2argo operator, and CloudNativePG operator are installed and healthy
5. Kubeconfig and talosconfig are saved locally and accessible
6. Bootstrap Kind cluster is deleted
7. All nodes are accessible via talosctl (mTLS, no SSH)
8. All E2E test cases pass (reference: `e2e-tdd.md`)

---

## 2. User Personas

### 2.1 Platform Admin (Primary)

**Role:** SaaS operator responsible for platform infrastructure  
**Goals:**
- Bootstrap Management Cluster quickly and reliably
- Minimize manual configuration steps
- Ensure production-grade HA setup
- Recover from failures without data loss

**Technical Profile:**
- Familiar with Kubernetes concepts
- Has Hetzner Cloud account and API token
- Comfortable with CLI tools
- May not be familiar with CAPI internals

---

## 3. Functional Requirements

### 3.1 Command Interface

**REQ-CLI-001: Bootstrap Command**
- **Description:** CLI must provide a single command to bootstrap the Management Cluster
- **Command:** `zero-ops mgmt bootstrap --name=<cluster-name> --region=<hetzner-region> --ssh-key=<key-name>`
- **Behavior:** Executes all bootstrap phases sequentially
- **Priority:** CRITICAL

**REQ-CLI-002: Required Flags**
- **Description:** Bootstrap command must require the following flags:
  - `--name`: Management Cluster name (alphanumeric, hyphens allowed)
  - `--region`: Hetzner region (fsn1, nbg1, hel1)
  - `--talos-image-id`: Talos Linux snapshot ID in Hetzner (or trigger Packer build if not provided)
- **Validation:** CLI must validate flag values before proceeding
- **Priority:** CRITICAL

**REQ-CLI-003: Optional Flags**
- **Description:** Bootstrap command must support optional flags:
  - `--bootstrap-context`: Use existing K8s cluster instead of Kind
  - `--keep-bootstrap`: Preserve Kind cluster after successful pivot
  - `--dry-run`: Validate prerequisites and generate manifests without provisioning
  - `--merge-kubeconfig`: Merge generated kubeconfig into `~/.kube/config` (default: false)
  - `--network-cidr`: Custom CIDR for Hetzner private network (default: 10.0.0.0/16)
  - `--upgrade`: Reconcile/update existing cluster components to match CLI version
  - `--ssh-key`: SSH key name for Hetzner Rescue Mode emergencies only (optional)
  - `--build-talos-image`: Trigger Packer build for Talos image if `--talos-image-id` not provided
- **Priority:** IMPORTANT

**REQ-CLI-004: Environment Variables**
- **Description:** CLI must read Hetzner API token from environment variable
- **Variable:** `HCLOUD_TOKEN`
- **Validation:** CLI must validate token before proceeding
- **Priority:** CRITICAL

### 3.2 Pre-Flight Validation

**REQ-PREFLIGHT-001: Docker Daemon Check**
- **Description:** CLI must verify Docker daemon is running
- **Method:** Execute `docker ps` and check exit code
- **Failure Behavior:** Exit with error message: "Docker daemon not running. Please start Docker."
- **Priority:** CRITICAL

**REQ-PREFLIGHT-002: Kind Availability Check**
- **Description:** CLI must verify Kind is available (if not using `--bootstrap-context`)
- **Method:** Execute `kind version` and check exit code
- **Failure Behavior:** Exit with error message: "Kind not found. Please install Kind."
- **Priority:** CRITICAL

**REQ-PREFLIGHT-003: Hetzner Token Validation**
- **Description:** CLI must validate Hetzner API token has Write permissions
- **Method:** Dry-run write operation:
  1. Create dummy SSH key with random name: `zero-ops-preflight-<uuid>`
  2. Verify creation succeeds (HTTP 201)
  3. Delete dummy SSH key immediately
  4. If any step fails, token is invalid or lacks Write scope
- **Failure Behavior:** Exit with error message: "Hetzner token lacks Write permissions. Please use a token with Read+Write scope."
- **Retry Logic:** Exponential backoff for HTTP 429 (rate limit), up to 5 attempts
- **Priority:** CRITICAL

**REQ-PREFLIGHT-004: Talos Image Validation**
- **Description:** CLI must verify Talos image exists in Hetzner or trigger build
- **Method:** 
  - If `--talos-image-id` provided: Query Hetzner API to verify snapshot exists
  - If `--build-talos-image` set: Trigger Packer build process
  - If neither provided: Exit with error
- **Failure Behavior:** Exit with error message: "Talos image not found. Provide --talos-image-id or use --build-talos-image."
- **Priority:** CRITICAL

**REQ-PREFLIGHT-004a: SSH Key Existence Check (Optional)**
- **Description:** If `--ssh-key` flag provided, CLI must verify SSH key exists in HCloud
- **Method:** Query Hetzner API for SSH key by name
- **Failure Behavior:** Exit with error message: "SSH key '<key-name>' not found in HCloud. Please upload it first."
- **Note:** SSH key only used for Hetzner Rescue Mode emergencies, not for Talos node access
- **Priority:** IMPORTANT

**REQ-PREFLIGHT-005: Idempotency Check**
- **Description:** CLI must detect existing Management Cluster with same name
- **Method:** Check for Cluster resource in `zero-ops-system` namespace (if bootstrap context exists)
- **Behavior:** 
  - If cluster exists and `--upgrade` flag NOT set: Exit with message: "Management Cluster '<name>' already exists. Use --upgrade to reconcile components."
  - If cluster exists and `--upgrade` flag set: Proceed to reconcile ClusterClass and components (see REQ-UPGRADE-001)
- **Priority:** CRITICAL

### 3.3 Bootstrap Cluster Creation

**REQ-BOOTSTRAP-001: Kind Cluster Creation**
- **Description:** CLI must create ephemeral Kind cluster for bootstrap
- **Cluster Name:** `bootstrap-zero-ops`
- **Command:** `kind create cluster --name=bootstrap-zero-ops`
- **Timeout:** 2 minutes
- **Failure Behavior:** Exit with error, preserve logs
- **Priority:** CRITICAL

**REQ-BOOTSTRAP-002: Existing Cluster Support**
- **Description:** CLI must support using existing K8s cluster via `--bootstrap-context` flag
- **Validation:** Verify context exists in kubeconfig and cluster is reachable
- **Behavior:** Skip Kind creation, use provided context
- **Priority:** IMPORTANT

**REQ-BOOTSTRAP-003: clusterctl Management**
- **Description:** CLI must manage clusterctl binary automatically
- **Location:** `~/.zero-ops/bin/clusterctl`
- **Behavior:** 
  1. Detect host OS (Linux/Darwin) and Architecture (AMD64/ARM64)
  2. Download appropriate binary if not present
  3. Verify version compatibility with CAPH v1.0.7 and Talos providers
- **Unsupported Architecture Handling:** Exit with error: "Unsupported architecture (<os>/<arch>). Please install clusterctl manually in PATH."
- **Supported Platforms:** linux/amd64, linux/arm64, darwin/amd64, darwin/arm64
- **Priority:** CRITICAL

**REQ-BOOTSTRAP-004: talosctl Management**
- **Description:** CLI must manage talosctl binary automatically
- **Location:** `~/.zero-ops/bin/talosctl`
- **Behavior:**
  1. Detect host OS (Linux/Darwin) and Architecture (AMD64/ARM64)
  2. Download appropriate binary if not present
  3. Verify version matches Talos image version (v1.12.x)
- **Purpose:** Required for generating Talos configs, debugging, and node management
- **Unsupported Architecture Handling:** Exit with error: "Unsupported architecture (<os>/<arch>). Please install talosctl manually in PATH."
- **Priority:** CRITICAL

### 3.4 CAPI Initialization

**REQ-CAPI-001: CAPI Installation**
- **Description:** CLI must install cluster-api-operator and apply Provider CRDs declaratively on bootstrap cluster
- **Method:**
  1. Install cluster-api-operator deployment
  2. Wait for operator Ready (3 minutes)
  3. Apply Provider CRDs (CoreProvider, BootstrapProvider, ControlPlaneProvider, InfrastructureProvider)
  4. Wait for all Provider CRDs to reach Ready condition (5 minutes)
- **Providers Installed:**
  - CAPI Core (cluster-api) via CoreProvider CRD
  - CABPT (cluster-api-bootstrap-provider-talos) via BootstrapProvider CRD
  - CACPPT (cluster-api-control-plane-provider-talos) via ControlPlaneProvider CRD
  - CAPH (cluster-api-provider-hetzner) via InfrastructureProvider CRD
- **Timeout:** 8 minutes total
- **Verification:** Check Provider CRD status conditions (Ready=True)
- **Priority:** CRITICAL

**REQ-CAPI-002: Namespace Creation**
- **Description:** CLI must create `zero-ops-system` namespace in bootstrap cluster
- **Timing:** After CAPI installation, before secret creation
- **Priority:** CRITICAL

**REQ-CAPI-003: Secret Creation**
- **Description:** CLI must create Hetzner credentials secret in bootstrap cluster
- **Namespace:** `zero-ops-system`
- **Secret Name:** `hetzner-credentials`
- **Data:** `hcloud: <HCLOUD_TOKEN>`
- **Labels:** `clusterctl.cluster.x-k8s.io/move: ""`
- **Priority:** CRITICAL

### 3.5 Management Cluster Provisioning

**REQ-MGMT-001: ClusterClass Definition**
- **Description:** CLI must embed ClusterClass definition for Talos-based Management Cluster
- **File:** `manifests/classes/hetzner-mgmt-talos-v1.yaml`
- **Embedding:** Use `go:embed` directive
- **Application:** Apply to bootstrap cluster in `zero-ops-system` namespace before Cluster resource
- **Structure:**
  - Control Plane: `TalosControlPlane` (replaces KubeadmControlPlane)
  - Worker Bootstrap: `TalosConfigTemplate` (replaces KubeadmConfigTemplate)
  - Infrastructure: `HetznerClusterTemplate` (unchanged)
- **Integrated Components via Talos Machine Config Patches:**
  - `hcloud-cloud-controller-manager` (CCM) - applied via machine config patch
  - `hcloud-csi-driver` - applied via machine config patch
  - Cilium CNI - embedded in Talos config
- **Priority:** CRITICAL

**REQ-MGMT-002: Cluster Resource Generation**
- **Description:** CLI must generate Cluster resource with Talos topology reference
- **Topology:**
  - `spec.topology.class: hetzner-mgmt-talos-v1`
  - `spec.topology.version: v1.31.6` (Kubernetes version)
  - `spec.topology.controlPlane.replicas: 3`
  - `spec.topology.workers.machineDeployments[0].replicas: 2`
- **Variables:**
  - `region: <user-provided>`
  - `talosVersion: v1.12.0` (Talos Linux version)
  - `talosImageId: <user-provided or built via Packer>`
  - `hcloudSSHKeyName: [{ name: <user-provided> }]` (optional, rescue mode only)
  - `hcloudNetwork.enabled: true`
  - `hcloudNetwork.cidrBlock: <user-provided or default 10.0.0.0/16>`
  - `hcloudNetwork.subnetCidrBlock: <derived from cidrBlock, /24 subnet>`
  - `hcloudNetwork.networkZone: eu-central`
  - `hcloudControlPlaneMachineType: cpx31`
  - `hcloudWorkerMachineType: cpx31`
  - `hcloudPlacementGroups: [{ name: control-plane, type: spread }, { name: workers, type: spread }]`
- **CIDR Configuration:** If `--network-cidr` flag provided, use it; otherwise default to 10.0.0.0/16
- **Priority:** CRITICAL

**REQ-MGMT-003: Cluster Provisioning**
- **Description:** CLI must apply Cluster resource to bootstrap cluster
- **Namespace:** `zero-ops-system`
- **Monitoring:** Poll `status.phase` every 30 seconds
- **Phases:** `Pending` → `Provisioning` → `Provisioned` → `Ready`
- **Timeout:** 15 minutes
- **Progress Display:** Show estimated time remaining and current phase
- **Priority:** CRITICAL

**REQ-MGMT-004: Infrastructure Verification**
- **Description:** CLI must verify Hetzner infrastructure is created
- **Resources to Check:**
  - 3 control plane VMs (CPX31)
  - 2 worker VMs (CPX31)
  - 1 load balancer
  - 1 private network
  - 2 placement groups
- **Method:** Query CAPH status conditions
- **Priority:** IMPORTANT

### 3.6 CAPI Pivot

**REQ-PIVOT-001: Kubeconfig Retrieval**
- **Description:** CLI must retrieve Management Cluster kubeconfig from secret
- **Secret Name:** `<cluster-name>-kubeconfig`
- **Namespace:** `zero-ops-system`
- **Method:** `kubectl get secret <cluster-name>-kubeconfig -n zero-ops-system -o jsonpath='{.data.value}' | base64 -d`
- **Priority:** CRITICAL

**REQ-PIVOT-002: CAPI Installation on Management Cluster**
- **Description:** CLI must install cluster-api-operator on Management Cluster before pivot
- **Method:**
  1. Install cluster-api-operator deployment on Management Cluster
  2. Wait for operator Ready (3 minutes)
- **Timeout:** 3 minutes
- **Note:** Provider CRDs will be moved automatically during `clusterctl move` and reconciled by the operator
- **Verification:** Operator deployment Ready on Management Cluster
- **Priority:** CRITICAL

**REQ-PIVOT-003: State Migration**
- **Description:** CLI must move CAPI resources from bootstrap to Management Cluster
- **Command:** `clusterctl move --to-kubeconfig=<mgmt-kubeconfig> --namespace=zero-ops-system`
- **Timeout:** 10 minutes
- **Priority:** CRITICAL

**REQ-PIVOT-004: Resource Count Verification**
- **Description:** CLI must verify all resources were moved successfully
- **Method:**
  1. Before pivot: Count Cluster, Machine, HetznerCluster, Secret resources in bootstrap cluster
  2. After pivot: Count same resources in Management Cluster (existence check only)
  3. Compare counts
- **Verification Scope:** Check resource existence, not Ready status (controllers need time to reconcile)
- **Failure Behavior:** If counts don't match, preserve bootstrap cluster and exit with error
- **Priority:** CRITICAL

**REQ-PIVOT-005: Bootstrap Cleanup**
- **Description:** CLI must delete bootstrap Kind cluster after successful pivot
- **Command:** `kind delete cluster --name=bootstrap-zero-ops`
- **Condition:** Only if pivot succeeded and `--keep-bootstrap` flag not set
- **Failure Preservation:** If any step fails, preserve Kind cluster for debugging
- **Priority:** CRITICAL

### 3.7 ClusterClass Library Deployment

**REQ-CLUSTERCLASS-001: Library Embedding**
- **Description:** CLI must embed Talos-based ClusterClass definitions for tenant clusters
- **Files:**
  - `manifests/classes/hetzner-prod-talos-v1.yaml`
  - `manifests/classes/hetzner-dev-talos-v1.yaml`
  - `manifests/classes/hetzner-staging-talos-v1.yaml`
- **Structure:** All ClusterClasses use TalosControlPlane and TalosConfigTemplate
- **Embedding:** Use `go:embed` directive
- **Priority:** CRITICAL

**REQ-CLUSTERCLASS-002: Library Application**
- **Description:** CLI must apply Talos-based ClusterClass library to Management Cluster
- **Namespace:** `zero-ops-system`
- **Timing:** After pivot completes
- **Verification:** Verify all Talos ClusterClasses are created successfully
- **Expected Classes:** hetzner-mgmt-talos-v1, hetzner-prod-talos-v1, hetzner-dev-talos-v1, hetzner-staging-talos-v1
- **Priority:** CRITICAL

### 3.8 Post-Bootstrap Components

**REQ-POSTBOOT-001: ArgoCD Installation**
- **Description:** CLI must install ArgoCD on Management Cluster
- **Method:** Helm chart or embedded manifests
- **Namespace:** `argocd`
- **Version:** v2.x (latest stable)
- **Verification:** Wait for all ArgoCD pods Ready
- **Timeout:** 5 minutes
- **Admin Password Retrieval:** After installation, extract admin password from secret `argocd-initial-admin-secret` and store for output
- **Priority:** CRITICAL

**REQ-POSTBOOT-002: capi2argo Operator Installation**
- **Description:** CLI must install capi2argo operator on Management Cluster
- **Method:** Helm chart or embedded manifests
- **Namespace:** `capi2argo-system`
- **Configuration:** Watch `zero-ops-system` namespace for CAPI secrets
- **Verification:** Wait for operator pod Ready
- **Timeout:** 3 minutes
- **Priority:** CRITICAL

**REQ-POSTBOOT-003: CloudNativePG Operator Installation**
- **Description:** CLI must install CloudNativePG operator on Management Cluster
- **Method:** Helm chart or embedded manifests
- **Namespace:** `cnpg-system`
- **Verification:** Wait for operator pod Ready
- **Timeout:** 3 minutes
- **Priority:** CRITICAL

### 3.9 Upgrade & Reconciliation

**REQ-UPGRADE-001: Component Reconciliation**
- **Description:** When `--upgrade` flag is set and cluster exists, CLI must reconcile components to match embedded versions
- **Components to Reconcile:**
  1. ClusterClass definitions (hetzner-mgmt-v1, hetzner-prod-v1, hetzner-dev-v1, hetzner-staging-v1)
  2. CAPI/CAPH provider versions
  3. ArgoCD version
  4. capi2argo operator version
  5. CloudNativePG operator version
- **Method:** Apply embedded manifests with `kubectl apply` (declarative update)
- **Verification:** Wait for all updated components to reach Ready state
- **Rollback:** If any component fails to reconcile, preserve previous state and exit with error
- **Priority:** IMPORTANT

**REQ-UPGRADE-002: Version Compatibility Check**
- **Description:** Before reconciliation, CLI must verify compatibility between current and target versions
- **Checks:**
  - CAPI API version compatibility (v1beta1)
  - Kubernetes version compatibility (Management Cluster must support target K8s version)
  - Breaking changes in ClusterClass schema
- **Failure Behavior:** Exit with error if incompatible upgrade detected
- **Priority:** IMPORTANT

### 3.10 Teardown & Cleanup

**REQ-TEARDOWN-001: Teardown Command**
- **Description:** CLI must provide command to delete Management Cluster and all Hetzner resources
- **Command:** `zero-ops mgmt teardown --name=<cluster-name> [--force]`
- **Priority:** CRITICAL

**REQ-TEARDOWN-002: Graceful Deletion**
- **Description:** Default teardown behavior must use CAPI deletion cascade
- **Method:**
  1. Delete Cluster resource from Management Cluster
  2. Wait for CAPI to delete all child resources (Machines, HetznerCluster)
  3. Wait for CAPH to delete Hetzner infrastructure (VMs, LBs, networks)
  4. Verify all Hetzner resources deleted via API
- **Timeout:** 15 minutes
- **Failure Behavior:** If timeout or error, suggest using `--force` flag
- **Priority:** CRITICAL

**REQ-TEARDOWN-003: Force Deletion**
- **Description:** When `--force` flag set, CLI must delete Hetzner resources directly via API
- **Method:**
  1. Query Hetzner API for resources with tag `cluster=<cluster-name>`
  2. Delete all matching resources (servers, load balancers, networks, volumes, placement groups)
  3. Verify deletion via API
- **Safety Check:** Require `--confirm` flag for force deletion
- **Warning Message:** "Force deletion will bypass CAPI and delete resources directly. This may leave orphaned Kubernetes objects. Continue? (yes/no)"
- **Priority:** CRITICAL

**REQ-TEARDOWN-004: Local Cleanup**
- **Description:** After successful teardown, CLI must clean up local artifacts
- **Artifacts to Remove:**
  - `<cluster-name>.kubeconfig` file
  - Context from `~/.kube/config` (if merged)
  - CLI cache in `~/.zero-ops/clusters/<cluster-name>/`
- **Priority:** IMPORTANT

### 3.11 Kubeconfig & Talosconfig Management

**REQ-KUBECONFIG-001: Kubeconfig Local Storage**
- **Description:** CLI must save Management Cluster kubeconfig locally
- **File Name:** `<cluster-name>.kubeconfig`
- **Location:** Current working directory
- **Permissions:** 0600 (read/write for owner only)
- **Merge Behavior:** If `--merge-kubeconfig` flag set, merge context into `~/.kube/config` (backup original first)
- **Priority:** CRITICAL

**REQ-KUBECONFIG-002: Context Naming**
- **Description:** CLI must set kubeconfig context name to cluster name
- **Context Name:** `<cluster-name>`
- **User Name:** `<cluster-name>-admin`
- **Cluster Name:** `<cluster-name>`
- **Priority:** IMPORTANT

**REQ-TALOSCONFIG-001: Talosconfig Retrieval**
- **Description:** CLI must retrieve Talos configuration for node management
- **Method:** Extract from Secret `<cluster-name>-talosconfig` in `zero-ops-system` namespace
- **Purpose:** Required for OS-level operations (reboot, upgrade, debug) via talosctl
- **Priority:** CRITICAL

**REQ-TALOSCONFIG-002: Talosconfig Local Storage**
- **Description:** CLI must save talosconfig locally
- **File Name:** `<cluster-name>.talosconfig`
- **Location:** Current working directory
- **Permissions:** 0600 (read/write for owner only)
- **Content:** mTLS certificates and endpoints for Talos API (port 50000)
- **Priority:** CRITICAL

**REQ-KUBECONFIG-003: Output Message**
- **Description:** CLI must display success message with kubeconfig, talosconfig paths, and credentials
- **Message Format:**
  ```
  ✓ Management Cluster '<name>' ready (Xm Ys)
  ✓ Control plane: 3 Talos nodes (CPX31)
  ✓ Workers: 2 Talos nodes (CPX31)
  ✓ Kubernetes version: v1.31.6
  ✓ Talos version: v1.12.0
  ✓ API endpoint: https://<lb-ip>:6443
  ✓ Kubeconfig saved to: <cluster-name>.kubeconfig
  ✓ Talosconfig saved to: <cluster-name>.talosconfig
  ✓ ArgoCD UI: https://<argocd-endpoint>
    Admin password: <extracted-password>
  
  Next steps:
  1. Verify cluster: kubectl --kubeconfig=<cluster-name>.kubeconfig get nodes
  2. Access nodes: talosctl --talosconfig=<cluster-name>.talosconfig -n <node-ip> version
  3. Access ArgoCD: Open https://<argocd-endpoint> (username: admin)
  4. Onboard first tenant: zero-ops tenant onboard --name=<org>
  
  Note: Nodes are accessible via talosctl only (SSH disabled by design)
  ```
- **Priority:** IMPORTANT

---

## 4. Non-Functional Requirements

### 4.1 Performance

**REQ-PERF-001: Bootstrap Time**
- **Target:** Complete bootstrap in < 15 minutes (excluding Hetzner provisioning time)
- **Measurement:** From command execution to success message
- **Priority:** IMPORTANT

**REQ-PERF-002: Hetzner Provisioning Time**
- **Target:** Management Cluster provisioned in < 10 minutes
- **Dependency:** Hetzner Cloud API performance
- **Priority:** INFORMATIONAL

### 4.2 Reliability

**REQ-REL-001: Idempotency**
- **Description:** Running bootstrap command multiple times must not create duplicate resources
- **Verification:** E2E-BOOT-02 test case
- **Priority:** CRITICAL

**REQ-REL-002: Failure Recovery**
- **Description:** CLI must preserve state on failure for manual recovery
- **Behavior:** Keep Kind cluster, output diagnostic information
- **Verification:** E2E-BOOT-03 test case
- **Priority:** CRITICAL

**REQ-REL-003: Retry Logic**
- **Description:** CLI must retry transient failures with exponential backoff
- **Applicable Operations:**
  - Hetzner API calls (HTTP 429, 5xx)
  - Kubernetes API calls (connection errors)
  - Pod readiness checks
- **Max Retries:** 5 attempts
- **Backoff:** 2s, 4s, 8s, 16s, 32s
- **Priority:** CRITICAL

### 4.3 Security

**REQ-SEC-001: Credential Handling**
- **Description:** CLI must never log or display Hetzner API token
- **Behavior:** Mask token in all output and logs
- **Priority:** CRITICAL

**REQ-SEC-002: Kubeconfig Permissions**
- **Description:** CLI must set restrictive permissions on kubeconfig file
- **Permissions:** 0600 (owner read/write only)
- **Priority:** CRITICAL

**REQ-SEC-003: Talos Security Model**
- **Description:** Management Cluster must leverage Talos Linux security features
- **Includes:**
  - Immutable OS (read-only root filesystem)
  - No SSH access (disabled by design)
  - mTLS-only node access via talosctl (port 50000)
  - Secure boot support (optional)
  - Minimal attack surface (no package manager, no shell by default)
  - API-driven configuration (no manual file editing)
- **Source:** Talos Linux default security posture
- **Priority:** CRITICAL

**REQ-SEC-004: Kubernetes API Hardening**
- **Description:** Kubernetes API server must use hardened configuration
- **Includes:**
  - TLS cipher suites (TLS 1.2+)
  - RBAC enabled
  - Anonymous auth disabled
  - Kubelet certificate rotation enabled
  - Admission controllers enabled
- **Source:** Talos default Kubernetes configuration
- **Priority:** CRITICAL

### 4.4 Observability

**REQ-OBS-001: Progress Logging**
- **Description:** CLI must display progress for each bootstrap phase
- **Format:** `[PHASE] Description... (Xm Ys elapsed)`
- **Phases:**
  1. Pre-flight validation
  2. Bootstrap cluster creation
  3. CAPI initialization
  4. Management Cluster provisioning
  5. CAPI pivot
  6. ClusterClass deployment
  7. Post-bootstrap components
- **Priority:** IMPORTANT

**REQ-OBS-002: Error Messages**
- **Description:** CLI must display actionable error messages
- **Format:** `Error: <description>. Suggestion: <remediation>`
- **Examples:**
  - "Error: Hetzner API quota exceeded. Suggestion: Increase quota in Hetzner Console or use smaller server type."
  - "Error: SSH key 'admin-key' not found. Suggestion: Upload SSH key to HCloud first."
- **Priority:** CRITICAL

**REQ-OBS-003: Debug Mode**
- **Description:** CLI must support verbose logging via `--debug` flag
- **Behavior:** Output all API calls, kubectl commands, and internal state
- **Priority:** IMPORTANT

### 4.5 Compatibility

**REQ-COMPAT-001: CAPI Version**
- **Description:** CLI must be compatible with CAPI v1.10.x
- **Verification:** Test against CAPI v1.10.0 and v1.10.x
- **Priority:** CRITICAL

**REQ-COMPAT-002: CAPH Version**
- **Description:** CLI must be compatible with CAPH v1.0.7
- **Verification:** Use CAPH v1.0.7 metadata for version pinning
- **Priority:** CRITICAL

**REQ-COMPAT-003: Kubernetes Version**
- **Description:** Management Cluster must run Kubernetes v1.31.6
- **Verification:** Match CAPH tested version
- **Priority:** CRITICAL

---

## 5. Technical Constraints

### 5.1 CAPI Namespace Limitation

**CONSTRAINT-001: Co-location Requirement**
- **Description:** All CAPI objects (Cluster, ClusterClass, Secrets, HetznerCluster) must be in the same namespace
- **Reason:** CAPI does not support cross-namespace ClusterClass references
- **Namespace:** `zero-ops-system`
- **Impact:** Tenant isolation will be achieved via separate namespaces in future phases
- **Source:** CAPI community findings
- **Priority:** CRITICAL

### 5.2 Worker Node Requirement

**CONSTRAINT-002: Mandatory Worker Nodes**
- **Description:** Management Cluster must have at least 2 worker nodes
- **Reason:** Control plane nodes have NoSchedule taint; CAPI controllers require schedulable nodes
- **Impact:** Provider CRD reconciliation will fail without worker nodes (operator cannot schedule provider deployments)
- **Source:** CAPI documentation
- **Priority:** CRITICAL

### 5.3 Resource Count Verification

**CONSTRAINT-003: Custom Verification Logic**
- **Description:** `clusterctl move` does not provide native resource count verification
- **Impact:** CLI must implement custom logic to count CAPI CRDs before/after pivot
- **Implementation:** List resources via kubectl, compare counts
- **Priority:** CRITICAL

---

## 6. Dependencies

### 6.1 External Tools

| Tool | Version | Purpose | Installation |
|------|---------|---------|--------------|
| Docker | 20.10+ | Container runtime for Kind | User pre-installs |
| Kind | 0.20+ | Ephemeral bootstrap cluster | User pre-installs |
| clusterctl | 1.10.x | CAPI lifecycle management | CLI auto-downloads |
| talosctl | 1.12.x | Talos node management | CLI auto-downloads |
| Packer | 1.9+ (optional) | Talos image building | User pre-installs (if using --build-talos-image) |

### 6.2 Hetzner Resources

| Resource | Quantity | Type | Purpose |
|----------|----------|------|---------|
| Control Plane VMs | 3 | CPX31 | Kubernetes control plane |
| Worker VMs | 2 | CPX31 | CAPI controller workloads |
| Load Balancer | 1 | LB11 | Control plane API endpoint |
| Private Network | 1 | /16 CIDR | Inter-node communication |
| Placement Groups | 2 | Spread | HA distribution |
| SSH Key | 1 | RSA/ED25519 | Node access |

### 6.3 Kubernetes Components

| Component | Version | Purpose | Installation Method |
|-----------|---------|---------|---------------------|
| CAPI Core | v1.10.x | Cluster lifecycle | cluster-api-operator (CoreProvider CRD) |
| CAPH | v1.0.7 | Hetzner provider | cluster-api-operator (InfrastructureProvider CRD) |
| hcloud-cloud-controller-manager | v1.x | Node/LB management | Embedded in ClusterClass |
| hcloud-csi-driver | v2.x | Persistent volumes | Embedded in ClusterClass |
| Cilium CNI | v1.14+ | Networking | Embedded in ClusterClass |
| ArgoCD | v2.x | GitOps engine | Helm/manifests |
| capi2argo | latest | CAPI-ArgoCD bridge | Helm/manifests |
| CloudNativePG | v1.x | PostgreSQL operator | Helm/manifests |

---

## 7. Error Scenarios & Handling

### 7.1 Pre-Flight Failures

| Error | Detection | Handling | Exit Code |
|-------|-----------|----------|-----------|
| Docker not running | `docker ps` fails | Display error, suggest starting Docker | 1 |
| Kind not found | `kind version` fails | Display error, suggest installing Kind | 1 |
| Invalid Hetzner token | Dry-run write fails (401) | Display error, suggest checking HCLOUD_TOKEN | 1 |
| Read-only Hetzner token | Dry-run write fails (403) | Display error: "Token lacks Write permissions" | 1 |
| SSH key not found | API query returns 404 | Display error, suggest uploading key | 1 |
| Unsupported architecture | OS/Arch detection fails | Display error, suggest manual clusterctl install | 1 |
| Network CIDR conflict | User validation (manual) | Display warning about potential conflicts | 0 |
| Cluster already exists (no --upgrade) | Cluster resource found | Display info, suggest --upgrade flag | 0 |
| Cluster already exists (with --upgrade) | Cluster resource found | Proceed to reconciliation | 0 |

### 7.2 Provisioning Failures

| Error | Detection | Handling | Exit Code |
|-------|-----------|----------|-----------|
| Hetzner quota exceeded | CAPH reports quota error | Display error, suggest quota increase | 1 |
| Network provisioning timeout | HetznerCluster stuck in Provisioning | Retry 3 times, then fail with logs | 1 |
| VM creation failure | Machine stuck in Provisioning | Display CAPH logs, preserve Kind cluster | 1 |
| Kubeconfig not generated | Secret not found after 15 min | Display error, suggest manual inspection | 1 |

### 7.3 Pivot Failures

| Error | Detection | Handling | Exit Code |
|-------|-----------|----------|-----------|
| Resource count mismatch | Custom verification fails | Preserve Kind cluster, display diff | 1 |
| clusterctl move timeout | Command exceeds 10 min | Preserve Kind cluster, display logs | 1 |
| Management Cluster unreachable | kubectl commands fail | Preserve Kind cluster, suggest network check | 1 |

### 7.4 Teardown Failures

| Error | Detection | Handling | Exit Code |
|-------|-----------|----------|-----------|
| Graceful deletion timeout | CAPI deletion exceeds 15 min | Suggest using --force flag | 1 |
| Management Cluster unreachable | kubectl commands fail | Automatically attempt force deletion | 0 |
| Hetzner API error during force delete | API returns 5xx | Retry with exponential backoff, then fail | 1 |
| Orphaned resources detected | Post-deletion verification finds resources | Display list, suggest manual cleanup | 1 |
| Force delete without --confirm | User safety check | Prompt for confirmation, exit if declined | 1 |

---

## 8. Acceptance Criteria

### 8.1 E2E Test Coverage

All test cases defined in `e2e-tdd.md` must pass:

- **E2E-BOOT-01:** Zero to Hero bootstrap (happy path)
- **E2E-BOOT-02:** Idempotency check (with and without --upgrade)
- **E2E-BOOT-03:** Recovery from partial failure
- **E2E-BOOT-04:** Connectivity and access control
- **E2E-BOOT-05:** Artifact validation (ClusterClasses, CSI, CCM)
- **E2E-BOOT-06:** Hetzner quota handling
- **E2E-BOOT-07:** Clean teardown (graceful and force modes)
- **E2E-BOOT-08:** Token permission validation (read-only vs read-write)
- **E2E-BOOT-09:** Network CIDR customization
- **E2E-BOOT-10:** Kubeconfig merge functionality

### 8.2 Manual Verification

After bootstrap completes, the following commands must succeed:

```bash
# Verify nodes
kubectl --kubeconfig=<cluster-name>.kubeconfig get nodes
# Expected: 5 nodes (3 control plane + 2 workers), all Ready

# Verify CAPI controllers
kubectl --kubeconfig=<cluster-name>.kubeconfig get pods -n capi-system
# Expected: All pods Running

# Verify ClusterClasses
kubectl --kubeconfig=<cluster-name>.kubeconfig get clusterclasses -n zero-ops-system
# Expected: hetzner-mgmt-v1, hetzner-prod-v1, hetzner-dev-v1, hetzner-staging-v1

# Verify ArgoCD
kubectl --kubeconfig=<cluster-name>.kubeconfig get pods -n argocd
# Expected: All pods Running

# Verify capi2argo
kubectl --kubeconfig=<cluster-name>.kubeconfig get pods -n capi2argo-system
# Expected: Operator pod Running

# Verify CloudNativePG
kubectl --kubeconfig=<cluster-name>.kubeconfig get pods -n cnpg-system
# Expected: Operator pod Running

# Verify CSI Driver
kubectl --kubeconfig=<cluster-name>.kubeconfig get pods -n kube-system -l app=hcloud-csi
# Expected: CSI controller and node pods Running

# Verify Cloud Controller Manager
kubectl --kubeconfig=<cluster-name>.kubeconfig get pods -n kube-system -l app=hcloud-cloud-controller-manager
# Expected: CCM pod Running

# Verify StorageClass
kubectl --kubeconfig=<cluster-name>.kubeconfig get storageclass
# Expected: hcloud-volumes StorageClass exists

# Verify Hetzner resources (via Hetzner Console or API)
# Expected: 5 VMs, 1 LB, 1 network, 2 placement groups

# Test PVC provisioning (validates CSI)
kubectl --kubeconfig=<cluster-name>.kubeconfig apply -f - <<EOF
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: test-pvc
  namespace: default
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 10Gi
  storageClassName: hcloud-volumes
EOF
kubectl --kubeconfig=<cluster-name>.kubeconfig get pvc test-pvc
# Expected: STATUS=Bound
```

---

## 9. Resolved Gaps

All 9 GAP analysis questions resolved:

- **GAP-INFRA-01:** CSI/CCM embedded in ClusterClass
- **GAP-LOGIC-01:** Token validation uses dry-run write operation
- **GAP-LOGIC-02:** OS/Arch detection implemented with fallback
- **GAP-LOGIC-03:** `--merge-kubeconfig` flag added
- **GAP-DATA-01:** `--network-cidr` flag added
- **GAP-DATA-02:** `--upgrade` flag added for reconciliation
- **GAP-EDGE-01:** Pivot verification checks existence only
- **GAP-EDGE-02:** ArgoCD password extracted and displayed
- **GAP-EDGE-03:** Teardown command with graceful/force modes defined

---

## 10. References

### 10.1 External Documentation

- [Cluster API Documentation](https://cluster-api.sigs.k8s.io/)
- [CAPH Documentation](https://syself.com/docs/caph/)
- [CAPH Quickstart](https://syself.com/docs/caph/getting-started/quickstart/prerequisites)
- [clusterctl Commands](https://cluster-api.sigs.k8s.io/clusterctl/commands/commands.html)
- [Hetzner Cloud API](https://docs.hetzner.cloud/)
- [capi2argo Operator](https://github.com/dntosas/capi2argo-cluster-operator)

### 10.2 Internal Documents

- `.kiro/syself/prds/prd.md` - Product Requirements Document
- `.kiro/specs/management-cluster/e2e-tdd.md` - E2E Test Specifications
- `.kiro/specs/syself/syself/repos/cluster-api-provider-hetzner/` - CAPH Source Code

---

**Document Status:** APPROVED  
**Next Phase:** Design Specification

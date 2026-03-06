Okay, here is a behavior-driven, end-to-end (E2E) Test Case Specification focusing strictly on **Journey A: Platform Bootstrap**.

These test cases define the "Definition of Done" for the first phase. The engineering team can use this list to deduce the necessary requirements (e.g., "The CLI must support a `--dry-run` flag" or "The bootstrap process must be idempotent").

---

# E2E Test Specifications: Platform Bootstrap (Phase 1)

**Goal:** Validate that a Platform Admin can successfully transform a blank workstation into a fully self-hosted "Mothership" Management Cluster on Hetzner.

## 1. Core Happy Path: The "Zero to Hero" Bootstrap
**ID:** `E2E-BOOT-01`
**Description:** Verify the complete, uninterrupted bootstrap flow from a local machine to a remote, self-hosted Management Cluster.

**Preconditions:**
*   A clean machine with Docker installed.
*   Valid Hetzner Cloud API Token exported as `HCLOUD_TOKEN`.
*   No existing Kind clusters or kubeconfig files.

**Behavioral Steps:**
1.  Admin runs `zero-ops mgmt bootstrap --name=mothership-01 --region=fsn1 --ssh-key=admin-key`.
2.  **Verify:** The system creates a local ephemeral bootstrap cluster (Kind).
3.  **Verify:** The system installs CAPI, CAPH, and Cert-Manager on the local cluster.
4.  **Verify:** The system provisions actual infrastructure on Hetzner (1 Load Balancer, 3 Control Plane VMs, 1 Private Network).
5.  **Verify:** The system performs a CAPI Pivot (moves resources from Local -> Remote).
6.  **Verify:** The local Kind cluster is automatically deleted after success.
7.  **Verify:** A `mothership-01.kubeconfig` file is generated in the current directory.

**Pass Criteria:**
*   `kubectl --kubeconfig=mothership-01.kubeconfig get nodes` shows 3 Ready nodes.
*   `kubectl --kubeconfig=mothership-01.kubeconfig get pods -n caph-system` shows the Hetzner provider running *on the remote cluster*.
*   The Kind cluster no longer exists (`kind get clusters` is empty).

---

## 2. Idempotency & Re-entrancy
**ID:** `E2E-BOOT-02`
**Description:** Verify that running the bootstrap command a second time on an already-running cluster does not destroy or duplicate resources.

**Preconditions:**
*   `E2E-BOOT-01` has completed successfully.
*   The `mothership-01.kubeconfig` exists.

**Behavioral Steps:**
1.  Admin runs `zero-ops mgmt bootstrap --name=mothership-01 --region=fsn1`.
2.  **Verify:** The CLI detects the existing cluster (via API check or local state).
3.  **Verify:** The CLI outputs "Management Cluster 'mothership-01' already exists and is healthy."
4.  **Verify:** No new VMs are created in the Hetzner Console.
5.  **Verify:** The existing kubeconfig remains valid.

**Pass Criteria:**
*   Process exits with Success (0 code).
*   Total resource count in Hetzner matches the count from Test 01.

---

## 3. Recovery from Partial Failure (The "Broken Pipe" Scenario)
**ID:** `E2E-BOOT-03`
**Description:** Verify that if the bootstrap fails midway (e.g., network interrupt), the user can resume or clean up without manual intervention.

**Preconditions:**
*   Clean environment.

**Behavioral Steps:**
1.  Admin runs `zero-ops mgmt bootstrap`.
2.  **Simulation:** Force kill the CLI process *after* the Kind cluster is up but *before* the remote cluster is ready.
3.  Admin runs `zero-ops mgmt bootstrap` again.
4.  **Verify:** The CLI detects the "orphaned" local bootstrap cluster.
5.  **Verify:** The CLI offers to "Resume" or "Reset".
6.  **Action:** Admin selects "Reset" (or runs with `--force`).
7.  **Verify:** The CLI cleans up the stale Kind cluster and starts fresh.

**Pass Criteria:**
*   The second run completes successfully.
*   No "zombie" Kind clusters are left running.

---

## 4. Connectivity & Access Control
**ID:** `E2E-BOOT-04`
**Description:** Verify that the bootstrapped Management Cluster is securely accessible via the generated credentials.

**Preconditions:**
*   Bootstrap complete.

**Behavioral Steps:**
1.  **Action:** Attempt to access the cluster API using the generated `mothership.kubeconfig`.
    *   **Verify:** Access granted (e.g., `kubectl get nodes`).
2.  **Action:** Attempt to access the cluster API *without* the kubeconfig (anonymous/public access).
    *   **Verify:** Access denied (401 Unauthorized).
3.  **Action:** Verify the API Endpoint matches the Hetzner Load Balancer IP.

**Pass Criteria:**
*   Public Internet access to the API Server port (6443) works strictly via TLS + Authentication.

---

## 5. Artifact Validation (The "Batteries Included" Check)
**ID:** `E2E-BOOT-05`
**Description:** Verify that the Mothership comes pre-loaded with the necessary CAPI Templates (ClusterClasses) required for Journey B.

**Preconditions:**
*   Bootstrap complete.

**Behavioral Steps:**
1.  Admin runs `kubectl --kubeconfig=mothership.kubeconfig get clusterclasses -n default`.
2.  **Verify:** The list includes `hetzner-prod-v1`, `hetzner-dev-v1`.
3.  **Action:** Admin inspects `hetzner-prod-v1`.
4.  **Verify:** The definition matches the embedded manifest in the CLI source code (version consistency).

**Pass Criteria:**
*   `ClusterClass` resources exist and are marked `Ready` (if status applies) or valid.

---

## 6. Hetzner Cloud Limits Handling
**ID:** `E2E-BOOT-06`
**Description:** Verify the behavior when the Cloud Provider rejects the request due to quotas/limits (Behavioral check for error handling).

**Preconditions:**
*   A Hetzner Token for a project with **0 quota** (or mocked/simulated failure).

**Behavioral Steps:**
1.  Admin runs `zero-ops mgmt bootstrap`.
2.  **Verify:** The CLI initializes Kind.
3.  **Verify:** The CLI attempts to provision via CAPH.
4.  **Verify:** CAPH reports a "Quota Exceeded" error internally.
5.  **Verify:** The CLI catches this timeout/error state within a reasonable time (e.g., 5 minutes) and reports a human-readable error to the Admin.
6.  **Verify:** The CLI does *not* hang indefinitely.

**Pass Criteria:**
*   CLI exits with non-zero code.
*   Output contains "Hetzner API Error" or "Quota Exceeded".
*   Instruction provided: "Please check Hetzner Cloud limits."

---

## 7. Clean Teardown
**ID:** `E2E-BOOT-07`
**Description:** Verify that the Admin can decommission the Management Cluster, removing all traces from the cloud provider.

**Preconditions:**
*   Bootstrap complete.

**Behavioral Steps:**
1.  Admin runs `zero-ops mgmt teardown --name=mothership-01 --force`.
2.  **Verify:** The CLI connects to the cluster.
3.  **Verify:** The CLI deletes the Cluster resource.
4.  **Verify:** Infrastructure (VMs, LBs, Networks, Volumes) is removed from Hetzner.

**Pass Criteria:**
*   Hetzner Project is empty (except for floating resources if any).
*   Local kubeconfig is deleted or archived.
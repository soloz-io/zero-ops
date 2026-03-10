Here is the Behavior-Driven Development (BDD) End-to-End Test Case Specification strictly focused on the `cnpg-provisioning` scope (Platform Database & `cnpg2monitor` operator).

These test cases define the exact behavior developers must implement and provide the exact commands QA/Automation will use to verify success, completely isolated from Phase 4 (Alloy/VictoriaMetrics).

---

# BDD Test Case Specification: CNPG Provisioning & Observability Operator

**Version:** 1.0  
**Scope:** `zero-ops-platform-db` bootstrap, `cnpg2monitor` operator deployment, PodMonitor patching, K8s Event emission.  
**Test Runner/Framework Target:** Ginkgo/Gomega (Go) or Bash/Kuttl E2E scripts.

---

## Suite 1: Platform Database Bootstrap (`zero-ops-platform-db`)

### Scenario 1.1: High Availability Cluster Provisioning
**Description:** Verifies that the core SaaS database provisions successfully with HA enabled.
*   **Given** a running Management Cluster with the CloudNativePG operator installed
*   **When** the `zero-ops-platform-db` manifest is applied to the `zero-ops-system` namespace
*   **Then** a `Cluster` resource named `zero-ops-platform-db` is created
*   **And** within 3 minutes, its `status.phase` transitions to `ClusterPhaseHealthy`
*   **And** exactly 3 pods matching `postgresql.cnpg.io/cluster=zero-ops-platform-db` reach the `Ready` state.

**Verification Command:**
```bash
kubectl wait --for=jsonpath='{.status.phase}'=ClusterPhaseHealthy cluster/zero-ops-platform-db -n zero-ops-system --timeout=180s
kubectl get pods -n zero-ops-system -l postgresql.cnpg.io/cluster=zero-ops-platform-db | grep -c "Running" # Expected: 3
```

### Scenario 1.2: Post-Init SQL and Database Owner Verification
**Description:** Verifies that the bootstrap process correctly configures the SaaS API schema and required monitoring roles.
*   **Given** the `zero-ops-platform-db` cluster is `Healthy`
*   **When** querying the primary PostgreSQL pod
*   **Then** a database named `zeroops` exists
*   **And** a role named `zeroops-api` exists and owns the database
*   **And** the `postgres_ai` schema exists and `pg_stat_statements` extension is installed.

**Verification Command:**
```bash
# Verify database and owner
kubectl exec -it -n zero-ops-system zero-ops-platform-db-1 -- psql -U postgres -c "\l" | grep "zeroops" | grep "zeroops-api"

# Verify monitoring schemas
kubectl exec -it -n zero-ops-system zero-ops-platform-db-1 -- psql -U postgres -d zeroops -c "\dn" | grep "postgres_ai"
kubectl exec -it -n zero-ops-system zero-ops-platform-db-1 -- psql -U postgres -d zeroops -c "\dx" | grep "pg_stat_statements"
```

### Scenario 1.3: Secret Generation for API Consumption
**Description:** Ensures the connection secret is generated in the correct format for the upcoming `zero-ops-api`.
*   **Given** the `zero-ops-platform-db` cluster is `Healthy`
*   **When** checking the `zero-ops-system` namespace
*   **Then** a Secret named `zero-ops-platform-db-app` exists
*   **And** it contains non-empty `username`, `password`, `dbname`, and `uri` keys.

**Verification Command:**
```bash
kubectl get secret zero-ops-platform-db-app -n zero-ops-system -o jsonpath='{.data.uri}' | base64 -d
```

---

## Suite 2: `cnpg2monitor` Operator Auto-Wiring

### Scenario 2.1: Topology Label Injection (The Golden Path)
**Description:** Verifies that the operator successfully patches CNPG-generated PodMonitors with fleet topology labels.
*   **Given** the `cnpg2monitor` operator is running in the `cnpg2monitor-system` namespace
*   **And** the `zero-ops-system` namespace possesses the label `zero-ops.io/region=fsn1` and `zero-ops.io/cluster_id=mothership`
*   **When** a CNPG Cluster is created with `labels["zero-ops.io/monitored"]="true"` and `enablePodMonitor: true`
*   **Then** a `PodMonitor` named `<cluster-name>` is created by CNPG
*   **And** within 2 seconds, `cnpg2monitor` patches it
*   **And** the `PodMonitor` contains a `relabelings` array where `targetLabel: region` has `replacement: fsn1`.

**Verification Command:**
```bash
kubectl get podmonitor zero-ops-platform-db -n zero-ops-system -o yaml | grep -A 2 "targetLabel: region"
# Expected output:
# - targetLabel: region
#   replacement: fsn1
```

### Scenario 2.2: Ignoring Unmonitored Databases
**Description:** Ensures the operator respects the opt-in monitoring label.
*   **Given** the `cnpg2monitor` operator is running
*   **When** a CNPG Cluster is created *without* the `zero-ops.io/monitored="true"` label (but has `enablePodMonitor: true`)
*   **Then** CNPG generates the `PodMonitor`
*   **And** `cnpg2monitor` ignores it
*   **And** the `PodMonitor` does *not* contain the custom topology `relabelings` array.

**Verification Command:**
```bash
kubectl get podmonitor test-unmonitored-db -n zero-ops-system -o yaml | grep "relabelings"
# Expected output: (Empty)
```

### Scenario 2.3: Dynamic Namespace Label Updates
**Description:** Verifies the operator updates the PodMonitor if the parent namespace's topology metadata changes.
*   **Given** a monitored CNPG Cluster with a successfully patched `PodMonitor`
*   **When** the namespace label `zero-ops.io/region` is updated from `fsn1` to `hel1`
*   **Then** `cnpg2monitor` detects the namespace change
*   **And** patches the `PodMonitor` so the `region` replacement value is updated to `hel1`.

**Verification Command:**
```bash
kubectl label namespace zero-ops-system zero-ops.io/region=hel1 --overwrite
sleep 2
kubectl get podmonitor zero-ops-platform-db -n zero-ops-system -o yaml | grep -A 1 "targetLabel: region" | grep "hel1"
```

---

## Suite 3: AI Correlation Event Emission

### Scenario 3.1: Emitting Scale Events
**Description:** Verifies the operator emits a high-level business event when a database is scaled, intended for the OpenSearch timeline.
*   **Given** a monitored CNPG Cluster running with 3 instances
*   **When** the `Cluster` spec is updated to `instances: 4`
*   **Then** `cnpg2monitor` detects the `.Spec.Instances` difference
*   **And** emits a standard Kubernetes Event with `Reason: CNPGScaled` on the `Cluster` object
*   **And** the event message states "Cluster scaled from 3 to 4 instances".

**Verification Command:**
```bash
kubectl patch cluster zero-ops-platform-db -n zero-ops-system --type='merge' -p '{"spec":{"instances":4}}'
sleep 2
kubectl get events -n zero-ops-system --field-selector reason=CNPGScaled -o jsonpath='{.items[*].message}'
```

### Scenario 3.2: Emitting PostgreSQL Config Change Events
**Description:** Verifies the operator emits an event when Postgres parameters change (crucial for diagnosing DB crashes).
*   **Given** a monitored CNPG Cluster
*   **When** the `.Spec.Postgresql.Parameters["shared_buffers"]` is modified from `128MB` to `256MB`
*   **Then** `cnpg2monitor` detects the `.Spec.Postgresql.Parameters` difference
*   **And** emits a Kubernetes Event with `Reason: CNPGConfigChanged` on the `Cluster` object
*   **And** the event message highlights the changed parameter.

**Verification Command:**
```bash
kubectl patch cluster zero-ops-platform-db -n zero-ops-system --type='merge' -p '{"spec":{"postgresql":{"parameters":{"shared_buffers":"256MB"}}}}'
sleep 2
kubectl get events -n zero-ops-system --field-selector reason=CNPGConfigChanged -o jsonpath='{.items[*].message}'
```

---
**End of Specification**
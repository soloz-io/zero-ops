# Product Requirements Document: Platform Database & Zero-Touch Monitoring Operator (`cnpg2monitor`)

**Version:** 1.0  
**Status:** APPROVED  
**Project Name:** Zero-Ops Platform (Phase 2)  
**Author:** Senior Platform Architecture Team  

---

## 1. Executive Summary

This initiative delivers the foundational highly available Platform Database (`zero-ops-platform-db`) via CloudNativePG (CNPG) and introduces a new Kubernetes operator named `cnpg2monitor`. Targeted at **Platform Engineers** and **SaaS Tenants**, this delivers zero-touch observability: any time a database is provisioned (whether the central platform DB or a tenant's BYOC database), the operator automatically discovers it, extracts its credentials, and registers it with the centralized PostgresAI monitoring stack. Key constraints include maintaining a stateless operator design, strictly using Kubernetes-native event-driven reconciliation (no polling), and leveraging `pgwatch3`'s native YAML auto-reload capabilities for zero-downtime metric configuration.

---

## 2. Problem Statement

### **Current State**
The Management Cluster currently has the CloudNativePG operator installed, but lacks the actual relational database required to run the `zero-ops-api` (SaaS backend). Furthermore, the platform intends to use the `postgres-ai` stack for deep database observability (bloat, unused indexes, wait events). Currently, registering a new database into `postgres-ai` requires manual edits to `instances.yml`, manual secret injection, and pod restarts. 

### **Gap / Motivation**
We cannot proceed with Phase 2 (Tenant API) without a running platform database. Additionally, in a BYOC PaaS model, we cannot ask tenants to manually configure Grafana/PGWatch every time they deploy a database. We must bridge the gap between CNPG cluster provisioning and the PostgresAI observability stack automatically.

### **Constraints & Non-Goals**
**Constraints:**
- **Go-Centric:** The `cnpg2monitor` operator must be written in Go using `controller-runtime`.
- **Event-Driven:** Must use Kubernetes Informers; synchronous polling is strictly forbidden.
- **Declarative Hand-off:** The operator must write a native Kubernetes `Secret` that is mounted into PGWatch. It must not make imperative HTTP API calls to external systems if declarative file-mounting suffices.

**Non-Goals:**
- Modifying the core PostgresAI codebase or Helm charts beyond volume mount adjustments.
- Managing VictoriaMetrics or Grafana dashboards directly via this operator (this operator only handles database *discovery and registration*).

---

## 3. User Personas & User Journeys

### **Personas**
- **Platform Admin:** Responsible for the health of the `zero-ops-platform-db` and the global monitoring stack. Needs immediate visibility into the platform's state without manual wiring.
- **Tenant Developer:** Provisions BYOC Postgres clusters via the Zero-Ops CLI. Expects instant access to "Services on Top" like advanced query analysis and index health without knowing how PGWatch works under the hood.

### **End-to-End User Journeys**

#### **Journey 1: Platform Database Bootstrap (Day 0)**
- **Trigger:** Platform Admin applies the `zero-ops-platform-db` CNPG Cluster manifest to the Management Cluster.
- **Actions:** 
  - K8s schedules 3 CNPG instances.
  - The `cnpg2monitor` operator detects the new `Cluster` CR.
- **System Response:** 
  - The operator extracts the `zero-ops-platform-db-app` Secret.
  - The operator updates the central `pgwatch-dynamic-sources` Secret.
  - The `pgwatch3` pod natively detects the file change and auto-reloads its target list.
- **Error State:** If the CNPG Cluster fails to provision, the operator ignores it until `status.phase == ClusterPhaseHealthy`.

#### **Journey 2: Tenant Database Provisioning (Day 1)**
- **Trigger:** Tenant Developer types `zero-ops service add postgres` via CLI.
- **Actions:** The CLI applies a CNPG `Cluster` CR into the tenant's isolated namespace (`tenant-acme-corp`).
- **System Response:** The `cnpg2monitor` operator (running globally) detects the tenant DB, extracts the read-only credentials, and dynamically injects the tenant DB into the PostgresAI stack. The tenant instantly sees their DB in Grafana.

#### **Journey 3: Database Decommission (Day 2)**
- **Trigger:** Tenant deletes their database cluster.
- **System Response:** The operator receives a K8s Delete event, removes the database from the `pgwatch-dynamic-sources` Secret, and PGWatch stops scraping the deleted endpoints, preventing timeout alerts.

---

## 4. Proposed Architecture

### **4.1 High-Level Flow**

```mermaid
graph TB
    subgraph "Management Cluster"
        API[zero-ops-api]
        
        subgraph "CNPG Operator"
            DB[zero-ops-platform-db<br/>(Cluster CR)]
            SEC[zero-ops-platform-db-app<br/>(Secret)]
        end
        
        subgraph "Zero-Touch Observability"
            OP[cnpg2monitor Operator]
            PGW_SEC[pgwatch-dynamic-sources<br/>(Secret)]
            PGW[pgwatch-prometheus Pod]
            VM[VictoriaMetrics]
            GF[Grafana]
        end
    end
    
    API -->|Reads| SEC
    API -->|Queries| DB
    
    OP -.->|1. Watches| DB
    OP -->|2. Reads| SEC
    OP -->|3. Updates| PGW_SEC
    
    PGW -.->|4. Mounts & Auto-reloads| PGW_SEC
    PGW -->|5. Scrapes via SQL| DB
    PGW -->|6. Pushes Metrics| VM
    GF -->|7. Visualizes| VM
```

### **4.2 Components & Responsibilities**

| Component | Implementation Choice | Responsibility |
| :--- | :--- | :--- |
| **Platform DB** | CloudNativePG (`Cluster` CR) | Highly available 3-node PostgreSQL database storing SaaS tenant metadata and billing. |
| **cnpg2monitor** | Go Operator (`controller-runtime`) | Discovers CNPG Clusters, extracts credentials, and maintains the PGWatch target configuration. |
| **PGWatch Config** | K8s `Secret` (YAML format) | Stores the `instances.yml` equivalent. Mounted into the PGWatch pod to leverage its `fsnotify` auto-reload feature. |
| **PostgresAI** | Helm Chart (`pgwatch3` + VM) | Executes heavy SQL metric queries (bloat, wait events) against discovered databases and stores them. |

### **4.3 Integration & Control Plane**
- **CNPG Integration:** The operator strictly targets `postgresql.cnpg.io/v1/Cluster` resources labeled with `zero-ops.io/monitored: "true"`. It uses the auto-generated `-ro` (read-only) Service endpoint for safe metric scraping.
- **PGWatch Integration:** By updating a K8s Secret mounted into the `pgwatch-prometheus` pod, Kubelet propagates the file change to the container. `pgwatch3` detects the filesystem event and dynamically adds/removes databases without a pod restart.

---

## 5. Technical Specifications

### **5.1 Platform Changes**
1. **Apply `zero-ops-platform-db`:**
   - Namespace: `zero-ops-system`
   - Kind: `Cluster` (CNPG)
   - Replicas: 3, Storage: 20Gi (`hcloud-volumes`), Owner: `zeroops-api`.
2. **Deploy `cnpg2monitor` Operator:**
   - ServiceAccount requiring `get, list, watch` on `postgresql.cnpg.io/clusters` and `secrets` across all namespaces.
   - RBAC requires `get, update, patch` on the `pgwatch-dynamic-sources` Secret in `zero-ops-system`.
3. **Update PostgresAI Helm Chart:**
   - Remove the `sources-generator` sidecar/initContainer.
   - Mount the `pgwatch-dynamic-sources` Secret directly to `/etc/pgwatch/sources.yml`.

### **5.2 Core Resources & APIs**

**Platform DB Manifest (Target):**
```yaml
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: zero-ops-platform-db
  namespace: zero-ops-system
  labels:
    zero-ops.io/monitored: "true"
spec:
  instances: 3
  storage:
    size: 20Gi
    storageClass: hcloud-volumes
  bootstrap:
    initdb:
      database: zeroops
      owner: zeroops-api
  monitoring:
    enablePodMonitor: true
```

**PGWatch Source YAML Structure (Generated by Operator):**
```yaml
- name: zero-ops-system_zero-ops-platform-db
  conn_str: postgresql://zeroops-api:<pass>@zero-ops-platform-db-ro.zero-ops-system.svc.cluster.local:5432/zeroops?sslmode=require
  preset_metrics: full
  is_enabled: true
  custom_tags:
    env: production
    cluster: zero-ops-platform
    node_name: platform-db
```

### **5.3 Defaulting & Automation Logic**
- **Database Discovery:** The operator builds the target PGWatch `name` using the format `{namespace}_{cluster-name}`.
- **Connection String:** Extracted from the `<cluster-name>-app` Secret. The operator modifies the host to use the `-ro` (read-only) service (`<cluster-name>-ro.<namespace>.svc.cluster.local`) to ensure metric queries do not impact the primary writer node's performance.

### **5.4 Operational Semantics (Lifecycle & Frequency) [REQUIRED]**
- **Kubernetes Events (`cnpg2monitor`):**
  - *Startup:* Operator performs a full list of all `Cluster` resources and initializes the PGWatch Secret.
  - *Runtime:* Responds to Add/Update/Delete events from the `SharedInformerFactory` via a work queue.
  - *Frequency:* Triggered instantly on CNPG Cluster status changes. **Never** polls the K8s API on a timer.
- **Secret Mounting (PGWatch):**
  - *Startup:* Reads `/etc/pgwatch/sources.yml`.
  - *Runtime:* `pgwatch3` listens to `fsnotify` events. When Kubelet updates the mounted Secret (usually within 60-120 seconds of the K8s API update), PGWatch reloads its connections.
- **Credentials (API):**
  - *Startup:* `zero-ops-api` reads the `zero-ops-platform-db-app` Secret via environment variables at boot. Does not re-read on the request path.

### **5.5 Idiomatic Behavior & Anti-Patterns [REQUIRED]**
- **Idiomatic:**
  - Maintaining an in-memory cache in the operator of all monitored clusters, regenerating the entire YAML list, and applying a single patch to the `pgwatch-dynamic-sources` Secret.
  - Relying on native Kubelet file propagation and PGWatch's built-in file watcher.
- **Anti-Patterns (Implementers MUST NOT do the following):**
  - **MUST NOT** synchronously poll K8s for Cluster/Secret changes. Use Informers.
  - **MUST NOT** restart the PGWatch pods imperatively via the operator.
  - **MUST NOT** scrape the primary node (`-rw`) for heavy monitoring queries; always target the read-only replica service (`-ro`).

### **5.6 Security, Compliance & Reliability**
- **Isolation:** The operator runs in `zero-ops-system`. It holds cross-namespace read privileges for Secrets, but the reconciliation loop MUST validate that the Secret belongs to a CNPG Cluster before reading it (checking OwnerReferences or specific CNPG labels).
- **TLS:** The connection string injected into PGWatch strictly enforces `sslmode=require`.
- **Reliability:** By decoupling the operator from the monitoring stack (communicating purely via a K8s Secret), if the operator crashes, PGWatch continues monitoring existing databases without interruption.

---

## 6. User Journey Deep Dives (Scenario-Based)

### **Scenario 1: Auto-wiring a new Tenant Database**
**Actors:** Platform, K8s API, `cnpg2monitor`, PGWatch.
**Preconditions:** Operator is running. PostgresAI stack is running.

**Step-by-Step Flow:**
1. **User Action:** Tenant provisions `Cluster` named `tenant-db` in namespace `tenant-acme`.
2. **Platform Action:** CNPG provisions the database and creates Secret `tenant-db-app`.
3. **Operator Action:** `cnpg2monitor` receives an `Add` event for `Cluster`. It requeues until `status.phase` is `ClusterPhaseHealthy`.
4. **Operator Action:** Once healthy, it fetches the `tenant-db-app` Secret.
5. **Operator Action:** It formats a PGWatch entry: `postgresql://user:pass@tenant-db-ro.tenant-acme.svc:5432/app?sslmode=require`.
6. **Operator Action:** It appends this to its in-memory list and patches the `pgwatch-dynamic-sources` Secret in `zero-ops-system`.
7. **Platform Action:** Kubelet updates the mounted file inside the `pgwatch-prometheus` pod.
8. **Platform Action:** PGWatch logs `Reloading YAML sources` and begins scraping `tenant-db` for bloat and wait events.

**Variations / Edge Cases:**
- *Secret missing:* If the operator detects a healthy cluster but the Secret is missing, it returns an error to the controller-runtime queue, which triggers exponential backoff retries.

---

## 7. Success Criteria & Acceptance Tests

**Platform Database Verification:**
- [ ] `zero-ops-platform-db` `Cluster` is provisioned and reports 3 Ready instances.
- [ ] `zero-ops-api` successfully connects using the `-app` Secret and executes its schema migrations.

**Operator & Observability Verification:**
- [ ] The `pgwatch-dynamic-sources` Secret contains a valid YAML array with the `zero-ops-platform-db` connection string targeting the `-ro` service.
- [ ] Deploying a dummy CNPG cluster in a different namespace automatically results in the Secret being updated within 1 second.
- [ ] Deleting the dummy cluster automatically removes its entry from the Secret.
-[ ] Grafana (PostgresAI Dashboard #1) displays the new database metrics without requiring any pod restarts.

**Verification Commands:**
```bash
# Verify Platform DB
kubectl get cluster zero-ops-platform-db -n zero-ops-system

# Verify Operator generated the sources
kubectl get secret pgwatch-dynamic-sources -n zero-ops-system -o jsonpath='{.data.sources\.yml}' | base64 -d

# Verify PGWatch logs for auto-reload
kubectl logs deployment/postgres-ai-monitoring-pgwatch-prometheus -n zero-ops-system | grep "Reloading YAML"
```
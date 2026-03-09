You are absolutely right to challenge me on this. My previous assessment was too narrow. In a true enterprise PaaS, the **Platform Database (`zero-ops-platform-db`) is the most critical stateful component** in the entire system. It holds tenant metadata, billing, and routing data. If it goes down or suffers from extreme bloat/transaction wraparound, the entire SaaS goes down. 

Furthermore, you are spot on about the tenant clusters: since we are enforcing a standardized Service Catalog, tenant databases will *also* be deployed using CloudNativePG. Therefore, solving deep observability for CNPG once solves it for the entire platform.

Here is an analysis of how we can integrate deep observability (like the `postgres-ai` stack) with CloudNativePG, the effort required, and architectural possibilities for the Zero-Ops platform.

---

### 1. How CNPG Metrics Work (The Baseline)
CloudNativePG does **not** require external sidecars like `postgres-exporter` (which the `postgres-ai` Helm chart uses in its `self-postgres-exporter` deployment). 
* The CNPG instance pod itself natively exposes a Prometheus metrics endpoint (default port `9187`).
* CNPG allows injecting **Custom Queries** via a `ConfigMap` referenced in the `Cluster` CR (`spec.monitoring.customQueriesConfigMap`). 

### 2. Adapting `postgres-ai` for CNPG (Effort Level: Low to Medium)
There are two ways to adapt the provided `postgres-ai` Helm chart to work seamlessly with CNPG. 

**Path A: The PGWatch "Pull" Method (Lowest Effort)**
The `postgres-ai` stack relies heavily on `pgwatch3`. PGWatch doesn't care if the database is on RDS, bare metal, or CNPG. It just needs a connection string.
* **How:** We deploy the `postgres-ai` Helm chart in the management cluster. In its `values.yaml`, instead of pointing it to an external DB, we point `monitoredDatabases` to the CNPG Read-Only Service (`zero-ops-platform-db-ro.zero-ops-system.svc.cluster.local`) and pass the CNPG-generated auth secret.
* **Effort:** Very Low. We just map the CNPG secret to the Helm values.

**Path B: The Native CNPG "Push" Method (Medium Effort, High Elegance)**
Instead of running `pgwatch3` to query the database, we take the heavy SQL queries from `postgres-ai/config/metrics-postgres.yml` (bloat analysis, wait events, unused indexes) and convert them into a **CNPG Custom Queries ConfigMap**.
* **How:** Prometheus/VictoriaMetrics scrapes the CNPG pods directly. The `postgres-ai` Grafana dashboards simply point to VictoriaMetrics. We completely drop the `pgwatch3` pods from the Helm chart, saving compute resources.
* **Effort:** Medium. We would need to write a script/tool to translate PGWatch YAML metrics into CNPG YAML metrics, but the SQL logic remains exactly the same.

### 3. Building an Operator (`cnpg2monitor`)
Your idea of building an operator like `capi2argo` for CNPG metrics is brilliant. This enables **Zero-Touch Observability**. 

In our BYOC model, a tenant will go to the CLI and type `zero-ops service add postgres`. We want them to instantly get AI-driven insights without configuring Grafana or PGWatch manually.

**How a `cnpg2monitor` operator would work:**
1. **Watch:** The Go operator watches for `postgresql.cnpg.io/v1/Cluster` resources across all tenant namespaces (and the management namespace).
2. **Extract:** When a CNPG cluster becomes `Ready`, the operator reads the generated `<cluster-name>-app` Secret to get the connection credentials.
3. **Register:** The operator automatically injects these credentials into the Observability Stack. 
    * If we use PGWatch, it calls the PGWatch API to register the new database.
    * If we use VictoriaMetrics `vmagent`, it automatically generates a `ServiceMonitor` or `PodMonitor` CRD targeting the new CNPG pods.
4. **Cleanup:** When the tenant deletes the DB, the operator unregisters it, preventing scrape timeouts.

### 4. Platform Possibilities & Suggestions

Based on our architecture, here is the suggested approach for the Zero-Ops Platform:

**For the immediate phase (Phase 2 - Platform DB):**
* Provision the CNPG `Cluster` for `zero-ops-platform-db` with basic native Prometheus metrics enabled (standard CNPG configuration). 
* We do *not* need to deploy the full `postgres-ai` stack right now to unblock the Go API development. We just need the database running.

**For the Observability Phase (Phase 3/4):**
* Deploy the `postgres-ai` VictoriaMetrics/Grafana/Flask stack in the Mothership cluster as the **Global Observability Control Plane**.
* Build the `cnpg2monitor` operator. 
* Use **Path B** (Native CNPG Push). When a tenant creates a CNPG cluster, the `cnpg2monitor` operator automatically applies a `ConfigMap` containing the `postgres-ai` advanced SQL queries (bloat, unused indexes) to their namespace, attaches it to their CNPG `Cluster`, and creates a `PodMonitor` so the Global VictoriaMetrics instantly starts scraping it.

### Next Step
This confirms that the AI-driven monitoring stack is highly relevant for both the Management Plane and Tenant Planes. 

To proceed with our immediate roadmap, shall I go ahead and write the **CNPG Provisioning Spec** (`.kiro/specs/tenant-onboarding/cnpg-provisioning/spec.md`) so we can get the `zero-ops-platform-db` running and start building the Go API?
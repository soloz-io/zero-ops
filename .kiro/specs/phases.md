Here is the outcome-based, progressive roadmap to bridge the gap between your current Hub infrastructure and the fully automated Spoke Pool architecture.

---

### Immediate Next Step: Where to start today?
You should start with **Phase 1: The SaaS Template (Crossplane)**. Do not write any more Go code until you have declaratively defined what a "Starter Tenant" actually looks like in Kubernetes YAML.

---

### Progressive Phased Roadmap

#### Phase 1: The Spoke Pool Provisioner (Cell-Based Scaling)
**Goal:** Automate the creation of the Spoke Pool clusters themselves.
*   **Action:** Create a Crossplane Composition for the Spoke Pool infrastructure.
*   **Deliverable 1:** A `SpokePool` XR and Composition that uses CAPI/CAPH to spin up a Hetzner cluster.
*   **Deliverable 2:** A `ClusterResourceSet` (or ArgoCD App-of-Apps) that injects the "Edge Catalog" (`edge-catalog/` directory) into the new Spoke Pool:
    *   `argocd-agent` (to pull tenant workloads)
    *   `nats-leaf-node` (to bridge billing events back to the Hub)
    *   `spire-agent` (for mTLS identity)
    *   The Shared CNPG Cluster (the database engine for the 100 starter tenants).
*   **Outcome:** You can spin up "Spoke Pool 02" with a single declarative file, and it automatically registers itself with the Hub's ArgoCD, ready to accept tenants.

#### Phase 2: The HeadLamp Integration (Spoke Controller)
**Goal:** Close the feedback loop. The Hub needs to know when a tenant is actually ready without querying the K8s API directly.
Dynamic Kubeconfig Generation (Production, Scalable)
*   **Deliverable 3:** Hub-Spoke Fleet management UI.
Create a sidecar controller that:

Watches AINativeSaaS XRs in Hub
When Spoke Silo provisioned → generates kubeconfig with limited RBAC
Writes kubeconfig to shared volume
Headlamp auto-reloads (uses fsnotify watcher)

#### Phase 3: The Blueprint (`AINativeSaaS` XRD & Composition A)
**Goal:** Define the exact Kubernetes footprint of a Starter Tenant (the "200 planes" model) so Crossplane knows how to stamp them out.
*   **Action:** Create the `xrds/` directory in your monorepo.
*   **Deliverable 1:** `xrds/definitions/ainativesaas-v1.yaml` (The API schema defining `tier`, `tenantId`, `quotas`).
*   **Deliverable 3:** `xrds/compositions/ainativesaas-starter.yaml`. This composition must take the XR and generate:
    *   Namespace: `tenant-{id}-cp`
    *   Namespace: `tenant-{id}-app`
    *   `NetworkPolicy` for strict dual-plane isolation (as discussed).
    *   `ResourceQuota` per namespace.
    *   Logical DBs and Roles inside the Spoke Pool's shared CNPG cluster.
*   **Outcome:** You can `kubectl apply -f dummy-tenant.yaml` on the Hub, and Crossplane successfully provisions the isolated dual-namespaces and databases.

#### Phase 4: The Fleet Registry (ArgoCD ApplicationSet)
**Goal:** Replace manual `kubectl apply` with GitOps orchestration. 
*   **Action:** Implement the `IProvisioner` interface in `internal/opensbt/providers/gitops/`.
*   **Deliverable 1:** Set up the `fleet-registry` Git repository structure (`tenants/starter/...`).
*   **Deliverable 4:** Deploy the ArgoCD `ApplicationSet` to the Hub. Configure it with a Matrix Generator that reads the Git repo and targets clusters labeled `spoke-type: pool`.
*   **Outcome:** Committing a folder like `tenants/starter/tenant-123/values.yaml` to Git automatically triggers ArgoCD to deploy the `AINativeSaaS` XR, which Crossplane then fulfills.

#### Phase 5: The MCP-First Interface
**Goal:** Connect the AI Agents to the GitOps engine.
*   **Action:** Wire the `opensbt` toolkit into your `mcp-server`.
*   **Deliverable 1:** Implement the `tenant_create` and `environment_create` tools in `cmd/mcp-server/tools/tenant/`.
*   **Deliverable 2:** The MCP tool uses the `IProvisioner` (GitOps provider) to generate the tenant YAML and commit it to the `fleet-registry`.
*   **Outcome:** You can open Cursor or Goose, type *"Onboard Acme Corp on the Starter Tier"*, and the agent will call the MCP tool, commit to Git, trigger ArgoCD, provision the namespaces via Crossplane, and report success when the Spoke Controller updates the DB.

---

### Summary of your next coding session:
Create `xrds/definitions/ainativesaas-v1.yaml` and `xrds/compositions/ainativesaas-starter.yaml`. 

Getting Crossplane to successfully stamp out the `tenant-cp` and `tenant-app` namespaces with proper NetworkPolicies and CNPG logical databases is the **hardest and most critical infrastructure task remaining**. Once that is proven, the GitOps and MCP layers are just routing strings and YAMLs.
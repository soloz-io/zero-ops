Based on a thorough review of the provided codebase, the `agents-core` team has implemented the core service logic, database schemas, and API clients. However, they have missed implementing the critical execution boundaries, and they have incorrectly assumed that certain components (like the OSS AgentRegistry) will magically handle Kubernetes deployments.

Furthermore, testing this code requires the `open-sbt` platform team to deploy specific infrastructure, route traffic, and inject tenant context.

Here is the **Integration & Deployment Requirement Specification** designed to be handed over to the `open-sbt` maintainer team. It outlines exactly what must be provisioned by `open-sbt`, and crucially, highlights the missing components that currently block end-to-end testing.

---

# Integration & Deployment Requirements: `agents-core`

**To:** `open-sbt` Platform Maintainers  
**From:** `agents-core` Development Team  
**Purpose:** Infrastructure prerequisites, routing rules, and missing component alerts required to deploy and test the `agents-core` MCP functionality.

---

## Part 1: Infrastructure & Deployment Prerequisites (open-sbt Responsibility)

To test the `agents-core` service, the `open-sbt` platform must provision the following supporting infrastructure in the Hub cluster:

### 1.1 AgentRegistry OSS Deployment
The `agents-core` services act as an orchestration layer on top of the open-source AgentRegistry.
*   **Action:** Deploy the AgentRegistry OSS container to the `platform-agentregistry` namespace.
*   **Network:** Expose it internally as a ClusterIP service at `http://agentregistry.platform-agentregistry.svc.cluster.local:8080`.
*   **Database:** Provide it with a connection string to the `control_plane` DB, specifically pointing to the `agentregistry` schema.

### 1.2 Hub PostgREST Deployment
The `Status Service` reads deployment infrastructure statuses via PostgREST to adhere to the Status Controller pattern.
*   **Action:** Ensure PostgREST is deployed against the Hub Centralised DB.
*   **Network:** Expose it internally at `https://postgrest.hub.zero-ops.io`.
*   **Auth:** Configure PostgREST to accept Ory Hydra JWTs (`postgrest_auth` role) so `agents-core` can query the `agent_infra_status` table.

### 1.3 Database Schema Migrations
The `agents-core` team has provided SQL migrations that must be applied to the platform databases:
*   **Control Plane DB:** Apply `migrations/001_agentregistry_schema.sql` and `migrations/002_agents_schema.sql`. (Grants `mcp_server` access to these schemas).
*   **Hub Centralised DB:** Apply `migrations/003_hub_agent_infra_status.sql` (Creates the status tables and the `pg_notify` triggers).

### 1.4 Auth-Proxy & Gateway Routing
The provided `auth-proxy` validates JWTs and injects `X-Auth-*` headers. 
*   **Action:** Configure the ingress/gateway to route all requests matching `/mcp/*` through the `auth-proxy` and then to the (yet to be deployed) `mcp-server` pod.

---

## Part 2: Tenant Identity & Context Requirements (open-sbt Responsibility)

The `agents-core` logic relies heavily on the JWT token for tenant isolation and Spoke routing. Currently, the `open-sbt` tenant onboarding flow (`api/handlers/tenant.go`) does **not** assign a Spoke Cluster.

### 2.1 Spoke Cluster Assignment
*   **Requirement:** During the `POST /api/v1/tenants` (or `tenant-registrations`) onboarding flow, `open-sbt` MUST assign a `spoke_cluster_id` (e.g., `spoke-pool-01` or `spoke-silo-acme`) to the tenant.
*   **Requirement:** `open-sbt` must ensure that Ory Hydra includes the `spoke_cluster_id` claim inside the user's JWT. 

### 2.2 Header Injection
The API Gateway/Auth-Proxy must extract these claims and inject them as headers to the downstream `agents-core` services:
*   `X-Auth-Tenant-Id`
*   `X-Auth-Tenant-Tier`
*   `X-Auth-Spoke-Cluster-Id`

---

## 🚨 Part 3: MISSING COMPONENTS (Blockers for E2E Testing)

**ATTENTION:** Upon codebase review, the `agents-core` team has only committed the internal `service`, `client`, and `database` logic. **The actual execution binaries and deployment adapters are completely missing from the codebase.** 

You cannot successfully test `agents-core` until the following pieces are implemented by the respective developers:

### Blocker 1: Missing MCP Server Entrypoint (`cmd/mcp-server/main.go`)
*   **Issue:** There is no executable to run. The codebase contains the business logic (`AgentService`, `DeploymentService`), but the actual MCP JSON-RPC Server that exposes the tools (`create_agent`, `deploy_agent`) to Cursor/Goose does not exist in the code.
*   **Fix Required:** The `agents-core` team must provide the `cmd/mcp-server/main.go` binary that registers the tools and handles the MCP protocol.

### Blocker 2: Missing Deployment Adapter (The "Magic" Bug)
*   **Issue:** In `agent-core/service/deployment_service.go` (Line 46), the code says:
    `// 4. Create deployment via AgentRegistry (handles CRD generation and Kubernetes apply)`
*   **Why this is broken:** As established in the architectural design, the OSS AgentRegistry is a **CRUD-only** database wrapper. It *does not* generate CRDs or talk to Kubernetes. 
*   **Fix Required:** The `agents-core` team must implement the `Deployment Adapter` (e.g., `internal/agents-core/adapter/k8s_spoke_adapter.go`) that takes the Agent definition, renders the `Kagent` CRD YAML, and uses the Kubernetes API to apply it directly to the assigned Spoke Cluster.

### Blocker 3: Missing Spoke Controller
*   **Issue:** The architecture relies on a Spoke Controller deployed to the Spoke clusters to watch the `Kagent` pods and write their status back to the Hub PostgREST DB.
*   **Fix Required:** The `operators/spoke-controller/*` codebase is entirely missing. Without this, agents will be deployed, but their status will never report back as "Ready".

### Blocker 4: Missing NATS Status Subscriber
*   **Issue:** Migration `003` emits a `pg_notify` when the Spoke Controller updates the DB. However, the daemon that listens to this notification and updates the `agentregistry.deployments` table (`cmd/nats-subscriber/main.go`) is missing.
*   **Fix Required:** The `agents-core` team must provide the worker daemon that closes the status loop between the Hub DB and the Control Plane DB.

---

### Conclusion & Next Steps
**For the `open-sbt` team:** Please provision the databases, apply the migrations, deploy the OSS AgentRegistry, and update the Auth/Tenant flow to include `spoke_cluster_id`.

**For the `agents-core` team:** Testing cannot proceed until you push the actual `cmd/mcp-server` binary, implement the missing K8s Deployment Adapter (AgentRegistry does *not* do this for you), and provide the Spoke Controller/NATS subscriber workers.
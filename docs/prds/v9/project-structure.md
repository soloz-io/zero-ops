
**File:** `docs/architecture/project-structure.md`
**Status:** APPROVED (Aligned with PRD v9.0)

### 1. The Verdict: Go-Centric Monorepo

For a Zero-Ops platform incorporating a Hub-Spoke architecture, distributed identity, and an AI Agent system, a **Structured Monorepo is mandatory**. 

**Why?** The "Killer Feature" of this architecture is **Correlation and Consistency**.
Your Hub API routing logic, your Agent's MCP tool definitions, your Spoke Controller status sync, and your Infrastructure Templates (Crossplane Compositions) must be perfectly version-locked. If you split these into a Polyrepo, an Agent might fail because the `crossplane-mcp` server expects a `v1` XRD schema, but the Composition repository was upgraded to `v2`.

In a Monorepo, a single Pull Request can update a Crossplane XRD, the Go API validation logic, the Spoke Controller status mapping, and the Agent's prompt instructions simultaneously.

### 2. The Monorepo Structure (v9.0)

This structure enforces strict separation between the Hub (control plane), Spoke Pool (shared tenants), Spoke Silo (dedicated tenants), and AI Agents.

```text
zero-ops/
├── cmd/                          # Binary entry points
│   ├── api/                      # zero-ops-api (Hub)
│   ├── agentgateway/             # AgentGateway (Hub + Silo)
│   ├── hub-event-router/         # NATS JetStream consumer (Hub)
│   ├── auth-proxy/               # Ory stack interface (Hub + Silo)
│   └── mgmt/                     # CLI: bootstrap, eject, migrate
│
├── pkg/
│   ├── api/                      # REST API handlers
│   ├── fleetstate/               # Fleet routing metadata
│   ├── policies/                 # Policy evaluation engine
│   ├── agents/
│   │   ├── collaborator/
│   │   ├── workers/
│   │   │   ├── metrics_agent.go
│   │   │   ├── lifecycle_agent.go
│   │   │   ├── gitops_agent.go
│   │   │   ├── diagnostics_agent.go
│   │   │   ├── upgrade_agent.go
│   │   │   ├── report_agent.go
│   │   │   └── provisioning_agent.go  # NEW v9.0 — AINativeSaaS XR claims
│   │   └── mcp/
│   │       ├── fleet_state_mcp.go
│   │       ├── victoriametrics_mcp.go
│   │       ├── opensearch_mcp.go
│   │       ├── capi_mcp.go
│   │       ├── argocd_mcp.go
│   │       ├── k8sgpt_mcp.go
│   │       ├── audit_mcp.go
│   │       └── crossplane_mcp.go      # NEW v9.0 — XR claim operations
│   ├── agentgateway/              # NEW v9.0 — A2A + MCP routing
│   ├── identity/                  # NEW v9.0 — Ory Kratos/Hydra/Keto clients
│   ├── console/                   # NEW v9.0 — Platform Console backend
│   ├── hubeventrouter/            # NEW v9.0 — NATS consumer routing
│   ├── rag/
│   ├── execution/
│   │   ├── policy_gate.go
│   │   ├── workflows.go
│   │   └── approval.go
│   └── db/
│
├── operators/
│   ├── cnpg2monitor/              # Fleet-wide CNPG monitoring
│   │   ├── cmd/main.go
│   │   └── internal/controller/
│   └── spoke-controller/          # NEW v9.0 — Crossplane condition watcher
│       ├── cmd/main.go
│       └── internal/
│           ├── controller/
│           │   └── ainativesaas_controller.go
│           └── store/
│               ├── interface.go   # IControlPlaneStore interface
│               ├── hub_store.go   # HubStore: direct PostgREST
│               └── local_store.go # LocalStore: local DB + NATS sync
│
├── xrds/                          # NEW v9.0 — Crossplane SaaS Templates
│   ├── definitions/
│   │   └── ainativesaas-v1.yaml
│   └── compositions/
│       ├── ainativesaas-starter-hetzner.yaml
│       └── ainativesaas-enterprise-hetzner.yaml
```

### 3. Execution Path: Where does code actually run?

To understand this structure, you must understand the v9.0 hub-spoke deployment topology:

1. **Developer's Laptop:** Runs MCP-compatible clients (Goose, Cursor). Makes authenticated MCP calls to AgentGateway.

2. **Hub Cluster (Control Plane):** Runs:
   - `cmd/api/` — zero-ops-api (tenant lifecycle, commits to Git)
   - `cmd/agentgateway/` — AgentGateway (MCP routing, JWT validation via auth-proxy)
   - `cmd/hub-event-router/` — NATS JetStream consumer (billing/lifecycle/notifications)
   - `cmd/auth-proxy/` — Ory stack interface (Kratos/Hydra/Keto)
   - Crossplane (watches AINativeSaaS XRs, reconciles Compositions)
   - VictoriaMetrics, Loki, Grafana (observability)
   - Hub Centralised DB (PostgreSQL) — fleet state, resource status
   - Control Plane Shared DB (PostgreSQL) — tenant config, sessions

3. **Spoke Pool Cluster (Shared Tenants):** Runs:
   - ArgoCD Agent (pulls from Hub OCI registry)
   - Spoke Controller (watches Crossplane claims → writes status to Hub via PostgREST)
   - NATS Leaf Node (billing events → Hub)
   - KSM + Grafana Alloy (metrics scrape → remote_write → VictoriaMetrics on Hub)
   - Tenant workloads (namespaced isolation)
   - Uses HubStore (direct connection to Control Plane Shared DB)

4. **Spoke Silo Cluster (Dedicated Tenants):** Runs:
   - Local Ory stack (Kratos + Hydra)
   - Local AgentGateway + auth-proxy
   - ArgoCD (full instance or Agent, based on gitops.mode)
   - Spoke Controller (watches Crossplane claims → writes status to Hub via PostgREST)
   - NATS Leaf Node (state sync + billing → Hub)
   - KSM + Grafana Alloy (metrics scrape → remote_write → VictoriaMetrics on Hub)
   - Tenant Control Plane DB (local PostgreSQL)
   - Tenant workloads (physical isolation)
   - Uses LocalStore (writes locally, syncs via NATS)

5. **Tenant Clusters (Hetzner VMs):** Runs Ubuntu + kubeadm (not Talos). At boot, CAPI injects edge-catalog via ClusterResourceSet. ArgoCD pulls catalog from OCI registry.

### 4. Hub-Spoke Data Flow Patterns

**Status Sync (Synchronous):**
Spoke Controller → Hub-side PostgREST → Hub Centralised DB
- Direct HTTPS POST with per-spoke JWT
- Controller-runtime native retry with exponential backoff

**Control Plane State (Dual Path):**
- Spoke Pool: Direct PgSQL connection to Control Plane Shared DB (HubStore)
- Spoke Silo: Local Tenant Control Plane DB → NATS Leaf Node → Hub Event Router → Control Plane Shared DB (LocalStore)

**Billing/Lifecycle Events (Asynchronous):**
Spoke → NATS Leaf Node → Hub JetStream → Hub Event Router → Hub Centralised DB

**Observability (Push):**
Grafana Alloy (all spokes) → VictoriaMetrics (Hub) via remote_write  [metrics]
Grafana Alloy (all spokes) → Loki (Hub) via remote_write             [logs]
KSM (all spokes) → scraped by Alloy → VictoriaMetrics               [K8s resource state metrics]

### 5. When should you Split? (The Exit Strategy)

You should start with a Monorepo. You should **only** split (Polyrepo) if:

1. **Open Source Strategy:** If you decide to open-source the CLI and catalog but keep zero-ops-api and agents closed-source.
2. **Massive Scale:** You grow past 100+ platform engineers, where the AI Agent team and the Hub API team begin blocking each other's CI/CD pipelines.

**Recommendation:** Stick to the Monorepo. It is the only way to build a complex, multi-agent, hub-spoke platform rapidly without drowning in versioning drift.

### 6. MCP Tool Server Process Architecture

**Important Note:** MCP tool servers in `pkg/agents/mcp/` are written as Go packages but run as separate processes per the PRD architecture. Each MCP server requires:

- **Separate Process:** Independent process with its own RBAC ServiceAccount
- **Network Listener:** Dedicated network endpoint for MCP protocol
- **Entrypoint Strategy:** Either separate `cmd/` entrypoints or goroutines within agent binary
- **No Direct Auth:** AgentGateway handles all JWT validation via auth-proxy; tool servers receive pre-validated requests with injected `X-Auth-*` headers

This distinction affects agent binary structure in Phase 6 implementation.

### 7. Critical v9.0 Architectural Constraints

**ClickHouse Deferral:** ClickHouse is deferred until PostgreSQL billing queries become a bottleneck (typically 10M+ rows). Until then, billing records are stored in Hub Centralised DB.

**Keto Deferral:** Keto permission checks (step 3 of JWT validation flow) are deferred to Demo 2. Demo 1 implements JWT signature validation and identity trait fetching only.

**IControlPlaneStore Pattern:** The same interface, two implementations injected at spoke bootstrap:
- HubStore (Spoke Pool): Direct synchronous writes to Hub
- LocalStore (Spoke Silo): Local writes with eventual consistency via NATS

**PostgREST Dual Deployment:**
- Hub-side PostgREST: Exposes Hub Centralised DB (resource status writes from Spoke Controllers)
- Spoke-side PostgREST: Exposes Control Plane Shared DB (Pool) or local Tenant Control Plane DB (Silo)

These are never merged and serve different databases with different access patterns.
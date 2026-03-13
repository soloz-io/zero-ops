
**File:** `docs/architecture/project-structure.md`
**Status:** APPROVED (Aligned with PRD v7.0)

### 1. The Verdict: Go-Centric Monorepo

For a Zero-Ops platform incorporating an API-centered control plane, Fleet Sharding, and an AI Agent system, a **Structured Monorepo is mandatory**. 

**Why?** The "Killer Feature" of this architecture is **Correlation and Consistency**.
Your SaaS API routing logic, your Agent's MCP tool definitions, and your Infrastructure Templates (ClusterClasses) must be perfectly version-locked. If you split these into a Polyrepo, an Agent might hallucinate or fail because the `capi-mcp` server expects a `v1beta1` payload, but the `ClusterClass` repository was upgraded to `v1beta2`.

In a Monorepo, a single Pull Request can update a Kubernetes CRD, the Go API validation logic, and the Agent's prompt instructions simultaneously.

### 2. The Monorepo Structure (v7.0)

This structure enforces a strict separation of concerns between the Control Plane, the Edge, and the AI Agents.

```text
zero-ops/
├── cmd/                             # Entrypoints (The "Apps")
│   ├── zero-ops/                    # Thin CLI (REST API Client only)
│   ├── zero-ops-api/                # SaaS Control Plane (Tenant/Shard Router)
│   ├── zero-ops-worker/             # Async billing & telemetry polling
│   └── zero-ops-agent/              # Multi-Agent Collaboration System
│
├── pkg/                             # Shared Go Code
│   ├── api/                         # REST API handlers & models
│   ├── agents/                      # AI Agent logic
│   │   ├── collaborator/            # Orchestrator agent
│   │   ├── workers/                 # Specialist agents (Diagnostics, Upgrade)
│   │   └── mcp/                     # MCP Tool Servers (capi-mcp, opensearch-mcp)
│   ├── sharding/                    # Fleet Shard dispatch + health checker
│   ├── db/                          # PostgreSQL sqlc generated code
│   ├── execution/                   # Safe Execution Layer (Policy Gates)
│   ├── fleetstate/                  # Fleet state management & query.go
│   ├── policies/                    # Policy evaluation engine
│   ├── llmgateway/                  # LLM gateway and routing
│   └── rag/                         # RAG system for AI agents
│
├── manifests/                       # Fleet Shard Infrastructure (Pushed by API)
│   ├── shards/                      # Shard bootstrap manifests (CAPI, CAPH)
│   └── classes/                     # CAPI Topologies (Talos + Hetzner)
│
├── argo-workflows/                  # Safe Execution Layer Workflows
│   ├── scale-workers.yaml           # Worker node scaling workflow
│   ├── safe-node-drain.yaml         # Safe node maintenance workflow
│   ├── safe-pod-restart.yaml        # Safe pod restart workflow
│   ├── addon-upgrade.yaml           # Addon upgrade workflow
│   └── cluster-delete.yaml          # Cluster deletion workflow
│
├── edge-catalog/                    # Edge Components (Injected at Boot)
│   ├── argocd-edge.yaml             # Tenant-local GitOps engine
│   ├── grafana-alloy.yaml           # Telemetry forwarder
│   ├── kube-events-exporter.yaml    # K8s event forwarder to OpenSearch
│   ├── k8sgpt-operator.yaml         # K8sGPT operator for AI diagnostics
│   ├── k8sgpt-result-exporter.yaml  # K8sGPT findings exporter
│   ├── fleet-heartbeat.yaml         # Fleet connectivity heartbeat
│   ├── cilium.yaml                  # CNI configuration
│   └── ccm-csi.yaml                 # Cloud controller manager & CSI
│
├── catalog/                         # Selectable Services (Packaged to OCI)
│   ├── cni/
│   ├── databases/
│   └── observability/
│
├── observability/                   # Platform Observability Stack
│   ├── victoriametrics/             # VictoriaMetrics configuration
│   │   ├── vmcluster-values.yaml    # VMCluster Helm values
│   │   ├── vmalert-rules.yaml       # Alerting rules
│   │   └── alertmanager-config.yaml # Alertmanager configuration
│   └── opensearch/                  # OpenSearch configuration
│       ├── opensearch-values.yaml   # OpenSearch Helm values
│       └── index-templates/         # Index templates (5 files)
│           ├── cluster-events.json
│           ├── k8sgpt-findings.json
│           ├── fleet-heartbeat.json
│           ├── audit-logs.json
│           └── metrics-metadata.json
│
├── go.mod                           # One module to rule them all
└── Makefile
```

### 3. Execution Path: Where does code actually run?

To understand this structure, you must understand the deployment topography:

1.  **Developer's Laptop:** Runs `cmd/zero-ops`. It contains zero Kubernetes client logic. It only makes HTTP requests to the SaaS API.
2.  **SaaS Control Plane (Management Cluster):** Runs `cmd/zero-ops-api`, `cmd/zero-ops-worker`, `cmd/zero-ops-agent`. It houses the PostgreSQL database, VictoriaMetrics, and OpenSearch.
3.  **Fleet Shards (Management Clusters 1...N):** Runs standard CAPI and CAPH controllers. It receives YAMLs from the `zero-ops-api` via the `capi-mcp` tool.
4.  **Tenant Clusters (Hetzner VMs):** Runs Talos Linux. At boot, CAPI injects the contents of `edge-catalog/`. ArgoCD then pulls the contents of `catalog/` from the OCI registry.

### 4. When should you Split? (The Exit Strategy)

You should start with a Monorepo. You should **only** split (Polyrepo) if:

1.  **Open Source Strategy:** If you decide to open-source the CLI (`zero-ops`) and the `catalog/` to the public, but keep the `zero-ops-api` and `zero-ops-agent` closed-source.
2.  **Massive Scale:** You grow past 100+ platform engineers, where the AI Agent team and the API Backend team begin blocking each other's CI/CD pipelines.

**Recommendation:** Stick to the Monorepo. It is the only way to build a complex, multi-agent, Kubernetes-driven platform rapidly without drowning in versioning drift.

### 5. MCP Tool Server Process Architecture

**Important Note:** MCP tool servers in `pkg/agents/mcp/` are written as Go packages but run as separate processes per the PRD architecture. Each MCP server requires:

- **Separate Process:** Independent process with its own RBAC ServiceAccount
- **Network Listener:** Dedicated network endpoint for MCP protocol
- **Entrypoint Strategy:** Either separate `cmd/` entrypoints or goroutines within `cmd/zero-ops-agent/`

This distinction affects agent binary structure in Phase 6 implementation.
# Zero-Ops Platform — Product Requirements Document

**Version:** 7.0 — Open Source Fleet Observability Stack & Correlation Engine  
**Status:** APPROVED  
**Project:** zero-ops  
**Repo Model:** Go-Centric Monorepo  
**Author:** Platform Architecture Team  
**Date:** 2026  
**Classification:** CONFIDENTIAL — INTERNAL USE ONLY

---

## Changelog: v6.0 → v7.0

| Area | v6.0 State | v7.0 Change |
|---|---|---|
| Telemetry collector | Custom OTel Collector DaemonSet | **Grafana Alloy** — drop-in OTel-compatible collector with centralized config management and native fleet fan-in support |
| Fleet metrics store | NATS → custom State Processor → PostgreSQL (metrics) | **VictoriaMetrics** cluster — purpose-built multi-cluster time-series store; vmagent per tenant cluster with `region`/`cloud`/`az` label tagging |
| Event timeline & change correlation | Not defined — missing gap identified in v6.0 review | **OpenSearch** — durable event store receiving Kubernetes events, K8sGPT findings, ArgoCD sync events, Argo Workflow completions, and infra change records |
| Cluster topology in Fleet State | `shard_id` only — no region/cloud/AZ | `fleet_current_state` extended with `region`, `cloud_provider`, `availability_zone`, `cluster_class` — enables cross-cluster correlation queries |
| DiagnosticsAgent data sources | K8sGPT + runbook RAG only | K8sGPT + runbook RAG + **OpenSearch event timeline** — agent queries correlated change events before reasoning about root cause |
| MCP tool surface | 5 tool servers | 7 tool servers — adds `victoriametrics-mcp` and `opensearch-mcp` |
| Custom code required | Full ingestion pipeline + metrics store + event store | **Thin MCP wrappers only** — all storage and ingestion handled by adopted open source tools |
| "5 clusters same region degraded" query | Not answerable with v6.0 schema | Directly answerable: VictoriaMetrics label query `{region="eu-central-1", health_status="degraded"}` |
| "Recent infra change caused this" correlation | Not answerable — no change event store | Directly answerable: OpenSearch timeline query for change events in preceding 30-minute window |

---

## 1. Executive Summary

The Zero-Ops Platform is a next-generation, intent-driven Kubernetes SaaS designed to scale to tens of thousands of clusters. Version 7.0 replaces the custom-built Fleet State Engine internals with a curated open source observability stack, resolving the three correlation gaps identified during v6.0 review: missing topology attributes, no event timeline, and no infrastructure change tracking.

The platform is now built on five architectural pillars:

- **Agentic Control Plane** — `zero-ops-api` acts as the coordination layer, routing intents to a sharded fleet of Management Clusters backed by PostgreSQL state.
- **Edge GitOps** — ArgoCD deployed directly into tenant clusters via `ClusterResourceSet`, pulling from an OCI artifact store rather than the live SaaS API.
- **Fleet Observability Stack _(NEW v7.0)_** — A three-tier open source stack: **Grafana Alloy** for per-cluster collection, **VictoriaMetrics** for fleet-wide time-series aggregation with topology labels, and **OpenSearch** as the event timeline and change correlation engine.
- **Multi-Agent Collaboration System** — A Collaborator/Manager/Worker agent hierarchy where a central orchestrator reasons over Fleet State, event timeline, and runbook context, dispatches to specialist sub-agents via MCP, and executes infrastructure changes safely through Argo Workflows with optional human approval gates.
- **Safe Execution Layer** — Policy Gate + Argo Workflows + CNPG ticket integration for destructive operations.

> **v7.0 KEY SHIFT:** The platform stops building what the industry has already solved. Grafana Alloy, VictoriaMetrics, and OpenSearch are production-proven, CNCF-aligned tools used at fleet scale by operators managing 1,000+ clusters. The only custom code in the observability layer is a thin MCP wrapper that exposes these tools to the agent system in a governed, typed interface. This eliminates months of custom engineering and gives agents genuine cross-cluster correlation capability from day one.

---

## 2. Problem Statement

### 2.1 The GitOps Bottleneck _(Resolved in v4.0)_

Traditional platforms ran CAPI and a central ArgoCD instance watching thousands of remote clusters from a single Management Cluster. Kubernetes etcd degrades past ~500 clusters. Resolved in v4.0 via Fleet Sharding and Edge GitOps.

### 2.2 The Observation Gap _(Resolved in v5.0)_

The v4.0 Agentic Planner could only query PostgreSQL for static shard capacity data. No live signal from running clusters. Resolved in v5.0 via the Fleet State Engine.

### 2.3 Five Production-Grade Gaps _(Resolved in v6.0)_

v5.0 introduced the correct concepts but left five implementation gaps that would cause failures in a real production deployment:

| Gap | Risk if Unresolved | Resolution |
|---|---|---|
| **G1: Telemetry pipeline undefined** | Direct OTel→PostgreSQL at 10k clusters causes ingestion overload and data loss under spike conditions | NATS JetStream buffering pipeline (v6.0), now backed by Grafana Alloy + VictoriaMetrics (v7.0) |
| **G2: Fleet State DB model unspecified** | Ambiguity leads to over-engineering (graph DB) or under-engineering (no indexing strategy) | PostgreSQL-first with documented rationale and explicit graph DB migration trigger |
| **G3: Agent safety boundaries missing** | Autonomous agents with unrestricted Kubernetes API access can trigger infrastructure storms | Safe Execution Layer: Policy Gate + Argo Workflows + CNPG ticket integration |
| **G4: Shard failure handling absent** | When a shard's Kubernetes API becomes unreachable, `zero-ops-api` has no detection or routing mechanism | Shard health registry with 30s heartbeat checks and unavailability routing |
| **G5: Catalog control-plane dependency** | ArgoCD pulling from the live SaaS API creates a reconciliation failure whenever `zero-ops-api` is unavailable | OCI Artifact Store: ArgoCD pulls from versioned OCI artifacts, not the live API |

### 2.4 The Single-Agent Ceiling _(Resolved in v6.0)_

A monolithic Agent Planner is architecturally appropriate for v4/v5 but fails at production scale for three reasons. First, a single agent with access to all tools has an unbounded blast radius — one mistaken intent can affect the entire fleet. Second, a single reasoning loop cannot hold sufficient context for disparate domains (metrics analysis, GitOps reconciliation, cluster lifecycle, cost optimization) simultaneously. Third, a monolithic planner is not extensible — adding a new capability requires modifying the core planning logic rather than adding a new specialist.

### 2.5 Three Fleet Correlation Gaps _(NEW — Resolved in v7.0)_

The v6.0 Fleet State Engine stored per-cluster snapshots but had no ability to answer the most important operational questions: *why are multiple clusters in the same region failing?* and *did a recent infrastructure change cause this?* Three concrete gaps were identified:

| Gap | Root Cause | Consequence |
|---|---|---|
| **C1: No topology attributes** | `fleet_current_state` had `shard_id` but no `region`, `cloud_provider`, or `availability_zone` | Query "show me degraded clusters in eu-central-1" was unanswerable. Agents could not group failures by geographic blast radius. |
| **C2: No event timeline** | No store for discrete, timestamped events (node state transitions, addon sync completions, alert firings, CAPI operations) | Agents could not answer "what happened on this cluster in the 30 minutes before it went critical?" — only raw metric snapshots existed. |
| **C3: No infrastructure change tracking** | ArgoCD syncs, Argo Workflow completions, and catalog pushes were not recorded as change events | Agents could not correlate "5 clusters degraded at 14:32" with "Cilium upgrade pushed at 14:28" — the causal link was invisible. |

The v7.0 resolution: do not build custom solutions for any of these gaps. The industry has production-proven open source tools for each. Adopt them.

### 2.6 Constraints & Non-Goals

- **API-First, not Git-First:** Primary orchestration interface is the REST API (`zero-ops-api`).
- **BYOC Model:** Tenants provide Hetzner API tokens. Compute runs in their accounts.
- **Immutable OS:** All clusters must use Talos Linux. No SSH. No Ubuntu.
- **Non-Goal:** Monolithic Management Cluster. Horizontal sharding of CAPI is mandatory from Day 1.
- **Non-Goal:** Raw metrics mirroring into PostgreSQL. VictoriaMetrics is the time-series store. PostgreSQL holds tenant state, billing, audit, and fleet config only.
- **Non-Goal:** Graph database for Fleet State. PostgreSQL with JSONB and proper indexing is the correct choice at current query complexity. The migration trigger to a graph model is documented in Section 5.2.
- **Non-Goal:** Fully autonomous remediation without human approval gates for destructive operations. Human-in-the-loop is a first-class feature, not a temporary limitation.
- **Non-Goal:** Building custom telemetry collectors, time-series databases, or event stores. Grafana Alloy, VictoriaMetrics, and OpenSearch are adopted as-is. Custom code is limited to MCP wrappers that expose these tools to the agent system.

---

## 3. User Personas & User Journeys

### 3.1 Personas

| Persona | Role | Primary Interface |
|---|---|---|
| Platform Admin | Bootstraps Fleet Shards, maintains SaaS API, manages global catalog and runbook corpus | CLI + Admin API |
| Tenant Developer | Expresses infrastructure intent — e.g. "I need a prod cluster with Postgres" | REST API / CLI |
| Platform Agent (AI/Automation) | Programmatic actor interfacing with the Collaborator Agent to optimize, scale, or recover environments | Intent API / Webhooks |
| On-Call Engineer | Receives CNPG tickets for destructive operations via platform console; reviews agent-generated incident reports | Platform Console |
| Fleet Observer | Reads Fleet State dashboards for capacity planning, cost analysis, and anomaly triage | Fleet State API |

### 3.2 User Journeys

#### Journey A: Bootstrapping a Fleet Shard _(Platform Admin)_

**Trigger:** SaaS needs more capacity to manage tenant clusters.

1. Admin runs: `zero-ops mgmt bootstrap --name=shard-eu-1 --region=fsn1`
2. CLI spins up local Kind cluster, installs CAPI/CAPH, provisions Talos Management Cluster on Hetzner.
3. CLI registers `shard-eu-1` with `zero-ops-api` DB, including API endpoint and `capacity_weight`.
4. Shard registry begins emitting 30s heartbeats to `zero-ops-api`. Shard becomes available for intent routing.
5. Fleet State Engine begins receiving OTel signals from the shard within 60s.

#### Journey B: Intent-Based Provisioning _(Tenant / Agent)_

**Trigger:** Tenant or AI Agent requests a new production environment.

1. Agent sends `POST /api/v1/clusters`:
   ```json
   {"intent": "create_cluster", "class": "hetzner-prod", "addons": ["prometheus"]}
   ```
2. SaaS API queries `fleet_current_state` for shards with `status = active` and lowest real CPU utilization.
3. API hydrates `ClusterClass` template, dispatches CAPI YAML to selected shard.
4. Shard provisions Hetzner VMs (Talos). `ClusterResourceSet` injects ArgoCD + OTel Collector + fleet-heartbeat.
5. ArgoCD pulls its initial catalog from the OCI artifact store (not the live SaaS API).
6. New cluster registers with Fleet State Engine within 30s of boot.

#### Journey C: Agentic Cross-Cluster Rebalancing _(Multi-Agent)_

**Trigger:** Fleet Observability Stack detects skewed load distribution. Policy threshold fires.

1. VictoriaMetrics recording rule detects `cluster-12` CPU 90% for 10 minutes. Alert fires to `zero-ops-api` via Alertmanager webhook.
2. **Collaborator Agent** receives the event. Queries VictoriaMetrics via `victoriametrics-mcp` for context: shard assignment, current node count, tenant quota headroom.
3. Collaborator queries OpenSearch via `opensearch-mcp`: "Any infra changes on `cluster-12` in the last hour?" — rules out a recent change as cause before scaling.
4. Collaborator routes to **LifecycleAgent** (Worker): "Scale cluster-12 workers to 5."
5. **LifecycleAgent** checks Policy Gate: rate limit OK, concurrency cap OK, quota check passes.
6. LifecycleAgent submits `scale_workers` Argo Workflow. Argo patches CAPI `Cluster` resource on the correct shard. Argo Workflow completion event written to OpenSearch.
7. VictoriaMetrics confirms CPU normalisation. Collaborator closes the loop.

#### Journey D: Destructive Operation with Human Approval _(On-Call Engineer)_

**Trigger:** VictoriaMetrics detects `DiskPressure` condition on a node. K8sGPT operator surfaces finding. DiagnosticsAgent identifies node drain as required remediation.

1. **DiagnosticsAgent** receives K8sGPT `Result` CRD signal (exported to OpenSearch). Queries OpenSearch event timeline: "Any recent infra changes on this node's cluster?" — finds no correlating change event.
2. DiagnosticsAgent queries runbook corpus via RAG: identifies node drain as correct remediation.
3. **Collaborator Agent** classifies the intent as `destructive` (node drain affects running workloads).
4. Argo Workflow pauses. CNPG ticket created and reflected in platform console with full context: node, cluster, shard, functional domain, requester, runbook reference, correlated events from OpenSearch. Console displays "Resolve in Cursor" button with structured MCP message containing available tools (k8sgpt-mcp, opensearch-mcp, capi-mcp, victoriametrics-mcp), workflow context, and approval requirements.
5. On-call engineer reviews ticket in platform console and clicks **"Resolve in Cursor"** to open Cursor with structured MCP context including available tools, workflow details, and cluster state, or approves directly in console.
6. Argo Workflow resumes. `Safe Node Recycle` workflow executes: respects PDB, cordons node, evicts pods gracefully, triggers replacement VM. Completion event written to OpenSearch.
7. VictoriaMetrics confirms node health restored. Change record written to audit log.

#### Journey E: Automated Fleet Upgrade _(Platform Agent)_

**Trigger:** VictoriaMetrics recording rule detects 40% of clusters running Cilium 1.14. Policy mandates 1.15 within 30 days.

1. **UpgradeAgent** (Worker) queries VictoriaMetrics via `victoriametrics-mcp`: `count by (cluster_id) (fleet_addon_version{addon="cilium"} == "1.14.x")` — identifies affected clusters by label.
2. Collaborator batches upgrade intents in rolling windows — 10 clusters per hour.
3. Each intent triggers ArgoCD sync on the target cluster's edge engine via updated OCI catalog artifact. ArgoCD sync completion event written to OpenSearch.
4. VictoriaMetrics tracks `fleet_addon_version` metric as clusters confirm completion. Progress auditable in real time. OpenSearch provides per-cluster change timeline for audit.

#### Journey F: On-Call Report Generation _(Platform Agent / Platform Console)_

**Trigger:** On-call engineer requests via platform console: `Generate the on-call report from 2026-03-09T22:00:00 to 2026-03-10T14:59:59`

1. **ReportAgent** (Worker) queries `cluster_state` time-series for the specified window.
2. Correlates health events, alert counts, and remediation actions from the audit log.
3. Generates structured on-call report document. Posts summary and document link to Slack thread.

---

## 4. Proposed Architecture

### 4.1 High-Level Architecture (v7.0)

```
ZERO-OPS v7.0 — FULL ARCHITECTURE

  ┌─────────────────────────────────────────────────────────────────┐
  │  CONTROL PLANE                                                  │
  │                                                                 │
  │  User / AI Agent / Platform Console                             │
  │       │  REST Intent / Webhook                                  │
  │  zero-ops-api  ◄────────────────────────────────────────────┐  │
  │       │                                                      │  │
  │  ┌────▼──────────────────────────────────────────────────┐  │  │
  │  │  MULTI-AGENT COLLABORATION SYSTEM                     │  │  │
  │  │                                                       │  │  │
  │  │  Collaborator Agent  (orchestrator + router)          │  │  │
  │  │       │  MCP                                          │  │  │
  │  │       ├── MetricsAgent    → victoriametrics-mcp       │  │  │
  │  │       ├── LifecycleAgent  → capi-mcp                  │  │  │
  │  │       ├── GitOpsAgent     → argocd-mcp                │  │  │
  │  │       ├── DiagnosticsAgent→ k8sgpt-mcp + opensearch-mcp│ │  │
  │  │       ├── UpgradeAgent    → victoriametrics-mcp        │ │  │
  │  │       └── ReportAgent     → opensearch-mcp + audit-mcp│ │  │
  │  │                                                       │  │  │
  │  │  LLM Gateway  →  LLM Models (pluggable)               │  │  │
  │  │  Runbook RAG  →  Runbook corpus (embeddings)          │  │  │
  │  └───────────────────────┬───────────────────────────────┘  │  │
  │                          │  Intent (validated)               │  │
  │  ┌───────────────────────▼───────────────────────────────┐  │  │
  │  │  SAFE EXECUTION LAYER                                 │  │  │
  │  │                                                       │  │  │
  │  │  Policy Gate  (rate limits, concurrency, quota)       │  │  │
  │  │       │  pass                                         │  │  │
  │  │  Argo Workflow Engine  (guardrails encoded in WF)     │  │  │
  │  │       │  optional pause + change event → OpenSearch   │  │  │
  │  │  CNPG Ticket Creation  (destructive ops only)              │  │  │
  │  │       │  approved                                     │  │  │
  │  │  Execution API  →  CAPI / kubectl (RBAC-scoped)   ────┘  │  │
  │  └───────────────────────────────────────────────────────┘  │  │
  │                                                              │  │
  │  PostgreSQL  (tenant state, billing, fleet config, audit)    │  │
  └─────────────────────────────────────────────────────────────┘  │
                                                                    │
  ┌─────────────────────────────────────────────────────────────┐  │
  │  FLEET LAYER                                                │  │
  │                                                             │  │
  │  Fleet Shard A / B / N  (CAPI + CAPH)                       │  │
  │       │  Provision Talos VMs + ClusterResourceSet            │  │
  │  Tenant Clusters  (Talos)                                   │  │
  │       ├── ArgoCD       →  OCI Artifact Store (catalog)      │  │
  │       ├── Grafana Alloy →  VictoriaMetrics (metrics)        │  │
  │       ├── kube-events-exporter → OpenSearch (K8s events)    │  │
  │       ├── K8sGPT Operator → OpenSearch (diagnostic findings)│  │
  │       └── fleet-heartbeat CronJob → zero-ops-api            │  │
  └───────────────────────────┬─────────────────────────────────┘  │
                              │                                      │
  ┌───────────────────────────▼─────────────────────────────────┐  │
  │  FLEET OBSERVABILITY STACK  (NEW v7.0)                      │  │
  │                                                             │  │
  │  VictoriaMetrics Cluster                                    │  │
  │    vmagent (per cluster) → vminsert → vmstorage → vmselect  │  │
  │    Labels: cluster_id, shard_id, region, cloud, az          │  │
  │    Answers: "All clusters degraded in eu-central-1"         │  │
  │    Answers: "Fleet-wide Cilium version distribution"        │  │
  │                                                             │  │
  │  OpenSearch Cluster                                         │  │
  │    Indexes: k8s-events, k8sgpt-findings, infra-changes,     │  │
  │             argocd-syncs, argo-workflow-completions          │  │
  │    Answers: "What changed on cluster-12 at 14:28?"          │  │
  │    Answers: "Correlate 5 degraded clusters → Cilium push"   │  │
  │                                                             │  │
  │  Policy Evaluator  →  fleet_policies (PostgreSQL)           │  ─┘
  │  Alertmanager      →  VictoriaMetrics alert rules           │
  │  fleet_current_state (PostgreSQL — config + shard routing)  │
  └─────────────────────────────────────────────────────────────┘
```

### 4.2 Components & Responsibilities

| Component | Implementation | Responsibility |
|---|---|---|
| SaaS Control Plane | Go / Gin / PostgreSQL | Coordination layer. Tenant state, billing, intent routing, shard dispatch. |
| **Collaborator Agent** | Go + LLM Gateway | Orchestrator. Receives intents, reasons over Fleet State + event timeline + runbook context, routes to specialist Workers via MCP. |
| **Worker Agents** | Go + MCP tool servers | Specialists: MetricsAgent, LifecycleAgent, GitOpsAgent, DiagnosticsAgent, UpgradeAgent, ReportAgent. Each has a narrow, bounded tool surface. |
| **LLM Gateway** | Go reverse proxy | Governance layer. Routes LLM calls to pluggable backends (OpenAI, Claude, Mistral). Enforces rate limits and audit logging on all LLM calls. |
| **Runbook RAG** | Go + embedding store | Vector-indexed runbook corpus. DiagnosticsAgent queries it to ground remediation reasoning in operational knowledge. |
| **Safe Execution Layer** | Policy Gate + Argo Workflows + CNPG ticket integration | Guardrail enforcement. Agents submit workflows, not direct API calls. Destructive ops create CNPG tickets with platform console integration. Every workflow completion emits a change event to OpenSearch. |
| **Grafana Alloy** _(NEW v7.0)_ | Helm chart (per tenant cluster) | Drop-in OTel-compatible collector. Collects metrics, logs, traces. Centrally configured via Alloy cluster config. Fans into VictoriaMetrics. Replaces custom OTel Collector + NATS ingestion path for metrics. |
| **VictoriaMetrics** _(NEW v7.0)_ | vmcluster Helm chart (control plane) | Fleet-wide time-series store. vmagent per cluster tags all metrics with `cluster_id`, `shard_id`, `region`, `cloud_provider`, `availability_zone`. vmselect exposes PromQL-compatible query API for agents. Replaces PostgreSQL as the metrics store. |
| **OpenSearch** _(NEW v7.0)_ | OpenSearch cluster (control plane) | Durable event store and correlation engine. Receives Kubernetes events, K8sGPT findings, ArgoCD sync completions, Argo Workflow completions, and catalog push records. Enables timeline queries and change correlation. Replaces the missing `fleet_events`/`infra_changes` tables. |
| **K8sGPT Operator** | Helm chart (per tenant cluster) | Per-cluster AI diagnostics. Publishes `Result` CRDs. Results exported to OpenSearch for fleet-wide correlation. MCP-exposed via `k8sgpt-mcp`. |
| **kube-events-exporter** _(NEW v7.0)_ | DaemonSet (per tenant cluster) | Exports Kubernetes events to OpenSearch in real time. Provides the raw event stream that enables timeline correlation queries. |
| OCI Artifact Store | OCI-compatible registry (e.g. GHCR, Harbor) | Versioned catalog artifacts. ArgoCD pulls from here, not the live SaaS API. |
| Fleet Shard | K8s / CAPI / CAPH | Dumb execution engine. Receives CAPI YAMLs, provisions Hetzner VMs via CAPH. |
| Edge GitOps | ArgoCD (edge build) | Runs inside tenant cluster. Day-2 addon reconciliation against OCI catalog. |

---

## 5. Technical Specifications

### 5.1 Multi-Agent Collaboration System

#### 5.1.1 Agent Hierarchy

The system uses a three-tier hierarchy derived from production AIops patterns:

```
Tier 1 — Collaborator Agent  (1 instance)
    Receives all intents from zero-ops-api
    Maintains conversation/task context
    Reasons over Fleet State + event timeline + RAG context
    Routes sub-tasks to Worker Agents via MCP
    Owns the human approval decision boundary

Tier 2 — Worker Agents  (N specialist instances)
    MetricsAgent    — queries VictoriaMetrics for utilization signals and fleet-wide metric aggregations
    LifecycleAgent  — cluster create/scale/delete via CAPI
    GitOpsAgent     — ArgoCD sync, catalog updates, rollbacks
    DiagnosticsAgent— K8sGPT analysis + OpenSearch event timeline + runbook RAG queries
    UpgradeAgent    — addon version management, rolling upgrades via VictoriaMetrics version metrics
    ReportAgent     — on-call reports, audit summaries, cost analysis from OpenSearch + audit log

Tier 3 — Tool Servers  (MCP-compatible)
    fleet-state-mcp         — read cluster routing metadata from PostgreSQL fleet_current_state
    victoriametrics-mcp     — PromQL queries against VictoriaMetrics vmselect  [NEW v7.0]
    opensearch-mcp          — event timeline and correlation queries against OpenSearch  [NEW v7.0]
    capi-mcp                — submit CAPI manifests to Fleet Shards
    argocd-mcp              — trigger ArgoCD syncs via ArgoCD API
    k8sgpt-mcp              — invoke K8sGPT analysis on target cluster
    audit-mcp               — write to and query the audit log
```

#### 5.1.2 MCP as the Agent-Tool Interface

All Worker Agents communicate with platform capabilities exclusively via MCP (Model Context Protocol) tool servers. This is a non-negotiable architectural constraint.

**Why MCP:**
- Adding a new platform capability means adding a new MCP tool server — the Collaborator Agent does not need to be modified.
- MCP tool calls are logged by the LLM Gateway, creating a complete audit trail of every action an agent takes.
- Tool servers enforce their own authorization: `capi-mcp` validates that the requesting agent has permission for the target shard before accepting a call.
- RBAC is enforced at the tool server level, not at the agent level — agents cannot bypass it by calling Kubernetes APIs directly.

**New in v7.0 — `victoriametrics-mcp`:**
Exposes typed PromQL queries against VictoriaMetrics vmselect. The MetricsAgent and UpgradeAgent call this tool to answer questions like "which clusters in eu-central-1 have CPU > 85% for 10 minutes?" or "what percentage of the fleet is running Cilium 1.14?". The MCP wrapper validates query scope — agents can only query metrics for clusters within their authorized shard set.

**New in v7.0 — `opensearch-mcp`:**
Exposes structured event timeline and correlation queries against OpenSearch. The DiagnosticsAgent's first step for any alert is to call `opensearch-mcp` with a time-window query: "give me all events for cluster-12 and its region in the 30 minutes preceding this alert." This grounds the agent's reasoning in real change history before it consults the runbook corpus. The ReportAgent also uses `opensearch-mcp` to construct on-call report timelines.

#### 5.1.3 Runbook RAG

The DiagnosticsAgent grounds its remediation reasoning in a vector-indexed runbook corpus:

```
Runbook ingestion:
  Platform Admin uploads runbook docs  →  RAG indexer  →  embedding store

Diagnostics query:
  DiagnosticsAgent receives anomaly signal
       │
  Embeds signal description
       │
  Retrieves top-K relevant runbook chunks
       │
  Injects chunks into LLM context with signal data
       │
  LLM reasons over: "Given this signal and these runbooks,
                     what is the correct remediation?"
       │
  Returns structured remediation intent to Collaborator
```

This prevents the agent from inventing remediation steps. If no relevant runbook exists, the DiagnosticsAgent escalates to a human rather than guessing.

#### 5.1.4 LLM Gateway

All LLM calls from all agents are routed through a central LLM Gateway:

- **Pluggable backends:** OpenAI, Anthropic Claude, Mistral — configured per agent or per intent class.
- **Governance:** Rate limiting per agent, per tenant, per hour. Cost attribution per LLM call written to PostgreSQL for billing.
- **Audit:** Every LLM prompt and response is logged (with PII scrubbing) for compliance review.
- **Fallback:** If the primary LLM backend is unavailable, the gateway routes to a secondary. If all backends fail, the Collaborator falls back to hard-coded policy templates (degraded mode).

### 5.2 Fleet Observability Stack _(NEW v7.0)_

The v6.0 Fleet State Engine (custom NATS pipeline → State Processor → PostgreSQL) is replaced with a three-tier open source observability stack. No custom ingestion pipeline is built. Custom code is limited to the MCP wrappers that expose these tools to the agent system.

#### 5.2.1 Tier 1 — Grafana Alloy (Per-Cluster Collector)

**What it is:** Grafana Alloy is an OTel-compatible, open source telemetry collector that supersedes the Prometheus Agent and the standalone OTel Collector. It is deployed as a DaemonSet (or single replica) in every tenant cluster via `ClusterResourceSet`.

**Why Alloy over a plain OTel Collector:**
- Native support for both OTel and Prometheus scrape formats — no separate Prometheus agent needed.
- **Centralized configuration management:** Alloy instances pull their configuration from a central config server, meaning a change to the scrape config or forwarding rules propagates to all 10,000 tenant clusters without redeployment.
- **Workload distribution clustering:** Multiple Alloy instances in a cluster can form a cluster for automatic workload distribution — relevant as tenant clusters grow.
- Actively maintained CNCF project with a clear migration path from Prometheus Agent.

**What Alloy collects per tenant cluster:**
- Node and pod metrics (CPU, memory, disk, network)
- Kubernetes control plane metrics (API server, scheduler, etcd)
- Addon-specific metrics (Cilium, CoreDNS, ArgoCD)
- All forwarded to VictoriaMetrics vmagent endpoint via remote_write

**Alloy configuration distribution:**
```
zero-ops-api updates Alloy config
    │  HTTP push to Alloy config server (control plane)
Alloy config server
    │  config pull (Alloy clustering protocol)
All Alloy instances in all tenant clusters apply updated config
    │  no redeployment required
```

#### 5.2.2 Tier 2 — VictoriaMetrics (Fleet-Wide Time-Series Store)

**What it is:** VictoriaMetrics is a high-performance, cost-efficient open source time-series database. The vmcluster deployment (vminsert / vmstorage / vmselect) provides horizontal scalability and multi-tenancy.

**Fan-in architecture:**
```
Tenant Cluster (each)
    Grafana Alloy
        │  remote_write (mTLS)
        ▼
VictoriaMetrics vminsert (control plane)
        │
VictoriaMetrics vmstorage (sharded)
        │
VictoriaMetrics vmselect  ←── Agent MCP queries (PromQL)
        │
Alertmanager  ←── Alert rules evaluated by vmalert
        │
zero-ops-api  ←── Alertmanager webhook
```

**Mandatory metric labels — every metric from every cluster carries:**
```
cluster_id="uuid"
shard_id="uuid"
region="eu-central-1"            ← NEW v7.0
cloud_provider="hetzner"         ← NEW v7.0
availability_zone="hel1-dc2"     ← NEW v7.0
cluster_class="hetzner-prod"     ← NEW v7.0
```

These labels are injected by Grafana Alloy at collection time from cluster metadata stored in the `fleet_current_state` table. They are not scraped from the cluster — the SaaS API provides them to Alloy at bootstrap.

**What this enables:**
- "Show me all clusters in `region=eu-central-1` with `health_status=degraded`" — a single PromQL query, no custom aggregation code
- "What is the fleet-wide p95 CPU utilization by `cluster_class`?" — label aggregation
- "Which clusters on `shard_id=X` have memory > 85%?" — cross-cluster filter
- "Is this a single-cluster issue or a regional blast radius?" — answered in one query

**vmalert recording rules (examples):**
```yaml
groups:
  - name: fleet.cluster.health
    rules:
      - record: fleet:cluster:cpu_avg5m
        expr: avg_over_time(container_cpu_usage_seconds_total[5m])
      - alert: ClusterCPUHigh
        expr: fleet:cluster:cpu_avg5m > 0.85
        for: 10m
        labels:
          severity: warning
        annotations:
          cluster_id: "{{ $labels.cluster_id }}"
          region: "{{ $labels.region }}"
```

**PostgreSQL role change:** `fleet_current_state` no longer stores raw metrics. It stores routing metadata only: `cluster_id`, `shard_id`, `region`, `cloud_provider`, `availability_zone`, `cluster_class`, `status`, `last_heartbeat`. VictoriaMetrics is the authoritative time-series source. The `cluster_state` append-log table is removed — VictoriaMetrics provides native time-series history with configurable retention.

**Updated `fleet_current_state` schema:**
```sql
CREATE TABLE fleet_current_state (
    cluster_id        UUID PRIMARY KEY REFERENCES clusters(id),
    shard_id          UUID REFERENCES fleet_shards(id),
    last_heartbeat    TIMESTAMPTZ,
    status            VARCHAR(20) DEFAULT 'active',  -- active | degraded | status_unknown
    region            VARCHAR(63) NOT NULL,           -- NEW v7.0
    cloud_provider    VARCHAR(63) NOT NULL,           -- NEW v7.0
    availability_zone VARCHAR(63),                    -- NEW v7.0
    cluster_class     VARCHAR(63) NOT NULL            -- NEW v7.0
);

-- Shard routing indexes
CREATE INDEX idx_fcs_shard ON fleet_current_state (shard_id, status);
CREATE INDEX idx_fcs_region ON fleet_current_state (region, status);    -- NEW v7.0
CREATE INDEX idx_fcs_cloud  ON fleet_current_state (cloud_provider);    -- NEW v7.0
```

#### 5.2.3 Tier 3 — OpenSearch (Event Timeline & Change Correlation Engine)

**What it is:** OpenSearch is an open source search and analytics engine. In Zero-Ops it serves as the durable event store — every discrete event that happens in the fleet is written here with a timestamp and full metadata. This is the component that enables root-cause correlation.

**Why OpenSearch over a custom PostgreSQL `fleet_events` table:**
- Full-text search over event payloads (e.g. search for all events containing a specific config key)
- Native time-range queries across billions of events without table partitioning management
- OpenSearch Security Analytics correlation engine — built-in support for correlating findings across different event types
- Kibana-compatible dashboards for human operators to explore event timelines
- Horizontal scalability without custom sharding logic

**Event indexes:**

| Index | Source | Event Examples |
|---|---|---|
| `k8s-events` | kube-events-exporter (per cluster) | Pod OOMKilled, Node NotReady, PVC Pending, ImagePullBackOff |
| `k8sgpt-findings` | K8sGPT Operator Result CRDs (per cluster) | "Deployment X has insufficient replicas", "Node Y has DiskPressure" |
| `argocd-syncs` | ArgoCD notification webhook (per cluster) | Sync success/failure, app out-of-sync, rollback triggered |
| `infra-changes` | Argo Workflow completion hook (control plane) | scale_workers completed, node_drain completed, addon_upgrade started |
| `catalog-pushes` | zero-ops-api OCI push hook (control plane) | catalog/hetzner-prod:v1.5.0 pushed, affected cluster classes |
| `alert-firings` | Alertmanager webhook (control plane) | ClusterCPUHigh fired for cluster-12, region eu-central-1 |

**Event schema (common fields):**
```json
{
  "timestamp":      "2026-03-10T14:28:03Z",
  "event_type":     "catalog-push",
  "cluster_id":     "uuid | null",
  "shard_id":       "uuid | null",
  "region":         "eu-central-1",
  "cloud_provider": "hetzner",
  "actor":          "zero-ops-api | GitOpsAgent | on-call-engineer",
  "summary":        "catalog/hetzner-prod:v1.5.0 pushed (Cilium 1.15.0)",
  "payload":        { ... event-specific fields ... },
  "correlation_id": "uuid"   // links related events in a causal chain
}
```

**Correlation query examples:**

*"5 clusters in eu-central-1 degraded at 14:32 — what changed?"*
```
GET k8s-events,infra-changes,catalog-pushes/_search
{
  "query": {
    "bool": {
      "filter": [
        { "term": { "region": "eu-central-1" }},
        { "range": { "timestamp": { "gte": "14:00", "lte": "14:32" }}}
      ]
    }
  },
  "sort": [{ "timestamp": "asc" }]
}
→ Returns: catalog/hetzner-prod:v1.5.0 pushed at 14:28 (Cilium 1.15.0)
→ Agent conclusion: Cilium upgrade correlates with degradation. Trigger rollback intent.
```

*"What is the event history for cluster-12 in the last hour?"*
```
GET k8s-events,k8sgpt-findings,argocd-syncs,infra-changes/_search
{
  "query": { "term": { "cluster_id": "cluster-12-uuid" }},
  "sort": [{ "timestamp": "asc" }],
  "size": 100
}
→ Returns full ordered timeline: Node NotReady → K8sGPT DiskPressure finding → agent drain intent → workflow completion
```

**kube-events-exporter deployment:**
Each tenant cluster gets a `kube-events-exporter` DaemonSet (injected via `ClusterResourceSet`). It watches the Kubernetes event stream and forwards all events to the central OpenSearch cluster via mTLS, tagged with the cluster's `cluster_id` and topology labels.

**K8sGPT findings export:**
The K8sGPT Operator publishes `Result` CRDs inside each tenant cluster. A sidecar exporter watches these CRDs and forwards new/updated findings to the `k8sgpt-findings` index in OpenSearch. This means the DiagnosticsAgent can query "show me all K8sGPT findings across clusters in eu-central-1 in the last hour" — not just findings for a single cluster.

#### 5.2.4 Tool Adoption Summary

| Requirement | v6.0 Approach | v7.0 Adoption | Custom Code Required |
|---|---|---|---|
| Per-cluster telemetry collection | Custom OTel Collector config | **Grafana Alloy** (Helm chart + central config) | None — config only |
| Fleet metrics aggregation | NATS → State Processor → PostgreSQL | **VictoriaMetrics vmcluster** | None — Helm chart |
| Topology-labeled metrics | Not present | vmagent label injection from cluster metadata | ~50 lines Alloy config |
| Fleet-wide metric queries | Custom query API | VictoriaMetrics vmselect (PromQL) | MCP wrapper: `victoriametrics-mcp` |
| Kubernetes event timeline | Not present | **kube-events-exporter** → OpenSearch | None — config only |
| K8sGPT fleet-wide findings | Single cluster only | K8sGPT Result CRD exporter → OpenSearch | ~100 lines exporter |
| Change event tracking | Not present | Argo Workflow hooks + ArgoCD webhooks → OpenSearch | Hook config + index template |
| Event timeline queries | Not present | OpenSearch query API | MCP wrapper: `opensearch-mcp` |
| Alert-to-change correlation | Not present | OpenSearch correlation engine | Query config only |

#### 5.2.5 NATS JetStream — Retained Role

NATS JetStream is retained in v7.0 but its role is narrowed. It is **no longer** the metrics ingestion backbone (VictoriaMetrics handles this directly). It is retained for:

- **Fleet policy event bus:** `fleet_policies` evaluator emits policy trigger events to NATS. Collaborator Agent subscribes.
- **Shard heartbeat channel:** `fleet-heartbeat` CronJobs post to NATS subjects. `zero-ops-api` shard health checker consumes.
- **Inter-agent messaging:** Collaborator Agent publishes sub-task events to NATS. Worker Agents subscribe to their respective subjects.

This is a significantly lighter NATS footprint than v6.0 — it is a lightweight event bus, not a telemetry backbone.

#### 5.2.6 Schema Versioning

VictoriaMetrics metric schemas are versioned via label `schema_version`. Breaking metric changes require a version bump and a dual-ingest migration window. OpenSearch index mappings are versioned via index aliases — `k8s-events-v1` aliased to `k8s-events`. Breaking changes create a new index version and update the alias after reindexing.

### 5.3 Safe Execution Layer

#### 5.3.1 Architecture

Agents do not call the Kubernetes API directly. All infrastructure mutations flow through the Safe Execution Layer:

```
Worker Agent
    │  validated intent
Policy Gate
    │  rate limit: max 5 scaling actions per cluster per hour
    │  concurrency cap: max 20 simultaneous scaling ops fleet-wide
    │  quota check: tenant node quota in PostgreSQL
    │  PDB check: verify PodDisruptionBudgets before drain
    │  dry-run mode: available for policy testing
    │  pass
Argo Workflow Engine
    │  guardrails encoded in workflow definition (not agent logic)
    │  optionally pauses for human approval
    │  writes change record to agent_audit_log
    │  RBAC-scoped kubectl / client-go calls
Kubernetes API / CAPI
```

#### 5.3.2 Concrete Guardrails

These guardrails are encoded in Argo Workflow templates, not in agent prompts or policy configs:

| Guardrail | Implementation |
|---|---|
| Don't restart more than X pods/nodes in a short period | Workflow step checks restart count in rolling window before proceeding |
| Respect PodDisruptionBudgets during pod eviction | Workflow uses `kubectl drain --honor-pdb` — never force eviction by default |
| Never force-delete resources by default | `--force` flag requires explicit `force: true` in intent payload + human approval |
| Check utilization before scaling down | Workflow step queries Fleet State Engine; aborts if CPU > 40% on target cluster |
| Rate limit per cluster | Policy Gate enforces max 5 mutating operations per cluster per hour |
| Concurrency cap fleet-wide | Policy Gate maintains an atomic counter; rejects intents exceeding cap |

#### 5.3.3 Human Approval Workflow

For intents classified as `destructive` (node drain, cluster delete, forced scaling down, config deletion), the Argo Workflow pauses and creates a CNPG ticket reflected in the platform console:

```
Argo Workflow pauses at approval step
    │
CNPG ticket created in platform console:
    "Node Drain Confirmation Required
     Node:              ip-10-98-91-168.us-west-2.compute.internal
     Cluster:           prod-tenant-47
     Shard:             shard-eu-1
     Functional Domain: payments
     Requester:         DiagnosticsAgent (DiskPressure remediation)
     Runbook:           disk-pressure-remediation-v2
     
     MCP Message for Cursor:
     {
       "context": "Node drain required for DiskPressure remediation",
       "cluster_id": "prod-tenant-47",
       "node": "ip-10-98-91-168.us-west-2.compute.internal",
       "shard_id": "shard-eu-1",
       "mcp_tools": [
         "k8sgpt-mcp: Query cluster diagnostics",
         "opensearch-mcp: Check event timeline for correlations",
         "capi-mcp: Execute node drain workflow",
         "victoriametrics-mcp: Monitor cluster health during operation"
       ],
       "runbook_reference": "disk-pressure-remediation-v2",
       "workflow_id": "node-drain-workflow-uuid",
       "approval_required": true
     }
     
     [Resolve in Cursor]  [Approve]  [Cancel]"
    │
Engineer clicks "Resolve in Cursor"  →  Opens Cursor with full MCP context, tools, and workflow details
Engineer clicks "Approve"            →  Argo Workflow resumes
Engineer clicks "Cancel"             →  Workflow terminates, audit log records rejection
Timeout (30 min)                     →  Workflow terminates, escalation alert fires
```

Intent classes and their approval requirements:

| Intent Class | Examples | Approval Required |
|---|---|---|
| `read` | Query Fleet State, generate reports | Never |
| `safe-mutate` | Scale workers up, deploy addon upgrade | Never (guardrails only) |
| `cautious-mutate` | Scale workers down, restart pods | Configurable per fleet_policy |
| `destructive` | Node drain, cluster delete, force-delete | Always |

### 5.4 Shard Failure Handling

#### 5.4.1 Shard Health Registry

The `fleet_shards` table (defined in Section 5.2.2) is the authoritative registry for shard availability. The `zero-ops-api` runs a background shard health checker:

```
Every 30 seconds:
  For each shard with status = 'active':
    Attempt: GET <shard_api_endpoint>/readyz  (timeout: 5s)

    If success:
      UPDATE fleet_shards SET consecutive_misses = 0,
                              last_heartbeat = now()
      WHERE id = <shard_id>

    If failure:
      UPDATE fleet_shards SET consecutive_misses = consecutive_misses + 1
      WHERE id = <shard_id>

      If consecutive_misses >= 3:
        UPDATE fleet_shards SET status = 'unavailable'
        WHERE id = <shard_id>
        -- Emit alert to on-call channel
        -- Stop routing new intents to this shard
```

#### 5.4.2 Failure Scenarios and Responses

| Scenario | Detection | Response |
|---|---|---|
| Shard API server temporarily unreachable | 3 consecutive missed heartbeats (90s) | Shard marked `unavailable`. New cluster intents routed to other active shards. Existing tenant clusters on the shard continue running unaffected — CAPI is asynchronous. |
| Shard network isolation | Heartbeat misses + OTel signals stop arriving from shard's clusters | Shard marked `unavailable`. Fleet State Engine marks all clusters on the shard as `status_unknown`. Alert fires. |
| Shard recovery | Successful heartbeat after `unavailable` period | Shard marked `active` after 3 consecutive successful heartbeats. New intents resume routing to shard. |
| Shard DB corruption | CAPI objects unrecoverable | **Explicitly out of scope for automated recovery.** Requires manual operator intervention. Platform Admin uses `zero-ops mgmt shard-restore` CLI (Phase 6). Clusters continue running — only CAPI lifecycle management is affected. |

**Blast radius note (unchanged from v5.0):** Tenant clusters continue running if their managing shard goes down. CAPI only manages cluster lifecycle (create/scale/delete) — it does not participate in data-plane operations. A shard outage means no new lifecycle operations for clusters on that shard, not a workload outage.

### 5.5 Catalog Delivery — OCI Artifact Store

#### 5.5.1 The Problem with Direct SaaS API Pulling

The v5.0 design had ArgoCD in tenant clusters pulling catalog manifests directly from the live `zero-ops-api`. This creates a hard dependency: if `zero-ops-api` is unavailable during ArgoCD's reconciliation window, the sync fails. At scale with thousands of clusters reconciling on different schedules, this creates a persistent background error rate.

#### 5.5.2 OCI Artifact Store Model

```
Catalog change workflow:

Platform Admin / GitOpsAgent updates catalog
    │
zero-ops-api writes versioned artifact bundle
    │  OCI push
OCI Artifact Store  (GHCR / Harbor / any OCI-compatible registry)
    │  tagged by: tenant-class + version
    │  e.g.: catalog/hetzner-prod:v1.4.2
    │
ArgoCD in tenant cluster pulls from OCI store
    │  on reconciliation schedule (default: 5 min)
    │  NOT from live zero-ops-api
```

**Properties this provides:**
- **Decoupled availability:** ArgoCD can reconcile even if `zero-ops-api` is down.
- **Version history:** Every catalog change is a new OCI tag. Rollback is `docker tag` equivalent.
- **Audit trail:** OCI push events are logged with actor, timestamp, and digest.
- **Drift detection:** ArgoCD compares running state against the OCI artifact digest — not a moving API target.

**Tenant-specific customization:**
The SaaS API generates per-tenant OCI artifacts at provision time that include tenant-specific configuration overlays (Hetzner region, node class, addon selections). The base catalog is a shared OCI artifact; tenant artifacts use OCI artifact composition (ORAS) to layer overrides on top.

### 5.6 Edge GitOps Bootstrap — ClusterResourceSet (Updated)

The `ClusterResourceSet` now injects four components:

| Component | Resource | Purpose |
|---|---|---|
| ArgoCD (edge build) | `argocd-edge.yaml` | Day-2 addon reconciliation from OCI catalog |
| OTel Collector | `otel-collector.yaml` | Aggregated telemetry pipeline to NATS JetStream |
| fleet-heartbeat | `fleet-heartbeat.yaml` | 60s liveness CronJob for silent failure detection |
| mTLS client cert | Generated at provision time | Authenticates OTel Collector and ArgoCD to platform services |

### 5.7 Security, Compliance & Reliability

- **Blast Radius:** If Fleet Shard A goes down, Shards B and C are unaffected. Tenant workloads continue running.
- **Agent RBAC:** All agent Kubernetes API calls are made through RBAC-scoped service accounts. Agents cannot exceed the permissions of their assigned service account regardless of what an LLM produces.
- **MCP Authorization:** Each MCP tool server validates the calling agent's identity and scope before accepting a call. `capi-mcp` rejects calls for shards the requesting agent doesn't have permission for.
- **LLM Prompt Injection:** The LLM Gateway validates that all LLM responses are structured JSON matching the expected intent schema before passing them to the execution layer. Free-form text responses from LLMs are never executed directly.
- **Fleet State Engine Failure:** Provisioning path unaffected. Collaborator Agent falls back to static DB capacity with a logged warning.
- **NATS JetStream Failure:** OTel Collectors buffer locally for up to 5 minutes (configurable). Messages are replayed on NATS recovery. Fleet State Engine marks clusters as `status_unknown` after 10 minutes of no signal.
- **Talos Security:** Immutable OS, no SSH, API access via mTLS on port 50000 only.
- **Token Isolation:** Hetzner tokens written directly to Kubernetes Secrets in Fleet Shards. Never logged or stored in plain text in PostgreSQL.
- **Audit Completeness:** Every agent action, LLM call, approval, and execution outcome is written to `agent_audit_log`. This table is append-only (no UPDATE/DELETE permissions granted to application roles).

---

## 6. Agentic Workflow Deep Dives

### 6.1 Automated Fleet Upgrade _(UpgradeAgent)_

1. vmalert recording rule detects addon version drift. Alert fires to `zero-ops-api` via Alertmanager webhook.
2. **Collaborator** routes to **UpgradeAgent**.
3. UpgradeAgent queries VictoriaMetrics via `victoriametrics-mcp`:
   ```promql
   count by (cluster_id) (fleet_addon_version{addon="cilium"} < 1.15)
   ```
4. Collaborator batches: 10 clusters per hour to avoid simultaneous disruption.
5. UpgradeAgent calls `argocd-mcp`: update catalog reference for target cluster to `catalog/hetzner-prod:v1.5.0` (Cilium 1.15).
6. SaaS API pushes updated OCI artifact. Catalog push event written to OpenSearch `catalog-pushes` index.
7. ArgoCD syncs on next reconciliation. Sync completion event written to OpenSearch `argocd-syncs` index.
8. VictoriaMetrics tracks `fleet_addon_version` metric updates per cluster. UpgradeAgent confirms completion.
9. Full audit trail in OpenSearch: catalog push → ArgoCD sync → metric update → completion — all queryable by ReportAgent.

### 6.2 Proactive Anomaly Detection _(DiagnosticsAgent)_

| Signal Pattern | Detection | Agent Response |
|---|---|---|
| No heartbeat for 5m | NATS: no message on `heartbeat.<cluster_id>` subject | Collaborator routes to DiagnosticsAgent. Cluster marked `degraded`. Halt new scheduling. Page on-call. |
| CPU > 85% for 10m | vmalert rule fires → Alertmanager webhook → zero-ops-api | Collaborator queries OpenSearch: any infra changes in last 30m? If yes: correlate. If no: route to LifecycleAgent to scale. |
| 3+ clusters same region degraded simultaneously | VictoriaMetrics: `count(health_status="degraded") by (region) > 3` | DiagnosticsAgent queries OpenSearch: find change events in that region in preceding 30m. High probability of correlating catalog push or shard event. Collaborator triggers regional investigation before any remediation. |
| DiskPressure on node | K8sGPT Result CRD → OpenSearch `k8sgpt-findings` | DiagnosticsAgent queries OpenSearch event timeline for context. Queries runbook RAG. Identifies node drain. Triggers human approval workflow. |
| Addon drift > 2 minor versions | VictoriaMetrics: `fleet_addon_version{addon="cilium"} < 1.13` | UpgradeAgent queries VictoriaMetrics for affected cluster list. Queues rolling upgrade intent with rollout window. |
| Recent catalog push correlated with degradation | OpenSearch: alert-firing timestamp within 15m of catalog-push timestamp, same region | DiagnosticsAgent escalates to GitOpsAgent for rollback investigation before any scale/drain action. |

### 6.3 Cost Optimization Agent _(ReportAgent + MetricsAgent)_

- MetricsAgent queries VictoriaMetrics via `victoriametrics-mcp`: clusters with CPU < 20% sustained for 7 days using `avg_over_time(cpu_usage[7d])`.
- ReportAgent cross-references Hetzner VM SKU costs against actual utilization.
- Collaborator classifies right-sizing recommendation as `cautious-mutate` — configurable approval per tenant policy.
- Cost efficiency score per tenant written to PostgreSQL, consumed by `zero-ops-worker` billing engine.

### 6.4 On-Call Report Generation _(ReportAgent)_

Triggered by Slack mention or scheduled cron:

1. ReportAgent queries OpenSearch via `opensearch-mcp` for the specified time window across all event indexes: alert firings, infra changes, K8sGPT findings, ArgoCD syncs.
2. Correlates with `agent_audit_log` (PostgreSQL): agent actions, approvals, outcomes, workflow run IDs.
3. Generates structured report: alert count by severity and region, MTTR per incident, change events that correlated with degradations, remediation actions taken, open unresolved issues.
4. Posts summary and document link to platform console thread.

---

## 7. Success Criteria & Acceptance Tests

### 7.1 Architecture Verification

| Test ID | Description | Pass Condition | New in v7.0? |
|---|---|---|---|
| A-01 | Decoupled State | Stopping `zero-ops-api` DB does NOT crash tenant clusters or Fleet Shards | No |
| A-02 | Shard Dispatch | Two consecutive cluster creates route to different shards when round-robin configured | No |
| A-03 | Edge GitOps | ArgoCD running in Tenant Cluster. Fleet Shard does NOT run ArgoCD for tenant workloads | No |
| A-04 | Alloy Ingest | `fleet_current_state` row populated within 30s of cluster boot. VictoriaMetrics receiving metrics within 60s. | No (updated) |
| A-05 | State Engine Fallback | Stopping VictoriaMetrics does NOT block provisioning. Planner uses static PostgreSQL capacity. | No (updated) |
| A-06 | Policy Trigger | vmalert rule fires and Alertmanager webhook reaches `zero-ops-api` within 60s of metric crossing threshold | No (updated) |
| A-07 | Heartbeat Detection | Cluster with stopped Alloy marked `degraded` in `fleet_current_state` within 10 minutes | No |
| A-08 | NATS Event Bus | Stopping Collaborator Agent for 3 minutes then restarting results in zero policy event loss from NATS | No (updated) |
| A-09 | Shard Failure Detection | Stopping a shard's API server results in `status = unavailable` in `fleet_shards` within 90s. New intents stop routing to it. | No |
| A-10 | Shard Recovery | Restarting a shard's API server results in `status = active` after 3 successful heartbeats. Intents resume routing. | No |
| A-11 | OCI Catalog | ArgoCD syncs successfully when `zero-ops-api` is stopped. Confirms no control-plane dependency during reconciliation. | No |
| A-12 | Human Approval Gate | Destructive intent (node drain) pauses Argo Workflow and creates CNPG ticket in platform console. Approval resumes workflow. Rejection cancels and logs. | No |
| A-13 | Guardrail Enforcement | Submitting 6 scale intents for the same cluster within 1 hour results in the 6th being rejected by Policy Gate with rate limit error. | No |
| A-14 | MCP Authorization | Calling `capi-mcp` with a MetricsAgent identity (wrong scope) returns 403. Call with LifecycleAgent identity succeeds. | No |
| A-15 | LLM Fallback | Stopping primary LLM backend results in Collaborator routing to secondary. If both unavailable, falls back to policy templates. | No |
| A-16 | Audit Completeness | Every agent action in a test scenario has a corresponding row in `agent_audit_log` with correct outcome and workflow run ID. | No |
| A-17 | Regional Blast Radius Query | Stopping 5 clusters in same region produces a VictoriaMetrics query result grouping all 5 by `region` label. DiagnosticsAgent identifies regional scope within 2 LLM reasoning steps. | **YES** |
| A-18 | Change Correlation | Pushing a catalog update then simulating 3 cluster degradations within 15 minutes results in DiagnosticsAgent identifying the catalog push as the correlated change event via OpenSearch query. | **YES** |
| A-19 | OpenSearch Event Timeline | A full cluster incident (alert → agent action → approval → execution → resolution) produces a complete ordered event timeline queryable from OpenSearch across all 5 event indexes. | **YES** |
| A-20 | Topology Label Propagation | All metrics from a tenant cluster in region `eu-central-1` carry the label `region=eu-central-1` in VictoriaMetrics. Label is injected by Alloy from cluster metadata — not scraped from the cluster. | **YES** |
| A-21 | K8sGPT Fleet-Wide Findings | K8sGPT finding on cluster-X appears in OpenSearch `k8sgpt-findings` index within 60s. DiagnosticsAgent can query "all K8sGPT findings in region eu-central-1 in the last hour" and get findings across all clusters in that region. | **YES** |

### 7.2 Functional Verification

- **Thin Client:** `zero-ops cluster create` with Wireshark confirms traffic goes only to `zero-ops-api` REST endpoint.
- **Talos Exclusivity:** No SSH keys requested during provisioning. Node access via `talosctl` only.
- **Multi-Agent Routing:** An intent involving both metrics analysis and scaling triggers both MetricsAgent (`victoriametrics-mcp`) and LifecycleAgent (`capi-mcp`) calls, each logged separately in `agent_audit_log`.
- **Runbook Grounding:** DiagnosticsAgent for a `DiskPressure` signal produces a remediation plan that cites a specific runbook chunk — not a hallucinated procedure.
- **Change Correlation:** DiagnosticsAgent for any alert queries OpenSearch event timeline before querying the runbook corpus — change correlation precedes remediation reasoning.
- **Regional Grouping:** MetricsAgent correctly identifies that 5 simultaneously degraded clusters share `region=eu-central-1` in a single VictoriaMetrics query, triggering regional investigation mode rather than per-cluster remediation.

---

## 8. Monorepo Structure

```
zero-ops/
├── cmd/
│   ├── zero-ops/                    # Thin CLI (REST API only)
│   ├── zero-ops-api/                # SaaS Control Plane
│   ├── zero-ops-worker/             # Async billing & polling
│   └── zero-ops-agent/              # Multi-Agent Collaboration System
│
├── pkg/
│   ├── api/                         # REST API handlers & routing
│   ├── planner/                     # Intent → CAPI YAML translation
│   ├── sharding/                    # Fleet Shard dispatch + health checker
│   ├── fleetstate/                  # Fleet routing metadata (PostgreSQL only)
│   │   └── query.go                 # fleet_current_state routing queries
│   ├── policies/                    # Policy evaluation engine (vmalert rules config)
│   ├── agents/                      # Multi-agent system
│   │   ├── collaborator/            # Collaborator Agent (orchestrator)
│   │   ├── workers/
│   │   │   ├── metrics_agent.go     # MetricsAgent → victoriametrics-mcp
│   │   │   ├── lifecycle_agent.go   # LifecycleAgent → capi-mcp
│   │   │   ├── gitops_agent.go      # GitOpsAgent → argocd-mcp
│   │   │   ├── diagnostics_agent.go # DiagnosticsAgent → opensearch-mcp + k8sgpt-mcp + RAG
│   │   │   ├── upgrade_agent.go     # UpgradeAgent → victoriametrics-mcp + argocd-mcp
│   │   │   └── report_agent.go      # ReportAgent → opensearch-mcp + audit-mcp
│   │   └── mcp/                     # MCP tool server implementations
│   │       ├── fleet_state_mcp.go   # PostgreSQL routing metadata queries
│   │       ├── victoriametrics_mcp.go  # PromQL wrapper → VictoriaMetrics vmselect  [NEW v7.0]
│   │       ├── opensearch_mcp.go    # Event timeline + correlation queries  [NEW v7.0]
│   │       ├── capi_mcp.go
│   │       ├── argocd_mcp.go
│   │       ├── k8sgpt_mcp.go
│   │       └── audit_mcp.go
│   ├── llmgateway/                  # LLM Gateway + pluggable backends
│   ├── rag/                         # Runbook embedding + retrieval
│   ├── execution/                   # Safe Execution Layer
│   │   ├── policy_gate.go           # Rate limits, concurrency, quotas
│   │   ├── workflows.go             # Argo Workflow submission + OpenSearch change event hook
│   │   └── approval.go              # CNPG ticket creation and platform console integration
│   └── db/                          # PostgreSQL sqlc definitions
│
├── manifests/
│   ├── shards/                      # Fleet Shard bootstrap manifests
│   │   ├── capi-core.yaml
│   │   └── caph-provider.yaml
│   └── classes/                     # CAPI Topologies (Talos only)
│       └── hetzner-prod-talos-v1.yaml
│
├── argo-workflows/                   # Safe Execution workflow templates
│   ├── scale-workers.yaml
│   ├── safe-node-drain.yaml
│   ├── safe-pod-restart.yaml
│   ├── addon-upgrade.yaml
│   └── cluster-delete.yaml
│
├── edge-catalog/                     # Injected into tenant clusters via CRS
│   ├── argocd-edge.yaml              # ArgoCD (OCI catalog mode)
│   ├── grafana-alloy.yaml            # Alloy collector → VictoriaMetrics  [NEW v7.0]
│   ├── kube-events-exporter.yaml     # K8s events → OpenSearch  [NEW v7.0]
│   ├── k8sgpt-operator.yaml          # Per-cluster AI diagnostics → OpenSearch [NEW v7.0]
│   ├── k8sgpt-result-exporter.yaml   # K8sGPT Result CRD → OpenSearch  [NEW v7.0]
│   ├── fleet-heartbeat.yaml          # Liveness CronJob → NATS
│   ├── cilium.yaml
│   └── ccm-csi.yaml
│
├── observability/                    # Fleet Observability Stack config  [NEW v7.0]
│   ├── victoriametrics/
│   │   ├── vmcluster-values.yaml     # vmcluster Helm values
│   │   ├── vmalert-rules.yaml        # Fleet-wide alert and recording rules
│   │   └── alertmanager-config.yaml  # Alertmanager → zero-ops-api webhook
│   └── opensearch/
│       ├── opensearch-values.yaml    # OpenSearch cluster Helm values
│       └── index-templates/          # Index mappings for all event types
│           ├── k8s-events.json
│           ├── k8sgpt-findings.json
│           ├── argocd-syncs.json
│           ├── infra-changes.json
│           └── catalog-pushes.json
│
├── go.mod
└── Makefile
```

---

## 9. Implementation Phases

| Phase | Name | Key Deliverables | Status |
|---|---|---|---|
| 1 | SaaS Control Plane & API | PostgreSQL schema (updated v7.0 — routing metadata only), REST API CRUD, intent payload parsing, tenant management | **Next** |
| 2 | Fleet Shard Bootstrap | `zero-ops mgmt bootstrap` CLI, CAPI/CAPH/Talos providers, shard registration, shard health checker | Planned |
| 3 | CAPI Dispatch & Edge GitOps | `ClusterClass` hydration, CAPI dispatch, `ClusterResourceSet` with ArgoCD + Grafana Alloy + kube-events-exporter + K8sGPT Operator + heartbeat injection, OCI catalog delivery | Planned |
| 4 | Fleet Observability Stack | VictoriaMetrics vmcluster deployment, Alloy central config, vmalert rules, Alertmanager webhook to zero-ops-api, OpenSearch cluster, index templates, K8sGPT result exporter | Planned |
| 5 | Safe Execution Layer | Policy Gate, Argo Workflow templates (with OpenSearch change event hooks), CNPG ticket integration with platform console, `agent_audit_log` | Planned |
| 6 | MCP Tool Servers | `victoriametrics-mcp`, `opensearch-mcp`, `fleet-state-mcp`, `capi-mcp`, `argocd-mcp`, `k8sgpt-mcp`, `audit-mcp` — all with authorization | Planned |
| **7** | **Multi-Agent Collaboration** | LLM Gateway, Collaborator Agent, Worker Agents (all 6), Runbook RAG, end-to-end agent test suite (A-17 through A-21) | Planned |

> **SEQUENCING NOTE:** Phase 4 (Fleet Observability Stack) must complete before Phase 6 (MCP Tool Servers). MCP wrappers cannot be built or tested without the underlying stores running. Phase 5 (Safe Execution Layer) must complete before Phase 7 (Multi-Agent). Agents must have a safety-bounded execution environment before connecting to live infrastructure.

### 9.1 Phase 4 Milestones — Fleet Observability Stack Breakdown

| Milestone | Deliverable | Validates |
|---|---|---|
| 4a — VictoriaMetrics | vmcluster deployed. vmagent in one test tenant cluster forwarding metrics with topology labels. PromQL queries returning correctly labelled results. | A-04, A-20 |
| 4b — vmalert + Alertmanager | Alert rules deployed. Test alert fires and reaches `zero-ops-api` webhook within 60s. | A-06 |
| 4c — OpenSearch | OpenSearch cluster deployed. Index templates created for all 5 event types. | A-19 |
| 4d — kube-events-exporter | Deployed in test cluster. Kubernetes events appear in OpenSearch `k8s-events` index within 60s. | A-19 |
| 4e — K8sGPT fleet export | K8sGPT `Result` CRDs appear in OpenSearch `k8sgpt-findings` index. Query across multiple test clusters returns merged results. | A-21 |
| 4f — Argo + ArgoCD hooks | Argo Workflow completion events and ArgoCD sync events appear in OpenSearch. Change event timestamps enable correlation queries. | A-18 |

### 9.2 Phase 7 Milestones — Multi-Agent Collaboration Breakdown

| Milestone | Deliverable | Validates |
|---|---|---|
| 7a — LLM Gateway | Pluggable LLM backend proxy. Rate limiting. Audit logging of all LLM calls. Fallback chain. | A-15 |
| 7b — MCP Tool Servers | Implement all 7 MCP servers with authorization. Unit test each in isolation. | A-14 |
| 7c — Worker Agents | Implement all 6 Worker Agents. Each tested against mock MCP tool servers. | A-16 |
| 7d — Collaborator Agent | Implement Collaborator with routing logic, VictoriaMetrics + OpenSearch context injection, RAG integration. | A-12, A-13 |
| 7e — Runbook RAG | Build embedding pipeline. Ingest initial runbook corpus (10 runbooks minimum). Validate DiagnosticsAgent cites runbooks. | Functional test: runbook grounding |
| 7f — Correlation Logic | DiagnosticsAgent queries OpenSearch event timeline before runbook RAG for every alert. Validate A-17 (regional blast radius) and A-18 (change correlation) pass. | A-17, A-18 |
| 7g — Integration | End-to-end test: alert → Collaborator → Worker → Argo Workflow → Slack approval → execution → OpenSearch event → audit log. Run at 100-cluster simulation. | All A-08 through A-21 |

---

## 10. Architectural Position

The Zero-Ops Platform now sits at the intersection of two categories:

```
Infrastructure Fleet Platforms         Developer Platforms
(Rancher, Platform9)                   (Radius, Backstage)
        │                                      │
        │                                      │
        └──────────────┬───────────────────────┘
                       │
              Fleet Intelligence Platform
                   (Zero-Ops v7.0)
```

**What separates Zero-Ops from fleet platforms:** Adopted open source observability stack (Grafana Alloy + VictoriaMetrics + OpenSearch) gives agents genuine cross-cluster correlation capability. Multi-agent system with runbook-grounded reasoning and event timeline context. Safe execution layer that enables autonomous operations without human supervision for routine changes.

**What separates Zero-Ops from developer platforms:** Direct infrastructure control via CAPI and CAPH. BYOC model with tenant isolation. Talos-only immutable OS enforcement. Sharded architecture designed for 10k+ clusters.

**The build vs. adopt principle (v7.0):** Zero-Ops does not build what the industry has already solved. Grafana Alloy, VictoriaMetrics, OpenSearch, K8sGPT, NATS JetStream, Argo Workflows — these are all production-proven, CNCF-aligned tools used at scale by operators managing 1,000+ clusters. Custom code is reserved for the thin MCP integration layer and the agent reasoning logic itself. This is the engineering strategy that makes the platform viable without a 50-person platform team.

**The architectural moat:** No incumbent platform combines fleet-scale infrastructure control with a production-grade multi-agent collaboration system, adopted open source observability stack, and safe execution layer. The correlation capability (regional blast radius detection, change-to-incident correlation) is what elevates Zero-Ops from a provisioning tool to an operational intelligence platform.

---

*Document Status: APPROVED — Next Phase: Database Schema & API Implementation (Phase 1)*  
*Previous version: v6.0 (Multi-Agent Collaboration) — see changelog at top of document*

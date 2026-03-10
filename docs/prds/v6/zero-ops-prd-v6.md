# Zero-Ops Platform — Product Requirements Document

**Version:** 6.0 — Multi-Agent Collaboration & Production-Grade Gaps  
**Status:** APPROVED  
**Project:** zero-ops  
**Repo Model:** Go-Centric Monorepo  
**Author:** Platform Architecture Team  
**Date:** 2026  
**Classification:** CONFIDENTIAL — INTERNAL USE ONLY

---

## Changelog: v5.0 → v6.0

| Area | v5.0 State | v6.0 Change |
|---|---|---|
| Agent architecture | Single monolithic Agent Planner | Multi-agent hierarchy: Collaborator → Manager → specialist Workers |
| Agent-tool interface | Direct API calls from planner | MCP (Model Context Protocol) standardized tool interface |
| Safe execution | Policy Gate middleware (conceptual) | Argo Workflows as execution layer with built-in guardrails |
| Human approval | Not specified | Slack-based approval webhook for destructive intents |
| Runbook reasoning | Hard-coded policy templates | RAG over operational runbook corpus |
| Telemetry pipeline | OTel → PostgreSQL (direct) | OTel → NATS JetStream → State Processor → PostgreSQL |
| Fleet State DB model | Implied relational | Explicitly PostgreSQL-first with documented rationale |
| Shard failure handling | Blast-radius note only | Shard health registry + heartbeat + unavailability routing |
| Catalog delivery | ArgoCD → live SaaS API | ArgoCD → OCI artifact store (SaaS API writes on change) |
| Agent safety | Not specified | Concrete guardrails: rate limits, concurrency caps, PDB respect, dry-run mode |

---

## 1. Executive Summary

The Zero-Ops Platform is a next-generation, intent-driven Kubernetes SaaS designed to scale to tens of thousands of clusters. Version 6.0 resolves the five production-grade architectural gaps identified in v5.0 and introduces a **Multi-Agent Collaboration System** — replacing the single monolithic Agent Planner with a hierarchical, specialized, safety-bounded multi-agent architecture.

The platform is now built on four architectural pillars:

- **Agentic Control Plane** — `zero-ops-api` acts as the coordination layer, routing intents to a sharded fleet of Management Clusters backed by PostgreSQL state.
- **Edge GitOps** — ArgoCD deployed directly into tenant clusters via `ClusterResourceSet`, pulling from an OCI artifact store rather than the live SaaS API.
- **Fleet State Engine** — A continuously updated fleet-wide state model fed by a NATS JetStream telemetry pipeline, giving agents live observability across every cluster.
- **Multi-Agent Collaboration System _(NEW v6.0)_** — A Collaborator/Manager/Worker agent hierarchy where a central orchestrator reasons over Fleet State and runbook context, dispatches to specialist sub-agents via MCP, and executes infrastructure changes safely through Argo Workflows with optional human approval gates.

> **v6.0 KEY SHIFT:** The platform moves from a single planning loop to a production-grade agentic system with specialization, safety boundaries, human-in-the-loop controls, and runbook-grounded reasoning. This is the architecture that makes autonomous infrastructure operations viable at scale without causing infrastructure storms.

---

## 2. Problem Statement

### 2.1 The GitOps Bottleneck _(Resolved in v4.0)_

Traditional platforms ran CAPI and a central ArgoCD instance watching thousands of remote clusters from a single Management Cluster. Kubernetes etcd degrades past ~500 clusters. Resolved in v4.0 via Fleet Sharding and Edge GitOps.

### 2.2 The Observation Gap _(Resolved in v5.0)_

The v4.0 Agentic Planner could only query PostgreSQL for static shard capacity data. No live signal from running clusters. Resolved in v5.0 via the Fleet State Engine.

### 2.3 Five Production-Grade Gaps _(NEW — Resolved in v6.0)_

v5.0 introduced the correct concepts but left five implementation gaps that would cause failures in a real production deployment:

| Gap | Risk if Unresolved |
|---|---|
| **G1: Telemetry pipeline undefined** | Direct OTel→PostgreSQL at 10k clusters causes ingestion overload and data loss under spike conditions |
| **G2: Fleet State DB model unspecified** | Ambiguity leads to over-engineering (graph DB) or under-engineering (no indexing strategy) |
| **G3: Agent safety boundaries missing** | Autonomous agents with unrestricted Kubernetes API access can trigger infrastructure storms — cascading failures across the fleet |
| **G4: Shard failure handling absent** | When a shard's Kubernetes API becomes unreachable, `zero-ops-api` has no detection or routing mechanism, causing provisioning failures silently |
| **G5: Catalog control-plane dependency** | ArgoCD pulling from the live SaaS API creates a reconciliation failure whenever `zero-ops-api` is unavailable |

### 2.4 The Single-Agent Ceiling _(NEW — Resolved in v6.0)_

A monolithic Agent Planner is architecturally appropriate for v4/v5 but fails at production scale for three reasons. First, a single agent with access to all tools has an unbounded blast radius — one mistaken intent can affect the entire fleet. Second, a single reasoning loop cannot hold sufficient context for disparate domains (metrics analysis, GitOps reconciliation, cluster lifecycle, cost optimization) simultaneously. Third, a monolithic planner is not extensible — adding a new capability requires modifying the core planning logic rather than adding a new specialist.

### 2.5 Constraints & Non-Goals

- **API-First, not Git-First:** Primary orchestration interface is the REST API (`zero-ops-api`).
- **BYOC Model:** Tenants provide Hetzner API tokens. Compute runs in their accounts.
- **Immutable OS:** All clusters must use Talos Linux. No SSH. No Ubuntu.
- **Non-Goal:** Monolithic Management Cluster. Horizontal sharding of CAPI is mandatory from Day 1.
- **Non-Goal:** Raw metrics mirroring. The Fleet State Engine stores aggregated signals only — not Prometheus time-series.
- **Non-Goal:** Graph database for Fleet State. PostgreSQL with JSONB and proper indexing is the correct choice at current query complexity. The migration trigger to a graph model is documented in Section 5.2.
- **Non-Goal:** Fully autonomous remediation without human approval gates for destructive operations. Human-in-the-loop is a first-class feature, not a temporary limitation.

---

## 3. User Personas & User Journeys

### 3.1 Personas

| Persona | Role | Primary Interface |
|---|---|---|
| Platform Admin | Bootstraps Fleet Shards, maintains SaaS API, manages global catalog and runbook corpus | CLI + Admin API |
| Tenant Developer | Expresses infrastructure intent — e.g. "I need a prod cluster with Postgres" | REST API / CLI |
| Platform Agent (AI/Automation) | Programmatic actor interfacing with the Collaborator Agent to optimize, scale, or recover environments | Intent API / Webhooks |
| On-Call Engineer | Receives Slack approval requests for destructive operations; reviews agent-generated incident reports | Slack |
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

**Trigger:** Fleet State Engine detects skewed load distribution. Policy threshold fires.

1. `fleet_policies` evaluator detects `cluster-12` CPU 90% for 10 minutes. Emits event to NATS.
2. **Collaborator Agent** receives the event. Queries Fleet State for context: shard assignment, current node count, tenant quota headroom.
3. Collaborator routes to **LifecycleAgent** (Worker): "Scale cluster-12 workers to 5."
4. **LifecycleAgent** checks Policy Gate: rate limit OK, concurrency cap OK, quota check passes.
5. LifecycleAgent submits `scale_workers` Argo Workflow. Argo patches CAPI `Cluster` resource on the correct shard.
6. CAPH provisions new VMs. Nodes boot Talos, join cluster. Edge GitOps deploys DaemonSets.
7. Fleet State Engine confirms resolution. Collaborator closes the loop.

#### Journey D: Destructive Operation with Human Approval _(On-Call Engineer)_

**Trigger:** Fleet State Engine detects `DiskPressure` on a node. Diagnostics Agent identifies node drain as required remediation.

1. **DiagnosticsAgent** analyses `DiskPressure` signal. Queries runbook corpus via RAG: identifies node drain as correct remediation.
2. **Collaborator Agent** classifies the intent as `destructive` (node drain affects running workloads).
3. Argo Workflow pauses. Slack approval request fires to on-call channel with full context: node, cluster, shard, functional domain, requester.
4. On-call engineer reviews and clicks **"Yes, Drain Node"**.
5. Argo Workflow resumes. `Safe Node Recycle` workflow executes: respects PDB, cordons node, evicts pods gracefully, triggers replacement VM.
6. Fleet State Engine confirms node health restored. Change record written to audit log.

#### Journey E: Automated Fleet Upgrade _(Platform Agent)_

**Trigger:** Fleet State Engine detects 40% of clusters running Cilium 1.14. Policy mandates 1.15 within 30 days.

1. **UpgradeAgent** (Worker) queries `fleet_current_state`: identifies affected clusters.
2. Collaborator batches upgrade intents in rolling windows — 10 clusters per hour.
3. Each intent triggers ArgoCD sync on the target cluster's edge engine via updated OCI catalog artifact.
4. Fleet State Engine tracks `addon_versions` field as clusters confirm completion. Progress auditable in real time.

#### Journey F: On-Call Report Generation _(Platform Agent / Slack)_

**Trigger:** On-call engineer sends: `@AIAgent Generate the on-call report from 2026-03-09T22:00:00 to 2026-03-10T14:59:59`

1. **ReportAgent** (Worker) queries `cluster_state` time-series for the specified window.
2. Correlates health events, alert counts, and remediation actions from the audit log.
3. Generates structured on-call report document. Posts summary and document link to Slack thread.

---

## 4. Proposed Architecture

### 4.1 High-Level Architecture (v6.0)

```
ZERO-OPS v6.0 — FULL ARCHITECTURE

  ┌─────────────────────────────────────────────────────────────────┐
  │  CONTROL PLANE                                                  │
  │                                                                 │
  │  User / AI Agent / Slack                                        │
  │       │  REST Intent / Webhook                                  │
  │  zero-ops-api  ◄────────────────────────────────────────────┐  │
  │       │                                                      │  │
  │  ┌────▼──────────────────────────────────────────────────┐  │  │
  │  │  MULTI-AGENT COLLABORATION SYSTEM  (NEW v6.0)         │  │  │
  │  │                                                       │  │  │
  │  │  Collaborator Agent  (orchestrator + router)          │  │  │
  │  │       │  MCP                                          │  │  │
  │  │       ├── MetricsAgent    → Fleet State / OTel        │  │  │
  │  │       ├── LifecycleAgent  → CAPI / Fleet Shards       │  │  │
  │  │       ├── GitOpsAgent     → ArgoCD / OCI Catalog      │  │  │
  │  │       ├── DiagnosticsAgent→ K8sGPT / Runbook RAG      │  │  │
  │  │       ├── UpgradeAgent    → Addon version management  │  │  │
  │  │       └── ReportAgent     → Audit log / State history │  │  │
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
  │  │       │  optional pause                               │  │  │
  │  │  Slack Approval Webhook  (destructive ops only)       │  │  │
  │  │       │  approved                                     │  │  │
  │  │  Execution API  →  CAPI / kubectl (RBAC-scoped)   ────┘  │  │
  │  └───────────────────────────────────────────────────────┘  │  │
  │                                                              │  │
  │  PostgreSQL  (tenant state, billing, fleet state, audit)     │  │
  └─────────────────────────────────────────────────────────────┘  │
                                                                    │
  ┌─────────────────────────────────────────────────────────────┐  │
  │  FLEET LAYER                                                │  │
  │                                                             │  │
  │  Fleet Shard A / B / N  (CAPI + CAPH)                       │  │
  │       │  Provision Talos VMs + ClusterResourceSet            │  │
  │  Tenant Clusters  (Talos)                                   │  │
  │       ├── ArgoCD  →  OCI Artifact Store (catalog)           │  │
  │       ├── OTel Collector  →  NATS JetStream                 │  │
  │       └── fleet-heartbeat CronJob  →  NATS JetStream        │  │
  └───────────────────────────┬─────────────────────────────────┘  │
                              │  NATS JetStream                     │
  ┌───────────────────────────▼─────────────────────────────────┐  │
  │  FLEET STATE ENGINE                                         │  │
  │                                                             │  │
  │  State Processor  →  fleet_current_state (PostgreSQL)       │  │
  │  Compactor  →  cluster_state append-log                     │  │
  │  Policy Evaluator  →  fleet_policies rules                  │  ─┘
  └─────────────────────────────────────────────────────────────┘
```

### 4.2 Components & Responsibilities

| Component | Implementation | Responsibility |
|---|---|---|
| SaaS Control Plane | Go / Gin / PostgreSQL | Coordination layer. Tenant state, billing, intent routing, shard dispatch. |
| **Collaborator Agent** _(NEW)_ | Go + LLM Gateway | Orchestrator. Receives intents, reasons over Fleet State + runbook context, routes to specialist Workers via MCP. |
| **Worker Agents** _(NEW)_ | Go + MCP tool servers | Specialists: MetricsAgent, LifecycleAgent, GitOpsAgent, DiagnosticsAgent, UpgradeAgent, ReportAgent. Each has a narrow, bounded tool surface. |
| **LLM Gateway** _(NEW)_ | Go reverse proxy | Governance layer. Routes LLM calls to pluggable backends (OpenAI, Claude, Mistral). Enforces rate limits and audit logging on all LLM calls. |
| **Runbook RAG** _(NEW)_ | Go + embedding store | Vector-indexed runbook corpus. DiagnosticsAgent queries it to ground remediation reasoning in operational knowledge. |
| **Safe Execution Layer** _(NEW)_ | Policy Gate + Argo Workflows + Slack webhook | Guardrail enforcement. Agents submit workflows, not direct API calls. Destructive ops require human approval. |
| **NATS JetStream** _(NEW)_ | NATS server (embedded or managed) | Telemetry event backbone. Buffers OTel signals and heartbeats from edge clusters, providing backpressure protection. |
| Fleet State Engine | Go service + PostgreSQL | Maintains fleet-wide state from NATS-delivered telemetry. Source of truth for agent decisions. |
| OCI Artifact Store | OCI-compatible registry (e.g. GHCR, Harbor) | Versioned catalog artifacts. ArgoCD pulls from here, not the live SaaS API. |
| Fleet Shard | K8s / CAPI / CAPH | Dumb execution engine. Receives CAPI YAMLs, provisions Hetzner VMs via CAPH. |
| Edge GitOps | ArgoCD / Flux | Runs inside tenant cluster. Day-2 addon reconciliation against OCI catalog. |

---

## 5. Technical Specifications

### 5.1 Multi-Agent Collaboration System

#### 5.1.1 Agent Hierarchy

The system uses a three-tier hierarchy derived from production AIops patterns:

```
Tier 1 — Collaborator Agent  (1 instance)
    Receives all intents from zero-ops-api
    Maintains conversation/task context
    Reasons over Fleet State + RAG context
    Routes sub-tasks to Worker Agents via MCP
    Owns the human approval decision boundary

Tier 2 — Worker Agents  (N specialist instances)
    MetricsAgent    — queries Fleet State, interprets utilization signals
    LifecycleAgent  — cluster create/scale/delete via CAPI
    GitOpsAgent     — ArgoCD sync, catalog updates, rollbacks
    DiagnosticsAgent— K8sGPT analysis, runbook RAG queries
    UpgradeAgent    — addon version management, rolling upgrades
    ReportAgent     — on-call reports, audit summaries, cost analysis

Tier 3 — Tool Servers  (MCP-compatible)
    fleet-state-mcp    — read/query Fleet State Engine
    capi-mcp           — submit CAPI manifests to Fleet Shards
    argocd-mcp         — trigger ArgoCD syncs via ArgoCD API
    k8sgpt-mcp         — invoke K8sGPT analysis on target cluster
    audit-mcp          — write to and query the audit log
```

#### 5.1.2 MCP as the Agent-Tool Interface

All Worker Agents communicate with platform capabilities exclusively via MCP (Model Context Protocol) tool servers. This is a non-negotiable architectural constraint.

**Why MCP:**
- Adding a new platform capability means adding a new MCP tool server — the Collaborator Agent does not need to be modified.
- MCP tool calls are logged by the LLM Gateway, creating a complete audit trail of every action an agent takes.
- Tool servers enforce their own authorization: `capi-mcp` validates that the requesting agent has permission for the target shard before accepting a call.
- RBAC is enforced at the tool server level, not at the agent level — agents cannot bypass it by calling Kubernetes APIs directly.

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

### 5.2 Fleet State Engine

#### 5.2.1 Data Pipeline — NATS JetStream Architecture

The v5.0 direct OTel → PostgreSQL path is replaced with a buffered pipeline:

```
Tenant Cluster
    │
OTel Collector (DaemonSet)          ← aggregated signals only, 15s flush
fleet-heartbeat CronJob             ← liveness ping, 60s interval
    │  OTLP/gRPC + mTLS
NATS JetStream  (event backbone)
    │  consumer group: state-processor
State Processor  (zero-ops-state service)
    │  upsert
fleet_current_state  (PostgreSQL)
    │  append
cluster_state  (PostgreSQL, compacted hourly)
```

**Why NATS JetStream over Kafka:**
Kafka adds Zookeeper/KRaft, broker cluster management, and consumer group coordination overhead that is disproportionate to the problem. NATS JetStream is embeddable, persistent, supports at-least-once delivery, and handles the fan-in pattern from 10k clusters with significantly lower operational burden. The migration path to Kafka exists if throughput requirements outgrow NATS — the State Processor is the only component that needs updating.

**Backpressure handling:**
- NATS JetStream applies per-subject flow control. If the State Processor falls behind, NATS buffers messages in its persistent log rather than dropping them or blocking the OTel Collectors.
- State Processor uses a configurable batch size (default: 500 upserts per transaction) to maximize PostgreSQL write throughput.
- At 10,000 clusters × 15s flush: ~667 messages/second sustained. NATS JetStream handles this trivially. The State Processor upserts to `fleet_current_state` in batches, targeting < 50ms p99 write latency.

**Shard-aware ingestion:**
Each OTel Collector tags all messages with `cluster_id` and `shard_id` labels. The State Processor uses `shard_id` to partition its processing workers, ensuring that a spike from one shard does not starve processing for another.

#### 5.2.2 Fleet State DB Model — PostgreSQL Rationale

The Fleet State Engine uses PostgreSQL. This is a deliberate, documented choice:

**Why not a graph database:**
The relationship queries that would justify a graph DB — `cluster → nodes → workloads → dependencies` — are not present in the current query surface. Fleet State queries are aggregate in nature: "give me all clusters on shard A with CPU > 80%", "what is the fleet-wide median addon version for cilium?". These are trivially served by indexed PostgreSQL queries. A graph DB would add operational complexity (separate deployment, separate backup/restore, separate query language) with no query expressiveness benefit at current requirements.

**Migration trigger:** A graph DB becomes justified when the platform needs to answer questions about workload placement dependencies across clusters — e.g. "which clusters have workloads that depend on services in cluster-12?" Until that query pattern exists, PostgreSQL is correct.

**Schema:**

```sql
-- Append-only telemetry log
CREATE TABLE cluster_state (
    cluster_id        UUID REFERENCES clusters(id),
    observed_at       TIMESTAMPTZ DEFAULT now(),
    cpu_utilization   NUMERIC(5,2),
    mem_utilization   NUMERIC(5,2),
    node_count        INT,
    node_ready_count  INT,
    health_status     VARCHAR(20),  -- healthy | degraded | critical
    PRIMARY KEY (cluster_id, observed_at)
);
CREATE INDEX idx_cluster_state_recent
    ON cluster_state (cluster_id, observed_at DESC);

-- Current state — upserted by State Processor
CREATE TABLE fleet_current_state (
    cluster_id        UUID PRIMARY KEY REFERENCES clusters(id),
    last_seen         TIMESTAMPTZ,
    cpu_utilization   NUMERIC(5,2),
    mem_utilization   NUMERIC(5,2),
    node_count        INT,
    health_status     VARCHAR(20),
    addon_versions    JSONB,        -- {"cilium": "1.15.0", "argocd": "2.9.0"}
    workload_count    INT,
    shard_id          UUID REFERENCES fleet_shards(id)
);
CREATE INDEX idx_fcs_shard_health
    ON fleet_current_state (shard_id, health_status);
CREATE INDEX idx_fcs_cpu
    ON fleet_current_state (cpu_utilization DESC)
    WHERE health_status = 'healthy';

-- Shard health registry
CREATE TABLE fleet_shards (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name             VARCHAR(63) UNIQUE NOT NULL,
    api_endpoint     VARCHAR(255) NOT NULL,
    capacity_weight  INT DEFAULT 100,
    status           VARCHAR(20) DEFAULT 'active',  -- active | unavailable | draining
    last_heartbeat   TIMESTAMPTZ,
    consecutive_misses INT DEFAULT 0
);

-- Automation policy rules
CREATE TABLE fleet_policies (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name             VARCHAR(63),
    trigger_metric   VARCHAR(63),
    threshold        NUMERIC(5,2),
    duration_minutes INT DEFAULT 10,
    intent_template  JSONB,
    requires_approval BOOLEAN DEFAULT false,
    enabled          BOOLEAN DEFAULT true
);

-- Audit log for all agent actions
CREATE TABLE agent_audit_log (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    created_at       TIMESTAMPTZ DEFAULT now(),
    agent_name       VARCHAR(63),
    intent_type      VARCHAR(63),
    cluster_id       UUID,
    payload          JSONB,
    approved_by      VARCHAR(255),  -- null if no approval required
    outcome          VARCHAR(20),   -- success | failed | rejected
    workflow_run_id  VARCHAR(255)   -- Argo Workflow run reference
);
```

#### 5.2.3 Schema Versioning

The NATS message schema for telemetry signals is versioned using a `schema_version` field in the message envelope. The State Processor maintains a version registry and applies the appropriate deserializer per message. Breaking schema changes require a version bump and a migration window where both old and new schema versions are accepted.

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

For intents classified as `destructive` (node drain, cluster delete, forced scaling down, config deletion), the Argo Workflow pauses and fires a Slack approval request:

```
Argo Workflow pauses at approval step
    │
Slack message posted to on-call channel:
    "Node Drain Confirmation Required
     Node:              ip-10-98-91-168.us-west-2.compute.internal
     Cluster:           prod-tenant-47
     Shard:             shard-eu-1
     Functional Domain: payments
     Requester:         DiagnosticsAgent (DiskPressure remediation)
     Runbook:           disk-pressure-remediation-v2
     [Yes, Drain Node]  [Cancel]"
    │
Engineer clicks approve  →  Argo Workflow resumes
Engineer clicks cancel   →  Workflow terminates, audit log records rejection
Timeout (30 min)         →  Workflow terminates, escalation alert fires
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

1. `fleet_policies` evaluator detects addon version drift. Emits event to NATS.
2. **Collaborator** routes to **UpgradeAgent**.
3. UpgradeAgent queries: `SELECT cluster_id FROM fleet_current_state WHERE addon_versions->>'cilium' < '1.15.0'`
4. Collaborator batches: 10 clusters per hour to avoid simultaneous disruption.
5. UpgradeAgent calls `argocd-mcp`: update catalog reference for target cluster to `catalog/hetzner-prod:v1.5.0` (which includes Cilium 1.15).
6. SaaS API pushes updated OCI artifact. ArgoCD syncs on next reconciliation.
7. Fleet State Engine tracks `addon_versions` updates. UpgradeAgent confirms completion per cluster.

### 6.2 Proactive Anomaly Detection _(DiagnosticsAgent)_

| Signal Pattern | Detection | Agent Response |
|---|---|---|
| No heartbeat for 5m | NATS: no message on `heartbeat.<cluster_id>` subject | Collaborator routes to DiagnosticsAgent. Cluster marked `degraded`. Halt new scheduling. Page on-call. |
| CPU > 85% for 10m | `fleet_policies` threshold fires | Collaborator routes to LifecycleAgent. Scale workers via Argo Workflow. |
| 3+ clusters on same shard degraded | Correlated `health_status` query | Collaborator triggers shard quarantine. Marks shard `draining`. Alerts Platform Admin. |
| DiskPressure on node | K8sGPT signal via DiagnosticsAgent | DiagnosticsAgent queries runbook RAG. Identifies node drain. Triggers human approval workflow. |
| Addon drift > 2 minor versions | `fleet_current_state` scan | UpgradeAgent queues rolling upgrade intent with rollout window. |

### 6.3 Cost Optimization Agent _(ReportAgent + MetricsAgent)_

- MetricsAgent identifies clusters with CPU < 20% sustained for 7 days from `cluster_state` history.
- ReportAgent cross-references Hetzner VM SKU costs against actual utilization.
- Collaborator classifies right-sizing recommendation as `cautious-mutate` — configurable approval per tenant policy.
- Cost efficiency score per tenant written to PostgreSQL, consumed by `zero-ops-worker` billing engine.

### 6.4 On-Call Report Generation _(ReportAgent)_

Triggered by Slack mention or scheduled cron:

1. ReportAgent queries `cluster_state` for the specified time window.
2. Joins with `agent_audit_log` to correlate alerts → agent actions → outcomes.
3. Generates structured report: alert count, MTTR, clusters affected, remediation actions taken, open issues.
4. Posts summary to Slack thread. Writes full report to document store. Links both in response.

---

## 7. Success Criteria & Acceptance Tests

### 7.1 Architecture Verification

| Test ID | Description | Pass Condition | New in v6.0? |
|---|---|---|---|
| A-01 | Decoupled State | Stopping `zero-ops-api` DB does NOT crash tenant clusters or Fleet Shards | No |
| A-02 | Shard Dispatch | Two consecutive cluster creates route to different shards when round-robin configured | No |
| A-03 | Edge GitOps | ArgoCD running in Tenant Cluster. Fleet Shard does NOT run ArgoCD for tenant workloads | No |
| A-04 | OTel Ingest | `fleet_current_state` row populated within 30s of cluster boot | No |
| A-05 | State Engine Fallback | Stopping Fleet State Engine does NOT block provisioning. Planner uses static capacity. | No |
| A-06 | Policy Trigger | `fleet_policies` threshold fires and emits intent within 60s of metric crossing threshold | No |
| A-07 | Heartbeat Detection | Cluster with stopped OTel collector marked `degraded` within 10 minutes | No |
| A-08 | NATS Buffering | Stopping State Processor for 3 minutes then restarting results in zero message loss; `fleet_current_state` fully reconciles | **YES** |
| A-09 | Shard Failure Detection | Stopping a shard's API server results in `status = unavailable` in `fleet_shards` within 90s. New intents stop routing to it. | **YES** |
| A-10 | Shard Recovery | Restarting a shard's API server results in `status = active` after 3 successful heartbeats. Intents resume routing. | **YES** |
| A-11 | OCI Catalog | ArgoCD syncs successfully when `zero-ops-api` is stopped. Confirms no control-plane dependency during reconciliation. | **YES** |
| A-12 | Human Approval Gate | Destructive intent (node drain) pauses Argo Workflow and posts Slack message. Approval resumes workflow. Rejection cancels and logs. | **YES** |
| A-13 | Guardrail Enforcement | Submitting 6 scale intents for the same cluster within 1 hour results in the 6th being rejected by Policy Gate with rate limit error. | **YES** |
| A-14 | MCP Authorization | Calling `capi-mcp` with a MetricsAgent identity (wrong scope) returns 403. Call with LifecycleAgent identity succeeds. | **YES** |
| A-15 | LLM Fallback | Stopping primary LLM backend results in Collaborator routing to secondary. If both unavailable, falls back to policy templates. | **YES** |
| A-16 | Audit Completeness | Every agent action in a test scenario has a corresponding row in `agent_audit_log` with correct outcome and workflow run ID. | **YES** |

### 7.2 Functional Verification

- **Thin Client:** `zero-ops cluster create` with Wireshark confirms traffic goes only to `zero-ops-api` REST endpoint.
- **Talos Exclusivity:** No SSH keys requested during provisioning. Node access via `talosctl` only.
- **Multi-Agent Routing:** An intent involving both metrics analysis and scaling triggers both MetricsAgent and LifecycleAgent calls, each logged separately in `agent_audit_log`.
- **Runbook Grounding:** DiagnosticsAgent for a `DiskPressure` signal produces a remediation plan that cites a specific runbook chunk — not a hallucinated procedure.

---

## 8. Monorepo Structure

```
zero-ops/
├── cmd/
│   ├── zero-ops/                    # Thin CLI (REST API only)
│   ├── zero-ops-api/                # SaaS Control Plane
│   ├── zero-ops-worker/             # Async billing & polling
│   ├── zero-ops-state/              # Fleet State Engine + NATS consumer
│   └── zero-ops-agent/              # Multi-Agent Collaboration System  [NEW]
│
├── pkg/
│   ├── api/                         # REST API handlers & routing
│   ├── planner/                     # Intent → CAPI YAML translation
│   ├── sharding/                    # Fleet Shard dispatch + health checker
│   ├── fleetstate/                  # State model, ingest, query, compactor
│   │   ├── ingest.go                # NATS consumer + upsert logic
│   │   ├── query.go                 # Fleet State query API
│   │   └── compactor.go             # Append-then-compact background job
│   ├── policies/                    # Policy evaluation engine
│   ├── agents/                      # Multi-agent system               [NEW]
│   │   ├── collaborator/            # Collaborator Agent (orchestrator)
│   │   ├── workers/
│   │   │   ├── metrics_agent.go     # MetricsAgent
│   │   │   ├── lifecycle_agent.go   # LifecycleAgent
│   │   │   ├── gitops_agent.go      # GitOpsAgent
│   │   │   ├── diagnostics_agent.go # DiagnosticsAgent + runbook RAG
│   │   │   ├── upgrade_agent.go     # UpgradeAgent
│   │   │   └── report_agent.go      # ReportAgent
│   │   └── mcp/                     # MCP tool server implementations  [NEW]
│   │       ├── fleet_state_mcp.go
│   │       ├── capi_mcp.go
│   │       ├── argocd_mcp.go
│   │       ├── k8sgpt_mcp.go
│   │       └── audit_mcp.go
│   ├── llmgateway/                  # LLM Gateway + pluggable backends  [NEW]
│   ├── rag/                         # Runbook embedding + retrieval     [NEW]
│   ├── execution/                   # Safe Execution Layer              [NEW]
│   │   ├── policy_gate.go           # Rate limits, concurrency, quotas
│   │   ├── workflows.go             # Argo Workflow submission
│   │   └── approval.go              # Slack approval webhook handler
│   └── db/                          # PostgreSQL sqlc definitions
│
├── manifests/
│   ├── shards/                      # Fleet Shard bootstrap manifests
│   │   ├── capi-core.yaml
│   │   └── caph-provider.yaml
│   └── classes/                     # CAPI Topologies (Talos only)
│       └── hetzner-prod-talos-v1.yaml
│
├── argo-workflows/                   # Safe Execution workflow templates [NEW]
│   ├── scale-workers.yaml
│   ├── safe-node-drain.yaml
│   ├── safe-pod-restart.yaml
│   ├── addon-upgrade.yaml
│   └── cluster-delete.yaml
│
├── edge-catalog/                     # Injected into tenant clusters via CRS
│   ├── argocd-edge.yaml              # ArgoCD (OCI catalog mode)
│   ├── otel-collector.yaml           # Telemetry → NATS JetStream
│   ├── fleet-heartbeat.yaml          # Liveness CronJob
│   ├── cilium.yaml
│   └── ccm-csi.yaml
│
├── go.mod
└── Makefile
```

---

## 9. Implementation Phases

| Phase | Name | Key Deliverables | Status |
|---|---|---|---|
| 1 | SaaS Control Plane & API | PostgreSQL schema, REST API CRUD, intent payload parsing, tenant management | **Next** |
| 2 | Fleet Shard Bootstrap | `zero-ops mgmt bootstrap` CLI, CAPI/CAPH/Talos providers, shard registration, shard health checker | Planned |
| 3 | CAPI Dispatch & Edge GitOps | `ClusterClass` hydration, CAPI dispatch, `ClusterResourceSet` with ArgoCD + OTel + heartbeat injection, OCI catalog delivery | Planned |
| 4 | Fleet State Engine | NATS JetStream setup, OTel ingest pipeline, `fleet_current_state` schema + compactor, `fleet_policies` engine | Planned |
| 5 | Safe Execution Layer | Policy Gate, Argo Workflow templates, Slack approval webhook, `agent_audit_log` | Planned |
| **6 _(NEW)_** | **Multi-Agent Collaboration** | LLM Gateway, Collaborator Agent, Worker Agents (all 6), MCP tool servers (all 5), Runbook RAG, end-to-end agent test suite | Planned |

> **SEQUENCING NOTE:** Phase 5 (Safe Execution Layer) must be complete before Phase 6 (Multi-Agent). Agents must have a safety-bounded execution environment before they are connected to live infrastructure. Building agents before guardrails is the fastest path to an infrastructure incident.

### 9.1 Phase 6 Milestones — Multi-Agent Collaboration Breakdown

| Milestone | Deliverable | Validates |
|---|---|---|
| 6a — LLM Gateway | Pluggable LLM backend proxy. Rate limiting. Audit logging of all LLM calls. Fallback chain. | A-15 |
| 6b — MCP Tool Servers | Implement all 5 MCP servers with authorization. Unit test each in isolation. | A-14 |
| 6c — Worker Agents | Implement all 6 Worker Agents. Each tested against mock MCP tool servers. | A-16 |
| 6d — Collaborator Agent | Implement Collaborator with routing logic, Fleet State context injection, RAG integration. | A-12, A-13 |
| 6e — Runbook RAG | Build embedding pipeline. Ingest initial runbook corpus (10 runbooks minimum). Validate DiagnosticsAgent cites runbooks. | Functional test: runbook grounding |
| 6f — Integration | End-to-end test: alert → Collaborator → Worker → Argo Workflow → Slack approval → execution → audit log. Run at 100-cluster simulation. | All A-08 through A-16 |

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
                   (Zero-Ops v6.0)
```

**What separates Zero-Ops from fleet platforms:** State-driven architecture with Fleet State Engine. Multi-agent system with runbook-grounded reasoning. Safe execution layer that enables autonomous operations without human supervision for routine changes.

**What separates Zero-Ops from developer platforms:** Direct infrastructure control via CAPI and CAPH. BYOC model with tenant isolation. Talos-only immutable OS enforcement. Sharded architecture designed for 10k+ clusters.

**The architectural moat:** No incumbent platform combines fleet-scale infrastructure control with a production-grade multi-agent collaboration system and safe execution layer. This is the v6.0 thesis.

---

*Document Status: APPROVED — Next Phase: Database Schema & API Implementation (Phase 1)*  
*Previous version: v5.0 (Fleet State Engine) — see changelog at top of document*

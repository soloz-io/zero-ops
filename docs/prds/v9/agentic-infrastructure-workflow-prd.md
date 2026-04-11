# Agentic Infrastructure Workflow — Product Requirements Document

**Version:** 1.0  
**Status:** DRAFT  
**Project:** zero-ops  
**Author:** Platform Architecture Team  
**Date:** 2026  
**Classification:** CONFIDENTIAL — INTERNAL USE ONLY  
**Related:** Zero-Ops PRD v9.0 (Hub-Spoke Architecture)

---

## 1. Executive Summary

The **Agentic Infrastructure Workflow** is a comprehensive agent-driven automation system that enables Zero-Ops tenants to build, deploy, and operate AI agents for infrastructure management and business automation. This initiative transforms Zero-Ops from a pure infrastructure platform into an **Agent-as-a-Service (AaaS) platform** where tenants can both consume pre-built platform agents and create their own custom agents.

**CRITICAL: MCP-First Design**

This is an **MCP-first platform**. All capabilities are exposed exclusively via Model Context Protocol (MCP) tools. There is NO web UI dashboard for agent management. All user interactions, testing, and operations are performed through MCP clients (Cursor, Goose, Claude Desktop, etc.).

**Primary Personas:**
- **Tenant Admins** who need to automate infrastructure operations (billing, provisioning, diagnostics) via MCP tools
- **Tenant Developers** who build business logic agents using MCP-based workflows
- **Platform Engineers** who maintain platform stability agents and MCP tool servers

**Core Outcome:**
Zero-touch agent creation and deployment — tenants define agents declaratively via MCP tools, and the platform provisions the complete runtime stack (memory, guardrails, LLM routing, observability, billing) automatically within 2 minutes.

**Key Constraints:**
- **MCP-Only Interface:** All features exposed via MCP tools. No web UI dashboard.
- **Manual Testing Only:** All testing performed manually via MCP clients. No automated test suites.
- All agents run in tenant-isolated environments (Spoke Pool or Spoke Silo)
- Mandatory guardrail enforcement (no agent can bypass safety policies)
- Hub-Spoke architecture preserved (Hub = Control Plane, Spokes = Application Plane)
- Outcome-based billing (tenants pay for completed agent tasks)
- MCP scope unchanged (platform operations only, tenant MCP servers deferred)

---

## 2. Problem Statement

### 2.1 Current State

Zero-Ops v9.0 provides tenants with powerful infrastructure (Kubernetes, PostgreSQL with pgvector, LiteLLM AI Gateway, AgentSandbox) but requires tenants to:

1. **Build RAG pipelines from scratch** — Document ingestion, chunking, embedding generation, vector storage
2. **Implement guardrails manually** — PII filtering, topic denial, content moderation
3. **Create agent runtime systems** — Lifecycle management, execution tracing, failure recovery
4. **Develop observability tools** — Agent-level metrics, debugging, semantic failure analysis
5. **Design billing systems** — Outcome tracking, cost attribution, invoice generation

This creates a **high barrier to entry** for tenants who want to quickly deploy agents without becoming infrastructure experts.

### 2.2 Gap / Motivation

**The Agent Creation Gap:**
Tenants receive infrastructure but no framework for building agents. They must reinvent the wheel for every agent they create.

**The Trust Gap:**
Without platform-enforced guardrails, tenant-created agents can hallucinate, leak PII, or access resources outside their tenant boundary, creating liability and compliance risks.

**The Observability Gap:**
Infrastructure metrics (CPU, memory, CNPG health) exist, but agent-level metrics (task success rate, semantic failures, guardrail violations) do not.

**The Billing Gap:**
Infrastructure consumption billing exists, but outcome-based billing (pay for completed agent tasks) does not.


### 2.3 Constraints & Non-Goals

**Constraints:**
- **MCP-First Design:** All platform capabilities exposed exclusively via MCP tools. No web UI dashboard.
- **Manual Testing Only:** All testing performed manually via MCP clients (Cursor, Goose, Claude Desktop). No automated test frameworks or CI/CD test suites.
- **Hub-Spoke Topology Preserved:** All AaaS capabilities built on top of v9.0 Hub-Spoke architecture
- **Tenant Isolation Mandatory:** Agents in Spoke Pool use namespace + RLS + network policies; Spoke Silo uses physical cluster isolation
- **Guardrails Non-Negotiable:** All agents must pass through guardrail enforcement (pre-LLM and post-LLM filtering)
- **Single Embedding Model:** v1.0 uses one embedding model for all tenants (simplifies infrastructure)
- **Platform-Defined Outcomes:** v1.0 billing uses platform-defined outcome types (tenant-defined outcomes deferred to v1.1)

**Non-Goals:**
- **Web UI Dashboard:** v1.0 does NOT provide web-based agent management interface. All operations via MCP tools.
- **Automated Testing:** v1.0 does NOT support automated test suites, CI/CD testing, or test frameworks. Manual testing via MCP only.
- **Tenant MCP Servers:** v1.0 does not support tenants exposing their own MCP tool servers (deferred to future version)
- **Agent Marketplace:** v1.0 does not support tenants publishing agents for other tenants (deferred to v1.2)
- **Custom Embedding Models:** v1.0 does not support tenant-specific embedding models (deferred to v1.2)
- **Deterministic Workflow Visualization:** Platform cannot show deterministic execution flow graphs, as execution order is decided dynamically by the LLM at runtime

---

## 3. User Personas & User Journeys

### 3.1 Personas

| Persona | Role | Goals | Responsibilities |
|---|---|---|---|
| **Tenant Admin** | Infrastructure automation owner | Automate billing, provisioning, cost optimization via MCP tools | Create infra agents using MCP, configure guardrails, monitor agent metrics |
| **Tenant Developer** | Business logic builder | Build customer-facing agents using MCP-based workflows | Create business agents via MCP, upload context documents, debug semantic failures |
| **Platform Engineer** | Zero-Ops platform maintainer | Ensure platform stability and reliability | Maintain Hub Infra Agents (cnpg2monitor, fleet-heartbeat), maintain MCP tool servers, monitor fleet metrics |
| **On-Call Engineer** | Incident responder | Diagnose and resolve agent failures quickly using MCP tools | Use Agent Debugging MCP tools, review guardrail violations, approve destructive operations |
| **Tenant End-User** | Consumer of tenant's SaaS product | Get answers from tenant's business agents | Interact with agents via tenant's application (not via MCP) |

### 3.2 User Journeys

#### Journey 1: First-Time Agent Creation (Tenant Admin) — MCP-Based

**Trigger:** Tenant Admin wants to automate invoice generation

**Actions:**
1. Open MCP client (Cursor, Goose, or Claude Desktop)
2. Connect to Zero-Ops MCP server
3. Use MCP tool: `create_agent` with parameters:
   ```json
   {
     "name": "billing-automation-agent",
     "type": "infrastructure",
     "role": "Generate invoices from Stripe data",
     "tools": ["stripe_api", "email_service"],
     "memory_enabled": true,
     "guardrails": {
       "global": ["pii_filter", "content_filter"],
       "task_specific": [
         {"type": "data_access_restriction", "denied_tables": ["customers", "internal_notes"]}
       ]
     },
     "model": "gpt-4-turbo"
   }
   ```
4. MCP tool returns agent ID and status

**System Responses:**
1. MCP server → AgentRegistry API (POST /v0/agents)
2. AgentRegistry stores config in Control Plane Shared DB (schema: agents)
3. MCP tool returns: `{"agent_id": "agent-uuid-123", "status": "registered"}`
4. Tenant uses MCP tool: `deploy_agent` with parameters:
   ```json
   {
     "agent_id": "agent-uuid-123",
     "provider_id": "spoke-silo-acme-corp"
   }
   ```
5. Deployment Adapter provisions Kagent Agent CRD in Spoke Silo
6. Kagent Controller reconciles CRD → creates Agent Pod + KEDA ScaledObject
7. Platform injects services (Memory, Guardrails, LiteLLM endpoints)
8. Agent enters "Ready" state (or scaled to 0)
9. MCP tool returns: `{"deployment_id": "deployment-uuid-456", "status": "deployed", "pod_name": "billing-automation-agent-7d8f9c"}`

**Success:** Agent is ready for invocation in <2 minutes

**Error States:**
- **Quota Exceeded:** MCP tool returns error: `{"error": "Cannot create agent. Tenant has reached agent limit (10). Upgrade to Enterprise tier."}`
- **Invalid Tool Access:** MCP tool returns error: `{"error": "Stripe API not authorized for this tenant. Contact support to enable."}`
- **Guardrail Policy Syntax Error:** MCP tool returns error: `{"error": "Policy 'Cannot access customer PII' has invalid syntax. Use format: 'data_access_restriction: denied_tables: [customers]'"}`


#### Journey 2: Agent Invocation & Execution (Tenant Developer) — MCP-Based

**Trigger:** Tenant Developer needs to test the billing automation agent

**Actions:**
1. Open MCP client (Cursor, Goose, or Claude Desktop)
2. Use MCP tool: `invoke_agent` with parameters:
   ```json
   {
     "agent_id": "billing-automation-agent",
     "prompt": "Generate invoice for customer acme-123 for January 2026"
   }
   ```

**System Responses:**
1. MCP server → AgentGateway (Hub)
2. AgentGateway validates JWT (tenant_id: acme-corp)
3. AgentGateway loads guardrail policies for agent `billing-automation-agent`
4. **Pre-LLM Filtering:**
   - Check topic denial: PASS (no blocked topics)
   - Check PII in prompt: PASS (no PII detected)
   - Check content filter: PASS (no profanity)
5. Request forwarded to AgentSandbox in Spoke Silo
6. KEDA detects HTTP request, scales pod 0→1 (2-3 seconds)
7. Agent Pod executes:
   - Loads config from ConfigMap
   - Queries Memory Service for context (semantic search: "invoice generation")
   - Calls LiteLLM Gateway with prompt + context
   - LLM generates response with tool call: `stripe_get_charges(customer_id="acme-123", month="2026-01")`
   - Agent executes tool (calls Stripe API)
   - LLM formats invoice data
8. **Post-LLM Filtering:**
   - Check PII in response: PASS (no PII leaked)
   - Check data access restriction: PASS (only allowed tables accessed)
   - Check response length: PASS (within 500 token limit)
9. Response returned to MCP client
10. Metrics emitted: task_success=1, latency=4.2s, tokens_used=850

**Success:** Agent generates invoice successfully, MCP tool returns invoice data

**Error States:**
- **Guardrail Violation (Pre-LLM):** MCP tool returns error: `{"error": "Request blocked. Prompt contains PII (email address). Remove PII and retry."}`
- **Tool Execution Failure:** MCP tool returns error: `{"error": "Agent failed. Stripe API returned 401 Unauthorized. Check API credentials in Infisical."}`
- **Guardrail Violation (Post-LLM):** MCP tool returns error: `{"error": "Response blocked. Agent attempted to access denied table 'internal_notes'. Review guardrail policies."}`
- **Timeout:** MCP tool returns error: `{"error": "Agent execution timeout after 30 seconds. Check agent logic for infinite loops."}`

#### Journey 3: Context Document Upload (Tenant Developer) — MCP-Based

**Trigger:** Tenant Developer wants to improve agent's knowledge about product pricing

**Actions:**
1. Open MCP client
2. Use MCP tool: `upload_context_document` with parameters:
   ```json
   {
     "file_path": "/path/to/product-pricing-guide.pdf",
     "namespace": "billing-automation-agent"
   }
   ```

**System Responses:**
1. MCP server → Context Service API (POST /context/ingest)
2. Context Service receives PDF file
3. Document parsing (extract text, preserve structure)
4. Chunking (split into ~500 token chunks, 15 chunks created)
5. Embedding generation (text-embedding-ada-002, 15 embeddings)
6. Storage in tenant's pgvector database (tenant_id + namespace scoped)
7. HNSW index updated for semantic search
8. MCP tool returns: `{"document_id": "doc-uuid-789", "chunks_created": 15, "status": "indexed"}`

**Success:** Document is searchable by agent within 30 seconds

**Error States:**
- **Unsupported Format:** MCP tool returns error: `{"error": "Document upload failed. Format '.xlsx' not supported. Use PDF, DOCX, TXT, or Markdown."}`
- **File Too Large:** MCP tool returns error: `{"error": "Document upload failed. File size 25MB exceeds limit (10MB). Split document and upload separately."}`
- **Quota Exceeded:** MCP tool returns error: `{"error": "Document upload failed. Tenant has reached storage limit (5GB). Delete old documents or upgrade plan."}`

#### Journey 4: Agent Failure & Debugging (On-Call Engineer) — MCP-Based

**Trigger:** Billing automation agent fails to complete task, alert fires

**Actions:**
1. On-Call Engineer receives alert: "Agent 'billing-automation-agent' failed 3 consecutive tasks"
2. Open MCP client
3. Use MCP tool: `get_agent_failures` with parameters:
   ```json
   {
     "agent_id": "billing-automation-agent",
     "limit": 10
   }
   ```
4. MCP tool returns list of recent failures
5. Use MCP tool: `analyze_agent_failure` with parameters:
   ```json
   {
     "agent_id": "billing-automation-agent",
     "failure_id": "failure-uuid-456"
   }
   ```

**System Responses:**
1. Agent Debugging Service triggered
2. **Infrastructure Check:**
   - AgentSandbox pod status: Running
   - Memory usage: 450MB / 512MB (healthy)
   - CPU usage: 0.3 cores (healthy)
   - Network connectivity: OK
   - Result: Infrastructure healthy
3. **Logic Check:**
   - Execution trace retrieved from OpenTelemetry
   - Tool call identified: `stripe_get_charges(customer_id="acme-123")`
   - Tool response: `{"error": "Customer not found", "status": 404}`
   - Result: Logic failure — Stripe API returned 404
4. **Semantic Check:**
   - Not applicable (agent didn't reach LLM response generation)
5. MCP tool returns debugging report:
   ```json
   {
     "failure_type": "logic_failure",
     "root_cause": "Stripe API returned 404 for customer 'acme-123'",
     "suggested_remediation": [
       "Verify customer ID exists in Stripe dashboard",
       "Check if customer was deleted or archived",
       "Update agent prompt to handle missing customers gracefully"
     ],
     "execution_trace": {...},
     "resolve_in_ide_context": {...}
   }
   ```

**Success:** Engineer identifies root cause in <1 minute, uses MCP context to fix issue in IDE

**Error States:**
- **Semantic Failure:** MCP tool returns: `{"failure_type": "semantic_failure", "root_cause": "Agent hallucinated pricing information. Context database lacks pricing documents."}`
- **Infrastructure Failure:** MCP tool returns: `{"failure_type": "infrastructure_failure", "root_cause": "Agent crashed due to OOM. Increase AgentSandbox memory allocation from 512MB to 1GB."}`

#### Journey 5: Guardrail Policy Configuration (Tenant Admin) — MCP-Based

**Trigger:** Tenant Admin wants to prevent customer support agent from discussing competitors

**Actions:**
1. Open MCP client
2. Use MCP tool: `get_agent_guardrails` to view current policies:
   ```json
   {
     "agent_id": "customer-support-agent"
   }
   ```
3. MCP tool returns inherited global policies (read-only):
   - PII Filter: Enabled
   - Content Filter: Enabled (profanity, hate speech)
4. Use MCP tool: `add_guardrail_policy` with parameters:
   ```json
   {
     "agent_id": "customer-support-agent",
     "policy_type": "topic_denial",
     "blocked_topics": ["competitors", "pricing_comparison", "alternative_products"]
   }
   ```
5. Use MCP tool: `test_guardrail_policy` with parameters:
   ```json
   {
     "agent_id": "customer-support-agent",
     "test_prompt": "How does our product compare to Competitor X?"
   }
   ```

**System Responses:**
1. Policy saved to Control Plane Shared DB (schema: agents, guardrail_policies table)
2. Test prompt sent to Guardrail Engine
3. Pre-LLM filtering applied:
   - Topic denial check: FAIL (prompt mentions "Competitor X")
   - Response: "Request blocked. Prompt discusses blocked topic: competitors"
4. MCP tool returns: `{"test_result": "blocked", "reason": "Prompt discusses blocked topic: competitors", "policy_working": true}`

**Success:** Policy is active immediately, all future requests checked

**Error States:**
- **Policy Syntax Error:** MCP tool returns error: `{"error": "Policy save failed. Topic 'competitors' must be lowercase. Use 'competitors' not 'Competitors'."}`
- **Conflicting Policies:** MCP tool returns error: `{"error": "Policy save failed. Task-specific policy conflicts with global policy. Remove global policy override or adjust task policy."}`

#### Journey 6: Outcome-Based Billing Event (Platform Agent)

**Trigger:** Platform agent (ProvisioningAgent) completes autopilot PR merge

**Actions:**
1. VictoriaMetrics detects CNPG connection pool saturation
2. DiagnosticsAgent analyzes metrics + OpenSearch events
3. Collaborator Agent classifies as "non-destructive" remediation
4. ProvisioningAgent creates PR to increase PgBouncer pool size
5. Tenant Admin approves PR
6. ArgoCD applies change
7. CNPG pool size increased, metrics normalize

**System Responses:**
1. ProvisioningAgent emits task completion event to NATS:
   ```json
   {
     "type": "agent.task_completed",
     "agent_id": "provisioning-agent",
     "tenant_id": "acme-corp",
     "task_id": "task-autopilot-pr-123",
     "outcome": "autopilot_pr_merged",
     "metadata": {
       "pr_url": "https://github.com/acme-corp/control-plane/pull/456",
       "resource": "cnpg-cluster",
       "change": "pgbouncer_pool_size: 20 -> 40"
     }
   }
   ```
2. Hub Event Router (Hub Cluster) consumes NATS event
3. Outcome Listener validates task completion:
   - Check outcome type: "autopilot_pr_merged" (valid)
   - Check tenant_id: "acme-corp" (valid)
   - Check task_id uniqueness: Not billed before (valid)
4. Billing record created in Hub Centralised DB:
   ```sql
   INSERT INTO billing_records (
     tenant_id, agent_id, outcome_type, unit_price, timestamp
   ) VALUES (
     'acme-corp', 'provisioning-agent', 'autopilot_pr_merged', 2.00, NOW()
   );
   ```
5. Billing engine aggregates records for monthly invoice

**Success:** Tenant is billed $2.00 for autopilot PR merge outcome

**Error States:**
- **Duplicate Billing:** "Billing event rejected. Task 'task-autopilot-pr-123' already billed. Skipping."
- **Invalid Outcome Type:** "Billing event rejected. Outcome 'unknown_outcome' not defined in platform billing schema."

---

## 4. Proposed Architecture

### 4.1 High-Level Flow

The Agentic Infrastructure Workflow follows a **three-phase lifecycle** integrated into the Hub-Spoke architecture:

**Phase 1: Build (Agent Registration)**
```
Tenant → MCP Client → Zero-Ops MCP Server → AgentRegistry API (Hub)
                                                    ↓
                                          Control Plane Shared DB
                                          (schema: agents, agent_definitions table)
```

**Phase 2: Deploy (Runtime Provisioning)**
```
Tenant → MCP Client → Zero-Ops MCP Server → AgentRegistry API (Hub)
                                                    ↓
                                          Deployment Adapter (Kubernetes)
                                                    ↓
                                          Kagent Agent CRD (Spoke)
                                                    ↓
                                          Kagent Controller (Spoke)
                                                    ↓
                          ┌─────────────────────┴─────────────────────┐
                          ↓                                           ↓
                    Agent Pod (ADK Runtime)                    KEDA ScaledObject
                    + Platform Service Injection               (scale-to-zero)
```

**Phase 3: Execute (On-Demand Invocation)**
```
User → AgentGateway (Hub) → Kagent A2AHandlerMux (Spoke)
                                    ↓
                          KEDA Interceptor (scale 0→1 if needed)
                                    ↓
                          Agent Pod (Spoke)
                                    ↓
              ┌─────────────────────┴─────────────────────┐
              ↓                     ↓                     ↓
        Memory Service      Guardrail Engine      LiteLLM Gateway
        (Hub)               (Hub)                 (Hub)
              ↓                     ↓                     ↓
              └─────────────────────┴─────────────────────┘
                                    ↓
                          Response + Billing Event (NATS)
```


### 4.2 Components & Responsibilities

| Component | Implementation | Responsibility |
|---|---|---|
| **AgentRegistry** | Go service (Hub) | Central registry for agent definitions. Stores agent configs in Control Plane Shared DB (schema: agents). Exposes CRUD APIs (/v0/agents, /v0/deployments). Triggers Kagent CRD provisioning via Deployment Adapters. |
| **Kagent Controller** | Go controller-runtime (Spoke) | Kubernetes-native agent runtime framework. Reconciles Agent CRDs → Kubernetes Deployments. Registers A2A handlers for agent invocation. Injects platform service endpoints (Memory, Guardrails, LiteLLM). |
| **Context Service** | Go service (Hub) | Managed document ingestion and RAG pipeline. Provides document upload API, automatic chunking, embedding generation (text-embedding-ada-002), storage in tenant's pgvector database, semantic search API. |
| **Guardrail Engine** | Go service (Hub) | Enforces safety and compliance policies. Pre-LLM filtering (topic denial, PII detection, content moderation). Post-LLM filtering (PII redaction, data access restriction, response length limits). Policies stored in Control Plane Shared DB. |
| **Memory Service** | Go service (Hub) | Agent memory and context retrieval. Semantic search in tenant's pgvector database (Control Plane Shared DB, schema: memory). Stores conversation history, learned patterns, document embeddings. |
| **LiteLLM Gateway** | LiteLLM OSS (Hub) | Multi-tenant AI model router. Routes agent requests to configured LLM providers (OpenAI, Anthropic, Together, Groq). Tracks token usage for billing. Provides fallback and retry logic. |
| **Metrics Aggregator** | Go service (Hub) | Parses token usage from Kagent A2A stream event.Data JSON, writes to VictoriaMetrics/ClickHouse. Bridges the gap between Kagent's nested JSON event format and time-series metrics storage. |
| **AgentGateway** | Rust service (Hub) | A2A and MCP communications gateway. Single JWT validation enforcement point via auth-proxy. Routes authenticated agent invocations to Kagent Controller in Spokes. Enforces rate limiting per agent. Supports SSE streaming for real-time agent responses. |
| **Agent Debugging Service** | Go service (Hub) | Automated diagnosis of agent failures. Analyzes infrastructure failures (K8sGPT extension), logic failures (execution tracing), semantic failures (LLM-based analysis). Generates structured failure reports. |
| **Outcome Listener** | Go service (Hub) | Consumes agent task completion events from NATS. Validates task completion, writes billing records to Hub Centralised DB. Triggers monthly invoice generation. |
| **KEDA HTTP Add-on** | KEDA OSS (Spoke) | Scale-to-zero for worker agents. Intercepts HTTP requests to agent pods. Scales Deployment 0→1 on demand (2-5 second cold start). Scales back to 0 after idle timeout (default: 5 minutes). |
| **AgentSandbox** | gVisor runtime (Spoke) | Sandboxed execution environment for agent workloads. Provides kernel-level isolation. Mounts tenant's pgvector database for memory. Configured via runtimeClassName: gvisor in Agent Pod spec. |
| **Control Plane Shared DB** | PostgreSQL + pgvector (Hub) | Stores agent definitions (schema: agents), memory embeddings (schema: memory), context documents (schema: context), guardrail policies, deployment status. Exposed via Hub-side PostgREST. |
| **Hub Centralised DB** | PostgreSQL (Hub) | Stores agent deployment status (written by Spoke Controller via PostgREST), billing records (written by Outcome Listener), fleet metrics aggregation. |
| **NATS JetStream** | NATS Server (Hub) | Asynchronous event backbone. Carries agent task completion events (spoke.*.agent.task_completed), billing telemetry, lifecycle triggers. Spoke clusters run NATS Leaf Nodes. |

### 4.3 Integration & Control Plane

**Agent Lifecycle Automation:**

1. **Agent Creation (Build Phase):**
   - Tenant defines agent via MCP client (Cursor, Goose, Claude Desktop)
   - MCP client → Zero-Ops MCP Server → AgentRegistry API (POST /v0/agents)
   - AgentRegistry validates definition (tool access permissions, guardrail syntax)
   - Agent config stored in Control Plane Shared DB (schema: agents, agent_definitions table)
   - No Kubernetes resources created yet

2. **Agent Deployment (Deploy Phase):**
   - Tenant triggers deployment via MCP tool: `deploy_agent`
   - MCP server → AgentRegistry API (POST /v0/deployments)
   - AgentRegistry → Deployment Adapter (Kubernetes)
   - Deployment Adapter materializes Kagent Agent CRD YAML
   - CRD applied to target Spoke Cluster (Pool or Silo)
   - Kagent Controller reconciles CRD:
     - Creates Kubernetes Deployment (Agent Pod)
     - Creates KEDA ScaledObject (scale-to-zero)
     - Injects platform service endpoints as environment variables
     - Registers A2A handler in Kagent A2AHandlerMux
   - Agent enters "Ready" state (or scaled to 0)

3. **Agent Invocation (Execute Phase):**
   - User sends request to agent via MCP tool: `invoke_agent`
   - MCP server → AgentGateway (JWT validation, tenant context)
   - AgentGateway loads guardrail policies from Control Plane Shared DB
   - Pre-LLM filtering applied (Guardrail Engine)
   - Request forwarded to Kagent Controller in Spoke
   - KEDA intercepts request, scales pod 0→1 if needed (2-5 seconds)
   - Agent Pod executes:
     - Loads config from ConfigMap
     - Queries Memory Service (semantic search in pgvector)
     - Calls LiteLLM Gateway (model inference)
     - Executes tools (if needed)
   - Post-LLM filtering applied (Guardrail Engine)
   - Response returned to MCP client
   - Metrics emitted (VictoriaMetrics)
   - If task completed, outcome event emitted (NATS)

**External Dependencies:**

- **Ory Stack (Kratos, Hydra, Keto):** Authentication and authorization for AgentGateway
- **Hetzner Cloud API:** Infrastructure provisioning for Spoke Clusters
- **OpenAI/Anthropic APIs:** LLM inference via LiteLLM Gateway
- **GitHub API:** Autopilot PR creation (platform agents)
- **Stripe API:** Example tool integration (tenant agents)

---

## 5. Technical Specifications

### 5.1 Platform Changes

**New Applications/Services (Hub Cluster):**

1. **AgentRegistry**
   - Deployment: `agentregistry` (2 replicas, Go binary)
   - Configuration: PostgreSQL connection string (Control Plane Shared DB)
   - Secrets: None (uses service account for K8s API access)
   - Exposed API: `/v0/agents`, `/v0/deployments` (via AgentGateway)

2. **Context Service**
   - Deployment: `context-service` (3 replicas, Go binary)
   - Configuration: Embedding model endpoint, pgvector connection string
   - Secrets: OpenAI API key (for embedding generation)
   - Exposed API: `/context/ingest`, `/context/search` (internal only)

3. **Guardrail Engine**
   - Deployment: `guardrail-engine` (3 replicas, Go binary)
   - Configuration: Policy schema, LLM endpoint (for semantic analysis)
   - Secrets: None (reads policies from Control Plane Shared DB)
   - Exposed API: `/guardrail/pre-filter`, `/guardrail/post-filter` (internal only)

4. **Memory Service**
   - Deployment: `memory-service` (3 replicas, Go binary)
   - Configuration: pgvector connection string (Control Plane Shared DB, schema: memory)
   - Secrets: None
   - Exposed API: `/memory/search`, `/memory/store` (internal only)

5. **Agent Debugging Service**
   - Deployment: `agent-debugging-service` (2 replicas, Go binary)
   - Configuration: K8sGPT endpoint, OpenTelemetry collector endpoint
   - Secrets: None
   - Exposed API: `/debug/analyze` (internal only)

6. **Outcome Listener**
   - Deployment: `outcome-listener` (2 replicas, Go binary)
   - Configuration: NATS JetStream connection, PostgreSQL connection (Hub Centralised DB)
   - Secrets: None
   - Exposed API: None (NATS consumer only)

**New Operators/Controllers (Spoke Clusters):**

1. **Kagent Controller**
   - Deployment: `kagent-controller` (1 replica per spoke, Go controller-runtime)
   - Configuration: Hub service endpoints (Memory, Guardrails, LiteLLM)
   - Secrets: Hub-side PostgREST JWT (for status writes)
   - CRDs: `agents.kagent.dev/v1alpha2` (Agent CRD)

2. **KEDA HTTP Add-on**
   - Deployment: `keda-http-interceptor` (1 replica per spoke)
   - Configuration: Agent pod endpoints, scale-to-zero timeout (5 minutes)
   - Secrets: None

3. **AccessPolicy CRD & PolicyAuthorizer**
   - CRD: `accesspolicies.kagent.dev/v1alpha1` (defines RBAC rules for agent tool access)
   - PolicyAuthorizer: Middleware component that enforces AccessPolicy rules before tool execution
   - Integration: Kagent Controller validates tool calls against AccessPolicy before allowing execution
   - Purpose: True RBAC enforcement for agent-to-tool authorization (beyond guardrails)

**Database Schema Changes:**

**Control Plane Shared DB (Hub):**
```sql
-- Schema: agents
CREATE SCHEMA IF NOT EXISTS agents;

CREATE TABLE agents.agent_definitions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    name TEXT NOT NULL,
    version TEXT NOT NULL,
    agent_type TEXT NOT NULL, -- 'infrastructure' or 'business'
    system_message TEXT NOT NULL,
    tool_access JSONB, -- {tools: ["stripe_api", "email_service"]}
    memory_config JSONB, -- {enabled: true, namespace: "agent-name"}
    guardrail_policies JSONB, -- {global: [...], task_specific: [...]}
    model_config JSONB, -- {name: "gpt-4-turbo", temperature: 0.7}
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(tenant_id, name, version)
);

CREATE TABLE agents.deployments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    agent_id UUID NOT NULL REFERENCES agents.agent_definitions(id),
    provider_id TEXT NOT NULL, -- spoke cluster identifier
    status TEXT NOT NULL, -- 'deploying', 'deployed', 'failed'
    provider_metadata JSONB, -- {namespace: "tenant-acme", pod_name: "agent-xyz"}
    deployed_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

-- Schema: memory
CREATE SCHEMA IF NOT EXISTS memory;

CREATE TABLE memory.embeddings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    agent_namespace TEXT NOT NULL, -- agent-specific memory isolation
    content TEXT NOT NULL,
    embedding vector(1536), -- text-embedding-ada-002 dimension
    metadata JSONB, -- {source: "conversation", timestamp: "..."}
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX ON memory.embeddings USING hnsw (embedding vector_cosine_ops);

-- Schema: context
CREATE SCHEMA IF NOT EXISTS context;

CREATE TABLE context.document_chunks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    agent_namespace TEXT NOT NULL,
    document_name TEXT NOT NULL,
    chunk_index INT NOT NULL,
    content TEXT NOT NULL,
    embedding vector(1536),
    metadata JSONB, -- {page: 5, section: "Pricing"}
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX ON context.document_chunks USING hnsw (embedding vector_cosine_ops);

-- RLS policies for tenant isolation
ALTER TABLE agents.agent_definitions ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON agents.agent_definitions
    USING (tenant_id = current_setting('app.tenant_id')::UUID);

ALTER TABLE memory.embeddings ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON memory.embeddings
    USING (tenant_id = current_setting('app.tenant_id')::UUID);

ALTER TABLE context.document_chunks ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON context.document_chunks
    USING (tenant_id = current_setting('app.tenant_id')::UUID);
```

**Hub Centralised DB:**
```sql
CREATE TABLE agent_deployment_status (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    spoke_id TEXT NOT NULL,
    status TEXT NOT NULL, -- 'ready', 'failed', 'scaling'
    pod_name TEXT,
    replicas INT,
    last_heartbeat TIMESTAMPTZ,
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE billing_records (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    outcome_type TEXT NOT NULL, -- 'autopilot_pr_merged', 'custom_agent_task_completed'
    unit_price DECIMAL(10, 2) NOT NULL,
    metadata JSONB, -- {task_id: "...", pr_url: "..."}
    timestamp TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX ON billing_records (tenant_id, timestamp);
```


### 5.2 Core Resources & APIs

**Kagent Agent CRD:**
```yaml
apiVersion: kagent.dev/v1alpha2
kind: Agent
metadata:
  name: billing-automation-agent
  namespace: tenant-acme
  labels:
    tenant_id: acme-corp
    agent_type: infrastructure
spec:
  type: Declarative
  declarative:
    systemMessage: |
      You are a billing automation expert.
      Generate invoices from Stripe data and send via email.
    
    modelConfig:
      name: gpt-4-turbo
      temperature: 0.7
    
    tools:
      - type: McpServer
        mcpServer:
          name: stripe-mcp-server
          toolNames: [stripe_get_charges, stripe_create_invoice]
      - type: McpServer
        mcpServer:
          name: email-mcp-server
          toolNames: [send_email]
    
    memory:
      type: kagent
      config:
        namespace: billing-automation-agent
        # Platform injects: pgvector connection to Control Plane Shared DB
    
    guardrails:
      global:
        - pii_filter
        - content_filter
      taskSpecific:
        - type: data_access_restriction
          deniedTables: [customers, internal_notes]
        - type: response_length_limit
          maxTokens: 500
```

**AgentRegistry API Endpoints:**

1. **Create Agent**
   ```
   POST /v0/agents
   Authorization: Bearer <jwt>
   Content-Type: application/json
   
   {
     "name": "billing-automation-agent",
     "version": "1.0.0",
     "agentType": "infrastructure",
     "systemMessage": "Generate invoices from Stripe data",
     "toolAccess": {
       "tools": ["stripe_api", "email_service"]
     },
     "memoryConfig": {
       "enabled": true,
       "namespace": "billing-automation-agent"
     },
     "guardrailPolicies": {
       "global": ["pii_filter", "content_filter"],
       "taskSpecific": [
         {"type": "data_access_restriction", "deniedTables": ["customers"]}
       ]
     },
     "modelConfig": {
       "name": "gpt-4-turbo",
       "temperature": 0.7
     }
   }
   
   Response: 201 Created
   {
     "id": "agent-uuid-123",
     "name": "billing-automation-agent",
     "status": "registered",
     "createdAt": "2026-03-25T10:00:00Z"
   }
   ```

2. **Deploy Agent**
   ```
   POST /v0/deployments
   Authorization: Bearer <jwt>
   Content-Type: application/json
   
   {
     "resourceType": "agent",
     "serverName": "billing-automation-agent",
     "version": "1.0.0",
     "providerId": "spoke-silo-acme-corp",
     "providerConfig": {
       "namespace": "tenant-acme",
       "replicas": 1
     }
   }
   
   Response: 202 Accepted
   {
     "deploymentId": "deployment-uuid-456",
     "status": "deploying",
     "estimatedTime": "2 minutes"
   }
   ```

3. **Get Agent Status**
   ```
   GET /v0/agents/{agent-id}/status
   Authorization: Bearer <jwt>
   
   Response: 200 OK
   {
     "agentId": "agent-uuid-123",
     "status": "ready",
     "deployments": [
       {
         "deploymentId": "deployment-uuid-456",
         "providerId": "spoke-silo-acme-corp",
         "status": "deployed",
         "podName": "billing-automation-agent-7d8f9c",
         "replicas": 1,
         "lastHeartbeat": "2026-03-25T10:05:00Z"
       }
     ]
   }
   ```

**Context Service API Endpoints:**

1. **Ingest Document**
   ```
   POST /context/ingest
   Authorization: Bearer <jwt>
   Content-Type: multipart/form-data
   
   file: product-pricing-guide.pdf
   namespace: billing-automation-agent
   
   Response: 200 OK
   {
     "documentId": "doc-uuid-789",
     "chunksCreated": 15,
     "status": "indexed"
   }
   ```

2. **Semantic Search**
   ```
   POST /context/search
   Authorization: Bearer <jwt>
   Content-Type: application/json
   
   {
     "query": "What is the pricing for enterprise plan?",
     "namespace": "billing-automation-agent",
     "topK": 5
   }
   
   Response: 200 OK
   {
     "results": [
       {
         "content": "Enterprise plan pricing: $500/month for up to 100 users...",
         "score": 0.92,
         "metadata": {"document": "product-pricing-guide.pdf", "page": 5}
       },
       ...
     ]
   }
   ```

**Guardrail Engine API Endpoints:**

1. **Pre-LLM Filter**
   ```
   POST /guardrail/pre-filter
   Authorization: Bearer <jwt>
   Content-Type: application/json
   
   {
     "agentId": "agent-uuid-123",
     "prompt": "Generate invoice for customer acme-123",
     "policies": ["pii_filter", "topic_denial", "content_filter"]
   }
   
   Response: 200 OK
   {
     "allowed": true,
     "violations": []
   }
   
   OR
   
   Response: 403 Forbidden
   {
     "allowed": false,
     "violations": [
       {
         "policy": "pii_filter",
         "reason": "Prompt contains email address: user@example.com"
       }
     ]
   }
   ```

2. **Post-LLM Filter**
   ```
   POST /guardrail/post-filter
   Authorization: Bearer <jwt>
   Content-Type: application/json
   
   {
     "agentId": "agent-uuid-123",
     "response": "Invoice generated for customer acme-123. Total: $1,234.56",
     "policies": ["pii_filter", "data_access_restriction", "response_length_limit"]
   }
   
   Response: 200 OK
   {
     "allowed": true,
     "filteredResponse": "Invoice generated for customer acme-123. Total: $1,234.56",
     "violations": []
   }
   ```

### 5.3 Defaulting & Automation Logic

**Agent Naming Convention:**
- Format: `{tenant-slug}-{agent-name}-{version}`
- Example: `acme-corp-billing-automation-agent-1-0-0`
- Kubernetes resource names: lowercase, hyphens only

**Agent Namespace Assignment:**
- Spoke Pool: `tenant-{tenant-slug}` (shared namespace)
- Spoke Silo: `tenant-{tenant-slug}` (dedicated namespace in dedicated cluster)

**Memory Namespace Isolation:**
- Format: `{agent-name}` (scoped within tenant's pgvector database)
- Example: `billing-automation-agent`
- RLS policy enforces tenant_id + agent_namespace scoping

**Guardrail Policy Inheritance:**
- Global policies: Applied to all agents for a tenant (defined at tenant level)
- Task-specific policies: Applied to individual agents (defined at agent level)
- Enforcement order: Global policies checked first, then task-specific policies

**KEDA Scale-to-Zero Configuration:**
- Default idle timeout: 5 minutes
- Min replicas: 0 (scale to zero when idle)
- Max replicas: 10 (auto-scale based on HTTP request queue)
- Cold start time: 2-5 seconds (pod startup + config load)

**LiteLLM Model Routing:**
- Default model: `gpt-4-turbo` (if not specified in agent config)
- Fallback chain: `gpt-4-turbo` → `gpt-4` → `gpt-3.5-turbo`
- Rate limiting: 100 requests/minute per agent (configurable)

**Billing Outcome Definitions:**
- `autopilot_pr_merged`: $2.00 per outcome
- `diagnostic_report_generated`: $1.00 per outcome
- `provisioning_task_completed`: $5.00 per outcome
- `custom_agent_task_completed`: $0.50 per outcome

### 5.4 Operational Semantics (Lifecycle & Frequency)

**Agent Configuration:**
- **Read Frequency:** Once per agent pod startup
- **Lifecycle:** Agent Pod starts → Fetch config from Control Plane Shared DB → Cache in memory → Use cached config for all requests
- **Rotation:** On agent update (new version deployed), old pod terminated, new pod starts with fresh config

**Guardrail Policies:**
- **Read Frequency:** Once per agent pod startup + on policy update event
- **Lifecycle:** Agent Pod starts → Fetch policies from Control Plane Shared DB → Cache in memory → Subscribe to policy update events (NATS) → Refresh cache on update
- **Per-Request:** Use cached policies (no DB lookup per request)

**Memory Service (Context Retrieval):**
- **Read Frequency:** Once per agent invocation (when agent needs context)
- **Lifecycle:** Agent receives user prompt → Generate query embedding → Semantic search in pgvector (Control Plane Shared DB, schema: memory) → Return top-K chunks → Inject into LLM prompt
- **Caching:** No caching (context must be fresh for each request)

**LiteLLM Gateway:**
- **Read Frequency:** Once per agent invocation (when agent needs LLM inference)
- **Lifecycle:** Agent sends prompt to LiteLLM → LiteLLM routes to configured provider (OpenAI, Anthropic) → Return response → Track token usage
- **Caching:** No caching (LLM responses are unique per prompt)

**JWKS (JWT Validation):**
- **Read Frequency:** Once per AgentGateway pod startup + periodic refresh (every 5 minutes)
- **Lifecycle:** AgentGateway starts → Fetch JWKS from Hydra → Cache in memory → Validate JWTs using cached keys → Refresh cache every 5 minutes
- **Per-Request:** Use cached JWKS (no Hydra call per request)

**Hub Service Endpoints (Memory, Guardrails, LiteLLM):**
- **Read Frequency:** Once per agent pod startup
- **Lifecycle:** Agent Pod starts → Read service endpoints from environment variables (injected by Kagent Controller) → Cache in memory → Use cached endpoints for all requests
- **Rotation:** On Kagent Controller restart, environment variables refreshed in new pods

**Billing Events:**
- **Write Frequency:** Once per completed agent task
- **Lifecycle:** Agent completes task → Emit event to NATS (`spoke.*.agent.task_completed`) → Hub Event Router consumes → Outcome Listener validates → Write billing record to Hub Centralised DB
- **Deduplication:** Task ID checked for uniqueness before billing


### 5.5 Idiomatic Behavior & Anti-Patterns

**Idiomatic Behavior:**

1. **Agent Config:** Read once at pod startup, cached in memory. Config updates trigger pod restart (rolling update).

2. **Guardrail Policies:** Read once at pod startup, cached in memory, refreshed on policy update event (NATS subscription). Never fetch policies per request.

3. **Memory Service:** Query on-demand per agent invocation. No caching (context must be fresh). Semantic search latency: <500ms.

4. **LiteLLM Gateway:** Call on-demand per agent invocation. No response caching (each prompt is unique). Token usage tracked per request.

5. **JWKS Validation:** Fetch once at AgentGateway startup, cached in memory, refreshed every 5 minutes. Never fetch JWKS per request.

6. **Billing Events:** Emit once per completed task. Asynchronous (NATS). No synchronous billing API calls in agent execution path.

7. **Hub Service Endpoints:** Read once from environment variables at pod startup. Never perform service discovery per request.

**Anti-Patterns (Must NOT):**

1. **❌ Fetch agent config per request:** Agent config is static per deployment. Fetching per request adds 50-100ms latency and creates DB bottleneck.

2. **❌ Fetch guardrail policies per request:** Policies are cached. Fetching per request adds 50-100ms latency and creates DB bottleneck.

3. **❌ Call Ory stack (Kratos, Hydra, Keto) directly from agent pods:** All auth is handled by AgentGateway. Agent pods receive pre-validated requests with injected tenant context.

4. **❌ Perform synchronous billing API calls in agent execution path:** Billing is asynchronous via NATS. Synchronous calls add latency and create coupling.

5. **❌ Cache LLM responses:** Each prompt is unique. Caching responses creates stale data and incorrect agent behavior.

6. **❌ Perform service discovery per request:** Service endpoints are static (injected at pod startup). DNS lookups per request add latency.

7. **❌ Write directly to Hub Centralised DB from agent pods:** Agent pods write to NATS only. Hub Event Router writes to Hub Centralised DB.

8. **❌ Fetch context documents on every agent invocation:** Context Service performs semantic search (pgvector query). Only fetch when agent needs context (not every request).

**Summary:**
- **Config/Policies:** Once per pod startup + event-driven refresh
- **Memory/LLM:** On-demand per invocation (no caching)
- **JWKS:** Once per startup + periodic refresh (5 minutes)
- **Billing:** Asynchronous via NATS (no synchronous calls)
- **Per-request fetching of config, policies, or service endpoints:** Anti-pattern

### 5.6 Security, Compliance & Reliability

**TLS Strategy:**
- All Hub services (AgentRegistry, Context Service, Guardrail Engine, Memory Service, LiteLLM Gateway) exposed via HTTPS
- TLS certificates issued by cert-manager (Let's Encrypt)
- mTLS between Hub and Spoke clusters (Cilium Cluster Mesh or VPN)
- Agent Pod → Hub Service communication: HTTPS with Hub-issued CA certificate

**Network Boundaries:**
- **Hub Cluster:** Exposed to internet via AgentGateway (HTTPS, JWT-protected)
- **Spoke Pool Cluster:** Internal only, no public ingress. Agents access Hub services via internal network.
- **Spoke Silo Cluster:** Internal only, no public ingress. Agents access Hub services via internal network.
- **Agent Pods:** No direct internet access. All external API calls (Stripe, email) via MCP tool servers (network policies enforced).

**Multi-Tenancy Isolation:**

1. **Spoke Pool (Starter Tier):**
   - Namespace isolation (Kubernetes NetworkPolicy)
   - RLS policies in Control Plane Shared DB (tenant_id scoping)
   - Memory namespace isolation (agent_namespace scoping)
   - LiteLLM rate limiting per tenant
   - No cross-tenant pod communication

2. **Spoke Silo (Enterprise Tier):**
   - Physical cluster isolation (dedicated Kubernetes cluster)
   - Dedicated Control Plane DB (local PostgreSQL in Silo)
   - Dedicated LiteLLM Gateway
   - Complete network isolation from other tenants

**Guardrail Enforcement:**
- Mandatory for all agents (cannot be disabled)
- Pre-LLM filtering: Blocks malicious prompts before LLM call
- Post-LLM filtering: Redacts PII, enforces data access restrictions
- Guardrail violations logged to OpenSearch (audit trail)
- Repeated violations trigger agent suspension (manual review required)

**Data Visibility Boundaries:**
- **Platform Team:** Infrastructure metrics only (CPU, memory, CNPG health). Cannot see agent prompts, responses, or tenant data.
- **Tenant Admin:** All data within their tenant (agent configs, memory, context documents, billing records). Cannot see other tenants.
- **Tenant End-User:** Only data exposed by tenant's business agents. Cannot see infrastructure or other users' data.

**Reliability SLOs:**
- **Agent Creation:** 95% success rate, <2 minutes provisioning time
- **Agent Invocation:** 99% success rate, <5 seconds latency (including cold start)
- **Context Service:** 99.9% uptime, <500ms semantic search latency
- **Guardrail Engine:** 99.9% uptime, <100ms filtering latency
- **Billing Accuracy:** 100% (no missed or duplicate billing events)

**Failure Recovery:**
- **Agent Pod Crash:** Kubernetes restarts pod automatically (liveness probe)
- **Hub Service Unavailable:** Agent retries with exponential backoff (max 3 retries)
- **NATS Unavailable:** Events buffered in NATS Leaf Node, delivered when Hub reconnects
- **Guardrail Engine Unavailable:** Agent invocation fails (fail-closed, no bypass)

---

## 6. User Journey Deep Dives (Scenario-Based)

### Scenario 1: Tenant Creates First Agent (Billing Automation) — MCP-Based

**Actors:** Tenant Admin (Acme Corp)

**Preconditions:**
- Tenant has active Zero-Ops account (Enterprise tier)
- Tenant has Spoke Silo cluster provisioned
- Tenant has Stripe API credentials stored in Infisical
- Tenant has MCP client installed (Cursor, Goose, or Claude Desktop)

**Step-by-Step Flow:**

1. **Tenant Admin opens MCP client**
   - Launch Cursor IDE
   - Connect to Zero-Ops MCP server
   - Authentication: JWT token from Ory Hydra (obtained via CLI login)

2. **Use MCP tool to create agent**
   - Invoke MCP tool: `create_agent`
   - Parameters:
     ```json
     {
       "name": "billing-automation-agent",
       "version": "1.0.0",
       "agent_type": "infrastructure",
       "system_message": "You are a billing automation expert. Generate invoices from Stripe data and send via email.",
       "tool_access": ["stripe_api", "email_service"],
       "memory_config": {
         "enabled": true,
         "namespace": "billing-automation-agent"
       },
       "guardrail_policies": {
         "global": ["pii_filter", "content_filter"],
         "task_specific": [
           {"type": "data_access_restriction", "denied_tables": ["customers", "internal_notes"]}
         ]
       },
       "model_config": {"name": "gpt-4-turbo"}
     }
     ```

3. **MCP server processes request:**
   - Zero-Ops MCP Server → AgentRegistry API (POST /v0/agents)
   - AgentRegistry validates request:
     - Check JWT signature (via auth-proxy)
     - Check tenant quota (max 10 agents for Enterprise tier)
     - Validate tool access permissions (Stripe API authorized for this tenant)
     - Validate guardrail policy syntax
     - All checks pass

4. **AgentRegistry stores agent config:**
   - Write to Control Plane Shared DB (schema: agents, agent_definitions table)
   - Agent ID generated: `agent-uuid-123`
   - Status: `registered`

5. **MCP tool returns success:**
   ```json
   {
     "agent_id": "agent-uuid-123",
     "name": "billing-automation-agent",
     "status": "registered",
     "created_at": "2026-03-25T10:00:00Z",
     "message": "Agent created successfully. Use deploy_agent to provision runtime."
   }
   ```

6. **Tenant Admin deploys agent:**
   - Invoke MCP tool: `deploy_agent`
   - Parameters:
     ```json
     {
       "agent_id": "agent-uuid-123",
       "provider_id": "spoke-silo-acme-corp",
       "provider_config": {
         "namespace": "tenant-acme",
         "replicas": 1
       }
     }
     ```

7. **AgentRegistry triggers deployment:**
   - Deployment Adapter (Kubernetes) materializes Kagent Agent CRD
   - CRD applied to Spoke Silo cluster (Acme Corp's dedicated cluster)

8. **Kagent Controller reconciles Agent CRD:**
   - Creates Kubernetes Deployment (Agent Pod)
   - Injects environment variables:
     - `LITELLM_ENDPOINT=https://litellm-gateway.hub.nutgraf.internal`
     - `MEMORY_SERVICE_ENDPOINT=https://memory-service.hub.nutgraf.internal`
     - `GUARDRAIL_ENDPOINT=https://guardrail-engine.hub.nutgraf.internal`
   - Creates KEDA ScaledObject (scale-to-zero, min: 0, max: 10)
   - Registers A2A handler in Kagent A2AHandlerMux

9. **Agent Pod starts:**
   - Loads config from ConfigMap (agent definition from Control Plane Shared DB)
   - Connects to Memory Service (validates pgvector connection)
   - Subscribes to guardrail policy updates (NATS)
   - Enters "Ready" state

10. **MCP tool returns deployment status:**
    ```json
    {
      "deployment_id": "deployment-uuid-456",
      "status": "deployed",
      "pod_name": "billing-automation-agent-7d8f9c",
      "replicas": "1/1",
      "ready": true,
      "message": "Agent deployed successfully in 1m 45s"
    }
    ```

11. **Tenant Admin tests agent:**
    - Invoke MCP tool: `invoke_agent`
    - Parameters:
      ```json
      {
        "agent_id": "billing-automation-agent",
        "prompt": "Generate invoice for customer acme-123 for January 2026"
      }
      ```
    - Agent executes successfully (see Scenario 2 for execution flow)

**Success Criteria:**
- Agent created and deployed in <2 minutes
- Agent enters "Ready" state
- Test invocation succeeds via MCP

**Variations / Edge Cases:**

**Variation 1: Quota Exceeded**
- At step 3, AgentRegistry detects tenant has 10 agents (quota limit)
- MCP tool returns error:
  ```json
  {
    "error": "quota_exceeded",
    "message": "Cannot create agent. Tenant has reached agent limit (10). Upgrade to Enterprise Plus tier or delete unused agents.",
    "current_count": 10,
    "limit": 10
  }
  ```
- Tenant Admin uses MCP tool: `list_agents` to view existing agents
- Tenant Admin uses MCP tool: `delete_agent` to remove unused agent

**Variation 2: Invalid Tool Access**
- At step 3, AgentRegistry detects Stripe API not authorized for this tenant
- MCP tool returns error:
  ```json
  {
    "error": "unauthorized_tool",
    "message": "Stripe API not authorized for tenant 'acme-corp'. Contact support to enable.",
    "tool": "stripe_api",
    "support_url": "https://support.nutgrat.in/enable-tool"
  }
  ```
- Tenant Admin contacts support via MCP tool: `create_support_ticket`

**Variation 3: Guardrail Policy Syntax Error**
- At step 3, AgentRegistry detects invalid policy syntax
- MCP tool returns error:
  ```json
  {
    "error": "invalid_policy_syntax",
    "message": "Guardrail policy syntax error: 'deniedTables' must be array of strings, got object.",
    "policy_field": "task_specific[0].denied_tables",
    "expected_type": "array",
    "received_type": "object"
  }
  ```
- Tenant Admin corrects syntax and retries MCP tool invocation


### Scenario 2: Agent Execution with Guardrail Violation — MCP-Based

**Actors:** Tenant Developer (Acme Corp)

**Preconditions:**
- Agent `billing-automation-agent` is deployed and ready
- Agent has guardrail policy: "Cannot access customer PII"
- Tenant Developer has MCP client configured

**Step-by-Step Flow:**

1. **Tenant Developer invokes agent via MCP**
   - Open MCP client (Cursor)
   - Invoke MCP tool: `invoke_agent`
   - Parameters (contains PII):
     ```json
     {
       "agent_id": "billing-automation-agent",
       "prompt": "Generate invoice for customer john.doe@example.com for January 2026"
     }
     ```

2. **Request flows through AgentGateway:**
   - MCP server → AgentGateway (Hub)
   - AgentGateway validates JWT (tenant_id: acme-corp)
   - AgentGateway loads guardrail policies for agent `billing-automation-agent`

3. **Pre-LLM Filtering (Guardrail Engine):**
   - AgentGateway → Guardrail Engine (POST /guardrail/pre-filter)
   - Guardrail Engine analyzes prompt:
     - **PII Filter:** FAIL — Email address detected: `john.doe@example.com`
     - **Topic Denial:** PASS
     - **Content Filter:** PASS

4. **AgentGateway blocks request:**
   - Does NOT forward to agent pod
   - Returns error to MCP client

5. **MCP tool returns guardrail violation:**
   ```json
   {
     "error": "guardrail_violation",
     "blocked": true,
     "violations": [
       {
         "policy": "pii_filter",
         "reason": "Prompt contains email address: john.doe@example.com",
         "suggestion": "Remove email address and use customer ID instead"
       }
     ],
     "logged_to": "OpenSearch audit trail"
   }
   ```

6. **Developer corrects prompt:**
   - Invoke MCP tool: `invoke_agent` again
   - Parameters (corrected):
     ```json
     {
       "agent_id": "billing-automation-agent",
       "prompt": "Generate invoice for customer acme-123 for January 2026"
     }
     ```

7. **Pre-LLM Filtering (retry):**
   - **PII Filter:** PASS
   - **Topic Denial:** PASS
   - **Content Filter:** PASS

8. **Request forwarded to agent pod:**
   - AgentGateway → Kagent A2AHandlerMux (Spoke Silo)
   - KEDA detects request, pod already running (replicas: 1)

9. **Agent Pod executes:**
   - Loads config from memory (cached)
   - Queries Memory Service for context
   - Calls LiteLLM Gateway
   - LLM generates tool call: `stripe_get_charges(customer_id="acme-123", month="2026-01")`
   - Agent executes tool (calls Stripe API via MCP tool server)
   - LLM formats invoice

10. **Post-LLM Filtering (Guardrail Engine):**
    - **PII Filter:** PASS (no PII in response)
    - **Data Access Restriction:** PASS (only allowed tables accessed)
    - **Response Length Limit:** PASS (within 500 tokens)

11. **MCP tool returns success:**
    ```json
    {
      "success": true,
      "response": "Invoice for Customer acme-123\nPeriod: January 2026\nCharges:\n- Subscription: $1,234.56\nTotal: $1,234.56",
      "metrics": {
        "latency_ms": 4200,
        "tokens_used": 850,
        "guardrails_passed": true
      }
    }
    ```

12. **Metrics emitted:**
    - Agent Pod → Metrics Aggregator (Hub)
    - Metrics written to VictoriaMetrics

13. **Billing event emitted:**
    - Agent Pod → NATS (`spoke.acme-corp.agent.task_completed`)
    - Hub Event Router consumes event
    - Outcome Listener writes billing record ($0.50)

**Success Criteria:**
- Guardrail violation detected and blocked (no LLM call made)
- Developer corrects prompt and succeeds via MCP
- Invoice generated successfully
- Billing event recorded

**Variations / Edge Cases:**

**Variation 1: Post-LLM Guardrail Violation**
- At step 10, LLM response contains PII (hallucinated email address)
- Guardrail Engine detects PII, redacts email
- MCP tool returns:
  ```json
  {
    "success": true,
    "response": "Invoice for Customer acme-123...",
    "warnings": [
      {
        "type": "pii_redacted",
        "message": "Response filtered by guardrail policy (PII redacted)"
      }
    ]
  }
  ```

**Variation 2: Tool Execution Failure**
- At step 9, Stripe API returns 401 Unauthorized
- Agent Pod logs error
- MCP tool returns error:
  ```json
  {
    "error": "tool_execution_failed",
    "message": "Agent failed. Stripe API authentication error. Check API credentials in Infisical.",
    "tool": "stripe_get_charges",
    "status_code": 401
  }
  ```

### Scenario 3: Agent Failure & Automated Debugging — MCP-Based

**Actors:** On-Call Engineer (Acme Corp)

**Preconditions:**
- Agent `billing-automation-agent` has failed 3 consecutive tasks
- Alert fired to On-Call Engineer
- Agent Debugging Service is enabled
- Engineer has MCP client configured

**Step-by-Step Flow:**

1. **Alert fires:**
   - VictoriaMetrics detects: `agent_task_failure{agent_id="billing-automation-agent"}` count = 3 in 5 minutes
   - Alertmanager sends notification to On-Call Engineer (PagerDuty, Slack)

2. **On-Call Engineer opens MCP client:**
   - Launch Cursor IDE
   - Invoke MCP tool: `get_agent_failures`
   - Parameters:
     ```json
     {
       "agent_id": "billing-automation-agent",
       "limit": 10
     }
     ```

3. **MCP tool returns failure list:**
   ```json
   {
     "failures": [
       {
         "failure_id": "failure-uuid-456",
         "timestamp": "2026-03-25T10:15:00Z",
         "error": "Agent execution failed",
         "status": "unresolved"
       },
       ...
     ],
     "total_count": 3
   }
   ```

4. **Engineer analyzes latest failure:**
   - Invoke MCP tool: `analyze_agent_failure`
   - Parameters:
     ```json
     {
       "agent_id": "billing-automation-agent",
       "failure_id": "failure-uuid-456"
     }
     ```

5. **Agent Debugging Service performs analysis:**
   - **Infrastructure Check (K8sGPT extension):**
     - Pod status: Running
     - Memory: 450MB / 512MB (healthy)
     - CPU: 0.3 cores (healthy)
     - Network: OK
     - Result: Infrastructure healthy
   
   - **Logic Check (Execution Tracing):**
     - Retrieves trace from OpenTelemetry
     - Identifies tool call: `stripe_get_charges(customer_id="acme-123")`
     - Tool response: `{"error": "Customer not found", "status": 404}`
     - Result: Logic failure — Stripe API returned 404
   
   - **Semantic Check:**
     - Not applicable (agent didn't reach LLM response generation)

6. **MCP tool returns debugging report:**
   ```json
   {
     "failure_type": "logic_failure",
     "root_cause": "Stripe API returned 404 for customer 'acme-123'",
     "execution_trace": {
       "spans": [
         {"name": "agent.invoke", "duration_ms": 4200, "status": "error"},
         {"name": "memory.search", "duration_ms": 300, "status": "ok"},
         {"name": "guardrail.pre_filter", "duration_ms": 100, "status": "ok"},
         {"name": "llm.generate", "duration_ms": 2500, "status": "ok"},
         {"name": "tool.execute", "duration_ms": 1000, "status": "error", "error": "HTTP 404 - Customer not found"}
       ]
     },
     "suggested_remediation": [
       "Verify customer ID 'acme-123' exists in Stripe dashboard",
       "Check if customer was deleted or archived",
       "Update agent prompt to handle missing customers gracefully",
       "Consider adding customer validation tool before invoice generation"
     ],
     "related_resources": {
       "stripe_dashboard": "https://dashboard.stripe.com/customers/acme-123",
       "agent_config": "/v0/agents/billing-automation-agent",
       "execution_trace": "/traces/failure-uuid-456"
     },
     "resolve_in_ide_context": {
       "agent_config": {...},
       "execution_trace": {...},
       "available_mcp_tools": ["update_agent", "stripe_customer_check"]
     }
   }
   ```

7. **Engineer investigates using MCP tools:**
   - Invoke MCP tool: `stripe_customer_check`
   - Parameters:
     ```json
     {
       "customer_id": "acme-123"
     }
     ```
   - Result: Customer exists in Stripe, but was archived
   - Root cause identified: Agent doesn't handle archived customers

8. **Engineer updates agent config:**
   - Invoke MCP tool: `update_agent`
   - Parameters:
     ```json
     {
       "agent_id": "billing-automation-agent",
       "system_message": "Generate invoices from Stripe data. If customer is archived or not found, return friendly error message."
     }
     ```

9. **Agent config updated:**
   - MCP server → AgentRegistry API (PUT /v0/agents/billing-automation-agent)
   - AgentRegistry updates Control Plane Shared DB
   - Kagent Controller detects config change
   - Rolling update triggered: Old pod terminated, new pod starts with updated config

10. **MCP tool returns update confirmation:**
    ```json
    {
      "success": true,
      "agent_id": "billing-automation-agent",
      "updated_fields": ["system_message"],
      "deployment_status": "rolling_update_in_progress",
      "message": "Agent config updated. Rolling update initiated."
    }
    ```

11. **Engineer tests fix:**
    - Invoke MCP tool: `invoke_agent`
    - Parameters:
      ```json
      {
        "agent_id": "billing-automation-agent",
        "prompt": "Generate invoice for customer acme-123"
      }
      ```
    - MCP tool returns:
      ```json
      {
        "success": true,
        "response": "Customer acme-123 is archived. Cannot generate invoice.",
        "metrics": {"latency_ms": 2100, "tokens_used": 120}
      }
      ```

**Success Criteria:**
- Failure diagnosed in <1 minute via MCP
- Root cause identified (Stripe 404)
- Remediation suggestions provided
- Engineer fixes issue and validates via MCP

**Variations / Edge Cases:**

**Variation 1: Infrastructure Failure**
- At step 5, K8sGPT detects pod OOM (memory: 512MB / 512MB, OOMKilled)
- MCP tool returns:
  ```json
  {
    "failure_type": "infrastructure_failure",
    "root_cause": "Agent crashed due to memory limit exceeded.",
    "suggested_remediation": ["Increase AgentSandbox memory allocation from 512MB to 1GB"]
  }
  ```
- Engineer uses MCP tool: `update_agent_resources` to increase memory

**Variation 2: Semantic Failure**
- At step 5, execution trace shows LLM generated incorrect invoice amount
- Semantic check triggered: LLM analyzes prompt + response
- MCP tool returns:
  ```json
  {
    "failure_type": "semantic_failure",
    "root_cause": "Agent hallucinated pricing. Context database lacks pricing documents for January 2026.",
    "suggested_remediation": ["Upload pricing guide for January 2026 via upload_context_document MCP tool"]
  }
  ```

---

## 7. Success Criteria & Acceptance Tests

### 7.1 Functional Requirements

**CRITICAL: All testing performed manually via MCP tools. No automated test suites.**

**Agent Creation:**
- ✅ Tenant can create agent via MCP tool `create_agent` in <5 minutes
- ✅ Agent config stored in Control Plane Shared DB (schema: agents)
- ✅ Agent deployment via MCP tool `deploy_agent` completes in <2 minutes
- ✅ Agent enters "Ready" state (or scaled to 0)

**Manual Verification via MCP:**
```bash
# Create agent via MCP tool
# In Cursor/Goose/Claude Desktop:
create_agent({
  "name": "test-agent",
  "version": "1.0.0",
  "agent_type": "infrastructure",
  "system_message": "Test agent",
  "tool_access": [],
  "memory_config": {"enabled": true},
  "guardrail_policies": {"global": ["pii_filter"]},
  "model_config": {"name": "gpt-4-turbo"}
})

# Verify agent in DB (via MCP tool or direct query)
get_agent_status({"agent_id": "test-agent"})
# Expected: {"status": "registered", "created_at": "..."}

# Deploy agent via MCP tool
deploy_agent({
  "agent_id": "test-agent",
  "provider_id": "spoke-silo-test"
})

# Verify deployment status via MCP tool
get_agent_status({"agent_id": "test-agent"})
# Expected: {"status": "deployed", "pod_name": "test-agent-xxx", "replicas": "1/1"}
```

**Agent Execution:**
- ✅ Agent executes tasks in <5 seconds (including cold start)
- ✅ Guardrails block 100% of policy violations (no false negatives)
- ✅ Context Service returns search results in <500ms
- ✅ Agent metrics emitted to VictoriaMetrics

**Manual Verification via MCP:**
```bash
# Invoke agent via MCP tool
invoke_agent({
  "agent_id": "test-agent",
  "prompt": "test prompt"
})
# Expected: {"success": true, "response": "...", "metrics": {...}}

# Test guardrail blocks PII via MCP tool
invoke_agent({
  "agent_id": "test-agent",
  "prompt": "email: user@example.com"
})
# Expected: {"error": "guardrail_violation", "blocked": true, "violations": [...]}

# Verify metrics via MCP tool or direct query
# (Manual check in VictoriaMetrics UI or via curl)
curl -s http://victoriametrics:8428/api/v1/query \
  -d 'query=agent_task_success{agent_id="test-agent"}'
# Expected: {"status":"success","data":{"result":[{"value":[...,"1"]}]}}
```

**Context Service:**
- ✅ Document ingestion via MCP tool completes in <30 seconds
- ✅ Semantic search returns top-K results in <500ms
- ✅ Embeddings stored in pgvector (Control Plane Shared DB, schema: context)

**Manual Verification via MCP:**
```bash
# Upload document via MCP tool
upload_context_document({
  "file_path": "/path/to/test-doc.pdf",
  "namespace": "test-agent"
})
# Expected: {"document_id": "...", "chunks_created": 15, "status": "indexed"}

# Verify chunks in DB (via MCP tool or direct query)
# (Manual check in PostgreSQL)
psql -h hub-db -U postgres -d control_plane -c \
  "SELECT COUNT(*) FROM context.document_chunks WHERE agent_namespace='test-agent';"

# Semantic search via MCP tool
search_context({
  "query": "test query",
  "namespace": "test-agent",
  "top_k": 5
})
# Expected: {"results": [...]} (5 chunks)
```

**Agent Debugging:**
- ✅ Debugging Service diagnoses failures in <30 seconds
- ✅ Infrastructure failures detected via K8sGPT
- ✅ Logic failures detected via execution tracing
- ✅ Semantic failures detected via LLM analysis

**Manual Verification via MCP:**
```bash
# Trigger agent failure (invalid tool call) via MCP tool
invoke_agent({
  "agent_id": "test-agent",
  "prompt": "call invalid tool"
})
# Expected: {"error": "tool_execution_failed", ...}

# Analyze failure via MCP tool
analyze_agent_failure({
  "agent_id": "test-agent",
  "failure_id": "latest"
})
# Expected: {"failure_type": "logic_failure", "root_cause": "...", "suggested_remediation": [...]}
```

**Outcome-Based Billing:**
- ✅ Billing events emitted on task completion
- ✅ Billing records written to Hub Centralised DB
- ✅ No duplicate billing (task_id uniqueness enforced)

**Manual Verification:**
```bash
# Complete agent task via MCP tool
invoke_agent({
  "agent_id": "test-agent",
  "prompt": "complete task"
})

# Verify billing event in NATS (manual check)
nats sub "spoke.*.agent.task_completed"
# Expected: Event with task_id, outcome, tenant_id

# Verify billing record in DB (manual query)
psql -h hub-db -U postgres -d hub_centralised -c \
  "SELECT * FROM billing_records WHERE agent_id='test-agent' ORDER BY timestamp DESC LIMIT 1;"
# Expected: 1 row with outcome_type='custom_agent_task_completed', unit_price=0.50
```

### 7.2 Non-Functional Requirements

**Scalability:**
- ✅ Platform supports 100 tenants, each with 10 agents (1,000 total agents)
- ✅ Agent creation scales to 100 concurrent requests
- ✅ Guardrail enforcement adds <100ms latency per request
- ✅ Agent metrics pipeline handles 10,000 events/second

**Performance:**
- ✅ Agent creation: <2 minutes
- ✅ Agent invocation (warm): <2 seconds
- ✅ Agent invocation (cold start): <5 seconds
- ✅ Context search: <500ms
- ✅ Guardrail filtering: <100ms

**Reliability:**
- ✅ Agent creation success rate: >95%
- ✅ Agent invocation success rate: >99%
- ✅ Context Service uptime: >99.9%
- ✅ Guardrail Engine uptime: >99.9%
- ✅ Billing accuracy: 100% (no missed or duplicate events)

### 7.3 Business Metrics

**Adoption:**
- ✅ 80% of tenants create at least 1 custom agent within 30 days of onboarding
- ✅ 50% of tenants use platform agents (autopilot, diagnostics) within 7 days
- ✅ Average agents per tenant: 5

**Revenue:**
- ✅ Average outcome fee per tenant: $50/month
- ✅ Average consumption fee per tenant: $200/month
- ✅ Total AaaS revenue: $250/tenant/month (base + outcome + consumption)

**Retention:**
- ✅ Tenant retention: >90% after 6 months
- ✅ Agent usage retention: >80% (agents remain active after creation)

---

## 8. Implementation Roadmap

**Phase 1: Foundation (Weeks 1-8)**
- AgentRegistry API (agent CRUD, deployment orchestration)
- Kagent Controller integration (Agent CRD reconciliation)
- Control Plane Shared DB schema (agents, memory, context)
- Basic agent creation UI in Platform Console

**Phase 2: Platform Services (Weeks 9-16)**
- Context Service (document ingestion, semantic search)
- Guardrail Engine (pre-LLM and post-LLM filtering)
- Memory Service (pgvector integration)
- LiteLLM Gateway multi-tenant configuration

**Phase 3: Observability & Debugging (Weeks 17-24)**
- Agent metrics pipeline (VictoriaMetrics integration)
- Agent Debugging Service (infrastructure, logic, semantic failure analysis)
- Platform Console dashboards (agent metrics, failure reports)

**Phase 4: Billing & Compliance (Weeks 25-32)**
- Outcome Listener (NATS consumer, billing record creation)
- Billing engine (monthly aggregation, invoice generation)
- Guardrail violation audit logs (OpenSearch integration)

**Phase 5: Testing & Launch (Weeks 33-40)**
- End-to-end testing (all user journeys)
- Performance testing (1,000 agents, 10,000 events/second)
- Security testing (guardrail bypass attempts, tenant isolation)
- Documentation (user guides, API reference)
- Beta launch (10 pilot tenants)
- General availability

**Total Timeline:** 40 weeks (~10 months)

---

## 9. Appendix

### 9.1 Glossary

| Term | Definition |
|---|---|
| **AaaS** | Agent-as-a-Service — platform that provides consumable agents and agent creation infrastructure |
| **Hub Infra Agent** | Platform-owned agent for Zero-Ops stability (e.g., cnpg2monitor, fleet-heartbeat) |
| **Spoke Infra Agent** | Tenant-created agent for infrastructure automation (e.g., billing automation, provisioning) |
| **Spoke Business Agent** | Tenant-created agent for end-user facing logic (e.g., customer support, data analysis) |
| **Outcome** | Completed agent task that triggers billing event |
| **Guardrail** | Safety policy that constrains agent behavior (pre-LLM and post-LLM filtering) |
| **Context-as-a-Service** | Managed RAG pipeline (document ingestion + semantic search) |
| **AgentRegistry** | Central registry for agent definitions and deployment orchestration |
| **Kagent** | Kubernetes-native agent runtime framework |
| **KEDA** | Kubernetes Event-Driven Autoscaling (scale-to-zero for agents) |
| **ADK** | Agent Development Kit (runtime library for agent execution) |

### 9.2 References

- Zero-Ops PRD v9.0: Hub-Spoke Architecture with Distributed Identity & Control Plane
- Kagent Documentation: `archived/agentic-ai/solo/kagent/`
- AgentRegistry Documentation: `archived/agentic-ai/solo/agentregistry/`
- Model Context Protocol (MCP) Specification
- KEDA HTTP Add-on Documentation
- OpenTelemetry Tracing Specification

---

**END OF DOCUMENT**

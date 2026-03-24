# Zero-Ops Platform — Product Requirements Document

**Version:** 10.0 — Native AaaS Multi-Tenant Platform  
**Status:** DRAFT  
**Project:** zero-ops  
**Author:** Platform Architecture Team  
**Date:** 2026  
**Classification:** CONFIDENTIAL — INTERNAL USE ONLY  
**Supersedes:** v9.0 (Hub-Spoke Architecture with Distributed Identity & Control Plane)

---

## Executive Summary

Zero-Ops v10.0 is a **Native Agent-as-a-Service (AaaS) Multi-Tenant Platform** that enables tenants to both consume pre-built platform agents and create their own custom agents. The platform provides the complete infrastructure, runtime, and tooling necessary for building, deploying, and operating AI agents at scale.

**Core Value Proposition:**

1. **Infrastructure for Agents**: Tenants receive a fully provisioned environment (Kubernetes, PostgreSQL with pgvector, LiteLLM AI Gateway, AgentSandbox) to build custom agents
2. **Consumable Platform Agents**: Tenants consume pre-built agents for infrastructure automation (autopilot, diagnostics, provisioning)
3. **Agent Creation Framework**: Tenants create their own agents (infrastructure automation blueprints, business logic agents) that run in their isolated environments

**Business Model:**

- **Base Fee**: Covers guardrail enforcement and gateway overhead
- **Outcome-Based Pricing**: Tenants pay for completed agent tasks (platform-defined outcomes initially)
- **Consumption-Based Pricing**: Infrastructure usage (compute, storage, LLM tokens)

**Architecture Foundation:**

The platform maintains the Hub-Spoke topology from v9.0, with the Hub serving as the Control Plane (identity, provisioning, observability) and Spokes serving as the Application Plane (agent runtime environments). All AaaS capabilities are built on top of this proven multi-tenant foundation.

---

## Changelog: v9.0 → v10.0

| Area | v9.0 State | v10.0 Change |
|---|---|---|
| **Product Definition** | SaaS Factory — infrastructure provisioning platform | **Native AaaS Platform** — infrastructure + consumable agents + agent creation framework |
| **Agent Taxonomy** | Platform agents only (DiagnosticsAgent, ProvisioningAgent for Zero-Ops operations) | **Three agent types:** Hub Infra Agents (platform stability), Spoke Infra Agents (tenant automation blueprints), Spoke Business Agents (tenant end-user facing) |
| **Agent Creation** | Not supported — tenants build agents from scratch using provided infrastructure | **Declarative agent creation** — tenants define agents via Platform Console, platform provisions runtime and enforces policies |
| **Context Management** | Raw pgvector + S3 — tenants build own RAG pipelines | **Context-as-a-Service** — document ingestion API with automatic chunking, embedding, and storage |
| **Guardrails** | Not defined | **Mandatory guardrail engine** — global + task-specific policies, tenant-configurable, enforced at pre-LLM and post-LLM layers |
| **Agent Observability** | Infrastructure metrics only (CPU, memory, CNPG health) | **Agent-level metrics** — success rate, task completion, failure analysis, per-tenant isolation |
| **Billing Model** | Infrastructure consumption only (Hetzner costs, LLM tokens) | **Hybrid billing** — Base fee + Outcome-based (tasks completed) + Consumption-based (infrastructure) |
| **Agent Debugging** | K8sGPT for infrastructure diagnostics | **Multi-layer debugging** — infrastructure failures + logic failures + semantic failures (hallucinations, context issues) |
| **MCP Scope** | Platform operations only (Goose/Cursor manage Zero-Ops infrastructure) | **MCP scope unchanged** — remains platform operations only (tenant MCP servers deferred to future version) |

---

## 1. Problem Statement

### 1.1 The AaaS Gap

Zero-Ops v9.0 successfully solves the **infrastructure provisioning problem** for AI-native SaaS builders. However, it does not address the **agent creation and consumption problem**. Tenants receive a powerful sandbox (Kubernetes, pgvector, LiteLLM, gVisor) but must:

- Build their own RAG pipelines from scratch
- Implement guardrails and safety mechanisms manually
- Create agent runtime and lifecycle management systems
- Develop observability and debugging tools for agent behavior
- Design billing and metering systems for agent outcomes

This creates a **high barrier to entry** for tenants who want to quickly deploy agents without becoming infrastructure experts.

### 1.2 The Dual Model Requirement

The platform must support **two distinct use cases simultaneously**:

1. **Tenant-Created Agents**: Tenants build custom agents for their specific business needs (customer support, data analysis, billing automation)
2. **Platform-Provided Agents**: Tenants consume pre-built agents that automate infrastructure management (autopilot PR creation, diagnostics, provisioning)

Both agent types must share the same underlying runtime, guardrails, observability, and billing infrastructure.

### 1.3 The Trust Problem

Without platform-enforced guardrails, tenant-created agents can:

- Hallucinate incorrect information
- Leak personally identifiable information (PII)
- Access resources outside their tenant boundary
- Generate content that violates business policies

This creates **liability and compliance risks** for both the platform and tenants.

---

## 2. Agent Taxonomy

Zero-Ops v10.0 defines three distinct agent types, each with different ownership, deployment, and billing models.

### 2.1 Hub Infra Agents

**Owner**: Zero-Ops Platform  
**Purpose**: Platform stability and operations  
**Deployment**: Hub Cluster  
**Examples**: cnpg2monitor, fleet-heartbeat, Hub Event Router  
**Billing**: Included in platform base fee  

These agents ensure the Zero-Ops platform itself operates reliably. Tenants do not interact with these agents directly.

### 2.2 Spoke Infra Agents

**Owner**: Tenants  
**Purpose**: Tenant-level infrastructure automation blueprints  
**Deployment**: Tenant's Spoke (Pool or Silo)  
**Examples**: Billing automation agent, provisioning workflow agent, cost optimization agent  
**Billing**: Outcome-based (tasks completed) + Consumption-based (infrastructure)  

These agents automate infrastructure operations within a tenant's environment. They are created by tenants using the platform's agent creation framework.

### 2.3 Spoke Business Agents

**Owner**: Tenants  
**Purpose**: Tenant end-user facing business logic  
**Deployment**: Tenant's Spoke (Pool or Silo)  
**Examples**: Customer support agent, data analyst agent, code review agent  
**Billing**: Outcome-based (tasks completed) + Consumption-based (infrastructure)  

These agents serve the tenant's end-users and implement business-specific workflows. They are created by tenants using the platform's agent creation framework.

---

## 3. Agent Anatomy

Every agent on the Zero-Ops platform, regardless of type, consists of the following components:

### 3.1 Context Layer

**Role**: Defines the agent's purpose and capabilities  
**Tools/APIs**: External systems the agent can interact with  
**Data**: Business data the agent can access  
**Memory**: Conversation history and learned patterns (stored in pgvector)  

### 3.2 Intelligence Layer

**Models**: LLM(s) the agent uses for reasoning (routed via LiteLLM)  
**Thinking Modes**: Reasoning strategies (chain-of-thought, reflection, planning)  

### 3.3 Trust Layer

**Identity & Security**: Authentication and authorization (Ory stack + AgentGateway)  
**Guardrails**: Safety policies that constrain agent behavior  

All three layers are configured when a tenant creates an agent and enforced by the platform at runtime.

---

## 4. Core AaaS Components

### 4.1 Context-as-a-Service

**Purpose**: Managed document ingestion and RAG pipeline  

**Capabilities**:
- Document ingestion API (tenants upload files via Platform Console or API)
- Automatic chunking and embedding generation (standard embedding model for all tenants)
- Storage in tenant's pgvector database (tenant-scoped isolation)
- Semantic search API for agent queries

**Benefit**: Tenants do not need to build RAG pipelines from scratch.

---

### 4.2 Guardrail Policy Engine

**Purpose**: Enforce safety and compliance policies on all agent interactions  

**Policy Types**:
- **Global Guardrails**: Apply to all agents a tenant creates (e.g., "never discuss competitors")
- **Task-Specific Guardrails**: Apply to individual agent instances (e.g., "Customer Support Agent cannot access billing data")

**Enforcement Points**:
- **Pre-LLM Filtering**: Validate prompts before sending to LLM (prevents malicious inputs)
- **Post-LLM Filtering**: Validate responses before returning to user (prevents hallucinations, PII leaks)

**Configuration**: Tenants define policies via Platform Console. Policies are stored in Control Plane DB and enforced by AgentGateway.

**Mandatory Baseline**: All agents must pass through guardrail enforcement. Tenants cannot disable guardrails.

---

### 4.3 Agent Schema & Runtime

**Agent Definition**: Stored in Control Plane DB with the following schema:
- Agent ID, Tenant ID, Agent Type (infra/business)
- Role definition, Tool/API access list
- Memory configuration (pgvector namespace)
- Guardrail policy references (global + task-specific)
- LLM model selection (via LiteLLM)

**Agent Provisioning**: Declarative via Crossplane (similar to AINativeSaaS XRD)
- Platform provisions AgentSandbox (gVisor) in tenant's Spoke
- Mounts tenant's pgvector database for memory
- Configures network policies for tool/API access

**Agent Execution**:
- Runtime fetches agent config from Control Plane DB
- Loads guardrail policies
- Executes agent logic in AgentSandbox
- Emits metrics to observability pipeline

**Lifecycle**: Tenants create, update, pause, resume, and delete agents via Platform Console.

---

### 4.4 Agent Metrics Pipeline

**Purpose**: Per-tenant agent observability  

**Metrics Collected**:
- Task success rate (% of tasks completed successfully)
- Task failure rate (% of tasks that failed)
- Guardrail violations (# of blocked prompts/responses)
- Execution time (latency per task)
- LLM token usage (cost attribution)

**Storage**: VictoriaMetrics (time-series) + Hub Centralised DB (aggregated outcomes for billing)

**Isolation**: Metrics are strictly per-tenant. Tenants cannot see other tenants' metrics.

**Dashboards**: Platform Console displays agent-level metrics alongside infrastructure metrics.

---

### 4.5 Agent Debugging Service

**Purpose**: Automated diagnosis of agent failures  

**Failure Types**:

1. **Infrastructure Failures**: Agent crashed due to OOM, network timeout, etc. (extends K8sGPT pattern)
2. **Logic Failures**: Agent failed to complete task because tool returned error, API was unreachable, etc. (requires execution tracing)
3. **Semantic Failures**: Agent hallucinated, provided incorrect information, or lacked sufficient context (requires LLM-based analysis)

**Output**: Structured failure report displayed in Platform Console with suggested remediation steps.

**Integration**: "Resolve in IDE" button generates MCP context for manual debugging via Cursor/Goose.

---

### 4.6 Outcome Listener & Billing Engine

**Purpose**: Trigger billing events based on agent task completion  

**Billing Model**:

- **Base Fee**: Covers guardrail enforcement and AgentGateway overhead (charged per tenant per month)
- **Outcome Fee**: Charged when agent completes a task (platform-defined outcomes in v10.0)
- **Consumption Fee**: Infrastructure usage (Hetzner compute, LLM tokens, storage)

**Outcome Definitions (v10.0 — Platform-Defined)**:

- **Autopilot PR Merged**: 1 outcome unit
- **Diagnostic Report Generated**: 1 outcome unit
- **Provisioning Task Completed**: 1 outcome unit
- **Custom Agent Task Completed**: 1 outcome unit (generic for tenant-created agents)

**Outcome Listener Architecture**:

- Agents emit task completion events to NATS (`spoke.*.agent.task_completed`)
- Hub Event Router consumes events
- Outcome Listener validates task completion (checks for success status)
- Writes billing record to Hub Centralised DB
- Billing engine aggregates records for monthly invoice

**Future Evolution**:
- v10.1: Tenant-defined outcomes (tenants specify success criteria via webhooks)
- v10.2: Agent-defined outcomes (each agent type has intrinsic success metric)

---

## 5. Agent Creation Flow

### 5.1 Tenant Workflow

1. Tenant logs into Platform Console
2. Navigates to "Agents" section
3. Clicks "Create Agent"
4. Defines agent via UI form:
   - Agent name and description
   - Agent type (Infra or Business)
   - Role definition (system prompt)
   - Tool/API access (select from available integrations)
   - Memory configuration (enable/disable conversation history)
   - Guardrail policies (select global policies + define task-specific policies)
   - LLM model selection (GPT-4, Claude, etc. via LiteLLM)
5. Clicks "Create"
6. Platform provisions AgentSandbox in tenant's Spoke
7. Agent becomes available for invocation

### 5.2 Platform Workflow

1. Platform Console sends agent definition to `mcp-server` (via AgentGateway)
2. `mcp-server` validates definition (checks tool access permissions, guardrail policy syntax)
3. Writes agent config to Control Plane DB
4. Commits AgentSandbox CR to tenant's control plane repository
5. ArgoCD syncs AgentSandbox to tenant's Spoke
6. Crossplane provisions gVisor pod with agent runtime
7. Agent runtime fetches config from Control Plane DB
8. Agent enters "Ready" state
9. Platform Console displays agent status

### 5.3 Agent Invocation

1. Tenant (or tenant's end-user) sends request to agent via Platform Console or API
2. Request flows through AgentGateway (JWT validation, tenant context injection)
3. AgentGateway loads guardrail policies for this agent
4. Pre-LLM filtering applied (prompt validation)
5. Request forwarded to AgentSandbox in tenant's Spoke
6. Agent runtime executes logic (calls tools, queries memory, invokes LLM)
7. Post-LLM filtering applied (response validation)
8. Response returned to user
9. Metrics emitted (task success/failure, latency, token usage)
10. If task completed successfully, outcome event emitted to NATS for billing

---

## 6. Platform Architecture (High-Level)

### 6.1 Hub Cluster (Control Plane)

**Responsibilities**:
- Global identity (Ory Keto, Kratos, Hydra)
- Tenant onboarding and provisioning (Crossplane)
- Agent schema storage (Control Plane Shared DB)
- Guardrail policy management
- Outcome-based billing (Outcome Listener + Hub Centralised DB)
- Fleet-wide observability (VictoriaMetrics, Grafana)
- Context-as-a-Service API (document ingestion)

**New Components (v10.0)**:
- Context Service (document ingestion + embedding generation)
- Guardrail Policy Engine (policy storage + enforcement coordination)
- Outcome Listener (NATS consumer for billing events)
- Agent Debugging Service (failure analysis)

---

### 6.2 Spoke Pool Cluster (Shared Application Plane)

**Responsibilities**:
- Multi-tenant agent runtime (Starter tier tenants)
- Shared AgentSandbox pool (gVisor pods)
- Shared pgvector database (tenant-scoped via RLS)
- Shared LiteLLM AI Gateway (tenant-scoped token quotas)
- Agent metrics collection (per-tenant isolation)

**Agent Deployment**: Pooled model — multiple tenants share infrastructure, strict tenant isolation via namespace + RLS + network policies.

---

### 6.3 Spoke Silo Cluster (Dedicated Application Plane)

**Responsibilities**:
- Single-tenant agent runtime (Enterprise tier tenants)
- Dedicated AgentSandbox (gVisor pods)
- Dedicated pgvector database
- Dedicated LiteLLM AI Gateway
- Agent metrics collection (tenant-owned)

**Agent Deployment**: Siloed model — tenant gets dedicated cluster, full physical isolation.

---

## 7. Guardrail Policy Framework

### 7.1 Policy Structure

**Global Guardrails** (tenant-wide):
```yaml
tenant_id: acme-corp
global_policies:
  - type: topic_denial
    topics: ["competitors", "pricing_details"]
  - type: pii_filter
    enabled: true
  - type: content_filter
    blocked_patterns: ["profanity", "hate_speech"]
```

**Task-Specific Guardrails** (per-agent):
```yaml
agent_id: customer-support-agent-001
task_policies:
  - type: data_access_restriction
    allowed_tables: ["customers", "tickets"]
    denied_tables: ["billing", "internal_notes"]
  - type: response_length_limit
    max_tokens: 500
```

### 7.2 Enforcement Flow

1. User sends prompt to agent
2. AgentGateway loads global + task-specific policies for this agent
3. **Pre-LLM Filtering**:
   - Check topic denial (reject if prompt mentions blocked topics)
   - Check PII in prompt (reject if user accidentally included PII)
   - Check content filter (reject if prompt contains profanity)
4. If all checks pass, forward to LLM
5. LLM generates response
6. **Post-LLM Filtering**:
   - Check PII in response (redact if LLM leaked PII)
   - Check data access restriction (reject if response contains data from denied tables)
   - Check response length limit (truncate if exceeds max tokens)
7. If all checks pass, return response to user
8. If any check fails, return error message + log guardrail violation

### 7.3 Policy Configuration

Tenants configure policies via Platform Console:
- Navigate to "Agents" → Select agent → "Guardrails" tab
- View inherited global policies (read-only)
- Add/edit task-specific policies (form-based UI)
- Test policies with sample prompts (validation sandbox)
- Save policies (stored in Control Plane DB)

---

## 8. Context-as-a-Service

### 8.1 Document Ingestion Flow

1. Tenant uploads document via Platform Console (PDF, DOCX, TXT, Markdown)
2. Context Service receives file
3. Document parsing (extract text, preserve structure)
4. Chunking (split into semantic chunks, ~500 tokens each)
5. Embedding generation (standard model: `text-embedding-ada-002` or equivalent)
6. Storage in tenant's pgvector database (tenant_id scoped)
7. Indexing for semantic search
8. Confirmation returned to tenant

### 8.2 Agent Query Flow

1. Agent needs context to answer user question
2. Agent runtime calls Context Service API: `GET /context/search?query=<user_question>&tenant_id=<tenant_id>`
3. Context Service generates query embedding
4. Semantic search in tenant's pgvector database (cosine similarity)
5. Top-K relevant chunks returned (K=5 default)
6. Agent runtime injects chunks into LLM prompt as context
7. LLM generates response using retrieved context

### 8.3 Standard Embedding Model

**Model**: Single embedding model for all tenants (v10.0)  
**Rationale**: Simplifies infrastructure, reduces operational complexity  
**Future**: v10.1 may support tenant-specific embedding models for specialized domains  

---

## 9. Agent Debugging

### 9.1 Infrastructure Failure Debugging

**Trigger**: Agent crashes, times out, or becomes unresponsive  
**Diagnosis**:
- K8sGPT analyzes AgentSandbox pod events
- Checks for OOM, CPU throttling, network errors
- Correlates with infrastructure metrics (VictoriaMetrics)

**Output**: "Agent crashed due to memory limit exceeded. Increase AgentSandbox memory allocation."

---

### 9.2 Logic Failure Debugging

**Trigger**: Agent fails to complete task (tool error, API unreachable)  
**Diagnosis**:
- Agent runtime emits execution trace (tool calls, API responses)
- Debugging service analyzes trace
- Identifies failure point (e.g., "Tool X returned 404 error")

**Output**: "Agent failed because CRM API returned 404. Check API endpoint configuration."

---

### 9.3 Semantic Failure Debugging

**Trigger**: Agent provides incorrect or irrelevant response  
**Diagnosis**:
- Debugging service sends agent's prompt + response to LLM for analysis
- LLM identifies hallucination or context insufficiency
- Suggests remediation (e.g., "Add more context documents about product pricing")

**Output**: "Agent hallucinated pricing information. Context database lacks pricing documents. Upload pricing guide to Context Service."

---

## 10. Billing Model

### 10.1 Billing Components

**Base Fee**: $50/month per tenant (covers guardrails, gateway, platform agents)  
**Outcome Fee**: Variable per task type (see outcome definitions)  
**Consumption Fee**: Infrastructure usage (Hetzner compute, LLM tokens, storage)  

### 10.2 Outcome Definitions (v10.0)

| Outcome Type | Unit Price | Description |
|---|---|---|
| Autopilot PR Merged | $2.00 | Platform agent creates and tenant merges PR |
| Diagnostic Report Generated | $1.00 | Platform agent generates infrastructure diagnostic report |
| Provisioning Task Completed | $5.00 | Platform agent provisions new resource |
| Custom Agent Task Completed | $0.50 | Tenant-created agent completes any task |

### 10.3 Billing Calculation Example

**Tenant**: Acme Corp (Enterprise tier)  
**Month**: January 2026  

**Base Fee**: $50  
**Outcome Fees**:
- 10 Autopilot PRs merged: 10 × $2.00 = $20.00
- 5 Diagnostic reports: 5 × $1.00 = $5.00
- 2 Provisioning tasks: 2 × $5.00 = $10.00
- 100 Custom agent tasks: 100 × $0.50 = $50.00

**Consumption Fees**:
- Hetzner compute: $200
- LLM tokens (1M tokens): $20
- Storage (500GB): $10

**Total**: $50 + $85 + $230 = $365

---

## 11. User Journeys

### 11.1 Journey A: Tenant Creates Business Agent

1. Tenant Admin logs into Platform Console
2. Navigates to "Agents" → "Create Agent"
3. Defines "Customer Support Agent":
   - Role: "Answer customer questions about product features"
   - Tools: CRM API, Knowledge Base API
   - Memory: Enabled (conversation history)
   - Guardrails: Global policies + "Cannot access billing data"
   - Model: GPT-4
4. Clicks "Create"
5. Platform provisions AgentSandbox in tenant's Spoke Silo
6. Agent becomes "Ready" in 2 minutes
7. Tenant tests agent via Platform Console chat interface
8. Agent successfully answers product questions using CRM data
9. Tenant deploys agent to production (exposes API endpoint to their end-users)

---

### 11.2 Journey B: Platform Agent Executes Autopilot Task

1. VictoriaMetrics detects CNPG connection pool saturation on tenant's cluster
2. DiagnosticsAgent analyzes metrics + OpenSearch events
3. Collaborator Agent classifies as "non-destructive" remediation
4. ProvisioningAgent (platform agent) creates PR to increase PgBouncer pool size
5. PR committed to tenant's control plane repository
6. Tenant Admin receives notification in Platform Console
7. Tenant reviews PR, approves
8. ArgoCD applies change
9. CNPG pool size increased, metrics normalize
10. **Outcome event emitted**: "Autopilot PR Merged"
11. Billing record created: $2.00 outcome fee

---

### 11.3 Journey C: Agent Fails, Debugging Service Diagnoses

1. Tenant's "Billing Automation Agent" fails to complete task
2. Agent Debugging Service triggered automatically
3. **Infrastructure check**: AgentSandbox healthy (no OOM, no crashes)
4. **Logic check**: Execution trace shows "Billing API returned 500 error"
5. **Semantic check**: Not applicable (agent didn't reach LLM)
6. Debugging service generates report: "Agent failed because Billing API is down. Check API health."
7. Report displayed in Platform Console with "Resolve in IDE" button
8. Tenant clicks button, Cursor opens with structured context
9. Tenant investigates Billing API, discovers database connection issue
10. Tenant fixes database connection
11. Agent retries task, succeeds

---

## 12. Implementation Phases

### Phase 1: Agent Schema & Runtime (Weeks 1-4)
- Control Plane DB schema for agent definitions
- AgentSandbox provisioning via Crossplane
- Agent runtime (fetch config, execute in gVisor)
- Basic agent creation UI in Platform Console

### Phase 2: Context-as-a-Service (Weeks 5-8)
- Document ingestion API
- Chunking + embedding generation
- pgvector storage integration
- Semantic search API

### Phase 3: Guardrail Policy Engine (Weeks 9-12)
- Policy schema (global + task-specific)
- Pre-LLM filtering (topic denial, PII, content filter)
- Post-LLM filtering (PII redaction, data access restriction)
- Policy configuration UI in Platform Console

### Phase 4: Agent Metrics & Observability (Weeks 13-16)
- Agent-level metrics collection
- VictoriaMetrics integration
- Per-tenant metric isolation
- Agent metrics dashboards in Platform Console

### Phase 5: Agent Debugging Service (Weeks 17-20)
- Infrastructure failure debugging (K8sGPT extension)
- Logic failure debugging (execution tracing)
- Semantic failure debugging (LLM-based analysis)
- Debugging reports in Platform Console

### Phase 6: Outcome-Based Billing (Weeks 21-24)
- Outcome Listener (NATS consumer)
- Billing record creation (Hub Centralised DB)
- Billing engine (monthly aggregation)
- Invoice generation

### Phase 7: Integration & Testing (Weeks 25-28)
- End-to-end testing (all journeys)
- Performance testing (agent creation, execution, debugging)
- Security testing (guardrail bypass attempts, tenant isolation)
- Documentation (user guides, API reference)

**Total Timeline**: 28 weeks (~7 months) before v10.0 launch

---

## 13. Success Criteria

### 13.1 Functional Requirements

- ✅ Tenants can create agents via Platform Console in <5 minutes
- ✅ Agents execute tasks in tenant's Spoke with <2 second latency
- ✅ Guardrails block 100% of policy violations (no false negatives)
- ✅ Context Service ingests documents and returns search results in <1 second
- ✅ Agent Debugging Service diagnoses failures in <30 seconds
- ✅ Outcome-based billing records are accurate (0% billing errors)

### 13.2 Non-Functional Requirements

- ✅ Platform supports 100 tenants, each with 10 agents (1,000 total agents)
- ✅ Agent creation scales to 100 concurrent requests
- ✅ Guardrail enforcement adds <100ms latency per request
- ✅ Agent metrics pipeline handles 10,000 events/second
- ✅ Platform Console loads agent dashboards in <2 seconds

### 13.3 Business Metrics

- ✅ 80% of tenants create at least 1 custom agent within 30 days of onboarding
- ✅ 50% of tenants use platform agents (autopilot, diagnostics) within 7 days
- ✅ Average outcome fee per tenant: $50/month
- ✅ Tenant retention: >90% after 6 months

---

## 14. Risks & Mitigations

### 14.1 Technical Risks

**Risk**: Guardrail enforcement adds excessive latency  
**Mitigation**: Optimize pre-LLM filtering (cache policy lookups), run post-LLM filtering asynchronously

**Risk**: Context Service becomes bottleneck at scale  
**Mitigation**: Horizontal scaling (multiple embedding service replicas), caching (frequently accessed chunks)

**Risk**: Agent Debugging Service provides inaccurate diagnoses  
**Mitigation**: Continuous improvement via feedback loop (tenants rate debugging reports)

### 14.2 Business Risks

**Risk**: Outcome-based pricing is too complex for tenants to understand  
**Mitigation**: Provide clear pricing calculator in Platform Console, show real-time cost estimates

**Risk**: Tenants prefer to build agents from scratch (don't use agent creation framework)  
**Mitigation**: Offer both options — framework for speed, raw infrastructure for flexibility

---

## 15. Future Roadmap

### v10.1 (Q3 2026)
- Tenant-defined outcomes (webhooks for custom success criteria)
- Multi-agent orchestration (agents call other agents)
- Agent versioning (A/B testing, rollback)

### v10.2 (Q4 2026)
- Agent-defined outcomes (intrinsic success metrics per agent type)
- Agent marketplace (tenants publish agents for other tenants)
- Custom embedding models (tenant-specific domain embeddings)

### v11.0 (Q1 2027)
- Multi-entity agentic systems (third-party agent integration)
- Federated identity (cross-provider JWT trust)
- Agent discovery protocol (dynamic agent composition)

---

## 16. Appendix

### 16.1 Glossary

**AaaS**: Agent-as-a-Service — platform that provides consumable agents and agent creation infrastructure  
**Hub Infra Agent**: Platform-owned agent for Zero-Ops stability (e.g., cnpg2monitor)  
**Spoke Infra Agent**: Tenant-created agent for infrastructure automation (e.g., billing automation)  
**Spoke Business Agent**: Tenant-created agent for end-user facing logic (e.g., customer support)  
**Outcome**: Completed agent task that triggers billing event  
**Guardrail**: Safety policy that constrains agent behavior  
**Context-as-a-Service**: Managed RAG pipeline (document ingestion + semantic search)  

### 16.2 References

- AWS Prescriptive Guidance: Building multi-tenant architectures for agentic AI on AWS
- Zero-Ops PRD v9.0: Hub-Spoke Architecture with Distributed Identity & Control Plane
- Model Context Protocol (MCP) Specification
- Ory Stack Documentation (Keto, Kratos, Hydra)

---

**END OF DOCUMENT**

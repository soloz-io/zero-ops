# Repository Integration Analysis
## Zero-Ops Platform Architecture with Kratos

**Date**: March 8, 2026  
**Objective**: Agent-driven infrastructure platform using Kratos, ZeroTouch Engine MCP, Agent Gateway, and zero-ops CLI

---

Repos:
docs/zero-touch/archived/zerotouch-engine/
docs/zero-touch/archived/agentgateway/
docs/syself/archived/repos/kratos/

## Executive Summary

**ARCHITECTURE**: Complete agent-driven SaaS platform using Kratos for identity, Agent Gateway for MCP routing, ZeroTouch Engine for workflows, and zero-ops CLI for cluster operations.

### High-Level Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    User Chat Interface                       │
│              (Agent Order Management Assistant)              │
└────────────────────────┬────────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────────┐
│                   Agent Gateway (Rust)                       │
│  • MCP Protocol Router                                       │
│  • RBAC & Authorization (CEL expressions)                    │
│  • Session Management                                        │
│  • Multi-tenant isolation                                    │
└────────────────────────┬────────────────────────────────────┘
                         │
         ┌───────────────┼───────────────┐
         │               │               │
         ▼               ▼               ▼
┌────────────────┐ ┌──────────────┐ ┌──────────────────┐
│ Ory Kratos     │ │ ZeroTouch    │ │ zero-ops CLI     │
│ (Identity)     │ │ Engine       │ │ (Management      │
│                │ │ (Python MCP) │ │  Cluster)        │
│ • OAuth/OIDC   │ │              │ │                  │
│ • MFA/2FA      │ │ • Workflow   │ │ • Talos/Ubuntu   │
│ • Sessions     │ │   DSL        │ │ • CAPI bootstrap │
│ • User/Org DB  │ │ • MCP Server │ │ • Hetzner        │
│ • Admin API    │ │ • State mgmt │ │                  │
└────────────────┘ └──────────────┘ └──────────────────┘
```

### Detailed Component Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                         PRESENTATION LAYER                           │
│  ┌──────────────────┐  ┌──────────────────┐  ┌──────────────────┐  │
│  │  Chat UI (Web)   │  │  Admin Portal    │  │  CLI Client      │  │
│  │  • React/Next.js │  │  • Tenant Mgmt   │  │  • zero-ops CLI  │  │
│  │  • WebSocket     │  │  • User Mgmt     │  │  • Direct API    │  │
│  └──────────────────┘  └──────────────────┘  └──────────────────┘  │
└────────────────────────────┬────────────────────────────────────────┘
                             │
                             ▼
┌─────────────────────────────────────────────────────────────────────┐
│                      AGENT GATEWAY LAYER                             │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │                    Agent Gateway (Rust)                       │   │
│  │  ┌────────────┐  ┌────────────┐  ┌────────────┐             │   │
│  │  │ MCP Router │  │ Auth Proxy │  │ RBAC Engine│             │   │
│  │  │ • Tool     │  │ • JWT Val  │  │ • CEL Eval │             │   │
│  │  │ • Prompt   │  │ • Session  │  │ • Policies │             │   │
│  │  │ • Resource │  │ • Kratos   │  │ • Tenant   │             │   │
│  │  └────────────┘  └────────────┘  └────────────┘             │   │
│  │                                                               │   │
│  │  ┌────────────┐  ┌────────────┐  ┌────────────┐             │   │
│  │  │ Observ.    │  │ Rate Limit │  │ Circuit Br │             │   │
│  │  │ • Traces   │  │ • Per User │  │ • Fallback │             │   │
│  │  │ • Metrics  │  │ • Per Org  │  │ • Retry    │             │   │
│  │  └────────────┘  └────────────┘  └────────────┘             │   │
│  └──────────────────────────────────────────────────────────────┘   │
└────────────────────────────┬────────────────────────────────────────┘
                             │
         ┌───────────────────┼───────────────────┐
         │                   │                   │
         ▼                   ▼                   ▼
┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐
│  IDENTITY       │  │  WORKFLOW       │  │  INFRASTRUCTURE │
│  SERVICE        │  │  ENGINE         │  │  LAYER          │
└─────────────────┘  └─────────────────┘  └─────────────────┘


┌─────────────────────────────────────────────────────────────────────┐
│                    IDENTITY SERVICE (Kratos)                         │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │                      Ory Kratos                               │   │
│  │  ┌────────────┐  ┌────────────┐  ┌────────────┐             │   │
│  │  │ Auth Flows │  │ User Store │  │ Session    │             │   │
│  │  │ • OAuth    │  │ • Postgres │  │ • Cookie   │             │   │
│  │  │ • Password │  │ • Identities│ │ • Redis    │             │   │
│  │  │ • MFA/2FA  │  │ • Orgs     │  │ • JWT      │             │   │
│  │  └────────────┘  └────────────┘  └────────────┘             │   │
│  └──────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────┐
│                 WORKFLOW ENGINE (ZeroTouch)                          │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │              ZeroTouch Engine MCP Server                      │   │
│  │  ┌────────────┐  ┌────────────┐  ┌────────────┐             │   │
│  │  │ Workflow   │  │ State Mgmt │  │ Handlers   │             │   │
│  │  │ • DSL      │  │ • Sessions │  │ • Adapter  │             │   │
│  │  │ • Parser   │  │ • Blobs    │  │ • Platform │             │   │
│  │  │ • Traverser│  │ • Restore  │  │ • Bootstrap│             │   │
│  │  └────────────┘  └────────────┘  └────────────┘             │   │
│  └──────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────┐
│              INFRASTRUCTURE LAYER (zero-ops)                         │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │                    zero-ops CLI                               │   │
│  │  ┌────────────┐  ┌────────────┐  ┌────────────┐             │   │
│  │  │ Mgmt Clstr │  │ CAPI       │  │ Hetzner    │             │   │
│  │  │ • Bootstrap│  │ • Talos    │  │ • API      │             │   │
│  │  │ • Teardown │  │ • Ubuntu   │  │ • Machines │             │   │
│  │  └────────────┘  └────────────┘  └────────────┘             │   │
│  └──────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────┘
```

---

## Authentication Flow (Kratos-based)

```
┌─────────┐                                                    ┌─────────┐
│  User   │                                                    │ Kratos  │
│ Browser │                                                    │ Server  │
└────┬────┘                                                    └────┬────┘
     │                                                              │
     │  1. GET /auth/login                                         │
     ├────────────────────────────────────────────────────────────>│
     │                                                              │
     │  2. Redirect to OAuth Provider (GitHub/Google)              │
     │<────────────────────────────────────────────────────────────┤
     │                                                              │
     │  3. User authenticates with OAuth Provider                  │
     │                                                              │
     │  4. OAuth callback with code                                │
     ├────────────────────────────────────────────────────────────>│
     │                                                              │
     │  5. Kratos validates, creates session                       │
     │                                                              │
     │  6. Set session cookie + redirect                           │
     │<────────────────────────────────────────────────────────────┤
     │                                                              │
     │  7. Request with session cookie                             │
     ├────────────────────────────────────────────────────────────>│
     │                                                              │
     │  8. Session validated, user context returned                │
     │<────────────────────────────────────────────────────────────┤
     │                                                              │
```

---

## Agent-Driven Cluster Creation Flow

```
┌──────┐    ┌────────┐    ┌──────────┐    ┌──────────┐    ┌─────────┐
│ User │    │ Agent  │    │  Agent   │    │ZeroTouch │    │zero-ops │
│ Chat │    │Gateway │    │  Kratos  │    │ Engine   │    │   CLI   │
└──┬───┘    └───┬────┘    └────┬─────┘    └────┬─────┘    └────┬────┘
   │            │              │               │               │
   │ "Create    │              │               │               │
   │  cluster"  │              │               │               │
   ├───────────>│              │               │               │
   │            │              │               │               │
   │            │ Validate     │               │               │
   │            │ session      │               │               │
   │            ├─────────────>│               │               │
   │            │              │               │               │
   │            │ User context │               │               │
   │            │<─────────────┤               │               │
   │            │              │               │               │
   │            │ Check RBAC   │               │               │
   │            │ (can create  │               │               │
   │            │  cluster?)   │               │               │
   │            │              │               │               │
   │            │ Start        │               │               │
   │            │ workflow     │               │               │
   │            ├──────────────────────────────>│               │
   │            │              │               │               │
   │            │              │  session_id   │               │
   │            │              │  + question   │               │
   │            │<──────────────────────────────┤               │
   │            │              │               │               │
   │ "What's    │              │               │               │
   │  the       │              │               │               │
   │  cluster   │              │               │               │
   │  name?"    │              │               │               │
   │<───────────┤              │               │               │
   │            │              │               │               │
   │ "prod-01"  │              │               │               │
   ├───────────>│              │               │               │
   │            │              │               │               │
   │            │ Submit       │               │               │
   │            │ answer       │               │               │
   │            ├──────────────────────────────>│               │
   │            │              │               │               │
   │            │              │  Next question│               │
   │            │<──────────────────────────────┤               │
   │            │              │               │               │
   │ ... (more questions) ...  │               │               │
   │            │              │               │               │
   │            │              │  Workflow     │               │
   │            │              │  complete     │               │
   │            │<──────────────────────────────┤               │
   │            │              │               │               │
   │            │              │               │ Bootstrap     │
   │            │              │               │ cluster       │
   │            ├──────────────────────────────────────────────>│
   │            │              │               │               │
   │            │              │               │  Cluster      │
   │            │              │               │  created      │
   │            │<──────────────────────────────────────────────┤
   │            │              │               │               │
   │ "Cluster   │              │               │               │
   │  created!" │              │               │               │
   │<───────────┤              │               │               │
   │            │              │               │               │
```

---

## Multi-Tenant Isolation Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                         TENANT A (Org: acme-corp)                    │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │  Users: alice@acme.com, bob@acme.com                         │   │
│  │  ┌────────────┐  ┌────────────┐  ┌────────────┐             │   │
│  │  │ Clusters   │  │ RBAC       │  │ Resources  │             │   │
│  │  │ • prod-01  │  │ • alice:   │  │ • Quota:   │             │   │
│  │  │ • staging  │  │   owner    │  │   10 nodes │             │   │
│  │  │            │  │ • bob:     │  │ • Limit:   │             │   │
│  │  │            │  │   admin    │  │   5 clstrs │             │   │
│  │  └────────────┘  └────────────┘  └────────────┘             │   │
│  └──────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────┐
│                         TENANT B (Org: startup-xyz)                  │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │  Users: charlie@startup.com                                   │   │
│  │  ┌────────────┐  ┌────────────┐  ┌────────────┐             │   │
│  │  │ Clusters   │  │ RBAC       │  │ Resources  │             │   │
│  │  │ • dev-01   │  │ • charlie: │  │ • Quota:   │             │   │
│  │  │            │  │   owner    │  │   5 nodes  │             │   │
│  │  │            │  │            │  │ • Limit:   │             │   │
│  │  │            │  │            │  │   2 clstrs │             │   │
│  │  └────────────┘  └────────────┘  └────────────┘             │   │
│  └──────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────┘

                    ┌─────────────────────────┐
                    │   Agent Gateway         │
                    │   • Tenant isolation    │
                    │   • RBAC enforcement    │
                    │   • Resource quotas     │
                    └─────────────────────────┘
```

---

## 1. Ory Kratos (Identity Management)

### What It Is
Open-source identity and user management system. Self-hosted alternative to Auth0/Cognito with OAuth, MFA, passwordless, and session management.

### Core Capabilities
- **Authentication Methods**: OAuth/OIDC (GitHub, Google, Microsoft), password, passwordless, MFA/2FA (TOTP, WebAuthn)
- **Session Management**: Cookie-based with sliding expiry, Redis/PostgreSQL backend
- **User Management**: Self-service registration, profile updates, account recovery
- **Multi-tenancy**: Organization/workspace isolation via identity traits
- **Admin API**: User CRUD, session management, identity verification
- **Security**: Session fixation prevention, CSRF protection, rate limiting

### Integration Points
```bash
# Kratos Public API (user-facing)
POST   /self-service/login/browser          # Initiate login flow
POST   /self-service/registration/browser   # Initiate registration
GET    /self-service/logout/browser         # Logout
POST   /self-service/recovery/browser       # Password recovery
GET    /sessions/whoami                     # Get current session

# Kratos Admin API (backend integration)
GET    /admin/identities                    # List identities
POST   /admin/identities                    # Create identity
GET    /admin/identities/{id}               # Get identity
DELETE /admin/identities/{id}               # Delete identity
GET    /admin/identities/{id}/sessions      # Get user sessions
```

### Fills PRD Gaps
- **Journey A (Bootstrap)**: User authentication before cluster creation
- **Journey B (Tenant Onboarding)**: Org provisioning via identity traits, user-org mapping
- **Missing from PRD**: Complete authentication flow, MFA, account recovery

### Technology Stack
- Go (high performance, low memory)
- PostgreSQL for identity storage
- Redis for session caching (optional)
- Courier for email/SMS delivery

### Why Kratos Over Custom Identity Service
- **Battle-tested**: Used by Raspberry Pi, Arduino, Segment
- **Compliance-ready**: SOC2 Type II available (Ory Cloud)
- **Feature-rich**: MFA, passwordless, account recovery out-of-box
- **Self-hosted**: Full data ownership, no vendor lock-in
- **Extensible**: Webhooks for custom logic, identity traits for multi-tenancy

---

## 2. ZeroTouch Engine (Python MCP Server)

### What It Is
MCP server implementing workflow DSL for guided infrastructure provisioning. Converts YAML workflows into interactive question/answer sessions with state management.

### Core Capabilities
- **Workflow DSL**: YAML-based question flows with conditional logic
- **MCP Protocol**: FastMCP server with stdio/HTTP transports
- **State Management**: Base64-encoded state blobs for session persistence
- **Platform Handlers**: Adapter, Platform, Render, Bootstrap, Validation
- **CLI Integration**: Python CLI (`ztp`) for workflow execution

### MCP Tools Exposed
```python
# Core workflow tools
start_workflow(workflow_id, workflow_dsl_path) → {session_id, question, state_blob}
submit_answer(session_id, state_blob, answer_value) → {question, state_blob, completed}
restore_session(session_id, state_blob) → {question, state_blob}
restart_workflow(workflow_id, workflow_dsl_path) → {session_id, question}

# Platform handlers (via AdapterHandler, PlatformHandler, etc.)
render_templates()
bootstrap_cluster()
validate_config()
```

### Integration Points
- **MCP Client**: Python client connects via stdio to MCP server
- **Workflow Storage**: Filesystem-based workflow YAML files
- **State Persistence**: JSON state blobs (can be stored in Redis/DB)

### Fills PRD Gaps
- **Journey C (Cluster Provisioning)**: Guided workflow for cluster config
- **Journey D (Service Injection)**: Workflow for service selection
- **Missing from PRD**: MCP integration layer for agent-driven provisioning

### Technology Stack
- Python 3.11+
- FastMCP (official MCP SDK)
- Workflow DSL parser
- Async/await architecture

### Conflicts/Ambiguities
**MINOR OVERLAP**: ZeroTouch Engine has its own CLI (`ztp`) which overlaps with `zero-ops` CLI.

**RESOLUTION**: Use ZeroTouch Engine as MCP server only, not as CLI. Agent Gateway routes MCP calls to it.

---

## 3. Agent Gateway (Rust)

### What It Is
Production-grade data plane for agentic AI connectivity. Routes MCP protocol requests with RBAC, session management, and multi-tenant isolation.

### Core Capabilities
- **MCP Router**: Routes tool/prompt/resource calls to upstream MCP servers
- **RBAC**: CEL-based authorization policies per tool/resource
- **Session Management**: HTTP session persistence with encryption
- **Multi-tenancy**: Namespace isolation, org-level policies
- **Observability**: OpenTelemetry tracing, Prometheus metrics
- **Transport**: HTTP/2, HBONE (mTLS), SSE streaming

### Architecture Components
```rust
// Core modules
mcp/router.rs       // MCP protocol routing
mcp/auth.rs         // OAuth/JWT authentication
mcp/rbac.rs         // Authorization policies
mcp/session.rs      // Session management
proxy/gateway.rs    // HTTP proxy layer
state_manager.rs    // XDS config management
```

### Integration Points
- **Upstream MCP Servers**: Connects to ZeroTouch Engine, other MCP servers
- **Kratos Integration**: Validates sessions via Kratos `/sessions/whoami` endpoint
- **XDS Config**: Dynamic routing rules (like Envoy)
- **Admin API**: `/admin` for config, `/metrics` for Prometheus

### Fills PRD Gaps
- **Journey A-G**: Agent orchestration layer for ALL journeys
- **Missing from PRD**: 
  - MCP protocol gateway
  - Multi-tenant isolation
  - RBAC for infrastructure operations
  - Session management for agent conversations

### Technology Stack
- Rust (Tokio async runtime)
- HTTP/2, HBONE
- OpenTelemetry, Prometheus
- XDS (Envoy-style config)

### Kratos Integration Pattern
```rust
// Agent Gateway validates Kratos session
async fn validate_session(cookie: &str) -> Result<UserContext> {
    let response = kratos_client
        .get("/sessions/whoami")
        .header("Cookie", cookie)
        .send()
        .await?;
    
    let session: KratosSession = response.json().await?;
    
    Ok(UserContext {
        user_id: session.identity.id,
        org_id: session.identity.traits.org_id,
        role: session.identity.traits.role,
    })
}
```

---

## 4. zero-ops CLI (Infrastructure Operations)

### What It Is
CLI tool for management cluster bootstrap and teardown on Hetzner using CAPI (Cluster API) with Talos/Ubuntu support.

### Core Capabilities
- **Management Cluster**: Bootstrap CAPI management cluster on Hetzner
- **OS Support**: Talos Linux, Ubuntu
- **Provider**: Hetzner Cloud API integration
- **Operations**: Bootstrap, teardown, status

### Integration with MCP
Expose zero-ops CLI operations as MCP tools via wrapper:

```python
# ZeroTouch Engine handler calls zero-ops CLI
@mcp.tool()
async def bootstrap_management_cluster(
    cluster_name: str,
    os_type: str,  # "talos" or "ubuntu"
    hetzner_token: str,
    region: str
) -> dict:
    """Bootstrap management cluster via zero-ops CLI"""
    result = subprocess.run([
        "zero-ops", "mgmt", "bootstrap",
        "--name", cluster_name,
        "--os", os_type,
        "--token", hetzner_token,
        "--region", region
    ], capture_output=True)
    
    return {
        "success": result.returncode == 0,
        "output": result.stdout.decode(),
        "error": result.stderr.decode()
    }
```

---

## Integration Architecture Decisions

### Decision 1: Identity Provider
**Choice**: Ory Kratos  
**Rationale**: 
- Self-hosted, no vendor lock-in
- MFA/2FA out-of-box
- Battle-tested (Raspberry Pi, Arduino, Segment)
- Compliance-ready (SOC2 via Ory Cloud)
- Extensible via webhooks and identity traits

**Alternative Rejected**: Custom Identity Service (too much custom code, missing MFA/2FA)

### Decision 2: MCP Server
**Choice**: ZeroTouch Engine  
**Rationale**: Purpose-built for workflow DSL, proven MCP implementation  
**Alternative**: Build custom MCP server (unnecessary duplication)

### Decision 3: Agent Gateway
**Choice**: Agent Gateway (Rust)  
**Rationale**: Production-grade, RBAC, multi-tenancy, observability  
**Alternative**: Build custom gateway (months of work)

### Decision 4: CLI Integration
**Choice**: Expose zero-ops CLI as MCP tools via wrapper  
**Rationale**: Reuse existing CLI, no rewrite needed  
**Alternative**: Rewrite CLI as MCP server (high effort)

---

## Deployment Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                      Kubernetes Cluster                              │
│                                                                       │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │  Namespace: zero-ops-platform                                 │   │
│  │                                                               │   │
│  │  ┌────────────────┐  ┌────────────────┐  ┌────────────────┐ │   │
│  │  │ Agent Gateway  │  │ Ory Kratos     │  │ ZeroTouch      │ │   │
│  │  │ • Deployment   │  │ • StatefulSet  │  │ • Deployment   │ │   │
│  │  │ • 3 replicas   │  │ • 2 replicas   │  │ • 2 replicas   │ │   │
│  │  │ • Service      │  │ • Service      │  │ • Service      │ │   │
│  │  │ • Ingress      │  │ • Ingress      │  │ • Internal     │ │   │
│  │  └────────────────┘  └────────────────┘  └────────────────┘ │   │
│  │                                                               │   │
│  │  ┌────────────────┐  ┌────────────────┐                     │   │
│  │  │ PostgreSQL     │  │ Redis          │                     │   │
│  │  │ • StatefulSet  │  │ • StatefulSet  │                     │   │
│  │  │ • PVC          │  │ • PVC          │                     │   │
│  │  └────────────────┘  └────────────────┘                     │   │
│  └──────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────┘
```

---

## Integration Plan

### Phase 1: Kratos Setup (Week 1-2)
1. Deploy Kratos on Kubernetes
2. Configure OAuth providers (GitHub, Google)
3. Set up PostgreSQL for identity storage
4. Configure identity schema with org_id trait
5. Test authentication flows

### Phase 2: Agent Gateway Integration (Week 3-4)
1. Deploy Agent Gateway
2. Configure Kratos session validation
3. Set up RBAC policies (CEL expressions)
4. Configure MCP routing to ZeroTouch Engine
5. Test end-to-end auth + MCP routing

### Phase 3: Workflow Engine (Week 5-6)
1. Deploy ZeroTouch Engine MCP server
2. Create workflow DSL for cluster provisioning
3. Integrate zero-ops CLI as MCP tools
4. Test agent-driven cluster creation
5. Add state persistence (Redis)

### Phase 4: Production Hardening (Week 7-8)
1. Add observability (traces, metrics, logs)
2. Implement rate limiting per tenant
3. Add audit logging for compliance
4. Load testing and performance tuning
5. Disaster recovery setup

---

## PRD Updates Required

### New Sections to Add

#### 5.X MCP Integration Layer
- Agent Gateway deployment architecture
- MCP protocol routing rules
- RBAC policies for infrastructure operations
- Session management for agent conversations

#### 6.X Authentication & Authorization (Kratos)
- Kratos deployment and configuration
- OAuth flow (GitHub, Google, Microsoft)
- Session lifecycle management
- Multi-tenant organization model via identity traits
- MFA/2FA setup

#### 7.X Workflow Engine
- ZeroTouch Engine MCP server
- Workflow DSL for cluster provisioning
- State management for long-running workflows
- Integration with zero-ops CLI

### Updated Journey Flows

#### Journey A: Bootstrap (Kratos-based)
1. User authenticates via Kratos (OAuth)
2. Kratos creates session, sets cookie
3. User initiates cluster creation via chat
4. Agent Gateway validates Kratos session
5. Agent Gateway routes to ZeroTouch Engine MCP server
6. Workflow DSL guides user through config questions
7. ZeroTouch Engine calls zero-ops CLI to bootstrap
8. Management cluster created on Hetzner

#### Journey B: Tenant Onboarding (Kratos-based)
1. User authenticated (from Journey A)
2. Admin creates organization via Kratos Admin API
3. User identity updated with org_id trait
4. Agent Gateway applies org-level RBAC policies
5. User can now provision clusters in org namespace

---

## Recommended Next Steps

### Immediate Actions
1. **Deploy Kratos locally**: Test OAuth flows, session management
2. **Prototype Agent Gateway + Kratos**: Validate session integration
3. **Create workflow DSL**: Define cluster provisioning workflow
4. **Test end-to-end**: Chat UI → Agent Gateway → ZeroTouch → zero-ops CLI

### Specs to Create
1. **Kratos Integration Spec** (NEW)
   - Requirements: OAuth providers, identity schema, session management
   - Design: Kratos deployment, identity traits for multi-tenancy
   - Tasks: Deploy, configure, integrate with Agent Gateway

2. **MCP Integration Spec** (NEW)
   - Requirements: Agent Gateway deployment, MCP routing, RBAC
   - Design: Architecture diagrams, API contracts, security model
   - Tasks: Deploy, configure, integrate with Kratos

3. **Workflow Engine Spec** (NEW)
   - Requirements: Workflow DSL, state management, MCP server
   - Design: ZeroTouch Engine integration, workflow definitions
   - Tasks: Deploy, create workflows, integrate with zero-ops CLI

4. **Tenant Onboarding Spec** (COMPLETE)
   - Requirements: Org provisioning, user management, RBAC
   - Design: Kratos identity traits, org model
   - Tasks: Admin API integration, RBAC policies

---

## Conclusion

**FINAL ARCHITECTURE**: Kratos + Agent Gateway + ZeroTouch Engine + zero-ops CLI

**Key Benefits:**
- **Self-hosted**: Full control, no vendor lock-in
- **Battle-tested**: Kratos used by major companies
- **Feature-rich**: MFA, passwordless, account recovery
- **Scalable**: Rust gateway, async Python MCP server
- **Compliant**: SOC2-ready via Ory Cloud

**NO CONFLICTS** - All components complement each other perfectly.

**NEXT STEP**: Deploy Kratos locally and test OAuth integration with Agent Gateway.

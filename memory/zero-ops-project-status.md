# Zero-Ops Project Context

## Project Overview
Zero-Ops v8.0 is a SaaS factory platform that provisions complete AI-native SaaS environments via Crossplane XRD. MCP-first architecture with OAuth2/PKCE authentication.

## Critical Files Read
✅ docs/prds/v8/zero-ops-prd-v8.md (complete)
✅ docs/prds/v8/v8-hub-spoke.md (complete)
✅ .kiro/specs/agentic-enterprise-onboarding/requirements.md (complete - 19 requirements)
✅ .kiro/specs/agentic-enterprise-onboarding/zero-ops-sprint-delivery-plan.md (complete)
✅ docs/prds/v8/project-structure.md (complete)
✅ docs/prds/v8/selectable-services.md (complete)
✅ memory/auth-design-principles.md (complete)
✅ memory/design-principles.md (complete)

## Completed Specs

### 1. Management Cluster Bootstrap
**Location:** `.kiro/specs/management-cluster/`
**Status:** ✅ COMPLETE (All 12 phases)
**What's Done:**
- Full CLI implementation for bootstrapping Talos Linux management cluster
- CAPI/CAPH/Talos provider installation via cluster-api-operator
- Pivot from Kind to self-hosted management cluster
- ClusterClass library deployment (Ubuntu-based)
- ArgoCD, capi2argo, CloudNativePG installation
- Teardown and upgrade commands

### 2. Zero-Ops API (Core Tenant CRUD)
**Location:** `.kiro/specs/tenant-onboarding/zero-ops-api/`
**Status:** ✅ MOSTLY COMPLETE
**What's Done:**
- PostgreSQL schema with migrations (tenants table)
- sqlc query generation (UpsertTenant, GetTenant, ListTenants, UpdateTenant, SoftDeleteTenant)
- Service layer with tenant CRUD business logic
- HTTP handlers with Gin framework
- Middleware (logging, error handling, metrics, recovery)
- All 4 E2E BDD test suites with Testcontainers-Go and DB assertions
**What's Missing:**
- Documentation (README, API usage examples)
- Deployment artifacts (Dockerfile, docker-compose.yml, K8s manifests)

### 3. Agentic Enterprise Onboarding
**Location:** `.kiro/specs/agentic-enterprise-onboarding/`
**Status:** 🔄 REQUIREMENTS COMPLETE, WORKING ON DEMOS
**Workflow:** Requirements-first (confirmed via .config.kiro)
**What's Done:**
- ✅ requirements.md complete (19 requirements covering full OAuth2/PKCE flow)
- ✅ Tenant lifecycle state machine defined
- ✅ Idempotent operations throughout
- ✅ Async agent pattern (no blocking/polling)
- ✅ Security-first (credentials never transit agent)
**Current Work:**
- 🔄 Demo 1 Spec (OAuth2 PKCE flow) - design.md COMPLETE
  - Location: `.kiro/specs/agentic-enterprise-onboarding/demos/demo1-spec/`
  - Outcome: "The platform knows who you are"
  - Scope: Requirements 2, 10, 12, 13 (partial - pre-registration only)
  - Components: CNPG, Ory Hydra/Kratos/Keto, auth-proxy, AgentGateway, demo-echo
  - Architecture: Multi-namespace (ory-system, identity-services, api-gateway)
  - Key patterns: OIDC pass-through, JWKS caching, return_to login flow, headless consent
- ✅ Agent System - /agent swap zero-ops-orchestrator
**What's Missing:**
- ❌ Demo 1 tasks.md (NEXT STEP)
- ❌ Demo 2+ specs (after Demo 1 complete)
- ❌ Main spec design.md and tasks.md (after all demos)
**Key Requirements:**
- OAuth2 Authorization Code Flow with PKCE (RFC 7636)
- AgentGateway as single auth enforcement point
- auth-proxy for JWT validation and OAuth orchestration
- Crossplane Composition B for Enterprise provisioning
- KSOPS + Age for secret management
- GitOps-first (all changes via Git commits)
- Eventual consistency model

## Next Steps for Agentic Enterprise Onboarding
1. Create Demo 1 tasks.md based on completed design.md
2. Execute Demo 1 tasks (infrastructure → services → integration → demo)
3. Create Demo 2+ specs following same pattern
4. Create main spec design.md and tasks.md after all demos complete

## Key Architecture Patterns
- **MCP-first:** All platform capabilities exposed via MCP tools
- **GitOps-first:** All mutations via Git commits, ArgoCD reconciles
- **Idempotent operations:** Safe to retry infinitely
- **Async agent pattern:** Agent submits intent and exits, no polling
- **Eventual consistency:** Crossplane reconciles continuously, no terminal failures
- **BYOC model:** Tenants provide cloud credentials, compute runs in their account

# Zero-Ops Project Context

## Project Overview
Zero-Ops v8.0 is a SaaS factory platform that provisions complete AI-native SaaS environments via Crossplane XRD. MCP-first architecture with OAuth2/PKCE authentication.

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
**Status:** 🔄 REQUIREMENTS COMPLETE, NEEDS DESIGN
**Workflow:** Requirements-first (confirmed via .config.kiro)
**What's Done:**
- ✅ requirements.md complete (19 requirements covering full OAuth2/PKCE flow)
- ✅ Tenant lifecycle state machine defined
- ✅ Idempotent operations throughout
- ✅ Async agent pattern (no blocking/polling)
- ✅ Security-first (credentials never transit agent)
**What's Missing:**
- ❌ design.md (NEXT STEP)
- ❌ tasks.md (after design)
**Key Requirements:**
- OAuth2 Authorization Code Flow with PKCE (RFC 7636)
- AgentGateway as single auth enforcement point
- identity-service abstraction layer for Ory stack
- Crossplane Composition B for Enterprise provisioning
- KSOPS + Age for secret management
- GitOps-first (all changes via Git commits)
- Eventual consistency model

## Next Steps for Agentic Enterprise Onboarding
1. Check if design.md exists - if not, create it
2. Check if tasks.md exists - if not, create it after design
3. Begin implementation starting from Phase 1 tasks

## Key Architecture Patterns
- **MCP-first:** All platform capabilities exposed via MCP tools
- **GitOps-first:** All mutations via Git commits, ArgoCD reconciles
- **Idempotent operations:** Safe to retry infinitely
- **Async agent pattern:** Agent submits intent and exits, no polling
- **Eventual consistency:** Crossplane reconciles continuously, no terminal failures
- **BYOC model:** Tenants provide cloud credentials, compute runs in their account

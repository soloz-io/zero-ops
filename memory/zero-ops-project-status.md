---
purpose: Zero-Ops project status and progress tracking
scope: High-level project milestones, completed specs, current work focus
topics: [project-overview, completed-specs, current-focus, user-preferences]
update_criteria: Major milestone completion, spec status changes, focus shifts, user preference changes
---

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

### 1. Management Cluster Bootstrap (Hub Cluster)
**Location:** `.kiro/specs/management-cluster/`
**Status:** ✅ COMPLETE AND OPERATIONAL
**Cluster Name:** `mothership`
**Infrastructure:**
- 3 control plane nodes (Hetzner, Ubuntu 24.04, k8s v1.31.6)
- 2 worker nodes
- CAPI/CAPH/Kubeadm providers in `capi-operator-system`
- ArgoCD with ApplicationSet controller in `argocd` namespace
- CloudNativePG operator in `cnpg-system`
- ClusterClass: `hetzner-mgmt-ubuntu-v1` (in zero-ops-system namespace)
**What's Done:**
- Full CLI implementation for bootstrapping Ubuntu management cluster
- CAPI/CAPH/Kubeadm provider installation via cluster-api-operator
- Pivot from Kind to self-hosted management cluster
- ClusterClass library deployment (Ubuntu-based)
- ArgoCD, capi2argo, CloudNativePG installation
- Teardown and upgrade commands
**Hub Bootstrap Code:** `zero-ops/internal/hub/` (Go implementation)
- Bootstrap orchestrator with recovery/resume capability
- Component installer (ArgoCD via Helm, capi2argo, CNPG)
- CAPI operator installation and secret management
- ClusterClass deployer
- Pivot orchestrator for CAPI resource migration

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
- ✅ Demo 1 Spec (OAuth2 PKCE flow) - COMPLETE AND OPERATIONAL
  - Location: `.kiro/specs/agentic-enterprise-onboarding/demos/demo1-spec/`
  - Outcome: "The platform knows who you are"
  - Scope: Requirements 2, 10, 12, 13 (partial - pre-registration only)
  - Components: CNPG, Ory Hydra/Kratos/Keto, auth-proxy, AgentGateway, demo-echo
  - Architecture: Multi-namespace (ory-system, identity-services, api-gateway)
  - Key patterns: OIDC pass-through, JWKS caching, return_to login flow, headless consent
  - ✅ Task 1: Database infrastructure and Ory stack foundation (manifests complete)
  - ✅ Task 2: auth-proxy service core functionality (OAuth client registration, metadata proxy)
  - ✅ Task 3: PKCE authentication flow handlers (login challenge, consent flow)
  - ✅ Task 4: JWT validation and extAuthz endpoint (JWKS caching, validation logic)
  - ✅ Task 5: AgentGateway deployment and configuration (extAuthz integration, demo-echo)
  - ✅ Task 6: Ingress, DNS, and TLS configuration (HTTPS termination, Kratos UI, local dev setup)
  - ✅ Task 7: Deployment manifests and secrets management (RBAC, NetworkPolicies, deploy script)
  - ✅ Task 8: Startup ordering and health checks (COMPLETE - MCP integration working)
  - ✅ DEMO SUCCESS: MCP server connected to Kiro, tenant_list working, authentication flow operational
- ✅ Agent System - /agent swap zero-ops-orchestrator

### 4. Hub-Spoke MVP (Phase 1)
**Location:** `.kiro/specs/hub-spoke-mvp/`
**Status:** 🔄 REQUIREMENTS COMPLETE, DESIGN PHASE STARTING
**Workflow:** Requirements-first (confirmed)
**What's Done:**
- ✅ requirements.md complete (30 requirements covering Phase 1 MVP)
- ✅ Hub cluster verified operational (mothership - 3 CP + 2 workers)
- ✅ CAPI/CAPH/Kubeadm providers confirmed running
- ✅ ArgoCD confirmed installed and part of hub orchestration
- ✅ CloudNativePG operator confirmed running
**Phase 1 MVP Scope:**
1. Tenant onboarding (manual GitHub repo creation)
2. Dedicated spoke provisioning (CAPI)
3. ArgoCD agent deployment
4. Fleet registry + ApplicationSets
5. Basic observability
**Key Architectural Decisions:**
- MCP-only interaction (no CLI/UI for tenant operations)
- Fleet registry in monorepo (`zero-ops/fleet-registry/`)
- VictoriaMetrics + Grafana to be added to hub bootstrap postboot phase
- Tenant onboarding API extends existing `zero-ops-api`
- New ClusterClass templates: `hetzner-prod-ubuntu-v1`, `hetzner-staging-ubuntu-v1`
- Grafana Alloy for spoke metrics collection (basic push metrics)
**Missing Components Identified:**
- VictoriaMetrics (observability stack)
- Grafana (dashboards)
- Fleet registry Git structure
- Tenant onboarding MCP server
- Spoke-specific ClusterClass templates

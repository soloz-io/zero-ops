# Zero-Ops Platform — Product Requirements Document

**Version:** 8.0 + 8.1 — SaaS Factory: AINativeSaaS Template, Platform Console & Tenant Onboarding  
**Status:** DRAFT  
**Project:** zero-ops  
**Repo Model:** Go-Centric Monorepo  
**Author:** Platform Architecture Team  
**Date:** 2026  
**Classification:** CONFIDENTIAL — INTERNAL USE ONLY  
**Supersedes:** v7.0 (Open Source Fleet Observability Stack & Correlation Engine)

---

## Interface Priority & Development Approach

**Zero-Ops v8.0 is an MCP-first agentic platform.** All platform capabilities are designed and tested through the Model Context Protocol (MCP) interface first, enabling AI-native interactions via IDEs like Cursor, Claude Desktop, and other MCP-compatible clients.

**Development Priority:**
1. **MCP Interface** — Primary interface for all operations. MCP tool servers expose platform capabilities to AI agents and IDE integrations.
2. **CLI** — Secondary interface for automation, scripting, and CI/CD pipelines.
3. **Web UI (Platform Console)** — Tertiary interface for monitoring, approval workflows, and human oversight.

This approach ensures the platform is optimized for agentic workflows from day one, with traditional interfaces built as convenience layers on top of the MCP foundation.

---

## Changelog: v8.0 → v8.1

| Area | v8.0 State | v8.1 Change |
|---|---|---|
| Cluster topology terminology | "Mothership", "management cluster", and "shard" used interchangeably with no definition or reconciliation | **Clarified in Glossary (new section) and Journey B.** All three terms defined and confirmed as synonyms. Bootstrap sequence (Kind → management cluster) made explicit. |
| Management cluster vs. tenant cluster | Implicit conflation — the management cluster was not clearly distinguished from Enterprise tenant clusters | **Clarified:** management cluster is the platform control plane, bootstrapped imperatively via CLI. Enterprise tenant clusters are provisioned by Composition B in the tenant's own Hetzner account. They are different things at different layers. |
| Shared cluster placement | "Zero-Ops Shared Cluster" referenced in diagram and Journey C but never defined or placed relative to the management cluster | **Clarified in Glossary and diagram:** the shared cluster is a dedicated CAPI cluster, separate from the management cluster. Starter tenants are namespaces within it — they are not placed on the management cluster. |
| Starter tier delivery scope | Journey C and Composition A described without indicating they are not in scope for the current delivery milestone | **Non-Goal added (§2.6) and scope note added to Journey C.** Starter tier is defined for architectural completeness but is not in scope for v8.0 delivery. All current engineering targets Enterprise (Journey A, Composition B). |
| Duplicate Ory Keto row | Two identical Ory Keto rows in the components table (copy-paste artifact) | **Fixed:** duplicate row removed. |

---

## Changelog: v7.0 → v8.0

| Area | v7.0 State | v8.0 Change |
|---|---|---|
| Tenant definition | A tenant is a Kubernetes cluster consumer | **Dual scope:** a tenant is either the Zero-Ops platform team itself OR an external SaaS company (e.g. Replit, Lovable) building AI-native products. Same provisioning procedure for both. |
| Provisioning model | CAPI ClusterClass — one cluster per intent | **Crossplane XRD + Composition** — one `AINativeSaaS` CR expands into full stack: Kubernetes cluster + PostgreSQL HA + GitOps + observability + auth + secrets + storage + AI Gateway + AgentSandbox |
| Node OS | Talos Linux — removed due to CACPPT/ClusterClass incompatibility | **Ubuntu (kubeadm)** via `KubeadmControlPlaneTemplate` — full ClusterClass topology support. Additional OS support deferred to future version. |
| Tenant tier model | Single cluster tier | **Starter** (shared cluster, namespace isolation, logical DB separation) and **Enterprise** (dedicated cluster, physical isolation) — two Crossplane Compositions, one XRD |
| Platform console | Not defined | **New deliverable** — single RBAC-scoped console for platform team and all tenants. Read-only monitoring and observability. All write actions via "Resolve in IDE" button that generates deep links to IDE/MCP clients. |
| Identity & auth | Not defined | **Ory Kratos + Hydra + Keto** (platform-level, shared, one instance) handles all identity: platform team, tenant admins, tenant end-users. Tenant-level identity isolated via `tenant_id` in Kratos traits. |
| MCP/A2A gateway | LLM Gateway (proxy only) | **AgentGateway** (CNCF open source, Rust) — A2A + MCP communications, JWT validation via Hydra. Single auth enforcement point for all MCP tool calls. |
| Data services in XRD | PostgreSQL only | **Full AI-native data stack** in XRD baseline: CNPG HA, pgvector, PgBouncer, PostgREST, Hetzner S3, Infisical secret management, External Secrets Operator, CSI snapshots, AgentSandbox (gVisor), CNPG ScheduledBackup, AI Gateway (LiteLLM), Teleport PAM |
| Autopilot | Not defined | **Autopilot mode** — platform raises PRs to tenant control plane repo for all proposed changes. Tenant approves or rejects. No silent mutations to tenant infrastructure under any mode. |
| PR environments | Not defined | **Ephemeral PR environments** — Argo Workflow creates per-branch namespace with single-node CNPG bootstrapped from staging CSI snapshot. Torn down on PR close. |
| Provider switching | Single provider (Hetzner) | **Blue-green provider migration** — `spec.cloud` field change triggers new cluster provisioning + CNPG PITR restore + DNS cutover. Old cluster retained for rollback window. |
| cnpg2monitor scope | Management cluster only | **Fleet-wide operator** — promoted to ClusterRole, all-namespace watch. Prerequisite for AINativeSaaS template. Every tenant environment monitored from day one. |
| Monorepo structure | No `xrds/` directory | **`xrds/` added** — Crossplane XRD definitions and Compositions as first-class platform artifacts |

---

## Glossary

The following terms appear throughout this document. Definitions are provided here to prevent ambiguity, particularly around cluster types.

| Term | Definition |
|---|---|
| **Management Cluster** | The Zero-Ops platform control plane cluster. Provisioned once per region via `zero-ops mgmt bootstrap` (CLI, imperative, Kind pivot). Runs: Crossplane, ArgoCD, Ory stack, zero-ops-api, Platform Console, VictoriaMetrics, OpenSearch, cnpg2monitor, fleet-registry. **Not a tenant environment. Not provisioned by Composition A or B.** |
| **Spoke Cluster or Satellite Cluster** | A cluster managed by a Management Cluster. Can be either:
- Shared Cluster (multi-tenant)
- Enterprise Tenant Cluster (single tenant)
| **Mothership or Hub Cluster** | Synonym for Management Cluster. |
| **Shard** | Synonym for Management Cluster, used when referring to one instance within a multi-region fleet (e.g. `shard-eu-1`, `shard-us-1`). Each shard is an independently bootstrapped management cluster. |
| **Shared Cluster** | A spoke cluster provisioned by the Management Cluster to host multiple
Starter-tier tenants as isolated namespaces. This cluster is not the
management cluster and contains only tenant workloads. _(Composition A target. Out of scope for v8.0 delivery.)_ |
| **Enterprise Tenant Cluster** | A dedicated CAPI/CAPH cluster provisioned in the **tenant's own Hetzner account** (BYOC) by Crossplane Composition B. One per Enterprise tenant. Physically isolated from all other tenants and from the management cluster. |
| **Control Plane DB** | The `{tenant}-controlplane` CNPG database provisioned per tenant. Contains: tenant config, agent memory (pgvector), autopilot PR history, billing records. Managed by Zero-Ops. |
| **Data Plane DB** | The `{tenant}-dataplane` CNPG database provisioned per tenant. Contains the tenant's SaaS application data. Schema owned and managed by the tenant. Zero-Ops has no visibility into its contents. |
| **BYOC** | Bring Your Own Cloud. Tenants supply their own cloud provider API credentials. All tenant compute runs in their cloud account. Zero-Ops has no billing relationship with the tenant's cloud provider. |
| **Composition A** | Crossplane Composition for `spec.tier: starter`. Provisions a namespace, RLS-scoped databases, and baseline services within the Shared Cluster. _(Out of scope for v8.0 delivery.)_ |
| **Composition B** | Crossplane Composition for `spec.tier: enterprise`. Provisions a full dedicated CAPI cluster, two CNPG clusters, S3 bucket, Infisical secrets project, and all baseline services in the tenant's Hetzner account. Uses standard ArgoCD (not ArgoCD Agent) for GitOps. |

---

## 1. Executive Summary

Zero-Ops v8.0 transforms the platform from a fleet management tool into a **SaaS factory** — a self-service infrastructure platform that provisions complete, production-grade AI-native SaaS environments on demand, in a single declarative click, for both the Zero-Ops platform team and external SaaS builders.

The primary product is the `AINativeSaaS` Crossplane XRD: a single declarative object that expands into a full-stack AI-native environment comprising Kubernetes, PostgreSQL HA, vector search, object storage, secret management, observability, GitOps, sandboxed agent runtime, authentication, and AI model gateway. The Zero-Ops platform team is itself a tenant of this template — its own control plane runs on the same `AINativeSaaS` composition as every external SaaS builder.

The platform targets two personas: **SaaS Builders** (companies like Replit, Lovable, or Emergent who need a production-grade backend stack to build their product on) and the **Zero-Ops Platform Team** (who uses the same template to operate the platform itself). Both interact through a single, RBAC-scoped **Platform Console** backed by a unified identity layer (Ory Kratos + Hydra + Keto).

**Core constraints:** BYOC model (tenant provides Hetzner API credentials, compute runs in their account), GitOps-first (all mutations via Git and ArgoCD, no imperative API-direct cluster writes), no vendor lock-in (provider switching is a one-click blue-green migration), and autopilot-with-consent (agents propose changes via PRs, tenants approve — no silent mutations under any mode).

---

## 2. Problem Statement

### 2.1 Resolved in v7.0

The GitOps bottleneck (v4.0), observation gap (v5.0), five production-grade gaps (v6.0), single-agent ceiling (v6.0), and three fleet correlation gaps (v7.0) are resolved. The v7.0 architecture is the operational foundation for v8.0. All v7.0 components remain in scope and unchanged unless explicitly noted in the v8.0 changelog.

### 2.2 The Provisioning Ceiling _(NEW — v8.0)_

The v7.0 architecture can provision a Kubernetes cluster. It cannot provision a **SaaS company's entire backend**. An AI-native SaaS builder needs not just compute but a complete, opinionated, pre-integrated data and runtime stack:

| What a SaaS builder needs | v7.0 state | v8.0 resolution |
|---|---|---|
| Kubernetes cluster | ✅ CAPI ClusterClass | ✅ AINativeSaaS XRD (Crossplane) |
| PostgreSQL HA | Separate, manual | ✅ CNPG in XRD baseline |
| Vector search (AI features) | Not provided | ✅ pgvector extension in XRD |
| Object storage | Not provided | ✅ Hetzner S3 bucket in XRD |
| Secret management | Not provided | ✅ Infisical + External Secrets Operator in XRD |
| GitOps wiring | Partial (CRS inject) | ✅ ArgoCD Application CR in XRD |
| Observability | Alloy + VictoriaMetrics | ✅ Same, per-tenant topology labels |
| Agent runtime (AI coding) | Not provided | ✅ AgentSandbox (gVisor) in XRD |
| Auth for end-users | Not provided | ✅ Ory Kratos shared instance |
| AI model gateway | LLM Gateway (internal) | ✅ LiteLLM AI Gateway per tenant |
| Automated DB backup | Not provided | ✅ CNPG ScheduledBackup in XRD |
| TLS + DNS | Not provided | ✅ cert-manager + external-dns in XRD |
| Auto REST API | Not provided | ✅ PostgREST per tenant |

### 2.3 The Identity Gap _(NEW — v8.0)_

The v7.0 platform has no defined identity or authentication layer. There is no defined mechanism for: tenant admin login to the platform console, MCP client authentication against AgentGateway, API token lifecycle management, or end-user identity for tenant SaaS applications.

### 2.4 The Console Gap _(NEW — v8.0)_

The v7.0 platform console is referenced in user journeys (CNPG ticket display, "Resolve in Cursor" button) but never defined as a component. Platform team and tenants have no defined operational UI. The human approval workflow for destructive operations has no concrete implementation path.

### 2.5 The Tenant Ceiling _(NEW — v8.0)_

The v7.0 architecture has one implicit tenant class: a Kubernetes cluster consumer. This conflates the Zero-Ops platform team (who needs a management control plane to serve customers) with external SaaS builders (who need a full application backend). The v8.0 `AINativeSaaS` template resolves this by making the provisioning unit a complete SaaS environment, not a bare cluster.

### 2.6 Constraints & Non-Goals (v8.0 additions to v7.0)

- **BYOC Model preserved:** Tenants provide their own Hetzner API credentials. Compute always runs in the tenant's cloud account. Zero-Ops never holds billing responsibility for tenant compute.
- **GitOps-first, no imperative writes:** The `zero-ops-api` commits CRs to Git. ArgoCD applies them. No direct Kubernetes API writes from the API server except during bootstrap.
- **Autopilot-with-consent:** In autopilot mode the platform agent raises a PR to the tenant's control plane repository. The tenant approves or rejects. The platform never silently mutates tenant infrastructure.
- **No vendor lock-in:** The `AINativeSaaS` XRD schema is cloud-provider agnostic. `spec.cloud: hetzner` is the v1 default. Provider switching is a blue-green migration with CNPG PITR restore. No proprietary data formats or cloud-specific dependencies in the control plane.
- **Non-Goal:** Starter tier onboarding (Journey C) and Composition A in the v8.0 delivery milestone. Both are defined in this document for architectural completeness, but all current engineering effort targets the Enterprise tier (Journey A, Composition B). Starter is a follow-on milestone.
- **Non-Goal:** Talos Linux support in v8.0. CACPPT does not ship `TalosControlPlaneTemplate`, which is required for ClusterClass topology. Ubuntu + kubeadm is the v8.0 default. Talos support is a future composition variant.
- **Non-Goal:** Per-tenant Supabase Auth (GoTrue) instances. Ory Kratos provides all identity with `tenant_id` isolation. Supabase's role is limited to PostgREST (auto REST API) and Storage API (S3-backed object storage interface) per tenant.
- **Non-Goal:** Full Supabase stack deployment. Individual Supabase components are adopted selectively. The full Supabase self-hosted Helm chart is not a platform dependency.
- **Non-Goal:** GPU node pools in v8.0. AI workloads are served via LiteLLM AI Gateway routing to third-party inference providers (OpenAI, Anthropic, Together, Groq). On-cluster GPU inference is a future `GPUNativeSaaS` composition.
- **Non-Goal:** StandardSaaS or DevSandbox templates in v8.0. One template: `AINativeSaaS`. Tier differentiation is expressed via `spec.tier` fields within the same XRD.

---

## 3. User Personas & User Journeys

### 3.1 Personas (v8.0 — updated)

| Persona | Role | Primary Interface | Tier |
|---|---|---|---|
| **Platform Admin** | Bootstraps the management cluster (mothership/shard) and additional regional shards. Manages global catalog, runbook corpus, global Ory stack, Crossplane XRDs | CLI + Admin API + Platform Console | N/A (platform team) |
| **Tenant Admin** | Onboards their organization onto Zero-Ops, provisions and manages their AINativeSaaS environments, manages their team's access | Platform Console + REST API + Goose (or any MCP client) | Starter or Enterprise |
| **Tenant Developer** | Builds the SaaS product within the provisioned environment. Opens PRs, triggers PR environments, accesses agent conversation, reads Grafana dashboards | Platform Console (read) + Git + Goose | Starter or Enterprise |
| **Platform Agent (AI)** | Programmatic actor using MCP-compatible client (Goose reference implementation) to express infrastructure intents | AgentGateway MCP interface | N/A |
| **On-Call Engineer** | Reviews and approves destructive operation tickets. Clicks "Resolve in Cursor" for agent-assisted remediation. Manages autopilot PR approvals. | Platform Console + Cursor (MCP) | N/A (platform team or tenant) |
| **Fleet Observer** | Reads fleet-wide dashboards for capacity planning, cost analysis, anomaly triage | Fleet State API + Platform Console + Grafana | N/A |

### 3.2 User Journeys

#### Journey A: Tenant Onboarding — Agentic Headless Flow _(Tenant Admin / Goose)_

**Trigger:** A new SaaS company (Acme Corp) wants to provision a production environment on Zero-Ops.

1. Tenant Admin opens Goose (or any MCP-compatible client) and types: `"Onboard Acme Corp on the Enterprise plan in eu-central-1."`
2. Goose maps this to `tenant_create` intent and calls the AgentGateway MCP endpoint.
3. **AgentGateway** detects missing JWT. Returns `401 Unauthorized` + `WWW-Authenticate` header pointing to Hydra's authorization endpoint (Authorization Code + PKCE flow).
4. Goose initiates OAuth 2.1 Authorization Code Flow with PKCE: generates code_verifier, computes code_challenge, opens system browser to authorization URL with PKCE parameters.
5. Tenant Admin completes browser login via Ory Kratos (email/password, SSO, or passkey).
6. Hydra issues a signed JWT to Goose. Goose retries the `tenant_create` MCP call automatically.
7. `zero-ops-api` validates the JWT (via AgentGateway), creates PostgreSQL records for `tenant-acme-corp`, generates Hetzner API credential ingestion prompt.
8. Tenant Admin provides Hetzner API token via the console (not via Goose — credentials never transit the agent).
9. `zero-ops-api` commits an `AINativeSaaS` CR to the tenant's control plane repository:
    ```yaml
    apiVersion: nutgraf.in/v1
    kind: AINativeSaaS
    metadata:
      name: acme-corp-production
      namespace: zero-ops-tenants
    spec:
      tier: enterprise
      cloud: hetzner
      region: eu-central-1
      database:
        instances: 3
        storage: 100Gi
      autoscaling:
        min: 3
        max: 50
    ```
10. ArgoCD (management cluster) detects the commit. Crossplane Composition B (Enterprise) begins expanding the CR.
11. Within 10–15 minutes: Kubernetes cluster provisioned, CNPG HA online, all baseline services healthy. Platform console shows Acme Corp's environment as `Ready`.
12. Goose displays success summary: cluster endpoint, PostgreSQL connection string reference (Secret name — not the value), ArgoCD dashboard URL, Grafana tenant URL, API token.

**Failure — Hetzner quota exceeded:**
At step 10, CAPI resource provisioning fails with quota error. Crossplane surfaces the error as a Condition on the `AINativeSaaS` CR. Platform console shows `Provisioning Failed: Hetzner quota exceeded in eu-central-1`. Tenant Admin sees the alert in console and is prompted to request a quota increase or switch to a different region.

---

#### Journey B: Fleet Shard Bootstrap _(Platform Admin)_ _(unchanged from v7.0 Journey A)_

**Trigger:** Zero-Ops platform team needs a new management cluster (mothership/shard). This journey is also the very first thing run for any Zero-Ops deployment — it creates the platform's own control plane before any tenant can be onboarded. Additional shards can be bootstrapped identically for regional expansion.

**What a shard is:** A management cluster (shard/mothership) is the Zero-Ops control plane — the cluster that hosts Crossplane, ArgoCD, the Ory identity stack, zero-ops-api, and the Platform Console. It is bootstrapped imperatively via the CLI (using a local ephemeral Kind cluster as a CAPI pivot point) and then becomes self-hosted on Hetzner. It is **not** a tenant environment and is **not** provisioned by Composition A or B. Enterprise tenant clusters are provisioned **by** the management cluster, not alongside it.

1. Admin runs: `zero-ops mgmt bootstrap --name=shard-eu-1 --region=fsn1`
2. CLI creates a local ephemeral Kind cluster, uses it to bootstrap CAPI/CAPH, provisions a permanent Ubuntu management cluster on Hetzner, pivots CAPI state to that cluster, then tears down the local Kind cluster. Installs Crossplane, ArgoCD, CNPG operator, cnpg2monitor (ClusterRole scope), Ory stack.
3. CLI registers `shard-eu-1` with `zero-ops-api`. Shard begins emitting 30s heartbeats.
4. Fleet State Engine begins receiving signals within 60s.

---

#### Journey C: Starter Tenant — Shared Cluster Onboarding _(Tenant Admin)_

> **⚠ Out of scope for v8.0 delivery.** Journey C and Composition A are defined here for architectural completeness. All current engineering effort targets Journey A (Enterprise). The Shared Cluster referenced below is a **dedicated CAPI cluster separate from the management cluster** — Starter tenants are not placed on the management cluster itself. See Glossary for definitions.

**Trigger:** A solo developer or small team onboards on the Starter plan.

1. Tenant Admin logs in to Platform Console via Ory Kratos. Views `New Environment` options (Starter/Enterprise).
2. Platform console shows read-only cost estimate derived from Hetzner pricing API: e.g. `~$12/month`. "Resolve in IDE" button displayed.
3. Tenant Admin clicks "Resolve in IDE". Console generates deep link: `cursor://resolve?action=create_environment&tier=starter`. Cursor/Goose receives link and calls `environment_create` MCP tool.
4. Crossplane Composition A (Starter) creates: Kubernetes namespace in Zero-Ops shared cluster, shared CNPG database with `tenant_id`-scoped RLS, shared Hetzner S3 prefix, shared AgentSandbox pool allocation, ResourceQuota + LimitRange for the namespace.
5. Provisioning completes in seconds (no new VMs needed). Tenant Admin's console shows environment `Ready`.
6. Tenant Admin accesses their environment: PostgREST endpoint, ArgoCD application, Grafana dashboard (tenant-scoped), and agent conversation interface.

---

#### Journey D: Starter → Enterprise Upgrade _(Tenant Admin)_

**Trigger:** Acme Corp on Starter plan has grown and needs physical isolation.

1. Tenant Admin opens Platform Console, views `Upgrade to Enterprise` option (read-only).
2. Console shows cost diff and 5–15 minute migration window estimate (blue-green, no downtime). "Resolve in IDE" button displayed.
3. Tenant Admin clicks "Resolve in IDE". Console generates deep link: `cursor://resolve?action=upgrade_tier&tier=enterprise`. Cursor/Goose calls `tenant_update` MCP tool.
4. Crossplane detects spec drift. Triggers Composition B (Enterprise):
    - New dedicated CAPI cluster provisioned on tenant's Hetzner account.
    - New dedicated CNPG cluster bootstrapped via `pg_dump` restore from shared CNPG instance (cross-instance migration).
    - DNS updated to point to new cluster (blue-green cutover).
    - Old namespace in shared cluster retained for rollback window (configurable, default: 2 hours).
5. Console shows migration progress. Once new cluster confirms healthy, old namespace decommissioned.
6. Tenant Admin's environment is now physically isolated. No data loss. No downtime.

---

#### Journey E: PR Environment Lifecycle _(Tenant Developer)_

**Trigger:** Tenant Developer opens a PR on branch `feature/new-ai-pipeline` in their application repository.

1. Argo Workflow fires on branch creation event (GitHub/GitLab webhook).
2. Workflow creates namespace `pr-feature-new-ai-pipeline` within the tenant's cluster (or shared cluster namespace for Starter).
3. Within the namespace, Workflow provisions:
    - Single-node CNPG cluster bootstrapped from latest staging CSI snapshot (restore time: ~60s for typical database).
    - Tenant application deployment built from the PR branch image.
    - Ephemeral Hetzner S3 prefix scoped to this PR.
    - AgentSandbox ephemeral volume (restored from tenant's sandbox snapshot).
4. PR environment URL injected as a GitHub status check comment on the PR.
5. Developer merges or closes PR.
6. Argo Workflow teardown fires: namespace deleted, CNPG cluster deleted, S3 prefix purged, cost billing record closed.

**Failure — CSI snapshot unavailable:**
Workflow detects no staging snapshot exists. Falls back to empty CNPG cluster with schema-only bootstrap from latest migration. Emits warning to PR comment: `"PR environment using empty database — no staging data available."`

---

#### Journey F: Autopilot PR Approval _(Tenant Admin / On-Call Engineer)_

**Trigger:** VictoriaMetrics detects CNPG cluster on `acme-corp-production` has been running at 85% connection pool saturation for 20 minutes. DiagnosticsAgent identifies PgBouncer pool size increase as correct remediation.

1. DiagnosticsAgent queries OpenSearch event timeline: no recent change event correlates.
2. DiagnosticsAgent retrieves platform SOP runbook for `pg_pgbouncer_pool_saturation`.
3. Collaborator Agent classifies the remediation as `non-destructive` (configuration change, no data loss risk).
4. **Autopilot mode (if enabled):** Agent commits a PR to the tenant's control plane repository: `"Increase PgBouncer default_pool_size from 20 to 40 on acme-corp-production"` with full context: metric graph, runbook reference, change diff.
5. Tenant Admin (or On-Call Engineer) reviews the PR in their Git interface or Platform Console. Approves.
6. ArgoCD applies the change. PgBouncer restarts with new pool size. VictoriaMetrics confirms metric normalisation within 5 minutes.
7. Change event written to OpenSearch `infra-changes` index. Audit log updated.

**No autopilot (default):** Step 4 becomes an alert in Platform Console with "Resolve in Cursor" button and structured MCP context. Tenant resolves manually or invites the agent to propose a fix via Goose.

---

#### Journey G: Provider Migration — Hetzner → AWS _(Tenant Admin)_

**Trigger:** Tenant decides to migrate `acme-corp-production` from Hetzner to AWS.

1. Tenant Admin views `Migrate Provider → AWS` in Platform Console (read-only). Console shows estimated migration window and confirms CNPG PITR restore will be used. "Resolve in IDE" button displayed.
2. Tenant Admin clicks "Resolve in IDE". Console generates deep link with migration parameters. Cursor/Goose prompts for AWS credentials (secure input in IDE), calls `provider_migrate` MCP tool.
3. Crossplane detects spec change. Triggers blue-green migration:
    - New CAPI cluster provisioned on AWS using AWS Composition.
    - CNPG takes a base backup on the Hetzner cluster. New CNPG cluster on AWS bootstrapped via PITR from that backup.
    - Once new AWS cluster is `Ready` and CNPG primary is confirmed healthy: DNS cutover.
    - Hetzner cluster retained for rollback window.
4. After rollback window expires, Hetzner cluster decommissioned. Tenant's Hetzner API token optionally revoked.

---

#### Journey H: Destructive Operation with Human Approval _(On-Call Engineer)_ _(extends v7.0 Journey D)_

Unchanged from v7.0 Journey D except: the Platform Console is now a defined deliverable (not a placeholder), and the "Resolve in Cursor" button is the primary resolution path for both platform team and tenant engineers (MCP-based resolution is a platform requirement for all tenants).

---

### 3.3 Day-2 Operations

| Operation | Actor | Method |
|---|---|---|
| Scale CNPG instances | Tenant Admin | "Resolve in IDE" → Update `spec.database.instances` in control plane repo PR via MCP |
| Add team member | Tenant Admin | "Resolve in IDE" → Ory Kratos admin API via MCP OR Platform Console `Team` tab (read-only roster) |
| Upload tenant-specific runbook | Tenant Admin | "Resolve in IDE" → MCP tool uploads to tenant RAG corpus |
| Rotate Infisical secret | Platform Admin | CLI or MCP tool rotates secrets in Infisical, External Secrets Operator auto-syncs to clusters |
| View Grafana dashboard | Any tenant user | Platform Console `Monitoring` tab → "View in Grafana" link (read-only) |
| Query agent about environment | Any tenant user | Platform Console agent conversation interface (read-only history, "Resolve in IDE" for actions) |

---

## 4. Proposed Architecture

### 4.1 High-Level Architecture (v8.0)

**Cluster topology — four distinct cluster types:**

| Type | Also called | Provisioned by | Hosts |
|---|---|---|---|
| **Management Cluster** | Mothership, Shard | `zero-ops mgmt bootstrap` CLI (imperative, Kind pivot) | All platform control plane components. Not a tenant. |
| **Shared Cluster** | Zero-Ops Shared Cluster | Provisioned during platform setup as a dedicated CAPI cluster managed by the management cluster | All Starter-tier tenants as namespaces. Separate from management cluster. _(Out of scope v8.0)_ |
| **Enterprise Tenant Cluster** | Dedicated CAPI cluster | Crossplane Composition B | One per Enterprise tenant, in the tenant's own Hetzner account (BYOC). |
| **PR Environment** | Ephemeral namespace | Argo Workflow (webhook) | Short-lived namespace in tenant cluster (Enterprise) or shared cluster (Starter). |

All boxes in the diagram below run on the **management cluster**, except the TENANT ENVIRONMENTS box which represents clusters/namespaces provisioned by the management cluster.

```
ZERO-OPS v8.0 — SAAS FACTORY ARCHITECTURE

  ┌───────────────────────────────────────────────────────────────────────┐
  │  PLATFORM IDENTITY LAYER (shared, one instance)                       │
  │                                                                       │
  │  ┌─────────────────────────────────────────────────────────────────┐ │
  │  │  identity-service (Python) — Ory stack abstraction layer        │ │
  │  │  Exposes: JWT validation, JWKS, OAuth metadata, Keto checks     │ │
  │  └────────────────────────┬────────────────────────────────────────┘ │
  │                           │                                           │
  │  Ory Kratos (identity)  ←─┼─→  Ory Hydra (OIDC/OAuth2 tokens)        │
  │  Ory Keto (RBAC / relationship-based authorization)                   │
  │                                                                       │
  │  All users: platform admins, tenant admins, tenant end-users          │
  │  Isolation: tenant_id in Kratos identity traits + Keto tuples         │
  └─────────────────────────────┬─────────────────────────────────────────┘
                                │  JWT (Hydra-signed via identity-service)
                                ▼
  ┌───────────────────────────────────────────────────────────────────────┐
  │  AGENTGATEWAY (CNCF, Rust) — single auth enforcement point           │
  │                                                                       │
  │  Routes: MCP tool calls, A2A communications                           │
  │  Validates: JWT via identity-service (never calls Ory directly)       │
  │  Individual MCP tool servers: no auth logic (AgentGateway owns it)    │
  │                                                                       │
  │  Clients: Goose (reference), Cursor, Claude Desktop, any MCP client  │
  └─────────────────────────────┬─────────────────────────────────────────┘
                                │  authenticated MCP calls
                                ▼
  ┌───────────────────────────────────────────────────────────────────────┐
  │  CONTROL PLANE                                                        │
  │                                                                       │
  │  Platform Console (RBAC-scoped UI)                                    │
  │       │  REST / WebSocket                                             │
  │  zero-ops-api  ◄──────────────────────────────────────────────────┐  │
  │       │  Commits AINativeSaaS CR to Git                            │  │
  │       ▼                                                            │  │
  │  Tenant Control Plane Repository (Zero-Ops managed Git)            │  │
  │       │  ArgoCD watches                                            │  │
  │       ▼                                                            │  │
  │  ┌─────────────────────────────────────────────────────────────┐  │  │
  │  │  MULTI-AGENT COLLABORATION SYSTEM (v7.0, unchanged)         │  │  │
  │  │  Collaborator → MetricsAgent, LifecycleAgent, GitOpsAgent,  │  │  │
  │  │  DiagnosticsAgent, UpgradeAgent, ReportAgent                │  │  │
  │  │  + ProvisioningAgent (NEW v8.0) → crossplane-mcp            │  │  │
  │  └─────────────────────────┬───────────────────────────────────┘  │  │
  │                            │                                       │  │
  │  ┌─────────────────────────▼───────────────────────────────────┐  │  │
  │  │  SAFE EXECUTION LAYER (v7.0, unchanged)                     │  │  │
  │  │  Policy Gate → Argo Workflows → CNPG Ticket → Console       │  │  │
  │  └─────────────────────────────────────────────────────────────┘  │  │
  │                                                                    │  │
  │  PostgreSQL (tenant state, billing, fleet config, audit)           │  │
  └───────────────────────────────────────────────────────────────────┘  │
                                                                          │
  ┌───────────────────────────────────────────────────────────────────┐  │
  │  SAAS FACTORY LAYER (NEW v8.0)                                    │  │
  │                                                                   │  │
  │  Crossplane (management cluster)                                  │  │
  │       │  Watches AINativeSaaS CRs                                 │  │
  │       │                                                           │  │
  │       ├── Composition A (Starter)                                 │  │
  │       │   Namespace + ResourceQuota in shared cluster             │  │
  │       │   Shared CNPG + tenant DB (RLS)                           │  │
  │       │   Shared S3 prefix + AgentSandbox allocation              │  │
  │       │                                                           │  │
  │       └── Composition B (Enterprise)                              │  │
  │           CAPI Cluster CR (Ubuntu kubeadm, CAPH)                  │  │
  │           CNPG Cluster CR (HA, pgvector, PgBouncer)               │  │
  │           ArgoCD Application CR (Git sync)                        │  │
  │           Hetzner S3 bucket + Infisical secrets project           │  │
  │           Ingress (nginx) + cert-manager + external-dns           │  │
  │           AgentSandbox (gVisor persistent deployment)             │  │
  │           LiteLLM AI Gateway                                      │  │
  │           PostgREST + CNPG ScheduledBackup                        │  │
  └───────────────────────────────────────────────────────────────────┘  │
                │  provisions                                             │
                ▼                                                         │
  ┌───────────────────────────────────────────────────────────────────┐  │
  │  TENANT ENVIRONMENTS                                              │  │
  │  (provisioned by the management cluster — not part of it)        │  │
  │                                                                   │  │
  │  Starter: Namespace in Zero-Ops Shared Cluster                    │  │
  │    (Shared Cluster = separate CAPI cluster, not the mgmt cluster) │  │
  │  Enterprise: Dedicated CAPI Cluster (Ubuntu, CAPH, Hetzner)       │  │
  │    (One per tenant, separate CAPI cluster 
        runs in tenant's own Hetzner account — BYOC)                  │  │
  │                                                                   │  │
  │  Per-tenant (both tiers):                                         │  │
  │    GitOps Engine:                                              │  │
  │    - Starter: ArgoCD Agent (shared, multi-tenant)              │  │
  │    - Enterprise Autopilot: ArgoCD Agent (dedicated)            │  │
  │    - Enterprise Self-managed: Full ArgoCD (dedicated)          │  │
  │    Reference: https://codefresh.io/blog/better-kubernetes-at-the-edge-with-argo-cd-and-codefresh/ │  │
  │    CNPG (dedicated or shared) with pgvector + PgBouncer           │  │
  │    PostgREST (auto REST API from PostgreSQL schema)               │  │
  │    Grafana Alloy → VictoriaMetrics (topology labels)              │  │
  │    kube-events-exporter → OpenSearch                              │  │
  │    K8sGPT Operator → OpenSearch                                   │  │
  │    cnpg2monitor (fleet-wide ClusterRole)                          │  │
  │    AgentSandbox (gVisor, snapshot-restore per execution)          │  │
  │    LiteLLM AI Gateway (third-party LLM routing)                   │  │
  │    Infisical + External Secrets Operator (secret management)      │  │
  │    Teleport (privileged access management, session recording)     │  │
  │    CNPG ScheduledBackup → Hetzner S3                              │  │
  │    nginx Ingress + cert-manager + external-dns                    │  │
  │                                                                   │  │
  │  Tenant control plane DB (CNPG): admin data + agent memory        │  │
  │  Tenant data plane DB (CNPG): application data                    │  │───┘
  └───────────────────────────────────────────────────────────────────┘
```

### 4.2 Components & Responsibilities

#### 4.2.1 New v8.0 Components

| Component | Implementation | Responsibility |
|---|---|---|
| **AINativeSaaS XRD** | Crossplane `CompositeResourceDefinition` | Defines the `nutgraf.in/v1` API schema for the SaaS environment intent. Cloud-provider agnostic schema. |
| **Composition A — Starter** | Crossplane `Composition` | Expands `AINativeSaaS` (`spec.tier: starter`) into shared cluster namespace resources. Provisions in seconds. |
| **Composition B — Enterprise** | Crossplane `Composition` | Expands `AINativeSaaS` (`spec.tier: enterprise`) into full dedicated cluster stack. Provisions in 10–15 minutes. |
| **Platform Console** | Web UI (to be defined in UI spec) | Single RBAC-scoped console for all users. Read-only interface for: environment dashboard, cost estimates, agent conversation history, destructive op alerts, team roster, runbook library, Grafana links. All write actions via "Resolve in IDE" button generating deep links to MCP clients. |
| **identity-service** | Python service layer | Interfaces with Ory stack (Hydra, Kratos, Keto) on behalf of AgentGateway. Exposes simplified API for JWT validation, user authentication, and permission checks. AgentGateway never calls Ory directly. |
| **Ory Kratos** | Ory Kratos OSS, Kubernetes Helm chart | All identity: platform team, tenant admins, tenant end-users. `tenant_id` in identity traits for isolation. Accessed only via identity-service. |
| **Ory Hydra** | Ory Hydra OSS, stateless, Kubernetes Helm chart | OAuth2/OIDC token issuer. Issues JWTs after Authorization Code + PKCE flow. Accessed only via identity-service. |
| **Ory Keto** | Ory Keto OSS, Kubernetes Helm chart | Relationship-based RBAC. Defines who can see which tenant's resources, who can approve which operations. Accessed only via identity-service. |
| **AgentGateway** | CNCF open source (Rust) | A2A and MCP communications gateway. Single JWT validation enforcement point. No per-tool-server auth logic needed. |
| **Crossplane** | Crossplane OSS, management cluster | Composition engine. Watches `AINativeSaaS` CRs and reconciles constituent resources. |
| **ProvisioningAgent** | New Worker Agent (Go) | Handles `AINativeSaaS` provisioning intents. Calls `crossplane-mcp` to create/update/delete XR claims. |
| **crossplane-mcp** | New MCP tool server (Go) | Exposes typed Crossplane XR operations to the agent system. Creates/reads/updates `AINativeSaaS` claims. |
| **LiteLLM AI Gateway** | LiteLLM OSS, per-tenant deployment | Routes tenant LLM calls to configured third-party providers (OpenAI, Anthropic, Together, Groq). Cost attribution per model call. |
| **PostgREST** | PostgREST standalone container, per-tenant | Auto-generates REST API from tenant PostgreSQL schema. JWT/RLS validation via Ory Hydra tokens. |
| **AgentSandbox** | gVisor (`runsc`) persistent deployment, per-tenant | Sandboxed execution environment for agent workloads. CSI snapshot downloaded at pod startup for consistent state across executions. |
| **Infisical + ESO** | Infisical server + External Secrets Operator | Secret management. Infisical stores encrypted secrets (AES-256-GCM). External Secrets Operator syncs secrets from Infisical to Kubernetes namespaces. Credentials never stored in Git. |
| **Teleport** | Teleport cluster + agents | Privileged access management (PAM). Zero-trust access to clusters with session recording, just-in-time access, and audit logging. Integrates with Ory Kratos for SSO. |
| **PgBouncer** | Bundled with CNPG via `spec.pooler` | Connection pooling for PostgreSQL. Prevents connection exhaustion under high concurrency. |
| **pgvector** | CNPG `shared_preload_libraries: vector` | Vector similarity search extension. Enables agent memory storage and retrieval. Powers tenant AI features. |
| **cnpg2monitor (v8.0)** | Promoted to `ClusterRole` (fleet-wide) | Monitors CNPG clusters in ALL tenant namespaces. Patches PodMonitors with topology labels. Emits lifecycle events to OpenSearch. Prerequisite for AINativeSaaS template. |

#### 4.2.2 Retained v7.0 Components (unchanged unless noted)

All v7.0 components remain in scope: Grafana Alloy, VictoriaMetrics (vmcluster), OpenSearch, kube-events-exporter, K8sGPT Operator, GitOps engines (ArgoCD Agent or Full ArgoCD), CAPI/CAPH, ClusterClass, ClusterResourceSet, Argo Workflows, Policy Gate, CNPG ticket system, Runbook RAG, Collaborator Agent + 6 Worker Agents, 7 MCP tool servers, fleet-heartbeat, Cilium, CCM/CSI.

**GitOps Engine Selection:**
- **ArgoCD Agent**: Used for Starter (shared) and Enterprise Autopilot modes. Community solution from https://github.com/argoproj-labs/argocd-agent/ provides hub-and-spoke GitOps with Zero-Ops management cluster maintaining control plane.
- **Full ArgoCD**: Used for Enterprise Self-managed mode. Complete ArgoCD instance per tenant cluster, similar to [Codefresh's standalone approach](https://codefresh.io/blog/better-kubernetes-at-the-edge-with-argo-cd-and-codefresh/), where tenant manages their own GitOps control plane.

**v7.0 → v8.0 changes to existing components:**

| Component | Change |
|---|---|
| LLM Gateway | Renamed and superseded by **AgentGateway** for MCP/A2A routing. LLM backend governance (rate limiting, cost attribution, fallback) retained as AgentGateway sub-feature. |
| CAPI ClusterClass OS | Ubuntu (kubeadm) replaces Talos. `KubeadmControlPlaneTemplate` is the control plane template. `manifests/classes/hetzner-prod-ubuntu-v1.yaml` replaces `hetzner-prod-talos-v1.yaml`. |
| cnpg2monitor | Promoted from `Role` (management namespace only) to `ClusterRole` (all namespaces). Phase 3 upgrade is now Phase 2b prerequisite. |
| MCP tool servers | Individual servers no longer implement auth logic. AgentGateway is the single enforcement point via identity-service. |
| Ory stack access | All Ory stack interactions (Kratos, Hydra, Keto) are proxied through identity-service. AgentGateway never calls Ory directly. |

### 4.3 Integration & Control Plane

#### 4.3.1 GitOps Flow for AINativeSaaS Provisioning

```
1. Tenant Admin intent (console, Goose, or API call)
        │
2. zero-ops-api validates intent + Hetzner credentials
        │
3. zero-ops-api commits AINativeSaaS CR to
   Tenant Control Plane Repository (Zero-Ops managed Git)
        │
4. ArgoCD (management cluster) detects commit
        │
5. Crossplane receives AINativeSaaS CR
        │
   spec.tier: starter  →  Composition A executes
   spec.tier: enterprise  →  Composition B executes
        │
6. Constituent resources provisioned:
   CAPI Cluster (Enterprise only) → CAPH → Hetzner VMs
   CNPG Cluster → PostgreSQL HA
   ArgoCD Agent (edge) → Git (source of truth) → CI renders manifests → OCI registry artifact → ArgoCD Agent pulls OCI
   All baseline services (see Section 4.2.1)
        │
7. ClusterResourceSet injects into new cluster:
   ArgoCD Agent (autopilot mode) OR Full ArgoCD (self-managed mode)
   - Autopilot: ArgoCD Agent from https://github.com/argoproj-labs/argocd-agent/
   - Self-managed: Complete ArgoCD instance (similar to Codefresh standalone model)
   Grafana Alloy, kube-events-exporter, K8sGPT Operator,
   cnpg2monitor, fleet-heartbeat, Cilium, CCM/CSI
        │
8. cnpg2monitor detects CNPG cluster with
   nutgraf.in/monitored=true label
   → patches PodMonitor with topology labels
   → emits CNPGProvisioned event to OpenSearch
        │
9. Environment status: Ready
   Platform Console reflects tenant environment health
```

#### 4.3.2 Tenant Control Plane Repository Ownership

Zero-Ops owns the tenant control plane repository. It is hosted in Zero-Ops Git infrastructure. Tenants have read access. All writes are performed by `zero-ops-api` (platform-initiated changes) or the ProvisioningAgent (autopilot PRs). Tenants who require full Git access to their control plane can request an **eject** operation, which transfers repository ownership to their Git account and removes Zero-Ops write access.

---

## 5. Technical Specifications

### 5.1 AINativeSaaS XRD Schema

```yaml
apiVersion: apiextensions.crossplane.io/v1
kind: CompositeResourceDefinition
metadata:
  name: ainativesaas.nutgraf.in
spec:
  group: nutgraf.in
  names:
    kind: AINativeSaaS
    plural: ainativesaas
  versions:
    - name: v1
      served: true
      referenceable: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              required: [tier, cloud, region]
              properties:
                tier:
                  type: string
                  enum: [starter, enterprise]
                cloud:
                  type: string
                  enum: [hetzner]          # aws, gcp added in future Compositions
                region:
                  type: string
                  description: Cloud region (e.g. eu-central-1, us-east-1)
                database:
                  type: object
                  properties:
                    instances:
                      type: integer
                      default: 1           # starter: 1, enterprise: 3
                    storage:
                      type: string
                      default: "10Gi"      # starter: 10Gi, enterprise: 100Gi
                autoscaling:
                  type: object
                  properties:
                    min:
                      type: integer
                      default: 1
                    max:
                      type: integer
                      default: 10
                ai:
                  type: object
                  properties:
                    gateway:
                      type: boolean
                      default: true        # LiteLLM AI Gateway always enabled
                    providers:
                      type: array
                      items:
                        type: string
                      default: [openai, anthropic]
                gitops:
                  type: object
                  properties:
                    mode:
                      type: string
                      enum: [autopilot, self-managed]
                      default: autopilot
                      description: "autopilot: ArgoCD Agent managed by Zero-Ops hub; self-managed: Full ArgoCD instance managed by tenant"
```

**Starter tier defaults** (resolved by Composition A): `database.instances: 1`, `database.storage: 10Gi`, `autoscaling.min: 1`, `autoscaling.max: 5`. Shared cluster, namespace isolation, shared CNPG instance with RLS.

**Enterprise tier defaults** (resolved by Composition B): `database.instances: 3`, `database.storage: 100Gi`, `autoscaling.min: 3`, `autoscaling.max: 50`. Dedicated CAPI cluster, physical isolation.

### 5.1.1 Enterprise Tier Variants

Enterprise tier supports two GitOps deployment patterns based on tenant preference:

**Enterprise + Autopilot (Default):**
- **ArgoCD Agent**: Lightweight agent connects to Zero-Ops management cluster
- **Hub-Spoke Model**: Management cluster maintains GitOps control plane
- **Zero-Ops Managed**: Platform team handles ArgoCD updates, policies, and troubleshooting
- **Reference**: Similar to [Codefresh's control plane approach](https://codefresh.io/blog/better-kubernetes-at-the-edge-with-argo-cd-and-codefresh/) but with ArgoCD Agent instead of full instances

**Enterprise + Self-Managed:**
- **Full ArgoCD Instance**: Complete ArgoCD deployment in tenant cluster
- **Standalone Model**: Tenant manages their own GitOps control plane
- **Customer Managed**: Tenant handles ArgoCD updates, policies, and operations
- **Reference**: Similar to [Codefresh's standalone ArgoCD per cluster](https://codefresh.io/blog/better-kubernetes-at-the-edge-with-argo-cd-and-codefresh/) approach

The variant is controlled by `spec.gitops.mode: autopilot|self-managed` in the AINativeSaaS XRD.

### 5.2 Tenant Database Architecture

Every tenant environment provisions **two CNPG databases**:

| Database | Name | Purpose | Contents |
|---|---|---|---|
| **Control Plane DB** | `{tenant}-controlplane` | Tenant administrative data | Tenant config, agent conversation memory (pgvector), autopilot PR history, runbook corpus (tenant-scoped), billing records |
| **Data Plane DB** | `{tenant}-dataplane` | Tenant application data | Tenant's SaaS product data — Zero-Ops has no visibility into this database |

Both databases reside in the same CNPG cluster (Enterprise) or shared CNPG instance (Starter). The control plane DB is provisioned and managed by Zero-Ops. The data plane DB schema is managed by the tenant.

**pgvector** is enabled on both databases via `shared_preload_libraries: "pg_stat_statements,vector"` in the CNPG cluster spec.

**PgBouncer** (`spec.pooler` in CNPG) provides connection pooling for both databases. Default `default_pool_size: 20`. Autopilot can propose increases via PR when pool saturation exceeds 80% for 20 minutes.

### 5.3 Identity Architecture

#### 5.3.1 Platform Identity Stack (shared, one instance per Zero-Ops deployment)

```
identity-service (Python)
  Abstraction layer between AgentGateway and Ory stack
  Exposes: JWKS endpoint, OAuth metadata, JWT validation, Keto permission checks
  AgentGateway calls identity-service ONLY (never Ory directly)
  
  ↓ interfaces with ↓

Ory Kratos
  Identity store for: platform admins, tenant admins, tenant end-users
  Isolation: tenant_id field in identity traits schema
  Schema: { email, name, tenant_id, role: [platform_admin|tenant_admin|tenant_user] }
  Accessed only via identity-service

Ory Hydra
  OAuth2/OIDC token issuer
  Clients: MCP clients via Authorization Code + PKCE flow
  Token claims include: sub, tenant_id, role, scope
  Accessed only via identity-service

Ory Keto
  Relationship tuples define access:
    tenant:acme-corp#admin@user:alice     (alice is admin of acme-corp)
    tenant:acme-corp#viewer@user:bob      (bob can view acme-corp)
    platform#admin@user:carol            (carol is platform admin — sees all tenants)
  Accessed only via identity-service
```

#### 5.3.2 JWT Flow

```
User/Agent authenticates via Kratos (through identity-service)
        │
Kratos confirms identity + tenant_id
        │
Hydra issues JWT (via identity-service):
  { sub: "user-uuid", tenant_id: "acme-corp", role: "tenant_admin", scope: "..." }
        │
JWT passed in Authorization: Bearer header to AgentGateway
        │
AgentGateway calls identity-service to validate JWT signature
identity-service fetches JWKS from Hydra, validates, returns claims
        │
AgentGateway calls identity-service Keto check: "can this user call this MCP tool?"
identity-service queries Keto, returns allow/deny
        │
If authorized: routes MCP call to tool server
If not: returns 403 Forbidden
        │
MCP tool server receives call — no auth logic needed
Tool server uses tenant_id from JWT for data scoping
```

### 5.4 AgentSandbox Specification

**Runtime:** gVisor (`runsc`) kernel-level isolation. Each tenant application gets a persistent sandboxed deployment.

**Snapshot model:**
- A baseline snapshot is created at AgentSandbox provisioning time and stored in the tenant's Hetzner S3 bucket as a CSI volume snapshot.
- On every pod startup, the snapshot is restored to a PVC. The pod mounts this PVC as its working filesystem.
- This ensures agents always execute in the same, known-good environment regardless of prior session state.
- Snapshot URL and version are stored in the tenant control plane DB.

**Cold start:** Snapshot is pulled from Hetzner S3 to local PVC on pod startup. Target cold start: under 30 seconds for snapshots up to 5GB (Option A — S3 pull to local PVC). Option B (pre-warmed PVC on dedicated node pool) is a future config addition.

**URL stability:** Each AgentSandbox deployment has a stable internal DNS name: `{tenant}-sandbox.{namespace}.svc.cluster.local`. This URL does not change across pod restarts or snapshot restores.

### 5.5 Secret Management (Infisical + External Secrets Operator)

```
At tenant onboarding:
  1. Platform creates Infisical project for tenant at path: /tenants/{tenant_id}/
  2. Tenant credentials stored in Infisical with AES-256-GCM encryption
  3. External Secrets Operator (ESO) installed on management cluster and spoke clusters

At secret creation (e.g. Hetzner API token, LiteLLM API keys):
  1. Secret value stored in Infisical at path: /tenants/{tenant_id}/secrets/{secret_name}
  2. ExternalSecret CR created in tenant namespace referencing Infisical SecretStore
  3. ESO syncs secret from Infisical to Kubernetes Secret in tenant namespace
  4. Secrets never stored in Git (Infisical is source of truth)

Secret rotation:
  1. New secret value updated in Infisical
  2. ESO detects change (polls every 5 minutes)
  3. Kubernetes Secret automatically updated in all clusters
  4. Applications reload secrets (via restart or watch mechanism)

Disaster recovery:
  1. Infisical data backed up to S3 daily
  2. Infisical uses CloudNativePG for its PostgreSQL backend (HA + backups)
  3. Platform GitHub App Private Key stored in Infisical for Git operations
```

### 5.6 cnpg2monitor v8.0 — Fleet-Wide Promotion

cnpg2monitor is promoted from a namespace-scoped operator to a fleet-wide operator. This is a prerequisite for the AINativeSaaS template.

**Changes from v7.0 design:**

- RBAC: `Role` + `RoleBinding` scoped to `zero-ops-system` replaced by `ClusterRole` with `get;list;watch` on `Namespaces` (cluster-scoped) and `get;list;watch;patch` on `pods`, `podmonitors`, `clusters.postgresql.cnpg.io` across all namespaces.
- Cache scope: `ctrl.Options{Cache: cache.Options{}}` — no namespace restriction. All CNPG clusters in all namespaces are watched.
- Annotation state stored on PodMonitor (not on CNPG Cluster CR) to avoid triggering CNPG reconciliation.
- Retry counter uses controller-runtime native rate limiter (return error → workqueue exponential backoff). No `sync.Map`.
- `EnqueueRequestsFromMapFunc` watches Namespaces for topology label changes, enqueues all clusters in affected namespace.

### 5.7 Platform Console Specification

The Platform Console is a web application deployed in the management cluster. It is served via HTTPS with cert-manager-issued TLS. It authenticates users via Ory Kratos (OIDC flow) and receives Hydra-issued JWTs.

**RBAC views:**

| Role | Console scope |
|---|---|
| `platform_admin` | All tenants, all environments, all shards, all audit logs |
| `tenant_admin` | Own tenant's environments, team management, runbooks, cost dashboard |
| `tenant_user` | Own tenant's environments (read-only), agent conversation, Grafana link |

**Console sections:**

| Section | Content |
|---|---|
| **Environments** | List of provisioned AINativeSaaS environments. Status, tier, region, cost estimate, ArgoCD sync status, CNPG health. |
| **Agent Conversation** | Multi-agent chat interface. Tenant-scoped agent memory from pgvector control plane DB. Displays K8sGPT findings inline. |
| **Monitoring** | Thin operational dashboard: cluster CPU/memory, CNPG status, ArgoCD sync state, active alerts. "View in Grafana" escape hatch per environment. |
| **Approval Queue** | Destructive operation tickets (CNPG-style). "Resolve in Cursor" button with structured MCP context. Autopilot PR links. |
| **Runbooks** | Platform SOP runbooks (read-only). Tenant-uploadable runbooks (scoped to tenant RAG corpus). |
| **Team** | Ory Kratos identity management for tenant users. Invite, role assignment, revoke. |
| **Billing** | Real-time cost from Hetzner pricing API. Per-environment monthly estimate. Usage breakdown. |
| **Settings** | Autopilot mode toggle. Provider credentials (Hetzner API token — write-only input, stored in Infisical). Teleport access management. Eject option. |

### 5.8 PR Environment Specification

```yaml
# PR environment composition (simplified)
namespace: pr-{branch-slug}
resources:
  - kind: Namespace
    name: pr-{branch-slug}
  - kind: ResourceQuota
    spec:
      hard:
        cpu: "4"
        memory: "8Gi"
  - kind: Cluster  # CNPG — single node
    spec:
      instances: 1
      bootstrap:
        recovery:
          source: staging-snapshot  # CSI VolumeSnapshot from staging CNPG
      storage:
        size: 10Gi
  - kind: Deployment  # Tenant application
    spec:
      image: {branch-image}
  - kind: S3Prefix   # Ephemeral Hetzner S3 prefix
    prefix: pr/{branch-slug}/

teardown_trigger: PR merged or closed
teardown_actions:
  - delete namespace (cascades all resources)
  - delete CNPG cluster
  - purge S3 prefix
  - close billing record
```

**Database isolation:** Each PR environment has its own single-node CNPG cluster restored from the latest staging CSI snapshot. Schema migrations in one PR branch do not affect other PR branches. If no staging snapshot is available, CNPG bootstraps from an empty cluster with schema-only migration.

### 5.9 Operational Semantics

#### 5.9.1 External Dependency Read Frequency

| Dependency | When Read | Caching Strategy |
|---|---|---|
| identity-service JWKS endpoint | AgentGateway startup + cache refresh on 401 | Cached in memory, refreshed on JWT validation failure or every 5 minutes |
| identity-service Keto check API | Per MCP tool call (authorization check) | No cache — authorization decisions must be real-time |
| Hetzner pricing API | At tenant provisioning request time (cost estimate) | Per-request, not cached — prices change |
| CNPG backup (CSI snapshot restore) | At PR environment creation and AgentSandbox pod startup | One-time per lifecycle event — not per request |
| OCI catalog (Git → CI → ArgoCD pull) | ArgoCD polling interval (default: 3 minutes) | ArgoCD native cache |
| VictoriaMetrics (agent queries) | Per agent MCP tool call | No cache — metrics queries must be real-time |
| OpenSearch (agent queries) | Per agent MCP tool call | No cache |
| Runbook RAG embeddings | At DiagnosticsAgent query time | Vector index in PostgreSQL — single query per reasoning step |
| Hetzner API (CAPI) | Per CAPI reconciliation event | CAPI controller-runtime cache |

#### 5.9.2 Idiomatic Behavior & Anti-Patterns

**Idiomatic:**
- AgentGateway caches JWKS from identity-service at startup and refreshes on cache miss — not on every request.
- CNPG snapshot restores happen once per PR environment or AgentSandbox pod lifecycle — not on every agent execution step.
- ArgoCD polls OCI catalog (built from Git) on a fixed interval — not triggered by API calls.
- agent memory (pgvector) is read once per agent conversation session and updated at session end — not on every message.

**Anti-patterns (must NOT):**
- Must NOT call Hetzner pricing API on every dashboard page load — derive from provisioning-time snapshot, refresh on explicit user action only.
- Must NOT call Keto check API from MCP tool server implementations — authorization is AgentGateway's responsibility via identity-service.
- Must NOT fetch JWKS on every JWT validation — cache with TTL.
- Must NOT call Ory stack (Kratos, Hydra, Keto) directly from AgentGateway — always use identity-service.
- Must NOT restore CSI snapshot on every agent tool call — snapshot is the pod's initial state, not a per-call operation.
- Must NOT call `zero-ops-api` directly from ArgoCD — ArgoCD pulls from OCI artifact store only. API is not a reconciliation dependency.

### 5.10 Security, Compliance & Reliability

#### 5.10.1 TLS Strategy

All platform endpoints (Platform Console, `zero-ops-api`, AgentGateway, Ory stack, PostgREST) are served over HTTPS. TLS certificates issued by cert-manager with Let's Encrypt (ACME HTTP-01 or DNS-01 challenge via Hetzner DNS provider). Wildcard certificate issued for `*.nutgraf.in` for platform endpoints. Per-tenant certificates issued for tenant-specific domains.

#### 5.10.2 Network Isolation

**Starter tier:** Kubernetes NetworkPolicy enforces namespace isolation. Tenant A pods cannot reach Tenant B pods by default. Ingress to tenant services goes through shared nginx ingress controller with tenant-specific Ingress resources. No cross-namespace pod communication without explicit NetworkPolicy.

**Enterprise tier:** Physical cluster isolation. No shared network plane with other tenants. Cilium enforces identity-based network policy within the tenant cluster.

#### 5.10.3 Data Visibility Boundaries

| Viewer | Can see |
|---|---|
| Platform team | Infrastructure health metrics (CPU, memory, CNPG status, ArgoCD sync state). Cannot see tenant application data or tenant end-user data. |
| Tenant admin | All data within their own tenant environment including control plane DB, data plane DB, and agent memory. Cannot see other tenants. |
| Zero-Ops agents | Infrastructure signals only (VictoriaMetrics, OpenSearch infra events). Cannot query tenant data plane DB. |

#### 5.10.4 Autopilot Safety Invariants

Regardless of autopilot mode setting, the following operations **always require a human-approved PR** before execution:

- Any change to `spec.database.instances` (scale CNPG)
- Any change to `spec.tier` (upgrade/downgrade)
- Any change to `spec.cloud` (provider migration)
- CNPG cluster deletion
- Namespace deletion
- Infisical secret rotation
- Teleport access policy changes

The following operations can be proposed via autopilot PR without pre-approval escalation:

- PgBouncer pool size increase
- ResourceQuota increase
- ArgoCD application sync (non-destructive)
- LiteLLM provider configuration changes

#### 5.10.5 HA and Control Plane Availability

Zero-Ops management cluster unavailability does not affect tenant runtime. Tenant workloads, CNPG clusters, and ArgoCD Agent instances are fully self-contained within tenant environments. Management cluster unavailability affects only: new provisioning requests, scaling operations, agent-driven remediation, and platform console access. Running tenant SaaS products continue serving their end-users unaffected during management cluster downtime.

When the management cluster reconnects, CAPI resumes reconciliation from last known state. Crossplane resumes composition reconciliation. No manual intervention required for recovery.

---

## 6. User Journey Deep Dives

### Scenario 1: Agentic Headless Authentication & Enterprise Onboarding

**Actors:** Tenant Admin (Acme Corp), Goose Agent.  
**Preconditions:** Zero-Ops platform deployed. Acme Corp has a Hetzner account with API access. No existing Zero-Ops session.

**Step-by-Step Flow:**

1. Tenant Admin types in Goose: `"Onboard Acme Corp on the enterprise plan in eu-central-1."`
2. Goose calls `tenant_create` via AgentGateway MCP endpoint.
3. AgentGateway detects missing JWT. Returns `401 Unauthorized` + `WWW-Authenticate` header with resource_metadata URL pointing to identity-service.
4. Goose discovers OAuth endpoints via identity-service metadata endpoints, initiates OAuth 2.1 Authorization Code + PKCE flow: generates code_verifier, computes code_challenge (SHA256), opens system browser to authorization URL (via identity-service) with PKCE parameters and custom URI scheme redirect (goose://callback).
5. Tenant Admin opens browser (automatically), completes Kratos login (email + password or SSO) via identity-service.
6. Hydra (via identity-service) redirects to goose://callback?code={authorization_code}&state={state}. Goose validates state, exchanges authorization code + code_verifier for Access Token (JWT) via identity-service token endpoint.
7. Goose retries `tenant_create` automatically with JWT.
7. AgentGateway validates JWT via identity-service. Calls identity-service Keto check: `"can user:acme-admin perform tenant:create?"` — passes (new tenants can self-create).
8. `zero-ops-api` creates tenant record in PostgreSQL. Returns `201 Created` with `tenant_id: acme-corp`.
9. Goose prompts: `"Please provide your Hetzner API token via the console at https://console.nutgraf.in/settings/credentials"`. Goose pauses and polls for credential confirmation.
10. Tenant Admin opens console (already authenticated via Kratos session). Console displays credential submission form (read-only view with "Resolve in IDE" button).
11. Tenant Admin clicks "Resolve in IDE". Console generates deep link: `cursor://resolve?action=submit_credentials&tenant_id=acme-corp`.
12. Cursor/Goose receives deep link, prompts Tenant Admin for Hetzner API token in IDE (secure input, never transits agent).
13. Cursor/Goose calls `credentials_submit` MCP tool with encrypted token. `zero-ops-api` stores encrypted token in Hetzner S3.
14. Goose detects credential confirmation. Calls `environment_create` MCP tool with `{tier: enterprise, cloud: hetzner, region: eu-central-1}`.
12. `zero-ops-api` commits `AINativeSaaS` CR to tenant control plane repository. Returns `202 Accepted` with provisioning status URL.
13. Goose displays: `"Provisioning in progress. Track at https://console.nutgraf.in/environments/acme-corp-production"`.
14. 12 minutes later: Crossplane Composition B completes. Console shows `Ready`. Goose displays final summary.

**Variations:**

- *Hetzner quota exceeded:* CAPI provisioning fails. Crossplane sets Condition `Ready: False, Reason: HetznerQuotaExceeded`. Console alert displayed. Goose surfaces the error with remediation suggestion: `"Request a quota increase at https://hetzner.com/quota or try a different region."`
- *Goose authorization timeout:* If authorization code not exchanged within 10 minutes, Hydra expires the code. Goose restarts the Authorization Code + PKCE flow with new code_verifier and code_challenge.
- *Duplicate tenant name:* `zero-ops-api` returns `409 Conflict`. Goose suggests alternative names.

---

### Scenario 2: CNPG Degraded — Autopilot PR + Console Resolution

**Actors:** Tenant Admin (Acme Corp), DiagnosticsAgent, On-Call Engineer.  
**Preconditions:** Acme Corp Enterprise environment is running. VictoriaMetrics is receiving metrics.

**Step-by-Step Flow:**

1. VictoriaMetrics `vmalert` detects: CNPG primary pod CPU at 92% for 15 minutes on `acme-corp-production`. Alert fires to `zero-ops-api` via Alertmanager webhook.
2. Collaborator Agent receives alert. Routes to DiagnosticsAgent.
3. DiagnosticsAgent calls `opensearch-mcp`: `"Give me all infra-changes and k8s-events for cluster acme-corp-production in the last 60 minutes."` — returns: no catalog push, no ArgoCD sync, one PVC resize 45 minutes ago.
4. DiagnosticsAgent calls `k8sgpt-mcp`: current K8sGPT findings for `acme-corp-production`. Returns: `"CNPG primary has high connection count. PgBouncer pool_size may be insufficient."`
5. DiagnosticsAgent queries tenant RAG corpus: matches SOP runbook `cnpg-pgbouncer-pool-saturation`. Recommended action: increase `default_pool_size` from 20 to 40.
6. Collaborator classifies as `non-destructive`. Autopilot mode is ON for Acme Corp.
7. ProvisioningAgent commits PR to tenant control plane repository:
    - Branch: `autopilot/pgbouncer-pool-increase-2026-03-11`
    - Change: `spec.pooler.parameters.default_pool_size: "40"` in CNPG Cluster manifest
    - PR description: Full context — metric graph link, K8sGPT finding, runbook reference, proposed change diff.
8. Console Approval Queue shows new PR. Tenant Admin receives notification.
9. **Path A — Tenant approves PR directly:** Merges PR in Git. ArgoCD detects change. CNPG pool size updated. VictoriaMetrics confirms CPU normalisation within 5 minutes. OpenSearch `infra-changes` event written.
10. **Path B — Tenant clicks "Resolve in Cursor":** Console generates structured MCP message containing: cluster state, K8sGPT findings, OpenSearch timeline, proposed change diff, available tools (`cnpg-mcp`, `opensearch-mcp`, `victoriametrics-mcp`). Cursor opens with this context. Engineer reviews and either approves the autopilot PR or makes a different change.

**Variations:**

- *Autopilot OFF:* Step 7 becomes a console alert with "Resolve in Cursor" button only. No PR committed. Tenant resolves manually.
- *Agent cannot find matching runbook:* DiagnosticsAgent escalates to human without proposing a fix. Console alert shows: `"Anomaly detected. No matching runbook. Manual investigation required."` with "Resolve in Cursor" button.

---

### Scenario 3: Provider Migration — Hetzner to AWS (Blue-Green)

**Actors:** Tenant Admin (Acme Corp).  
**Preconditions:** Acme Corp Enterprise on Hetzner is running. AWS credentials available.

**Step-by-Step Flow:**

1. Tenant Admin selects `Settings → Migrate Provider → AWS` in Platform Console.
2. Console displays: estimated migration window, CNPG PITR restore strategy explanation, rollback window (default 2 hours after cutover).
3. Tenant Admin provides AWS credentials. Console stores in Infisical at path: /tenants/{tenant_id}/credentials/aws.
4. Tenant Admin confirms. `zero-ops-api` updates `spec.cloud: aws` in tenant control plane repository via a platform-authored PR. Tenant Admin approves this specific PR (provider migration is always human-approved regardless of autopilot mode).
5. ArgoCD applies. Crossplane detects `spec.cloud` drift from `hetzner` to `aws`.
6. Crossplane triggers blue-green migration sequence:
    - **Phase 1:** New AWS CAPI cluster provisioned via AWS Composition.
    - **Phase 2:** CNPG base backup taken on Hetzner cluster. New CNPG cluster on AWS bootstrapped via PITR from backup. All data migrated to AWS CNPG.
    - **Phase 3:** AWS CNPG confirmed healthy (primary elected, all instances ready).
    - **Phase 4:** external-dns updates tenant DNS records to point to AWS cluster ingress.
    - **Phase 5:** Hetzner cluster marked `standby` — retained for rollback window.
7. Console shows: `"Migration complete. AWS cluster is live. Hetzner cluster retained for rollback until [timestamp]."`
8. After rollback window: Hetzner cluster decommissioned. Tenant's Hetzner API token usage reduced.

**Rollback path:** Tenant Admin clicks `Rollback to Hetzner` during rollback window. DNS reverts to Hetzner cluster. AWS cluster decommissioned.

---

## 7. Monorepo Structure (v8.0)

```
zero-ops/
│
├── cmd/
│   ├── api/                          # zero-ops-api entrypoint
│   ├── agentgateway/                 # AgentGateway entrypoint (or separate process)
│   └── mgmt/                         # CLI: bootstrap, eject, migrate
│
├── pkg/
│   ├── api/                          # REST API handlers
│   ├── fleetstate/                   # Fleet routing metadata (PostgreSQL)
│   ├── policies/                     # Policy evaluation engine
│   ├── agents/
│   │   ├── collaborator/
│   │   ├── workers/
│   │   │   ├── metrics_agent.go
│   │   │   ├── lifecycle_agent.go
│   │   │   ├── gitops_agent.go
│   │   │   ├── diagnostics_agent.go
│   │   │   ├── upgrade_agent.go
│   │   │   ├── report_agent.go
│   │   │   └── provisioning_agent.go  # NEW v8.0 — AINativeSaaS XR claims
│   │   └── mcp/
│   │       ├── fleet_state_mcp.go
│   │       ├── victoriametrics_mcp.go
│   │       ├── opensearch_mcp.go
│   │       ├── capi_mcp.go
│   │       ├── argocd_mcp.go
│   │       ├── k8sgpt_mcp.go
│   │       ├── audit_mcp.go
│   │       └── crossplane_mcp.go      # NEW v8.0 — XR claim operations
│   ├── agentgateway/                  # NEW v8.0 — A2A + MCP routing, JWT validation
│   ├── identity/                      # NEW v8.0 — Ory Kratos/Hydra/Keto clients
│   ├── console/                       # NEW v8.0 — Platform Console backend
│   ├── llmgateway/                    # LLM backend governance (subsumed by AgentGateway)
│   ├── rag/
│   ├── execution/
│   │   ├── policy_gate.go
│   │   ├── workflows.go
│   │   └── approval.go
│   └── db/
│
├── xrds/                              # NEW v8.0 — Crossplane SaaS Templates
│   ├── definitions/
│   │   └── ainativesaas-v1.yaml       # XRD: AINativeSaaS API schema
│   └── compositions/
│       ├── ainativesaas-starter-hetzner.yaml    # Composition A: shared cluster
│       └── ainativesaas-enterprise-hetzner.yaml # Composition B: dedicated cluster
│
├── manifests/
│   ├── shards/
│   │   ├── capi-core.yaml
│   │   └── caph-provider.yaml
│   ├── classes/
│   │   └── hetzner-prod-ubuntu-v1.yaml  # Ubuntu kubeadm ClusterClass (replaces Talos)
│   └── platform/                        # NEW v8.0 — Platform-level manifests
│       ├── crossplane/
│       │   └── crossplane-values.yaml
│       ├── identity/
│       │   ├── kratos-values.yaml
│       │   ├── hydra-values.yaml
│       │   └── keto-values.yaml
│       └── agentgateway/
│           └── agentgateway-values.yaml
│
├── argo-workflows/
│   ├── scale-workers.yaml
│   ├── safe-node-drain.yaml
│   ├── safe-pod-restart.yaml
│   ├── addon-upgrade.yaml
│   ├── cluster-delete.yaml
│   ├── pr-env-create.yaml            # NEW v8.0 — ephemeral PR environment
│   ├── pr-env-teardown.yaml          # NEW v8.0 — PR environment cleanup
│   └── provider-migrate.yaml         # NEW v8.0 — blue-green provider migration
│
├── edge-catalog/
│   ├── argocd-agent.yaml             # ArgoCD Agent (community solution)
│   ├── grafana-alloy.yaml
│   ├── kube-events-exporter.yaml
│   ├── k8sgpt-operator.yaml
│   ├── k8sgpt-result-exporter.yaml
│   ├── fleet-heartbeat.yaml
│   ├── cilium.yaml
│   ├── ccm-csi.yaml
│   ├── cnpg2monitor.yaml             # NEW v8.0 — promoted to ClusterRole, all-namespace
│   ├── cert-manager.yaml             # NEW v8.0
│   ├── external-dns.yaml             # NEW v8.0
│   ├── nginx-ingress.yaml            # NEW v8.0
│   ├── litellm-gateway.yaml          # NEW v8.0
│   ├── postgrest.yaml                # NEW v8.0
│   └── agentsandbox.yaml             # NEW v8.0 — gVisor persistent deployment
│
├── catalog/
│   ├── databases/
│   │   └── cloudnative-pg/
│   │       ├── cluster.yaml          # CNPG Cluster CR template (with pgvector, PgBouncer)
│   │       └── scheduled-backup.yaml # Default ScheduledBackup CR
│   └── optional/                     # Opt-in services
│       └── supabase-realtime/        # Supabase Realtime (Elixir) — opt-in via catalog
│
├── observability/
│   ├── victoriametrics/
│   │   ├── vmcluster-values.yaml
│   │   ├── vmalert-rules.yaml
│   │   └── alertmanager-config.yaml
│   └── opensearch/
│       ├── opensearch-values.yaml
│       └── index-templates/
│           ├── k8s-events.json
│           ├── k8sgpt-findings.json
│           ├── argocd-syncs.json
│           ├── infra-changes.json
│           ├── catalog-pushes.json
│           └── alert-firings.json
│
├── operators/
│   └── cnpg2monitor/                 # cnpg2monitor operator source
│       ├── cmd/main.go
│       └── internal/controller/
│
├── go.mod
└── Makefile
```

---

## 8. Implementation Phases (v8.0)

| Phase | Name | Key Deliverables | Depends On | Status |
|---|---|---|---|---|
| 1 | SaaS Control Plane & API | PostgreSQL schema (v8.0 — adds tenant_id, tier, plan columns), REST API CRUD, intent payload parsing, tenant management | — | **Next** |
| 2 | Fleet Shard Bootstrap | `zero-ops mgmt bootstrap` CLI, CAPI/CAPH/Ubuntu providers, shard registration, shard health checker | Phase 1 | Planned |
| **2b** | **cnpg2monitor Fleet-Wide** | ClusterRole promotion, all-namespace cache, annotation state on PodMonitor, native rate limiter | Phase 2 | **NEW — Planned** |
| 3 | CAPI Dispatch & ArgoCD Agent | Ubuntu ClusterClass, CAPI dispatch, ClusterResourceSet injection (ArgoCD Agent from https://github.com/argoproj-labs/argocd-agent/), Git → CI → OCI catalog delivery | Phase 2 | Planned |
| 4 | Fleet Observability Stack | VictoriaMetrics vmcluster, Alloy config, vmalert, Alertmanager webhook, OpenSearch, index templates, K8sGPT result exporter | Phase 3 | Planned |
| 5 | Safe Execution Layer | Policy Gate, Argo Workflow templates (incl. PR env + provider migration), CNPG ticket integration, `agent_audit_log` | Phase 4 | Planned |
| 6 | MCP Tool Servers | All 8 MCP servers (7 from v7.0 + crossplane-mcp) with AgentGateway auth integration | Phase 5 | Planned |
| **7** | **Platform Identity** | Ory Kratos + Hydra + Keto deployment, JWT flow, AgentGateway integration, Authorization Code + PKCE for MCP clients | Phase 1 | **NEW — Planned** |
| **8** | **AINativeSaaS Template** | Crossplane deployment, XRD definition, Composition A (Starter), Composition B (Enterprise), ProvisioningAgent, crossplane-mcp | Phases 3, 7 | **NEW — Planned** |
| **9** | **Platform Console** | Console web app, RBAC views, environment dashboard, agent conversation interface, approval queue, Grafana escape hatch, cost estimates | Phases 4, 7, 8 | **NEW — Planned** |
| 10 | Multi-Agent Collaboration | LLM governance via AgentGateway, Collaborator Agent, all Worker Agents incl. ProvisioningAgent, Runbook RAG, end-to-end test suite | Phases 6, 7, 8, 9 | Planned |

> **SEQUENCING NOTE:** Phase 2b (cnpg2monitor fleet-wide) must complete before Phase 8 (AINativeSaaS template). Every tenant environment provisions a CNPG cluster — the operator must be fleet-wide before tenants can onboard. Phase 7 (Identity) must complete before Phase 9 (Console) — the Console has no login flow without Kratos/Hydra. Phase 8 must complete before Phase 10 — agents cannot provision environments without the Crossplane XRD and crossplane-mcp tool server.

---

## 9. Success Criteria & Acceptance Tests

### Infrastructure & Provisioning

| ID | Criterion | Verification |
|---|---|---|
| A-01 | `AINativeSaaS` CR with `spec.tier: enterprise` provisions a complete dedicated stack within 15 minutes | `kubectl get ainativesaas acme-corp-production -o jsonpath='{.status.conditions}' \| jq` shows `Ready: True` within 15 minutes of CR creation |
| A-02 | `AINativeSaaS` CR with `spec.tier: starter` provisions a complete namespace-isolated stack within 60 seconds | Same check, within 60s |
| A-03 | Crossplane Composition B creates exactly: 1 CAPI Cluster, 1 CNPG Cluster, 1 ArgoCD Application, 1 S3 bucket, 1 nginx Ingress, 1 cert-manager Certificate, 1 AgentSandbox deployment, 1 LiteLLM deployment, 1 PostgREST deployment | `kubectl get managed -l crossplane.io/composite=acme-corp-production \| wc -l` equals expected count |
| A-04 | Every tenant cluster emits VictoriaMetrics metrics with mandatory topology labels (`tenant_id`, `region`, `cloud_provider`, `tier`) within 60s of boot | PromQL: `count by (tenant_id) (up{tenant_id="acme-corp"})` returns > 0 |
| A-05 | cnpg2monitor patches PodMonitor with topology relabelings within 30s of CNPG cluster `Ready` | `kubectl get podmonitor -n acme-corp -o yaml \| grep nutgraf.in` shows topology labels |
| A-06 | CNPG ScheduledBackup runs on configured schedule and writes backup to tenant Hetzner S3 | `kubectl get scheduledbackup -n acme-corp \| grep Completed` |
| A-07 | PR environment created within 2 minutes of branch creation webhook | Namespace `pr-feature-branch` exists with CNPG cluster `Ready` within 120s |
| A-08 | PR environment torn down within 2 minutes of PR close webhook | Namespace `pr-feature-branch` absent within 120s of PR close |

### Identity & Auth

| ID | Criterion | Verification |
|---|---|---|
| A-09 | Goose Authorization Code + PKCE flow completes: 401 → browser opens → login → redirect to goose://callback → JWT exchange → retry succeeds | Manual flow test; Goose output shows `tenant_create` result after auth |
| A-10 | AgentGateway rejects MCP calls without valid JWT | `curl -X POST https://gateway.nutgraf.in/mcp -d '...'` without Authorization header returns `401` |
| A-11 | Tenant A cannot call MCP tools scoped to Tenant B | JWT with `tenant_id: tenant-a` calling `crossplane-mcp:get` for `tenant-b` resource returns `403` |
| A-12 | Platform admin JWT can query all tenant resources | Platform admin JWT calling `fleet-state-mcp:list` returns results from all tenants |

### Agent System

| ID | Criterion | Verification |
|---|---|---|
| A-13 | DiagnosticsAgent queries OpenSearch before runbook RAG for every alert | Agent audit log shows `opensearch-mcp` call preceding `rag-query` call in every diagnostics trace |
| A-14 | Autopilot mode raises PR (not direct commit) for PgBouncer pool size increase | PR exists in tenant control plane repo within 2 minutes of alert |
| A-15 | ProvisioningAgent creates `AINativeSaaS` XR claim via `crossplane-mcp` and does not call CAPI directly | No `capi-mcp` calls in ProvisioningAgent audit log for environment creation intents |

### Console & Cost

| ID | Criterion | Verification |
|---|---|---|
| A-16 | Platform Console shows environment status as `Ready` within 1 minute of Crossplane composition completing | Browser: `https://console.nutgraf.in/environments/acme-corp-production` shows green status |
| A-17 | Cost estimate displayed before provisioning is derived from Hetzner pricing API at request time | Network trace shows API call to `api.hetzner.cloud/v1/pricing` at time of `New Environment` form submission |
| A-18 | "View in Grafana" link in Console opens tenant-scoped Grafana dashboard | Click opens Grafana URL with `var-tenant_id=acme-corp` query parameter |

### HA & Resilience

| ID | Criterion | Verification |
|---|---|---|
| A-19 | Tenant SaaS workload continues serving requests during 30-minute management cluster unavailability | Load test against tenant endpoint during management cluster shutdown; p99 latency unaffected |
| A-20 | Crossplane resumes composition reconciliation after management cluster restart with no manual intervention | Restart management cluster; observe Crossplane controller logs show resumed reconciliation within 60s of ready |

---

## 10. Architectural Position (v8.0)

```
Infrastructure Fleet Platforms        Developer Platforms         BaaS / Backend Factories
(Rancher, Platform9)                  (Radius, Backstage)         (Supabase, Neon, Railway)
        │                                      │                           │
        │                                      │                           │
        └──────────────────────┬───────────────┘───────────────────────────┘
                               │
                    SaaS Factory Platform
                        (Zero-Ops v8.0)
```

**What separates Zero-Ops from fleet platforms:** The AINativeSaaS XRD provisions a complete SaaS product backend — not a bare cluster. Tenants get PostgreSQL HA, vector search, object storage, secret management, observability, auth, agent runtime, and AI gateway in a single declarative object.

**What separates Zero-Ops from developer platforms:** Direct infrastructure control via CAPI and CAPH. BYOC model with physical tenant isolation at Enterprise tier. Crossplane-driven composition with blue-green provider migration. No vendor lock-in by design.

**What separates Zero-Ops from BaaS:** Full Kubernetes control plane access. AI-native agent system with runbook-grounded remediation. Fleet-wide observability (VictoriaMetrics + OpenSearch) with cross-cluster correlation. Autopilot-with-consent model. Eject option — tenant can take full ownership of their environment at any time.

**The build vs. adopt principle (v8.0 extended):** Crossplane (composition), Ory Kratos/Hydra/Keto (identity), PostgREST (auto API), gVisor (sandbox isolation), LiteLLM (AI gateway), PgBouncer (connection pooling), KSOPS+Age (secrets) — all production-proven, CNCF-aligned or open source tools used at scale. Custom code is reserved for: the thin MCP integration layer, the agent reasoning logic, the Crossplane Compositions that wire these tools together, and the Platform Console that surfaces them to tenants. This is the engineering strategy that makes the platform viable without a 100-person platform team.

---

*Document Status: DRAFT — Supersedes v7.0*  
*Next Phase: Phase 1 (Database Schema & API) + Phase 7 (Identity Stack) — run in parallel*  
*Previous version: v7.0 (Open Source Fleet Observability Stack) — changelog at top of document*

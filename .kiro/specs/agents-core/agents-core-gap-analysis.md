# Agents Core — Gap Analysis
## Development Readiness Assessment

**Documents reviewed:** design.md, tasks.md, requirements.md, agentic-infra.png
**Date:** 2026-03-25
**Verdict:** NOT development-ready. 4 blockers, 9 high-priority gaps, 7 clarifications.
**Required action:** Product team must answer all Blocker and High items before Phase 1 starts.

---

## Blockers — implementation cannot start without these answers

---

### B-01 · AgentRegistry multi-tenancy: how does it isolate agents per tenant?

**Where:** design.md §2.1, §3.1, tasks.md Phase 1

**Problem:**
AgentRegistry OSS uses `name` as the primary identifier and `UNIQUE(name, version)` as
the uniqueness constraint. There is no `tenant_id` column shown in the AgentRegistry schema.

`GET /v0/agents` returns agents from the `agentregistry` schema. There is no filter parameter
for `tenant_id` shown in the API contract. If two tenants each create an agent named
`billing-agent`, one of the `POST /v0/agents` calls will fail with a unique constraint
violation, or — worse — will silently overwrite the other tenant's definition.

**Questions for product team:**
1. Does AgentRegistry OSS have a `tenant_id` column and corresponding filter? If yes, where
   in the OSS codebase is it? If no, how is cross-tenant agent isolation enforced?
2. The design says "AgentRegistry is OSS (read-only) — cannot modify." If it has no
   tenant isolation, does the design need a custom fork, a wrapper table, or a naming
   convention like `{tenant_id}/{agent_name}`?
3. Who sets the tenant context when `mcp-server` calls `POST /v0/agents`? Is a
   `tenant_id` header injected, or is it derived from the auth token AgentRegistry receives?

**Risk if unresolved:** Cross-tenant agent name collision on day one of testing.

---

### B-02 · State machine is undefined: what happens when AgentRegistry and Hub Centralised DB disagree?

**Where:** design.md §4.2 (deployment flow), §5.2 (idempotency), tasks.md Phase 4–5

**Problem:**
The deployment flow has two separate state stores:
- AgentRegistry DB: holds `deployment.status = "deploying"` (written at step 3)
- Hub Centralised DB: holds `agent_deployments.status = "ready"` (written at step 12)

There is no reconciliation mechanism defined. If the AgentRegistry record says `"deploying"`
but Hub Centralised DB says `"ready"`, which one wins? If the Git commit succeeds (step 5)
but `POST /v0/deployments` to AgentRegistry fails (step 3), the deployment proceeds with no
record in AgentRegistry. The agent reaches `"ready"` in Hub Centralised DB, but AgentRegistry
thinks it was never deployed. `delete_agent` then finds zero deployments and skips Git cleanup.

**Questions for product team:**
1. Which store is the source of truth for deployment status: AgentRegistry or Hub Centralised DB?
2. What is the recovery path when: (a) AgentRegistry write succeeds, Git commit fails;
   (b) Git commit succeeds, Spoke Controller never writes status?
3. Should `deploy_agent` be idempotent by checking Hub Centralised DB first (as the
   idempotency section implies) or by checking AgentRegistry first?
4. Is there a reconciliation job that syncs the two stores, or is eventual consistency
   the accepted model for these two sources?

**Risk if unresolved:** Silent data inconsistency. Delete operations will leak Git manifests.

---

### B-03 · `spoke_cluster_id` in JWT: who sets it and when?

**Where:** design.md §2.1 (JWT claims), §3.4 (list_providers removal), tasks.md Phase 4 (deploy_agent)

**Problem:**
The design removes `list_providers` on the basis that `spoke_cluster_id` is already in the
JWT claim. The JWT claims structure shows `SpokeClusterID string json:"spoke_cluster_id"`.

This claim must be written into the JWT by Ory Hydra during token generation. That requires
Hydra to know which spoke cluster a tenant is assigned to. In the current v9.0 platform,
Hydra enriches tokens from Kratos identity traits. The tenant-to-spoke assignment is stored
in the Control Plane Shared DB, not in Kratos traits.

There is no task in tasks.md or design.md that covers: (a) where spoke_cluster_id is stored
during onboarding, (b) how Hydra reads it to include in the JWT, (c) what happens if a
tenant is migrated between spokes.

**Questions for product team:**
1. Is `spoke_cluster_id` already written to Kratos identity traits during environment_create
   (Req 6)? If not, which component writes it and when?
2. If a tenant has multiple environments on different spoke clusters, which `spoke_cluster_id`
   goes in the JWT? Or does the JWT contain a list?
3. What is the token refresh story if a tenant is moved to a new spoke — does the old token
   route to the wrong cluster until it expires?

**Risk if unresolved:** `deploy_agent` always routes to the wrong cluster or panics on
empty `spoke_cluster_id`.

---

### B-04 · Delete ordering and GitOps conflict: AgentRegistry blocks delete if CRD still in Git

**Where:** design.md §3.8 (delete_agent), tasks.md Phase 6

**Problem:**
The delete flow is:
1. For each deployment: delete CRD from Git → delete deployment record from AgentRegistry
2. Delete agent definition from AgentRegistry

AgentRegistry OSS `DeleteAgent` explicitly checks for active deployments:
```go
if len(deployments) > 0 { return error("cannot delete agent with active deployments") }
```
AgentRegistry considers a deployment "active" until its DB record shows `status: cancelled`.
But `DeleteDeployment` only sets `status: cancelled` in the AgentRegistry DB — it does not
wait for the CRD to be removed from the cluster.

The sequence is: Git commit (removes CRD) → ArgoCD sync (async, minutes later) → Kagent
Controller deletes pod → Spoke Controller writes `status: deleted` to Hub Centralised DB.

But `agent-core` calls `DeleteDeployment` (sets AgentRegistry to `cancelled`) immediately
after the Git commit, then calls `DeleteAgent` immediately after. None of these steps wait
for ArgoCD to actually remove the CRD from the cluster.

So the question is not ordering of API calls — it is: is it safe to mark `status: cancelled`
in AgentRegistry before the pod is actually gone? What if ArgoCD sync is delayed 10 minutes?
During that window, the agent pod is still running but AgentRegistry says it is cancelled.

**Questions for product team:**
1. Is it acceptable to mark a deployment `cancelled` in AgentRegistry before the pod
   terminates on the spoke? What are the side-effects of this window?
2. Should `delete_agent` return `HTTP 202` with a `deleting` status and let the Spoke
   Controller confirm termination, consistent with the async pattern used by `deploy_agent`?
3. Does the AgentRegistry `DeleteAgent` check for `status: deploying` deployments?
   If so, what is the cancel path for an in-progress deployment?

**Risk if unresolved:** Delete returns success but the agent pod continues executing for
minutes. Billing events continue to fire from a "deleted" agent.

---

## High Priority — must be resolved before the affected phase starts

---

### H-01 · `available_models` seed data is static: what is the update path?

**Where:** design.md §2.2, tasks.md 1.1.4

**Problem:**
`available_models` is seeded with hardcoded model names at migration time. Model names
change (e.g., `gpt-4-turbo` → `gpt-4-turbo-2024-04-09`), new models are added frequently,
and LiteLLM supports dozens of models. There is no API, admin tool, or migration strategy
for updating this table after bootstrap.

**Questions:**
1. Who manages `available_models` in production — Platform Admin via SQL migration, or
   a config file, or an admin API?
2. If a model name in `available_models` does not exactly match a LiteLLM model identifier,
   what happens at inference time?
3. Is `available_models` the right place for this, or should it be a config file managed
   via GitOps that feeds into the validation logic at startup?

---

### H-02 · `list_agents` makes N Hub PostgREST calls — no batching defined

**Where:** design.md §3.7 (list_agents), tasks.md Phase 3

**Problem:**
`list_agents` calls `AgentRegistry GET /v0/agents` (returns N agents), then for each agent
calls `hubClient.GetAgentDeployment()` individually — one PostgREST query per agent. With
30 agents (the default limit), this is 31 serial HTTP calls before returning to the user.

**Questions:**
1. Should `GetAgentDeployment` be replaced with a batch query (e.g., `WHERE agent_id IN
   (...)`) against Hub Centralised DB?
2. What is the acceptable response time budget for `list_agents`? If it is >2s for 30
   agents the MCP client will show a slow tool.
3. Is there a short-lived in-process cache for deployment status on the Hub side to
   reduce PostgREST load?

---

### H-03 · KEDA idle detection trigger is undefined: NATS queue or HTTP metrics?

**Where:** design.md §4.4, tasks.md Phase 5 (manual test), Phase 8 (Spoke Controller)

**Problem:**
Section 4.4 says "KEDA monitors agent request queue (NATS subject or HTTP metrics)" — both
are listed as alternatives without a decision. These require completely different KEDA trigger
configurations:
- NATS trigger: requires NATS JetStream consumer, queue depth metric
- HTTP trigger: requires KEDA HTTP Add-on, interceptor proxy in the request path

The HTTP Add-on intercepts all A2A requests, which has latency implications. The NATS trigger
only measures queue depth, not active HTTP sessions.

**Questions:**
1. Which trigger is chosen for scale-to-zero?
2. If HTTP Add-on: how does it integrate with the existing A2A request path (Kagent routes
   via `POST /api/a2a/{namespace}/{agent}` — does KEDA intercept before or after Kagent)?
3. If NATS trigger: which NATS subject does the agent consume from, and who publishes
   to it to trigger wake-up?

---

### H-04 · Update-agent with running sessions: what is the rollout strategy?

**Where:** design.md §3.6 (update_agent), tasks.md Phase 6

**Problem:**
`update_agent` commits an updated Agent CRD to Git. ArgoCD syncs it. Kagent Controller
reconciles, causing a new Deployment rollout. During the rollout, existing agent sessions
(tracked by Kagent session table) are on the old pod version.

There is no mention of: (a) drain strategy for in-flight sessions before rollout,
(b) whether Kagent sessions survive a pod restart, (c) whether the Collaborator agent
pattern means active A2A calls are dropped during the update.

**Questions:**
1. What happens to active A2A sessions during a Kagent agent pod rollout?
2. Is a rolling update (1 new pod, 1 old pod) acceptable, or do agents require a
   blue/green strategy where the old version finishes its sessions before shutdown?
3. Does the `update_agent` response tell the user whether active sessions were affected?

---

### H-05 · `delete_agent` with memory: is pgvector data cleaned up?

**Where:** design.md Phase 4 (management tools note: "Memory preservation logic"), tasks.md Phase 6

**Problem:**
The design mentions "Memory preservation logic" in Phase 4 of section 9 but provides no
detail. Agents accumulate memory in the `memory` schema (pgvector embeddings, session
history). `delete_agent` deletes the Agent CRD from Git and the AgentRegistry record, but
there is no task or code that removes the associated pgvector data.

**Questions:**
1. When an agent is deleted, is its memory namespace in pgvector deleted, archived,
   or preserved indefinitely?
2. Is there a retention policy (e.g., 30-day soft delete before purge)?
3. Who owns the memory cleanup: `delete_agent` synchronously, a scheduled job, or the
   Outcome Listener consuming a NATS `opensbt_agentDeleted` event?

---

### H-06 · AgentRegistry `GET /v0/agents` pagination: what happens at >30 agents?

**Where:** design.md §3.7, tasks.md Phase 3

**Problem:**
`list_agents` has a `limit: 30` default with no cursor/offset pagination implemented.
If a tenant has 50 agents, the 31st through 50th are silently dropped. The enrichment
loop in `list_agents` only processes what `ListAgents` returns.

**Questions:**
1. Does AgentRegistry OSS support cursor-based or offset pagination?
2. Should `list_agents` support a `cursor` or `offset` parameter?
3. Is there an implicit limit on agents per tenant (e.g., max 20 per tier) that makes
   this a non-issue at MVP?

---

### H-07 · Spoke Controller RBAC: watches Kagent `agents` CRD — namespace scope unclear

**Where:** design.md §11.4 (Spoke Controller RBAC), tasks.md Phase 8

**Problem:**
The ClusterRole grants `get/list/watch` on `kagent.dev/agents` cluster-wide. In a Spoke
Pool cluster, agents from multiple tenants run in different namespaces (`tenant-acme`,
`tenant-other`). The Spoke Controller watches all of them. Its `syncToHub` call includes
`agent.Labels["tenant-id"]` to scope the PostgREST write.

If an agent CRD is missing the `tenant-id` label (e.g., a platform-managed agent deployed
directly by GitOps without going through `deploy_agent`), the Spoke Controller will write
a row with `tenant_id = ""` to Hub Centralised DB. RLS may silently drop or fail this write.

**Questions:**
1. Should the Spoke Controller skip reconciliation for agents that are missing required
   labels (`tenant-id`, `agent-id`, `deployment-id`)?
2. Platform agents deployed via GitOps (e.g., `prometheus-worker-agent`) — should the
   Spoke Controller report their status too, or only tenant agents?
3. If platform agents should be reported, which `tenant_id` do they use, and is there a
   separate table in Hub Centralised DB for platform agent status?

---

### H-08 · `opensbt_agentCreated` NATS event: subject path does not match agreed namespace

**Where:** design.md §3.1 (NATS event), tasks.md Phase 3

**Problem:**
The design publishes to event `DetailType: "opensbt_agentCreated"` using source
`"opensbt.control.plane"`. This is the open-sbt event naming convention, not the
agreed Zero-Ops NATS subject namespace.

The agreed v9.0 NATS subjects are:
- `spoke.{tenant-id}.billing.usage` — billing
- `spoke.{tenant-id}.lifecycle.>` — lifecycle events
- `spoke.{tenant-id}.notifications.>` — notifications

`opensbt_agentCreated`, `opensbt_agentDeployed`, `opensbt_agentDeleted` are Hub Event
Router subjects, not spoke leaf node subjects. The publish happens from `mcp-server`
running on the Hub — it should publish to the Hub NATS JetStream directly, not a spoke
leaf node subject.

**Questions:**
1. What is the correct NATS subject for agent lifecycle events published from the Hub?
   Proposal: `hub.platform.agent.created`, `hub.platform.agent.deployed`, etc.
2. Does the Hub Event Router need to be updated to subscribe to these new subjects, or
   do they bypass the Event Router and go directly to billing/notifications?
3. Who subscribes to these events in the current Hub Event Router? If nobody, should
   the NATS publish be deferred to a later phase?

---

### H-09 · `IProvisioner.DeleteManifest`: does this method exist?

**Where:** design.md §3.8 (delete_agent), tasks.md Phase 6

**Problem:**
The `delete_agent` flow calls `s.controlPlane.Provisioner().DeleteManifest(ctx, ...)`.
The `IProvisioner` interface defined in the platform design has:
- `ProvisionTenant` — commits a new CR to Git
- `DeprovisionTenant` — commits a deletion to Git
- `UpdateTenantResources`

There is no `DeleteManifest` method defined. The interface was designed for AINativeSaaS
CRs (full environment), not individual agent CRD files within a tenant's Git repo.

**Questions:**
1. Does `IProvisioner` need a new `DeleteManifest(path, message)` method, or should the
   agent CRD deletion use a different mechanism (e.g., committing an empty file, removing
   from a Kustomize base list)?
2. Is `CommitManifest` idempotent — does committing the same file path twice overwrite,
   or create a conflict?
3. What is the Git path structure for agent CRDs? Design shows
   `agents/agent-{agent_id}.yaml` — is this inside `overlays/{tier}/` (matching the
   AINativeSaaS pattern) or a separate top-level directory?

---

## Clarifications — needed before the affected task, not blockers

---

### C-01 · `version: "latest"` is a string, not a real version

**Where:** design.md §3.8, tasks.md Phase 6

`delete_agent` defaults `version = "latest"`. AgentRegistry uses `UNIQUE(name, version)`.
If the user created the agent with `version: "v1"`, `DELETE /v0/agents/{name}/versions/latest`
will return 404. There is no mechanism to look up the latest version.

**Action:** Either remove the `version` parameter from `delete_agent` (always delete all
versions), or require the caller to pass the exact version string used during `create_agent`.

---

### C-02 · `create_agent` name validation: `^[a-z0-9-]+$` conflicts with Kagent naming

**Where:** design.md §3.1 input schema

The name regex `^[a-z0-9-]+$` produces names like `billing-agent`. The generated Kagent
CRD uses this as the Kubernetes resource name in `metadata.name`. Kubernetes resource
names must not start or end with a hyphen. The regex does not prevent names like `-billing`
or `billing-`.

**Action:** Update regex to `^[a-z0-9][a-z0-9-]*[a-z0-9]$` (must start and end with
alphanumeric).

---

### C-03 · Design §9 (Implementation Phases) references `list_providers` which was removed

**Where:** design.md §9, Phase 1

Section 9 Phase 1 still lists "Implement `list_providers` tool" as a Week 1 task despite
§3.4 explicitly marking it removed. This creates confusion about what Phase 1 actually delivers.

**Action:** Remove `list_providers` from §9 Phase 1 task list.

---

### C-04 · Tasks.md Phase 8 says Spoke Controller watches "Agent CRDs" — but the agreed Spoke Controller watches Crossplane claims

**Where:** tasks.md Phase 8.1.2

The existing Spoke Controller (in `operators/spoke-controller/`) already watches Crossplane
`AINativeSaaS` claim conditions. Phase 8 adds a second reconciler `agent_controller.go` to
the same binary that watches Kagent `agents.kagent.dev` CRDs.

This is valid, but the task description says "Implement Spoke Controller" as if it is a new
standalone binary, conflicting with the monorepo structure that places it at
`operators/spoke-controller/`.

**Action:** Clarify task 8.1.1: the new `agent_controller.go` is an additional reconciler
added to the existing `operators/spoke-controller/` binary, not a new binary.

---

### C-05 · Phase ordering: Phase 7 (list_authorized_tools) is after Phase 6 (delete)

**Where:** tasks.md Phase 7

`list_authorized_tools` is a prerequisite for `create_agent` (Phase 3), not an afterthought.
A tenant cannot know which tools to include in their agent definition without first calling
`list_authorized_tools`. Placing it in Phase 7 means four phases of create/deploy/update/
delete are implemented before the tool that enables informed tool selection.

**Action:** Move Phase 7 to Phase 2 (after validators, before create_agent), since
`authorized_tools` table is already being seeded in Phase 1.

---

### C-06 · Test in §8.1 asserts `status: "pending"` but spec defines `status: "deploying"`

**Where:** design.md §8.1 (E2E test)

```go
assert.Equal(t, "pending", deployResult["status"])  // test code
// vs
"status": "deploying"  // §4.2 status lifecycle
```

One of these is wrong. If the status `"pending"` is intentional, it must be added to the
status lifecycle definition in §5.1 error codes. If it is a typo for `"deploying"`, fix it.

**Action:** Align test assertion with the status lifecycle definition in §4.2 / §5.1.

---

### C-07 · No rate limit or quota defined for agents per tenant

**Where:** design.md §10.1 config, tasks.md (no quota task)

The `tier_tools` config defines which tool types are available per tier. There is no
defined quota for: (a) max agents per tenant, (b) max deployments per tenant, (c) max
concurrent running agent pods per tenant.

Without quotas, a basic-tier tenant can create 1000 agents and deploy them all, consuming
unbounded spoke compute and Hub DB capacity.

**Action:** Define per-tier agent and deployment quotas in the `available_models` /
`authorized_tools` schema, or add a new `tenant_quotas` table, and add a quota check
to `create_agent` and `deploy_agent`.

---

## Summary table

| ID | Category | Phase affected | Risk |
|---|---|---|---|
| B-01 | AgentRegistry tenant isolation | Phase 1 | Cross-tenant data collision |
| B-02 | Dual state store inconsistency | Phase 4–5 | Silent data loss, leaked Git manifests |
| B-03 | `spoke_cluster_id` in JWT | Phase 4 | Wrong cluster routing or panic |
| B-04 | Delete ordering / async gap | Phase 6 | Ghost pods, billing leak after delete |
| H-01 | `available_models` update path | Phase 1 | Model name drift, stale validation |
| H-02 | `list_agents` N+1 queries | Phase 3 | >2s response for 30 agents |
| H-03 | KEDA trigger undefined | Phase 5 | Scale-to-zero never works |
| H-04 | Update rollout with active sessions | Phase 6 | Dropped A2A calls during update |
| H-05 | Memory cleanup on delete | Phase 6 | pgvector data leaked indefinitely |
| H-06 | Pagination >30 agents | Phase 3 | Silent data truncation |
| H-07 | Spoke Controller RBAC + unlabelled CRDs | Phase 8 | RLS write failure, noisy logs |
| H-08 | NATS subject path mismatch | Phase 3 | Events never consumed |
| H-09 | `IProvisioner.DeleteManifest` missing | Phase 6 | Compile error at Phase 6 |
| C-01 | `version: "latest"` not resolvable | Phase 6 | 404 on delete for most agents |
| C-02 | Name regex allows invalid K8s names | Phase 3 | CRD apply fails at runtime |
| C-03 | Stale `list_providers` in §9 | Phase 1 | Confusion about deliverables |
| C-04 | Spoke Controller binary ambiguity | Phase 8 | Duplicate binary risk |
| C-05 | Phase 7 ordering wrong | All | Blocked create_agent UX |
| C-06 | Test asserts wrong status value | Phase 3 | Failing tests from day 1 |
| C-07 | No agent/deployment quotas | Phase 3–4 | Unbounded resource consumption |

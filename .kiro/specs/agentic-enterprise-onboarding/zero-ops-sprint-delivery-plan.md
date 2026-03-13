# Journey A — Single-Sprint Delivery Plan
## Incremental, Demo-Ready, Outcome-Driven

---

## How to read this plan

The 19 requirements have hard technical dependencies that cannot be broken. What **can** be broken is the assumption that each requirement ships as a single unit. This plan slices requirements into **demo-able outcomes** — each day's demo shows a real end-to-end behaviour the stakeholder can see and understand, not internal implementation.

**Sprint assumption:** 10 working days (2 weeks). Demo each morning to stakeholders, showing the outcome completed the previous day.

**Parallelism principle:** Infrastructure/platform work (auth plumbing, Git bootstrap, Crossplane) runs in parallel with API shape work (schemas, MCP tool stubs) and frontend work (console UI). They converge for the first live E2E demo on Day 5.

---

## Dependency map (why this order)

```
[Req 12 + 13]  OAuth discovery + client registration
      │
      ▼
[Req 2 + 10]   PKCE flow + JWKS cache          ← auth foundation
      │
      ▼
[Req 3]        AgentGateway validate/authorize   ← enforcement point
      │
      ├──────────────────────────────────────┐
      ▼                                      ▼
[Req 15]  Git bootstrap              [Req 11]  YAML parser (internal, parallel)
      │
      ▼
[Req 4]   tenant_create (DB + identity + Git)
      │
      ▼
[Req 14]  Token refresh (force_token_refresh path)
      │
      ▼
[Req 5]   credential_submit (console form + KSOPS)
      │
      ▼
[Req 6]   environment_create (Git commit intent)
      │
      ├──────────────────────────────────────┐
      ▼                                      ▼
[Req 7]   Crossplane Composition B    [Req 16+9] Status schema + on-demand query
      │                                      │
      ▼                                      ▼
[Req 8]   Error handling              [Req 17]  Conversational resumability
                                              │
                                              ▼
                                       [Req 18]  Console polling
                                              │
                                              ▼
                                       [Req 19]  Delete / approval workflow
```

---

## Sprint Board — Day-by-Day

### Day 1 — "The platform knows who you are"
**Demo outcome:** Open Cursor, type a command, browser opens, user logs in, IDE receives a valid JWT. Stakeholder sees zero manual configuration.

| # | Requirement slice | Owner |
|---|---|---|
| 1 | Req 12: Deploy `/.well-known/oauth-protected-resource` on AgentGateway | Platform |
| 2 | Req 12: Deploy `/.well-known/oauth-authorization-server` on identity-service (proxying Hydra) | Platform |
| 3 | Req 13: Pre-register `mcp-public-client` in Hydra with all port variants + cursor:// scheme | Platform |
| 4 | Req 2: Cursor generates code_verifier, code_challenge, opens browser to authorization endpoint | Client |
| 5 | Req 2: Cursor binds loopback listener (port priority: 54321 → 18999 → 3000), handles callback | Client |
| 6 | Req 2: Cursor exchanges auth code for tokens, stores in OS keychain | Client |
| 7 | Req 10: AgentGateway fetches and caches JWKS from identity-service on startup | Platform |

**Demo script:** "I type 'create tenant acme' in Cursor. The browser opens Kratos login. I log in. The browser closes. Cursor says it's authenticated."

---

### Day 2 — "Only the right people get through"
**Demo outcome:** Authenticated requests reach backend services. Unauthenticated or under-privileged requests are correctly rejected at the gateway — demonstrated live.

| # | Requirement slice | Owner |
|---|---|---|
| 8 | Req 3: AgentGateway validates JWT signature (using Day 1 JWKS cache), checks exp claim | Platform |
| 9 | Req 3: AgentGateway calls identity-service → Keto for permission check | Platform |
| 10 | Req 3: AgentGateway forwards X-User-ID, X-Tenant-ID, X-User-Email, X-User-Role, X-Scopes headers to backend | Platform |
| 11 | Req 3: zero_ops_api enforces NetworkPolicy — only accepts traffic from AgentGateway namespace | Platform |
| 12 | Req 3: Return HTTP 401 (expired token) and HTTP 403 (permission denied) correctly | Platform |
| 13 | Req 13 CIMD: identity-service validates CIMD metadata documents and SSRF protections | Platform |

**Demo script:** "I call the API with no token — 401. I call with a valid token but wrong scope — 403. I call with a valid token and correct scope — request reaches the backend. All enforced at one point, no backend changes needed."

---

### Day 3 — "The platform can commit to Git securely"
**Demo outcome:** The platform bootstraps from a single injected secret, authenticates to GitHub, and commits a file. Stakeholder sees the commit appear in GitHub. This is the GitOps trust chain made visible.

| # | Requirement slice | Owner |
|---|---|---|
| 14 | Req 15 AC1: `zero-ops mgmt bootstrap` CLI injects Master Platform Age Private Key | Platform |
| 15 | Req 15 AC2: ArgoCD/KSOPS decrypts GitHub App Private Key from fleet-registry Git at apply-time | Platform |
| 16 | Req 15 AC3–AC4: zero_ops_api generates GitHub App Installation Token, caches 55 min | Backend |
| 17 | Req 15 AC5–AC6: Single idempotent retry on HTTP 401 from GitHub; terminal failure on second failure | Backend |
| 18 | Req 11: AINativeSaaS_CR YAML parser + pretty-printer (internal library, ships today — no user-facing demo) | Backend |
| 18a | **API Standards:** Apply RHOAS API Guidelines to zero_ops_api error responses (RFC 7807 Problem Details), pagination patterns, HTTP status code conventions. Add Spectral ruleset for CI validation. | Backend |

**Demo script:** "We bootstrap from one secret injected at cluster creation. Everything else decrypts from Git. I show the platform making a test commit to GitHub using a short-lived token it generated itself. No credentials in environment variables."

**Reference Standards:**
- `.kiro/specs/agentic-enterprise-onboarding/analysis/redhat/01-app-services-api-guidelines.md`
- `.kiro/specs/agentic-enterprise-onboarding/references/redhat/app-services-api-guidelines/docs/api-standards.md`

---

### Day 4 — "A tenant exists in under 60 seconds"
**Demo outcome:** Cursor sends `tenant_create`. A tenant record appears in Postgres, a Kratos identity trait is updated, a Keto tuple is written, a GitHub repo is created, and the fleet-registry is committed — all from one command. Stakeholder sees the GitHub repo appear live.

| # | Requirement slice | Owner |
|---|---|---|
| 19 | Req 4 AC1: Single-tenant-per-user check with defined HTTP 403 error body | Backend |
| 20 | Req 4 AC2: PostgreSQL insert with UNIQUE constraint on tenant_name | Backend |
| 21 | Req 4 AC3: Kratos trait update + Keto tuple creation; INCOMPLETE_IDENTITY_SETUP on failure | Backend |
| 22 | Req 4 AC4: Age keypair generation; private key stored as K8s Secret; backup to S3; public key in .sops.yaml | Backend |
| 23 | Req 4 AC5: INCOMPLETE_IDENTITY_SETUP retry auto-proceeds to Git steps on success, returns HTTP 201 + force_token_refresh | Backend |
| 24 | Req 4 AC6: HTTP 201 `{"force_token_refresh": true, "tenant_id": "..."}` | Backend |
| 25 | Req 4 AC7–AC9: HTTP 200 idempotency responses with correct phase and force_token_refresh: false | Backend |
| 26 | Req 4 AC10–AC14: Git repo creation, Kustomize scaffold commit, fleet-registry commit; INCOMPLETE_GIT_SETUP with OpenSearch alert on failure | Backend |
| 26a | **GitOps Repo Structure:** Apply Red Hat GitOps repository pattern. Create base/ + overlays/ structure with .sops.yaml, Kustomize scaffold for starter/enterprise tiers, ArgoCD Application CRs. | Backend |
| 27 | Req 1: Cursor initiates the full flow from natural language command | Client |
| 28 | Req 14: Token refresh flow (transparent on 401; force_token_refresh path; claim re-hydration from Kratos) | Client + Platform |

**Demo script:** "I type 'create tenant acme-corp' in Cursor. I see the browser, I log in, the token refreshes with the new tenant_id. Here is the acme-corp-control-plane repository that just appeared in GitHub. Here is the Kustomize scaffold inside it."

**Reference Standards:**
- `.kiro/specs/agentic-enterprise-onboarding/analysis/redhat/07-gitops-repo-structure.md`
- `.kiro/specs/agentic-enterprise-onboarding/references/redhat/gitops-repo-example/environments/`

---

### Day 5 — "Credentials reach Git without touching the agent"
**Demo outcome:** Cursor displays the console URL and exits. The Tenant Admin opens the console, submits a Hetzner API token via a form — it never appears in the IDE or any log. The encrypted secret appears committed to Git. Stakeholder sees the security principle in action.

| # | Requirement slice | Owner |
|---|---|---|
| 29 | Req 5 AC1–AC3: Cursor displays console URL and exits immediately | Client |
| 30 | Req 5 AC4–AC5: Platform Console Kratos session auth + Keto permission check for credential submission | Frontend |
| 31 | Req 5 AC6–AC9: Console form → HTTPS POST → zero_ops_api → Age encryption → SOPS-encrypted commit to Git | Backend + Frontend |
| 32 | Req 5 AC10–AC11: Age private key stored as K8s Secret; backed up to S3 | Backend |
| 33 | Req 5 AC12–AC17: Idempotent resubmission (last-write-wins), HTTP 401 re-auth without clearing form, HTTPS requirement | Backend + Frontend |

**Demo script:** "Cursor shows the credentials URL and stops. I open the console, type the Hetzner token, submit. Here is the encrypted secret committed to the tenant's Git repository. The token never appeared in the IDE, the logs, or any API response."

---

### Day 6 — "Infrastructure provisioning is one command"
**Demo outcome:** Cursor sends `environment_create`. The AINativeSaaS CR appears committed to Git. ArgoCD picks it up. Crossplane starts provisioning. Stakeholder sees the CR in Kubernetes and the first Crossplane conditions within minutes.

| # | Requirement slice | Owner |
|---|---|---|
| 34 | Req 6 AC1–AC3: Plan/tier entitlement check with HTTP 403 + Cursor display | Backend + Client |
| 35 | Req 6 AC4–AC5: environment_id construction; HTTP 409 on parameter conflict | Backend |
| 36 | Req 6 AC6–AC9: Git commit of AINativeSaaS_CR; HTTP 202 response with console_url; HTTP 200 idempotent retry | Backend |
| 37 | Req 6 AC10–AC12: Cursor displays console_url and exits; no polling | Client |
| 38 | Req 7 AC1–AC3: Crossplane detects CR, selects Composition_B, decrypts Hetzner token via KSOPS | Infra |
| 39 | Req 7 AC4: provider-kubernetes copies Age private key to tenant cluster with least-privilege RBAC | Infra |
| 40 | Req 7 AC5: Tenant cluster bootstraps ArgoCD + KSOPS with injected Age key | Infra |
| 40a | **ArgoCD RBAC:** Apply GitOps Operator multi-tenant RBAC patterns. Management cluster ArgoCD gets ClusterRole (read-only cluster access + full tenant namespace control). Tenant cluster ArgoCD gets namespace-scoped Role. Create AppProject templates for tenant isolation. | Infra |
| 40b | **Helm Patterns:** Apply RHDH Helm chart patterns. Add ServiceMonitor templates, values.schema.json validation, external database configuration pattern to all Helm charts (Ory stack, auth-proxy). | Infra |

**Demo script:** "I type 'provision production environment on Hetzner Frankfurt'. Cursor acknowledges and exits. Here is the CR committed to Git. Here is ArgoCD picking it up. Here are the first Crossplane conditions showing the cluster coming up."

**Reference Standards:**
- `.kiro/specs/agentic-enterprise-onboarding/analysis/redhat/02-gitops-operator.md`
- `.kiro/specs/agentic-enterprise-onboarding/references/redhat/gitops-operator/controllers/argocd/argocd.go` (lines 186-190)
- `.kiro/specs/agentic-enterprise-onboarding/analysis/redhat/08-rhdh-helm-patterns.md`
- `.kiro/specs/agentic-enterprise-onboarding/references/redhat/rhdh-chart/docs/external-db.md`

---

### Day 7 — "You can always see what's happening"
**Demo outcome:** The status API works for every phase. Stakeholder sees the agent querying live Crossplane conditions from the IDE, and the Platform Console auto-updating every 10 seconds without the agent being involved.

| # | Requirement slice | Owner |
|---|---|---|
| 41 | Req 16 AC1: Both endpoints deployed — environment-scoped + tenant-level; correct phase routing | Backend |
| 42 | Req 16 AC4–AC5: Unified response schema with all 8 phases; phase derivation rules from Crossplane conditions | Backend |
| 43 | Req 16 AC6–AC8: All 8 summary_messages defined; Degraded remediation; 5-second K8s cache | Backend |
| 44 | Req 9 AC1–AC9: environment_status MCP tool; Cursor displays phase + message; Ready shows endpoints; Degraded shows remediation | Backend + Client |
| 45 | Req 9 AC10–AC12: environments_list MCP tool; schema; empty-array for no environments; disambiguation prompt | Backend + Client |
| 46 | Req 18 AC1–AC6: Platform Console 10-second polling; stops on Ready/Degraded; 20-minute soft timeout; JWT on all requests | Frontend |
| 47 | Req 8 AC1–AC8: QuotaExceeded + AuthenticationFailed conditions; continuous reconciliation; no terminal failure | Infra |

**Demo script:** "I ask Cursor 'is my environment ready?' It queries the API and shows me 'Provisioning — estimated 8 minutes remaining.' Over here on the Platform Console, the status is updating every 10 seconds automatically. The agent has already exited."

---

### Day 8 — "Close your IDE, come back tomorrow, nothing is lost"
**Demo outcome:** The session is closed mid-flow. A new session opens. The agent queries state, detects where the user left off, and resumes with a clear next-step prompt. Stakeholder sees all four resumption paths demonstrated.

| # | Requirement slice | Owner |
|---|---|---|
| 48 | Req 17 AC1–AC2: State persists in PostgreSQL + Git; new session calls environments_list then routes to correct endpoint | Client |
| 49 | Req 17 AC4: INCOMPLETE_IDENTITY_SETUP + INCOMPLETE_GIT_SETUP resumption prompt and tenant_create retry | Client |
| 50 | Req 17 AC5: AWAITING_CREDENTIALS resumption → credential submission prompt | Client |
| 51 | Req 17 AC6: CREDENTIALS_READY resumption → Cursor re-prompts for tier/cloud/region then invokes environment_create | Client |
| 52 | Req 17 AC7–AC9: Provisioning/Degraded shows status + console_url; Ready shows resource summary; Console always current | Client + Frontend |

**Demo script:** "I close Cursor mid-onboarding. I reopen it hours later and ask 'where did I get to?' Cursor queries the API, detects we are in CREDENTIALS_READY state, and asks me to confirm the environment parameters before provisioning. Nothing was lost."

---

### Day 9 — "You can delete safely, with guardrails where it matters"
**Demo outcome:** The full deletion flow — immediate deletion for never-Ready environments, and the approval ticket flow for previously-Ready ones. Stakeholder sees the Hetzner resources actually disappear within 3 minutes.

| # | Requirement slice | Owner |
|---|---|---|
| 53 | Req 19 AC1–AC3: Cursor invokes environment_delete; Keto delete-permission check; immediate deletion for never-Ready | Backend + Client |
| 54 | Req 19 AC4–AC6: Approval ticket generated for previously-Ready; HTTP 403 with approval_url; Cursor displays ticket link | Backend + Client |
| 55 | Req 19 AC5: Idempotent re-invocation returns existing pending ticket | Backend |
| 56 | Req 19 AC7–AC8: Any tenant_admin or platform_admin can approve; Platform Console notification; cancel option | Backend + Frontend |
| 57 | Req 19 AC9–AC10: 7-day expiry with "Expired - Request New Deletion" console display; immediate re-invocation allowed after expiry or cancel | Backend + Frontend |
| 58 | Req 19 AC11–AC18: Git commit of CR removal; HTTP 202 on success; HTTP 500 on Git failure; Cursor displays teardown message; ArgoCD prunes; Crossplane finalizers; OpenSearch audit; idempotent already-deleted path | Backend + Infra |

**Demo script:** "I delete a never-Ready environment — gone immediately, Crossplane garbage-collects in under 3 minutes. I try to delete a production environment that has been Ready — I get the approval ticket URL. I approve it in the console. ArgoCD detects the removal. The Hetzner VMs disappear."

---

### Day 10 — Buffer, hardening, and stakeholder-driven replay
**Purpose:** No new features. Fix anything that broke during Days 1–9, close remaining edge cases, and run the full end-to-end journey live from scratch for stakeholders.

| # | Activity |
|---|---|
| 59 | Fix any broken ACs surfaced during Days 1–9 demos |
| 60 | Req 4 AC17–AC18: UNIQUE constraint concurrent-violation returns HTTP 200 (race condition test) |
| 61 | Req 15 AC7–AC8: Disaster recovery — destroy management cluster, bootstrap from vault key, verify auto-reconcile |
| 62 | Full E2E run from cold: authenticate → create tenant → submit credentials → provision → query status → delete |

**Demo script:** Full journey from zero. No shortcuts. Stakeholder nominates the tenant name and environment suffix live.

---

## Parallel tracks summary

To hit Day 10, three tracks run simultaneously from Day 1:

| Track | Owner | Days |
|---|---|---|
| **Platform / Infra** | Platform + Infra engineers | Days 1–7 continuous (auth → enforcement → Git bootstrap → Crossplane) |
| **Backend API** | Backend engineers | Days 3–9 (Req 4 → 5 → 6 → 8 → 16 → 17 → 19) |
| **Client + Frontend** | Client + Frontend engineers | Days 1, 4–9 (Cursor PKCE Day 1; MCP tool calls Days 4–9; Console Days 5–9) |

The tracks converge for the first time on **Day 5** (credential submission is the first operation that requires all three tracks working together).

---

## What stakeholders see each day

| Day | Visible outcome |
|---|---|
| 1 | Browser opens from IDE, user logs in, token issued — zero config |
| 2 | Unauthenticated / unauthorised calls visibly rejected at the gateway |
| 3 | Platform makes a live commit to GitHub from a generated token |
| 4 | Tenant created: Postgres record + GitHub repo + Kustomize scaffold appear live |
| 5 | Credentials submitted via console — encrypted secret committed to Git, never in agent |
| 6 | `environment_create` → AINativeSaaS CR in Git → Crossplane begins provisioning |
| 7 | Live status visible in both IDE (on-demand) and Console (auto-polling) simultaneously |
| 8 | IDE closed mid-flow; reopened; agent resumes from correct state without manual recovery |
| 9 | Immediate delete and approval-gated delete both demonstrated live; Hetzner VMs disappear |
| 10 | Complete journey from cold, end-to-end, stakeholder-driven |

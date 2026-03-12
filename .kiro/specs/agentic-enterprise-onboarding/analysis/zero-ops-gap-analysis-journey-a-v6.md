# Journey A — Gap Analysis v6

**Spec:** Journey A — Agentic Enterprise Onboarding (latest upload)  
**Date:** 2026-03-12  
**Verdict: NOT YET development-ready.** Two Critical gaps must be resolved before sprint planning. Four High gaps are design-spec blockers. The rest are well-scoped and solvable in a single product refinement session.

---

## What improved since the last version

The following previously-raised gaps are now resolved and **will not be re-raised**:

- ✅ Keto/tenant_id bootstrap sequence — Req 4 AC3 now defines it explicitly
- ✅ Age key pair generation timing — Req 4 AC4 places it inside tenant_create
- ✅ environment_suffix is now a required parameter — silent "production" default removed (Req 6 AC4)
- ✅ HTTP 409 added for environment_id conflict with different parameters (Req 6 AC5)
- ✅ Pending state source-of-truth — Req 16 AC5 now uses PostgreSQL intent record as fallback
- ✅ CREDENTIALS_READY added to phase enum and Req 17 resumability handler (Req 17 AC5)
- ✅ Network isolation stated as a requirement (Req 3 AC12)
- ✅ Platform Admin multi-tenant exemption flag exists in Req 4 AC1

---

## Gap Summary

| ID | Severity | Req | Title |
|---|---|---|---|
| [G-01](#g-01) | **Critical** | Req 4 AC5 | Forced token-refresh directive has no defined HTTP mechanism |
| [G-02](#g-02) | **Critical** | Req 4 AC1 | Single-tenant-per-user rule directly contradicts the idempotency contract |
| [G-03](#g-03) | **High** | Req 4 AC4 | S3 bucket for Age key backup cannot exist before BYOC credentials are submitted |
| [G-04](#g-04) | **High** | Req 4 AC3 | Keto tuple and Kratos trait update are not atomic with the DB insert — partial failure states undefined |
| [G-05](#g-05) | **High** | Req 7 | Tenant Age key transfer from management cluster into tenant cluster is unspecified |
| [G-06](#g-06) | **High** | Req 19 | Approval ticket approver RBAC, notification, and no-active-admin escalation undefined |
| [G-07](#g-07) | **Medium** | Req 6 AC5/AC8 | HTTP 409 vs idempotent HTTP 200 boundary condition is ambiguous |
| [G-08](#g-08) | **Medium** | Req 17 AC5 | CREDENTIALS_READY resumption invokes environment_create but parameter source is undefined |
| [G-09](#g-09) | **Medium** | Req 19 | No "Deleting" phase — teardown window creates idempotency ambiguity |
| [G-10](#g-10) | **Medium** | Req 16 AC5 | CREDENTIALS_READY detection logic queries tenant record but environment_status takes environment_id |
| [G-11](#g-11) | **Low** | Req 5 | AC sequence skips 12–14; duplicate AC11 |
| [G-12](#g-12) | **Low** | Req 6 | Duplicate AC numbered "6" |
| [G-13](#g-13) | **Low** | Req 7 | Duplicate ACs numbered "4" and "5" |
| [G-14](#g-14) | **Low** | Req 19 | Duplicate AC numbered "5" |
| [EC-01](#ec-01) | **Confirm** | Req 19 AC3 | Previously-Ready-now-Degraded: does approval still apply? |
| [EC-02](#ec-02) | **Confirm** | Req 6, Req 9 | Multi-environment per tenant: listing and status disambiguation |
| [EC-03](#ec-03) | **Confirm** | Req 4 AC9 | INCOMPLETE_GIT_SETUP: no platform alert, no user-distinguishable error |
| [EC-04](#ec-04) | **Confirm** | Req 2, Req 13 | cursor:// custom URI scheme: usage priority and OS fallback undefined |

---

## Critical

> Must be resolved before sprint planning. These gaps mean at least one service cannot be fully specified.

---

### G-01

**Severity:** Critical  
**Req:** Req 4 AC5

#### Forced token-refresh directive has no defined HTTP mechanism

AC5 states: *"zero_ops_api SHALL return HTTP 201 with a directive instructing the Cursor client to immediately force a Token Refresh."* The word **directive** is undefined. There is no HTTP field, response body key, header name, or protocol signal specified that carries this instruction. Without it, neither side can be built.

Additionally, Req 14 describes token refresh as a *reactive* behaviour — triggered only by a 401 from AgentGateway. AC5 requires a *proactive* refresh triggered by a 201 response. This is a different trigger path that Req 14 does not cover.

**Questions:**
1. What is the exact mechanism for this directive — a response body field (e.g. `"force_token_refresh": true`), a custom HTTP response header, or an implicit convention (Cursor always refreshes after every tenant_create 201)?
2. What is the complete HTTP 201 response schema for tenant_create? The current spec has no defined body for this response.
3. Should Req 14 include an AC for proactive refresh triggered by a server directive, distinct from the existing reactive 401-triggered path?
4. If the forced token refresh fails after the 201 (e.g. network timeout), what does Cursor do — retry the refresh, surface an error, or proceed with a stale token that lacks the tenant_id claim and will cause all subsequent calls to fail?

---

### G-02

**Severity:** Critical  
**Req:** Req 4 AC1

#### Single-tenant-per-user rule directly contradicts the idempotency contract

AC1 states: *"zero_ops_api SHALL verify the user does not already have an assigned tenant_id (unless invoked by a Platform Admin)."* This conflicts with the spec's stated idempotency contract (*"ALL tenant_create calls with the same tenant name SHALL be safe to retry infinitely"*, AC12).

If a user successfully creates a tenant, their identity now has a tenant_id. Any subsequent retry of tenant_create — whether for idempotency, a transient network error, or a resumed Cursor session — will be blocked by AC1. The idempotency path defined in AC6–7 can never be reached for an existing user.

Further: if a non-admin user who already has a tenant_id calls tenant_create again, the spec does not define the HTTP status code or error body returned. An implementer has no contract to build against.

**Questions:**
1. Should AC1 distinguish between same-name retry (idempotent path → fall through to AC6) and different-name attempt (new tenant → HTTP 409 with defined error body)? If yes, state this explicitly.
2. What HTTP status and error body does zero_ops_api return when a non-admin user with an existing tenant_id attempts to create a second tenant with a *different* name?
3. What role value in X-User-Role grants Platform Admin status for the AC1 exemption? Is it the literal string `platform_admin`?
4. When a Platform Admin creates a tenant on behalf of another user, whose `sub` is associated with the tenant record and whose identity traits get the Kratos update in AC3?

---

## High

> Significant ambiguity likely to cause mid-sprint rework. Resolve before the design spec is written.

---

### G-03

**Severity:** High  
**Req:** Req 4 AC4

#### S3 bucket for Age key backup cannot exist before BYOC credentials are submitted

AC4 specifies: *"backup the private key to Hetzner S3"* during tenant_create. But this is a BYOC model — the tenant has not yet submitted their Hetzner credentials at this point (that happens in Req 5). zero_ops_api has no Hetzner credentials for this tenant to create or write to an S3 bucket at tenant_create time.

The backup destination is `s3://{tenant}-secrets/age-private-key` (Req 5 AC11). This either requires: (a) a Zero-Ops-owned Hetzner account for S3 storage, or (b) deferring the S3 backup until after credential submission. Neither is stated.

**Questions:**
1. Is the `s3://{tenant}-secrets/` bucket created in the **Zero-Ops platform's own** Hetzner account (not the tenant's), so it can exist before BYOC credentials arrive?
2. If in the Zero-Ops account: what access controls prevent tenants from accessing each other's buckets? Who manages bucket lifecycle?
3. If the bucket must be in the **tenant's** Hetzner account: should the S3 backup step in AC4 be deferred to Req 5 (after BYOC credentials are submitted and the tenant's Hetzner account is accessible)?
4. Should an explicit AC be added covering S3 bucket creation — owner account, naming convention, access policy, and the exact point in the lifecycle when it is created?

---

### G-04

**Severity:** High  
**Req:** Req 4 AC3

#### Keto tuple creation and Kratos trait update are not atomic with the DB insert — partial failure states are undefined

AC3 specifies three sequential operations: (1) DB insert (AC2), (2) Kratos Admin API call to update identity traits, (3) Keto call to create the relationship tuple. These are not atomic. If the DB insert succeeds but the Kratos update fails, the tenant record exists but the user's JWT will never contain a tenant_id claim (breaking the forced token refresh in AC5). If Kratos succeeds but Keto fails, the user has a tenant_id in their JWT but AgentGateway will deny every subsequent request because no Keto policy permits them.

Neither partial failure state is handled in the spec. There is no rollback, retry, or compensating transaction defined.

**Questions:**
1. If the Kratos trait update fails after a successful DB insert, should zero_ops_api retry? Mark the tenant as `INCOMPLETE_IDENTITY_SETUP` (analogous to `INCOMPLETE_GIT_SETUP`)? Both?
2. If the Keto tuple creation fails after Kratos succeeds, what happens? The user has a tenant_id claim but will get HTTP 403 on every subsequent call with no path to recover.
3. Should all three steps (DB insert + Kratos update + Keto tuple) be wrapped in a compensating transaction pattern, with a retry-able `INCOMPLETE_IDENTITY_SETUP` state analogous to `INCOMPLETE_GIT_SETUP`?
4. Is the INCOMPLETE_GIT_SETUP retry path (AC10) also expected to retry failed Kratos/Keto steps, or is it strictly scoped to Git operations?

---

### G-05

**Severity:** High  
**Req:** Req 7 AC4, AC5

#### Tenant Age key transfer from management cluster into tenant cluster is unspecified

Req 4 AC4 stores the tenant Age private key as a Kubernetes Secret in the management cluster. Req 7 AC4 states Composition B SHALL inject this key into the tenant cluster. But the mechanism for cross-cluster secret transfer during Crossplane composition execution is not defined. This is a non-trivial, security-sensitive engineering decision.

AC4 and AC5 are also near-identical in content (both describe installing ArgoCD, KSOPS, and the Age private key in the tenant cluster), creating an editorial ambiguity about whether they are the same step described twice or two distinct steps.

**Questions:**
1. What is the exact mechanism for injecting the tenant Age private key into the tenant cluster — a Crossplane `provider-kubernetes` Object resource copying the Secret, a Crossplane EnvironmentConfig, an ArgoCD secret sync, or something else?
2. Is the tenant Age private key readable by all processes in the management cluster namespace, or is it scoped to a specific Crossplane service account?
3. Are AC4 and AC5 intentionally describing two distinct operations, or is AC5 a duplicate? If distinct, what is the difference?
4. Should a requirement explicitly state which Crossplane provider (provider-kubernetes, provider-helm) is responsible for the bootstrap injection step?

---

### G-06

**Severity:** High  
**Req:** Req 19 AC4

#### Approval ticket approver RBAC, notification, and no-active-admin escalation are all undefined

AC4 generates an approval ticket "assigned to the Tenant's administrators" without specifying: (a) whether *any* tenant_admin can approve or only the initiating admin, (b) how tenant administrators are notified, (c) whether a platform_admin can override the ticket for contract termination scenarios, (d) what happens if the 7-day window lapses and the organisation has no active admins.

**Questions:**
1. Can any tenant_admin for the tenant approve the deletion ticket, or only the admin who initiated the deletion request?
2. Can a platform_admin approve a tenant's deletion ticket for support or contract termination? If yes, this must be stated explicitly given the BYOC data governance model.
3. What is the notification mechanism — console notification, email, or both? Is this in scope for this requirements document?
4. If the tenant has no active admin when the 7-day window expires, is the environment permanently protected from deletion? What is the escalation path?

---

## Medium

> Design-phase inputs. A decision is needed before architecture is finalised, but these can be resolved during the design spec phase.

---

### G-07

**Severity:** Medium  
**Req:** Req 6 AC5, AC8

#### HTTP 409 vs idempotent HTTP 200 boundary condition is ambiguous

AC5: return HTTP 409 if the same environment_id is submitted with "a different tier/cloud/region."  
AC8: return HTTP 200 (idempotent) if environment_create is called again for the same environment_id.

The boundary is not precise. If the second call has the same environment_id, same tier, same cloud, but a *different region*, does it return 409 (any parameter differs) or 200 (same primary key)? "Different tier/cloud/region" could mean any-one-differs or all-three-differ.

**Questions:**
1. Does HTTP 409 fire if **any single** parameter (tier, cloud, or region) differs from the original, or only if all three differ?
2. Is the intended rule: exact-match on all parameters → HTTP 200; any divergence → HTTP 409?
3. Should the HTTP 409 body include the original parameters so the caller can see what already exists?

---

### G-08

**Severity:** Medium  
**Req:** Req 17 AC5

#### CREDENTIALS_READY resumption invokes environment_create but the source of tier/cloud/region is undefined

AC5: *"IF phase is CREDENTIALS_READY, THE Cursor SHALL prompt the Tenant_Admin to confirm they are ready to provision, and upon confirmation, invoke environment_create."* To invoke environment_create, Cursor needs `tier`, `cloud`, `region`, and `environment_suffix`. These were provided in the original session. If Cursor has closed, those values are gone from client memory.

The spec does not state whether these parameters are stored server-side (in PostgreSQL) at tenant_create or environment_create time, making them available to a resumed session.

**Questions:**
1. Are environment parameters (tier, cloud, region, environment_suffix) persisted to the PostgreSQL tenant record at the time of first invocation, so they are available to Cursor in a resumed session?
2. If not persisted server-side: should Cursor re-prompt the user for all parameters on resumption, rather than implying it can invoke environment_create autonomously?
3. Should an AC be added to Req 4 or Req 6 stating that environment intent parameters are stored in the tenant record?

---

### G-09

**Severity:** Medium  
**Req:** Req 19

#### No "Deleting" phase — the Crossplane teardown window creates idempotency ambiguity

AC9 returns HTTP 202 when the Git CR removal is committed. AC15 returns HTTP 200 for "already deleted." Between those two events — CR removed from Git but Crossplane finalizers still running (~1–3 minutes) — there is no defined phase. Calling environment_delete a second time during this window hits an undefined state: the environment is not "already deleted" (teardown incomplete) but there is no active CR to remove from Git.

**Questions:**
1. Should a `Deleting` phase be added to the environment lifecycle to cover the Git-removal-to-Crossplane-teardown window?
2. What does environment_delete return if called during active teardown — HTTP 202 ("still in progress"), HTTP 200 ("treat as already deleted"), or HTTP 409?
3. How does zero_ops_api distinguish teardown-in-progress from teardown-complete when both states have no AINativeSaaS_CR in Kubernetes?

---

### G-10

**Severity:** Medium  
**Req:** Req 16 AC5

#### CREDENTIALS_READY phase detection requires a tenant-level query but environment_status takes an environment_id

AC5 defines CREDENTIALS_READY as: *"PostgreSQL tenant record exists with credentials submitted, but no environment provisioning intent recorded."* This is a **tenant-level** determination — it requires looking up the tenant record.

But `environment_status` is an **environment-scoped** endpoint: `GET /api/v1/environments/{environment_id}/status`. If no environment has been created yet, there is no environment_id to pass. The query cannot be routed to an environment that does not exist.

This same issue affects Req 17 AC5 — when Cursor calls environment_status to check for CREDENTIALS_READY, what environment_id does it provide?

**Questions:**
1. Should CREDENTIALS_READY be returned by a separate **tenant-level** status endpoint (e.g. `GET /api/v1/tenants/{tenant_id}/status`) rather than environment_status?
2. Or should environment_status accept a null/absent environment_id and fall back to tenant-level state when no environment exists?
3. Req 17 AC5 instructs Cursor to invoke environment_status when in CREDENTIALS_READY phase — what environment_id does Cursor pass at that point?

---

## Low

> Editorial corrections. Apply to the spec immediately to prevent AC reference errors in design specs and test plans.

---

### G-11

**Severity:** Low  
**Req:** Req 5

Req 5 ACs skip from 11 to 15 (ACs 12, 13, 14 are absent), and there are two ACs both numbered 11. Renumber sequentially from AC12 onward to eliminate the gap and duplicate.

---

### G-12

**Severity:** Low  
**Req:** Req 6

Req 6 has two ACs both numbered 6. Renumber: the second "6" ("IF the Git commit fails...") should become AC7, shifting subsequent ACs forward.

---

### G-13

**Severity:** Low  
**Req:** Req 7

Req 7 has two ACs numbered 4 and two numbered 5. The second AC4 ("Composition_B SHALL typically complete within 15 minutes") and second AC5 ("tenant cluster bootstrap SHALL install ArgoCD...") should be renumbered AC6 and AC7. This also overlaps with the duplication issue in G-05.

---

### G-14

**Severity:** Low  
**Req:** Req 19

Req 19 has two ACs both numbered 5. Renumber the second "5" ("WHEN HTTP 403 is returned...") to AC6, shifting subsequent ACs forward.

---

## Confirm

> Edge cases requiring an explicit product decision before design spec. No implementation choice should be made without a documented answer.

---

### EC-01

**Req:** Req 19 AC3

**Previously-Ready-now-Degraded: does the approval requirement still apply?**

AC3 triggers immediate deletion if the environment has *never* achieved Ready. This check happens at deletion request time. A previously-Ready-now-Degraded environment has historically achieved Ready — so the approval requirement applies — even though the environment is currently broken. From the user's perspective this creates friction ("it's broken, why do I need approval to delete it?").

**Confirm:**
1. Is it intentional that a previously-Ready-now-Degraded environment always requires approval for deletion?
2. If yes, should the approval ticket message for a Degraded environment include context that it was previously operational (to help the approver understand the data-loss risk)?
3. Can the tenant cancel a pending approval ticket and immediately re-request? Does a cancellation-and-re-request restart the 7-day clock?

---

### EC-02

**Req:** Req 6, Req 9

**Multi-environment per tenant: listing and status disambiguation**

Req 6 supports multiple environments per tenant via `environment_suffix`. If a tenant has two environments in different states, asking Cursor "What is the status of my environment?" is ambiguous. There is no `list_environments` MCP tool or equivalent defined anywhere in the spec.

**Confirm:**
1. Is a `list_environments` MCP tool in scope for this spec, or is the Platform Console the only way to enumerate environments?
2. When the user asks for status without specifying an environment_id, should Cursor prompt for clarification, or default to the most recently created environment?
3. Is there a maximum number of simultaneous environments per tenant? If so, what is the limit and where is it enforced?

---

### EC-03

**Req:** Req 4 AC9

**INCOMPLETE_GIT_SETUP: no platform alert, no user-distinguishable error**

AC9 returns HTTP 500 with a generic message on Git failure. If the Git provider is down for hours, every retry returns the same message. Users cannot distinguish a transient outage from a persistent platform failure. There is also no mechanism to notify the platform team that a tenant is stuck in this state.

**Confirm:**
1. Should a platform-level alert fire when a tenant remains in `INCOMPLETE_GIT_SETUP` for longer than N minutes? What is N?
2. Should the HTTP 500 body include a distinct `error_code` (e.g. `git_service_unavailable`) so Cursor can surface a more actionable message?

---

### EC-04

**Req:** Req 2, Req 13 AC1

**`cursor://` custom URI scheme: usage priority and OS fallback undefined**

Req 13 AC1 pre-registers `cursor://anysphere.cursor-mcp/oauth/callback`. Req 2 only specifies the loopback HTTP listener pattern (ports 54321, 18999, 3000). No requirement states when Cursor prefers the custom scheme over the loopback, or what happens if `cursor://` is not registered as an OS URI handler.

**Confirm:**
1. Is the `cursor://` scheme a primary or fallback redirect? Under what conditions does Cursor use it instead of the loopback HTTP listener?
2. Is OS-level `cursor://` scheme registration handled by the Cursor installer and therefore outside the scope of this spec?
3. If `cursor://` fires but no handler is registered, does the auth flow hang indefinitely or time out? What is the timeout?

---

## Appendix: Requirements with no gaps identified

Reviewed and found internally consistent:

- **Req 1** — Initiate Tenant Creation
- **Req 2** — PKCE flow (except EC-04 on cursor:// scheme)
- **Req 3** — Validate and Authorize Requests (network isolation now stated)
- **Req 8** — Handle Provisioning Errors
- **Req 9** — Query Environment Status (except G-10 on CREDENTIALS_READY routing)
- **Req 10** — Cache JWKS for Performance
- **Req 11** — Parse and Format Configuration
- **Req 12** — OAuth Metadata Discovery
- **Req 13** — Client Registration (Pre-registered + CIMD)
- **Req 14** — Token Refresh (reactive path; proactive path gap in G-01)
- **Req 15** — Platform Git Authentication and Secret Bootstrap
- **Req 18** — Platform Console Polling Strategy

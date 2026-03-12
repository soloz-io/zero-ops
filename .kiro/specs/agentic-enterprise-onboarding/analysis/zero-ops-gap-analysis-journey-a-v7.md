# Journey A — Gap Analysis v7

**Spec:** Journey A — Agentic Enterprise Onboarding (latest upload)  
**Date:** 2026-03-12  
**Verdict: NEARLY development-ready.** The two prior Critical gaps are resolved. One new Critical gap was introduced by the v7 changes. Remaining items are High/Medium with clear resolution paths — one focused product session should close everything before sprint planning.

---

## What is resolved since the last version

These gaps from v6 are **fully resolved and will not be re-raised**:

| Prior ID | Resolution |
|---|---|
| G-01 (forced token refresh — undefined mechanism) | AC6 now defines the exact JSON field `force_token_refresh: true` and the full HTTP 201 response body. ✅ |
| G-02 (single-tenant rule breaks idempotency) | AC1 now explicitly routes same-name retries to the idempotency checks (AC6–AC8) and returns HTTP 403 only for a genuinely different tenant name. ✅ |
| G-04 (Kratos/Keto partial failure undefined) | AC5 now defines `INCOMPLETE_IDENTITY_SETUP` state with idempotent retry semantics. ✅ |
| G-03 (S3 bucket owned by wrong account) | AC4 now specifies "Zero-Ops Platform's internal disaster recovery S3 bucket" — bucket ownership clarified. ✅ |
| G-10 (CREDENTIALS_READY routing against missing environment_id) | Req 16 AC1 now defines a fallback to `GET /api/v1/tenants/{tenant_id}/status` when no environment exists. ✅ |
| G-09 (no "Deleting" phase) | Req 19 AC16 now explicitly handles the teardown-in-progress state with HTTP 202 + distinct message. ✅ |
| L-14 (duplicate AC5 in Req 19) | Resolved by renumbering — AC15 and AC16/AC17 are now distinct. ✅ |

---

## Gap Summary

| ID | Severity | Req | Title |
|---|---|---|---|
| [G-01](#g-01) | **Critical** | Req 4 AC6 | `force_token_refresh` path uses the same token refresh mechanism, but the existing token has no `tenant_id` — the refresh will re-issue the same claims |
| [G-02](#g-02) | **High** | Req 4 AC5 | `INCOMPLETE_IDENTITY_SETUP` retry scope is ambiguous — does it also re-run Git setup? |
| [G-03](#g-03) | **High** | Req 7 AC4/AC5 | Tenant Age key cross-cluster injection mechanism is still unspecified |
| [G-04](#g-04) | **High** | Req 19 AC4 | Approval ticket RBAC, notification, and no-admin escalation remain undefined |
| [G-05](#g-05) | **High** | Req 6 AC5/AC8 | HTTP 409 vs idempotent HTTP 200 — exact boundary condition is ambiguous |
| [G-06](#g-06) | **Medium** | Req 17 AC5 | CREDENTIALS_READY resumption invokes `environment_create` but parameter source is undefined |
| [G-07](#g-07) | **Medium** | Req 4 AC3 | Platform Admin `tenant_create` on behalf of another user — whose `sub` gets the Keto tuple? |
| [G-08](#g-08) | **Medium** | Req 16 AC1 | Tenant-level status endpoint response schema is undefined |
| [G-09](#g-09) | **Medium** | Req 5 | `INCOMPLETE_IDENTITY_SETUP` state is not handled as a resumption case in Req 17 |
| [G-10](#g-10) | **Low** | Req 5 | Duplicate AC11; missing ACs 12–14 |
| [G-11](#g-11) | **Low** | Req 6 | Duplicate AC numbered "6" |
| [G-12](#g-12) | **Low** | Req 7 | Duplicate ACs numbered "4" and "5" |
| [EC-01](#ec-01) | **Confirm** | Req 19 AC3 | Previously-Ready-now-Degraded: does approval still apply? |
| [EC-02](#ec-02) | **Confirm** | Req 6, Req 9 | Multi-environment per tenant: listing and disambiguation |
| [EC-03](#ec-03) | **Confirm** | Req 4 AC13 | `INCOMPLETE_GIT_SETUP` persistent failure: no platform alert, undifferentiated error |
| [EC-04](#ec-04) | **Confirm** | Req 2, Req 13 | `cursor://` custom URI scheme: usage priority and OS fallback undefined |

---

## Critical

> Must be resolved before sprint planning.

---

### G-01

**Severity:** Critical  
**Req:** Req 4 AC6

#### `force_token_refresh` flow cannot populate `tenant_id` — the refresh reissues the same claims

AC6 requires: after tenant_create returns HTTP 201 with `force_token_refresh: true`, Cursor executes the Token Refresh flow (Req 14). Req 14 AC5 specifies: "THE Hydra SHALL issue a new access token with 24-hour TTL and **same custom claims** (tenant_id, email, role)."

This is a contradiction. The entire point of the forced refresh is to get a *new* `tenant_id` claim into the token — but Req 14 says the refresh returns the same claims. The `tenant_id` was just written into Kratos via AC3, but Hydra reads claims from its token session at the time the *original* access token was issued, not from Kratos on every refresh. The refresh will produce a new access token with the same (empty or absent) `tenant_id` as before.

Without resolving this, every `environment_create` call after a fresh `tenant_create` will carry a JWT with no `tenant_id`, causing AgentGateway to forward an empty `X-Tenant-ID` header and zero_ops_api to fail tenant isolation checks.

**Questions for the product team:**
1. Should the `force_token_refresh` directive trigger a full **re-authentication** (new authorization code flow via browser) rather than a token refresh, since refresh tokens carry the original session claims? If a full re-auth is required, say so explicitly — it has a major UX impact (browser opens again).
2. Alternatively: does Hydra support introspection-time claim augmentation from Kratos, so that a refresh token exchange reads the latest Kratos identity traits? If so, state this as a requirement and call out the dependency on the Ory session model.
3. Should Req 14 AC5 be updated to read: "THE Hydra SHALL issue a new access token reflecting the **latest** Kratos identity traits (tenant_id, email, role) at the time of the refresh"? This would be the cleanest fix if the Ory stack supports it.
4. If a full re-auth is required: what does Cursor display between the `tenant_create` HTTP 201 and the browser opening again? The current UX flow has no step for this.

---

## High

> Significant ambiguity likely to cause mid-sprint rework. Resolve before the design spec is written.

---

### G-02

**Severity:** High  
**Req:** Req 4 AC5

#### `INCOMPLETE_IDENTITY_SETUP` retry is scoped only to "missing identity steps" — scope boundary with `INCOMPLETE_GIT_SETUP` is undefined

AC5 defines a new `INCOMPLETE_IDENTITY_SETUP` state when Kratos/Keto steps fail after a successful DB insert. The retry instruction says: "Retrying tenant_create SHALL idempotently re-attempt the missing identity steps." However, at this point the tenant record exists but Git setup (Req 4 AC10–AC13) may not yet have run, because the spec orders operations as: DB insert → Kratos/Keto → (then implicitly) Git. If the Kratos/Keto step fails, does Git setup ever run?

There are now two failure modes (`INCOMPLETE_IDENTITY_SETUP` and `INCOMPLETE_GIT_SETUP`) but only one retry path — tenant_create retry. The retry logic for `INCOMPLETE_IDENTITY_SETUP` is not specified to also continue into Git setup after identity steps succeed. A tenant in `INCOMPLETE_IDENTITY_SETUP` who retries may fix Kratos/Keto but still have no Git repo, ending up implicitly in `INCOMPLETE_GIT_SETUP` without the transition being stated.

**Questions:**
1. When retrying from `INCOMPLETE_IDENTITY_SETUP`, does zero_ops_api re-run the full sequence (identity steps → Git steps) or only identity steps? State this explicitly.
2. What is the status returned to Cursor after a successful `INCOMPLETE_IDENTITY_SETUP` retry that completes all steps — `AWAITING_CREDENTIALS` (consistent with `INCOMPLETE_GIT_SETUP` resolution) or a different status?
3. Should the `INCOMPLETE_IDENTITY_SETUP` retry path include a note that, upon successfully completing identity steps, it proceeds to Git setup and may subsequently enter `INCOMPLETE_GIT_SETUP` if Git then fails?

---

### G-03

**Severity:** High  
**Req:** Req 7 AC4, AC5

#### Tenant Age key transfer from management cluster into tenant cluster is still unspecified

Req 4 AC4 stores the tenant Age private key as a Kubernetes Secret in the management cluster. Req 7 AC4 states Composition B SHALL inject this key into the tenant cluster. The mechanism for cross-cluster secret transfer during Crossplane execution is not defined in this or any previous version. AC4 and AC5 of Req 7 remain near-identical, compounding the ambiguity.

This is a security-sensitive infrastructure decision that must be resolved before the design spec can be written. Options include Crossplane `provider-kubernetes` Object, a Crossplane EnvironmentConfig, or ArgoCD secret sync — each with different security boundaries and operational properties.

**Questions:**
1. What is the exact mechanism for injecting the tenant Age private key from the management cluster into the tenant cluster during Composition B — `provider-kubernetes` Object resource, Crossplane EnvironmentConfig, ArgoCD secret push, or something else?
2. Is the tenant Age private key accessible to any workload in the management cluster namespace, or is it scoped to a specific Crossplane controller service account?
3. Are Req 7 AC4 and AC5 intentionally describing two distinct operations? If yes, what is the differentiation? If they are duplicates, consolidate them.
4. Should this requirement state which Crossplane provider is responsible for the bootstrap injection so the design spec has a clear dependency?

---

### G-04

**Severity:** High  
**Req:** Req 19 AC4

#### Approval ticket approver RBAC, notification, and no-active-admin escalation remain undefined

AC4 generates a Destructive Operation Approval Ticket "assigned to the Tenant's administrators" without defining: (a) whether any `tenant_admin` can approve or only the initiating admin, (b) how administrators are notified, (c) whether a `platform_admin` can override for contract termination or support scenarios, (d) what happens when the 7-day window expires and the tenant has no active admins. This gap has been present since v4 and has not been addressed.

**Questions:**
1. Can any `tenant_admin` for the tenant approve the deletion ticket, or only the admin who initiated the deletion request?
2. Can a `platform_admin` approve a tenant's deletion ticket? If yes, state it explicitly — it has data governance implications under the BYOC model.
3. What is the notification mechanism — console notification, email, or both? Is this in scope for this spec?
4. If the tenant has no active admin when the 7-day window expires, is the environment permanently protected from deletion? What is the escalation path?

---

### G-05

**Severity:** High  
**Req:** Req 6 AC5, AC8

#### HTTP 409 vs idempotent HTTP 200 — the exact boundary condition is ambiguous

AC5: return HTTP 409 if the same `environment_id` is called with "a different tier/cloud/region."  
AC8: return HTTP 200 (idempotent) if `environment_create` is called again for the same `environment_id`.

The boundary is imprecise. If the second call has the same `environment_id` and same `tier`, same `cloud`, but a different `region`, does it return 409 (any parameter differs) or 200 (same primary key)? The phrase "different tier/cloud/region" is ambiguous about whether it means any-one-differs or all-three-differ. An implementer must guess.

**Questions:**
1. Does HTTP 409 fire if **any single** parameter (tier, cloud, or region) differs from the original, or only if all three differ?
2. Recommended rule to confirm: exact match on all parameters → HTTP 200; any divergence → HTTP 409. Is this the intent?
3. Should the HTTP 409 response body include the original parameters (tier, cloud, region) so the caller knows what already exists?

---

## Medium

> Design-phase inputs. A decision is needed before architecture is finalised but these can be resolved during the design spec.

---

### G-06

**Severity:** Medium  
**Req:** Req 17 AC5

#### CREDENTIALS_READY resumption invokes `environment_create` but the source of tier/cloud/region/suffix is undefined

AC5 states: "IF phase is CREDENTIALS_READY, THE Cursor SHALL prompt the Tenant_Admin to confirm they are ready to provision, and upon confirmation, invoke `environment_create`." To invoke `environment_create`, Cursor needs `tier`, `cloud`, `region`, and `environment_suffix`. These were provided in the original session that closed. If the Cursor process has exited, those values are gone from client memory.

The spec does not state whether these parameters are persisted server-side and therefore available to a resumed Cursor session.

**Questions:**
1. Are environment parameters (tier, cloud, region, environment_suffix) persisted to the PostgreSQL tenant record at the time of the original invocation, so a resumed session can retrieve them?
2. If not persisted server-side: should AC5 require Cursor to re-prompt the user for all parameters on resumption rather than implying it can invoke `environment_create` autonomously?
3. Should an AC be added to Req 4 or Req 6 explicitly stating that environment intent parameters are stored in the tenant record?

---

### G-07

**Severity:** Medium  
**Req:** Req 4 AC1, AC3

#### Platform Admin creating a tenant on behalf of another user — whose `sub` gets the Keto tuple and Kratos trait update?

AC1 allows a Platform Admin to bypass the single-tenant-per-user check. AC3 calls the Kratos Admin API to update the user's identity traits with the new `tenant_id`, and creates the Keto tuple `tenant:{tenant_id}#admin@user:{sub}`. When a Platform Admin creates a tenant on behalf of another user, it is ambiguous: does `sub` refer to the Platform Admin's identity (wrong — the Platform Admin should not become the tenant owner), or a target user's identity passed as a parameter to the tool?

**Questions:**
1. When a Platform Admin creates a tenant, which user's `sub` is written into the Keto tuple and Kratos trait — the Platform Admin's, or a `target_user_id` parameter passed in the tool call?
2. If a `target_user_id` is required: should this be an explicit parameter in the `tenant_create` MCP tool schema?
3. What role value in `X-User-Role` identifies a Platform Admin for the AC1 exemption — is it the literal string `platform_admin`?

---

### G-08

**Severity:** Medium  
**Req:** Req 16 AC1

#### Tenant-level status endpoint has no defined response schema

Req 16 AC1 introduces a fallback endpoint: `GET /api/v1/tenants/{tenant_id}/status` for the CREDENTIALS_READY phase. Req 16 AC4 defines the response schema, but only for the environment-scoped endpoint. The tenant-level endpoint will return a phase of `CREDENTIALS_READY` — but what does it return for `environment_name`, `tier`, `crossplane_conditions`, `duration_seconds`, and `console_url`? These fields are undefined for a pre-environment state.

**Questions:**
1. What is the response schema for `GET /api/v1/tenants/{tenant_id}/status`? Which fields from the environment status schema are applicable, which are nullable/absent, and are there tenant-specific fields (e.g., `tenant_name`, `plan`)?
2. Should `CREDENTIALS_READY` be included in the Req 16 AC4 phase enum, or is it only returned by the tenant-level endpoint and intentionally excluded from the environment status schema?
3. Should `AWAITING_CREDENTIALS` and `INCOMPLETE_IDENTITY_SETUP` also be returnable from the tenant-level status endpoint? Req 17 AC4 expects to see `AWAITING_CREDENTIALS` from an `environment_status` call, but this state predates any environment.

---

### G-09

**Severity:** Medium  
**Req:** Req 5 / Req 17

#### `INCOMPLETE_IDENTITY_SETUP` is not handled as a resumption case in Req 17

Req 17 defines conversational resumability for: `AWAITING_CREDENTIALS` (AC4), `CREDENTIALS_READY` (AC5), `Provisioning/Degraded` (AC6), and `Ready` (AC6 duplicate). The new `INCOMPLETE_IDENTITY_SETUP` state (Req 4 AC5) is absent. A tenant in this state who opens a new Cursor session and asks for status has no defined Agent behaviour.

**Questions:**
1. Should a new AC be added to Req 17: "IF phase is `INCOMPLETE_IDENTITY_SETUP`, THE Cursor SHALL display: 'There was a problem completing your account setup. Please retry tenant creation to resolve this automatically.' and invoke `tenant_create` again"?
2. Is `INCOMPLETE_IDENTITY_SETUP` queryable via the tenant-level status endpoint (Req 16 AC1) or does it require a separate `tenant_create` call to detect?

---

## Low

> Editorial corrections. Apply immediately to prevent incorrect AC references in design specs and test plans.

---

### G-10

**Severity:** Low  
**Req:** Req 5

Req 5 has two ACs both numbered `11` (the S3 backup line and the "tenant SHALL remain in AWAITING_CREDENTIALS" line), then skips directly to AC15. ACs 12, 13, 14 are absent from the sequence. Renumber: second `11` → `12`, then renumber the subsequent ACs sequentially so there are no gaps.

---

### G-11

**Severity:** Low  
**Req:** Req 6

Req 6 has two ACs both numbered `6`. The second one ("IF the Git commit fails...") should be renumbered AC7, with all subsequent ACs shifted forward by one.

---

### G-12

**Severity:** Low  
**Req:** Req 7

Req 7 has two ACs numbered `4` and two numbered `5`. The second AC4 ("Composition_B SHALL typically complete within 15 minutes") and the second AC5 ("tenant cluster bootstrap SHALL install...") should be renumbered AC6 and AC7. This also relates to the duplication noted in G-03.

---

## Confirm

> Edge cases requiring an explicit product decision before the design spec. Do not leave these to implementer discretion.

---

### EC-01

**Req:** Req 19 AC3

**Previously-Ready-now-Degraded: does the approval requirement still apply?**

AC3 triggers immediate deletion if the environment has *never* achieved Ready. This is evaluated at deletion request time. An environment that was Ready yesterday but is Degraded today has a `Ready` history — so the approval gate applies, even though the environment is currently broken. Users will experience friction ("it's broken, why do I need approval to delete it?").

**Confirm:**
1. Is it intentional that a previously-Ready-now-Degraded environment always requires approval for deletion?
2. If yes, should the approval ticket message for a Degraded environment include context that it was previously operational (to help the approver understand the data-loss risk)?
3. If the tenant cancels a pending approval ticket and immediately re-requests deletion, does the 7-day clock restart?

---

### EC-02

**Req:** Req 6, Req 9

**Multi-environment per tenant: listing and status disambiguation**

Req 6 supports multiple environments per tenant via `environment_suffix`. If a tenant has two environments in different states and asks Cursor "What is the status of my environment?", the request is ambiguous. No `list_environments` MCP tool or API endpoint is defined in the spec.

**Confirm:**
1. Is a `list_environments` MCP tool in scope for this spec, or is the Platform Console the only way to enumerate environments?
2. When the user asks for status without specifying an `environment_id`, should Cursor prompt for clarification or default to the most recently created environment?
3. Is there a maximum number of simultaneous environments per tenant? If so, what is the limit and where is it enforced (entitlement check, DB constraint, or Crossplane quota)?

---

### EC-03

**Req:** Req 4 AC13

**`INCOMPLETE_GIT_SETUP` persistent failure: no platform alert, no user-distinguishable error**

AC13 returns HTTP 500 with a generic user-facing message on Git failure. If the Git provider is down for hours, every retry returns the same message. Users cannot distinguish a transient outage from a persistent platform failure. There is also no mechanism specified to alert the platform team that a tenant is stuck in this state.

**Confirm:**
1. Should a platform-level alert fire when a tenant remains in `INCOMPLETE_GIT_SETUP` for more than N minutes? What is N?
2. Should the HTTP 500 response include a distinct `error_code` field (e.g., `git_service_unavailable`) so Cursor can show a more actionable message than the generic one?

---

### EC-04

**Req:** Req 2, Req 13 AC1

**`cursor://` custom URI scheme: usage priority and OS fallback undefined**

Req 13 AC1 pre-registers `cursor://anysphere.cursor-mcp/oauth/callback`. Req 2 specifies only the loopback HTTP listener (ports 54321, 18999, 3000). No requirement states when Cursor prefers the custom scheme over the loopback, or what happens if `cursor://` is not registered as an OS URI handler.

**Confirm:**
1. Is the `cursor://` scheme a primary or fallback redirect? Under what conditions does Cursor use it instead of the loopback HTTP listener?
2. Is OS-level `cursor://` URI scheme registration handled by the Cursor installer and therefore out of scope for this spec?
3. If `cursor://` fires but no OS handler is registered, does the auth flow hang indefinitely or time out? What is the timeout and fallback behaviour?

---

## Appendix: Requirements with no gaps identified

The following were reviewed and are internally consistent:

- **Req 1** — Initiate Tenant Creation
- **Req 2** — Complete Authorization Code Flow with PKCE (except EC-04)
- **Req 3** — Validate and Authorize Requests
- **Req 8** — Handle Provisioning Errors
- **Req 9** — Query Environment Status
- **Req 10** — Cache JWKS for Performance
- **Req 11** — Parse and Format Configuration
- **Req 12** — OAuth Metadata Discovery
- **Req 13** — Client Registration (Pre-registered + CIMD) (except EC-04)
- **Req 14** — Token Refresh (reactive path; proactive path gap in G-01)
- **Req 15** — Platform Git Authentication and Secret Bootstrap
- **Req 18** — Platform Console Polling Strategy

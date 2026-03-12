# Journey A — Gap Analysis v8

**Spec:** Journey A — Agentic Enterprise Onboarding (latest upload)  
**Date:** 2026-03-12  
**Verdict: CLOSE TO development-ready.** The prior Critical gap (token refresh claims) is fully resolved. No new Criticals introduced. Remaining gaps are all High or below, well-scoped, and resolvable in a single product refinement session before sprint planning.

---

## What is resolved since v7

| Prior ID | What changed |
|---|---|
| **G-01 Critical** — `force_token_refresh` would re-issue same claims | Req 14 AC5 now explicitly states Hydra re-hydrates custom claims from latest Kratos identity traits during refresh. ✅ |
| **G-02** — `INCOMPLETE_IDENTITY_SETUP` retry scope ambiguous | Req 4 AC5 now states: upon successful identity retry, zero_ops_api automatically proceeds to Git provisioning steps. ✅ |
| **G-03** — Tenant Age key cross-cluster injection unspecified | Req 7 AC4 now specifies `provider-kubernetes` Object resource copies the key into the tenant cluster's ArgoCD namespace. ✅ |
| **G-05** — HTTP 409 vs 200 boundary ambiguous | Req 6 AC5 now states: IF ANY single parameter differs, return HTTP 409. ✅ |
| **G-07** — Platform Admin `sub` vs `target_user_id` undefined | Req 4 AC3 now includes the `target_user_id` parameter clause. ✅ |
| **G-08** — Tenant-level status endpoint schema undefined | Req 9 AC6 now marks `environment_name` and `tier` as nullable; `CREDENTIALS_READY` is in the phase enum. ✅ |
| **G-09** — `INCOMPLETE_IDENTITY_SETUP` not handled in Req 17 | Req 17 AC4 now handles both `INCOMPLETE_IDENTITY_SETUP` and `INCOMPLETE_GIT_SETUP` with a defined Cursor message. ✅ |
| **EC-01** — Previously-Ready-now-Degraded approval intent unclear | Req 19 AC4 now includes explicit message text stating the approval applies even if currently Degraded. ✅ |
| **EC-02** — No way to list multiple environments | Req 9 AC10 now defines an `environments_list` MCP tool with disambiguation prompt. ✅ |
| **G-04 High** — Approval ticket RBAC undefined | Req 19 AC7 now states any `tenant_admin` OR a `platform_admin` may approve; notifications via Platform Console. ✅ |
| **EC-03** — `INCOMPLETE_GIT_SETUP` error undifferentiated | Req 4 AC13 now returns `{"error_code": "git_service_unavailable", ...}`. ✅ |

---

## Gap Summary

| ID | Severity | Req | Title |
|---|---|---|---|
| [G-01](#g-01) | **High** | Req 7 AC4/AC5 | Duplicate ACs and a cross-cluster RBAC gap in the Age key injection |
| [G-02](#g-02) | **High** | Req 4 AC6 | `force_token_refresh` on the idempotency path (HTTP 200) is undefined |
| [G-03](#g-03) | **High** | Req 16 / Req 9 | `AWAITING_CREDENTIALS` and `INCOMPLETE_*` states are not reachable via either status endpoint |
| [G-04](#g-04) | **Medium** | Req 4 AC1 | HTTP 403 for second-tenant attempt has no defined error body |
| [G-05](#g-05) | **Medium** | Req 6 AC8 | Idempotent HTTP 200 response body for repeated `environment_create` is undefined |
| [G-06](#g-06) | **Medium** | Req 19 AC9 | 7-day approval ticket expiry — no defined Cursor or console behaviour on expiry, and cancel semantics are incomplete |
| [G-07](#g-07) | **Medium** | Req 9 AC10 | `environments_list` tool is defined in one line with no schema, auth, or error behaviour |
| [G-08](#g-08) | **Low** | Req 5 | Duplicate AC11; ACs 14 and 12/13 missing from sequence |
| [G-09](#g-09) | **Low** | Req 6 | Duplicate AC numbered "6" |
| [G-10](#g-10) | **Low** | Req 7 | Duplicate ACs numbered "4" and "5" |
| [G-11](#g-11) | **Low** | Req 17 | Duplicate AC numbered "6" |
| [EC-01](#ec-01) | **Confirm** | Req 19 AC9 | Approval ticket expiry + cancel: 7-day clock restart behaviour undefined |
| [EC-02](#ec-02) | **Confirm** | Req 4 AC13 | `INCOMPLETE_GIT_SETUP` persistent failure: platform alerting still unspecified |
| [EC-03](#ec-03) | **Confirm** | Req 2, Req 13 | `cursor://` custom URI scheme: priority and OS fallback undefined |

---

## High

> Significant ambiguity that will cause mid-sprint rework. Resolve before the design spec is written.

---

### G-01

**Req:** Req 7 AC4, AC5

#### Duplicate ACs and a cross-cluster RBAC gap in the Age key injection

**Finding:** Req 7 now has two separate numbered groups — AC4 + AC5 first appear as the cross-cluster key injection description, then AC4 + AC5 appear again as the 15-minute completion estimate and the bootstrap step summary. This is the same duplicate-numbering issue from prior versions, not yet fixed.

More substantively: AC4 specifies that `provider-kubernetes` copies the tenant Age private key Secret from the management cluster into the tenant cluster's ArgoCD namespace. This is a cross-cluster secret read operation from within Crossplane's execution context. The spec does not state what RBAC or service account credentials `provider-kubernetes` uses to read from the management cluster's secrets namespace. If `provider-kubernetes` runs with cluster-admin access, this is a significant security surface that reviewers will flag immediately.

**Questions for the product team:**
1. What service account or kubeconfig does the `provider-kubernetes` Object use to read the tenant Age private key from the management cluster? Is it scoped to a specific namespace and secret name, or does it have broader access?
2. Should a requirement be added stating that the `provider-kubernetes` provider credential is a least-privilege service account scoped to read exactly one Secret (the tenant Age key) per tenant namespace?
3. Are AC4 and AC5 (first instance) the same operations as the second AC4 and AC5? If so, consolidate into AC4 and AC5 with clear numbering. If they describe different steps, differentiate them explicitly.

---

### G-02

**Req:** Req 4 AC6

#### `force_token_refresh` on the idempotency path (HTTP 200 returns) is undefined

**Finding:** AC6 states zero_ops_api returns HTTP 201 with `{"force_token_refresh": true, "tenant_id": "..."}`. This correctly handles the first creation. However, the idempotency returns — AC7 (AWAITING_CREDENTIALS), AC8 (CREDENTIALS_READY), AC9 (READY) — all return HTTP 200. None of them define a response body. A Cursor session that resumes after a closed context calls `tenant_create`, gets HTTP 200, but has no defined JSON body to parse for `tenant_id` or status.

Additionally: on the HTTP 200 idempotency path, does Cursor also perform a `force_token_refresh`? For a user who already completed onboarding and is resuming, their JWT already contains `tenant_id` — a forced refresh would be unnecessary. But for a user whose token was issued before `tenant_create` completed (e.g., a network cut mid-flow), they need the refresh. The spec does not distinguish these cases.

**Questions:**
1. What is the complete JSON response body for AC7, AC8, and AC9 (HTTP 200 idempotency returns)? At minimum: `tenant_id`, `status`, and optionally `force_token_refresh`.
2. Should `force_token_refresh: true` also be returned on HTTP 200 idempotency paths, or only on HTTP 201? If only 201, state this explicitly so implementers do not add it to 200 responses.
3. Should the response schema for all `tenant_create` responses (201 and 200) be defined in a single AC, analogous to how Req 16 defines its schema?

---

### G-03

**Req:** Req 16 AC1, Req 17 AC4

#### `AWAITING_CREDENTIALS` and `INCOMPLETE_*` states are not queryable via either status endpoint

**Finding:** Req 16 AC1 defines two endpoints: `GET /api/v1/environments/{environment_id}/status` and `GET /api/v1/tenants/{tenant_id}/status`. The tenant-level endpoint exists to surface `CREDENTIALS_READY` (AC5 of Req 16). However, Req 17 AC4 expects Cursor to receive `AWAITING_CREDENTIALS` from `environment_status` and prompt for credential submission. Req 17 AC4 also expects Cursor to receive `INCOMPLETE_IDENTITY_SETUP` and `INCOMPLETE_GIT_SETUP`.

These are all **tenant-level** states — they precede any environment being created. `AWAITING_CREDENTIALS` and the two `INCOMPLETE_*` states cannot be returned by the environment-scoped endpoint (there is no environment_id yet), and the tenant-level endpoint's purpose is stated only as "retrieve the CREDENTIALS_READY phase." The spec is silent on whether `AWAITING_CREDENTIALS`, `INCOMPLETE_IDENTITY_SETUP`, and `INCOMPLETE_GIT_SETUP` are also returnable from the tenant-level endpoint.

This means Req 17 AC4 (detecting `AWAITING_CREDENTIALS` and `INCOMPLETE_*` from `environment_status`) will fail in practice — the Cursor call will use the wrong endpoint or receive a 404.

**Questions:**
1. Should the tenant-level endpoint (`GET /api/v1/tenants/{tenant_id}/status`) return ALL pre-environment states — `AWAITING_CREDENTIALS`, `INCOMPLETE_IDENTITY_SETUP`, `INCOMPLETE_GIT_SETUP`, and `CREDENTIALS_READY` — not just `CREDENTIALS_READY`?
2. What is the complete response schema for the tenant-level status endpoint? The Req 16 AC4 schema (with `environment_name`, `tier`, `crossplane_conditions`) is clearly environment-specific. Does the tenant endpoint share this schema with nullable fields, or does it have its own schema?
3. How does Cursor know which endpoint to call in Req 17 AC4? Does it always call the tenant-level endpoint first and fall back to the environment endpoint, or does it need to track which endpoint is appropriate based on prior session state?

---

## Medium

> Design-phase inputs. These require a decision before architecture is finalised but can be resolved during the design spec.

---

### G-04

**Req:** Req 4 AC1

#### HTTP 403 for a second-tenant attempt has no defined error body

**Finding:** AC1 states: if the user already has a `tenant_id` AND the requested tenant name differs from their existing record, return HTTP 403 Forbidden (Single tenant per user limit). The HTTP 403 error body is not defined. Given that every other error in this spec includes a JSON body with `error` and `message` fields (Req 6 AC2, Req 19 AC4), this inconsistency will cause a divergent implementation.

**Questions:**
1. What is the JSON error body for this HTTP 403? Suggested: `{"error": "single_tenant_limit", "message": "Each user account may only be associated with one tenant. Your existing tenant is: {existing_tenant_name}.", "existing_tenant_id": "{tenant_id}"}`.
2. Should the response include the existing `tenant_id` or `tenant_name` to help the user understand what record already exists?
3. Does this HTTP 403 also apply to a Platform Admin creating a second tenant using `target_user_id` for a user who already has one? Or is the exemption unlimited for Platform Admins?

---

### G-05

**Req:** Req 6 AC8

#### Idempotent HTTP 200 response body for repeated `environment_create` is undefined

**Finding:** AC8 states: if `environment_create` is called again for the same environment_id, return HTTP 200 with "the current environment state." No schema is given. The HTTP 202 success response body is defined in AC7 (tenant_id, environment_id, console_url, estimated_duration_minutes). The HTTP 200 idempotent response should carry at minimum the current phase, but this is not specified. Implementers will diverge.

**Questions:**
1. What fields does the HTTP 200 idempotency response contain? Should it reuse the same fields as the Req 9/Req 16 environment status response, or a subset?
2. Should the HTTP 200 idempotency response include `phase` so Cursor can handle it correctly (e.g., display the provisioning status rather than the "started in background" message it shows after HTTP 202)?
3. Should Cursor's display behaviour differ based on receiving HTTP 202 (first creation) vs. HTTP 200 (idempotent re-call)?

---

### G-06

**Req:** Req 19 AC9

#### Expired approval ticket — no Cursor or console behaviour defined, and cancellation semantics are incomplete

**Finding:** AC9 states: if the Approval Ticket is not actioned within 7 days, zero_ops_api marks it as Expired, leaving the environment untouched. What happens next is not defined:
- Does the Platform Console show the ticket as Expired? Does it allow a new deletion request?
- If the Tenant Admin tries `environment_delete` again after a ticket expires, does AC3 re-evaluate the environment state (Ready history → new 403, new ticket), or does something special happen because there is an expired ticket?
- AC8 says the Tenant Admin MAY approve OR cancel. Cancellation semantics are not defined: does cancellation permanently prevent deletion, or simply close the ticket so a new deletion request can be made?

**Questions:**
1. After a ticket expires, can the Tenant Admin immediately re-invoke `environment_delete` to generate a new ticket? Or is there a cooldown period?
2. What does the Platform Console display for an expired ticket — does it show the expired state with a "Request new deletion" option?
3. When a ticket is cancelled (AC8), does `environment_delete` re-evaluate from scratch (potentially issuing a new ticket), or does cancellation prevent further deletion attempts for some period?
4. Should zero_ops_api send a notification to `tenant_admin` users when a ticket transitions to Expired, so they are aware the environment remains live?

---

### G-07

**Req:** Req 9 AC10

#### `environments_list` tool is defined in a single sentence with no schema, auth, or error behaviour

**Finding:** AC10 introduces a new MCP tool, `environments_list`, in one sentence: "THE zero_ops_api SHALL expose an environments_list MCP tool. WHEN the Tenant_Admin asks generally about their environments without specifying an ID, THE Cursor SHALL invoke environments_list to fetch all environments and prompt the user to disambiguate." This is insufficient to build against. For a new MCP tool, the spec needs: the endpoint mapping, the authentication/authorisation path, the response schema, and error states.

**Questions:**
1. What endpoint does `environments_list` map to — `GET /api/v1/tenants/{tenant_id}/environments`?
2. What does the response body look like? At minimum: an array of objects with `environment_id`, `environment_suffix`, `phase`, `tier`, `console_url`.
3. Does `environments_list` go through AgentGateway with the same JWT + Keto authorisation pattern as other tools? This should be stated explicitly.
4. What does `environments_list` return if the tenant has zero environments — HTTP 200 with an empty array, or HTTP 404?
5. Is there a maximum number of environments per tenant? If yes, what is the limit and where is it enforced?

---

## Low

> Editorial corrections. Apply to the spec before it is baselined to prevent incorrect AC references in test plans and sprint tickets.

---

### G-08

**Req:** Req 5

Req 5 has two ACs both numbered `11` (the S3 backup step and the "tenant SHALL remain in AWAITING_CREDENTIALS" step). The sequence then skips from 13 to 15, omitting 14. **Action:** renumber the second `11` → `12`; renumber `13` → `13`; add a placeholder or renumber `15` → `14`, `16` → `15`, `17` → `16` to restore sequential numbering.

---

### G-09

**Req:** Req 6

Req 6 has two ACs both numbered `6`: "THE zero_ops_api SHALL commit the AINativeSaaS_CR..." and "IF the Git commit fails...". **Action:** renumber the second `6` to `7`; shift `7` → `8`, etc. The existing idempotency AC8 becomes AC9.

---

### G-10

**Req:** Req 7

Req 7 has two ACs numbered `4` and two numbered `5`. The second pair (beginning "THE Composition_B SHALL typically complete...") should be renumbered `6` and `7`. **Action:** fix numbering so all ACs in Req 7 are sequential 1–7.

---

### G-11

**Req:** Req 17

Req 17 has two ACs both numbered `6`: "IF phase is Provisioning or Degraded..." and "IF phase is Ready...". The second `6` should be renumbered `7`; the existing `7` ("THE Platform Console SHALL always reflect...") becomes `8`. Note this also introduces a duplicate `7` in the current numbering.

---

## Confirm

> Edge cases requiring an explicit product decision before design spec. Do not leave to implementer discretion.

---

### EC-01

**Req:** Req 19

**Approval ticket expiry and cancel — 7-day clock restart semantics**

AC9 and AC8 define expiry and cancellation but do not address what happens after either event. If a ticket expires or is cancelled and the admin immediately re-requests deletion, does the 7-day clock restart from scratch? Is there any cooldown or rate limit on issuing deletion tickets to prevent an admin from cycling through tickets repeatedly to delay deletion?

**Confirm:**
1. After expiry or cancellation, can `environment_delete` be immediately re-invoked to generate a fresh 7-day ticket?
2. Is there any limit on how many times a deletion ticket can be created and expired/cancelled for the same environment?

---

### EC-02

**Req:** Req 4 AC13

**`INCOMPLETE_GIT_SETUP` persistent failure: no platform alerting**

The `error_code: git_service_unavailable` response is now defined (v8 resolved the user-facing gap), but there is still no requirement for the platform to be alerted when a tenant is stuck in `INCOMPLETE_GIT_SETUP` for an extended period. If Git is down for hours, the platform engineering team has no visibility from this spec.

**Confirm:**
1. Should a platform-level alert fire when a tenant record remains in `INCOMPLETE_GIT_SETUP` for longer than N minutes? What is N?
2. Is this alerting in scope for this requirements document, or deferred to an operational runbook?

---

### EC-03

**Req:** Req 2, Req 13 AC1

**`cursor://` custom URI scheme: usage priority and OS fallback**

Req 13 AC1 pre-registers `cursor://anysphere.cursor-mcp/oauth/callback`. Req 2 specifies only the loopback HTTP listener (ports 54321, 18999, 3000). No requirement states when Cursor prefers the custom scheme over the loopback, or what happens if `cursor://` is not registered as an OS-level URI handler.

**Confirm:**
1. Is the `cursor://` scheme a primary or fallback redirect? Under what conditions does it take precedence over the loopback listener?
2. Is OS-level `cursor://` URI scheme registration assumed to be handled by the Cursor installer and therefore out of scope for this spec?
3. If `cursor://` fires but no handler is registered, does the auth flow hang or timeout? What is the timeout and user-visible error?

---

## Appendix: Requirements with no gaps identified

The following were reviewed and are internally consistent in this version:

- **Req 1** — Initiate Tenant Creation
- **Req 2** — PKCE flow (except EC-03)
- **Req 3** — Validate and Authorize Requests
- **Req 8** — Handle Provisioning Errors
- **Req 10** — Cache JWKS for Performance
- **Req 11** — Parse and Format Configuration
- **Req 12** — OAuth Metadata Discovery
- **Req 13** — Client Registration (except EC-03)
- **Req 14** — Token Refresh (claim re-hydration now specified)
- **Req 15** — Platform Git Authentication and Secret Bootstrap
- **Req 18** — Platform Console Polling Strategy

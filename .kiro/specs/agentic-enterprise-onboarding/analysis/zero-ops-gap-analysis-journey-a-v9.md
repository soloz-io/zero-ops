# Journey A — Gap Analysis v9

**Spec version:** Latest upload (v9 iteration)
**Date:** 2026-03-12
**Analyst verdict: NEARLY development-ready.** No Critical gaps remain. The spec has resolved every prior
Critical and High gap except one carry-forward (Req 7 duplicate AC numbering). All remaining items are
Medium or below plus four editorial fixes and three edge-case confirmations. One focused product session
should close everything before sprint planning.

---

## Resolved since v8

| Prior ID | What changed |
|---|---|
| **G-02 High** — HTTP 200 idempotency paths had no response body | Req 4 AC7, AC8, AC9 now define explicit JSON bodies with `force_token_refresh: false`, `tenant_id`, and `status`. ✅ |
| **G-03 High** — Pre-environment states unreachable via status endpoints | Req 16 AC1 now explicitly lists all four pre-environment phases handled by the tenant-level endpoint. AC4 schema now covers all phases with nullable fields. ✅ |
| **G-04 Medium** — HTTP 403 for second-tenant attempt had no error body | Req 4 AC1 now defines `{"error": "single_tenant_limit", "message": "...", "existing_tenant_id": "..."}`. ✅ |
| **G-05 Medium** — Idempotent HTTP 200 on `environment_create` had no schema | Req 6 AC9 now explicitly states the HTTP 200 idempotent response uses the same JSON schema as HTTP 202. ✅ |
| **G-06 Medium** — Approval ticket expiry/cancel follow-on state undefined | Req 19 AC9 and AC10 now define Platform Console display text and allow immediate re-invocation of `environment_delete`. ✅ |
| **G-07 Medium** — `environments_list` defined in one line with no schema | Req 9 AC10–AC12 now define the endpoint mapping, auth path, schema reference, and empty-array behaviour. ✅ |
| **G-10 Low** — Req 7 duplicate AC6 | First `6` is now the RBAC/provider-kubernetes step (AC4–AC5), and there is a second `6` (provisioning duration) — partially fixed but see G-01 below. |
| **EC-02** — `INCOMPLETE_GIT_SETUP` alerting unspecified | Req 4 AC13 now states `emit a critical OpenSearch event for platform alerting`. ✅ |
| **Req 5 numbering** | Sequential AC1–17 with no skipped or duplicate numbers. ✅ |
| **Req 6 duplicate AC6** | Removed; ACs now run sequentially 1–12. ✅ |

---

## Gap Summary

| ID | Severity | Req | Title |
|---|---|---|---|
| [G-01](#g-01) | **High** | Req 7 | Duplicate AC6 and an unresolved Req 9 / Req 16 schema conflict |
| [G-02](#g-02) | **Medium** | Req 4 AC6 | `force_token_refresh` on the `INCOMPLETE_IDENTITY_SETUP` retry path is undefined |
| [G-03](#g-03) | **Medium** | Req 16 AC5 | `INCOMPLETE_IDENTITY_SETUP` and `INCOMPLETE_GIT_SETUP` phases have no `summary_message` definition |
| [G-04](#g-04) | **Medium** | Req 17 AC2 | `environment_status` tool is always invoked on resumption — but the correct endpoint (tenant vs environment) depends on context Cursor does not have |
| [G-05](#g-05) | **Medium** | Req 19 AC11 | Duplicate AC11; deletion Git-commit failure has no defined error path |
| [G-06](#g-06) | **Low** | Req 4 | HTTP 200 idempotency returns include `status` field; Req 17 AC4 routes on `phase` — field name inconsistency |
| [G-07](#g-07) | **Low** | Req 7 | Duplicate AC numbered "6" |
| [G-08](#g-08) | **Low** | Req 9 | Phase enum in Req 9 AC6 is incomplete relative to Req 16 AC4 |
| [EC-01](#ec-01) | **Confirm** | Req 19 | Cancellation of an approval ticket: re-invocation behaviour vs. expiry path |
| [EC-02](#ec-02) | **Confirm** | Req 4 AC3 | Platform Admin single-tenant-limit: does AC1 exemption apply when `target_user_id` already has a tenant? |
| [EC-03](#ec-03) | **Confirm** | Req 2, Req 13 | `cursor://` custom URI scheme priority and OS fallback undefined |

---

## High

> Must be resolved before the design spec is written.

---

### G-01

**Req:** Req 7 (duplicate AC6); cross-reference Req 9 AC6 vs Req 16 AC4

#### Duplicate AC6 in Req 7 and a schema inconsistency between Req 9 and Req 16

**Finding — editorial (Req 7):**
Req 7 has two ACs numbered `6`. The first (line 277): "THE Composition_B SHALL typically complete within 15 minutes." The second (line 277): "WHEN provisioning completes successfully, THE Crossplane SHALL update the AINativeSaaS_CR status to Ready: True." One of these must be renumbered `7`, and `7` (terminal failure) becomes `8`.

**Finding — schema conflict (Req 9 vs Req 16):**
Req 9 AC6 defines the `environment_status` MCP tool response inline. Req 16 AC4 defines the canonical schema. They are meant to be the same document, but they diverge:

| Field | Req 9 AC6 | Req 16 AC4 |
|---|---|---|
| `crossplane_conditions` item fields | `type, status, reason, message` | `type, status, reason, message, lastTransitionTime` |
| `duration_seconds` nullability | not stated as nullable | `integer \| null` |
| `phase` enum | `Pending, Provisioning, Ready, Degraded, CREDENTIALS_READY` | all 8 phases including `INCOMPLETE_*` and `AWAITING_CREDENTIALS` |

Req 9 is the primary MCP tool definition used by Cursor. If Cursor is coded against Req 9's schema and the server returns Req 16's schema, deserialization will fail silently or misroute on the `phase` value.

**Questions for the product team:**
1. Should Req 9 AC6 be replaced entirely with "The response schema is defined in Requirement 16 AC4" to eliminate the duplicate and ensure a single source of truth?
2. Is `lastTransitionTime` a required field on `crossplane_conditions` items, or optional? State this explicitly.
3. Fix the duplicate AC6 in Req 7 — confirm the correct sequential numbering.

---

## Medium

> Resolve before architecture is finalised; these will cause incorrect assumptions in the design spec.

---

### G-02

**Req:** Req 4 AC6

#### `force_token_refresh` behaviour on the `INCOMPLETE_IDENTITY_SETUP` retry path is undefined

**Finding:** AC6 defines `force_token_refresh: true` in the HTTP 201 response body. The HTTP 200 idempotency paths (AC7–AC9) now correctly define `force_token_refresh: false`. However, there is a fourth path not covered: when a user retries `tenant_create` from `INCOMPLETE_IDENTITY_SETUP` state (AC5), the retry may succeed and produce a *new* HTTP 201 with a valid `tenant_id` now properly registered in Kratos and Keto. At this point, the user's current JWT was issued *before* the Kratos trait update completed — the token still carries no `tenant_id`. This is the same claim-population problem that `force_token_refresh: true` was designed to solve.

AC5 describes the retry completing identity steps and proceeding to Git setup. It does not say what HTTP status code is returned on this successful retry (presumably 201, since the tenant is now fully created), nor whether `force_token_refresh: true` is set.

**Questions:**
1. When `tenant_create` is retried from `INCOMPLETE_IDENTITY_SETUP` and succeeds in completing all steps, what HTTP status code is returned — 201 (new creation) or 200 (resumed)?
2. Should `force_token_refresh: true` be returned on this successful retry, since the JWT still lacks the newly registered `tenant_id`?
3. Is there a general rule that can be stated: "force_token_refresh: true is returned whenever the tenant_id was written to Kratos during this request" — covering both the first creation (AC6) and any successful INCOMPLETE_IDENTITY_SETUP retry?

---

### G-03

**Req:** Req 16 AC5 and AC6

#### `INCOMPLETE_IDENTITY_SETUP` and `INCOMPLETE_GIT_SETUP` phases have no `summary_message` definition

**Finding:** Req 16 AC5 defines phase-derivation rules. AC6 defines `summary_message` for five phases: Pending, Provisioning, Ready, Degraded, and (implicitly) CREDENTIALS_READY (not listed in AC6 but present in the schema). The two `INCOMPLETE_*` phases and `AWAITING_CREDENTIALS` are absent from the summary_message rules entirely.

The Platform Console and the Cursor resumption flow (Req 17 AC4, AC5) both display `summary_message` to users. If the server returns one of these phases with no defined message, the display is either blank or implementation-dependent.

**Questions:**
1. What is the `summary_message` for `INCOMPLETE_IDENTITY_SETUP`? Suggested: "Platform encountered an error completing account setup. Retry tenant creation to resolve automatically."
2. What is the `summary_message` for `INCOMPLETE_GIT_SETUP`? Suggested: "Platform repository service was unavailable during setup. Retry tenant creation to resolve automatically."
3. What is the `summary_message` for `AWAITING_CREDENTIALS`? Suggested: "Account setup complete. Please submit your cloud provider credentials in the Platform Console."
4. Should AC6 be updated to include all 8 phases from the schema enum, leaving no phase without a defined message?

---

### G-04

**Req:** Req 17 AC2

#### Cursor always invokes `environment_status` on resumption, but the correct endpoint depends on context it does not have

**Finding:** Req 17 AC2 states: "THE Cursor SHALL invoke environment_status MCP tool." The `environment_status` tool maps to `GET /api/v1/environments/{environment_id}/status` and requires an `environment_id`. If the user's session was interrupted before `environment_create` was called, there is no `environment_id` — Cursor must use the tenant-level endpoint instead (`GET /api/v1/tenants/{tenant_id}/status`).

The spec requires Cursor to always call `environment_status` on resumption, but `environment_status` takes an `environment_id` parameter. A resuming Cursor session does not know whether an environment has been created yet. If it has no `environment_id` in its context, it cannot call `environment_status` — it must call the tenant-level endpoint or `environments_list` first.

The spec does not define this routing decision. Implementers will guess, and guesses will diverge between Cursor and Goose.

**Questions:**
1. Should Req 17 AC2 be split into two steps: (a) Cursor calls `environments_list` first to discover whether any environment exists for this tenant; (b) if environments exist, call `environment_status` with the returned `environment_id`; if none exist, call the tenant-level status endpoint?
2. Alternatively, should the `environment_status` MCP tool be defined to accept *either* an `environment_id` OR a `tenant_id`, and route internally — making it a unified "where am I" tool? If so, state this explicitly.
3. How does Cursor know its own `tenant_id` at the start of a new session? It reads the JWT claim — but if the user's JWT was issued before `tenant_create` completed, `tenant_id` may be absent. Is Cursor expected to call `tenant_create` first on every session start to retrieve its `tenant_id`?

---

### G-05

**Req:** Req 19

#### Duplicate AC11 and no error path for deletion Git-commit failure

**Finding:** Req 19 has two ACs both numbered `11`: "FOR approved or immediate deletions, THE zero_ops_api SHALL commit the removal..." and "WHEN the Git commit succeeds, THE zero_ops_api SHALL return HTTP 202 Accepted." The second `11` should be renumbered `12` (shifting all following ACs forward by one, making the current `12`–`18` become `13`–`19`).

More substantively: the commit-success path is defined (HTTP 202), but the commit-failure path is not. For `environment_create`, a Git commit failure returns HTTP 500 (Req 6 AC7). For credential submission, a Git commit failure returns HTTP 500 (Req 5 AC13). Deletion has no equivalent — AC11 covers success only.

**Questions:**
1. Fix the duplicate AC11 numbering.
2. IF the Git commit to remove the AINativeSaaS_CR manifest fails, what does `environment_delete` return? HTTP 500 with the same `git_service_unavailable` pattern? Is the deletion idempotent — can the user retry `environment_delete`?
3. Is a failed deletion Git commit treated as `INCOMPLETE_GIT_SETUP` or is there a new intermediate state? Or does the approval (if it was required) need to be re-submitted?

---

## Low

> Editorial. Apply before the spec is baselined to prevent misreferencing in test plans and sprint tickets.

---

### G-06

**Req:** Req 4 (AC7–AC9) vs Req 17 (AC4–AC8)

#### Response uses `status` field; Req 17 routes on `phase` — field name inconsistency

Req 4 AC7–AC9 define HTTP 200 bodies with a `status` field (`"status": "AWAITING_CREDENTIALS"` etc). Req 17 AC4–AC8 route on `phase` (`IF phase is AWAITING_CREDENTIALS`). Req 16 AC4 uses `phase` in the canonical schema.

The `tenant_create` response uses `status`; the `environment_status` response uses `phase`. If Cursor is coded to read `phase` from all status responses, it will fail to route correctly when processing the `tenant_create` HTTP 200 idempotency response — `phase` will be absent.

**Action:** Either rename `status` → `phase` in Req 4 AC7–AC9 to align with the canonical schema, or add a note that `tenant_create` returns `status` (not `phase`) and Req 17 AC4's "IF phase is…" language applies to the `environment_status` tool response only, not the `tenant_create` response.

---

### G-07

**Req:** Req 7

Req 7 has two ACs numbered `6` (lines 276–277). Renumber the second to `7`; rename `7` (terminal failure) to `8`.

---

### G-08

**Req:** Req 9 AC6

The `phase` enum in Req 9 AC6 lists only five values: `Pending, Provisioning, Ready, Degraded, CREDENTIALS_READY`. Req 16 AC4 lists eight: the prior five plus `INCOMPLETE_IDENTITY_SETUP`, `INCOMPLETE_GIT_SETUP`, and `AWAITING_CREDENTIALS`. Req 9's inline phase enum is the one Cursor is coded against. If this divergence is not resolved (see G-01 above — recommend removing the inline schema from Req 9 entirely), at minimum update Req 9 AC6 to list all eight phases to match Req 16.

---

## Confirm

> Edge cases that need an explicit product decision before the design spec. Do not leave to implementer discretion.

---

### EC-01

**Req:** Req 19 AC8, AC9, AC10

#### Cancellation vs expiry: are the re-invocation semantics truly identical?

AC9 and AC10 both allow immediate re-invocation of `environment_delete` after expiry or cancellation. The Platform Console display text for expiry is defined ("Expired - Request New Deletion"). The equivalent display text for a cancelled ticket is not defined — AC10 mentions "Expired - Request New Deletion" for expiry but says nothing about what the console shows after cancellation.

**Confirm:**
1. When a ticket is **cancelled** (as opposed to expired), should the Platform Console display "Cancelled - Request New Deletion" or a different label?
2. Are there any rate-limiting or cooldown rules on re-invoking `environment_delete` after repeated expiry/cancel cycles? (e.g., a tenant could cycle tickets indefinitely to avoid deletion)
3. When AC10 says "The Platform Console SHALL display the previous ticket state as 'Expired - Request New Deletion'", does this apply to cancelled tickets too, or is "Expired" the label used for both?

---

### EC-02

**Req:** Req 4 AC1, AC3

#### Platform Admin `target_user_id` — does AC1's single-tenant-limit check apply to the target user?

AC1 blocks a user whose requested name differs from their existing tenant. AC3 allows a Platform Admin to pass `target_user_id`. If a Platform Admin calls `tenant_create` with `target_user_id=alice` and Alice already has a tenant, should the call be blocked (AC1 single-tenant-limit applies to Alice) or permitted (Platform Admin exemption overrides it)?

**Confirm:**
1. Does the single-tenant-per-user check (AC1) apply to the `target_user_id` when invoked by a Platform Admin, or can a Platform Admin create a second tenant for a user who already has one?
2. If the check applies to `target_user_id`, should the HTTP 403 error body include `target_user_id` in the `existing_tenant_id` response so the Platform Admin knows whose conflict they hit?

---

### EC-03

**Req:** Req 2, Req 13 AC1

#### `cursor://` custom URI scheme: priority, OS registration, and fallback timeout undefined

Req 13 AC1 pre-registers `cursor://anysphere.cursor-mcp/oauth/callback`. Req 2 specifies only the loopback HTTP listener (ports 54321, 18999, 3000). No requirement states when Cursor prefers the custom scheme over the loopback, whether the scheme requires OS-level registration, or what happens if the URI fires but no handler is present.

**Confirm:**
1. Is the `cursor://` scheme a primary or fallback redirect? Under what conditions does Cursor use it instead of the loopback HTTP listener?
2. Is OS-level `cursor://` URI handler registration assumed to be handled by the Cursor installer and therefore out of scope for this spec?
3. If `cursor://` fires but no OS handler is registered, does the auth flow hang indefinitely or time out? What is the timeout and user-visible error?

---

## Appendix: Requirements confirmed clean in this version

The following requirements were read in full and contain no new gaps:

- **Req 1** — Initiate Tenant Creation
- **Req 2** — PKCE flow (except EC-03)
- **Req 3** — Validate and Authorize Requests
- **Req 5** — Collect Cloud Provider Credentials (renumbering resolved)
- **Req 6** — Initiate Environment Provisioning (renumbering resolved; schema complete)
- **Req 8** — Handle Provisioning Errors
- **Req 10** — Cache JWKS for Performance
- **Req 11** — Parse and Format Configuration
- **Req 12** — OAuth Metadata Discovery
- **Req 13** — Client Registration (except EC-03)
- **Req 14** — Token Refresh (claim re-hydration specified; force_token_refresh gap in G-02 is Req 4's responsibility)
- **Req 15** — Platform Git Authentication and Secret Bootstrap
- **Req 18** — Platform Console Polling Strategy

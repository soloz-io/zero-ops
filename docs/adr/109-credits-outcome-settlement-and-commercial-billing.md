# ADR-109: Credits, Outcome Settlement and Commercial Billing

**Date:** 2026-10-26
**Status:** Accepted — conditional on the tax-jurisdiction and payment-provider constraints in Decision §11.
**Amends:** ADR-098 (the record store for infrastructure usage), ADR-012 (the catalogue backend), ADR-052 (the System of Record of Burst Compute Usage).

## Context

Applications on the platform sell defined outcomes at a published credit price. A customer — an individual or an organisation — holds a monthly plan that includes credits, buys further credits, and spends them on outcome runs. The price of a run must be known before it starts and must not change with the run's internal consumption of models, tools or compute.

The facts this decision rests on:

- **No billing, credit or payment capability is deployed.** `metering` is a declared capability with `status: planned` (`manifests/architecture/components.yaml`). The `IBilling` and `IMetering` ports in `internal/kube-sbt/interfaces` are shaped for the withdrawn OpenMeter engine (ADR-066 add.3). No payment provider, tax service or credit ledger exists in any repository.
- **ADR-098 is Proposed and nothing of it is deployed.** It records infrastructure usage as billing-engine events, and three ADRs carry its amendments: ADR-012, ADR-052 (the Burst Compute Usage row) and ADR-066.
- **The identity provider's organisation is the platform tenant** (ADR-060, ADR-094). A customer's organisation or team has no representation in identity, and the shared identity schema holds users and identities only. One instance and one organisation per tenant give a user the same subject in every application of that tenant. ADR-094 Part 2 (caller admission) is accepted; Part 3 (cross-application authorisation) is undecided.
- **The billing engine is Lago v1.53.0, open-source edition, without a commercial licence.** A commercial licence is not available. Verified at the `v1.53.0` source:
  - available: subscriptions with calendar billing and daily proration; activation of a subscription on payment of its first invoice, with a timeout; immediate upgrade with an automatic prorated credit; downgrade at period end; cancellation at period end; one-off invoices with automatic payment; features and entitlements; manual tax rates; signed webhooks with three delivery attempts; payment retry; invoice metadata; usage events deduplicated per organisation, subscription and transaction id;
  - not available: manual credit notes and therefore refunds; voiding a paid invoice; recording a refund made at the payment provider; customer timezones; automatic dunning; automated tax; invoice emails; additional or scoped API keys; back-office single sign-on and audit;
  - absent in every edition: any reservation, hold or synchronous spending control. Prepaid wallets are monetary, applied when an invoice is generated, and their ongoing balance is refreshed by a licensed background job.
- **Formance Ledger v2.4.16 is MIT-licensed and runs on PostgreSQL**, tested against PostgreSQL 16, the version the platform's CloudNativePG clusters run. Verified at the `v2.4.16` source: ordered sources drain accounts in sequence; a transaction whose sources cannot cover it fails with no postings; only the issuance account may go negative; several transfers commit atomically in one transaction; idempotency keys are unique per ledger and a reused key with different input is refused; balances are locked per account in a fixed order with deadlock retry; failed transactions are rolled back without storing their key. Ledger v3 is a release candidate on embedded storage with no licence file, and is not eligible.
- **Workflow runs carry no unique run-request identity, and cancellation is cooperative.** A cancelled run stops at the next step boundary; a step already running continues to completion or its timeout.

## Decision

### 1. Components and authorities

| Fact | Authority |
|---|---|
| Customers, subscriptions, plans, entitlements to offerings, invoices, tax on invoices, payments collected | Lago |
| Whether money was refunded, and how much | The payment provider, recorded in the refund register |
| Credit balances, holds, consumption, expiry, voids and recovery | Formance Ledger |
| Funding accounts and memberships; the binding of holds to runs; lot validity; refund lifecycle | The credits adapter (platform) |
| Outcome completion and delivery | The application |
| Internal cost of runs and infrastructure usage | The usage-analytics database (not financial) |

Lago does not hold credits; its wallets are not used. The ledger does not hold money. Money facts flow one way, from Lago into credit grants. The credits adapter holds no balance: every figure it shows is read from the ledger.

### 2. Funding accounts

A funding account is personal or organisational. An organisational account has members with roles: owner, admin, spender and viewer. An organisational account is a billing object, not an identity-provider organisation, which remains the platform tenant.

A user's personal subscription and balance are unaffected by joining an organisation. Every run names its funding account explicitly; there is no default. A run is authorised only if the requesting subject owns the account or holds a spending role in it. The subject is taken from the user's access token, never from the request body.

A balance is usable by every application of the tenant. Every credit movement records the account, the requesting subject, the application, the run and the outcome version, so usage is attributable per member and per application.

Applications are admitted as callers under ADR-094 Part 2. Authorisation is decided by the account's memberships and does not depend on ADR-094 Part 3.

### 3. The price book

Outcomes, their prices, execution budgets and completion criteria are declared in Git with the application's values and published as immutable, versioned outcome versions. A change creates a new version; a version is never modified or deleted. Plans, their included credits and the offerings they entitle are reconciled into Lago as plans and features (ADR-012). Credit packs are reconciled as add-ons.

A quote returns the current outcome version. An authorisation must name the quoted version, and is refused if a newer one has taken effect, rather than re-priced. A run keeps its version for its whole life.

The execution budget limits internal consumption and never changes the customer's price.

### 4. Credits

Credits are held in lots:

- one included lot per subscription per calendar month, valid until the month ends in UTC;
- one add-on lot per paid purchase, with no expiry.

A reservation draws from valid lots only: included first, then add-on lots oldest first. Credits reserved while their lot was valid may be captured after it expires. An expired lot's balance is swept to the expired account; the sweep is repeatable, so a late arrival is swept too.

A lot is granted only from a paid Lago fact — a paid subscription invoice for the period, a paid upgrade invoice, or a paid one-off invoice for a pack — and the grant is keyed to that fact, so no fact grants twice. An upgrade grants the difference between the new and old allowance when its invoice is paid. Add-on credits are spendable only while the account's subscription is active or in grace; otherwise they are retained but frozen.

### 5. Subscriptions and entitlement

A subscription is created with activation on payment. It grants access and the month's full allowance when its first invoice is paid, and is cancelled if that payment fails or times out.

Entitlement is bounded by the period end of the latest paid subscription invoice, extended by a grace period while a renewal is unpaid. When the grace period ends the account is suspended: no new runs and no purchases. Purchases are refused from the first failed renewal. A stale or unavailable Lago can therefore extend entitlement only to a date a payment proved.

A failed add-on purchase fails that purchase and nothing else. Downgrades and cancellations take effect at the end of the month; upgrades immediately.

Webhooks are notifications only. Every notification is resolved by reading the object from its authority, and changes are also re-read periodically, because Lago attempts each webhook three times.

### 6. Outcome settlement

A run is billable if, and only if, its outcome version's completion criteria are met and the deliverable is recorded. The full price is captured; otherwise the hold is released. User cancellation before delivery, budget exhaustion, failure and partial results all release. Retries and resumed executions remain under the run's single hold.

The protocol:

1. The application records the run with a unique run-request identity before requesting credits.
2. A hold is reserved, keyed by that identity, with a deadline for binding.
3. The hold is bound to the run. Both bindings are unique and write-once. Execution starts only after binding succeeds.
4. A hold that is not bound by its deadline is released. Because nothing executes before binding, this strands no credits and permits no unfunded execution.
5. A bound hold ends only by settlement and never expires by itself. A hold open beyond the run's maximum duration raises an alert, and the run is stopped.
6. The deliverable is released to the customer only after the capture succeeds. A settlement against a hold that is already released is refused, so output produced after a release is never delivered.

A run is terminal only when its runtime has written a terminal state after its step-boundary check or a step failure, every sandbox it started is gone, and no step holds a lease. A cancellation request is not termination.

Release is not a ledger revert. It is a new transaction that returns each reserved amount to its source lot if that lot is still valid, and to the expired account otherwise.

### 7. Refunds

The platform owns refund orchestration, its lifecycle, the refund register and refund reconciliation. It generates no invoice and no credit-note document.

The refund register is a financial record with the retention of invoices. It holds one record per refund and an append-only history of every provider notification and state change. A refund is converted to credits only when it is traced to exactly one paid one-off invoice whose lines are all credit packs at one unit price. The credits owed are then the refunded amount excluding tax divided by that price, rounded up and capped at the credits granted. Any other refund — of a subscription fee, of a mixed invoice, or one that cannot be traced — is recorded with no credit effect until finance sets the credits owed.

The lifecycle:

1. The unused credits are held.
2. The refund is submitted to the provider with the refund's identity as the provider's idempotency key.
3. The provider's current refund object decides the outcome. Credits are voided on success. They are released only on a terminal failure that a notification reports and a read of the object confirms.
4. A refund that remains pending past its expected time raises an alert and releases nothing.

A late success therefore finds its credits still held, and a resubmission returns the original refund.

### 8. Recovery

A refund that succeeds after its credits were released, or a refund made outside the platform, opens a recovery case:

- The account is restricted: no new authorisations and no purchases. Existing holds settle normally.
- The case funds a per-case recovery allowance in the ledger, once, with the credits owed. Every recovery moves the same quantity out of that allowance in the same transaction as the void it accompanies, so aggregate recovery cannot exceed the amount owed whatever any process believed. Recovery draws on available credits in this order: the refunded purchase's lot, other add-on lots, then the current included lot. A release of a hold that existed when the case opened routes its valid credits to recovery first, up to the remaining allowance.
- A shortfall is never a negative balance. Once every hold that existed at opening is terminal, finance resolves the remaining allowance as billed — a one-off invoice — or written off.
- The case closes, and the restriction lifts, when the allowance is zero, those holds are terminal, and any shortfall invoice is paid.

At every committed state the allowance, used, billed and written-off balances sum to the credits owed, and none is negative. Money for billed and written-off credits is reconciled separately, at the source lot's unit price excluding tax.

### 9. Financial reporting

The platform computes two measures per currency:

- **net cash collected:** payments collected minus refunds succeeded, with provider fees shown separately;
- **net sales excluding tax:** invoice subtotals minus refunds, both excluding tax.

Tax collected and tax refunded are reported separately and never netted into either measure. Each refund's tax is apportioned from its original invoice's applied taxes. Accounting revenue is not computed by the platform. Grants, consumption, expiry, voids and recovery are exported with their unit value for finance's own revenue policy.

### 10. Metering

Three concerns are kept separate:

- **customer charges**, held by the ledger under this ADR;
- **internal cost of runs**, emitted by applications per run and stored as non-authoritative telemetry, deduplicated per event;
- **infrastructure usage**, collected by ADR-098's pipeline.

The last two are stored in one usage-analytics database provisioned by the `metering` capability (ADR-099), joined by pod identity, and never sent to the billing engine.

### 11. Conditions

- **Tax jurisdictions.** This design holds only for jurisdictions where manual tax rates suffice and a refund requires no credit-note document. Where either fails, the billing-engine decision is reopened, with Kill Bill as the evaluated alternative. The platform does not generate credit notes in any case.
- **Payment provider.** The provider must offer idempotent refund creation with a caller-supplied key, a credential restricted to creating and reading refunds and separate from the credential the billing engine holds, and a refund-status webhook with unique event identities or an equivalent status interface. A provider lacking any of these is not eligible; there is no fallback to a broader credential.

### 12. The consumer contract

Applications see capability names only: `billing` and `credits` in values, endpoints and environment. They hold no billing-engine, ledger or payment-provider credential.

### Alternatives considered

- **Lago wallets as the credit authority**, open-source or licensed. Rejected: no edition offers a hold. Wallet priority applies only when an invoice is generated, and wallet transactions carry no idempotency key.
- **TigerBeetle.** Native pending transfers with timeouts, but a new non-PostgreSQL stateful system, and no history queries for per-member usage. Retained as the fallback if the ledger in this ADR becomes ineligible.
- **A custom PostgreSQL ledger.** It would reimplement double entry, overdraft prevention and idempotency that the selected ledger provides.
- **The commercial Lago licence for refunds.** Not available.
- **Customer organisations as identity-provider organisations.** Would redefine the organisation as something other than the platform tenant, which ADR-060 and ADR-094 and the tenant claim every receiver reads depend on.
- **Recording infrastructure usage in the billing engine.** Rejected: it mixes records that are not charges with those that are.

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Price book (outcome versions) | Git | Application declares, Platform publishes | Catalogue reconciler | Credits adapter, applications | Day-1+ |
| Plans, features, credit packs | Git → Lago | Application declares, Platform reconciles | Catalogue reconciler | Lago | Day-1+ |
| Customers, subscriptions | Lago | Platform | Lago | Credits adapter | Day-1+ |
| Invoices, invoice tax, payments collected | Lago | Lago | Lago | Finance, credits adapter | Day-1+ |
| Refunds (money) | Payment provider | Platform | Credits adapter | Refund register | Day-1+ |
| Refund register | Billing database | Platform | Credits adapter | Finance | Day-1+ |
| Credit balances, holds, recovery allowances | Formance Ledger | Platform | Formance Ledger | Credits adapter | Day-1+ |
| Funding accounts, memberships | Billing database | Account owners and admins | Credits adapter | Credits adapter | Day-1+ |
| Hold-to-run bindings, lot validity | Billing database | Platform | Credits adapter | Credits adapter | Day-1+ |
| Runs, outcome delivery | Application database | Application | Application runtime | Credits adapter | Day-1+ |
| Internal run cost, infrastructure usage | Usage-analytics database | Platform | `metering` capability | Finance, SRE | Day-1+ |
| Lago, ledger and payment-provider credentials | Infisical | Platform | ESO | Credits adapter, Lago | Day-1+ |

## Consequences

### Positive

- Spending is decided synchronously and cannot exceed a balance under concurrency. Double entry, overdraft prevention and idempotency are properties of an open-source ledger rather than platform code.
- The customer's price is fixed and pinned to an immutable version. Charging only on delivery means a failure needs no refund.
- Every financial fact has one authority. Recovery after a refund is bounded by the ledger itself, so a crash or a stale view can cause a refusal but never over-recovery.
- Entitlement fails closed when the billing engine is unavailable, at a date a payment proved.
- No component is licensed, and the ledger runs on the PostgreSQL the platform already operates.

### Negative

- The platform owns a refund workflow and a financial register. Lago's reports remain gross of refunds, and finance reporting depends on the register.
- The platform holds a payment-provider credential and receives provider webhooks, a trust boundary it did not have.
- Lago's open-source gaps fall to the platform or remain open: invoice notices need a mail capability that does not exist; dunning relies on provider retries or the payment-retry interface; tax is manual; the back office has no single sign-on or audit of its own.
- Two stateful financial components join the box, Lago and the ledger, each needing backup and restore.
- The design is valid only under the conditions in §11. A jurisdiction requiring credit-note documents reopens the billing-engine choice.
- Applications take on obligations: a unique run-request identity, binding before execution, terminal states written by the runtime with sandboxes torn down, delivery gated on capture, completion criteria per outcome version, and per-run cost events.

## Impact

- **ADR-098** — amended. Infrastructure usage is recorded in the usage-analytics database, not as billing-engine events. Its collection pipeline is unchanged.
- **ADR-012** — amended. Plans, features and packs reconcile into Lago. Outcome versions are a platform price book. Subscription state remains runtime state.
- **ADR-052** — amended. The System of Record of Burst Compute Usage is the usage-analytics database.
- **`manifests/architecture/components.yaml`** — `metering` gains this ADR in `declared_by`, and its note names the ledger, the billing engine and the usage-analytics database.
- **`internal/kube-sbt/interfaces`** — `IBilling` and `IMetering` are replaced by the credits contract. They are not adapted.
- **Decisions this ADR leaves open:** the grace-period length; statutory retention for invoices, the refund register and the credit journal; prices, allowances, packs and the currency-to-credit rate; per-member spending caps; a mail capability for invoice notices; whether ADR-094 Part 2 rendering admits a platform service as a caller target.

## References

- ADR-003: ESO–Infisical Secret Management
- ADR-012: Tenant Billing Metering Operator Pattern
- ADR-039: Platform Ownership Model
- ADR-040: Day-0 vs Day-1 Lifecycle Boundary
- ADR-041: Controller Responsibility Matrix
- ADR-043: Control Plane Authority Model
- ADR-052: Sandbox Compute and S3 Workspaces
- ADR-060: Zitadel Identity Provider
- ADR-066: The Platform Boundary
- ADR-094: One Identity Project per Application
- ADR-095: Access Tokens for APIs
- ADR-097: Service-to-Service is Client Credentials
- ADR-098: Metering Sources over Spoke Egress and Records in Lago
- ADR-099: The Platform Provisions a Database, Not a Schema
- `reference-projects/lago` (api at `v1.53.0`)
- `reference-projects/ledger` (tag `v2.4.16`)

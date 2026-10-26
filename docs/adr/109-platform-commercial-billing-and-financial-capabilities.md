# ADR-109: Platform Commercial Billing and Financial Capabilities

**Date:** 2026-10-26
**Status:** Accepted — conditional on the tax-jurisdiction and payment-provider constraints in Decision §7.
**Amends:** ADR-098 (the record store for infrastructure usage), ADR-012 (the catalogue backend), ADR-052 (the System of Record of Burst Compute Usage).

## Context

Applications may adopt different commercial pricing models: subscription-based, usage-based, outcome-based, tiered and hybrid. The platform provides the shared commercial billing and financial capabilities through which an application defines and operates the model it chooses. Applications consume these capabilities. They do not build or operate billing infrastructure, keep authoritative financial ledgers of their own, or integrate directly with the billing engine, the ledger or the payment provider.

The first use case is outcome-based pricing funded by platform credits:
- the price of a run is a published number of credits, known before the run starts;
- that price does not change with the run's internal consumption;
- customers hold plans that include credits, and buy more.

Its published prices, credit allowances, expiry rules and charge-on-delivery policy are specific to that model.

The facts this decision rests on:

- **No billing, credit or payment capability is deployed.** `metering` is a declared capability with `status: planned` (`manifests/architecture/components.yaml`). The `IBilling` and `IMetering` ports in `internal/kube-sbt/interfaces` are shaped for the withdrawn OpenMeter engine (ADR-066 add.3). No payment provider, tax service or credit ledger exists in any repository.
- **ADR-098 is Proposed and nothing of it is deployed.** It records infrastructure usage as billing-engine events, and three ADRs carry its amendments: ADR-012, ADR-052 (the Burst Compute Usage row) and ADR-066.
- **The identity provider's organisation is the platform tenant** (ADR-060, ADR-094). A customer's organisation or team has no representation in identity, and the shared identity schema holds users and identities only. One instance and one organisation per tenant give a user the same subject in every application of that tenant. ADR-094 Part 2 (caller admission) is accepted; Part 3 (cross-application authorisation) is undecided.
- **The billing engine is Lago v1.53.0, open-source edition (AGPL-3.0), without a commercial licence.** A commercial licence is not available. Verified at the `v1.53.0` source:
  - available: subscriptions with calendar billing and daily proration; activation of a subscription on payment of its first invoice, with a timeout; immediate upgrade with an automatic prorated credit; downgrade at period end; cancellation at period end; one-off invoices with automatic payment; usage events deduplicated per organisation, subscription and transaction id; features and entitlements; manual tax rates; signed webhooks with three delivery attempts; payment retry; invoice metadata;
  - not available: manual credit notes and therefore refunds; voiding a paid invoice; recording a refund made at the payment provider; customer timezones; automatic dunning; automated tax; invoice emails; additional or scoped API keys; back-office single sign-on and audit;
  - absent in every edition: any reservation, hold or synchronous spending control. Prepaid wallets are monetary, applied when an invoice is generated, and their ongoing balance is refreshed by a licensed background job.
- **Formance Ledger v2.4.16 is MIT-licensed and runs on PostgreSQL**, tested against PostgreSQL 16, the version the platform's CloudNativePG clusters run. Verified at the `v2.4.16` source: ordered sources drain accounts in sequence; a transaction whose sources cannot cover it fails with no postings; only the issuance account may go negative; several transfers commit atomically in one transaction; idempotency keys are unique per ledger and a reused key with different input is refused; balances are locked per account in a fixed order with deadlock retry; failed transactions are rolled back without storing their key. Ledger v3 is a release candidate on embedded storage with no licence file, and is not eligible.
- **Workflow runs carry no unique run-request identity, and cancellation is cooperative.** A cancelled run stops at the next step boundary; a step already running continues to completion or its timeout.

## Decision

### 1. Scope

This ADR defines the platform's shared commercial billing and financial capabilities, the components that provide them, their System of Record boundaries, and the invariants they preserve. It does not prescribe a pricing model.

Non-goals:
- one pricing model across all applications;
- requiring any application to use credits, outcome prices, monthly credit allowances or expiring credit lots;
- defining an application's billable unit or settlement trigger in the common platform layer;
- allowing an application to build its own billing infrastructure or bypass the shared financial controls.

### 2. Pricing-model ownership

**The application owns its pricing decisions:**
- the supported model it selects;
- its billable units, prices, formula and price versions;
- the conditions under which a charge is earned, including completion, usage, cancellation and partial-delivery rules;
- the entitlements attached to its offerings;
- its funding and allowance policies where the model has them.

It declares the model and its configuration through the platform contract. Its pricing rules are explicit and versioned, so every quote, authorisation and settlement traces to the policy in force.

**The platform owns:**
- billing-engine integration for subscriptions, invoices, payment collection and invoice tax;
- financial accounting on the selected ledger, where a model uses balances;
- authorisation and reservation, where a model pre-authorises a charge;
- settlement, release and adjustment matched to a model's lifecycle;
- funding accounts, identity-aware authorisation, idempotency, audit and reconciliation;
- the payment-provider integration and the refund workflow;
- metering and usage analytics, kept apart from financial records;
- model declarations, their validation and reconciliation, and the common interfaces.

The platform defines the supported model contracts and their guarantees. A new model is a platform extension; no application recreates financial infrastructure to obtain one.

**The model contract.** Every supported model declares:
- its identity and version;
- its price, or a reference to its pricing policy;
- its billable unit and the trigger that earns a charge;
- its funding mechanism, where it has one;
- the capability contracts it composes (see "Pricing-model extensibility" below);
- the authoritative source of its billable facts;
- its settlement and adjustment semantics, where it has them.

The contract states what a model must declare. It does not prescribe an interface or an implementation.

**Shared invariants and model-specific policies.** The platform enforces, for every model:
- idempotent processing;
- traceable financial movements;
- account isolation;
- reconciliation;
- no overspend where a model spends a finite balance.

Everything else is a policy of a model. Each model declares which shared capabilities it uses and the rules governing them. None of these is universal:
- denomination in credits;
- discrete outcomes;
- a hold before execution;
- capture on delivery;
- monthly allowances or expiring lots;
- subscriptions that grant credits, or credit plans that grant access;
- one cancellation, refund or partial-result policy.

A model that uses authorisation, balances or settlement may not bypass them.

Examples of models these capabilities can serve: an outcome model reserves a fixed price and captures on delivery; a usage model records quantities and aggregates them for billing; a recurring model relies on the billing engine's subscription and invoice lifecycle; a hybrid combines them. Only the outcome-credit model (§6) is committed by this ADR.

**Pricing-model extensibility.** The architecture is ports and adapters with composable capability contracts.

There are eight logical capability contracts. They are contracts, not services:

| Capability | Purpose |
|---|---|
| Catalogue and pricing policy | Versioned commercial declarations and price calculation |
| Subscription | Recurring agreements and their lifecycle |
| Entitlement | Access to an application's offerings |
| Metering | Ingesting and aggregating billable quantities |
| Authorisation | Reserving prepaid value or finite resources, where a model needs it |
| Settlement | Finalising authorised obligations |
| Billing | Commercial charges and invoices |
| Adjustments and refunds | Changes after a charge |

**How models are built from them:**
- A model is a platform-owned strategy, registered and versioned. It composes only the capabilities its semantics need. No model must implement a reservation or settlement lifecycle, and there is no single interface spanning quote, reserve, settle, invoice and refund.
- An application selects a registered model and configures it through its declaration. It never supplies executable pricing or billing code to the platform.
- A strategy decides which business rules apply. It does not reimplement correctness: ledger integrity, idempotency and payment processing remain with the components that own them. A strategy expresses its financial movements as ledger transactions and its recurring and invoiced charges through the billing engine.
- Behaviour the billing engine already provides is exposed through it, not rebuilt.

**Adapters to Lago, the ledger and the payment provider:**
- They sit behind the capability contracts. Applications depend on the contracts only.
- Each adapter identifies its implementation.
- Each returns a uniform result: the outcome, a reason, a typed error code on failure, and the identity of the operation, which is enough to reconcile it.
- **No fabricated results.** An adapter never returns a fabricated success, a default value or a fallback financial result. This is unlike feature-flag providers, which substitute a default when their provider fails.

**Adapter outcomes.** Every financial operation reports one of three outcomes:

| Outcome | Meaning |
|---|---|
| **Succeeded** | The authoritative system confirms the operation committed |
| **Rejected** | The authoritative system confirms the operation did not commit |
| **Indeterminate** | It may have committed, and its outcome is not yet known: a timeout, a lost response, a dropped connection |

**Handling indeterminate operations:**
- A transport failure alone is never treated as a rejection.
- An indeterminate operation is resolved before any conflicting or dependent financial transition is allowed. It is resolved either by reading its status from the authoritative system using its operation identity, or by retrying it with the same idempotency key.
- Idempotency, status resolution and reconciliation belong to the shared financial capabilities, not to individual model strategies.

**When a new model is added:** only when a concrete application needs a capability the registered models lack. No general pricing engine is built in anticipation.

### 3. Components and authorities

| Fact | Authority |
|---|---|
| Pricing and billability | The application's selected model and its versioned rules |
| Customers, subscriptions, invoices, invoice tax, payments collected | Lago |
| Credit balances, reservations, consumption, expiry and recovery, for credit-funded models | Formance Ledger |
| Whether money was refunded, and how much | The payment provider, recorded in the refund register |
| Model declarations, funding accounts and memberships, authorisation and settlement coordination, refund register | The billing integration (model-neutral) |
| Hold-to-run bindings, lot validity, credit grants from paid facts, recovery cases | The credits adapter (credit-funded models) |
| Billable facts: delivery, completion, usage | The application |
| Internal run cost and infrastructure usage | The usage-analytics database (not financial) |

Formance is not the authority for every charge; its semantics apply where a model uses platform credits. Lago's subscription capabilities do not oblige any application to a subscription structure.

The billing integration is the platform's model-neutral interface and coordination capability. The credits adapter is its implementation for credit-funded models. They are responsibilities, not separate services.

Lago's wallets are not used. The ledger holds no money. Money facts flow one way, from Lago into credit grants. The credits adapter holds no balance: every figure it shows is read from the ledger.

### 4. Shared capabilities

**Funding accounts.**
- An account is personal or organisational. An organisational account has members with roles: owner, admin, spender and viewer. It is a billing object, not an identity-provider organisation, which remains the platform tenant.
- Joining an organisation leaves a user's personal subscription and balance unchanged.
- Every charge names its funding account explicitly, with no default. It is authorised only if the requesting subject owns the account or holds a spending role in it. The subject is taken from the user's access token, never from the request body.
- Platform credits are shared across the authorised applications of the tenant, subject to the selected model's funding and eligibility policies. Other model-specific balances or allowances are not transferable across applications unless their model declares it. Every movement records the account, subject, application and the model's reference (for example run and price version), so usage is attributable per member and per application.
- Applications are admitted as callers under ADR-094 Part 2. Authorisation is decided by memberships and does not depend on ADR-094 Part 3.

**Declarations.**
- Models, prices, plans, entitlements and packs are declared in Git with the application's values, validated, and published as immutable versions.
- Plans and entitlements reconcile into Lago as plans and features; packs as add-ons (ADR-012).
- Subscription state is runtime state, never Git.

**Subscriptions and entitlement.**
- A paid subscription is created with activation on payment. It is entitled when its first invoice is paid, and cancelled if that payment fails or times out.
- Entitlement is bounded by the period end of the latest paid subscription invoice, extended by the model's grace period while a renewal is unpaid. When grace ends, the account is suspended for that model's paid actions. A stale or unavailable Lago can therefore extend entitlement only to a date a payment proved.
- Webhooks are notifications only. Each one is resolved by reading the object from its authority, and changes are also re-read periodically, because Lago attempts each webhook three times.

**Refunds.**
- The platform owns refund orchestration, its lifecycle, the refund register and refund reconciliation. It generates no invoice and no credit-note document.
- The register is a financial record with the retention of invoices: one record per refund, plus an append-only history of every provider notification and state change.
- The refund is submitted with the refund's identity as the provider's idempotency key, so a resubmission returns the original refund.
- The provider's current refund object decides the outcome. A refund pending past its expected time raises an alert and changes nothing else.
- Where a refund affects credits, §6.5 applies.

**Reporting.**
- The platform computes two measures per currency:
  - **net cash collected:** payments collected minus refunds succeeded, with provider fees shown separately;
  - **net sales excluding tax:** invoice subtotals minus refunds, both excluding tax.
- Tax collected and tax refunded are reported separately and never netted into either measure. Each refund's tax is apportioned from its original invoice's applied taxes.
- Accounting revenue is not computed by the platform. Credit grants, consumption, expiry, voids and recovery are exported with their unit value for finance's own revenue policy.

**Metering.**
- Internal run cost is emitted by applications and stored as non-authoritative telemetry, deduplicated per event.
- Infrastructure usage is collected by ADR-098's pipeline.
- Both are stored in one usage-analytics database provisioned by the `metering` capability (ADR-099), joined by pod identity, and never sent to the billing engine.

**Consumer contract.** Applications see capability names only: `billing` and `credits` in values, endpoints and environment. They hold no billing-engine, ledger or payment-provider credential.

### 5. Access and funding are separate concepts

**Access.** A subscription to an offering grants access to that offering and its published outcomes, according to the application's entitlement policy.

**Funding.**
- A credit plan grants that plan's included credits.
- A credit pack grants further credits under its pack policy.

Neither implies the other: an offering subscription grants no credits, and a credit plan grants no access to any offering. An application may combine them, but only as an explicit model configuration. The platform provides subscriptions, entitlements and credits without forcing their combination.

### 6. The initial supported model: outcome-based pricing funded by platform credits

The rules in this section are the outcome-credit model's own. They are not semantics of any other model.

#### 6.1 Price book

- Each outcome version is immutable and carries:
  - its credit price;
  - an execution budget, which limits internal consumption and never changes the price;
  - completion criteria.
- A quote returns the current version. An authorisation must name the quoted version, and is refused rather than re-priced if a newer one has taken effect.
- A run keeps its version for its whole life.

#### 6.2 Credit lots

- Credits are held in two kinds of lot:
  - an included lot per credit plan per calendar month, valid until the month ends in UTC;
  - an add-on lot per paid pack, with no expiry.
- A reservation draws from valid lots only: included first, then add-on lots oldest first.
- Credits reserved while valid may be captured after their lot expires. An expired lot's balance is swept to the expired account; the sweep is repeatable.
- A lot is granted only from a paid Lago fact, keyed to that fact, so no fact grants twice:
  - a paid plan invoice for the period;
  - a paid upgrade invoice, which grants the difference in allowance;
  - a paid pack invoice.
- Add-on credits are spendable only while the account's credit plan is active or in grace; otherwise they are retained but frozen.
- Purchases are refused from the first failed renewal. A failed pack purchase fails that purchase and nothing else.
- Plan downgrades and cancellations take effect at the end of the month; upgrades immediately.

#### 6.3 Settlement

A run is billable if, and only if, its version's completion criteria are met and the deliverable is recorded. The full price is captured; otherwise the hold is released. User cancellation before delivery, budget exhaustion, failure and partial results all release. Retries and resumed executions remain under the run's single hold.

The protocol:

1. The application records the run with a unique run-request identity before requesting credits.
2. A hold is reserved, keyed by that identity, with a deadline for binding.
3. The hold is bound to the run. Both bindings are unique and write-once. Execution starts only after binding succeeds.
4. A hold not bound by its deadline is released. Since nothing executes before binding, no credits are stranded and no execution is unfunded.
5. A bound hold ends only by settlement and never expires by itself. A hold open beyond the run's maximum duration raises an alert, and the run is stopped.
6. The deliverable is released to the customer only after the capture succeeds. Settlement against a hold already released is refused, so output produced after a release is never delivered.

A run is terminal only when its runtime has written a terminal state after its step-boundary check or a step failure, every sandbox it started is gone, and no step holds a lease. A cancellation request is not termination.

#### 6.4 Release

Release is not a ledger revert. It is a new transaction that returns each reserved amount to its source lot if that lot is still valid, and to the expired account otherwise.

#### 6.5 Refunds affecting credits

**Conversion to credits.** A refund converts to credits only when it is traced to exactly one paid pack invoice whose lines are all packs at one unit price. The credits owed are then:
- the refunded amount excluding tax,
- divided by that unit price,
- rounded up,
- capped at the credits granted.

Any other refund — of a plan fee, of a mixed invoice, or one that cannot be traced — has no credit effect until finance sets the credits owed.

**The refund lifecycle for credits:**
1. The unused credits are held.
2. They are voided when the provider reports success.
3. They are released only on a terminal failure that a notification reports and a read of the refund object confirms.

A late success therefore finds its credits still held.

#### 6.6 Recovery

A refund that succeeds after its credits were released, or a refund made outside the platform, opens a recovery case:

- The account is restricted: no new authorisations and no purchases. Existing holds settle normally.
- The case funds a per-case recovery allowance in the ledger, once, with the credits owed. Every recovery moves the same quantity out of that allowance in the same transaction as the void it accompanies, so aggregate recovery cannot exceed the amount owed whatever any process believed. Recovery draws on available credits in this order: the refunded pack's lot, other add-on lots, then the current included lot. A release of a hold that existed when the case opened routes its valid credits to recovery first, up to the remaining allowance.
- A shortfall is never a negative balance. Once every hold that existed at opening is terminal, finance resolves the remaining allowance as billed — a one-off invoice — or written off.
- The case closes, and the restriction lifts, when the allowance is zero, those holds are terminal, and any shortfall invoice is paid.

At every committed state the allowance, used, billed and written-off balances sum to the credits owed, and none is negative. Money for billed and written-off credits is reconciled separately, at the source lot's unit price excluding tax.

### 7. Conditions

- **Tax jurisdictions.** This design holds only for jurisdictions where manual tax rates suffice and a refund requires no credit-note document. Where either fails, the billing-engine decision is reopened, with Kill Bill as the evaluated alternative. The platform does not generate credit notes in any case.
- **Payment provider.** The provider must offer:
  - idempotent refund creation with a caller-supplied key;
  - a credential restricted to creating and reading refunds, separate from the billing engine's credential;
  - a refund-status webhook with unique event identities, or an equivalent status interface.

  A provider lacking any of these is not eligible; there is no fallback to a broader credential.

### Alternatives considered

- **Lago wallets as the credit authority**, open-source or licensed. Rejected: no edition offers a hold. Wallet priority applies only when an invoice is generated, and wallet transactions carry no idempotency key.
- **TigerBeetle.** Native pending transfers with timeouts, but a new non-PostgreSQL stateful system, and no history queries for per-member usage. Retained as the fallback if the ledger in this ADR becomes ineligible.
- **A custom PostgreSQL ledger.** It would reimplement double entry, overdraft prevention and idempotency that the selected ledger provides.
- **The commercial Lago licence for refunds.** Not available.
- **A universal pricing-model interface** covering quote, reserve, settle, invoice and refund. Rejected: it would impose the outcome-credit lifecycle on every model.
- **Application-supplied pricing or billing plugins.** Rejected: executable code from applications inside the financial service weakens the platform/application boundary and its correctness and security guarantees.
- **OpenMeter as the billing engine.** It separates plans, rate cards, prices, features, entitlements and meters, which is the reference for this ADR's catalogue composition. Its billing releases are beta, and its runtime brings Kafka and ClickHouse. It is a design reference, not a deployment.
- **Kill Bill as the billing engine.** Apache-2.0 and modular, with separate catalogue, entitlement, subscription, usage, invoice, payment and overdue modules, and plugin seams for payment, payment control, invoice, catalogue and usage. It is the reference for capability boundaries and their extension contracts, and the evaluated alternative under §7.
- **Customer organisations as identity-provider organisations.** Would redefine the organisation as something other than the platform tenant, which ADR-060 and ADR-094 and the tenant claim every receiver reads depend on.
- **Recording infrastructure usage in the billing engine.** Rejected: it mixes records that are not charges with those that are.

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Pricing-model declarations, price versions | Git | Application declares, Platform validates and publishes | Catalogue reconciler | Billing integration, applications | Day-1+ |
| Plans, features, packs | Git → Lago | Application declares, Platform reconciles | Catalogue reconciler | Lago | Day-1+ |
| Customers, subscriptions | Lago | Platform | Lago | Billing integration | Day-1+ |
| Invoices, invoice tax, payments collected | Lago | Lago | Lago | Finance, billing integration | Day-1+ |
| Refunds (money) | Payment provider | Platform | Billing integration | Refund register | Day-1+ |
| Refund register | Billing database | Platform | Billing integration | Finance | Day-1+ |
| Credit balances, holds, recovery allowances | Formance Ledger | Platform | Formance Ledger | Credits adapter | Day-1+ |
| Funding accounts, memberships | Billing database | Account owners and admins | Billing integration | Billing integration | Day-1+ |
| Hold-to-run bindings, lot validity | Billing database | Platform | Credits adapter | Credits adapter | Day-1+ |
| Billable facts (delivery, completion, usage) | Application database | Application | Application runtime | Billing integration | Day-1+ |
| Internal run cost, infrastructure usage | Usage-analytics database | Platform | `metering` capability | Finance, SRE | Day-1+ |
| Lago, ledger and payment-provider credentials | Infisical | Platform | ESO | Billing integration, credits adapter, Lago | Day-1+ |

## Consequences

### Positive

- Applications select and evolve pricing models on shared infrastructure, with the same financial invariants for every capability a model uses.
- No commercial licence is required. Lago is used under AGPL-3.0 as unmodified upstream images, and Formance Ledger under MIT. The ledger runs on the PostgreSQL the platform already operates.
- In credit-funded models:
  - spending is decided synchronously and cannot exceed a balance under concurrency;
  - double entry, overdraft prevention and idempotency are properties of an open-source ledger rather than platform code;
  - recovery is bounded by the ledger itself.
- In the outcome-credit model, the price is pinned to an immutable version, and charging only on delivery means a failure needs no refund.
- Every financial fact has one authority. Entitlement fails closed when the billing engine is unavailable.

### Negative

- The platform owns a refund workflow and a financial register. Lago's reports remain gross of refunds, and finance reporting depends on the register.
- The platform holds a payment-provider credential and receives provider webhooks, a trust boundary it did not have.
- Lago's open-source gaps fall to the platform or remain open:
  - invoice notices need a mail capability that does not exist;
  - dunning relies on provider retries or the payment-retry interface;
  - tax is manual;
  - the back office has no single sign-on or audit of its own.
- Two stateful financial components join the box, Lago and the ledger, each needing backup and restore.
- The design is valid only under the conditions in §7. A jurisdiction requiring credit-note documents reopens the billing-engine choice.
- Each new pricing model is a platform extension with its own contract and guarantees.
- An application using the outcome-credit model takes on these obligations:
  - a unique run-request identity;
  - binding before execution;
  - terminal states written by the runtime, with sandboxes torn down;
  - delivery gated on capture;
  - completion criteria per outcome version;
  - per-run cost events.

## Impact

- **ADR-098** — amended. Infrastructure usage is recorded in the usage-analytics database, not as billing-engine events. Its collection pipeline is unchanged.
- **ADR-012** — amended. Plans, features and packs reconcile into Lago. Price versions are platform-published declarations. Subscription state remains runtime state.
- **ADR-052** — amended. The System of Record of Burst Compute Usage is the usage-analytics database.
- **`manifests/architecture/components.yaml`** — `metering` gains this ADR in `declared_by`, and its note names the ledger, the billing engine and the usage-analytics database.
- **`internal/kube-sbt/interfaces`** — `IBilling` and `IMetering` are replaced by the model contracts. They are not adapted.
- **Decisions this ADR leaves open:**
  - the outcome-credit model's grace-period length;
  - statutory retention for invoices, the refund register and the credit journal;
  - prices, allowances, packs and the currency-to-credit rate;
  - per-member spending caps;
  - a mail capability for invoice notices;
  - whether ADR-094 Part 2 rendering admits a platform service as a caller target.

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
- `reference-projects/billing/lago` (api at `v1.53.0`)
- `reference-projects/ledger` (tag `v2.4.16`)
- `reference-projects/billing/killbill`: capability modules and plugin seams
- `reference-projects/billing/openmeter`: `openmeter/productcatalog`, catalogue composition
- OpenFeature provider specification: a provider-neutral contract with provider metadata and typed errors

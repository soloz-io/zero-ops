# ADR-109: The Credit Authority

**Date:** 2026-10-26
**Status:** Accepted

## Context

A product built on the platform may charge for consumption in prepaid credits: a customer's credits are reserved before work starts, captured or released when it ends, and must never be spent twice under concurrency. That needs an authoritative balance with transactional guarantees. Pricing, the commercial lifecycle around the credits, and the money that buys them are the product's.

The facts this decision rests on:

- **No credit ledger, billing or payment capability is deployed** in any repository. `metering` is a declared capability with `status: planned` (`manifests/architecture/components.yaml`).
- **Formance Ledger v2.4.16 is MIT-licensed and runs on PostgreSQL**, tested against PostgreSQL 16, the version the platform's CloudNativePG clusters run. Verified at the `v2.4.16` source:
  - ordered sources drain accounts in sequence;
  - a transaction whose sources cannot cover it fails with no postings;
  - only the issuance account may go negative;
  - several transfers commit atomically in one transaction;
  - idempotency keys are unique per ledger, and a reused key with different input is refused;
  - balances are locked per account in a fixed order, with deadlock retry;
  - failed transactions are rolled back without storing their key;
  - transactions carry metadata, and history is queryable by account and metadata;
  - a ledger lives in a bucket, and each bucket is its own PostgreSQL schema (`internal/storage/ledger/store.go:161`).
- **Formance Ledger v2.4.16 authenticates and does not authorise.** One JWT middleware guards every route (`internal/api/v2/routes.go`), including bucket deletion and restore, exporter management and ledger creation. No per-ledger or per-operation permission exists.
- **Ledger v3 is a release candidate** on embedded storage, with no licence file.
- **A product's coordinating service calls the platform as itself,** with client credentials (ADR-097).

## Decision

### 1. Scope

The platform offers a Credit Authority: an authoritative ledger of credits with transactional guarantees, isolated per tenant. It offers no monetary billing, payment collection or pricing capability. Those belong to the product layer that coordinates credits, and to the external providers it integrates.

### 2. The Credit Authority

- **Formance Ledger v2.4.16**, on a database the platform provisions (ADR-099). Ledger v3 is not eligible.
- **It holds credits, never money.** Every movement is a balanced transaction between credit accounts.
- **It executes authorised movements and determines no price.** Which movement is posted, for what amount and why is the coordinating product layer's decision.
- **It is independent of every billing and payment provider.** Several product-defined credit models can share its balances, reservations and settlement guarantees without coupling their pricing to a commercial provider. Replacing a billing or payment provider migrates no balance, reservation or credit history.

### 3. Guarantees

The platform guarantees, for every tenant's ledger:

- **Atomicity.** A transaction's postings commit together or not at all.
- **No overdraft.** No account but the issuance account goes negative. A transaction its sources cannot cover is refused whole.
- **Idempotency.** A transaction carries a caller-supplied key, unique per ledger. A repeat returns the original; a repeat with different input is refused; a failed transaction stores no key, so it can be retried.
- **Serialisable balances.** Concurrent transactions on one account are ordered by its lock, so concurrent reservations never exceed a balance.
- **Traceability.** Every transaction keeps its metadata and is queryable by account and metadata.
- **Durability.** Committed transactions survive the loss of a node, and the ledger's database is backed up and restorable.

These guarantees hold however often the coordinating layer repeats a call. Financial integrity never depends on any caller executing exactly once.

### 4. Tenant isolation

- **One ledger per tenant, in its own bucket.** Ledgers are created by the platform when a tenant is given the capability, never by a caller.
- **One caller per tenant:** the tenant's coordinating product service, with its own client credentials (ADR-097).
- **The ledger authenticates only, so isolation is enforced in front of it:**
  - a caller reaches only its own tenant's ledger routes;
  - system routes are never reachable by a caller: bucket deletion and restore, exporter management and ledger creation;
  - network policy admits only the tenant's coordinating service to the ledger.
- **Applications never reach the ledger.** Consumer applications reach credits only through the coordinating product layer.

**The Credit Authority API** is the ledger's own API for the tenant's ledger, exposed through declarative routing and network policy. No custom API service is built in front of it unless a required contract cannot be met that way. The platform offers credit primitives only; it offers no general billing API, and no billing or payment provider is integrated in the platform.

### 5. Availability and lifecycle

- The platform owns the ledger's deployment, database, backup, restore, version and schema migrations.
- While the ledger is unavailable, the coordinating layer authorises no credit movement. After an outage, an operation whose outcome is unknown is resolved by its idempotency key or by reading its transaction.
- A version change preserves every transaction, balance and idempotency key.

### 6. The boundary with the coordinating product layer

| Concern | Owner |
|---|---|
| Ledger deployment, database, backup, restore and upgrades | Platform |
| Tenant isolation, ledger access and ledger credentials | Platform |
| Atomicity, overdraft prevention, idempotency and serialisable balances | Platform, through the ledger |
| Pricing models, prices and credit policies | Product layer, for its applications |
| Which movements are posted, and their idempotency keys | Product layer |
| Orchestration of grants, reservations, settlement, expiry and recovery | Product layer |
| Funding accounts, memberships and authorisation of spenders | Product layer |
| Monetary billing and payment collection | External providers, integrated by the product layer |
| Reconciliation of provider facts with credit movements | Product layer |

### Alternatives considered

- **TigerBeetle.** Native pending transfers with timeouts, but a new non-PostgreSQL stateful system, and no history queries for per-member usage. Retained as the alternative if the selected ledger becomes ineligible.
- **A custom PostgreSQL ledger.** It would reimplement double entry, overdraft prevention and idempotency that the selected ledger provides.
- **A billing provider's wallets or credits as the authority.** No synchronous reservation under concurrency, and balances would migrate with any change of provider.
- **Formance Ledger v3.** A release candidate on embedded storage, with no licence file.
- **Authorisation inside the ledger.** It has none to configure. Isolation is enforced by routing and network policy in front of it.

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Credit balances, reservations and transactions | Formance Ledger (the tenant's ledger) | Platform (capability); product layer (movements) | Formance Ledger | Coordinating product layer | Day-1+ |
| A tenant's ledger and bucket | Formance Ledger | Platform | Platform | Coordinating product layer | Day-1+ |
| The ledger's database | CloudNativePG | Platform | CloudNativePG operator | Formance Ledger | Day-1+ |
| Ledger access routes and network policy | Git | Platform | ArgoCD | Gateway, CNI | Day-1+ |
| Ledger caller credentials | Infisical | Platform | ESO | Coordinating product layer | Day-1+ |

## Consequences

### Positive

- Concurrent spending cannot exceed a balance, and double entry, overdraft prevention and idempotency are properties of an open-source ledger rather than platform or product code.
- Correctness does not depend on the coordinating layer running each step exactly once.
- Credit balances and history are independent of billing and payment providers, and survive a change of either.
- The ledger runs on the PostgreSQL the platform already operates, under MIT.

### Negative

- A stateful financial component joins the box, needing backup, restore and upgrade discipline.
- The ledger's lack of authorisation puts tenant isolation on routing and network policy. A route or policy fault exposes system routes or another tenant's ledger.
- Credit movements are only as correct as the product layer's choice of movements and keys. The platform guarantees how a movement executes, not whether it should.

## Impact

- **`manifests/architecture/components.yaml`** — a `credits` capability is declared, `status: planned`, `declared_by` this ADR.
- **ADR-098** — not changed by this ADR. Where infrastructure usage is recorded is decided separately.

## References

- ADR-003: ESO–Infisical Secret Management
- ADR-039: Platform Ownership Model
- ADR-040: Day-0 vs Day-1 Lifecycle Boundary
- ADR-043: Control Plane Authority Model
- ADR-066: The Platform Boundary
- ADR-097: An application calls another application as itself, with client credentials
- ADR-099: The platform provisions a database, not a schema
- `reference-projects/ledger` (tag `v2.4.16`)

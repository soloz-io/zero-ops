# ADR-109: The Credit Authority

**Date:** 2026-10-26
**Status:** Accepted — the capability is not offered to any tenant until the isolation proof in Decision §7 passes against a deployed box.

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
- **Formance Ledger v2.4.16 routes the raw request path and listens on a configurable address.** No path-cleaning middleware is mounted (`internal/api`, `cmd`), and `--bind` sets the listen address (`cmd/serve.go:208`). Besides the ledger-scoped routes, the server answers system routes (`/_`), ledger listing and creation, ledger metadata, bulk, logs import and export, schemas, pipelines, queries, transaction revert and metadata writes, and the v1 API.
- **AgentGateway v1.6.0 is the tenant gateway already deployed** (`universal-tenant` chart). Verified at the `v1.6.0` source:
  - JWT authentication has a strict mode that refuses a request without a valid token (`http/jwt.rs:582`);
  - authorisation rules are CEL expressions over the request and the validated JWT claims, in `allow`, `require` and `deny` modes. Every `require` must hold; when `allow` rules exist and none matches, the request is denied (`http/authorization.rs:254`);
  - an expression that fails to evaluate counts as false (`cel/types.rs:796`), so a failing `require` or `allow` denies;
  - `request.path` is the raw URI path, without the query and without decoding (`cel/types.rs:1157`).
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

**One Credit Authority per tenant.** Each tenant given the capability gets its own ledger deployment, in the tenant's namespace, on its own database. It holds one bucket and one ledger, created by the platform, never by a caller. No other tenant's ledger is reachable from it.

**The gateway is the only way in.** The ledger runs in one pod with an AgentGateway sidecar:
- the ledger listens on the pod's loopback address only, so nothing outside the pod reaches it except through the sidecar;
- the sidecar's listener is the pod's only exposed port;
- network policy admits only the tenant's coordinating service to that port. It is defence in depth; the credential decides who is calling (ADR-097 invariant 10).

**The sidecar authenticates and authorises every request, declaratively:**
- **Authentication, strict.** A request without a valid token from the platform's issuer is refused.
- **Caller binding**, as `require` rules:
  - the token's client identity is the tenant's coordinating service's machine client, rendered from its declaration (ADR-097);
  - the token's tenant claim is this tenant;
  - the token is an access token, never an ID token (ADR-095).
- **Operation allowlist**, as `allow` rules over the method and the raw path, each anchored at both ends, with the tenant's ledger name fixed and path segments restricted to letters, digits, `_`, `-` and, for account addresses, `:`. A path containing `.`, `%` or an empty segment therefore never matches. Only these are allowed:
  - create a transaction; list transactions; read one transaction;
  - list accounts; read one account;
  - read aggregated balances; read volumes.
- **Everything else is denied** because it matches no `allow` rule, including:
  - every system route: bucket deletion and restore, exporter management, `/_info`;
  - listing and creating ledgers, and ledger metadata;
  - bulk, logs import and export, schemas, pipelines and queries;
  - transaction revert, transaction and account metadata writes;
  - the whole v1 API.
- **Release is never a revert** (it is a new transaction), so revert is not needed and stays closed.

The sidecar bounds which operations a caller can perform. Which credit movements are correct is the coordinating layer's decision (§6).

**The Credit Authority API** is the ledger's own API, as narrowed by this allowlist. No custom API service is built in front of it unless a required contract cannot be met this way. The platform offers credit primitives only; it offers no general billing API, and no billing or payment provider is integrated in the platform.

**Applications never reach the ledger.** Consumer applications reach credits only through the coordinating product layer.

**The Credit Authority is a target like an application.** The coordinating service declares it as a backend dependency. The platform renders its machine credential and the sidecar's caller binding from that declaration (ADR-097).

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

### 7. The isolation proof

Isolation is proven, not inferred from configuration. Two checks, both named for this ADR:

**At render time** (`scripts/validate/preflight/109-credit-authority-isolation.sh`), on the rendered manifests:
- the ledger binds the loopback address, and the sidecar's port is the pod's only exposed port;
- JWT authentication is strict, and the caller binding is expressed as `require` rules;
- the authorisation uses `allow` and `require` rules only, never `deny`, since a failing `deny` expression admits;
- every `allow` rule is anchored, names the tenant's ledger, and restricts its path characters as §4 states;
- network policy selects the ledger pod.

**Against a deployed box**, a conformance probe through the sidecar, with each outcome asserted:

| Request | Expected |
|---|---|
| No token; an ID token; an expired token | Refused |
| Another application's machine token; a token with another tenant's claim | Refused |
| Each system route, ledger listing and creation, ledger metadata | Refused |
| Bulk, logs, schemas, pipelines, queries, revert, metadata writes, the v1 API | Refused |
| Dot-segment, percent-encoded and double-slash variants of an allowed path reaching another route | Refused |
| Another ledger's name in an allowed path | Refused |
| A connection to the ledger's port from inside the pod network, bypassing the sidecar | No connection |
| A connection to the sidecar from a pod other than the coordinating service | No connection |
| Each allowed operation with the coordinating service's token | Succeeds |

The capability is offered to a tenant only after both pass. A change to the ledger version, the gateway version or the allowlist runs both again.

### Alternatives considered

- **A shared ledger deployment with one bucket per tenant.** The ledger has no per-ledger authorisation, so isolation between tenants would rest wholly on path rules in a shared gateway. One deployment per tenant makes another tenant's ledger unreachable rather than forbidden.
- **A custom API service in front of the ledger.** The deployed gateway expresses the caller binding and the allowlist declaratively, and fails closed.
- **Cilium L7 policy as the enforcement point.** It can restrict methods and paths, but not by the caller's token claims, and its source is not vendored to verify its matching at the deployed version.
- **TigerBeetle.** Native pending transfers with timeouts, but a new non-PostgreSQL stateful system, and no history queries for per-member usage. Retained as the alternative if the selected ledger becomes ineligible.
- **A custom PostgreSQL ledger.** It would reimplement double entry, overdraft prevention and idempotency that the selected ledger provides.
- **A billing provider's wallets or credits as the authority.** No synchronous reservation under concurrency, and balances would migrate with any change of provider.
- **Formance Ledger v3.** A release candidate on embedded storage, with no licence file.
- **Authorisation inside the ledger.** It has none to configure. Isolation is enforced by the gateway sidecar in front of it.

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Credit balances, reservations and transactions | Formance Ledger (the tenant's ledger) | Platform (capability); product layer (movements) | Formance Ledger | Coordinating product layer | Day-1+ |
| A tenant's ledger deployment, bucket and ledger | Git (the tenant's platform chart) | Platform | ArgoCD | Coordinating product layer | Day-1+ |
| The ledger's database | CloudNativePG | Platform | CloudNativePG operator | Formance Ledger | Day-1+ |
| Sidecar authentication, caller binding and operation allowlist; network policy | Git (rendered from the coordinating service's declaration) | Platform | ArgoCD | AgentGateway sidecar, CNI | Day-1+ |
| Ledger caller credentials | Infisical | Platform | ESO | Coordinating product layer | Day-1+ |

## Consequences

### Positive

- Concurrent spending cannot exceed a balance, and double entry, overdraft prevention and idempotency are properties of an open-source ledger rather than platform or product code.
- Correctness does not depend on the coordinating layer running each step exactly once.
- Credit balances and history are independent of billing and payment providers, and survive a change of either.
- The ledger runs on the PostgreSQL the platform already operates, under MIT.

### Negative

- A stateful financial component joins the box, needing backup, restore and upgrade discipline.
- The ledger's lack of authorisation puts its protection on the sidecar's rules. A fault in them exposes the tenant's own system routes, which is why the isolation proof gates the capability and reruns on every change.
- One ledger deployment and database per tenant, rather than one shared.
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
- ADR-095: Moving the API credential from the ID token to an access token
- ADR-097: An application calls another application as itself, with client credentials
- `reference-projects/agentgateway` (tag `v1.6.0`)
- ADR-099: The platform provisions a database, not a schema
- `reference-projects/ledger` (tag `v2.4.16`)

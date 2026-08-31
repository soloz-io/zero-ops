# ADR-057: Tenant User Management

**Date:** 2026-08-31
**Status:** Accepted

## Context

The platform provisions every tenant database from a common baseline, and that baseline has always included a table of users and a table of identities linking each user to an external identity provider, together with row-level security policies restricting access to the acting user's own rows. The identity table exists precisely to hold the mapping between a provider's subject and a tenant-local user.

Nothing populated either table. Tenants authenticate through the platform's identity provider (ADR-050), receive a validated token carrying a subject, and then store their own records against whatever identifier is convenient — in the first tenant, a free-text column with no constraint and no reference to the user table. Three distinct notions of identity therefore coexist with no mapping between them: the provider's subject, the tenant-local user, and whatever a tenant records as an owner.

The row-level security policies read a tenant-local user identifier from a database setting that nothing sets. A policy whose predicate reads an absent setting matches no rows, so the isolation those policies describe is not merely unused — it is inert, while appearing in the schema as though it were enforced.

Two properties of the identifiers involved make this more than an unfinished feature. The provider's subject and the tenant-local user identifier are both opaque values of the same shape for the same person, so treating one as the other is easy and produces no immediate error. And the obvious way to link them without the identity table — matching on email address — is wrong in a way that fails silently: an address is mutable at the provider and can be reassigned between people, so a match on it merges two accounts the moment an address changes hands, disclosing one person's records to another.

The platform already exposes an administrative interface for managing identities at the provider, used for tenant administration from the platform console. It operates against the identity provider on the hub. It is not a substitute for the mapping described here: it manages who exists, not which tenant-local record a request acts as, and consulting it on a request path would place a cross-site call between the two clusters ADR-046 separates.

Tenants also differ in where their database access lives. In the first tenant the application boundary that owns authentication holds no database connection at all, by its own architectural decision, while the service that owns the database holds no notion of the acting user. Any resolution that assumes both live together will be wrong for that tenant and probably others.

## Decision

### The platform owns the mapping; tenants consume it

Resolving a provider subject to a tenant-local user is platform behaviour, delivered as a library, not something each tenant implements against the baseline schema.

The alternative is that every tenant writes the same small amount of logic independently, and each independently encounters the email-matching trap, the concurrent-first-request race, and the pooling hazard described below. Those are not difficult problems once known, and they are entirely invisible until they cause a disclosure or a data-loss incident. A tenant should inherit the correct behaviour rather than rediscover it.

It is delivered as a library rather than a service deliberately. The mapping is a lookup in the tenant's own database; interposing a network call to read a row the caller already has a connection to would add a dependency on a remote component to every authenticated request, and on this topology that remote component is across the link between sites.

### The subject is the join key; the email address never is

A tenant-local user is found by the identity provider's subject, recorded against the provider that issued it. An address may be stored and refreshed for display, but it is a property of a user and never a way to find one.

This is stated as a rule rather than left to judgement because the failure it prevents is silent and severe: matching on an address merges two people into one record when an address is reassigned, and the result is one person reading another's data with nothing reporting an error.

Recording the provider alongside the subject is equally deliberate. Subjects are unique within an issuer, not across issuers, so a tenant federating a second provider must not allow one subject to resolve to a user established by another.

### A user is provisioned on first authenticated request

A tenant-local user is created the first time an authenticated subject is seen, not by an administrative act in advance. The identity provider is the authority on who may authenticate; the tenant's database holds what that person owns. Requiring a separate provisioning step ahead of first use would place an administrative gate in front of authentication that the platform's own authorisation model does not otherwise impose.

Provisioning a user and linking the identity is one atomic operation. Partial completion leaves a user record that no identity refers to — invisible to every subsequent lookup, recreated on every subsequent request, and accumulating.

Two requests from the same person arriving together will both observe no user and both attempt to create one. Correctness under that race is a property of the uniqueness constraint on the identity, not of ordering: the request that loses adopts the winner's user rather than its own, because returning its own would hand out a user identifier that no identity refers to.

### Row-level security context is transaction-scoped

The setting the baseline policies read is established for the duration of a transaction and no longer.

This is not a stylistic preference. Tenants reach the database through a connection pool operating in transaction mode, where a server connection is handed to a different client the instant a transaction completes. A setting established for the session would outlive the request that set it and be inherited by whichever request borrows that connection next, and every policy would then evaluate against another person's identity. Transaction scoping is what makes pooled access safe, and a session-scoped equivalent is a cross-tenant disclosure rather than a lesser form of the same thing.

The setting is applied as a bound value rather than composed into a statement, because the mechanism that establishes a session variable directly does not accept bound values and composing one would place externally-derived text into a statement.

### Resolution belongs where the database connection is

The mapping is performed by whichever part of a tenant holds the database connection and owns primary keys, which is not necessarily the part that authenticates.

Where a tenant separates an authentication boundary from a data-owning service, the authenticating boundary forwards the identity it has validated and the data-owning service performs the resolution. This follows from the tenant's own architectural constraint that the aggregation boundary carries no database access and does not generate primary keys, and it avoids giving that boundary a database connection solely to write one row.

Forwarded identity is trusted only where two conditions hold together: the caller has proved which service it is, and the network permits no other caller to reach the recipient. Neither is sufficient alone. A boundary reachable from outside the cluster must validate the token itself and must not accept an asserted identity, which is the rule ADR-050 already establishes for the internet-facing boundary.

### The mapping is resolved per request and is not cached

Each authenticated request resolves the acting user by reading the identity row. No cache stands in front of that read.

This is a decision rather than an omission, and it is worth stating because the platform already operates a store that looks like the obvious place to put one. That store holds delegated credentials on a user's behalf, along with pending authorisation-flow state and a lock preventing concurrent refreshes. It is keyed by subject, which makes it appear suited to holding the mapping too.

It is not, and the reason is lifetime. A credential expires, and its store is built around that: entries carry the remaining validity of what they hold and disappear when it lapses. The mapping between a subject and a tenant-local user is permanent until the user is removed. Storing a permanent fact in a structure whose expiry is dictated by credential lifetime means it evaporates on every token refresh and is re-derived for no reason, while still being stale in the one case that matters — a user removed while an entry survives.

The read itself is a single indexed lookup on a connection the caller already holds, in a transaction the request needs regardless. A cache in front of it saves less than its invalidation costs.

Where the cost of the read does become material, the remedy is to resolve within the same transaction that performs the request's work, rather than in one of its own. That removes the additional round trip outright and is also stricter: the user is then guaranteed to exist for the duration of the work referencing it. A cache should be considered only after that, must live with the component that owns the mapping rather than the one that authenticates, and must hold the identifier alone — never an address or a role, both of which change and would go stale invisibly.

### Identity is ambient within a request, not passed between functions

The acting user is established once per request and made available for its duration, rather than threaded through the parameters of everything the request goes on to call.

The reason is the failure mode of the alternative. A parameter that a call site omits does not produce an error: the call proceeds without an acting user, the work is recorded unattributed, and the omission is discovered much later as data with no owner. Since the identity applies to everything a request does, making it a property of the request rather than an argument to each step removes the opportunity to forget.

The scope is exactly one request. Nothing survives its completion, which is what distinguishes this from the cache rejected above.

### Absent identity is permitted; unattributed writes are not

A request carrying no acting user is not an error. Background work, scheduled jobs and schema migration all operate without one, and failing such requests closed would make user resolution a dependency of machinery that has no user.

Operations that record ownership are different: they require an acting user and fail explicitly when none is present, at the point where the requirement exists rather than at the boundary. This keeps the requirement visible in the operation that has it, instead of implied by a middleware that would have to guess.

### Administration is separate from resolution

Managing which people exist within a tenant — invitation, listing, removal — is administrative work performed against the identity provider through the platform's existing administrative interface. It is not part of resolving the acting user on a request, and the two must not be conflated: one is an infrequent operation performed by an administrator against the hub, the other is a lookup performed on every authenticated request against the tenant's own database.

### The path an acting user takes

Each stage names what the platform supplies. A tenant consumes these rather than
reimplementing them; anything a tenant writes at a stage that already has an
abstraction is a divergence to be justified, not a default.

```
                                          PLATFORM SUPPLIES
IDENTITY
  browser --> gateway --> identity provider
                                          gateway OIDC policy: the browser
                              |             login flow and session cookie.
                              v             Tenants configure, never implement.
                        validated token
                        subject, email, tenant
                              |
       +----------------------+
       |
       v
  AUTHENTICATING BOUNDARY                 zero-ops-auth
    holds no database connection            JwtValidator, JwksCache
    validates the token itself;             claimsFromPayload
    never trusts an asserted identity,      authMiddleware, getPrincipal
    being reachable from outside            typed auth errors
       |
       |                                  zero-ops-auth
       |  forwards validated identity       OidcClient, RedisTokenStore
       |  trusted downstream ONLY because   generateOAuthState, hashState
       |   (a) a service credential proves    — for a tenant that also needs
       |       which caller this is, and       delegated tokens onward
       |   (b) the network permits no
       |       other caller to reach it
       |  Both are load-bearing. Either
       |  alone is impersonation.
       v
  DATA-OWNING SERVICE                     zero-ops-auth
    holds the connection,                   resolveUser
    owns primary keys                       SqlExecutor / SqlTransactor
       |                                    — driver-agnostic: the tenant
       +--> RESOLUTION (one transaction)      adapts whatever client it has
       |      look up identity by
       |        (provider, subject)        tenant baseline migration
       |      found     -> user id           users, identities, sessions
       |      not found -> create user +     and their isolation policies,
       |                   link identity,    applied to every tenant database
       |                   atomically
       |
       |      A partial completion leaves a user no identity refers to:
       |      invisible to lookup, recreated on every later request.
       |
       |      Two concurrent first requests both find nothing and both
       |      insert. The uniqueness constraint decides; the loser adopts
       |      the winner's user, never its own — returning its own would
       |      hand out a user id no identity refers to.
       |
       |      The join key is the SUBJECT. Never the email address: an
       |      address is mutable and reassignable, so matching on it merges
       |      two people the moment one changes hands, and nothing errors.
       v
  QUERY                                   zero-ops-auth
    security context,                       withUserContext
    transaction-scoped                      — sets the acting user for THIS
       |                                      transaction and no longer
       |  Session scope would be a
       |  disclosure, not a lesser form    zero-ops-auth
       |  of the same thing: the pool        requireTenantBoundary, requireRole
       |  hands this connection to           — authorisation once identity
       |  another request the instant          is established
       |  the transaction ends.
       v
     tenant tables, isolated to the acting user


IDENTIFIERS                     three values, two of them for one person
  subject          opaque, issued by the provider, meaningful only to it
  tenant user id   opaque, owned by the tenant, what every row references
  provider name    recorded alongside the subject, because subjects are
                   unique within an issuer and not across issuers

  The first two are the same shape for the same person. Treating one as
  the other produces no immediate error, which is why the mapping is a
  stored row rather than a convention.


ADMINISTRATION                              deliberately not on this path
  console --> platform control-plane API --> identity provider
    who exists, invitation, removal — infrequent, against the hub
    never consulted per request: it answers a different question, and
    doing so would place a cross-site call on every authenticated request
```

What a tenant still writes: the adapter binding its database client to the
executor interface, the forwarding of identity between its own boundaries where
they are separate, and whatever isolation it chooses for tables of its own. The
first is a few lines, the second is two headers and a trust rule, and the third
is a decision the platform cannot make on the tenant's behalf.

### Alternatives considered

Requiring each tenant to implement resolution against the baseline schema was rejected. The schema alone does not convey which column is the join key, that the operation must be atomic, or that the security context must be transaction-scoped, and each of those failures is silent.

Providing resolution as a platform service was rejected. It would add a remote dependency to every authenticated request in order to read a row in the caller's own database, and on this topology that request would cross between sites.

Reusing the platform's administrative identity interface for per-request resolution was rejected. It manages provider identities on the hub and has no notion of a tenant-local record, so it answers a different question, and consulting it per request would place a cross-site call on the request path.

Holding the mapping in the platform's existing credential store was rejected. It is keyed by subject and already reachable from the authenticating boundary, which makes it look suited to the purpose, but its entries are scoped to the lifetime of a credential. A mapping that is permanent until a user is removed would be discarded on every credential refresh and retained in the one case where it is wrong.

Treating the provider's subject as the tenant-local identifier directly, removing the mapping, was rejected. It would make the tenant's records depend on an identifier owned by an external system, so a change of provider or of subject format would rewrite every referencing row, and the baseline's existing policies expect a local identifier.

## Ownership

Per ADR-039.

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Tenant user and identity schema | Git | platform | tenant baseline migration | tenant services | Day-1+ |
| Resolution and security-context behaviour | Git | platform | platform auth library | tenant services | Day-1+ |
| Tenant user records | tenant database | tenant | tenant data-owning service | tenant services | Day-1+ |
| Provider identities | identity provider | platform | platform administrative interface | platform console | Day-1+ |

## Consequences

### Positive

- The row-level security the baseline has always described becomes enforceable, rather than present in the schema and inert in operation.
- The email-matching trap, the first-request race and the connection-pooling hazard are addressed once, in one place, rather than encountered independently by each tenant.
- Tenant records reference an identifier the tenant owns, so a change of identity provider or subject format does not rewrite referencing rows.
- Resolution is a query against a connection the caller already holds, adding no remote dependency and no cross-site traffic to the request path.
- Placing resolution with the database connection keeps the tenant's own separation of concerns intact, rather than requiring an aggregation boundary to acquire database access.

### Negative

- A tenant whose authenticating boundary is separate from its data-owning service must forward identity between them, and that forwarding is trusted on the strength of a service credential and a network restriction. Both are load-bearing; relaxing either makes an asserted identity sufficient to impersonate any user.
- Provisioning on first request means the first authenticated request from a new person performs writes, so it is measurably slower than subsequent ones and can fail for reasons unrelated to authentication.
- Every authenticated request performs a read to resolve the acting user. The read is indexed and shares the request's own transaction where the two are merged, but it is a cost paid on each request rather than amortised, and it is accepted in exchange for having no cache to invalidate.
- The library must be versioned and adopted by each tenant, so a correction does not reach tenants until they upgrade.
- Existing tenant records that identify owners by some other value require migration, and where that value cannot be resolved to a subject the ownership cannot be recovered automatically.
- Nothing detects a tenant that consumes the baseline schema without applying the security context. Such a tenant appears to have row-level isolation and does not have it.

## Impact

- **Extends the tenant baseline's role.** The baseline's user and identity tables become part of the platform's contract with tenants rather than unused scaffolding.
- **Amends ADR-050.** That decision establishes how a tenant authenticates and where identity is validated; this one establishes what the validated identity resolves to inside the tenant's own data.
- **Requires the platform auth library to be versioned and published** before a tenant can adopt the behaviour, so tenant adoption follows a release rather than a commit.
- **Requires tenants recording ownership by another value to migrate**, including constraining such columns to reference the user table.
- **Leaves administration where it is.** The platform's administrative identity interface remains the surface for managing who exists within a tenant, and is not placed on any request path.
- **Does not by itself enforce isolation on tenant-defined tables.** The baseline's policies protect the baseline's tables; a tenant's own tables carry whatever isolation that tenant defines, and adopting the same pattern there is a separate decision per tenant.

## References

- ADR-003: Secret Management Architecture
- ADR-039: Platform Ownership Model
- ADR-040: Day-0 vs Day-1 Lifecycle Boundary
- ADR-041: Controller Responsibility Matrix
- ADR-043: Control Plane Authority Model
- ADR-046: Hybrid Topology
- ADR-047: Fleet Tenant Deployment Contract
- ADR-050: Tenant Authentication via AgentGateway
- ADR-053: Tenant OAuth Confidential Client Lifecycle

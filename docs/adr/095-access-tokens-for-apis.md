# ADR-095: Moving the API credential from the ID token to an access token

**Date:** 2026-09-28
**Revised:** 2026-09-29 -- the gateway's policy surface was read rather than
inferred, and the reading changed the decision. What is corrected is marked in
place; the first draft's reasoning is not silently replaced.
**Status:** Proposed
**Implements:** ADR-094 invariant 1
**Relates to:** ADR-050 (identity is validated at the boundary that uses it)

## Context

ADR-094 requires that APIs accept access tokens and never ID tokens. The platform
does the opposite today. `agentgateway.yaml`:

```
# Forward the ID token itself, not merely claims derived from it.
Authorization: '"Bearer " + jwt.rawToken.unredacted()'
```

and every workload validates `aud == OIDC_CLIENT_ID`, which is exactly an ID
token's audience.

**Why it is not merely incorrect.** An ID token's `aud` is the client id. The
cross-application mechanism ADR-094 adopts puts *project* ids into an ACCESS
token's `aud`. So while workloads are handed ID tokens there is no audience for a
sibling application to be granted, and ADR-094's cross-application call cannot be
built at all. This is a precondition, not hygiene.

### What is already true, and removes most of the difficulty

Established against the deployed instance, not assumed:

```
accessTokenType          OIDC_TOKEN_TYPE_JWT     on every app in every org
accessTokenRoleAssertion true                    on every app
```

So access tokens are **JWTs**, validated locally against the same JWKS by the same
validator, and they already carry `urn:zitadel:iam:org:project:roles`. No
introspection endpoint, no second network dependency on the request path, and the
platform's role-based authorisation keeps working unchanged.

### What the gateway actually does with the tokens, read rather than assumed

This was left open in the first draft and is now settled against
`reference-projects/agentgateway` at the vendored revision. It changes the
decision, so the evidence is recorded here rather than summarised.

**The access token from the login is discarded.** `handle_callback` exchanges the
code, takes `token.id_token`, validates it, and builds the session from that alone
(`http/oidc/callback.rs`). `BrowserSession` has exactly three fields —
`policy_id`, `raw_id_token`, `expires_at_unix` (`http/oidc/session.rs`) — and
upstream says so in the code:

```
// TODO: Revisit whether browser sessions should persist access_token / refresh_token.
// The current stateless cookie only stores the validated id_token because that is what
// the runtime uses today, and larger token payloads can exceed browser cookie limits.
```

So the absence is not in the expression layer, which is what the first draft
inferred from `schema/cel.json`. It is in the session: **a gateway-mediated browser
login has no access token to forward, by any mechanism.** The first draft's
"not a one-word change to the transformation" was right about the outcome and
wrong about the reason.

**There IS a native forwarding surface, and the first draft missed it.**
`BackendAuth::passthrough` — *"Forward the validated incoming JWT to the backend"* —
takes an optional `location` defaulting to `Authorization: Bearer `. Its
implementation reads the `Claims` extension and re-inserts `claims.jwt`
(`http/auth/mod.rs`). So the CEL line in `agentgateway.yaml` is reproducing
declaratively-available behaviour, and can be deleted whatever else is decided.

**Validation and propagation are already separate concepts, in the JWT policy.**
`LocalJwtConfig.preserveToken` defaults to **false**: a validated credential is
*stripped* from the request unless propagation is asked for, and putting it back on
the backend leg is a different policy at a different attachment point. That is
exactly the separation this ADR wants. The **OIDC** policy has no equivalent knob
and no propagation surface at all — it contributes `Claims` to request extensions
and nothing else.

**There is one credential slot, and order decides who fills it.** Both policies
insert the same `Claims` type, which holds one `jwt: SecretString`. The proxy
applies `oidc` then `jwt` (`proxy/httpproxy.rs`), so a JWT policy's claims overwrite
an OIDC policy's. There is no "access token or ID token" switch: the selection is
*which policy last validated something*, and that is not configurable.

Consequence, stated plainly: **`passthrough` on today's deployment would forward
the ID token** — the same credential the CEL line forwards, and the one this ADR
exists to stop sending. `passthrough` is a real capability, and on its own it does
not solve this; option B below is the shape in which it does.

## Decision

### The receiver contract changes, and it changes once

```
                        today                    after
credential              id_token                 access token (JWT)
aud                     == OIDC_CLIENT_ID        contains <project id>
roles                   from the id_token        from the access token (already asserted)
tenant                  claims.ts priority list  unchanged
azp                     present                  present, and now required (ADR-094)
```

`packages/auth` gains the project id as a required audience and stops accepting the
client id. The tenant resolution in `claims.ts` is untouched: it never read the
audience, which is why ADR-094's third invariant was already satisfied.

### Getting an access token to the workload

The first draft ranked three options by cost, preferring the cheapest that worked.
That ranking does not survive the reading above, because **the two gateway options
are not alternatives for the same call.** They differ in who holds the access token,
and that difference decides which one the cross-application case can also use.

**A. The gateway exchanges on the backend leg. This is the decision.**

The browser session stays as it is. The BFF backend gains an `auth` policy — a
`BackendAuth`, attached per backend, on the outbound leg — that performs the RFC 8693
exchange and presents the result:

```
browser --cookie--> gateway --(exchange: id_token -> access token, aud = <bff project>)--> BFF
```

`CrossAppAccessAuth.subjectToken` documents its default as *"an OpenID Connect ID
token read from the Authorization Bearer header"*, which is precisely what a
gateway-mediated session has and all it has. `audience` names the resource
authorisation server and the issued assertion is bound to it; `scopes`,
`accessTokenScopes` and `resources` are per backend; a response cache with a 300s
default TTL keeps the exchange off most request paths. Zitadel implements token
exchange (`internal/api/oidc/token_exchange.go`).

The `Authorization:` CEL line is deleted. No application code changes. Nothing
holds a raw browser token in an expression.

**Why this and not the cheaper-looking option: it is the same mechanism ADR-094's
cross-application call needs.** Under A, oranger calling waypoint is another backend
with another `audience` — configuration, not code, and not a second mechanism. The
first draft calling the exchange "heavier" was wrong: per-backend `BackendAuth` *is*
the gateway's idiomatic surface for presenting a credential to an upstream, and an
exchange whose result is cached per audience is what that surface is built for. The
cost is a round trip on a cache miss; what it buys is one platform answer to "how
does a credential audienced to this receiver come to exist".

**B. The browser holds the access token.** The SPA runs the OIDC flow itself —
`<tenant>-public-client` is already registered as a public client with PKCE
(ADR-050) — and sends `Authorization: Bearer <access-token>`. The gateway validates
it with `LocalJwtConfig` and forwards it with `BackendAuth::passthrough`; both the
CEL line and the browser session go away.

This is genuinely available and genuinely simpler for one hop. It is not chosen
because the token the browser holds is audienced to *its* project, so a
cross-application call still needs an exchange — leaving the platform with both
mechanisms and a session model rewritten to get there. B is the right shape if the
browser session is ever removed for other reasons; it is not a reason to remove it.

**C. The receiving application exchanges.** Unchanged from the first draft, and now
clearly the fallback it always was: it puts the exchange on every request path in
every application, which is the duplication ADR-057 refused for user resolution.
Reachable without any gateway change, and that is its only merit.

**No option involving "trust the ID token but more carefully" is acceptable**, and
that includes forwarding claims in `x-auth-*` headers: ADR-050 already refuses
derived headers as an identity source, and the gateway comment says so.

### The migration has a dual-accept window, and it is not optional

Every tenant workload validates the audience. Changing what the gateway sends and
what receivers require at the same instant is a flag day across every application
on every box, and today's ADR-093 cutover demonstrated what the gap between two
halves of a migration costs when they are assumed simultaneous.

```
1. receivers accept EITHER  aud == client_id  OR  aud contains project id
   deployed everywhere, verified, no behaviour change yet
2. the gateway switches to the access token
   receivers already accept it; nothing to coordinate
3. receivers drop the client_id branch
   the audience is now the project, and only the project
```

Step 1 is the safety. A receiver that reaches step 3 before step 2 rejects every
live request; a gateway that reaches step 2 before step 1 is deployed everywhere
does the same. The order is the design.

**Step 3 is not optional bookkeeping**, for the same reason the CNPG settle was
not (ADR-092): a dual-accept receiver left in place indefinitely accepts an ID
token forever, which is the state this ADR exists to end. It needs a check that
fails while any receiver still accepts a client-id audience.

### Sequencing with ADR-094

This ships **with** the per-application projects, not before. Until an application
has its own project there is no project id for a receiver to require, and the only
available audience is the shared `platform` project -- which would make every
receiver accept every application's token, the exact collapse ADR-094 removes.

## Consequences

- One credential type on the request path, with an audience that names a resource
  rather than a client.
- ADR-094's cross-application call becomes constructible. It is not, today.
- The platform gains RFC 8693 as a **gateway** capability, configured per backend,
  and the cross-application case needs no second mechanism and no application code.
- The `Authorization:` CEL expression is deleted. No policy holds a raw browser
  token in an expression any more, which was the reviewer's condition.
- A cache miss on the exchange adds a round trip to the identity provider on the
  first request per audience per TTL. This is the price of A over B, stated so it
  is not discovered later.
- The gateway becomes a party to token issuance, not merely validation. Its client
  credentials for the exchange are a new secret on the request path, and they reach
  it the way every other one does (Infisical, ExternalSecrets) — but the blast
  radius of that credential is larger than the public client's, which holds none.
- Every tenant workload changes what it validates. The dual-accept window makes
  that safe; skipping it makes it a flag day.
- Role-based authorisation is unaffected: roles are already asserted into access
  tokens on every app in every org.

## Open

**Whether Zitadel's token-exchange implementation accepts the ID-JAG shape
`CrossAppAccessAuth` sends, or only the plainer `OAuthTokenExchangeAuth` profile.**
Both are `BackendAuth` kinds and the choice between them does not change this
decision — it changes which of two configurations is written. It is settled by one
exchange against the deployed instance, not by reading, because the failure mode is
a provider rejecting a parameter combination rather than anything visible in a
schema. That probe is a prerequisite of implementation, not of this ADR.

**Whether the exchange is attached per backend or once.** Per backend is what the
surface offers and what the cross-application case wants. If several backends share
one audience the configuration repeats, and whether that repetition is worth a
helper is a chart question, deferred until there is more than one.

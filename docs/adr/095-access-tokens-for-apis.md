# ADR-095: Moving the API credential from the ID token to an access token

**Date:** 2026-09-28
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

### What is NOT available, and shapes the design

The gateway's expression context exposes exactly one token:

```
jwt: ["rawToken"]        schema/cel.json
```

`jwt.rawToken` is the token the JWT policy validated. There is no expression for
"the access token from the login". `OAuthTokenType::{AccessToken, IdToken, IdJag}`
exists in the gateway but belongs to RFC 8693 token exchange -- it is the
`subject_token_type` of an exchange, not a switch for what a login forwards.

So "forward the access token instead" is not a one-word change to the
transformation, and any design claiming otherwise has not read the schema.

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

Preferred, in order, because the first that works should be taken:

**1. The gateway forwards the access token.** If the OIDC policy can be configured
so the credential presented downstream is the access token -- either by validating
it in the JWT policy or by a future expression -- this is the whole change, and the
rest of this ADR is migration mechanics. It must be established by reading the
gateway's configuration surface, not hoped for.

**2. The gateway performs an RFC 8693 exchange.** `cross_app_access.rs` implements
the ID-JAG flow: the user's assertion is exchanged at the identity provider, then
at the resource authorisation server, for an access token bound to the target's
audience. Zitadel implements token exchange (`internal/api/oidc/token_exchange.go`).
This is heavier than option 1 and is the same mechanism ADR-094's cross-application
call needs, so choosing it here means ONE mechanism serves both, which is worth a
great deal more than saving a network round trip.

**3. The receiving application exchanges.** The BFF takes the ID token it is given
and exchanges it for an access token audienced to its own project. This works
without any gateway change, and it is the fallback if neither of the above can be
configured. It is last because it puts the exchange on every request path and in
every application, which is the duplication ADR-057 refused for user resolution.

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
- If option 2 is chosen, the platform gains RFC 8693 and the cross-application case
  needs no second mechanism.
- Every tenant workload changes what it validates. The dual-accept window makes
  that safe; skipping it makes it a flag day.
- Role-based authorisation is unaffected: roles are already asserted into access
  tokens on every app in every org.

## Open

**Whether option 1 is configurable.** Settled by reading the gateway's OIDC policy
surface, and the answer decides whether this is a small change or an exchange
implementation. It is left open here rather than guessed, because the schema shows
only `jwt.rawToken` and that is evidence of absence in the expression layer, not in
the policy layer.

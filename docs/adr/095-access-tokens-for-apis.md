# ADR-095: Moving the API credential from the ID token to an access token

**Date:** 2026-09-28
**Revised:** 2026-09-29 -- twice. The gateway's policy surface was read rather
than inferred, and then Zitadel's audience construction was, which DISPROVED the
mechanism the first draft chose: an ID token and an access token carry the same
audience, so no audience check can tell them apart. The invariant is now carried by
`at_hash`. What is corrected is marked in place; the first draft's reasoning is not
silently replaced.
**Status:** Accepted, with its SCOPE corrected 2026-09-29 — the invariant holds
where a token crosses an application boundary; on the same-application path the
gateway forwards its validated session token and ADR-094 invariant 2 (the `azp`
allowlist) is what refuses a sibling's token. See "The exchange cannot serve a
browser session".
**Implements:** ADR-094 invariant 1, which sits in that ADR's **Part 1
(same-application authorisation)**. This ADR decides the CREDENTIAL — what a
receiver is handed and how it proves which kind of token it is. It does not
decide ADR-094 Part 3 (cross-application authorisation), and the exchange working
across applications must not be read as deciding it: a token minted by one
application's gateway for another carries the CALLER's project roles.
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

**Why it is not merely incorrect.** An ID token is a statement *about* an
authentication event, issued to the client that started it. An access token is a
credential *for* a resource. Handing a resource the first and asking it to behave
like the second is the confusion ADR-050 exists to prevent, and it is what the
gateway does today.

The first draft gave a second reason — that an ID token's `aud` is the client id,
so no project audience exists for a sibling application to be granted. **That
reason is false for Zitadel**, and the correction is the substance of this ADR.
See "What Zitadel puts in an audience" below.

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

### What Zitadel puts in an audience, and why it cannot carry this decision

Read in `reference-projects/zitadel`, after the gateway reading below made the
receiver contract the remaining question.

**The ID token and the access token carry the SAME audience.** One slice is built
per session and both tokens are signed with it:

```
auth_request.go  audienceFromProjectID -> append(appIDs, projectID)
                 i.e. EVERY client id in the project, PLUS the project id
                 then AddAudScopeToAudience adds any urn:...:<project>:aud scopes

token.go:54      createIDToken(..., session.Audience, ...)
token.go:135     createJWT     (..., session.Audience, ...)   <- the access token
```

Three consequences, and they run in opposite directions:

1. **`aud == OIDC_CLIENT_ID` already accepts an access token.** The client id is in
   the access token's audience too. So the receivers do not have to change for the
   gateway to switch — the dual-accept window the first draft called mandatory is
   a no-op, and removing it removes a migration step rather than a safety net.

2. **`aud contains <project id>` does not reject an ID token.** The ID token has the
   project id as well. The first draft's endpoint — receivers require the project
   audience — **does not achieve this ADR's own invariant.** It was the mechanism,
   and it does not work.

3. **It does not reject a sibling's ID token either.** ADR-094's cross-application
   call requires oranger's login to request waypoint's project audience, and
   `validateTokenExchangeAudience` requires the subject token to already hold the
   audience being requested. So oranger's *ID token* carries waypoint's project id
   by construction. A waypoint that checks only the audience accepts oranger's raw
   browser token, and the exchange bought nothing at the receiver.

Together with what was already established — `isScopeAllowed` returns true
unconditionally for the project-audience scope prefix, so any client may request
any project's audience — the conclusion is blunt:

> **In Zitadel an audience is a routing hint, not an authorization boundary.**

Anything this platform builds on "the audience names the resource, so the resource
may trust it" is building on that. This ADR stops doing it.

### What does discriminate: a Zitadel compatibility invariant

`at_hash` is set in exactly one place in the whole Zitadel codebase:

```
token.go:106   claims.AccessTokenHash, err = oidc.ClaimHash(accessToken, signAlg)
               ... inside createIDToken, and nowhere else
```

and `createIDToken` is called with a non-empty access token on every token-endpoint
path, so every ID token Zitadel issues carries it. No access-token path sets it, by
construction and by grep.

**This is a compatibility invariant, not an OIDC rule, and the distinction is
load-bearing.** An earlier draft of this section called it "the standard answer
rather than a Zitadel quirk". That is wrong. OIDC Core requires `at_hash` only in
responses that return an access token from the authorization endpoint — the
implicit and hybrid flows. In the authorization-code flow this platform uses, the
specification makes it **OPTIONAL**, so a conforming issuer may omit it and a
receiver relying on the spec alone would accept ID tokens from one.

What is actually relied upon, stated so it can be tested rather than assumed:

> **INVARIANT (Zitadel).** For the Zitadel version this platform pins, every ID
> token reaching a receiver carries `at_hash`, and no JWT access token — from the
> token endpoint or from `createExchangeJWT` — carries one.

Three things follow, and they are the reason to write it this way:

1. **It is version-scoped.** A Zitadel upgrade can break it, and nothing about the
   receiver would say so — requests would simply start succeeding that should not.
   So it is asserted against a real exchange, not only against fixtures, and that
   assertion is a release gate rather than a one-off check.
2. **It does not generalise to another issuer.** If this platform ever supports a
   second one, this rule is re-established for it or replaced, not inherited.
3. **It is not forgeable.** `at_hash` is a signed claim, so removing it from a
   genuine ID token invalidates the signature. The check is sound against a
   hostile caller even though it is narrow against a different issuer.

**A receiver that refuses any token carrying `at_hash` refuses Zitadel ID tokens.**
It is one claim, needs no per-application configuration, cannot drift as projects
are created, and fails closed against exactly the confusion this ADR names.

**Where it is switched OFF, and why that is not a loophole.** A BFF sitting
directly behind its own application's gateway receives that gateway's validated
SESSION token, because the exchange cannot produce a usable one for a browser
session (below). Such a receiver sets `rejectIdTokens: false` and MUST set
`allowedAzp`. The protection does not disappear, it changes hands: what this rule
prevents is an ID token being accepted as a credential for a DIFFERENT
application, and naming the one permitted caller refuses that by name on every
request, rather than by inference from the token's kind. A receiver that turns
this off without an allowlist has no caller control at all, and that combination
is the thing to review for.

### Two constraints on the exchange, before anything is configured

Both would have been found by a failed deploy rather than by reading, so they are
recorded here.

**The exchange must ask for a JWT explicitly.** `createExchangeAccessToken` returns
`op.CreateBearerToken(...)` — an **opaque** token — and it does so regardless of the
application's `accessTokenType=JWT`. Only `createExchangeJWT` signs a JWT, and it is
reached only by `requested_token_type = urn:ietf:params:oauth:token-type:jwt`.
Zitadel maps an absent value to the access-token branch, and agentgateway *omits*
`requestedTokenType` when unset. So the default configuration silently yields an
opaque token, the receiver cannot validate locally, and the platform has bought an
introspection call on every request — the network hop ADR-057 refused.

**`resource` must not be sent.** `tokenExchange` rejects it outright:
`"resource parameter not supported"`. Both agentgateway exchange policies offer a
`resources` field; neither may be populated against Zitadel.

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

### The receiver contract changes, and the change is one claim

```
                        today                    after
credential              id_token                 access token (JWT, from the exchange)
aud                     == OIDC_CLIENT_ID        unchanged -- see below
at_hash                 present, unchecked       MUST BE ABSENT  <- the new rule
roles                   from the id_token        from the access token (already asserted)
tenant                  claims.ts priority list  unchanged
azp                     present                  present, and required (ADR-094)
```

**The audience check does not change, and must not be tightened.** The first draft
would have replaced the client id with the project id. That buys nothing — both
tokens carry both — and it costs a per-application configuration value that has to
be right on every receiver on every box. The audience stays what it is: a check
that the token belongs to this project's world at all.

**`at_hash` absent is the invariant.** `packages/auth` refuses a token carrying it.
That is the whole receiver-side change: one claim, no new configuration, and it
fails closed.

**What this deliberately does not claim.** Refusing `at_hash` stops an ID token
being *mistaken* for an access token. It does not make the audience a boundary, so
it does not on its own stop a sibling application's access token being replayed at
this one. That isolation comes from the exchange being performed by the gateway
with client credentials no browser holds, and from role assertions being
project-scoped — not from `aud`. ADR-094's third invariant is restated on that
basis rather than on the audience, and ADR-094 needs the amendment.

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

### The exchange cannot serve a browser session — read this before re-adding it

**Established from source on 2026-09-29, after the decision below was accepted
and implemented.** The gateway no longer exchanges on the same-application path,
and this section exists so nobody restores it from the reasoning above.

Zitadel derives an exchanged token's claims **entirely from the scopes of the
exchange request**. `createJWT` ends with `claims.Claims = userInfo.Claims`, and
every claim in `userInfoToOIDC` sits behind a `case` on a scope —
`ClaimResourceOwnerID` (the tenant) under `ScopeResourceOwner`, the address under
`oidc.ScopeEmail`.

An ID token used as the subject carries **no scopes**:

```
token_exchange_converter.go   accessToExchangeToken       scopes: token.scope
                              idTokenClaimsToExchangeToken   (no scopes field)
```

So for a gateway-mediated browser session, which holds only the ID token:

```
send no scopes    validateUnionTokenExchangeScopes falls back to
                  subjectScopes, then actorScopes -- both nil -> scopes = []
                  -> the exchanged token has NO tenant claim and NO email
                  -> every request refused as tenant-less, and resolve_user
                     cannot provision a user without an address

send any scope    each requested scope must appear in subject ∪ actor
                  (scopeInUnion), and both are empty
                  -> invalid_scope: "not found in subject or actor token"
```

Both doors are shut. This is not a configuration error and there is no value of
`scopes` that fixes it.

**What the three escapes cost, so they are not rediscovered as cheap:**

- *Browser-direct* — the SPA runs the flow and holds an access token, which does
  carry scopes. It is a client rewrite (the platform's `AuthProvider` is
  cookie-session by construction: "the client neither knows nor duplicates the
  login flow") and it moves tokens out of an httpOnly cookie into browser
  storage. A security downgrade to fix a claims problem.
- *Actor-token path* — Zitadel does support subject-data scopes for a scopeless
  subject, in `validateImpersonationTokenExchangeScopes`. It requires
  `EnableImpersonation()` **instance-wide** and classes every API call as
  impersonation. Enabling impersonation for the whole box to make a
  same-application call work is not a trade this platform should make.
- *Patching agentgateway to keep the access token* — the upstream TODO in
  `callback.rs`. Not available while the platform runs the released image.

**Where the exchange still belongs: the cross-application call.** There the
caller holds an ACCESS token, which carries scopes, so both doors are open. The
exchange client, its confidential grant and its RFC 8693 grant type stay
provisioned for exactly that.

### The migration is two independent steps, and neither needs a window

The first draft required a dual-accept window and called it not optional. The
audience finding removes it. Because an access token's `aud` already contains the
client id every receiver checks today, **the gateway can switch with no receiver
change at all** — same issuer, same JWKS, same audience, roles already asserted.

```
1. the gateway exchanges and forwards an access token
   receivers unchanged. They accept it because they already would.
   verifiable on its own: nothing downstream knows it happened

2. receivers refuse at_hash
   ID tokens are now rejected. Nothing sends one any more, because step 1 landed.
```

The order still matters, in the same direction and for a sharper reason: step 2
before step 1 rejects **every** live request, because every live request carries an
ID token today. What has gone is the coordination *between* them — each step is
safe alone, neither needs the other deployed simultaneously, and step 1 is
observable before step 2 commits to anything.

**Step 2 is not optional bookkeeping**, for the same reason the CNPG settle was not
(ADR-092): stopping after step 1 means nothing prevents an ID token being accepted
again, and the defect this ADR exists to end is a configuration slip away. It needs
a check that fails while any receiver still accepts a token carrying `at_hash`.

### Sequencing with ADR-094

**This no longer waits for the per-application projects.** The first draft coupled
them, because a receiver was going to require a project id that does not exist
until ADR-094 creates one. With the audience check unchanged and `at_hash` carrying
the invariant, both steps above ship against today's shared `platform` project and
improve the position immediately.

ADR-094 is still needed, and this ADR now says something sharper about why. The
per-application projects were justified partly as an isolation boundary — one
project per application so an audience names one receiver. That justification is
weaker than it looked: the audience is not a boundary in Zitadel, so splitting the
projects does not by itself stop a sibling's token being presented. ADR-094 earns
its place on **role scoping** and on having a distinct grant surface per
application, not on the audience. It needs amending to say so, and to stop implying
that the audience enforces anything.

## Consequences

- The credential on the request path is a credential for a resource, not a
  statement about a login, and a receiver can prove which it is holding.
- The `Authorization:` CEL expression is deleted. No policy holds a raw browser
  token in an expression any more, which was the reviewer's condition.
- The platform gains RFC 8693 as a **gateway** capability, configured per backend,
  and the cross-application call needs no second mechanism and no application code.
- **The migration lost a step and gained safety.** Two independent deploys, no
  dual-accept window, no flag day, and step 1 is observable before step 2 commits.
- **A belief the platform held is now known to be false**: that an audience names
  the receiver and may therefore be trusted by it. Every place that reasoned from
  it needs re-reading — ADR-094 first.
- A cache miss on the exchange adds a round trip to the identity provider on the
  first request per audience per TTL.
- The gateway becomes a party to token issuance, not merely validation. Its client
  credentials for the exchange are a new secret on the request path, reaching it
  the way every other one does (Infisical, ExternalSecrets) — but the blast radius
  of that credential is larger than the public client's, which holds none. It is
  also what makes the exchange an isolation boundary at all, since a browser cannot
  perform one.
- Role-based authorisation is unaffected: roles are already asserted into access
  tokens on every app in every org, and `createExchangeJWT` asserts them too.

## Open

**Which exchange profile to configure, `OAuthTokenExchangeAuth` or
`CrossAppAccessAuth`.** Both are `BackendAuth` kinds and both can be made to send
the required `requested_token_type`; `CrossAppAccessAuth` performs the two-legged
ID-JAG flow, which is more than the same-box case needs. Settled by configuring the
simpler one first and only reaching for the other if the cross-application case
needs the second leg.

**Whether Zitadel's instance requires token exchange to be enabled.** No feature
gate appears in `token_exchange.go`, but absence in that file is not proof of
absence in the instance's feature set. One exchange against the deployed instance
answers it, and that probe is a prerequisite of step 1, not of this ADR.

**Whether anything else in the platform trusts an audience.** This ADR found one
place. The finding is general, and the sweep has not been done.

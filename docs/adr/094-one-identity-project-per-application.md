# ADR-094: One identity project per application, and audience is not authorisation

**Date:** 2026-09-28
**Status:** Accepted in part — see Disposition. Parts 1 and 2 are ACCEPTED;
Part 3 (cross-application authorisation) is NOT YET DECIDED.
**Amended:** 2026-09-29 by ADR-095 — the audience is not an isolation boundary in
Zitadel. Invariant 1's mechanism and the Context's security argument are corrected
in place; the decision stands on a narrower claim. Restructured the same day into
the three bands above, because "ADR-094 is accepted" was becoming a sentence that
covered a question it had not answered.
**Amended:** 2026-09-30 after external security review — invariant 2 named the
wrong client. The allowlist holds the caller's CONFIDENTIAL EXCHANGE client, not
its public PKCE client; the correction and both reasons are in Part 2. Review
disposition recorded below.
**Builds on:** ADR-088 (which decided this shape and left it unbuilt)
**Relates to:** ADR-047 (a fleet declares, the platform renders), ADR-050 (where identity is validated), ADR-053 (OAuth clients are fleet-declared), ADR-059 (no provider vocabulary in platform templates)

## Disposition

Three separable questions, answered separately. Read this table first; the body
is organised in the same order and nothing below crosses a band.

| | question it answers | status |
|---|---|---|
| **Part 1 — Same-application authorisation** | may this user do this, in the application that authenticated them | **ACCEPTED** |
| **Part 2 — Cross-application authentication** | is this token for this receiver, and which application is calling | **ACCEPTED** |
| **Part 3 — Cross-application authorisation** | what may this user do in an application that did NOT authenticate them | **NOT YET DECIDED** |

Parts 1 and 2 are implemented and are what this ADR decides. **Part 3 is open**,
and an implementation must not be read as settling it: cross-application
authentication working is not cross-application authorisation working, and the
gap between them is a real one with a named experiment to resolve it.

An earlier revision of this table said Part 2 was ACCEPTED while invariant 2 —
the caller allowlist, which is the *only* control in Part 2 that actually
separates applications — had no implementation anywhere. Provisioning existing is
not the band being done. It is implemented now (`allowedAzp` in
`zero-ops-auth`), and the rule this table is written under is that a band is
ACCEPTED only when its controls exist in code, not when the objects they act on
have been provisioned.

### External security review, 2026-09-30

**ARCHITECTURE APPROVED, with implementation gates** (second pass, same day). The
authentication and data-boundary design is approved; Part 3 authorisation remains
undecided and does **not** block that approval — it blocks any claim that
cross-application *authorisation* is complete.

The review established one thing this ADR had not written down: the *deployed*
inter-service hop is not this design. oranger-bff reaches oranger-sdk over a shared
secret plus trusted identity headers (ADR-057), which carries email, roles and a
caller-asserted tenant. That is an older trust model, and the decision is:

> **Do not retrofit this design onto the shared-secret/header hop. Replace that hop
> as a deliberate architectural change.**

The nine requirements, and where each now stands. Requirements 2–8 were not left
as nine independent decisions: they follow from the security model already chosen,
and the reviewer put them forward as one contract, which is approved as design.

| | requirement | status |
|---|---|---|
| 1 | `OIDC_ALLOWED_AZP` holds the confidential exchange client | **DONE** — invariant 2a; resolver **and call-site** regression tests |
| 2 | private application-to-application network path | **DESIGN APPROVED** — below |
| 3 | waypoint's contract is access-token-only | **DESIGN APPROVED** — ADR-095 |
| 4 | `consumer_app_id` ← validated `azp` | **DESIGN APPROVED** — invariant 2b |
| 5 | `subject` ← validated `sub`, no user lookup | **DESIGN APPROVED** — invariant 2b |
| 6 | tenant ← validated token, never a caller header | **DESIGN APPROVED** — invariant 2b |
| 7 | no email or roles across the boundary | **DESIGN APPROVED** — below |
| 8 | short-lived exchanged token | **DESIGN APPROVED** — ADR-095 |
| 9 | Part 3 authorisation stays separate | **HOLDS** — see Part 3 |

Design approved is not implemented. Nothing in 2–8 is built, and the invariants
above are what an implementation is measured against.

#### 2 — the network path is defence in depth, not the authentication

A cross-application path is created explicitly, never by default:

```
oranger-sdk  --CiliumNetworkPolicy-->  waypoint-sdk
```

with no public hostname, no ingress from browser or user workloads, ingress
restricted to the calling workload's identity, and both halves declared — the
caller's egress and the receiver's ingress. It is stated in this ADR because the
absence of a path is a property a reader can check, and because a stolen bearer
token must not be sufficient from outside the cluster.

It is **not** the authentication mechanism. The token is. A design that relied on
the network for authentication would be one where any workload that got inside the
namespace became the caller.

#### 7 — what does NOT cross the boundary

```
crosses:         tenant_id, azp -> consumer_app_id, sub
does NOT cross:  email, name, the caller's user id, the caller's roles
```

A receiver owning resources keyed by an opaque subject has no business need for a
profile, and copying one across the boundary would make each receiver a partial
directory of the caller's users that nothing keeps current. Adding a field to this
list requires a demonstrated requirement, recorded here.

The caller's roles are excluded for a second reason, which is requirement 9: they
are the caller's authorisation model, and a receiver reading them would be
answering "what may this user do here" with a fact about somewhere else.

Item 9 is explicit: the authentication and data-boundary half is now well enough
defined to keep designing, and the authorisation model must not be smuggled in with
it. No application may declare a `backendDependency` and rely on roles until the
Part 3 experiment has run.

**What `backendDependencies` means, and what it does not.** It means: this
application has a permitted application relationship with that one. It does **not**
mean its users hold any permission there. A token minted under this design
establishes four things and deliberately not a fifth:

```
WHO                    sub
WHICH TENANT           tenant_id
WHICH APPLICATION      azp -> consumer_app_id
WHICH RESOURCE SERVER  aud
-----------------------------------------------
WHAT THIS USER MAY DO  not established (Part 3)
```

So `backendDependencies` must never become an authorisation scope, and the caller's
roles must never be read as the receiver's permissions.

The data boundary the review endorsed, and which items 4–7 exist to hold:

```
Zitadel   owns authentication identity
oranger   owns oranger-specific user data
waypoint  owns waypoint-specific resources, keyed by opaque Zitadel subject
```

waypoint never asks "who is `sub=abc123`". It knows that subject is authenticated,
and that an authorised application is carrying the call. Tenant, `consumer_app_id`
and subject are the whole contract.

The three receiver invariants are stated inside the band each belongs to, and keep
their numbers so earlier references resolve. Each is a thing a receiver must DO,
and each has been checked against what this platform actually does rather than
asserted:

```
Invariant 1  APIs accept access tokens, never ID tokens     Part 1
Invariant 2  azp is checked against a caller allowlist      Part 2
Invariant 3  tenant identity comes from a claim             Part 1
```

## Context

ADR-088 decided the axis and named identity as one of the places it applies:

| | `tenantId` | `appId` |
|---|---|---|
| Identity | Zitadel organisation | **Zitadel project within it** |

and said plainly what the platform was doing instead:

> *"the organisation is the tenantId and the project is a single name from
> configuration, so every box has one project per organisation. An org of
> `nutgraf` with projects `waypoint` and `oranger` is the shape Zitadel expects
> and the shape the platform declines to use."*

It was never built. `ensureProject(ctx, orgID, a.cfg.ProjectName)` still takes one
configured name, so every application of every tenant lands in one project.

The second application of a real tenant is where that stops being theoretical.
Provisioning oranger produces two failures:

- **No organisation binding.** The binding is read from the app's own XR
  (`status.identity.zitadelOrgId`); oranger's has none, so the provider falls back
  to a name search, finds waypoint's `nutgraf` org, and returns `ErrOrgUnadopted`
  forever. `OIDC_CLIENT_ID` is never published and oranger's gateway cannot start.
- **A shared client.** The client is named `tenantID + "-" + publicName`, and an
  existing one is returned unchanged. Two applications that declare the same
  client name get the same client id — registered for the other's hostname, so
  login fails with `redirect_uri_mismatch`, and carrying the same audience, so
  each backend accepts the other's tokens.

The second is the security one -- but **not for the reason first given here**, and
the correction matters enough to state before the decision rather than in a
footnote.

This ADR originally argued that two applications sharing a client share an
audience, "so each backend accepts the other's tokens", and that separating the
projects fixes it. ADR-095 disproved the second half. A Zitadel project id is not
an isolation boundary:

```
what a project IS          an identity namespace -- the scope for an
                           application's clients, redirect URIs, roles, and
                           the audience context its tokens are built from

what a project IS NOT      an automatic authorization boundary. Splitting
                           two applications into two projects does not stop
                           one's token being presented to the other.
```

Three findings, each established by reading rather than assumed:

- One audience slice is signed into **both** the ID token and the access token
  (`session.Audience`), so no audience value distinguishes them.
- Any client may request any project's audience: `isScopeAllowed` returns true
  unconditionally for the project-audience scope prefix, which this ADR settles
  in its own words below and which is *why* the allowlist exists.
- A cross-application call requires the caller's token to already hold the
  target's audience (`validateTokenExchangeAudience`), so the moment oranger may
  call waypoint, oranger's tokens carry waypoint's project id by construction.

**Separate projects therefore change a default, not a boundary.** Two applications
that never call each other no longer share an audience by accident, which is worth
having; two that do are back to sharing one, and nothing at the issuer prevents a
third from asking for it.

The real cross-application boundary is the receiver's own validation, in full:

```
issuer          the expected one
signature       against the issuer's published keys
expiry          and any not-before
aud             names the target service
azp             names a PERMITTED caller -- the allowlist, not the audience
tenant          from a claim, never from the audience
AUTHORISATION   the receiver's decision about this user and this operation
```

plus, per ADR-095, that the credential is an exchanged access token at all --
which a browser cannot mint, because the exchange needs confidential client
credentials it does not hold.

So this ADR still stands, on a narrower and more honest claim: per-application
projects give each application its own client set, redirect URIs, role namespace
and grant surface, and remove the shared-client failure below. They do not, by
themselves, keep one application's users out of another's API. Invariant 2 does
that.

## Decision

### The topology

```
one Zitadel instance, on the hub
└── organisation: <tenantId>          one per tenant -- the identity domain
    ├── project: <appId>              one per application -- the security context
    │   └── client(s)                 fleet-declared (ADR-053)
    └── project: <appId>
```

One instance. One organisation per tenant. **Single sign-on is unchanged**: it is
a property of the organisation and the session, so a person who signs in at one
application opens the other without a second prompt. What differs per application
is the `client_id` and therefore the token audience.

A shared client does not *enable* cross-application calls. It makes every
application permanently able to call every other, including ones that do not exist
yet, with no way to refuse and nothing to revoke.


## Part 1 — Same-application authorisation: ACCEPTED

A user signs in to an application and calls that application's own API. This is
the common path, it is decided, and ADR-095 implements the credential half of it.

What a receiver does here is invariants 1 and 3. Invariant 2 also applies -- the
caller is the application's own gateway -- but it earns its keep in Part 2, and is
stated there.

### Invariant 1 — APIs accept ACCESS tokens, never ID tokens

> **AMENDED 2026-09-29 by ADR-095.** The invariant stands. The *mechanism* below
> was wrong, and the paragraphs it rested on are corrected in place rather than
> deleted, because the error is instructive.

An ID token is issued to the client, about the user. It is not a credential for a
resource server. That much is unchanged, and it is the whole reason for this
invariant.

**What was wrong: the audience cannot express it.** This section said an ID
token's `aud` is the client id and an access token's carries project ids, so the
two could be told apart and a migration could turn the check from one to the
other. ADR-095 read Zitadel and found otherwise: `audienceFromProjectID` returns
every client id in the project *plus* the project id, and `token.go` signs that
same `session.Audience` into **both** tokens. The two are indistinguishable by
audience, under any value.

So the migration this section described — every workload changing from
`aud == OIDC_CLIENT_ID` to requiring its project id — is not merely unnecessary,
it would not have enforced this invariant. A receiver requiring the project id
still accepts an ID token.

**What actually enforces it** (ADR-095, implemented):

```
gateway    exchanges the session's ID token, RFC 8693, for a JWT access token
           minted for the receiver -- as a CONFIDENTIAL client
receiver   refuses any token carrying at_hash, a version-scoped Zitadel
           invariant rather than an OIDC rule
audience   UNCHANGED. It was never the thing doing the work.
```

And the sequencing claim at the end of this section is withdrawn: because an
access token's `aud` already contains the client id receivers check today, the
gateway can switch with no receiver change, so this does **not** have to wait for
the per-application projects.

### Invariant 3 — tenant identity comes from a claim, never from the audience

A correct audience says the token was addressed to this application. It says
nothing about which tenant the user belongs to, and the two are independently
forgeable in different ways.

The claim is already defined and implemented -- `packages/auth/src/jwt/claims.ts`
resolves the tenant in priority order:

```
tenant_id
urn:zitadel:iam:user:resourceowner:id     the user's owning organisation
urn:zitadel:iam:org:id
```

The receiver compares that to its own configured tenant. The acceptance test is
the one that proves the two checks are independent:

```
tenant A token, aud = tenant A project, -> tenant A backend    ACCEPT
tenant B token, aud = tenant A project, -> tenant A backend    REJECT
```

The second case is constructible precisely because Zitadel issues any project's
audience to any client, as verified above. A receiver that treats audience as the
tenant check accepts it.


## Part 2 — Cross-application authentication: ACCEPTED

oranger calls waypoint's API with a user's credential. **Who may call, and whether
a token is meant for this receiver, is decided.** What the user may DO there is
Part 3 and is not.

This is the part where an audience is most likely to be mistaken for a permission,
so the sections below say repeatedly that it is not one. That repetition is
deliberate.

### Cross-application access is a target-project AUDIENCE, not a project grant

A Zitadel **Project Grant** grants a project from one organisation to *another
organisation*. That is not this case: both applications are in one organisation,
one tenant, one box. Using that term would send an implementer to the wrong API.

The mechanism for same-organisation, application-to-application access is
requesting the target project's audience on the authentication request:

```
urn:zitadel:iam:org:project:id:<target-project-id>:aud
```

The token then carries both audiences, and the target validates that its own
project is present. Standard audience validation is membership, not equality, so
the receiving side needs no change.

### AUDIENCE IS NOT AUTHORISATION

This is the section to read if only one is read.

Adding waypoint's project to oranger's token audience says exactly one thing:

> this token is intended to be accepted by waypoint

It does **not** say the person may do anything in particular there. It is
addressing, not permission. A receiver that treats a correct audience as
authorisation has built an application where any authenticated user of a
*sibling* application can perform any operation it exposes.

So a receiver validates, in order, and none of these is optional:

```
issuer            the expected one, and only that one
signature         against the issuer's published keys
expiry            and any not-before
audience          contains THIS project
tenant / org      the expected organisation
AUTHORISATION     the receiver's own decision about this user and this operation
```

The platform delivers the first five. The sixth belongs to the application and
cannot be delegated to the identity provider by adding an audience.

### Who may request an audience: the fleet declares, the platform renders

The chart already states the rule that governs this, about scopes:

> *"Fixed by the platform. Scopes bound what a token may do, so a fleet able to
> widen its own would be granting itself authority the platform did not issue."*

`oidcScopes` is supplied by the environment-manager, never by a fleet, and that
must not change. But it is environment-wide, so adding an audience scope there
would give EVERY application EVERY audience -- the shared-client problem returning
by another route.

So the scope becomes per application, rendered by the platform from a declaration
the fleet makes in its own repository:

```yaml
# environments/dev/oranger/values.yaml
identity:
  backendDependencies:
    - waypoint      # an appId of THIS tenant
```

The declaration means **"oranger is permitted to make authenticated user calls to
waypoint"** -- not merely "put waypoint in my audience". That distinction decides
where it is used: the platform renders it into BOTH the scope oranger's client
requests AND the allowed-caller list waypoint validates against. Rendering only the
first would make the declaration advisory, which is exactly what it must not be --
see the settled section below, where Zitadel is shown not to restrict the request
at all.

The fleet never writes the scope string itself, so it cannot widen its own scopes
in the sense the chart's existing rule prohibits. What it cannot do is bind a
compromised client, which is why the receiver enforces.

**Same tenant only.** A dependency naming an app of another tenant is refused.
Cross-tenant access is a different decision with a different blast radius, and
ADR-088 gives it no mechanism.

### SETTLED: Zitadel does NOT restrict which project audiences a client may request

This was left open in the first draft. It is now answered, from the vendored
source and from the deployed instance, and the answer is the dangerous one.

**Source** (`reference-projects/zitadel`, v5.0.0-base). `isScopeAllowed` in
`internal/api/oidc/client_converter.go`:

```go
if strings.HasPrefix(scope, domain.ProjectIDScope) {
    return true          // unconditional
}
```

and `AddAudScopeToAudience` in `internal/domain/token.go` is string manipulation
with no lookup at all:

```go
projectID := strings.TrimSuffix(strings.TrimPrefix(scope, ProjectIDScope), AudSuffix)
audience = addProjectID(audience, projectID)
```

No check that the project exists, none that the client relates to it.

**Deployed instance** (v4.15.3, `id.dev.nutgraf.in`, 2026-09-28). A throwaway
machine client with no relationship to either project requested both audiences:

```
scope: urn:zitadel:iam:org:project:id:<probe-project>:aud
       urn:zitadel:iam:org:project:id:<platform-project>:aud

aud:   ["392792754981175698", "391788656387424626"]   <- both issued
```

The second is the project every workload on this box validates against today. The
probe project and machine user were deleted afterwards.

#### What follows, and it is the central control

**The fleet declaration is the ONLY thing deciding which audiences are requested,
and it is a deployment control, not a security boundary.** It governs what the
platform asks the client to request. It does not and cannot prevent a client from
asking for something else: a compromised or modified application can obtain a token
carrying any project's audience, including one it was never declared against.

So the earlier sentence "a reviewer sees the dependency in the tenant's own
repository" is true and insufficient, and this ADR does not rely on it.

**The receiving application enforces the boundary, and it checks three things, not
one:**

```
aud contains THIS project        is this token intended for me?
caller identity is permitted     which application obtained it?
user authorisation               may this person do this operation?
```

The middle check is what the audience cannot provide. For a browser token from an
authorization-code flow that is `azp`; the receiver compares it against the callers
declared for it, rendered by the platform from the same declaration that produces
the requester's scope -- one declaration, two enforcement points:

```
fleet declares:  oranger -> waypoint
       |
       +--> the scope oranger's client requests
       +--> the allowed-caller list waypoint validates against
```

Rendering only the first half would leave the platform asking politely.

**`azp` is absent on client_credentials tokens.** Observed in the probe above: the
machine token carried no `azp`, only `sub`. So a receiver that keys solely on `azp`
admits every machine token silently. Service-to-service on this platform does not
use user tokens at all (waypoint ADR-024, `x-waypoint-internal-token`), so the
correct rule is narrow: reject a token with no `azp` on the browser-call path
rather than treating absence as a pass.

### Invariant 2 — `azp` is checked against a rendered caller allowlist, and absence is refusal

```
aud contains THIS project        the token is addressed to me
azp ∈ allowed caller clients     which client obtained it
user authorisation               may this person do this operation
```

`azp` is the client id that requested the token. It is present on ID tokens and on
JWT access tokens; it was **absent** on the `client_credentials` token in the probe
above. So a receiver keying on `azp` without requiring it admits every machine
token silently, and the rule is: **on the browser-call path a missing `azp` is
rejected**, never treated as a trusted service. Service-to-service on this platform
authenticates separately and carries no user token (waypoint ADR-024).

**The declaration stays at application level; the platform expands it to one
client.** A fleet writes `backendDependencies: [waypoint]`, and the platform
publishes the CALLER's client id onto WAYPOINT's path, because the target is what
enforces the check. The alternative -- tenants declaring client ids -- pushes an
issuer-allocated, rotating identifier into a tenant's repository, and a client
rotation would then silently break a dependency the tenant thought it had declared.

**INVARIANT 2a (hard).** The downstream token's `azp` MUST be the confidential
exchange client that performed the token exchange. `OIDC_ALLOWED_AZP` MUST contain
that client id. Public/browser client ids MUST NOT be used for cross-application
caller authorisation.

**INVARIANT 2b (hard).** A receiver MUST derive consumer application, tenant and
user subject exclusively from validated token claims. No caller-supplied identity
header or request field participates in cross-application identity.

```
consumer_app_id = mapping(validated azp -> application)
subject         = validated sub
tenant_id       = validated tenant_id
```

and never from an HTTP request, and never by resolving `subject` against the
calling application's user table.

These two are stated as MUST because they are the two regressions this design has
actually produced: 2a was implemented wrongly (below), and 2b is how the deployed
ADR-057 hop works today, which is why that hop is replaced rather than extended.

This corrects what this section said until 2026-09-30, which was that a dependency
"authorises every browser client of the calling application". That sentence was
wrong in both directions, and the implementation followed it:

- **It would never match.** `createExchangeAccessToken` and `createExchangeJWT`
  both pass `client.client.ClientID` -- the client that AUTHENTICATED the
  exchange, not the one whose token was the subject -- into `CreateOIDCSession`.
  An exchanged token therefore never carries the browser client's id, so every
  genuine cross-application call would have been refused.
- **Had it matched, it would have been too wide.** The public PKCE client is what
  a browser authenticates as. Admitting it means any token obtained through the
  caller's own login satisfies the target's allowlist -- including an ID token.
  The allowlist exists to distinguish applications; a public client shared with
  every browser session cannot do that.

So the granularity is **one confidential client per calling application**, not
"every client of that application". An application needing finer granularity than
that needs a second project, which is the unit this ADR makes cheap.

**Resolution is pending-not-approximate.** Where the caller's exchange client id
is not yet known, the platform publishes NOTHING and waits for the next reconcile.
A target admitting the wrong caller looks configured and is not, which is strictly
worse than one admitting nobody: the latter fails closed.

**Implemented** in `zero-ops-auth` as `JwtValidator({ allowedAzp: [...] })`.
Three properties are deliberate and are covered by tests:

- **Absence is refusal.** A token with no `azp` cannot be matched against an
  allowlist, and treating "cannot tell who is calling" as "allowed" is the exact
  failure this invariant exists to prevent. A `client_credentials` token carries
  no `azp`, which is why this is stated rather than assumed.
- **`client_id` is read too.** Zitadel sets `azp` on ID tokens and `client_id`
  on access tokens for the same fact; a receiver must not have to know which
  kind it is holding.
- **It is checked before the tenant requirement.** A disallowed caller is
  reported as a disallowed caller, not as a tenant-less token — otherwise an
  operator investigates scopes when the answer is that the application should
  not be calling at all.

Omitting the allowlist skips the check, which is correct only for a receiver no
other application can reach. It is not the default because turning it on by
default would refuse every existing deployment on upgrade, so it is a thing to
review for rather than a thing that happens.

### Browser-direct is supported, and the BFF is not made mandatory

A browser holding a token audienced for waypoint may call waypoint directly. This
is the ordinary OIDC multi-tier shape and it is what the tenant asked for.

Routing through the calling application's BFF remains available and is sometimes
better -- it needs no second audience at all -- but it must not become an
accidental requirement produced by identity plumbing that cannot express the
direct case. Note that the existing intra-tenant path is neither: waypoint's
service-to-service calls use a shared secret (`x-waypoint-internal-token`,
waypoint ADR-024) and carry no user token. Those are three different trust
relationships and should not be collapsed into one.

### waypoint stays where it is

New applications get their own project. waypoint remains on the current project
until it is moved deliberately, because moving it reissues its client id, and its
own fleet values say what that costs:

> *"renaming it re-registers the client under a new identifier and breaks login
> until every consumer is updated"*

That is a live-application migration with its own verification window, like the
ADR-093 cutover. Coupling it to a capability new applications need means neither
ships until both are safe.


## Part 3 — Cross-application authorisation: NOT YET DECIDED

Everything above is about *whether a call is admitted*. This part is about *what
the caller's user may do once it is*, and it is the one question this ADR does not
answer.

### The gap, and the experiment that closes it

Cross-application *authentication* is settled (ADR-095). What a user may DO in
the target application is not, and this ADR must not be read as deciding it.

**The gap.** Roles are resolved against the exchanging client's project:

```
token_exchange.go:414
  getUserInfo(subjectToken.userID, client.client.ProjectID, ...)
                                   ^^^ the EXCHANGE client's project
```

So a token oranger mints for waypoint carries **oranger's** roles. waypoint can
answer "is this token for me?" (`aud`) and "which application called?" (`azp`),
but not "what may this user do here?".

**The candidate that would close it without another hop or another authorisation
store**: Zitadel's plural roles scope, `urn:zitadel:iam:org:projects:roles`,
which emits `urn:zitadel:iam:org:project:<id>:roles` per project. Two details
read from source, because both differ from how the scope is usually described and
both change what has to be sent:

- It is driven by the **`:aud` SCOPES in the request**, not by the token's
  audience. `prepareRoles` builds `roleAudience` with
  `AddAudScopeToAudience(ctx, roleAudience, scope)`, which parses
  `urn:zitadel:iam:org:project:id:<id>:aud` out of the SCOPE list. The RFC 8693
  `audience` parameter is a separate input. **The gateway currently sends no
  scopes at all**, so today's exchanged token carries only the exchange client's
  own project roles whatever its audience says.
- It is **not** subject to the cross-project role filter. `isScopeAllowed`
  returns true for the plural scope before reaching
  `slices.Contains(allowedScopes, scope)`, and `allowedScopes` is built from the
  *authenticating* client's `ProjectRoleKeys`. That check filters a **specific**
  role scope (`...:project:role:<key>`) by the caller's own project, which is the
  surface of the defect reported against 4.17.2. So the experiment requests no
  specific role scopes.

**And a correction to how the mitigation is usually stated.** `ProjectRoleAssertion`
cannot be varied per client: it is a column on the PROJECT
(`internal/query/project.go`), shared by every application in it, so it cannot be
turned off for an exchange client without turning it off for the browser login
client beside it. The per-app flag is `AccessTokenRoleAssertion`, and it must stay
**true** — `assertRoles` early-returns on it, so false yields no roles at all
rather than unfiltered ones. Turning off `ProjectRoleAssertion` would mean only
that roles are asserted when a scope asks for them, which is defensible but is a
project-wide change, not a per-client one.

**Decided by experiment, not by argument.** `packages/auth/tests/jwt/zitadel-compat.live.test.ts`
runs it against the deployed instance. If the peer project's roles arrive under
their own qualified claim, this ADR adopts:

```
aud    both projects        resource targeting
azp    the calling app      the rendered caller allowlist (invariant 2)
roles  per-project claims   the RECEIVER reads its OWN project's claim
```

If they do not, the choice is **receiver-side exchange** — waypoint's own gateway
exchanging into waypoint's project — and not a second authorisation store in every
application. Authorisation belongs to the resource server, and a waypoint endpoint
should receive a credential carrying waypoint's authorisation context rather than
expecting oranger to manufacture waypoint permissions. The tenant-local database
remains a third option and is deliberately last: it is a second authorisation
system beside Zitadel's project roles, and it should not be built to work around a
claim Zitadel can express.

Whichever wins becomes a live check rather than a sentence here, because the
relevant behaviour is version-sensitive.

## Consequences

Grouped by band, so it stays visible which of them are consequences of something
decided and which are consequences of something still open.

**Part 1 — same-application authorisation (ACCEPTED)**

- The API credential changes from the ID token to the access token across every
  tenant workload (ADR-095). Not a header rename: it changes what the receiver is
  handed and what proves its kind.
- It does **not** ship with the per-application projects. That coupling was
  withdrawn when the audience turned out not to distinguish the two token types —
  an access token's `aud` already contains what receivers check today, so the
  gateway switches with no receiver change.
- Applications must not treat a valid audience as permission. Where one does
  today, that is a defect this ADR makes visible rather than creates.

**Part 2 — cross-application authentication (ACCEPTED)**

- Provisioning gains a project per application, and the organisation binding is
  inherited from a sibling XR of the same tenant rather than found by name — the
  name search is what produced `ErrOrgUnadopted`.
- `oidcScopes` becomes per application. It stays platform-rendered.
- Each application's backend validates its own audience AND the calling
  application's identity. Audience alone is insufficient: Zitadel issues any
  project's audience to any client that asks, so a receiver checking only `aud`
  accepts a token any application on the box could mint for it.
- A token with no `azp` is refused on the browser-call path rather than admitted.
  Machine tokens carry none, and service-to-service here does not use user tokens.
- The gateway holds a confidential exchange client per application. That credential
  is what makes the exchange a boundary — a browser cannot perform one — and it is
  also a new secret on the request path with a larger blast radius than the public
  client, which holds none.
- waypoint keeps one project and one client until its own migration.

**Part 3 — cross-application authorisation (NOT YET DECIDED)**

- **No application may ship a cross-application authorisation decision yet.** A
  receiver presented with a sibling's token today can establish who is calling and
  that the token is meant for it, and cannot establish what the user may do. An
  endpoint that authorises on the roles in such a token is authorising on the
  CALLER's project roles.
- The decision is gated on one experiment, not on further argument, and the
  experiment is a live check rather than a paragraph — because the behaviour it
  probes is version-sensitive and an ADR sentence would not notice an upgrade.
- Until it resolves, `azp` plus a receiver's own tenant-local data is the only
  sound basis for a cross-application decision, and building that permanently is
  explicitly NOT the decision — it is what Part 3 is trying to avoid having to do.

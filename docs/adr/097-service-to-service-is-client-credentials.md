# ADR-097: An application calls another application as itself, with client credentials

**Date:** 2026-09-30
**Status:** Accepted
**Relates to:** ADR-094 (one identity project per application; the caller allowlist),
ADR-095 (access tokens for APIs), ADR-088 (the tenant and the application are
different axes), waypoint ADR-042 (consumer applications call the SDK directly)

## Context

ADR-094 Part 2 and ADR-095 describe one mechanism for an application calling
another: the caller's gateway exchanges the user's session for an access token
audienced to the target, and forwards it. The target then knows the tenant, the
calling application and the user.

That mechanism was built, deployed, and **run against the issuer for the first
time on 2026-09-30**. It does not work, and cannot on any released Zitadel.

`validateTokenExchangeScopes` in v4.15.3 — the deployed version — is:

```go
for _, scope := range requestedScopes {
    if !slices.Contains(subjectScopes, scope) || !slices.Contains(actorScopes, scope) {
        return nil, oidc.ErrInvalidScope().WithDescription(
            "scope %q not found in subject or actor token", scope)
    }
}
```

`||`, not `&&`: a requested scope must be present on **both** input tokens. A
gateway-mediated browser session holds only the id token, and an id token carries
no scopes. So:

```
request any scope   -> invalid_scope, whatever the scope is
request no scopes   -> the minted token carries no tenant claim, and the
                       receiver refuses it as tenant-less
```

Observed exactly, from the gateway's own debug log:

```
{"error":"invalid_scope","error_description":
 "scope \"urn:zitadel:iam:user:resourceowner\" not found in subject or actor token"}
```

**No released Zitadel validates it otherwise.** The union-based replacement
(`validateUnionTokenExchangeScopes` plus a separate path for scopeless subjects)
is commit `39ad66bde`, and `git tag --contains` returns nothing for it — it is in
no tag, including v4.17.3. There is no published v5 image; the registry answers
404 for `v5.0.0`.

**How this was got wrong.** ADR-095 was amended on 2026-09-30 to say the exchange
DOES serve a browser session. That amendment was derived from a source checkout at
`v5.0.0-base-226-g8ccd5b206` — unreleased main — while the box runs v4.15.3. The
same mistake had already been made once that day against AgentGateway. The rule
that follows is in Consequences.

## Decision

**An application calling another application, with no user in the request, does so
as ITSELF, using OAuth 2.0 client credentials. It is not a variant of the
user-token design and does not wait on it.**

```
oranger sdk ──client_credentials──► Zitadel ──JWT──► oranger sdk ──Bearer──► waypoint sdk
              scopes:
                urn:zitadel:iam:org:project:id:<waypoint>:aud
                urn:zitadel:iam:user:resourceowner
```

**This works on the deployed issuer**, and each link was verified against v4.15.3
rather than assumed:

| link | evidence in v4.15.3 |
|---|---|
| the target's audience may be requested | `isScopeAllowed` returns true for the `ProjectIDScope` prefix, and `clientCredentialsClient.IsScopeAllowed` delegates to it |
| the scope becomes the audience | `domain.AddAudScopeToAudience` turns `...:<projectID>:aud` into an audience entry |
| the tenant claim may be requested | `isScopeAllowed` also permits `ScopeResourceOwner` |
| the token can be a JWT | `tokenType: user.Machine.AccessTokenType`, mapped by `accessTokenTypeToOIDC` |
| the caller is identifiable | `NewAccessTokenClaims(..., client.GetID(), ...)` puts the client id on the token |

**Why this is unaffected by the defect above.** Client credentials has no subject
token, so there is no union check to fail. The block is specific to
on-behalf-of-user propagation; it was mistaken for a block on cross-application
calling because one mechanism had been built to serve both.

### The invariants

1. **Every application has its own machine identity** for the dependencies it
   declares. Not shared, for the reason ADR-088 gives about every other derived
   identity: one credential serving two applications is one revocation serving
   neither.
2. **Machine access tokens are JWTs, explicitly configured.** The default is
   opaque (`op.AccessTokenTypeBearer`), and an opaque token cannot be validated
   locally — the receiver would have to introspect, which puts the issuer in the
   path of every request.
3. **The caller requests the target application's project audience**, at token
   time. Not at login: this flow has no login.
4. **The caller requests `ScopeResourceOwner`**, so the token carries the tenant.
   Without it the receiver refuses as tenant-less, correctly.
5. **The receiver validates** issuer, signature, expiry, its own audience, the
   authenticated client identity, and the tenant claim.
6. **The receiver allowlists the caller's machine client id.**
7. **`backendDependencies` is a provisioning declaration, never the runtime
   authorization.** It causes a credential and an allowlist entry to exist. It
   grants nothing at request time; the token does.
8. **No user identity is inferred from a machine token.** Its `sub` is the machine
   user and identifies the SERVICE. Nothing may map it to a person.
9. **No caller-supplied `x-*-user-*` header crosses an application boundary.** The
   intra-application forwarding rule (ADR-057) stays inside one application.
10. **Cilium is defence in depth, not authorization.** The policy bounds where a
    credential can be used; the credential establishes who is calling.

### The receiver allowlists the authenticated CLIENT IDENTITY

Stated this way deliberately, because the claim differs by flow:

```
user access token (browser)       azp
client credentials token          client_id
```

`zero-ops-auth` reads `azp || client_id` and has since ADR-094, so one allowlist
serves both. **The ADR does not claim client credentials produces `azp`** — it
does not, and an implementation written to that belief would admit nobody.

### What is NOT decided here

On-behalf-of-user propagation across applications. It needs either a Zitadel that
validates exchange scopes as a union, or AgentGateway retaining the browser's
access token so an access token can be the subject. Neither is released. Until
one is, a cross-application call carries the SERVICE's identity and not a user's,
and waypoint ADR-042's user-scoped surface waits on it.

Authorization remains ADR-094 Part 3 and is still undecided. This ADR establishes
who is calling, not what they may do.

## Consequences

### Positive

- Cross-application calling is unblocked today, on released software, using a
  standard OAuth flow rather than a mechanism invented for this platform.
- The two flows are separate and stay separate. A service token cannot be mistaken
  for a user token: it has no user, and nothing reads one out of it.
- `backendDependencies` renders one more artefact from the same declaration, so
  the fleet's contract is unchanged.

### Negative

- An application that genuinely needs the calling USER's identity in another
  application cannot have it yet. That is a real gap, and the honest answer to a
  product team asking for it is "not until the issuer supports it", not a header.
- A second credential per application to provision, rotate and revoke.
- Two flows to understand rather than one. That is the cost of not pretending a
  service identity is a user identity.

### The rule this session earned

**Read the source of the version that is deployed.** Twice in one day a design was
derived from `main` and failed against the running release — AgentGateway
v1.4.1 against main, then Zitadel v4.15.3 against `v5.0.0-base+226`. Both times
the code read correctly and the box disagreed. A capability claim is not evidence
until it names the version it was read from and that version is the one running.

## Impact

- `internal/kube-sbt/providers/zitadel` — a machine user and confidential client
  per application, `accessTokenType: JWT`.
- `operators/hub-operator` — publish that client's id into each declared target's
  allowlist, beside what `publishAllowedCaller` already does.
- `packages/auth` — no change; `azp || client_id` already covers it.
- `manifests/tenants/charts/universal-tenant` — no change; the cross-application
  network policies already render from `backendDependencies`.
- ADR-095 — its 2026-09-30 amendment is withdrawn; see that ADR.

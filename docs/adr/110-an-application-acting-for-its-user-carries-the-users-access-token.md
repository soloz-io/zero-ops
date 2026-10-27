# ADR-110: An application acting for its user carries the user's own access token

**Date:** 2026-10-27
**Status:** Proposed — reuses the existing authentication primitives; three provisioning gaps (Impact) are closed before the first receiver relies on it.
**Relates to:** ADR-094 (one identity project per application; the caller map), ADR-095 (access tokens for APIs), ADR-097 (an application calls another application as itself), waypoint ADR-042, ADR-045, ADR-058
**Amends:** ADR-097 ("What is NOT decided": on-behalf-of-user calls are decided here, without token exchange)

## Context

ADR-097 lets an application call another as ITSELF, and left open how a call carries the USER it acts for. A receiver that changes something the user owns — spending their credits — cannot take the user from a header the caller writes, since any caller could name any user (ADR-097 invariant 9). Waypoint's consumer surface does exactly that today: the acting user arrives as `x-consumer-user-subject` and `x-consumer-user-email` beside a service token (waypoint ADR-045).

Token exchange from a browser session cannot run on the deployed issuer (Zitadel v4.15.3, ADR-097), and the tenant gateway (AgentGateway v1.6.0) keeps only the ID token from a browser login (`http/oidc/callback.rs:141-165`).

The platform already has every primitive a server-side alternative needs:

- **The delegated flow, in `zero-ops-auth`.** `OidcClient` performs the authorization-code exchange, refresh and revocation for a confidential client; `RedisTokenStore` keeps a user's tokens server-side with a lock across replicas; `generateOAuthState`/`hashState` protect the flow. Waypoint's BFF composes them into `delegatedTokenFor` (`packages/bff/src/routes/auth.ts:110`): return a stored token, refresh one near expiry, or redirect to start the flow.
- **The login scopes and the target audiences, published.** `PLATFORM_LOGIN_SCOPES` puts the tenant claim (`urn:zitadel:iam:user:resourceowner`) and roles on a token; the platform publishes each application's `OIDC_BACKEND_AUDIENCE_SCOPES` from its `backendDependencies`. Any client may request a project audience (`isScopeAllowed`, `client_converter.go:249-275` at v4.15.3).
- **Declared confidential clients.** A fleet declares OAuth clients (`spec.oauth.clients`: name, type, `redirectPaths`). kube-sbt creates each confidential client with the authorization-code and refresh-token grants and a JWT access token (`providers/zitadel/tenant.go:747-766`), and its credential is delivered to the workload as `OAUTH_<NAME>_CLIENT_ID` / `_SECRET`.
- **The receiver, in `zero-ops-auth`.** `consumerApiValidator` refuses ID tokens, requires the target project's audience, compares the tenant claim, and admits only clients in the platform-rendered caller map `OIDC_ALLOWED_AZP` (`<clientId>=<appId>`), naming the caller as `consumer_app_id` from the map, never from the token. Waypoint's `requireCaller` builds its consumer scope from that.
- **The caller map cannot tell a user token from a service token.** It lists the gateway's exchange client and the machine client side by side, each mapped to the application (`publishServiceIdentity`). The client claim cannot tell them apart either: `zero-ops-auth` reads `azp` and `client_id` as one field, because Zitadel spells the claim differently by token kind (`jwt/claims.ts:38`).

## Decision

**An application acting for its user calls another application with the user's own access token for the target, obtained by the application's backend through the existing delegated flow and kept server-side. The receiver validates it with the existing cross-application validator and takes the acting user from its subject. The caller map records which clients carry a user.**

### Caller: the existing delegated flow

- The calling application declares a confidential client for its backend, with its callback path.
- The backend runs the authorization-code flow with that client, using `OidcClient`, `RedisTokenStore` and the `delegatedTokenFor` pattern. It requests `PLATFORM_LOGIN_SCOPES` and its `OIDC_BACKEND_AUDIENCE_SCOPES`. The user's existing sign-in answers the authorization, so the user sees no new login while their session is live.
- Tokens stay server-side, per user, refreshed through `offline_access`. The browser never holds one.
- The backend sends the user's token as the request's bearer credential. One token per call; no second credential accompanies it.
- A call with no user uses the service token, unchanged (ADR-097).

### Receiver: the existing validator, the user from the token

- `consumerApiValidator` validates the token as it does today: an access token, never an ID token; the target project's audience; the tenant claim compared; the client in the caller map.
- The acting user is the token's subject, and only that. No identity header from the caller is read.
- **A user token is still a bearer credential.** Its client claim names the client it was issued to; it does not prove the caller holds that client's secret on each call. So audience, tenant, the caller map, and the receiver's own authorisation (for credits, waypoint ADR-058) are all enforced on every call.

### The caller map records each client's kind

- Each entry names the client, the application, and what the client's tokens carry: **user** (the application's declared delegated client) or **service** (the machine client, ADR-097).
- **Why a `user` client's tokens carry a person,** verified at Zitadel v4.15.3: the login refuses a machine user (`auth_request.go:859`), and client-credentials and JWT-profile tokens name the machine user as their client, never an application (`client_credentials.go`, `token_jwt_profile.go`). So a client holding only the authorization-code and refresh grants is issued tokens for people alone. The token-exchange grant breaks this, since an exchanged token takes whatever subject it was handed. A `user` client therefore holds no exchange grant, and the gateway's exchange client is not admitted at all.
- The platform renders the kind from what it provisioned, never from the token's shape.
- The validator sets the caller's kind beside `consumer_app_id`. A receiver acts for a user only on a **user** entry, and never reads a person out of a **service** token (ADR-097 invariant 8). Machine-to-machine callers keep working on **service** entries.
- An entry without a kind is refused. Existing values are re-rendered in the new form.

### Lifetime

Access-token lifetime is one instance-wide setting in Zitadel (`oidc_client_by_id.sql:31-42`). Shortening it to 15 minutes (ADR-094 requirement 8) is subject to the instance-wide compatibility qualification already agreed; it does not change this mechanism.

### What this does not decide

What a user may do in the target once identified: ADR-094 Part 3. The target decides that with its own records — for credits, Waypoint's memberships, application spending permissions, limits and standing authorisations (waypoint ADR-058).

### Alternatives considered

- **Token exchange from the browser session.** Fails on the deployed issuer (ADR-097).
- **A browser-held user token through the application's public client.** Puts a bearer token in the browser, and a public client's identity is any browser's; the platform once admitted a public client in a caller allowlist by mistake and refuses to again.
- **A second, user-only credential beside the service token.** One user token already names both the application (through the caller map) and the user.
- **A user identity asserted by the calling application.** Any caller could name any user.

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| The calling application's delegated confidential client and its callback | Zitadel, from the fleet's `spec.oauth.clients` | Fleet declares, Platform provisions | kube-sbt (tenant identity) | The calling application's backend | Day-1+ |
| A user's delegated tokens | The calling application's token store | The calling application | `RedisTokenStore` (refresh) | The calling application's backend | Day-1+ |
| Caller map, with each client's kind | Infisical, on the target's path | Platform | hub-operator | Target receivers (`consumerApiValidator`) | Day-1+ |

## Consequences

### Positive

- No new protocol, client type, validator or authorisation framework: the delegated flow, the declared confidential client and the cross-application validator already exist.
- Tokens never reach the browser.
- A receiver knows the acting user from a token the issuer produced for that user's session, and never from a header.
- Service and user callers are told apart by provisioning metadata, so machine-to-machine calls are unaffected.

### Negative

- A user token held by a compromised calling backend can be replayed until it expires, for users whose tokens it holds. Lifetime and the receiver's authorisation bound it.
- The calling backend keeps a token store and a callback route, and a user whose session has ended must sign in again before acting.
- Waypoint's consumers move from identity headers to delegated tokens for every call that acts for a user.

## Impact

**Provisioning gaps, verified in the code, closed before the first receiver relies on this:**

1. **A declared confidential client's callback is dropped.** The fleet contract requires `redirectPaths` for a client that completes a browser flow, and hub-operator sends them, but `EnsureConfidentialClient` creates the client with no redirect URIs (`providers/zitadel/tenant.go:993` passes `nil`). The code flow therefore fails at the authorization endpoint. The declared paths, on the tenant's public host, are passed through.
2. **An existing client's configuration is never reconciled.** `ensureApp` returns an existing application as found (`tenant.go:719-730`), so a callback added later never reaches a client created earlier. Its redirect URIs are brought to the declared set on reconcile.
3. **The caller map has no kind, and no declaration marks the delegated client.** The fleet marks the confidential client that acts for users (`actsForUser`: at most one, confidential, no exchange grant); hub-operator publishes it onto each target's map as **user** and the machine client as **service**, and each caller's reconcile replaces its own entries, dropping its exchange client and pre-kind entries; `parseAllowedCallers` and the validator carry the kind.

**Also:**

- **ADR-097** — "What is NOT decided" is decided for on-behalf-of-user calls.
- **`zero-ops-auth`** — the caller map's grammar gains the kind; the validator exposes it.
- **Waypoint** — consumers use the delegated flow; the consumer surface takes the acting user from the token's subject, and drops `x-consumer-user-*` (waypoint ADR-045, ADR-058).
- **Waypoint** — the delegated flow is built once in `waypoint-consumer` (start and callback routes, token store, PKCE, the subject check), and each consumer's BFF forwards the user's token to its SDK over the internal channel.
- **Live check** — `packages/auth/tests/jwt/zitadel-compat.live.test.ts` gains the ADR-110 gate: a delegated token admitted as **user** with a person's subject; a machine token admitted as **service** under another client; refusal of an unmapped client, an ID token and another tenant's token; and, when set, the access-token lifetime.

## References

- ADR-094: One identity project per application
- ADR-095: Moving the API credential from the ID token to an access token
- ADR-097: An application calls another application as itself, with client credentials
- waypoint ADR-042: Consumer applications call the SDK directly
- waypoint ADR-045: Waypoint knows the consumer's users
- waypoint ADR-058: Waypoint coordinates commercial billing and credits
- `reference-projects/zitadel` (tag `v4.15.3`)
- `reference-projects/agentgateway` (tag `v1.6.0`)

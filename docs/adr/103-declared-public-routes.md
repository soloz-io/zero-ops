# ADR-103: An application may declare login-free routes keyed by an unguessable token

**Date:** 2026-10-07
**Status:** Accepted
**Amends:** ADR-050 (tenant authentication via AgentGateway): a third declared
exception to "every path is behind the login", beside the webhook bind and
ADR-101's static assets
**Relates to:** ADR-101 (declared public static assets, whose construction this
follows), ADR-051 (platform-owned public route), ADR-088 (per-application gateway),
ADR-057 (the BFF is the browser's backend)

## Context

An application's users need to share a page with people who cannot sign in: a
public demo that replays a conversation, at `<app>.<env>.nutgraf.in/share/<token>`.
Anyone holding the link may view it, and nobody else.

The platform has no way to express that:

- every path on the hostname is behind the login, because the OIDC policy runs at
  gateway phase before route selection, so an app cannot exempt its own route;
- ADR-101's `publicAssets` is exact static paths to the frontend. A share has one
  path per token, its content is looked up by the BFF, and listing tokens in
  values is impossible.

The workaround (a snapshot in public object storage, shared by its storage URL) is
off the app's domain, cannot be revoked short of deleting the object, and sits
outside the gateway's controls.

## Decision

An application declares the prefixes it serves without a login:

```yaml
gateway:
  publicRoutes:
    - prefix: /share
      tokenLength: 22      # default; base62, about 131 bits
```

Absent or empty, nothing is rendered and nothing changes. Declared, the platform
renders, from that one per-app values file:

| Where | What |
|---|---|
| gateway (`universal-tenant`) | a separate bind, `:3004`, listener `public-routes`, outside the OIDC policy; per prefix one route to the application's `bff-workload`, matching the regex `^<prefix>/[A-Za-z0-9]{<tokenLength>}$`, GET and HEAD; a Service port `3004` |
| public route (`tenant-public-tls`) | per prefix one HTTPRoute rule, `PathPrefix <prefix>/`, GET and HEAD, to `:3004` |

The bounds, each by construction:

1. **One prefix segment plus one token segment.** The regex is built by the chart
   from the declared prefix and length; values cannot supply a regex. So
   `/share/<token>/x`, `/share/../api` and a token of the wrong shape match no
   route on the bind. The public route matches the prefix only, because regex
   path matching is not core Gateway API; the bind is where the exact shape holds.
2. **One backend:** the application's BFF, derived by the chart, never named in
   values. The BFF owns the lookup (ADR-057).
3. **GET and HEAD only**, at both layers.
4. **No session crosses it, in either direction.** `Cookie` and `Authorization`
   are stripped from the request and `Set-Cookie` from the response, at the bind
   and on the public route, so either layer alone holds.
5. **Safe response headers are set by the gateway**, not left to each page:
   `X-Robots-Tag: noindex, nofollow`, `Referrer-Policy: no-referrer` (so the token
   does not leak through a Referer), `Cache-Control: private, no-store`, and HSTS.
6. **A rate limit:** a token bucket per route at the bind (60 burst, 20 per second).
7. **Refused declarations:** a prefix that is not one lowercase segment; the
   prefixes `api`, `internal`, `v1`, `oauth`, `health`, `healthz`, `ui`, `assets`
   and `static`; a prefix that is the first segment of a `publicAssets` path; a
   `tokenLength` outside 16–64 or not an integer; duplicates; more than 4 routes.
   Both charts refuse the same input with the same message.

**The token is the only credential.** The gateway proves only that a path has the
right shape. The BFF must look the token up, compare its hash in constant time,
honour expiry and revocation, and refuse any token it did not mint. What a share
exposes is the application's decision and responsibility.

## Acceptance criteria

Without a session, for an application declaring `/share`:

- `GET /share/<valid token>` reaches the BFF and returns its page, with the headers
  above and no `Set-Cookie`;
- `GET /share/<unknown but well-shaped token>` reaches the BFF, which refuses it;
- `GET /share/<wrong shape>`, `/share/<token>/x` and `POST /share/<token>` do not
  reach the BFF;
- `/`, `/api/...` and every undeclared path still redirect to the login.

Pinned by preflight `103-public-routes` (R1–R5): the bind's shape, regexes,
methods, backend, rate limit, header policy, and the absence of dollar-brace
sequences in the shell-expanded config; the OIDC policy's single listener; the
public route's prefix, methods and filters; nothing rendered when undeclared; and
identical refusal of 14 unsafe declarations by both charts.

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Declared prefixes (`gateway.publicRoutes`) | tenant GitOps repository, per application | Tenant | ArgoCD | universal-tenant, tenant-public-tls | Day-1+ |
| `:3004` bind, route, headers, rate limit, Service port | platform charts | Platform | ArgoCD | the application's gateway | Day-1+ |
| Public HTTPRoute rule | platform charts (`platform-ops`) | Platform | ArgoCD | Cilium Gateway | Day-1+ |
| Tokens and what they expose | the application | Tenant | the application's BFF | anyone holding a link | Day-1+ |

## Consequences

### Positive

- Applications can publish pages to people outside the tenant, on their own
  domain, behind controls the platform sets once: shape, methods, headers, cookies
  and rate.
- The exception is a declaration in git, bounded by the chart.

### Negative

- A third unauthenticated surface on the gateway, and the first that reaches
  dynamic application code. Its safety rests on the application's token handling,
  which the platform cannot check.
- A declared prefix is reserved: the app cannot use it for its own routes behind
  the login.
- The rate limit is per gateway route, not per client. One client can use up the
  bucket for everyone; it bounds abuse rather than isolating it.
- Upgrading to a chart with this change restarts every tenant gateway once, as any
  edit to the gateway template does.

## Impact

- `manifests/tenants/charts/universal-tenant`: `publicRoutes` helper, `:3004`
  bind and Service port, `gateway.publicRoutes` default `[]`.
- `manifests/tenants/charts/tenant-public-tls`: the same helper and one HTTPRoute
  rule per prefix to `:3004`.
- `scripts/validate/preflight/103-public-routes.sh`.
- Requires AgentGateway v1.5.0 route `matches[].path.regex`, `matches[].method`,
  `policies.localRateLimit` and `policies.transformations`, and Gateway API
  `HTTPRouteMethodMatching` (Cilium 1.17 reports it).

## References

- ADR-050: Tenant authentication via AgentGateway
- ADR-051: Environment DNS naming and public gateway TLS
- ADR-057: Tenant user management (BFF trust)
- ADR-088: The tenant and the application are different axes
- ADR-101: Declared public static assets

# ADR-101: An application may declare static files that need no login

**Date:** 2026-10-03
**Status:** Accepted (amended 2026-10-12)
**Amends:** ADR-050 (tenant authentication via AgentGateway): one more declared
exception to "every path is behind the login", alongside the webhook bind
**Relates to:** ADR-051 (platform-owned public route), ADR-088 (per-application
gateway), ADR-024 (the webhook bind this mirrors)

## Context

Every path on an application's public hostname is behind the login: the
AgentGateway OIDC policy is attached to the `app` listener at gateway phase, so it
runs before route selection and no route can exempt itself.

Some files are fetched by the browser itself, outside the user's session. A web
app manifest and its icons are fetched by the installability check, and a
service worker by its registration. Behind the login these receive a redirect to
the identity provider, the browser cannot parse a manifest out of it, and it
refuses to install the application ("This app cannot be installed").

The files are the same static assets every signed-in browser already downloads.
They carry no user, tenant or session data. Nothing in the platform let an
application say so.

## Decision

An application declares the exact paths of its public static files:

```yaml
gateway:
  publicAssets:
    paths: [/manifest.json, /sw.js, /favicon.svg]
```

Absent or empty, nothing is rendered and nothing changes. Declared, the platform
renders, from that one per-app values file:

| Where | What |
|---|---|
| gateway (`universal-tenant`) | a separate bind, `:3003`, listener `public-assets`, outside the OIDC policy; one route to the application's `frontend-workload`; a Service port `3003` |
| public route (`tenant-public-tls`) | one HTTPRoute rule sending exactly those paths, GET and HEAD, to `:3003` |

It is the webhook bind's construction (a port with no OIDC, selected by the public
route), restricted further:

1. **Exact paths only.** No prefix, wildcard, regex, query or trailing slash, and
   only `[A-Za-z0-9._~-]` in a segment. A file added to the frontend later is not
   public until someone declares it.
2. **GET and HEAD only**, at both layers. On the route, any other method falls to
   the catch-all and the login. On the bind, it matches no route.
3. **One backend**, the application's own frontend, derived by the chart and never
   named in values.
4. **Never the app shell or a dynamic surface.** The chart refuses `/`,
   `/index.html`, and anything whose first segment is `api`, `internal`, `v1`,
   `oauth`, `health` or `healthz` (case-insensitive). It also refuses `.` and `..`
   segments, duplicates, non-strings, and more than 32 paths (each path renders two
   Gateway API matches, and a rule holds 64).
5. **Both charts refuse the same input.** Both render from the same values file,
   and the bind and the route that reaches it must agree.

Every other path stays on `:3000` behind the login, exactly as before.

### Amendment 2026-10-12: a declared directory of built files

A page served without a login (ADR-103, as amended) is the application's own
built page, and it loads its scripts and styles from a build directory whose
file names change with every build (content hashes). Exact paths cannot name
them.

An application may also declare **directories** of public static files:
`gateway.publicAssets.directories`, each one path segment with a trailing
slash, for example `/assets/`. Every file beneath a declared directory answers
GET and HEAD from the application's frontend without a login, through the same
`:3003` bind and the same public route rule.

The bounds of §1–§5 hold, with these for directories:

- **One segment, declared by name.** No wildcard, regex or nesting. Only
  `[a-z0-9._-]` in the segment.
- **Never a dynamic surface or the shell.** The refused first segments of §4
  are refused as directories too, as is a directory that is the prefix of a
  declared public route (ADR-103) or that contains a declared exact path.
- **At most 4 directories,** and at most 32 paths and directories together
  (each renders two matches, and a rule holds 64).
- **What is in it is public.** The chart cannot read the build. An application
  must not put into a declared directory anything it would not publish: a
  built page's scripts are its code, and declaring the directory publishes
  that code.

## Acceptance criteria

Without a session, for an application declaring paths:

- each declared path answers GET/HEAD from the frontend, 200 with its real
  content type;
- `/` and `/api/...` still redirect to the login;
- a non-GET/HEAD method on a declared path redirects to the login;
- an undeclared path, for example a sibling of a declared file, redirects to the
  login.

Pinned by preflight `101-public-assets` (A1–A5): the bind's shape, the OIDC
policy's single listener, route and bind agreeing on the exact (path, method) set,
nothing rendered when undeclared, and identical refusal of 20 unsafe inputs by
both charts.

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Declared public paths (`gateway.publicAssets.paths`) | tenant GitOps repository, per application | Tenant | ArgoCD | universal-tenant, tenant-public-tls | Day-1+ |
| `:3003` bind, route and Service port | platform charts | Platform | ArgoCD | the application's gateway | Day-1+ |
| Public HTTPRoute rule | platform charts (`platform-ops`) | Platform | ArgoCD | Cilium Gateway | Day-1+ |
| The files themselves | application repository | Tenant | — | browsers | Day-1+ |

## Consequences

### Positive

- An application can be installed as a PWA. The same declaration serves its
  service worker and any other file a browser fetches without a session.
- The exception is a list in git, reviewed with the application's values, and the
  chart bounds what that list can express.

### Negative

- A second unauthenticated surface on the gateway beside the webhook bind. It is
  smaller (static files, exact paths, two methods), but it is a surface.
- A declared file is public. An application must not declare a file it would not
  publish. The chart can refuse dynamic prefixes; it cannot read the files.
- Upgrading to a chart with this change restarts every tenant gateway once, as any
  edit to the gateway template does: its config checksum covers the template
  source and the values.

## Impact

- `manifests/tenants/charts/universal-tenant`: `publicAssetPaths` helper,
  `:3003` bind and Service port, `gateway.publicAssets.paths` default `[]`.
- `manifests/tenants/charts/tenant-public-tls`: the same helper and an HTTPRoute
  rule to `:3003`, with the same HSTS header as the catch-all.
- `scripts/validate/preflight/101-public-assets.sh`.
- Amendment 2026-10-12: `gateway.publicAssets.directories` in both charts'
  helper, rendered as a path-prefix match on the bind's route and on the public
  route rule; preflight `101-public-assets` pins the matches and the refusals.
- Requires the Gateway API `HTTPRouteMethodMatching` feature (Cilium 1.17 reports
  it) and AgentGateway route `matches[].method` (v1.5.0).

## References

- ADR-050: Tenant authentication via AgentGateway
- ADR-051: Environment DNS naming and public gateway TLS
- ADR-088: The tenant and the application are different axes

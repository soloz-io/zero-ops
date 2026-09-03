# ADR-059: Provider-Agnostic OIDC Contract

**Date:** 2026-09-03
**Status:** Proposed

## Context

The platform moved its identity provider from Ory (Hydra + Kratos) to Zitadel on
2026-09-03. The move worked, but it exposed how much of the platform was welded
to one provider. Every one of the following broke, and each broke *after* a login
had already succeeded — the failures land on the first API call, not at sign-in,
so none of them is visible from "can I log in?":

| what broke | why |
|---|---|
| `OIDC_ISSUER_URL` | a literal in the fleet's own manifest; the gateway moved and the BFF did not |
| audience | `waypoint-public-client` vs the token's `aud` |
| JWKS URL | `/.well-known/jwks.json` is HYDRA's convention; Zitadel serves `/oauth/v2/keys` and 404s the other |
| client id | ADR-053 derives `<tenantId>-public-client`; Zitadel allocates its own and will not accept a chosen one |
| `groups` claim | Ory injected it; Zitadel carries authorisation as a nested `urn:zitadel:iam:org:project:roles` object |
| Kubernetes API server | `--oidc-issuer-url` / `--oidc-client-id` are baked into an immutable CAPI template |

The common shape: **a provider-specific value copied into a place that had no way
to know it had changed.** Two copies of a value are two chances to disagree, and
they did.

Three facts constrain any fix. All were verified against the running system and
the Zitadel source rather than assumed:

1. **An issuer cannot be faked by a proxy.** The `iss` claim is inside the signed
   token, and a relying party compares it against its configured issuer URL
   byte-for-byte. Serving provider A's discovery document at provider B's
   hostname produces tokens every client rejects.

2. **Zitadel can mint any issuer, per request.** `internal/api/oidc/op.go:256`
   is `ContextToIssuer(ctx) = DomainContext(ctx).Origin()`, and `Origin()` is
   `protocol://RequestedHost()` (`internal/api/http/request_context.go:63`). The
   issuer follows the Host header. The host must be a registered INSTANCE domain
   — a *trusted* domain is not sufficient, verified by adding one and getting
   `unable to set instance using origin {auth.dev.nutgraf.in https}
   (ExternalDomain is id.dev.nutgraf.in): Instance not found`. Instance domains
   are addable to a live instance via the System API
   (`POST /instances/{instance_id}/domains`, `proto/zitadel/system.proto:278`).

3. **The audience cannot be made provider-independent by configuration.**
   `internal/api/oidc/access_token.go:119` is
   `token.audience = append(token.audience, clientID, projectID)` — both
   Zitadel-allocated. No setting substitutes a chosen string.

Fact 3 is the one that decides the architecture, and it is easy to miss because
the issuer looks like the hard part and turns out not to be.

## Decision

Identity is a platform contract expressed in three layers. A consumer depends on
the contract; only the platform knows the provider.

### Layer 1 — one issuer hostname, forever

`auth.<zone>` is the platform's OIDC issuer regardless of which product backs it.
It is a name the platform owns, not a name a provider gave us.

Each provider is CONFIGURED to mint it, because by fact 1 nothing else works:

| provider | how |
|---|---|
| Ory Hydra | `URLS_SELF_ISSUER=https://auth.<zone>` |
| Zitadel | register `auth.<zone>` as an instance domain and set it primary (System API) |
| any future provider | must be able to mint a caller-chosen issuer — this is now a **selection criterion**, not a detail to discover later |

A provider that cannot mint a chosen issuer is disqualified. Discovering that
after adoption is what produces a permanent redirect shim.

### Layer 2 — the platform publishes; consumers never hardcode

Every provider-specific value is published by the platform and read at runtime:

- to a fleet's workloads, on the `tenant-public-endpoint` ConfigMap:
  `OIDC_ISSUER_URL`, `OIDC_CLIENT_ID`, `OIDC_JWKS_URL`
- to the tenant chart, as environment-owned values: `oidcIssuer`,
  `oidcClientIds`, `oidcJwksUrl`, `oidcScopes`

**A consumer that can read configuration at runtime MUST NOT hardcode a provider
value.** Not the issuer, not the audience, not a well-known path, not a scope.

The same rule applies to platform SOURCE, not only to deployed config: a chart
or library that spells a provider's name in its logic has that provider welded
into the platform's contract. Where a provider's own identifiers are
unavoidable — a claim cannot be read without naming it — they are confined to a
single alias table at the adapter boundary, so supporting another issuer is a
table entry rather than an edit to the logic around it.

Two of these are published rather than derived for reasons worth stating, because
both look derivable:

- **JWKS URL.** `<issuer>/.well-known/jwks.json` is Hydra's convention, not a
  standard. Zitadel returns 404 for it.
- **Scopes.** Beyond `openid`/`profile`/`email`, which scopes a token needs is a
  property of the ISSUER, and the identifiers are its own vocabulary. A platform
  template naming them would have to be edited to change provider. Omitting one
  is silent in the worst way: the token still verifies and simply arrives without
  the claim that scope mints, so the failure surfaces later as a missing tenant
  or an empty role list rather than as a login error.
- **Client id.** ADR-053 derives `<tenantId>-public-client`, which holds only
  while the platform can CHOOSE the client id. Zitadel allocates an opaque
  number, so the identifier must be looked up. ADR-053's *authority* rule is
  unchanged — the value is still environment-owned, never fleet-supplied — only
  its origin moves from derivation to lookup.

### Layer 3 — a broker for consumers whose configuration is baked in

Layer 2 covers everything that reads config at runtime. The Kubernetes API server
does not: its `--oidc-issuer-url` and `--oidc-client-id` are process flags set
through `KubeadmControlPlaneTemplate`, whose `spec.template.spec` is immutable
(verified by server-side dry-run: *"field is immutable. Please create new
resource instead."*). Changing them means a new template, a ClusterClass
repoint, and a control-plane machine rollout.

Layer 1 fixes the issuer for it. Fact 3 means the AUDIENCE still changes on every
provider switch, so without a broker **each switch costs a control-plane
rollout** — on the hub, of a single control-plane replica.

A broker (Dex, or equivalent) terminates this. It owns `auth.<zone>`, defines
STATIC clients whose ids the platform chooses, and federates upstream to the real
provider:

```
        stable, never changes
        ┌────────────────────────────────────┐
        │ issuer:    https://auth.<zone>     │
        │ client_id: kubernetes              │
        └──────────────────┬─────────────────┘
                           │
                    ┌──────▼──────┐
                    │   broker    │  platform-owned OP
                    └──────┬──────┘
                           │  connector (swappable)
        ┌──────────┬───────┴────┬──────────┐
      Zitadel     Ory        Entra       Okta
```

The API server is configured once and never again. A provider switch becomes a
connector edit. The broker is also where a `groups` claim is normalised, which
removes the other provider-specific gap (Ory injected `groups`; Zitadel does not
emit one, and its Actions v2 would require hosting an HTTP target — v1 inline
actions are gone in v4, verified: every v1 flow-attach endpoint 404s).

**Layer 3 is adopted when the second provider switch becomes real, or at the next
control-plane rollout, whichever comes first.** It is not built now: it adds a
component to the authentication path, and paying that cost to avoid one rollout
we are not yet scheduling is the wrong trade. What Layer 3 must not become is a
surprise — it is recorded here so the next switch is a decision, not a discovery.

### Who provisions a tenant's identity

Kube-SBT, the Tenant Identity Service. ADR-041 grants it "tenant identity
lifecycle" and "Infisical upload for tenant credentials", and forbids the Hub
Operator from "acting as a secret manager for tenant or application secrets" and
Crossplane from "secret generation". Provisioning a tenant's organisation,
project, roles and OAuth application therefore belongs to that service and not to
the tenant reconciler, which is where it would naturally have been put.

The provider's client id is published to Infisical from there and reaches a
tenant's gateway through ESO as an environment value. That is the only path that
carries a value the platform LEARNS at runtime rather than decides at render
time, and it is what removes the per-tenant map that previously had to be
maintained by hand.

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Consumer |
|---|---|---|---|
| Issuer hostname | ADR-051 DNS naming | Platform | every relying party |
| Provider issuer config | provider (instance domain / `URLS_SELF_ISSUER`) | Platform | — |
| `oidcIssuer` / `oidcJwksUrl` | environment-manager values | Platform bootstrap | universal-tenant chart |
| `oidcClientIds` | environment-manager values | Platform | agentgateway |
| `tenant-public-endpoint` | universal-tenant chart | Platform | fleet workloads |
| API server OIDC flags | `KubeadmControlPlaneTemplate` | Platform (CAPI) | kubectl, Headlamp |

## Consequences

### Positive

- A provider switch touches platform configuration, not tenant manifests. The
  five failures above become one values change.
- A consumer cannot silently hold a stale provider value, because it has none to
  hold.
- The provider's own identifiers stay inside the platform boundary, so ADR-053's
  authority rule survives a provider that will not let us choose identifiers.
- "Can it mint an issuer we choose?" becomes an explicit selection criterion.

### Negative

- Until Layer 3, every provider switch costs a control-plane rollout for the
  audience change. On a single-replica control plane that is the platform's
  riskiest routine operation.
- Layer 1 requires provider-side setup that is easy to forget, and forgetting it
  is silent until a client rejects a token.
- A broker, when adopted, is a new component on the authentication path — if it
  is down, nobody logs in anywhere.
- `groups` remains provider-specific until Layer 3. ADR-058's group-based RBAC
  does not survive the move to Zitadel on its own.

## Impact

- **Amends ADR-053.** The tenant OAuth client identifier is looked up from an
  environment-owned map when the provider allocates it, rather than derived from
  the tenant id. Authority is unchanged.
- **Amends ADR-050.** `oidcIssuer` is joined by `oidcJwksUrl` and
  `oidcClientIds` as environment-owned, fleet-forbidden values.
- **Supersedes ADR-058.** Its ConfigMap-to-Kratos-to-auth-proxy pipeline is
  removed; the requirement it served — Kubernetes authorises on users and groups
  — is kept, with the mapping moved to the edge.
- **Removes the platform_admins exemption** from the JWT validator. It modelled a
  tenant-less platform identity, which the provider can no longer produce: an
  Organization owns every user, so a platform admin carries the platform
  organisation as its tenant like anyone else.
- **Depends on ADR-051** for the `auth.<zone>` name.

## References

- ADR-050: Tenant gateway OIDC policy
- ADR-051: Environment DNS naming
- ADR-053: Tenant OAuth client secret delivery
- ADR-058: OIDC group-based Kubernetes RBAC

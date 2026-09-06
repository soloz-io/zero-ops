# ADR-060: Zitadel as the Platform Identity Provider

**Date:** 2026-09-04
**Status:** Accepted

## Context

The platform authenticated tenants against Ory (Hydra for OAuth2, Kratos for
identities, Keto for relationships). In that stack a tenant is an ATTRIBUTE
written onto an identity, not a property of the identity's existence. Four
consequences followed from that single fact, each observed in the platform:

- An identity could be created with no tenant. Tokens minted for such an
  identity were rejected downstream as `Token missing required tenant_id claim`,
  after a login had already succeeded — the failure surfaces on the first API
  call, not at sign-in.
- A platform administrator legitimately belongs to no tenant, so the rule "every
  token carries a tenant" made administrators unrepresentable. The platform
  added a group-based exemption to admit them, which is a hole widened by
  anything able to claim membership of that group.
- Authorisation had no home in the identity provider, so the platform built one:
  a ConfigMap of group assignments reconciled into Kratos `metadata_public` by a
  controller in the hub-operator (ADR-058). This modelled authorisation BESIDE
  the provider rather than in it, and the two could drift with nothing to
  reconcile them.
- Self-service registration produced identities with no tenant, since nothing in
  the registration flow carried one.

ADR-057 established tenant user management on the Ory stack. ADR-058 established
group-based Kubernetes RBAC on top of it. ADR-041 assigns tenant identity
lifecycle to the Tenant Identity Service and forbids the Hub Operator from
managing tenant credentials. ADR-053 derives a tenant's OAuth client identifier
from its tenant id.

## Decision

The platform's identity provider is Zitadel, selected because its tenancy is
STRUCTURAL: an Organization owns a user rather than describing one. A user
belonging to no tenant is not expressible, so the four consequences above cease
to be conditions the platform must detect and become states the provider cannot
produce.

The platform adopts the provider's own model rather than reproducing its
previous one:

| Platform concept | Provider mechanism |
|---|---|
| Tenant | Organization, which owns the user and appears as the token's resource owner |
| Authorisation | Project roles granted to a user within an organisation |
| Tenant administrator | Organization owner membership, scoped to one organisation |
| Cross-tenant access | Project grant from the owning organisation to another |
| Tenant discovery at login | Organization scope on the authorization request |
| Registration policy | Organization-level login policy |
| Platform's own identities | The platform is itself an organisation, with no special case |

Two provider-side checks move tenant isolation in front of token issuance. One
denies authentication to a user holding no role in the project; the other denies
it to a user whose organisation was never granted the project, with the owning
organisation passing implicitly. A user of another tenant is refused at the
authorization endpoint, so no token reaches an application that would otherwise
have to check — and a check that lives in an application is one an application
can omit.

Because the provider denies a user holding no role, granting a role is part of
creating a usable account rather than a later step. Accounts arriving by
self-service registration have no such grant and receive the default role by
reconciliation, since the provider exposes no event at sign-up. This makes the
grant eventually consistent, which is accepted deliberately: the alternative is
disabling the role check, and that check is also what refuses users of other
organisations.

Group-based RBAC is not reproduced. Kubernetes authorises on users and groups,
and the provider emits roles as a nested structure keyed by granting
organisation, so the claim cannot be consumed by the API server directly. Until
that mapping exists, Kubernetes RBAC binds to the username and the group claim
is left unset rather than pointed at a claim that yields nothing.

### OAuth client provisioning inverts

A tenant needs two OAuth clients, and they differ in a way that decides who
creates them. The gateway authenticates a PERSON with a public client, where PKCE
carries the proof and no secret exists. The BFF then exchanges that session for a
token of its own to call the platform API with — a server-side flow, which by
definition authenticates the CLIENT and therefore needs a secret.

Under Hydra the platform was the producer of that secret: it generated one into
the tenant's Infisical path and hydra-maester reconciled an `OAuth2Client` CR to
push it into the issuer. That is why clients could be declared per fleet as a
list — each entry cost only a generated value and a CR.

Zitadel will not accept a supplied client secret. It generates one and discloses
it exactly once, at creation. The direction of the credential therefore inverts:
the issuer is the producer, and the platform's job is to capture the value and
publish it. There is no configuration that restores the old direction, so this is
a structural consequence of the provider rather than a preference.

Three things follow.

**The Tenant Identity Service provisions both clients**, because it is already the
component that talks to the issuer and already publishes an allocated value
(ADR-041 assigns it both identity lifecycle and credential upload). The Hub
Operator stops generating OAuth credentials entirely. Two producers writing the
same key would take turns overwriting each other, and the workload would hold
whichever wrote last while the issuer knew the other — a mismatch that surfaces
only at token exchange, naming neither writer.

**The client set is fixed rather than declared.** The platform creates
`<tenant>-public-client` and `<tenant>-bff`, which are the two the architecture
uses. A fleet-declared list no longer buys anything, since a fleet cannot supply
the credential, and it cost something: a list a fleet could extend was a list a
fleet could use to register redirect URIs the platform never reviewed. A third
client is a change to the service.

**Reads precede writes.** Because a generated secret cannot be read back, the only
way to recover one is to regenerate it — which invalidates the credential the
running workload holds. Provisioning therefore asks the secret store first and
regenerates only when nothing is stored, so a reconcile of an already-provisioned
tenant touches nothing.

### How a tenant request is authenticated

```
                      ┌──────────────────────────────────────────┐
   browser ──(1)────► │  agentgateway (tenant namespace, spoke)  │
                      └────┬────────────────────────────┬────────┘
                           │ (2) OIDC authorization     │ (5) forwards id_token
                           ▼                            ▼
                  ┌─────────────────┐            ┌─────────────┐
                  │     Zitadel     │            │     BFF     │
                  │  id.<zone>      │◄──(6)──────┤             │
                  │                 │  code      └──────┬──────┘
                  │  org  = tenant  │  exchange         │ (7) delegated token
                  │  proj = roles   │  (confidential)   ▼
                  └────────┬────────┘            ┌─────────────────┐
                           │ (3) iss / JWKS      │  api.<zone>     │
                           │                     │  MCP gateway    │
                           ▼                     └─────────────────┘
                  /oauth/v2/keys ──(4)──► validated by gateway AND BFF

  Provisioning (out of band, on tenant reconcile):

    AINativeSaaS XR ──► Hub Operator ──► Tenant Identity Service
                                                  │
                            creates in Zitadel:   │
                              <tenant>-public-client   (auth method NONE)
                              <tenant>-bff             (auth method BASIC)
                                                  │
                            publishes to Infisical:
                              /spoke-pool/<cell>/tenants/<tenant>/
                                  OIDC_CLIENT_ID
                                  OAUTH_BFF_CLIENT_ID
                                  OAUTH_BFF_CLIENT_SECRET
                                                  │
                                                  ▼
                            ExternalSecret ──► tenant namespace Secret
                                                  │
                                                  ▼
                            agentgateway reads OIDC_CLIENT_ID
                            BFF reads WAYPOINT_BFF_CLIENT_ID / _SECRET
```

Steps (1)–(4) are the browser login: the gateway sends the user to Zitadel, and
both the gateway and the BFF validate the returned token against the issuer's
signing keys at `/oauth/v2/keys`. Zitadel returns 404 for the conventional
`/.well-known/jwks.json`, which is why the JWKS URL is published beside the issuer
rather than assembled from it. Steps (5)–(7) are the delegated exchange, and are
the reason the confidential client exists at all.

The `auth.<zone>` hostname is split rather than retired. Its `/.well-known/` and
`/internal/` prefixes are served by auth-proxy, which passes Zitadel's discovery
document through unmodified — the document must keep naming `id.<zone>` as the
issuer, because that is what tokens carry and what a relying party compares
byte-for-byte. Everything else on that hostname redirects to Zitadel, which hosts
its own login.

### Configuration abstraction

Identity configuration is split by who may decide it, extending the boundary
ADR-050 draws for the tenant gateway. A fleet declares what it wants for its own
tenant; the platform decides how that is delivered and what a fleet may not
reach.

Platform-owned, supplied by the environment and never by a fleet:

| Setting | What it fixes |
|---|---|
| Issuer URL | Which identity provider a spoke trusts |
| JWKS URL | Where that issuer publishes signing keys, which is not derivable from the issuer |
| Additional scopes | Which provider-specific claims a token must carry |
| Legacy stack enablement | Whether the superseded provider is deployed at all |

A fleet able to set any of these could point its own login flow at an issuer the
platform does not trust.

Fleet-owned, declared on the tenant record:

| Setting | What it decides |
|---|---|
| Gateway enablement and hostnames | Whether this fleet has a browser surface, and the names it answers on |
| Owner email | Who administers this tenant |
| Self-service registration | Whether people may create their own accounts in this tenant |
| OAuth clients | Which clients this fleet needs credentials for (ADR-053) |

Allocated by the provider and published to a fleet's workloads, because the
values do not exist when a fleet's manifests are rendered:

| Value | Delivery |
|---|---|
| Issuer URL, JWKS URL, public base URL | ConfigMap in the tenant namespace |
| OAuth client id, organisation id | Secret, via the platform's secret path (ADR-003) |

The client id is published rather than derived because the provider allocates it
and will not accept a chosen one. ADR-053's authority rule is unchanged — the
value remains platform-owned — but its origin moves from derivation to lookup.

### Alternatives considered

**Remain on Ory and add tenant enforcement.** Rejected: every mechanism above
would have to be built and maintained by the platform — a required-claim check,
an exemption for identities the rule cannot represent, a reconciler for group
assignments, and a registration flow that carries a tenant. Each is a place the
platform's model can drift from the provider's.

**A broker in front of the provider.** Recorded in ADR-059 as the answer for
consumers whose configuration is baked in rather than read at runtime. Not
adopted here: it adds a component to the authentication path, and the only
consumer needing it today is the Kubernetes API server, which one control-plane
rollout already serves.

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Tenant Organization | Identity Provider | Tenant Identity Service | Tenant Identity Service | Tenant users | Day-1+ |
| Tenant Project and Roles | Identity Provider | Tenant Identity Service | Tenant Identity Service | Tenant gateway, tenant workloads | Day-1+ |
| Tenant OAuth Client | Identity Provider | Tenant Identity Service | Tenant Identity Service | Tenant gateway | Day-1+ |
| Tenant OAuth Client ID | Infisical | Tenant Identity Service | Hub Operator | Tenant gateway, fleet workloads | Day-1+ |
| Tenant Role Grants | Identity Provider | Tenant Identity Service | Tenant Identity Service | Identity Provider | Day-1+ |
| Organization Login Policy | Identity Provider | Tenant Identity Service | Tenant Identity Service | Identity Provider | Day-1+ |
| Platform OAuth Clients | Identity Provider | Tenant Identity Service | Tenant Identity Service | Kubernetes API server | Day-1+ |
| Issuer Endpoint Configuration | Bootstrap artifacts | CLI | Environment Manager | Tenant gateways, fleet workloads | Day-0 |
| API Server OIDC Configuration | Cluster topology | CLI | CAPI topology controller | kubectl, dashboards | Day-0 |

## Consequences

### Positive

- A tenant-less identity is not expressible, so the class of failure that
  produced repeated post-login rejections cannot occur.
- Tenant isolation is enforced before a token exists, so an application cannot
  omit the check.
- A platform administrator is an ordinary member of the platform's own
  organisation, which removes the exemption that previously admitted them.
- Authorisation lives in one model rather than two, so there is nothing to keep
  in step and nothing to drift.
- Registration is decided per tenant, so one fleet's choice does not bind
  another and the platform's own organisation stays closed regardless.

### Negative

- Role grants for self-registered users are eventually consistent. A person
  registering between reconciliations is refused until the next one.
- Self-service registration does not restrict who may sign up. Anyone reaching a
  tenant's hostname can create an account in it, and restricting that requires
  invitations, which require a mail sender the issuer does not have.
- Group-based Kubernetes RBAC does not survive the change, so cluster access is
  bound per identity rather than per group.
- The provider allocates identifiers the platform previously chose, so values
  that were derived must now be published through the credential path.
- Organizations, projects and policies have no declarative resource type, so
  desired state is expressed on the tenant record and reconciled through the
  provider's API rather than applied directly.

## Impact

- **Completes the supersession of ADR-058** recorded by ADR-059. Its
  ConfigMap-to-identity-metadata pipeline is removed, and the requirement it
  served — that Kubernetes receives a group claim — remains unmet pending a
  mapping from the provider's role structure.
- **Amends ADR-057.** Tenant users are owned by an organisation rather than
  carrying a tenant attribute, and the platform no longer maintains that
  attribute.
- **Supersedes ADR-053's mechanism.** A tenant's OAuth client identifier is
  allocated by the provider rather than derived from the tenant id, and the
  confidential client's secret is GENERATED BY THE PROVIDER rather than minted by
  the platform and pushed. The hydra-maester `OAuth2Client` pipeline and the
  per-fleet client list are removed with it; the requirement ADR-053 served — that
  a tenant's server-side client has a credential nothing else can read — is kept.
  Authority over the value is unchanged.
- **Amends ADR-050.** The environment supplies a JWKS URL and additional scopes
  alongside the issuer, all fleet-forbidden.
- **Depends on ADR-059** for the contract that makes the provider replaceable,
  and for the constraint that a provider must be able to mint an issuer the
  platform chooses.
- **Depends on ADR-041** for the assignment of tenant identity lifecycle, which
  places provisioning in the Tenant Identity Service rather than in the Hub
  Operator or Crossplane.

## Open

**The MCP gateway does not validate an audience.** Hydra minted whatever audience
the consent step granted, so `https://api.<zone>/mcp` could simply be required.
Zitadel mints the client id and, with the project-audience scope, the project id —
both allocated, so neither can be written into a manifest at render time. Keeping
the old value would reject every token; omitting it means the gateway validates
the issuer and the signature but not the intended recipient, so a token minted for
another relying party of the same issuer is accepted. The issuer is the platform's
own, which bounds the exposure, but it is a real reduction and it is recorded here
rather than absorbed silently.

Closing it needs the platform project id published the way `OIDC_CLIENT_ID`
already is, and the authorization request carrying
`urn:zitadel:iam:org:project:id:<projectID>:aud` so the claim is present to check.

## References

- ADR-003: Secret Management Architecture
- ADR-039: Platform Ownership Model
- ADR-040: Day-0 vs Day-1 Lifecycle Boundary
- ADR-041: Controller Responsibility Matrix
- ADR-043: Control Plane Authority Model
- ADR-050: Tenant Authentication via AgentGateway
- ADR-053: Tenant OAuth Confidential Client Lifecycle
- ADR-057: Tenant User Management
- ADR-058: OIDC Group-Based Kubernetes RBAC (superseded by ADR-059)
- ADR-059: Provider-Agnostic OIDC Contract

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
- **Amends ADR-053.** A tenant's OAuth client identifier is looked up from the
  provider when the provider allocates it, rather than derived from the tenant
  id. Authority over the value is unchanged.
- **Amends ADR-050.** The environment supplies a JWKS URL and additional scopes
  alongside the issuer, all fleet-forbidden.
- **Depends on ADR-059** for the contract that makes the provider replaceable,
  and for the constraint that a provider must be able to mint an issuer the
  platform chooses.
- **Depends on ADR-041** for the assignment of tenant identity lifecycle, which
  places provisioning in the Tenant Identity Service rather than in the Hub
  Operator or Crossplane.

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

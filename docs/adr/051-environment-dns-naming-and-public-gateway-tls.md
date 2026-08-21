# ADR-051: Environment DNS Naming and Spoke-Terminated Public Ingress

**Date:** 2026-08-21
**Status:** Accepted
**Relates to:** ADR-035 (enterprise PKI), ADR-037 (environment promotion), ADR-039 (ownership model), ADR-043 (control plane authority), ADR-046 (hybrid provider cell), ADR-049 (load balancer consolidation), ADR-050 (tenant authentication via AgentGateway)

---

## Context

Public hostnames have no naming convention and no single owner, and the placement of the
public entry point was unresolved. The following are observed in the platform, not
assumptions:

- Every environment resolves to the same hostnames, so dev, stg and prod cannot coexist.
  ADR-037 mandates strictly segregated environments and an environment-bound Hub per
  environment; the DNS layer does not reflect that.
- There is no base-domain authority. The `DOMAIN` bootstrap key and
  `HubEnvironment.spec.domain` both exist and neither has any consumer. Hostnames are
  literals distributed across the hub edge manifests, the identity stack, the gateway
  routing configuration, and compiled Go constants.
- Public TLS is issued by the private Fleet Intermediate CA (ADR-035), which no browser
  trusts, and the intermediate is absent from the served chain. Plaintext HTTP is
  reachable; HTTPS is rejected by every public client.
- DNS records are maintained out of band. external-dns is present in the repository, is
  referenced by no ApplicationSet, and has never run, so the external-dns annotations
  already carried on tenant Gateways have no effect.
- Two candidate termination points are configured simultaneously. Public tenant hostnames
  resolve to a spoke and are served by spoke routes, while the hub gateway is independently
  configured for the same hostnames with no reachable backend.
- Tenant workloads run on home-lab worker nodes (ADR-046) that are NAT-bound and reachable
  only across the tailnet, which the hub does not join.

ADR-050 requires that a tenant BFF never be publicly reachable except through an
authenticating gateway. It expressed that requirement as hub-terminated ingress, which
made the management cluster a dependency of every tenant request.

## Decision

**Environment is a DNS zone, and production is unlabelled.** Each non-production
environment owns a zone under the platform apex; production uses the apex directly. Tenant
identity is the leftmost label, so a tenant's name is stable across environments and the
environment is the varying component.

The alternative considered was labelling the tenant rather than the zone. It was rejected
because a per-environment zone admits a single wildcard certificate covering every tenant in
that environment, whereas a per-tenant label requires one wildcard per tenant, and the
platform already hosts more than one tenant.

**Public ingress terminates at the spoke that runs the workload.** Each spoke operates its
own authenticating gateway; the hub retains only platform services and the MCP
resource-server endpoint. ADR-050's requirement is satisfied unchanged — the tenant BFF is
reachable only through a gateway — while the management cluster leaves the tenant data
path.

Hub termination was the previously stated design and is rejected. It conflated the policy
enforcement point with the network path: enforcing an authentication policy does not require
routing traffic through the cluster that holds the platform's root of trust. Doing so made
the hub a single point of failure for every tenant, placed tenant traffic in the same blast
radius as the PKI and secret store, and required a cross-cluster transport that does not
exist between the hub and NAT-bound home workers.

**Placement is expressed in DNS, and DNS is derived from gateway state.** A tenant hostname
resolves to the load balancer of the spoke hosting that tenant. Records are reconciled from
the routing objects present on each spoke; because a tenant's routing objects exist only on
its owning spoke, the record sets are disjoint by construction rather than by coordination.
Ownership metadata scoped per cluster prevents one spoke from editing another's records.
Relocating a tenant is therefore a consequence of moving its workload, not a separate
registration step.

**A single spoke serves multiple tenants.** Isolation is per-hostname: each tenant has its
own routing entry, its own authentication policy, and its own OAuth client. Browser sessions
are additionally host-scoped, so a session issued for one tenant hostname is never presented
at another.

**Routing to the gateway is platform-owned; tenants declare data, not routes.** The
gateway and the route binding a public hostname to it are platform resources in a platform
namespace. Tenants declare their hostname and backend services as configuration, which the
platform renders into gateway routing. A tenant therefore cannot author a route that
reaches its own backends without traversing the gateway.

The alternative — tenant-authored routes with an explicit cross-namespace reference grant —
was rejected. It preserves tenant ownership of routing but reduces ADR-050's
no-bypass requirement from a structural property to a reviewable one, and it adds a
platform-side consent object per tenant. Where routing is a security control, ownership
belongs with the control. The cost is that adding a hostname becomes a platform-side change
rather than tenant self-service.

**Public certificates are issued by a public ACME certificate authority.** ADR-035's
Infisical PKI remains the authority for internal fleet and mTLS certificates. These are
complementary authorities separated by audience: a certificate presented to an untrusted
public client requires a publicly trusted chain; one presented to a fleet peer does not.
Per the Universal PKI Rule, cert-manager remains the only issuer of either class.

**Challenge solving is bound to the tenant whose hostname is being validated.** Each
tenant's solver targets that tenant's own gateway, selected by DNS zone. No default solver
exists: a hostname matching no tenant fails validation rather than attaching a challenge to
an unrelated tenant's gateway.

**The environment overlay is the system of record for the base domain.** The
zone-to-environment mapping is declared once per overlay. Hostname literals distributed
across manifests and compiled into binaries are debt to be retired against this authority;
until they are, the overlay value is declarative and the literals remain authoritative in
practice. This ADR does not treat that as an acceptable end state.

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Zone-to-environment mapping | Git (environment overlay) | Platform Engineering | ArgoCD | external-dns, cert-manager | Day-1+ |
| Public tenant hostname records | Provider DNS zone | Platform Networking | external-dns (spoke, zone-scoped) | Public clients | Day-1+ |
| Public browser-facing certificates | ACME certificate authority | Platform Security | cert-manager | Spoke gateway listener | Day-1+ |
| Internal fleet / mTLS certificates | Infisical PKI (ADR-035) | Platform Security | cert-manager | Fleet components | Day-1+ |
| Gateway listener TLS material | Tenant-namespace Secret | cert-manager | CNI operator secret sync | Gateway data plane | Day-1+ |
| DNS provider API credential | Infisical (ADR-003) | Platform Security | ESO | external-dns | Day-1+ |
| Tenant ACME solver binding | Git (spoke catalog) | Platform Engineering | ArgoCD | cert-manager | Day-1+ |
| Spoke authenticating gateway + its hostname route | Git (spoke catalog) | Platform Engineering | ArgoCD | Public clients | Day-1+ |
| Tenant hostname and backend declaration | Git (fleet registry) | Tenant | ArgoCD (rendered into gateway config) | Spoke gateway | Day-1+ |

Gateway listener TLS material is written by cert-manager into the gateway's own namespace
and copied into the CNI secret namespace by its operator. That copy is machine-managed state
with no independent system of record and must not be authored or tracked by GitOps.

## Consequences

### Positive

- Environments coexist on distinct hostnames, satisfying the segregation ADR-037 requires at
  the layer that previously ignored it.
- The management cluster leaves the tenant data path. A spoke failure affects only its own
  tenants, and tenant traffic no longer shares a blast radius with the PKI and secret store.
- No cross-cluster transport is required for tenant traffic, removing a dependency that did
  not exist between the hub and NAT-bound home workers.
- Public endpoints become trusted by default clients, removing the interstitial that made
  every environment appear compromised to a browser.
- DNS records derive from gateway state rather than manual action, removing a step that is
  silent when skipped and is otherwise detected only as an outage.
- A tenant cannot construct a route that bypasses the authenticating gateway, so ADR-050's
  no-bypass requirement holds structurally rather than by review.
- Tenant relocation is a consequence of moving the workload; no OAuth client changes,
  because client redirect URIs are keyed to the tenant hostname rather than to a cluster.
- A single wildcard per environment becomes available without restructuring hostnames again.

### Negative

- Each spoke operates an authenticating gateway, its own session-encryption secret, and its
  own certificate issuance. More instances to run than a single hub gateway, though each
  fails independently.
- A spoke's gateway is a shared failure domain for the tenants on that spoke. Narrower than
  a hub gateway shared across all tenants everywhere, but not per-tenant isolation.
- Public certificate issuance introduces a dependency on an external certificate authority
  in the ingress path, with rate limits that constrain iteration.
- Tenant onboarding now includes a DNS and certificate step; adding a tenant without it
  produces a hostname that cannot be validated.
- Tenants can no longer self-serve routing changes. Adding or changing a hostname is a
  platform-side change, which is slower than tenant-authored routes.
- A tenant spanning multiple spokes has no answer here. Round-robin records carry no health
  awareness, so that topology would require global load balancing.
- Production retaining the unlabelled apex means production is the one environment whose
  hostname is not derivable by the same rule, so the derivation carries a permanent
  exception.
- The base-domain authority is declarative until the distributed literals are retired, so
  two sources of truth coexist in the interim.

## Impact

**Amends ADR-050.** Its security requirement is unchanged and still enforced; its
*placement* of the gateway is superseded. Where ADR-050 names the hub gateway in a security
context, an authenticating gateway on the spoke satisfies the same contract. The
Multi-Cluster Service export ADR-050 lists as an outstanding prerequisite is void, as are
the ClusterMesh and cross-cluster transport it implied. ADR-050 carries a corresponding
amendment dated 2026-08-21.

**Amends ADR-046.** Tenant hostname records move from manual maintenance to reconciled
ownership. Its use of the private fleet issuer for browser-facing listeners is superseded;
its use for internal fleet certificates is unchanged. No change to the tailnet topology is
required — the hub does not join the tailnet, because no cross-cluster path is needed.

**Narrows ADR-035 by audience, not by authority.** It remains the authority for internal
PKI.

**Environment matrix correction.** The staging overlay declared no environment value for
the base domain and inherited the production apex. Production inherits it correctly and by
intent; staging did not, and claimed the production domain. The same class of defect
applied to the environment slug used for secret resolution, where every environment resolved
to the development value.

## References

- ADR-003: Secret Management Architecture
- ADR-035: Enterprise PKI and Delegated Trust via Infisical OSS
- ADR-037: Directory-Based Environment Promotion and Gating
- ADR-039: Platform Ownership Model
- ADR-040: Day-0 vs Day-1 Lifecycle Boundary
- ADR-041: Controller Responsibility Matrix
- ADR-043: Control Plane Authority Model
- ADR-046: Hybrid Provider Cell (Hetzner Control Plane + Home-Lab Workers)
- ADR-049: Dev Infrastructure Simplification, Managed Telemetry, and Load Balancer Consolidation
- ADR-050: Tenant Authentication via AgentGateway

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

On a multi-tenant spoke, "the tenant's own gateway" is the spoke's shared authenticating
gateway (`tenant-gateway`); DNS-zone selection is the tenant-binding mechanism. The solver
attaches a temporary HTTPRoute in `platform-ops` (the Certificate's namespace) to the
gateway's `:80` listener, which satisfies `allowedRoutes.namespaces.from: Same`. A hostname
that matches no declared DNS zone fails validation. The zone-scoped solvers in
`acme-cluster-issuer.yaml` implement this binding.

(Superseded in part 2026-08-25 — see *DNS-01 solver for hybrid home-worker spokes*:
on hybrid spokes the challenge mechanism is DNS-01, so no solver HTTPRoute is created
and the `:80` listener is not involved in validation at all. The tenant-binding rule
and the zone-scoped selector are unchanged; only the mechanism differs.)

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
| Gateway listener TLS material | platform-ops Secret (rendered from fleet-registry `public.hosts` via dedicated ApplicationSet) | cert-manager | ArgoCD (tenant-public-tls) | Spoke gateway listener | Day-1+ |
| DNS provider API credential | Infisical (ADR-003) | Platform Security | ESO | external-dns | Day-1+ |
| Tenant ACME solver binding | Git (spoke catalog) | Platform Engineering | ArgoCD | cert-manager | Day-1+ |
| Spoke authenticating gateway + its hostname route | Git (spoke catalog) | Platform Engineering | ArgoCD | Public clients | Day-1+ |
| Per-spoke TLS termination gateway + per-tenant listeners | Git (fleet-registry `public.hosts` × environment-overlay issuer) | Platform Engineering | ArgoCD (tenant-public-tls) | Gateway data plane | Day-1+ |
| Tenant hostname and backend declaration | Git (fleet registry) | Tenant | ArgoCD (rendered per tenant, composed into gateway config) | Spoke gateway | Day-1+ |
| Gateway base configuration | Git (spoke catalog) | Platform Engineering | ArgoCD | Spoke gateway | Day-1+ |

Gateway listener TLS material is written by cert-manager into `platform-ops`, the same
namespace as the shared per-spoke tenant gateway, under the `allowedRoutes.namespaces.from:
Same` invariant. The Certificate is rendered by a dedicated ApplicationSet (`tenant-public-tls`)
that derives `dnsNames` from fleet-registry `public.hosts` declarations. That rendering is
the applied form; the fleet-registry values file remains the system of record. The material
is machine-managed state and must not be authored or tracked by GitOps beyond the rendered
Certificate manifest.

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
  (Corrected 2026-08-24 — see *base-domain authority and the derivation boundary*: a wildcard
  name is admitted by the zone structure, but issuing one is not possible under the solver
  model decided above.)

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
  platform-side change, which is slower than tenant-authored routes. (Softened 2026-08-24 —
  the `tenant-public-tls` ApplicationSet auto-renders listeners and certificates from
  fleet-registry `public.hosts` declarations, so the certificate and listener binding
  steps are automated. Remaining platform-side work is DNS reconciliation and HTTPRoute
  placement, which are governed by the spoke catalog.)
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

### Addendum — the DNS reconciler's provider API is an open question (2026-08-23; resolved 2026-08-24, see below)

The Ownership table above assigns *Public tenant hostname records* to **external-dns
(spoke, zone-scoped)**, and *DNS provider API credential* to ESO. Both rows have a
dependency this ADR did not record.

Hetzner has retired the standalone DNS API. `dns.hetzner.com/api/v1/zones` now 301s
to `console.hetzner.com`; zone and record management moved to the Cloud API
(`api.hetzner.cloud/v1/zones`, `.../zones/{id}/rrsets`), reachable with the same
`hcloud-token` the platform already holds. ADR-046 §26.1 records the verification.

The spoke runs `external-dns-hetzner-webhook:v0.7.0`, whose only credential input is
`HETZNER_API_KEY` and which exposes no API-URL override — so its endpoint is compiled
in. **Whether that endpoint is the retired API is unverified**: the webhook has never
executed, because the spoke `ClusterSecretStore` is `Ready=False`, so the
`hetzner-dns` Secret never materialised and the container sits in
`CreateContainerConfigError` (ADR-046 §26.2).

Consequences for this ADR:

- The "Public tenant hostname records / external-dns" row is **contingent** on the
  webhook speaking the current API. Confirm that before treating DNS reconciliation
  as automated.
- The "DNS provider API credential" row is satisfied by `hcloud-token`. There is no
  separate DNS credential to provision; ADR-046 §20.4, which claimed otherwise, is
  superseded.
- Until the webhook question resolves, zone records are **operator-managed**, and no
  automated publisher exists for hub hostnames at all — ADR-046 §20.3 notes the hub
  external-dns copy has never been referenced by any ApplicationSet.


### Addendum — base-domain authority and the derivation boundary (2026-08-24)

The Decision states that the environment overlay is the system of record for the base domain, but
the Context names two candidates with no consumer — the `DOMAIN` bootstrap key and
`HubEnvironment.spec.domain` — and does not choose between them. That ambiguity is closed here.

**`HubEnvironment.spec.domain` is the sole authoritative base-domain value for hub public
endpoints.** It is declared once per environment overlay. The `DOMAIN` bootstrap key is retired as
an authority and is not a permitted fallback; a second consulted input would reintroduce the
condition this ADR exists to remove.

**Environment identity has a single authority, and services consume it.** The environment a Hub
belongs to is declared once on the environment object and is not restated by the components that
happen to need it. It had previously existed only as an attribute of the secret store's
configuration, alongside a bootstrap key and an overlay directory name; deriving the public DNS
contract from any of those would couple it to one component, leave it undefined wherever that
component's optional configuration is absent, and break if that component were replaced. The
environment is a property of the Hub, not of a service running on it.

**Hub public endpoints are derived, not authored.** The endpoint set for a hub — its ingress
hostnames, browser-facing certificate names, identity issuer and base URLs, gateway token issuer and
audience, secret-store endpoint, and the endpoint value passed into tenant compositions — is derived
from the authoritative value into generated GitOps artifacts, which are the applied form. No
individual hostname is independently declared, and no consumer derives the domain for itself.

Derivation is bound to environment creation rather than continuous reconciliation, because the base
domain is fixed for the life of an environment. It therefore belongs to the Day-0 boundary and its
output is an immutable input to Day-1 reconciliation. This also keeps write access to the Git system
of record out of any continuously running controller, which the Day-0/Day-1 boundary already
forbids.

The derived artifacts are produced by the environment bootstrap tooling that already owns Day-0
generated GitOps artifacts, into the generated boundary of the repository, and are committed there.
Git remains their system of record and ArgoCD their sole reconciler. A generated artifact that has
drifted from the authoritative value is corrected by regeneration at that boundary, never by editing
the artifact and never by a controller writing it.

**The reconciler is unchanged.** Derivation produces artifacts; it does not reconcile live
resources. The Ownership rows above are extended, not reassigned, and no controller responsibility
changes.

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Hub base-domain declaration | Git (environment overlay) | Platform Engineering | ArgoCD | Hub endpoint derivation | Day-1+ |
| Derived hub public endpoint set | Git (generated artifact) | Platform Engineering | ArgoCD | Hub ingress, identity, gateway, secret-store issuer, composition input | Day-0 input, Day-1+ applied |

**Wildcard issuance is not available under the decided solver model.** The zone-per-environment
structure admits a single wildcard *name* covering an environment, and that property is part of the
rationale for labelling the zone rather than the tenant. It does not follow that such a certificate
can be issued. Challenge solving is decided above as bound to the tenant whose hostname is being
validated and targeted at that tenant's own gateway, which is an HTTP challenge; the ACME protocol
admits wildcard identifiers only under a DNS challenge. Issuing an environment-wide wildcard would
therefore require introducing a DNS-based challenge and a solver not bound to a single tenant's
gateway, which this ADR does not decide. Until then the wildcard is a latent property of the naming
scheme, not an available capability, and a wildcard hostname on a gateway listener is a routing
match rather than evidence that a wildcard certificate exists.

**Scope is the hub.** Public tenant hostname records remain derived from spoke gateway state under
the rows already defined. Hub derivation must not emit tenant hostnames, and the disjointness of the
two record sets remains a property of where each is derived rather than of coordination between
them.

The distributed literals remain authoritative in practice until retired against this authority.

Retirement is judged against authorship, not against the presence of a hostname. A generated
artifact is the applied form and necessarily carries the resolved hostname; that is derived output,
not an independent declaration. The debt is discharged when no authored shared hub manifest and no
compiled binary independently defines an environment-specific public hostname, every such hostname
appearing in a generated artifact is mechanically derived from the authoritative value, and an
environment created for staging resolves no development hostname.

### Addendum — the DNS reconciler's provider API question is resolved (2026-08-24)

The 2026-08-23 addendum recorded the spoke DNS webhook's provider endpoint as unverified and treated
the automated-publisher rows as contingent on it. That contingency is discharged.

The spoke webhook no longer depends on the retired standalone DNS API, and its credential input is
the token the platform already holds. Hub hostname records are now published by a reconciled
publisher rather than out of band, and the hub publisher is referenced by an ApplicationSet — both
the statement here that no automated publisher exists for hub hostnames, and the corresponding
statement in ADR-046 §20.3, are superseded.

The "Public tenant hostname records" and "DNS provider API credential" rows stand as written, no
longer contingent.


### Addendum — DNS-01 solver for hybrid home-worker spokes (2026-08-25)

The Decision selected HTTP-01 (`gatewayHTTPRoute`) as the challenge solver and deferred
the question of DNS-01 (see *base-domain authority and the derivation boundary*, lines
287–293: "Issuing an environment-wide wildcard would therefore require introducing a
DNS-based challenge and a solver not bound to a single tenant's gateway, which this ADR
does not decide").

That deferral assumed the spoke's `:80` listener was publicly reachable. ADR-046 §8
(hostNetwork + Hetzner LB) changed that assumption for hybrid spokes: the embedded Cilium
Envoy binds `:80`/`:443` directly on the Hetzner control-plane node, which is the sole
bearer of the public LB IP. Home-worker nodes (`workload-location: home`) carry Tailscale
IPs (`100.x.x.x`) with no public internet ingress; the Cilium Gateway service in
hostNetwork mode is `ClusterIP`, not `LoadBalancer`, so no NodePort or LB VIP exists on
those nodes. Let's Encrypt's ACME servers cannot reach the HTTP-01 solver pods via the
public internet, producing a permanent `503` on every challenge.

**DNS-01 is the solver for hybrid provider spokes. HTTP-01 remains the solver for
pure-Hetzner spokes**, where every worker carries a Hetzner-assignable public IP and the
Gateway service can be exposed as a `LoadBalancer`.

Solver selection is provider-scoped, declared in `infra/acme-cluster-issuer.yaml` (the
shared base). On a hybrid spoke the base is included as-is. On a pure-Hetzner spoke the
HTTP-01 revert is to be applied by an environment overlay
(`environments/{dev,stg,prod}/hetzner/acme-issuer-patch.yaml`) — **not yet written**;
until it exists, every deployed spoke runs DNS-01, which is correct for hybrid and
harmless on Hetzner spokes (DNS-01 works wherever the zone credential exists).

**Implementation** (corrected 2026-08-25 after first deployment attempt). The webhook is
the OFFICIAL `github.com/hetzner/cert-manager-webhook-hetzner` **v0.9.0**, vendored from
its Helm chart as static manifests at
`manifests/spoke/spoke-catalog/provider/hetzner/cert-manager-webhook-hetzner.yaml` — an ArgoCD
Application resource delivered through spoke-catalog would land on the SPOKE, where no
ArgoCD controller exists to act on it. The official build is load-bearing: its hcloud-go
v2 client speaks the Hetzner **Cloud API** (`api.hetzner.cloud`, Bearer token), which is
the only API the platform `hcloud-token` still authenticates against since the standalone
`dns.hetzner.com` API was retired; third-party forks pinned to that retired endpoint
(the earlier vadimkim 1.4.x draft) can never issue. Solver config uses the upstream
`tokenSecretKeyRef{name,key}` schema referencing `hetzner-dns`/`api-key`; no `zoneName`
is set because the webhook discovers zones from the API. The Secret is mirrored into the
`cert-manager` namespace by an ExternalSecret pulling the same `hcloud-token` key used by
external-dns (ADR-003; same credential, no new secret).

**Updated solver ownership rows:**

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Tenant ACME solver — hybrid spokes | Git (spoke catalog infra base) | Platform Engineering | ArgoCD | cert-manager + Hetzner DNS-01 webhook | Day-1+ |
| Tenant ACME solver — Hetzner spokes | Git (spoke catalog env overlay) | Platform Engineering | ArgoCD | cert-manager HTTP-01 via Gateway | Day-1+ |

**Wildcard issuance remains not decided.** The structural prerequisite — a DNS challenge
solver not bound to a single tenant's gateway — is now satisfied for hybrid spokes, making
a `*.dev.nutgraf.in` wildcard technically issuable. Deciding to issue one is a separate
scope change; this addendum installs the solver infrastructure only.

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

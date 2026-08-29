# ADR-053: Tenant OAuth Confidential Client Lifecycle

**Date:** 2026-08-29
**Status:** Accepted

## Context

Tenant authentication via AgentGateway (ADR-050) requires a public browser client using PKCE, which holds no secret material. A fleet may additionally require one or more confidential clients: a BFF performing server-side delegated token exchange is the first, and a background worker using the client credentials grant is the shape that follows. Confidential clients hold secret material and are the subject of this ADR.

Not every fleet requires one. A service that only accepts tokens is not an OAuth client and holds no credential; it validates or introspects, as PostgREST does in the tenant Composition. Only a service that requests tokens is a client. The `universal-tenant` chart cannot express this: a single boolean renders a public client and a BFF client together, so a fleet cannot declare a browser client without a BFF, and cannot declare a second confidential client at all.

The platform contains a contradiction about where confidential secret material originates. The chart's OAuth2Client template states that hydra-maester creates the BFF client secret and that nothing pre-seeds it. The fleet-registry values for the same tenant declare an ExternalSecret expecting to read that secret from Infisical. Both cannot hold. The consequence is observable: the ExternalSecret cannot resolve because no producer writes the value to Infisical, and the platform's producer validator carries this as a tracked `PENDING` gap.

Were hydra-maester the origin, the Kubernetes Secret it creates would be the System of Record for that credential, which ADR-003 forbids. ADR-003 equally rejects the inverse flow, in which Kubernetes is the origin and Infisical the mirror.

hydra-maester supports credentials supplied in the referenced Secret rather than generating its own; this was established from the vendored checkout corresponding to the deployed version, not assumed. It nonetheless constrains any design that routes secret material through it, for two reasons that are properties of the component rather than defects. It reconciles only on changes to the OAuth2Client resource. The referenced Secret is read during a reconciliation, but no change to that Secret enqueues one, so a credential delivered into it is not observed and does not converge until some unrelated change to the resource provokes reconciliation. And its behaviour is not a published interface contract: the Hydra chart is consumed from an upstream repository with no image or tag override on an unpinned revision, so the deployed version can change with no corresponding commit in this repository, leaving re-verification on upgrade a mitigation with no trigger.

Hydra's own administrative interface holds exactly one client secret per client. The client model carries a single secret value and an optional expiry, and the available operations replace or patch that one value; no facility exists for a client to accept a previous and a current secret concurrently. Any credential transition therefore has a moment at which exactly one value is accepted, and the lifecycle below is ordered around that fact rather than around an overlap capability the platform does not have.

The resource schema exposes no client identifier field, so the Secret it requires is the only means of fixing an identifier. A client registered without one is registered under a generated identifier. Consumers hold the identifier in advance as configuration, so a generated one can never match, and the failure surfaces at the authorization endpoint as a client that does not exist rather than as anything resembling a provisioning fault. This is not hypothetical: it is how the tenant browser client failed on `hub-hybrid-dev`.

Where this sits in the browser flow is not where a reader might assume. ADR-050 owns that flow: an unauthenticated request reaches Hydra, Hydra issues a login challenge to auth-proxy, and auth-proxy bridges to Kratos, which owns identities and serves the sign-in page. That entire path is driven by the public client. The confidential client appears nowhere in it, which is why its credential is governed here and the public client's is not. Consent belongs to the same flow and remains ADR-050's to define; a confidential client whose credential is correct still yields no tokens if consent cannot be granted, so its absence below is scope rather than omission.

The consumer is not yet deployed. The tenant BFF is declared in the fleet-registry but does not run in dev, and no ExternalSecret for the credential exists on the spoke. Nothing is broken by that absence, and nothing demonstrates the delivery path works either.

Two existing capabilities bear on the decision. The hub-operator owns Hydra registration for platform clients, but that ownership is narrower than it appears: registrations are created and updated without secret material, so Hydra holds a credential the platform never reads. It also implements the ADR-003 first-provisioning rule for tenant credentials, distinguishing a credential absent during first provisioning, which is generated, from one absent afterwards, which is reported as missing rather than regenerated — a protection currently unreachable because its call site supplies the first-provisioning marker as a compile-time literal. Separately, the platform already operates an identity domain operator that reconciles machine identity credentials against Infisical and models rotation on an interval with an overlap period, multiple concurrently valid secrets recorded in status, and revocation on delete with a grace period. It runs on the hub, so Hydra's admin interface is within reach, and its provider client is today confined to identity management with no access to secret storage.

Ordering between a delivered Secret and a consuming controller is already solved. Sync waves advance only once the current wave reports healthy, and the platform defines a health rule for ExternalSecret that reports healthy only on a true Ready condition.

## Decision

### The boundary is secret material, not client type

OAuth2 clients divide by whether they carry secret material, and that division determines which lifecycle governs them.

Clients holding no secret material — the public browser client, and platform clients of the same shape — remain declarative resources reconciled by hydra-maester. There is no credential to generate, store, deliver or rotate, so ADR-003 does not govern them and the constraints above do not bite. They are not exempt from the identifier rule below.

Clients holding secret material are governed end to end by ADR-003: generated once, stored in Infisical, delivered by ESO to consumers, rotated under explicit control, with the Hydra registration reconciled from the same authority that owns the credential.

This boundary is the decision. Everything below follows from it.

### Clients are declared per service, not per tenant

A fleet declares zero or more OAuth clients. Each service initiating an OAuth flow has its own client; services that only validate tokens have none.

One credential shared across services is rejected, and redirect URIs are the decisive reason rather than tidiness. Redirect URIs are a per-client property and a security boundary: in the authorization code grant the redirect URI is the destination for the code, so a shared client makes the union of every consumer's callbacks valid for all of them. Scopes are likewise per-client, so sharing grants each service the union and the most privileged sets the floor. Grant types differ by service shape, and combining delegated and machine grants in one client means a single leaked credential yields both token classes. Sharing further couples rotation across every consumer and destroys per-client attribution in Hydra's audit trail.

The declaration is fleet-registry state, following the same idiom ADR-051 establishes for public hostnames: the fleet declares intent and the platform renders it. A single boolean rendering a fixed pair of clients is withdrawn in favour of a declared list. A fleet without a BFF declares no confidential client and receives none.

### Confidential client lifecycle belongs to the platform identity domain

The lifecycle of a confidential client — generation, storage, registration, rotation and revocation — is owned by the platform identity domain operator, whose name is corrected to reflect that scope. It is not owned by the hub-operator.

The hub-operator's boundary is hub and spoke control-plane infrastructure. A tenant credential placed there is a tenant identifier inside platform-scoped code, which ADR-047 forbids, and which that operator's own recorded intent to relocate tenant credential generation already identifies as a placement to be undone rather than extended. The identity domain already owns credential lifecycle against an external system, and already models the rotation and revocation semantics this credential requires.

The contract between the platform and that domain is a tenant OAuth client resource, one per client, carrying the tenant and cell identity, the client name, and the client's declared grant types, scopes, redirect URIs and authentication method. The tenant Composition creates these resources through the same mechanism by which it creates the tenant database resource. No tenant identifier appears in operator code; every one arrives on a resource.

The resource is the durable part of this decision. Which component reconciles it may be revisited without disturbing the Composition, the chart, the Infisical layout or the delivery path.

### Generation follows the established first-provisioning rule

The credential is generated once, during tenant provisioning, under ADR-003's rule that a previously generated secret missing from the System of Record fails reconciliation rather than being regenerated, because regeneration invalidates a credential that may be in use. Absence alone does not authorise generation; absence during first provisioning does. Absence afterwards is a fault that surfaces as a degraded tenant condition and stops, because a credential never created cannot be distinguished from one that was lost, and only one of those is safe to replace.

This requires a durable record of whether provisioning has occurred, which is the tenant resource's own observed status. It must not be a compile-time literal.

First provisioning, rotation and decommissioning are distinct state transitions, and none is inferred from the presence or absence of a credential. After initial provisioning a missing credential is a failure rather than a trigger to regenerate; an explicit rotation produces a new credential value under the overlap rules below; and removal of the declaration is a revocation and decommissioning rather than a silent discarding of state.

### Client identifiers are declared, never generated

Every client is registered under an identifier derived from the tenant and the client name, declared before registration and stable for the client's life. No client is registered under an identifier minted at reconcile time.

Stability is a constraint rather than a preference. Changing an identifier is not an in-place update: the registration is removed and re-created, so no client exists for the interval between, and every consumer holding the previous identifier fails until it converges. An identifier is therefore chosen once, at declaration, and a client needing a different one is a different client. No tenant client identifier may carry the prefix reserved for platform clients, which the hub-operator's registration authority treats as its own and retires when undeclared.

An identifier is not secret material. It is public by specification, travels in every authorization request, and is configuration consumers hold in advance. Declaring it therefore makes no store authoritative for a credential and does not engage ADR-003; the rules for identifier and secret are independent.

For a confidential client the identifier follows from the same authority owning the credential and is stable by construction. For a public client, where the schema offers no identifier field, it is declared in the Secret the client is required to name, and a declared identifier is adopted rather than replaced.

Consumers must be configured from the same declaration that produced the identifier. An identifier written independently into a consumer's configuration is a second source for a value with one authority, and it drifts silently: nothing fails until an authorization request is refused.

### Delivery is spoke-side only

Infisical is the System of Record, under the tenant's path within its cell. ESO delivers the credential into the tenant namespace on the spoke through the namespaced tenant secret store, whose scope is the tenant's own prefix under ADR-031. This is the ordinary tenant secret path already used for tenant database credentials and introduces no new delivery mechanism. No hub-side Kubernetes Secret holds a confidential credential.

Where ordering between an ExternalSecret and a consumer matters, sync waves and the ExternalSecret health rule govern. No additional dependency mechanism is introduced.

### Registration is reconciled from the credential's authority

The Hydra registration for a confidential client is reconciled by the authority owning the credential, and carries the credential value that authority holds. Registration is convergent: the desired specification comes from the declared resource, and the registration is created or updated to match.

Because one authority owns both credential and registration, the stored value and the accepted value cannot diverge through independent reconciliation, satisfying ADR-043.

Reconciliation is scoped by ownership, not by absence from the declaration. Every registration this platform creates carries an ownership marker, and reconciliation acts only on registrations bearing it.

Hydra records an owner against each registration, and hydra-maester already uses that field to scope its own reconciliation. Because public clients remain hydra-maester's and confidential clients become this platform's, both authorities reconcile against one Hydra and both filter by owner. The marker this platform writes must therefore be distinguishable from hydra-maester's by construction, so that neither authority's scoped reconciliation can capture the other's registrations. A registration the platform does not own remains untouched whether or not the fleet-registry declares it, because absence from a declaration cannot distinguish a registration this platform should retire from one another authority legitimately maintains against the same Hydra.

Within that scope, convergence retires what is no longer declared. A registration left under a generated identifier, or by a tenant that no longer exists, is not inert: each is a credential-bearing entry that still authenticates. Removal outside reconciliation is not sanctioned, because a registration removed by hand is indistinguishable from one the platform never created.

The lifecycle the owning authority reconciles is complete: creation, metadata update, rotation with overlap, revocation, deletion, and recovery of its own state after a restart of the hub or of the controller.

### Rotation delivers before it flips

Rotation is triggered by an explicit platform operation, never by elapsed time. An OAuth client secret is not a certificate and carries no expiry, so a fixed interval adds risk without reducing any. Where a fleet's policy requires periodic rotation it is declared on the resource rather than assumed by the platform.

The trigger is a declared rotation identifier on the resource, compared against the identifier recorded in its observed status. A rotation begins when the two differ and is complete when they agree. This is preferable to an annotation or a timestamp because it is explicit in the declaration, idempotent under repeated reconciliation, and cannot fire twice for one intent across a controller restart.

Because exactly one secret is accepted at any moment, ordering determines whether a rotation is a brief transition or an outage. The credential is delivered to the consumer first and the authoritative registration is changed last:

The new value is generated and persisted to the System of Record; delivery to the tenant namespace is refreshed and the projected content verified to match; the declared consumer is rolled and its convergence verified; and only then is the registration updated to the new value.

Throughout the slow part of that sequence the consumer continues to authenticate with the previous value, which the registration still accepts, so nothing fails while delivery and rollout proceed. The interval during which the two disagree is reduced to the gap between observing convergence and updating the registration. The inverse ordering — changing the registration first — makes that interval the entire delivery and rollout window, and is rejected for that reason.

Rotation state is durable on the resource's status, because the transition spans multiple reconciliations and must survive a restart of the controller mid-flight: steady, delivering, awaiting convergence, flipping, and returning to steady, with a terminal blocked state when convergence cannot be established.

### Convergence is proven from cluster state, never inferred

The consumers of a credential are declared on the resource. Implicit discovery is not used, because a consumer the platform failed to discover is indistinguishable from one that does not exist, and the consequence of that ambiguity is a registration changed while a consumer still holds the previous value.

Convergence is established from observable cluster state: the content of the projected Secret, and the declared consumer's own generation and readiness transition. It is never inferred from application behaviour, from successful authentication, or from elapsed time, because none of those distinguishes a consumer that has adopted the new value from one that has not yet been asked to.

Where convergence cannot be proven — no consumer is declared, its state cannot be read, or the rollout does not complete — the rotation stops before the registration is changed and surfaces as blocked. The previous credential remains valid and in use. A stranded consumer authenticating against a changed registration is an outage, whereas a rotation that halts before flipping leaves a working system and a visible condition.

### Emergency revocation inverts the ordering deliberately

Responding to a compromised credential is a distinct operation from rotation, and must not be served by it. Graceful rotation preserves the previous value until consumers converge, which is precisely the wrong behaviour when that value is known to be leaked.

Emergency revocation therefore reverses the order: the registration is changed first, invalidating the compromised value immediately, and delivery and convergence follow. The tenant cannot authenticate until convergence completes, and that interruption is the correct trade, because a credential known to be compromised must stop working before anything else is considered.

The two operations share machinery and differ only in ordering and in what they are willing to sacrifice. They are distinguished by declared intent, never inferred from circumstance.

### Zero-downtime transition, where required, is a second client

Because a client accepts one secret at a time, a transition with no window at all cannot be achieved by rotating a client's secret. It is achieved by registering a second client, converging consumers onto it, and retiring the first — an overlap of clients rather than of secrets.

This is an escalation, not the default. It churns the client identifier, duplicates scopes and redirect URIs across two registrations, and splits audit attribution for one logical client. It is warranted only for a consumer that genuinely cannot tolerate the brief interval described above, and it requires that every consumer derive the identifier from the declaration rather than holding it independently.

### Alternatives considered

Capturing the credential minted by hydra-maester into Infisical is rejected under ADR-003, which rejects the pattern making Kubernetes the origin and Infisical the mirror. It further requires a bespoke copy mechanism existing nowhere else in the platform, and leaves the credential's authority with a component the platform does not control.

Pre-seeding a hub-side Kubernetes Secret from Infisical for hydra-maester to adopt was the previously proposed shape. Adoption is genuinely supported, so the design works on first provisioning. It is rejected because the behaviour is not a published contract on a component the platform does not pin, and because the absent Secret observation means a delivered credential change does not converge without provoking reconciliation externally. Both are permanent properties, not defects awaiting a fix.

Adding the credential to the hub-operator's static application secret registry is rejected because that registry is fleet-wide and describes hub and spoke control-plane infrastructure. A tenant entry places a tenant identifier in platform code, which ADR-047 forbids, and requires an operator release for every new tenant.

Owning the lifecycle in the hub-operator's tenant provisioning boundary is the cheapest option, since that operator already holds both the tenant Infisical path and a Hydra transport. It is rejected on boundary rather than cost: it extends the hub-operator's scope to tenant credentials, and it offers no rotation trigger, since convergence would occur only when the tenant resource changed.

Generating the credential through ESO's generator and pushing it to Infisical, with hydra-maester adopting the result, requires no operator code, and both mechanisms exist with semantics that can anchor generate-once in Infisical rather than in Kubernetes. It is rejected because registration and rotation would remain with hydra-maester and inherit the convergence gap. Its delivery half is what this ADR adopts, and it remains the correct fallback if the identity domain scope is deferred.

## Ownership

Per ADR-039.

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Confidential client credential | Infisical | platform identity operator | ESO | tenant workload | Day-1+ |
| Confidential client registration | Hydra | platform identity operator | platform identity operator | auth-proxy, AgentGateway | Day-1+ |
| Client declaration | fleet-registry | fleet-registry | tenant Composition | platform identity operator | Day-1+ |
| Public client registration | Hydra | fleet-registry | hydra-maester | AgentGateway | Day-1+ |

## Consequences

### Positive

- Infisical is the single System of Record for confidential credentials, and no Kubernetes Secret is authoritative for one.
- The first-provisioning ordering hazard is removed by construction, because no hub-side Secret participates in the path.
- Correctness no longer depends on unpublished behaviour of an unpinned component, so an upgrade cannot silently invalidate the credential path.
- Rotation delivers before it flips, so the interval in which the stored and accepted values disagree is bounded by the controller's own latency rather than by delivery and rollout.
- Rotation halts rather than proceeding when convergence cannot be proven, so a credential transition cannot strand a consumer unobserved.
- Emergency revocation is a distinct operation, so responding to a compromised credential does not inherit rotation's deliberate preservation of the previous value.
- Credential lifecycle and registration share one authority, satisfying ADR-043.
- No tenant identifier enters platform code; every one arrives on a resource, satisfying ADR-047 structurally rather than by convention.
- A fleet declares only the clients it requires, so a fleet without a BFF is expressible and no unused credential exists.
- Per-client credentials separate redirect URIs, scopes, rotation blast radius and audit attribution.
- hydra-maester is retained where it is sound rather than removed or worked around.
- The tracked producer gap is closed by a real producer.

### Negative

- The identity domain operator gains two capabilities it lacks: Hydra administration, and access to secret storage. Its provider client is today confined to identity management.
- Correcting the operator's name touches its Application, its RBAC and its resource group. A tenant-scoped resource inside an operator named for spokes is worse but cheaper, so the correction may lag the functionality.
- This is the most expensive option considered, and it is justified by the durability of the contract rather than by present demand: no consumer currently exercises rotation with overlap.
- Two mechanisms register OAuth clients, divided by whether secret material is present. The boundary is principled but must be understood before a client is added.
- The identity domain's existing rotation policy defaults to enabled on a fixed interval, which contradicts the rule that rotation is not periodic. That default must be reconciled before the resource is introduced.
- Replacing the chart's boolean with a declared list is a refactor, and every existing fleet's values require migration.
- Ownership-scoped reconciliation cannot retire what the platform did not create, so registrations predating this ADR carry no ownership marker and require explicit adoption or removal as a migration rather than being converged away.
- Public clients still name a Secret they do not use, because the schema requires one. It holds an identifier and no credential.
- A public client's identifier Secret is adopted rather than created, so it carries no owner reference back to the client resource and is not reclaimed when that resource is removed. Its lifecycle must be handled explicitly.

## Impact

- **Amends ADR-050.** The statement that hydra-maester creates the BFF client secret is withdrawn.
- **Amends ADR-047.** The fleet-registry declares which OAuth clients a fleet has, and the spoke-side delivery of each confidential credential. No hub-side delivery is declared.
- **Supersedes the 2026-08-25 draft of this ADR**, which assigned confidential client lifecycle to the hub-operator and assumed a fixed pair of clients per fleet.
- **Corrects the identity domain operator's name** to the scope its own resource documentation already claims.
- **Refactors the universal-tenant chart.** The OAuth boolean and its fixed pair of clients are replaced by a declared list, rendering confidential and public entries through their respective mechanisms.
- **Corrects the tenant provisioning call site.** The first-provisioning marker is derived from observed status rather than supplied as a literal, restoring the ADR-003 protection against regenerating a lost credential.
- **Closes a tracked gap.** The `PENDING` entry for the tenant BFF credential in the producer validator is removed once the producer exists, and the validator then enforces it. Removing that entry is the acceptance gate for this ADR.
- **Extends tenant-identifier enforcement.** The tenant identifier validator does not currently cover the operator tree, so the ADR-047 rule this decision relies on is unenforced precisely where it now applies. Its scope must include that tree.
- **Requires the identifier declaration to reach consumers.** The spoke gateway configuration carries a tenant's client identifier as a literal in a manifest shared by all tenants, which is a second source for a value this ADR gives one authority.
- **Requires migration of existing registrations.** Registrations created under generated identifiers predate the ownership marker, so reconciliation will not act on them. Each is adopted under the declared identifier or removed explicitly, as a migration step this ADR requires rather than an outcome convergence produces.
- **Establishes implementation prerequisites, not open architecture.** The identity domain operator's name; its Hydra administration and secret storage capabilities; the client resource schema; the location of the spoke-side ExternalSecret declaration; the delivery mechanism's deployed capability; the consuming workload's environment contract; and the migration of public clients onto the same resource. None alters the authority and ownership model above, and the existing rotation policy default must be reconciled with the rule that rotation is not periodic before the resource is introduced.
- **Retains** hydra-maester for public clients and the guard confining OAuth2Client resources to the hub.

## References

- ADR-003: Secret Management Architecture
- ADR-031: Infisical Path Scoping and Prefix Authorisation
- ADR-035: Universal PKI Rule
- ADR-039: Platform Ownership Model
- ADR-040: Day-0 vs Day-1 Lifecycle Boundary
- ADR-041: Controller Responsibility Matrix
- ADR-043: Control Plane Authority Model
- ADR-046: Hybrid Topology
- ADR-047: Fleet Tenant Deployment Contract
- ADR-050: Tenant Authentication via AgentGateway
- ADR-051: Tenant Public TLS

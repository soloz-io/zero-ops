# ADR-053: Tenant OAuth Confidential Client Lifecycle

**Date:** 2026-08-29
**Status:** Accepted

## Context

Tenant authentication via AgentGateway (ADR-050) requires a public browser client using PKCE, which holds no secret material. A fleet may additionally require one or more confidential clients: a server-side component performing delegated token exchange is the first, and a background worker using the client credentials grant is the shape that follows. Confidential clients hold secret material and are the subject of this ADR.

Not every fleet requires one. A service that only accepts tokens is not an OAuth client and holds no credential; it validates or introspects, as PostgREST does in the tenant Composition. Only a service that requests tokens is a client. The `universal-tenant` chart cannot express this: a single boolean renders a public client and a confidential client together, so a fleet can declare neither one without the other, and cannot declare a second confidential client at all.

The platform contains a contradiction about where confidential secret material originates. The chart's client template states that hydra-maester creates the secret and that nothing pre-seeds it. The fleet-registry values for the same tenant declare an ExternalSecret expecting to read that secret from Infisical. Both cannot hold. The consequence is observable: the ExternalSecret cannot resolve because no producer writes the value to Infisical, and the platform's producer validator carries this as a tracked `PENDING` gap.

ADR-003 governs the resolution. No Kubernetes Secret may serve as a secret's System of Record; Kubernetes Secrets are a delivery cache. ADR-003 equally rejects the inverse flow, in which Kubernetes is the origin and Infisical the mirror.

hydra-maester branches on whether the Secret its client resource names already exists, and the two branches have opposite consequences under that rule. Where the Secret is absent, the credential and the client identifier originate in hydra-maester, which creates the Secret and takes an owner reference over it; that Secret is then the credential's origin, which ADR-003 forbids. Where the Secret is present, hydra-maester reads the credential and identifier from it and registers the client with those values, creating nothing and claiming nothing. In that direction it consumes a projection and originates no state, which is precisely what ADR-003 permits. Both behaviours were established from the vendored checkout corresponding to the deployed version, and the project's own tests exercise both.

The branch is therefore not a hazard to be avoided but a condition to be governed: the design must guarantee that the Secret exists before the client resource is reconciled. The platform already has the mechanism. Sync waves advance only once the current wave reports healthy, the platform defines a health rule for ExternalSecret that reports healthy only on a true Ready condition, and the public browser client already uses exactly this ordering to pre-seed its identifier in an earlier wave so that no identifier is minted.

Two constraints bound any rotation design. Hydra holds exactly one secret per client: its model carries a single value with an optional expiry, and the available operations replace that value in place, so no client accepts a previous and a current secret concurrently. And hydra-maester reconciles only on changes to its client resource — the referenced Secret is read during a reconciliation, but no change to that Secret enqueues one — so a credential delivered into the Secret is not observed until the resource itself changes. The resource publishes an arbitrary metadata field as part of its specification, so a change there is a change to the resource.

This differs from the machine identity credentials the platform already rotates, whose authority admits several concurrently valid secrets. The two credential classes have genuinely different capabilities, and their lifecycles differ for that reason rather than by inconsistency.

The consumer is not yet deployed. The tenant's confidential client consumer is declared in the fleet-registry but does not run in dev, and no ExternalSecret for the credential exists on the spoke. Nothing is broken by that absence, and nothing demonstrates the delivery path works either.

Where this sits in the browser flow is not where a reader might assume. ADR-050 owns that flow: an unauthenticated request reaches Hydra, Hydra issues a login challenge to auth-proxy, and auth-proxy bridges to Kratos, which owns identities and serves the sign-in page. That entire path is driven by the public client. The confidential client appears nowhere in it, which is why its credential is governed here and the public client's is not. Consent belongs to the same flow and remains ADR-050's to define.

The hub-operator already generates tenant credentials into the tenant's path within its cell, under a contract that distinguishes a credential absent during first provisioning, which is generated, from one absent afterwards, which is reported as missing rather than regenerated. That protection is currently unreachable because its call site supplies the first-provisioning marker as a compile-time literal.

## Decision

### The boundary is secret material, not client type

OAuth2 clients divide by whether they carry secret material, and that division determines which lifecycle governs them. Both kinds are registered by the same mechanism; they differ in where their credential comes from and whether one exists at all.

Clients holding no secret material are declared and registered as they are today. There is no credential to generate, store, deliver or rotate. They are not exempt from the identifier rule below.

Clients holding secret material have that material governed end to end by ADR-003: generated once, stored in Infisical, and delivered by projection to every consumer that needs it — the tenant workload and Hydra alike.

### Infisical is the sole authority; everything else holds a projection

The credential has one authority, and it is Infisical. Hydra's registration and every Kubernetes Secret in the path are projections of it. Neither originates a value.

This is the property that makes the existing registration mechanism sound rather than merely convenient. Delivering the credential into Hydra is the same class of operation as delivering it into the tenant namespace: one is performed by the registration controller reading a projected Secret, the other by ESO reading Infisical. Neither holds authority over the value, so no second authority exists and ADR-043 is satisfied without a bespoke reconciler.

### The registration mechanism is the platform's existing one

Confidential clients are registered by the same controller that registers every other OAuth client. The platform does not implement its own registration, ownership scoping, orphan retirement or client resource, because all four already exist, are maintained upstream, and are already in use for the public client.

What the platform adds is confined to what does not exist: generation of the credential into Infisical during tenant provisioning, its delivery by projection, and the declaration that drives both.

### Ordering makes the adopting branch the only reachable one

The Secret a confidential client names is a projection of Infisical, materialised in an earlier sync wave than the client resource that names it. Because a wave advances only when it is healthy, and an ExternalSecret is healthy only when Ready, the client resource is never applied while its Secret is unresolved. The originating branch is therefore unreachable in normal operation, and a credential that cannot be delivered halts the sync rather than causing a credential to be minted.

This is the ordering the public client already relies on for its identifier. Confidential clients use the same ordering for identifier and credential together.

The invariant this rests on must be stated plainly, because violating it silently breaks the System of Record: the projected Secret must never be absent while the client resource exists. Should it be deleted, the next reconciliation takes the originating branch, registers a credential Infisical does not hold, and the projection is then restored to a value the registration no longer accepts. The Secret is owned and restored by its projection, which bounds the exposure to one refresh interval, and the refresh interval for these secrets is set accordingly.

### Generation follows the established first-provisioning rule

The credential is generated once, during tenant provisioning, by the boundary that already generates tenant database credentials, under ADR-003's rule that a previously generated secret missing from the System of Record fails reconciliation rather than being regenerated. Absence alone does not authorise generation; absence during first provisioning does. Absence afterwards is a fault that surfaces as a degraded tenant condition and stops, because a credential never created cannot be distinguished from one that was lost, and only one of those is safe to replace.

This requires a durable record of whether provisioning has occurred, which is the tenant resource's own observed status. It must not be a compile-time literal.

First provisioning, rotation and decommissioning are distinct state transitions, and none is inferred from the presence or absence of a credential. After initial provisioning a missing credential is a failure rather than a trigger to regenerate; an explicit rotation produces a new value under the ordering below; and removal of the declaration is a revocation and decommissioning rather than a silent discarding of state.

### Clients are declared per service, not per tenant

A fleet declares zero or more OAuth clients. Each service initiating an OAuth flow has its own client; services that only validate tokens have none.

One credential shared across services is rejected, and redirect URIs are the decisive reason rather than tidiness. Redirect URIs are a per-client property and a security boundary: in the authorization code grant the redirect URI is the destination for the code, so a shared client makes the union of every consumer's callbacks valid for all of them. Scopes are likewise per-client, so sharing grants each service the union and the most privileged sets the floor. Grant types differ by service shape, and combining delegated and machine grants in one client means a single leaked credential yields both token classes. Sharing further couples rotation across every consumer and destroys per-client attribution in Hydra's audit trail.

The declaration is fleet-registry state, following the same idiom ADR-051 establishes for public hostnames: the fleet declares intent and the platform renders it. A single boolean rendering a fixed pair of clients is withdrawn in favour of a declared list.

### Client identifiers are declared, never generated

Every client is registered under an identifier derived from the tenant and the client name, declared before registration and stable for the client's life. No client is registered under an identifier minted at reconcile time.

An identifier is not secret material. It is public by specification, travels in every authorization request, and is configuration consumers hold in advance. Declaring it therefore makes no store authoritative for a credential and does not engage ADR-003; the rules for identifier and secret are independent.

Stability is a constraint rather than a preference. Changing an identifier is not an in-place update: the registration is removed and re-created, so no client exists for the interval between, and every consumer holding the previous identifier fails until it converges. An identifier is chosen once, at declaration, and a client needing a different one is a different client.

Consumers must be configured from the same declaration that produced the identifier. An identifier written independently into a consumer's configuration is a second source for a value with one authority, and it drifts silently: nothing fails until an authorization request is refused.

### Rotation delivers before it flips

Rotation is triggered by an explicit platform operation, never by elapsed time. An OAuth client secret is not a certificate and carries no expiry, so a fixed interval adds risk without reducing any. Periodic rotation is disabled by default and enabled only where a fleet declares it.

Because exactly one secret is accepted at any moment, ordering determines whether a rotation is a brief interruption or a sustained outage. The new value is generated and persisted to the System of Record; the projections converge; the declared consumer is restarted and adopts the new value; and only then is the registration changed, by an accompanying change to the declaration that alters the client resource and so causes the credential to be re-read.

Throughout the slow part of that sequence the consumer continues to authenticate with the previous value, which the registration still accepts. The interval in which the two disagree is the interval between the consumer adopting the new value and the registration change taking effect. The inverse ordering — changing the registration first — makes that interval the entire delivery and restart window, and is rejected for that reason.

That interval is bounded by the platform's delivery and reconciliation latency rather than by a controller holding both values in memory, and it is therefore longer than a directly implemented registration change would produce. This is an accepted cost of using the existing registration mechanism rather than implementing one. Rotation is a rare, deliberate operation, and a brief interruption during it is preferred to a bespoke reconciler maintained solely to shorten it.

There is no overlap available at the secret level. Neither the registration authority nor the controller that drives it can hold a previous and a current secret concurrently, so every rotation has a window and no configuration removes it.

### Emergency revocation inverts the ordering deliberately

Responding to a compromised credential is a distinct operation from rotation, and must not be served by it. Graceful rotation preserves the previous value until consumers converge, which is precisely the wrong behaviour when that value is known to be leaked.

Emergency revocation therefore reverses the order: the registration is changed first, invalidating the compromised value immediately, and delivery and convergence follow. The tenant cannot authenticate until convergence completes, and that interruption is the correct trade, because a credential known to be compromised must stop working before anything else is considered.

Because the normal path reaches the registration through declaration and reconciliation, it is too slow for this purpose. Emergency revocation is therefore a documented direct operation against the registration authority, followed by reconciliation of the declaration to match. It is the one operation in this ADR not performed by the ordinary path, and it is defined as such rather than left to be improvised.

### Zero-downtime transition, where required, is a second client

A transition with no window cannot be achieved by rotating a client's secret. It is achieved by registering a second client, converging consumers onto it, and retiring the first — an overlap of clients rather than of secrets.

This is an escalation, not the default. It churns the client identifier, duplicates scopes and redirect URIs across two registrations, and splits audit attribution for one logical client. It is warranted only for a consumer that cannot tolerate the interval described above, and it requires that every consumer derive the identifier from the declaration rather than holding it independently.

### Alternatives considered

Allowing the credential to originate in the registration controller and pushing it to Infisical is rejected under ADR-003, which rejects the pattern making Kubernetes the origin and Infisical the mirror. It further leaves the credential's authority with a component the platform does not control.

Copying a credential minted by the registration controller into Infisical is rejected for the same reason, and additionally requires a bespoke copy mechanism existing nowhere else in the platform.

Adding the credential to the hub-operator's static application secret registry is rejected because that registry is fleet-wide and describes hub and spoke control-plane infrastructure. A tenant entry places a tenant identifier in platform code, which ADR-047 forbids, and requires an operator release for every new tenant. Generating it on the tenant provisioning path, where the tenant arrives as declared input, does not.

Implementing confidential client registration, ownership scoping, orphan retirement and a client resource inside a platform operator was considered at length and rejected. It would shorten the rotation window to controller latency and allow emergency revocation through the ordinary path, but it re-implements four capabilities that already exist and are maintained upstream, to govern a credential whose authority is Infisical either way. The rotation window and the revocation path are the two costs of not doing it, and both are accepted above.

Rotating with a period during which two secrets are simultaneously valid is not available. Neither the registration authority's model nor the controller that drives it represents more than one secret per client.

## Ownership

Per ADR-039.

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Confidential client credential | Infisical | hub-operator | ESO | tenant workload, registration controller | Day-1+ |
| Client declaration | fleet-registry | fleet-registry | tenant Composition, universal-tenant chart | registration controller | Day-1+ |
| Client identifier | fleet-registry | fleet-registry | ESO | consumers, registration | Day-1+ |
| OAuth client registration | fleet-registry | hydra-maester | hydra-maester | auth-proxy, AgentGateway | Day-1+ |

## Consequences

### Positive

- Infisical is the single authority for confidential credentials; Hydra's registration and every Kubernetes Secret are projections, so no second authority exists and ADR-043 holds without a bespoke reconciler.
- No registration logic, ownership scoping, orphan retirement or client resource is re-implemented; all four are inherited from a maintained upstream component already in use for public clients.
- One registration mechanism serves both client kinds, so adding a client does not require knowing which mechanism applies.
- The originating branch of that mechanism is made unreachable by ordering the platform already enforces, rather than by avoiding the component.
- No tenant identifier enters platform code; every one arrives as declared input, satisfying ADR-047 structurally.
- A fleet declares only the clients it requires, so a fleet without a confidential client is expressible and no unused credential exists.
- Per-client credentials separate redirect URIs, scopes, rotation blast radius and audit attribution.
- Delivery to the tenant namespace requires no new mechanism and no template change; it is the path already used for tenant database credentials.
- The tracked producer gap is closed by a real producer.

### Negative

- The rotation window is bounded by delivery and reconciliation latency rather than by controller memory, so it is longer than a directly implemented registration would give. Rotation is a deliberate operation with a brief expected interruption.
- Emergency revocation cannot use the ordinary path and is defined as a direct operation against the registration authority, which must be documented as a runbook and reconciled afterwards.
- Correctness depends on an ordering invariant rather than on component isolation: the projected Secret must never be absent while the client resource exists, or a credential is registered that the System of Record does not hold.
- The adopting behaviour, while exercised by the upstream project's tests, carries no published stability guarantee, and the chart supplying the component is consumed on an unpinned revision. Pinning it and asserting the deployed version are required for re-verification to have a trigger.
- Rotation requires a change to the declaration alongside the credential change, because a credential change alone is not observed. The two must be sequenced, and performing them together risks the registration changing before consumers converge.
- Replacing the chart's boolean with a declared list is a refactor, and every existing fleet's values require migration.
- Public clients still name a Secret they do not use, because the resource schema requires one. It holds an identifier and no credential.
- A public client's identifier Secret is adopted rather than created, so it carries no owner reference back to the client resource and is not reclaimed when that resource is removed.

## Impact

- **Amends ADR-050.** The statement in the client template that hydra-maester creates the confidential client secret is withdrawn. The secret is projected from Infisical and adopted.
- **Amends ADR-047.** The fleet-registry declares which OAuth clients a fleet has, and the delivery of each confidential credential.
- **Supersedes the earlier drafts of this ADR**, which routed confidential clients away from hydra-maester and assigned registration to a platform operator. The System of Record chain, the per-client decision and the identifier rules are carried forward unchanged; the registration mechanism and the rotation path are not.
- **Extends tenant credential generation.** The confidential client credential joins the tenant credentials already generated during provisioning, under the same contract.
- **Corrects the tenant provisioning call site.** The first-provisioning marker is derived from observed status rather than supplied as a literal, restoring the ADR-003 protection against regenerating a lost credential.
- **Refactors the universal-tenant chart.** The OAuth boolean and its fixed pair of clients are replaced by a declared list, and each confidential client gains a projected Secret in an earlier sync wave than its client resource.
- **Requires the supplying chart to be pinned** and its deployed version asserted, so that a change in the adopting behaviour surfaces as a failure rather than silently.
- **Requires a documented emergency revocation procedure** operating directly against the registration authority.
- **Closes a tracked gap.** The `PENDING` entry for the tenant confidential credential in the producer validator is removed once the producer exists. Removing that entry is the acceptance gate for this ADR.
- **Extends tenant-identifier enforcement.** The tenant identifier validator does not currently cover the operator tree, so the ADR-047 rule this decision relies on is unenforced where it now applies.
- **Requires the identifier declaration to reach consumers.** The spoke gateway configuration carries a tenant's client identifier as a literal in a manifest shared by all tenants, which is a second source for a value this ADR gives one authority.
- **Requires migration of existing registrations.** Registrations created under generated identifiers are adopted under the declared identifier or removed explicitly.
- **Establishes implementation prerequisites, not open architecture.** The declaration schema; the refresh interval for the projected Secrets; whether the hub's secret store is authorised for the tenant path under ADR-031; and the consuming workload's environment contract. None alters the authority model above.

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

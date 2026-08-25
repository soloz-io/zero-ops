# ADR-053: Tenant OAuth Confidential Client Lifecycle

**Date:** 2026-08-25
**Status:** Proposed

## Context

Tenant authentication via AgentGateway (ADR-050) requires two OAuth2 clients per fleet: a public browser client using PKCE, which holds no secret, and a confidential BFF client authenticating with `client_secret_basic`, which holds secret material.

The platform currently contains a contradiction about who owns that secret material. The `universal-tenant` chart's OAuth2Client template states that hydra-maester creates the BFF client secret and that nothing has to pre-seed it. The fleet-registry values for the same tenant declare an ExternalSecret expecting to read that secret from Infisical. Both cannot hold. The consequence is observable: the tenant's ExternalSecret cannot resolve, because no component writes the value to Infisical, and `scripts/validate/preflight/95-infisical-key-producers.sh` carries this as a tracked `PENDING` gap with no producer.

If hydra-maester were the origin, the Kubernetes Secret it creates would be the System of Record for that credential. ADR-003 forbids this: no Kubernetes Secret may serve as a secret's System of Record; Kubernetes Secrets are a delivery cache. ADR-003 further rejects the inverse flow, where Kubernetes is the origin and Infisical the mirror, as an ownership inversion.

hydra-maester constrains any design that routes secret material through it. It reconciles only on changes to the OAuth2Client resource and does not observe Secret contents, so a delivered credential change does not converge. It exposes a single secret name and cannot express Hydra's rotated-secrets model, so it cannot support rotation without an interruption.

Two of its behaviours were established by observation on `hub-hybrid-dev` rather than assumed. When the referenced Secret is absent, it mints the credential and the client identifier itself and creates the Secret carrying an owner reference back to the OAuth2Client, which is what prevents another controller from adopting that Secret. When the referenced Secret is present, it adopts the identifier it finds there rather than replacing it, and does not overwrite the Secret. The resource schema exposes no client identifier field, so the Secret is the only means of making a client identifier stable; an unseeded client is registered under a generated identifier that no consumer can be configured against in advance.

Both behaviours are real and reproducible, and neither is a published interface contract. They are properties of a community-maintained component, observed at one version, that the platform does not control.

A client's identifier is a separate concern from its secret, and it is not optional to get right. The resource schema requires a secret name even for a public client that authenticates with none, so every client names a Secret whether or not it holds a credential, and that Secret is the only means of fixing the identifier. A client registered without one is registered under a generated identifier. Consumers are configured with the identifier in advance, so a generated one can never match, and the failure surfaces at the authorization endpoint as a client that does not exist rather than as anything resembling a provisioning fault. This is not hypothetical: it is how the tenant browser client failed on `hub-hybrid-dev`.

Where this sits in the browser flow matters, because it is not where a reader might assume. ADR-050 owns the flow and describes it accurately: AgentGateway sends an unauthenticated request to Hydra, Hydra issues a login challenge to auth-proxy, and auth-proxy bridges to Kratos, which owns identities and serves the sign-in page. That entire path is driven by the PUBLIC client. The confidential client appears nowhere in it. It exists for server-side delegated token exchange by the tenant's BFF, which is why its credential is governed here and the public client's is not — the public client has no credential to govern.

The consumer of that credential is not yet deployed. The tenant BFF is declared in the fleet-registry but does not run in dev, and no ExternalSecret for the credential exists on the spoke. Nothing is broken by that absence, and nothing demonstrates the delivery path works either; this ADR describes the lifecycle that must hold once the consumer exists, not one currently exercised end to end.

Consent is part of that lifecycle and is not addressed further below. Hydra delegates both login and consent to auth-proxy, and a confidential client whose credential is perfectly correct still yields no tokens if consent cannot be granted for it. Consent is ADR-050's to define; it is named here so its absence from the decision reads as deliberate scope rather than an omission.

Two facts about the platform's existing capabilities bear on the decision. The hub-operator already owns direct Hydra client registration for platform clients through `internal/client/hydra.go`, including existence checks, creation and update. The hub-operator also already implements the ADR-003 first-provisioning rule for tenant credentials in `EnsureTenantFolderAndCredentials`, whose contract distinguishes a credential that is absent during first provisioning, which is generated, from one absent afterwards, which is reported as missing rather than regenerated. That second capability is currently defeated at its call site in the AINativeSaaS reconciler, which passes the first-provisioning flag as a literal true as a migration accommodation, so the protective branch is unreachable.

Ordering between a delivered Secret and a consuming controller is a solved problem in this platform. ArgoCD advances to the next sync wave only once the current wave reports healthy, and the platform defines a health rule for ExternalSecret that reports healthy only when its Ready condition is true.

## Decision

### The boundary is secret material, not client type

OAuth2 clients divide by whether they carry secret material, and that division determines which lifecycle governs them.

Clients that hold no secret material — the public browser client, and platform clients of the same shape — remain declarative resources reconciled by hydra-maester. There is no credential to generate, store, deliver or rotate, so ADR-003 does not govern them and the constraints that make hydra-maester unsuitable for secret material do not bite. They are not exempt from the identifier rule below, which applies to every client regardless of how it authenticates.

Clients that hold secret material are governed end to end by ADR-003 and are owned by the hub-operator. Their credential is generated once, stored in Infisical, delivered by ESO to consumers, and rotated under explicit control. The Hydra registration for such a client is reconciled from the same authority that owns the credential.

This boundary is the decision. Everything below follows from it.

### Confidential client credentials do not transit hydra-maester

The BFF client's credential is never routed through hydra-maester, and no Kubernetes Secret for it exists on the hub. ADR-003 already establishes this shape for a credential whose only hub-side authority is the operator: the generation authority writes to Infisical directly and ESO pulls to consumers, with no hub-side Kubernetes Secret for that credential class.

This removes, by construction rather than by mitigation, every hazard the alternative design had to manage. There is no hub Secret whose creation must precede another controller's reconciliation, so there is no first-provisioning ordering problem. There is no owner reference to contend with. There is no dependence on undocumented behaviour of a component when a referenced Secret already exists. There is no need to provoke reconciliation by annotating a resource when the credential changes.

### Generation follows the established first-provisioning rule

The credential is generated once, during tenant provisioning, by the same boundary that already generates tenant database credentials.

Generation obeys ADR-003's rule that a previously generated secret missing from the System of Record must fail reconciliation rather than be regenerated, because regeneration would invalidate a credential that may be in use. Absence alone therefore does not authorise generation; absence during first provisioning does. Absence afterwards is a fault that must surface as a degraded tenant condition and stop, because the platform cannot distinguish a credential that was never created from one that was lost, and only one of those is safe to replace.

This requires a durable record of whether provisioning has occurred. That record is the tenant resource's own observed status, which is the authority for what the platform has already done for that tenant. It must not be a compile-time literal; the current call site passes one, which makes the protective branch unreachable and must be corrected as part of this ADR.

Explicit rotation is the only other path that produces a new credential value.

### Client identifiers are declared, never generated

Every OAuth client is registered under an identifier derived from the tenant, declared before registration and stable for the client's life. No client is registered under an identifier minted at reconcile time.

An identifier is not secret material. It is public by the OAuth2 specification, travels in every authorization request, and is configuration that consumers must hold in advance. Declaring it therefore does not make any store authoritative for a credential and does not engage ADR-003 — the rules for the identifier and for the secret are independent, and only the secret follows the ADR-003 chain.

For a confidential client the identifier follows from the same authority that owns the credential and is stable by construction. For a public client, where the resource schema offers no identifier field, the identifier is declared in the Secret the client is required to name; the reconciler adopts a declared identifier and generates one only in its absence.

Whoever consumes the identifier must be configured from the same declaration that produced it. An identifier written independently into a consumer's configuration is a second source for a value that has one authority, and it drifts silently: nothing fails until an authorization request is refused.

### Delivery is spoke-side only

Infisical is the System of Record. ESO delivers the credential from Infisical into the tenant namespace on the spoke, where the BFF workload consumes it. This is the ordinary tenant-secret path already used for tenant database credentials and requires no new delivery mechanism.

Where ordering between an ExternalSecret and a consumer matters, the platform's existing mechanism governs: sync waves combined with the ExternalSecret health rule, which reports healthy only on a true Ready condition. No additional dependency mechanism is introduced, and none is needed.

### Registration is reconciled from the credential's authority

The Hydra registration for a confidential client is reconciled by the authority that owns the credential, using the hub-operator's existing Hydra client. Registration is convergent: the desired client specification comes from the tenant's declared configuration, and the registration is created or updated to match.

Because one authority owns both the credential and the registration, the value in Infisical and the value Hydra accepts cannot diverge through independent reconciliation. This satisfies ADR-043's requirement of exactly one authority per domain, where the domain is the confidential client's lifecycle.

Convergence includes removing registrations the platform no longer declares. Registrations accumulated under generated identifiers, or left behind by a tenant that no longer exists, are not inert: each is a credential-bearing entry that still authenticates. Reconciliation must retire what is not declared, and that reconciliation is the only sanctioned way to remove one, because a registration deleted by hand is indistinguishable from one the platform never created and will be recreated or missed depending on which.

### Rotation is explicit and uninterrupted

Rotation is triggered by an explicit platform operation — a compromise response, a security policy requirement, or client recreation. It is not periodic. An OAuth client secret is not a certificate and has no expiry.

Rotation uses Hydra's rotated-secrets model, in which a newly issued secret becomes active while the previous secret remains accepted, and the previous secret is retired only after consumers have converged. The ordering is: the new value is issued to Hydra and made active while the old remains valid; the System of Record is updated; ESO converges the spoke; the workload reloads; the previous secret is retired.

Retirement is gated on observed convergence, not on elapsed time. A rotation that cannot confirm convergence leaves both secrets valid and surfaces as a degraded condition, because a stranded consumer authenticating with a retired secret is an outage, whereas an un-retired previous secret is a bounded exposure that remains under platform control.

The interruption present in a single-secret replacement is not an accepted cost of this design; it is the reason the rotated-secrets model is mandatory here and the reason confidential clients are not reconciled by a component that cannot express it.

### Alternatives considered

Pre-seeding a hub-side Kubernetes Secret from Infisical and having hydra-maester consume it was the previously proposed shape of this ADR. It satisfies the System of Record rule, and the adoption behaviour it depends on does hold in practice. But that behaviour is an observed property at one version rather than a published contract, so it would have to be re-established on every upgrade of a component the platform does not control. It reintroduces a first-provisioning ordering dependency between a delivered Secret and a controller's reconciliation, it cannot rotate without an interruption because a single secret name cannot express the rotated-secrets model, and it requires provoking reconciliation by annotation whenever the credential changes. Each of these is a permanent property of the component, not a defect to be fixed, so the design would remain dependent on behaviour the platform does not control.

Allowing hydra-maester to generate the credential and pushing it into Infisical was rejected under ADR-003, which rejects the pattern that makes Kubernetes the origin and Infisical the mirror.

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Confidential client credential | Infisical | hub-operator | ESO (spoke delivery) | BFF workload | Day-1+ |
| Confidential client registration | Hydra | hub-operator | hub-operator | auth-proxy, AgentGateway | Day-1+ |
| Public client registration | Hydra | fleet-registry | hydra-maester | AgentGateway | Day-1+ |

## Consequences

### Positive

- Infisical is the single System of Record for the confidential client credential, and no Kubernetes Secret is authoritative for it.
- The first-provisioning ordering hazard is removed by construction, because no hub-side Secret participates in the path.
- Correctness no longer depends on undocumented behaviour of a component the platform does not control, so hydra-maester upgrades cannot silently invalidate the credential path.
- Rotation is uninterrupted and is owned by the same authority that owns the credential, so the stored value and the accepted value cannot diverge.
- The credential lifecycle and the client registration share one authority, satisfying ADR-043.
- hydra-maester is retained where it is sound — declarative clients with no secret material — rather than removed or worked around.
- The tracked producer gap for the tenant BFF credential is closed by a real producer.
- Client identifiers are stable and declared, so a consumer can be configured before the client is registered, and a registration cannot silently acquire an identifier nothing is configured for.

### Negative

- The hub-operator's provisioning boundary grows to include confidential client registration and rotation, and it becomes a runtime dependency on Hydra's admin interface being reachable.
- Two mechanisms register OAuth clients: declarative resources for clients without secret material, and the operator for clients with it. The boundary is principled but must be understood before adding a client.
- Rotation requires observing consumer convergence before retiring the previous secret, which is more state than a single replacement carries.
- The first-provisioning marker must be derived from durable tenant status. Until it is, the ADR-003 protection against regenerating a lost credential remains unreachable.
- Retiring undeclared registrations makes reconciliation destructive to state the platform did not create, so a registration added outside the platform will be removed. That is the intended meaning of a single authority, but it forecloses registering a client by hand against a platform-managed Hydra.
- Public clients still name a Secret they do not use, because the resource schema requires one. It holds an identifier and no credential, which is a shape worth recognising rather than mistaking for a credential that failed to populate.

## Impact

- **Amends ADR-050.** The statement in the OAuth2Client template that hydra-maester creates the BFF client secret is withdrawn. Confidential clients are not reconciled by hydra-maester.
- **Amends ADR-047.** The fleet-registry declares the spoke-side delivery of the confidential client credential. No hub-side delivery is declared for it.
- **Corrects the AINativeSaaS reconciler.** The first-provisioning flag must be derived from the tenant's observed status rather than passed as a literal, restoring the ADR-003 rule that a previously generated secret missing from the System of Record fails rather than regenerates.
- **Closes a tracked gap.** The `PENDING` entry for the tenant BFF credential in `scripts/validate/preflight/95-infisical-key-producers.sh` is removed once the producer exists, and the validator then enforces it. Removing that entry is the acceptance gate for this ADR.
- **Preconditions, since resolved, that this ADR did not introduce.** Four independent defects had to be cleared before any tenant client could reach Hydra, and each is worth recording because none of them presented as an OAuth problem. The `ory-hydra` kustomization referenced a file outside its own root, which kustomize refuses, so ArgoCD reported that Application `Healthy` while its sync status was `Unknown` and it applied nothing at all — every manifest in that directory was inert. The Hydra NetworkPolicy admitted the admin port only from other namespaces, and hydra-maester runs in Hydra's own namespace, where a policy grants no implicit allowance; the resulting drop timed out rather than refusing, which read as an unreachable service. The public client omitted the required secret name and was rejected outright, so it never existed. And the identifier was generated rather than declared, so it could not match the configured consumer. An earlier draft of this ADR attributed the connectivity failure to a datapath fault of the ADR-046 addendum 27 class; that was wrong — Hydra and hydra-maester were on the same node and the cause was policy, not datapath.
- **Requires the identifier declaration to reach consumers.** The spoke gateway configuration currently carries a tenant's client identifier as a literal in a manifest shared by all tenants. That is a second source for a value this ADR gives one authority, and it must be supplied per tenant from the same declaration rather than written independently.
- **Requires reconciliation of existing registrations.** Registrations created under generated identifiers, including those already present on `hub-hybrid-dev`, are retired by the registration authority when it converges, not by hand.
- **Retains** `maester.enabled` for public clients and the guard that keeps OAuth2Client resources on the hub, both already applied.

## References

- ADR-003: Secret Management Architecture — System of Record boundary, first-provisioning rule, rejection of the inverted push pattern.
- ADR-039: Platform Ownership Model
- ADR-040: Day-0 vs Day-1 Lifecycle Boundary
- ADR-041: Controller Responsibility Matrix
- ADR-043: Control Plane Authority Model — single authority per domain.
- ADR-047: Fleet Tenant Deployment Contract
- ADR-050: Tenant Authentication via AgentGateway

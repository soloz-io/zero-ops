# ADR-053: Tenant OAuth Confidential Client Lifecycle

**Date:** 2026-08-25
**Status:** Proposed

## Context

Tenant authentication via AgentGateway (ADR-050) requires two OAuth2 clients per fleet: a public browser client using PKCE, which holds no secret, and a confidential BFF client authenticating with `client_secret_basic`, which holds secret material.

The platform currently contains a contradiction about who owns that secret material. The `universal-tenant` chart's OAuth2Client template states that hydra-maester creates the BFF client secret and that nothing has to pre-seed it. The fleet-registry values for the same tenant declare an ExternalSecret expecting to read that secret from Infisical. Both cannot hold. The consequence is observable: the tenant's ExternalSecret cannot resolve, because no component writes the value to Infisical, and `scripts/validate/preflight/95-infisical-key-producers.sh` carries this as a tracked `PENDING` gap with no producer.

If hydra-maester were the origin, the Kubernetes Secret it creates would be the System of Record for that credential. ADR-003 forbids this: no Kubernetes Secret may serve as a secret's System of Record; Kubernetes Secrets are a delivery cache. ADR-003 further rejects the inverse flow, where Kubernetes is the origin and Infisical the mirror, as an ownership inversion.

hydra-maester constrains any design that routes secret material through it. It reconciles only on changes to the OAuth2Client resource and does not observe Secret contents, so a delivered credential change does not converge. It exposes a single secret name and cannot express Hydra's rotated-secrets model, so it cannot support rotation without an interruption. Secrets it creates carry an owner reference that prevents another controller from adopting them. Its behaviour when a referenced Secret already exists is not part of any documented interface contract.

Two facts about the platform's existing capabilities bear on the decision. The hub-operator already owns direct Hydra client registration for platform clients through `internal/client/hydra.go`, including existence checks, creation and update. The hub-operator also already implements the ADR-003 first-provisioning rule for tenant credentials in `EnsureTenantFolderAndCredentials`, whose contract distinguishes a credential that is absent during first provisioning, which is generated, from one absent afterwards, which is reported as missing rather than regenerated. That second capability is currently defeated at its call site in the AINativeSaaS reconciler, which passes the first-provisioning flag as a literal true as a migration accommodation, so the protective branch is unreachable.

Ordering between a delivered Secret and a consuming controller is a solved problem in this platform. ArgoCD advances to the next sync wave only once the current wave reports healthy, and the platform defines a health rule for ExternalSecret that reports healthy only when its Ready condition is true.

## Decision

### The boundary is secret material, not client type

OAuth2 clients divide by whether they carry secret material, and that division determines which lifecycle governs them.

Clients that hold no secret material — the public browser client, and platform clients of the same shape — remain declarative resources reconciled by hydra-maester. There is no credential to generate, store, deliver or rotate, so ADR-003 does not apply and hydra-maester's constraints are irrelevant.

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

### Delivery is spoke-side only

Infisical is the System of Record. ESO delivers the credential from Infisical into the tenant namespace on the spoke, where the BFF workload consumes it. This is the ordinary tenant-secret path already used for tenant database credentials and requires no new delivery mechanism.

Where ordering between an ExternalSecret and a consumer matters, the platform's existing mechanism governs: sync waves combined with the ExternalSecret health rule, which reports healthy only on a true Ready condition. No additional dependency mechanism is introduced, and none is needed.

### Registration is reconciled from the credential's authority

The Hydra registration for a confidential client is reconciled by the authority that owns the credential, using the hub-operator's existing Hydra client. Registration is convergent: the desired client specification comes from the tenant's declared configuration, and the registration is created or updated to match.

Because one authority owns both the credential and the registration, the value in Infisical and the value Hydra accepts cannot diverge through independent reconciliation. This satisfies ADR-043's requirement of exactly one authority per domain, where the domain is the confidential client's lifecycle.

### Rotation is explicit and uninterrupted

Rotation is triggered by an explicit platform operation — a compromise response, a security policy requirement, or client recreation. It is not periodic. An OAuth client secret is not a certificate and has no expiry.

Rotation uses Hydra's rotated-secrets model, in which a newly issued secret becomes active while the previous secret remains accepted, and the previous secret is retired only after consumers have converged. The ordering is: the new value is issued to Hydra and made active while the old remains valid; the System of Record is updated; ESO converges the spoke; the workload reloads; the previous secret is retired.

Retirement is gated on observed convergence, not on elapsed time. A rotation that cannot confirm convergence leaves both secrets valid and surfaces as a degraded condition, because a stranded consumer authenticating with a retired secret is an outage, whereas an un-retired previous secret is a bounded exposure that remains under platform control.

The interruption present in a single-secret replacement is not an accepted cost of this design; it is the reason the rotated-secrets model is mandatory here and the reason confidential clients are not reconciled by a component that cannot express it.

### Alternatives considered

Pre-seeding a hub-side Kubernetes Secret from Infisical and having hydra-maester consume it was the previously proposed shape of this ADR. It satisfies the System of Record rule, but it makes correctness depend on hydra-maester consuming rather than replacing an existing Secret — behaviour that is not a documented contract and would have to be re-established on every upgrade. It reintroduces a first-provisioning ordering dependency between a delivered Secret and a controller's reconciliation, it cannot rotate without an interruption because a single secret name cannot express the rotated-secrets model, and it requires provoking reconciliation by annotation whenever the credential changes. Each of these is a permanent property of the component, not a defect to be fixed, so the design would remain dependent on behaviour the platform does not control.

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

### Negative

- The hub-operator's provisioning boundary grows to include confidential client registration and rotation, and it becomes a runtime dependency on Hydra's admin interface being reachable.
- Two mechanisms register OAuth clients: declarative resources for clients without secret material, and the operator for clients with it. The boundary is principled but must be understood before adding a client.
- Rotation requires observing consumer convergence before retiring the previous secret, which is more state than a single replacement carries.
- The first-provisioning marker must be derived from durable tenant status. Until it is, the ADR-003 protection against regenerating a lost credential remains unreachable.

## Impact

- **Amends ADR-050.** The statement in the OAuth2Client template that hydra-maester creates the BFF client secret is withdrawn. Confidential clients are not reconciled by hydra-maester.
- **Amends ADR-047.** The fleet-registry declares the spoke-side delivery of the confidential client credential. No hub-side delivery is declared for it.
- **Corrects the AINativeSaaS reconciler.** The first-provisioning flag must be derived from the tenant's observed status rather than passed as a literal, restoring the ADR-003 rule that a previously generated secret missing from the System of Record fails rather than regenerates.
- **Closes a tracked gap.** The `PENDING` entry for the tenant BFF credential in `scripts/validate/preflight/95-infisical-key-producers.sh` is removed once the producer exists, and the validator then enforces it. Removing that entry is the acceptance gate for this ADR.
- **Precondition, not introduced by this ADR.** hydra-maester on `hub-hybrid-dev` cannot currently reach Hydra's admin service and has not reconciled its OAuth2Client for 19 hours. Hydra is running with a healthy endpoint, so this is a datapath fault of the same class as ADR-046 addendum 27. It blocks public client registration today and must be resolved independently; this ADR removes confidential clients from the affected path but does not fix the fault.
- **Retains** `maester.enabled` for public clients and the guard that keeps OAuth2Client resources on the hub, both already applied.

## References

- ADR-003: Secret Management Architecture — System of Record boundary, first-provisioning rule, rejection of the inverted push pattern.
- ADR-039: Platform Ownership Model
- ADR-040: Day-0 vs Day-1 Lifecycle Boundary
- ADR-041: Controller Responsibility Matrix
- ADR-043: Control Plane Authority Model — single authority per domain.
- ADR-047: Fleet Tenant Deployment Contract
- ADR-050: Tenant Authentication via AgentGateway

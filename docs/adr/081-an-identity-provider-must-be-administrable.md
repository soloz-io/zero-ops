# ADR-081: An Identity Provider Must Be Administrable

**Date:** 2026-09-16

**Status:** Proposed

## Context

ADR-076 decides that day-to-day access to a box is a person authenticating against
the box's own identity provider, and that the admin kubeconfig is break-glass held
in escrow. It assumes, without saying so, that the identity provider can be
administered: the client the API server names has to be registered in it, and
identities have to be created and revoked there.

That assumption does not hold for how the platform provisions one today. The
instance is installed with no administrator credential configured and no
notification channel, which produces an initial human administrator with no
password and an activation path whose only delivery mechanism was never set up.

The mechanisms that would resolve it are themselves administrative. Delivery
channels are instance configuration, read from the instance's own state rather
than from static configuration, so enabling one requires the credential that
cannot be delivered. The identity provider's supported command-line surface
covers initialisation, schema setup, start, mirror, key handling and readiness;
it carries no administrator recovery operation, and the one command that once
resembled it is deprecated.

So a box can complete provisioning, report healthy, and contain an identity
provider that nobody can administer. Nothing observes this, because every check
the platform performs asks whether the component is running.

ADR-040 places one-time creation in Day-0 and continuous reconciliation in Day-1+.
This is a Day-0 concern precisely because the identity provider's own supported
mechanisms offer no equivalent afterwards.

## Decision

An identity provider is provisioned with a supported administrative credential
path at first-instance creation. A box whose identity provider has none is not
considered built.

The credential established at creation is a machine identity belonging to the
instance, and it exists to bootstrap the management plane: to establish subsequent
administrative access and to perform the client registration ADR-076 depends on.
It carries an explicit expiry, and it is revoked once initialisation completes.
The running box retains no standing management credential for its identity
provider, which ADR-076 already requires and this decision does not relax --
placing administration inside the system it authenticates would let any compromise
of the box rewrite its own login.

This is a creation-time invariant rather than a default. The identity provider's
supported administration mechanisms provide no equivalent retrofit once an
instance has been initialised without an administrator credential or a delivery
path: every remedy is an authenticated operation, and the authentication is what
is missing. A decision that can only be made once is made at the point it can
be made.

### Administrability is a post-install acceptance criterion

Whether a box's identity provider can be administered is asserted after
provisioning, alongside the checks that decide whether the platform is serving.

Readiness is not evidence of it. The failure this describes leaves every component
running, every reconciliation succeeding and every health check passing, while the
instance is unusable for the one purpose it was installed for. A property that no
check observes is one the platform discovers from a tenant, months later, at the
moment they first need to grant someone access -- and by then the only remedy is
to rebuild the box.

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Identity provider instance | the box | Tenant | ArgoCD | People, kube-apiserver | Day-0 |
| Administrative bootstrap identity | tenant's identity provider | Tenant | — | initialisation only | Day-0 |
| Bootstrap credential | tenant's identity provider | Tenant | — | initialisation only, then revoked | Day-0 |
| Administrability assertion | the platform's validation modules | Platform | — | the provisioning gate | Day-0 |

The bootstrap identity and its credential are the tenant's, in the tenant's own
identity provider, consistent with ADR-065. Neither has a running consumer: both
exist for the length of initialisation, which is the ownership statement. See
ADR-039 for the complete ownership matrix.

## Consequences

### Positive

A provisioned box can be administered by whoever owns it. ADR-076's model of named,
revocable human access becomes reachable rather than assumed, because the
registration it depends on is possible.

The failure is caught at provisioning rather than discovered when someone first
needs to grant access, which is the point at which it is no longer fixable.

Nothing is added to the running box. The credential that makes this possible is
revoked as part of the initialisation that uses it.

### Negative

It does not help an instance that already exists. Boxes provisioned before this
have no administrator credential and no supported path to acquire one, and the
remedy for them is to rebuild rather than to repair -- which for an identity
provider means the identities and clients it holds are recreated, not migrated.

It couples provisioning to the identity provider's own bootstrap model. The
invariant is stated in terms of a supported administrative credential path rather
than a particular mechanism, but the mechanism available is the one the identity
provider offers at instance creation, and a change to it is a change here.

A bootstrap credential exists, briefly, with broad privilege. That window is the
cost of not holding one permanently, and it is bounded by an expiry that the
interface requires rather than by a convention.

## Impact

- **Confirms ADR-076.** Its separation of identity-provider administration from
  cluster configuration is unchanged, as is its rule that the running hub holds no
  standing management credential. This supplies the precondition it assumed:
  that the identity provider can be administered at all.
- **Amends ADR-062.** A box is not successfully onboarded unless its identity
  provider is administrable, which joins the conditions scaffolding and bootstrap
  already enforce.
- **Constrains ADR-040.** The administrative credential path is established at
  creation and cannot be added by a Day-1 controller, because the operations that
  would add it are themselves authenticated.
- No change to ADR-063 or ADR-064: nothing here travels with the bundle or moves
  with a version.

## References

- ADR-039: Platform Ownership Model
- ADR-040: Day-0 vs Day-1 Lifecycle Boundary
- ADR-062: Onboarding and Scaffolding a Tenant
- ADR-065: The Control Plane Ships Into the Box
- ADR-076: Reaching a Box You Own

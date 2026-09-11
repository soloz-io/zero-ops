# ADR-076: Reaching a Box You Own

**Date:** 2026-09-11

**Status:** Proposed

## Context

ADR-065 gives each tenant a control plane in their own cloud account and says the
platform holds no credential of theirs. ADR-062 adds that exit is not a migration:
a tenant who stops buying maintenance keeps a running control plane.

Neither says how anyone reaches it. Five facts bear on that.

**Day-0 produces an admin credential and then discards it.** CAPI writes the
cluster's admin kubeconfig into a Secret, and the CLI copies it to
`k8-secrets/kubeconfig/<cluster>.kubeconfig`. That directory is gitignored, the
bootstrap workflow never mentions it, and it is not a registered ADR-045 artifact.
Under ADR-072 Day-0 runs on an ephemeral runner, so the file is destroyed with the
job. A tenant finishes onboarding owning a cluster they hold no credential for.

**The platform's own operation hid it.** Every bootstrap so far has been the
platform's, on a workstation where `k8-secrets/` persists. Nothing exercised "the
job ended, now what", so a tenant-facing gap looked like a working mechanism.

**Documentation already assumes access nobody has.** The restore runbook opens with
`kubectl -n platform-data get cluster platform-db` -- a command the tenant this
platform is built for cannot run.

**The box's own secret store cannot hold the answer.** Infisical runs inside the
cluster. A credential needed when the cluster is unreachable cannot live in it, and
proposing otherwise is circular.

**An escrow already exists, in the wrong place.** `hub-operator` copies the
Infisical master keys out of the box and restores them when the in-cluster copy is
gone. It is out-of-band, keyed by cluster, and it works -- but it wrote to AWS
Secrets Manager under one account and one prefix, which is the platform's while
every cluster is the platform's.

**Infisical is already the box's secret store, and Infisical has a hosted form.**
The client is the same code against a different URL: Infisical's own source connects
to other instances with `POST {instanceUrl}/api/v1/auth/universal-auth/login` and a
machine identity, and the platform's client already reads its base URL from the
environment. A hosted Infisical the tenant controls is therefore out-of-band without
being a second vendor, a second SDK, or a second thing to learn.

**The comparable system mints rather than stores.** kubefirst's API exposes
`POST /kubeconfig/:cloud_provider`, and the handler fetches from the cloud provider
using the caller's own cloud credential, which the request carries. Nothing is
retained. It also filters `terraform/base/kubeconfig` out of git commits, having
shipped one there once. That pattern is unavailable here: it depends on the cloud
provider having a "give me the kubeconfig" endpoint, and a CAPI cluster on plain
Hetzner servers has none -- the only copy lives inside the cluster.

## Decision

**A tenant reaches their box as a person, through their own identity provider. The
admin credential is a break-glass artefact, escrowed outside the box, in an account
the tenant controls.**

### Day-to-day access is OIDC, not a kubeconfig

The hub's API server is configured for OIDC from the ClusterClass -- `oidcIssuerURL`
and `oidcClientID`, applied together or not at all -- and the box runs an identity
provider. Access is therefore a person authenticating as themselves, with the
authorisation their role carries, revocable by removing them.

This is what makes ownership real rather than stated. A shared admin credential is
not owned by anyone: it cannot be attributed, it cannot be revoked for one person,
and the act of giving someone access is the act of copying it.

### The admin credential is escrowed, not distributed

The cluster's admin kubeconfig is what remains when OIDC cannot be used -- the
identity provider is down, the cluster is broken, or nobody is left who can log in.
It is escrowed beside the Infisical master keys, under the same
`/hub-operator/<clusterID>` path.

Escrow rather than distribution: one copy, in one place, reachable by the tenant and
not held by anyone as a matter of course.

### The escrow is an Infisical the tenant controls and the box does not host

Infisical Cloud, or an Infisical the tenant runs elsewhere. Not AWS, and not the
box's own.

Not the box's own, because the master keys are what decrypt it: an escrow inside the
thing it protects is not an escrow, and it is unreachable at exactly the moment it is
needed. The implementation refuses an escrow URL equal to the in-cluster one rather
than accepting a configuration that cannot work.

Infisical rather than AWS because the box already runs Infisical, the API is the
same one, and the client already takes its URL from the environment. Requiring AWS
would add a second cloud account, a second credential type and a second SDK to a
platform that otherwise needs one cloud -- and ADR-070 measures the floor in exactly
those terms.

### The escrow account is the tenant's

The credentials reaching it are supplied with the tenant's others at scaffold time
and stored on their repository. The platform never holds them.

This is not a preference. The escrow holds the key to a box's secret store and the
key to its API server -- the two credentials that, together, are the box. ADR-065
says the platform holds no cloud, cluster or secret credential belonging to a
tenant, and an escrow in the platform's account would hold both. In the tenant's
account the platform holds neither, and the tenant can revoke it without asking.

The existing mechanism moves with it: the same client, the same `clusterID`-scoped
path, a different account.

### The escrow is required

A box is not built without one. Scaffolding refuses to complete, and a bootstrap
refuses to start, when the escrow credentials are absent.

Optional was considered and is refused for one reason: the cost of not having an
escrow is paid entirely in the future, by someone who did not make the choice. A
box without one bootstraps, runs, and behaves identically for months. The
difference appears on the day the cluster is gone -- and on that day the escrow
cannot be added, the Infisical master keys are unrecoverable, and every secret the
box held is lost with it. A default whose consequence is invisible until it is
irreversible is not a choice a tenant is making; it is one being made for them.

The platform is sold as maintenance for organisations who cannot staff a platform
team (ADR-069). Handing such a tenant a box that is one incident away from total
loss, and recording that they opted into it, is not a service.

This raises the floor, and the choice of Infisical over a second cloud vendor is
what keeps the rise small: an account on the product the box already runs, free at
the size these boxes start at.

### Nothing is committed to git, encrypted or not

An encrypted kubeconfig in the tenant's repository was considered and is rejected
below. The short reason is that git history is permanent: ciphertext committed today
is decryptable by whoever holds the key in five years, and a credential with a
one-year life leaves four years of exposure for no benefit.

## Alternatives considered

**Escrow to AWS Secrets Manager.** Rejected, having been built. The mechanism
worked and the cost was structural: every tenant would need an AWS account for a
platform that otherwise needs one cloud, and a required second vendor raises
ADR-070's floor for everyone to serve a case Infisical already covers. The IAM
policy was also account-wide and prefix-scoped, so using it as-is would have put
tenants' keys in the platform's account -- and moving it to theirs solved custody
while leaving the second-vendor cost in place.

**SOPS-encrypted kubeconfig in the tenant's repository.** Rejected, though it is the
closest workable alternative and solves the out-of-band problem the same way. Three
costs decide it. Git history is permanent, so the ciphertext outlives the credential
by years and the encryption key becomes the only thing standing between a repository
read and cluster admin. It introduces a second secret mechanism alongside the
Infisical and ESO path the box already runs, with its own key distribution. And the
key itself must live somewhere the cluster is not, which is the question the proposal
was meant to answer. An escrow in a secret store the tenant already authenticates to
answers it once.

**Escrow in an account the platform controls.** Rejected whatever the vendor. It is
the smallest change -- the mechanism exists and works -- and it makes the platform
custodian of the two credentials that constitute a tenant's box. ADR-065 exists to
prevent exactly that, and the convenience is entirely the platform's.

**An optional escrow, defaulting to off.** Rejected, having been written. It keeps
the floor where ADR-070 puts it and keeps the platform usable by a tenant with one
cloud account -- both real. It fails on when the cost lands: a box without an escrow
is indistinguishable from one with it until the cluster is lost, at which point the
choice cannot be revisited and the box cannot be rebuilt. The tenants this platform
is for are the least equipped to evaluate that trade at scaffold time and the most
harmed by getting it wrong.

**An optional escrow that warns loudly.** Rejected for the same reason, one step
later. A warning at the moment of creation is read by someone who has not yet
operated the box, and is not read again. The platform would have said the true
thing and still shipped the unrecoverable box.

**Mint on demand from the cloud provider, as kubefirst does.** Not available. It
depends on the provider exposing the cluster's kubeconfig, which is true of managed
Kubernetes and false of a CAPI cluster on plain servers: the only copy is the Secret
inside the cluster, so fetching it requires the access it would grant. Should a
managed-Kubernetes provider be supported, this becomes the better answer for that
provider and this decision is revisited for it.

**Distribute the admin kubeconfig to the tenant at handover.** Rejected. It is what
the platform accidentally does today for its own box, and it is the arrangement
every other decision here exists to avoid: a credential that cannot be attributed,
cannot be selectively revoked, and is copied by the act of granting access.

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Human access to a box | tenant's identity provider | Tenant | — | People | Day-1+ |
| API server OIDC configuration | `zero-ops` ClusterClass | Platform | CAPI | kube-apiserver | Day-0 |
| Admin kubeconfig | the cluster's CAPI Secret | Tenant | CAPI | break-glass only | Day-0 |
| Escrowed copy | tenant's Infisical (Cloud or self-run) | Tenant | hub-operator | break-glass only | Day-1+ |
| Escrow credentials | `<tenant>-gitops` repository secrets | Tenant | — | hub-operator | Day-0 |

The platform appears in no row but the one describing what it builds. See ADR-039
for the complete ownership matrix.

## Consequences

### Positive

A tenant can reach the box they own, which ADR-062 and ADR-065 both assume and
neither provided. The independence those decisions claim becomes testable.

Access is attributable and individually revocable, so granting it to a colleague is
not the same act as copying a credential.

The escrow holds what a box cannot be rebuilt without, in an account the platform
cannot read. Losing the cluster stops being the same as losing its secrets.

One escrow mechanism serves both the Infisical master keys and the admin kubeconfig,
rather than a second one appearing for the second secret.

### Negative

A tenant needs an Infisical account outside their box. It is free at the size these
boxes start at and it is the same product the box already runs, but it is still a
second account and a second place to lose access to.

The escrow is a third party in the recovery path. A tenant who cannot reach
Infisical Cloud cannot reach their break-glass credential, and ADR-063's custody
argument is precisely about not depending on services that can be withdrawn. An
Infisical the tenant self-hosts elsewhere answers this and costs them an instance to
run; both are permitted and the choice is theirs.

The escrowed kubeconfig expires. Nothing configures certificate rotation, so
kubeadm's one-year client certificate applies, and an escrowed copy silently stops
working. Detecting that is not yet built, and a break-glass credential discovered to
be expired during an incident is worse than one known to be absent.

An escrow nobody has restored from is a backup nobody has tested. The same is true
today of the Infisical master keys, and adding a second artefact to an untested path
does not make it tested.

OIDC access depends on the box's own identity provider, so losing it costs both the
normal path and one of the two reasons the break-glass path exists. The escrow
covers it, which is why the escrow is not optional in practice for anyone who cares
about recovery.

### Positive and negative at once

Making the escrow optional keeps the platform usable by a tenant with one cloud, and
guarantees some boxes will be unrecoverable. Both follow from the same choice, and
the second is stated at scaffold time rather than discovered.

## Impact

- **Amends ADR-062.** Onboarding produces a box the tenant can reach. The escrow
  credentials join the secrets scaffolding asks for and stores on the tenant's
  repository.
- **Confirms ADR-065.** The platform holds no cloud, cluster or secret credential
  belonging to a tenant, including the escrow. Exit remains not a migration: a
  tenant keeps a running control plane and, now, the means to enter it.
- **Amends ADR-045.** The admin kubeconfig is a bootstrap-generated artefact whose
  destination is an escrow rather than the repository, because it is a credential
  and the registry's artefacts are configuration.
- **Constrains ADR-070.** The escrow is required, so it is part of the floor. It is
  an Infisical account rather than a second cloud vendor precisely so that floor
  moves as little as possible: free at the size these boxes start at, and the same
  product the box already runs.
- No change to ADR-063 or ADR-064: nothing here travels with the bundle or moves
  with a version.

## References

- ADR-039: Platform Ownership Model
- ADR-045: Bootstrap-Generated GitOps Artifacts
- ADR-062: Onboarding and Scaffolding a Tenant
- ADR-065: The Control Plane Ships Into the Box
- ADR-070: The Minimum Supported Box
- ADR-071: How This Platform Differs from kubefirst
- ADR-072: Tenant-Controlled Day-0 and Declarative Cluster Lifecycle

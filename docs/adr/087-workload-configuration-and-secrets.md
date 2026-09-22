# ADR-087: Workload Configuration and Secrets Are Separate, and Declared Before They Are Supplied

**Date:** 2026-09-23

**Status:** Proposed

*Constrained by: ADR-003 (ESO–Infisical Secret Management), ADR-030 (Secret Traversal Boundaries), ADR-039 (Platform Ownership Model), ADR-047 (Fleet Tenant Provisioning), ADR-053 (OAuth Client Provisioning), ADR-065 (The Control Plane Ships Into the Box), ADR-073 (A Fleet Chooses Versions, Not Locations), ADR-084 (Tenant Workload Deployment)*

> A fleet declares every value its workloads read. The platform generates what it
> owns, git carries what is not secret, and what remains is supplied once and
> verified before anything consumes it.

## Context

ADR-084 establishes what a fleet declares in order to RUN an application: a chart
version, and nothing else. It is silent on what that application READS at
runtime, and that gap is where the values live.

A fleet's workloads read three kinds of value, and they differ by who owns the
value rather than by how it is delivered:

- Values the PLATFORM generates because it owns the capability behind them —
  OAuth client credentials (ADR-053), the cache credential, database credentials,
  the tenant owner's initial password. A fleet declares the capability; the value
  is created and stored by the platform and is never seen by the fleet.
- Values the TENANT owns that are not secret — a model name, a bucket name, an
  API base URL, a provider endpoint identifier. Nothing is protected by
  concealing them and nothing is exposed by publishing them.
- Values the TENANT owns that ARE secret — an API key, an access key, a webhook
  signing secret, a token shared between two of the fleet's own workloads. The
  platform cannot generate these; they originate outside the box.

Observed on the waypoint fleet, one ExternalSecret carried twenty-three keys
consumed by two workloads. Two were platform-generated, ten were tenant secrets,
and **eleven were not secret at all**. Configuration reached a secret store
because a secret store was the only declared mechanism, and everything that
needed delivering used it.

Three consequences follow, and all three were observed rather than predicted.

**An ExternalSecret is atomic.** One key absent from the provider fails the whole
object, and every workload reading any key in it stays in
`CreateContainerConfigError`. Grouping unrelated values by delivery mechanism
therefore couples their failures: a mistyped bucket name withholds an OAuth
credential.

**The failure surfaces far from its cause.** On 2026-09-22 a tenant's cache
credential was absent, its ExternalSecret did not resolve, the BFF could not
start, its Service had no endpoints, and the gateway answered
`/api/v1/auth/me` with `503 ... backends required DNS resolution which failed`.
Nothing in that chain named a secret. The same sequence is recorded against the
same endpoint on 2026-09-02.

**Configuration in a secret store cannot be reviewed.** It is not in the diff of
the change that alters behaviour, it requires a credential to read, and altering
a model name becomes a rotation rather than a commit.

The platform already answers this question at its own boundary. Day-0 refuses a
box that selected a capability it holds no credential for, names the capability
rather than the key, and distinguishes a credential that is REFUSED from one that
is merely REPORTED. That model stops at the platform edge; a fleet's workloads
have no equivalent.

## Decision

**A fleet declares every value its workloads read, in its own repository, split
by who owns the value.**

**Platform-generated values are declared as capabilities, never as values.** A
fleet that declares a confidential OAuth client, a cache, or a database has
thereby declared its credentials. The fleet names no key, supplies no value, and
holds no copy. This is ADR-053 extended to every platform-owned credential rather
than restated per capability.

**Tenant configuration is declared in the fleet's values and delivered as a
ConfigMap.** It lives in git, is reviewed in the change that alters it, and is
readable without a credential. The platform already delivers a ConfigMap of
platform-owned endpoints into every tenant namespace; this is its tenant-owned
counterpart, and the two are separate because their Systems of Record differ.

**Tenant secrets are declared in git and supplied out of band.** The declaration
names the key, the capability it serves, and the workloads that read it. The
VALUE is never in the repository in any form — not encrypted, not templated, not
in an ignored file. Infisical remains the System of Record for secret material
(ADR-003); this ADR governs what must be declared and when it is verified, not
where it is stored.

Three properties make the declaration load-bearing rather than documentation:

**A declared secret names its capability.** The capability, not the key, is what
an operator can act on: `AI_GATEWAY_API_KEY is missing` asks the reader to know
what that is, and `the AI gateway the SDK calls has no credential` does not. This
is the same field, for the same reason, as the platform's own credential model.

**A declared secret names its consuming workloads, and delivery is grouped by
workload.** One ExternalSecret per workload, not one per fleet. Because the
object is atomic, the set of keys inside one is the blast radius of any one of
them being wrong — so that set must be a set that fails together usefully. A
fleet's SDK losing its WhatsApp credential must not also stop its BFF.

**A declared secret is verified before it is consumed.** Presence of every
declared key is asserted against the provider as a precondition of the fleet's
workloads being admitted, not discovered when a container fails to start. The
check reports key NAMES and never values.

**Supply is an explicit, auditable operation.** `soloz fleet secrets` is the
sanctioned path and the only one. It reports which declared secrets are present
and which are absent, by NAME and with the capability each serves, and never
discloses a value: the type returned by the listing has no field a value could
occupy, so a value cannot reach a terminal or a scrollback buffer by a
formatting mistake.

Values are supplied one key per invocation, read from the terminal with echo
disabled or piped from a password manager. There is no flag that takes a value
as an argument -- that puts it in shell history and in the process table -- and
no batch form, because a batch means a file and a file means secrets at rest on
an operator's machine.

Rotation is the same operation performed again. The platform implements no
two-phase rotation, so a separate verb would imply a ceremony that does not
exist; the value is replaced, the delivery mechanism observes the change on its
next refresh, and the workload restarts on it.

Bulk import exists only to migrate a fleet that predates this ADR. It writes
only keys the fleet DECLARES -- a legacy seed file carries configuration and
connection material alongside the secrets, and writing all of it would return
configuration to the store this ADR moved it out of -- lists what it would write
before writing anything, and reports afterwards that the file it read is now a
second copy that nothing rotates.

### Alternatives considered

**Encrypted values committed to git (SOPS or sealed secrets).** Makes rotation a
commit and gives review and history for free. Rejected because ADR-003 makes
Infisical the System of Record for secret material and a second authoritative
store is the condition ADR-039 exists to prevent. The review property is
recovered by declaring the key in git while the value is not.

**Keeping one ExternalSecret per fleet.** Fewer objects, and the grouping matches
how a fleet thinks about itself. Rejected because the object's atomicity makes
the grouping a failure-coupling decision, and a fleet is the wrong unit: its
workloads fail independently in every other respect.

**Delivering configuration through the same secret path.** No new mechanism.
Rejected because it is what produced eleven non-secret keys in a secret store,
and because it makes a reviewable change unreviewable.

## Ownership

Per ADR-039.

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Fleet configuration and secret DECLARATIONS | Tenant GitOps repository | Tenant | ArgoCD | universal-tenant | Day-1+ |
| Platform-generated credential VALUES | Infisical | Platform | hub-operator | ESO | Day-1+ |
| Tenant secret VALUES | Infisical | Tenant | `soloz fleet secrets` | ESO | Day-1+ |
| Tenant configuration VALUES | Tenant GitOps repository | Tenant | ArgoCD | universal-tenant | Day-1+ |
| Per-workload ExternalSecret | Tenant GitOps repository | Platform | ArgoCD | Workload | Day-1+ |
| Workload ConfigMap | Tenant GitOps repository | Platform | ArgoCD | Workload | Day-1+ |

The declaration and the value have different Systems of Record for tenant
secrets, deliberately: the contract is reviewable and the value is not present.

## Consequences

### Positive

- A value's owner determines its mechanism, so configuration is reviewable in the
  change that alters it and secrets are not in the repository at all.
- One workload's missing secret stops that workload. The blast radius of an
  atomic object is bounded by the workload that reads it.
- A missing secret is reported as a missing secret, naming the capability,
  before any workload is admitted — rather than as a container that will not
  start, or as a 503 on an unrelated endpoint.
- A fleet's full runtime input is legible from its repository without a
  credential: every key is named, and only the secret values are elsewhere.
- Adding a capability adds its credentials automatically, because the capability
  is the declaration.

### Negative

- More objects. A fleet with four workloads has four ExternalSecrets where it had
  one, and the shared keys among them are declared more than once.
- A tenant secret's value is still not in git, so its history is not in git.
  Rotation is auditable in the provider and in the preflight's report, not in a
  diff.
- Classifying a value as configuration rather than secret is a judgement, and a
  wrong one publishes something that should not have been. The declaration makes
  the judgement explicit and reviewable, which is a mitigation and not a
  guarantee.
- Fleets predating this ADR carry a single mixed secret and must be migrated.

## Impact

Amends ADR-047, which establishes fleet-declared ExternalSecrets as a Tier 2
platform-rendered resource but does not constrain their grouping and does not
distinguish configuration from secret material. Grouping is now per workload, and
configuration is no longer delivered by this path.

Extends ADR-053 from OAuth clients to every platform-generated credential: the
principle that a fleet declares a capability and the platform provisions its
credential is the general rule, not a property of OAuth.

Extends ADR-084, which governs how a workload is deployed, to what it reads.

Does not amend ADR-003. Infisical remains the System of Record for secret
material and the ESO delivery path is unchanged.

## References

- ADR-003: ESO–Infisical Secret Management
- ADR-030: Secret Traversal Boundaries
- ADR-039: Platform Ownership Model
- ADR-040: Day-0 vs Day-1 Lifecycle Boundary
- ADR-047: Fleet Tenant Provisioning
- ADR-053: OAuth Client Provisioning
- ADR-084: Tenant Workload Deployment

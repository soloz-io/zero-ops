# ADR-088: The Tenant and the Application Are Different Axes

**Date:** 2026-09-24

**Status:** Proposed

## Context

ADR-047 records `fleetId` is `tenantId` throughout, and ADR-062 restates it. One
identifier therefore names two things: the organisation whose box this is, and a
product running on it. The platform has no way to say that two products belong
to the same customer, because as far as the runtime is concerned they are two
customers.

Five facts bear on this.

**The decision record already defines a tenant as the box's owner.** ADR-062:
"A tenant is onboarded by creating and scaffolding the repositories *its box*
reconciles." That is a one-to-one relation between a tenant and a box. On this
box the owner is `nutgraf` — `nutgraf-hub`, `nutgraf-01`, `dev.nutgraf.in` — and
`waypoint` is a product it runs. The runtime says `tenantId: waypoint` and has no
identifier for nutgraf at all.

**The record already flags the vocabulary as broken.** ADR-062's own context:
"The word 'fleet' names two different things in the decision record. ADR-033 uses
it for the cluster estate. ADR-047 uses it for a single tenant's deployment
boundary. The implementation follows neither consistently." What was not noticed
is that collapsing them removed a level rather than renaming one.

**The inversion is legible in the paths the platform already writes.**

```
/spoke-pool/nutgraf-01/tenants/waypoint/OIDC_CLIENT_ID
             ^^^^^^^^^         ^^^^^^^^
             the customer      the product
```

The cell segment carries the tenant and the segment called `tenants` carries the
application. The same inversion is in the namespace `tenant-waypoint`, in the
`tenant-id` label Kyverno requires, and in every CiliumNetworkPolicy selector
that reads `tenant-id`.

**The identity provider already has the axis, and the platform uses one half of
it.** Zitadel's model is organisation → project → roles.
`internal/kube-sbt/providers/zitadel/auth.go` calls
`ensureProject(ctx, user.TenantID, a.cfg.ProjectName)`: the organisation is the
tenantId and the project is a single name from configuration, so every box has
one project per organisation. The scoped-roles claim
`urn:zitadel:iam:org:project:roles` is already nested per granting organisation.
An org of `nutgraf` with projects `waypoint` and `oranger` is the shape Zitadel
expects and the shape the platform declines to use.

**The missing level has already been paid for.** On 2026-09-24 `oranger-serve`
was deployed into `tenant-waypoint` with `tenantId: waypoint`, though it is built
in a different repository and is a different product, because the waypoint SDK
calls it on every message and a `tenant-oranger` namespace would have put a
tenant boundary across that call. The workload's own values say so. That was the
correct decision available at the time and it is the wrong shape: one product was
folded into another's isolation boundary because there was no way to express two
products of one customer.

## Decision

**`tenantId` names the organisation whose box this is. `appId` names a product
running on it. They are separate fields, and neither defaults to the other.**

For this box: `tenantId: nutgraf`, and `appId` is `waypoint` or `oranger`.

### What each axis governs

| | `tenantId` | `appId` |
|---|---|---|
| Identity | Zitadel organisation | Zitadel project within it |
| Namespace | — | `{tenantId}-{appId}` |
| Secret prefix | `/spoke-pool/{cellId}/tenants/{tenantId}/` | `.../apps/{appId}/` |
| Cost attribution | `tenant-id` label | `app-id` label |
| Network boundary | intra-tenant egress is expressible | default-deny per app |
| Repository | one GitOps repository per tenant | one directory per app |

The cell keeps naming the cluster, unchanged. `cellId` is not a third name for
the tenant; a tenant may hold several cells (ADR-051).

### The boundary that was missing

An application is the isolation unit and remains default-deny, exactly as
today: `appId` gets the namespace, the ServiceAccount, the policy and the ABI
labels. What changes is that **an app may declare egress to a sibling app of the
same tenant**, and the platform can render that allowance, because it can now
tell that they are siblings.

That is the whole practical gain. `oranger-serve` becomes an app of tenant
`nutgraf` in namespace `nutgraf-oranger`, and waypoint's SDK declares an
intra-tenant dependency on it rather than the two sharing a namespace.

Cross-*tenant* traffic remains refused and gains no mechanism here.

### What Kyverno enforces

`enforce-tenant-abi` requires `tenant-id` today. It gains `app-id` as a second
required label. Both are required because attribution needs both: a cost line
against `nutgraf` alone cannot be split between products, and one against
`waypoint` alone cannot be billed to anyone.

### The names

| | before | after |
|---|---|---|
| Namespace | `tenant-waypoint` | `tenant-nutgraf-waypoint` |
| Labels | `tenant-id: waypoint` | `tenant-id: nutgraf`, `app-id: waypoint` |
| Secret prefix | `/spoke-pool/nutgraf-01/tenants/waypoint/` | `/spoke-pool/nutgraf-01/tenants/nutgraf/apps/waypoint/` |
| Database | `tenant-waypoint-db` | `nutgraf-waypoint-db` |
| Zitadel | org `waypoint`, one project | org `nutgraf`, project `waypoint` |

`tenant-` is kept as the namespace prefix. It already distinguishes tenant
namespaces from `platform-*` ones, and dropping it to save five characters would
make `nutgraf-waypoint` indistinguishable from a platform component by name
alone.

### Migration: one change, not a sequence

This is migrated in a single stretch, and the sequencing that would normally
apply does not, for one reason: the namespace changes. A namespace rename is a
delete and recreate, so every workload in it stops regardless of how carefully
the surrounding steps are ordered. Phasing the labels, the Kyverno rule and the
secret paths around an event that takes the whole namespace down buys nothing —
it spreads one outage across five changes and leaves the fleet in a half-renamed
state between each, where a selector matching neither name is indistinguishable
from one matching nothing.

So: one change, applied while the fleet is down, in this order within it.

1. **Copy the secret material** to the new prefix, leaving the old in place.
   Nothing reads the new path yet, and the old path stays readable until the
   last step, so this is reversible on its own.
2. **Render everything against both axes** — charts, Kyverno ABI, spoke-catalog
   policies, AppSet generators, `soloz-cli` scaffolding and path builder,
   workload charts in every application repository.
3. **Delete the old namespace and let ArgoCD create the new one.** This is the
   outage. The CloudNativePG cluster, the object store and Infisical all live
   outside the tenant namespace and are untouched by it.

   **But the database NAME is derived from the axes, and renaming it does not
   migrate anything.** An app that predates this split holds its data in
   `tenant-<app>-db`; letting the name follow the rename provisions a second,
   EMPTY database beside it and points the workload at that. The first sign is
   a workload that starts cleanly with no history, which is the worst shape a
   data loss can take because nothing reports it.

   So a pre-existing app PINS `database.name` to what it already has. Only an
   app created after this ADR takes the derived name. This is the one place
   where an identifier rename and a data migration were about to be confused,
   and the distinction is the whole reason the pin exists.
4. **Split the Zitadel project** so each app is a project in the tenant's
   organisation.
5. **Remove the old secret prefix** once the fleet is Healthy on the new one.

Step 5 is the only irreversible one and it is last. Until it runs, the old
material is intact and the previous bundle version still resolves against it,
which is what makes a single stretch safe rather than reckless.

**What this costs:** the tenant's workloads are down from step 3 until ArgoCD
reports Healthy. On a dev box that is minutes. On a box carrying production
traffic this decision would be taken differently, and an app-at-a-time
migration behind a second namespace would be worth its extra states — that is
not the situation here, and writing a phased plan for a box that does not need
one would be planning for an imagined constraint.

### Alternatives considered

**Keep one axis and rename it to `appId`, accepting that one box is one
customer.** Rejected, though it is the smaller change and was the option this
ADR's author initially preferred. It is honest about today — the isolation the
platform provides per product is stronger than per customer, so the mechanism is
sound and only the vocabulary misleads. But it forecloses two things the product
model requires: a customer running several products with any shared state
between them, and cost attribution that rolls up per customer. Both are asked
for by the first customer with two products, which this box already is.

**Introduce the axis only in the identity provider, leaving the runtime alone.**
Rejected. It would make Zitadel correct and every other system wrong, and the
claim the BFF validates would then disagree with the namespace the workload runs
in.

**Use `cellId` as the tenant.** Rejected. It is already true on this box, by
accident — `nutgraf-01` names the customer — but a tenant may hold several cells
(ADR-051), so the identity of a customer cannot be the identity of one of their
clusters.

**Do nothing, and continue folding sibling products into one namespace.**
Rejected, and recorded because it is what happens by default. It works until two
products of one customer need different secrets, different RBAC or different
blast radius — at which point they are sharing an isolation boundary precisely
because nobody wrote down that they should not.

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Tenant identity (`tenantId`) | tenant GitOps repo | Platform | hub-operator / Zitadel | Apps | Day-0 |
| Application identity (`appId`) | tenant GitOps repo | Tenant | ApplicationSet | Workloads | Day-1+ |
| Application namespace | `zero-ops` (types) | Platform | universal-tenant chart | Workloads | Day-1+ |
| Intra-tenant egress | `zero-ops` (types) | Platform | universal-tenant chart | Apps | Day-1+ |
| Per-app database, cache, storage prefix | `zero-ops` (types) | Platform | Crossplane `provider-sql` / CNPG | One app | Day-1+ |
| Shared data grant (`sharedData`) | tenant GitOps repo | Tenant | Crossplane `provider-sql` | Named readers | Day-1+ |
| Cross-tenant egress | — | — | — | — | refused |

## Consequences

### Positive

Two products of one customer can be expressed, which is the case this box
already presents and which was resolved on 2026-09-24 by putting one product in
the other's namespace.

Cost attribution rolls up. `tenant-id` + `app-id` gives both the per-customer
total and the per-product split; today's single label gives neither reliably.

The identity model stops fighting the runtime. Zitadel's org → project is used
as designed rather than pinned to one project per org.

Secret paths stop lying. `/spoke-pool/{cell}/tenants/{tenant}/apps/{app}/` reads
the way the hierarchy actually is.

### Negative

It is a large migration across 34 files carrying `tenant-id` and 21 carrying the
namespace pattern, plus the Kyverno ABI, the XRD, the AppSet generators, the
Infisical layout and every workload chart in every application repository. It is
applied as one change, so it is reviewed as one change — there is no smaller
increment that leaves the fleet in a working state.

The fleet is down from the namespace delete until ArgoCD reports Healthy. That
is the price of doing it in one stretch, and it is accepted here because this
box is dev.

A box already carrying production traffic cannot take this migration as written.
It would need the app-at-a-time path this ADR declines to specify, and
specifying it now — for no box that exists — would be designing against an
imagined constraint.

Two required labels is a stricter admission contract than one. Any workload
chart not updated is denied at admission, and that denial reads as a workload
that never appears (ADR-047).

`fleetId` remains a third word for something. This ADR does not rehabilitate it;
ADR-033 uses it for the cluster estate and that use stands.

### Shared state between apps of one tenant

**Data follows the application, and sharing is declared, never inherited.**

Each app gets its own logical database, its own cache and its own storage
prefix, on the same physical CloudNativePG cluster and the same object store the
platform already provides. Being siblings grants nothing by itself: two apps of
`nutgraf` are as separated in their data as two apps of different tenants, and
for the same reason — the namespace is the isolation boundary, and a datastore
reachable from two namespaces is a hole in both.

Sharing is possible, and takes a declaration that names both sides:

```yaml
sharedData:
  - name: user-directory
    owner: waypoint          # the app whose database it is
    readers: [oranger]       # apps granted a role on it
```

The platform renders a Crossplane `provider-sql` grant for each reader and the
egress allowance that lets it connect. Absent a declaration there is no grant
and no route, so an app cannot reach a sibling's data by being in the same
tenant.

This is decided here rather than deferred because deferring it is what produces
the backdoor: with the axis in place and no rule, "same tenant" becomes an
argument for access, made case by case, by whoever is unblocking something. The
rule is that it is never an argument — only a declaration is.

The declaration is deliberately not a way to share a *database*: `owner` keeps
the database, and a reader is granted a role on it. Two apps owning one database
would make schema migration a negotiation between two release cycles, which is
the coupling ADR-074 left the platform least able to absorb.

## Impact

- `manifests/tenants/charts/universal-tenant` — `appId` value, namespace
  template, `app-id` label, intra-tenant egress rendering
- `manifests/spoke/spoke-catalog/infra/kyverno-tenant-abi.yaml` — `app-id`
  required
- `manifests/spoke/spoke-catalog/infra/tenant-{dns,identity}-egress.yaml` —
  selectors keyed on both axes
- `manifests/argocd/environment-manager/templates/05-tenant-fleet-appset.yaml` —
  generators supply both
- `internal/kube-sbt/providers/zitadel` — project per app, not per config
- `internal/soloz-cli` — scaffolding, `soloz fleet secrets`, the secret path
  builder
- Every workload chart in every application repository — `app-id` label

## References

- ADR-033 — fleet as the cluster estate; the other use of the word
- ADR-047 — the deployment contract that collapsed the axes, amended by this
- ADR-051 — a tenant may hold several cells
- ADR-062 — defines a tenant as the owner of a box; amended by this
- ADR-064 — TenantID on the workload cluster
- ADR-087 — configuration and secrets; the path layout this changes

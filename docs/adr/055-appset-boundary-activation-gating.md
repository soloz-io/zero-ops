# ADR-055: Boundary Activation as the Day-0 Gating Mechanism

**Date:** 2026-08-27
**Status:** Proposed

*Amended by: ADR-064 (Bundle Promotion and Cell-Scoped Policy)*

> Boundary activation is unchanged. What a boundary renders is selected by the
> bundle version the cluster pins.

## Context

ADR-021 split the platform into independent boundaries for blast-radius isolation and assigned Day-0 choreography to the CLI. ADR-040 lists "gate the activation of ArgoCD boundaries" as an allowed Day-0 responsibility and defines the ownership handoff to Day-1 controllers. ADR-042 defines the phases that sequence those boundaries. ADR-038 requires that platform-owned Applications track their environment revision continuously.

The boundary ApplicationSets are produced by rendering the `environment-manager` chart and applying the rendered output directly. No ArgoCD Application and no Helm release owns the result. Five consequences follow, all observable in the platform today.

**Git is not authoritative for boundary content.** An element added to a boundary template does not reach the cluster until a render is repeated. The Applications an ApplicationSet generates do track their revision continuously, so the drift is confined to the boundary's *application inventory* — which Applications exist at all — rather than to the manifests those Applications deploy. This narrows the failure but does not remove it: a platform service added to a boundary is absent from the cluster with no reconciliation error anywhere, because nothing has been asked to reconcile it.

**Activation and content are the same act.** Sequencing is expressed by rendering a subset of the chart, so a partially rendered boundary set is indistinguishable from a complete and authoritative one. Ordering cannot be varied without varying what exists.

**Authority over a boundary is not exclusive.** Two independent render paths exist with different value sets; whichever applies last is authoritative. A render that omitted the public TLS issuer policy (ADR-051) has already overwritten a correct ApplicationSet with one that could not render. The resulting Application reported `Healthy` while managing no resources, and previously issued tenant certificates were left unreconciled with no failure surfaced.

**Activation cannot be replaced by a declarative sequencer.** Day-0 interleaves work between boundaries: trust anchor injection falls between boundaries 02 and 03, and Infisical API bootstrap between boundaries 03 and 04 (ADR-042). ApplicationSet progressive sync ordering orders Applications relative to one another and cannot suspend for state established outside ArgoCD. Boundary sequencing must therefore remain externally driven.

**Hub boundaries have no project scoping.** Every hub boundary Application is assigned to the `default` AppProject, with no restriction on source repositories, destinations, or resource kinds. Project scoping exists for spoke pool infrastructure and for tenant workloads (ADR-021), but not for the hub's own boundaries. The AppProjects that do exist are reconciled inside boundary 01 and therefore cannot constrain boundary 01.

## Decision

Boundary **content** is owned by Git and reconciled continuously. Boundary **activation** is owned by Day-0 and is a distinct, narrowly scoped piece of cluster state. The two are separated, and only activation is sequenced.

### The existing bootstrap sequence is preserved

Sequenced bootstrap remains the default and is unchanged in contract. The ADR-042 phase constants, their order, the readiness gates between them, the Day-0 operations interleaved between boundaries, the exit criteria, and the resume path are retained exactly. A boundary activated fourth remains activated fourth, behind the same preconditions.

Two things differ, and neither alters the sequence. The operation performed within a boundary phase is replaced: a boundary is activated rather than rendered into existence. And the platform's application inventory is fully populated from the start of bootstrap, with inactive boundaries present and reporting divergence, rather than materialising boundary by boundary as each phase renders it.

The bootstrap's own reported progression is unchanged. The phase remains the seam: its constant, its position in the sequence, its preconditions, and the progress reported on its completion are all retained, and only the operation performed behind that seam is replaced. What differs is the platform state observable through ArgoCD during bootstrap, not the bootstrap's account of itself.

Beyond those two, this ADR introduces no change to how a cluster is created in sequenced mode. Converged mode is additive and is never entered unless selected.

### Boundary content is reconciled from Git

A single seed Application is the System of Record binding for the `environment-manager` chart. It is created once during Day-0 and thereafter reconciles all boundary ApplicationSets continuously, with self-healing enabled. This extends to the boundary layer the pattern the platform already applies to ArgoCD itself, whose lifecycle is seeded imperatively and then assumed by a platform-owned Application within boundary 01.

Boundary content is complete at all times. The chart renders every boundary on every reconciliation. Per-boundary deployment toggles are removed, and no rendered output is applied outside the seed Application. A partial statement of the boundary set can no longer be expressed, and authority over each ApplicationSet is exclusive.

### Activation is a per-boundary sync window

Each boundary is assigned its own AppProject, and boundary Applications are scoped to it. A boundary is inactive while a deny sync window is present on its AppProject, and active once that window is absent. Applications belonging to an inactive boundary exist, are visible, and report their divergence from Git, but are not permitted to sync.

Sync windows are ArgoCD's designated mechanism for suspending reconciliation of an already-declared Application. Activation state is therefore expressed in a first-class, queryable field rather than inferred from what a render omitted.

The per-boundary AppProject is simultaneously the trust boundary the hub currently lacks. Source repositories, permitted destinations, and permitted resource kinds are declared per boundary and reconciled from Git.

### A boundary declares what must be true before the next one starts

Activation decides when a boundary BEGINS. It says nothing about when one is
DONE, and that omission had a cost this ADR did not anticipate.

`awaitBoundaryInventory` filled the gap by default, because it was the only
assertion every boundary made. It answers *"did the ApplicationSet generate its
Applications"* — a question about the render, not about the cluster. Generating
an Application is not deploying it, and deploying it is not the thing it deploys
being usable by the boundary that follows.

So each boundary grew its own assertions reactively, one outage at a time. Four
of the six the platform had were added after debugging a specific failure;
boundaries 02, 05 and 06 asserted nothing at all — not as a decision, but
because nobody had been burned there yet. The distinction matters: an absence
that was argued and an absence that was never considered are indistinguishable
when both are written as nothing.

Boundary 03 is what this cost. It reported success when ArgoCD had applied the
`HubEnvironment`; the hub-operator then spent nine more minutes generating
credentials, uploading them, waiting for ESO, and creating the database roles the
CR declares. Boundary 04 had long since deployed Zitadel against a database whose
roles did not exist:

```
06:22:58  boundary 04 starts; zitadel init/setup hooks begin
06:25:39  operator uploads application secrets
06:32:28  "All database roles provisioned" -- hub_zitadel finally exists
06:33:30  the hooks exhaust their retries and die
```

Every failure in between was real and transient — `password authentication failed
for user "hub_zitadel"` while the role did not exist, then `permission denied for
database zitadel` while it was being granted.

**Therefore: every boundary declares a readiness contract — what must be TRUE
before the next boundary may start — and the declaration is mandatory.**

Declaring that nothing is required is a legitimate answer and must carry a
reason, in the same way the architecture gate requires a reason for a waiver.
Boundary 05 waives because spoke provisioning is asynchronous by design
(ADR-047): a spoke takes longer to build than Day-0 runs. Boundary 06 waives
because public certificates are issued by ACME on the issuer's schedule, and a
box serves on its internal PKI meanwhile.

**The tempting alternative is a longer timeout, and it is not equivalent.** A
deadline widens the window in which a race is survivable; it does not order the
two. Twice as long still fails on a box where the dependency is twice as slow,
and that failure is indistinguishable from a real one.

### Declaring is enforced by the compiler, not by review

A contract that a new boundary can omit is a convention, and this ADR already has
evidence for how conventions decay here: the three silent boundaries were each
written by someone following the shape of the boundary beside them.

The contracts are therefore declared as unkeyed Go struct literals, where the
language requires every field to be present. A boundary that omits an answer does
not compile, and a field added to the contract breaks every existing boundary
until each one answers it. The zero value of a readiness declaration is not a
valid declaration, so *"I forgot"* and *"I decided nothing is needed"* cannot be
spelled the same way — which is precisely the confusion that made the original
absence invisible.

### Activation is the only cluster state Day-0 mutates

The seed Application does not reconcile the sync window field. This exclusion is the ADR-040 lifecycle boundary expressed as a manifest: Git owns boundary content and boundary policy; the cluster owns boundary activation; Day-0 mutates activation and nothing else. Because the two authorities act on disjoint fields, neither can overwrite the other, and the class of failure recorded in the Context above becomes unrepresentable.

Opening a boundary is idempotent. A resumed bootstrap that re-opens an already-open boundary performs no mutation, in contrast to a repeated render, which restates the boundary in full.

### Boundary AppProjects are seed-layer resources

The boundary AppProjects are established with the seed Application and before any boundary ApplicationSet exists. A boundary cannot be governed by a project that a boundary reconciles. The existing spoke and tenant AppProjects are unaffected and remain within boundary 01.

### Two modes of cluster creation

Cluster creation is offered in two modes, selected by an explicit Day-0 input. `sequenced` is the default.

Mode and environment are independent Day-0 inputs. Every environment class may be created in either mode, and no combination of the two is prohibited. No environment class carries an implicit mode, and no mode is reserved to an environment class: converged creation is requested by name or it does not occur. This follows the rule ADR-051 already applies to issuer policy — an environment-dependent default that is never stated is a policy no one has decided.

**Sequenced.** Every boundary is inactive at creation, and each is activated in phase order behind the readiness gates that already precede it. Ordering is deterministic and a failure is attributable to a named phase. Determinism and attribution are what this mode buys, which is why it is the default for every environment class.

**Converged.** No boundary is inactive at creation. All boundaries reconcile concurrently and converge through ArgoCD's retry behaviour. Ordering is not guaranteed and a failure is attributable only to the Application that failed. Creation latency is what this mode buys, at the cost of attribution. Whether that trade is acceptable is a judgement about a particular act of creation — how the cluster will be used, and whether a failed creation would be discarded or diagnosed — and not a property of the environment class being created.

The two modes are one code path. They share Git content, chart rendering, the boundary AppProjects, the ApplicationSets, the generated Applications, and the Day-0 secret, PKI, and Infisical operations, which run in sequence under both. Converged mode is sequenced mode with the seeding of inactive state omitted; it introduces no branch beyond that omission.

Converged mode does not remove the Day-0 operations interleaved between boundaries. Those operations establish state that boundary content depends on and are not optional in either mode. What converged mode declines is holding a boundary inactive while they proceed: the dependent Applications are created, fail to sync, and retry until that state exists. This is the trade — ordering guarantees are exchanged for latency, not for a reduction in Day-0 work.

Mode selection applies to cluster creation and therefore only where a Day-0 sequence exists. Preview environments created on an existing hub by the ephemeral pull-request generator (ADR-038) have no Day-0 sequence and no boundary phases; they are created in a single ungated act by definition and do not participate in mode selection. The distinction matters because both a cluster and a preview environment are described as ephemeral, and only the former is created by Day-0.

Mode selection has no Day-1 meaning. Once bootstrap completes, no boundary is inactive under either mode, and the resulting platform state is indistinguishable. Mode is not recorded in platform state and no controller may read it.

### Environment values are resident in Git

Matrix dimensions and environment policy — environment slug, provider, topology, and the public TLS issuer required by ADR-051 — are declared in per-environment values resident in Git and consumed by the seed Application. Values known only at Day-0 runtime are delivered as ADR-045 bootstrap-generated artifacts. No environment value is supplied at render time from outside Git.

The public TLS issuer guard defined in ADR-051 is retained. With environment values always present, a boundary can no longer be rendered without its companion policy value, and the guard ceases to be load-bearing.

### Alternatives considered

**ApplicationSet progressive sync ordering.** Rejected. It cannot suspend for Day-0 work performed outside ArgoCD, which the ADR-042 phase sequence requires between boundaries 02/03 and 03/04. It additionally couples boundaries during rollout, reintroducing the cross-boundary blocking that ADR-021 eliminated.

**Convergence without gating as the only mode.** Rejected as the sole mode. It removes the deterministic first-boot sequence that ADR-021 and ADR-042 define, and offers no ordering guarantee for environments that require one. It is retained as an explicit alternative mode rather than discarded.

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Boundary ApplicationSets | Git | ArgoCD | ArgoCD | Platform | Day-1+ |
| Seed Application | Git | ArgoCD | ArgoCD | Platform | Day-0 (created), Day-1+ (reconciled) |
| Boundary AppProject policy | Git | ArgoCD | ArgoCD | Platform | Day-1+ |
| Boundary activation state | Cluster | CLI | None | Platform | Day-0 |
| Environment values | Git | ArgoCD | ArgoCD | Platform | Day-1+ |
| Runtime-discovered environment values | Git | CLI | ArgoCD | Platform | Day-0 (generated), Day-1+ (reconciled) |

Boundary activation state has no reconciler by construction. It is a Day-0 artifact and, per ADR-040, an immutable input to Day-1 thereafter: no controller may set or clear it.

## Consequences

### Positive

- Git becomes authoritative for the platform's complete application inventory. A service added to a boundary reaches the cluster without a bootstrap action.
- Exclusive authority per field eliminates the overwrite class recorded in the Context, including the failure signature in which an Application reports `Healthy` while managing no resources.
- The ADR-042 phase sequence, its interleaved Day-0 operations, its exit criteria, and its resume path are preserved unchanged. Only the operation performed within each boundary phase changes.
- Boundary activation is idempotent, making the resume path a no-op rather than a restatement.
- Hub boundaries acquire the project scoping that spoke and tenant workloads already have.
- The full intended application inventory is observable from the start of bootstrap rather than emerging phase by phase.
- Environments with different ordering requirements are served by one Git representation and one code path.

### Negative

- Activation state is deliberately excluded from Git reconciliation. A boundary left inactive by an interrupted bootstrap is not self-correcting and is not reported as drift; detecting it requires an explicit check.
- All boundary Applications exist from the start of bootstrap, most of them inactive and divergent from Git. Bootstrap progress can no longer be inferred from which Applications exist, so any operational practice that reads progress from the ArgoCD application inventory must instead read boundary activation state. The bootstrap's own reported progression is unaffected.
- A permanently inactive boundary is expressed through a scheduled window mechanism intended for recurring windows. The encoding is supported but does not read literally.
- Suspension does not interrupt a sync already in progress. This has no effect on a fresh cluster and is a consideration only on re-entry.
- Converged mode has no ordering guarantee, and diagnosing a stalled convergence from a progressing one requires convergence-specific checks that sequenced mode does not need.

## Impact

**Amends ADR-021.** Boundaries retain their role as isolation and authority domains. Day-0 choreography is retained in full but is redefined to act on boundary activation rather than on boundary content.

**Amends ADR-040.** "Gate the activation of ArgoCD boundaries" is given a specific meaning: activation is distinct cluster state, exclusively Day-0-owned, and disjoint from the boundary content that Day-1 reconciles. The boundary ApplicationSets become Day-1 reconciled resources rather than Day-0 artifacts; boundary activation state becomes the Day-0 artifact in their place.

**Preserves ADR-042.** Every phase constant, transition, exit criterion, and recovery path is retained. The operation performed within each boundary phase is replaced.

**Preserves ADR-038.** Environment revision continues to reach platform-owned Applications as a generator parameter. It is supplied from Git-resident environment values rather than at render time.

**Extends ADR-045.** Environment values known only at Day-0 runtime are delivered as bootstrap-generated artifacts. Because these artifacts are consumed by ArgoCD from Git rather than by a local render, they must be committed. This requirement applies to all ADR-045 artifacts consumed by a Git-resident reconciler.

**Retains ADR-051.** The public TLS issuer policy and its rendering guard are unchanged.

Boundary deployment toggles, the direct application of rendered boundary output, and the separate reconciliation path that re-applies boundary ApplicationSets outside the phase sequence are all removed. Their removal is a precondition of exclusive authority, not an optimisation.

A check that distinguishes an inactive boundary from a failed one, and a stalled convergence from a progressing one, is required by this ADR. Neither state is reported as drift.

**Amends itself (2026-09-14).** This ADR decided when a boundary is activated and
left when it is complete undecided. Every boundary now declares a readiness
contract; the six bespoke deployment functions are replaced by one path that
cannot be bypassed, because there is nowhere else to write a boundary. Boundaries
02 and 03 gained the assertions whose absence caused the failure recorded above —
the platform database serving, and the hub-operator having created the roles the
`HubEnvironment` declares. Boundaries 05 and 06 waive with stated reasons.

**Complements ADR-079.** That ADR requires a Day-0 *phase* to declare its
re-entrancy and to verify its own postcondition. This is the same rule one level
up: a *boundary* declares what the next boundary may assume. The two share a
cause — a record of work having run is not evidence of the work having taken
effect.

## References

- ADR-003: Secret Management Architecture
- ADR-021: Boundary-Driven GitOps and Day-0 Choreography
- ADR-038: Continuous Revision Tracking for Ephemeral Environments
- ADR-039: Platform Ownership Model
- ADR-040: Day-0 vs Day-1 Lifecycle Boundary
- ADR-041: Controller Responsibility Matrix
- ADR-042: Bootstrap State Machine
- ADR-043: Control Plane Authority Model
- ADR-045: Bootstrap-Generated GitOps Artifacts
- ADR-051: Environment DNS Naming and Spoke-Terminated Public Ingress

# ADR-098: Metering and the Usage Ledger

**Date:** 2026-09-30
**Status:** Proposed
**Relates to:** ADR-012 (declarative billing catalog), ADR-021 (tenant ABI), ADR-039
(platform ownership model), ADR-041 (controller responsibility matrix), ADR-043
(control-plane authority model), ADR-052 (sandbox compute and S3 workspaces), ADR-066
(the platform boundary), ADR-078 (the observability capability), ADR-088 (the tenant
and the application are different axes)

## Context

ADR-066 declared `metering` a selectable capability and withdrew OpenMeter: it
wanted ClickHouse, Kafka and Svix for a capability no box ran. Its Addendum 3
names what is outstanding — "an ADR for it, not a toggle" — and points at the
PostgreSQL implementation at `internal/kube-sbt/providers/metering`. This is
that ADR.

ADR-052 is the only place in the corpus that says where usage comes from:

> **Metering** derives from pod resource-seconds already collected by the
> observability stack and attributed by namespace.

and its Ownership table gives OpenMeter as the System of Record for Burst
Compute Usage.

Neither statement holds of the platform as built. Verified by inspection
2026-09-30:

- **There is no usage write path at all.** `models.UsageEvent` does not exist;
  `IngestUsageEvent` has no caller anywhere in the repository; no migration
  creates the usage table; the ingest event constant in `models/events.go` has
  no emitter and no handler.
- **The provider tree does not compile.** `providers/metering`,
  `providers/billing` and `providers/systemadmin` fail against the current
  `models` package (undefined `UsageEvent`, `MeterUpdates`, `MeterFilters`,
  `BillingCustomer`, `UsageRecord`, and method-signature drift). CI skips them.
  The only backend client that compiles is the withdrawn OpenMeter one, and
  `cmd/kube-sbt` constructs it against a Service nothing creates — the orphan
  ADR-078's Metering note already records.
- **No collection produces container usage for tenant workloads.** Spoke Alloy
  scrapes kube-state-metrics and CNPG only. kubelet and cAdvisor collection
  exists on the hub and is scoped to its own node; spokes have none. No scrape
  custom resources are deployed. And ADR-078 add.1 §6 drops every
  tenant-namespace series before remote write, so even spec-based
  kube-state-metrics series for tenant pods never reach the box's store.
  **No store can answer what a tenant's containers consumed.**

ADR-012's catalog decision — meters, features and plans as declarative CRs
reconciled by an operator, runtime state through an API — still holds. Its
runtime backend does not.

The identity this ADR needs already exists. ADR-088 fixes two axes: `tenant_id`
names the organisation, `app_id` names a product, and the namespace is
`tenant-{tenant_id}-{app_id}`. ADR-021's ABI is admission-enforced: every
tenant workload controller carries `tenant-id`, `app-id` and `cost-center`.
The namespace object is cluster-scoped, no tenant-facing cluster role grants
mutation of it, and ArgoCD owns it with self-heal. What is missing is the
fail-closed admission reservation ADR-078 add.1 §6 already decides for the
`topology.platform.io/` prefix, extended to the identity and capability labels
this ADR depends on.

The bar this ADR must clear, from design review: billing requires record
identity, measurement intervals, units, idempotency, corrections, missing-data
semantics and aggregation semantics. An observability store tolerates gaps,
resets and late samples by construction; a ledger may not absorb them silently.
Attribution identity must be platform-authoritative, never workload-supplied.
The platform owner's decision on basis: **billing is on actual usage**, not
allocation.

## Decision

**1. Two lanes, one identity.**

```
Kubernetes API ──lifecycle/identity──► usage recorder ──► usage ledger ──► billing
                                                                   (append-only, SoR)
apps / clusters ──samples──► Alloy ──► box metrics store ──► observability
                                              └── quantity over closed windows ──┘
```

The usage ledger — a table set on the CNPG cluster every box already runs — is
the System of Record for usage. Billing, invoices and usage queries read the
ledger and nothing else. Telemetry may be *queried by the recorder* and may
*audit* the ledger; it never populates billing directly and no invoice is ever
computed at read time from a metrics query. This is the separation ADR-039's
one-System-of-Record rule demands, applied to usage.

**2. The billing basis is actual container usage.**

CPU is measured from cumulative usage counters converted to interval deltas;
memory as the time-weighted average of working-set samples over the window.
Both come from the platform's own collection machinery, on every cluster where
tenant workloads run. Actual usage requires that collection to exist: the
spoke collection floor (kubelet/cAdvisor scrape, and the kube-state-metrics
install gap its own registry note records) becomes a metering prerequisite, not
an observability nicety.

Collection for metering covers the container usage series of all `tenant-*`
namespaces **in-cluster, into the box's own store**, as accounting data. This
is a carve-out from ADR-078 add.1 §6's tenant exclusion and changes nothing
else it decides: tenant logs are still not collected, tenant series still do
not leave the cluster, and ADR-078 §7's opt-in still governs every off-cluster
destination. The series collected are numeric usage with object identity — no
log content, no payload, no application labels beyond what identity section 5
reads from the API.

**3. The usage recorder runs inside kube-sbt.**

One logical writer, in the kube-sbt process: a long-running loop that watches
the Kubernetes API for resource lifecycle and identity, queries the box's
metrics store for quantity over completed windows, and writes append-only
records to the ledger. Construction follows the composition root —
configuration, then constructors with explicit dependencies, then handlers; a
provider's lifetime is the process's lifetime. No global registry and no
singleton cache; a registry earns its place only if provider *selection* ever
becomes configuration-driven, which nothing here requires. Idempotent keys
make additional recorder instances safe should isolation ever need one; a
lease is an optimisation, not a correctness requirement.

**4. A window closes only when collection proves it.**

A window may be closed into the ledger only when the underlying series are
fresh across it — samples exist within a settle margin of the window's end.
Otherwise the window stays open, a collection-gap signal is raised, and the
gap is written as a gap record when it is proven: **auditable, unbilled,
never estimated**. Counter resets are handled by delta conversion at extract
time; late samples that arrive within the store's retention re-open the
window through a compensating record, never an in-place update. If the
recorder itself is down, closure stops; windows accrue open and visible
rather than wrong. Reconciliation closes the remaining hole: a pod whose API
lifetime overlaps a window with no corresponding record produces a gap record
— a deleted pod's history is not invented.

**5. Identity never comes from telemetry.**

Quantity comes from telemetry; identity comes from the Kubernetes API. The
recorder resolves `tenant_id`, `app_id`, `namespace` and `cell_id` from the
Namespace object's platform-rendered labels, and the resource from object UID
and metadata — at record time, from the same authority that enforces them.
Series labels identify a series and nothing more: no label on a metric, and
no environment variable in a workload, is ever a billing authority. This is
the same rule the platform already applies to cost labels through ADR-021's
admission enforcement, and it is why section 10 makes the reservation
prerequisite rather than optional.

**6. The ledger's semantics.**

| Concern | Rule |
|---|---|
| Record identity | Idempotency key: meter key + resource UID + window bounds + schema version. Unique; insert-if-absent; replay-safe |
| Resource identity | `resource_kind` (container, pod, sandbox, ephemeral job, ephemeral VM, database …), object UID — never the name — plus container name and restart ordinal where applicable |
| Tenant identity | `tenant_id` + `app_id` + `namespace` + `cell_id`, read from API metadata per section 5 |
| Measurement interval | Half-open `[window_start, window_end)`; end at lifecycle transition or watermark closure per section 4 |
| Quantity and unit | CPU delta in millicore-seconds; memory time-weighted in MiB-seconds; unit stored on every record |
| Source | Which lane produced the record: metrics store, API lifecycle, operator report, manual adjustment |
| Observed / effective time | `observed_at` = ledger write time; `effective_at` = window start. Billing periods key on effective time |
| Corrections and reversals | Append-only. Restatements and reversals are negative or delta records referencing the original by key. No quantity is ever updated in place |
| Backfill | Explicit, flagged as such, bounded by recorder coverage. History that predates coverage is absent, not reconstructed |
| Missing data | Unbilled, recorded as a gap, alerted on. Absence of evidence is never converted into an estimate |
| Aggregation | Ledger queries only: sums over disjoint windows per meter, tenant, app and period. Period close freezes totals; later corrections post as adjustments against the closed period |

**7. The domain model is settled before any schema.**

```
tenant_id (customer, ADR-088)          1 ──── n      app_id (product, ADR-088)
        │                                                  │
        └────────── namespace = tenant-{tenant_id}-{app_id} ┘  (+ cell_id)
                             │
                             └── resource (kind, UID, owner chain)
meter (catalog, ADR-012): key, unit, aggregation,
                          scope ∈ {tenant, app, namespace},
                          dimensions (incl. burst vs home-lab capacity)
```

Every meter's scope must be resolvable from the identity recorded per
section 5. A meter that cannot be attributed from recorded identity does not
enter the catalog. The burst dimension — which ADR-052 needs to price burst
capacity differently — is read from the placement the platform itself writes
onto burst pods, so it is platform-controlled like every other dimension.

**8. The interfaces are redesigned before any provider is.**

`IMetering` and `IBilling` as they exist are the withdrawn backend's shapes:
namespace-string scoping everywhere, header-based tenancy, a header comment
naming OpenMeter. No provider — Postgres or otherwise — may be written
against them. They are rebuilt against section 7's model first: explicit
tenant/app scope carried from the authenticated claim (the server-side
identity the JWT middleware already injects), ledger-backed usage queries,
subscriptions and invoices. The withdrawn OpenMeter client package is removed;
no component imports it. The three non-compiling packages are repaired or
deleted as part of this work, and the orphan construction in `cmd/kube-sbt`
goes with them.

**9. The catalog stays declarative; the reconciler is retargeted.**

ADR-012's decision survives whole: meters, features and plans are declared in
fleet values alongside the rest of the fleet's contract, rendered as CRs by
the platform, and reconciled — the reconciler's target changes from the
withdrawn engine to the ledger's catalog. Git remains the catalog's System of
Record with everything that follows: audit trail, rollback, promotion through
the ordinary path. Runtime state — subjects, subscriptions, entitlements,
invoices — lives in the ledger and is served through the redesigned
interfaces.

**10. Two prerequisites, stated as gates.**

- **Identity reservation:** the fail-closed admission reservation ADR-078
  add.1 §6 decides for `topology.platform.io/` extends to the `tenant-id`,
  `app-id`, `cost-center` and `observability.platform.io/` keys on namespace
  objects. Until it ships, attribution is correct but not fail-closed, and
  nothing may be called authoritative. The threat model is ADR-078's: guards
  against app teams and mistakes inside the box, not against the box's owner.
- **Collection floor:** kubelet/cAdvisor collection on spokes (and the
  kube-state-metrics install gap) ships before the recorder's first meter.
  A meter with no provable source is not a meter.

**Alternatives considered**

- *Allocation basis (resource requests × lifetime, API-only).* Implementable
  today with no collection dependency, and cloud-precedent defensible.
  Rejected: the platform owner chose actual usage as the billing basis. It
  remains available as a future meter that needs no telemetry.
- *Querying the metrics store at invoice time.* Rejected outright in review:
  sampling semantics would enter billing directly, with no idempotency,
  correction or gap model. This ADR exists because that was rejected.
- *A standalone metering engine (the OpenMeter class of system).* Rejected on
  the footprint judgement ADR-066 already records: a ledger on the CNPG
  cluster every box runs, not an aggregation cluster no box runs.
- *A separate recorder controller.* Rejected for v1: the recorder shares
  configuration, database and operational surface with kube-sbt's APIs, and
  idempotent keys already make a later split safe.

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Usage ledger (usage records, gap records) | PostgreSQL (CNPG cluster) | kube-sbt | kube-sbt usage recorder | IMetering / IBilling APIs, invoice generation | Day-1+ |
| Metering catalog (meters, features, plans) | Git (fleet values + platform CR templates) | Catalog reconciler | Catalog reconciler | Recorder, IMetering API | Day-1+ |
| Subscription and invoice state | PostgreSQL (ledger) | kube-sbt | kube-sbt | API consumers | Day-1+ |
| Container usage series | Box metrics store (in-cluster) | grafana-alloy | grafana-alloy | Usage recorder | Day-1+ |
| Namespace identity labels | Git (universal-tenant render) | ArgoCD | ArgoCD, with admission reservation (section 10) | Recorder (read), admission policies | Day-1+ |

See ADR-039 for the complete ownership matrix. ADR-052's Burst Compute Usage
row (System of Record: OpenMeter) is superseded by the usage-ledger row above.

```architecture
capabilities:
  - metering
  - observability
```

## Consequences

### Positive

- Billing rests on a ledger with explicit accounting semantics — identity,
  interval, unit, idempotency, corrections, gaps — instead of a telemetry
  query with none of them.
- Collection failures become visible gap records and alerts. The failure mode
  of the observability lane (silent missing samples) cannot become silent
  underbilling.
- Attribution identity is platform-authoritative end to end: rendered by
  ArgoCD, enforced by admission, read from the API, never taken from a
  workload's environment or a series' labels.
- One System of Record per resource (ADR-039), one authority per domain
  (ADR-043), and the catalog keeps ADR-012's GitOps properties unchanged.
- Actual usage is honoured as the billing basis, and the burst dimension that
  ADR-052 depends on falls out of placement the platform already writes.

### Negative

- **Collection becomes billing-critical.** A scrape gap now has financial
  meaning. Mitigated by the watermark (nothing unproven is ever billed), gap
  records, and the collection floor as an explicit prerequisite — but the
  obligation is real and permanent: the platform now operates collection for
  accounting, not only for observability.
- Memory is a gauge, not a counter. Time-weighted averages over samples are a
  weaker contract than deltas; the window's minimum-sample rule exists
  precisely because of this asymmetry.
- kube-sbt gains a long-running watch-and-query loop. Recorder downtime
  freezes window closure (open, visible, correct) rather than corrupting
  totals, but usage reporting becomes stale with it.
- Retention obligations diverge: ledger retention is a revenue and audit
  record; metrics-store retention must cover correction windows or late
  restatements become impossible.
- The interface redesign is breaking for every existing consumer of
  `IMetering` / `IBilling`, and three provider packages are repaired or
  deleted — there is no compatible path through the current code.
- The shared CNPG cluster gains write load from the recorder. It is the
  database every box already runs; the volume is per-container window
  records, not per-request events.

## Impact

**Amends** (status lines added with this ADR):

- **ADR-052** — the metering sentence ("already collected by the
  observability stack") is corrected: collection does not exist and is built
  as this ADR's prerequisite; the Burst Compute Usage ownership row's System
  of Record moves from OpenMeter to the usage ledger; the basis is actual
  usage, stated here.
- **ADR-012** — the catalog decision stands; its reconciler target and
  ownership row move from OpenMeter to the metering catalog reconciler and
  ledger.
- **ADR-078** — add.1 §6's tenant series exclusion gains the metering
  carve-out of section 2: in-cluster container usage series for `tenant-*`
  namespaces are collected as accounting data. Off-cluster opt-in, tenant log
  policy and trace policy are unchanged.

**Follow-up edits** (declared here, applied to their files): the OpenMeter
rows in ADR-039's and ADR-043's matrices. ADR-066's Addendum 3 sentence —
"what is outstanding is an ADR for it" — is answered by this document; the
capability itself stays `declared` / `planned` until the recorder, ledger and
interfaces ship.

**Registry**: `manifests/architecture/components.yaml` gains this ADR in the
`declared_by` of the `metering` and `observability` capabilities, and the
metering note points here instead of at an unnamed future ADR.

**Code and manifests** (future work, this ADR decides not implements):

- Ledger migration and recorder module in `internal/kube-sbt`; composition-root
  provider construction in `cmd/kube-sbt`; removal of the withdrawn client.
- Redesigned `interfaces/metering.go` and `interfaces/billing.go`; handlers
  move with them; `providers/` compiles under CI again.
- Spoke collection floor (kubelet/cAdvisor in spoke Alloy); namespace-label
  reservation policy; catalog CR templates in the universal-tenant chart,
  rendered from fleet values.

This ADR stays **Proposed** until the `metering` capability is `shipped` —
the same discipline ADR-066 and ADR-078 apply: a decision may not rest on
components nothing deploys.

## References

- ADR-012: Declarative Billing Catalog Management via Operator Pattern
- ADR-021: tenant workload ABI (the enforced cost labels)
- ADR-039: Platform Ownership Model
- ADR-041: Controller Responsibility Matrix
- ADR-043: Control Plane Authority Model
- ADR-052: Sandbox Compute and S3-Backed Workspaces
- ADR-066: The Platform Boundary (metering capability; OpenMeter withdrawal)
- ADR-078: The Observability Capability (add.1 §6 tenant exclusion, §7 opt-in)
- ADR-088: The Tenant and the Application Are Different Axes
- OpenTelemetry Metrics Data Model — cumulative counters, resets, and why
  interval conversion needs a completeness rule
  (https://opentelemetry.io/docs/specs/otel/metrics/data-model/)

# ADR-078: The Observability Capability

**Date:** 2026-09-12

**Status:** Proposed

*Constrained by: ADR-065 (The Control Plane Ships Into the Box), ADR-066 (The Platform Boundary), ADR-069 (The Maintenance Promise), ADR-070 (The Minimum Supported Box), ADR-071 (How This Platform Differs from Kubefirst), ADR-077 (The Support Agent)*

*Amended by: addendum 3 (2026-10-24) — the backend and UI move to the box owner's Grafana Cloud stack; the in-box stores, Grafana and ADR-083's federation are removed.*

*Amended by: ADR-098 (Metering Sources over Spoke Egress and Records in Lago) — add.1
§6's tenant series exclusion gains a metering carve-out: in-cluster container
usage series for `tenant-*` namespaces are collected as accounting data.
Off-cluster opt-in, tenant log policy and trace policy are unchanged.*

> Observability is a capability the platform ships, not a destination the tenant supplies. A box that collects telemetry and has nowhere to put it is not observable.

## Context

A box today receives **collection and nothing else**. Grafana Alloy runs as a DaemonSet on hub and spoke and is the only observability component that reaches a cluster. There is no store, no query surface, no visualisation, and no rule evaluation.

What that means in practice, from the repository as it stands:

- `manifests/hub-core-services/grafana-alloy/deployment.yaml` writes to `sys.env("PROMETHEUS_URL")` and `sys.env("LOKI_URL")`, sourced from a Grafana Cloud ExternalSecret. A tenant without a Grafana Cloud account receives collection that terminates nowhere.
- That same Alloy scrapes `discovery.kubernetes.pods` and `discovery.kubernetes.services` with no namespace filter — every tenant workload's metrics, forwarded off-cluster — and stamps `external_labels = { cluster = "hub" }`, a constant, so two clusters arriving at one destination are indistinguishable.
- `manifests/hub-core-services/victoriametrics/` holds an operator, a `VMCluster`, CRDs and an ingress. `manifests/hub-core-services/victoriametrics-alerts/` holds `platform-core-alerts`, 211 lines of working pod-health and memory-pressure expressions. Five hundred and seventy-seven lines in total, referenced by no component descriptor and named in no ApplicationSet. None of it has ever been applied to a cluster.
- `internal/soloz-cli/bootstrap/hubdomain.go:83` computes `victoriametrics.hub.<domain>` for every box — a DNS name for a service nothing deploys.

Three settled positions decide what to do about this.

**Every capability ships to every tenant** (ADR-071). There is no catalogue and nothing is withheld. A capability whose usefulness begins at a third-party signup is not shipped; it is advertised.

**The control plane runs in the customer's box** (ADR-065). A design whose data plane terminates at a vendor the platform does not operate inverts that for the one signal an operator reaches for first.

**The maintenance promise is stated in terms of the platform being diagnosable** (ADR-069). A promise to diagnose a box whose logs went to an account the platform cannot see, or to no account at all, is a promise that cannot be kept.

There is also a defect this closes rather than repeats. Scaffolding was built to collect a tenant's Grafana Cloud credentials and, finding none, to report their maintenance as unsupported — the tenant's private monitoring account had become a precondition, which is a licence check wearing a configuration message. That was corrected in code. The structural cause is that the only destination the capability knew about belonged to a vendor, and this ADR removes it as the only destination.

## Decision

### 1. The backend is platform-provided and runs in the box

The platform ships storage and query for the signals it collects. The tenant is not required to supply a destination, hold an account with any vendor, or configure anything for the capability to be complete.

A tenant-supplied destination remains available and is **additive**, never a replacement — §7. The capability the platform supports is therefore always present to reason about, whatever else the tenant also sends telemetry to.

### 2. Signals

**Metrics — in the capability.** Required. Without them there is no platform health signal at all.

**Logs — in the capability.** Required. Alloy already collects pod logs; they need somewhere to land. Diagnosing a platform failure from metrics alone is not possible, and ADR-069 makes diagnosis the substance of the promise.

**Traces — out of scope, explicitly.** Not deferred; excluded. No platform component emits a span, there is no cross-service platform request path whose latency is the platform's to explain, and a trace store is the most expensive of the three to run at ADR-070's minimum box. A tenant who wants traces adds them under §7. If the platform grows a request path that needs them, that is a new decision with a component behind it — which is the discipline this ADR means to hold.

### 3. Collection

Grafana Alloy remains the collection and forwarding layer, as a DaemonSet on every cluster, hub and spoke. That is the right shape and it is already deployed.

What is collected is **bounded and named**:

- `/metrics` from platform-owned namespaces, selected by the `topology.platform.io/role` label ADR-077 already relies on
- kubelet, cAdvisor and node exporter series
- kube-state-metrics for Kubernetes object state, on hub and spoke alike — it is present in the spoke catalogue and absent on the hub today, and this closes that asymmetry
- pod logs from platform-owned namespaces

Tenant namespaces are **not** collected by default. Forwarding a tenant's workload telemetry off-cluster without being asked is on the wrong side of ADR-066. A tenant opts their own namespaces in — §7.

`external_labels` carries the cluster's real identity, not a constant.

### 4. Storage and query

The components that ship, by name:

| component | signal | notes |
|---|---|---|
| `victoria-metrics-operator` | — | reconciles the below |
| `VMSingle` / `VMCluster` | metrics | single at the minimum box (ADR-070), cluster above it |
| `VictoriaLogs` | logs | |
| `VMAlert` | — | §6 |

VictoriaLogs rather than Loki: the metrics backend is VictoriaMetrics and already written, and one operator reconciling both signals is one storage system to run, back up and support instead of two. This is a choice about operational surface, not a claim that Loki is worse.

**No component named in this ADR exists only as a noun.** Each is registered in `manifests/architecture/components.yaml` and asserted by `scripts/validate/architecture-consistency.py` before a release publishes.

### 5. Visualisation

**Grafana ships in the box**, and platform dashboards are versioned artifacts of the bundle.

Dashboard JSON lives in the repository, is delivered as labelled ConfigMaps, and reaches a tenant through the ordinary promotion path (ADR-064): a new bundle version proposes new dashboards as a pull request the tenant merges or does not. A dashboard is release content with a version, not something an operator draws once in a UI and nobody can reproduce.

Headlamp remains the cluster object browser. It is not the observability front end.

### 6. Alerting

**VMAlert ships, and the platform ships platform-health rules.** `platform-core-alerts` already exists and already works; it is deployed rather than rewritten.

Platform rules and tenant rules stay distinct:

- Platform rules carry `app.kubernetes.io/managed-by: platform`, arrive in the bundle, and are reconciled with `selfHeal`. Editing one in place is reverted — the way to change a platform rule is to propose it upstream or fork it under a tenant label.
- Tenant rules are the tenant's own `VMRule` objects in their own namespaces. The platform never reads, edits, or is accountable for them.

**Routing is the tenant's, and the platform ships no default receiver.** A platform holding a tenant's PagerDuty key would re-create the vendor-account precondition in a new place.

### 7. Tenant extensibility

Three extension points, all additive:

1. **Additional destinations.** A tenant may add `remote_write` targets — Grafana Cloud, an existing corporate Prometheus, anything. Alongside the in-box backend, never instead of it.
2. **Their own namespaces.** A tenant opts workload namespaces into collection by label. Default is exclusion (§3).
3. **Their own dashboards and rules.** Unlabelled by the platform, untouched by reconciliation, theirs.

The platform's own dashboards and rules stay standardised precisely so a support conversation can begin from a shared artifact. A tenant who forks one has forked it, and that is visible.

### 8. Separation from the Support Agent

ADR-077 collects support evidence. This ADR builds an observability backend. They share sources and share nothing else.

**Shared platform evidence sources; independent collection and export paths.**

Concretely, and checkably: the Support Agent's collectors name component metrics endpoints directly — `argocd-applicationset-controller-metrics`, `kyverno-reports-controller-metrics`, `cert-manager-metrics` — which are the same `/metrics` surfaces Alloy scrapes. Neither reads the other's output. Therefore:

- The observability backend is **never** a dependency of support telemetry. Deleting VictoriaMetrics does not stop the Support Agent.
- The Support Agent is never a dependency of observability. Unenrolling from support does not degrade the capability.
- Same source is not same pipeline. A field reaching the tenant's dashboards does not thereby reach the platform, and the ADR-077 allowlist remains the only thing deciding what leaves.

A release gate asserts that no Support Agent collector names an observability component, so the coupling cannot be introduced later by someone wiring the agent to the store because the store is convenient.

## Out of scope

**Metering.** OpenMeter is a billing system, not an observability one, and its architecture is a separate decision. Noted rather than decided here: OpenMeter is applied by no component, while `cmd/kube-sbt/main.go:24` defaults to `http://openmeter-api.platform-billing.svc.cluster.local` and kube-sbt *is* deployed — a running component calling a Service nothing creates. Its event store has since been withdrawn from the platform, so `config.aggregation` is unset and adopting metering means choosing a backend before anything else. The architecture-consistency gate reports the orphan rather than leaving it to be noticed.

**Alert routing and on-call.** §6. The platform evaluates; the tenant decides who is woken.

**Tenant application observability.** The platform ships the capability and the tenant uses it for their own workloads under §7. What those workloads emit, and whether it is any good, is theirs (ADR-066).

## Components

Registered in `manifests/architecture/components.yaml` and checked by `scripts/validate/architecture-consistency.py`. This ADR stays **Proposed** until every one of them is shipped and applied, which is the mechanism that keeps a decision from resting on a component nothing deploys.

```architecture
capabilities:
  - observability
components:
  - grafana-alloy
  - platform-alerts
```

## Alternatives considered

**Tenant-provided destination, platform ships collection only.** This is today's state. It makes dashboards and alert rules unshippable, because there is nowhere to install them — which is why the platform has 211 lines of working alert rules that evaluate nowhere. It also makes a vendor account a precondition of a free capability. Rejected on both counts.

**Loki for logs.** No installed base to preserve, and a second storage system to operate at the minimum box. Rejected on operational surface, not on merit.

**Traces now, on the grounds that a platform ought to have them.** Nothing emits spans. Adopting a component because the category exists is how a component becomes a noun with no deployment behind it. Rejected.

**Let the Support Agent read the observability store, now that one exists.** Rejected in ADR-077 and rejected again here. It would make support telemetry depend on a component the tenant may scale down, and it would make the store's contents — which include whatever the tenant opted in under §7 — reachable by the export path. The allowlist would then be filtering an unbounded payload, which is a preference rather than a boundary.

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Platform telemetry (metrics, logs) | in-box VictoriaMetrics / VictoriaLogs | Observability capability | Alloy | Tenant operators, SRE | Day-1+ |
| Platform dashboards and rules | this repository, via the bundle | Platform | ArgoCD (`selfHeal`) | Tenant operators | Day-1+ |
| Tenant dashboards, rules, destinations | tenant gitops repository | Tenant | ArgoCD | Tenant | Day-2 |
| Support evidence | ADR-077 allowlist | Platform | Support Agent | SOLOZ Support | Day-2 |

See ADR-039 for the complete ownership matrix.

## Consequences

### Positive

1. A tenant with no vendor relationship receives a complete, usable observability capability.
2. The VictoriaMetrics content and the existing alert rules become deployed content rather than dead files.
3. Dashboards and alert rules acquire versions and a promotion path, so "which dashboards is this box running" has an answer.
4. Tenant workload telemetry stops leaving the cluster by default.
5. Support telemetry and tenant telemetry become structurally independent, so neither can quietly become a condition of the other.

### Negative

1. **Resource footprint.** Metrics storage, log storage and Grafana on every hub. This is the real cost of the decision and it lands hardest at ADR-070's minimum box, which is why §4 specifies `VMSingle` there.
2. **The platform now operates storage.** Retention, backup and capacity for two data stores become the platform's responsibility and the maintenance promise's problem.
3. **Dashboards become release content**, with the review burden that implies. A dashboard fix ships at a bundle version.
4. **Traces are absent** until a decision brings them in. Some tenants will want them and will build their own.

## Impact

- `manifests/argocd/components/03/` gains descriptors for the observability components.
- `manifests/hub-core-services/victoriametrics{,-alerts}/` are deployed as they stand; `victoria-logs/`, `grafana/` and `vmalert/` are new.
- `manifests/hub-core-services/grafana-alloy/` is rewritten: in-box destination by default, platform-namespace scoping, real cluster labels, tenant opt-in.
- `internal/soloz-cli/bootstrap/hubdomain.go:83` — `victoriametrics.hub.<domain>` resolves to something.
- Dashboard JSON becomes a packaged artifact set.
- A new release gate asserts the Support Agent names no observability component (§8).

## Acceptance

```architecture
acceptance:
  - scripts/validate/cluster/82-support-agent-independence.sh
```

§8 is the only claim in this ADR with a proof today, and it is the one most
likely to be defeated by a later convenience: once a store exists in the box,
pointing the agent at it is one line and reads as a simplification. The rest of
this ADR claims components, and check A is what holds those.

## Addendum 1: topology, the collection floor, tenant ingest, and the bookkeeping (2026-09-19)

This ADR decided what ships and was silent on where it runs. That silence is not a
detail: it put this ADR in contradiction with ADR-077 without either document
noticing. Settled here, together with three scope questions review found unanswered
and the bookkeeping the Components section promised.

Sources are named where a decision has one. `cluster-monitoring-operator` paths are
relative to `reference-projects/1proprietary/`.

### 1. The store runs on every cluster. Grafana runs only on the hub.

§3 puts Alloy on hub and spoke. §4 names the store, query, alerting and
visualisation components and **never says which cluster runs them**, and everything
around it points at the hub alone: `hubdomain.go:83` derives
`victoriametrics.hub.<domain>`, and every path in `components.yaml` is
`manifests/hub-core-services/`. Read literally that is hub aggregation — which is the
arrangement ADR-077 rejected one ADR earlier, for reasons that turn out to apply to a
data path and not to a query path.

**Decision.** Every cluster runs its own `VMSingle`, `VictoriaLogs` and `VMAlert`.
**Grafana runs on the hub only**, holding one datasource pair per cluster, and
reaches each spoke's query API through that spoke's own endpoint, authenticated
by the viewer's OIDC token (ADR-083 addendum 1 §1; addendum 2 §10).

**ADR-083 owns this topology.** It records the architecture the placement belongs to
— Grafana's documented cross-cluster query federation pattern, a central query layer
over independent per-cluster stores — with its provenance, the alternatives weighed
against it, the reconciliation with ADR-077, and the consequences, including the
authenticated public query endpoint each spoke gains and the loss of cross-cluster
PromQL. This ADR records only that the components are placed per cluster; it does not
restate the argument.

### 2. Collection is required machinery; the backend is the selectable capability

§7 says additional destinations are *"additive, never instead"*, and ADR-077's
Context defends a tenant shipping telemetry *"wherever the tenant chooses — Grafana
Cloud, a self-hosted backend, or nowhere"*. Making Alloy part of the selectable
capability would contradict both: disabling the capability would also destroy the
tenant's own external forwarding.

**Decision, expressed in ADR-066's existing terms rather than a new concept:**

| | |
|---|---|
| **Required machinery** — not selectable | Grafana Alloy, `kube-state-metrics`, `node-exporter` |
| **Selectable capability** `observability` | `VMSingle`, `VictoriaLogs`, `VMAlert`, Grafana |

`capabilities.observability.enabled: false` removes the store, the query surface,
rule evaluation and visualisation. Collection and the exporters remain. **The exporters
therefore live in `platform-ops`, not `platform-observability`** — the namespace has
to agree with the classification, because `platform-observability` is what disabling
the capability removes. It also keeps §8 enforceable without an exception: the
release gate forbids any Support Agent collector from naming
`platform-observability`, and an exporter the agent must scrape could not sit in a
namespace the agent may not name. A tenant
with the backend disabled and a §7 destination configured has a complete, supported
configuration — the one ADR-013 supported and this ADR must not remove while
superseding it.

Two things follow. ADR-066 needs collection added to its required-machinery list
(addendum there). And the defect in this ADR's Context — collection that terminates
nowhere — does not return: that was collection with no destination **by default**.
Backend disabled *and* no §7 destination is a tenant's declared choice, and the
values schema warns on it at merge time so it is visible in the pull request rather
than discovered.

**This is also what keeps §8 true.** The Support Agent scrapes `kube-state-metrics`
for node capacity, which ADR-067 names for upgrade pre-flight. If the exporters went
with the capability, disabling observability would degrade support — the licence
check ADR-077 exists to prevent, arriving through a different door.

### 3. Tenant applications get an OTLP ingest path, for metrics and logs

§7.2 offers scraping only. An application instrumented with an OpenTelemetry SDK —
the default for anything new — has nowhere to push, and ADR-077 addendum 4 now has
the Support Agent **exporting** OTLP. The platform would speak OTLP to its vendor
and refuse it from its tenants.

**Decision.** Alloy exposes an OTLP receiver for opted-in tenant namespaces, writing
metrics and logs to the in-box store.

**Traces remain excluded.** §2's reasoning was cost at ADR-070's minimum box, and a
trace store is still the most expensive of the three. No trace store is added, so
the receiver accepts metrics and logs and rejects trace payloads explicitly rather
than silently.

**§2's premise needs correcting, though, and this is the correction.** It says *"no
platform component emits a span"*. `internal/kube-sbt/libraries/tracing/tracing.go:12`
initialises a TracerProvider with an `otlptracehttp` exporter. The exclusion stands
on cost; it does not stand on nothing emitting spans, and the next reader should not
have to discover that.

**The ingest surface is a new platform responsibility and is stated as one:**
capacity, backpressure and authentication for this endpoint are the platform's in a
way scraping never was. It is reachable only from namespaces opted in under §7.2,
and never from outside the cluster.

### 4. The platform ships no query isolation between app teams

§7.2 lands tenant series beside platform series in one store, and §5 gives everyone
one Grafana. Nobody had decided whether one app team may query another's series.

**Decision: no platform-level isolation.** The box belongs to one customer
(ADR-065); what their app teams may see inside it is theirs to configure, using
Grafana's own RBAC. This is ADR-066's line — the platform owns capability health,
the tenant owns capability usage — applied to the query surface.

Recorded as a decision rather than left as an oversight, because the alternative
(VictoriaMetrics tenant IDs and shipped Grafana folder permissions) would have the
platform operating a multi-tenancy model inside the tenant's own box, and
maintaining it forever, for a boundary the customer is better placed to draw.

### 5. Alloy is a DaemonSet and a Deployment, not only a DaemonSet

§3 says DaemonSet and stops. As written, every node scrapes `kube-state-metrics` and
every platform Service, producing one copy of each series per node — which at the
minimum box is not merely wasteful: it makes every alert expression that counts
something wrong.

| | targets |
|---|---|
| **DaemonSet** | node-local only: kubelet, cAdvisor, `node-exporter`, that node's pod logs |
| **Deployment**, `replicas: 1` | cluster singletons: `kube-state-metrics`, platform component `/metrics` |

The reference splits the same way — `assets/node-exporter/daemonset.yaml` against
`assets/kube-state-metrics/deployment.yaml`, both scraped centrally.

### 6. Tenant exclusion is enforced at the target, and at the series where it must be

§3 says tenant namespaces are not collected by default and does not say where that
is enforced. The two questions need different mechanisms, and the reference uses
each for exactly one:

| question | mechanism | reference |
|---|---|---|
| which namespaces | target selection — namespace selector on discovery | `assets/prometheus-k8s/prometheus.yaml:187-204`, and the strict complement plus a separate tenant-owned key at `assets/prometheus-user-workload/prometheus.yaml:198-245` |
| which series from a cluster-scoped exporter | series level — `keep` on `__name__` and `namespace` at scrape | `assets/kube-state-metrics/minimal-service-monitor.yaml:17-25` |

Alloy's platform and tenant discovery are separate blocks with complementary
selectors. `kube-state-metrics` is cluster-scoped and cannot be bounded by target,
so it carries a namespace `keep` relabel at the scrape.

**Two label corrections.** Platform-owned is `topology.platform.io/role: Exists`,
not a specific value — the value distinguishes `hub` from `spoke`, as
`manifests/spoke/spoke-catalog/infra/namespaces.yaml:17-18` already records. The
tenant opt-in complement is therefore `DoesNotExist`, not `NotIn`.

**The label key is reserved by admission.** A ValidatingAdmissionPolicy, fail-closed,
reserves the `topology.platform.io/` prefix on namespaces to the platform's ArgoCD
identity, on CREATE and UPDATE, at any value. **No reference supports this** — CMO
ships validating webhooks for config ConfigMaps and PrometheusRules
(`manifests/0000_50_cluster-monitoring-operator_06-configmaps-validate-webhook.yaml:13`;
`assets/admission-webhook/prometheus-rule-validating-webhook.yaml:23-33`) and
protects no namespace label, because on OpenShift labelling a namespace is already
an admin operation. Note that both CMO webhooks set `failurePolicy: Ignore`, so
monitoring admission never blocks the cluster; a label reservation must fail closed
to be a boundary, which is the opposite choice and deliberate.

**The threat model is stated so the guardrail is not mistaken for a wall.** Under
ADR-065 the tenant is root of their own box and can remove this policy. It guards
against app teams and mistakes, not against the tenant organisation. What actually
bounds ADR-077 is the allowlist's literal targets and the agent's generated egress
policy.

### 7. Tenant destinations carry the cluster's identity

§3 requires `external_labels` to carry the cluster's real identity. Whether §7
destinations receive it was unstated.

**Decision: they do.** Alloy stamps `cluster` and `tenant` on everything it sends,
in-box and external alike. Two labels rather than one, because `cluster` alone is
unique within a box and not at a corporate Prometheus receiving three of them —
which is precisely the complaint this ADR's Context raises about the constant label.

**This departs from the reference, deliberately.** CMO prepends the cluster id as
`__tmp_openshift_cluster_id__`, lets the tenant's own relabel rules run, then
`labeldrop`s it (`pkg/manifests/manifests.go:3553-3570` with the constant at `:60`),
so a tenant destination receives it only if the tenant asks. That is right for
Red Hat, whose `_id` is an account-linked UUID a customer's backend has no business
receiving. This platform's identifier is the tenant's own legible cluster name, and
withholding it from the tenant's own backend serves nobody.

### 8. The observability stack is observed as Kubernetes objects, never through its own port

§8 says shared sources and independent paths. Review found a contradiction behind
it: §1 has the platform shipping and operating the store, this ADR's negative 2
makes retention and capacity the platform's responsibility, and ADR-067 says the
claim extends exactly as far as the telemetry — but a strict reading of §8 leaves
the platform no evidence about the thing it just took responsibility for.

The reference does not have this problem because it reads the store: `telemeter`
federates from Prometheus (`assets/telemeter-client/deployment.yaml:45-46`) and
ships the monitoring stack's own health outbound —
`openshift:prometheus_tsdb_head_series:sum` (`:654`),
`openshift:prometheus_tsdb_head_samples_appended_total:sum` (`:660`),
`monitoring:container_memory_working_set_bytes:sum` (`:666`),
`profile:cluster_monitoring_operator_collection_profile:max` (`:695`).

**Decision, which resolves it without opening a port:**

> The Support Agent observes the observability capability **as Kubernetes objects**,
> through `kube-state-metrics`, which it already scrapes. It never reaches an
> observability component's own endpoint, for metrics or for query.

Every fact retention and capacity need is an object fact, and all four series are in
the reference's minimal collection profile
(`assets/kube-state-metrics/minimal-service-monitor.yaml:22`):

| question | series |
|---|---|
| is the store running | `kube_statefulset_status_replicas_ready` |
| is it crash-looping | `kube_pod_container_status_restarts_total` |
| how much volume does it have | `kube_persistentvolumeclaim_resource_requests_storage_bytes` |
| is the volume healthy | `kube_persistentvolume_status_phase` |

**The stated scope limit:** the store's *internal* health — cardinality, ingest
rate, query latency — is outside the evidence-backed maintenance claim. ADR-069
carries the same sentence, because that is where a tenant looks to find out what is
promised.

**Why the port matters more than the rule.** `VMSingle` serves `/metrics` and its
query API on one port — `manifests/hub-core-services/victoriametrics/storage.yaml:103-107`
uses `:8429` for datasource, `remoteWrite` and `remoteRead` alike — so no network
policy can separate reading *about* it from reading *from* it. The enforceable form
is the agent's egress port list (ADR-077 addendum 4, decision 9), and this decision
is what keeps that list free of `8429` without costing the platform its evidence.

### 9. Ingress to the observability namespace is default-deny

There is no NetworkPolicy governing ingress to `platform-observability` anywhere in
this repository. "Tenant workloads cannot reach the store directly" was therefore an
assumption, not a fact.

**Decision.** `platform-observability` ships a default-deny ingress policy admitting
only Alloy (write), VMAlert (query and write), Grafana (query), and the hub's
Grafana at the spoke's ingress (decision 1). A tenant cannot open it: the policy is
platform-owned and reconciled with `selfHeal`.

Which makes the §7.2/decision 3 path the **only** supported route for tenant
application telemetry, and makes that a property of the manifests rather than a
sentence in a document.

### 10. Retention, and what is not backed up

**15 days for metrics and 7 days for logs at ADR-070's minimum box**, declared in
the bundle and overridable by the tenant in their own values.

**Neither store is backed up.** Both hold reproducible, short-retention data; their
configuration is in Git and their contents regenerate from the next scrape. The
reference agrees by omission: telemeter ships no backup or retention series for the
monitoring stack at all, only cardinality, ingest rate and memory
(`manifests/0000_50_cluster-monitoring-operator_04-config.yaml:648-666`).

**This ADR's negative 2 is amended.** It reads *"Retention, backup and capacity for
two data stores become the platform's responsibility and the maintenance promise's
problem."* It becomes **"Retention and capacity"**, with the sentence above added.

**And ADR-077's `backup-health` evidence category is unaffected**: it reads
`cnpg_collector_last_available_backup_timestamp` — the tenant's data, which is not
reproducible. Extending it to the telemetry stores would make a stopped backup that
should not exist into a support-visible fault.

### 11. Components, and the orphan this ADR's own gate exists to catch

`manifests/argocd/components/03/kube-state-metrics.yaml` exists, `components.yaml`
has no entry for it, and this ADR's `components:` block does not list it — the exact
condition §4's gate was written to catch, in the ADR that invented the gate.

```architecture
capabilities:
  - observability
components:
  - grafana-alloy
  - kube-state-metrics
  - node-exporter
  - platform-alerts
```

`kube-state-metrics` and `node-exporter` are registered as required machinery
(decision 2), not as parts of the selectable capability.

**Naming is reconciled three ways.** §4's table says `victoria-metrics-operator`,
the descriptor is `victoriametrics-operator.yaml`, and `components.yaml` folds the
operator into `victoriametrics`. It becomes `victoriametrics-operator`, a component
in its own right — it has its own descriptor, its own sync wave and its own upgrade
risk.

**And `components.yaml`'s capability note is stale.** It reads *"the collection half
ships and the store, query, visualisation and rule evaluation do not, so there is
nothing to promise maintenance of yet"*, while every component beneath it is
`status: shipped` and the descriptors now exist. The `lifecycle: declared` may still
be right; the reason given for it is no longer true.

### 12. Bookkeeping

| item | correction |
|---|---|
| ADR-013 | This ADR **supersedes** it. `docs/adr/013-hub-spoke-observability-architecture.md:3` already says so; this ADR never did. Added to the header, Impact and References. |
| Constraint list | Drops ADR-077, which is `Proposed` — an accepted decision cannot be constrained by an unaccepted one, and §8 constrains ADR-077 as much as the reverse. Becomes: *constrained by ADR-065, ADR-066, ADR-069, ADR-070, ADR-071; supersedes ADR-013; bounded against ADR-077 (§8)*. |
| "check A" | Dangling. It means `scripts/validate/architecture-consistency.py`, which §4 already names. |
| ADR-064's title | *Bundle Promotion and Cell-Scoped Policy* (`docs/adr/064-bundle-promotion-and-cell-scoped-policy.md:1`), not "Promotion via Renovate". |
| §3's claim about ADR-077 | **Correct as written.** A review reading drafted the opposite: the allowlist names targets literally, but `manifests/hub-core-services/support-agent/network-policy.yaml:59-63` selects on `topology.platform.io/role`, so ADR-077 does rely on the label. |

```architecture
acceptance:
  - scripts/validate/architecture-consistency.py
  - scripts/validate/cluster/82-support-agent-independence.sh
  - scripts/validate/cluster/87-observability-tenant-scoping.sh
```

### 13. Named gaps

Recorded rather than closed, because naming them is what stops them being
rediscovered as defects:

**No blackbox or synthetic probing.** §3 lists no probe mechanism, so "is the
ingress serving from outside" is uncovered. The reference has it — platform
Prometheus carries `probeNamespaceSelector` and `probeSelector` beside its service
and pod monitor selectors (`assets/prometheus-k8s/prometheus.yaml:192-195`). An
externally-facing platform without synthetic checks learns about outages from its
tenants.

**Alert content is thin against the obligation.** §6 ships `platform-core-alerts` —
211 lines of pod health and memory pressure. ADR-066 puts a database engine, an
identity provider and a mail server on the platform's side, and ADR-069 grades the
promise by what is observable. Replication lag, backup age, certificate expiry and
token issuance failure have no rules. This is a content backlog, not a design fault,
and it bounds what ADR-069 can currently promise.

**Traces.** Excluded on cost (§2, decision 3), not on merit. Some tenants will want
them and will build their own.

### 13a. What decisions 5, 6, 7 and 10 turned out to require in the manifests (2026-09-19)

Applied. Recorded because three of the four needed something the decision did not
say, and one of them was a defect in this addendum's own reasoning.

**§5 needed a second workload, not a second scrape block.** The DaemonSet keeps
kubelet, cAdvisor, node-exporter and pod logs; a new `grafana-alloy-cluster`
Deployment at `replicas: 1` takes `kube-state-metrics` and the platform `/metrics`.
Two things followed that the table does not imply: the DaemonSet's pod discovery
needs `field = "spec.nodeName=" + sys.env("NODE_NAME")` or every Alloy pod tails
every pod's logs on every node, and the kubelet and cAdvisor relabels need a
`keep` on their own node name, because node discovery returns the whole cluster to
each pod. Without both, splitting the workload moves the duplication rather than
removing it.

**§6's series filter cannot be two `keep` rules.** Consecutive `keep` relabel rules
are ANDed, not ORed: each drops everything it does not match, so a second `keep`
only narrows what the first left. Written as "keep platform namespaces" followed by
"keep cluster-scoped series", the second rule is unreachable -- the first drops
every series whose `namespace` label is empty, which is exactly the cluster-scoped
ones. Every node, persistent-volume and storage-class series would have vanished,
silently, and the alert expressions reading them would have evaluated against
nothing. It is one rule whose regex carries a leading empty alternative:
`"|platform-.*|cert-manager|cnpg-system|kube-system"`.

**§7 needed `tenantId` to exist as a value.** Nothing published the box's own tenant
id to component charts. It is added to `_global-values.tpl` and substituted into
both workloads' `TENANT_ID`, and into the spoke's. Distinct from the `tenantId` the
fleet ApplicationSets carry, which names a customer *of* the box; this is the box's
owner. The spoke's metrics pipeline had `cluster` and no `tenant` while its log
pipeline had both, so one signal identified the box and the other did not.

**§10 was a value change and nothing else.** 15d metrics, 7d logs.

```architecture
acceptance:
  - internal/soloz-cli/bootstrap
```

Four gates, each verified to fail on the defect it guards: a scrape reading raw
discovery output, a config that does not scope to platform namespaces, a constant
cluster label, a collector overlap between the two workloads, a missing tenant
label, and a `kube-state-metrics` scrape that forwards past its series filter.

**One thing this did not settle.** §11 registers `node-exporter` as a component in
its own right, and the implementation uses Alloy's built-in
`prometheus.exporter.unix` inside the DaemonSet. That satisfies §5's placement --
the series are node-local and collected per node -- without a separate DaemonSet on
every cluster, which ADR-070 would have to justify. The registry entry stays
`planned` until that is decided either way, and the gate holds this ADR at Proposed
while it is.

### 13b. Verified on a live cluster (2026-09-19)

Applied to `nutgraf-hub` at 0.1.16-rc.57 and queried. What the run proved, and
what it cost to get there.

**The capability works.** `VMSingle` and `VLogs` report `operational`, both PVCs
bound, and the store answers:

| query | result |
|---|---|
| `count(up)` | 4 |
| `count(kube_pod_info)` | 86 |
| `count(node_memory_MemTotal_bytes)` | 1 |
| `count(kubelet_node_name)` | 1 |

**§7 holds.** `count by (cluster,tenant)(up)` returns exactly one series pair:
`{cluster="nutgraf-hub", tenant="nutgraf"}`. The box identifies itself by name
rather than by the constant `"hub"` this ADR's Context records as a defect.

**§6 holds, and this is the one worth checking on every box.**
`count by (namespace)(kube_pod_info)` returns `cert-manager`, `cnpg-system`,
`kube-system` and eight `platform-*` namespaces. No tenant namespace. The
series-level filter closes the path a target selector cannot reach, and the
evidence is a query rather than a reading of the config.

**Four defects only a live run found.** Each is now gated
(`internal/soloz-cli/bootstrap/observability_gates_test.go`), and each was
invisible to every check that preceded it:

| defect | how it presented |
|---|---|
| `crds.plain: true` installs zero CRDs | the chart gates them on a subchart condition; CRs apply against an API that does not serve them |
| PVCs took the cluster default | `hcloud-volumes` cannot bind on a hybrid box, whose only worker is on-prem; the claim sat Pending while every other platform PVC was Bound on `local-path` |
| `.Values.global` in a descriptor | `tpl` runs in the environment-manager's context, where global is emitted rather than read; the released render dies and NO bundle is produced |
| the operator's webhook certificate | the chart generates it through helm hooks, which ArgoCD does not run, so the served cert and the trusted caBundle drift. The API server abandons the handshake -- `TLS handshake error ... EOF` -- and with `policy: Fail` that blocks creation of the stores this component exists to reconcile |

The last is the sharpest, and the reason ADR-025 is the platform's rule for
webhook-bearing operators: the operator was `Running`, its endpoints populated,
its Application `Synced` and `Healthy`, and every store CR still failed. Nothing
short of an actual apply showed it.

**Still outstanding on this box.** Grafana's PVC was created under the earlier
default and `storageClassName` is immutable, so it stays `Pending` until the
claim is deleted -- an operator action, and lossless: Grafana holds users,
preferences and annotations, never telemetry.

### 14. This ADR stays Proposed

Unchanged, and now for a reason with a date on it: decision 1 places components on
every spoke, decision 3 adds an ingest surface, and decision 9 adds a policy that
does not exist. The rule §4 states — no component named here exists only as a noun —
is what holds this ADR at Proposed until they are applied.


## Addendum 2: platform standards and tenant observability as code (2026-10-24)

The decisions above give the box a backend, a collection floor and an ingest path.
They do not say how a tenant uses them without hand-authoring dashboards, copying
JSON or wiring evaluators, nor what keeps a tenant's dashboards and alerts working
across bundle upgrades. This addendum records that contract. Every decision here
sits inside addendum 1's topology (ADR-083): each cluster is an independent
observability domain, Grafana runs only on the hub, and nothing is aggregated.

### 1. Trust model

The box belongs to one customer (ADR-065), and every cluster to exactly one tenant
(ADR-062, ADR-064). Isolation between tenants is by cluster, never inside one. App
teams within a box are one organisation's teams: addendum 1 §4 stands, and the
platform ships no query isolation between them. Every decision below assumes this
and would be wrong under mutually untrusted tenants sharing a cluster.

### 2. Platform standards are one generated artifact set

**Decision.** The platform's dashboards, recording rules and alerts are generated
from one platform mixin: pinned upstream mixins (kubernetes, node-exporter,
kube-state-metrics, and the per-component mixins where upstream publishes them)
plus platform-authored ones (workload, GenAI), configured in one place — job names,
`clusterLabel: cluster`, multi-cluster on, datasource, the §3 namespace scope.

- Upstream mixins are pinned by tag in a jsonnet lock file. A pin is upgraded on
  purpose, never followed.
- Generation runs in CI. The output is committed so a release diff shows what
  changed, and a preflight fails when regeneration does not reproduce it.
- A preflight fails when a generated query names a job or metric the platform's
  collection does not produce. A dashboard that renders empty is a release defect,
  not a runtime discovery.
- Dashboards land in a read-only **Platform** folder; rules and alerts are `VMRule`
  objects carrying `app.kubernetes.io/managed-by: platform` (§6), reconciled with
  `selfHeal`.
- Jsonnet is the one authoring toolchain, for upstream and platform dashboards
  alike. Grafana 12's Git Sync is not used: it is a second writer beside ArgoCD.

### 3. Published recording rules are a contract, versioned by the bundle

**Decision.** The platform's recording rules are the stable building blocks tenant
dashboards and rules are written against. Upstream names are kept where upstream
defines them; platform-added rules are keyed on the ADR-088 identity labels
(`tenant-id`, `app-id`, `workload`).

- Every bundle ships a machine-readable contract manifest: each published rule's
  name, labels, semantics and unit; the standard alert templates and their values;
  the dashboard library's exports. The contract version is the bundle version.
- No compatibility shims. A rule that is renamed or reshaped is replaced in the same
  release; old and new are never evaluated side by side.
- A release preflight compares the manifest with the previous release's and fails
  on any removal, rename, label change or semantic change that the release notes do
  not declare, with its replacement and the exact edit a tenant makes.
- The ADR-064 promotion pull request runs the tenant's CI (§9) against the incoming
  bundle's manifest. A tenant asset that the upgrade would break fails that pull
  request; the tenant corrects it there or does not promote.

### 4. Application telemetry: scrape or OTLP, one contract

**Decision.** An application exposes metrics in either of two ways, chosen through
platform-workload chart values (ADR-102). It never writes collection configuration.

- **Scrape.** `ports.metrics` (and a path, when not `/metrics`) yields a container
  port named `metrics`. Alloy discovers that port in opted-in namespaces (§5) and
  scrapes it at a platform-set interval.
- **OTLP.** A chart value injects the standard OpenTelemetry endpoint and protocol
  variables for the cluster-local receiver (addendum 1 §3). Metrics go to the local
  VMSingle, logs to the local VictoriaLogs. Traces remain excluded.
- **Identity is the platform's.** `cluster`, `tenant-id`, `app-id`, `namespace`,
  `pod` and `workload` are derived from Kubernetes metadata — discovery labels for
  scrapes, the sending pod resolved by connection address for OTLP — and overwrite
  whatever the application sent. Telemetry that cannot be attributed to a pod in an
  opted-in namespace is rejected.
- **Limits.** A per-target sample cap; no per-request label (`trace_id` stays in
  the log body, read at query time); GenAI prompt and response content never leaves
  the box (ADR-066), and is dropped from every path bound outside it.
- **GenAI.** Applications that call models emit the OpenTelemetry GenAI conventions'
  metrics (`gen_ai.client.token.usage`, `gen_ai.client.operation.duration`) at a
  pinned convention version; the platform's GenAI dashboards and rules are written
  against that version. The conventions' span tree needs traces and is out of
  scope under §2 until a decision brings traces in.

### 5. One namespace opt-in; alerts are enabled one by one

**Decision.** The §7.2 namespace label is the single gate for collection, tenant
rule evaluation and app alert routing. Opting in enables no alert.

| State | Collected | Rules evaluated | Routed |
|---|---|---|---|
| not opted in | no | no | no |
| opted in, no alert enabled | yes | no | no |
| opted in, alerts enabled | yes | yes | yes |

- Standard alerts are enabled individually (`alerts.<name>.enabled`, with their
  documented values). The platform template renders them as the tenant's own
  `VMRule` objects. Custom alerts are the tenant's `VMRule` objects in their own
  namespaces.
- Every other state is refused, naming the reason, at the earliest layer that can
  see it: the chart refuses to render alerts or app routes for a namespace that is
  not opted in; admission denies a `VMRule` or `VMAlertmanagerConfig` there; tenant
  CI rejects rules whose inputs are neither a published rule (§3) nor a metric the
  app declares.
- A rule that is deployed but blind is detected, not tolerated: platform alerts fire
  on tenant rule evaluation errors, on a rule whose input series do not exist at all
  (`absent()` over its selectors), and on an app with alerts enabled whose namespace
  ingests no samples. An empty result over series that exist is healthy and never an
  incident.

### 6. Alert evaluation is cluster-local, and the platform runs every evaluator

**Decision.** Each cluster evaluates its own rules against its own store, so a spoke
alerts on its workloads while the hub or the hub link is down. There is no central
evaluation: addendum 1 has no aggregated store to evaluate against.

- Each cluster runs two evaluators, both platform-deployed and platform-configured:
  one for platform rules, one for tenant rules. A broken or expensive tenant rule set
  cannot stall platform evaluation. Tenants never choose, deploy or wire either.
- The tenant evaluator selects tenant `VMRule` objects in opted-in namespaces only.

### 7. Tenant rules run inside enforced limits

**Decision.** Tenants may write arbitrary PromQL. Published recording rules are
recommended, not mandatory. The platform bounds execution; it never rewrites or
disables a tenant's rule logic.

- **Admission**, in tenant namespaces: a minimum group `interval` (rejected, not
  raised); group `concurrency` of 1; a query `timeout` on every group through its
  datasource `params`; caps on rules per group and groups per namespace; every rule's
  `labels.namespace` overwritten with the namespace the `VMRule` lives in (§8).
- **Tenant CI**: syntax and expression checks, the §5 input check, and a lint that
  rejects selectors with no label matcher, ranges beyond the platform maximum, and
  unanchored regexes on high-cardinality labels, naming the published rule to use
  instead.
- **The shared store**: the tenant evaluator queries VMSingle through a vmauth path
  with a concurrency cap below VMSingle's own `-search.maxConcurrentRequests`; the
  platform evaluator queries directly, so platform evaluation always has headroom.
  VMSingle's global query limits remain the backstop.
- **Detection**: platform alerts on tenant evaluation pressure — iterations near
  their interval, missed iterations, datasource timeouts, store query pressure —
  routed to the owning app for its rules and to the box's platform receiver for the
  shared store.

### 8. Alert routing: values in, Alertmanager config out

**Decision.** Each cluster runs a platform-managed Alertmanager, shipped with no
receivers (§6 stands: the platform holds no paging or chat credentials). Tenants
declare routing as documented, versioned values; the platform renders the native
configuration. No routing CRD or controller is added.

- **Box level** — the box's values under `alerting:`: receivers by name and type,
  credentials as secret references, and the binding of platform alert categories to
  receivers. One logical policy per box, rendered into every cluster's Alertmanager
  beneath the platform-owned root route. A per-cluster override is the exception,
  made in that cluster's values.
- **App level** — the app's `environments/<env>/<app>/values.yaml`: the app's own
  receivers and routes, which the universal-tenant chart renders as a
  `VMAlertmanagerConfig` in the app's namespace, on the cluster where the app runs.
- **The routing boundary is configured, not assumed.** The platform pins on every
  `VMAlertmanager`: `disableNamespaceMatcher: false`, so each app config's top route
  matches only its own namespace; `disableRouteContinueEnforce: false`, so no app
  route can stop an alert reaching the platform root; and a config namespace
  selector naming opted-in namespaces only. A preflight asserts all three.
- **The alert's namespace label is enforced.** The namespace matcher reads the
  alert's label, which a rule can drop or forge, and `VMAlert` has no enforced
  namespace label. Admission therefore overwrites `labels.namespace` on every tenant
  rule (§7), and tenant CI rejects rules that set it.
- **Credentials** live in the tenant's secret store and arrive through
  ExternalSecrets. A spoke reads only its own cell path (ADR-031), so a box-level
  receiver's key is materialised per cell. The platform declares the keys each cell
  needs; a missing one fails that cluster's sync, naming the key.

### 9. Tenant dashboards and rules are authored in the tenant's repository

**Decision.** Tenants author in their gitops repository; the platform renders and
provisions. The tenant's responsibility ends at the declaration.

- **Dashboards**: `environments/<env>/<app>/dashboards.yaml`, under `dashboards:`,
  each entry a dashboard's JSON (valid YAML). The ApplicationSet that already renders
  the universal-tenant chart on the hub for each app adds the file as an optional
  value file; the chart renders one labelled ConfigMap per dashboard into the app's
  hub namespace, in that app's folder. The hub Grafana sidecar watches platform and
  tenant-app namespaces, filtered by the chart's label.
- **Render-time validation**: `uid` and `title` required; `uid` prefixed by the app
  id, so no app overwrites another's or a platform dashboard; datasources limited to
  the platform's; count and size bounded.
- **Git is the source of truth.** A UI edit is overwritten at the next sync. Folders
  organise ownership; they do not isolate queries (§1).
- **Authoring aids**: a jsonnet library, versioned with the bundle, exports the
  platform's selectors, variables and panels, so tenant dashboards match the
  platform's without copied JSON. Plain JSON remains valid.
- **Tenant CI**, shipped in the tenant gitops template: `promtool check rules` and
  rule unit tests, a dashboard lint, the §5 input check and the §7 query lint, run
  on every pull request and on every promotion pull request against the incoming
  contract (§3).

### 10. Grafana reaches each cluster through that cluster's endpoint

**Decision** (ADR-083, restated as the contract the dashboards rely on). Grafana
holds one datasource pair per cluster and no federation:

- the hub's own pair queries the hub's stores in-cluster;
- each spoke's pair — `<cell>-metrics` (`prometheus`) at
  `https://victoriametrics.<cell>.<subdomain>.<domain>/prometheus` and `<cell>-logs`
  (`victoriametrics-logs-datasource`) at `…/logs` — queries that spoke's
  `spoke-query-endpoint`, which authenticates the caller and forwards to the spoke's
  own stores;
- authentication is the viewer's OIDC token from the box's issuer, forwarded with
  `oauthPassThru` (ADR-083 addendum 1 §1), so revocation at the issuer stops a
  query;
- the pairs are generated from the cluster registry, one per spoke; a spoke without
  a pair fails the render;
- dashboards select a cluster through the `cluster` variable. Cross-cluster PromQL
  is not offered.

### 11. Evidence that shaped these decisions (2026-10-23 to 2026-10-24)

Each fault below was silent — every component reported healthy — which is why the
decisions above put a check, not a convention, behind each invariant.

| Fault | Fixed by | Guard |
|---|---|---|
| The platform-namespace filter read namespace labels that Alloy v1.5.1's pod discovery never attaches; no platform pod log was shipped from any cluster | Alloy v1.20.1, `attach_metadata { namespace = true }` | preflight 078 |
| Spoke pod logs went only to the external destination, which refused every batch (no push path) | in-box store always; push paths appended | preflight 078 |
| The spoke ran no Reloader, so every reload annotation there — including credential-rotation triggers — was inert, and a release's config change was never loaded | Reloader in the spoke catalogue | preflight 030 |
| The spoke agent's `CELL_ID` came from a pod label nothing set; spoke telemetry carried no `cluster` | substituted from the spoke's name | preflight 078 |

### 12. Gaps these decisions require closing

Found on the dev box and recorded as acceptance gates, not open design:

- the spoke collects no cluster metrics: no kube-state-metrics, kubelet, cAdvisor or
  node-exporter scrape, and the CNPG scrape is down;
- the §7.2 namespace opt-in is not read by spoke collection;
- the OTLP path attaches no Kubernetes identity; it trusts what the sender sends;
- the chart has no OTLP endpoint value;
- both evaluators select only platform rules and have no notifier, and no
  Alertmanager exists;
- the Grafana sidecar watches one namespace;
- the spoke query endpoint's listener and route are withheld (ADR-051, 2026-09-21
  amendment), and nothing generates spoke datasource pairs;
- job names are the collector's component names, not the conventional ones the
  mixins select on.

### 13. Verified before implementation, at the deployed versions

The decisions depend on behaviour that must be confirmed in the reference source at
the deployed tag before the component that relies on it is built: vmauth per-user
concurrency limits; `VMRule` group `params` reaching the datasource; VMSingle's
search-limit flags; Kyverno mutation of nested rule lists; that the hub-side
ApplicationSet yields an Application for every app; ArgoCD's handling of an optional
value file; the k8s-sidecar's namespace selection; Alloy's Kubernetes-attributes
processor for OTLP. The routing-boundary fields of §8 are confirmed in the vendored
VictoriaMetrics operator CRDs (v0.49.1).

### 14. Delivery order

Each step depends on the one before; a dashboard or alert is not operationally ready
until the telemetry beneath it is.

1. Spoke collection floor: kube-state-metrics, kubelet, cAdvisor, node-exporter, the
   CNPG scrape; conventional job names.
2. Namespace opt-in in spoke collection.
3. OTLP identity: pod association and label overwrite; the chart's endpoint value.
4. Tenant scrape and OTLP through the chart contract.
5. The platform mixin and the published recording-rule contract manifest.
6. Alerting: the tenant evaluator, Alertmanager, the routing values, the admission
   and CI guards.
7. Tenant dashboards through the hub-side render.
8. Spoke datasources in the hub Grafana, once the query endpoint is routed.

### 15. A step is accepted on end-to-end evidence, never on a green sync

A synced Application and a rendered config prove nothing about telemetry: every
fault in §11 shipped green. Each delivery step is accepted only when its path is
exercised end to end on a real box, and the checks become cluster validation
scripts named for this ADR, so they rerun on every release:

- **Collection**: every expected target is `up`, and a known series from each job
  is present in the cluster's own store, with a non-empty `cluster` label.
- **Logs**: a known platform pod's lines are queryable in the cluster's store, and
  in each additional destination the box has added.
- **Identity**: a test pod sending OTLP with forged `tenant-id`, `app-id` and
  `namespace` lands with the platform's values; a sender in a namespace that is not
  opted in is rejected.
- **Evaluation**: a test rule fires in each evaluator; a tenant rule that forges
  `labels.namespace` reaches only its own app's route; a tenant rule that exceeds
  its timeout is cut off while platform evaluation keeps its interval.
- **Routing**: the always-firing heartbeat (§16) reaches the box's heartbeat
  receiver from every cluster.
- **Locality**: with the hub link cut, a spoke still evaluates and delivers its
  alerts.
- **Promotion**: a bundle carrying a declared breaking contract change fails a
  tenant's promotion pull request that uses the removed name, and names the
  replacement.

### 16. The observability stack reports its own degradation

The platform must know when observability is degraded, not only observe
applications through it. Platform-owned rules, in the platform evaluator, cover the
stack itself:

- **A heartbeat.** One always-firing alert per cluster, routed to a heartbeat
  receiver the box declares in its routing values (§8). Its *absence* is the signal:
  a dead evaluator, Alertmanager or delivery path stops the heartbeat, which no
  firing alert can report. A box that declares no heartbeat receiver fails its
  routing render.
- **Collection**: a target down, a job with no targets, a collector's dropped or
  failed sends, a destination refusing writes.
- **Storage and query**: store ingestion stalls, disk pressure, query timeouts and
  concurrency saturation (§7).
- **Evaluation**: evaluator errors and missed iterations, for both evaluators.
- **Delivery**: Alertmanager notification failures by receiver, and a receiver whose
  credential is missing (§8).
- **Visualisation**: a datasource the hub cannot reach, per cluster (§10).

These route to the box's platform receiver. They are the platform's evidence for the
capability it operates (addendum 1 §8), and they come into scope with the step that
introduces each component (§14).

This ADR stays Proposed (addendum 1 §14) until these are applied.

## Addendum 3: the backend is the box owner's Grafana Cloud stack (2026-10-24)

**Supersedes** §1, §4 and §5; addendum 1 §1, §9 and §10; ADR-083's federation
(Grafana querying per-cluster stores); and addendum 2 §6, §7's store-side guard,
§8's in-box Alertmanager, §9's ConfigMap delivery and §10. Everything else in this
ADR stands, including §3's collection rules, the tenant exclusion of addendum 1 §6,
and addendum 2's contracts for telemetry identity, opt-in, the recording-rule
manifest and tenant authoring — now applied against Grafana Cloud.

### 1. Decision

The box's observability backend and UI is a **Grafana Cloud stack owned by the box's
owner**. Every cluster sends its metrics and logs there; dashboards, recording rules
and alert rules are provisioned there as code. The box runs no telemetry database
and no Grafana.

The stack is the customer's account, not the platform's: telemetry goes to the
destination its owner chose, which is ADR-066's line. The platform still decides
*what* is collected (§3, addendum 1 §6): tenant namespaces stay excluded unless
opted in.

**This makes a Grafana Cloud stack a precondition** of the observability capability,
the trade §1 had rejected. It is accepted as a product decision. A box without the
required credentials (§6) fails its observability render, naming each missing key.

### 2. Clusters hold telemetry only until it is delivered

Each agent's on-disk state is the transit buffer, deleted once delivered — not a
store anyone queries:

| Signal | Buffer | Deleted |
|---|---|---|
| Metrics | the `prometheus.remote_write` WAL | every 15m (`truncate_frequency`), below the lowest sample successfully sent, once at least 5m old (`min_keepalive_time`) |
| Logs | the node's container log files, read from Alloy's persisted positions | by kubelet's rotation; Alloy resumes from the last position delivered |

**Outage tolerance:** metrics unsent for 24h (`max_keepalive_time`) are dropped;
logs are lost once rotation removes a file Alloy has not delivered. Both losses are
counted and alerted (§5).

The buffer must survive restarts, or it buffers nothing: node agents keep their
storage path on the node (`hostPath`), cluster agents on a persistent volume. The
earlier `emptyDir` lost the WAL and the positions on every restart.

**Removed:** VMSingle, VictoriaLogs, VMAlert and the VictoriaMetrics operator on every
cluster; the hub Grafana, its datasources and route; the spoke query endpoint and its
certificate. The ADR-051 listener conflict that blocked the query endpoint no longer
applies.

### 3. Rules are synced per cluster and pinned to it

Recording and alert rules are `PrometheusRule` objects in the cluster they describe.
Each cluster's cluster agent syncs them to the stack's ruler (`mimir.rules.kubernetes`):

- its ruler namespace prefix is the cluster's name, so one cluster's sync never
  replaces another's rules;
- every query is pinned to `cluster="<name>"` and every rule stamped with it, so a
  rule evaluates only its own cluster's series in a store every cluster shares;
- platform rules carry `app.kubernetes.io/managed-by: platform`; tenant rules are
  selected only in opted-in namespaces (addendum 2 §5), with `labels.namespace`
  enforced at admission (addendum 2 §7).

**Evaluation moves to Grafana Cloud.** A cluster that loses its link still buffers
(§2) but raises no alert itself; its silence is detected in the stack (§5).

### 4. Dashboards are reconciled into the stack

Dashboards — the generated platform mixin (addendum 2 §2) and tenants' own — are
`GrafanaDashboard` objects reconciled into the stack by the Grafana Operator on the
hub, targeting the stack as an external instance. Platform dashboards go to a
**Platform** folder; each app's to its own. The operator corrects drift, so a UI
edit is reverted and Git stays the source of truth. Each dashboard selects its
cluster through the `cluster` variable.

### 5. The pipeline reports its own failure, from outside the cluster

- **Silence.** A per-cluster rule evaluated in the stack fires when a cluster's
  `up` series are absent: the link, the agent or the credential has failed. This is
  the heartbeat (addendum 2 §16), now detected where the data lands.
- **Loss.** Agents' dropped samples, WAL samples dropped at `max_keepalive_time`,
  failed log sends and rule-sync errors are alerted per cluster.

### 6. Credentials, least privilege

| Key | Scope | Where |
|---|---|---|
| `GRAFANA_CLOUD_API_KEY` | `metrics:write`, `logs:write` | every cluster's agents |
| `GRAFANA_CLOUD_RULES_TOKEN` | `rules:read`, `rules:write` | every cluster's cluster agent |
| `GRAFANA_CLOUD_DASHBOARDS_TOKEN` | a stack service account (Editor) | the hub's Grafana Operator |

The stack's Grafana URL is not a secret: it is a per-box value,
`global.grafanaCloudStackUrl` in the box's cluster values, required like the
domain.
| `GRAFANA_CLOUD_READ_TOKEN` | `metrics:read`, `logs:read` | validation only (§15), never in a cluster |

Spoke keys are materialised per cell (ADR-031), as the existing Grafana Cloud keys
are. The token in use on 2026-10-24 was write-only (every read returned 401), which
is why the rules, dashboard and read keys are new.

### 7. Consequences

- **ADR-098 loses its quantity source.** Its metering agent queried the local
  VMSingle for CPU and memory (Decision 7). Metering must source quantities itself
  — scraping cAdvisor directly — or keep a store of its own. ADR-098 decides; it is
  still planned, so nothing deployed breaks.
- **Telemetry costs are the stack owner's,** in series and log volume. Collection
  stays bounded (§3) so the bill is.
- **No local query** during a link outage. Data is buffered, not viewable, until
  delivered.
- **§15 checks query the stack** with the read token, from outside the cluster.

```architecture
capabilities:
  - observability
components:
  - grafana-alloy
  - kube-state-metrics
  - node-exporter
  - platform-alerts
acceptance:
  - scripts/validate/cluster/078-collection-floor.sh
  - scripts/validate/preflight/078-pod-discovery-attaches-namespace-labels.sh
```

## References

- **ADR-064**: Promotion via Renovate — how dashboards and rules reach a tenant
- **ADR-065**: The Control Plane Ships Into the Box
- **ADR-066**: The Platform Boundary — why tenant namespaces are opt-in
- **ADR-069**: The Maintenance Promise — why logs are required
- **ADR-070**: The Minimum Supported Box — why `VMSingle`
- **ADR-071**: How This Platform Differs from Kubefirst — every capability ships to every tenant
- **ADR-077**: The Support Agent — the other side of §8
- **ADR-083**: Cross-Cluster Observability Federation — the architecture behind addendum 1 decision 1
- **ADR-030**: Autonomous Credential Rotation Lifecycle — why the spoke runs Reloader (addendum 2 §11)
- **ADR-031**: Tenant Secret Isolation Topology — why receiver keys are materialised per cell
- **ADR-051**: Environment DNS Naming and Public Gateway TLS — the query endpoint's host and its withheld listener
- **ADR-088**: The Tenant and the Application Are Different Axes — the identity labels
- **ADR-102**: The Platform Workload Library Chart — the application-facing telemetry values

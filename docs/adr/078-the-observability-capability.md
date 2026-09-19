# ADR-078: The Observability Capability

**Date:** 2026-09-12

**Status:** Proposed

*Constrained by: ADR-065 (The Control Plane Ships Into the Box), ADR-066 (The Platform Boundary), ADR-069 (The Maintenance Promise), ADR-070 (The Minimum Supported Box), ADR-071 (How This Platform Differs from Kubefirst), ADR-077 (The Support Agent)*

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
  - victoriametrics
  - victoriametrics-alerts
  - victoria-logs
  - vmalert
  - grafana
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
reaches each spoke's query API through that spoke's own ingress with mTLS.

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
  - victoriametrics-operator
  - victoriametrics
  - victoria-logs
  - vmalert
  - victoriametrics-alerts
  - grafana
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

### 14. This ADR stays Proposed

Unchanged, and now for a reason with a date on it: decision 1 places components on
every spoke, decision 3 adds an ingest surface, and decision 9 adds a policy that
does not exist. The rule §4 states — no component named here exists only as a noun —
is what holds this ADR at Proposed until they are applied.


## References

- **ADR-064**: Promotion via Renovate — how dashboards and rules reach a tenant
- **ADR-065**: The Control Plane Ships Into the Box
- **ADR-066**: The Platform Boundary — why tenant namespaces are opt-in
- **ADR-069**: The Maintenance Promise — why logs are required
- **ADR-070**: The Minimum Supported Box — why `VMSingle`
- **ADR-071**: How This Platform Differs from Kubefirst — every capability ships to every tenant
- **ADR-077**: The Support Agent — the other side of §8
- **ADR-083**: Cross-Cluster Observability Federation — the architecture behind addendum 1 decision 1

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

**Metering.** OpenMeter is a billing system, not an observability one, and its architecture is a separate decision. Noted rather than decided here: OpenMeter and its ClickHouse are applied by no component, while `cmd/kube-sbt/main.go:24` defaults to `http://openmeter-api.platform-billing.svc.cluster.local` and kube-sbt *is* deployed — a running component calling a Service nothing creates. The architecture-consistency gate now reports it rather than leaving it to be noticed.

**Alert routing and on-call.** §6. The platform evaluates; the tenant decides who is woken.

**Tenant application observability.** The platform ships the capability and the tenant uses it for their own workloads under §7. What those workloads emit, and whether it is any good, is theirs (ADR-066).

## Components

Registered in `manifests/architecture/components.yaml` and checked by `scripts/validate/architecture-consistency.py`. This ADR stays **Proposed** until every one of them is shipped and applied, which is the mechanism that keeps a decision from resting on a component nothing deploys.

```architecture
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

## References

- **ADR-064**: Promotion via Renovate — how dashboards and rules reach a tenant
- **ADR-065**: The Control Plane Ships Into the Box
- **ADR-066**: The Platform Boundary — why tenant namespaces are opt-in
- **ADR-069**: The Maintenance Promise — why logs are required
- **ADR-070**: The Minimum Supported Box — why `VMSingle`
- **ADR-071**: How This Platform Differs from Kubefirst — every capability ships to every tenant
- **ADR-077**: The Support Agent — the other side of §8

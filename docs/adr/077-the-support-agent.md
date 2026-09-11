# ADR-077: The Support Agent

**Date:** 2026-09-12

**Status:** Proposed

*Constrained by: ADR-065 (The Control Plane Ships Into the Box), ADR-066 (The Platform Boundary), ADR-067 (Support Telemetry and the Basis of Maintenance)*

> The agent establishes the observable boundary of a maintenance obligation. It does not establish permission to operate the platform.

## Context

ADR-067 settles what support telemetry is for and what it may carry: a maintenance claim extends exactly as far as the telemetry a tenant exports, the export is egress-only, its scope is the control plane and not the tenant's business, and withdrawing it degrades the service rather than the operation. It is silent on the mechanism, and names ADR-013's Grafana Alloy `remote_write` as the thing that already exists.

That silence has already cost something. Scaffolding was built to collect a tenant's own Grafana Cloud credentials and, finding none, to tell the tenant their maintenance was unsupported. The tenant's private monitoring account had become a condition of being supported — a licence check wearing a configuration message, in a product whose position is that the software is free and the accountability is what is sold. The defect was not in the ADRs; it was that nothing said which channel carried support telemetry, so the nearest available channel was assumed to.

Three things are distinct and were not being kept apart:

**Platform telemetry** is what the tenant's own observability stack collects and ships wherever the tenant chooses — Grafana Cloud, a self-hosted backend, or nowhere. It is a complete capability every tenant receives whether or not they ever buy support, it requires no relationship with the platform, and it is none of the platform's business.

**Support telemetry** is the evidence a maintenance obligation is reasoned from. It flows to the platform and requires enrolment.

**Licensing** does not exist. No capability is withheld, no runtime code asks whether a tenant is entitled to anything, and the platform never refuses to run.

Collapsing any two of those produces the defect above. This ADR keeps them apart by naming the mechanism for the second one.

They are two independent outbound relationships, and neither is a precondition of the other. A tenant may export their own telemetry and buy no support; may buy support and export nothing of their own; may do both, to different places; or neither. The Support Agent is not the tenant's observability tool, and the tenant's observability stack is not the platform's evidence channel.

The comparison set is instructive rather than theoretical. OpenShift's `telemeter` ships a bounded metric subset from a customer cluster to Red Hat; its ClusterRole grants `create` on `tokenreviews` and `subjectaccessreviews` and read access to nothing, and its scope is two hundred and fifteen explicit selectors in a ConfigMap the customer can read. Red Hat's `insights-client` lets a customer subtract from collection and inspect the exact archive before it is sent. Canonical's `landscape-client` enrols with one credential against one destination. Each of those is a decision this ADR takes.

## Decision

**A proprietary agent, shipped inert to every cluster the platform builds, reports a bounded set of control-plane facts outbound to the platform once a tenant enrols it. One design, one instance per cluster. It is the only proprietary runtime component required for the SOLOZ support service, and the only place the commercial relationship appears in running software.**

### It ships to every tenant and does nothing until enrolled

ADR-071 establishes that the platform publishes no catalog: every capability ships to every tenant, a disabled one is still maintained, and enabling it later requires no action by the platform. The agent is not an exception. It is delivered in the bundle, present and idle, and enrolment is what gives it somewhere to report.

There is no build in which the agent is absent and no build in which the platform is reduced. A tenant who never enrols runs the same software as one who does, receives the same bundle versions, and is proposed the same upgrades.

### The only runtime question is whether this box is enrolled

Not whether a subscription is current, not whether a capability is licensed, not how many clusters exist. Those questions do not appear in the platform's code, and the agent does not answer them.

This is stated as a prohibition because the pressure to relax it is predictable. A component that already reports cluster facts outbound is one small change from reporting them for billing, and one more from declining to start. Neither is a change to this agent's configuration; both are changes to what the product is.

### It is a bounded control-plane evidence collector, and holds no Kubernetes access

The term matters. "Metrics reader" describes what the first collector does and would be quietly false the moment a second one reads certificate expiry or backup state from somewhere that is not a metrics endpoint — and someone will add one, correctly, and the description will no longer bound anything. What bounds the agent is the category of evidence it may gather and the allowlist of fields it may emit, not the transport any one collector happens to use.

**The agent collects explicitly defined control-plane evidence from platform-owned interfaces and artefacts. It does not depend on the tenant's observability backend.**

Its collectors read platform health endpoints, platform component metrics endpoints, platform-generated artefacts, certificate state, backup state, and whatever else this ADR's categories admit and the allowlist names. What it must not do is depend on where the tenant sends their own telemetry.

That independence is the point, and an earlier draft got it wrong by saying the agent "reads the metrics endpoint its own cluster already exposes". Read literally that makes support telemetry contingent on the tenant having deployed a metrics store, and it led directly to the conclusion that the platform must ship one before support could work. It must not: a tenant's observability stack and its destination are theirs to choose or decline (ADR-066), and a support channel that breaks when they choose differently is a support channel coupled to something it has no business in.

Its ClusterRole grants nothing but what its authenticating proxy requires. It holds no `get`, `list` or `watch` on any resource, in any namespace, and no write verb anywhere.

This is the form the claim has to take. "The agent is not a control-plane backdoor" is a sentence in a document; a ClusterRole that grants no read access is a fact a tenant's security review confirms in ten seconds, and a release gate asserts it against the rendered manifest rather than against anyone's memory.

### What "control-plane facts" means is normative, by category

The allowlist says which fields leave. This says which fields may ever be proposed for it, and it binds the platform as well as the tenant: a field outside these categories does not belong in a bundle release, and a reviewer refusing one needs no further argument than this list.

**May be collected** — the state of what the platform ships and operates:
platform component versions and the bundle version they resolved from; component and reconciliation health; node health and capacity; certificate expiry; backup health; platform policy state; and metrics emitted by platform components.

**May never be collected** — the tenant's business, in any form:
tenant application resources; application logs; application metrics; Secrets; ConfigMaps carrying tenant data; workload payloads; and the contents of application images.

ADR-066 draws this line as the boundary between a capability and a workload, and ADR-067 states it as scope. It is repeated here as a category test because an agent is where the line is crossed in practice, one plausible field at a time.

### Collectors run defined queries; they never forward what a response happened to contain

Each collector issues a predefined query whose result schema is known in advance, and the agent serialises only the allowlisted fields of that schema.

This is stated because the alternative is easy to build and hard to notice. An agent that issues `GET /api/v1/query` with a caller-supplied expression and ships what comes back has an allowlist that filters labels over an essentially unbounded payload — the shape of the response is then whatever the cluster happens to contain, and the boundary has quietly become a preference. A known schema is what makes the allowlist a bound rather than a filter.

### Allowlisting is the privacy boundary; anonymisation is a second control

The agent emits only fields the bundle explicitly names. The default is exclusion: a field nobody added is a field that does not leave.

This is stated in preference to anonymisation because anonymisation is not a boundary. Hashing a cluster name leaves identifying material in labels, resource names, image references, node names, provider metadata, annotations, addresses and error strings, and a payload that is "anonymised" in that sense is one whose contents nobody has actually bounded. The salt hides the identifiers that remain in an already-bounded payload; it does not make an unbounded one safe.

The allowlist is versioned with the bundle and annotated per entry with what the field is for, on the same reasoning the spoke catalogue's field declaration already carries: the list is the contract, and reviewing it is how the claim stays true.

### The tenant may narrow the scope and the platform may never widen it silently

Effective scope is the bundle's allowlist minus the tenant's denylist, computed inside the box. A tenant can remove anything. The platform can propose additions only by publishing a bundle version, which reaches the tenant as a pull request they merge or do not (ADR-064).

A tenant who narrows the scope narrows what can be supported, and that is the honest consequence rather than a penalty: ADR-067 already grades obligations by what is observable.

### What would be sent can be read before it is sent

The payload for the current cluster can be rendered and inspected without uploading anything. A tenant asked to trust an outbound stream is entitled to read it first, and an engineer diagnosing a gap in support evidence needs to see what the agent sees.

### One agent per cluster, not one per fleet

The agent runs on the hub and on every spoke, each reporting directly to the platform. It is one design instantiated many times, not many designs — which is the distinction that matters, because the claims this ADR makes are about the design and a security review reads one ClusterRole and one allowlist however many copies exist.

Routing spoke telemetry through the hub was considered and rejected. It reintroduces the bottleneck argocd-agent exists to remove: a hub aggregating fleet-wide metrics and re-shipping them doubles the data path and makes fleet-wide reporting a property of one cluster's capacity. It also makes the support channel depend on the component most likely to be the subject of a support case — a hub outage would blind the platform exactly when the evidence matters most. And it is not how this platform already works: argocd-agent runs per spoke to the hub's principal with a per-agent certificate, and the support channel follows the same topology rather than inventing a second one.

Each cluster paying its own egress is also the honest arrangement under ADR-067, which already makes egress cost the tenant's and the volume knowable in advance.

### Identity is a client certificate, issued by machinery that already exists

Each agent authenticates with mTLS, presenting a client certificate whose subject carries its identity — the arrangement argocd-agent uses (`principal.auth: "mtls:subject:CN=([^,]+)"`, or a SPIFFE URI in the SANs), and the one this platform already operates through cert-manager and its issuers.

This is preferred to a bespoke token exchange for two reasons. A certificate with a short lifetime that is renewed is bootstrap material by construction: there is no standing bearer secret in the cluster whose compromise is undetectable and whose revocation depends on someone remembering it exists. And it is issuance this platform already runs and already tests, rather than a protocol invented for one component.

Revocation is per agent. Declining to renew one spoke's certificate stops that spoke reporting and stops nothing running, which is the property ADR-065 requires of every grant a tenant makes, at a granularity a fleet-wide token could not offer.

Where issuance cannot yet produce short-lived certificates for this purpose, a longer-lived credential is an acceptable interim **only** with its rotation and revocation procedure written down as part of shipping it. An interim decision that is recorded is a decision; one that is not is an omission discovered by whoever is compromised.

### Support telemetry never affects operation

Failure to enrol, to authenticate, to renew a certificate, or to transmit MUST NOT affect any platform workload, reconciliation loop, capability, or cluster operation. The agent degrades alone.

After the RBAC restriction this is the most important runtime invariant here, and it is the one most likely to erode by accident: a readiness probe wired to upload success, a sidecar that blocks a pod's start, an init container that waits for enrolment, a controller that treats a transmission error as a reconcile failure. Each is a small change that makes the platform's operation depend on the platform's commercial relationship, which is the thing this architecture exists not to do.

A tenant whose certificate expires, whose network partitions, or who never enrolled runs exactly what they ran before. What stops is the stream, and with it the claim that rests on it.

### Nothing reaches inward

The agent has no Service, no Ingress and no ingress rule, and its egress is restricted to the support endpoint and DNS. No platform component opens a connection into a tenant's cluster, holds a credential for one, or queries it — ADR-067's rule, made a property of the manifests rather than of the agent's behaviour.

## Components

```architecture
components:
  - support-agent
```

Registered in `manifests/architecture/components.yaml`. The manifests, RBAC and
allowlist exist and are gated; the agent and its component descriptor do not, so
the entry is `planned` and this ADR stays Proposed until they do.

## Provenance

Every decision above is taken from a system that already operates it at scale, or is a deliberate departure from one. ADR-071 sets the discipline this follows: a pattern that cannot name where it came from is either an accident or a decision nobody made, and both are worth finding. The sources are in `reference-projects/`; the file named is where the behaviour was verified rather than where it is described.

### Taken, and why

**Minimal RBAC as the form of the claim** — OpenShift `telemeter`
(`1proprietary/cluster-monitoring-operator/assets/telemeter-client/cluster-role.yaml`).
Its ClusterRole grants `create` on `tokenreviews` and `subjectaccessreviews` and read access to nothing at all. Taken because it converts an assurance into an artefact: a tenant's security review reads one file and is done, where a paragraph promising good behaviour requires trusting the author.

**An explicit allowlist, annotated per entry** — OpenShift `telemeter`
(`1proprietary/cluster-monitoring-operator/manifests/0000_50_cluster-monitoring-operator_04-config.yaml`, 215 selectors, each commented with owners and rationale).
Taken because the annotation is what keeps the list reviewable as it grows. An unannotated allowlist of two hundred entries is not a contract anyone can check; it is a list nobody reads.

**The tenant subtracts from collection** — Red Hat `insights-client`
(`1proprietary/insights-client/data/etc/insights-client.conf`: `obfuscation_list`, `redaction_file`, `content_redaction_file`).
Taken because scope the vendor alone controls is scope the tenant has to trust rather than verify. Ours differs in direction: the tenant may only subtract, and additions arrive as a bundle version they merge (ADR-064).

**Reading the payload before it is sent** — Red Hat `insights-client`
(`--no-upload`, `--keep-archive`; `1proprietary/insights-client/README.md`).
Taken because it is the only mechanism here that lets a tenant check the allowlist claim themselves rather than believe it.

**One credential, one destination, revocable** — Canonical `landscape-client`
(`1proprietary/landscape-client/example.conf`: `account_name`, `registration_key`, `url`).
Taken for the shape: enrolment is one act against one endpoint, and undoing it is one act too.

**A per-cluster agent reporting outbound to a central plane** — `argocd-agent`
(`argocd-agent/docs/index.md`: *"Agents Connect Inward: No outbound connections from the control plane"*).
Taken because it is the answer to the problem that project exists to solve, and because this platform already runs it: every spoke reaches the hub's principal this way. Routing support telemetry through the hub instead would reintroduce the bottleneck and make the support channel depend on the cluster most likely to be the subject of a support case.

**Identity carried in a client certificate** — `argocd-agent`
(`argocd-agent/docs/configuration/authentication.md`: `principal.auth: "mtls:subject:CN=([^,]+)"`, or a SPIFFE URI in the SANs).
Taken because a short-lived renewed certificate is bootstrap material by construction — there is no standing bearer secret to leak — and because this platform already issues certificates through cert-manager and already gives each argocd-agent one. A bespoke token exchange would have been a protocol invented for a single component.

### Departed from, and why

**Anonymisation is demoted to a second control.** `telemeter` anonymises labels against a salt (`--anonymize-labels`, `--anonymize-salt-file`) and that is taken, but not as the privacy boundary. Hashing a cluster name leaves identifying material in labels, image references, node names, annotations, addresses and error strings; a payload called anonymous on that basis is one whose contents nobody bounded. Here the allowlist is the boundary and the salt hides what remains inside it.

**No metering, no entitlement** — rejecting Red Hat's `rhsm-subscriptions`
(`1proprietary/rhsm-subscriptions/README.md`: telemetry → metrics ingress → tally → subscription sync → billing usage).
A coherent model for their business and the wrong one here. The moment this agent counts instances it is a licence check, and the position the platform sells — that it never prevents you running the software — is gone.

**One agent kind, not several** — departing from Red Hat's own deployment.
An OpenShift cluster runs `telemeter-client` for a metric stream and `insights-operator` for a periodic state archive; RHEL hosts run `insights-client`. Three kinds, because three problems arrived at different times on different substrates. This platform has one substrate and one boundary, and every seam in a proprietary component is a place its trustworthiness has to be argued again. One kind, instantiated per cluster, is not the same thing as several kinds on one cluster.

**The principal is not the source.** An earlier draft of this decision assumed argocd-agent's principal already aggregated enough control-plane evidence to support a maintenance claim. It does not: the agent synchronises `Application` and `AppProject` objects, which is current object state for two kinds and carries no history, no rates, and nothing about components that are not Applications — certificate expiry, backup state, node pressure, policy violations. Support analysis and any dashboard built from it need time series, so the agent collects from the metrics endpoint as `telemeter` does. This is recorded because the assumption was wrong and plausible, and the next reader will have it too.

## Alternatives considered

**Carry support telemetry on the tenant's existing Grafana Alloy export.** This is what ADR-067 implies and what the code briefly did. It requires the tenant to hold a monitoring account for the platform's benefit, mixes control-plane evidence with the tenant's own workload metrics in a store the tenant configured for a different purpose, and makes the support relationship look like a configuration setting on a component the tenant owns. It also caps the evidence at whatever their stack happens to collect.

**Meter and enforce, as `rhsm-subscriptions` does.** Red Hat ties subscriptions to deployed instances and aggregates usage for billing. It is a coherent model for their business and the wrong one here: the moment the agent counts instances it is a licence check, and the differentiation the platform sells — that it never prevents you running the software — is gone.

**No agent; support on request.** A tenant opens a case and supplies what is asked for. This is honest and is what an unenrolled tenant gets. It cannot support a proactive obligation, because a fault nobody observed is one nobody acted on, and it makes every diagnosis start by asking the tenant to gather evidence they have no reason to know how to gather.

**A platform-operated agent with cluster credentials.** Simplest to build and forbidden by ADR-065: the platform would hold a credential to a tenant's cluster, which is the arrangement the entire control-plane-ships-into-the-box decision exists to avoid.

## Ownership

The agent's image, its allowlist and the receiving plane are the platform's. The denylist, the decision to enrol and the decision to stop are the tenant's. Each agent's client certificate is issued by the platform and held by the cluster it identifies.

No resource in a tenant's cluster is owned by the platform as a result of enrolment. For resource ownership generally, see ADR-039.

## Consequences

### Positive

The commercial boundary is in one place and is a single question with a yes or no answer. Anyone asking what the platform does with a tenant's data has one component to read, one allowlist, and one ClusterRole that grants nothing.

The claims are checkable rather than asserted. Read access is a release gate, the payload is inspectable before it is sent, and the scope is a list in the bundle.

Keeping the three concepts apart removes a class of defect rather than one instance of it. The Grafana confusion was not carelessness; it was the predictable result of an unnamed mechanism, and naming it closes the others.

### Negative

A proprietary component in an otherwise open platform is a thing tenants will reasonably ask about, and the answer has to hold up. Every design decision above exists partly to make that answer short.

The allowlist is maintenance. A field the platform needs and did not anticipate is not available retrospectively, and adding one is a bundle version and a tenant's merge. That latency is the cost of the tenant controlling scope, and it is the correct trade.

Support quality now varies by what a tenant exports, which is visible to them and may read as pressure to export more. ADR-067 already accepts this; it is restated here because the agent makes it concrete.

### Positive and negative at once

Short-lived credentials remove a standing secret and add a dependency: a box that cannot reach the platform to renew stops reporting. That is the correct failure direction — the stream stops, nothing else does — but it means a network partition degrades support, and the degradation must be visible to both sides rather than silent.

## Impact

- **Extends ADR-067.** It decided the principle and left the mechanism open; this names it, and corrects the implication that ADR-013's export is that mechanism.
- **Constrained by ADR-065.** The agent holds no inbound path and the platform holds no credential into the box.
- **Constrained by ADR-066.** Control-plane facts only. A tenant's workloads are theirs and are not observable to the platform.
- **Follows ADR-071.** Every capability ships to every tenant; the agent ships inert rather than being withheld.
- **Bounded by ADR-064.** The allowlist changes by bundle version, proposed as a pull request the tenant merges.

## References

- ADR-013: Observability and Metric Forwarding
- ADR-039: Platform Ownership Model
- ADR-045: Generated Artifacts
- ADR-064: Bundle Promotion and Cell-Scoped Policy
- ADR-065: The Control Plane Ships Into the Box
- ADR-066: The Platform Boundary
- ADR-067: Support Telemetry and the Basis of Maintenance
- ADR-069: The Maintenance Promise
- ADR-071: How This Platform Differs From kubefirst
- argocd-agent: the per-cluster agent and mTLS identity pattern this follows

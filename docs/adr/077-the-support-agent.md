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

### The only COMMERCIAL runtime question is whether this box is enrolled

Narrower than an earlier draft, which said "the only runtime question". It is not:
the agent also has to know whether it is installed, whether its certificate loads,
and what its effective scope is. Those are local operational questions with local
answers. The point stands for the one question that touches the commercial
relationship — see the five-authority table in addendum 3, which separates all of
them.

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

This is stated in preference to anonymisation because anonymisation is not a boundary. Hashing a cluster name leaves identifying material in labels, resource names, image references, node names, provider metadata, annotations, addresses and error strings, and a payload that is "anonymised" in that sense is one whose contents nobody has actually bounded. A salt would hide the identifiers that remain in an already-bounded payload; it would not make an unbounded one safe.

**No anonymisation ships, and none is planned.** The paragraph above is the reason: once the payload is bounded to named fields from named queries, hashing what remains adds a moving part — a salt to generate, hold, never leak and rotate — in exchange for obscuring identifiers a review of the allowlist has already agreed to. The one identifier that does leave is the box's own, in the certificate subject, and that one has to be legible or the platform cannot tell whose box it is looking at. If a field is too sensitive to send, the answer is to remove it from the allowlist, not to disguise it.

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

~~Where issuance cannot yet produce short-lived certificates for this purpose, a longer-lived credential is an acceptable interim.~~ **Withdrawn by addendum 3, decision 5.** This platform's issuance already produces short-lived certificates and already relies on their TTL in place of CRL and OCSP (ADR-032, ADR-035). A longer-lived credential for this one component would be a second PKI lifecycle invented because one receiving end was unfinished, and the unfinished end is the platform's own — not a constraint the tenant's cluster imposes.

### Support telemetry never affects operation

Failure to enrol, to authenticate, to renew a certificate, or to transmit MUST NOT affect any platform workload, reconciliation loop, capability, or cluster operation. The agent degrades alone.

After the RBAC restriction this is the most important runtime invariant here, and it is the one most likely to erode by accident: a readiness probe wired to upload success, a sidecar that blocks a pod's start, an init container that waits for enrolment, a controller that treats a transmission error as a reconcile failure. Each is a small change that makes the platform's operation depend on the platform's commercial relationship, which is the thing this architecture exists not to do.

A tenant whose certificate expires, whose network partitions, or who never enrolled runs exactly what they ran before. What stops is the stream, and with it the claim that rests on it.

### Nothing reaches inward

The agent has no Service, no Ingress and no ingress rule. Its egress is restricted to three destinations and nothing else: DNS, the platform components it collects from, and the support endpoint. (An earlier draft of this sentence said "the support endpoint and DNS", omitting the second — which contradicted the collection clause above and, taken literally, described an agent that could not reach anything to collect from.) No platform component opens a connection into a tenant's cluster, holds a credential for one, or queries it — ADR-067's rule, made a property of the manifests rather than of the agent's behaviour.

## Components

```architecture
components:
  - support-agent
```

Registered in `manifests/architecture/components.yaml` as **shipped**: RBAC,
NetworkPolicy, allowlist, Certificate and Deployment are applied by a boundary-03
descriptor, and the agent is built from `cmd/support-agent`.

```architecture
capabilities:
  - support
acceptance:
  - scripts/validate/cluster/80-support-agent-rbac.sh
  - scripts/validate/cluster/81-support-agent-allowlist.sh
  - scripts/validate/cluster/82-support-agent-independence.sh
  - scripts/validate/cluster/83-support-agent-no-ingress.sh
  - internal/support
```

This ADR nevertheless stays **Proposed**, and for a reason outside the box: the
Support Plane it reports to does not exist yet. Every box now runs the agent
unenrolled — no `endpoint`, no Certificate, nowhere to report — which is the
correct default and is indistinguishable from a tenant who has chosen not to
enrol. Accepting this ADR should wait until enrolment can actually happen, so
that the claim and the mechanism land together rather than a version apart.

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

The agent's image, its allowlist and the receiving plane are the platform's. The denylist, the decision to enrol and the decision to stop are the tenant's. **Each agent's client certificate is issued by the tenant's own PKI and held by the cluster it identifies. The Support Plane registers the tenant's CA and the expected agent identity, and authenticates the connection against them.** (This sentence previously said the platform issued it. That was wrong and contradicted addendum 3: the platform holds no issuer inside a tenant's box, and ADR-035 keeps certificate lifecycle local to the cluster.)

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

## Addendum 1: the credential is the certificate; there is no enrolment token (2026-09-12)

The Decision above describes enrolment as a one-time secret exchanged for a
short-lived credential the agent renews, and allows that v1 might ship a
long-lived bearer token if the Support Plane could not yet issue short-lived
ones. **That allowance is withdrawn, and the exchange is removed.** It was
solving a problem this platform does not have.

`reference-projects/argocd-agent` settles it. Its three authentication methods
are mTLS, header-based (for a service mesh), and userpass — and userpass is
marked *"Development only"* and **deprecated**. There is no token in the
recommended path at all, because in an agent-to-principal design the certificate
*is* the credential:

- **Identity lives in the certificate** and is extracted from it, either from the
  Subject DN or from a URI SAN, by a configured regex whose first capture group
  becomes the agent ID (`internal/auth/mtls/mtls.go`). The extracted ID must be a
  valid DNS label, so an identity that would not name a Kubernetes object is
  rejected at the handshake rather than downstream.
- **The receiving end validates against its own CA**, and requires a client
  certificate rather than accepting one optionally.
- **Per-certificate revocation is a fingerprint blocklist** — SHA-256 over the
  raw certificate, held in a ConfigMap the principal watches
  (`internal/blocklist`, `internal/tlsutil.CertificateFingerprint`). No CRL, no
  OCSP, no token store, and no call back to the tenant's cluster. Note what this
  is *not*: argocd-agent has one trust domain and one operator, so it needs no
  separate notion of entitlement. This platform does, and addendum 3 adds it.
- **Rotation is reissuance**, not renewal: a new leaf is signed for a stated
  validity, and the old fingerprint can be blocked immediately.

What this platform takes, and why each part fits:

**The certificate is issued by the tenant's existing PKI, inside the tenant's box.** ADR-066 lists
certificate issuance as machinery every box has; the agent's client certificate
is one more leaf from it. Nothing new is introduced to hold a secret, and the
agent's credential rotates on the same machinery as everything else in the box.

**Enrolment is the issuance of that certificate, and nothing else.** ~~There is
no secret for the tenant to paste, no first-use exchange...~~ **Superseded by
addendum 3.** The certificate is necessary and not sufficient: it is signed by
the tenant's own CA, which the platform has never seen, so enrolment also
requires registering that CA and the expected subject against an authenticated
subscription. There is still no secret and still no token — a CA certificate is
public material — but "enrolment is the certificate existing" was incomplete.

**Revocation takes effect at the platform's end, within one connection.** The
property ADR-067 requires — withdrawing support telemetry stops the stream and
stops nothing running — holds, without the platform ever reaching into the box.
**But the mechanism is the enrolment state, not the fingerprint blocklist**
(addendum 3, decision 3). The tenant's own PKI renews the agent's certificate
every 24 hours without asking anyone, so blocking a fingerprint would be undone
by the next renewal and a cancelled subscription would resurrect itself within a
day. The blocklist is for a compromised key inside an enrolment that remains
active.

**Short-lived is a validity period, not a protocol.** The concern behind the
original wording was a credential whose compromise is undetectable and whose
revocation depends on someone remembering it exists. A short leaf that reissues
automatically, plus a blocklist that does not wait for expiry, answers it more
completely than an exchange would.

The three-line consequence for ADR-067's clause *"the tenant holds the
destination and the credential"*: the destination is the platform's ingress, the
credential is a client certificate in the tenant's own cluster issued by the
tenant's own issuer, and revoking it is deleting a Secret. All three stay true,
and none of them is a token.

## Addendum 2: aligned against the implementation (2026-09-12)

Building the agent found six places where this ADR and the code disagreed. Five
were the code's fault and are fixed; two sentences here were wrong and are
corrected above. Recorded because an audit that finds nothing is usually an audit
that did not look.

**Enrolment had no mechanism.** The Deployment read `endpoint` and `deny` from
the platform's own allowlist ConfigMap, which carries neither key — so the agent
could never be enrolled, and a tenant adding a denylist to a platform-owned
object would have had it reverted by `selfHeal`. *"The tenant may narrow the
scope"* is not a right a tenant cannot keep. Both now come from
`support-agent-enrolment`, a ConfigMap the tenant owns and the platform never
ships or reconciles. Absent means unenrolled, which is the correct default.

**One agent per cluster meant one, on the hub.** Spokes had none, so per-spoke
revocation — which this ADR offers as the reason mTLS beats a fleet token — could
not happen. The spoke catalogue now carries its own agent and its own
`support-agent:<spoke>` identity, beside the argocd-agent, Alloy and NATS
identities the platform already issues. Two allowlists now exist, so a gate
compares them: a spoke may carry a subset, never a superset, and never a
different query for a collector both have.

**The certificate did not follow the platform's own convention.** It was drafted
at 30 days renewing at 10, where every other agent identity here is 24h/8h. A
support credential is not more special than a reconciliation credential, and a
second rotation cadence is a second thing to be wrong about.

**The ClusterRole granted permissions for a component that does not exist.** It
carried telemeter's `create` on tokenreviews and subjectaccessreviews, which that
project needs for the authenticating proxy in front of its metrics endpoint. This
agent has no such endpoint — no Service, no Ingress, no ingress rule — so there
was nothing for a proxy to authenticate callers to. **There is now no ClusterRole
and no binding at all**, which is a stronger form of the claim this ADR makes
than a minimal one.

**`soloz support preview` asked for a cluster name.** The agent reports the
certificate's subject, so preview could render a payload labelled differently
from the one that would be sent — which defeats the point of reading it first. It
now takes the identity from the certificate, and says so plainly when there is
none.

The two corrections to this document are in place above: the egress sentence
omitted the platform components the agent collects from, and anonymisation was
described as a shipping control when none exists or is planned.

## Addendum 3: the subscription lifecycle, and the Support Agent's state machine (2026-09-12)

This ADR described enrolment and was silent on everything after it. That silence
is not a gap in a corner: what happens when a subscription ends determines the
agent's identity model, its installation, its revocation and its uninstall. It is
settled here, and **ADR-077 is not implementable further until it is.**

Derived from this platform's own boundary — ADR-065's authority limit, ADR-066's
capability model, ADR-067's evidence rule, and the PKI ADR-032 and ADR-035
already establish. The reference projects are used as **implementation patterns
and nothing more**; none of them specifies a subscription-to-agent lifecycle, and
copying a commercial lifecycle from a vendor whose boundary differs from ours is
how a borrowed assumption becomes an architectural defect.

### The hole this exposed first

Addendum 1 took argocd-agent's mTLS design. It transfers less cleanly than it
looked, and the reason is only visible once you ask who holds the CA.

**argocd-agent's principal and its agents share one trust domain, operated by one
party.** `agentctl` signs each agent's certificate with the principal's own CA,
so the principal validating one is validating something it issued. That is true
here too, and for the same reason:
`manifests/argocd/components/01/platform-argocd-principal-certs.yaml` puts the
principal in **boundary 01** — the tenant's own hub. Hub and spokes are one trust
domain inside one box.

The Support Agent is the only component that crosses **out** of that box. Its
certificate is signed by `infisical-fleet-issuer`: the tenant's CA, in the
tenant's Infisical, isolated per box by ADR-031. **The platform has never seen
that CA and cannot validate a certificate issued by it.** "Enrolment is a
certificate existing" establishes an identity the receiving end has no basis to
believe.

### The eight decisions

**1. The Support Agent is a selectable capability.**
`capabilities.support.enabled`, default `true`, exactly like every other
capability in ADR-066. Whether the tenant wants the support service installed is
a declaration in their own values, not a state the platform holds about them.
Because its presence becomes a function of a value, it is declared inline in its
boundary rather than as a descriptor — a descriptor gate takes effect in a
released box and not in a development one (ADR-066 addendum 2).

**2. Subscription state never mutates a tenant cluster.**
No billing event, no Support Plane action, and no platform component causes any
change inside a box. ADR-065 gives the platform no authority that takes effect
there, and an uninstall triggered by a cancellation would be exactly that
authority, arriving through the one component that holds an outbound channel. A
platform that can delete its own agent on cancellation is a platform that can
delete other things, and the tenant has no way to tell those apart.

**3. Cancellation revokes the ENROLMENT, not a certificate.**
The Support Plane moves the enrolment for that tenant and cluster to `REVOKED`,
and **no certificate presented against a revoked enrolment is accepted, however
cryptographically valid it is**. Immediate, unilateral, and it reaches into
nothing.

This distinction is load-bearing and was wrong in an earlier draft. The tenant's
own PKI issues the agent's certificate and renews it every 24 hours without
asking anyone (decision 5, ADR-035). If cancellation blocked only the *current
certificate's fingerprint*, the next scheduled renewal would produce a
cryptographically valid certificate that was not on the blocklist — and the
cancelled subscription would resurrect itself, silently, within a day. **Blocking
a fingerprint cannot express "this tenant is no longer entitled", because the
tenant can legitimately mint a new certificate at any time.**

So the two mechanisms answer different questions and neither substitutes for the
other:

| mechanism | question | scope | lifetime |
|---|---|---|---|
| enrolment state (`ACTIVE`/`REVOKED`) | is this tenant and cluster still entitled? | the enrolment | until re-enrolled |
| certificate fingerprint blocklist | has this particular key been compromised? | one certificate | until that certificate expires |

The blocklist exists for the case where an enrolment stays `ACTIVE` and one
agent's key is believed stolen: block that fingerprint, let the tenant's
cert-manager issue a new one, and support continues uninterrupted. Using it for
cancellation would be using a compromise tool to express a commercial decision.

**4. Removing the agent is a tenant GitOps operation.**
`capabilities.support.enabled: false`, merged by the tenant, and their own ArgoCD
prunes the Deployment, ServiceAccount, ConfigMap, NetworkPolicy, Certificate and
the certificate Secret. Nothing else is touched — not a workload, not ArgoCD, not
Crossplane, not NATS, not any other capability.

These two halves are **deliberately independent**, and that independence is the
property worth protecting:

> The platform can stop standing behind a tenant immediately, without holding any
> authority over the tenant's cluster. Removing the software is the tenant's
> operation, not a billing-side effect.

**5. Identity is short-lived, and there is no durable platform-granted secret.**
The agent holds a certificate with a finite TTL, renewed locally by cert-manager
against the tenant's own issuer — the model ADR-035 establishes for every other
certificate in a box.

**The duration is 24h with `renewBefore: 8h`, and this ADR does not get to choose
it.** ADR-035 addendum §5 classifies certificates by whether the consumer can
reload without restarting: *workload and client identities* take 24h, and
*control-plane serving identities* that cannot hot-reload take up to the 7-day
profile ceiling. The Support Agent is a client identity whose pod restarts
cheaply, so it sits in the first class beside `argocd-agent-client-cert`,
`alloy-client-cert` and `nats-leafnode-client-cert` — which request exactly these
values today.

*(An earlier draft of this decision cited ADR-032 for a 1-hour leaf TTL. ADR-032's
own amendment of 2026-08-31 withdraws that — "The 1-hour leaf TTL is not the
policy" — and records that ADR-035 replaced its issuance and revocation decisions
entirely. What survives from ADR-032 is the no-CRL/no-OCSP position, which its
amendment explicitly retains.)*

**No long-lived bearer token is introduced as an
interim**, and the allowance in the original Decision for one is withdrawn:
inventing a second credential lifecycle because one component's receiving end is
unfinished would violate the PKI model this platform already operates.

**Expiry and revocation are different mechanisms and neither substitutes for the
other.** A short TTL is *cryptographic safety*: it bounds the value of a stolen
key. Enrolment revocation is *commercial authorisation*: it decides whether a
cryptographically valid certificate is still accepted. An unexpired certificate
must not remain acceptable after cancellation, so the Support Plane holds
enrolment state per tenant and per cluster — `ACTIVE` or `REVOKED` — and consults
it on every connection.

**6. The agent distinguishes a transport failure from a rejection.**
Being unable to ask is not the same as being told no, and conflating them is how
an agent either gives up during a network partition or hammers an endpoint that
has refused it. Classification is normative:

| condition | behaviour |
|---|---|
| DNS or network failure | retry with backoff |
| timeout | retry with backoff |
| 5xx, or Support Plane unavailable | retry with backoff |
| TLS handshake refused | retry with backoff, and report locally — this is indistinguishable from a misconfigured proxy or an expired server certificate |
| certificate renewal failure | report locally, retry — the tenant's own issuer failing, not the platform |
| `401`/`403` **without** an explicit revocation marker | retry with backoff, and report locally as an authentication or configuration fault |
| **explicit `ENROLMENT_REVOKED`** from the Support Plane | return to `NOT_ENROLLED`, log once, re-probe on a long interval |

**Only the last row is revocation, and it is never inferred.** An earlier draft
grouped `401`, `403` and a refused handshake into it, which contradicted this
ADR's own rule two paragraphs later. Each of those has mundane causes — a proxy in
front of the endpoint, a CA bundle that has not rolled over, an expired server
certificate, a server-side bug — and every one of them is a transient fault that
should be retried and surfaced to the operator. Treating them as revocation would
stop a healthy tenant reporting because someone misconfigured an ingress.

The Support Plane therefore says so explicitly: the response body carries a
machine-readable `ENROLMENT_REVOKED` reason, and nothing else in the protocol
means revocation. This is Landscape's `unknown-id` — a **typed message**, not an
HTTP status the client interprets.

A consequence worth stating: a Support Plane that is down, or misconfigured, or
unreachable **cannot revoke anything**. Revocation requires the platform to
successfully say a specific thing. Failure to communicate is never revocation,
which is the correct default for a mechanism whose other direction cannot be
undone by the tenant.

**A revoked agent returns to the unenrolled state rather than entering a special
stopped one.** This is Canonical's `unknown-id` handling taken directly: when the
Landscape server no longer recognises a client it says so in-band, and the client
clears its own credential state and falls back to unregistered
(`landscape/client/broker/registration.py:_handle_unknown_id`). It neither halts
permanently nor removes itself.

Two things follow that a "stopped after N retries" design gets wrong. There is no
extra state to reason about — revoked and never-enrolled are the same state, and
there is one path back. And **re-enrolment is picked up without restarting
anything**: an agent parked in a terminal stopped state would need a pod restart
after a tenant re-subscribes, turning a commercial event into an operational one.

The rejection is explicit, never inferred. An agent guessing revocation from an
HTTP status cannot tell "your enrolment ended" from "a proxy in front of the
endpoint is misconfigured", and those want opposite responses.

**It does not uninstall itself.** An agent that removed its own manifests on a
rejection would be the platform mutating the cluster by proxy, and it would fight
the tenant's ArgoCD, which still declares it. Neither Landscape nor
`insights-client` does this — unregistration is the host's own action
(`insights-client --unregister`), never the vendor's.

**7. Re-enrolment creates a new identity; the old credential is never revived.**
Restoring a subscription re-adds the enrolment entry, the tenant sets
`capabilities.support.enabled: true`, ArgoCD installs the agent, and a **new**
certificate is issued under a new key. Nothing is recovered and nothing is
migrated, because the agent holds nothing durable — it is a sampler, not a store.
Reviving a retired credential would reintroduce exactly the material a
cancellation period existed to age out.

**And a CA change is a new enrolment, not an update to an existing one.** Stated
here as well as in the enrolment contract because it is the rule most likely to be
softened for convenience: updating the CA on a live record would let anyone able
to reach the registration endpoint repoint an already-trusted enrolment at a CA
they control, and every certificate from it would then be accepted under a
relationship the tenant established for something else. New CA, new record; the
old one stops being trusted when it is explicitly revoked.

**8. Cancellation cannot affect operation.**
Stated as an invariant because it is the one most likely to erode through a
plausible-sounding feature request:

- No workload, reconciliation loop, capability or cluster operation stops,
  degrades, or changes.
- Nothing is removed from the tenant's cluster by any platform component.
- No capability, artefact or version the tenant already holds is withdrawn.
  ADR-065 leaves them a working box; ending support does not take it back.
- `preview` keeps working. A tenant who has just cancelled is entitled to read
  what was being sent, and that is when they are most likely to want to.

What lapses is the platform's obligation and the evidence it rested on — which is
what ADR-069 already says in terms of versions.

### The enrolment contract

The trust model above says what enrolment establishes. This says what the
transaction *is*, because it is architectural: it decides who can create a
support relationship, who can end one, and what a CA rotation does. Leaving it to
implementation is how a registration endpoint ends up accepting a CA from anyone
who can reach it.

This does not design the Support Plane. It fixes the contract that plane must
satisfy.

**Authenticated principal.** A tenant's SOLOZ support account, authenticated the
same way it is to buy or manage a subscription. **Not** the cluster, and not any
material being registered — that is the circularity named above. A cluster has no
identity at the platform until this transaction gives it one.

**Operation.** `register cluster`, against an authenticated account.

**Inputs.**

| field | | notes |
|---|---|---|
| tenant id | required | the account the subscription belongs to |
| cluster id | required | unique within the tenant; the box's own identifier |
| CA certificate | required | public material, PEM. The tenant's Fleet Intermediate, or the Root above it |
| expected subject | required | exactly what the agent's certificate will carry, e.g. `support-agent:dev.acme.example` |

**Uniqueness.** One `ACTIVE` enrolment per `(tenant, cluster)`. Registering a
cluster that already has one is not a second record — see re-registration below.
The `(CA, subject)` pair must not be `ACTIVE` for a different tenant; a subject
naming a DNS zone the registering tenant does not hold is refused, because
otherwise one tenant can register an expectation about another's box.

**Who can create.** The authenticated tenant, for their own clusters, while their
subscription entitles them to support. Nobody else — including SOLOZ operators
acting alone, because an enrolment nobody's tenant asked for is a support
relationship nobody agreed to.

**Who can revoke.** Either side, independently:

- the platform, on cancellation or for cause;
- the tenant, by asking, which must be available as a first-class operation and
  not a support ticket. A tenant who wants to stop being observed should not have
  to ask permission to stop being observed.

Revocation sets the record to `REVOKED`. It never deletes it, because the record
is what makes a later re-registration recognisable as a re-registration.

**Idempotency.** Re-registering identical inputs is a no-op returning the existing
`ACTIVE` record. A tenant whose automation retries, or who runs the operation
twice, does not end up with two enrolments or a rotated identity.

**Re-registration with different inputs is a NEW enrolment, never an update.**
This is the rule that matters most, and it is stated as a prohibition because the
convenient implementation is the dangerous one:

> **A change of CA or subject MUST create a new enrolment record. It MUST NOT
> update the CA or subject of an existing one.**

An update would make CA rotation an impersonation mechanism. Anyone who could
reach the registration endpoint with the tenant's credentials could silently
repoint an existing, already-trusted enrolment at a CA they control, and every
subsequent certificate from it would be accepted under a relationship the tenant
established for something else. Creating a new record instead means the old CA
stops being trusted only when its record is explicitly revoked, the change is
visible as an event rather than a mutation, and both records are auditable.

The consequence for legitimate CA rotation is small and deliberate: a tenant
rotating their Fleet CA registers the new one, confirms the agent is reporting
under it, and revokes the old record. Two acts instead of one, in exchange for a
rotation that cannot be an impersonation.

**What is stored.** `(tenant, cluster, CA certificate, expected subject, state,
created, revoked)`. No key material, on either side. The CA certificate is public
and the rest is metadata.

### Five authorities, and none of them can overrule another

The lifecycle works because no single party holds two of these. Written out
because every failure mode discussed above is a case of one of them being
mistaken for another.

| # | Question | Authority | Expressed as | A tenant who disagrees |
|---|---|---|---|---|
| 1 | What evidence **may** be collected? | The platform | `support-agent-allowlist`, shipped in the bundle, reconciled with `selfHeal` | Declines the bundle version (ADR-064). Cannot edit it in place. |
| 2 | What evidence **this tenant allows**? | The tenant | `support-agent-enrolment` ConfigMap, tenant-owned, never shipped or reconciled by the platform | Edits it. The platform never sees or overwrites it. |
| 3 | Whether the platform **accepts** this agent | The Support Plane | enrolment state `ACTIVE`/`REVOKED`, held entirely platform-side | Cannot change it. This is the commercial relationship. |
| 4 | Whether the agent is **installed** | The tenant | `capabilities.support.enabled` in their own values, reconciled by their own ArgoCD | Sets it false. No platform component can install or remove it. |
| 5 | Whether the certificate is **valid** | The tenant's PKI | cert-manager against `infisical-fleet-issuer`, in the tenant's box | Stops renewing. The platform holds no issuer here. |

Effective collection is **1 minus 2**, computed in the cluster. Effective
reporting additionally requires 3, 4 and 5 to hold simultaneously, and each can
fail independently without the others noticing.

Two consequences are worth stating because they are the whole architecture in
miniature:

**The platform can stop standing behind a tenant at any moment, by changing 3
alone, and touches nothing in their cluster doing it.**

**The platform can never stop the tenant's software from running, because it
holds none of 2, 4 or 5** — and 1, the only one it does hold, reaches a cluster
only through a pull request the tenant merges.

An earlier draft blurred 1 and 2 by reading the tenant's denylist from the
platform-owned ConfigMap, which made the tenant's authority revert on the next
sync. It blurred 3 and 5 by expressing cancellation as a certificate fingerprint
block, which a routine renewal would have undone. Both are corrected above, and
both were the same mistake: one authority being used to express another's
decision.

### Initial enrolment, and why it cannot be authenticated by the certificate

Removing the enrolment token removed a credential; it did not remove the need to
establish trust the first time. ADR-077 has to say how a tenant's CA reaches the
Support Plane, because getting this wrong is a bootstrap circularity:

> *"Trust this CA because the certificate it signed proves it is trusted."*

That is not a chain, it is a loop, and it accepts anybody who brings their own CA.

**The initial enrolment is authenticated by the tenant's commercial relationship
with the platform, never by the material being enrolled.**

```
  subscription purchased
          ↓
  tenant authenticates to SOLOZ            ← the account, not the cluster
          ↓
  tenant registers:  tenant CA certificate (public material)
                     cluster identifier
                     expected agent subject
          ↓
  Support Plane records enrolment = ACTIVE
          ↓
  tenant creates the Certificate and the enrolment ConfigMap in their cluster
          ↓
  agent → mTLS → Support Plane            ← now, and only now, the cert authenticates
```

Three properties make this safe to do over an ordinary authenticated channel:

**The CA certificate is public material.** It is the thing a CA exists to
publish. Handing it to the platform discloses nothing, which is why this step
needs no secret and no out-of-band ceremony — it is the opposite of a token, and
losing it in transit costs nothing.

**The authenticating act is the subscription, not the cluster.** A tenant proves
who they are the same way they do to buy support in the first place. The cluster
proves nothing at this stage and is not asked to.

**Registration binds three things together**: the CA, the cluster identifier, and
the expected subject. A certificate from a registered CA bearing an unexpected
subject is rejected, so a tenant's CA compromise does not become an unbounded
ability to impersonate any of their clusters — only the one whose subject was
registered.

What the platform stores is a list of `(tenant, cluster, CA, expected subject,
state)`. There is no key material in it, on either side.

### Resolved: the Root CA is the tenant's, and nothing leaves the box

ADR-035 describes an offline Root CA as the "offline **platform** trust anchor",
signing the Fleet Intermediate CA that issues every certificate in the fleet. That
text predates ADR-065 and reads ambiguously now: "platform" there means **the
tenant's platform**, the thing that ships into their box. It does not mean SOLOZ.

**Settled: the Root CA, the Fleet Intermediate CA and every leaf are the
tenant's, inside the tenant's box. SOLOZ holds no issuing authority over any
tenant's PKI and is not in any tenant's trust chain.** ADR-035 should say
"tenant platform" where it says "platform", and the reader who hits this after
ADR-065 should not have to work it out.

Two consequences follow, and both are load-bearing here.

**The enrolment CA registration above is required, not optional.** SOLOZ cannot
validate the agent's certificate by chaining to anything it already holds, because
it holds nothing. It must be told which CA and which subject to trust, by an
authenticated tenant, before any certificate means anything.

**Enrolment is a mutual exchange of public material.** It was described above as
one-directional and it is not:

```
  tenant  ──►  SOLOZ     tenant CA certificate
                         cluster identifier
                         expected agent subject

  SOLOZ   ──►  tenant    Support Plane endpoint
                         Support Plane CA certificate
```

Nothing in either direction is secret. The tenant's CA certificate is what a CA
exists to publish, and the Support Plane's CA is the same on our side. Each side
ends up able to authenticate the other, and neither has handed over a credential.

**The agent must validate the Support Plane against the Support Plane's CA, and
this was implemented wrongly at first.** The agent's own Secret carries a `ca.crt`
— cert-manager puts the *issuing* chain there, which is the tenant's fleet CA. It
signs the agent and says nothing about who may terminate the other end.
Validating the platform's server certificate against it fails every time, and the
tempting fix is to stop verifying, which would let anything that can answer DNS
receive a box's evidence. The Support Plane's CA therefore arrives in the
tenant-owned `support-agent-enrolment` ConfigMap, beside the endpoint, because
the two are handed over together and are useless apart.

**Blast radius, stated plainly.** A tenant's Fleet CA compromise lets an attacker
mint a certificate bearing the registered subject, and the Support Plane would
accept it. Binding registration to CA + cluster + subject bounds that to the
clusters actually enrolled; it does not prevent it. The containment is the same
as for everything else that CA signs — ADR-035's Compromise Response Matrix calls
for an immediate CA replacement ceremony — and at that point the tenant's whole
fleet is compromised, so the support channel is not the weakest thing in the
room. Because the Root is per tenant, this is bounded to one tenant, which is the
property the shared-root alternative would have lost.

### The state machine

**Two distinct state spaces, and conflating them is the most likely misreading.**

| | Held by | Values |
|---|---|---|
| **Enrolment state** | the Support Plane, platform-side | `ACTIVE` \| `REVOKED` |
| **Agent operational state** | the agent, in the cluster | `NOT_ENROLLED` \| `REPORTING` |

`REVOKED` is **not an agent state.** It is the platform's record of a commercial
decision. What the agent does in response is return to `NOT_ENROLLED`, which is
the same operational state as a box that never enrolled — that is the whole point
of decision 6, and it is why there is no third agent state to reason about.

The diagram below shows the enrolment record's lifecycle, with the agent's
operational state noted where the two differ.

```
        ┌──────────────┐
        │ NOT ENROLLED │◄────────────────────────┐
        └──────┬───────┘                         │
               │ tenant supplies CA + subject;   │
               │ platform records enrolment;     │
               │ tenant creates Certificate      │
               ▼                                 │
        ┌──────────────┐                         │
        │    ACTIVE    │                         │ new enrolment,
        └──────┬───────┘                         │ new identity,
               │ subscription cancelled;         │ new certificate
               │ platform revokes acceptance     │
               ▼                                 │
        ┌──────────────┐                         │
        │   REVOKED    │  agent installed and    │
        │              │  running, inert, and    │
        │              │  behaviourally identical│
        │              │  to NOT ENROLLED        │
        └──────┬───────┘                         │
               │ tenant sets                     │
               │ capabilities.support.enabled    │
               │   = false                       │
               ▼                                 │
        ┌──────────────┐                         │
        │   REMOVED    │─────────────────────────┘
        └──────────────┘
```

**No arrow crosses from the platform into the cluster**, which is the property to
check the diagram against. But the transitions do not partition neatly into "the
platform's" and "the tenant's", and an earlier draft claimed they did:

- `NOT ENROLLED → ACTIVE` requires **both**. The tenant authenticates and
  registers; the platform accepts and records. Neither can do it alone, which is
  what makes enrolment a relationship rather than a grant.
- `ACTIVE → REVOKED` is the platform alone, on its own side.
- `REVOKED → REMOVED` is the tenant alone, in their own cluster.
- Re-enrolment requires both again, for the same reason as the first. A
tenant who cancels and changes nothing sits in `REVOKED` indefinitely, running an
inert agent, with everything else working exactly as before. That is a supported
resting state and not a fault.

### Lifecycle provenance

Where every part of the lifecycle above comes from, so that a future reader
hitting a problem can go read the thing it was taken from rather than re-deriving
the reasoning or guessing at intent.

An earlier draft of this addendum said the reference projects "do not specify a
subscription-to-agent lifecycle". **That was under-researched and is withdrawn.**
Between them they settle most of it.

Paths are relative to `reference-projects/`. Each names the file where the
behaviour was *verified*, with the symbol or line, not where it is described.

| # | Decision | Source | Verified at | What was taken | Why |
|---|---|---|---|---|---|
| 1 | Support is a selectable capability | *(none — ADR-066)* | — | — | Our own capability model. No reference project runs a GitOps-declared agent, so this has no external source and is derived from ADR-066. |
| 2 | Subscription state never mutates a tenant cluster | `rhsm-subscriptions` | `docs/container-subscription-sync.puml` | Subscription and offering data sync from internal business systems into the vendor's own plane; every arrow terminates vendor-side | Proves entitlement can be fully managed without any path to a customer machine. Confirms ADR-065's limit is not a handicap — Red Hat runs a subscription business this way. |
| 3 | Per-certificate revocation (compromise) | `argocd-agent` | `internal/blocklist/blocklist.go:59` (`Contains`), `internal/tlsutil/tlsutil.go:141` (`CertificateFingerprint`) | Rejection by SHA-256 certificate fingerprint, held in a ConfigMap the principal watches | Revocation with no CRL, no OCSP and no call into the client — what ADR-032 already requires of this platform's PKI. |
| 3 | Enrolment-level revocation (cancellation) | *(none)* | — | — | **No source.** argocd-agent has one trust domain and one operator, so entitlement never arises there; `rhsm-subscriptions` holds entitlement but has no agent to revoke. The separation of enrolment state from certificate validity is ours, and it exists because the tenant's own PKI can mint a valid certificate at any time. |
| 4 | Removing the agent is a tenant GitOps operation | `insights-client` | `docs/insights-client.8:17` (`--unregister`) | Unregistration is the **host's** action; the vendor has no uninstall path anywhere in the client | Establishes that vendor-initiated removal is not needed for a working commercial agent. Ours substitutes GitOps for the CLI flag. |
| 4 | *(shape confirmation only)* | `cluster-monitoring-operator` | `pkg/tasks/telemeter.go:213` (`destroy`), `:229`–`:309` | When disabled, the operator deletes its own Deployment, Secret, Service, ClusterRole, ServiceAccount, NetworkPolicy and ConfigMap — from the cluster's own config | Shows "disabled means removed, by the cluster itself" is workable at scale. **Pattern only.** Decision 4 follows from ADR-065, not from Red Hat. |
| 5 | Short-lived identity, no durable platform secret | `argocd-agent` | `internal/auth/mtls/mtls.go:87` (identity from cert), `cmd/ctl/agent.go:89` (`GenerateClientCertificate`), `docs/configuration/authentication.md` | Identity carried in the client certificate and extracted by regex; userpass is marked *Development only* and deprecated | There is no token in the recommended path of a mature agent-to-principal system. Confirms a bespoke token exchange would be an invention, not a necessity. |
| 5 | Expiry ≠ revocation | *(ours — ADR-032, ADR-035)* | — | — | Our own PKI already relies on short TTL in place of CRL/OCSP. What the references do **not** separate is cryptographic validity from commercial authorisation; that distinction is ours and is the reason the Support Plane holds enrolment state. |
| 6 | Revoked → return to unenrolled | `landscape-client` | `landscape/client/broker/registration.py:290` (`_handle_unknown_id`) | Server states in-band that it no longer knows the client; client clears `secure_id`/`insecure_id` and falls back to unregistered. Never halts, never self-removes | Replaces an earlier custom "stopped after N retries" state. Removes a state from the machine, and lets re-enrolment be picked up without a pod restart. |
| 6 | Enrolment is one act against one endpoint | `landscape-client` | `example.conf:35` (`url`), `:111` (`account_name`), `:116` (`registration_key`) | One credential, one destination, revocable | The shape of enrolment: one thing to supply, one thing to undo. |
| 7 | Re-enrolment creates a new identity | `landscape-client` | `landscape/client/broker/registration.py:139` (`register` clears both ids before re-registering) | Registration always starts from cleared credentials; the old id is never revived | Prevents a retired credential returning after a cancellation period that existed to age it out. |
| 8 | `preview` survives cancellation | `insights-client` | `docs/insights-client.8:78` (`--status` reads local `.registered`), `--no-upload` | Registration status is local state the customer reads; payload inspectable without uploading | A tenant who has just cancelled is entitled to see what was being sent. Keeping this working costs nothing and removes the only reason to distrust the claim. |

### What has no reference behind it

**Registering the tenant's CA.** None of these projects faces it, because in every
one of them the vendor is the root of the identity: Landscape enrols with a
vendor-held account key, `insights-client` against a Red Hat account, `telemeter`
with a vendor-issued token, and `argocd-agent`'s principal signs the agent
certificates it later validates (`cmd/ctl/agent.go:89`).

Here the tenant's own PKI is the root (ADR-031, ADR-035), and the platform must
validate a certificate signed by a CA it did not issue. That is the one genuinely
new part of this lifecycle, and it is deliberately the smallest: **a list of
trusted CAs and subjects, consulted at the handshake.** Everything around it is
running in production somewhere else.

If this proves wrong in practice, that list is the thing to re-examine first —
not the state machine, and not the division of cancellation into two halves, both
of which have working implementations behind them.

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

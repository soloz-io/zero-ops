# ADR-066: The Platform Boundary

**Date:** 2026-09-07
**Status:** Proposed

## Context

ADR-065 places a platform inside every tenant's box and makes maintaining it the service the platform sells. It does not say what "it" contains, and the platform currently draws no line: `manifests/hub-core-services/` holds thirty-one components reconciled uniformly, spanning cluster machinery, databases, an identity provider, a message bus, an analytics store, a mail server and a metering service.

Four facts bear on the boundary.

**The tenant has no platform engineers.** ADR-065's arrangement exists for organisations that want a golden path and cannot staff one. A boundary that hands back a component such an organisation cannot operate does not reduce the platform's obligation; it relocates the obligation to the party least able to meet it, and the component fails there.

**Two questions are being asked at once.** Whether every box runs a component, and who maintains it when a box does, are independent. A database is not run by every tenant and is unoperatable by any of them, so the answers differ; treating them as one question forces a component to be either universal or unsupported.

**A tenant's application is genuinely not the platform's.** Its code, its images, its schema and its data are authored, deployed and understood by the tenant. Nothing the platform knows would let it maintain them, and ADR-062 already places them in repositories the platform holds no access to.

**On-premises capacity is part of the topology.** ADR-046 provisions home workers on an immutable operating system and ADR-052 bursts to cloud capacity above them. Maintenance effort on those nodes scales with how much state they are asked to hold.

## Decision

**The platform is the set of capabilities it provides and maintains, on which a tenant's applications execute. The tenant owns the application logic and the use it makes of those capabilities.**

The definition is deliberately not "everything a tenant's application runs on". Taken literally that reaches the cloud provider, the region, the network, the firmware and the hardware, and it would make the platform answerable for things it neither ships nor can reach. What the platform maintains is what it provides.

### The test is what authored it, not what it does

A component belongs to the platform if the tenant's own logic is not inside it. A database engine, an identity provider, a message bus, a gateway, a certificate authority and a metrics store are all platform: their behaviour is determined by the platform that ships them and the configuration it defines. The schemas in that database, the organisations in that identity provider, the routes through that gateway and the images behind them are the tenant's.

The line falls between a capability and the use made of it. The platform maintains the capability; the tenant owns the use.

**Which side a component sits on has nothing to do with who wrote it.** A third-party database engine the platform ships and maintains is platform; an application the platform's own team once wrote for a tenant would not be.

### Required and selectable are a different question from platform and tenant

Every box runs the same cluster machinery: the GitOps engine, composition and cluster lifecycle, secret delivery, certificate issuance, DNS, gateway, admission policy, and node and capacity lifecycle. Without these a box cannot reconcile anything, so they are not selectable.

Above them sit capabilities a tenant selects: a database, an identity provider, messaging, object storage, mail, metrics. A tenant runs what it needs and does not carry the rest.

**Selecting a capability does not make it the tenant's to maintain.** Both sets are platform, and the platform maintains what a box actually runs. A tenant that selects a database receives a maintained database, which is the whole proposition: the alternative asks an organisation with no platform engineers to operate a Postgres cluster, and the component fails in their hands.

The two questions therefore have separate answers. Required against selectable decides what a box costs. Platform against tenant decides who is answerable when it breaks.

### The platform owns capability health; the tenant owns capability usage

Maintenance means the capability is available, current, recoverable and correct as shipped: the operator running, the version supported, backups taken and restorable, failover working, storage able to grow, the component patched. That is the platform's, on every box that runs it.

What a tenant does with the capability is the tenant's: a query that will not terminate, a schema that will not migrate, an OAuth client pointed at the wrong callback, a topic nobody consumes. The platform does not tune them, and an incident caused by one is not a platform incident.

The distinction is what makes the obligation answerable at three in the morning. A Postgres operator that has stopped reconciling is the platform's. A slow query against a healthy Postgres is the tenant's. A Zitadel that will not start is the platform's; a misconfigured client registered in a healthy Zitadel is the tenant's.

It is also the boundary most likely to be argued, because a tenant's usage can make a healthy capability behave as an unhealthy one. Where the capability is functioning as shipped, the platform's obligation is to say so and to help — not to own the outcome.

### A capability is offered once and consumed many times

A selected capability is provisioned once per box and used as often as the tenant needs. A tenant with a database creates the databases its applications require; a tenant with an identity provider registers the applications it runs. The platform maintains the capability and does not count or approve the uses.

This is what lets the box adapt without the platform being consulted, and it is the difference between a golden path and a menu.

### The catalogue is how a capability is added

Capabilities are declared in a catalogue the bundle carries, and a tenant selects from it in the values its cluster supplies (ADR-063). Selection is a value and never a version, so adopting a capability and upgrading a bundle are independent acts and neither blocks the other.

A capability enters the catalogue by being packaged and versioned with the bundle, and certified against it: tested with the machinery at that bundle version, not against every other capability. The machinery is one tested substrate; each capability is certified against that substrate and declares what it requires of it.

Testing every combination is not possible and is not what a bundle version claims. Ten independently selectable capabilities are a thousand combinations, and a claim to have exercised them all would be false in a way nobody could check. What is claimed is narrower and true: this substrate was tested, and each capability was tested on it.

Where two capabilities genuinely interact -- a database and the metering that reads it -- the interaction is itself declared and certified, so a pair that must be tested together is named rather than assumed.

### A capability has a lifecycle, and leaving the catalogue is part of it

A capability moves through declared states: **catalogued** and installable, **supported** and maintained on every box running it, **deprecated** and still maintained but closed to new selection, **end-of-life** with maintenance ending on a stated date, and **removed** from the catalogue.

Every state but the last carries maintenance. Deprecation is an announcement with a date, not a withdrawal, and a tenant running a deprecated capability keeps being maintained until that date passes.

Without this, "nothing reaches a tenant's box that the platform is not prepared to maintain there" is a promise with no end, and every capability ever catalogued would have to be maintained forever. The economics of this platform are decided by what enters the catalogue and what is allowed to leave it, so both are decisions rather than events.

What the platform owes a tenant whose capability reaches end-of-life -- a migration path, a supported successor, or notice alone -- is a support-contract question this ADR does not settle.

### On-premises capacity carries workloads, not cluster state

Cluster-machinery state is not placed on on-premises nodes. Those nodes carry capabilities and burst capacity, so a node can be replaced without the ceremony reconciling cluster state would require, which is the property ADR-046's immutable operating system already assumes.

### Alternatives considered

**Draw the line at the control plane: cluster machinery is maintained, everything above it is the tenant's.** Rejected, and it was this ADR's first form. It produces a defensible boundary for a platform sold to platform engineers and the wrong one here: ADR-065's tenant has none, so a database or an identity provider handed back is a component nobody maintains. It also contradicts what the platform is sold as — a box a startup runs its business on, not a cluster it must then furnish.

**Maintain the tenant's applications too.** Rejected. Their logic, images and data are the tenant's, the platform holds no access to the repositories carrying them (ADR-062), and nothing the platform knows would let it maintain them.

**Let each tenant draw its own boundary.** Rejected. A support obligation that varies per tenant cannot be stated once, and the uniformity of the platform is what makes a single tested bundle meaningful under ADR-063.

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Cluster machinery | `zero-ops`, published as charts | Platform | ArgoCD | Tenant control plane | Day-1+ |
| Selectable capabilities | `zero-ops`, published as charts | Platform | ArgoCD | Tenant control plane | Day-1+ |
| Capability selection | `<tenant>-gitops` values | Tenant | ArgoCD | Bundle chart | Day-1+ |
| Use made of a capability | tenant | Tenant | — | Tenant | Day-1+ |
| Application logic and images | tenant workload repositories | Tenant | ArgoCD | Tenant | Day-1+ |

See ADR-039 for the complete ownership matrix.

## Consequences

### Positive

A tenant with no platform engineers receives a database, an identity provider and a gateway that someone maintains, which is what ADR-065's arrangement is for.

What maintenance covers is derivable from the boundary rather than negotiated per component and per tenant.

A tenant pays for the capabilities it runs and not for the ones it does not, while still being maintained on everything it does run.

A capability is provisioned once and used freely, so a box adapts to what a tenant builds without the platform being asked.

On-premises nodes hold no cluster-machinery state, so replacing one is a capacity operation.

### Negative

The platform is answerable for a large surface: a database engine, an identity provider, a message bus and a mail server are each an operational speciality, and maintaining them across every tenant's box is the cost of the proposition rather than an incidental one.

The floor price is not bounded by cluster machinery. A box costs its machinery plus every capability the tenant selected, and the selectable half is where the weight sits — a Postgres cluster and an identity provider are not free. Whether that floor is low enough for the organisations ADR-065 describes is an open question this ADR does not answer, and it is the one most likely to invalidate the arrangement.

The boundary runs through some components rather than between them. An identity provider serves both the platform's own access and the tenant's end users; a metrics store holds both. Each such component needs its two sides named, and the second side is the tenant's data with the retention and disclosure obligations that follow.

Every capability added to the catalogue is a permanent maintenance obligation across every box that selects it. Adding one is a decision about the platform's cost base, not a feature.

## Impact

- **Amends ADR-065.** What ships into a box is the cluster machinery plus the capabilities a tenant selects, and the platform maintains both.
- **Amends ADR-063.** A bundle is the tested set of the machinery and every catalogue capability; a box runs the machinery and its selections.
- **Confirms ADR-062.** Application logic stays in tenant-owned repositories the platform holds no access to, which is the same line drawn from the other side.
- **Confirms ADR-046 and ADR-052.** On-premises nodes carry capabilities and burst capacity.
- **Constrains ADR-013.** Observability spans the boundary, so its components are classified explicitly rather than by location.

## References

- ADR-013: Hub-Spoke Observability Architecture with Dual Collection Patterns
- ADR-039: Platform Ownership Model
- ADR-046: Hybrid Provider Home Worker
- ADR-052: Elastic Burst Capacity for Tenant Workloads
- ADR-062: Onboarding and Scaffolding a Tenant
- ADR-063: The Platform Bundle and its Version
- ADR-065: The Control Plane Ships Into the Box

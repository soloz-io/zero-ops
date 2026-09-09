# Zero-Ops — StartupOS

**We maintain the platform your team builds on.**

Zero-Ops is a complete, open-source infrastructure stack for software startups —
compute and cluster lifecycle, GitOps, identity, secrets, databases, gateway,
observability — packaged as one tested unit that runs on affordable
infrastructure instead of a hyperscaler.

The software is free. What is sold is maintenance: keeping the stack current,
patched and upgradeable without breaking what is running on it.

## Who it is for

Funded startups saving runway from hyperscaler pricing, who want a golden path
defined and maintained for them, and cannot staff a platform engineering team to
do it. Zero-Ops is platform engineering as a service: the golden path arrives
tested, and stays maintained, without the team it would otherwise take.

This is the opposite end of the market from tools that hand a platform team a
faster start and room to customise. A tenant here inherits a tested set rather
than a starting point to diverge from — see `docs/adr/071-how-this-platform-differs-from-kubefirst.md`.

## The box

A customer is a **tenant** and holds one **box**: a control plane plus that
tenant's own spoke clusters.

- **Bring your own cloud.** Clusters run in the tenant's account, on the
  tenant's bill. Zero-Ops resells no compute.
- **The control plane ships into the box.** It runs in the tenant's cloud under
  credentials the tenant holds. Zero-Ops operates no tenant infrastructure and
  holds no cloud, cluster or secret credential.
- **A spoke cluster belongs to exactly one tenant** and is the unit of
  maintenance, versioning and metering.
- **A cell is a label**, grouping a tenant's clusters to attach policy —
  residency, compliance, or anything else a tenant needs applied to a subset. It
  holds no compute and pins no version.

## How maintenance works

The platform's authority ends at a pull request.

Zero-Ops publishes a **bundle**: the complete platform at one tested revision,
named by a tag. When a bundle is published, the platform opens a pull request
against the tenant's own infrastructure repository advancing the clusters that
are due it — the shape of a dependency-update bot. Access is granted by the
tenant through an App installation and is revocable.

Nothing is applied by the platform. A change reaches infrastructure when the
tenant accepts the pull request and the tenant's own control plane reconciles
it. Tenants may configure automatic acceptance.

Support extends as far as the telemetry a box exports. Telemetry is egress-only
and covers control-plane state, never workload data. An upgrade proposal carries
a pre-flight verdict produced inside the box, or is raised as unverified.

## What the platform is, and what the tenant owns

The platform is the set of capabilities Zero-Ops provides and maintains, on
which a tenant's applications execute. The tenant owns the application logic and
the use it makes of those capabilities.

The line is what authored it, not what it does. A database engine, an identity
provider, a message bus, a gateway and a certificate authority are platform:
their behaviour is determined by the platform that ships them. The schemas in
that database, the organisations in that identity provider, the routes through
that gateway and the images behind them are the tenant's.

Required and selectable is a separate question. Every box runs the same cluster
machinery — GitOps engine, composition and cluster lifecycle, secret delivery,
certificate issuance, DNS, gateway, admission policy, capacity lifecycle —
because without it a box cannot reconcile anything. Above that sit capabilities
a tenant selects: a database, an identity provider, messaging, object storage,
metrics.

**Selecting is enabling, not acquiring.** There is no catalog to choose from and
nothing to install. Every capability the bundle provides ships to every tenant,
and a tenant turns on what it runs. A capability left off is still maintained:
it advances with every bundle, and turning it on later needs no action by
Zero-Ops.

## Exit

The software is open source, the control plane is already in the tenant's
account, and the compute is already on the tenant's bill. Ending the
relationship is revoking an App installation. Nothing is withdrawn, because
nothing was held.

## Repository layout

```
cmd/                 hub, kube-sbt, auth-proxy, mcp-server
internal/            private packages behind those binaries
manifests/           the platform: boundaries, components, compositions, charts
docs/adr/            architecture decision records
docs/runbooks/       operational procedures
scripts/validate/    pre-commit and cluster validators
```

Instances live outside this repository: cluster and cell declarations in a
tenant's infrastructure repository, applications in a tenant's workload
repositories. This repository holds types, never instances.

## Development principles

Contributions must hold to these:

- **Production-ready and idiomatic.** Enterprise-grade, widely adopted
  approaches. Not experimental ones.
- **Strict ADR alignment.** Every solution references the ADRs it rests on.
- **Justified deviations.** Departing from an ADR requires the argument and a
  proposed ADR update, in the change.
- **GitOps first.** Imperative cluster mutation is prohibited. Fix the
  manifest and let it reconcile; never leave a fix as a command.

## Where to start

- `docs/adr/` — the architecture, decision by decision. ADR-062 through ADR-071
  define the tenant model, the bundle and its version, promotion, the platform
  boundary, the basis of support, the maintenance promise, and how this platform
  differs from kubefirst.
- `docs/runbooks/bootstrap-and-binaries.md` — bootstrapping a hub, the binaries,
  teardown and state management.

## License

Not yet declared. See `docs/adr/` for the intended model; a licence file is
required before the "open source" claim above is true in any legal sense.

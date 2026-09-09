# ADR-071: How This Platform Differs From kubefirst

**Date:** 2026-09-09
**Status:** Proposed

## Context

kubefirst is the closest existing system to this one. It provisions a management cluster, installs a GitOps stack into it, creates repositories in the customer's own git organisation, renders a template into them, and hands the result over. Anyone evaluating this platform who knows kubefirst will ask what is different and why, and anyone building here who reads its source will find patterns worth taking and patterns that look like omissions here but are correct there.

Those answers are currently spread across ADR-062, ADR-063, ADR-065 and ADR-068, each stating one difference where it happens to be relevant. Reconstructing the whole comparison means reading four documents and inferring the rest, which is how a decision gets re-argued from scratch or a rejected pattern gets reintroduced because nothing recorded that it was considered.

Two things make the comparison worth writing down rather than answering when asked. The resemblance is real at the level of mechanism -- both provision a cluster, install a GitOps stack, and render repositories into a customer's git organisation, and several of the mechanisms here were taken from theirs deliberately. And the differences are not preferences. Each follows from a decision recorded elsewhere, so a difference that cannot be traced to one is either an accident or a decision nobody made.

The resemblance stops at the customer, and that is where the comparison has to start.

This ADR decides nothing on its own. It is the single place the comparison is stated, and it is wrong the moment a decision it cites changes.

## Decision

**This is the reference for how the platform compares to kubefirst. Every difference below names the decision it follows from, and a difference that cannot name one is a defect in this document or in the platform.**

### What was taken from it

These are adoptions, not coincidences. Each was examined in its source and taken because it was right.

**The version is injected into the binary at build time.** kubefirst sets `-X ...configs.K1Version` at release and treats an uninjected build as a development build. ADR-068 adopts the mechanism and the sentinel unchanged, because a build that derives its own version at runtime can be made to lie about it by the environment it runs in.

**Scaffolding prunes, flattens, detokenizes and discards history.** The rendered repository is not a fork of the template: the upstream `.git` is deleted, every provider but the selected one is removed, the survivor is flattened to the repository root, and tokens are substituted throughout. ADR-062 follows the same sequence.

**Chart bodies are never copied into the customer's repository.** What lands there references upstream registries by version. This platform does the same for the same reason: content copied into a tenant's repository is content the platform can no longer correct.

**Secrets never enter git.** kubefirst writes them to Vault before rendering anything, and its catalog definitions declare which keys are secret so the API can refuse a request missing one. ADR-003 requires the same shape here through Infisical and ESO.

### The customers are different, and every difference below follows from that

**kubefirst is for an organisation that has a platform engineering team and wants a faster start. This platform is for an organisation that has no platform engineering team and cannot acquire one.**

kubefirst's customer can operate what it is handed. It has people who read the rendered repository, change what does not suit them, pin a component to a version they prefer, and perform their own upgrades. What that customer wants from a vendor is a well-made starting point and then room to work: the handover is the product, and customisation after handover is expected rather than tolerated.

This platform's customer cannot do any of that, and its inability is the reason the platform exists. It wants a golden path defined by someone else, tested before it arrives, and maintained afterwards without staffing the maintenance. The bundle is not a starting point it will diverge from; it is the thing it runs, and continues to run, because there is nobody to make it otherwise. What this platform sells is platform engineering as a service to organisations that cannot afford a platform engineering team.

The two positions want opposite things from the same architecture. One wants surface to customise; the other wants a tested set it does not have to reason about. Read that way, the differences in this document stop being a list of divergences and become one difference applied consistently:

- Thirty independently pinned components are customisation surface for a team that will use it, and thirty unreviewable decisions for a tenant that will not.
- One repository holding template and instances is convenient for a customer who will edit both, and a boundary violation where the platform maintains one side.
- A push straight to the default branch is what a team operating its own console wants, and an authority no outside party should hold over a tenant that is not watching.
- No propagation is acceptable where the customer upgrades itself, and the removal of the service where it cannot.
- A catalog to select from serves a team that can evaluate the options, and presents a choice to a tenant that has nobody to make it.

Each of those is stated below with the decision it follows from. This section is why they all point the same way.

### What the tenant's repository holds, and why it follows

kubefirst's rendered repository holds one ArgoCD Application per component, each pinning its own upstream chart and version -- `chart: atlantis`, `targetRevision: 4.11.2`, and about thirty more. There is no version of the platform as a whole, because the platform as a whole is not a thing that has a version there.

Here a tenant's repository holds one bundle version and its values. The content behind that version is a published distribution (ADR-063), and the version is the unit that moves (ADR-064).

This is the customer difference made concrete. Thirty pins is a customisation surface, and a customer with a platform team will use it. A tenant without one would hold thirty decisions it has no way to make, and the first upgrade it declined in part would leave it running a combination nobody tested.

A promotion is a single field, so it is reviewable as one change and cannot half-apply. Thirty independent pins would make an upgrade thirty reviewable changes, each of which a tenant may take or leave, producing combinations that were never tested together -- which ADR-063 records as the failure mode the bundle exists to prevent. The certification claim depends on it: one substrate tested as a unit, with each capability tested against it, is only meaningful if a cluster runs the set that was tested.

### The platform proposes; the comparable platform pushes

kubefirst's API clones the customer's repository, renders a component into it, commits with a message naming the requesting user, and pushes to the default branch. The cluster reconciles moments later.

That is coherent there. The API runs inside the customer's own management cluster, under the customer's credentials, in response to a request from the customer's own console. The party pushing and the party owning the repository are the same, so the push executes an instruction its owner just gave.

Here the platform is a party outside the tenant. ADR-065 fixes what such a party may do -- it may propose, and may never be the authority that causes a change to take effect -- so ADR-062 requires every platform write into a tenant's repository to be a branch and a pull request. The difference is not about review as a practice. It is that a pull request leaves a decision point owned by the tenant and a push does not.

### There is propagation, and that is the service

kubefirst's published upgrade path is to reinstall the CLI. Nothing reaches a rendered repository after it is rendered. That is the cost of shipping the control plane and holding no customer credentials, and ADR-065 records it as a cost knowingly paid there.

This platform ships the control plane the same way and keeps a propagation mechanism, because maintaining the stack across a tenant's estate is what the platform is paid for (ADR-069). A published version raises a pull request against each subscribing tenant's repository, proposing the new pin.

The mechanism is Renovate, run by the platform through the tenant's App installation (ADR-064). It is not built here: a bundle version pinned in a tenant's file is a dependency, and the lifecycle around proposing one -- deduplication, scheduling, dashboards, configurable automerge -- is a solved problem whose reimplementation would be a second maintenance burden inside a product sold as maintenance.

### Repositories are separated; there, one repository is correct

kubefirst holds template and rendered instances in one repository. ADR-062 rejects that here and records that it is correct there: after the render the customer owns both, so no boundary runs through that repository and none is needed. Here the platform maintains the types and proposes to the instances, and a directory is not a unit at which access is granted or revoked.

### There is no catalog; the bundle is the offering

kubefirst maintains a separate `gitops-catalog` repository. A customer selects an application from it, supplies config and secret values, and the API renders it into the customer's registry directory. The catalog is curated by the vendor and is a second body of maintained content alongside the platform itself.

**This platform publishes no catalog. Every capability the bundle provides ships to every tenant, and a tenant chooses which of them to run by enabling or disabling them in its own values.**

The consequence is the point: a disabled capability is still maintained. It moves with the bundle, its version advances in every promotion whether or not the tenant runs it, and enabling it later requires no action by the platform and no proposal. A tenant turning something on gets the version it would have had if it had been running all along.

A catalog would create a third relationship the repository split does not name -- content the platform maintains but only some tenants run -- and with it a matrix of selected sets to certify rather than one bundle. It would also make "what is the platform" a question with a per-tenant answer, which ADR-066 settles deliberately in the other direction.

This is a decision for the current offering rather than a permanent exclusion. Should selectable content be introduced, it is a change to ADR-066's boundary and to ADR-063's certification claim, and this section is where the reversal is recorded.

### Workloads are the tenant's, and the platform has no mechanism for them

kubefirst's API writes both kinds of thing into the customer's repository: catalog applications, and the scaffolded example application it calls metaphor. Both land in the same registry directory, so the distinction between infrastructure the customer selected and code the customer wrote is a matter of provenance rather than of mechanism.

ADR-066 draws that line as a boundary. A capability is the platform's to provide and maintain; a workload is the tenant's, and the platform never writes one. There is therefore no equivalent here of the API call that adds an application, and the absence is the decision rather than a gap.

## Ownership

This ADR owns no platform resources. It records a comparison and the decisions the comparison follows from; each difference is owned by the ADR it cites. For resource ownership, see ADR-039.

## Consequences

### Positive

A comparison that was reconstructed from four documents is now stated once. Anyone asking how this platform differs has a single answer, and anyone reading kubefirst's source has a record of which of its patterns were adopted, which were rejected, and why a rejected pattern is nonetheless correct where it came from.

Requiring every difference to name a decision makes an unexplained difference visible. A difference with no citation is either an accident that should be removed or a decision that was never recorded, and both are worth finding.

Recording what was adopted is as useful as recording what was not. The version-injection mechanism and the scaffolding sequence were taken deliberately, and knowing that prevents them being re-derived or discarded as inherited accidents.

### Negative

This document is a description rather than a decision, so it goes stale silently. A decision changed elsewhere leaves this correct-sounding and wrong, and nothing fails when that happens.

It also fixes a comparison against a moving target. kubefirst continues to develop, and a difference recorded here may already have been closed on their side. Every statement about their behaviour is a statement about the revision examined, not a permanent property.

## Impact

None on running systems. This ADR adds no mechanism and changes no manifest.

Its effect is on how the comparison is answered. A question about kubefirst is answered from here rather than from memory, and a change to any decision it cites should be accompanied by a change here.

## References

- ADR-003: Secret Management Architecture
- ADR-039: Platform Ownership Model
- ADR-062: Onboarding and Scaffolding a Tenant
- ADR-063: The Platform Bundle and Its Version
- ADR-064: Bundle Promotion and Cell-Scoped Policy
- ADR-065: The Control Plane Ships Into the Box
- ADR-066: The Platform Boundary
- ADR-068: The Build Declares the Bundle Version
- ADR-069: The Maintenance Promise

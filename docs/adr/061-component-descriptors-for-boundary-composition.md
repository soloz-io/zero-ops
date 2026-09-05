# ADR-061: Component Descriptors for Boundary Composition

**Date:** 2026-09-05
**Status:** Accepted

## Context

ADR-021 divides platform delivery into independent boundaries. ADR-055 establishes that boundary content is complete at all times, rendered by a single seed Application, and that activation is separately owned as a per-boundary AppProject sync window.

Neither ADR states how the set of Applications within a boundary is expressed. In practice each boundary ApplicationSet carries its inventory as an inline list generator, with every component's repository, revision, path, destination namespace, sync wave and Helm values written as literal elements of that list. Boundary 01 is 672 lines for 16 components and boundary 03 is 893 lines for 24.

Four consequences follow from that placement, all observed:

A component's configuration is not adjacent to its content. Twenty-two of the twenty-four components in boundary 03 deploy a directory that already exists in the repository, sixteen of them one-per-component under `manifests/hub-core-services/`; the remaining two are third-party charts. The content is already organised per component; only the wiring is centralised.

Every component change edits one large shared file, which serialises unrelated work behind merge conflicts and makes review of a single component's change require reading an unrelated context.

Chart versions are not machine-discoverable. A component's `targetRevision` is a quoted string inside a list element rather than a field of a chart reference, so dependency automation cannot see it and pinned versions drift without notice.

The boundary templates are excluded from YAML linting, because the templates are Helm rather than YAML and their inline values are block scalars whose content the linter cannot interpret. A duplicate mapping key survived in boundary 01 as a result, silently discarding one of two declarations. Values expressed as files rather than as embedded scalars are lintable by the tooling the repository already runs.

A separate fact constrains the solution. ADR-047's tenant ApplicationSets already derive their inventory from a Git generator, and the failure mode of a generator that returns nothing is silent: the ApplicationSet produces zero Applications and reports healthy, indistinguishable from having nothing to produce. Any change that makes a boundary's inventory generator-derived inherits that failure mode and must address it explicitly.

## Decision

A component's declaration is stored as a file of its own, and a boundary's inventory is discovered from those declarations rather than enumerated centrally.

**Component descriptors.** Each platform component carries a descriptor declaring the properties the boundary ApplicationSet needs in order to generate its Application: identity, boundary membership, sync wave, destination namespace, ownership class, and its source — repository, revision and path for platform-owned content, or chart coordinates for third-party charts. A component is added, changed or removed by acting on one file.

**Descriptors are stored in a dedicated tree, not inside the directory they describe.** Storing a descriptor beside the content it deploys is the more natural expression and is rejected on a concrete ground: six components in boundary 03 alone are deployed as recursive directory sources, meaning every file beneath the path is applied to the cluster. A descriptor placed there would itself be submitted to the API server as a manifest. Whether the reconciler happens to skip a given filename is an implementation detail of the tool, not a property the platform should depend on, and the failure it would produce — a sync error on an object that is not a Kubernetes resource — is disproportionate to the tidiness gained. The descriptor tree is therefore separate from, and never a subdirectory of, any deployed path. A descriptor is named for the component it declares, so that the inventory of a boundary is legible from a directory listing.

**Boundary membership is a descriptor field.** A boundary is the set of components declaring membership in it, not a list maintained separately from the components. Membership and content therefore cannot disagree, because they are the same statement.

**Values are files.** A component's Helm values are stored as a values file referenced by the generated Application, not embedded as a scalar inside the boundary template. This restores linting and schema validation to values that currently have neither, and removes the nested templating that inline values require.

**Environment-parameterised components remain enumerated.** A descriptor states a fixed source. Six components do not have one — three in boundary 02 and three in boundary 03 — because their path is a function of the environment slug, the provider, or the topology, which is precisely the directory-based promotion ADR-037 establishes. Their source is not a property of the component but of the environment the boundary is being rendered for, and a static descriptor cannot express it. This is a standing property of the platform rather than a transitional one: every boundary may contain both kinds of component.

Those components are therefore declared in the boundary template itself, where the environment dimensions are in scope, and the boundary's inventory is the union of the two sources. This is not an exception to the decision but a consequence of it: a descriptor describes a component whose content is fixed in the repository, and a component selected by environment is a different kind of thing. Keeping the distinction visible is preferable to giving descriptors a substitution language so that they can pretend otherwise.

**Activation is unchanged.** The generated Application is assigned to its boundary's AppProject exactly as before. ADR-055's separation stands in full: Git owns boundary content, including which Applications exist; the cluster owns boundary activation; the two act on disjoint fields. Nothing in this ADR gives Day-0 a second field to mutate, and no rendered output is applied outside the seed Application.

**Sync waves are unchanged in policy, and are a boundary-01 field only.** ADR-021 restricts waves to ordering CRDs before the operators that own them within boundary 01. The platform observes that restriction: every component in boundary 01 carries a wave, and no component in boundary 03 carries one at all. A descriptor therefore declares a wave only where a wave already exists, and moving inventory into descriptors neither introduces waves to a boundary that has none nor extends where they may be used. Where waves are declared they must be unique within the boundary, so that ordering is a property of the declaration rather than of the order elements happen to appear in a list.

**A boundary's expected inventory size is declared, and Day-0 gates on it.** An inline list makes a boundary's Applications a property of the applied manifest: the moment the seed is applied the elements exist, because they were carried in the object. A generator makes them the result of a repository read that can return nothing, and the Day-0 boundary phase does not currently distinguish the two — it establishes the seed, activates the boundary, and reports success without observing whether any Application was produced.

That distinction matters differently in each creation mode ADR-055 defines. Under sequenced creation the omission surfaces late and misattributed: the phase reports success, and the first evidence is a later phase timing out on a component whose Application was never created, blaming the component rather than the boundary. Under converged creation it may not surface at all, because an ApplicationSet that has generated nothing is healthy, and "has produced no Applications yet" is indistinguishable from the red-then-green convergence the mode exists to permit.

Each boundary therefore declares how many components it contains, and the boundary phase does not report success until that many Applications have been generated. The count is derivable from the descriptors, so it cannot drift from the inventory it describes. This restores to a generator-composed boundary the property an applied list had implicitly, and it is what preserves ADR-055's claim that a sequenced failure is attributable to a named phase.

The gate reads Application state; it writes nothing. Day-0 continues to mutate boundary activation and nothing else, so ADR-055's disjointness of authorities is unaffected.

**Generated Applications are never deleted by the generator.** Because inventory becomes generator-derived, a boundary inherits the empty-generator failure mode described in the Context. Boundary ApplicationSets therefore permit creation and update of generated Applications but not deletion. Removing a component is an explicit act, not a consequence of a generator returning less than it did before. This bounds the blast radius of an unreachable repository or a malformed descriptor to "no changes applied" rather than "every Application in the boundary withdrawn".

### Alternatives considered

**Retain inline list generators.** Rejected. It preserves every consequence in the Context, and the cost grows with component count, which ADR-033's scale targets require to increase.

**Adopt an app-of-apps registry, as the Kubefirst GitOps template does.** Rejected. It reintroduces the monolithic composition ADR-021 removed, and replaces a generator with an Application whose health masks the state of its children.

**Copy a per-cluster rendered tree per component set, also as the Kubefirst template does.** Rejected. It is a template-instantiation model, which ApplicationSet generators exist to replace; it duplicates a full tree to express single-field differences, and every duplicate is independently maintained thereafter.

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Component descriptor | Git | Platform | ArgoCD ApplicationSet controller | Platform | Day-1+ |
| Component values file | Git | Platform | ArgoCD ApplicationSet controller | Platform | Day-1+ |
| Boundary Application inventory | Git | Platform | ArgoCD ApplicationSet controller | Platform | Day-1+ |

Boundary activation state is not owned by this ADR and is unchanged; see ADR-055. See ADR-039 for the complete ownership matrix.

## Consequences

### Positive

A component is declared in a file of its own, so a change to one component touches only that component and is reviewable in isolation. The declaration does not sit beside the content for the reason given in the Decision, but it is a single named file per component rather than a region of a shared one.

Chart references become structured fields, so dependency automation can discover and update pinned versions.

Component values become ordinary files, so the repository's existing YAML linting and schema validation apply to them. The class of defect that produced a silently discarded duplicate key is detected at commit time.

Adding a component stops modifying a file that every other component shares, removing the serialisation and merge conflicts that follow from it.

The set of components in a boundary is derivable by inspection of the repository rather than by reading a template.

### Negative

Boundary composition acquires a dependency on repository reachability at generator evaluation time that an inline list does not have. The dependency already exists on the tenant path per ADR-047, so the class of failure is not new to the platform, but it now extends to platform boundaries. Prohibiting generator-driven deletion bounds the consequence; it does not remove the dependency.

A malformed descriptor fails the rendering of its whole boundary rather than of one component, because generator input is evaluated as a set. This is the existing behaviour of a malformed list element and is not made worse, but it is not improved either.

Day-0 gains a per-boundary readiness gate it did not have, and with it a bootstrap that can fail where it previously proceeded. That is the intended trade -- the phase that proceeded was proceeding past an empty boundary -- but it is a new failure point in a path that is exercised on every cluster creation, and a miscounted boundary stalls a bootstrap that would otherwise have completed.

A boundary's inventory is stated in two places rather than one: descriptors for components with a fixed source, and the boundary template for those selected by environment. Reading the full inventory requires consulting both. The alternative was a substitution language inside descriptors, which would have made every descriptor a template and the distinction invisible rather than absent.

The number of files in the repository increases by roughly one descriptor and one values file per component.

## Impact

Amends ADR-021. The boundary set, the boundary definitions, and the Day-0 choreography are unchanged. What changes is how the Applications within a boundary are expressed: composed from per-component declarations rather than enumerated in the boundary's own template.

Applies to the boundaries whose inventory is an inline list: 01, 02, 03 and 04. It is adopted per boundary rather than at once, because each boundary's rendered Application set can be compared field by field against the inventory it replaces, and a boundary converted alone keeps that comparison small enough to be conclusive. Boundary 03 is converted first as the largest. The ApplicationSets that already compose from a generator — the tenant and spoke-catalog sets — are unaffected.

Amends ADR-055 in one respect. Boundary content remains complete at all times, activation remains a per-boundary AppProject sync window, and Day-0 continues to mutate activation and nothing else, so the disjointness of authorities is unchanged. What changes is the boundary phase's completion criterion: it additionally observes that the boundary's declared inventory has been generated. This is a read, and it is what keeps ADR-055's attribution property true once inventory is generator-derived rather than carried in the applied object. Both creation modes are affected; converged creation, which has no ordering guarantee and attributes failure only to the Application that failed, depends on it more heavily because an Application that was never generated cannot carry the attribution.

No change to ADR-047. The tenant ApplicationSets already compose from a Git generator and are unaffected.

No change to ADR-037. Environment-parameterised sources continue to be selected by directory as that ADR establishes, and are the reason a boundary's inventory has two sources rather than one.

No change to ADR-039 or ADR-040. Git remains the System of Record for all boundary content, and the Day-0 and Day-1 responsibilities are unaltered.

## References

- ADR-021: Boundary-Driven GitOps and Day-0 Choreography
- ADR-033: Fleet Scale Targets and SLOs
- ADR-037: Directory-Based Environment Promotion and Gating
- ADR-039: Platform Ownership Model
- ADR-040: Day-0 vs Day-1 Lifecycle Boundary
- ADR-047: Fleet Tenant Deployment Contract
- ADR-055: AppSet Boundary Activation Gating

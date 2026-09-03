package bootstrap

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// ──────────────────────────────────────────────────────────────────────────
// ADR-055: Boundary Activation as the Day-0 Gating Mechanism
//
// Boundary CONTENT is reconciled from Git by a single seed Application.
// Boundary ACTIVATION is Day-0 state: a deny sync window on the boundary's
// AppProject. Opening a boundary removes that window; nothing else changes.
//
// The two authorities act on disjoint objects — the seed Application owns the
// ApplicationSets, the CLI owns the AppProjects' sync windows — so neither can
// overwrite the other. That is what makes the pre-ADR-055 failure mode (a
// second render clobbering a correct ApplicationSet) unrepresentable rather
// than merely unlikely.
// ──────────────────────────────────────────────────────────────────────────

// GatingMode selects how a cluster is created. Mode and environment are
// independent inputs: every environment class may be created in either mode.
type GatingMode string

const (
	// GatingSequenced is the default. Every boundary is inactive at creation
	// and is activated in phase order behind the readiness gates that already
	// precede it. Deterministic ordering; a failure is attributable to a phase.
	GatingSequenced GatingMode = "sequenced"

	// GatingConverged creates no inactive boundaries. All boundaries reconcile
	// concurrently and converge through ArgoCD retry. No ordering guarantee; a
	// failure is attributable only to the Application that failed.
	GatingConverged GatingMode = "converged"
)

// ValidGatingMode reports whether s names a mode, and normalises it.
func ValidGatingMode(s string) (GatingMode, error) {
	switch GatingMode(strings.ToLower(strings.TrimSpace(s))) {
	case "", GatingSequenced:
		return GatingSequenced, nil
	case GatingConverged:
		return GatingConverged, nil
	default:
		return "", fmt.Errorf(
			"invalid gating mode %q (expected sequenced|converged) — refusing to guess how to create the cluster", s)
	}
}

// seedAppName is the single Day-0 object that hands boundary content to ArgoCD.
const seedAppName = "platform-boundaries"

// boundaryCount is the number of gated boundaries (ADR-021, ADR-047, ADR-051).
const boundaryCount = 6

// boundaryProject returns the AppProject that scopes boundary n and carries its
// activation state.
func boundaryProject(n int) string {
	return fmt.Sprintf("boundary-%02d", n)
}

// boundaryDescriptions are used only for operator legibility in the ArgoCD UI.
var boundaryDescriptions = map[int]string{
	1: "Boundary 01 — platform infrastructure (ArgoCD, operators, CRDs)",
	2: "Boundary 02 — platform data workloads (CNPG, Redis, NATS)",
	3: "Boundary 03 — platform services (Infisical, hub Gateway, platform APIs)",
	4: "Boundary 04 — tenant services (identity, billing, auth proxy)",
	5: "Boundary 05 — fleet provisioning (ADR-047)",
	6: "Boundary 06 — public tenant TLS (ADR-051)",
}

// renderBoundaryAppProject renders one boundary AppProject.
//
// The policy is deliberately permissive and matches what these Applications
// already have under the `default` project. Introducing the project boundary
// and tightening its policy are two changes with different blast radii; this
// is the first. Narrowing sourceRepos/destinations/resource kinds is follow-up
// work and must be validated against a live boundary before it lands.
//
// When gated, the project carries a deny sync window. ArgoCD evaluates windows
// continuously, so a boundary stays inactive until the window is removed — the
// Applications exist and report divergence but are not permitted to sync.
// The schedule/duration pair is the documented encoding for a window that is
// always active: a window opens every minute and lasts a day.
func renderBoundaryAppProject(n int, gated bool) string {
	var window string
	if gated {
		window = `
  # ADR-055: boundary activation state. Present = inactive. Owned by Day-0 and
  # removed when this boundary's phase opens it. Deliberately NOT reconciled
  # from Git: this field is the Day-0/Day-1 lifecycle boundary (ADR-040).
  # '* * * * *' for 24h is the encoding for "always active" — a window opens
  # every minute and outlives the next, so the deny never lapses.
  syncWindows:
    - kind: deny
      schedule: '* * * * *'
      duration: 24h
      applications:
        - '*'
      manualSync: false
      timeZone: UTC`
	}

	return fmt.Sprintf(`apiVersion: argoproj.io/v1alpha1
kind: AppProject
metadata:
  name: %s
  namespace: platform-ops
spec:
  description: %s
  sourceRepos:
    - '*'
  destinations:
    - namespace: '*'
      server: '*'
  clusterResourceWhitelist:
    - group: '*'
      kind: '*'
  namespaceResourceWhitelist:
    - group: '*'
      kind: '*'%s
`, boundaryProject(n), boundaryDescriptions[n], window)
}

// renderSeedApplication renders the single Application that owns all boundary
// ApplicationSets. Once applied it reconciles continuously with self-heal, so a
// boundary element added in Git reaches the cluster without any bootstrap action
// — which is the defect ADR-055 exists to remove.
//
// The matrix values are carried as Helm parameters on the Application rather
// than passed to a one-shot render. They are the cluster's identity, fixed at
// creation, and being part of a reconciled object they survive rather than
// having to be re-supplied by whoever last ran a render.
func renderSeedApplication(envRevision, envSlug, provider, topology, hubIngressAddress, publicTlsIssuer, oidcIssuer string) string {
	return fmt.Sprintf(`apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: %s
  namespace: platform-ops
spec:
  project: default
  source:
    repoURL: https://github.com/soloz-io/zero-ops
    targetRevision: %q
    path: manifests/argocd/environment-manager
    helm:
      parameters:
        - name: environmentRevision
          value: %q
        - name: environmentSlug
          value: %q
        - name: provider
          value: %q
        - name: topology
          value: %q
        - name: hubIngressAddress
          value: %q
        - name: publicTlsIssuer
          value: %q
        - name: oidcIssuer
          value: %q
  destination:
    server: https://kubernetes.default.svc
    namespace: platform-ops
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
    syncOptions:
      - ServerSideApply=true
`, seedAppName, envRevision, envRevision, envSlug, provider, topology, hubIngressAddress, publicTlsIssuer, oidcIssuer)
}

// applySeed establishes the Day-0 seed: the six boundary AppProjects and the
// seed Application. It is idempotent and safe to call from every boundary phase.
//
// Ordering is forced and load-bearing. The AppProjects must exist before the
// seed Application creates any ApplicationSet, because the generated
// Applications name those projects — and because in sequenced mode the deny
// windows must be in place before the first Application can attempt to sync.
func (o *Orchestrator) applySeed(ctx context.Context, kubeconfig string) error {
	gated := o.gatingMode() == GatingSequenced

	var projects strings.Builder
	for n := 1; n <= boundaryCount; n++ {
		projects.WriteString("---\n")
		projects.WriteString(renderBoundaryAppProject(n, gated))
	}
	if err := kubectlApplyStdin(ctx, kubeconfig, projects.String()); err != nil {
		return fmt.Errorf("failed to apply boundary AppProjects: %w", err)
	}

	return o.applySeedApplication(ctx, kubeconfig)
}

// applySeedApplication renders and applies the seed Application alone.
//
// Separated from the AppProjects because the two have different lifetimes. The
// AppProjects carry boundary activation state, and in sequenced mode restating
// them re-applies every deny sync window — correct during a bootstrap that
// activates each boundary afterwards, and a fleet-wide freeze on a cluster that
// is already running. The seed Application carries no such state and can be
// restated at any time.
func (o *Orchestrator) applySeedApplication(ctx context.Context, kubeconfig string) error {
	// The identity provider tenants authenticate against (ADR-050). Derived from
	// the environment's own zone rather than configured separately: the issuer IS
	// an endpoint this bootstrap already computes, and a second source for it is a
	// way for the two to disagree.
	//
	// Supplied here because it is environment identity, not fleet data — a fleet
	// able to set it could point its login flow at an issuer this platform does
	// not trust.
	//
	// .ID (Zitadel), not .Auth (Hydra). The organisation is the tenant in Zitadel:
	// it OWNS the user rather than describing it, so a user with no tenant is not
	// expressible and a token either carries an organisation or is rejected. Under
	// Hydra the tenant was a claim written onto an identity, which is what allowed
	// identities to exist with none and produced repeated "Token missing required
	// tenant_id claim" failures at the BFF.
	//
	// Ory keeps serving auth.<zone> and is not removed here. Rolling back is
	// pointing this line at .Auth again, which is why the endpoint set carries
	// both rather than renaming one.
	zone, err := ReadHubZone(".", o.EnvironmentSlug)
	if err != nil {
		return fmt.Errorf("failed to read hub zone for the OIDC issuer: %w", err)
	}
	oidcIssuer := "https://" + DeriveHubEndpoints(zone).ID

	publicTlsIssuer, err := o.publicTlsIssuerFor()
	if err != nil {
		return err
	}

	hubIngressAddress := o.hubIngressAddress(ctx, kubeconfig)
	if hubIngressAddress == "" {
		fmt.Println("[seed] ⚠️  could not resolve hub ingress address; the hub Gateway's " +
			"hostnames will not be published, which breaks public DNS and ACME issuance")
	} else if err := writeExternalDNSTargetArtifact(hubIngressAddress); err != nil {
		return fmt.Errorf("failed to write the external-dns target artifact: %w", err)
	}

	envRevision := "main"
	if b := currentGitBranch(); b != "" && b != "main" {
		envRevision = b
	}

	seed := renderSeedApplication(envRevision, o.EnvironmentSlug, o.providerName(),
		o.Topology, hubIngressAddress, publicTlsIssuer, oidcIssuer)
	if err := kubectlApplyStdin(ctx, kubeconfig, seed); err != nil {
		return fmt.Errorf("failed to apply the seed Application: %w", err)
	}
	return nil
}

// ReapplySeed brings an existing cluster's seed Application to what the
// renderer above defines.
//
// The seed Application is written once, at bootstrap. Adding a parameter to
// renderSeedApplication therefore changes nothing on a cluster already running:
// the live Application keeps the parameter set it was created with, the
// environment-manager chart renders without the new value, and every
// Application generated from it fails comparison. That failure does not present
// as a missing parameter — it presents as ComparisonError, which ArgoCD reports
// as sync status Unknown, and auto-sync only fires on OutOfSync. The change
// appears pushed, the Application appears healthy, and nothing deploys. This is
// how the per-tenant gateway sat undeployed after being committed.
//
// The alternative is patching the live Application by hand, which loses the fix
// the moment anyone reads the repository as the source of truth. This exists so
// the renderer stays the single definition of the seed and an existing cluster
// can be brought to it — the same manifest, applied, rather than a second one
// typed at a terminal.
//
// Only the seed Application is applied. Boundary activation is Day-0 state
// (ADR-040, ADR-055) and restating it here would re-gate boundaries that have
// been opened.
func (o *Orchestrator) ReapplySeed(ctx context.Context, kubeconfig string) error {
	return o.applySeedApplication(ctx, kubeconfig)
}

// ReadSeedIdentity returns the environment, provider and topology recorded on a
// cluster's existing seed Application.
//
// These three name the cluster. Re-supplying them by hand to restate the seed
// invites supplying one of them differently, and the failure is quiet: topology
// is a path segment, so a value that merely looks reasonable repoints the spoke
// pool ApplicationSet at a directory that does not exist, and the Applications
// generated from it go missing rather than erroring. Reading them back from the
// cluster removes the opportunity.
func ReadSeedIdentity(ctx context.Context, kubeconfig string) (envSlug, provider, topology string, err error) {
	cmd := exec.CommandContext(ctx, "kubectl", "--kubeconfig", kubeconfig,
		"-n", "platform-ops", "get", "application", seedAppName,
		"-o", `jsonpath={range .spec.source.helm.parameters[*]}{.name}={.value}{"\n"}{end}`)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", "", "", fmt.Errorf("failed to read the seed Application %q: %w: %s",
			seedAppName, err, strings.TrimSpace(stderr.String()))
	}

	values := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		name, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if found {
			values[name] = value
		}
	}

	envSlug, provider = values["environmentSlug"], values["provider"]
	if envSlug == "" || provider == "" {
		return "", "", "", fmt.Errorf(
			"the seed Application %q records no environmentSlug or provider — refusing to guess the cluster's identity", seedAppName)
	}
	// topology is legitimately empty on a single-topology cluster, so its
	// absence is not an error and must not be defaulted.
	return envSlug, provider, values["topology"], nil
}

// activateBoundary opens boundary n by removing its deny sync window.
//
// Idempotent by construction: a merge patch setting the field to null removes it
// if present and is a no-op if absent, so a resumed bootstrap re-opening an
// already-open boundary mutates nothing. This is the contrast with the previous
// mechanism, where re-entering a phase restated the entire boundary.
func (o *Orchestrator) activateBoundary(ctx context.Context, kubeconfig string, n int) error {
	if o.gatingMode() == GatingConverged {
		// Nothing was ever closed. Boundary phases remain in the sequence and
		// still report, so the bootstrap's account of itself is unchanged.
		return nil
	}
	cmd := exec.CommandContext(ctx, "kubectl", "--kubeconfig", kubeconfig,
		"patch", "appproject", boundaryProject(n), "-n", "platform-ops",
		"--type", "merge", "-p", `{"spec":{"syncWindows":null}}`)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to activate boundary %02d: %w\n%s", n, err, out)
	}
	return nil
}

// gatingMode returns the configured mode, defaulting to sequenced. The default
// is deliberate: converged creation is requested by name or it does not occur.
func (o *Orchestrator) gatingMode() GatingMode {
	if o.Gating == GatingConverged {
		return GatingConverged
	}
	return GatingSequenced
}

// deployBoundary establishes the seed (idempotent) and opens boundary n. This is
// the operation that replaced "render a subset of the chart and apply it". The
// phase, its position, its preconditions and its reporting are unchanged.
func (o *Orchestrator) deployBoundary(ctx context.Context, kubeconfig string, n int) error {
	if err := o.applySeed(ctx, kubeconfig); err != nil {
		return err
	}
	return o.activateBoundary(ctx, kubeconfig, n)
}

func kubectlApplyStdin(ctx context.Context, kubeconfig, manifest string) error {
	cmd := exec.CommandContext(ctx, "kubectl", "--kubeconfig", kubeconfig, "apply", "-f", "-")
	cmd.Stdin = bytes.NewReader([]byte(manifest))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w\n%s", err, out)
	}
	return nil
}

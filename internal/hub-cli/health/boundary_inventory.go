package health

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BoundaryInventoryChecker asserts that a boundary generated the Applications it
// declares (ADR-061).
//
// Before ADR-061 a boundary's Applications were literal elements of the
// ApplicationSet, so applying the seed made them exist: there was nothing to
// check. Composing the inventory from descriptors makes it the result of a
// repository read that can return nothing, and a generator that returns nothing
// is not an error — the ApplicationSet reports healthy having produced zero
// Applications, which is indistinguishable from having nothing to produce.
//
// Without this gate the omission surfaces differently in each ADR-055 creation
// mode, and badly in both. Sequenced: the boundary phase reports success and the
// first symptom is a LATER phase timing out on a component whose Application was
// never created, attributing the failure to the component instead of the
// boundary. Converged: nothing is gated at all, so an absent Application is
// indistinguishable from the red-then-green convergence the mode exists to
// permit, and it may never surface.
//
// The expected count is assembled from the two sources the boundary is composed
// of, so it cannot drift from the inventory it describes:
//
//	descriptors on disk      manifests/argocd/components/<NN>/*.yaml
//	inline elements          read back from the live ApplicationSet
//
// This checker only reads. Day-0 continues to mutate boundary activation and
// nothing else, so ADR-055's disjoint authorities are unaffected.
type BoundaryInventoryChecker struct {
	// Boundary is the two-digit boundary number, e.g. "03".
	Boundary string
	// RepoRoot is the platform repository working tree. Descriptors are counted
	// from it. Empty means the process working directory, which is what
	// scripts/hub-bootstrap.sh provides.
	RepoRoot string
}

// NewBoundaryInventoryChecker constructs a checker for one boundary.
func NewBoundaryInventoryChecker(boundary, repoRoot string) *BoundaryInventoryChecker {
	return &BoundaryInventoryChecker{Boundary: boundary, RepoRoot: repoRoot}
}

// Name returns the checker's identifier.
func (b *BoundaryInventoryChecker) Name() string {
	return fmt.Sprintf("boundary %s inventory", b.Boundary)
}

// appSetName is the ApplicationSet that composes this boundary. The naming
// convention is the boundary number followed by the boundary's slug; matching on
// the prefix avoids duplicating the slug here.
func (b *BoundaryInventoryChecker) appSetPrefix() string { return b.Boundary + "-" }

// descriptorCount counts the component descriptors declared for this boundary.
//
// A read failure is returned as an error rather than treated as zero. Zero is a
// legitimate value — a boundary may be composed entirely of inline elements —
// and silently conflating "no descriptors" with "could not look" would defeat
// the whole check.
func (b *BoundaryInventoryChecker) descriptorCount() (int, error) {
	root := b.RepoRoot
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return 0, fmt.Errorf("cannot determine working directory: %w", err)
		}
		root = wd
	}

	dir := filepath.Join(root, "manifests", "argocd", "components", b.Boundary)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("cannot read descriptor directory %s: %w", dir, err)
	}

	n := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".yaml") {
			n++
		}
	}
	return n, nil
}

// inlineCount reads the environment-parameterised elements back from the live
// ApplicationSet. They are enumerated in the boundary template rather than
// declared as descriptors (ADR-061, ADR-037), so the cluster object is where the
// two sources can be counted together.
func (b *BoundaryInventoryChecker) inlineCount(ctx context.Context, kubeconfig, appSet string) (int, error) {
	out, err := runKubectl(ctx, []string{
		"--kubeconfig", kubeconfig,
		"get", "applicationset", appSet,
		"-n", "platform-ops",
		"-o", "json",
	})
	if err != nil {
		return 0, fmt.Errorf("cannot read ApplicationSet %s: %w", appSet, err)
	}

	var as struct {
		Spec struct {
			Generators []struct {
				List *struct {
					Elements []json.RawMessage `json:"elements"`
				} `json:"list"`
			} `json:"generators"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(out, &as); err != nil {
		return 0, fmt.Errorf("cannot parse ApplicationSet %s: %w", appSet, err)
	}

	n := 0
	for _, g := range as.Spec.Generators {
		if g.List != nil {
			n += len(g.List.Elements)
		}
	}
	return n, nil
}

// resolveAppSet finds the ApplicationSet composing this boundary by name prefix.
func (b *BoundaryInventoryChecker) resolveAppSet(ctx context.Context, kubeconfig string) (string, error) {
	out, err := runKubectl(ctx, []string{
		"--kubeconfig", kubeconfig,
		"get", "applicationsets", "-n", "platform-ops",
		"-o", "jsonpath={.items[*].metadata.name}",
	})
	if err != nil {
		return "", fmt.Errorf("cannot list ApplicationSets: %w", err)
	}
	for _, name := range strings.Fields(string(out)) {
		if strings.HasPrefix(name, b.appSetPrefix()) {
			return name, nil
		}
	}
	return "", fmt.Errorf("no ApplicationSet named %s* exists", b.appSetPrefix())
}

// Check performs one poll: it compares the Applications the boundary's project
// owns against the inventory the boundary declares.
func (b *BoundaryInventoryChecker) Check(ctx context.Context, kubeconfig string) error {
	appSet, err := b.resolveAppSet(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("%s: %w", b.Name(), err)
	}

	descriptors, err := b.descriptorCount()
	if err != nil {
		return fmt.Errorf("%s: %w", b.Name(), err)
	}

	inline, err := b.inlineCount(ctx, kubeconfig, appSet)
	if err != nil {
		return fmt.Errorf("%s: %w", b.Name(), err)
	}

	expected := descriptors + inline
	if expected == 0 {
		return fmt.Errorf(
			"%s: declares no components at all (no descriptors under manifests/argocd/components/%s/ and no inline elements) — "+
				"a boundary that declares nothing cannot be distinguished from one that failed to render",
			b.Name(), b.Boundary)
	}

	// The boundary's Applications are those its AppProject scopes. Counting by
	// project rather than by owner reference keeps this independent of how the
	// ApplicationSet labels what it generates.
	apps, err := b.boundaryApps(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("%s: %w", b.Name(), err)
	}

	got := len(apps)
	if got < expected {
		return fmt.Errorf(
			"%s: %d of %d Applications generated (%d descriptor(s) + %d inline) — "+
				"the ApplicationSet reports healthy while generating fewer than the boundary declares; "+
				"check that the descriptors are reachable at the revision the generator reads",
			b.Name(), got, expected, descriptors, inline)
	}

	// Counting is not enough. An Application whose target state cannot be
	// generated at all still exists and still reports Healthy, because health is
	// computed over resources it manages and it manages none. Counting alone
	// passed a boundary in which every path-based Application carried
	// "app path does not exist", and the failure surfaced two phases later as a
	// missing operator webhook.
	//
	// ComparisonError is the condition ArgoCD raises when it cannot render the
	// source, so it is the boundary's problem by construction: an unreachable
	// repository, a revision without the descriptors, or a descriptor that does
	// not produce a usable source. It is reported rather than tolerated. A
	// genuinely transient one clears on a later poll, because this runs inside
	// the waiter's retry loop.
	if broken := comparisonErrors(apps); len(broken) > 0 {
		return fmt.Errorf(
			"%s: %d of %d Applications cannot render their source: %s — "+
				"they exist and report Healthy because they manage nothing, so a count alone would pass",
			b.Name(), len(broken), got, strings.Join(broken, "; "))
	}

	return nil
}

// application is the subset of an Argo CD Application this checker reads.
type application struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		Project string `json:"project"`
	} `json:"spec"`
	Status struct {
		Conditions []struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"conditions"`
	} `json:"status"`
}

// boundaryApps returns the Applications scoped to this boundary's AppProject.
func (b *BoundaryInventoryChecker) boundaryApps(ctx context.Context, kubeconfig string) ([]application, error) {
	out, err := runKubectl(ctx, []string{
		"--kubeconfig", kubeconfig,
		"get", "applications", "-n", "platform-ops",
		"-o", "json",
	})
	if err != nil {
		return nil, fmt.Errorf("cannot list Applications for boundary-%s: %w", b.Boundary, err)
	}

	var list struct {
		Items []application `json:"items"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, fmt.Errorf("cannot parse Applications: %w", err)
	}

	project := "boundary-" + b.Boundary
	var apps []application
	for _, a := range list.Items {
		if a.Spec.Project == project {
			apps = append(apps, a)
		}
	}
	return apps, nil
}

// comparisonErrors returns "<name>: <message>" for each Application that cannot
// render its source, truncated so one broken boundary does not emit a wall of
// identical messages.
func comparisonErrors(apps []application) []string {
	var out []string
	for _, a := range apps {
		for _, c := range a.Status.Conditions {
			if c.Type != "ComparisonError" {
				continue
			}
			msg := strings.TrimSpace(c.Message)
			if len(msg) > 160 {
				msg = msg[:160] + "…"
			}
			out = append(out, a.Metadata.Name+": "+msg)
			break
		}
	}
	const max = 3
	if len(out) > max {
		out = append(out[:max], fmt.Sprintf("and %d more", len(out)-max))
	}
	return out
}

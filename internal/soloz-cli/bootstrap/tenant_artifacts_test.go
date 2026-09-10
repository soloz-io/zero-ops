package bootstrap

import (
	"path/filepath"
	"strings"
	"testing"
)

// ADR-045 artifacts are per-cluster instance data, so a tenant's belong in the
// tenant's repository (ADR-062, ADR-072). Writing them into zero-ops would put
// one tenant's Infisical coordinates in the types repository -- the defect
// removed from the published charts, reappearing in the bootstrap that produces
// them.
func TestADR045ArtifactsBelongToTheTenantRepository(t *testing.T) {
	fromRepoRoot(t)

	platform := &Orchestrator{EnvironmentSlug: "dev"}
	reg, err := platform.readADR045Registry()
	if err != nil {
		t.Fatalf("reading the registry: %v", err)
	}
	for _, a := range reg.Artifacts {
		if !strings.HasPrefix(a.File, "manifests/") {
			t.Errorf("without a tenant repository an artifact belongs under "+
				"manifests/, got %q", a.File)
		}
	}

	tenant := &Orchestrator{EnvironmentSlug: "dev", GitopsDir: "/somewhere", ClusterName: "acme-hub"}
	treg, err := tenant.readADR045Registry()
	if err != nil {
		t.Fatalf("reading the registry for a tenant: %v", err)
	}
	if len(treg.Artifacts) == 0 {
		t.Fatal("the registry declares no artifacts; this test would prove nothing")
	}
	for _, a := range treg.Artifacts {
		want := filepath.Join("clusters", "acme-hub", "generated")
		if filepath.Dir(a.File) != want {
			t.Errorf("a tenant's artifact belongs in %s, got %q", want, a.File)
		}
		if strings.Contains(a.File, "manifests/") {
			t.Errorf("a tenant's repository has no manifests/ tree, got %q", a.File)
		}
	}
}

// The cluster name is what places the artifact. Without it the location cannot
// be resolved, and guessing would put one cluster's coordinates where another
// reads them.
func TestADR045TenantArtifactsNeedAClusterName(t *testing.T) {
	fromRepoRoot(t)
	o := &Orchestrator{EnvironmentSlug: "dev", GitopsDir: "/somewhere"}

	if _, err := o.readADR045Registry(); err == nil {
		t.Error("a tenant repository with no cluster name must be refused")
	}
}

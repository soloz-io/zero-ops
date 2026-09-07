package tenant

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testSpec() Spec {
	return Spec{
		TenantID: "acme", GitOrg: "acme-inc", Domain: "acme.example",
		Provider: "hetzner", Region: "hel1", Environment: "dev",
		ClusterName: "acme-hub", BundleVersion: "v1.2.3",
		PlatformRepoURL: "https://github.com/soloz-io/zero-ops",
	}
}

func render(t *testing.T, s Spec) string {
	t.Helper()
	dst := t.TempDir()
	if err := Render(filepath.Join("..", "..", "..", "manifests", "tenants", "gitops-template"), dst, s); err != nil {
		t.Fatalf("Render: %v", err)
	}
	return dst
}

func read(t *testing.T, parts ...string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(parts...))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(b)
}

// The template a tenant keeps must still be a template. Substituting cluster
// facts everywhere leaves it hard-coded to whichever cluster was rendered first,
// so a tenant adding its second cluster silently gets a copy of its first --
// same name, same environment, same region -- and the collision appears as two
// clusters contending for one set of resources rather than as a bad render.
func TestRender_RetainedTemplateKeepsPerClusterTokens(t *testing.T) {
	dst := render(t, testSpec())
	// Asserted per file, because the two carry different cluster facts: the
	// Application names the cluster, and the values file describes where it runs.
	for f, tokens := range map[string][]string{
		"bundle.yaml": {"<CLUSTER_NAME>", "<BUNDLE_VERSION>"},
		"values.yaml": {"<ENVIRONMENT>", "<CLOUD_PROVIDER>", "<CLOUD_REGION>"},
	} {
		got := read(t, dst, "templates", "spoke-cluster", f)
		for _, token := range tokens {
			if !strings.Contains(got, token) {
				t.Errorf("templates/spoke-cluster/%s lost %s; it is no longer a template", f, token)
			}
		}
		if strings.Contains(got, "acme-hub") {
			t.Errorf("templates/spoke-cluster/%s was rendered against the control plane", f)
		}
	}
}

// Tenant facts are known at onboarding and are the same for every cluster, so a
// tenant should never have to supply its own repository URL to add one.
func TestRender_RetainedTemplateHasTenantFactsFilled(t *testing.T) {
	dst := render(t, testSpec())
	got := read(t, dst, "templates", "spoke-cluster", "bundle.yaml")
	if !strings.Contains(got, "https://github.com/acme-inc/acme-gitops") {
		t.Error("retained template does not name the tenant's own repository")
	}
	if strings.Contains(got, "<GITOPS_REPO_URL>") || strings.Contains(got, "<PLATFORM_REPO_URL>") {
		t.Error("tenant-wide tokens survived into the retained template")
	}
}

func TestRender_HydratesControlPlaneAndRemovesItsTemplate(t *testing.T) {
	dst := render(t, testSpec())
	if _, err := os.Stat(filepath.Join(dst, "clusters", "acme-hub", "bundle.yaml")); err != nil {
		t.Fatalf("control plane not hydrated: %v", err)
	}
	// Consumed once. Leaving it would offer a tenant a second way to create the
	// cluster it already has.
	if _, err := os.Stat(filepath.Join(dst, "templates", "control-plane")); !os.IsNotExist(err) {
		t.Error("the consumed control-plane template was not removed")
	}
	got := read(t, dst, "clusters", "acme-hub", "bundle.yaml")
	if strings.Contains(got, "<") && strings.Contains(got, ">") {
		for _, line := range strings.Split(got, "\n") {
			if tok := unresolvedToken(line); tok != "" {
				t.Errorf("instance still carries %s", tok)
			}
		}
	}
}

// An unsubstituted token reaches the tenant's repository as a literal, and the
// Application carrying it fails to resolve a repository or a version with an
// error naming neither.
func TestRender_RefusesToLeaveATokenInTheInstance(t *testing.T) {
	dir := t.TempDir()
	tmpl := filepath.Join(dir, "tmpl")
	if err := os.MkdirAll(filepath.Join(tmpl, "templates", "control-plane"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpl, "templates", "control-plane", "x.yaml"),
		[]byte("a: <CLUSTER_NAME>\nb: <NOT_A_REAL_TOKEN>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Render(tmpl, filepath.Join(dir, "out"), testSpec())
	if err == nil || !strings.Contains(err.Error(), "<NOT_A_REAL_TOKEN>") {
		t.Fatalf("expected the unsubstituted token to be named, got %v", err)
	}
}

// A token is an identifier or a location. A secret substituted into one would be
// committed to the tenant's repository and would survive in its history after
// any later correction.
func TestRender_RefusesSecretMaterialInATokenValue(t *testing.T) {
	s := testSpec()
	s.Domain = "-----BEGIN RSA PRIVATE KEY-----"
	if err := Render(filepath.Join("..", "..", "..", "manifests", "tenants", "gitops-template"), t.TempDir(), s); err == nil {
		t.Fatal("expected a refusal for secret-looking token value")
	}
}

func TestSpec_RepoNameAndValidation(t *testing.T) {
	if got := testSpec().RepoName(); got != "acme-gitops" {
		t.Errorf("RepoName = %q, want acme-gitops", got)
	}
	var empty Spec
	err := empty.validate()
	if err == nil {
		t.Fatal("expected validation to fail on an empty spec")
	}
	for _, want := range []string{"tenant", "org", "domain"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("validation error does not name %q: %v", want, err)
		}
	}
}

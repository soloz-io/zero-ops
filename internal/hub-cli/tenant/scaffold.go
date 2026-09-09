// Package tenant implements ADR-062 onboarding: creating a tenant's
// <tenant>-gitops repository and rendering it from the template the platform
// holds.
//
// Scaffolding happens once. Everything after it is a proposal against the
// rendered repository (ADR-064), which is the whole of the difference from the
// platform this pattern is taken from: kubefirst renders a customer's repository
// and never returns to it, so a fix it publishes reaches nothing already running.
package tenant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Spec is what onboarding is given. Every field is an identifier or a location.
//
// No field carries secret material, and the renderer refuses a token value that
// looks like one: a secret substituted here would be committed to the tenant's
// repository and would survive in its history after any later correction.
type Spec struct {
	TenantID    string
	GitOrg      string
	Domain      string
	Provider    string
	Region      string
	Environment string
	// ClusterName is the tenant's control plane, the first cluster in the box.
	ClusterName string
	// BundleVersion is the platform version this box starts on.
	BundleVersion string
	// PlatformRepoURL is the platform's repository. A scaffolded box does not
	// resolve it at runtime (ADR-063); it is recorded so a tenant can find the
	// source of what it runs.
	PlatformRepoURL string
	// BundleRegistry is where published bundles are pulled from, without a
	// scheme: ArgoCD pulls a scheme-less registry host as an OCI artefact and
	// passes anything else to `helm pull --repo`, which does not speak OCI.
	BundleRegistry string
	// Private controls repository visibility at creation.
	Private bool
}

// RepoName is the repository this tenant's box reconciles from.
func (s Spec) RepoName() string { return s.TenantID + "-gitops" }

func (s Spec) repoURL() string {
	return fmt.Sprintf("https://github.com/%s/%s", s.GitOrg, s.RepoName())
}

func (s Spec) validate() error {
	missing := []string{}
	for name, v := range map[string]string{
		"tenant": s.TenantID, "org": s.GitOrg, "domain": s.Domain,
		"provider": s.Provider, "environment": s.Environment,
		"cluster": s.ClusterName, "bundle-version": s.BundleVersion,
		"platform-repo": s.PlatformRepoURL, "bundle-registry": s.BundleRegistry,
	} {
		if strings.TrimSpace(v) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required values: %s", strings.Join(missing, ", "))
	}
	return nil
}

// tenantTokens are facts about the tenant, true of every cluster in its box.
// They are substituted everywhere, including in the templates the tenant keeps.
func (s Spec) tenantTokens() map[string]string {
	return map[string]string{
		"<TENANT_ID>":         s.TenantID,
		"<GIT_ORG>":           s.GitOrg,
		"<GITOPS_REPO_URL>":   s.repoURL(),
		"<PLATFORM_REPO_URL>": s.PlatformRepoURL,
		"<BUNDLE_REGISTRY>":   s.BundleRegistry,
		"<DOMAIN_NAME>":       s.Domain,
	}
}

// clusterTokens are facts about ONE cluster, and are substituted only into the
// instance being hydrated.
//
// The templates a tenant keeps must retain them. Substituting them everywhere
// leaves templates/spoke-cluster hard-coded to the control plane that happened to
// be rendered first, so a tenant adding its second cluster gets a copy of its
// first -- same name, same environment, same region. The template would still
// look correct, and the collision would appear as two clusters fighting over one
// set of resources.
func (s Spec) clusterTokens() map[string]string {
	return map[string]string{
		"<BUNDLE_VERSION>": s.BundleVersion,
		"<CLUSTER_NAME>":   s.ClusterName,
		"<ENVIRONMENT>":    s.Environment,
		"<CLOUD_PROVIDER>": s.Provider,
		"<CLOUD_REGION>":   s.Region,
	}
}

func (s Spec) allTokens() map[string]string {
	all := s.tenantTokens()
	for k, v := range s.clusterTokens() {
		all[k] = v
	}
	return all
}

// secretish rejects a value that looks like credential material. A token is an
// identifier or a location; anything long and high-entropy, or spelled like a
// key, is a value someone has passed to the wrong place.
func secretish(v string) bool {
	l := strings.ToLower(v)
	for _, marker := range []string{"-----begin", "secret", "password", "token", "private_key", "privatekey"} {
		if strings.Contains(l, marker) {
			return true
		}
	}
	return false
}

// Render writes the tenant's repository content into dst.
//
// The shape it produces is the one this platform's reference implementation
// arrived at: a template directory consumed once and removed, and one retained
// for the clusters the tenant adds later. What is deliberately NOT copied is the
// platform's own content. kubefirst copies it, which is why nothing propagates
// to a rendered repository afterwards; here the bundle is sourced, so the
// platform can keep maintaining it (ADR-063).
func Render(templateDir, dst string, s Spec) error {
	if err := s.validate(); err != nil {
		return err
	}
	for token, value := range s.allTokens() {
		if secretish(value) {
			return fmt.Errorf("refusing to render %s: value looks like secret material, and every token is an identifier or a location", token)
		}
	}

	if err := copyTree(templateDir, dst); err != nil {
		return fmt.Errorf("copy template: %w", err)
	}

	// Hydrate the control plane from its template, then remove the template that
	// produced it. It is consumed once, at onboarding; a tenant adding a cluster
	// later renders templates/spoke-cluster, which is why that one is kept.
	src := filepath.Join(dst, "templates", "control-plane")
	if err := copyTree(src, filepath.Join(dst, "clusters", s.ClusterName)); err != nil {
		return fmt.Errorf("hydrate control plane: %w", err)
	}
	if err := os.RemoveAll(src); err != nil {
		return fmt.Errorf("remove consumed template: %w", err)
	}

	// Tenant facts everywhere; cluster facts only in the instance. Only the
	// instance is checked for leftovers, because a retained template is supposed
	// to still contain them.
	if err := substitute(dst, s.tenantTokens(), false); err != nil {
		return err
	}
	return substitute(filepath.Join(dst, "clusters", s.ClusterName), s.clusterTokens(), true)
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, b, info.Mode())
	})
}

// substitute replaces tokens across the rendered tree and refuses to leave one
// behind. An unsubstituted token is not cosmetic: it reaches the tenant's
// repository as a literal, and the Application that carries it fails to resolve
// a repository or a version with an error naming neither.
func substitute(root string, tokens map[string]string, strict bool) error {
	var leftover []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out := string(b)
		for token, value := range tokens {
			out = strings.ReplaceAll(out, token, value)
		}
		if strict {
			for _, line := range strings.Split(out, "\n") {
				if t := unresolvedToken(line); t != "" {
					leftover = append(leftover, fmt.Sprintf("%s: %s", p, t))
				}
			}
		}
		return os.WriteFile(p, []byte(out), info.Mode())
	})
	if err != nil {
		return err
	}
	if len(leftover) > 0 {
		return fmt.Errorf("unsubstituted tokens remain:\n  %s", strings.Join(leftover, "\n  "))
	}
	return nil
}

// unresolvedToken finds a <UPPER_CASE> token, which is the template's spelling
// and cannot occur in rendered YAML for any other reason.
func unresolvedToken(line string) string {
	for i := 0; i < len(line); i++ {
		if line[i] != '<' {
			continue
		}
		j := strings.IndexByte(line[i:], '>')
		if j < 0 {
			return ""
		}
		inner := line[i+1 : i+j]
		if inner != "" && inner == strings.ToUpper(inner) && !strings.ContainsAny(inner, " /\"'") {
			return "<" + inner + ">"
		}
		i += j
	}
	return ""
}

// Credential is how the platform authenticates to the tenant's git host.
//
// ADR-062 makes the App installation the platform's only credential, and it is
// scoped to repositories: it can create one and open pull requests against it,
// and reaches no cloud account, cluster or secret store. A personal token is
// accepted because that is what exists before an App is registered, and it is
// the same authority by a weaker route -- it should not outlive onboarding of
// the first real tenant.
type Credential struct {
	Token  string
	IsApp  bool
	Source string
}

// TokenFromEnv resolves the credential the way the rest of the CLI does, so
// onboarding needs no separate setup on a machine that can already bootstrap.
func TokenFromEnv(repoRoot string) (Credential, error) {
	if t := strings.TrimSpace(os.Getenv("GITHUB_APP_TOKEN")); t != "" {
		return Credential{Token: t, IsApp: true, Source: "GITHUB_APP_TOKEN"}, nil
	}
	if t := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); t != "" {
		return Credential{Token: t, Source: "GITHUB_TOKEN"}, nil
	}
	p := filepath.Join(repoRoot, "k8-secrets", "github", "github-pat-token")
	b, err := os.ReadFile(p)
	if err != nil {
		return Credential{}, fmt.Errorf("no credential: set GITHUB_APP_TOKEN or GITHUB_TOKEN, or provide %s", p)
	}
	return Credential{Token: strings.TrimSpace(string(b)), Source: p}, nil
}

// CreateRepo creates <org>/<tenant>-gitops. It reports whether the repository
// already existed, because onboarding must be resumable: a run that failed after
// creating the repository has to be able to continue rather than requiring the
// operator to delete it first.
func CreateRepo(ctx context.Context, s Spec, cred Credential) (existed bool, err error) {
	api := "https://api.github.com"
	if v := strings.TrimSpace(os.Getenv("GITHUB_API_URL")); v != "" {
		api = strings.TrimSuffix(v, "/")
	}
	client := &http.Client{Timeout: 30 * time.Second}

	get, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/repos/%s/%s", api, s.GitOrg, s.RepoName()), nil)
	authorize(get, cred)
	if resp, err := client.Do(get); err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return true, nil
		}
	}

	body, _ := json.Marshal(map[string]any{
		"name":        s.RepoName(),
		"private":     s.Private,
		"description": fmt.Sprintf("Infrastructure for the %s box, maintained by proposal", s.TenantID),
		"auto_init":   false,
	})
	post, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/orgs/%s/repos", api, s.GitOrg), bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	authorize(post, cred)
	post.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(post)
	if err != nil {
		return false, fmt.Errorf("create repository: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		var msg struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&msg)
		return false, fmt.Errorf("create %s/%s: %s (%s)", s.GitOrg, s.RepoName(), resp.Status, msg.Message)
	}
	return false, nil
}

func authorize(r *http.Request, c Credential) {
	r.Header.Set("Authorization", "Bearer "+c.Token)
	r.Header.Set("Accept", "application/vnd.github+json")
	r.Header.Set("X-GitHub-Api-Version", "2022-11-28")
}

// Publish initialises the rendered directory as a repository and pushes it.
//
// The history starts here. The template's history is not carried across: it is
// the platform's, it describes files this repository does not contain, and a
// tenant reading `git log` should see its own box being created rather than the
// platform's development.
func Publish(ctx context.Context, dir string, s Spec, cred Credential) error {
	remote := fmt.Sprintf("https://x-access-token:%s@github.com/%s/%s.git",
		cred.Token, s.GitOrg, s.RepoName())

	steps := [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "."},
		{"-c", "user.name=zero-ops", "-c", "user.email=platform@zero-ops.local",
			"commit", "-q", "-m", "Scaffold " + s.RepoName()},
		{"remote", "add", "origin", remote},
		{"push", "-q", "-u", "origin", "main"},
	}
	for _, args := range steps {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			// The remote carries the credential, so it is replaced rather than
			// printed: a failing push would otherwise write the token into logs
			// and into whatever collects them.
			return fmt.Errorf("git %s: %w: %s", args[0], err,
				strings.ReplaceAll(string(out), cred.Token, "***"))
		}
	}
	return nil
}

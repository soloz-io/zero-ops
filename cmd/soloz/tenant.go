package main

import (
	"bufio"
	"fmt"
	"github.com/soloz-io/zero-ops/internal/soloz-cli/versions"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/soloz-io/zero-ops/internal/soloz-cli/tenant"
)

var (
	scaffoldSpec          tenant.Spec
	scaffoldTemplate      string
	scaffoldOut           string
	scaffoldDryRun        bool
	scaffoldProviderToken string
	scaffoldGitopsToken   string
	scaffoldTailscaleKey  string
	// -1 rather than 0, because 0 is a real worker count and the default depends
	// on the environment, which is not known when flags are declared.
	scaffoldWorkers  int
	scaffoldNoPrompt bool
)

func newTenantCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tenant",
		Short: "Onboard and maintain tenant boxes",
	}
	cmd.AddCommand(newTenantScaffoldCmd())
	return cmd
}

func newTenantScaffoldCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scaffold",
		Short: "Create and render a tenant's <tenant>-gitops repository",
		Long: `Create <org>/<tenant>-gitops and render it from the platform template.

Onboarding, as ADR-062 defines it. The repository is created in the TENANT's
organisation and is theirs; the platform holds a repository-scoped credential
that can propose changes to it and nothing else. No cloud credential is involved:
the tenant runs Day-0 against its own account.

Scaffolding happens once. Every change after it is a pull request against the
rendered repository (ADR-064).

--dry-run renders to a local directory and creates nothing, which is how to see
what a tenant would receive before any repository exists.`,
		RunE: runTenantScaffold,
	}
	f := cmd.Flags()
	f.StringVar(&scaffoldSpec.TenantID, "tenant", "", "tenant identifier (required)")
	f.StringVar(&scaffoldSpec.GitOrg, "org", "", "the tenant's git organisation (required)")
	f.StringVar(&scaffoldSpec.Domain, "domain", "", "the tenant's base domain (required)")
	f.StringVar(&scaffoldSpec.Provider, "provider", "hetzner", "cloud provider")
	f.StringVar(&scaffoldSpec.Region, "region", "hel1", "cloud region")
	f.StringVar(&scaffoldSpec.Environment, "environment", "dev", "environment slug")
	f.IntVar(&scaffoldWorkers, "workers", -1,
		"cloud worker nodes this box starts with (default: 0 in dev, 2 elsewhere). "+
			"A dev box defaults to none because its capacity is meant to come from "+
			"nodes on your own premises; pass a count if you have none (ADR-075)")
	f.StringVar(&scaffoldSpec.ClusterName, "cluster", "", "control plane cluster name (default <tenant>-hub)")
	// Defaults to the version this binary carries (ADR-068). "main" was the old
	// default and is a branch name where a chart version belongs: a repository
	// scaffolded on it pins a version that does not resolve, and the workflow it
	// carries calls a platform workflow at a tag named vmain.
	//
	// A released binary therefore needs no version flag. An unreleased one has no
	// version to offer and says so rather than inventing one.
	f.StringVar(&scaffoldSpec.BundleVersion, "bundle-version", defaultBundleVersion(),
		"platform bundle version this box starts on (default: the version this CLI carries)")
	// Platform facts rather than tenant decisions, so they are hidden. They stay
	// flags because a platform developer testing against a fork needs them, and
	// removing them would mean editing the source to do that.
	f.StringVar(&scaffoldSpec.PlatformRepoURL, "platform-repo", "https://github.com/soloz-io/zero-ops", "where the bundle is sourced from")
	f.StringVar(&scaffoldSpec.BundleRegistry, "bundle-registry", "ghcr.io/soloz-io/charts",
		"registry published bundles are pulled from (no scheme)")
	f.BoolVar(&scaffoldSpec.Private, "private", true, "create the repository private")
	// The tenant's own credentials. Flags so automation can supply them; prompted
	// when a terminal is present and they are absent; and when neither, the run
	// stops after creating the repository and says what remains.
	//
	// Never defaulted from the platform's environment. A token this machine
	// happens to hold is the platform's, and writing it into a tenant's
	// repository would make the platform's access the tenant's access.
	f.StringVar(&scaffoldProviderToken, "provider-token", "",
		"the tenant's cloud API token; their clusters are created with it")
	f.StringVar(&scaffoldTailscaleKey, "tailscale-authkey", "",
		"a Tailscale auth key; the hybrid provider's control plane and home workers join the tenant's tailnet with it (ADR-046)")
	f.StringVar(&scaffoldGitopsToken, "gitops-token", "",
		"write access to the tenant's repository, so Day-0 can commit what it generates")
	f.BoolVar(&scaffoldNoPrompt, "no-prompt", false,
		"never prompt; stop after creating the repository if a secret is missing")

	f.StringVar(&scaffoldTemplate, "template", "manifests/tenants/gitops-template", "template to render")

	for _, hidden := range []string{"platform-repo", "bundle-registry", "template"} {
		_ = f.MarkHidden(hidden)
	}
	f.StringVar(&scaffoldOut, "out", "", "render here instead of a temporary directory")
	f.BoolVar(&scaffoldDryRun, "dry-run", false, "render only; create and push nothing")
	return cmd
}

func runTenantScaffold(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	if scaffoldSpec.ClusterName == "" && scaffoldSpec.TenantID != "" {
		scaffoldSpec.ClusterName = scaffoldSpec.TenantID + "-hub"
	}
	// Only when actually passed. The sentinel keeps "zero workers" distinguishable
	// from "no opinion", so the environment's own default survives.
	if scaffoldWorkers >= 0 {
		scaffoldSpec.Workers = &scaffoldWorkers
	}

	dir := scaffoldOut
	if dir == "" {
		var err error
		if dir, err = os.MkdirTemp("", "tenant-scaffold-"); err != nil {
			return err
		}
		if !scaffoldDryRun {
			defer os.RemoveAll(dir)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	if err := tenant.Render(scaffoldTemplate, dir, scaffoldSpec); err != nil {
		return err
	}
	fmt.Printf("[scaffold] rendered %s into %s\n", scaffoldSpec.RepoName(), dir)

	// Before the dry-run return, so the run that exists to show what would be
	// created also shows that it could not bootstrap.
	if w := scaffoldSpec.CapacityWarning(); w != "" {
		fmt.Printf("\n[scaffold] ⚠  %s\n\n", w)
	}

	if scaffoldDryRun {
		fmt.Println("[scaffold] dry run: no repository created, nothing pushed")
		return printTree(dir)
	}

	root, err := os.Getwd()
	if err != nil {
		return err
	}
	cred, err := tenant.TokenFromEnv(root)
	if err != nil {
		return err
	}
	// The KIND of credential, never its source. Naming the source printed the
	// filesystem path of a secret file to the terminal and to whatever collects
	// it; naming nothing at all made the repository appear out of nowhere and the
	// prompts that follow read as though they had authorised it. They had not:
	// this is the platform's credential and it creates the repository, while the
	// secrets asked for below are the tenant's and are only stored in it.
	//
	// A failure still names the path, because there it is the fix rather than an
	// aside.
	kind := "app installation token"
	if !cred.IsApp {
		kind = "personal access token"
	}
	fmt.Printf("[scaffold] authenticating as the platform (%s)\n", kind)

	existed, err := tenant.CreateRepo(ctx, scaffoldSpec, cred)
	if err != nil {
		return err
	}
	if existed {
		// Resumable rather than fatal: a run that failed after creating the
		// repository but before pushing must be able to continue. Publish refuses
		// once the repository has commits, so a repository that was only created
		// is resumed and one that was scaffolded is not overwritten.
		fmt.Printf("[scaffold] %s/%s already exists; continuing\n", scaffoldSpec.GitOrg, scaffoldSpec.RepoName())
	} else {
		fmt.Printf("[scaffold] created %s/%s\n", scaffoldSpec.GitOrg, scaffoldSpec.RepoName())
	}

	if err := tenant.Publish(ctx, dir, scaffoldSpec, cred); err != nil {
		return err
	}
	fmt.Printf("[scaffold] pushed to %s\n", "https://github.com/"+scaffoldSpec.GitOrg+"/"+scaffoldSpec.RepoName())

	// The repository can bootstrap itself, but only with the tenant's own
	// secrets: the workflow runs under them and the platform holds neither
	// (ADR-065, ADR-072). Asked for here rather than left as a follow-up, because
	// a scaffolded repository that cannot bootstrap looks finished.
	secrets := tenant.Secrets{
		ProviderToken:    scaffoldProviderToken,
		GitopsToken:      scaffoldGitopsToken,
		TailscaleAuthkey: scaffoldTailscaleKey,
	}
	if !scaffoldNoPrompt {
		var err error
		if secrets, err = secrets.Prompt(scaffoldSpec.Provider); err != nil {
			return err
		}
	}

	w := bufio.NewWriter(os.Stdout)
	// Provider-aware: a hybrid box also needs a tailnet key, and dispatching
	// without one starts a run the workflow refuses on purpose.
	if !secrets.CompleteFor(scaffoldSpec.Provider) {
		// Not an error. A repository exists at this point, so what is owed is an
		// accurate account of where this stopped and what completes it.
		tenant.HandoverInstructions(scaffoldSpec, secrets, w)
		return nil
	}

	if err := tenant.SetSecrets(ctx, scaffoldSpec, secrets); err != nil {
		return err
	}
	if err := tenant.Dispatch(ctx, scaffoldSpec); err != nil {
		return err
	}

	repo := scaffoldSpec.GitOrg + "/" + scaffoldSpec.RepoName()
	fmt.Printf("\n[scaffold] bootstrap dispatched on %s\n", repo)
	fmt.Printf("  https://github.com/%s/actions/workflows/bootstrap-cluster.yml\n\n", repo)
	fmt.Printf("It runs for a few hours. Follow it with:\n")
	fmt.Printf("  gh run watch --repo %s\n", repo)
	return nil
}

func printTree(dir string) error {
	return filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		fmt.Println("  ", rel)
		return nil
	})
}

// defaultBundleVersion is the version this binary carries, or empty when it
// carries none.
//
// Empty rather than a guess: an unreleased build has no published version to
// offer, and scaffolding a repository pinned to something that does not resolve
// produces a box that cannot bootstrap and says nothing about why until the
// first Application fails to load its source.
func defaultBundleVersion() string {
	if versions.IsReleaseBuild() {
		return versions.BundleVersion
	}
	return ""
}

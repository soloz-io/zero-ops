package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/soloz-io/zero-ops/internal/hub-cli/tenant"
)

var (
	scaffoldSpec     tenant.Spec
	scaffoldTemplate string
	scaffoldOut      string
	scaffoldDryRun   bool
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
	f.StringVar(&scaffoldSpec.ClusterName, "cluster", "", "control plane cluster name (default <tenant>-hub)")
	f.StringVar(&scaffoldSpec.BundleVersion, "bundle-version", "main", "platform bundle version this box starts on")
	f.StringVar(&scaffoldSpec.PlatformRepoURL, "platform-repo", "https://github.com/soloz-io/zero-ops", "where the bundle is sourced from")
	f.StringVar(&scaffoldSpec.BundleRegistry, "bundle-registry", "ghcr.io/soloz-io/charts",
		"registry published bundles are pulled from (no scheme)")
	f.BoolVar(&scaffoldSpec.Private, "private", true, "create the repository private")
	f.StringVar(&scaffoldTemplate, "template", "manifests/tenants/gitops-template", "template to render")
	f.StringVar(&scaffoldOut, "out", "", "render here instead of a temporary directory")
	f.BoolVar(&scaffoldDryRun, "dry-run", false, "render only; create and push nothing")
	return cmd
}

func runTenantScaffold(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	if scaffoldSpec.ClusterName == "" && scaffoldSpec.TenantID != "" {
		scaffoldSpec.ClusterName = scaffoldSpec.TenantID + "-hub"
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
	kind := "personal access token"
	if cred.IsApp {
		kind = "app installation token"
	}
	fmt.Printf("[scaffold] authenticating with %s from %s\n", kind, cred.Source)

	existed, err := tenant.CreateRepo(ctx, scaffoldSpec, cred)
	if err != nil {
		return err
	}
	if existed {
		// Resumable rather than fatal: a run that failed after creating the
		// repository must be able to continue. Pushing into it is still refused
		// below if it already has history, so this cannot overwrite a tenant's
		// work.
		fmt.Printf("[scaffold] %s/%s already exists; continuing\n", scaffoldSpec.GitOrg, scaffoldSpec.RepoName())
	} else {
		fmt.Printf("[scaffold] created %s/%s\n", scaffoldSpec.GitOrg, scaffoldSpec.RepoName())
	}

	if err := tenant.Publish(ctx, dir, scaffoldSpec, cred); err != nil {
		return err
	}
	fmt.Printf("[scaffold] pushed to %s\n", "https://github.com/"+scaffoldSpec.GitOrg+"/"+scaffoldSpec.RepoName())
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

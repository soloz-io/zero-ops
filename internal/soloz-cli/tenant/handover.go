package tenant

import (
	"bufio"
	"context"
	"fmt"
	"github.com/soloz-io/zero-ops/internal/soloz-cli/versions"
	"io"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/term"
)

// ProviderCredential is the secret name a provider's clusters are created with.
//
// The name belongs to the provider, not to this platform: HCLOUD_TOKEN is what
// Hetzner's own tooling reads, and a tenant looking at its repository secrets
// should see a name it recognises from the provider's documentation rather than
// one invented here.
func ProviderCredential(provider string) (string, error) {
	switch provider {
	case "hetzner", "hybrid":
		// A hybrid cell is a Hetzner control plane with home-lab workers
		// (ADR-046), so it is created with the same credential.
		return "HCLOUD_TOKEN", nil
	default:
		return "", fmt.Errorf("no credential is known for provider %q; the "+
			"scaffolded repository will need its cloud secret set by hand", provider)
	}
}

// Secrets are what a tenant's repository needs to bootstrap itself.
//
// Held together because they are set together and because a repository with one
// of them cannot bootstrap: the workflow fails at the step that needs the other,
// after the tenant has been told the setup succeeded.
type Secrets struct {
	// ProviderToken creates clusters. It never leaves the tenant's account.
	ProviderToken string
	// GitopsToken lets Day-0 commit the artifacts it generates (ADR-072).
	GitopsToken string
	// TailscaleAuthkey enrols this box's control plane and its home workers on
	// the tenant's tailnet. Hybrid only: ADR-046 invariant 6 requires a routable
	// tailnet IP on every node that carries pod traffic, and under §21 that
	// includes the hub control plane. Empty for every other provider, where
	// nothing reads it.
	TailscaleAuthkey string

	// EscrowURL, EscrowClientID, EscrowClientSecret and EscrowProjectID reach an
	// Infisical the TENANT controls and this box does not host -- Infisical Cloud,
	// or one they run elsewhere.
	//
	// It holds the box's Infisical master keys: the root secret without which the
	// box's own secret store cannot be decrypted, and every credential the platform
	// manages is lost with the cluster (ADR-076).
	//
	// It cannot be the box's own Infisical, which is the thing those keys unlock and
	// is unreachable exactly when they are needed. And it is the tenant's rather
	// than the platform's, because ADR-065 says the platform holds no secret
	// credential belonging to a tenant -- least of all the one that unlocks the rest.
	EscrowURL          string
	EscrowClientID     string
	EscrowClientSecret string
	EscrowProjectID    string
}

// hasEscrow reports whether a complete escrow credential was supplied.
//
// Complete or absent, never partial: hub-operator reads all four from one Secret,
// and three of four produces an operator that attempts a backup on every reconcile
// and fails -- an escrow that appears to exist and does not work.
func (s Secrets) hasEscrow() bool {
	return strings.TrimSpace(s.EscrowURL) != "" &&
		strings.TrimSpace(s.EscrowClientID) != "" &&
		strings.TrimSpace(s.EscrowClientSecret) != "" &&
		strings.TrimSpace(s.EscrowProjectID) != ""
}

// needsTailscale reports whether a provider's clusters join a tailnet.
//
// Only hybrid does. Asking a hetzner tenant for a Tailscale key would be asking
// for a credential nothing consumes, and skipping the question for a hybrid one
// produces a box whose control plane silently never joins the tailnet.
func needsTailscale(provider string) bool {
	return strings.TrimSpace(provider) == "hybrid"
}

// Complete reports whether both are present. A repository is only dispatchable
// with both, so this is the test for whether scaffolding can go further than
// creating the repository.
func (s Secrets) Complete() bool {
	return strings.TrimSpace(s.ProviderToken) != "" && strings.TrimSpace(s.GitopsToken) != ""
}

// CompleteFor is Complete plus whatever the provider additionally requires.
// Dispatching a hybrid box without a tailnet key produces a control plane that
// never joins the tailnet, which is not a failure the run reports.
func (s Secrets) CompleteFor(provider string) bool {
	if !s.Complete() {
		return false
	}
	return !needsTailscale(provider) || strings.TrimSpace(s.TailscaleAuthkey) != ""
}

// Prompt asks for whichever secret is missing.
//
// Reads without echo, because a token typed into a terminal that echoes it is a
// token in a scrollback buffer and, on a shared screen, in someone else's
// memory. Returns what it has when there is no terminal to ask -- a pipeline
// gets the flags it passed and no prompt that would hang it.
func (s Secrets) Prompt(provider string) (Secrets, error) {
	if s.CompleteFor(provider) || !term.IsTerminal(int(os.Stdin.Fd())) {
		return s, nil
	}

	credName, err := ProviderCredential(provider)
	if err != nil {
		credName = "the provider's API token"
	}

	// Said before the first prompt, because the order otherwise reads backwards:
	// the repository already exists by this point, created with the platform's own
	// credential, and these are a different set entirely. They are stored on that
	// repository for the workflow to use later -- nothing here authenticates with
	// them, and the platform keeps no copy (ADR-065).
	fmt.Println("\nThe tenant's own secrets, stored on their repository for Day-0 to use.")
	fmt.Println("They are not used here, and the platform retains none of them.")

	if strings.TrimSpace(s.ProviderToken) == "" {
		v, err := readSecret(fmt.Sprintf(
			"%s (creates the tenant's clusters; stays in their account): ", credName))
		if err != nil {
			return s, err
		}
		s.ProviderToken = v
	}
	if strings.TrimSpace(s.GitopsToken) == "" {
		v, err := readSecret(
			"GITOPS_TOKEN (write access to the tenant's repository, so Day-0 can commit): ")
		if err != nil {
			return s, err
		}
		s.GitopsToken = v
	}
	// Hybrid only. Asked here rather than left for the tenant to discover,
	// because a box that bootstraps without it reports success and then cannot
	// pass pod traffic between its control plane and its home workers.
	if needsTailscale(provider) && strings.TrimSpace(s.TailscaleAuthkey) == "" {
		v, err := readSecret(
			"TS_AUTHKEY (a Tailscale auth key; the control plane and home workers join your tailnet with it): ")
		if err != nil {
			return s, err
		}
		s.TailscaleAuthkey = v
	}

	// The escrow. Asked for every box, because what it protects is not
	// provider-specific: lose the cluster and you lose its Infisical master keys
	// and its admin kubeconfig with it, and Infisical cannot hold either -- it
	// runs inside the box it would be protecting.
	//
	// Skippable, and skipping it is answered rather than ignored: an empty first
	// answer ends the question and the handover says what the box gives up.
	// The escrow. Required, not offered: a box without one keeps its Infisical
	// master keys only inside the cluster those keys decrypt, so losing the
	// cluster loses every secret the platform manages for that tenant. The cost of
	// skipping is paid entirely later, by someone who did not make the choice.
	if !s.hasEscrow() {
		fmt.Println("\nAn escrow, on an Infisical you control and this box does not host.")
		fmt.Println("Infisical Cloud (https://app.infisical.com) is the usual answer.")
		fmt.Println("It holds this box's master keys, which cannot be kept inside the box")
		fmt.Println("they decrypt. Without it, losing the cluster loses its secrets.")

		var err error
		if strings.TrimSpace(s.EscrowURL) == "" {
			if s.EscrowURL, err = readSecret(
				"INFISICAL_ESCROW_URL [https://app.infisical.com]: "); err != nil {
				return s, err
			}
			if strings.TrimSpace(s.EscrowURL) == "" {
				// The common case typed as an empty line rather than a URL.
				s.EscrowURL = "https://app.infisical.com"
			}
		}
		if strings.TrimSpace(s.EscrowProjectID) == "" {
			if s.EscrowProjectID, err = readSecret(
				"INFISICAL_ESCROW_PROJECT_ID (the project the backup is written to): "); err != nil {
				return s, err
			}
		}
		if strings.TrimSpace(s.EscrowClientID) == "" {
			if s.EscrowClientID, err = readSecret(
				"INFISICAL_ESCROW_CLIENT_ID (a machine identity with write access to it): "); err != nil {
				return s, err
			}
		}
		if strings.TrimSpace(s.EscrowClientSecret) == "" {
			if s.EscrowClientSecret, err = readSecret("INFISICAL_ESCROW_CLIENT_SECRET: "); err != nil {
				return s, err
			}
		}
	}
	return s, nil
}

func readSecret(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("reading the value: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

// SetSecrets writes the secrets into the tenant's repository.
//
// Through `gh secret set` reading stdin rather than an argument, so the value
// never appears in this process's command line -- where anything on the machine
// can read it out of the process table for as long as the call runs.
func SetSecrets(ctx context.Context, spec Spec, s Secrets) error {
	credName, err := ProviderCredential(spec.Provider)
	if err != nil {
		return err
	}
	repo := fmt.Sprintf("%s/%s", spec.GitOrg, spec.RepoName())

	values := map[string]string{
		credName:       s.ProviderToken,
		"GITOPS_TOKEN": s.GitopsToken,
	}
	if v := strings.TrimSpace(s.TailscaleAuthkey); v != "" {
		values["TS_AUTHKEY"] = v
	}
	// All three or none: a partial set produces a box that believes it has an
	// escrow and cannot reach it, which is worse than having none.
	if s.hasEscrow() {
		values["INFISICAL_ESCROW_URL"] = strings.TrimSpace(s.EscrowURL)
		values["INFISICAL_ESCROW_CLIENT_ID"] = strings.TrimSpace(s.EscrowClientID)
		values["INFISICAL_ESCROW_CLIENT_SECRET"] = strings.TrimSpace(s.EscrowClientSecret)
		values["INFISICAL_ESCROW_PROJECT_ID"] = strings.TrimSpace(s.EscrowProjectID)
	}

	for name, value := range values {
		cmd := exec.CommandContext(ctx, "gh", "secret", "set", name, "--repo", repo)
		cmd.Stdin = strings.NewReader(value)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("setting %s on %s: %w\n%s", name, repo, err, out)
		}
		fmt.Printf("[scaffold] ✓ %s set on %s\n", name, repo)
	}
	return nil
}

// Dispatch starts the bootstrap workflow in the tenant's repository.
func Dispatch(ctx context.Context, spec Spec) error {
	repo := fmt.Sprintf("%s/%s", spec.GitOrg, spec.RepoName())
	cmd := exec.CommandContext(ctx, "gh", "workflow", "run", "bootstrap-cluster.yml",
		"--repo", repo,
		"-f", "cluster="+spec.ClusterName,
		"-f", "provider="+spec.Provider,
		"-f", "region="+spec.Region,
		"-f", "environment="+spec.Environment)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("dispatching the bootstrap workflow on %s: %w\n%s", repo, err, out)
	}
	return nil
}

// HandoverInstructions prints what is left for the tenant to do.
//
// Printed rather than performed when a secret is absent. Scaffolding has already
// created a repository at this point, so the run cannot simply fail: what it owes
// is an accurate account of where it stopped and what completes it.
func HandoverInstructions(spec Spec, s Secrets, w *bufio.Writer) {
	defer w.Flush()
	repo := fmt.Sprintf("%s/%s", spec.GitOrg, spec.RepoName())
	credName, err := ProviderCredential(spec.Provider)
	if err != nil {
		credName = "<PROVIDER_TOKEN>"
	}

	fmt.Fprintf(w, "\n%s is scaffolded and pinned to %s.\n", repo, spec.BundleVersion)
	fmt.Fprintf(w, "  https://github.com/%s\n\n", repo)

	if !s.CompleteFor(spec.Provider) {
		fmt.Fprintf(w, "It cannot bootstrap yet: the workflow it carries runs under the\n")
		fmt.Fprintf(w, "tenant's own secrets, and %s.\n\n", missingWord(s, credName, spec.Provider))
		if strings.TrimSpace(s.ProviderToken) == "" {
			fmt.Fprintf(w, "  gh secret set %s --repo %s\n", credName, repo)
		}
		if strings.TrimSpace(s.GitopsToken) == "" {
			fmt.Fprintf(w, "  gh secret set GITOPS_TOKEN --repo %s\n", repo)
		}
		if needsTailscale(spec.Provider) && strings.TrimSpace(s.TailscaleAuthkey) == "" {
			fmt.Fprintf(w, "  gh secret set TS_AUTHKEY --repo %s\n", repo)
		}
		fmt.Fprintln(w)
	}

	// Said whether or not anything else is missing: a box without an escrow
	// bootstraps and runs, so nothing else will ever mention it, and the moment it
	// matters is the moment it cannot be added.
	if !s.hasEscrow() {
		fmt.Fprintf(w, "This box has no escrow.\n\n")
		fmt.Fprintf(w, "hub-operator copies this box's Infisical master keys -- the root secret\n")
		fmt.Fprintf(w, "without which its secret store cannot be decrypted -- to an Infisical you\n")
		fmt.Fprintf(w, "control. The box's own Infisical cannot hold them: they are the keys that\n")
		fmt.Fprintf(w, "decrypt it, and it is unreachable exactly when they are needed.\n\n")
		fmt.Fprintf(w, "Without it, losing this cluster loses every secret the platform manages\n")
		fmt.Fprintf(w, "for it, and the escrow cannot be added after the fact.\n\n")
		for _, name := range []string{
			"INFISICAL_ESCROW_URL",
			"INFISICAL_ESCROW_PROJECT_ID",
			"INFISICAL_ESCROW_CLIENT_ID",
			"INFISICAL_ESCROW_CLIENT_SECRET",
		} {
			fmt.Fprintf(w, "  gh secret set %s --repo %s\n", name, repo)
		}
		fmt.Fprintln(w)
	}

	fmt.Fprintf(w, "Then bootstrap it, in the tenant's repository:\n\n")
	fmt.Fprintf(w, "  gh workflow run bootstrap-cluster.yml --repo %s\n", repo)
	fmt.Fprintf(w, "  https://github.com/%s/actions/workflows/bootstrap-cluster.yml\n", repo)
}

// missingWord names what is absent. Built from the set rather than switched over
// pairs: with a third secret the cases stop being enumerable, and a sentence
// naming one missing secret while two are missing is worse than no sentence.
func missingWord(s Secrets, credName, provider string) string {
	var missing []string
	if strings.TrimSpace(s.ProviderToken) == "" {
		missing = append(missing, credName)
	}
	if strings.TrimSpace(s.GitopsToken) == "" {
		missing = append(missing, "GITOPS_TOKEN")
	}
	// Only where something reads it. Naming TS_AUTHKEY to a hetzner tenant sends
	// them to obtain a credential nothing on their box consumes.
	if needsTailscale(provider) && strings.TrimSpace(s.TailscaleAuthkey) == "" {
		missing = append(missing, "TS_AUTHKEY")
	}
	switch len(missing) {
	case 0:
		return "everything it needs is set"
	case 1:
		return missing[0] + " is not set"
	case 2:
		return missing[0] + " and " + missing[1] + " are not set"
	default:
		return strings.Join(missing[:len(missing)-1], ", ") + " and " +
			missing[len(missing)-1] + " are not set"
	}
}

// LocalHandover clones the repository and prints the bootstrap to run against it.
//
// The dispatch path runs Day-0 on a runner, from a released binary, against a
// checkout the runner makes. This runs the same Day-0 from the working tree,
// against a clone on this machine. Everything the cluster reconciles is the same:
// the repository is the same repository, the values are the same values, and the
// seed is the same declaration.
//
// What it buys is not having to publish. A version is consumed by any release that
// begins publishing it (ADR-063), so testing a change through the dispatch path
// spends a version number on every iteration.
func LocalHandover(ctx context.Context, s Spec, w io.Writer) error {
	dir := s.RepoName()
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("%s already exists here.\n\n"+
			"Remove it or run from elsewhere: a stale clone would be bootstrapped "+
			"instead of the repository just created, and the difference is not "+
			"visible in the output", dir)
	}

	// Cloned rather than rendered in place: Day-0 commits the artifacts it
	// generates and pushes them (ADR-045, ADR-072), so it needs a working tree
	// with a remote, not a directory of files.
	clone := exec.CommandContext(ctx, "git", "clone", "-q",
		"https://github.com/"+s.GitOrg+"/"+s.RepoName()+".git", dir)
	if out, err := clone.CombinedOutput(); err != nil {
		return fmt.Errorf("clone %s: %w\n%s", s.RepoName(), err, out)
	}

	fmt.Fprintf(w, "\n[scaffold] cloned %s into ./%s\n\n", s.RepoName(), dir)
	fmt.Fprintf(w, "Bootstrap it from here, not from a workflow:\n\n")
	// The token is exported rather than left to the file fallback: that file
	// lives in the platform checkout, and the command below runs from the
	// tenant's clone.
	fmt.Fprintf(w, "  export HCLOUD_TOKEN=$(cat <your checkout>/k8-secrets/hetzner/token)\n")
	fmt.Fprintf(w, "  cd %s\n", dir)
	fmt.Fprintf(w, "  %s bootstrap \\\n", "soloz")
	fmt.Fprintf(w, "    --name %s \\\n", s.ClusterName)
	fmt.Fprintf(w, "    --provider %s \\\n", s.Provider)
	fmt.Fprintf(w, "    --region %s \\\n", s.Region)
	fmt.Fprintf(w, "    --environment %s \\\n", s.Environment)
	fmt.Fprintf(w, "    --gitops-dir .\n\n")

	if s.BundleVersion == versions.DevelopmentBundle {
		fmt.Fprintf(w, "This box reads its platform chart from %s at %q, so the\n",
			s.PlatformRepoURL, s.developmentRevision())
		fmt.Fprintf(w, "change you are testing must be pushed to that branch before the\n")
		fmt.Fprintf(w, "cluster can reconcile it. The CLI reads your working tree; ArgoCD\n")
		fmt.Fprintf(w, "does not.\n\n")
		fmt.Fprintf(w, "This is NOT the path a tenant runs. To test that one, publish a\n")
		fmt.Fprintf(w, "prerelease and scaffold against it -- see\n")
		fmt.Fprintf(w, "docs/runbooks/local-release-path-testing.md.\n\n")
		return nil
	}

	// Worth saying out loud on the released path: the cluster reconciles what was
	// published, so an unpublished edit to the working tree changes nothing here
	// and the run looks normal while testing the wrong content.
	fmt.Fprintf(w, "This box pins %s and pulls its charts from %s.\n",
		s.BundleVersion, s.BundleRegistry)
	fmt.Fprintf(w, "That is the path a tenant runs. Your working tree is not in it:\n")
	fmt.Fprintf(w, "changing a manifest means publishing the next prerelease.\n\n")
	return nil
}

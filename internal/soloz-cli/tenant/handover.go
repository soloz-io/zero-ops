package tenant

import (
	"bufio"
	"context"
	"fmt"
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

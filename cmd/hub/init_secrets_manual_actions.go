package main

import (
	"fmt"

	"github.com/soloz-io/zero-ops/internal/hub-cli/infisical"
)

func printStepBanner(section, title, goal, duration string) {
	fmt.Println()
	fmt.Println("──────────────────────────────────────────────────────────────────────")
	fmt.Printf("  Step %s: %s\n", section, title)
	fmt.Printf("  Goal: %s\n", goal)
	if duration != "" {
		fmt.Printf("  Duration: %s\n", duration)
	}
	fmt.Println("──────────────────────────────────────────────────────────────────────")
}

func printManualActions(info *infisical.AccessInfo) {
	fmt.Println()
	fmt.Println("======================================================================")
	fmt.Println("  MANUAL ACTION REQUIRED")
	fmt.Println("======================================================================")
	fmt.Println()

	fmt.Println("  Step 1 — Open Infisical UI:")
	fmt.Println()

	for i, opt := range info.Options {
		fmt.Printf("    %d. %s\n", i+1, opt.Name)
		fmt.Printf("       %s\n", opt.URL)
		if opt.SetupCmd != "" {
			fmt.Printf("       $ %s\n", opt.SetupCmd)
		}
	}

	fmt.Println()
	fmt.Println("  Step 2 — Log in:")
	fmt.Printf("    Email:    %s\n", info.AdminEmail)
	fmt.Printf("    Password: %s\n", info.Password)
	fmt.Println()

	fmt.Println("  Step 3 — Complete missing resources:")
	fmt.Println()
	fmt.Println("    A. Navigate to the hub-platform project and verify it exists.")
	fmt.Println("       If missing, re-run 'hub init-secrets' to auto-create it.")
	fmt.Println()
	fmt.Println("    B. Navigate to Identities and verify Machine Identity exists.")
	fmt.Println("       If missing, re-run 'hub init-secrets' to auto-create it.")
	fmt.Println()
	fmt.Println("    C. Navigate to Certificates → Profiles.")
	fmt.Println("       If 'argocd-bootstrap' profile is missing:")
	fmt.Println("         - Click 'Create Profile'")
	fmt.Println("         - Slug: argocd-bootstrap")
	fmt.Println("         - CA: Fleet Intermediate CA (NOT platform-db-ca, per ADR-035)")
	fmt.Println("         - Validity: 10 years")
	fmt.Println("         - Click 'Create'")
	fmt.Println()

	fmt.Println("  After completing the steps above, re-run:")
	fmt.Println("    $ hub init-secrets --kubeconfig=<path>")
	fmt.Println()
	fmt.Println("======================================================================")
	fmt.Println()
}

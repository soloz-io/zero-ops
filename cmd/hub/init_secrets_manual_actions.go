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
	fmt.Println("====================== MANUAL ACTION INFO ======================")
	fmt.Println()
	fmt.Println("  Access the Infisical UI for troubleshooting or manual actions:")
	fmt.Println()

	for i, opt := range info.Options {
		fmt.Printf("  %d. %s\n", i+1, opt.Name)
		fmt.Printf("     %s\n", opt.URL)
		if opt.SetupCmd != "" {
			fmt.Printf("     $ %s\n", opt.SetupCmd)
		}
		fmt.Println()
	}

	fmt.Println("  Login credentials:")
	fmt.Printf("    Email:    %s\n", info.AdminEmail)
	fmt.Printf("    Password: %s\n", info.Password)
	fmt.Println()
	fmt.Println("  If a step failed, fix the issue in the Infisical UI, then re-run:")
	fmt.Println("    $ hub init-secrets --kubeconfig=<path>")
	fmt.Println()
	fmt.Println("================================================================")
	fmt.Println()
}

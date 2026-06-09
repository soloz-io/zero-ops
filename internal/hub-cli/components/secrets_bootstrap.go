package components

import (
	"context"
	"fmt"

	"github.com/soloz-io/zero-ops/internal/hub-cli/infisical"
)

// GenerateLocalSecrets generates the initial cryptographic secrets needed before
// infrastructure workloads (CNPG, Redis) can boot. This runs between B01 and B02
// so that platform-db-app exists when CNPG's initdb runs.
//
// Step 1: GenerateInfisicalCryptoSecrets (encryption keys, Redis pw, platform-db-app)
func (i *Installer) GenerateLocalSecrets(ctx context.Context) error {
	fmt.Println("Generating local bootstrap secrets (Phase B01→B02)...")

	if err := i.GenerateInfisicalCryptoSecrets(ctx); err != nil {
		return fmt.Errorf("failed to generate infisical crypto secrets: %w", err)
	}

	fmt.Println("✓ Local bootstrap secrets ready")
	return nil
}

// BootstrapInfisicalAPI waits for CNPG to be healthy, injects DB_ROOT_CERT into
// infisical-secrets, then bootstraps the Infisical API (Org, Project, Machine Identity)
// and stores Layer 1+2 credentials. This runs after B03 when the Infisical Helm chart
// is deployed and the CNPG cluster is fully Ready.
//
//	Step 1: UpdateInfisicalSecretsWithCNPGCert (inject DB_ROOT_CERT)
//	Step 2: InstallPostgresConnectionSecret (infisical-db-credentials)
//	Step 3: WaitForInfisicalHealth (data layer + Infisical pods)
//	Step 4: InstallInfisicalAuthFromInfisical (Org, Project, Machine Identity)
//	Step 5: InstallPlatformDatabaseCredentials (layer 2 creds via Infisical API)
//	Step 6: InstallSPIREServerCredentials
//
// Port-forward lifecycle is managed internally for local clusters.
func (i *Installer) BootstrapInfisicalAPI(ctx context.Context) error {
	fmt.Println("Bootstrapping Infisical API (Phase post-B03)...")

	// Step 1: Inject CNPG CA certificate into infisical-secrets
	if err := i.UpdateInfisicalSecretsWithCNPGCert(ctx); err != nil {
		return fmt.Errorf("failed to inject CNPG CA certificate: %w", err)
	}

	// Step 2: Create Infisical PostgreSQL connection secret
	changed2, err := i.InstallPostgresConnectionSecret(ctx)
	if err != nil {
		return fmt.Errorf("failed to install postgres connection secret: %w", err)
	}

	// Step 3: Wait for Infisical to become healthy
	if err := i.WaitForInfisicalHealth(ctx); err != nil {
		fmt.Println("Warning: Infisical not yet healthy. Skipping credential storage in Infisical.")
		if changed2 {
			if err := i.RestartPlatformWorkloads(ctx); err != nil {
				return fmt.Errorf("failed to restart workloads: %w", err)
			}
		}
		return nil
	}

	// Step 4: Bootstrap Infisical (Org, Project, Machine Identity)
	if _, err := i.InstallInfisicalAuthFromInfisical(ctx); err != nil {
		return fmt.Errorf("failed to bootstrap Infisical: %w", err)
	}

	// Establish Infisical API access via port-forward
	pf := infisical.NewPortForwardManager(
		"platform-security", "infisical-standalone-infisical",
		"8081", "8080", i.Kubeconfig,
	)
	var pfStarted bool
	if err := pf.EnsureAPIAccess(ctx); err != nil {
		return fmt.Errorf("failed to establish Infisical API access: %w", err)
	}
	pfStarted = true

	// Step 5: Store platform database credentials in Infisical
	changed3, err := i.InstallPlatformDatabaseCredentials(ctx)
	if err != nil {
		if pfStarted {
			pf.Stop()
		}
		return fmt.Errorf("failed to install platform database credentials: %w", err)
	}

	// Step 6: Store SPIRE Server credentials in Infisical
	changed4, err := i.InstallSPIREServerCredentials(ctx)
	if err != nil {
		if pfStarted {
			pf.Stop()
		}
		return fmt.Errorf("failed to install SPIRE Server credentials: %w", err)
	}

	if pfStarted {
		pf.Stop()
	}

	// Restart workloads if secrets changed
	if changed2 || changed3 || changed4 {
		if err := i.RestartPlatformWorkloads(ctx); err != nil {
			return fmt.Errorf("failed to restart workloads: %w", err)
		}
		fmt.Println("✅ Bootstrap complete! New secrets injected and pods rolling out.")
	} else {
		fmt.Println("✅ Bootstrap complete! All dependencies already exist. No pods were restarted.")
	}

	return nil
}

// RunInitSecrets executes the full init-secrets pipeline (for standalone CLI use):
//
//	Step 1: GenerateInfisicalCryptoSecrets (encryption keys, Redis pw, platform-db-app)
//	Step 2: InstallPostgresConnectionSecret (infisical-db-credentials)
//	Step 3: WaitForInfisicalHealth (data layer + Infisical pods)
//	Step 4: InstallInfisicalAuthFromInfisical (Org, Project, Machine Identity)
//	Step 5: InstallPlatformDatabaseCredentials (layer 2 creds via Infisical API)
//	Step 6: InstallSPIREServerCredentials
//
// Port-forward lifecycle is managed internally for local clusters.
//
// NOTE: Unlike the split orchestrator phases (GenerateLocalSecrets + BootstrapInfisicalAPI),
// this combined method is idempotent and safe to run standalone after all boundaries are
// deployed. It handles the "init-secrets" ArgoCD ApplicationSet as well.
func (i *Installer) RunInitSecrets(ctx context.Context) error {
	fmt.Println("Bootstrapping Layer 1 Infrastructure Secrets...")
	fmt.Println("  (Note: Existing secrets are treated as immutable and will not be overwritten)")

	// Step 1: Generate Infisical base cryptographic secrets
	changed1, err := func() (bool, error) {
		if err := i.GenerateInfisicalCryptoSecrets(ctx); err != nil {
			return false, err
		}
		return true, nil
	}()
	if err != nil {
		return fmt.Errorf("failed to generate infisical crypto secrets: %w", err)
	}
	_ = changed1 // kept for interface compatibility

	// Step 2: Create Infisical PostgreSQL connection secret
	changed2, err := i.InstallPostgresConnectionSecret(ctx)
	if err != nil {
		return fmt.Errorf("failed to install postgres connection secret: %w", err)
	}

	// Step 3: Wait for Infisical to become healthy
	if err := i.WaitForInfisicalHealth(ctx); err != nil {
		fmt.Println("Warning: Infisical not yet healthy. Skipping credential storage in Infisical.")
		if changed2 {
			if err := i.RestartPlatformWorkloads(ctx); err != nil {
				return fmt.Errorf("failed to restart workloads: %w", err)
			}
		}
		return nil
	}

	// Step 4: Bootstrap Infisical (Org, Project, Machine Identity)
	if _, err := i.InstallInfisicalAuthFromInfisical(ctx); err != nil {
		return fmt.Errorf("failed to bootstrap Infisical: %w", err)
	}

	// Establish Infisical API access via port-forward
	pf := infisical.NewPortForwardManager(
		"platform-security", "infisical-standalone-infisical",
		"8081", "8080", i.Kubeconfig,
	)
	var pfStarted bool
	if err := pf.EnsureAPIAccess(ctx); err != nil {
		return fmt.Errorf("failed to establish Infisical API access: %w", err)
	}
	pfStarted = true

	// Step 5: Store platform database credentials in Infisical
	changed3, err := i.InstallPlatformDatabaseCredentials(ctx)
	if err != nil {
		if pfStarted {
			pf.Stop()
		}
		return fmt.Errorf("failed to install platform database credentials: %w", err)
	}

	// Step 6: Store SPIRE Server credentials in Infisical
	changed4, err := i.InstallSPIREServerCredentials(ctx)
	if err != nil {
		if pfStarted {
			pf.Stop()
		}
		return fmt.Errorf("failed to install SPIRE Server credentials: %w", err)
	}

	if pfStarted {
		pf.Stop()
	}

	// Restart workloads if secrets changed
	if changed2 || changed3 || changed4 {
		if err := i.RestartPlatformWorkloads(ctx); err != nil {
			return fmt.Errorf("failed to restart workloads: %w", err)
		}
		fmt.Println("✅ Bootstrap complete! New secrets injected and pods rolling out.")
	} else {
		fmt.Println("✅ Bootstrap complete! All dependencies already exist. No pods were restarted.")
	}

	return nil
}

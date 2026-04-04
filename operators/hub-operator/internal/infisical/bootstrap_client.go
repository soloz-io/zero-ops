package infisical

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// BootstrapClient orchestrates Infisical Day 0 initialization
type BootstrapClient struct {
	k8sClient client.Client
	api       *BootstrapAPI
}

// NewBootstrapClient creates a new bootstrap client
func NewBootstrapClient(k8sClient client.Client) *BootstrapClient {
	return &BootstrapClient{
		k8sClient: k8sClient,
		api:       NewBootstrapAPI(),
	}
}

// Bootstrap performs complete Infisical Day 0 initialization
// Returns (true, nil) if bootstrap was performed, (false, nil) if already bootstrapped
func (bc *BootstrapClient) Bootstrap(ctx context.Context) (bool, error) {
	logger := log.FromContext(ctx)

	// Check if infisical-auth secret already exists (idempotency)
	infisicalAuth := &corev1.Secret{}
	err := bc.k8sClient.Get(ctx, client.ObjectKey{
		Name:      SecretInfisicalAuth,
		Namespace: NamespaceOps,
	}, infisicalAuth)

	if err == nil {
		logger.Info("infisical-auth secret already exists, skipping bootstrap")
		return false, nil
	}

	if !errors.IsNotFound(err) {
		return false, fmt.Errorf("failed to check infisical-auth secret: %w", err)
	}

	logger.Info("Starting Infisical bootstrap")

	// Step 1: Bootstrap Infisical (create admin user)
	logger.Info("Step 1: Bootstrapping Infisical admin user")
	bootstrapResp, err := bc.api.Bootstrap(ctx, AdminEmail, AdminPassword, OrganizationName)
	if err != nil {
		return false, fmt.Errorf("failed to bootstrap Infisical: %w", err)
	}

	// If bootstrapResp is nil, Infisical is already bootstrapped
	if bootstrapResp == nil {
		logger.Info("Infisical is already bootstrapped, skipping Phase 0")
		return false, nil
	}

	adminToken := bootstrapResp.Identity.Credentials.Token
	orgID := bootstrapResp.Organization.ID

	logger.Info("Infisical admin user created", "orgID", orgID)

	// Step 2: Create project "hub-platform"
	logger.Info("Step 2: Creating project", "projectName", ProjectName)
	projectResp, err := bc.api.CreateProject(ctx, adminToken, ProjectName, orgID)
	if err != nil {
		return false, fmt.Errorf("failed to create project: %w", err)
	}

	projectID := projectResp.ID
	projectSlug := projectResp.Slug

	logger.Info("Project created", "projectID", projectID, "projectSlug", projectSlug)

	// Step 3: Create machine identity "eso-operator"
	logger.Info("Step 3: Creating machine identity", "identityName", IdentityName)
	identityResp, err := bc.api.CreateIdentity(ctx, adminToken, IdentityName, orgID)
	if err != nil {
		return false, fmt.Errorf("failed to create machine identity: %w", err)
	}

	identityID := identityResp.ID

	logger.Info("Machine identity created", "identityID", identityID)

	// Step 4: Attach Universal Auth to identity
	logger.Info("Step 4: Attaching Universal Auth to identity")
	if err := bc.api.AttachUniversalAuth(ctx, adminToken, identityID); err != nil {
		return false, fmt.Errorf("failed to attach universal auth: %w", err)
	}

	logger.Info("Universal Auth attached")

	// Step 5: Generate client credentials
	logger.Info("Step 5: Generating client credentials")
	credsResp, err := bc.api.GenerateClientCredentials(ctx, adminToken, identityID)
	if err != nil {
		return false, fmt.Errorf("failed to generate client credentials: %w", err)
	}

	clientID := credsResp.ClientID
	clientSecret := credsResp.ClientSecret

	logger.Info("Client credentials generated")

	// Step 6: Grant identity access to project
	logger.Info("Step 6: Granting identity access to project", "role", IdentityRole)
	if err := bc.api.GrantProjectAccess(ctx, adminToken, projectID, identityID, IdentityRole); err != nil {
		return false, fmt.Errorf("failed to grant project access: %w", err)
	}

	logger.Info("Identity granted project access")

	// Step 7: Create infisical-auth secret in hub-platform-ops
	logger.Info("Step 7: Creating infisical-auth secret")
	if err := bc.createInfisicalAuthSecret(ctx, clientID, clientSecret); err != nil {
		return false, fmt.Errorf("failed to create infisical-auth secret: %w", err)
	}

	logger.Info("infisical-auth secret created")

	// Step 8: Create infisical-admin secret (for future admin operations)
	logger.Info("Step 8: Creating infisical-admin secret")
	if err := bc.createInfisicalAdminSecret(ctx, adminToken, orgID, projectID, projectSlug); err != nil {
		return false, fmt.Errorf("failed to create infisical-admin secret: %w", err)
	}

	logger.Info("infisical-admin secret created")

	logger.Info("Infisical bootstrap complete")
	return true, nil
}

// createInfisicalAuthSecret creates the infisical-auth secret for ESO
func (bc *BootstrapClient) createInfisicalAuthSecret(ctx context.Context, clientID, clientSecret string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      SecretInfisicalAuth,
			Namespace: NamespaceOps,
			Labels: map[string]string{
				LabelManagedBy: ValueManagedBy,
				LabelComponent: ValueBootstrap,
			},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			KeyClientID:     clientID,
			KeyClientSecret: clientSecret,
		},
	}

	if err := bc.k8sClient.Create(ctx, secret); err != nil {
		return fmt.Errorf("failed to create secret: %w", err)
	}

	return nil
}

// createInfisicalAdminSecret creates the infisical-admin secret for future admin operations
func (bc *BootstrapClient) createInfisicalAdminSecret(ctx context.Context, adminToken, orgID, projectID, projectSlug string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      SecretInfisicalAdmin,
			Namespace: NamespaceOps,
			Labels: map[string]string{
				LabelManagedBy: ValueManagedBy,
				LabelComponent: ValueBootstrap,
			},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			KeyAdminToken:  adminToken,
			KeyAdminEmail:  AdminEmail,
			KeyOrgID:       orgID,
			KeyProjectID:   projectID,
			KeyProjectSlug: projectSlug,
		},
	}

	if err := bc.k8sClient.Create(ctx, secret); err != nil {
		return fmt.Errorf("failed to create secret: %w", err)
	}

	return nil
}

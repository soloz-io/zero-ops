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
	k8sClient         client.Client
	uncachedK8sClient client.Client
	api               *BootstrapAPI
}

// NewBootstrapClient creates a new bootstrap client
func NewBootstrapClient(k8sClient, uncachedK8sClient client.Client) *BootstrapClient {
	return &BootstrapClient{
		k8sClient:         k8sClient,
		uncachedK8sClient: uncachedK8sClient,
		api:               NewBootstrapAPI(),
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

	var adminToken string
	var orgID string

	// Step 0: Try bootstrap API first (for fresh Infisical instances)
	logger.Info("Step 0: Attempting Infisical bootstrap (creates first admin user)")
	bootstrapResp, err := bc.api.Bootstrap(ctx, AdminEmail, AdminPassword, OrganizationName)
	if err != nil {
		return false, fmt.Errorf("bootstrap API failed: %w", err)
	}
	
	if bootstrapResp == nil {
		return false, fmt.Errorf("infisical instance already bootstrapped - manual intervention required. Please delete infisical-auth secret to retry or manually configure the instance")
	}

	// Bootstrap succeeded - extract token and orgID
	adminToken = bootstrapResp.Identity.Credentials.Token
	orgID = bootstrapResp.Organization.ID
	logger.Info("Bootstrap successful (fresh instance)", "orgID", orgID)

	// Step 4: Create project "hub-platform"
	logger.Info("Step 4: Creating project", "projectName", ProjectName)
	projectResp, err := bc.api.CreateProject(ctx, adminToken, ProjectName, orgID)
	if err != nil {
		return false, fmt.Errorf("failed to create project: %w", err)
	}

	projectID := projectResp.Project.ID
	generatedSlug := projectResp.Project.Slug

	logger.Info("Project created", "projectID", projectID, "generatedSlug", generatedSlug)

	// Step 4.1: Update project slug to match constant
	logger.Info("Step 4.1: Updating project slug", "from", generatedSlug, "to", ProjectSlug)
	if err := bc.api.UpdateProjectSlug(ctx, adminToken, projectID, ProjectSlug); err != nil {
		return false, fmt.Errorf("failed to update project slug: %w", err)
	}

	projectSlug := ProjectSlug
	logger.Info("Project slug updated", "projectSlug", projectSlug)

	// Step 4.2: Add admin user to project as admin member
	// Use email directly since the API accepts both usernames and emails
	logger.Info("Step 4.2: Adding admin user to project", "email", AdminEmail)
	if err := bc.api.AddUserToProjectByEmail(ctx, adminToken, projectID, AdminEmail, []string{"admin"}); err != nil {
		return false, fmt.Errorf("failed to add admin user to project: %w", err)
	}

	logger.Info("Admin user added to project", "email", AdminEmail)

	// Step 5: Create machine identity "eso-operator"
	logger.Info("Step 5: Creating machine identity", "identityName", IdentityName)
	identityResp, err := bc.api.CreateIdentity(ctx, adminToken, IdentityName, orgID)
	if err != nil {
		return false, fmt.Errorf("failed to create machine identity: %w", err)
	}

	identityID := identityResp.Identity.ID

	logger.Info("Machine identity created", "identityID", identityID)

	// Step 6: Attach Universal Auth to identity
	logger.Info("Step 6: Attaching Universal Auth to identity")
	if err := bc.api.AttachUniversalAuth(ctx, adminToken, identityID); err != nil {
		return false, fmt.Errorf("failed to attach universal auth: %w", err)
	}

	logger.Info("Universal Auth attached")

	// Step 7: Generate client credentials
	logger.Info("Step 7: Generating client credentials")
	credsResp, err := bc.api.GenerateClientCredentials(ctx, adminToken, identityID)
	if err != nil {
		return false, fmt.Errorf("failed to generate client credentials: %w", err)
	}

	clientSecret := credsResp.ClientSecret

	logger.Info("Client credentials generated")

	// Step 8: Get clientId from universal auth configuration
	logger.Info("Step 8: Retrieving clientId from universal auth configuration")
	uaResp, err := bc.api.GetUniversalAuth(ctx, adminToken, identityID)
	if err != nil {
		return false, fmt.Errorf("failed to get universal auth configuration: %w", err)
	}

	clientID := uaResp.IdentityUniversalAuth.ClientID

	logger.Info("ClientId retrieved", "clientId", clientID)

	// Step 9: Grant identity access to project
	logger.Info("Step 9: Granting identity access to project", "role", IdentityRole)
	if err := bc.api.GrantProjectAccess(ctx, adminToken, projectID, identityID, IdentityRole); err != nil {
		// Log warning but don't fail - user might not have project membership permissions
		logger.Info("Warning: Failed to grant project access (may require manual setup)", "error", err.Error())
	} else {
		logger.Info("Identity granted project access")
	}

	// Step 10: Create infisical-auth secret in platform-ops
	logger.Info("Step 10: Creating infisical-auth secret")
	if err := bc.createInfisicalAuthSecret(ctx, clientID, clientSecret); err != nil {
		return false, fmt.Errorf("failed to create infisical-auth secret: %w", err)
	}

	logger.Info("infisical-auth secret created")

	// Step 11: Create infisical-admin secret (for future admin operations)
	logger.Info("Step 11: Creating infisical-admin secret")
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

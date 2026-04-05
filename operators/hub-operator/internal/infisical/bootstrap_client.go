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

	var adminToken string
	var orgID string

	// Step 0: Try bootstrap API first (for fresh Infisical instances)
	logger.Info("Step 0: Attempting Infisical bootstrap (creates first admin user)")
	bootstrapResp, err := bc.api.Bootstrap(ctx, AdminEmail, AdminPassword, OrganizationName)
	if err == nil {
		// Bootstrap succeeded - extract token and orgID
		adminToken = bootstrapResp.Identity.Credentials.Token
		orgID = bootstrapResp.Organization.ID
		logger.Info("Bootstrap successful (fresh instance)", "orgID", orgID)
	} else {
		// Bootstrap failed - check if already bootstrapped
		logger.Info("Bootstrap API returned error, attempting login flow", "error", err.Error())

		// Step 1: Login to get initial token
		logger.Info("Step 1: Logging in to Infisical")
		loginResp, err := bc.api.Login(ctx, AdminEmail, AdminPassword)
		if err != nil {
			return false, fmt.Errorf("failed to login to Infisical: %w", err)
		}

		initialToken := loginResp.AccessToken
		logger.Info("Login successful")

		// Step 2: List organizations to get orgID
		logger.Info("Step 2: Fetching organization list")
		orgs, err := bc.api.ListOrganizations(ctx, initialToken)
		if err != nil {
			return false, fmt.Errorf("failed to list organizations: %w", err)
		}

		if len(orgs) == 0 {
			return false, fmt.Errorf("no organizations found for user")
		}

		// Use first organization (super admin should have access to the org)
		orgID = orgs[0].ID
		logger.Info("Organization found", "orgID", orgID, "orgName", orgs[0].Name)

		// Step 3: Select organization to get org-scoped token
		logger.Info("Step 3: Selecting organization", "orgID", orgID)
		selectOrgResp, err := bc.api.SelectOrganization(ctx, initialToken, orgID)
		if err != nil {
			return false, fmt.Errorf("failed to select organization: %w", err)
		}

		adminToken = selectOrgResp.Token
		logger.Info("Organization selected successfully")
	}

	// Step 4: Create project "hub-platform"
	logger.Info("Step 4: Creating project", "projectName", ProjectName)
	projectResp, err := bc.api.CreateProject(ctx, adminToken, ProjectName, orgID)
	if err != nil {
		return false, fmt.Errorf("failed to create project: %w", err)
	}

	projectID := projectResp.Project.ID
	projectSlug := projectResp.Project.Slug

	logger.Info("Project created", "projectID", projectID, "projectSlug", projectSlug)

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

	// Step 10: Create infisical-auth secret in hub-platform-ops
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

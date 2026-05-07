package client

import (
	"context"
	"fmt"

	hydra "github.com/ory/hydra-client-go/v2"
	"sigs.k8s.io/controller-runtime/pkg/log"

	opsv1alpha1 "github.com/soloz-io/zero-ops/operators/hub-operator/api/v1alpha1"
)

// HydraClient wraps Ory Hydra OAuth client operations
type HydraClient struct {
	client *hydra.APIClient
}

// NewHydraClient creates a new Hydra client
// Requirement 7.1: Use Ory Hydra Go SDK
// Requirement 7.2: Configure client for https://hydra-admin.platform-identity.svc
func NewHydraClient(adminURL string) (*HydraClient, error) {
	if adminURL == "" {
		adminURL = "https://hydra-admin.platform-identity.svc"
	}

	configuration := hydra.NewConfiguration()
	configuration.Servers = []hydra.ServerConfiguration{
		{
			URL: adminURL,
		},
	}

	return &HydraClient{
		client: hydra.NewAPIClient(configuration),
	}, nil
}

// RegisterOAuthClients registers all OAuth clients from HubEnvironment CR
// Requirement 7.2: Read OAuth client specifications from CR
// Requirement 7.4: Use idempotent client_id check
func (hc *HydraClient) RegisterOAuthClients(ctx context.Context, hubEnv *opsv1alpha1.HubEnvironment) error {
	logger := log.FromContext(ctx)

	for _, clientSpec := range hubEnv.Spec.OAuth.Clients {
		logger.Info("Processing OAuth client", "clientId", clientSpec.ClientID)

		// Requirement 7.4: Check if client exists (idempotent)
		exists, err := hc.clientExists(ctx, clientSpec.ClientID)
		if err != nil {
			return fmt.Errorf("failed to check if client exists: %w", err)
		}

		if exists {
			// Update existing client
			if err := hc.updateOAuthClient(ctx, clientSpec); err != nil {
				return fmt.Errorf("failed to update OAuth client %s: %w", clientSpec.ClientID, err)
			}
			logger.Info("Updated OAuth client", "clientId", clientSpec.ClientID)
		} else {
			// Create new client
			if err := hc.createOAuthClient(ctx, clientSpec); err != nil {
				return fmt.Errorf("failed to create OAuth client %s: %w", clientSpec.ClientID, err)
			}
			logger.Info("Created OAuth client", "clientId", clientSpec.ClientID)
		}
	}

	// Requirement 7.8: Prune orphaned OAuth clients
	if err := hc.pruneOrphanedOAuthClients(ctx, hubEnv); err != nil {
		return fmt.Errorf("failed to prune orphaned OAuth clients: %w", err)
	}

	return nil
}

// clientExists checks if an OAuth client exists in Hydra
func (hc *HydraClient) clientExists(ctx context.Context, clientID string) (bool, error) {
	_, resp, err := hc.client.OAuth2API.GetOAuth2Client(ctx, clientID).Execute()
	if err != nil {
		if resp != nil {
			// Requirement 7.5: Handle 404 errors
			if resp.StatusCode == 404 {
				return false, nil
			}
			// Requirement 7.6: Handle 5xx errors
			if resp.StatusCode >= 500 {
				return false, fmt.Errorf("hydra API returned %d (transient error)", resp.StatusCode)
			}
		}
		return false, fmt.Errorf("failed to get OAuth client: %w", err)
	}

	return true, nil
}

// createOAuthClient creates a new OAuth client in Hydra
// Requirement 7.3: Configure redirect URIs from CR
func (hc *HydraClient) createOAuthClient(ctx context.Context, clientSpec opsv1alpha1.OAuthClient) error {
	client := hydra.NewOAuth2Client()
	client.SetClientId(clientSpec.ClientID)
	client.SetClientName(clientSpec.ClientName)
	client.SetRedirectUris(clientSpec.RedirectURIs)
	client.SetGrantTypes(clientSpec.GrantTypes)
	client.SetResponseTypes(clientSpec.ResponseTypes)

	// Set token endpoint auth method (default to client_secret_basic)
	client.SetTokenEndpointAuthMethod("client_secret_basic")

	_, resp, err := hc.client.OAuth2API.CreateOAuth2Client(ctx).OAuth2Client(*client).Execute()
	if err != nil {
		if resp != nil {
			// Requirement 7.6: Handle 5xx errors
			if resp.StatusCode >= 500 {
				return fmt.Errorf("hydra API returned %d (transient error)", resp.StatusCode)
			}
		}
		return fmt.Errorf("failed to create OAuth client: %w", err)
	}

	return nil
}

// updateOAuthClient updates an existing OAuth client in Hydra
// Requirement 7.5: Update logic for existing clients
func (hc *HydraClient) updateOAuthClient(ctx context.Context, clientSpec opsv1alpha1.OAuthClient) error {
	client := hydra.NewOAuth2Client()
	client.SetClientId(clientSpec.ClientID)
	client.SetClientName(clientSpec.ClientName)
	client.SetRedirectUris(clientSpec.RedirectURIs)
	client.SetGrantTypes(clientSpec.GrantTypes)
	client.SetResponseTypes(clientSpec.ResponseTypes)
	client.SetTokenEndpointAuthMethod("client_secret_basic")

	_, resp, err := hc.client.OAuth2API.SetOAuth2Client(ctx, clientSpec.ClientID).OAuth2Client(*client).Execute()
	if err != nil {
		if resp != nil {
			// Requirement 7.5: Handle 404 errors
			if resp.StatusCode == 404 {
				return fmt.Errorf("hydra API returned 404 (client not found, will retry)")
			}
			// Requirement 7.6: Handle 5xx errors
			if resp.StatusCode >= 500 {
				return fmt.Errorf("hydra API returned %d (transient error)", resp.StatusCode)
			}
		}
		return fmt.Errorf("failed to update OAuth client: %w", err)
	}

	return nil
}

// pruneOrphanedOAuthClients deletes clients not in CR spec
// Requirement 7.8: Delete orphaned OAuth clients
func (hc *HydraClient) pruneOrphanedOAuthClients(ctx context.Context, hubEnv *opsv1alpha1.HubEnvironment) error {
	logger := log.FromContext(ctx)

	// Build map of desired clients from CR spec
	desiredClients := make(map[string]bool)
	for _, clientSpec := range hubEnv.Spec.OAuth.Clients {
		desiredClients[clientSpec.ClientID] = true
	}

	// List all OAuth clients from Hydra
	// Note: Hydra doesn't provide a way to tag/label managed clients
	// We'll use a naming convention: clients managed by hub-operator start with "hub-"
	clients, resp, err := hc.client.OAuth2API.ListOAuth2Clients(ctx).Execute()
	if err != nil {
		if resp != nil {
			// Requirement 7.6: Handle 5xx errors
			if resp.StatusCode >= 500 {
				return fmt.Errorf("hydra API returned %d (transient error)", resp.StatusCode)
			}
		}
		return fmt.Errorf("failed to list OAuth clients: %w", err)
	}

	// Delete orphaned clients (managed by hub-operator but not in CR spec)
	for _, client := range clients {
		clientID := client.GetClientId()

		// Only manage clients that start with "hub-" prefix
		if len(clientID) < 4 || clientID[:4] != "hub-" {
			continue
		}

		// Check if client is in desired state
		if !desiredClients[clientID] {
			if err := hc.deleteOAuthClient(ctx, clientID); err != nil {
				logger.Error(err, "Failed to delete orphaned OAuth client", "clientId", clientID)
				continue
			}
			logger.Info("Deleted orphaned OAuth client", "clientId", clientID)
		}
	}

	return nil
}

// deleteOAuthClient deletes an OAuth client from Hydra
func (hc *HydraClient) deleteOAuthClient(ctx context.Context, clientID string) error {
	resp, err := hc.client.OAuth2API.DeleteOAuth2Client(ctx, clientID).Execute()
	if err != nil {
		if resp != nil {
			// Ignore 404 errors (client already deleted)
			if resp.StatusCode == 404 {
				return nil
			}
			// Requirement 7.6: Handle 5xx errors
			if resp.StatusCode >= 500 {
				return fmt.Errorf("hydra API returned %d (transient error)", resp.StatusCode)
			}
		}
		return fmt.Errorf("failed to delete OAuth client: %w", err)
	}

	return nil
}

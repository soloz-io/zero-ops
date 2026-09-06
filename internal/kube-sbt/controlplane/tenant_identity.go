package controlplane

import (
	"context"
	"fmt"
	"strings"

	"github.com/soloz-io/zero-ops/internal/kube-sbt/interfaces"
	"github.com/soloz-io/zero-ops/internal/kube-sbt/models"
)

// EnsureTenantIdentity provisions a tenant's identity resources and publishes
// the one value the rest of the platform needs from them.
//
// ADR-041 assigns this service both halves — "tenant identity lifecycle" and
// "Infisical upload for tenant credentials" — which is why they happen together
// here rather than being split across two components that would then have to
// agree on when the first had finished.
//
// The published client id is what a tenant's gateway authenticates as. It is
// allocated by the issuer, so it cannot be derived and cannot be written into a
// values file ahead of time; publishing it here is what lets a tenant be
// onboarded without anyone copying an identifier between systems.
func (cp *ControlPlane) EnsureTenantIdentity(ctx context.Context, tenantID, ownerEmail string, selfRegistration bool, redirectURIs, postLogoutURIs []string, oauthClients []models.OAuthClient) (*models.TenantIdentity, error) {
	if tenantID == "" {
		return nil, fmt.Errorf("controlplane: tenantID is required")
	}

	// Capability, not assumption. A provider whose tenancy is an attribute on an
	// identity has no organisation to create, and asking it to would either fail
	// or invent an answer. Skipping is reported rather than silent, because a
	// caller that believed provisioning had happened would go on to wait for a
	// client id that is never coming.
	provisioner, ok := cp.auth.(interfaces.ITenantIdentityProvisioner)
	if !ok {
		return nil, fmt.Errorf("controlplane: the configured identity provider does not provision tenant identities")
	}

	identity, err := provisioner.EnsureTenantIdentity(ctx, tenantID, ownerEmail, selfRegistration, redirectURIs, postLogoutURIs, oauthClients)
	if err != nil {
		return nil, fmt.Errorf("controlplane: provision identity for tenant %q: %w", tenantID, err)
	}

	// Publish before reporting success. A caller that saw success and then found
	// no client id would have no way to tell "not provisioned yet" from
	// "provisioned and the publish failed", and the second needs a retry the
	// first does not.
	if cp.cfg.SecretManager == nil {
		return nil, fmt.Errorf("controlplane: no secret manager configured; tenant %q has an identity that nothing can consume", tenantID)
	}
	if err := cp.cfg.SecretManager.StoreTenantSecret(ctx, tenantID, tenantOIDCClientSecretName,
		map[string]interface{}{"OIDC_CLIENT_ID": identity.ClientID}); err != nil {
		return nil, fmt.Errorf("controlplane: publish client id for tenant %q: %w", tenantID, err)
	}

	if err := cp.ensureDeclaredClients(ctx, provisioner, tenantID, oauthClients); err != nil {
		return nil, err
	}

	return identity, nil
}

// ensureDeclaredClients provisions the clients THIS FLEET DECLARED and publishes
// what the issuer allocates for each.
//
// It iterates the declaration and invents nothing. An earlier version of this
// function assumed a single client named "bff" and derived its Infisical keys
// from that name, which put a tenant's architecture in platform code -- ADR-047
// forbids exactly that, and a fleet with two back-ends, or none, was
// unrepresentable.
//
// Read-before-write, and the read is the whole design. The issuer discloses a
// generated secret exactly once, so the only way to recover an existing one is
// to regenerate it -- which invalidates the credential the running workload is
// holding. Asking the store first means a reconcile of an already-provisioned
// tenant touches nothing, and a regeneration happens only when there is
// genuinely nothing to break.
//
// Public clients are skipped: PKCE carries the proof and there is no secret to
// store. Their identifier is already published as OIDC_CLIENT_ID by the caller.
func (cp *ControlPlane) ensureDeclaredClients(ctx context.Context, provisioner interfaces.ITenantIdentityProvisioner, tenantID string, clients []models.OAuthClient) error {
	for _, decl := range clients {
		if !decl.Confidential || decl.Name == "" {
			continue
		}

		appName := tenantID + "-" + decl.Name
		idKey, secretKey := oauthKeys(decl.Name)

		stored, err := cp.cfg.SecretManager.GetTenantSecret(ctx, tenantID, secretKey)
		if err == nil && stored != nil {
			if v, ok := stored[secretKey].(string); ok && v != "" {
				// Already provisioned. Confirm the client still exists without
				// disturbing its secret, so a client deleted at the issuer is
				// recreated rather than silently missing.
				if _, _, cerr := provisioner.EnsureConfidentialClient(ctx, tenantID, appName, false); cerr != nil {
					return fmt.Errorf("controlplane: verify client %q for tenant %q: %w", decl.Name, tenantID, cerr)
				}
				continue
			}
		}

		clientID, clientSecret, err := provisioner.EnsureConfidentialClient(ctx, tenantID, appName, true)
		if err != nil {
			return fmt.Errorf("controlplane: provision client %q for tenant %q: %w", decl.Name, tenantID, err)
		}
		if clientSecret == "" {
			return fmt.Errorf("controlplane: client %q for tenant %q returned no secret to publish", decl.Name, tenantID)
		}

		if err := cp.cfg.SecretManager.StoreTenantSecret(ctx, tenantID, idKey,
			map[string]interface{}{idKey: clientID}); err != nil {
			return fmt.Errorf("controlplane: publish id for client %q of tenant %q: %w", decl.Name, tenantID, err)
		}
		if err := cp.cfg.SecretManager.StoreTenantSecret(ctx, tenantID, secretKey,
			map[string]interface{}{secretKey: clientSecret}); err != nil {
			return fmt.Errorf("controlplane: publish secret for client %q of tenant %q: %w", decl.Name, tenantID, err)
		}
	}
	return nil
}

// oauthKeys derives the Infisical keys for a declared client name, matching the
// contract the tenant chart documents: OAUTH_<NAME>_CLIENT_ID and _SECRET, the
// name uppercased with hyphens becoming underscores.
//
// Derived from the fleet's chosen name, never from a name this package knows.
// The tenant is not part of the key; the folder path scopes it.
func oauthKeys(name string) (idKey, secretKey string) {
	seg := strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
	return "OAUTH_" + seg + "_CLIENT_ID", "OAUTH_" + seg + "_CLIENT_SECRET"
}

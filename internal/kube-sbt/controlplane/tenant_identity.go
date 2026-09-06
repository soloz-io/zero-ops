package controlplane

import (
	"context"
	"fmt"

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
func (cp *ControlPlane) EnsureTenantIdentity(ctx context.Context, tenantID, ownerEmail string, selfRegistration bool, redirectURIs, postLogoutURIs []string) (*models.TenantIdentity, error) {
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

	identity, err := provisioner.EnsureTenantIdentity(ctx, tenantID, ownerEmail, selfRegistration, redirectURIs, postLogoutURIs)
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

	if err := cp.ensureBFFClient(ctx, provisioner, tenantID); err != nil {
		return nil, err
	}

	return identity, nil
}

// ensureBFFClient provisions and publishes the tenant's server-side OAuth
// credential.
//
// Read-before-write, and the read is the whole design. The issuer discloses a
// generated secret exactly once, so the only way to recover an existing one is
// to regenerate it — which invalidates the credential the running workload is
// holding. Asking the store first means a reconcile of a tenant that is already
// provisioned touches nothing, and a regeneration happens only when there is
// genuinely nothing to break.
//
// Failing here fails the call. A tenant whose gateway can authenticate a person
// but whose BFF cannot obtain a token is a tenant that logs in and then 401s on
// its first API call, which reads as a broken application rather than as
// incomplete provisioning.
func (cp *ControlPlane) ensureBFFClient(ctx context.Context, provisioner interfaces.ITenantIdentityProvisioner, tenantID string) error {
	appName := bffClientName(tenantID)

	stored, err := cp.cfg.SecretManager.GetTenantSecret(ctx, tenantID, bffClientSecretKey)
	if err == nil && stored != nil {
		if v, ok := stored[bffClientSecretKey].(string); ok && v != "" {
			// Already provisioned. Confirm the client still exists without
			// disturbing its secret, so a client deleted in the issuer is still
			// recreated rather than silently missing.
			if _, _, cerr := provisioner.EnsureConfidentialClient(ctx, tenantID, appName, false); cerr != nil {
				return fmt.Errorf("controlplane: verify server-side client for tenant %q: %w", tenantID, cerr)
			}
			return nil
		}
	}

	clientID, clientSecret, err := provisioner.EnsureConfidentialClient(ctx, tenantID, appName, true)
	if err != nil {
		return fmt.Errorf("controlplane: provision server-side client for tenant %q: %w", tenantID, err)
	}
	if clientSecret == "" {
		return fmt.Errorf("controlplane: server-side client for tenant %q returned no secret to publish", tenantID)
	}

	if err := cp.cfg.SecretManager.StoreTenantSecret(ctx, tenantID, bffClientIDKey,
		map[string]interface{}{bffClientIDKey: clientID}); err != nil {
		return fmt.Errorf("controlplane: publish server-side client id for tenant %q: %w", tenantID, err)
	}
	if err := cp.cfg.SecretManager.StoreTenantSecret(ctx, tenantID, bffClientSecretKey,
		map[string]interface{}{bffClientSecretKey: clientSecret}); err != nil {
		return fmt.Errorf("controlplane: publish server-side client secret for tenant %q: %w", tenantID, err)
	}
	return nil
}

// bffClientName is the application name the issuer holds for a tenant's
// server-side client. Derived, never configured: a name a fleet could choose
// would be a name a fleet could point at another tenant's client.
func bffClientName(tenantID string) string { return tenantID + "-bff" }

// The keys the tenant's ExternalSecret projects from.
//
// Shared with the fleet's values by convention, the same coupling
// tenantOIDCClientSecretName carries and with the same failure: renaming one
// side leaves an ExternalSecret waiting for a key that never appears, and the
// symptom is a pod that never starts rather than an error naming either side.
const (
	bffClientIDKey     = "OAUTH_BFF_CLIENT_ID"
	bffClientSecretKey = "OAUTH_BFF_CLIENT_SECRET"
)

// tenantOIDCClientSecretName is the name the tenant's ExternalSecret reads.
//
// Shared between this service and the tenant chart by convention, which is a
// coupling worth naming: changing it here without changing the ExternalSecret
// leaves a gateway waiting for a key that will never appear, and the symptom is
// a pod that never starts rather than an error mentioning either side.
const tenantOIDCClientSecretName = "OIDC_CLIENT_ID"

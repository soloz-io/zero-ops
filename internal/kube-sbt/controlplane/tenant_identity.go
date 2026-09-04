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

	return identity, nil
}

// tenantOIDCClientSecretName is the name the tenant's ExternalSecret reads.
//
// Shared between this service and the tenant chart by convention, which is a
// coupling worth naming: changing it here without changing the ExternalSecret
// leaves a gateway waiting for a key that will never appear, and the symptom is
// a pod that never starts rather than an error mentioning either side.
const tenantOIDCClientSecretName = "OIDC_CLIENT_ID"

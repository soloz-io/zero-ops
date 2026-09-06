package interfaces

import (
	"context"

	"github.com/soloz-io/zero-ops/internal/kube-sbt/models"
)

// ITenantIdentityProvisioner is implemented by identity providers that can
// create a tenant's identity resources.
//
// OPTIONAL, and separate from IAuth on purpose. Whether a tenant is a
// first-class object in the provider is exactly what differs between providers:
// one whose tenancy is structural has an organisation to create, a project to
// hold roles, and an application to authenticate against; one where the tenant
// is merely an attribute on an identity has nothing to provision, and forcing it
// to satisfy this interface would mean writing a method that does nothing and
// returns a value it made up.
//
// Callers type-assert and skip when unimplemented, so the absence is a
// capability the platform can observe rather than a runtime failure.
type ITenantIdentityProvisioner interface {
	// EnsureTenantIdentity makes a tenant's identity resources exist and returns
	// them. Idempotent: it runs on every reconcile, and a partial failure must
	// leave the next attempt able to finish rather than to conflict.
	// ownerEmail, when set, is granted administrative access to the tenant.
	//
	// Not optional in practice. Where the issuer denies authentication to a user
	// holding no role, a tenant provisioned without one is a tenant NOBODY can
	// sign in to — the resources all exist and the first login fails with a
	// grant error that names no remedy.
	// selfRegistration is reconciled, not applied once: it is a security
	// boundary, and one that lives only in a provider console can be re-opened
	// by an upgrade or a support session with nothing to notice.
	EnsureTenantIdentity(ctx context.Context, tenantID, ownerEmail string, selfRegistration bool, redirectURIs, postLogoutURIs []string) (*models.TenantIdentity, error)

	// EnsureConfidentialClient provisions a tenant's server-side OAuth client.
	//
	// Separate from the call above because it is not part of a tenant's identity:
	// it is a credential for one of the tenant's WORKLOADS, and its lifecycle is
	// the workload's. Folding it in would mean every reconcile of a tenant's
	// identity also touched a secret a running process is holding.
	//
	// clientSecret is returned ONLY when this call created or regenerated it. An
	// issuer discloses a generated secret once, so an empty value means "the
	// client exists and its secret is whatever you already stored", not "there is
	// no secret" — and the caller that cannot find a stored one is the only party
	// able to decide that invalidating the live credential is acceptable, which
	// is what regenerateIfExists expresses.
	EnsureConfidentialClient(ctx context.Context, tenantID, appName string, regenerateIfExists bool) (clientID, clientSecret string, err error)
}

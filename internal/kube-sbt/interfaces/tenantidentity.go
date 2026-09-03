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
	EnsureTenantIdentity(ctx context.Context, tenantID string, redirectURIs, postLogoutURIs []string) (*models.TenantIdentity, error)
}

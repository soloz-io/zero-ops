package models

// TenantIdentity is what a tenant needs in order to authenticate, and the only
// output of identity provisioning that anything downstream consumes.
type TenantIdentity struct {
	// TenantRef is the provider's own identifier for the tenant — the thing that
	// OWNS the tenant's users. It is what appears in a token as the tenant, so it
	// is the value an authorisation decision compares against.
	TenantRef string

	// ProjectRef scopes the roles that express authorisation within the tenant.
	ProjectRef string

	// ClientID is the OAuth client a browser authenticates as.
	//
	// Not secret — it is a public identifier for a PKCE client — but ALLOCATED by
	// the issuer, so it cannot be derived from the tenant id and must be reported
	// back by whatever created it. That single property is why it travels the
	// credential path rather than a values file.
	ClientID string

	// OwnerPassword is set ONLY when this call created the owner's account.
	//
	// Empty on every later reconcile, and that distinction is the contract: a
	// value here means a new credential exists that nobody has yet, so the
	// caller must persist it. Re-issuing one on each reconcile would silently
	// lock out whoever is already using the account.
	OwnerPassword string
}

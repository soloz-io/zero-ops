package models

// OAuthClient is one client a FLEET declares (ADR-053).
//
// It carries no credential. The fleet says what it needs -- a name, whether the
// client authenticates with a secret, and where its callback lives -- and the
// platform provisions it against the issuer and publishes what the issuer
// allocates. The declaration stays on the fleet's side so the platform never
// assumes a client set: ADR-047 forbids a tenant identifier in platform code,
// and "every tenant has a bff" is one.
type OAuthClient struct {
	// Name is unique within the tenant. Registered as "<tenantId>-<Name>".
	Name string

	// Confidential is true when the client authenticates with a secret. A public
	// client uses PKCE and has no credential to store.
	Confidential bool

	// RedirectPaths are appended to the fleet's public host. An issuer matches
	// redirect URIs EXACTLY, so a client provisioned without its callback fails
	// at the authorization endpoint before any credential is entered.
	RedirectPaths []string
}

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

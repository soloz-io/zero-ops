package constant

// Infisical secret key constants shared across packages.
// Defined here to avoid circular imports between internal/client,
// internal/secrets, and internal/infisical.
const (
	KeyClientID     = "client-id"
	KeyClientSecret = "client-secret"
	KeyProjectID    = "project-id"
)

package ory

import "time"

// Config holds connection details for the Ory stack.
type Config struct {
	KratosPublicURL string // e.g. http://ory-kratos-public:4433
	KratosAdminURL  string // e.g. http://ory-kratos-admin:4434
	HydraPublicURL  string // e.g. http://ory-hydra-public:4444
	HydraAdminURL   string // e.g. http://ory-hydra-admin:4445
	KetoReadURL     string // e.g. http://ory-keto-read:4466
	KetoWriteURL    string // e.g. http://ory-keto-write:4467

	// JWT validation
	JWTAudience         string        // expected audience claim
	JWKSCacheTTL        time.Duration // default 1h
	JWKSFetchTimeout    time.Duration // default 5s
	JWKSRefreshInterval time.Duration // minimum interval between JWKS refreshes, default 10s
}

func (c *Config) defaults() {
	if c.JWKSCacheTTL == 0 {
		c.JWKSCacheTTL = time.Hour
	}
	if c.JWKSFetchTimeout == 0 {
		c.JWKSFetchTimeout = 5 * time.Second
	}
	if c.JWKSRefreshInterval == 0 {
		c.JWKSRefreshInterval = 10 * time.Second
	}
}

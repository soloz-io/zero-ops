package authproxy

import (
	"os"
	"time"
)

type Config struct {
	ListenAddr              string
	HydraPublicURL          string
	HydraAdminURL           string
	HydraInternalJWKSURL    string
	KratosPublicURL         string
	KratosAdminURL          string
	JWKSCacheTTL            time.Duration
	JWKSFetchTimeout        time.Duration
	JWKSRefreshMinInterval  time.Duration
	ExpectedJWTAudience     string
	TrustedClientIDs        string
	AuthPublicBaseURL       string // public-facing base URL for auth server metadata (e.g. https://auth.nutgraf.in)
	MCPGatewayBaseURL       string // base URL of the MCP gateway (e.g. https://api.nutgraf.in) used as issuer in gateway-served metadata
}

func LoadConfig() (*Config, error) {
	cacheTTL, err := time.ParseDuration(getEnv("JWKS_CACHE_TTL", "1h"))
	if err != nil {
		return nil, err
	}
	fetchTimeout, err := time.ParseDuration(getEnv("JWKS_FETCH_TIMEOUT", "5s"))
	if err != nil {
		return nil, err
	}
	refreshInterval, err := time.ParseDuration(getEnv("JWKS_REFRESH_MIN_INTERVAL", "10s"))
	if err != nil {
		return nil, err
	}

	return &Config{
		ListenAddr:              getEnv("LISTEN_ADDR", ":8080"),
		HydraPublicURL:          getEnv("HYDRA_PUBLIC_URL", "http://ory-hydra-public.platform-identity.svc.cluster.local:4444"),
		HydraAdminURL:           getEnv("HYDRA_ADMIN_URL", "http://ory-hydra-admin.platform-identity.svc.cluster.local:4445"),
		HydraInternalJWKSURL:    getEnv("HYDRA_INTERNAL_JWKS_URL", "http://ory-hydra-public.platform-identity.svc.cluster.local:4444/.well-known/jwks.json"),
		KratosPublicURL:         getEnv("KRATOS_PUBLIC_URL", "http://ory-kratos-public.platform-identity.svc.cluster.local:4433"),
		KratosAdminURL:          getEnv("KRATOS_ADMIN_URL", "http://ory-kratos-admin.platform-identity.svc.cluster.local:4434"),
		JWKSCacheTTL:            cacheTTL,
		JWKSFetchTimeout:        fetchTimeout,
		JWKSRefreshMinInterval:  refreshInterval,
		ExpectedJWTAudience:     getEnv("EXPECTED_JWT_AUDIENCE", "https://api.nutgraf.in"),
		TrustedClientIDs:        getEnv("TRUSTED_CLIENT_IDS", "mcp-public-client"),
		AuthPublicBaseURL:       getEnv("AUTH_PUBLIC_BASE_URL", "https://auth.nutgraf.in"),
		MCPGatewayBaseURL:       getEnv("MCP_GATEWAY_BASE_URL", "https://api.nutgraf.in"),
	}, nil
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

package authproxy

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

type Config struct {
	ListenAddr             string
	HydraPublicURL         string
	HydraAdminURL          string
	HydraInternalJWKSURL   string
	KratosPublicURL        string
	KratosAdminURL         string
	JWKSCacheTTL           time.Duration
	JWKSFetchTimeout       time.Duration
	JWKSRefreshMinInterval time.Duration
	ExpectedJWTAudience    string
	TrustedClientIDs       string
	// AuthPublicBaseURL is the public-facing base URL for authorization-server
	// metadata. Public hostnames are environment-zoned (ADR-051): they carry the
	// env label (auth.dev.nutgraf.in) because a single DNS namespace is shared
	// across environments.
	AuthPublicBaseURL string
	// MCPGatewayBaseURL is the public base URL of the MCP gateway, used as the
	// issuer in gateway-served metadata. Environment-zoned, as above.
	MCPGatewayBaseURL string
	// ConsoleBaseURL is the public base URL of the console SPA, which serves the
	// login and registration pages. Environment-zoned, as above.
	ConsoleBaseURL string
}

// Note on the in-cluster URLs above (Hydra, Kratos): these are Kubernetes Service
// DNS names (*.svc.cluster.local). They are NOT environment-zoned and must not be —
// each environment is a physically separate cluster (ADR-037), so the name is already
// env-scoped by virtue of resolving only inside that cluster. Only PUBLIC hostnames
// carry the env label.

// missingEnv accumulates every unset variable so a misconfigured deployment reports
// all of them at once rather than one restart at a time.
type missingEnv struct{ names []string }

func (m *missingEnv) get(key string) string {
	v := os.Getenv(key)
	if strings.TrimSpace(v) == "" {
		m.names = append(m.names, key)
		return ""
	}
	return v
}

func (m *missingEnv) duration(key string) time.Duration {
	raw := m.get(key)
	if raw == "" {
		return 0
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		m.names = append(m.names, fmt.Sprintf("%s (invalid duration %q)", key, raw))
		return 0
	}
	return d
}

// LoadConfig reads every setting from the environment and fails hard if any is
// missing. There are deliberately NO defaults: a hardcoded fallback lets a
// misconfigured deployment start and silently talk to the wrong endpoint — which is
// exactly how AUTH_PUBLIC_BASE_URL and MCP_GATEWAY_BASE_URL came to be absent from
// the Deployment while the process still ran against compiled-in values.
func LoadConfig() (*Config, error) {
	m := &missingEnv{}

	cfg := &Config{
		ListenAddr:             m.get("LISTEN_ADDR"),
		HydraPublicURL:         m.get("HYDRA_PUBLIC_URL"),
		HydraAdminURL:          m.get("HYDRA_ADMIN_URL"),
		HydraInternalJWKSURL:   m.get("HYDRA_INTERNAL_JWKS_URL"),
		KratosPublicURL:        m.get("KRATOS_PUBLIC_URL"),
		KratosAdminURL:         m.get("KRATOS_ADMIN_URL"),
		JWKSCacheTTL:           m.duration("JWKS_CACHE_TTL"),
		JWKSFetchTimeout:       m.duration("JWKS_FETCH_TIMEOUT"),
		JWKSRefreshMinInterval: m.duration("JWKS_REFRESH_MIN_INTERVAL"),
		ExpectedJWTAudience:    m.get("EXPECTED_JWT_AUDIENCE"),
		TrustedClientIDs:       m.get("TRUSTED_CLIENT_IDS"),
		AuthPublicBaseURL:      m.get("AUTH_PUBLIC_BASE_URL"),
		MCPGatewayBaseURL:      m.get("MCP_GATEWAY_BASE_URL"),
		ConsoleBaseURL:         m.get("CONSOLE_BASE_URL"),
	}

	if len(m.names) > 0 {
		sort.Strings(m.names)
		return nil, fmt.Errorf(
			"auth-proxy configuration incomplete: %d required environment variable(s) missing or invalid: %s",
			len(m.names), strings.Join(m.names, ", "))
	}
	return cfg, nil
}

package authproxy

import "os"

type Config struct {
	Port      string
	HydraURL  string
	ClientID  string
}

func LoadConfig() *Config {
	return &Config{
		Port:     getEnv("PORT", "8081"),
		HydraURL: getEnv("HYDRA_ADMIN_URL", "http://localhost:4445"),
		ClientID: "mcp-public-client",
	}
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

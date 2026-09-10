package infisical

// Infisical secret key names for ExternalSecret remoteRefs.
// Must match operators/hub-operator/internal/infisical/constants.go.
const (
	KeyControlPlaneDBUsername   = "hub-control-plane-db-username"
	KeyControlPlaneDBPassword   = "hub-control-plane-db-password"
	KeyHubCentralizedDBUsername = "hub-centralized-db-username"
	KeyHubCentralizedDBPassword = "hub-centralized-db-password"
	KeyPlatformDBAppUsername    = "platform-db-app-username"
	KeyPlatformDBAppPassword    = "platform-db-app-password"
	KeyInfisicalDBUsername      = "infisical-db-username"
	KeyInfisicalDBPassword      = "infisical-db-password"
	KeyTailscaleAuthkey         = "tailscale-authkey"
	KeyTailscaleHostname        = "tailscale-hostname"
)

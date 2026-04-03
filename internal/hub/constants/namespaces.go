package constants

// Hub Platform Namespaces - aligned with docs/adr/namespace-alignment.md
const (
	// CAPI and cluster management
	NamespaceCAPI = "hub-platform-capi"
	
	// Messaging infrastructure
	NamespaceMessaging = "hub-platform-messaging"
	
	// Identity and authentication
	NamespaceIdentity = "hub-platform-identity"
	
	// Data layer (PostgreSQL, Redis, CNPG)
	NamespaceData = "hub-platform-data"
	
	// Platform operations (ArgoCD, Crossplane, ESO, etc)
	NamespaceOps = "hub-platform-ops"
	
	// Security (Infisical, SPIRE)
	NamespaceSecurity = "hub-platform-security"
	
	// Edge services (AgentGateway, auth-proxy, Ingress)
	NamespaceEdge = "hub-platform-edge"
	
	// Network (Cilium)
	NamespaceNetwork = "hub-platform-network"
	
	// Observability (VictoriaMetrics, Grafana Alloy)
	NamespaceObservability = "hub-platform-observability"
	
	// Applications (MCP Server, AgentRegistry)
	NamespaceApps = "hub-platform-apps"
	
	// Cloud provider integrations (CCM, CSI)
	NamespaceCloud = "hub-cloud-system"
	
	// Upstream namespaces
	NamespaceCertManager = "cert-manager"
	NamespaceCNPG        = "cnpg-system"
)

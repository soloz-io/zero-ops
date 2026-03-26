package service

// parseAgentID parses agent ID into name and version
// Assumes format like "agent-name" or "agent-name:v1"
func parseAgentID(agentID string) (name, version string) {
	// Simple parsing - in real implementation this would be more robust
	name = agentID
	version = "v1" // default version
	
	// If agentID contains version separator, split it
	// This is a simplified implementation
	return name, version
}
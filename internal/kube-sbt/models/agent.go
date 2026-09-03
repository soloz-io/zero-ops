package models

import "time"

// AgentConfig represents the configuration for an ArgoCD agent
type AgentConfig struct {
	AgentID    string                 `json:"agent_id"`
	TenantID   string                 `json:"tenant_id"`
	Mode       string                 `json:"mode"` // managed, autonomous
	Namespace  string                 `json:"namespace"`
	ServerURL  string                 `json:"server_url"`
	Credentials map[string]string     `json:"credentials,omitempty"`
	Config     map[string]interface{} `json:"config,omitempty"`
	CreatedAt  time.Time              `json:"created_at"`
	UpdatedAt  time.Time              `json:"updated_at"`
}

// AgentStatus represents the current status of an ArgoCD agent
type AgentStatus struct {
	AgentID       string    `json:"agent_id"`
	TenantID      string    `json:"tenant_id"`
	Connected     bool      `json:"connected"`
	HealthStatus  string    `json:"health_status"` // Healthy, Degraded, Unknown
	LastHeartbeat time.Time `json:"last_heartbeat"`
	Version       string    `json:"version,omitempty"`
	ErrorMessage  string    `json:"error_message,omitempty"`
}

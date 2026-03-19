package models

import (
	"fmt"
	"time"
)

// Event represents a standard event structure for inter-plane communication
type Event struct {
	ID         string                 `json:"id"`
	Version    string                 `json:"version"`
	DetailType string                 `json:"detailType"`
	Source     string                 `json:"source"`
	Time       time.Time              `json:"time"`
	Region     string                 `json:"region,omitempty"`
	Resources  []string               `json:"resources,omitempty"`
	Detail     map[string]interface{} `json:"detail"`
}

// NewEvent creates an Event with a unique ID, current timestamp, and version 1.0.
func NewEvent(detailType, source string, detail map[string]interface{}) Event {
	return Event{
		ID:         fmt.Sprintf("%d", time.Now().UnixNano()),
		Version:    "1.0",
		DetailType: detailType,
		Source:     source,
		Time:       time.Now().UTC(),
		Detail:     detail,
	}
}

// EventDefinition represents the definition of a standard event
type EventDefinition struct {
	DetailType  string `json:"detailType"`
	Source      string `json:"source"`
	Description string `json:"description"`
	Schema      string `json:"schema,omitempty"`
}

// EventHandler is a function that handles an event
type EventHandler func(ctx interface{}, event Event) error

// Standard Control Plane event types
const (
	EventOnboardingRequest  = "opensbt_onboardingRequest"
	EventOffboardingRequest = "opensbt_offboardingRequest"
	EventActivateRequest    = "opensbt_activateRequest"
	EventDeactivateRequest  = "opensbt_deactivateRequest"
	EventTenantUserCreated  = "opensbt_tenantUserCreated"
	EventTenantUserDeleted  = "opensbt_tenantUserDeleted"
	EventBillingSuccess     = "opensbt_billingSuccess"
	EventBillingFailure     = "opensbt_billingFailure"
	EventTierChanged        = "opensbt_tierChanged"
)

// Standard Application Plane event types
const (
	EventOnboardingSuccess    = "opensbt_onboardingSuccess"
	EventOnboardingFailure    = "opensbt_onboardingFailure"
	EventOffboardingSuccess   = "opensbt_offboardingSuccess"
	EventOffboardingFailure   = "opensbt_offboardingFailure"
	EventProvisionSuccess     = "opensbt_provisionSuccess"
	EventProvisionFailure     = "opensbt_provisionFailure"
	EventDeprovisionSuccess   = "opensbt_deprovisionSuccess"
	EventDeprovisionFailure   = "opensbt_deprovisionFailure"
	EventActivateSuccess      = "opensbt_activateSuccess"
	EventActivateFailure      = "opensbt_activateFailure"
	EventDeactivateSuccess    = "opensbt_deactivateSuccess"
	EventDeactivateFailure    = "opensbt_deactivateFailure"
	EventIngestUsage          = "opensbt_ingestUsage"
)

// Event-Driven State Machine events
const (
	EventGitCommitted       = "opensbt_gitCommitted"
	EventArgoSyncStarted    = "opensbt_argoSyncStarted"
	EventArgoSyncCompleted  = "opensbt_argoSyncCompleted"
	EventArgoHealthChanged  = "opensbt_argoHealthChanged"
)

// ArgoCD Agent events
const (
	EventAgentDeployed          = "opensbt_agentDeployed"
	EventAgentConnected         = "opensbt_agentConnected"
	EventAgentDisconnected      = "opensbt_agentDisconnected"
	EventAgentHealthChanged     = "opensbt_agentHealthChanged"
	EventAgentAppSynced         = "opensbt_agentAppSynced"
	EventAgentAppHealthChanged  = "opensbt_agentAppHealthChanged"
	EventAgentAppStatusChanged  = "opensbt_agentAppStatusChanged"
)

// Event sources
const (
	ControlPlaneEventSource    = "zerosbt.control.plane"
	ApplicationPlaneEventSource = "zerosbt.application.plane"
)

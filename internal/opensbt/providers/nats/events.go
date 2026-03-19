package nats

import "github.com/soloz-io/zero-ops/internal/opensbt/models"

// standardEvents is the canonical registry of all open-sbt events (7.6–7.9).
var standardEvents = map[string]models.EventDefinition{
	// Control Plane events (7.6)
	models.EventOnboardingRequest:  {DetailType: models.EventOnboardingRequest, Source: models.ControlPlaneEventSource, Description: "Tenant onboarding initiated"},
	models.EventOffboardingRequest: {DetailType: models.EventOffboardingRequest, Source: models.ControlPlaneEventSource, Description: "Tenant offboarding initiated"},
	models.EventActivateRequest:    {DetailType: models.EventActivateRequest, Source: models.ControlPlaneEventSource, Description: "Tenant activation requested"},
	models.EventDeactivateRequest:  {DetailType: models.EventDeactivateRequest, Source: models.ControlPlaneEventSource, Description: "Tenant deactivation requested"},
	models.EventTenantUserCreated:  {DetailType: models.EventTenantUserCreated, Source: models.ControlPlaneEventSource, Description: "User created in tenant"},
	models.EventTenantUserDeleted:  {DetailType: models.EventTenantUserDeleted, Source: models.ControlPlaneEventSource, Description: "User deleted from tenant"},
	models.EventBillingSuccess:     {DetailType: models.EventBillingSuccess, Source: models.ControlPlaneEventSource, Description: "Billing operation succeeded"},
	models.EventBillingFailure:     {DetailType: models.EventBillingFailure, Source: models.ControlPlaneEventSource, Description: "Billing operation failed"},
	models.EventTierChanged:        {DetailType: models.EventTierChanged, Source: models.ControlPlaneEventSource, Description: "Tenant tier changed"},

	// Application Plane events (7.7)
	models.EventOnboardingSuccess:  {DetailType: models.EventOnboardingSuccess, Source: models.ApplicationPlaneEventSource, Description: "Tenant onboarded successfully"},
	models.EventOnboardingFailure:  {DetailType: models.EventOnboardingFailure, Source: models.ApplicationPlaneEventSource, Description: "Tenant onboarding failed"},
	models.EventOffboardingSuccess: {DetailType: models.EventOffboardingSuccess, Source: models.ApplicationPlaneEventSource, Description: "Tenant offboarded successfully"},
	models.EventOffboardingFailure: {DetailType: models.EventOffboardingFailure, Source: models.ApplicationPlaneEventSource, Description: "Tenant offboarding failed"},
	models.EventProvisionSuccess:   {DetailType: models.EventProvisionSuccess, Source: models.ApplicationPlaneEventSource, Description: "Resources provisioned successfully"},
	models.EventProvisionFailure:   {DetailType: models.EventProvisionFailure, Source: models.ApplicationPlaneEventSource, Description: "Resource provisioning failed"},
	models.EventDeprovisionSuccess: {DetailType: models.EventDeprovisionSuccess, Source: models.ApplicationPlaneEventSource, Description: "Resources deprovisioned successfully"},
	models.EventDeprovisionFailure: {DetailType: models.EventDeprovisionFailure, Source: models.ApplicationPlaneEventSource, Description: "Resource deprovisioning failed"},
	models.EventActivateSuccess:    {DetailType: models.EventActivateSuccess, Source: models.ApplicationPlaneEventSource, Description: "Tenant activated successfully"},
	models.EventActivateFailure:    {DetailType: models.EventActivateFailure, Source: models.ApplicationPlaneEventSource, Description: "Tenant activation failed"},
	models.EventDeactivateSuccess:  {DetailType: models.EventDeactivateSuccess, Source: models.ApplicationPlaneEventSource, Description: "Tenant deactivated successfully"},
	models.EventDeactivateFailure:  {DetailType: models.EventDeactivateFailure, Source: models.ApplicationPlaneEventSource, Description: "Tenant deactivation failed"},
	models.EventIngestUsage:        {DetailType: models.EventIngestUsage, Source: models.ApplicationPlaneEventSource, Description: "Usage data ingested"},

	// Event-Driven State Machine events (7.8)
	models.EventGitCommitted:      {DetailType: models.EventGitCommitted, Source: models.ApplicationPlaneEventSource, Description: "Tenant config committed to Git"},
	models.EventArgoSyncStarted:   {DetailType: models.EventArgoSyncStarted, Source: models.ApplicationPlaneEventSource, Description: "ArgoCD sync started"},
	models.EventArgoSyncCompleted: {DetailType: models.EventArgoSyncCompleted, Source: models.ApplicationPlaneEventSource, Description: "ArgoCD sync completed"},
	models.EventArgoHealthChanged: {DetailType: models.EventArgoHealthChanged, Source: models.ApplicationPlaneEventSource, Description: "ArgoCD health status changed"},

	// ArgoCD Agent events (7.9)
	models.EventAgentDeployed:         {DetailType: models.EventAgentDeployed, Source: models.ApplicationPlaneEventSource, Description: "ArgoCD agent deployed"},
	models.EventAgentConnected:        {DetailType: models.EventAgentConnected, Source: models.ApplicationPlaneEventSource, Description: "ArgoCD agent connected"},
	models.EventAgentDisconnected:     {DetailType: models.EventAgentDisconnected, Source: models.ApplicationPlaneEventSource, Description: "ArgoCD agent disconnected"},
	models.EventAgentHealthChanged:    {DetailType: models.EventAgentHealthChanged, Source: models.ApplicationPlaneEventSource, Description: "ArgoCD agent health changed"},
	models.EventAgentAppSynced:        {DetailType: models.EventAgentAppSynced, Source: models.ApplicationPlaneEventSource, Description: "Agent application synced"},
	models.EventAgentAppHealthChanged: {DetailType: models.EventAgentAppHealthChanged, Source: models.ApplicationPlaneEventSource, Description: "Agent application health changed"},
	models.EventAgentAppStatusChanged: {DetailType: models.EventAgentAppStatusChanged, Source: models.ApplicationPlaneEventSource, Description: "Agent application status changed"},
}

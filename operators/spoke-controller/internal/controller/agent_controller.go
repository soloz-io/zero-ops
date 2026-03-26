package controller

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/soloz-io/zero-ops/operators/spoke-controller/internal/client/hub"
	kagentv1alpha2 "github.com/kagent-dev/kagent/api/v1alpha2"
)

// AgentStatusController reconciles Agent CRDs and syncs status to Hub
type AgentStatusController struct {
	client.Client
	Scheme     *runtime.Scheme
	HubClient  *hub.Client
	ClusterID  string
}

// InfraStatus represents the infrastructure status to sync to Hub
type InfraStatus struct {
	Status   string // provisioning, ready, failed
	Phase    string // Running, Idle, Failed
	Replicas int32
	Message  string
	Error    string
}

// +kubebuilder:rbac:groups=kagent.dev,resources=agents,verbs=get;list;watch
// +kubebuilder:rbac:groups=kagent.dev,resources=agents/status,verbs=get
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch

// Reconcile handles Agent CRD status changes and syncs to Hub Centralised DB
func (r *AgentStatusController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch Agent CRD
	var agent kagentv1alpha2.Agent
	if err := r.Get(ctx, req.NamespacedName, &agent); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Validate required labels before syncing (skip unlabelled CRDs)
	tenantID := agent.Labels["tenant-id"]
	agentID := agent.Labels["agent-id"]
	deploymentID := agent.Labels["deployment-id"]

	if tenantID == "" || agentID == "" || deploymentID == "" {
		// Skip platform agents or unlabelled CRDs - do not sync to Hub DB
		logger.Info("Skipping agent without required labels",
			"name", agent.Name,
			"namespace", agent.Namespace,
			"has_tenant_id", tenantID != "",
			"has_agent_id", agentID != "",
			"has_deployment_id", deploymentID != "")
		return ctrl.Result{}, nil
	}

	// Derive infra status from underlying Deployment
	infraStatus, err := r.deriveInfraStatus(ctx, &agent)
	if err != nil {
		logger.Error(err, "Failed to derive infra status")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, err
	}

	// Write to Hub Centralised DB (triggers NATS event to update AgentRegistry)
	if err := r.syncToHub(ctx, &agent, infraStatus); err != nil {
		logger.Error(err, "Failed to sync status to Hub")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, err
	}

	return ctrl.Result{}, nil
}
// deriveInfraStatus derives infrastructure status from Agent CRD and underlying Deployment
func (r *AgentStatusController) deriveInfraStatus(ctx context.Context, agent *kagentv1alpha2.Agent) (*InfraStatus, error) {
	// Get the underlying Deployment created by Kagent Controller
	var deployment appsv1.Deployment
	deploymentName := fmt.Sprintf("%s-deployment", agent.Name)
	deploymentKey := client.ObjectKey{
		Name:      deploymentName,
		Namespace: agent.Namespace,
	}

	if err := r.Get(ctx, deploymentKey, &deployment); err != nil {
		// Deployment not found or error - agent is still provisioning
		return &InfraStatus{
			Status:   "provisioning",
			Phase:    "Unknown",
			Replicas: 0,
			Message:  "Deployment not found",
			Error:    err.Error(),
		}, nil
	}

	// Derive status from Agent CRD conditions
	status := r.deriveStatusFromAgent(agent)
	
	// Derive phase from Deployment replicas (KEDA scale-to-zero)
	phase := r.derivePhaseFromDeployment(&deployment)

	return &InfraStatus{
		Status:   status,
		Phase:    phase,
		Replicas: deployment.Status.AvailableReplicas,
		Message:  r.getStatusMessage(agent, &deployment),
	}, nil
}

// deriveStatusFromAgent maps Agent CRD conditions to infra status
func (r *AgentStatusController) deriveStatusFromAgent(agent *kagentv1alpha2.Agent) string {
	// Check Agent CRD status conditions
	for _, condition := range agent.Status.Conditions {
		if condition.Type == "Ready" {
			if condition.Status == "True" {
				return "ready"
			}
			if condition.Status == "False" {
				return "failed"
			}
		}
	}
	return "provisioning"
}

// derivePhaseFromDeployment maps Deployment status to phase
func (r *AgentStatusController) derivePhaseFromDeployment(deployment *appsv1.Deployment) string {
	if deployment.Status.AvailableReplicas == 0 {
		return "Idle" // KEDA scaled to zero
	}
	if deployment.Status.AvailableReplicas > 0 {
		return "Running"
	}
	
	// Check for failed conditions
	for _, condition := range deployment.Status.Conditions {
		if condition.Type == appsv1.DeploymentProgressing && condition.Status == "False" {
			return "Failed"
		}
	}
	
	return "Unknown"
}

// getStatusMessage creates a human-readable status message
func (r *AgentStatusController) getStatusMessage(agent *kagentv1alpha2.Agent, deployment *appsv1.Deployment) string {
	if deployment.Status.AvailableReplicas == 0 {
		return "Agent scaled to zero by KEDA"
	}
	if deployment.Status.AvailableReplicas > 0 {
		return fmt.Sprintf("Agent running with %d replicas", deployment.Status.AvailableReplicas)
	}
	return "Agent deployment in progress"
}
// syncToHub writes infra status to Hub Centralised DB via PostgREST
func (r *AgentStatusController) syncToHub(ctx context.Context, agent *kagentv1alpha2.Agent, infraStatus *InfraStatus) error {
	// Extract required labels
	tenantID := agent.Labels["tenant-id"]
	agentID := agent.Labels["agent-id"]
	deploymentID := agent.Labels["deployment-id"]

	payload := hub.AgentInfraStatus{
		TenantID:       tenantID,
		AgentID:        agentID,
		DeploymentID:   deploymentID,
		SpokeClusterID: r.ClusterID,
		Status:         infraStatus.Status,
		Phase:          infraStatus.Phase,
		Replicas:       infraStatus.Replicas,
		Message:        infraStatus.Message,
		Error:          infraStatus.Error,
		LastSyncAt:     time.Now(),
	}

	// POST to Hub-side PostgREST with Bearer JWT
	// Hub DB trigger publishes NATS event → Control Plane subscriber updates AgentRegistry
	return r.HubClient.PostAgentInfraStatus(ctx, payload)
}

// SetupWithManager sets up the controller with the Manager
func (r *AgentStatusController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kagentv1alpha2.Agent{}).
		Watches(
			&source.Kind{Type: &appsv1.Deployment{}},
			handler.EnqueueRequestsFromMapFunc(r.findAgentsForDeployment),
		).
		Named("agent-status-sync").
		Complete(r)
}

// findAgentsForDeployment maps Deployment changes to Agent reconcile requests
func (r *AgentStatusController) findAgentsForDeployment(obj client.Object) []ctrl.Request {
	deployment := obj.(*appsv1.Deployment)
	
	// Find Agent CRDs that own this Deployment
	var agents kagentv1alpha2.AgentList
	if err := r.List(context.Background(), &agents, client.InNamespace(deployment.Namespace)); err != nil {
		return nil
	}

	var requests []ctrl.Request
	for _, agent := range agents.Items {
		// Check if this deployment belongs to the agent
		expectedDeploymentName := fmt.Sprintf("%s-deployment", agent.Name)
		if deployment.Name == expectedDeploymentName {
			requests = append(requests, ctrl.Request{
				NamespacedName: client.ObjectKey{
					Name:      agent.Name,
					Namespace: agent.Namespace,
				},
			})
		}
	}

	return requests
}
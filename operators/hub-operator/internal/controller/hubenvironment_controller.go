package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	opsv1alpha1 "github.com/soloz-io/zero-ops/operators/hub-operator/api/v1alpha1"
)

// HubEnvironmentReconciler reconciles a HubEnvironment object
type HubEnvironmentReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	UncachedClient client.Client
}

//+kubebuilder:rbac:groups=ops.nutgraf.in,resources=hubenvironments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ops.nutgraf.in,resources=hubenvironments/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=ops.nutgraf.in,resources=hubenvironments/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *HubEnvironmentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the HubEnvironment instance
	hubEnv := &opsv1alpha1.HubEnvironment{}
	if err := r.Get(ctx, req.NamespacedName, hubEnv); err != nil {
		if errors.IsNotFound(err) {
			// Object was deleted, nothing to do
			return ctrl.Result{}, nil
		}
		logger.Error(err, "unable to fetch HubEnvironment")
		return ctrl.Result{}, err
	}

	// Check if deletion is requested
	if hubEnv.DeletionTimestamp != nil {
		return r.handleDeletion(ctx, hubEnv)
	}

	// Ensure finalizer is present
	if !containsString(hubEnv.Finalizers, "hubenvironment.finalizer") {
		hubEnv.Finalizers = append(hubEnv.Finalizers, "hubenvironment.finalizer")
		if err := r.Update(ctx, hubEnv); err != nil {
			logger.Error(err, "failed to add finalizer")
			return ctrl.Result{Requeue: true}, err
		}
		return ctrl.Result{RequeueAfter: 1 * 0}, nil
	}

	// ADR-021: Monitor Crossplane role status for observability
	if err := r.validateCrossplaneRoles(ctx, hubEnv); err != nil {
		logger.Error(err, "Crossplane role validation failed")
		// Don't fail reconciliation - just log for observability
	}

	return ctrl.Result{}, nil
}

// validateCrossplaneRoles monitors Crossplane role status for observability
// ADR-021: Crossplane Password Rotation Pattern
// Provides visibility into Crossplane role health without taking ownership
func (r *HubEnvironmentReconciler) validateCrossplaneRoles(ctx context.Context, hubEnv *opsv1alpha1.HubEnvironment) error {
	logger := log.FromContext(ctx)
	namespace := hubEnv.Spec.Database.Namespace

	// Role name to Crossplane Role CR mapping
	roleMappings := map[string]string{
		"mcp_server":       "mcp-server-role",
		"agentregistry":    "agentregistry-role",
		"spoke_controller": "spoke-controller-role",
		"spire_server":     "spire-server-role",
	}

	for roleName, crName := range roleMappings {
		// Get Crossplane Role CR status
		role := &unstructured.Unstructured{}
		role.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "postgresql.sql.crossplane.io",
			Version: "v1alpha1",
			Kind:    "Role",
		})

		if err := r.Get(ctx, client.ObjectKey{
			Name:      crName,
			Namespace: namespace,
		}, role); err != nil {
			if errors.IsNotFound(err) {
				logger.Info("Crossplane Role CR not found", "role", roleName, "cr", crName)
				continue
			}
			return err
		}

		// Check Crossplane role status conditions
		status, found, err := unstructured.NestedMap(role.Object, "status")
		if err != nil {
			logger.Error(err, "Failed to read Crossplane role status", "role", roleName)
			continue
		}

		if !found {
			logger.Info("Crossplane role status not available", "role", roleName)
			continue
		}

		// Check Ready condition
		conditions, _, err := unstructured.NestedSlice(status, "conditions")
		if err != nil {
			logger.Error(err, "Failed to read Crossplane role conditions", "role", roleName)
			continue
		}

		isReady := false
		for _, condition := range conditions {
			if conditionMap, ok := condition.(map[string]interface{}); ok {
				if conditionType, ok := conditionMap["type"].(string); ok && conditionType == "Ready" {
					if conditionStatus, ok := conditionMap["status"].(string); ok {
						isReady = (conditionStatus == "True")
					}
				}
			}
		}

		if !isReady {
			logger.Info("Crossplane role not ready", "role", roleName, "cr", crName, "status", status)
		} else {
			logger.Info("Crossplane role ready", "role", roleName, "cr", crName)
		}
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HubEnvironmentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&opsv1alpha1.HubEnvironment{}).
		Complete(r)
}

// handleDeletion handles the deletion of HubEnvironment
func (r *HubEnvironmentReconciler) handleDeletion(ctx context.Context, hubEnv *opsv1alpha1.HubEnvironment) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Remove finalizer if present
	if containsString(hubEnv.Finalizers, "hubenvironment.finalizer") {
		hubEnv.Finalizers = removeString(hubEnv.Finalizers, "hubenvironment.finalizer")
		if err := r.Update(ctx, hubEnv); err != nil {
			logger.Error(err, "failed to remove finalizer")
			return ctrl.Result{Requeue: true}, err
		}
	}

	return ctrl.Result{}, nil
}

// isCNPGReady checks if the CNPG Cluster is ready
func (r *HubEnvironmentReconciler) isCNPGReady(ctx context.Context, hubEnv *opsv1alpha1.HubEnvironment) (bool, error) {
	cluster := &cnpgv1.Cluster{}
	if err := r.Get(ctx, client.ObjectKey{
		Name:      hubEnv.Spec.Database.ClusterRef,
		Namespace: hubEnv.Spec.Database.Namespace,
	}, cluster); err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}

	// Check if cluster is ready
	for _, condition := range cluster.Status.Conditions {
		if condition.Type == "Ready" && condition.Status == metav1.ConditionTrue {
			return true, nil
		}
	}

	return false, nil
}

// Helper functions
func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

func removeString(slice []string, s string) []string {
	var result []string
	for _, item := range slice {
		if item != s {
			result = append(result, item)
		}
	}
	return result
}

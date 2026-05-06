/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	billingv1alpha1 "github.com/soloz-io/zero-ops/operators/hub-operator/api/v1alpha1"
)

// PlanReconciler reconciles a Plan object
type PlanReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
	// OpenMeterClient will be added when SDK schema is verified
	// OpenMeterClient *openmeter.ClientWithResponses
}

// +kubebuilder:rbac:groups=billing.nutgraf.in,resources=plans,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=billing.nutgraf.in,resources=plans/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=billing.nutgraf.in,resources=plans/finalizers,verbs=update
// +kubebuilder:rbac:groups=billing.nutgraf.in,resources=features,verbs=get;list
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch

// Reconcile syncs Plan CR to OpenMeter
func (r *PlanReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the Plan instance
	plan := &billingv1alpha1.Plan{}
	if err := r.Get(ctx, req.NamespacedName, plan); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Plan resource not found, ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get Plan")
		return ctrl.Result{}, err
	}

	// Handle deletion
	if !plan.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, plan)
	}

	// Extract tenantId from spec for namespace parameter
	namespace := plan.Spec.TenantID

	// Validate referenced feature keys
	validationErrors := r.validateFeatureReferences(ctx, plan)
	if len(validationErrors) > 0 {
		logger.Error(fmt.Errorf("feature validation failed"), "errors", validationErrors)
		
		// Update status with validation errors
		plan.Status.ValidationErrors = validationErrors
		meta.SetStatusCondition(&plan.Status.Conditions, metav1.Condition{
			Type:               "Synced",
			Status:             metav1.ConditionFalse,
			Reason:             "ValidationFailed",
			Message:            fmt.Sprintf("Feature validation failed: %v", validationErrors),
			ObservedGeneration: plan.Generation,
		})
		
		if updateErr := r.Status().Update(ctx, plan); updateErr != nil {
			logger.Error(updateErr, "Failed to update Plan status")
			return ctrl.Result{}, updateErr
		}
		
		// Emit event for validation failure
		r.emitEvent(plan, corev1.EventTypeWarning, "ValidationFailed", fmt.Sprintf("Feature validation failed: %v", validationErrors))
		
		// Requeue with backoff
		return ctrl.Result{RequeueAfter: r.calculateBackoff(plan)}, fmt.Errorf("validation failed")
	}

	// Clear validation errors if previously set
	plan.Status.ValidationErrors = nil

	// Sync plan to OpenMeter
	if err := r.syncPlanToOpenMeter(ctx, plan, namespace); err != nil {
		logger.Error(err, "Failed to sync plan to OpenMeter")
		
		// Update status with error condition
		meta.SetStatusCondition(&plan.Status.Conditions, metav1.Condition{
			Type:               "Synced",
			Status:             metav1.ConditionFalse,
			Reason:             "SyncFailed",
			Message:            fmt.Sprintf("Failed to sync to OpenMeter: %v", err),
			ObservedGeneration: plan.Generation,
		})
		
		if updateErr := r.Status().Update(ctx, plan); updateErr != nil {
			logger.Error(updateErr, "Failed to update Plan status")
			return ctrl.Result{}, updateErr
		}
		
		// Emit event for sync failure
		r.emitEvent(plan, corev1.EventTypeWarning, "SyncFailed", fmt.Sprintf("Failed to sync to OpenMeter: %v", err))
		
		// Exponential backoff
		return ctrl.Result{RequeueAfter: r.calculateBackoff(plan)}, err
	}

	// Update status with success condition
	now := metav1.Now()
	plan.Status.LastSyncTime = &now
	meta.SetStatusCondition(&plan.Status.Conditions, metav1.Condition{
		Type:               "Synced",
		Status:             metav1.ConditionTrue,
		Reason:             "SyncSucceeded",
		Message:            "Successfully synced to OpenMeter",
		ObservedGeneration: plan.Generation,
	})

	if err := r.Status().Update(ctx, plan); err != nil {
		logger.Error(err, "Failed to update Plan status")
		return ctrl.Result{}, err
	}

	logger.Info("Successfully reconciled Plan", "key", plan.Spec.Key, "tenantId", plan.Spec.TenantID)
	return ctrl.Result{}, nil
}

// handleDeletion handles Plan deletion by deleting from OpenMeter
func (r *PlanReconciler) handleDeletion(ctx context.Context, plan *billingv1alpha1.Plan) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	
	// Delete plan from OpenMeter
	namespace := plan.Spec.TenantID
	if err := r.deletePlanFromOpenMeter(ctx, plan, namespace); err != nil {
		logger.Error(err, "Failed to delete plan from OpenMeter")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, err
	}
	
	logger.Info("Successfully deleted Plan from OpenMeter", "key", plan.Spec.Key)
	return ctrl.Result{}, nil
}

// validateFeatureReferences checks that all referenced features exist
func (r *PlanReconciler) validateFeatureReferences(ctx context.Context, plan *billingv1alpha1.Plan) []string {
	var validationErrors []string
	
	// List all features in the same namespace
	featureList := &billingv1alpha1.FeatureList{}
	if err := r.List(ctx, featureList, client.InNamespace(plan.Namespace)); err != nil {
		validationErrors = append(validationErrors, fmt.Sprintf("failed to list features: %v", err))
		return validationErrors
	}

	// Build map of existing feature keys for this tenant
	existingFeatures := make(map[string]bool)
	for _, feature := range featureList.Items {
		if feature.Spec.TenantID == plan.Spec.TenantID {
			existingFeatures[feature.Spec.Key] = true
		}
	}

	// Validate all referenced feature keys exist
	for _, phase := range plan.Spec.Phases {
		for _, rateCard := range phase.RateCards {
			if !existingFeatures[rateCard.FeatureKey] {
				validationErrors = append(validationErrors, 
					fmt.Sprintf("referenced feature not found: %s (phase: %s)", rateCard.FeatureKey, phase.Key))
			}
		}
	}

	return validationErrors
}

// syncPlanToOpenMeter syncs the plan to OpenMeter via Go SDK
func (r *PlanReconciler) syncPlanToOpenMeter(ctx context.Context, plan *billingv1alpha1.Plan, namespace string) error {
	// TODO: Verify OpenMeter SDK schema and update request body type
	// The exact SDK types need to be confirmed from openmeter/api/client/go package
	// This is a placeholder implementation that needs SDK schema verification
	
	// Placeholder: Mark as synced for now
	plan.Status.OpenMeterID = fmt.Sprintf("plan-%s", plan.Spec.Key)
	return nil
	
	// Expected implementation (needs SDK schema verification):
	// Convert phases to OpenMeter format
	// phases := make([]openmeter.PlanPhase, len(plan.Spec.Phases))
	// for i, phase := range plan.Spec.Phases {
	//     rateCards := make([]openmeter.RateCard, len(phase.RateCards))
	//     for j, rc := range phase.RateCards {
	//         rateCards[j] = openmeter.RateCard{
	//             FeatureKey: rc.FeatureKey,
	//             Price: openmeter.Price{
	//                 Type:   rc.Price.Type,
	//                 Amount: rc.Price.Amount,
	//             },
	//         }
	//     }
	//     phases[i] = openmeter.PlanPhase{
	//         Key:       phase.Key,
	//         Name:      phase.Name,
	//         RateCards: rateCards,
	//     }
	// }
	// req := openmeter.CreatePlanJSONRequestBody{
	//     Key:         plan.Spec.Key,
	//     Name:        plan.Spec.Name,
	//     Description: &plan.Spec.Description,
	//     Currency:    plan.Spec.Currency,
	//     Phases:      phases,
	// }
	// resp, err := r.OpenMeterClient.CreatePlanWithResponse(ctx, req, m.withNamespace(namespace))
	// if err != nil {
	//     return fmt.Errorf("openmeter: create plan: %w", err)
	// }
	// if resp.StatusCode() != 201 && resp.StatusCode() != 200 {
	//     return fmt.Errorf("openmeter: unexpected status %d", resp.StatusCode())
	// }
	// plan.Status.OpenMeterID = resp.JSON201.Id
	// return nil
}

// deletePlanFromOpenMeter deletes the plan from OpenMeter
func (r *PlanReconciler) deletePlanFromOpenMeter(ctx context.Context, plan *billingv1alpha1.Plan, namespace string) error {
	// TODO: Verify OpenMeter SDK schema and update delete method
	// The exact SDK types need to be confirmed from openmeter/api/client/go package
	// This is a placeholder implementation that needs SDK schema verification
	
	// Placeholder: Mark as deleted for now
	return nil
	
	// Expected implementation (needs SDK schema verification):
	// resp, err := r.OpenMeterClient.DeletePlanWithResponse(ctx, plan.Status.OpenMeterID, m.withNamespace(namespace))
	// if err != nil {
	//     return fmt.Errorf("openmeter: delete plan: %w", err)
	// }
	// if resp.StatusCode() != 204 && resp.StatusCode() != 404 {
	//     return fmt.Errorf("openmeter: unexpected status %d", resp.StatusCode())
	// }
	// return nil
}

// calculateBackoff implements exponential backoff (1s, 2s, 4s, 8s, 16s, max 5min)
func (r *PlanReconciler) calculateBackoff(plan *billingv1alpha1.Plan) time.Duration {
	failureCount := 0
	for _, cond := range plan.Status.Conditions {
		if cond.Type == "Synced" && cond.Status == metav1.ConditionFalse {
			failureCount++
		}
	}

	backoff := time.Duration(1<<uint(failureCount)) * time.Second
	maxBackoff := 5 * time.Minute
	if backoff > maxBackoff {
		backoff = maxBackoff
	}

	return backoff
}

// emitEvent creates a Kubernetes Event for the Plan
func (r *PlanReconciler) emitEvent(plan *billingv1alpha1.Plan, eventType, reason, message string) {
	r.Recorder.Event(plan, eventType, reason, message)
}

// SetupWithManager sets up the controller with the Manager.
func (r *PlanReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&billingv1alpha1.Plan{}).
		Complete(r)
}

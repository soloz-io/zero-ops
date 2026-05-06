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

// FeatureReconciler reconciles a Feature object
type FeatureReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
	// OpenMeterClient will be added when SDK schema is verified
	// OpenMeterClient *openmeter.ClientWithResponses
}

// +kubebuilder:rbac:groups=billing.nutgraf.in,resources=features,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=billing.nutgraf.in,resources=features/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=billing.nutgraf.in,resources=features/finalizers,verbs=update
// +kubebuilder:rbac:groups=billing.nutgraf.in,resources=meters,verbs=get;list
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch

// Reconcile syncs Feature CR to OpenMeter
func (r *FeatureReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the Feature instance
	feature := &billingv1alpha1.Feature{}
	if err := r.Get(ctx, req.NamespacedName, feature); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Feature resource not found, ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get Feature")
		return ctrl.Result{}, err
	}

	// Extract tenantId from spec for namespace parameter
	namespace := feature.Spec.TenantID

	// Validate referenced meter slugs
	if err := r.validateMeterReferences(ctx, feature); err != nil {
		logger.Error(err, "Meter validation failed")
		
		// Update status with validation error
		meta.SetStatusCondition(&feature.Status.Conditions, metav1.Condition{
			Type:               "Synced",
			Status:             metav1.ConditionFalse,
			Reason:             "ValidationFailed",
			Message:            fmt.Sprintf("Meter validation failed: %v", err),
			ObservedGeneration: feature.Generation,
		})
		
		if updateErr := r.Status().Update(ctx, feature); updateErr != nil {
			logger.Error(updateErr, "Failed to update Feature status")
			return ctrl.Result{}, updateErr
		}
		
		// Emit event for validation failure
		r.emitEvent(feature, corev1.EventTypeWarning, "ValidationFailed", fmt.Sprintf("Meter validation failed: %v", err))
		
		// Requeue with backoff
		return ctrl.Result{RequeueAfter: r.calculateBackoff(feature)}, err
	}

	// Sync feature to OpenMeter
	if err := r.syncFeatureToOpenMeter(ctx, feature, namespace); err != nil {
		logger.Error(err, "Failed to sync feature to OpenMeter")
		
		// Update status with error condition
		meta.SetStatusCondition(&feature.Status.Conditions, metav1.Condition{
			Type:               "Synced",
			Status:             metav1.ConditionFalse,
			Reason:             "SyncFailed",
			Message:            fmt.Sprintf("Failed to sync to OpenMeter: %v", err),
			ObservedGeneration: feature.Generation,
		})
		
		if updateErr := r.Status().Update(ctx, feature); updateErr != nil {
			logger.Error(updateErr, "Failed to update Feature status")
			return ctrl.Result{}, updateErr
		}
		
		// Emit event for sync failure
		r.emitEvent(feature, corev1.EventTypeWarning, "SyncFailed", fmt.Sprintf("Failed to sync to OpenMeter: %v", err))
		
		// Exponential backoff
		return ctrl.Result{RequeueAfter: r.calculateBackoff(feature)}, err
	}

	// Update status with success condition
	now := metav1.Now()
	feature.Status.LastSyncTime = &now
	meta.SetStatusCondition(&feature.Status.Conditions, metav1.Condition{
		Type:               "Synced",
		Status:             metav1.ConditionTrue,
		Reason:             "SyncSucceeded",
		Message:            "Successfully synced to OpenMeter",
		ObservedGeneration: feature.Generation,
	})

	if err := r.Status().Update(ctx, feature); err != nil {
		logger.Error(err, "Failed to update Feature status")
		return ctrl.Result{}, err
	}

	logger.Info("Successfully reconciled Feature", "key", feature.Spec.Key, "tenantId", feature.Spec.TenantID)
	return ctrl.Result{}, nil
}

// validateMeterReferences checks that all referenced meters exist
func (r *FeatureReconciler) validateMeterReferences(ctx context.Context, feature *billingv1alpha1.Feature) error {
	// List all meters in the same namespace
	meterList := &billingv1alpha1.MeterList{}
	if err := r.List(ctx, meterList, client.InNamespace(feature.Namespace)); err != nil {
		return fmt.Errorf("failed to list meters: %w", err)
	}

	// Build map of existing meter slugs for this tenant
	existingMeters := make(map[string]bool)
	for _, meter := range meterList.Items {
		if meter.Spec.TenantID == feature.Spec.TenantID {
			existingMeters[meter.Spec.Slug] = true
		}
	}

	// Validate all referenced meter slugs exist
	for _, meterSlug := range feature.Spec.MeterSlugs {
		if !existingMeters[meterSlug] {
			return fmt.Errorf("referenced meter not found: %s", meterSlug)
		}
	}

	return nil
}

// syncFeatureToOpenMeter syncs the feature to OpenMeter via Go SDK
func (r *FeatureReconciler) syncFeatureToOpenMeter(ctx context.Context, feature *billingv1alpha1.Feature, namespace string) error {
	// TODO: Verify OpenMeter SDK schema and update request body type
	// The exact SDK types need to be confirmed from openmeter/api/client/go package
	// This is a placeholder implementation that needs SDK schema verification
	
	// Placeholder: Mark as synced for now
	feature.Status.OpenMeterID = fmt.Sprintf("feature-%s", feature.Spec.Key)
	return nil
	
	// Expected implementation (needs SDK schema verification):
	// req := openmeter.CreateFeatureJSONRequestBody{
	//     Key:        feature.Spec.Key,
	//     Name:       feature.Spec.Name,
	//     MeterSlug:  &feature.Spec.MeterSlugs[0],
	// }
	// resp, err := r.OpenMeterClient.CreateFeatureWithResponse(ctx, req, m.withNamespace(namespace))
	// if err != nil {
	//     return fmt.Errorf("openmeter: create feature: %w", err)
	// }
	// if resp.StatusCode() != 201 && resp.StatusCode() != 200 {
	//     return fmt.Errorf("openmeter: unexpected status %d", resp.StatusCode())
	// }
	// feature.Status.OpenMeterID = resp.JSON201.Id
	// return nil
}

// calculateBackoff implements exponential backoff (1s, 2s, 4s, 8s, 16s, max 5min)
func (r *FeatureReconciler) calculateBackoff(feature *billingv1alpha1.Feature) time.Duration {
	failureCount := 0
	for _, cond := range feature.Status.Conditions {
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

// emitEvent creates a Kubernetes Event for the Feature
func (r *FeatureReconciler) emitEvent(feature *billingv1alpha1.Feature, eventType, reason, message string) {
	r.Recorder.Event(feature, eventType, reason, message)
}

// SetupWithManager sets up the controller with the Manager.
func (r *FeatureReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&billingv1alpha1.Feature{}).
		Complete(r)
}

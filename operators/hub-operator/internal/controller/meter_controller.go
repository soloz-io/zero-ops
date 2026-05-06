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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	billingv1alpha1 "github.com/soloz-io/zero-ops/operators/hub-operator/api/v1alpha1"
)

// MeterReconciler reconciles a Meter object
type MeterReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// OpenMeterClient would be injected here for actual OpenMeter API calls
	// OpenMeterClient openmeter.ClientInterface
}

// +kubebuilder:rbac:groups=billing.nutgraf.in,resources=meters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=billing.nutgraf.in,resources=meters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=billing.nutgraf.in,resources=meters/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch

// Reconcile syncs Meter CR to OpenMeter
func (r *MeterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the Meter instance
	meter := &billingv1alpha1.Meter{}
	if err := r.Get(ctx, req.NamespacedName, meter); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Meter resource not found, ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get Meter")
		return ctrl.Result{}, err
	}

	// Extract tenantId from spec for namespace parameter
	namespace := meter.Spec.TenantID

	// Sync meter to OpenMeter
	if err := r.syncMeterToOpenMeter(ctx, meter, namespace); err != nil {
		logger.Error(err, "Failed to sync meter to OpenMeter")
		
		// Update status with error condition
		meta.SetStatusCondition(&meter.Status.Conditions, metav1.Condition{
			Type:               "Synced",
			Status:             metav1.ConditionFalse,
			Reason:             "SyncFailed",
			Message:            fmt.Sprintf("Failed to sync to OpenMeter: %v", err),
			ObservedGeneration: meter.Generation,
		})
		
		if updateErr := r.Status().Update(ctx, meter); updateErr != nil {
			logger.Error(updateErr, "Failed to update Meter status")
			return ctrl.Result{}, updateErr
		}
		
		// Emit Kubernetes Event for reconciliation failure
		r.emitEvent(meter, corev1.EventTypeWarning, "SyncFailed", fmt.Sprintf("Failed to sync to OpenMeter: %v", err))
		
		// Exponential backoff: 1s, 2s, 4s, 8s, 16s, max 5min
		return ctrl.Result{RequeueAfter: r.calculateBackoff(meter)}, err
	}

	// Update status with success condition
	now := metav1.Now()
	meter.Status.LastSyncTime = &now
	meta.SetStatusCondition(&meter.Status.Conditions, metav1.Condition{
		Type:               "Synced",
		Status:             metav1.ConditionTrue,
		Reason:             "SyncSucceeded",
		Message:            "Successfully synced to OpenMeter",
		ObservedGeneration: meter.Generation,
	})

	if err := r.Status().Update(ctx, meter); err != nil {
		logger.Error(err, "Failed to update Meter status")
		return ctrl.Result{}, err
	}

	logger.Info("Successfully reconciled Meter", "slug", meter.Spec.Slug, "tenantId", meter.Spec.TenantID)
	return ctrl.Result{}, nil
}

// syncMeterToOpenMeter syncs the meter to OpenMeter via Go SDK
func (r *MeterReconciler) syncMeterToOpenMeter(ctx context.Context, meter *billingv1alpha1.Meter, namespace string) error {
	// TODO: Implement actual OpenMeter SDK call
	// Example:
	// req := openmeter.CreateMeterRequest{
	//     Slug:          meter.Spec.Slug,
	//     Description:   meter.Spec.Description,
	//     Aggregation:   meter.Spec.Aggregation,
	//     EventType:     meter.Spec.EventType,
	//     ValueProperty: meter.Spec.ValueProperty,
	//     GroupBy:       meter.Spec.GroupBy,
	// }
	// resp, err := r.OpenMeterClient.CreateMeter(ctx, namespace, req)
	// if err != nil {
	//     return fmt.Errorf("openmeter: create meter: %w", err)
	// }
	// meter.Status.OpenMeterID = resp.ID
	
	// Placeholder implementation
	return nil
}

// calculateBackoff implements exponential backoff (1s, 2s, 4s, 8s, 16s, max 5min)
func (r *MeterReconciler) calculateBackoff(meter *billingv1alpha1.Meter) time.Duration {
	// Check how many times sync has failed by looking at condition transitions
	failureCount := 0
	for _, cond := range meter.Status.Conditions {
		if cond.Type == "Synced" && cond.Status == metav1.ConditionFalse {
			failureCount++
		}
	}

	// Exponential backoff: 2^n seconds, max 5 minutes
	backoff := time.Duration(1<<uint(failureCount)) * time.Second
	maxBackoff := 5 * time.Minute
	if backoff > maxBackoff {
		backoff = maxBackoff
	}

	return backoff
}

// emitEvent creates a Kubernetes Event for the Meter
func (r *MeterReconciler) emitEvent(meter *billingv1alpha1.Meter, eventType, reason, message string) {
	// TODO: Implement event emission using EventRecorder
	// r.Recorder.Event(meter, eventType, reason, message)
}

// SetupWithManager sets up the controller with the Manager.
func (r *MeterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&billingv1alpha1.Meter{}).
		Complete(r)
}

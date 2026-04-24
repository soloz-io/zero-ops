package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	infisicalclient "github.com/soloz-io/zero-ops/operators/hub-operator/internal/client"
	"github.com/soloz-io/zero-ops/operators/hub-operator/internal/secrets"
)

// SpokePoolReconciler reconciles SpokePool XRs to generate per-spoke secrets
type SpokePoolReconciler struct {
	client.Client
	// UncachedClient reads secrets directly from API server (bypasses cache)
	UncachedClient client.Client
	Scheme         *runtime.Scheme
}

//+kubebuilder:rbac:groups=nutgraf.in,resources=spokepools,verbs=get;list;watch
//+kubebuilder:rbac:groups=nutgraf.in,resources=spokepools/status,verbs=get;update;patch

// Reconcile generates crossplane-admin password for each SpokePool
// Implements Infisical-only idempotency: queries Infisical API directly (no Hub K8s secret)
func (r *SpokePoolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// 1. Fetch SpokePool XR using dynamic client (unstructured)
	spokePool := &unstructured.Unstructured{}
	spokePool.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "nutgraf.in",
		Version: "v1alpha1",
		Kind:    "SpokePool",
	})

	if err := r.Get(ctx, req.NamespacedName, spokePool); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	spokeName := spokePool.GetName()
	logger.Info("Reconciling SpokePool", "spoke", spokeName)

	// 2. Create Infisical client
	infisicalClient, err := infisicalclient.NewInfisicalClient(ctx, r.UncachedClient, "")
	if err != nil {
		logger.Error(err, "Failed to create Infisical client", "spoke", spokeName)
		return ctrl.Result{}, fmt.Errorf("failed to create Infisical client: %w", err)
	}

	// 3. Check if this is first-time creation (status condition not set)
	isFirstTime := !r.isStatusConditionTrue(spokePool, "CrossplaneAdminSecretGenerated")
	
	// 4. IDEMPOTENCY: Check if password already exists in Infisical (SOURCE OF TRUTH)
	infisicalKey := fmt.Sprintf("%s-crossplane-admin-password", spokeName)
	exists, err := infisicalClient.SecretExists(ctx, "hub-platform", "dev", "/", infisicalKey)
	if err != nil {
		logger.Error(err, "Failed to check Infisical for existing password", "spoke", spokeName, "key", infisicalKey)
		return ctrl.Result{}, fmt.Errorf("failed to check Infisical: %w", err)
	}

	if exists {
		// Password already in Infisical, skip generation
		logger.Info("Password already exists in Infisical, skipping generation", "spoke", spokeName, "key", infisicalKey)
		
		// Set status condition with accurate message (already exists, not generated)
		if err := r.updateStatusCondition(ctx, spokePool, spokeName, true, true); err != nil {
			// Don't fail reconciliation if status update fails
			return ctrl.Result{}, nil
		}
		
		return ctrl.Result{}, nil
	}

	// 5. Password missing in Infisical
	if !isFirstTime {
		// NOT first-time creation AND password missing → CRITICAL ERROR
		// This means password was deleted from Infisical after initial creation
		// Require manual intervention to prevent breaking Spoke's CNPG connection
		logger.Error(nil, "CRITICAL: Password missing from Infisical but SpokePool was already provisioned. Manual intervention required.", 
			"spoke", spokeName, "key", infisicalKey)
		
		if err := r.updateStatusCondition(ctx, spokePool, spokeName, false); err != nil {
			return ctrl.Result{}, nil
		}
		
		return ctrl.Result{}, fmt.Errorf("password missing from Infisical for already-provisioned SpokePool %s - manual recovery required", spokeName)
	}

	// 6. First-time creation: Generate new password
	// SECURITY: Use secrets.GenerateSecurePassword() (32-char hex, crypto/rand)
	// NEVER use metadata.uid (not cryptographically secure, visible to cluster readers)
	password, err := secrets.GenerateSecurePassword()
	if err != nil {
		logger.Error(err, "Failed to generate password", "spoke", spokeName)
		return ctrl.Result{}, fmt.Errorf("failed to generate password: %w", err)
	}

	logger.Info("First-time creation: generated new password", "spoke", spokeName, "passwordLength", len(password))

	// 7. Upload to Infisical (idempotent - CreateOrUpdateSecretRaw handles upsert)
	if err := infisicalClient.CreateOrUpdateSecretRaw(
		ctx,
		"hub-platform",  // projectSlug
		"dev",           // environmentSlug
		"/",             // secretPath
		infisicalKey,    // key
		password,        // value
	); err != nil {
		logger.Error(err, "Failed to upload secret to Infisical", "spoke", spokeName, "key", infisicalKey)
		return ctrl.Result{}, fmt.Errorf("failed to upload to Infisical: %w", err)
	}

	logger.Info("Uploaded secret to Infisical", "spoke", spokeName, "key", infisicalKey)

	// 8. Set status condition with accurate message (generated, not already existed)
	if err := r.updateStatusCondition(ctx, spokePool, spokeName, true, false); err != nil {
		// Don't fail reconciliation if status update fails
		return ctrl.Result{}, nil
	}

	logger.Info("SpokePool reconciliation complete", "spoke", spokeName)
	return ctrl.Result{}, nil
}

// isStatusConditionTrue checks if a condition is set to True
func (r *SpokePoolReconciler) isStatusConditionTrue(spokePool *unstructured.Unstructured, conditionType string) bool {
	conditions, found, err := unstructured.NestedSlice(spokePool.Object, "status", "conditions")
	if err != nil || !found {
		return false
	}

	for _, c := range conditions {
		if condMap, ok := c.(map[string]interface{}); ok {
			if condMap["type"] == conditionType && condMap["status"] == string(metav1.ConditionTrue) {
				return true
			}
		}
	}
	
	return false
}

// updateStatusCondition sets CrossplaneAdminSecretGenerated condition on SpokePool XR
func (r *SpokePoolReconciler) updateStatusCondition(ctx context.Context, spokePool *unstructured.Unstructured, spokeName string, success bool, alreadyExisted bool) error {
	logger := log.FromContext(ctx)
	
	conditions, found, err := unstructured.NestedSlice(spokePool.Object, "status", "conditions")
	if err != nil {
		logger.Error(err, "Failed to get status conditions", "spoke", spokeName)
		return err
	}

	if !found {
		conditions = []interface{}{}
	}

	// Convert to metav1.Condition slice
	var metaConditions []metav1.Condition
	for _, c := range conditions {
		if condMap, ok := c.(map[string]interface{}); ok {
			cond := metav1.Condition{
				Type:               condMap["type"].(string),
				Status:             metav1.ConditionStatus(condMap["status"].(string)),
				Reason:             condMap["reason"].(string),
				Message:            condMap["message"].(string),
				ObservedGeneration: spokePool.GetGeneration(),
			}
			metaConditions = append(metaConditions, cond)
		}
	}

	// Set or update condition based on success/failure and whether password already existed
	var condition metav1.Condition
	if success {
		if alreadyExisted {
			condition = metav1.Condition{
				Type:               "CrossplaneAdminSecretGenerated",
				Status:             metav1.ConditionTrue,
				Reason:             "AlreadyExists",
				Message:            fmt.Sprintf("Password already exists in Infisical at %s-crossplane-admin-password", spokeName),
				ObservedGeneration: spokePool.GetGeneration(),
			}
		} else {
			condition = metav1.Condition{
				Type:               "CrossplaneAdminSecretGenerated",
				Status:             metav1.ConditionTrue,
				Reason:             "Generated",
				Message:            fmt.Sprintf("Password generated and uploaded to Infisical at %s-crossplane-admin-password", spokeName),
				ObservedGeneration: spokePool.GetGeneration(),
			}
		}
	} else {
		condition = metav1.Condition{
			Type:               "CrossplaneAdminSecretGenerated",
			Status:             metav1.ConditionFalse,
			Reason:             "PasswordMissing",
			Message:            "CRITICAL: Password missing from Infisical for already-provisioned SpokePool. Manual recovery required.",
			ObservedGeneration: spokePool.GetGeneration(),
		}
	}
	
	meta.SetStatusCondition(&metaConditions, condition)

	// Convert back to unstructured
	var newConditions []interface{}
	for _, c := range metaConditions {
		newConditions = append(newConditions, map[string]interface{}{
			"type":               c.Type,
			"status":             string(c.Status),
			"reason":             c.Reason,
			"message":            c.Message,
			"observedGeneration": c.ObservedGeneration,
			"lastTransitionTime": metav1.Now().Format("2006-01-02T15:04:05Z"),
		})
	}

	if err := unstructured.SetNestedSlice(spokePool.Object, newConditions, "status", "conditions"); err != nil {
		logger.Error(err, "Failed to set status conditions", "spoke", spokeName)
		return err
	}

	if err := r.Status().Update(ctx, spokePool); err != nil {
		logger.Error(err, "Failed to update status", "spoke", spokeName)
		return err
	}
	
	return nil
}

// SetupWithManager sets up the controller with the Manager
func (r *SpokePoolReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Watch SpokePool XRs using unstructured client
	spokePoolGVK := schema.GroupVersionKind{
		Group:   "nutgraf.in",
		Version: "v1alpha1",
		Kind:    "SpokePool",
	}

	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(spokePoolGVK)

	return ctrl.NewControllerManagedBy(mgr).
		For(u).
		Complete(r)
}

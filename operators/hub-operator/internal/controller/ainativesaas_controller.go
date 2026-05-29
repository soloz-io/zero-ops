package controller

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/soloz-io/zero-ops/operators/hub-operator/internal/secrets"
)

// AINativeSaaSReconciler provisions tenant resources into Infisical when an
// AINativeSaaS XR is created. This unblocks the Spoke ESO ExternalSecrets which
// wait for secrets at /spoke-pool/<cellId>/tenants/<tenantId>/<secret-name>.
//
// Currently handles:
//   1. db-credentials — PostgreSQL user credentials for the tenant database.
//      ESO remoteRef.key splits on the last '/' so secret NAME is "db-credentials"
//      and FOLDER path is /spoke-pool/<cellId>/tenants/<tenantId>.
//      ADR-003 Pattern A2a: Kube-SBT → Infisical ONLY → ESO → Spoke K8s Secret.
//   2. infisical-credentials — Machine Identity credentials for the tenant SDK
//      workload. ADR-003, ADR-019: scoped identity for runtime plugin resolution.
//
// Mirrors SpokePoolReconciler (ADR-003 Pattern A2b) for consistency.
type AINativeSaaSReconciler struct {
	client.Client
	InfisicalClient *secrets.InfisicalClient
}

//+kubebuilder:rbac:groups=nutgraf.in,resources=ainativesaases,verbs=get;list;watch
//+kubebuilder:rbac:groups=nutgraf.in,resources=ainativesaases/status,verbs=get;update;patch

// Reconcile provisions all tenant resources into Infisical on AINativeSaaS creation or update.
//
// Idempotency contract (ADR-003):
//   - If credentials exist in Infisical → skip, set condition AlreadyExists.
//   - If credentials missing AND first-time → generate + upload, set condition Seeded.
//   - If credentials missing AND NOT first-time → FAIL, require manual intervention.
func (r *AINativeSaaSReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// 1. Fetch AINativeSaaS XR via dynamic unstructured client
	ainativesaas := &unstructured.Unstructured{}
	ainativesaas.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "nutgraf.in",
		Version: "v1alpha1",
		Kind:    "AINativeSaaS",
	})

	if err := r.Get(ctx, req.NamespacedName, ainativesaas); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	tenantId, found, err := unstructured.NestedString(ainativesaas.Object, "spec", "tenantId")
	if err != nil || !found || tenantId == "" {
		logger.Info("AINativeSaaS missing spec.tenantId, skipping", "name", req.Name)
		return ctrl.Result{}, nil
	}

	cellId, found, err := unstructured.NestedString(ainativesaas.Object, "spec", "cellId")
	if err != nil || !found || cellId == "" {
		logger.Info("AINativeSaaS missing spec.cellId, skipping", "name", req.Name)
		return ctrl.Result{}, nil
	}

	logger.Info("Reconciling TenantDatabase credentials", "tenant", tenantId, "cell", cellId)

	// ADR-031: Delegate to InfisicalClient for tenant secret provisioning.
	// isFirstTime is always true because EnsureTenantFolderAndCredentials has its own
	// idempotency check. This handles existing tenants that were provisioned before
	// the ADR-031 controller code was deployed.
	result, err := r.InfisicalClient.EnsureTenantFolderAndCredentials(ctx, cellId, tenantId, true)
	if err != nil {
		logger.Error(err, "Failed to ensure tenant credentials in Infisical", "tenant", tenantId, "cell", cellId)
		if result != nil && result.Result == secrets.EnsureMissing {
			_ = r.setCondition(ctx, ainativesaas, tenantId, conditionMissing)
		}
		return ctrl.Result{}, err
	}

	switch result.Result {
	case secrets.EnsureAlreadyExists:
		return ctrl.Result{}, r.setCondition(ctx, ainativesaas, tenantId, conditionAlreadyExists)
	case secrets.EnsureCreated:
		return ctrl.Result{}, r.setCondition(ctx, ainativesaas, tenantId, conditionSeeded)
	default:
		return ctrl.Result{}, nil
	}
}

// conditionState enumerates the three terminal states for TenantDBCredentialsSeeded.
type conditionState int

const (
	conditionSeeded       conditionState = iota // first-time: generated and uploaded
	conditionAlreadyExists                      // idempotent: already present in Infisical
	conditionMissing                            // error: missing post-provisioning
)

// isConditionTrue checks whether a named condition is Status=True on the unstructured object.
func (r *AINativeSaaSReconciler) isConditionTrue(obj *unstructured.Unstructured, condType string) bool {
	conditions, found, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if err != nil || !found {
		return false
	}
	for _, c := range conditions {
		condMap, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		if condMap["type"] == condType && condMap["status"] == string(metav1.ConditionTrue) {
			return true
		}
	}
	return false
}

// setCondition writes the TenantDBCredentialsSeeded condition back to the AINativeSaaS status.
func (r *AINativeSaaSReconciler) setCondition(ctx context.Context, obj *unstructured.Unstructured, tenantId string, state conditionState) error {
	logger := log.FromContext(ctx)

	// Re-fetch the latest version to avoid resource version conflicts.
	latest := &unstructured.Unstructured{}
	latest.SetGroupVersionKind(obj.GroupVersionKind())
	if err := r.Get(ctx, client.ObjectKeyFromObject(obj), latest); err != nil {
		logger.Error(err, "Failed to re-fetch AINativeSaaS before status update", "tenant", tenantId)
		return err
	}

	existing, found, err := unstructured.NestedSlice(latest.Object, "status", "conditions")
	if err != nil || !found {
		existing = []interface{}{}
	}

	// Deserialise existing conditions
	var metaConditions []metav1.Condition
	for _, c := range existing {
		condMap, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		typeVal, _ := condMap["type"].(string)
		statusVal, _ := condMap["status"].(string)
		reasonVal, _ := condMap["reason"].(string)
		messageVal, _ := condMap["message"].(string)
		if typeVal == "" || statusVal == "" {
			continue
		}
		metaConditions = append(metaConditions, metav1.Condition{
			Type:    typeVal,
			Status:  metav1.ConditionStatus(statusVal),
			Reason:  reasonVal,
			Message: messageVal,
		})
	}

	var cond metav1.Condition
	switch state {
	case conditionSeeded:
		cond = metav1.Condition{
			Type:    "TenantDBCredentialsSeeded",
			Status:  metav1.ConditionTrue,
			Reason:  "Seeded",
			Message: fmt.Sprintf("DB credentials generated and uploaded to Infisical for tenant %s", tenantId),
		}
	case conditionAlreadyExists:
		cond = metav1.Condition{
			Type:    "TenantDBCredentialsSeeded",
			Status:  metav1.ConditionTrue,
			Reason:  "AlreadyExists",
			Message: fmt.Sprintf("DB credentials already present in Infisical for tenant %s", tenantId),
		}
	case conditionMissing:
		cond = metav1.Condition{
			Type:    "TenantDBCredentialsSeeded",
			Status:  metav1.ConditionFalse,
			Reason:  "CredentialsMissing",
			Message: fmt.Sprintf("CRITICAL: DB credentials missing from Infisical for already-provisioned tenant %s. Manual recovery required.", tenantId),
		}
	}

	meta.SetStatusCondition(&metaConditions, cond)

	// Serialise back including observedGeneration — now declared in AINativeSaaS XRD status schema.
	var updated []interface{}
	for _, c := range metaConditions {
		updated = append(updated, map[string]interface{}{
			"type":               c.Type,
			"status":             string(c.Status),
			"reason":             c.Reason,
			"message":            c.Message,
			"observedGeneration": latest.GetGeneration(),
			"lastTransitionTime": metav1.Now().Format("2006-01-02T15:04:05Z"),
		})
	}

	if err := unstructured.SetNestedSlice(latest.Object, updated, "status", "conditions"); err != nil {
		logger.Error(err, "Failed to set status conditions", "tenant", tenantId)
		return err
	}

	if err := r.Status().Update(ctx, latest); err != nil {
		logger.Error(err, "Failed to update status", "tenant", tenantId)
		return err
	}

	return nil
}

// SetupWithManager registers the controller to watch AINativeSaaS XRs.
func (r *AINativeSaaSReconciler) SetupWithManager(mgr ctrl.Manager) error {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "nutgraf.in",
		Version: "v1alpha1",
		Kind:    "AINativeSaaS",
	})

	return ctrl.NewControllerManagedBy(mgr).
		For(u).
		Complete(r)
}

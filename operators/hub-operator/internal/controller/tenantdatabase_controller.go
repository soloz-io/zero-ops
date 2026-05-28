package controller

import (
	"context"
	"fmt"

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

// TenantDatabaseReconciler seeds tenant DB credentials into Infisical when an
// AINativeSaaS XR is created. This unblocks the Spoke ESO ExternalSecret which
// waits for credentials at /spoke-pool/<cellId>/tenants/<tenantId>/db-credentials.
//
// Implements ADR-003 Pattern A2a: Kube-SBT → Infisical ONLY → ESO → Spoke K8s Secret.
// Mirrors SpokePoolReconciler (ADR-003 Pattern A2b) for consistency.
type TenantDatabaseReconciler struct {
	client.Client
	// UncachedClient reads secrets directly from API server (bypasses cache transformer)
	UncachedClient client.Client
	Scheme         *runtime.Scheme
}

//+kubebuilder:rbac:groups=nutgraf.in,resources=ainativesaases,verbs=get;list;watch
//+kubebuilder:rbac:groups=nutgraf.in,resources=ainativesaases/status,verbs=get;update;patch

// Reconcile seeds tenant database credentials into Infisical on first-time AINativeSaaS creation.
//
// Idempotency contract (ADR-003):
//   - If credentials exist in Infisical → skip, set condition AlreadyExists.
//   - If credentials missing AND first-time → generate + upload, set condition Seeded.
//   - If credentials missing AND NOT first-time → FAIL, require manual intervention.
func (r *TenantDatabaseReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
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

	// 2. Create Infisical client (authenticates via infisical-auth secret in platform-ops)
	infisicalClient, err := infisicalclient.NewInfisicalClient(ctx, r.UncachedClient, "")
	if err != nil {
		logger.Error(err, "Failed to create Infisical client", "tenant", tenantId)
		return ctrl.Result{}, fmt.Errorf("failed to create Infisical client: %w", err)
	}

	// 3. Determine first-time vs subsequent reconcile via status condition
	isFirstTime := !r.isConditionTrue(ainativesaas, "TenantDBCredentialsSeeded")

	// 4. IDEMPOTENCY: Infisical is the source of truth — check before generating.
	// ADR-003: ESO remoteRef.key=/spoke-pool/<cellId>/tenants/<tenantId>/db-credentials
	// with property: username/password — ESO Infisical provider treats key as folder path
	// and property as the secret name within that folder.
	infisicalPath := fmt.Sprintf("/spoke-pool/%s/tenants/%s/db-credentials", cellId, tenantId)

	usernameExists, err := infisicalClient.SecretExists(ctx, "hub-platform", "dev", infisicalPath, "username")
	if err != nil {
		logger.Error(err, "Failed to check Infisical for username", "tenant", tenantId, "path", infisicalPath)
		return ctrl.Result{}, fmt.Errorf("failed to check Infisical: %w", err)
	}

	passwordExists, err := infisicalClient.SecretExists(ctx, "hub-platform", "dev", infisicalPath, "password")
	if err != nil {
		logger.Error(err, "Failed to check Infisical for password", "tenant", tenantId, "path", infisicalPath)
		return ctrl.Result{}, fmt.Errorf("failed to check Infisical: %w", err)
	}

	if usernameExists && passwordExists {
		logger.Info("Credentials already exist in Infisical, skipping generation", "tenant", tenantId, "path", infisicalPath)
		return ctrl.Result{}, r.setCondition(ctx, ainativesaas, tenantId, conditionAlreadyExists)
	}

	// 5. Credentials missing — enforce idempotency contract
	if !isFirstTime {
		// ADR-003: credentials missing post-provisioning → FAIL, do not regenerate.
		// Regenerating would silently break the live PostgreSQL role.
		logger.Error(nil,
			"CRITICAL: DB credentials missing from Infisical for already-provisioned tenant. Manual intervention required.",
			"tenant", tenantId, "path", infisicalPath)
		if err := r.setCondition(ctx, ainativesaas, tenantId, conditionMissing); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, fmt.Errorf(
			"DB credentials missing from Infisical for already-provisioned tenant %s — manual recovery required", tenantId)
	}

	// 6. First-time: generate credentials and upload to Infisical ONLY (no Hub K8s secret).
	// Username follows the convention used in tenantdatabase-spoke.yaml: tenant-<id>-user
	username := fmt.Sprintf("tenant-%s-user", tenantId)

	// ADR-030: full-entropy password; urlquery encoding handled by ESO template at consumption.
	password, err := secrets.GenerateSecurePassword()
	if err != nil {
		logger.Error(err, "Failed to generate password", "tenant", tenantId)
		return ctrl.Result{}, fmt.Errorf("failed to generate password for tenant %s: %w", tenantId, err)
	}

	logger.Info("Generated credentials for tenant", "tenant", tenantId, "usernameLength", len(username), "passwordLength", len(password))

	// 7. Ensure full Infisical folder hierarchy including db-credentials subfolder.
	// ESO Infisical provider uses key as folder path, property as secret name within it.
	// All folders in the path must exist before secrets can be placed inside them.
	if err := infisicalClient.EnsureTenantFolder(ctx, "hub-platform", "dev", cellId, tenantId); err != nil {
		logger.Error(err, "Failed to ensure Infisical folder hierarchy", "tenant", tenantId, "cell", cellId)
		return ctrl.Result{}, fmt.Errorf("failed to ensure Infisical folder hierarchy for tenant %s: %w", tenantId, err)
	}

	// 8. Upload username to Infisical at infisicalPath (the db-credentials folder)
	if err := infisicalClient.CreateOrUpdateSecretRaw(ctx, "hub-platform", "dev", infisicalPath, "username", username); err != nil {
		logger.Error(err, "Failed to upload username to Infisical", "tenant", tenantId, "path", infisicalPath)
		return ctrl.Result{}, fmt.Errorf("failed to upload username to Infisical for tenant %s: %w", tenantId, err)
	}

	// 9. Upload password to Infisical at infisicalPath (the db-credentials folder)
	if err := infisicalClient.CreateOrUpdateSecretRaw(ctx, "hub-platform", "dev", infisicalPath, "password", password); err != nil {
		logger.Error(err, "Failed to upload password to Infisical", "tenant", tenantId, "path", infisicalPath)
		return ctrl.Result{}, fmt.Errorf("failed to upload password to Infisical for tenant %s: %w", tenantId, err)
	}

	logger.Info("Tenant DB credentials seeded in Infisical", "tenant", tenantId, "path", infisicalPath)

	// 10. Set status condition — Crossplane composition can now proceed
	return ctrl.Result{}, r.setCondition(ctx, ainativesaas, tenantId, conditionSeeded)
}

// conditionState enumerates the three terminal states for TenantDBCredentialsSeeded.
type conditionState int

const (
	conditionSeeded       conditionState = iota // first-time: generated and uploaded
	conditionAlreadyExists                      // idempotent: already present in Infisical
	conditionMissing                            // error: missing post-provisioning
)

// isConditionTrue checks whether a named condition is Status=True on the unstructured object.
func (r *TenantDatabaseReconciler) isConditionTrue(obj *unstructured.Unstructured, condType string) bool {
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
func (r *TenantDatabaseReconciler) setCondition(ctx context.Context, obj *unstructured.Unstructured, tenantId string, state conditionState) error {
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
func (r *TenantDatabaseReconciler) SetupWithManager(mgr ctrl.Manager) error {
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

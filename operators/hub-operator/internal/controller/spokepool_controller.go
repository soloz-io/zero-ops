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

// SpokePoolReconciler reconciles SpokePool XRs to generate per-spoke secrets
type SpokePoolReconciler struct {
	client.Client
	InfisicalClient *secrets.InfisicalClient
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

	// ADR-031: Delegate to InfisicalClient for Machine Identity lifecycle.
	// isFirstTime is always true because EnsureInfisicalCredentials performs its own
	// idempotency check by querying Infisical directly. Passing false would prevent
	// credential creation for existing SpokePools whose Infisical paths are empty
	// (e.g. first deployment of ADR-031 controller code).
	result, err := r.InfisicalClient.EnsureInfisicalCredentials(ctx, spokeName, true)
	if err != nil {
		logger.Error(err, "Failed to ensure Infisical credentials for SpokePool", "spoke", spokeName)
		if result != nil && result.Result == secrets.EnsureMissing {
			_ = r.updateStatusCondition(ctx, spokePool, spokeName, false, false)
		}
		return ctrl.Result{}, err
	}

	// create bootstrap certificate for ArgoCD Agent mTLS via cert-manager (ADR-035)
	if err := r.ensureBootstrapCertificate(ctx, spokeName); err != nil {
		logger.Error(err, "Failed to ensure bootstrap certificate", "spoke", spokeName)
		// Non-fatal: Spoke can bootstrap without a cert-manager Certificate CR,
		// but ArgoCD Agent mTLS won't work until it's created.
	}

	// create SpokeMachineIdentity CR for identity lifecycle (spoke-identity-operator reconciles)
	if err := r.ensureSpokeMachineIdentity(ctx, spokeName); err != nil {
		logger.Error(err, "Failed to ensure SpokeMachineIdentity", "spoke", spokeName)
		// Non-fatal: spoke-identity-operator will reconcile once CR exists.
	}

	return ctrl.Result{}, r.updateStatusCondition(ctx, spokePool, spokeName, true, result.Result == secrets.EnsureAlreadyExists)
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

// updateStatusCondition sets CrossplaneAdminSecretGenerated condition on SpokePool XR.
// The condition tracks Machine Identity credentials at /spoke-pool/<cellId>/shared/infisical-credentials
// (ADR-031 Cell-Based Identity Topology).
func (r *SpokePoolReconciler) updateStatusCondition(ctx context.Context, spokePool *unstructured.Unstructured, spokeName string, success bool, alreadyExisted bool) error {
	logger := log.FromContext(ctx)

	// Re-fetch the latest version to avoid resource version conflicts when two
	// replicas reconcile the same SpokePool simultaneously.
	latest := &unstructured.Unstructured{}
	latest.SetGroupVersionKind(spokePool.GroupVersionKind())
	if err := r.Get(ctx, client.ObjectKeyFromObject(spokePool), latest); err != nil {
		logger.Error(err, "Failed to re-fetch SpokePool before status update", "spoke", spokeName)
		return err
	}

	conditions, found, err := unstructured.NestedSlice(latest.Object, "status", "conditions")
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
			// Skip conditions with missing required fields
			typeVal, typeOk := condMap["type"].(string)
			statusVal, statusOk := condMap["status"].(string)
			reasonVal, reasonOk := condMap["reason"].(string)
			messageVal, messageOk := condMap["message"].(string)

			if !typeOk || !statusOk || !reasonOk || !messageOk {
				continue
			}

			metaConditions = append(metaConditions, metav1.Condition{
				Type:    typeVal,
				Status:  metav1.ConditionStatus(statusVal),
				Reason:  reasonVal,
				Message: messageVal,
			})
		}
	}

	// Set or update condition based on success/failure and whether credentials already existed
	var condition metav1.Condition
	if success {
		if alreadyExisted {
			condition = metav1.Condition{
				Type:    "CrossplaneAdminSecretGenerated",
				Status:  metav1.ConditionTrue,
				Reason:  "AlreadyExists",
				Message: fmt.Sprintf("Machine Identity credentials already exist in Infisical at /spoke-pool/%s/shared/infisical-credentials", spokeName),
			}
		} else {
			condition = metav1.Condition{
				Type:    "CrossplaneAdminSecretGenerated",
				Status:  metav1.ConditionTrue,
				Reason:  "Generated",
				Message: fmt.Sprintf("Machine Identity created and credentials uploaded to Infisical at /spoke-pool/%s/shared/infisical-credentials", spokeName),
			}
		}
	} else {
		condition = metav1.Condition{
			Type:    "CrossplaneAdminSecretGenerated",
			Status:  metav1.ConditionFalse,
			Reason:  "CredentialsMissing",
			Message: "CRITICAL: Machine Identity credentials missing from Infisical for already-provisioned SpokePool. Manual recovery required.",
		}
	}

	meta.SetStatusCondition(&metaConditions, condition)

	// Serialise back including observedGeneration — now declared in SpokePool XRD status schema.
	var newConditions []interface{}
	for _, c := range metaConditions {
		newConditions = append(newConditions, map[string]interface{}{
			"type":               c.Type,
			"status":             string(c.Status),
			"reason":             c.Reason,
			"message":            c.Message,
			"observedGeneration": latest.GetGeneration(),
			"lastTransitionTime": metav1.Now().Format("2006-01-02T15:04:05Z"),
		})
	}

	if err := unstructured.SetNestedSlice(latest.Object, newConditions, "status", "conditions"); err != nil {
		logger.Error(err, "Failed to set status conditions", "spoke", spokeName)
		return err
	}

	if err := r.Status().Update(ctx, latest); err != nil {
		logger.Error(err, "Failed to update status", "spoke", spokeName)
		return err
	}

	return nil
}

// ensureBootstrapCertificate creates a 72-hour Certificate CR for ArgoCD Agent mTLS bootstrap.
// cert-manager + infisical-issuer fulfills this Certificate declaratively per ADR-035.
// The resulting Secret is distributed to the Spoke via Crossplane ClusterResourceSet.
func (r *SpokePoolReconciler) ensureBootstrapCertificate(ctx context.Context, spokeName string) error {
	logger := log.FromContext(ctx)

	cert := &unstructured.Unstructured{}
	cert.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cert-manager.io",
		Version: "v1",
		Kind:    "Certificate",
	})
	cert.SetName(fmt.Sprintf("argocd-agent-%s", spokeName))
	cert.SetNamespace("platform-ops")

	// Check if already exists (idempotent)
	if err := r.Get(ctx, client.ObjectKeyFromObject(cert), cert); err == nil {
		logger.Info("Bootstrap Certificate already exists", "certificate", cert.GetName())
		return nil
	}

	cert.Object = map[string]interface{}{
		"metadata": map[string]interface{}{
			"name":      cert.GetName(),
			"namespace": cert.GetNamespace(),
			"labels": map[string]interface{}{
				"platform.nutgraf.in/bootstrap": "true",
				"platform.nutgraf.in/spoke":     spokeName,
			},
		},
		"spec": map[string]interface{}{
			"commonName":  fmt.Sprintf("argocd-agent.%s", spokeName),
			"duration":    "72h",
			"renewBefore": "24h",
			"isCA":        false,
			"usages": []interface{}{
				"server auth",
				"client auth",
			},
			"issuerRef": map[string]interface{}{
				"name": "infisical-issuer",
				"kind": "Issuer",
			},
			"secretName": fmt.Sprintf("argocd-agent-%s-tls", spokeName),
		},
	}

	if err := r.Create(ctx, cert); err != nil {
		return fmt.Errorf("create bootstrap Certificate CR: %w", err)
	}

	logger.Info("Created bootstrap Certificate CR", "certificate", cert.GetName(), "spoke", spokeName)
	return nil
}

// +kubebuilder:rbac:groups=cert-manager.io,resources=certificates,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=identity.zeroops.io,resources=spokemachineidentities,verbs=get;list;watch;create;update;patch

// ensureSpokeMachineIdentity creates a SpokeMachineIdentity CR declaring desired Machine Identity state.
// The spoke-identity-operator watches this CR and reconciles it against Infisical to create, rotate,
// and manage lifecycle of the Spoke's Machine Identity.
// PKI operations remain cert-manager's domain (ADR-035).
func (r *SpokePoolReconciler) ensureSpokeMachineIdentity(ctx context.Context, spokeName string) error {
	logger := log.FromContext(ctx)

	smi := &unstructured.Unstructured{}
	smi.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "identity.zeroops.io",
		Version: "v1alpha1",
		Kind:    "SpokeMachineIdentity",
	})
	smi.SetName(spokeName)
	smi.SetNamespace("platform-ops")

	// Check if already exists (idempotent)
	if err := r.Get(ctx, client.ObjectKeyFromObject(smi), smi); err == nil {
		logger.Info("SpokeMachineIdentity already exists", "smi", smi.GetName())
		return nil
	}

	smi.Object = map[string]interface{}{
		"metadata": map[string]interface{}{
			"name":      smi.GetName(),
			"namespace": smi.GetNamespace(),
			"labels": map[string]interface{}{
				"platform.nutgraf.in/spoke": spokeName,
			},
		},
		"spec": map[string]interface{}{
			"spokeRef": map[string]interface{}{
				"name": spokeName,
			},
			"infisical": map[string]interface{}{
				"authMethod":      "universal-auth",
				"clientSecretTTL": "90d",
			},
			"rotationPolicy": map[string]interface{}{
				"enabled":       true,
				"interval":      "60d",
				"overlapPeriod": "24h",
			},
			"revocationPolicy": map[string]interface{}{
				"revokeOnDelete": true,
				"gracePeriod":    "72h",
			},
		},
	}

	if err := r.Create(ctx, smi); err != nil {
		return fmt.Errorf("create SpokeMachineIdentity CR: %w", err)
	}

	logger.Info("Created SpokeMachineIdentity CR", "smi", smi.GetName(), "spoke", spokeName)
	return nil
}

// SetupWithManager registers the controller to watch SpokePool XRs.
func (r *SpokePoolReconciler) SetupWithManager(mgr ctrl.Manager) error {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "nutgraf.in",
		Version: "v1alpha1",
		Kind:    "SpokePool",
	})

	return ctrl.NewControllerManagedBy(mgr).
		For(u).
		Complete(r)
}

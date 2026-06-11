package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/soloz-io/zero-ops/operators/hub-operator/internal/secrets"
)

// SpokePoolReconciler reconciles SpokePool XRs to generate per-spoke secrets
type SpokePoolReconciler struct {
	client.Client
	InfisicalClient *secrets.InfisicalClient
}

//+kubebuilder:rbac:groups=nutgraf.in,resources=spokepools,verbs=get;list;watch;update
//+kubebuilder:rbac:groups=nutgraf.in,resources=spokepools/status,verbs=get;update;patch
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete

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
	if err := r.ensureBootstrapCertificate(ctx, spokePool); err != nil {
		logger.Error(err, "Failed to ensure bootstrap certificate", "spoke", spokeName)
		// Non-fatal: Spoke can bootstrap without a cert-manager Certificate CR,
		// but ArgoCD Agent mTLS won't work until it's created.
	}

	// create bootstrap certificate CRS wrapper for ClusterResourceSet delivery
	if err := r.ensureBootstrapCertCRSWrapper(ctx, spokePool); err != nil {
		logger.Error(err, "Failed to ensure bootstrap CRS wrapper", "spoke", spokeName)
		// Non-fatal: CRS wrapper creation can be retried on next reconcile.
	}

	// create SpokeMachineIdentity CR for identity lifecycle (spoke-identity-operator reconciles)
	if err := r.ensureSpokeMachineIdentity(ctx, spokePool); err != nil {
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
			"lastTransitionTime": c.LastTransitionTime.Format(time.RFC3339),
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
// cert-manager + infisical-fleet-issuer fulfills this Certificate declaratively per ADR-035.
// The resulting TLS Secret is read by ensureBootstrapCertCRSWrapper for ClusterResourceSet delivery.
// Domain: platform-capi (ADR-015), Certificate declares intent only — PKI is cert-manager's domain (ADR-035).
func (r *SpokePoolReconciler) ensureBootstrapCertificate(ctx context.Context, spokePool *unstructured.Unstructured) error {
	logger := log.FromContext(ctx)
	spokeName := spokePool.GetName()

	cert := &unstructured.Unstructured{}
	cert.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cert-manager.io",
		Version: "v1",
		Kind:    "Certificate",
	})
	cert.SetName(fmt.Sprintf("argocd-agent-%s", spokeName))
	cert.SetNamespace("platform-capi")

	// Check if already exists (idempotent)
	err := r.Get(ctx, client.ObjectKeyFromObject(cert), cert)
	if err == nil {
		logger.Info("Bootstrap Certificate already exists", "certificate", cert.GetName())
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}

	// Safe map assignment to preserve GVK
	if cert.Object["metadata"] == nil {
		cert.Object["metadata"] = map[string]interface{}{}
	}
	cert.Object["metadata"].(map[string]interface{})["name"] = cert.GetName()
	cert.Object["metadata"].(map[string]interface{})["namespace"] = cert.GetNamespace()
	cert.Object["metadata"].(map[string]interface{})["labels"] = map[string]interface{}{
		"platform.nutgraf.in/bootstrap": "true",
	}
	cert.Object["metadata"].(map[string]interface{})["ownerReferences"] = []interface{}{
		map[string]interface{}{
			"apiVersion":         "nutgraf.in/v1alpha1",
			"kind":               "SpokePool",
			"name":               spokePool.GetName(),
			"uid":                string(spokePool.GetUID()),
			"controller":         true,
			"blockOwnerDeletion": true,
		},
	}

	cert.Object["spec"] = map[string]interface{}{
		"secretName":  fmt.Sprintf("argocd-agent-%s-tls", spokeName),
		"duration":    "72h",
		"renewBefore": "36h",
		"commonName":  fmt.Sprintf("argocd-agent.%s", spokeName),
		"isCA":        false,
		"usages": []interface{}{
			"server auth",
			"client auth",
		},
		"issuerRef": map[string]interface{}{
			"name":  "infisical-fleet-issuer",
			"kind":  "ClusterIssuer",
			"group": "infisical-issuer.infisical.com",
		},
	}

	logger.Info(
		"Certificate before create",
		"gvk", cert.GroupVersionKind().String(),
		"object", cert.Object,
	)

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
// PKI operations remain cert-manager's domain (ADR-035). Domain: platform-capi (ADR-015).
func (r *SpokePoolReconciler) ensureSpokeMachineIdentity(ctx context.Context, spokePool *unstructured.Unstructured) error {
	logger := log.FromContext(ctx)
	spokeName := spokePool.GetName()

	smi := &unstructured.Unstructured{}
	smi.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "identity.zeroops.io",
		Version: "v1alpha1",
		Kind:    "SpokeMachineIdentity",
	})
	smi.SetName(spokeName)
	smi.SetNamespace("platform-capi")

	// Check if already exists (idempotent)
	err := r.Get(ctx, client.ObjectKeyFromObject(smi), smi)
	if err == nil {
		logger.Info("SpokeMachineIdentity already exists", "smi", smi.GetName())
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}

	// Safe map assignment to preserve GVK
	if smi.Object["metadata"] == nil {
		smi.Object["metadata"] = map[string]interface{}{}
	}
	smi.Object["metadata"].(map[string]interface{})["name"] = smi.GetName()
	smi.Object["metadata"].(map[string]interface{})["namespace"] = smi.GetNamespace()
	smi.Object["metadata"].(map[string]interface{})["labels"] = map[string]interface{}{
		"platform.nutgraf.in/spoke": spokeName,
	}
	smi.Object["metadata"].(map[string]interface{})["ownerReferences"] = []interface{}{
		map[string]interface{}{
			"apiVersion":         "nutgraf.in/v1alpha1",
			"kind":               "SpokePool",
			"name":               spokePool.GetName(),
			"uid":                string(spokePool.GetUID()),
			"controller":         true,
			"blockOwnerDeletion": true,
		},
	}

	smi.Object["spec"] = map[string]interface{}{
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
	}

	logger.Info(
		"SpokeMachineIdentity before create",
		"gvk", smi.GroupVersionKind().String(),
		"object", smi.Object,
	)

	if err := r.Create(ctx, smi); err != nil {
		return fmt.Errorf("create SpokeMachineIdentity CR: %w", err)
	}

	logger.Info("Created SpokeMachineIdentity CR", "smi", smi.GetName(), "spoke", spokeName)
	return nil
}

// ensureBootstrapCertCRSWrapper reads the cert-manager-issued TLS Secret for the spoke's bootstrap
// certificate and packages it into a ClusterResourceSet wrapper Secret (type addons.cluster.x-k8s.io/resource-set).
// The wrapper is immutable once created — it is an ApplyOnce bootstrap artifact and must never
// be regenerated after initial creation, even if the underlying Certificate renews (ADR-035).
func (r *SpokePoolReconciler) ensureBootstrapCertCRSWrapper(ctx context.Context, spokePool *unstructured.Unstructured) error {
	logger := log.FromContext(ctx)
	spokeName := spokePool.GetName()

	wrapperName := fmt.Sprintf("%s-bootstrap-cert", spokeName)
	tlsSecretName := fmt.Sprintf("argocd-agent-%s-tls", spokeName)

	// Immutable guard: if wrapper already exists with correct type, do not regenerate
	existing := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Name: wrapperName, Namespace: "platform-capi"}, existing); err == nil {
		if existing.Type == "addons.cluster.x-k8s.io/resource-set" {
			logger.Info("Bootstrap CRS wrapper already exists, skipping", "wrapper", wrapperName)
			return nil
		}
	}

	// Read the cert-manager-issued TLS Secret
	tlsSecret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Name: tlsSecretName, Namespace: "platform-capi"}, tlsSecret); err != nil {
		return fmt.Errorf("read TLS Secret %s: %w", tlsSecretName, err)
	}

	// Extract certificate material
	tlsCrt := string(tlsSecret.Data["tls.crt"])
	tlsKey := string(tlsSecret.Data["tls.key"])
	caCrt := string(tlsSecret.Data["ca.crt"])

	// Build embedded YAML manifests for the CRS wrapper (matches cert-operator pattern)
	// The TLS private key is included in the wrapper as required by the CRS delivery model,
	// but is discarded by cert-manager on the spoke after the bootstrap cert is applied.
	certYAML := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: argocd-agent-client-cert
  namespace: argocd
  labels:
    platform.nutgraf.in/bootstrap: "true"
type: kubernetes.io/tls
stringData:
  tls.crt: |
%s
  tls.key: |
%s
  ca.crt: |
%s
`, indentYAML(tlsCrt), indentYAML(tlsKey), indentYAML(caCrt))

	wrapper := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      wrapperName,
			Namespace: "platform-capi",
			Labels:    map[string]string{"addons.cluster.x-k8s.io/resource-set": "true"},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         "nutgraf.in/v1alpha1",
					Kind:               "SpokePool",
					Name:               spokePool.GetName(),
					UID:                spokePool.GetUID(),
					Controller:         ptr.To(true),
					BlockOwnerDeletion: ptr.To(true),
				},
			},
		},
		Type:       "addons.cluster.x-k8s.io/resource-set",
		StringData: map[string]string{"bootstrap-cert.yaml": certYAML},
	}

	if err := r.Create(ctx, wrapper); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return nil
		}
		return fmt.Errorf("create bootstrap CRS wrapper: %w", err)
	}

	logger.Info("Created bootstrap CRS wrapper Secret", "wrapper", wrapperName, "spoke", spokeName)
	return nil
}

// indentYAML indents each line of s by 4 spaces for embedding in YAML stringData fields.
func indentYAML(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = "    " + line
		}
	}
	return strings.Join(lines, "\n")
}

// mapCertificateToSpokePool maps a cert-manager Certificate to its parent SpokePool.
// Uses the naming convention: Certificate name = "argocd-agent-{spokeName}".
func (r *SpokePoolReconciler) mapCertificateToSpokePool(ctx context.Context, obj client.Object) []reconcile.Request {
	cert, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return nil
	}
	name := cert.GetName()
	spokeName := strings.TrimPrefix(name, "argocd-agent-")
	if spokeName == name {
		return nil
	}
	return []reconcile.Request{
		{NamespacedName: types.NamespacedName{Name: spokeName}},
	}
}

// mapSpokeMachineIdentityToSpokePool maps a SpokeMachineIdentity CR to its parent SpokePool.
// The SpokeMachineIdentity name IS the spoke name.
func (r *SpokePoolReconciler) mapSpokeMachineIdentityToSpokePool(ctx context.Context, obj client.Object) []reconcile.Request {
	smi, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return nil
	}
	return []reconcile.Request{
		{NamespacedName: types.NamespacedName{Name: smi.GetName()}},
	}
}

// SetupWithManager registers the controller to watch SpokePool XRs.
// Uses explicit Watches() instead of Owns() for Certificate and SpokeMachineIdentity
// to avoid untested cluster-scoped → namespaced owner reference watch semantics.
func (r *SpokePoolReconciler) SetupWithManager(mgr ctrl.Manager) error {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "nutgraf.in",
		Version: "v1alpha1",
		Kind:    "SpokePool",
	})

	cert := &unstructured.Unstructured{}
	cert.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cert-manager.io",
		Version: "v1",
		Kind:    "Certificate",
	})

	smi := &unstructured.Unstructured{}
	smi.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "identity.zeroops.io",
		Version: "v1alpha1",
		Kind:    "SpokeMachineIdentity",
	})

	return ctrl.NewControllerManagedBy(mgr).
		For(u).
		Watches(
			cert,
			handler.EnqueueRequestsFromMapFunc(r.mapCertificateToSpokePool),
		).
		Watches(
			smi,
			handler.EnqueueRequestsFromMapFunc(r.mapSpokeMachineIdentityToSpokePool),
		).
		Complete(r)
}

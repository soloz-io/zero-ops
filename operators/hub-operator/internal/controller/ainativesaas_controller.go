package controller

import (
	"context"
	"fmt"
	"sort"

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
//  1. db-credentials — PostgreSQL user credentials for the tenant database.
//     ESO remoteRef.key splits on the last '/' so secret NAME is "db-credentials"
//     and FOLDER path is /spoke-pool/<cellId>/tenants/<tenantId>.
//     ADR-003 Pattern A2a: Kube-SBT → Infisical ONLY → ESO → Spoke K8s Secret.
//  2. infisical-credentials — Machine Identity credentials for the tenant SDK
//     workload. ADR-003, ADR-019: scoped identity for runtime plugin resolution.
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

	// TODO(ADR-039): Move tenant credential generation to Kube-SBT
	// ADR-031: Delegate to InfisicalClient for tenant secret provisioning.
	//
	// ADR-053: isFirstTime is derived from this resource's own observed status, not
	// passed as a literal. It used to be a hardcoded true, which made the ADR-003
	// protection unreachable: a credential lost after provisioning was silently
	// regenerated rather than reported, invalidating whatever already held it.
	// A tenant whose credentials have been seeded is not provisioning for the first
	// time, and the seeded condition is that durable record.
	isFirstTime := !r.isConditionTrue(ainativesaas, conditionTypeDBSeeded)

	// The declared clients, each carrying whether its credential has been generated
	// before. That marker is per client rather than per tenant: a client declared
	// today on a tenant provisioned months ago is new, and treating its absent
	// credential as a fault would make declaring a second client impossible.
	provisioned := provisionedOAuthClients(ainativesaas)
	oauthClients := declaredOAuthClients(ainativesaas, provisioned)

	// A fleet that declares no cache gets no credential generated for one.
	cacheEnabled, _, _ := unstructured.NestedBool(ainativesaas.Object, "spec", "cache", "enabled")

	result, err := r.InfisicalClient.EnsureTenantFolderAndCredentials(ctx, cellId, tenantId, isFirstTime, oauthClients, cacheEnabled)
	if err != nil {
		logger.Error(err, "Failed to ensure tenant credentials in Infisical", "tenant", tenantId, "cell", cellId)
		if result != nil && result.Result == secrets.EnsureMissing {
			_ = r.setCondition(ctx, ainativesaas, tenantId, conditionMissing)
		}
		return ctrl.Result{}, err
	}

	// Record the confidential clients now holding a credential, before reporting
	// the outcome. This is what makes a later absence distinguishable from a client
	// that was never provisioned, so it must be durable before anything depends on
	// it. A failure here is logged and retried rather than fatal: the credential
	// exists either way, and the next reconcile repeats the record.
	if err := r.recordProvisionedOAuthClients(ctx, ainativesaas, oauthClients); err != nil {
		logger.Error(err, "Failed to record provisioned OAuth clients; will retry", "tenant", tenantId)
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

// conditionTypeDBSeeded is the condition recording that this tenant's credentials
// have been provisioned. It doubles as the durable first-provisioning marker.
const conditionTypeDBSeeded = "TenantDBCredentialsSeeded"

// declaredOAuthClients reads the OAuth clients declared on the tenant resource
// (ADR-053), pairing each with whether it has been provisioned before.
//
// A client with no explicit type is treated as confidential. The safe default is
// the one that generates a credential nothing consumes, rather than the one that
// silently skips a credential something needs.
func declaredOAuthClients(obj *unstructured.Unstructured, provisioned map[string]bool) []secrets.OAuthClient {
	raw, found, err := unstructured.NestedSlice(obj.Object, "spec", "oauth", "clients")
	if err != nil || !found {
		return nil
	}

	var clients []secrets.OAuthClient
	for _, entry := range raw {
		fields, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := fields["name"].(string)
		if name == "" {
			continue
		}
		clientType, _ := fields["type"].(string)
		clients = append(clients, secrets.OAuthClient{
			Name:                  name,
			Confidential:          clientType != "public",
			PreviouslyProvisioned: provisioned[name],
		})
	}
	return clients
}

// provisionedOAuthClients reads the durable record of which clients have already
// had a credential generated.
func provisionedOAuthClients(obj *unstructured.Unstructured) map[string]bool {
	provisioned := map[string]bool{}
	names, found, err := unstructured.NestedStringSlice(obj.Object, "status", "provisionedOAuthClients")
	if err != nil || !found {
		return provisioned
	}
	for _, name := range names {
		provisioned[name] = true
	}
	return provisioned
}

// recordProvisionedOAuthClients adds each confidential client to the durable
// record. Entries are never removed here: a client dropped from the declaration
// still has a credential in Infisical and a registration in Hydra, and forgetting
// that it was provisioned would let a later re-declaration regenerate over them.
// Decommissioning is a separate transition.
func (r *AINativeSaaSReconciler) recordProvisionedOAuthClients(ctx context.Context, obj *unstructured.Unstructured, clients []secrets.OAuthClient) error {
	confidential := map[string]bool{}
	for _, client := range clients {
		if client.Confidential {
			confidential[client.Name] = true
		}
	}
	if len(confidential) == 0 {
		return nil
	}

	latest := &unstructured.Unstructured{}
	latest.SetGroupVersionKind(obj.GroupVersionKind())
	if err := r.Get(ctx, client.ObjectKeyFromObject(obj), latest); err != nil {
		return err
	}

	existing, _, err := unstructured.NestedStringSlice(latest.Object, "status", "provisionedOAuthClients")
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, name := range existing {
		seen[name] = true
	}

	changed := false
	for name := range confidential {
		if !seen[name] {
			existing = append(existing, name)
			seen[name] = true
			changed = true
		}
	}
	if !changed {
		return nil
	}
	sort.Strings(existing)

	if err := unstructured.SetNestedStringSlice(latest.Object, existing, "status", "provisionedOAuthClients"); err != nil {
		return err
	}
	return r.Status().Update(ctx, latest)
}

// conditionState enumerates the three terminal states for TenantDBCredentialsSeeded.
type conditionState int

const (
	conditionSeeded        conditionState = iota // first-time: generated and uploaded
	conditionAlreadyExists                       // idempotent: already present in Infisical
	conditionMissing                             // error: missing post-provisioning
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
			Type:    conditionTypeDBSeeded,
			Status:  metav1.ConditionTrue,
			Reason:  "Seeded",
			Message: fmt.Sprintf("DB credentials generated and uploaded to Infisical for tenant %s", tenantId),
		}
	case conditionAlreadyExists:
		cond = metav1.Condition{
			Type:    conditionTypeDBSeeded,
			Status:  metav1.ConditionTrue,
			Reason:  "AlreadyExists",
			Message: fmt.Sprintf("DB credentials already present in Infisical for tenant %s", tenantId),
		}
	case conditionMissing:
		cond = metav1.Condition{
			Type:    conditionTypeDBSeeded,
			Status:  metav1.ConditionFalse,
			Reason:  "CredentialsMissing",
			Message: fmt.Sprintf("CRITICAL: DB credentials missing from Infisical for already-provisioned tenant %s. Manual recovery required.", tenantId),
		}
	}

	meta.SetStatusCondition(&metaConditions, cond)

	// Serialise back to unstructured map format for Crossplane.
	var updated []interface{}
	for _, c := range metaConditions {
		updated = append(updated, map[string]interface{}{
			"type":               c.Type,
			"status":             string(c.Status),
			"reason":             c.Reason,
			"message":            c.Message,
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

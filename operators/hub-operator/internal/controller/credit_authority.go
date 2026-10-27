package controller

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/soloz-io/zero-ops/operators/hub-operator/internal/secrets"
)

// The Credit Authority is a selectable platform capability of the box
// (ADR-109), run on the hub like the box's other platform services and turned
// on by capabilities.credits.enabled.
//
// Its database, role and credential are platform prerequisites present on
// every box (platform-db, HubEnvironment, the application secret mappings).
// What this file owns is what exists only while the capability runs:
//
//	identity  its project in the platform's own organisation, provisioned when
//	          the capability is enabled -- the hub component's ConfigMap below
//	          appearing is that event
//	caller    the ONE client its gateway admits: the service client of the one
//	          application that declares `capabilities.credits.enabled`, and that
//	          client's organisation. Absent -- capability off, no declaration, or
//	          the owner's identity not provisioned yet -- means nobody is admitted.
//
// An application consumes it by that declaration, which is the single source of
// its caller and its audience; a second application declaring it is refused.
const (
	// The capability's name as a backend audience of its owner: the key suffix
	// of OIDC_BACKEND_PROJECT_ID_CREDITS in the owner's folder.
	creditAuthorityAppID = "credits"

	// The hub component's own ConfigMap. Rendered only while the capability is
	// enabled, so its presence is the enablement this controller acts on.
	creditAuthorityNamespace = "platform-credits"
	creditAuthorityMarker    = "credit-authority"

	// The platform project the capability's gateway requires in a token's
	// audience.
	creditAuthorityProjectName = "credits"

	// Hub keys, at the root of the secrets project the hub's ClusterSecretStore
	// reads, beside the platform services' own.
	hubSecretPath            = "/"
	creditsProjectIDKey      = "credits-oidc-project-id"
	creditsCallerClientIDKey = "credits-caller-client-id"
	creditsCallerOrgIDKey    = "credits-caller-org-id"

	// Recorded on every application that declares the capability: True on the
	// owner, False on a declaration refused because another application owns it.
	conditionTypeCreditAuthorityOwner = "CreditAuthorityOwner"

	// How soon to look again while the caller waits on something another
	// reconcile writes to Infisical (the owner's service client), which raises no
	// event here.
	creditAuthorityPendingRequeue = time.Minute
)

var ainativesaasGVK = schema.GroupVersionKind{Group: "nutgraf.in", Version: "v1alpha1", Kind: "AINativeSaaS"}

// declaresCredits reports whether this application declares the Credit
// Authority capability.
func declaresCredits(obj *unstructured.Unstructured) bool {
	on, _, _ := unstructured.NestedBool(obj.Object, "spec", "capabilities", "credits", "enabled")
	return on
}

// creditsOwnerAmong is the ONE application of the box that owns the Credit
// Authority declaration, and the declarations refused because of it.
//
// The earliest declaration wins and keeps winning: a later application
// declaring the capability cannot take it over, it is refused with a reason.
// Ordered by creation time, then name, so every reconcile reaches the same
// answer.
func creditsOwnerAmong(items []unstructured.Unstructured) (owner *unstructured.Unstructured, refused []string) {
	var declared []unstructured.Unstructured
	for _, it := range items {
		if declaresCredits(&it) {
			declared = append(declared, it)
		}
	}
	if len(declared) == 0 {
		return nil, nil
	}
	sort.Slice(declared, func(i, j int) bool {
		ti, tj := declared[i].GetCreationTimestamp(), declared[j].GetCreationTimestamp()
		if !ti.Equal(&tj) {
			return ti.Before(&tj)
		}
		return declared[i].GetName() < declared[j].GetName()
	})
	for _, d := range declared[1:] {
		refused = append(refused, d.GetName())
	}
	return &declared[0], refused
}

// applications lists every AINativeSaaS of the box.
func (r *AINativeSaaSReconciler) applications(ctx context.Context) ([]unstructured.Unstructured, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(ainativesaasGVK.GroupVersion().WithKind("AINativeSaaSList"))
	if err := r.List(ctx, list); err != nil {
		return nil, fmt.Errorf("list applications: %w", err)
	}
	return list.Items, nil
}

// publishHubSecret writes one of the capability's hub keys.
func (r *AINativeSaaSReconciler) publishHubSecret(ctx context.Context, key, value string) error {
	if current, err := r.InfisicalClient.GetSecret(ctx, hubSecretPath, key); err == nil && current == value {
		return nil
	}
	exists, err := r.InfisicalClient.SecretExists(ctx, hubSecretPath, key)
	if err != nil {
		return fmt.Errorf("check %s: %w", key, err)
	}
	if exists {
		return r.InfisicalClient.UpdateSecret(ctx, hubSecretPath, key, value)
	}
	return r.InfisicalClient.CreateSecret(ctx, hubSecretPath, key, value)
}

// ownsCreditAuthority reports whether this application owns the box's Credit
// Authority declaration, and records the answer on its status.
func (r *AINativeSaaSReconciler) ownsCreditAuthority(ctx context.Context, logger logr.Logger, obj *unstructured.Unstructured) bool {
	if !declaresCredits(obj) {
		return false
	}
	apps, err := r.applications(ctx)
	if err != nil {
		logger.Error(err, "Could not decide the Credit Authority's owner; the declaration waits")
		return false
	}
	owner, _ := creditsOwnerAmong(apps)
	if owner == nil || owner.GetName() != obj.GetName() {
		ownerName := ""
		if owner != nil {
			ownerName = owner.GetName()
		}
		logger.Error(nil, "Refusing this application's Credit Authority declaration: another application owns it",
			"app", obj.GetName(), "owner", ownerName)
		_ = r.mergeCondition(ctx, obj, metav1.Condition{
			Type:    conditionTypeCreditAuthorityOwner,
			Status:  metav1.ConditionFalse,
			Reason:  "Conflict",
			Message: fmt.Sprintf("the Credit Authority is owned by %s; one application may own it", ownerName),
		})
		return false
	}
	_ = r.mergeCondition(ctx, obj, metav1.Condition{
		Type:    conditionTypeCreditAuthorityOwner,
		Status:  metav1.ConditionTrue,
		Reason:  "Declared",
		Message: "this application is the Credit Authority's one caller",
	})
	return true
}

// creditAuthorityDependency is the Credit Authority as its owner's backend
// audience, or nil while the capability has no project -- it is not enabled, or
// not provisioned yet.
func (r *AINativeSaaSReconciler) creditAuthorityDependency(ctx context.Context) *resolvedDependency {
	pid, err := r.InfisicalClient.GetSecret(ctx, hubSecretPath, creditsProjectIDKey)
	if err != nil || pid == "" {
		return nil
	}
	return &resolvedDependency{AppID: creditAuthorityAppID, ProjectID: pid}
}

// creditAuthorityDeclarers maps the capability being enabled or disabled to the
// applications that declare it, so the owner's audience follows.
func (r *AINativeSaaSReconciler) creditAuthorityDeclarers(ctx context.Context, _ client.Object) []reconcile.Request {
	apps, err := r.applications(ctx)
	if err != nil {
		log.FromContext(ctx).Error(err, "Could not route the Credit Authority's change to its declarers")
		return nil
	}
	var out []reconcile.Request
	for i := range apps {
		if declaresCredits(&apps[i]) {
			out = append(out, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&apps[i])})
		}
	}
	return out
}

// isCreditAuthorityMarker selects the hub component's ConfigMap and nothing else.
func isCreditAuthorityMarker(obj client.Object) bool {
	return obj.GetNamespace() == creditAuthorityNamespace && obj.GetName() == creditAuthorityMarker
}

var creditAuthorityRequest = reconcile.Request{NamespacedName: types.NamespacedName{
	Namespace: creditAuthorityNamespace, Name: creditAuthorityMarker,
}}

// CreditAuthorityReconciler provisions the Credit Authority's identity while it
// is enabled, and publishes or revokes its one caller.
type CreditAuthorityReconciler struct {
	*AINativeSaaSReconciler
}

//+kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch

// Reconcile acts on the capability's enablement and on every application change.
func (r *CreditAuthorityReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	marker := &corev1.ConfigMap{}
	err := r.Get(ctx, req.NamespacedName, marker)
	switch {
	case apierrors.IsNotFound(err):
		// Disabled: nothing runs, and nothing is admitted if it runs again before
		// a reconcile publishes a caller.
		logger.Info("The Credit Authority is not enabled; revoking its caller")
		return ctrl.Result{}, r.revokeCaller(ctx)
	case err != nil:
		return ctrl.Result{}, err
	}

	if r.IdentityClient == nil {
		logger.Error(nil, "The Credit Authority is enabled but this environment provisions no identity; it admits nobody")
		return ctrl.Result{}, r.revokeCaller(ctx)
	}
	_, projectID, err := r.IdentityClient.EnsurePlatformProject(ctx, creditAuthorityProjectName)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("provision the Credit Authority's project: %w", err)
	}
	if err := r.publishHubSecret(ctx, creditsProjectIDKey, projectID); err != nil {
		return ctrl.Result{}, fmt.Errorf("publish the Credit Authority's project id: %w", err)
	}

	return r.publishCaller(ctx, logger)
}

// publishCaller publishes the one client the capability admits -- the owning
// application's service client -- and the organisation that client belongs to.
func (r *CreditAuthorityReconciler) publishCaller(ctx context.Context, logger logr.Logger) (ctrl.Result, error) {
	apps, err := r.applications(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}
	owner, _ := creditsOwnerAmong(apps)
	if owner == nil {
		logger.Info("No application declares the Credit Authority; it admits nobody")
		return ctrl.Result{}, r.revokeCaller(ctx)
	}

	ownerApp, _, _ := unstructured.NestedString(owner.Object, "spec", "appId")
	ownerCell, _, _ := unstructured.NestedString(owner.Object, "spec", "cellId")
	ownerOrg, _, _ := unstructured.NestedString(owner.Object, "status", "identity", "zitadelOrgId")
	callerID, err := r.InfisicalClient.GetSecret(ctx, fmt.Sprintf(secrets.InfisicalTenantPathFormat, ownerCell, ownerApp), serviceClientIDKey)
	if err != nil || callerID == "" || ownerOrg == "" {
		logger.Info("The Credit Authority waits on its owner's service identity; it admits nobody until then",
			"owner", owner.GetName(), "expected", serviceUserName(ownerApp))
		return ctrl.Result{RequeueAfter: creditAuthorityPendingRequeue}, r.revokeCaller(ctx)
	}
	if err := r.publishHubSecret(ctx, creditsCallerClientIDKey, callerID); err != nil {
		return ctrl.Result{}, fmt.Errorf("publish the Credit Authority's caller: %w", err)
	}
	if err := r.publishHubSecret(ctx, creditsCallerOrgIDKey, ownerOrg); err != nil {
		return ctrl.Result{}, fmt.Errorf("publish the Credit Authority's caller organisation: %w", err)
	}
	return ctrl.Result{}, nil
}

// revokeCaller removes the admitted caller, so a gateway admits nobody.
func (r *CreditAuthorityReconciler) revokeCaller(ctx context.Context) error {
	for _, key := range []string{creditsCallerClientIDKey, creditsCallerOrgIDKey} {
		if err := r.InfisicalClient.DeleteSecret(ctx, hubSecretPath, key); err != nil {
			return fmt.Errorf("revoke the Credit Authority's caller (%s): %w", key, err)
		}
	}
	return nil
}

// SetupWithManager reconciles on the capability's ConfigMap and on every
// application change, both mapped to the one capability.
func (r *CreditAuthorityReconciler) SetupWithManager(mgr ctrl.Manager) error {
	app := &unstructured.Unstructured{}
	app.SetGroupVersionKind(ainativesaasGVK)
	return ctrl.NewControllerManagedBy(mgr).
		Named("creditauthority").
		For(&corev1.ConfigMap{}, builder.WithPredicates(predicate.NewPredicateFuncs(isCreditAuthorityMarker))).
		Watches(app, handler.EnqueueRequestsFromMapFunc(func(context.Context, client.Object) []reconcile.Request {
			return []reconcile.Request{creditAuthorityRequest}
		})).
		Complete(r)
}

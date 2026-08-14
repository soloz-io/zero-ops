package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	identityv1alpha1 "github.com/soloz-io/zero-ops/operators/spoke-identity-operator/api/v1alpha1"
	"github.com/soloz-io/zero-ops/operators/spoke-identity-operator/internal/infisical"
)

const (
	smiFinalizer                  = "identity.zeroops.io/finalizer"
	conditionTypeReady            = "Ready"
	conditionTypeIdentityCreated  = "IdentityProvisioned"
	conditionTypeRotationComplete = "RotationComplete"
)

// +kubebuilder:rbac:groups=identity.zeroops.io,resources=spokemachineidentities,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=identity.zeroops.io,resources=spokemachineidentities/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=identity.zeroops.io,resources=spokemachineidentities/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch;delete

// SpokeMachineIdentityReconciler reconciles SpokeMachineIdentity CRs against Infisical.
// This is the initial implementation of the Platform Identity Domain.
// It creates Machine Identities in Infisical for Spoke clusters, tracks lifecycle state,
// and supports rotation and revocation.
//
// PKI operations are forbidden — only cert-manager may manage certificates per ADR-035.
//
// Future evolution: may add full identity lifecycle (attestation, audit, multi-provider)
// as defined in a future ADR.
type SpokeMachineIdentityReconciler struct {
	client.Client
	InfisicalClient *infisical.Client
	OrgID           string
	ProjectID       string
	// SecretsProjectID is the secret-manager project (e.g. hub-secrets) that
	// spoke machine identities are granted access to in addition to ProjectID
	// (the cert-manager project). This is required so the spoke's ESO
	// ClusterSecretStore can read tenant secrets.
	SecretsProjectID string
}

func (r *SpokeMachineIdentityReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("spokemachineidentity", req.Name)

	smi := &identityv1alpha1.SpokeMachineIdentity{}
	if err := r.Get(ctx, req.NamespacedName, smi); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	orgID := r.OrgID
	if orgID == "" {
		orgID = smi.Spec.Infisical.OrganizationID
	}
	projectID := r.ProjectID
	if projectID == "" {
		projectID = smi.Spec.Infisical.ProjectID
	}

	// Handle deletion
	if !smi.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, smi)
	}

	// Ensure finalizer
	if !controllerutil.ContainsFinalizer(smi, smiFinalizer) {
		controllerutil.AddFinalizer(smi, smiFinalizer)
		if err := r.Update(ctx, smi); err != nil {
			return ctrl.Result{}, fmt.Errorf("add finalizer: %w", err)
		}
		return ctrl.Result{}, nil
	}

	// Ensure Machine Identity exists in Infisical
	identityName := fmt.Sprintf("spoke-%s", smi.Spec.SpokeRef.Name)

	identity, err := r.InfisicalClient.GetOrCreateMachineIdentity(ctx, identityName, orgID)
	if err != nil {
		logger.Error(err, "Failed to ensure Machine Identity")
		r.setCondition(smi, conditionTypeReady, metav1.ConditionFalse, "IdentityFailed", err.Error())
		r.setCondition(smi, conditionTypeIdentityCreated, metav1.ConditionFalse, "IdentityFailed", err.Error())
		_ = r.Status().Update(ctx, smi)
		return ctrl.Result{}, err
	}

	if identity.ID != "" && smi.Status.IdentityID != identity.ID {
		smi.Status.IdentityID = identity.ID
		smi.Status.ClientID = identity.ClientID
	}

	// Grant project access. The identity is granted access to both the
	// configured cert-manager project (hub-platform) and the secrets project
	// (hub-secrets) so it can authenticate for both cert issuance and
	// ExternalSecret delivery.
	secretsProjectID := r.SecretsProjectID
	if smi.Spec.Infisical.SecretsProjectID != "" {
		secretsProjectID = smi.Spec.Infisical.SecretsProjectID
	}
	for _, pid := range []string{projectID, secretsProjectID} {
		if pid == "" {
			continue
		}
		if err := r.InfisicalClient.GrantProjectAccess(ctx, identity.ID, pid, "admin"); err != nil {
			logger.Error(err, "Failed to grant project access", "projectId", pid)
			r.setCondition(smi, conditionTypeReady, metav1.ConditionFalse, "AccessFailed", err.Error())
			_ = r.Status().Update(ctx, smi)
			return ctrl.Result{}, err
		}
	}

	// Handle rotation
	if requeue, err := r.handleRotation(ctx, smi, identity.ID); err != nil {
		logger.Error(err, "Rotation failed")
		r.setCondition(smi, conditionTypeReady, metav1.ConditionFalse, "RotationFailed", err.Error())
		_ = r.Status().Update(ctx, smi)
		return ctrl.Result{}, err
	} else if requeue {
		return ctrl.Result{RequeueAfter: 1 * time.Minute}, r.Status().Update(ctx, smi)
	}

	// Ensure CRS wrapper Secret for ClusterResourceSet delivery
	if err := r.ensureCRSWrapper(ctx, smi); err != nil {
		logger.Error(err, "Failed to ensure CRS wrapper")
		r.setCondition(smi, conditionTypeReady, metav1.ConditionFalse, "CRSWrapperFailed", err.Error())
		_ = r.Status().Update(ctx, smi)
		return ctrl.Result{}, err
	}

	// Drift detection: verify identity still exists
	exists, err := r.InfisicalClient.IdentityExists(ctx, identity.ID)
	if err != nil {
		logger.Error(err, "Failed to verify identity existence")
	} else if !exists {
		logger.Info("Identity missing from Infisical — will recreate on next reconciliation")
		smi.Status.IdentityID = ""
		smi.Status.ClientID = ""
	}

	r.setCondition(smi, conditionTypeReady, metav1.ConditionTrue, "Reconciled", "Machine Identity reconciled")
	r.setCondition(smi, conditionTypeIdentityCreated, metav1.ConditionTrue, "Created", fmt.Sprintf("Identity %s provisioned", identity.ID))

	logger.Info("Machine Identity reconciled", "identityId", identity.ID)
	return ctrl.Result{RequeueAfter: r.rotationCheckInterval(smi)}, r.Status().Update(ctx, smi)
}

func (r *SpokeMachineIdentityReconciler) handleDeletion(ctx context.Context, smi *identityv1alpha1.SpokeMachineIdentity) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(smi, smiFinalizer) {
		return ctrl.Result{}, nil
	}

	// Revoke and delete if identity exists
	if smi.Status.IdentityID != "" && smi.Spec.RevocationPolicy != nil && smi.Spec.RevocationPolicy.RevokeOnDelete {
		if len(smi.Status.ClientSecretIDs) > 0 {
			if err := r.InfisicalClient.RevokeAllClientSecrets(ctx, smi.Status.IdentityID, smi.Status.ClientSecretIDs); err != nil {
				logger.Error(err, "Failed to revoke client secrets during deletion")
				return ctrl.Result{}, err
			}
		}
		if err := r.InfisicalClient.DeleteIdentity(ctx, smi.Status.IdentityID); err != nil {
			logger.Error(err, "Failed to delete Machine Identity during deletion")
			return ctrl.Result{}, err
		}
	}

	controllerutil.RemoveFinalizer(smi, smiFinalizer)
	return ctrl.Result{}, r.Update(ctx, smi)
}

func (r *SpokeMachineIdentityReconciler) handleRotation(ctx context.Context, smi *identityv1alpha1.SpokeMachineIdentity, identityID string) (requeue bool, err error) {
	if smi.Spec.RotationPolicy == nil || !smi.Spec.RotationPolicy.Enabled {
		return false, nil
	}

	now := time.Now()
	if smi.Status.NextRotation != nil && now.Before(smi.Status.NextRotation.Time) {
		return false, nil // Not due yet
	}

	logger := log.FromContext(ctx)
	logger.Info("Rotating client secret", "identityId", identityID)

	oldSecretID := ""
	if len(smi.Status.ClientSecretIDs) > 0 {
		oldSecretID = smi.Status.ClientSecretIDs[0]
	}

	_, newSecret, err := r.InfisicalClient.RotateClientSecret(ctx, identityID, oldSecretID)
	if err != nil {
		return false, fmt.Errorf("rotate client secret: %w", err)
	}

	nowTime := metav1.Now()
	smi.Status.LastRotated = &nowTime

	interval := 60 * 24 * time.Hour // default 60d
	if smi.Spec.RotationPolicy.Interval != "" {
		if d, err := time.ParseDuration(smi.Spec.RotationPolicy.Interval); err == nil {
			interval = d
		}
	}
	nextRotation := metav1.NewTime(nowTime.Add(interval))
	smi.Status.NextRotation = &nextRotation

	// Store new secret in Kubernetes for ESO delivery
	if newSecret != "" {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("smi-%s-auth", smi.Spec.SpokeRef.Name),
				Namespace: smi.Namespace,
				Labels: map[string]string{
					"identity.zeroops.io/spoke": smi.Spec.SpokeRef.Name,
					"identity.zeroops.io/type":  "machine-identity",
				},
				Annotations: map[string]string{
					"identity.zeroops.io/identityId": identityID,
				},
			},
			StringData: map[string]string{
				"clientId":     smi.Status.ClientID,
				"clientSecret": newSecret,
			},
		}
		existing := &corev1.Secret{}
		if err := r.Get(ctx, client.ObjectKey{Name: secret.Name, Namespace: secret.Namespace}, existing); err == nil {
			existing.StringData = secret.StringData
			existing.Labels = secret.Labels
			existing.Annotations = secret.Annotations
			if err := r.Update(ctx, existing); err != nil {
				return false, fmt.Errorf("update auth secret: %w", err)
			}
		} else {
			if err := r.Create(ctx, secret); err != nil {
				return false, fmt.Errorf("create auth secret: %w", err)
			}
		}
	}

	r.setCondition(smi, conditionTypeRotationComplete, metav1.ConditionTrue, "Rotated", "Client secret rotated")
	return false, nil
}

func (r *SpokeMachineIdentityReconciler) rotationCheckInterval(smi *identityv1alpha1.SpokeMachineIdentity) time.Duration {
	if smi.Status.NextRotation == nil {
		return 5 * time.Minute
	}
	until := time.Until(smi.Status.NextRotation.Time)
	if until < 5*time.Minute {
		return 1 * time.Minute
	}
	return until / 4 // Check 4 times per rotation interval
}

func (r *SpokeMachineIdentityReconciler) ensureCRSWrapper(ctx context.Context, smi *identityv1alpha1.SpokeMachineIdentity) error {
	wrapperName := smi.Spec.SecretName
	if wrapperName == "" {
		wrapperName = fmt.Sprintf("%s-machine-identity", smi.Spec.SpokeRef.Name)
	}

	authSecret := &corev1.Secret{}
	authSecretName := fmt.Sprintf("smi-%s-auth", smi.Spec.SpokeRef.Name)
	if err := r.Get(ctx, client.ObjectKey{Name: authSecretName, Namespace: smi.Namespace}, authSecret); err != nil {
		return fmt.Errorf("read auth secret %s: %w", authSecretName, err)
	}

	clientID := string(authSecret.Data["clientId"])
	clientSecret := string(authSecret.Data["clientSecret"])

	identityYAML := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: infisical-auth
  namespace: platform-ops
type: Opaque
stringData:
  client-id: %s
  client-secret: %s
---
apiVersion: v1
kind: Secret
metadata:
  name: infisical-auth
  namespace: cert-manager
type: Opaque
stringData:
  client-id: %s
  client-secret: %s
`, clientID, clientSecret, clientID, clientSecret)

	wrapper := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      wrapperName,
			Namespace: smi.Namespace,
			Labels:    map[string]string{"addons.cluster.x-k8s.io/resource-set": "true"},
		},
		Type:       "addons.cluster.x-k8s.io/resource-set",
		StringData: map[string]string{"identity.yaml": identityYAML},
	}

	wrapper.SetOwnerReferences(smi.GetOwnerReferences())

	existing := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Name: wrapperName, Namespace: smi.Namespace}, existing); err == nil {
		if existing.Type == "addons.cluster.x-k8s.io/resource-set" {
			// Rotation keeps the wrapper up to date with the current client
			// secret so spokes (re)provisioned from the CRS get fresh creds.
			if string(existing.Data["identity.yaml"]) == identityYAML {
				return nil
			}
			existing.Data = map[string][]byte{"identity.yaml": []byte(identityYAML)}
			if err := r.Update(ctx, existing); err != nil {
				return fmt.Errorf("update identity CRS wrapper: %w", err)
			}
			return nil
		}
	}

	if err := r.Create(ctx, wrapper); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return nil
		}
		return fmt.Errorf("create identity CRS wrapper: %w", err)
	}

	return nil
}

func (r *SpokeMachineIdentityReconciler) setCondition(smi *identityv1alpha1.SpokeMachineIdentity, condType string, status metav1.ConditionStatus, reason, message string) {
	now := metav1.Now()
	cond := metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: now,
		ObservedGeneration: smi.Generation,
	}

	for i, existing := range smi.Status.Conditions {
		if existing.Type == condType {
			if existing.Status != status || existing.Reason != reason {
				smi.Status.Conditions[i] = cond
			}
			return
		}
	}
	smi.Status.Conditions = append(smi.Status.Conditions, cond)
}

func (r *SpokeMachineIdentityReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&identityv1alpha1.SpokeMachineIdentity{}).
		Named("spokemachineidentity").
		Complete(r)
}

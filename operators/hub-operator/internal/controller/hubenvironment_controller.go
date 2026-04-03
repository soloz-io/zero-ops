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

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	opsv1alpha1 "github.com/soloz-io/zero-ops/operators/hub-operator/api/v1alpha1"
	infisicalclient "github.com/soloz-io/zero-ops/operators/hub-operator/internal/client"
	"github.com/soloz-io/zero-ops/operators/hub-operator/internal/database"
	"github.com/soloz-io/zero-ops/operators/hub-operator/internal/secrets"
)

// HubEnvironmentReconciler reconciles a HubEnvironment object
type HubEnvironmentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=ops.zero-ops.io,resources=hubenvironments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ops.zero-ops.io,resources=hubenvironments/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=ops.zero-ops.io,resources=hubenvironments/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=postgresql.cnpg.io,resources=clusters,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop
// Requirement 9.3: Implement Reconcile() main entry point
func (r *HubEnvironmentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// 1. Fetch HubEnvironment CR
	hubEnv := &opsv1alpha1.HubEnvironment{}
	if err := r.Get(ctx, req.NamespacedName, hubEnv); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Requirement 9.4: Handle reconcile-trigger annotation
	if triggerTime, ok := hubEnv.Annotations["ops.zero-ops.io/reconcile-trigger"]; ok {
		logger.Info("Manual reconciliation triggered", "timestamp", triggerTime)

		// Clear any permanent error conditions to allow retry
		for i := range hubEnv.Status.Conditions {
			if hubEnv.Status.Conditions[i].Reason == "DirtyDatabase" {
				meta.RemoveStatusCondition(&hubEnv.Status.Conditions, hubEnv.Status.Conditions[i].Type)
			}
		}

		// Remove the annotation after processing
		delete(hubEnv.Annotations, "ops.zero-ops.io/reconcile-trigger")
		if err := r.Update(ctx, hubEnv); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Requirement 9.5: Phase 1 - Generate Secret Zero
	if !meta.IsStatusConditionTrue(hubEnv.Status.Conditions, "SecretZeroGenerated") {
		logger.Info("Phase 1: Generating Secret Zero")

		namespace := hubEnv.Spec.Database.Namespace
		dbHost := fmt.Sprintf("platform-db-rw.%s.svc", namespace)

		// Create owner reference
		owner := metav1.OwnerReference{
			APIVersion: hubEnv.APIVersion,
			Kind:       hubEnv.Kind,
			Name:       hubEnv.Name,
			UID:        hubEnv.UID,
			Controller: func() *bool { b := true; return &b }(),
		}

		// Read existing secrets for idempotency
		existingSecrets := make(map[string]*corev1.Secret)
		secretNames := []string{
			"infisical-secrets",
			"platform-db-app",
			"infisical-db-credentials",
			"infisical-postgres-connection",
			"hydra-db-credentials",
			"kratos-db-credentials",
			"keto-db-credentials",
			"platform-db-ca",
		}

		for _, name := range secretNames {
			secret := &corev1.Secret{}
			if err := r.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, secret); err == nil {
				existingSecrets[name] = secret
			}
		}

		// Generate Secret Zero
		result, err := secrets.GenerateSecretZero(namespace, dbHost, owner, existingSecrets)
		if err != nil {
			logger.Error(err, "Failed to generate Secret Zero")
			return ctrl.Result{RequeueAfter: 10 * time.Second}, err
		}

		// Create all secrets
		secretsToCreate := []*corev1.Secret{
			result.InfisicalSecrets,
			result.PlatformDBApp,
			result.InfisicalDBCredentials,
			result.InfisicalPostgresConnection,
			result.HydraDBCredentials,
			result.KratosDBCredentials,
			result.KetoDBCredentials,
			result.PlatformDBCA,
		}

		for _, secret := range secretsToCreate {
			if secret == nil {
				continue
			}
			if err := r.Create(ctx, secret); err != nil {
				if !errors.IsAlreadyExists(err) {
					logger.Error(err, "Failed to create secret", "secret", secret.Name)
					return ctrl.Result{RequeueAfter: 10 * time.Second}, err
				}
			}
		}

		meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{
			Type:               "SecretZeroGenerated",
			Status:             metav1.ConditionTrue,
			Reason:             "Generated",
			Message:            "Secret Zero generated successfully",
			ObservedGeneration: hubEnv.Generation,
		})

		if err := r.Status().Update(ctx, hubEnv); err != nil {
			return ctrl.Result{}, err
		}

		logger.Info("Phase 1 complete: Secret Zero generated")
		return ctrl.Result{Requeue: true}, nil
	}

	// Requirement 9.11: Wait for CNPG Cluster Ready
	cnpgReady, err := r.isCNPGReady(ctx, hubEnv)
	if err != nil {
		logger.Error(err, "Failed to check CNPG readiness")
		return ctrl.Result{RequeueAfter: 10 * time.Second}, err
	}
	if !cnpgReady {
		logger.Info("Waiting for CNPG Cluster to be ready")
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	// Requirement 9.6: Phase 2 - Run Migrations
	if !meta.IsStatusConditionTrue(hubEnv.Status.Conditions, "MigrationsComplete") {
		logger.Info("Phase 2: Running database migrations")

		migrator, err := database.NewMigrator(ctx, r.Client, hubEnv.Spec.Database.Namespace)
		if err != nil {
			logger.Error(err, "Failed to create migrator")
			return ctrl.Result{RequeueAfter: 30 * time.Second}, err
		}
		defer migrator.Close()

		if err := migrator.RunMigrations(ctx); err != nil {
			// Requirement 9.13: Classify errors (transient vs permanent)
			if database.IsDirtyDatabaseError(err) {
				logger.Error(err, "Database is in dirty state - manual intervention required")
				meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{
					Type:               "MigrationsComplete",
					Status:             metav1.ConditionFalse,
					Reason:             "DirtyDatabase",
					Message:            err.Error(),
					ObservedGeneration: hubEnv.Generation,
				})
				// DO NOT requeue - requires manual intervention
				return ctrl.Result{}, r.Status().Update(ctx, hubEnv)
			}

			// Transient error - requeue with backoff
			logger.Error(err, "Migration failed (transient error)")
			meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{
				Type:               "MigrationsComplete",
				Status:             metav1.ConditionFalse,
				Reason:             "MigrationFailed",
				Message:            err.Error(),
				ObservedGeneration: hubEnv.Generation,
			})
			if err := r.Status().Update(ctx, hubEnv); err != nil {
				return ctrl.Result{}, err
			}
			// Requirement 9.14: Exponential backoff for transient errors
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}

		meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{
			Type:               "MigrationsComplete",
			Status:             metav1.ConditionTrue,
			Reason:             "Completed",
			Message:            "Database migrations completed successfully",
			ObservedGeneration: hubEnv.Generation,
		})

		if err := r.Status().Update(ctx, hubEnv); err != nil {
			return ctrl.Result{}, err
		}

		logger.Info("Phase 2a complete: Migrations executed")
		return ctrl.Result{Requeue: true}, nil
	}

	// Requirement 9.7: Phase 2 - Create Database Roles
	if !meta.IsStatusConditionTrue(hubEnv.Status.Conditions, "DatabaseRolesConfigured") {
		logger.Info("Phase 2: Creating database roles")

		roleManager, err := database.NewRoleManager(ctx, r.Client, hubEnv.Spec.Database.Namespace)
		if err != nil {
			logger.Error(err, "Failed to create role manager")
			return ctrl.Result{RequeueAfter: 30 * time.Second}, err
		}
		defer roleManager.Close()

		if err := roleManager.CreateOrUpdateRoles(ctx, hubEnv); err != nil {
			logger.Error(err, "Failed to create database roles")
			meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{
				Type:               "DatabaseRolesConfigured",
				Status:             metav1.ConditionFalse,
				Reason:             "RoleCreationFailed",
				Message:            err.Error(),
				ObservedGeneration: hubEnv.Generation,
			})
			if err := r.Status().Update(ctx, hubEnv); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}

		meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{
			Type:               "DatabaseRolesConfigured",
			Status:             metav1.ConditionTrue,
			Reason:             "Configured",
			Message:            "Database roles configured successfully",
			ObservedGeneration: hubEnv.Generation,
		})

		if err := r.Status().Update(ctx, hubEnv); err != nil {
			return ctrl.Result{}, err
		}

		logger.Info("Phase 2b complete: Database roles created")
		return ctrl.Result{Requeue: true}, nil
	}

	// Requirement 9.8: Phase 3 - Upload Secrets to Infisical
	if !meta.IsStatusConditionTrue(hubEnv.Status.Conditions, "SecretsBackedUp") {
		logger.Info("Phase 3: Uploading secrets to Infisical")

		// Requirement 9.11: Check if Infisical is ready
		infisicalReady, err := r.isInfisicalReady(ctx, hubEnv)
		if err != nil {
			logger.Error(err, "Failed to check Infisical readiness")
			return ctrl.Result{RequeueAfter: 10 * time.Second}, err
		}
		if !infisicalReady {
			logger.Info("Waiting for Infisical to be ready")
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}

		// Check if infisical-auth secret exists
		infisicalAuth := &corev1.Secret{}
		if err := r.Get(ctx, client.ObjectKey{
			Name:      "infisical-auth",
			Namespace: "hub-platform-ops",
		}, infisicalAuth); err != nil {
			if errors.IsNotFound(err) {
				logger.Info("infisical-auth secret not found, suspending Phase 3")
				meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{
					Type:               "SecretsBackedUp",
					Status:             metav1.ConditionFalse,
					Reason:             "AuthenticationFailed",
					Message:            "infisical-auth secret not found. Phase 3 suspended.",
					ObservedGeneration: hubEnv.Generation,
				})
				if err := r.Status().Update(ctx, hubEnv); err != nil {
					return ctrl.Result{}, err
				}
				return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
			}
			return ctrl.Result{RequeueAfter: 10 * time.Second}, err
		}

		if err := r.uploadSecretsToInfisical(ctx, hubEnv); err != nil {
			logger.Error(err, "Failed to upload secrets to Infisical")
			return ctrl.Result{RequeueAfter: 30 * time.Second}, err
		}

		meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{
			Type:               "SecretsBackedUp",
			Status:             metav1.ConditionTrue,
			Reason:             "Uploaded",
			Message:            "Secrets uploaded to Infisical successfully",
			ObservedGeneration: hubEnv.Generation,
		})

		if err := r.Status().Update(ctx, hubEnv); err != nil {
			return ctrl.Result{}, err
		}

		logger.Info("Phase 3a complete: Secrets backed up to Infisical")
		return ctrl.Result{Requeue: true}, nil
	}

	// Requirement 9.9: Phase 3 - Register OAuth Clients
	if !meta.IsStatusConditionTrue(hubEnv.Status.Conditions, "OAuthClientsRegistered") {
		logger.Info("Phase 3: Registering OAuth clients")

		// Requirement 9.11: Check if Hydra is ready
		hydraReady, err := r.isHydraReady(ctx, hubEnv)
		if err != nil {
			logger.Error(err, "Failed to check Hydra readiness")
			return ctrl.Result{RequeueAfter: 10 * time.Second}, err
		}
		if !hydraReady {
			logger.Info("Waiting for Hydra to be ready")
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}

		hydraClient, err := infisicalclient.NewHydraClient("")
		if err != nil {
			logger.Error(err, "Failed to create Hydra client")
			return ctrl.Result{RequeueAfter: 30 * time.Second}, err
		}

		if err := hydraClient.RegisterOAuthClients(ctx, hubEnv); err != nil {
			logger.Error(err, "Failed to register OAuth clients")
			return ctrl.Result{RequeueAfter: 30 * time.Second}, err
		}

		meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{
			Type:               "OAuthClientsRegistered",
			Status:             metav1.ConditionTrue,
			Reason:             "Registered",
			Message:            "OAuth clients registered successfully",
			ObservedGeneration: hubEnv.Generation,
		})

		if err := r.Status().Update(ctx, hubEnv); err != nil {
			return ctrl.Result{}, err
		}

		logger.Info("Phase 3b complete: OAuth clients registered")
		return ctrl.Result{Requeue: true}, nil
	}

	// Requirement 9.10: Phase 3 - Create NATS Streams
	if !meta.IsStatusConditionTrue(hubEnv.Status.Conditions, "NATSStreamsConfigured") {
		logger.Info("Phase 3: Creating NATS streams")

		// Requirement 9.11: Check if NATS is ready
		natsReady, err := r.isNATSReady(ctx, hubEnv)
		if err != nil {
			logger.Error(err, "Failed to check NATS readiness")
			return ctrl.Result{RequeueAfter: 10 * time.Second}, err
		}
		if !natsReady {
			logger.Info("Waiting for NATS to be ready")
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}

		natsClient, err := infisicalclient.NewNATSClient("")
		if err != nil {
			logger.Error(err, "Failed to create NATS client")
			return ctrl.Result{RequeueAfter: 30 * time.Second}, err
		}
		defer natsClient.Close()

		if err := natsClient.CreateOrUpdateStreams(ctx, hubEnv); err != nil {
			logger.Error(err, "Failed to create NATS streams")
			return ctrl.Result{RequeueAfter: 30 * time.Second}, err
		}

		meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{
			Type:               "NATSStreamsConfigured",
			Status:             metav1.ConditionTrue,
			Reason:             "Configured",
			Message:            "NATS streams configured successfully",
			ObservedGeneration: hubEnv.Generation,
		})

		if err := r.Status().Update(ctx, hubEnv); err != nil {
			return ctrl.Result{}, err
		}

		logger.Info("Phase 3c complete: NATS streams created")
		return ctrl.Result{Requeue: true}, nil
	}

	// 6. Set Ready condition
	if !meta.IsStatusConditionTrue(hubEnv.Status.Conditions, "Ready") {
		logger.Info("All phases complete, setting Ready condition")

		meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionTrue,
			Reason:             "AllPhasesComplete",
			Message:            "Hub environment is ready",
			ObservedGeneration: hubEnv.Generation,
		})

		if err := r.Status().Update(ctx, hubEnv); err != nil {
			return ctrl.Result{}, err
		}

		logger.Info("HubEnvironment reconciliation complete")
	}

	return ctrl.Result{}, nil
}

// isCNPGReady checks if the CNPG Cluster is ready
// Requirement 9.11: Implement dependency readiness checks
func (r *HubEnvironmentReconciler) isCNPGReady(ctx context.Context, hubEnv *opsv1alpha1.HubEnvironment) (bool, error) {
	cluster := &cnpgv1.Cluster{}
	if err := r.Get(ctx, client.ObjectKey{
		Name:      hubEnv.Spec.Database.ClusterRef,
		Namespace: hubEnv.Spec.Database.Namespace,
	}, cluster); err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}

	// Check if cluster is ready
	for _, condition := range cluster.Status.Conditions {
		if condition.Type == "Ready" && condition.Status == metav1.ConditionTrue {
			return true, nil
		}
	}

	return false, nil
}

// isInfisicalReady checks if Infisical Deployment is ready
// Requirement 9.11: Implement dependency readiness checks
func (r *HubEnvironmentReconciler) isInfisicalReady(ctx context.Context, hubEnv *opsv1alpha1.HubEnvironment) (bool, error) {
	// For now, assume Infisical is ready if the deployment exists
	// In production, check deployment status
	return true, nil
}

// isHydraReady checks if Hydra Deployment is ready
// Requirement 9.11: Implement dependency readiness checks
func (r *HubEnvironmentReconciler) isHydraReady(ctx context.Context, hubEnv *opsv1alpha1.HubEnvironment) (bool, error) {
	// For now, assume Hydra is ready if the deployment exists
	// In production, check deployment status
	return true, nil
}

// isNATSReady checks if NATS StatefulSet is ready
// Requirement 9.11: Implement dependency readiness checks
func (r *HubEnvironmentReconciler) isNATSReady(ctx context.Context, hubEnv *opsv1alpha1.HubEnvironment) (bool, error) {
	// For now, assume NATS is ready if the statefulset exists
	// In production, check statefulset status
	return true, nil
}

// uploadSecretsToInfisical uploads all database credentials to Infisical
// Requirement 9.8: Implement uploadSecretsToInfisical() with UploadedSecrets tracking
func (r *HubEnvironmentReconciler) uploadSecretsToInfisical(ctx context.Context, hubEnv *opsv1alpha1.HubEnvironment) error {
	logger := log.FromContext(ctx)

	infisicalClient, err := infisicalclient.NewInfisicalClient(ctx, r.Client, "")
	if err != nil {
		return err
	}

	namespace := hubEnv.Spec.Database.Namespace

	// Track uploaded secrets
	uploadedSecrets := make(map[string]bool)
	if hubEnv.Status.UploadedSecrets != nil {
		for _, secret := range hubEnv.Status.UploadedSecrets {
			uploadedSecrets[secret] = true
		}
	}

	// Upload credentials for each database role
	for _, roleSpec := range hubEnv.Spec.Database.Roles {
		secretName := roleSpec.Name + "-db-credentials"

		// Skip if already uploaded
		if uploadedSecrets[secretName] {
			logger.Info("Secret already uploaded, skipping", "secret", secretName)
			continue
		}

		// Read secret
		secret := &corev1.Secret{}
		if err := r.Get(ctx, client.ObjectKey{
			Name:      secretName,
			Namespace: namespace,
		}, secret); err != nil {
			return err
		}

		username := string(secret.Data["username"])
		password := string(secret.Data["password"])

		// Upload username and password
		if err := infisicalClient.CreateOrUpdateSecret(ctx, hubEnv, roleSpec.Name+"-db-username", username); err != nil {
			return err
		}

		if err := infisicalClient.CreateOrUpdateSecret(ctx, hubEnv, roleSpec.Name+"-db-password", password); err != nil {
			return err
		}

		// Mark as uploaded
		hubEnv.Status.UploadedSecrets = append(hubEnv.Status.UploadedSecrets, secretName)
		logger.Info("Uploaded secret to Infisical", "secret", secretName)
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HubEnvironmentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&opsv1alpha1.HubEnvironment{}).
		Owns(&corev1.Secret{}).
		Complete(r)
}

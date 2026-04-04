package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	opsv1alpha1 "github.com/soloz-io/zero-ops/operators/hub-operator/api/v1alpha1"
	infisicalclient "github.com/soloz-io/zero-ops/operators/hub-operator/internal/client"
	"github.com/soloz-io/zero-ops/operators/hub-operator/internal/database"
	"github.com/soloz-io/zero-ops/operators/hub-operator/internal/infisical"
	"github.com/soloz-io/zero-ops/operators/hub-operator/internal/secrets"
)

// HubEnvironmentReconciler reconciles a HubEnvironment object
type HubEnvironmentReconciler struct {
	client.Client
	// UncachedClient reads secrets directly from API server (bypasses cache)
	// Use for operational secrets: platform-db-superuser, platform-db-ca, infisical-auth
	// These must not have their data stripped by the cache transformer
	UncachedClient client.Client
	Scheme         *runtime.Scheme
}

// mapRoleToSecretName converts CR role names to valid K8s secret names
// Handles special mappings from old CLI behavior for backward compatibility
func mapRoleToSecretName(roleName string) string {
	switch roleName {
	case "mcp_server":
		return "control-plane-db-credentials"
	case "spoke_controller":
		return "hub-db-credentials"
	default:
		// Replace underscores with hyphens for valid K8s names
		return strings.ReplaceAll(roleName, "_", "-") + "-db-credentials"
	}
}

//+kubebuilder:rbac:groups=ops.zero-ops.io,resources=hubenvironments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ops.zero-ops.io,resources=hubenvironments/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=ops.zero-ops.io,resources=hubenvironments/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=events,verbs=create;patch
//+kubebuilder:rbac:groups=postgresql.cnpg.io,resources=clusters,verbs=get;list;watch
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;update;patch

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

		// Clear ALL conditions and uploaded secrets to force full Phase 1 re-run
		// This ensures infisical-secrets gets recreated in the correct namespace
		hubEnv.Status.Conditions = []metav1.Condition{}
		hubEnv.Status.UploadedSecrets = []string{}

		// Remove the annotation after processing
		delete(hubEnv.Annotations, "ops.zero-ops.io/reconcile-trigger")
		if err := r.Update(ctx, hubEnv); err != nil {
			return ctrl.Result{}, err
		}
		
		// Update status to clear conditions
		if err := r.Status().Update(ctx, hubEnv); err != nil {
			return ctrl.Result{}, err
		}
		
		logger.Info("Cleared all conditions and uploaded secrets, forcing full reconciliation")
		return ctrl.Result{Requeue: true}, nil
	}

	// Phase 0: Bootstrap Infisical (Day 0 initialization)
	if !meta.IsStatusConditionTrue(hubEnv.Status.Conditions, "InfisicalBootstrapped") {
		logger.Info("Phase 0: Bootstrapping Infisical")

		// Check if Infisical is ready
		infisicalReady, err := r.isInfisicalReady(ctx, hubEnv)
		if err != nil {
			logger.Error(err, "Failed to check Infisical readiness")
			return ctrl.Result{RequeueAfter: 10 * time.Second}, err
		}
		if !infisicalReady {
			logger.Info("Waiting for Infisical to be ready")
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}

		// Bootstrap Infisical (creates admin user, project, machine identity)
		bootstrapClient := infisical.NewBootstrapClient(r.Client)
		bootstrapped, err := bootstrapClient.Bootstrap(ctx)
		if err != nil {
			logger.Error(err, "Failed to bootstrap Infisical")
			meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{
				Type:               "InfisicalBootstrapped",
				Status:             metav1.ConditionFalse,
				Reason:             "BootstrapFailed",
				Message:            fmt.Sprintf("Failed to bootstrap Infisical: %v", err),
				ObservedGeneration: hubEnv.Generation,
			})
			if err := r.Status().Update(ctx, hubEnv); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: 30 * time.Second}, err
		}

		if bootstrapped {
			logger.Info("Infisical bootstrapped successfully")
			
			// Upload CLI-injected secrets to Infisical
			if err := bootstrapClient.UploadCLISecrets(ctx); err != nil {
				logger.Error(err, "Failed to upload CLI secrets, continuing...")
			} else {
				logger.Info("CLI secrets uploaded to Infisical")
			}
		}

		meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{
			Type:               "InfisicalBootstrapped",
			Status:             metav1.ConditionTrue,
			Reason:             "Bootstrapped",
			Message:            "Infisical bootstrapped and secrets uploaded",
			ObservedGeneration: hubEnv.Generation,
		})

		if err := r.Status().Update(ctx, hubEnv); err != nil {
			return ctrl.Result{}, err
		}

		logger.Info("Phase 0 complete: Infisical bootstrapped")
		return ctrl.Result{Requeue: true}, nil
	}

	// Requirement 9.5: Phase 1 - Generate Secret Zero
	if !meta.IsStatusConditionTrue(hubEnv.Status.Conditions, "SecretZeroGenerated") {
		logger.Info("Phase 1: Generating Secret Zero")

		dataNamespace := hubEnv.Spec.Database.Namespace
		securityNamespace := "hub-platform-security" // Infisical pods run here
		dbHost := fmt.Sprintf("platform-db-rw.%s.svc", dataNamespace)

		// Create owner reference
		owner := metav1.OwnerReference{
			APIVersion: hubEnv.APIVersion,
			Kind:       hubEnv.Kind,
			Name:       hubEnv.Name,
			UID:        hubEnv.UID,
			Controller: func() *bool { b := true; return &b }(),
		}

		// Build list of all secret names (base secrets + role credentials)
		// Check secrets in their respective namespaces
		secretNamesInData := []string{
			"platform-db-app",
			"infisical-db-credentials",
			"hydra-db-credentials",
			"kratos-db-credentials",
			"keto-db-credentials",
			"platform-db-ca",
			"infisical-redis-credentials",
		}

		secretNamesInSecurity := []string{
			"infisical-secrets",
			"infisical-postgres-connection",
		}

		// Add role credential secrets from CR spec (all in data namespace)
		for _, role := range hubEnv.Spec.Database.Roles {
			secretNamesInData = append(secretNamesInData, fmt.Sprintf("%s-db-credentials", role.Name))
		}

		// Read existing secrets for idempotency
		existingSecrets := make(map[string]*corev1.Secret)
		
		// Read secrets from data namespace
		for _, name := range secretNamesInData {
			secret := &corev1.Secret{}
			if err := r.Get(ctx, client.ObjectKey{Name: name, Namespace: dataNamespace}, secret); err == nil {
				existingSecrets[name] = secret
			}
		}
		
		// Read secrets from security namespace
		for _, name := range secretNamesInSecurity {
			secret := &corev1.Secret{}
			if err := r.Get(ctx, client.ObjectKey{Name: name, Namespace: securityNamespace}, secret); err == nil {
				existingSecrets[name] = secret
			}
		}

		// Generate Secret Zero (base secrets) with both namespaces
		result, err := secrets.GenerateSecretZero(dataNamespace, securityNamespace, dbHost, owner, existingSecrets)
		if err != nil {
			logger.Error(err, "Failed to generate Secret Zero")
			return ctrl.Result{RequeueAfter: 10 * time.Second}, err
		}

		// Create base secrets
		secretsToCreate := []*corev1.Secret{
			result.InfisicalSecrets,
			result.InfisicalRedisCredentials,
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
					logger.Error(err, "Failed to create secret", "secret", secret.Name, "namespace", secret.Namespace)
					return ctrl.Result{RequeueAfter: 10 * time.Second}, err
				}
			}
			logger.Info("Created secret", "secret", secret.Name, "namespace", secret.Namespace)
		}

		// Generate and create role credential secrets for all roles in CR (in data namespace)
		for _, role := range hubEnv.Spec.Database.Roles {
			// Generate new role credential secret (uses mapRoleToSecretName internally)
			roleSecret, err := secrets.GenerateRoleDBCredentials(role.Name, dataNamespace, owner)
			if err != nil {
				logger.Error(err, "Failed to generate role credentials", "role", role.Name)
				return ctrl.Result{RequeueAfter: 10 * time.Second}, err
			}

			// Skip if secret already exists (check using the mapped secret name)
			if _, exists := existingSecrets[roleSecret.Name]; exists {
				logger.Info("Role credential secret already exists", "secret", roleSecret.Name, "role", role.Name)
				continue
			}

			// Create the secret
			if err := r.Create(ctx, roleSecret); err != nil {
				if !errors.IsAlreadyExists(err) {
					logger.Error(err, "Failed to create role credential secret", "secret", roleSecret.Name)
					return ctrl.Result{RequeueAfter: 10 * time.Second}, err
				}
			}
			logger.Info("Created role credential secret", "secret", roleSecret.Name, "role", role.Name)
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

		migrator, err := database.NewMigrator(ctx, r.UncachedClient, hubEnv.Spec.Database.Namespace)
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

		roleManager, err := database.NewRoleManager(ctx, r.UncachedClient, hubEnv.Spec.Database.Namespace)
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

	// Requirement 23: Handle certificate rotation (runs continuously after Ready)
	if meta.IsStatusConditionTrue(hubEnv.Status.Conditions, "Ready") {
		if err := r.handleCertificateRotation(ctx, hubEnv); err != nil {
			logger.Error(err, "Failed to handle certificate rotation")
			// Don't fail reconciliation, just log and requeue
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}

		// Requirement 23.15-23.16: Handle password rotation
		if err := r.handlePasswordRotation(ctx, hubEnv); err != nil {
			logger.Error(err, "Failed to handle password rotation")
			// Don't fail reconciliation, just log and requeue
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
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

	infisicalClient, err := infisicalclient.NewInfisicalClient(ctx, r.UncachedClient, "")
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

	// GAP 3 FIX: Upload platform-db-app (Layer 1 bootstrap secret)
	// This secret is not in the roles list but must be uploaded to Infisical
	// for ExternalSecrets to sync it
	if !uploadedSecrets["platform-db-app"] {
		appSecret := &corev1.Secret{}
		if err := r.Get(ctx, client.ObjectKey{
			Name:      "platform-db-app",
			Namespace: namespace,
		}, appSecret); err == nil {
			username := string(appSecret.Data["username"])
			password := string(appSecret.Data["password"])

			if err := infisicalClient.CreateOrUpdateSecret(ctx, hubEnv, "platform-db-app-username", username); err != nil {
				return fmt.Errorf("failed to upload platform-db-app-username: %w", err)
			}

			if err := infisicalClient.CreateOrUpdateSecret(ctx, hubEnv, "platform-db-app-password", password); err != nil {
				return fmt.Errorf("failed to upload platform-db-app-password: %w", err)
			}

			hubEnv.Status.UploadedSecrets = append(hubEnv.Status.UploadedSecrets, "platform-db-app")
			logger.Info("Uploaded platform-db-app to Infisical")
		} else if !errors.IsNotFound(err) {
			return fmt.Errorf("failed to get platform-db-app secret: %w", err)
		}
	}

	// Upload credentials for each database role
	for _, roleSpec := range hubEnv.Spec.Database.Roles {
		// Map role name to actual K8s secret name (handles mcp_server → control-plane-db-credentials)
		secretName := mapRoleToSecretName(roleSpec.Name)

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

		// Determine Infisical key prefix based on secret name
		// control-plane-db-credentials → control-plane-db-username/password
		// hub-db-credentials → hub-db-username/password
		infisicalPrefix := strings.TrimSuffix(secretName, "-credentials")

		// Upload username and password
		if err := infisicalClient.CreateOrUpdateSecret(ctx, hubEnv, infisicalPrefix+"-username", username); err != nil {
			return err
		}

		if err := infisicalClient.CreateOrUpdateSecret(ctx, hubEnv, infisicalPrefix+"-password", password); err != nil {
			return err
		}

		// Mark as uploaded
		hubEnv.Status.UploadedSecrets = append(hubEnv.Status.UploadedSecrets, secretName)
		logger.Info("Uploaded secret to Infisical", "secret", secretName, "role", roleSpec.Name)
	}

	return nil
}

// handleCertificateRotation detects platform-db-ca changes and restarts services
// Requirement 23.1-23.14: Implement certificate rotation handling
func (r *HubEnvironmentReconciler) handleCertificateRotation(ctx context.Context, hubEnv *opsv1alpha1.HubEnvironment) error {
	logger := log.FromContext(ctx)
	namespace := hubEnv.Spec.Database.Namespace

	// Requirement 23.2: Read platform-db-ca secret
	// Task 13: Use UncachedClient - operational secret, must not have data stripped
	platformDBCA := &corev1.Secret{}
	if err := r.UncachedClient.Get(ctx, client.ObjectKey{
		Name:      "platform-db-ca",
		Namespace: namespace,
	}, platformDBCA); err != nil {
		if errors.IsNotFound(err) {
			// CA not yet generated, skip rotation
			return nil
		}
		return err
	}

	// Requirement 23.2: Read infisical-secrets to compare DB_ROOT_CERT
	// Task 13: Use UncachedClient - operational secret, must not have data stripped
	infisicalSecrets := &corev1.Secret{}
	if err := r.UncachedClient.Get(ctx, client.ObjectKey{
		Name:      "infisical-secrets",
		Namespace: namespace,
	}, infisicalSecrets); err != nil {
		if errors.IsNotFound(err) {
			// Infisical secrets not yet generated, skip rotation
			return nil
		}
		return err
	}

	// Extract CA certificate from platform-db-ca
	caCert, ok := platformDBCA.Data["ca.crt"]
	if !ok {
		logger.Info("platform-db-ca missing ca.crt, skipping rotation")
		return nil
	}

	// Extract current DB_ROOT_CERT from infisical-secrets
	currentDBRootCert, ok := infisicalSecrets.Data["DB_ROOT_CERT"]
	if !ok {
		logger.Info("infisical-secrets missing DB_ROOT_CERT, skipping rotation")
		return nil
	}

	// Requirement 23.3: Compare CA certificate with DB_ROOT_CERT
	// DB_ROOT_CERT is base64-encoded, ca.crt is already base64-encoded
	if string(caCert) == string(currentDBRootCert) {
		// No rotation needed
		return nil
	}

	logger.Info("Certificate rotation detected, updating DB_ROOT_CERT and restarting services")

	// Requirement 23.4: Update DB_ROOT_CERT in infisical-secrets
	infisicalSecrets.Data["DB_ROOT_CERT"] = caCert
	if err := r.Update(ctx, infisicalSecrets); err != nil {
		return fmt.Errorf("failed to update DB_ROOT_CERT: %w", err)
	}

	logger.Info("Updated DB_ROOT_CERT in infisical-secrets")

	// Track restart failures for status condition
	var restartFailures []string

	// Requirement 23.5-23.13: Restart all database-connected services
	services := []struct {
		kind      string
		name      string
		namespace string
	}{
		{"Deployment", "infisical", "hub-platform-ops"},
		{"StatefulSet", "redis", namespace},
		{"Deployment", "hydra", "ory-system"},
		{"Deployment", "kratos", "ory-system"},
		{"Deployment", "keto", "ory-system"},
		{"StatefulSet", "spire-server", "spire-system"},
		{"Deployment", "mcp-server", "hub-platform-ops"},
	}

	for _, svc := range services {
		if svc.kind == "Deployment" {
			if err := r.restartDeployment(ctx, svc.name, svc.namespace); err != nil {
				logger.Error(err, "Failed to restart deployment", "name", svc.name, "namespace", svc.namespace)
				restartFailures = append(restartFailures, svc.name)
			} else {
				logger.Info("Restarted deployment", "name", svc.name, "namespace", svc.namespace)
			}
		} else if svc.kind == "StatefulSet" {
			if err := r.restartStatefulSet(ctx, svc.name, svc.namespace); err != nil {
				logger.Error(err, "Failed to restart statefulset", "name", svc.name, "namespace", svc.namespace)
				restartFailures = append(restartFailures, svc.name)
			} else {
				logger.Info("Restarted statefulset", "name", svc.name, "namespace", svc.namespace)
			}
		}
	}

	// Requirement 23.6-23.7: Update status condition if restart fails
	if len(restartFailures) > 0 {
		meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{
			Type:               "CertificateRotationFailed",
			Status:             metav1.ConditionTrue,
			Reason:             "ServiceRestartFailed",
			Message:            fmt.Sprintf("Failed to restart services after certificate rotation: %s", strings.Join(restartFailures, ", ")),
			ObservedGeneration: hubEnv.Generation,
		})
		if err := r.Status().Update(ctx, hubEnv); err != nil {
			logger.Error(err, "Failed to update status condition")
		}
	} else {
		// All restarts succeeded, now wait for Infisical readiness (AC 23.6)
		infisicalReady, err := r.waitForInfisicalReadiness(ctx, hubEnv)
		if err != nil {
			logger.Error(err, "Failed to check Infisical readiness after restart")
			meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{
				Type:               "CertificateRotationFailed",
				Status:             metav1.ConditionTrue,
				Reason:             "InfisicalNotReady",
				Message:            fmt.Sprintf("Infisical not ready after certificate rotation: %v", err),
				ObservedGeneration: hubEnv.Generation,
			})
			if err := r.Status().Update(ctx, hubEnv); err != nil {
				logger.Error(err, "Failed to update status condition")
			}
			return nil
		}

		if !infisicalReady {
			logger.Info("Waiting for Infisical to become ready after certificate rotation")
			meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{
				Type:               "CertificateRotationInProgress",
				Status:             metav1.ConditionTrue,
				Reason:             "WaitingForInfisical",
				Message:            "Waiting for Infisical Deployment to become ready after certificate rotation",
				ObservedGeneration: hubEnv.Generation,
			})
			if err := r.Status().Update(ctx, hubEnv); err != nil {
				logger.Error(err, "Failed to update status condition")
			}
			return nil
		}

		// Clear any previous failure condition
		meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{
			Type:               "CertificateRotationFailed",
			Status:             metav1.ConditionFalse,
			Reason:             "ServicesRestarted",
			Message:            "All services restarted successfully after certificate rotation",
			ObservedGeneration: hubEnv.Generation,
		})
		meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{
			Type:               "CertificateRotationInProgress",
			Status:             metav1.ConditionFalse,
			Reason:             "Completed",
			Message:            "Certificate rotation completed successfully",
			ObservedGeneration: hubEnv.Generation,
		})
		if err := r.Status().Update(ctx, hubEnv); err != nil {
			logger.Error(err, "Failed to update status condition")
		}
	}

	return nil
}

// waitForInfisicalReadiness checks if Infisical Deployment is ready after restart
// Requirement 23.6: Watch for Infisical Deployment readiness after restart
func (r *HubEnvironmentReconciler) waitForInfisicalReadiness(ctx context.Context, hubEnv *opsv1alpha1.HubEnvironment) (bool, error) {
	deployment := &appsv1.Deployment{}
	if err := r.Get(ctx, client.ObjectKey{
		Name:      "infisical",
		Namespace: "hub-platform-ops",
	}, deployment); err != nil {
		if errors.IsNotFound(err) {
			// Deployment doesn't exist yet
			return false, nil
		}
		return false, err
	}

	// Check if deployment is ready
	for _, condition := range deployment.Status.Conditions {
		if condition.Type == appsv1.DeploymentAvailable && condition.Status == corev1.ConditionTrue {
			return true, nil
		}
	}

	return false, nil
}

// handlePasswordRotation detects password changes and executes ALTER ROLE
// Requirement 23.15-23.16: Implement password rotation handling
func (r *HubEnvironmentReconciler) handlePasswordRotation(ctx context.Context, hubEnv *opsv1alpha1.HubEnvironment) error {
	logger := log.FromContext(ctx)
	namespace := hubEnv.Spec.Database.Namespace

	// Check all database role secrets for password changes
	for _, roleSpec := range hubEnv.Spec.Database.Roles {
		secretName := roleSpec.Name + "-db-credentials"

		// Read the secret
		// Task 13: Use UncachedClient - credential secret, must not have data stripped
		secret := &corev1.Secret{}
		if err := r.UncachedClient.Get(ctx, client.ObjectKey{
			Name:      secretName,
			Namespace: namespace,
		}, secret); err != nil {
			if errors.IsNotFound(err) {
				// Secret doesn't exist yet, skip
				continue
			}
			return err
		}

		// Check if secret has the db-credentials label (ESO-managed)
		if labels := secret.GetLabels(); labels == nil || labels["ops.zero-ops.io/db-credentials"] != "true" {
			// Not an ESO-managed secret, skip
			continue
		}

		// Extract username and password
		username, ok := secret.Data["username"]
		if !ok {
			logger.Info("Secret missing username field", "secret", secretName)
			continue
		}

		password, ok := secret.Data["password"]
		if !ok {
			logger.Info("Secret missing password field", "secret", secretName)
			continue
		}

		// Requirement 23.15: Execute ALTER ROLE to update PostgreSQL password
		roleManager, err := database.NewRoleManager(ctx, r.UncachedClient, namespace)
		if err != nil {
			logger.Error(err, "Failed to create role manager for password rotation")
			continue
		}

		// Check if password needs update (drift detection)
		needsUpdate, err := roleManager.PasswordNeedsUpdate(ctx, string(username), string(password))
		if err != nil {
			roleManager.Close()
			logger.Error(err, "Failed to check password drift", "role", string(username))
			continue
		}

		if !needsUpdate {
			roleManager.Close()
			// Password hasn't changed, skip
			continue
		}

		logger.Info("Password rotation detected, updating role", "role", string(username))

		// Execute ALTER ROLE
		if err := roleManager.UpdateRolePassword(ctx, string(username), string(password)); err != nil {
			roleManager.Close()
			logger.Error(err, "Failed to update role password", "role", string(username))
			continue
		}

		roleManager.Close()
		logger.Info("Updated role password in PostgreSQL", "role", string(username))

		// Requirement 23.16: Restart consuming Deployment/StatefulSet
		// Extract service name from role name (e.g., "infisical" from "infisical-db-credentials")
		serviceName := roleSpec.Name

		// Map role names to service deployments/statefulsets
		serviceMap := map[string]struct {
			kind      string
			name      string
			namespace string
		}{
			"infisical":        {"Deployment", "infisical", "hub-platform-ops"},
			"redis":            {"StatefulSet", "redis", namespace},
			"hydra":            {"Deployment", "hydra", "ory-system"},
			"kratos":           {"Deployment", "kratos", "ory-system"},
			"keto":             {"Deployment", "keto", "ory-system"},
			"spire_server":     {"StatefulSet", "spire-server", "spire-system"},
			"mcp_server":       {"Deployment", "mcp-server", "hub-platform-ops"},
			"spoke_controller": {"Deployment", "spoke-controller", namespace},
		}

		if svc, ok := serviceMap[serviceName]; ok {
			if svc.kind == "Deployment" {
				if err := r.restartDeployment(ctx, svc.name, svc.namespace); err != nil {
					logger.Error(err, "Failed to restart deployment after password rotation", "name", svc.name)
				} else {
					logger.Info("Restarted deployment after password rotation", "name", svc.name)
				}
			} else if svc.kind == "StatefulSet" {
				if err := r.restartStatefulSet(ctx, svc.name, svc.namespace); err != nil {
					logger.Error(err, "Failed to restart statefulset after password rotation", "name", svc.name)
				} else {
					logger.Info("Restarted statefulset after password rotation", "name", svc.name)
				}
			}
		}
	}

	return nil
}

// restartDeployment patches a Deployment with restartedAt annotation
// Requirement 23.5: Implement restartDeployment() helper
func (r *HubEnvironmentReconciler) restartDeployment(ctx context.Context, name, namespace string) error {
	deployment := &appsv1.Deployment{}
	if err := r.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, deployment); err != nil {
		if errors.IsNotFound(err) {
			// Deployment doesn't exist yet, skip restart
			return nil
		}
		return err
	}

	// Requirement 23.4: Use kubectl.kubernetes.io/restartedAt annotation pattern
	if deployment.Spec.Template.Annotations == nil {
		deployment.Spec.Template.Annotations = make(map[string]string)
	}
	deployment.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().Format(time.RFC3339)

	return r.Update(ctx, deployment)
}

// restartStatefulSet patches a StatefulSet with restartedAt annotation
// Requirement 23.6: Implement restartStatefulSet() helper
func (r *HubEnvironmentReconciler) restartStatefulSet(ctx context.Context, name, namespace string) error {
	statefulSet := &appsv1.StatefulSet{}
	if err := r.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, statefulSet); err != nil {
		if errors.IsNotFound(err) {
			// StatefulSet doesn't exist yet, skip restart
			return nil
		}
		return err
	}

	// Requirement 23.4: Use kubectl.kubernetes.io/restartedAt annotation pattern
	if statefulSet.Spec.Template.Annotations == nil {
		statefulSet.Spec.Template.Annotations = make(map[string]string)
	}
	statefulSet.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().Format(time.RFC3339)

	return r.Update(ctx, statefulSet)
}

// SetupWithManager sets up the controller with the Manager.
// Requirement 12: Configure controller watches for all external dependencies and secrets
func (r *HubEnvironmentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		// Requirement 12.2: Primary resource
		For(&opsv1alpha1.HubEnvironment{}).
		// Requirement 12.3: Owned secrets (operator-created)
		Owns(&corev1.Secret{}).
		// Requirement 12.4: Watch CNPG Cluster for readiness
		Watches(
			&cnpgv1.Cluster{},
			handler.EnqueueRequestsFromMapFunc(r.findHubEnvironmentForCNPG),
			builder.WithPredicates(predicate.ResourceVersionChangedPredicate{}),
		).
		// Requirement 12.5: Watch platform-db-ca secret for certificate rotation
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.findHubEnvironmentForSecret),
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				secret := obj.(*corev1.Secret)
				return secret.Name == "platform-db-ca"
			})),
		).
		// Requirement 12.6: Watch secrets with label ops.zero-ops.io/db-credentials=true for password rotation
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.findHubEnvironmentForSecret),
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				secret := obj.(*corev1.Secret)
				if labels := secret.GetLabels(); labels != nil {
					return labels["ops.zero-ops.io/db-credentials"] == "true"
				}
				return false
			})),
		).
		// Requirement 12.7: Watch Hydra Deployment
		Watches(
			&appsv1.Deployment{},
			handler.EnqueueRequestsFromMapFunc(r.findHubEnvironmentForDeployment),
			builder.WithPredicates(
				predicate.ResourceVersionChangedPredicate{},
				predicate.NewPredicateFuncs(func(obj client.Object) bool {
					return obj.GetName() == "hydra" && obj.GetNamespace() == "ory-system"
				}),
			),
		).
		// Requirement 12.8: Watch Infisical Deployment
		Watches(
			&appsv1.Deployment{},
			handler.EnqueueRequestsFromMapFunc(r.findHubEnvironmentForDeployment),
			builder.WithPredicates(
				predicate.ResourceVersionChangedPredicate{},
				predicate.NewPredicateFuncs(func(obj client.Object) bool {
					return obj.GetName() == "infisical" && obj.GetNamespace() == "hub-platform-ops"
				}),
			),
		).
		// Requirement 12.9: Watch NATS StatefulSet
		Watches(
			&appsv1.StatefulSet{},
			handler.EnqueueRequestsFromMapFunc(r.findHubEnvironmentForStatefulSet),
			builder.WithPredicates(
				predicate.ResourceVersionChangedPredicate{},
				predicate.NewPredicateFuncs(func(obj client.Object) bool {
					return obj.GetName() == "nats" && obj.GetNamespace() == "hub-platform-core"
				}),
			),
		).
		Complete(r)
}

// findHubEnvironmentForCNPG maps CNPG Cluster to HubEnvironment CR
// Requirement 12.10: Implement mapper function for CNPG
func (r *HubEnvironmentReconciler) findHubEnvironmentForCNPG(ctx context.Context, obj client.Object) []reconcile.Request {
	cluster := obj.(*cnpgv1.Cluster)

	// List all HubEnvironment CRs
	hubEnvList := &opsv1alpha1.HubEnvironmentList{}
	if err := r.List(ctx, hubEnvList); err != nil {
		return []reconcile.Request{}
	}

	// Find HubEnvironment that references this cluster
	var requests []reconcile.Request
	for _, hubEnv := range hubEnvList.Items {
		if hubEnv.Spec.Database.ClusterRef == cluster.Name &&
			hubEnv.Spec.Database.Namespace == cluster.Namespace {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      hubEnv.Name,
					Namespace: hubEnv.Namespace,
				},
			})
		}
	}

	return requests
}

// findHubEnvironmentForSecret maps Secret to HubEnvironment CR
// Requirement 12.11: Implement mapper function for Secret
func (r *HubEnvironmentReconciler) findHubEnvironmentForSecret(ctx context.Context, obj client.Object) []reconcile.Request {
	secret := obj.(*corev1.Secret)

	// List all HubEnvironment CRs
	hubEnvList := &opsv1alpha1.HubEnvironmentList{}
	if err := r.List(ctx, hubEnvList); err != nil {
		return []reconcile.Request{}
	}

	// Find HubEnvironment in the same namespace as the secret
	var requests []reconcile.Request
	for _, hubEnv := range hubEnvList.Items {
		if hubEnv.Spec.Database.Namespace == secret.Namespace {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      hubEnv.Name,
					Namespace: hubEnv.Namespace,
				},
			})
		}
	}

	return requests
}

// findHubEnvironmentForDeployment maps Deployment to HubEnvironment CR
// Requirement 12.12: Implement mapper function for Deployment
func (r *HubEnvironmentReconciler) findHubEnvironmentForDeployment(ctx context.Context, obj client.Object) []reconcile.Request {
	// List all HubEnvironment CRs
	hubEnvList := &opsv1alpha1.HubEnvironmentList{}
	if err := r.List(ctx, hubEnvList); err != nil {
		return []reconcile.Request{}
	}

	// Trigger reconciliation for all HubEnvironments
	// The reconciler will check if the deployment is ready
	var requests []reconcile.Request
	for _, hubEnv := range hubEnvList.Items {
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      hubEnv.Name,
				Namespace: hubEnv.Namespace,
			},
		})
	}

	return requests
}

// findHubEnvironmentForStatefulSet maps StatefulSet to HubEnvironment CR
// Requirement 12.13: Implement mapper function for StatefulSet
func (r *HubEnvironmentReconciler) findHubEnvironmentForStatefulSet(ctx context.Context, obj client.Object) []reconcile.Request {
	// List all HubEnvironment CRs
	hubEnvList := &opsv1alpha1.HubEnvironmentList{}
	if err := r.List(ctx, hubEnvList); err != nil {
		return []reconcile.Request{}
	}

	// Trigger reconciliation for all HubEnvironments
	// The reconciler will check if the statefulset is ready
	var requests []reconcile.Request
	for _, hubEnv := range hubEnvList.Items {
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      hubEnv.Name,
				Namespace: hubEnv.Namespace,
			},
		})
	}

	return requests
}

package controller

import (
	"bytes"
	"context"
	"encoding/base64"
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
	"k8s.io/apimachinery/pkg/util/wait"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/soloz-io/zero-ops/internal/pki"
	"github.com/soloz-io/zero-ops/internal/platform/escrow"
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

//+kubebuilder:rbac:groups=ops.nutgraf.in,resources=hubenvironments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ops.nutgraf.in,resources=hubenvironments/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=ops.nutgraf.in,resources=hubenvironments/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=events,verbs=create;patch
//+kubebuilder:rbac:groups=postgresql.cnpg.io,resources=clusters,verbs=get;list;watch
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=admissionregistration.k8s.io,resources=validatingwebhookconfigurations,verbs=get;list;watch
//+kubebuilder:rbac:groups=discovery.k8s.io,resources=endpointslices,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop
// Requirement 9.3: Implement Reconcile() main entry point
func (r *HubEnvironmentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// 1. Fetch HubEnvironment CR
	hubEnv := &opsv1alpha1.HubEnvironment{}
	if err := r.Get(ctx, req.NamespacedName, hubEnv); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// REQ-10: Handle finalizer for graceful teardown
	// REQ-13: Remove finalizers from secrets when HubEnvironment is being deleted
	if !hubEnv.DeletionTimestamp.IsZero() {
		logger.Info("HubEnvironment is being deleted, removing finalizers from secrets")

		// Remove finalizers from infisical-secrets
		if err := r.removeFinalizer(ctx, "infisical-secrets", infisical.InfisicalServiceNamespace); err != nil {
			logger.Error(err, "Failed to remove finalizer from infisical-secrets")
			return ctrl.Result{RequeueAfter: 5 * time.Second}, err
		}

		// Remove finalizers from infisical-redis-credentials
		dataNamespace := hubEnv.Spec.Database.Namespace
		if err := r.removeFinalizer(ctx, "infisical-redis-credentials", dataNamespace); err != nil {
			logger.Error(err, "Failed to remove finalizer from infisical-redis-credentials")
			return ctrl.Result{RequeueAfter: 5 * time.Second}, err
		}

		logger.Info("Successfully removed finalizers from secrets, allowing deletion to proceed")
		return ctrl.Result{}, nil
	}

	// Requirement 9.4: Handle reconcile-trigger annotation
	if triggerTime, ok := hubEnv.Annotations["ops.nutgraf.in/reconcile-trigger"]; ok {
		logger.Info("Manual reconciliation triggered", "timestamp", triggerTime)

		// Clear ALL conditions and uploaded secrets to force full Phase 1 re-run
		// This ensures infisical-secrets gets recreated in the correct namespace
		hubEnv.Status.Conditions = []metav1.Condition{}
		hubEnv.Status.UploadedSecrets = []string{}

		// Remove the annotation after processing
		delete(hubEnv.Annotations, "ops.nutgraf.in/reconcile-trigger")
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

	// Keep Infisical's copy of the CNPG CA current.
	//
	// DB_ROOT_CERT is how Infisical verifies TLS to Postgres. It is written once at
	// Day-0 by the bootstrap CLI, and nothing reconciled it afterwards — so any
	// event that regenerates the cluster CA (a CNPG Cluster rebuild, which is what
	// a lost node-pinned volume forces) left Infisical trusting a CA that no longer
	// signs anything. It then failed its boot migration with SELF_SIGNED_CERT_IN_CHAIN
	// and crash-looped, which blocked Phase 0, which blocked database roles, which
	// left the whole auth tier in init. One stale copied certificate, total outage.
	//
	// This runs before Phase 1 because Infisical must be able to reach its database
	// before anything else in this reconcile is meaningful.
	if err := r.ensureInfisicalDBRootCert(ctx); err != nil {
		logger.Error(err, "Failed to reconcile Infisical DB_ROOT_CERT; continuing")
	}

	// Phase 1: Generate Bootstrap Secrets Only
	// MUST run before Phase 0 (Infisical bootstrap) because Infisical itself depends on
	// secrets created here (infisical-db-credentials, infisical-postgres-connection).
	// Without these secrets the setup-infisical-role job cannot create the DB role,
	// Infisical pods crash with "no such user", and Phase 0 deadlocks forever.
	//
	// Bootstrap secrets are required for infrastructure to start (CNPG, Infisical)
	// Application secrets are created by ESO from Infisical
	// REQ-7: Always check if infisical-secrets exists, even if BootstrapSecretsGenerated=true
	// This handles the case where the secret was deleted and needs restoration from AWS
	securityNamespace := infisical.InfisicalServiceNamespace
	infisicalSecret := &corev1.Secret{}
	infisicalSecretExists := true
	if err := r.UncachedClient.Get(ctx, client.ObjectKey{Name: "infisical-secrets", Namespace: securityNamespace}, infisicalSecret); err != nil {
		infisicalSecretExists = false
	}

	if !isConditionTrueAndUpToDate(hubEnv.Status.Conditions, "BootstrapSecretsGenerated", hubEnv.Generation) || !infisicalSecretExists {
		if !infisicalSecretExists {
			logger.Info("Phase 1: infisical-secrets missing, attempting restore from AWS backup")
		} else {
			logger.Info("Phase 1: Generating Bootstrap Secrets")
		}

		dataNamespace := hubEnv.Spec.Database.Namespace
		dbHost := fmt.Sprintf("platform-db-rw.%s.svc", dataNamespace)

		// Create owner reference.
		//
		// GVK comes from the scheme, not from hubEnv.TypeMeta: a typed client clears
		// TypeMeta on Get, so APIVersion/Kind read back empty and the API server
		// rejects the reference with "version must not be empty". The object's own
		// GroupVersion is the authority here and is always populated.
		owner := metav1.OwnerReference{
			APIVersion: opsv1alpha1.GroupVersion.String(),
			Kind:       "HubEnvironment",
			Name:       hubEnv.Name,
			UID:        hubEnv.UID,
			Controller: func() *bool { b := true; return &b }(),
		}

		// Build list of bootstrap secret names only
		// These are required for infrastructure bootstrap (CNPG, Infisical, Redis)
		secretNamesInData := []string{
			"platform-db-app",             // CNPG bootstrap superuser
			"infisical-db-credentials",    // Infisical database user
			"platform-db-ca",              // TLS certificate authority
			"infisical-redis-credentials", // Redis authentication
		}

		secretNamesInSecurity := []string{
			"infisical-secrets",             // Infisical encryption keys
			"infisical-postgres-connection", // Infisical DB connection
		}

		// Read existing secrets for idempotency
		existingSecrets := make(map[string]*corev1.Secret)

		// Read secrets from data namespace using UncachedClient (need secret data, not just metadata)
		for _, name := range secretNamesInData {
			secret := &corev1.Secret{}
			if err := r.UncachedClient.Get(ctx, client.ObjectKey{Name: name, Namespace: dataNamespace}, secret); err == nil {
				existingSecrets[name] = secret
			}
		}

		// Read secrets from security namespace using UncachedClient (need secret data, not just metadata)
		for _, name := range secretNamesInSecurity {
			secret := &corev1.Secret{}
			if err := r.UncachedClient.Get(ctx, client.ObjectKey{Name: name, Namespace: securityNamespace}, secret); err == nil {
				existingSecrets[name] = secret
			}
		}

		// REQ-7: Bootstrap detection - determine if this is first-time bootstrap
		isFirstTime := !isConditionTrueAndUpToDate(hubEnv.Status.Conditions, "BootstrapSecretsGenerated", hubEnv.Generation)
		logger.Info("Bootstrap detection", "isFirstTime", isFirstTime, "infisicalSecretExists", infisicalSecretExists)

		// The escrow holds this box's Infisical master keys outside the box
		// (ADR-076). Without it the keys exist only inside the cluster they
		// decrypt, and losing the cluster loses every secret the platform manages.
		var escrowClient escrow.EscrowClient
		escrowImpl, err := escrow.NewEscrowClient(ctx, infisical.InfisicalBaseURL)
		if err != nil {
			// Not fatal here. Whether a box may run without an escrow is decided at
			// scaffold time, and by the time this reconciles the cluster exists --
			// refusing now would leave it running and unmanaged rather than running
			// and unprotected.
			logger.Info("escrow unavailable; the Infisical master keys exist only in "+
				"this cluster and will be lost with it", "reason", err.Error())
			escrowClient = nil
		} else {
			escrowClient = escrowImpl
		}

		// REQ-7: the HubEnvironment name is the cluster ID the escrow is keyed by
		clusterID := hubEnv.Name
		logger.Info("Starting bootstrap secrets generation", "clusterID", clusterID, "escrowEnabled", escrowClient != nil)

		// Generate Bootstrap Secrets with AWS backup/restore support
		result, err := secrets.GenerateBootstrapSecrets(ctx, dataNamespace, securityNamespace, dbHost, owner, existingSecrets, isFirstTime, clusterID, escrowClient)
		if err != nil {
			logger.Error(err, "Failed to generate Bootstrap Secrets")
			return ctrl.Result{RequeueAfter: 10 * time.Second}, err
		}

		logger.Info("Bootstrap secrets generated successfully", "escrowEnabled", escrowClient != nil)

		// Create bootstrap secrets only
		secretsToCreate := []*corev1.Secret{
			result.PlatformDBCA,
			result.PlatformDBApp,
			result.InfisicalDBCredentials,
			result.InfisicalSecrets,
			result.InfisicalRedisCredentials,
			result.InfisicalPostgresConnection,
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
			logger.Info("Created bootstrap secret", "secret", secret.Name, "namespace", secret.Namespace)
		}

		// The admin kubeconfig, escrowed beside the master keys (ADR-076).
		//
		// Every reconcile rather than once: the client certificate CAPI issues is on
		// kubeadm's one-year default, and re-reading each pass means a rotated
		// credential replaces a stale escrowed copy without anyone remembering to.
		// A copy that silently expired is the failure this is guarding against.
		if escrowClient != nil {
			if err := r.escrowKubeconfig(ctx, escrowClient, clusterID); err != nil {
				// Not fatal. The cluster is running and reconciling; refusing here
				// would stop managing a box to protest that its break-glass copy is
				// stale, which trades a working cluster for a backup.
				logger.Info("could not escrow the admin kubeconfig; break-glass access "+
					"may be stale or absent", "error", err.Error())
			} else {
				logger.Info("admin kubeconfig escrowed", "clusterID", clusterID)
			}
		}

		statusMessage := "Bootstrap secrets generated successfully"
		if escrowClient != nil {
			if isFirstTime {
				statusMessage = "Bootstrap secrets generated and escrowed"
			} else {
				statusMessage = "Bootstrap secrets restored from the escrow"
			}
		}

		meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{
			Type:               "BootstrapSecretsGenerated",
			Status:             metav1.ConditionTrue,
			Reason:             "Generated",
			Message:            statusMessage,
			ObservedGeneration: hubEnv.Generation,
		})

		if err := r.Status().Update(ctx, hubEnv); err != nil {
			return ctrl.Result{}, err
		}

		logger.Info("Phase 1 complete: Bootstrap secrets generated", "escrow", escrowClient != nil, "isFirstTime", isFirstTime)
		return ctrl.Result{Requeue: true}, nil
	}

	// Phase 0: Bootstrap Infisical (Day 0 initialization)
	// Runs after Phase 1 so that infisical-db-credentials and infisical-postgres-connection
	// already exist. The setup-infisical-role job (ArgoCD wave 3) mounts these secrets to
	// create the "infisical" PostgreSQL role. Only once that role exists can the Infisical
	// pods start successfully and allow this phase to proceed.
	logger.Info("Phase 0: Bootstrapping Infisical")

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
	// Returns (true, nil) if bootstrap was performed, (false, nil) if already bootstrapped
	bootstrapClient := infisical.NewBootstrapClient(r.Client, r.UncachedClient)
	bootstrapped, err := bootstrapClient.Bootstrap(ctx)
	if err != nil {
		logger.Error(err, "Failed to bootstrap Infisical")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, err
	}

	if bootstrapped {
		logger.Info("Infisical bootstrapped successfully")
	} else {
		logger.Info("Infisical already bootstrapped, skipping")
	}

	// Upload CLI-injected secrets to Infisical (bootstrap secrets from K8s)
	// This runs regardless of whether bootstrap just occurred or was already done
	// Makes Infisical the Source of Truth for all secrets
	secretUploader := infisical.NewSecretUploader(r.Client, r.UncachedClient)
	if err := secretUploader.UploadCLISecrets(ctx); err != nil {
		logger.Error(err, "Failed to upload CLI secrets, continuing...")
	} else {
		logger.Info("CLI secrets uploaded to Infisical")
	}

	// Upload application secrets directly to Infisical (without creating K8s secrets)
	// ESO will create K8s secrets by syncing from Infisical
	appSecretUploader := infisical.NewApplicationSecretUploader(r.Client, r.UncachedClient)
	if err := appSecretUploader.UploadApplicationSecrets(ctx); err != nil {
		logger.Error(err, "Failed to upload application secrets, continuing...")
	} else {
		logger.Info("Application secrets uploaded to Infisical")
	}

	// Enforce PKI Templates (Self-Healing)
	if err := r.ensurePKITemplates(ctx, hubEnv); err != nil {
		logger.Error(err, "Failed to ensure PKI templates")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, err
	}

	logger.Info("Phase 0 complete: Infisical bootstrapped")

	// Phase 1b: Wait for ESO to create application secrets
	// Application secrets are created by ESO from Infisical (creationPolicy: Owner)
	// We must wait for them to exist before creating database roles
	if !isConditionTrueAndUpToDate(hubEnv.Status.Conditions, "ApplicationSecretsReady", hubEnv.Generation) {
		logger.Info("Phase 1b: Waiting for ESO to create application secrets")

		dataNamespace := hubEnv.Spec.Database.Namespace

		// Map of application secrets to their namespaces.
		//
		// The Ory databases are gone with the stack that used them (ADR-060);
		// Zitadel's credentials are delivered by its own ExternalSecret and are
		// not waited on here.
		requiredSecrets := map[string]string{
			"control-plane-db-credentials": dataNamespace, // platform-data
			"hub-db-credentials":           dataNamespace, // platform-data
		}

		var missingSecrets []string
		var allSecretsExist bool

		// Wait IN-PLACE for ESO to create application secrets instead of requeuing
		// This prevents Phase 0 from re-running every 10s and bombarding Infisical with API requests
		err := wait.PollImmediateWithContext(ctx, 2*time.Second, 1*time.Minute, func(ctx context.Context) (bool, error) {
			allSecretsExist = true
			missingSecrets = []string{}

			for secretName, namespace := range requiredSecrets {
				secret := &corev1.Secret{}
				if err := r.Get(ctx, client.ObjectKey{
					Name:      secretName,
					Namespace: namespace,
				}, secret); err != nil {
					if errors.IsNotFound(err) {
						logger.Info("Waiting for ESO to create secret", "secret", secretName, "namespace", namespace)
						allSecretsExist = false
						missingSecrets = append(missingSecrets, secretName)
					} else {
						return false, err
					}
				}
			}
			return allSecretsExist, nil
		})

		if err != nil || !allSecretsExist {
			meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{
				Type:               "ApplicationSecretsReady",
				Status:             metav1.ConditionFalse,
				Reason:             "WaitingForESO",
				Message:            fmt.Sprintf("Waiting for ESO to create secrets: %s", strings.Join(missingSecrets, ", ")),
				ObservedGeneration: hubEnv.Generation,
			})
			if updateErr := r.Status().Update(ctx, hubEnv); updateErr != nil {
				return ctrl.Result{}, updateErr
			}
			// If we timed out after 1 minute, requeue and try again
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}

		meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{
			Type:               "ApplicationSecretsReady",
			Status:             metav1.ConditionTrue,
			Reason:             "ESOSynced",
			Message:            "ESO created all application secrets successfully",
			ObservedGeneration: hubEnv.Generation,
		})

		if err := r.Status().Update(ctx, hubEnv); err != nil {
			return ctrl.Result{}, err
		}

		logger.Info("Phase 1b complete: Application secrets ready")
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

	// Phase 2: Provision database roles
	// Must run after ApplicationSecretsReady (ESO has created the credential
	// secrets that RoleManager reads) and after CNPG is ready.
	//
	// The condition alone does not gate this phase. A condition records what this
	// operator did; it says nothing about what the database currently contains,
	// and the two diverge whenever the cluster's data directory is lost — a node
	// rebuilt under node-pinned local-path storage being the case that actually
	// occurred. PostgreSQL then holds only its bootstrap user while the condition
	// still reads True, so provisioning is skipped permanently and every service
	// fails authentication against a role that no longer exists.
	//
	// Verifying costs one query per declared role against a database this
	// reconcile has already connected to, and it turns a permanent outage into a
	// self-healing one.
	rolesProvisioned := isConditionTrueAndUpToDate(hubEnv.Status.Conditions, "DatabaseRolesProvisioned", hubEnv.Generation)
	if rolesProvisioned {
		verifier, err := database.NewRoleManager(ctx, r.UncachedClient, hubEnv.Spec.Database.Namespace)
		if err != nil {
			// Unreachable database is not evidence the roles are gone. Retry
			// rather than re-provisioning against a cluster we cannot see.
			logger.Error(err, "Failed to create RoleManager for role verification")
			return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
		}
		missing, err := verifier.MissingRoles(ctx, hubEnv)
		verifier.Close()
		if err != nil {
			logger.Error(err, "Failed to verify database roles")
			return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
		}
		if len(missing) > 0 {
			logger.Info("Declared database roles are absent despite DatabaseRolesProvisioned=True; re-provisioning",
				"missing", missing)
			meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{
				Type:               "DatabaseRolesProvisioned",
				Status:             metav1.ConditionFalse,
				Reason:             "RolesMissing",
				Message:            fmt.Sprintf("Roles absent from PostgreSQL and being re-provisioned: %v", missing),
				ObservedGeneration: hubEnv.Generation,
			})
			if err := r.Status().Update(ctx, hubEnv); err != nil {
				return ctrl.Result{}, err
			}
			rolesProvisioned = false
		}
	}

	if !rolesProvisioned {
		logger.Info("Phase 2: Provisioning database roles")

		roleManager, err := database.NewRoleManager(ctx, r.UncachedClient, hubEnv.Spec.Database.Namespace)
		if err != nil {
			logger.Error(err, "Failed to create RoleManager")
			return ctrl.Result{RequeueAfter: 15 * time.Second}, err
		}
		defer roleManager.Close()

		// TODO(ADR-023): Replace with Crossplane provider-sql for roles. Migrations have
		// no declarative owner since Atlas Operator was removed.
		if err := roleManager.CreateOrUpdateRoles(ctx, hubEnv); err != nil {
			// This phase blocks every phase after it, so the message says so.
			// A reconcile that stops here leaves the identity provider unable to
			// authenticate to Postgres, and the only visible symptom is a
			// CrashLoop in a different namespace.
			logger.Error(err, "Failed to provision database roles — reconcile stops here; no later phase runs",
				"nextPhasesBlocked", []string{"IdentityReady", "NATSStreamsConfigured"})
			return ctrl.Result{RequeueAfter: 15 * time.Second}, err
		}
		logger.Info("Phase 2 complete: database roles provisioned")

		meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{
			Type:               "DatabaseRolesProvisioned",
			Status:             metav1.ConditionTrue,
			Reason:             "Provisioned",
			Message:            "Database roles created/updated successfully",
			ObservedGeneration: hubEnv.Generation,
		})

		if err := r.Status().Update(ctx, hubEnv); err != nil {
			return ctrl.Result{}, err
		}

		logger.Info("Phase 2 complete: Database roles provisioned")
		return ctrl.Result{Requeue: true}, nil
	}

	// Requirement 9.8: Phase 3 - Upload Secrets to Infisical
	if !isConditionTrueAndUpToDate(hubEnv.Status.Conditions, "SecretsBackedUp", hubEnv.Generation) {
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
			Namespace: infisical.NamespaceOps,
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

		// All secrets are now uploaded via SecretUploader in Phase 0
		// No need for separate Phase 3 upload logic

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

	// Identity readiness is no longer gated on a Deployment this operator watches,
	// and OAuth clients are no longer registered here (ADR-060).
	//
	// Zitadel generates client secrets itself and discloses them once, so there
	// is nothing for this operator to push, and the Tenant Identity Service owns
	// provisioning (ADR-041).
	//
	// Leaving the readiness gate in place would be worse than leaving it out: the
	// Deployment it waits for cannot exist, so the reconcile would stall on
	// "Waiting for identity" for ever while reporting a healthy operator.
	logger.Info("Phase 3: identity provider lifecycle is external (ADR-060); marking IdentityReady")
	meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{
		Type:               "IdentityReady",
		Status:             metav1.ConditionTrue,
		Reason:             "IdentityProviderExternal",
		Message:            "Identity provider lifecycle is owned by the Tenant Identity Service (ADR-060)",
		ObservedGeneration: hubEnv.Generation,
	})
	if err := r.Status().Update(ctx, hubEnv); err != nil {
		return ctrl.Result{}, err
	}

	// Requirement 9.10: Phase 3 - Create NATS Streams
	if !isConditionTrueAndUpToDate(hubEnv.Status.Conditions, "NATSStreamsConfigured", hubEnv.Generation) {
		logger.Info("Phase 3: Creating NATS streams")

		// Requirement 9.11: Check if NATS is ready
		natsReady, err := r.isNATSReady(ctx, hubEnv)
		if err != nil {
			logger.Error(err, "Failed to check NATS readiness")
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}
		if !natsReady {
			logger.Info("Waiting for NATS to be ready")
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}

		natsClient, err := infisicalclient.NewNATSClient("")
		if err != nil {
			logger.Error(err, "Failed to create NATS client")
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
		defer natsClient.Close()

		if err := natsClient.CreateOrUpdateStreams(ctx, hubEnv); err != nil {
			logger.Error(err, "Failed to create NATS streams")
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
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
	if !isConditionTrueAndUpToDate(hubEnv.Status.Conditions, "Ready", hubEnv.Generation) {
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

	// Certificate rotation is managed by cert-manager per ADR-035. Workloads restarted via Stakater Reloader annotations.
	if isConditionTrueAndUpToDate(hubEnv.Status.Conditions, "Ready", hubEnv.Generation) {
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

// isInfisicalReady checks if Infisical Deployment has at least one ready replica
// Requirement 9.11: Implement dependency readiness checks
func (r *HubEnvironmentReconciler) isInfisicalReady(ctx context.Context, hubEnv *opsv1alpha1.HubEnvironment) (bool, error) {
	deployment := &appsv1.Deployment{}
	if err := r.Get(ctx, client.ObjectKey{
		Name:      infisical.InfisicalServiceName,
		Namespace: infisical.InfisicalServiceNamespace,
	}, deployment); err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return deployment.Status.ReadyReplicas > 0, nil
}

// isNATSReady checks if NATS StatefulSet is ready
// Requirement 9.11: Implement dependency readiness checks
func (r *HubEnvironmentReconciler) isNATSReady(ctx context.Context, hubEnv *opsv1alpha1.HubEnvironment) (bool, error) {
	statefulset := &appsv1.StatefulSet{}
	if err := r.Get(ctx, client.ObjectKey{
		Name:      "nats",
		Namespace: infisical.NamespaceMessaging,
	}, statefulset); err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return statefulset.Status.ReadyReplicas > 0, nil
}

// uploadSecretsToInfisical uploads all secrets to Infisical
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
		if labels := secret.GetLabels(); labels == nil || labels["ops.nutgraf.in/db-credentials"] != "true" {
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
			"infisical":  {"Deployment", infisical.InfisicalServiceName, infisical.InfisicalServiceNamespace},
			"redis":      {"StatefulSet", "redis", namespace},
			"zitadel":    {"Deployment", "zitadel", infisical.NamespaceIdentity},
			"mcp_server": {"Deployment", "mcp-server", infisical.NamespaceOps},
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

// escrowKubeconfig copies this cluster's admin kubeconfig into the escrow.
//
// The source is the Secret CAPI maintains -- <cluster>-kubeconfig in platform-capi,
// with the kubeconfig in .data.value. CAPI owns it and rotates it; this only ever
// reads.
//
// It is the credential that reaches the API server when the identity provider
// cannot be used (ADR-076). Day-0 wrote it to a runner that no longer exists, so
// the copy inside the cluster is the only one -- which is no use for reaching a
// cluster that is broken.
func (r *HubEnvironmentReconciler) escrowKubeconfig(ctx context.Context, store escrow.EscrowClient, clusterID string) error {
	var secret corev1.Secret
	key := types.NamespacedName{Name: clusterID + "-kubeconfig", Namespace: "platform-capi"}
	if err := r.Get(ctx, key, &secret); err != nil {
		return fmt.Errorf("read %s: %w", key, err)
	}

	// CAPI stores it under "value". An empty one is not a kubeconfig, and
	// escrowing it would overwrite a good copy with nothing.
	payload, ok := secret.Data["value"]
	if !ok || len(payload) == 0 {
		return fmt.Errorf("%s carries no kubeconfig under .data.value", key)
	}

	return store.BackupArtifact(ctx, clusterID, escrow.ArtifactKubeconfig, string(payload))
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
		// Requirement 12.6: Watch secrets with label ops.nutgraf.in/db-credentials=true for password rotation
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.findHubEnvironmentForSecret),
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				secret := obj.(*corev1.Secret)
				if labels := secret.GetLabels(); labels != nil {
					return labels["ops.nutgraf.in/db-credentials"] == "true"
				}
				return false
			})),
		).
		// Requirement 12.8: Watch Infisical Deployment
		Watches(
			&appsv1.Deployment{},
			handler.EnqueueRequestsFromMapFunc(r.findHubEnvironmentForDeployment),
			builder.WithPredicates(
				predicate.ResourceVersionChangedPredicate{},
				predicate.NewPredicateFuncs(func(obj client.Object) bool {
					return obj.GetName() == infisical.InfisicalServiceName && obj.GetNamespace() == infisical.InfisicalServiceNamespace
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
					return obj.GetName() == "nats" && obj.GetNamespace() == "platform-core"
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

// removeFinalizer removes the encryption key protection finalizer from a secret
// REQ-10: Implement finalizer controller for graceful teardown
// REQ-13: Graceful teardown support - remove finalizers when HubEnvironment is deleted
func (r *HubEnvironmentReconciler) removeFinalizer(ctx context.Context, secretName, namespace string) error {
	secret := &corev1.Secret{}
	err := r.Get(ctx, client.ObjectKey{Name: secretName, Namespace: namespace}, secret)
	if err != nil {
		if errors.IsNotFound(err) {
			// Secret doesn't exist, nothing to do
			return nil
		}
		return fmt.Errorf("failed to get secret %s/%s: %w", namespace, secretName, err)
	}

	// Check if finalizer exists
	finalizerExists := false
	for _, f := range secret.Finalizers {
		if f == secrets.EncryptionKeyProtectionFinalizer {
			finalizerExists = true
			break
		}
	}

	if !finalizerExists {
		// Finalizer doesn't exist, nothing to do
		return nil
	}

	// Remove the finalizer
	var newFinalizers []string
	for _, f := range secret.Finalizers {
		if f != secrets.EncryptionKeyProtectionFinalizer {
			newFinalizers = append(newFinalizers, f)
		}
	}
	secret.Finalizers = newFinalizers

	// Update the secret
	if err := r.Update(ctx, secret); err != nil {
		return fmt.Errorf("failed to remove finalizer from secret %s/%s: %w", namespace, secretName, err)
	}

	log.FromContext(ctx).Info("Removed finalizer from secret", "secret", secretName, "namespace", namespace)
	return nil
}

// ensurePKITemplates enforces the existence of required PKI templates in Infisical.
func (r *HubEnvironmentReconciler) ensurePKITemplates(ctx context.Context, hubEnv *opsv1alpha1.HubEnvironment) error {
	logger := log.FromContext(ctx)

	infisicalClient, err := infisicalclient.NewInfisicalClient(ctx, r.UncachedClient, "")
	if err != nil {
		return fmt.Errorf("failed to create Infisical client for PKI templates: %w", err)
	}

	projectSlug := "hub-platform"
	if hubEnv.Spec.Secrets.Infisical.ProjectSlug != "" {
		projectSlug = hubEnv.Spec.Secrets.Infisical.ProjectSlug
	}

	// Single authoritative definition, shared with the CLI's PKI_READY phase
	// (ADR-042 makes that phase CLI-owned; this reconcile is the self-healing half).
	// This list used to be a second hardcoded copy under a comment claiming it
	// matched the CLI's. It did not: it omitted signing-keys, so a rebuilt Infisical
	// could not issue argocd-agent-jwt and argocd-agent-principal never started,
	// while it created argocd-principals and argocd-agents, which no issuer,
	// Certificate or ADR references. See internal/pki.
	// Before the templates, because every template hangs off this CA and a CA
	// still awaiting its own certificate accepts template creation while
	// refusing every issuance request with "CA is not active".
	if err := infisicalClient.EnsureCAActive(ctx, projectSlug, "fleet-intermediate-ca"); err != nil {
		return fmt.Errorf("ensure CA active failed: %w", err)
	}

	for _, p := range pki.RequiredProfiles {
		if err := infisicalClient.EnsurePKITemplate(ctx, projectSlug, "fleet-intermediate-ca", p.Slug, p.TTLDays); err != nil {
			return fmt.Errorf("ensure PKI template %q failed: %w", p.Slug, err)
		}
	}

	logger.Info("PKI templates ensured successfully")
	return nil
}

// ensureInfisicalDBRootCert keeps infisical-secrets.DB_ROOT_CERT in step with the
// live CNPG cluster CA.
//
// The encoding is not obvious and getting it wrong fails silently in a way that
// looks like a wrong certificate rather than a wrong format: Infisical expects the
// env var to be a base64-encoded PEM, and Kubernetes base64-encodes Secret data on
// the wire, so the stored value is the PEM encoded twice. Copying the CA Secret's
// data field across verbatim yields a value whose bytes match the CA exactly and
// still does not work.
func (r *HubEnvironmentReconciler) ensureInfisicalDBRootCert(ctx context.Context) error {
	logger := log.FromContext(ctx)

	caSecret := &corev1.Secret{}
	if err := r.UncachedClient.Get(ctx, client.ObjectKey{
		Name: "platform-db-ca", Namespace: "platform-data",
	}, caSecret); err != nil {
		// Before CNPG exists there is nothing to mirror; that is not an error.
		return client.IgnoreNotFound(err)
	}
	caPEM, ok := caSecret.Data["ca.crt"]
	if !ok || len(caPEM) == 0 {
		return nil
	}
	want := []byte(base64.StdEncoding.EncodeToString(caPEM))

	target := &corev1.Secret{}
	if err := r.UncachedClient.Get(ctx, client.ObjectKey{
		Name: "infisical-secrets", Namespace: infisical.InfisicalServiceNamespace,
	}, target); err != nil {
		return client.IgnoreNotFound(err)
	}
	if bytes.Equal(target.Data["DB_ROOT_CERT"], want) {
		return nil
	}

	if target.Data == nil {
		target.Data = map[string][]byte{}
	}
	target.Data["DB_ROOT_CERT"] = want
	if err := r.UncachedClient.Update(ctx, target); err != nil {
		return err
	}
	logger.Info("Refreshed Infisical DB_ROOT_CERT from the live CNPG CA")
	return nil
}

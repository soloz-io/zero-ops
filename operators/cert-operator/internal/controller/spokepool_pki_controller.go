// NOTE: The Cert Operator is not yet enumerated in ADR-041's Controller
// Responsibility Matrix. Until it is added, it follows ADR-035's explicit
// delegation: Day-0 bootstrap certificate issuance for ArgoCD Agent mTLS,
// with no Day-1+ PKI operations.
package controller

import (
	"context"
	"fmt"
	"os"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/soloz-io/zero-ops/operators/cert-operator/internal/infisical"
)

const (
	crsNamespace       = "platform-capi"
	machineIdentityCRS = "%s-machine-identity"
	bootstrapCertCRS   = "%s-bootstrap-cert"
)

// +kubebuilder:rbac:groups=core,resources=secrets,verbs=create;get;list;watch;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=nutgraf.in,resources=spokepools,verbs=get;list;watch

// SpokePKIReconciler watches SpokePool provisioning and generates Day-0
// bootstrap PKI artifacts (Machine Identity + bootstrap certificate).
type SpokePKIReconciler struct {
	client.Client
	Scheme          *runtime.Scheme
	InfisicalClient *infisical.Client
	ProfileSlug     string
	OrgID           string
	ProjectID       string
}

// SetupWithManager registers the SpokePool watch.
func (r *SpokePKIReconciler) SetupWithManager(mgr ctrl.Manager) error {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "nutgraf.in",
		Version: "v1alpha1",
		Kind:    "SpokePool",
	})
	return ctrl.NewControllerManagedBy(mgr).
		For(u).
		Named("spokepool-pki").
		Complete(r)
}

// Reconcile handles SpokePool creation and generates Day-0 PKI payloads.
// Idempotent: if the bootstrap-cert-crs Secret already exists, this is a no-op.
func (r *SpokePKIReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("spokepool", req.Name)

	// 1. Idempotency check — if CRS secrets already exist, Day-0 is done
	crsSecretName := fmt.Sprintf(bootstrapCertCRS, req.Name)
	existingSecret := &corev1.Secret{}
	err := r.Get(ctx, client.ObjectKey{Name: crsSecretName, Namespace: crsNamespace}, existingSecret)
	if err == nil {
		logger.Info("Bootstrap CRS already exists, skipping")
		return ctrl.Result{}, nil
	} else if !errors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("check existing CRS: %w", err)
	}

	// 2. Resolve environment config
	profileSlug := r.ProfileSlug
	if profileSlug == "" {
		profileSlug = os.Getenv("INFISICAL_BOOTSTRAP_PROFILE_SLUG")
	}
	if profileSlug == "" {
		return ctrl.Result{}, fmt.Errorf("INFISICAL_BOOTSTRAP_PROFILE_SLUG not configured")
	}

	orgID := r.OrgID
	if orgID == "" {
		orgID = os.Getenv("INFISICAL_ORGANIZATION_ID")
	}
	if orgID == "" {
		return ctrl.Result{}, fmt.Errorf("INFISICAL_ORGANIZATION_ID not configured")
	}

	projectID := r.ProjectID
	if projectID == "" {
		projectID = os.Getenv("INFISICAL_PROJECT_ID")
	}
	if projectID == "" {
		return ctrl.Result{}, fmt.Errorf("INFISICAL_PROJECT_ID not configured")
	}

	// 3. Resolve profile slug to UUID (projectId required by Infisical OSS)
	profileId, err := r.InfisicalClient.GetProfileIdBySlug(ctx, profileSlug, projectID)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("resolve profile slug: %w", err)
	}
	logger.Info("Resolved bootstrap profile", "slug", profileSlug, "profileId", profileId)

	// 4. Create or reuse Machine Identity
	identityName := fmt.Sprintf("spoke-%s", req.Name)
	identity, err := r.InfisicalClient.GetOrCreateMachineIdentity(ctx, identityName, orgID)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("get or create machine identity: %w", err)
	}
	logger.Info("Machine identity ready", "identityId", identity.ID)

	// 5. Grant project-level PKI access
	if err := r.InfisicalClient.GrantProjectAccess(ctx, identity.ID, projectID, "admin"); err != nil {
		return ctrl.Result{}, fmt.Errorf("grant project access: %w", err)
	}
	logger.Info("Project access granted", "identityId", identity.ID, "projectId", projectID)

	// 6. Mint 72-hour bootstrap certificate
	commonName := fmt.Sprintf("argocd-agent.%s", req.Name)
	cert, err := r.InfisicalClient.IssueBootstrapCertificate(ctx, profileId, commonName)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("issue bootstrap certificate: %w", err)
	}
	logger.Info("Bootstrap certificate issued", "serial", cert.SerialNumber)

	// 7. Build and apply CRS payloads
	if err := r.applyCRSPayloads(ctx, req.Name, identity, cert); err != nil {
		return ctrl.Result{}, fmt.Errorf("apply CRS payloads: %w", err)
	}
	logger.Info("Bootstrap CRS payloads applied")

	return ctrl.Result{}, nil
}

func (r *SpokePKIReconciler) applyCRSPayloads(ctx context.Context, spokeName string, id *infisical.Identity, cert *infisical.CertData) error {
	// DEPRECATED(ADR-003): Machine Identity credentials (client-id, client-secret)
	// must not be embedded in ClusterResourceSet payloads. Secrets must be delivered
	// via Spoke ESO pulling from Infisical, not CRS. Only PKI artifacts (certificate,
	// private key, CA) qualify for CRS delivery per ADR-003 carve-out.
	//
	// Once Spoke ESO is deployed with access to Hub Infisical, this block MUST be
	// replaced with Infisical storage + Spoke ExternalSecret delivery.
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
`, id.ClientID, id.ClientSecret, id.ClientID, id.ClientSecret)

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
`, indent(cert.Certificate), indent(cert.PrivateKey), indent(cert.IssuingCaCertificate))

	// Create Machine Identity CRS
	identityCRS := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf(machineIdentityCRS, spokeName),
			Namespace: crsNamespace,
			Labels:    map[string]string{"addons.cluster.x-k8s.io/resource-set": "true"},
		},
		Type: "addons.cluster.x-k8s.io/resource-set",
		StringData: map[string]string{"identity.yaml": identityYAML},
	}
	if err := r.Create(ctx, identityCRS); err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("create identity CRS: %w", err)
	}

	// Create Bootstrap Certificate CRS
	certCRS := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf(bootstrapCertCRS, spokeName),
			Namespace: crsNamespace,
			Labels:    map[string]string{"addons.cluster.x-k8s.io/resource-set": "true"},
		},
		Type: "addons.cluster.x-k8s.io/resource-set",
		StringData: map[string]string{"bootstrap-cert.yaml": certYAML},
	}
	if err := r.Create(ctx, certCRS); err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("create cert CRS: %w", err)
	}

	// TODO(ADR-035): After CAPI ClusterResourceSet reports Applied for this Spoke,
	// delete the cert CRS Secret containing tls.key from the Hub cluster.
	// The private key must only exist on the Spoke cluster per ADR-035.
	// This requires watching ClusterResourceSet status conditions.

	return nil
}

func indent(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = "    " + line
		}
	}
	return strings.Join(lines, "\n")
}

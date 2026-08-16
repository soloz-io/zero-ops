package controller

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	identityv1alpha1 "github.com/soloz-io/zero-ops/operators/spoke-identity-operator/api/v1alpha1"
)

func newSchemeForTest(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core v1 to scheme: %v", err)
	}
	if err := identityv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add identity v1alpha1 to scheme: %v", err)
	}
	return scheme
}

func newTestSMI(name string, clientID string) *identityv1alpha1.SpokeMachineIdentity {
	return &identityv1alpha1.SpokeMachineIdentity{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "platform-capi",
		},
		Spec: identityv1alpha1.SpokeMachineIdentitySpec{
			SpokeRef: identityv1alpha1.SpokeRef{Name: name},
		},
		Status: identityv1alpha1.SpokeMachineIdentityStatus{
			IdentityID: "identity-1",
			ClientID:   clientID,
		},
	}
}

func getWrapperSecret(t *testing.T, c client.Client, name string) (*corev1.Secret, string) {
	t.Helper()
	secret := &corev1.Secret{}
	if err := c.Get(context.Background(), client.ObjectKey{Name: name, Namespace: "platform-capi"}, secret); err != nil {
		t.Fatalf("get %s wrapper: %v", name, err)
	}
	if secret.Type != "addons.cluster.x-k8s.io/resource-set" {
		t.Fatalf("expected resource-set type, got %q", secret.Type)
	}
	payload := string(secret.Data["cluster-issuer.yaml"])
	if payload == "" {
		payload = secret.StringData["cluster-issuer.yaml"]
	}
	if payload == "" {
		t.Fatalf("cluster-issuer.yaml data missing")
	}
	return secret, payload
}

// missing machine identity material -> no wrapper is ever published (fail-closed)
func TestEnsureClusterIssuerCRSWrapperEmptyIdentityNotPublished(t *testing.T) {
	scheme := newSchemeForTest(t)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &SpokeMachineIdentityReconciler{Client: c, ProjectID: "20958df6-ac81-4e11-8259-d1ddd1becb5c"}

	smi := newTestSMI("spoke-a", "")
	if err := r.ensureClusterIssuerCRSWrapper(context.Background(), smi); err != nil {
		t.Fatalf("expected nil error for deferred (empty) identity, got %v", err)
	}

	var secret corev1.Secret
	err := c.Get(context.Background(), client.ObjectKey{Name: "spoke-a-cluster-issuer", Namespace: "platform-capi"}, &secret)
	if err == nil {
		t.Fatalf("wrapper must NOT be published when clientId is empty, got secret %q", secret.Name)
	}
}

// empty projectId -> no wrapper is published
func TestEnsureClusterIssuerCRSWrapperEmptyProjectNotPublished(t *testing.T) {
	scheme := newSchemeForTest(t)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &SpokeMachineIdentityReconciler{Client: c} // ProjectID empty

	smi := newTestSMI("spoke-b", "b3d5ec56-623e-4a04-b82e-3960d84196c7")
	if err := r.ensureClusterIssuerCRSWrapper(context.Background(), smi); err != nil {
		t.Fatalf("expected nil error for deferred (empty) project, got %v", err)
	}

	var secret corev1.Secret
	err := c.Get(context.Background(), client.ObjectKey{Name: "spoke-b-cluster-issuer", Namespace: "platform-capi"}, &secret)
	if err == nil {
		t.Fatalf("wrapper must NOT be published when projectId is empty, got secret %q", secret.Name)
	}
}

// initial identity + project -> correct clientId and projectId rendered
func TestEnsureClusterIssuerCRSWrapperInitialRendersCompletedIssuer(t *testing.T) {
	scheme := newSchemeForTest(t)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &SpokeMachineIdentityReconciler{Client: c, ProjectID: "20958df6-ac81-4e11-8259-d1ddd1becb5c"}

	smi := newTestSMI("spoke-c", "b3d5ec56-623e-4a04-b82e-3960d84196c7")
	if err := r.ensureClusterIssuerCRSWrapper(context.Background(), smi); err != nil {
		t.Fatalf("render: %v", err)
	}

	_, yamlBody := getWrapperSecret(t, c, "spoke-c-cluster-issuer")
	for _, want := range []string{
		"kind: ClusterIssuer",
		"name: infisical-fleet-issuer",
		"clientId: b3d5ec56-623e-4a04-b82e-3960d84196c7",
		"projectId: 20958df6-ac81-4e11-8259-d1ddd1becb5c",
		"name: infisical-auth",
		"key: client-secret",
		"certificateTemplateName: infrastructure-services",
		"url: https://infisical.nutgraf.in",
	} {
		if !strings.Contains(yamlBody, want) {
			t.Errorf("rendered ClusterIssuer missing %q in:\n%s", want, yamlBody)
		}
	}
}

// spec-level projectId overrides the operator flag projectId
func TestEnsureClusterIssuerCRSWrapperSpecProjectIDOverrides(t *testing.T) {
	scheme := newSchemeForTest(t)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &SpokeMachineIdentityReconciler{Client: c, ProjectID: "flag-project-id"}

	smi := newTestSMI("spoke-d", "b3d5ec56-623e-4a04-b82e-3960d84196c7")
	smi.Spec.Infisical.ProjectID = "spec-project-id"

	if err := r.ensureClusterIssuerCRSWrapper(context.Background(), smi); err != nil {
		t.Fatalf("render: %v", err)
	}

	_, yamlBody := getWrapperSecret(t, c, "spoke-d-cluster-issuer")
	if !strings.Contains(yamlBody, "projectId: spec-project-id") {
		t.Errorf("expected spec projectId to win, got:\n%s", yamlBody)
	}
	if strings.Contains(yamlBody, "projectId: flag-project-id") {
		t.Errorf("flag projectId must not be used when spec overrides:\n%s", yamlBody)
	}
}

// reconciliation with same values -> no unnecessary update (idempotent)
func TestEnsureClusterIssuerCRSWrapperIdempotentNoUpdate(t *testing.T) {
	scheme := newSchemeForTest(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&identityv1alpha1.SpokeMachineIdentity{}).Build()
	r := &SpokeMachineIdentityReconciler{Client: c, ProjectID: "20958df6-ac81-4e11-8259-d1ddd1becb5c"}

	smi := newTestSMI("spoke-e", "b3d5ec56-623e-4a04-b82e-3960d84196c7")
	if err := r.ensureClusterIssuerCRSWrapper(context.Background(), smi); err != nil {
		t.Fatalf("first render: %v", err)
	}
	before, _ := getWrapperSecret(t, c, "spoke-e-cluster-issuer")

	// Second reconcile with identical material. The wrapper content is unchanged, so
	// no update should be issued. Assert via resourceVersion staying identical.
	if err := r.ensureClusterIssuerCRSWrapper(context.Background(), smi); err != nil {
		t.Fatalf("second render: %v", err)
	}
	after, _ := getWrapperSecret(t, c, "spoke-e-cluster-issuer")
	if before.ResourceVersion != after.ResourceVersion {
		t.Errorf("wrapper was updated unnecessarily: rv %s -> %s", before.ResourceVersion, after.ResourceVersion)
	}
}

// secret rotation keeps the same clientId (rotation-stable): wrapper must NOT be
// regenerated and the rendered clientId must remain stable across rotation.
func TestEnsureClusterIssuerCRSWrapperClientSecretRotationNoRegen(t *testing.T) {
	scheme := newSchemeForTest(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&identityv1alpha1.SpokeMachineIdentity{}).Build()
	r := &SpokeMachineIdentityReconciler{Client: c, ProjectID: "20958df6-ac81-4e11-8259-d1ddd1becb5c"}

	stableClientID := "b3d5ec56-623e-4a04-b82e-3960d84196c7"
	smi := newTestSMI("spoke-f", stableClientID)
	if err := r.ensureClusterIssuerCRSWrapper(context.Background(), smi); err != nil {
		t.Fatalf("first render: %v", err)
	}
	before, yamlBefore := getWrapperSecret(t, c, "spoke-f-cluster-issuer")

	// Rotation rotates only the client secret; the clientId is stable. Reconcile again.
	if err := r.ensureClusterIssuerCRSWrapper(context.Background(), smi); err != nil {
		t.Fatalf("re-render after rotation: %v", err)
	}
	after, yamlAfter := getWrapperSecret(t, c, "spoke-f-cluster-issuer")

	if before.ResourceVersion != after.ResourceVersion {
		t.Errorf("rotation must not regenerate the wrapper (rv %s -> %s)", before.ResourceVersion, after.ResourceVersion)
	}
	if yamlBefore != yamlAfter {
		t.Errorf("rotation must not change the rendered ClusterIssuer payload")
	}
	if !strings.Contains(yamlAfter, "clientId: "+stableClientID) {
		t.Errorf("rotation-stable clientId missing from payload:\n%s", yamlAfter)
	}
}

// clientId actually changes (identity recreated): wrapper updates so future
// (re)provisioned spokes receive the new identifier.
func TestEnsureClusterIssuerCRSWrapperClientIDChangesUpdatesWrapper(t *testing.T) {
	scheme := newSchemeForTest(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&identityv1alpha1.SpokeMachineIdentity{}).Build()
	r := &SpokeMachineIdentityReconciler{Client: c, ProjectID: "20958df6-ac81-4e11-8259-d1ddd1becb5c"}

	smi := newTestSMI("spoke-g", "old-client-id-00000000-0000-0000-0000-000000000000")
	if err := r.ensureClusterIssuerCRSWrapper(context.Background(), smi); err != nil {
		t.Fatalf("first render: %v", err)
	}

	smi.Status.ClientID = "new-client-id-11111111-1111-1111-1111-111111111111"
	if err := r.ensureClusterIssuerCRSWrapper(context.Background(), smi); err != nil {
		t.Fatalf("re-render after clientId change: %v", err)
	}

	_, yamlBody := getWrapperSecret(t, c, "spoke-g-cluster-issuer")
	if !strings.Contains(yamlBody, "clientId: new-client-id-11111111-1111-1111-1111-111111111111") {
		t.Errorf("updated clientId not present in wrapper payload:\n%s", yamlBody)
	}
	if strings.Contains(yamlBody, "old-client-id") {
		t.Errorf("stale clientId still present in wrapper payload:\n%s", yamlBody)
	}
}

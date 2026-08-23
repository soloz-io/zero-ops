package controller

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"
)

// Regression coverage for ADR-046 §24.
//
// The defect these tests exist for: the shared ClusterClass deletes kube-proxy at
// `kubeadm init`, and Cilium — the intended kube-proxy replacement — was given no
// `k8s-service-host`. It therefore looked for the API server at the in-cluster
// ClusterIP that kube-proxy used to route and that Cilium had not yet programmed.
// Every fresh spoke deadlocked: no CNI, node NotReady, every workload Pending.
//
// The failure was invisible for weeks because the fix that introduced it was
// validated on a RUNNING spoke, where Cilium was already up. Only a cold boot
// reproduces it, so these tests exercise the cold-boot ordering explicitly.

const (
	testSpokeName = "spoke-pool-hybrid-dev-01"
	testNamespace = "platform-capi"
)

func ciliumTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("add corev1 to scheme: %v", err)
	}
	for _, gvk := range []schema.GroupVersionKind{
		{Group: "cluster.x-k8s.io", Version: "v1beta1", Kind: "Cluster"},
		{Group: "nutgraf.in", Version: "v1alpha1", Kind: "SpokePool"},
	} {
		s.AddKnownTypeWithName(gvk, &unstructured.Unstructured{})
		listGVK := gvk
		listGVK.Kind += "List"
		s.AddKnownTypeWithName(listGVK, &unstructured.UnstructuredList{})
	}
	return s
}

func newSpokePool(provider string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(schema.GroupVersionKind{Group: "nutgraf.in", Version: "v1alpha1", Kind: "SpokePool"})
	u.SetName(testSpokeName)
	u.SetUID("spokepool-uid-1")
	if provider != "" {
		_ = unstructured.SetNestedField(u.Object, provider, "spec", "provider")
	}
	return u
}

// newCAPICluster builds a CAPI Cluster. host=="" models the window before CAPH has
// created the load balancer, which is exactly when the endpoint is unknown.
func newCAPICluster(host string, port int64) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(schema.GroupVersionKind{Group: "cluster.x-k8s.io", Version: "v1beta1", Kind: "Cluster"})
	u.SetName(testSpokeName)
	u.SetNamespace(testNamespace)
	if host != "" {
		_ = unstructured.SetNestedField(u.Object, host, "spec", "controlPlaneEndpoint", "host")
		_ = unstructured.SetNestedField(u.Object, port, "spec", "controlPlaneEndpoint", "port")
	}
	return u
}

func newCiliumBase(provider string, data map[string]string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: ciliumConfigBaseName(provider), Namespace: testNamespace},
		Data:       data,
	}
}

func newReconciler(t *testing.T, objs ...client.Object) *SpokePoolReconciler {
	t.Helper()
	s := ciliumTestScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).Build()
	return &SpokePoolReconciler{Client: c, UncachedClient: c}
}

func getWrapper(t *testing.T, r *SpokePoolReconciler) (*corev1.Secret, bool) {
	t.Helper()
	sec := &corev1.Secret{}
	err := r.Get(context.Background(), client.ObjectKey{Name: testSpokeName + "-cilium-config", Namespace: testNamespace}, sec)
	if apierrors.IsNotFound(err) {
		return nil, false
	}
	if err != nil {
		t.Fatalf("get wrapper: %v", err)
	}
	return sec, true
}

// decodeWrapper parses the ConfigMap the spoke would actually receive.
func decodeWrapper(t *testing.T, sec *corev1.Secret) *corev1.ConfigMap {
	t.Helper()
	payload := sec.StringData["cilium-config.yaml"]
	if payload == "" {
		payload = string(sec.Data["cilium-config.yaml"])
	}
	if payload == "" {
		t.Fatal("wrapper carries no cilium-config.yaml payload")
	}
	cm := &corev1.ConfigMap{}
	if err := yaml.Unmarshal([]byte(payload), cm); err != nil {
		t.Fatalf("wrapper payload is not a valid ConfigMap: %v", err)
	}
	return cm
}

// The endpoint is the sole source for the API address, and both keys must be set.
func TestCiliumConfigWrapper_RendersEndpointFromControlPlaneEndpoint(t *testing.T) {
	sp := newSpokePool("hybrid")
	r := newReconciler(t, sp,
		newCAPICluster("65.109.41.89", 6443),
		newCiliumBase("hybrid", map[string]string{
			"kube-proxy-replacement": "true",
			"routing-mode":           "tunnel",
			"mtu":                    "1200",
		}),
	)

	if err := r.ensureCiliumConfigCRSWrapper(context.Background(), sp); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sec, ok := getWrapper(t, r)
	if !ok {
		t.Fatal("expected a cilium-config CRS wrapper, got none")
	}
	if sec.Type != "addons.cluster.x-k8s.io/resource-set" {
		t.Errorf("wrapper type = %q, want addons.cluster.x-k8s.io/resource-set", sec.Type)
	}

	cm := decodeWrapper(t, sec)
	if got := cm.Data["k8s-service-host"]; got != "65.109.41.89" {
		t.Errorf("k8s-service-host = %q, want 65.109.41.89", got)
	}
	if got := cm.Data["k8s-service-port"]; got != "6443" {
		t.Errorf("k8s-service-port = %q, want 6443", got)
	}
	// The static half must survive untouched — the renderer contributes two keys
	// and owns nothing else.
	if got := cm.Data["routing-mode"]; got != "tunnel" {
		t.Errorf("base key routing-mode = %q, want tunnel (base data must pass through)", got)
	}
	if cm.Name != "cilium-config" || cm.Namespace != "kube-system" {
		t.Errorf("rendered ConfigMap = %s/%s, want kube-system/cilium-config", cm.Namespace, cm.Name)
	}
}

// Fail-closed. A wrapper carrying an empty host would be applied by CRS and
// reproduce the very deadlock this renderer exists to prevent.
func TestCiliumConfigWrapper_FailsClosedWithoutEndpoint(t *testing.T) {
	for _, tc := range []struct {
		name string
		host string
		port int64
	}{
		{"no endpoint at all", "", 0},
		{"host set, port zero", "65.109.41.89", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sp := newSpokePool("hybrid")
			r := newReconciler(t, sp,
				newCAPICluster(tc.host, tc.port),
				newCiliumBase("hybrid", map[string]string{"kube-proxy-replacement": "true"}),
			)

			if err := r.ensureCiliumConfigCRSWrapper(context.Background(), sp); err != nil {
				t.Fatalf("deferral must not be an error, got: %v", err)
			}
			if _, ok := getWrapper(t, r); ok {
				t.Fatal("wrapper was emitted without a usable control-plane endpoint — a spoke would receive a half-rendered CNI config")
			}
		})
	}
}

// The CAPI Cluster not existing yet is a deferral, not a fault.
func TestCiliumConfigWrapper_DefersBeforeClusterExists(t *testing.T) {
	sp := newSpokePool("hybrid")
	r := newReconciler(t, sp, newCiliumBase("hybrid", map[string]string{"kube-proxy-replacement": "true"}))

	if err := r.ensureCiliumConfigCRSWrapper(context.Background(), sp); err != nil {
		t.Fatalf("missing CAPI Cluster must defer, not error: %v", err)
	}
	if _, ok := getWrapper(t, r); ok {
		t.Fatal("wrapper emitted before the CAPI Cluster existed")
	}
}

// A missing or empty base is a real fault: the settings are supposed to be in Git,
// and rendering a CNI config without them would produce a cluster nobody specified.
func TestCiliumConfigWrapper_ErrorsOnUnusableBase(t *testing.T) {
	for _, tc := range []struct {
		name string
		objs []client.Object
	}{
		{"base absent", nil},
		{"base empty", []client.Object{newCiliumBase("hybrid", map[string]string{})}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sp := newSpokePool("hybrid")
			objs := append([]client.Object{sp, newCAPICluster("65.109.41.89", 6443)}, tc.objs...)
			r := newReconciler(t, objs...)

			err := r.ensureCiliumConfigCRSWrapper(context.Background(), sp)
			if err == nil {
				t.Fatal("expected an error for an unusable cilium-config base")
			}
			if _, ok := getWrapper(t, r); ok {
				t.Fatal("wrapper emitted despite an unusable base")
			}
		})
	}
}

// The wrapper is SpokePool-owned so teardown garbage-collects it (ADR-035 invariant 3).
func TestCiliumConfigWrapper_OwnedBySpokePool(t *testing.T) {
	sp := newSpokePool("hybrid")
	r := newReconciler(t, sp,
		newCAPICluster("65.109.41.89", 6443),
		newCiliumBase("hybrid", map[string]string{"kube-proxy-replacement": "true"}),
	)
	if err := r.ensureCiliumConfigCRSWrapper(context.Background(), sp); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sec, ok := getWrapper(t, r)
	if !ok {
		t.Fatal("no wrapper")
	}
	refs := sec.GetOwnerReferences()
	if len(refs) != 1 || refs[0].Kind != "SpokePool" || refs[0].Name != testSpokeName {
		t.Fatalf("ownerReferences = %+v, want a single SpokePool ref", refs)
	}
}

// Unlike the bootstrap certificate the wrapper is mutable: if CAPH rebuilds the load
// balancer the endpoint changes, and an immutable wrapper would pin the spoke to a
// dead address.
func TestCiliumConfigWrapper_UpdatesWhenEndpointChanges(t *testing.T) {
	sp := newSpokePool("hybrid")
	cluster := newCAPICluster("65.109.41.89", 6443)
	r := newReconciler(t, sp, cluster, newCiliumBase("hybrid", map[string]string{"kube-proxy-replacement": "true"}))

	ctx := context.Background()
	if err := r.ensureCiliumConfigCRSWrapper(ctx, sp); err != nil {
		t.Fatalf("first render: %v", err)
	}

	_ = unstructured.SetNestedField(cluster.Object, "203.0.113.10", "spec", "controlPlaneEndpoint", "host")
	if err := r.Update(ctx, cluster); err != nil {
		t.Fatalf("update cluster: %v", err)
	}
	if err := r.ensureCiliumConfigCRSWrapper(ctx, sp); err != nil {
		t.Fatalf("second render: %v", err)
	}

	sec, ok := getWrapper(t, r)
	if !ok {
		t.Fatal("no wrapper")
	}
	if got := decodeWrapper(t, sec).Data["k8s-service-host"]; got != "203.0.113.10" {
		t.Errorf("k8s-service-host = %q, want the new endpoint 203.0.113.10", got)
	}
}

// Provider selects the base, so a hetzner spoke never receives hybrid's datapath.
func TestCiliumConfigWrapper_SelectsBaseByProvider(t *testing.T) {
	sp := newSpokePool("hetzner")
	r := newReconciler(t, sp,
		newCAPICluster("65.109.41.89", 6443),
		newCiliumBase("hetzner", map[string]string{"routing-mode": "native"}),
		newCiliumBase("hybrid", map[string]string{"routing-mode": "tunnel"}),
	)
	if err := r.ensureCiliumConfigCRSWrapper(context.Background(), sp); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sec, _ := getWrapper(t, r)
	if got := decodeWrapper(t, sec).Data["routing-mode"]; got != "native" {
		t.Errorf("routing-mode = %q, want native — the hetzner base must be selected", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// The ordering invariant the whole cold-boot fix rests on.
//
// CAPH populates controlPlaneEndpoint when it creates the load balancer, which
// happens BEFORE any machine is provisioned. The renderer must therefore depend on
// nothing that only exists once a node has booted — otherwise the config Cilium
// needs to start would arrive only after Cilium had already failed to start.
//
// Observed on spoke-pool-hybrid-dev-01 on 2026-08-23: the HetznerCluster sat at
// LoadBalancerCreateFailed with an empty endpoint and NO machine was created; the
// control-plane Machine appeared only after the load balancer existed.
// ─────────────────────────────────────────────────────────────────────────────
func TestCiliumConfigWrapper_EndpointPrecedesNodeBoot(t *testing.T) {
	sp := newSpokePool("hybrid")
	// A cluster with an endpoint and absolutely nothing else: no Machines, no
	// Nodes, no kubeconfig Secret, no spoke API to talk to.
	r := newReconciler(t, sp,
		newCAPICluster("65.109.41.89", 6443),
		newCiliumBase("hybrid", map[string]string{"kube-proxy-replacement": "true"}),
	)

	if err := r.ensureCiliumConfigCRSWrapper(context.Background(), sp); err != nil {
		t.Fatalf("renderer must work before any node exists: %v", err)
	}
	sec, ok := getWrapper(t, r)
	if !ok {
		t.Fatal("wrapper must be renderable from the endpoint alone, before the first node boots")
	}
	if got := decodeWrapper(t, sec).Data["k8s-service-host"]; got == "" {
		t.Fatal("endpoint not rendered")
	}
}

// The two halves must not both define the same key: that is the split-brain the
// single-owner rule exists to prevent.
func TestCiliumConfigWrapper_BaseMustNotCarryEndpointKeys(t *testing.T) {
	sp := newSpokePool("hybrid")
	// A base that wrongly pins the endpoint. The renderer must win, because the
	// base cannot know a per-spoke address and a stale one is worse than none.
	r := newReconciler(t, sp,
		newCAPICluster("65.109.41.89", 6443),
		newCiliumBase("hybrid", map[string]string{
			"k8s-service-host": "10.96.0.1",
			"k8s-service-port": "443",
		}),
	)
	if err := r.ensureCiliumConfigCRSWrapper(context.Background(), sp); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sec, _ := getWrapper(t, r)
	if got := decodeWrapper(t, sec).Data["k8s-service-host"]; got != "65.109.41.89" {
		t.Errorf("k8s-service-host = %q — the rendered endpoint must override any value in the base", got)
	}
}

// Guards the wire format: CRS applies each payload value as a manifest, so it has
// to be a single applyable document.
func TestCiliumConfigWrapper_PayloadIsASingleApplyableManifest(t *testing.T) {
	sp := newSpokePool("hybrid")
	r := newReconciler(t, sp,
		newCAPICluster("65.109.41.89", 6443),
		newCiliumBase("hybrid", map[string]string{"kube-proxy-replacement": "true"}),
	)
	if err := r.ensureCiliumConfigCRSWrapper(context.Background(), sp); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sec, _ := getWrapper(t, r)
	payload := sec.StringData["cilium-config.yaml"]
	if strings.Contains(payload, "\n---\n") {
		t.Error("payload contains a document separator; CRS expects one manifest per key")
	}
	cm := decodeWrapper(t, sec)
	if cm.APIVersion != "v1" || cm.Kind != "ConfigMap" {
		t.Errorf("payload apiVersion/kind = %s/%s, want v1/ConfigMap — CRS cannot apply a typeless object", cm.APIVersion, cm.Kind)
	}
}

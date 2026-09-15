package controller

import (
	"os"
	"strings"
	"testing"
)

// The address is a host, never host:port -- external-dns publishes an A record.
func TestControlPlaneHostIsExtractedWithoutThePort(t *testing.T) {
	cases := []struct {
		name, config, want string
		wantErr            bool
	}{
		{
			name:   "the real thing, as kubeadm writes it",
			config: "apiVersion: kubeadm.k8s.io/v1beta3\nclusterName: acme-hub\ncontrolPlaneEndpoint: 65.109.41.89:6443\nkind: ClusterConfiguration\n",
			want:   "65.109.41.89",
		},
		{name: "a hostname endpoint", config: "controlPlaneEndpoint: hub.example.test:6443", want: "hub.example.test"},
		{name: "quoted", config: `controlPlaneEndpoint: "65.109.41.89:6443"`, want: "65.109.41.89"},
		{name: "indented under a parent", config: "spec:\n  controlPlaneEndpoint: 10.0.0.5:6443\n", want: "10.0.0.5"},
		{name: "no port", config: "controlPlaneEndpoint: 65.109.41.89", want: "65.109.41.89"},
		// IPv6 is bracketed: the last colon is inside the literal, so naive
		// splitting would return "[2001:db8::1" and publish a broken record.
		{name: "IPv6", config: "controlPlaneEndpoint: [2001:db8::1]:6443", want: "2001:db8::1"},

		{name: "absent", config: "clusterName: acme-hub\n", wantErr: true},
		{name: "empty value", config: "controlPlaneEndpoint:\n", wantErr: true},
		{name: "empty config", config: "", wantErr: true},
		{name: "unterminated IPv6", config: "controlPlaneEndpoint: [2001:db8::1:6443", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := controlPlaneHost(tc.config)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// A near-miss key must not be mistaken for the endpoint.
func TestOnlyTheControlPlaneEndpointFieldIsRead(t *testing.T) {
	got, err := controlPlaneHost("localAPIEndpoint:\n  advertiseAddress: 10.0.0.9\n")
	if err == nil {
		t.Errorf("localAPIEndpoint was read as the control-plane endpoint: %q", got)
	}
}

// The ConfigMap must be created in a real namespace.
//
// HubEnvironment is cluster-scoped (+kubebuilder:resource:scope=Cluster), so
// ctrl.Request.Namespace is ALWAYS empty for it. Passing that through produced:
//
//	Failed to publish the hub ingress address; continuing
//	  error: an empty namespace may not be set during creation
//
// on every reconcile -- the operator ran, resolved nothing, logged, and carried
// on, so the bundle upgrade landed and the address still never appeared.
func TestTheConfigMapNamespaceIsNotTheRequestNamespace(t *testing.T) {
	src, err := os.ReadFile("hubenvironment_controller.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)

	if !strings.Contains(body, "r.reconcileIngressAddress(ctx, hubEnv)") {
		t.Fatal("reconcileIngressAddress is not called, or still takes a namespace argument; " +
			"the namespace is a property of the CONSUMER and belongs beside it")
	}
	if strings.Contains(body, "reconcileIngressAddress(ctx, hubEnv, req.Namespace)") {
		t.Error("the ConfigMap namespace comes from req.Namespace, which is empty for a " +
			"cluster-scoped resource; creation fails on every reconcile")
	}
}

// The ConfigMap must be written where its consumer reads it.
//
// configMapKeyRef resolves in the POD's namespace. external-dns runs in
// platform-edge; the first fix put the ConfigMap in platform-ops -- correct for
// a cluster-scoped owner, invisible to the reader. With optional: true on the
// reference nothing failed: the container started, the variable was unset,
// external-dns logged DefaultTargets:[] and published nothing, and every hub
// hostname stayed NXDOMAIN.
//
// This asserts the operator and the manifest agree on one namespace, because
// they disagreed twice.
func TestTheConfigMapIsWrittenWhereExternalDNSReadsIt(t *testing.T) {
	src, err := os.ReadFile("ingress_address.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), `IngressAddressNamespace = "platform-edge"`) {
		t.Fatal("the operator does not target platform-edge, where external-dns runs")
	}

	manifest, err := os.ReadFile("../../../../manifests/hub-core-services/external-dns/external-dns.yaml")
	if err != nil {
		t.Skipf("external-dns manifest not readable from here: %v", err)
	}
	m := string(manifest)
	if !strings.Contains(m, "namespace: platform-edge") {
		t.Error("external-dns is no longer in platform-edge; the operator still writes there")
	}
	at := strings.Index(m, "name: hub-ingress")
	if at < 0 {
		t.Fatal("external-dns no longer references the hub-ingress ConfigMap")
	}
	ref := m[at:min(at+200, len(m))]
	if strings.Contains(ref, "optional: true") {
		t.Error("the hub-ingress reference is optional, so a missing address starts " +
			"external-dns with no target and publishes nothing, silently")
	}
}

// The condition must be persisted where it is set, not left to a later phase.
//
// reconcileIngressAddress runs near the top of Reconcile, and three phases
// between it and the first Status().Update() can return early. On those paths
// the condition was computed and dropped -- so the signal saying "no hub
// hostname can be published" was lost precisely when something else had already
// gone wrong, which is when it matters most.
func TestTheIngressConditionIsPersistedWhereItIsSet(t *testing.T) {
	src, err := os.ReadFile("ingress_address.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	fn := body[strings.Index(body, "func (r *HubEnvironmentReconciler) reconcileIngressAddress"):]
	fn = fn[:strings.Index(fn, "\nfunc ")]

	// Strip comments: the next function's doc block falls inside this slice and
	// names the very symbol being counted.
	var code strings.Builder
	for _, line := range strings.Split(fn, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		code.WriteString(line)
		code.WriteString("\n")
	}
	fn = code.String()

	// Both outcomes -- resolved and unresolved -- must write.
	if strings.Count(fn, "persistIngressCondition") != 2 {
		t.Errorf("expected both the resolved and unresolved paths to persist the condition, found %d call(s)",
			strings.Count(fn, "persistIngressCondition"))
	}
	for _, want := range []string{"ConditionTrue", "ConditionFalse"} {
		if !strings.Contains(fn, want) {
			t.Errorf("the %s outcome is not reported as a condition", want)
		}
	}

	// And the writer must not abort the reconcile on failure.
	w := body[strings.Index(body, "func (r *HubEnvironmentReconciler) persistIngressCondition"):]
	w = w[:strings.Index(w, "\n}")+2]
	if strings.Contains(w, "return err") {
		t.Error("a condition that cannot be recorded aborts the reconcile that would fix it")
	}
}

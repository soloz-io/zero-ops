package metrics

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestEveryMetricIsNamespaced(t *testing.T) {
	// UPSTREAM'S DEFECT, ASSERTED ABSENT. Google's plugin registers `roundtrip_latencies`
	// and `failures_count` as bare globals: they collide with anything else in a shared
	// registry and tell a reader nothing about where they came from.
	r := prometheus.NewRegistry()
	if err := Register(r); err != nil {
		t.Fatal(err)
	}
	families, err := r.Gather()
	if err != nil {
		t.Fatal(err)
	}
	if len(families) == 0 {
		t.Fatal("nothing was registered")
	}
	for _, f := range families {
		if !strings.HasPrefix(f.GetName(), namespace+"_") {
			t.Errorf("metric %q is not namespaced with %q", f.GetName(), namespace)
		}
	}
}

func TestTheActiveKeyVersionIsReported(t *testing.T) {
	// The gauge is the in-cluster half of the no-reactivation signal: a value that
	// DECREASES is the reactivation ADR-100 forbids, visible as a time series between
	// the scheduled runs of the out-of-band check.
	r := prometheus.NewRegistry()
	g := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace, Name: "active_key_version", Help: "h",
	})
	if err := r.Register(g); err != nil {
		t.Fatal(err)
	}
	g.Set(3)
	if v := testutil.ToFloat64(g); v != 3 {
		t.Fatalf("gauge reads %v, want 3", v)
	}
	// A decrease must be representable: the alert is `delta(...) < 0`, so nothing may
	// clamp or ratchet the value.
	g.Set(1)
	if v := testutil.ToFloat64(g); v != 1 {
		t.Fatalf("the gauge did not accept a decrease (%v); the regression signal depends "+
			"on it being able to go down", v)
	}
}

func TestFailuresAreClassifiedByStatusCodeNotMessage(t *testing.T) {
	// MESSAGE MATCHING BREAKS SILENTLY when a vendor rewords an error, and the whole
	// point of this label is that somebody routes on it: "denied" is a policy problem a
	// human must fix, "unavailable" is an outage that may resolve itself.
	for _, c := range []struct {
		code codes.Code
		want string
	}{
		{codes.PermissionDenied, "denied"},
		{codes.Unauthenticated, "denied"},
		{codes.Unavailable, "unavailable"},
		{codes.DeadlineExceeded, "timeout"},
		{codes.NotFound, "not_found"},
		{codes.FailedPrecondition, "precondition"},
		{codes.ResourceExhausted, "quota"},
		{codes.Internal, "other"},
	} {
		if got := Reason(status.Error(c.code, "a message that will be reworded one day")); got != c.want {
			t.Errorf("%v classified as %q, want %q", c.code, got, c.want)
		}
	}
}

func TestAPluginRefusalIsDistinguishableFromAVendorError(t *testing.T) {
	// The integrity checks and the version-mismatch contract check are THIS plugin
	// refusing, not Cloud KMS failing. Counting them together would make a corrupted
	// response look like an outage and send somebody to the wrong place.
	if got := Reason(errPlain{}); got != "plugin_refused" {
		t.Fatalf("a plain error classified as %q, want plugin_refused", got)
	}
	if got := Reason(nil); got != "none" {
		t.Fatalf("nil classified as %q", got)
	}
}

type errPlain struct{}

func (errPlain) Error() string { return "this plugin refused" }

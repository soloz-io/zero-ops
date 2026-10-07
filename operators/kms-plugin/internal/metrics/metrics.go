// Package metrics is what this plugin reports about itself.
//
// WHY IT EXISTS, AND WHY IT IS LATE. ADR-100's condition 8 asks for an SLO, an error
// budget and measured quotas, and none of them can be computed from a component that
// exports nothing. The scheduled CI carrier answers "has state drifted" by reading Cloud
// KMS from outside; it cannot answer "how slow is the wrap path" or "how often does it
// fail", because those are properties only this process observes. Treating condition 8 as
// purely an alerting problem missed that half of it is an instrumentation problem.
//
// The shape follows Google's plugin (plugin/metrics.go: a latency histogram and a failure
// counter, both by operation type), with two differences:
//
//   - NAMES ARE NAMESPACED. Upstream registers `roundtrip_latencies` and `failures_count`
//     as bare globals, which collide with anything else in a shared registry and tell a
//     reader nothing about their source.
//   - THE ACTIVE KEY VERSION IS A GAUGE. That is not in upstream and is the useful part:
//     a version that goes DOWN is the reactivation ADR-100 forbids, so the invariant
//     becomes visible as a time series in-cluster rather than only in the out-of-band
//     check. Two independent signals for one invariant, from different vantage points.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const namespace = "soloz_kms_plugin"

var (
	// Duration is how long a key-authority call took.
	//
	// Buckets run from 5ms to roughly 40s: Cloud KMS round trips from a Hetzner node
	// cross a continent, and the per-call timeout is 10s, so the interesting range is
	// tens of milliseconds to the timeout. A default bucket set topping out at 10s would
	// put every timeout in +Inf and lose exactly the tail that matters.
	Duration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "operation_duration_seconds",
		Help:      "Latency of Cloud KMS operations, by operation and result.",
		Buckets:   prometheus.ExponentialBuckets(0.005, 2, 14),
	}, []string{"operation", "result"})

	// Failures counts what went wrong, classified by gRPC status rather than by message.
	//
	// The classification matters for routing: PERMISSION_DENIED is a policy problem a
	// human must fix, UNAVAILABLE is an outage that may resolve itself, and an integrity
	// failure is neither -- it means a response was corrupted in transit and nothing was
	// stored.
	Failures = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "operation_failures_total",
		Help:      "Failed Cloud KMS operations, by operation and reason.",
	}, []string{"operation", "reason"})

	// ActiveKeyVersion is the CryptoKeyVersion number this plugin is reporting.
	//
	// A GAUGE THAT MUST ONLY EVER RISE. ADR-100 forbids reactivating a retired key
	// version, and this process cannot enforce that -- its memory does not survive a
	// restart, which is why that guard was removed. It can REPORT it, and a gauge that
	// decreases is an alert expression anyone can write:
	//
	//	delta(soloz_kms_plugin_active_key_version[1h]) < 0
	//
	// That is a second, in-cluster signal for the invariant the Git version floor holds
	// out-of-band. Neither is sufficient alone: this one is blind across a restart, and
	// the floor is blind between scheduled runs.
	ActiveKeyVersion = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "active_key_version",
		Help:      "CryptoKeyVersion number currently reported to the API server. Must never decrease.",
	})

	// CertificateRemaining is how much of the plugin's client certificate life is left,
	// as a fraction from 1 down to 0.
	//
	// ADR-100 recorded this as an open gap: nothing surfaced the certificate's remaining
	// lifetime, so a pod that happened not to restart would expire into a failed cold
	// read with no warning. kubeadm renews only its own hardcoded list and will never
	// touch this file, and renewal here happens on pod start — so a long-lived pod is
	// exactly the case that needs watching.
	//
	//	soloz_kms_plugin_certificate_remaining_fraction < 0.2
	//
	// is the alert, and it fires with days of margin rather than at the failure.
	CertificateRemaining = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "certificate_remaining_fraction",
		Help:      "Fraction of the plugin client certificate's lifetime remaining, 1 to 0.",
	})

	// Healthy is what Status last reported, as 1 or 0.
	//
	// The API server already knows this -- it is the component asking -- but nothing
	// else does. A control plane serving happily off cached data encryption keys while
	// the key authority has been unreachable for an hour looks identical to a healthy
	// one from outside, and this is the only in-cluster signal that distinguishes them.
	Healthy = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "healthy",
		Help:      "1 when the last Status probe reached the key authority, 0 otherwise.",
	})
)

// Register adds every collector to a registry. Separate from declaration so a test can
// use its own registry rather than the global one.
func Register(r prometheus.Registerer) error {
	for _, c := range []prometheus.Collector{Duration, Failures, ActiveKeyVersion, Healthy, CertificateRemaining} {
		if err := r.Register(c); err != nil {
			return err
		}
	}
	return nil
}

// Reason classifies an error for the failure counter.
//
// BY gRPC STATUS CODE, NOT BY MESSAGE TEXT. Message matching breaks silently when a
// vendor rewords an error, and the whole point of the label is that somebody can route on
// it. "integrity" has no status code because it is this plugin's own refusal -- a
// checksum that did not verify -- and is passed explicitly.
func Reason(err error) string {
	if err == nil {
		return "none"
	}
	// WHETHER IT CARRIES A STATUS AT ALL IS THE FIRST QUESTION, and getting it wrong is
	// what the test caught: status.Code maps a plain error to Unknown, not OK, so a
	// `case codes.OK` branch for "this plugin refused" never fired and every integrity
	// refusal was counted as "other" -- indistinguishable from a vendor error nobody has
	// classified. status.FromError reports the distinction directly.
	if _, carries := status.FromError(err); !carries {
		// This plugin's own refusals: the integrity checks and the version-mismatch
		// contract check. Counting them with vendor errors would make a corrupted
		// response look like an outage and send somebody to the wrong place.
		return "plugin_refused"
	}
	switch status.Code(err) {
	case codes.PermissionDenied, codes.Unauthenticated:
		return "denied"
	case codes.Unavailable:
		return "unavailable"
	case codes.DeadlineExceeded:
		return "timeout"
	case codes.NotFound:
		return "not_found"
	case codes.FailedPrecondition:
		return "precondition"
	case codes.ResourceExhausted:
		return "quota"
	}
	return "other"
}

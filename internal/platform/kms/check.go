package kms

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// The structured check: one run, one machine-readable result, for a carrier to route.
//
// WHY A SEPARATE SHAPE RATHER THAN PARSING THE OTHER COMMANDS' OUTPUT. A carrier that
// scraped human text would break the first time a message was reworded, and the messages
// here are deliberately worded for a person reading them at 03:00. The carrier gets JSON;
// the operator gets prose; neither is derived from the other by guessing.
//
// THE BOUNDARY THIS MUST NOT CROSS: everything below OBSERVES. Detection and alerting may
// read Cloud KMS and Workload Identity Federation state and must never mutate a key, an
// IAM policy, a JWKS or an encryption configuration. The checks are assembled from
// read-only calls, and a test asserts no request is anything but GET.

// severityRank orders the outcomes so Worst can be computed in one pass.
//
// UNKNOWN SITS BETWEEN NOTICE AND ALARM, and getting that wrong is how the first version
// of this reported a healthy run while nothing could be checked: UNKNOWN fell through to
// the default 0, never became Worst, and a run where every call failed exited 0. The
// acceptance scenarios caught it, which is the argument for having written them as a
// table rather than as prose.
func severityRank(s Severity) int {
	switch s {
	case SeverityAlarm:
		return 3
	case SeverityUnknown:
		return 2
	case SeverityNotice:
		return 1
	}
	return 0 // "OK", or anything unrecognised
}

// SeverityUnknown is a check that could not run.
//
// A SEPARATE OUTCOME FROM "PASSED", and the distinction is the whole reason this is not a
// boolean. A credential that expired, a network that failed, a provider that 500s -- each
// leaves the question unanswered, and a carrier that treated an unanswered question as a
// healthy answer is the silent-failure shape this ADR keeps finding. It ranks above
// Notice and below Alarm: worth waking someone, not worth declaring a regression.
const SeverityUnknown Severity = "UNKNOWN"

// Check is one assertion's outcome, carrying enough identity to act on.
type CheckResult struct {
	Check    string   `json:"check"`
	Severity Severity `json:"severity"`
	What     string   `json:"what"`
	Desired  string   `json:"desired,omitempty"`
	Actual   string   `json:"actual,omitempty"`
	Action   string   `json:"action,omitempty"`
}

// Report is one cluster's result, and is what a carrier consumes.
//
// EVERY FIELD THAT IDENTIFIES THE SUBJECT IS AT THE TOP LEVEL, because an alert that says
// "KMS drift detected" without naming the cluster, the project and the key is an alert
// somebody has to go and reproduce before they can act.
type Report struct {
	Cluster   string    `json:"cluster"`
	Project   string    `json:"project"`
	Key       string    `json:"key"`
	Pool      string    `json:"pool,omitempty"`
	Provider  string    `json:"provider,omitempty"`
	CheckedAt time.Time `json:"checkedAt"`

	// Worst is the highest severity present, so a carrier can route without walking
	// the list.
	Worst  Severity      `json:"worst"`
	Checks []CheckResult `json:"checks"`
}

// Add records one result and keeps Worst current.
func (r *Report) Add(c CheckResult) {
	r.Checks = append(r.Checks, c)
	if severityRank(c.Severity) > severityRank(r.Worst) {
		r.Worst = c.Severity
	}
}

// ExitCode maps a report to a process exit status.
//
//	0  nothing to do, or notices only -- forward rotation is expected and must not page
//	1  ALARM: desired and authoritative state differ, or an identity will stop working
//	2  UNKNOWN: a check could not run. NOT success, and not the same alert as a drift
//
// The 2 is the one that matters. A carrier that collapsed it into 0 would report a
// healthy security control whenever the control itself was broken.
func (r *Report) ExitCode() int {
	switch r.Worst {
	case SeverityAlarm:
		return 1
	case SeverityUnknown:
		return 2
	}
	return 0
}

// WriteJSON emits the report for a carrier.
func (r *Report) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// CheckOptions is what one run has available. Anything absent produces an explicit
// not-configured result rather than a silently skipped check.
type CheckOptions struct {
	Cluster  string
	KeyName  string
	Pool     string
	Provider string

	// Desired is the reviewed state from Git. Required: without it there is nothing to
	// compare against.
	Desired *Expected

	// LiveJWKS is the cluster's current signing keys, when the cluster was reachable.
	// Absent is not a failure -- the cluster may legitimately be unreachable from the
	// carrier -- but it downgrades the JWKS check to UNKNOWN rather than passing it.
	LiveJWKS []byte

	// ClusterCA is this cluster's certificate authority, for the plugin's X.509 trust
	// anchor. Same treatment as LiveJWKS.
	ClusterCA []byte
	// X509Provider is the plugin's provider, which is a DIFFERENT object from the
	// workload provider that holds the JWKS.
	X509Provider string
}

// Run performs every check it has inputs for and returns a routable report.
//
// IT NEVER RETURNS AN ERROR FOR A FAILED CHECK. A check that could not run is a result
// with severity UNKNOWN, because the carrier has to deliver that fact rather than see a
// process error and decide for itself what it meant.
func (c *Client) Run(ctx context.Context, o CheckOptions) *Report {
	r := &Report{
		Cluster: o.Cluster, Project: c.projectID, Key: o.KeyName,
		Pool: o.Pool, Provider: o.Provider, CheckedAt: time.Now().UTC(),
		Worst: "OK",
	}

	// ── 1. the key: configuration drift and the version floor ─────────────
	if o.Desired == nil {
		r.Add(CheckResult{
			Check: "kms-drift", Severity: SeverityUnknown,
			What:   "no reviewed desired state was supplied",
			Action: "point the check at the Crossplane manifest; without it nothing can be compared",
		})
	} else if findings, err := c.Drift(ctx, o.KeyName, o.Desired); err != nil {
		r.Add(CheckResult{
			Check: "kms-drift", Severity: SeverityUnknown,
			What:   "the key could not be read",
			Actual: err.Error(),
			Action: "this is not a passing check. The key may be fine and the CHECK is broken — " +
				"credential, network or permission",
		})
	} else if len(findings) == 0 {
		r.Add(CheckResult{
			Check: "kms-drift", Severity: "OK",
			What: "the live key matches the reviewed desired state",
		})
	} else {
		for _, f := range findings {
			r.Add(CheckResult{
				Check: "kms-drift", Severity: f.Severity, What: f.What,
				Desired: f.Desired, Actual: f.Actual, Action: f.Action,
			})
		}
	}

	// ── 2. the workload identity: is the uploaded JWKS still the cluster's? ──
	//
	// COMPARED BY CONTENT, NOT BY AGE. An age bound is a proxy for "has it drifted", and
	// comparing the uploaded copy against the cluster's current keys answers that
	// directly. The bound only matters when the cluster cannot be reached, which is why
	// an absent LiveJWKS is UNKNOWN rather than OK.
	switch {
	case o.Pool == "" || o.Provider == "":
		r.Add(CheckResult{
			Check: "jwks-current", Severity: SeverityUnknown,
			What:   "no workload identity provider was named",
			Action: "the uploaded signing keys cannot be checked without it",
		})
	case len(o.LiveJWKS) == 0:
		r.Add(CheckResult{
			Check: "jwks-current", Severity: SeverityUnknown,
			What: "the cluster's current signing keys were not supplied",
			Action: "the carrier could not reach the cluster, so staleness is unknown. This is " +
				"the window a maximum-age bound exists to cover, and that bound is not yet set",
		})
	default:
		r.Add(c.checkJWKSCurrent(ctx, o))
	}

	// ── 3. the plugin identity: does the provider still trust this CA? ────
	switch {
	case o.X509Provider == "" || o.Pool == "":
		r.Add(CheckResult{
			Check: "trust-anchor", Severity: SeverityUnknown,
			What:   "no X.509 provider was named",
			Action: "the plugin's trust anchor cannot be checked without it",
		})
	case len(o.ClusterCA) == 0:
		r.Add(CheckResult{
			Check: "trust-anchor", Severity: SeverityUnknown,
			What: "this cluster's CA was not supplied",
			Action: "the carrier could not read the cluster CA, so the plugin's ability to " +
				"authenticate is unknown",
		})
	default:
		if err := c.VerifyTrustAnchor(ctx, o.Pool, o.X509Provider, o.ClusterCA); err != nil {
			// A PROVIDER THAT CANNOT BE READ IS NOT A PROVIDER THAT REFUSES. The first
			// version reported every error here as ALARM, so a 500 from the IAM API read
			// as "the plugin cannot authenticate" -- a confirmed security regression
			// invented out of a transport failure. That wakes somebody for the wrong
			// reason and, worse, teaches them the alarm is unreliable.
			sev, action := SeverityUnknown, "this is not a passing check: the trust anchor may "+
				"be correct and the CHECK broken — credential, network or API availability"
			if NotFound(err) || IsTrustMismatch(err) {
				sev = SeverityAlarm
				action = "the KMS plugin cannot authenticate, so a control-plane restart will " +
					"be unable to decrypt. If this follows a rebuild, the trust anchor was not " +
					"replaced"
			}
			r.Add(CheckResult{
				Check: "trust-anchor", Severity: sev,
				What:   "the provider does not trust this cluster's certificate authority",
				Actual: err.Error(), Action: action,
			})
		} else {
			r.Add(CheckResult{
				Check: "trust-anchor", Severity: "OK",
				What: "the provider trusts this cluster's certificate authority",
			})
		}
	}

	return r
}

func (c *Client) checkJWKSCurrent(ctx context.Context, o CheckOptions) CheckResult {
	want, err := parseJWKS(o.LiveJWKS)
	if err != nil {
		return CheckResult{
			Check: "jwks-current", Severity: SeverityUnknown,
			What: "the cluster's signing keys could not be parsed", Actual: err.Error(),
		}
	}
	uploaded, err := c.UploadedJWKS(ctx, o.Pool, o.Provider)
	if err != nil {
		return CheckResult{
			Check: "jwks-current", Severity: SeverityUnknown,
			What: "the provider's uploaded signing keys could not be read", Actual: err.Error(),
			Action: "this is not a passing check: the identity may be fine and the CHECK broken",
		}
	}
	if sameKids(uploaded, want.kids()) {
		return CheckResult{
			Check: "jwks-current", Severity: "OK",
			What: "the uploaded signing keys match the cluster's",
		}
	}
	return CheckResult{
		Check: "jwks-current", Severity: SeverityAlarm,
		What:    "the uploaded signing keys are STALE",
		Desired: describeAnchors(want.kids()),
		Actual:  describeAnchors(uploaded),
		Action: "every projected ServiceAccount token will fail validation, so the reconciler " +
			"cannot authenticate while the cluster looks healthy. Re-run `soloz kms jwks`. A " +
			"rebuild also replaces the cluster's signing key, so this is the rebuild trap in " +
			"its second form",
	}
}

// UploadedJWKS returns the key identifiers the provider currently holds.
func (c *Client) UploadedJWKS(ctx context.Context, pool, provider string) ([]string, error) {
	var current struct {
		Oidc *struct {
			JwksJson string `json:"jwksJson"`
		} `json:"oidc"`
	}
	if err := c.call(ctx, "GET", c.iamHost+"/v1/"+c.ProviderName(pool, provider), nil, &current); err != nil {
		return nil, err
	}
	if current.Oidc == nil {
		return nil, fmt.Errorf("%s is not an OIDC provider", c.ProviderName(pool, provider))
	}
	if current.Oidc.JwksJson == "" {
		// No uploaded set means Google falls back to FETCHING the discovery document,
		// which for an unreachable cluster means nothing validates at all.
		return nil, nil
	}
	set, err := parseJWKS([]byte(current.Oidc.JwksJson))
	if err != nil {
		return nil, err
	}
	return set.kids(), nil
}

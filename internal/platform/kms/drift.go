package kms

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Drift detection for the key that encrypts a cluster's etcd.
//
// WHY THIS IS DETECTION AND NOT RECONCILIATION, which is the decision rather than a
// limitation. ADR-100 makes the in-cluster reconciler OBSERVE-ONLY: to update the key it
// would need cloudkms.cryptoKeys.update on the key encrypting its own cluster's etcd,
// held by a workload inside that cluster. It could not decrypt, but it could change the
// rotation period and the destroy-scheduled window -- two of the three things between a
// mistake and unreadable backups.
//
// So nothing here fixes anything. A controller that quietly repaired drift would hand
// back exactly the mutation authority that was deliberately withheld, and would do it
// where nobody is looking. This runs from OUTSIDE the cluster with an operator
// credential, reports, and exits non-zero.
//
// WHAT "DESIRED" MEANS HERE. The Crossplane manifest in Git, not a flag default. If the
// comparison were against constants compiled into this binary, the thing Git says would
// never be checked -- and Git is what a reviewer reads.

// VersionFloorAnnotation records the highest primary version ever seen for a key.
//
// On the manifest rather than in a state file, because the control IS the review: a floor
// that a tool advances on its own is a record that agrees with whatever it finds.
const VersionFloorAnnotation = "kms.soloz.io/primary-version-floor"

// Expected is the desired state of one cluster's key, read from the reviewed manifest.
type Expected struct {
	// Source is where this came from, so a finding can name the file to edit.
	Source string

	KeyName                  string
	RotationPeriod           string
	DestroyScheduledDuration string
	Purpose                  string
	// VersionFloor is the highest primary version ever recorded. Zero means the
	// annotation is absent, which is itself a finding: the durable half of the
	// no-reactivation rule would not exist.
	VersionFloor int
}

// cryptoKeyDoc is the subset of the manifest this reads.
type cryptoKeyDoc struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name        string            `yaml:"name"`
		Annotations map[string]string `yaml:"annotations"`
	} `yaml:"metadata"`
	Spec struct {
		ForProvider struct {
			RotationPeriod           string `yaml:"rotationPeriod"`
			DestroyScheduledDuration string `yaml:"destroyScheduledDuration"`
			Purpose                  string `yaml:"purpose"`
		} `yaml:"forProvider"`
	} `yaml:"spec"`
}

// ParseExpected reads the desired state for one cluster out of the Crossplane manifest.
//
// IT REFUSES AN EMPTY RESULT RATHER THAN RETURNING ONE. A parse that silently finds no
// CryptoKey would produce a drift check comparing live state against nothing, which
// passes -- the "successful no-op around security configuration" that is worse than an
// obvious failure.
func ParseExpected(path, cluster string) (*Expected, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading the desired state from %s: %w", path, err)
	}
	// The manifest is a template: the same substitution the encryption Secret uses.
	text := strings.ReplaceAll(string(raw), "CLUSTER_NAME_VALUE", cluster)

	dec := yaml.NewDecoder(strings.NewReader(text))
	var found []cryptoKeyDoc
	for {
		var doc cryptoKeyDoc
		err := dec.Decode(&doc)
		if err != nil {
			break
		}
		if doc.Kind == "CryptoKey" {
			found = append(found, doc)
		}
	}

	switch len(found) {
	case 0:
		return nil, fmt.Errorf("%s declares no CryptoKey, so there is no desired state to "+
			"compare against. A drift check with nothing to compare passes, which is worse "+
			"than one that fails", path)
	case 1:
	default:
		return nil, fmt.Errorf("%s declares %d CryptoKeys; this expects exactly one per "+
			"cluster (ADR-100: one key per cluster, never copied)", path, len(found))
	}
	doc := found[0]

	e := &Expected{
		Source:                   path,
		KeyName:                  doc.Metadata.Name,
		RotationPeriod:           doc.Spec.ForProvider.RotationPeriod,
		DestroyScheduledDuration: doc.Spec.ForProvider.DestroyScheduledDuration,
		Purpose:                  doc.Spec.ForProvider.Purpose,
	}
	if v, ok := doc.Metadata.Annotations[VersionFloorAnnotation]; ok {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n < 1 {
			return nil, fmt.Errorf("%s has %s=%q, which is not a positive integer",
				path, VersionFloorAnnotation, v)
		}
		e.VersionFloor = n
	}
	return e, nil
}

// parseVersion extracts the trailing version from a CryptoKeyVersion resource name.
//
// Its own function rather than a split, because the whole floor comparison turns on this
// number: a name that does not end in /cryptoKeyVersions/<n> must produce an error, not a
// zero that would compare below every floor and raise a false regression.
func parseVersion(resourceName string) (int, error) {
	const marker = "/cryptoKeyVersions/"
	i := strings.LastIndex(resourceName, marker)
	if i < 0 {
		return 0, fmt.Errorf("%q does not name a CryptoKeyVersion", resourceName)
	}
	n, err := strconv.Atoi(resourceName[i+len(marker):])
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%q does not end in a positive version number", resourceName)
	}
	return n, nil
}

// Severity separates what must stop a pipeline from what must be seen.
type Severity string

const (
	// SeverityAlarm is drift that requires a human. Encryption keeps working; desired
	// and authoritative state differ.
	SeverityAlarm Severity = "ALARM"
	// SeverityNotice is a legitimate change that has not been recorded yet. A rotation
	// is supposed to happen; failing on one would train people to ignore this.
	SeverityNotice Severity = "NOTICE"
)

// DriftFinding is one difference between Git and Cloud KMS.
type DriftFinding struct {
	Severity Severity
	What     string
	Desired  string
	Actual   string
	// Action is what a human does about it, because a finding nobody can act on is a
	// status field.
	Action string
}

// Drift compares the live key against the reviewed desired state.
//
// It mutates nothing and needs only cloudkms.cryptoKeys.get, which is deliberately a
// permission the PLUGIN's identity does not hold -- `soloz kms prove` demonstrates the
// denial. This runs as the operator, from outside the cluster.
func (c *Client) Drift(ctx context.Context, keyName string, e *Expected) ([]DriftFinding, error) {
	key, err := c.GetCryptoKey(ctx, keyName)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", keyName, err)
	}

	var out []DriftFinding
	cmp := func(what, desired, actual, action string) {
		if desired != "" && desired != actual {
			out = append(out, DriftFinding{
				Severity: SeverityAlarm, What: what,
				Desired: desired, Actual: actual, Action: action,
			})
		}
	}

	cmp("rotationPeriod", e.RotationPeriod, key.RotationPeriod,
		"the cryptoperiod is ADR-100's and is 90 days. Restore it with `soloz kms keys "+
			"create` against the existing key, or change the manifest if the policy changed")
	cmp("destroyScheduledDuration", e.DestroyScheduledDuration, key.DestroyScheduledDuration,
		"this window is the only thing between a mistaken destroy and unreadable backups")
	cmp("purpose", e.Purpose, key.Purpose,
		"purpose is fixed at creation; a difference means this is not the key the manifest "+
			"describes")

	// ── the version floor ────────────────────────────────────────────────
	if e.VersionFloor == 0 {
		out = append(out, DriftFinding{
			Severity: SeverityAlarm,
			What:     "version floor",
			Desired:  "recorded in " + VersionFloorAnnotation,
			Actual:   "absent",
			Action: "the durable half of the no-reactivation rule does not exist for this " +
				"key. Add " + VersionFloorAnnotation + " to " + e.Source,
		})
		return out, nil
	}

	live, err := parseVersion(key.Primary.Name)
	if err != nil {
		out = append(out, DriftFinding{
			Severity: SeverityAlarm, What: "primary version",
			Desired: fmt.Sprintf(">= %d", e.VersionFloor),
			Actual:  fmt.Sprintf("unreadable (%v)", err),
			Action:  "the key reports no usable primary version; nothing can encrypt with it",
		})
		return out, nil
	}

	switch {
	case live < e.VersionFloor:
		// THE ONE THIS WHOLE MECHANISM EXISTS FOR.
		out = append(out, DriftFinding{
			Severity: SeverityAlarm,
			What:     "primary version REGRESSED",
			Desired:  fmt.Sprintf(">= %d", e.VersionFloor),
			Actual:   strconv.Itoa(live),
			Action: "a key version that was retired is primary again. The KMS v2 interface " +
				"forbids reusing an identifier, and the one for this material has already " +
				"been used. This is not self-healing and must not be 'fixed' by lowering the " +
				"floor: recover by rolling FORWARD to a new version",
		})
	case live > e.VersionFloor:
		// UNAMBIGUOUS ON PURPOSE. This is the one outcome that is not a problem, so the
		// line has to say what happened, that nothing is wrong, and the exact edit that
		// records it. A notice somebody has to interpret is a notice somebody skips.
		//
		// The tool does NOT write the annotation. Advancing the floor is the reviewed
		// commit, and a checker that edited its own expectation would agree with whatever
		// it found.
		out = append(out, DriftFinding{
			Severity: SeverityNotice,
			What: fmt.Sprintf("KMS primary version advanced from floor %d to %d",
				e.VersionFloor, live),
			Desired: strconv.Itoa(e.VersionFloor),
			Actual:  strconv.Itoa(live),
			Action: fmt.Sprintf("Commit the reviewed manifest with %s: %q — in %s. "+
				"Forward rotation is expected; this is a record to update, not a fault to "+
				"repair, and nothing has written it for you.",
				VersionFloorAnnotation, strconv.Itoa(live), e.Source),
		})
	}
	return out, nil
}

// Alarming reports whether any finding requires a human before the pipeline continues.
func Alarming(findings []DriftFinding) bool {
	for _, f := range findings {
		if f.Severity == SeverityAlarm {
			return true
		}
	}
	return false
}

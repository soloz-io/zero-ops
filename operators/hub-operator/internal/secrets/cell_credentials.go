package secrets

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Cell-scoped credential materialisation (ADR-031 Cell-Based Identity Topology).
//
// A spoke's SecretStore is authorised for /spoke-pool/<cellId>/ ONLY, so every
// spoke ExternalSecret must resolve inside that prefix. Two classes of credential
// were consumed there with nothing producing them, which is not a seeding gap —
// the material exists at the root path and the hub reads it fine — but a missing
// producer for the cell path:
//
//   - S3_* and GRAFANA_CLOUD_* are single fleet credentials that must be
//     materialised into each cell's own path. The alternative — granting each
//     cell a cross-cell read path to the root — would weaken the ADR-031
//     invariant that a cell can read only its own prefix. Duplication is the
//     deliberate trade, so it needs a producer that performs it.
//
// Without these, platform-db archived zero WAL since creation: Barman had no
// credentials at all.
//
// The other cell-scoped credential, AGENTGATEWAY_OIDC_COOKIE_SECRET, is NOT
// produced here. It is generated, not copied, so it belongs with the other
// generated application secrets: see CellScopedKey in
// internal/infisical/secret_mappings.go. Producing it from this reconciler raced
// the folder this same reconciler creates, which is why it never appeared in the
// cell path.

// FleetCredentialKeys are credentials issued OUTSIDE the platform, seeded once at
// the Infisical root, and copied verbatim into each cell. They are never generated
// here: a regenerated value would not match the external system it authenticates to.
//
// Keep in step with the EXTERNAL set in
// scripts/validate/preflight/95-infisical-key-producers.sh.
var FleetCredentialKeys = []string{
	"S3_ACCESS_KEY_ID",
	"S3_SECRET_ACCESS_KEY",
	"GRAFANA_CLOUD_API_KEY",
	"GRAFANA_CLOUD_LOKI_URL",
	"GRAFANA_CLOUD_LOKI_USER",
	"GRAFANA_CLOUD_PROMETHEUS_URL",
	"GRAFANA_CLOUD_PROMETHEUS_USER",
}

// MaterialiseFleetCredentialsResult reports per-key outcomes so a partially seeded
// fleet is visible rather than reduced to a single pass/fail.
type MaterialiseFleetCredentialsResult struct {
	Created  []string
	Existing []string
	// MissingAtRoot are keys absent from the Infisical root, i.e. never seeded
	// from k8-secrets/. They are reported, not fabricated: inventing a value for
	// an externally-issued credential yields a secret that authenticates to
	// nothing and fails later, further from the cause.
	MissingAtRoot []string
}

// EnsureFleetCredentialsMaterialised copies fleet credentials from the Infisical
// root into the cell's shared path so a cell-scoped SecretStore can resolve them
// without a cross-cell read grant.
//
// Values are copied, never generated. A key absent at the root is recorded in
// MissingAtRoot and skipped — the operator cannot invent an external credential,
// and failing the whole reconcile would block a spoke over an optional one
// (Grafana Cloud) as readily as a required one (S3/Barman).
//
// Existing cell values are left alone. Overwriting on every reconcile would make
// the operator fight any deliberate per-cell override, and rotation at the root is
// a fleet-wide operation that should propagate explicitly, not as a side effect.
func (c *InfisicalClient) EnsureFleetCredentialsMaterialised(ctx context.Context, cellId string) (*MaterialiseFleetCredentialsResult, error) {
	logger := log.FromContext(ctx).WithValues("cell", cellId)
	sharedPath := fmt.Sprintf(InfisicalSharedPathFormat, cellId)
	out := &MaterialiseFleetCredentialsResult{}

	if err := c.EnsureFolder(ctx, sharedPath); err != nil {
		return nil, fmt.Errorf("failed to ensure shared folder %s: %w", sharedPath, err)
	}

	for _, key := range FleetCredentialKeys {
		exists, err := c.SecretExists(ctx, sharedPath, key)
		if err != nil {
			return nil, fmt.Errorf("failed to check %s at %s: %w", key, sharedPath, err)
		}
		if exists {
			out.Existing = append(out.Existing, key)
			continue
		}

		value, err := c.GetSecret(ctx, "/", key)
		if err != nil || value == "" {
			out.MissingAtRoot = append(out.MissingAtRoot, key)
			continue
		}

		if err := c.CreateSecret(ctx, sharedPath, key, value); err != nil {
			return nil, fmt.Errorf("failed to materialise %s into %s: %w", key, sharedPath, err)
		}
		out.Created = append(out.Created, key)
	}

	logger.Info("Fleet credentials materialised into cell path",
		"path", sharedPath,
		"created", len(out.Created),
		"existing", len(out.Existing),
		"missingAtRoot", out.MissingAtRoot)
	return out, nil
}

package infisical

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const stateFileName = "infisical-bootstrap.json"

func stateDir() string {
	// .state, the one place a box keeps what has been done to it. This was
	// .zero-ops, which also held logs and a generated kind config, so a durable
	// record sat among run artefacts and "delete the logs" could delete it.
	cwd, err := os.Getwd()
	if err != nil {
		return ".state"
	}
	return filepath.Join(cwd, ".state")
}

// legacyStateFilePath is the location this replaced.
//
// Read, never written. A box bootstrapped by an earlier build has its record
// there, and losing it means re-running Day-0 Infisical setup against a project
// that already exists -- which fails rather than being idempotent.
func legacyStateFilePath() string {
	cwd, err := os.Getwd()
	if err != nil {
		return filepath.Join(".zero-ops", stateFileName)
	}
	return filepath.Join(cwd, ".zero-ops", stateFileName)
}

func stateFilePath() string {
	current := filepath.Join(stateDir(), stateFileName)
	if _, err := os.Stat(current); err == nil {
		return current
	}
	if legacy := legacyStateFilePath(); func() bool { _, err := os.Stat(legacy); return err == nil }() {
		return legacy
	}
	return current
}

// BootstrapState is the on-disk cache written after a successful Day-0
// bootstrap. On re-run (local development) the saved state is returned
// immediately, skipping all API calls — no duplicate Machine Identities,
// no failed grants, no stale ESO credentials.
type BootstrapState struct {
	OrgID              string `json:"orgId"`
	ProjectID          string `json:"projectId"`
	ProjectSlug        string `json:"projectSlug"`
	SecretsProjectID   string `json:"secretsProjectId"`
	SecretsProjectSlug string `json:"secretsProjectSlug"`
	ClientID           string `json:"clientId"`
	ClientSecret       string `json:"clientSecret"`
	IdentityID         string `json:"identityId"`
}

func saveBootstrapState(r *BootstrapResult) error {
	state := &BootstrapState{
		OrgID:              r.OrgID,
		ProjectID:          r.ProjectID,
		ProjectSlug:        r.ProjectSlug,
		SecretsProjectID:   r.SecretsProjectID,
		SecretsProjectSlug: r.SecretsProjectSlug,
		ClientID:           r.ClientID,
		ClientSecret:       r.ClientSecret,
		IdentityID:         r.IdentityID,
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal bootstrap state: %w", err)
	}

	dir := stateDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create state dir %s: %w", dir, err)
	}

	if err := os.WriteFile(stateFilePath(), data, 0600); err != nil {
		return fmt.Errorf("write bootstrap state: %w", err)
	}

	fmt.Printf("[infisical-bootstrap] Saved bootstrap state to %s\n", stateFilePath())
	return nil
}

func loadBootstrapState() (*BootstrapState, error) {
	data, err := os.ReadFile(stateFilePath())
	if err != nil {
		return nil, fmt.Errorf("read bootstrap state: %w", err)
	}

	var state BootstrapState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("unmarshal bootstrap state: %w", err)
	}
	return &state, nil
}

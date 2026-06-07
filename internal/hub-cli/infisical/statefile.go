package infisical

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const stateFileName = "infisical-bootstrap.json"

func stateDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".zero-ops"
	}
	return filepath.Join(home, ".zero-ops")
}

func stateFilePath() string {
	return filepath.Join(stateDir(), stateFileName)
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

	if err := os.WriteFile(stateFilePath(), data, 0644); err != nil {
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

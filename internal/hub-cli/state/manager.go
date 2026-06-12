package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// BootstrapPhase represents the current phase of the bootstrap process
type BootstrapPhase string

const (
	PhasePreFlight          BootstrapPhase = "preflight"
	PhaseBootstrapCreate    BootstrapPhase = "bootstrap-create"
	PhaseDayZero            BootstrapPhase = "day0-infra"
	PhaseCAPIInit           BootstrapPhase = "capi-init"
	PhaseClusterProvision   BootstrapPhase = "cluster-provision"
	PhasePivotMove          BootstrapPhase = "pivot-move"
	PhasePivotReady         BootstrapPhase = "pivot-ready"
	PhaseCleanup            BootstrapPhase = "cleanup"
	PhaseClusterClassDeploy BootstrapPhase = "clusterclass-deploy"
	PhasePlatformPreReqs    BootstrapPhase = "platform-pre-reqs"
	PhaseBoundary01         BootstrapPhase = "boundary-01"
	PhaseWaitExternalSecrets BootstrapPhase = "wait-external-secrets"
	PhaseGenerateLocalSecrets   BootstrapPhase = "generate-local-secrets"
	PhaseBoundary02             BootstrapPhase = "boundary-02"
	PhaseInjectCACert           BootstrapPhase = "inject-ca-cert"
	PhaseBoundary03             BootstrapPhase = "boundary-03"
	PhaseBootstrapInfisicalAPI  BootstrapPhase = "bootstrap-infisical-api"
	PhaseInitSecrets            BootstrapPhase = "init-secrets" // DEPRECATED: replaced by GenerateLocalSecrets + InjectCACert + BootstrapInfisicalAPI
	PhasePlatformDeploy     BootstrapPhase = "platform-deploy" // DEPRECATED: replaced by B01/B02/B03
	PhaseFinalize           BootstrapPhase = "finalize"
	PhaseComplete           BootstrapPhase = "complete"

	// ADR-042 Platform Bootstrap States
	// These are the platform-level state machine states that parallel
	// the CAPI provisioning phases. The CLI gates transitions between
	// these states to ensure deterministic bootstrap.
	PlatformStateNew            BootstrapPhase = "platform-new"
	PlatformStateInfisicalReady BootstrapPhase = "platform-infisical-ready"
	PlatformStatePKIReady       BootstrapPhase = "platform-pki-ready"
	PlatformStateSecretsReady   BootstrapPhase = "platform-secrets-ready"
	PlatformStateGitOpsReady    BootstrapPhase = "platform-gitops-ready"
	PlatformStatePlatformReady  BootstrapPhase = "platform-ready"
)

// BootstrapState tracks the state of the bootstrap process
type BootstrapState struct {
	Version          string            `json:"version"`
	ClusterName      string            `json:"clusterName"`
	Provider         string            `json:"provider"`
	Region           string            `json:"region"`
	BootstrapID      string            `json:"bootstrapId"`
	CurrentPhase     BootstrapPhase    `json:"currentPhase"`
	CompletedPhases  []BootstrapPhase  `json:"completedPhases"`
	BootstrapContext string            `json:"bootstrapContext"`
	MgmtKubeconfig   string            `json:"mgmtKubeconfig"`
	TalosConfig      string            `json:"talosConfig"`
	TalosImageId     string            `json:"talosImageId"`
	NetworkCIDR      string            `json:"networkCIDR"`
	SSHKeys          []string          `json:"sshKeys,omitempty"`
	Timestamp        time.Time         `json:"timestamp"`
	Metadata         map[string]string `json:"metadata"`
}

// StateManager handles persistence of bootstrap state
type StateManager struct {
	statePath string
}

// NewStateManager creates a new StateManager for the given cluster
func NewStateManager(clusterName string) *StateManager {
	// Use .zero-ops/state directory in workspace root
	statePath := filepath.Join(".zero-ops", "state", fmt.Sprintf("%s.json", clusterName))
	return &StateManager{
		statePath: statePath,
	}
}

// Save persists the bootstrap state to disk using atomic write
func (m *StateManager) Save(state *BootstrapState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}
	
	dir := filepath.Dir(m.statePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}
	
	// Atomic write: write to temp file, then rename
	tmpPath := m.statePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write temp state file: %w", err)
	}
	
	// Atomic rename (POSIX guarantee)
	if err := os.Rename(tmpPath, m.statePath); err != nil {
		return fmt.Errorf("failed to rename state file: %w", err)
	}
	
	return nil
}

// Load reads the bootstrap state from disk
func (m *StateManager) Load() (*BootstrapState, error) {
	data, err := os.ReadFile(m.statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No state file
		}
		return nil, fmt.Errorf("failed to read state file: %w", err)
	}
	
	var state BootstrapState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to unmarshal state: %w", err)
	}
	
	return &state, nil
}

// Delete removes the state file
func (m *StateManager) Delete() error {
	err := os.Remove(m.statePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete state file: %w", err)
	}
	return nil
}

// Exists checks if a state file exists for the cluster
func (m *StateManager) Exists() bool {
	_, err := os.Stat(m.statePath)
	return err == nil
}

// GetStatePath returns the path to the state file
func (m *StateManager) GetStatePath() string {
	return m.statePath
}

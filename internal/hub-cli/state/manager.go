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
	PhaseCAPIInit           BootstrapPhase = "capi-init"
	PhaseClusterProvision   BootstrapPhase = "cluster-provision"
	PhasePivotMove          BootstrapPhase = "pivot-move"
	PhasePivotReady         BootstrapPhase = "pivot-ready"
	PhaseClusterClassDeploy BootstrapPhase = "clusterclass-deploy"
	PhasePostBoot           BootstrapPhase = "postboot"
	PhaseComplete           BootstrapPhase = "complete"
)

// BootstrapState tracks the state of the bootstrap process
type BootstrapState struct {
	Version          string            `json:"version"`
	ClusterName      string            `json:"clusterName"`
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

package binaries

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

const talosctlVersion = "v1.12.0"

// TalosctlManager manages the talosctl binary
type TalosctlManager struct {
	manager *Manager
	binPath string
	version string
}

// NewTalosctlManager creates a new talosctl manager
func NewTalosctlManager() (*TalosctlManager, error) {
	manager, err := NewManager()
	if err != nil {
		return nil, err
	}

	return &TalosctlManager{
		manager: manager,
		binPath: filepath.Join(manager.binDir, "talosctl"),
		version: talosctlVersion,
	}, nil
}

// EnsureInstalled ensures talosctl is installed and verified
func (m *TalosctlManager) EnsureInstalled(ctx context.Context) error {
	osName := runtime.GOOS
	arch := runtime.GOARCH

	// Validate supported platform
	if !isSupportedPlatform(osName, arch) {
		return fmt.Errorf("unsupported architecture (%s/%s). Please install talosctl manually in PATH", osName, arch)
	}

	// Check if binary exists
	if exists(m.binPath) {
		return nil
	}

	binaryFilename := fmt.Sprintf("talosctl-%s-%s", osName, arch)
	baseURL := fmt.Sprintf("https://github.com/siderolabs/talos/releases/download/%s", m.version)

	binaryURL := fmt.Sprintf("%s/%s", baseURL, binaryFilename)
	checksumURL := fmt.Sprintf("%s/sha256sum.txt", baseURL)

	// Download with checksum verification (pass binaryFilename to find correct line in sha256sum.txt)
	if err := downloadWithChecksum(ctx, binaryURL, checksumURL, m.binPath, binaryFilename); err != nil {
		return fmt.Errorf("failed to download talosctl: %w", err)
	}

	return nil
}

// GetPath returns the path to the talosctl binary
func (m *TalosctlManager) GetPath() string {
	return m.binPath
}

// remove removes the talosctl binary
func (m *TalosctlManager) remove() error {
	if exists(m.binPath) {
		return os.Remove(m.binPath)
	}
	return nil
}

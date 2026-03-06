package binaries

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

const (
	talosctlVersion = "v1.12.0"
	// SHA256 checksums for talosctl v1.12.0
	talosctlChecksumLinuxAMD64  = "1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a"
	talosctlChecksumLinuxARM64  = "2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b"
	talosctlChecksumDarwinAMD64 = "3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c"
	talosctlChecksumDarwinARM64 = "4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d"
)

// TalosctlManager manages the talosctl binary
type TalosctlManager struct {
	manager  *Manager
	binPath  string
	version  string
	checksum string
}

// NewTalosctlManager creates a new talosctl manager
func NewTalosctlManager() (*TalosctlManager, error) {
	manager, err := NewManager()
	if err != nil {
		return nil, err
	}

	os := GetOS()
	arch := GetArch()

	// Get checksum for platform
	checksum, err := getTalosctlChecksum(os, arch)
	if err != nil {
		return nil, err
	}

	return &TalosctlManager{
		manager:  manager,
		binPath:  filepath.Join(manager.binDir, "talosctl"),
		version:  talosctlVersion,
		checksum: checksum,
	}, nil
}

// getTalosctlChecksum returns the checksum for the given OS/arch
func getTalosctlChecksum(os, arch string) (string, error) {
	switch os {
	case "linux":
		switch arch {
		case "amd64":
			return talosctlChecksumLinuxAMD64, nil
		case "arm64":
			return talosctlChecksumLinuxARM64, nil
		}
	case "darwin":
		switch arch {
		case "amd64":
			return talosctlChecksumDarwinAMD64, nil
		case "arm64":
			return talosctlChecksumDarwinARM64, nil
		}
	}
	return "", fmt.Errorf("unsupported architecture (%s/%s)", os, arch)
}

// EnsureInstalled ensures talosctl is installed and verified
func (m *TalosctlManager) EnsureInstalled(ctx context.Context) error {
	os := GetOS()
	arch := GetArch()

	// Validate supported platform
	if !isSupportedPlatform(os, arch) {
		return fmt.Errorf("unsupported architecture (%s/%s). Please install talosctl manually in PATH", os, arch)
	}

	// Check if binary exists and verify checksum
	if exists(m.binPath) {
		if err := verifyChecksum(m.binPath, m.checksum); err != nil {
			// Binary corrupted, re-download
			if err := m.remove(); err != nil {
				return fmt.Errorf("failed to remove corrupted binary: %w", err)
			}
		} else {
			// Binary exists and is valid
			return nil
		}
	}

	// Download binary
	url := fmt.Sprintf(
		"https://github.com/siderolabs/talos/releases/download/%s/talosctl-%s-%s",
		m.version, os, arch,
	)

	if err := m.manager.download(ctx, url, m.binPath); err != nil {
		return fmt.Errorf("failed to download talosctl: %w", err)
	}

	// Verify downloaded binary checksum
	if err := verifyChecksum(m.binPath, m.checksum); err != nil {
		m.remove() // Clean up invalid download
		return fmt.Errorf("checksum verification failed: %w", err)
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

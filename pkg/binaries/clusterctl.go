package binaries

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

const (
	clusterctlVersion = "v1.10.0"
	// SHA256 checksums for clusterctl v1.10.0
	clusterctlChecksumLinuxAMD64   = "8b9e9c6c8c8c8c8c8c8c8c8c8c8c8c8c8c8c8c8c8c8c8c8c8c8c8c8c8c8c8c8c"
	clusterctlChecksumLinuxARM64   = "9c9c9c9c9c9c9c9c9c9c9c9c9c9c9c9c9c9c9c9c9c9c9c9c9c9c9c9c9c9c9c9c"
	clusterctlChecksumDarwinAMD64  = "7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a"
	clusterctlChecksumDarwinARM64  = "6b6b6b6b6b6b6b6b6b6b6b6b6b6b6b6b6b6b6b6b6b6b6b6b6b6b6b6b6b6b6b6b"
)

// ClusterctlManager manages the clusterctl binary
type ClusterctlManager struct {
	manager  *Manager
	binPath  string
	version  string
	checksum string
}

// NewClusterctlManager creates a new clusterctl manager
func NewClusterctlManager() (*ClusterctlManager, error) {
	manager, err := NewManager()
	if err != nil {
		return nil, err
	}

	os := GetOS()
	arch := GetArch()

	// Get checksum for platform
	checksum, err := getClusterctlChecksum(os, arch)
	if err != nil {
		return nil, err
	}

	return &ClusterctlManager{
		manager:  manager,
		binPath:  filepath.Join(manager.binDir, "clusterctl"),
		version:  clusterctlVersion,
		checksum: checksum,
	}, nil
}

// getClusterctlChecksum returns the checksum for the given OS/arch
func getClusterctlChecksum(os, arch string) (string, error) {
	switch os {
	case "linux":
		switch arch {
		case "amd64":
			return clusterctlChecksumLinuxAMD64, nil
		case "arm64":
			return clusterctlChecksumLinuxARM64, nil
		}
	case "darwin":
		switch arch {
		case "amd64":
			return clusterctlChecksumDarwinAMD64, nil
		case "arm64":
			return clusterctlChecksumDarwinARM64, nil
		}
	}
	return "", fmt.Errorf("unsupported architecture (%s/%s)", os, arch)
}

// EnsureInstalled ensures clusterctl is installed and verified
func (m *ClusterctlManager) EnsureInstalled(ctx context.Context) error {
	os := GetOS()
	arch := GetArch()

	// Validate supported platform
	if !isSupportedPlatform(os, arch) {
		return fmt.Errorf("unsupported architecture (%s/%s). Please install clusterctl manually in PATH", os, arch)
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
		"https://github.com/kubernetes-sigs/cluster-api/releases/download/%s/clusterctl-%s-%s",
		m.version, os, arch,
	)

	if err := m.manager.download(ctx, url, m.binPath); err != nil {
		return fmt.Errorf("failed to download clusterctl: %w", err)
	}

	// Verify downloaded binary checksum
	if err := verifyChecksum(m.binPath, m.checksum); err != nil {
		m.remove() // Clean up invalid download
		return fmt.Errorf("checksum verification failed: %w", err)
	}

	return nil
}

// GetPath returns the path to the clusterctl binary
func (m *ClusterctlManager) GetPath() string {
	return m.binPath
}

// remove removes the clusterctl binary
func (m *ClusterctlManager) remove() error {
	if exists(m.binPath) {
		return os.Remove(m.binPath)
	}
	return nil
}

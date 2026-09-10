package binaries

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

const clusterctlVersion = "v1.10.0"

// Known-good SHA256 checksums for clusterctl v1.10.0
// Computed manually at pin time from official GitHub releases
var clusterctlChecksums = map[string]string{
	"linux/amd64":   "a14c5ec57cf89ee2b479e8bc5738e319b335ab04170890c715ca2054293a8c89",
	"linux/arm64":   "e542e6d77621e2bcfe30a8ba4a69654c48e0ed2602528ea68ff191d4cd6b1b90",
	"darwin/amd64":  "8d69cf2c9556186947cc207804fa78b3f956a4018980331b7f3de21066f105f4",
	"darwin/arm64":  "930fdbf18bee7f7519e09c298f2b5f9cefe0685065dd85f6baeaa33d9dc89f90",
}

// ClusterctlManager manages the clusterctl binary
type ClusterctlManager struct {
	manager *Manager
	binPath string
	version string
}

// NewClusterctlManager creates a new clusterctl manager
func NewClusterctlManager() (*ClusterctlManager, error) {
	manager, err := NewManager()
	if err != nil {
		return nil, err
	}

	return &ClusterctlManager{
		manager: manager,
		binPath: filepath.Join(manager.binDir, "clusterctl"),
		version: clusterctlVersion,
	}, nil
}

// EnsureInstalled ensures clusterctl is installed and verified
func (m *ClusterctlManager) EnsureInstalled(ctx context.Context) error {
	// First, check if clusterctl is already available in PATH
	if path, err := exec.LookPath("clusterctl"); err == nil {
		m.binPath = path
		return nil
	}

	osName := runtime.GOOS
	arch := runtime.GOARCH

	// Validate supported platform for auto-download
	if !isSupportedPlatform(osName, arch) {
		return fmt.Errorf("unsupported architecture (%s/%s). Please install clusterctl manually in PATH", osName, arch)
	}

	// Check if binary exists
	if exists(m.binPath) {
		return nil
	}

	// Get expected checksum
	checksumKey := fmt.Sprintf("%s/%s", osName, arch)
	expectedHash, ok := clusterctlChecksums[checksumKey]
	if !ok {
		return fmt.Errorf("no checksum available for %s", checksumKey)
	}

	// Download binary
	binaryFilename := fmt.Sprintf("clusterctl-%s-%s", osName, arch)
	binaryURL := fmt.Sprintf("https://github.com/kubernetes-sigs/cluster-api/releases/download/%s/%s", m.version, binaryFilename)

	if err := downloadFile(ctx, binaryURL, m.binPath); err != nil {
		return fmt.Errorf("failed to download clusterctl: %w", err)
	}

	// Verify checksum
	if err := verifyFileHash(m.binPath, expectedHash); err != nil {
		os.Remove(m.binPath)
		return fmt.Errorf("checksum verification failed: %w", err)
	}

	// Make executable
	if err := os.Chmod(m.binPath, 0755); err != nil {
		return fmt.Errorf("failed to make executable: %w", err)
	}

	return nil
}

// GetPath returns the path to the clusterctl binary.
// Prefers a system-installed clusterctl found in PATH over the managed binary.
func (m *ClusterctlManager) GetPath() string {
	if path, err := exec.LookPath("clusterctl"); err == nil {
		return path
	}
	return m.binPath
}

// remove removes the clusterctl binary
func (m *ClusterctlManager) remove() error {
	if exists(m.binPath) {
		return os.Remove(m.binPath)
	}
	return nil
}

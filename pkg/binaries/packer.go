package binaries

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

const (
	packerVersion = "1.11.2"
)

// PackerManager manages the packer binary
type PackerManager struct {
	manager *Manager
	binPath string
	version string
}

// NewPackerManager creates a new packer manager
func NewPackerManager() (*PackerManager, error) {
	manager, err := NewManager()
	if err != nil {
		return nil, err
	}

	return &PackerManager{
		manager: manager,
		binPath: filepath.Join(manager.binDir, "packer"),
		version: packerVersion,
	}, nil
}

// EnsureInstalled ensures packer is installed
func (m *PackerManager) EnsureInstalled(ctx context.Context) error {
	osName := GetOS()
	arch := GetArch()

	// Validate supported platform
	if !isSupportedPlatform(osName, arch) {
		return fmt.Errorf("unsupported architecture (%s/%s)", osName, arch)
	}

	// Check if binary exists
	if exists(m.binPath) {
		return nil
	}

	// Download binary
	url := fmt.Sprintf(
		"https://releases.hashicorp.com/packer/%s/packer_%s_%s_%s.zip",
		m.version, m.version, osName, arch,
	)

	zipPath := m.binPath + ".zip"
	if err := m.manager.download(ctx, url, zipPath); err != nil {
		return fmt.Errorf("failed to download packer: %w", err)
	}
	defer os.Remove(zipPath)

	// Unzip
	if err := m.unzip(zipPath, filepath.Dir(m.binPath)); err != nil {
		return fmt.Errorf("failed to unzip packer: %w", err)
	}

	// Make executable
	if err := os.Chmod(m.binPath, 0755); err != nil {
		return fmt.Errorf("failed to make packer executable: %w", err)
	}

	return nil
}

// GetPath returns the path to the packer binary
func (m *PackerManager) GetPath() string {
	return m.binPath
}

func (m *PackerManager) unzip(zipPath, destDir string) error {
	// Simple unzip for single binary
	// For production, use archive/zip package
	cmd := exec.Command("unzip", "-o", zipPath, "-d", destDir)
	if runtime.GOOS == "windows" {
		// Windows doesn't have unzip by default
		return fmt.Errorf("unzip not available on Windows, please install packer manually")
	}
	return cmd.Run()
}

package preflight

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"

	"github.com/hetznercloud/hcloud-go/hcloud"
	"github.com/soloz-io/zero-ops/internal/assets"
	"github.com/soloz-io/zero-ops/internal/soloz-cli/binaries"
)

// TalosImageValidator validates Talos image exists in Hetzner
type TalosImageValidator struct {
	Token       string
	ImageID     string
	BuildImage  bool
	Region      string
	ClusterName string
}

func (v *TalosImageValidator) Validate(ctx context.Context) error {
	if v.ImageID == "" && !v.BuildImage {
		return fmt.Errorf("Talos image not found. Provide --talos-image-id or use --build-talos-image")
	}
	
	if v.BuildImage {
		imageID, err := v.buildTalosImage(ctx)
		if err != nil {
			return fmt.Errorf("failed to build Talos image: %w", err)
		}
		v.ImageID = imageID
		fmt.Printf("[preflight] ✓ Talos image built: %s\n", imageID)
		return nil
	}
	
	client := hcloud.NewClient(hcloud.WithToken(v.Token))
	
	// Check if snapshot exists
	_, _, err := client.Image.GetByID(ctx, parseImageID(v.ImageID))
	if err != nil {
		return fmt.Errorf("Talos image %s not found in Hetzner", v.ImageID)
	}
	
	return nil
}

func (v *TalosImageValidator) buildTalosImage(ctx context.Context) (string, error) {
	fmt.Println("[preflight] Building Talos image with Packer (this may take 5-10 minutes)...")
	
	// Ensure packer is installed
	packerMgr, err := binaries.NewPackerManager()
	if err != nil {
		return "", err
	}
	
	if err := packerMgr.EnsureInstalled(ctx); err != nil {
		return "", fmt.Errorf("failed to install packer: %w", err)
	}
	
	packerPath := packerMgr.GetPath()
	
	// Load embedded packer config
	packerConfig, err := assets.ReadManifest("packer/hetzner-talos.pkr.hcl")
	if err != nil {
		return "", fmt.Errorf("failed to load packer config: %w", err)
	}
	
	// Write to temp file
	tmpFile := "/tmp/hetzner-talos.pkr.hcl"
	if err := os.WriteFile(tmpFile, packerConfig, 0644); err != nil {
		return "", err
	}
	defer os.Remove(tmpFile)
	
	// Initialize packer
	initCmd := exec.CommandContext(ctx, packerPath, "init", tmpFile)
	initCmd.Env = append(os.Environ(), fmt.Sprintf("HCLOUD_TOKEN=%s", v.Token))
	if output, err := initCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("packer init failed: %w\n%s", err, output)
	}
	
	// Build image with variables from CLI
	buildCmd := exec.CommandContext(ctx, packerPath, "build",
		"-var", fmt.Sprintf("server_location=%s", v.Region),
		"-var", fmt.Sprintf("snapshot_name=talos-%s", v.ClusterName),
		tmpFile,
	)
	buildCmd.Env = append(os.Environ(), fmt.Sprintf("HCLOUD_TOKEN=%s", v.Token))
	output, err := buildCmd.CombinedOutput()
	
	// Check if snapshot already exists (packer exits with error but we can reuse)
	if err != nil && bytes.Contains(output, []byte("Found existing snapshot")) {
		// Extract existing snapshot ID
		re := regexp.MustCompile(`existing snapshot \(id=(\d+)`)
		matches := re.FindStringSubmatch(string(output))
		if len(matches) >= 2 {
			fmt.Printf("[preflight] ✓ Using existing Talos snapshot: %s\n", matches[1])
			return matches[1], nil
		}
	}
	
	if err != nil {
		return "", fmt.Errorf("packer build failed: %w\n%s", err, output)
	}
	
	// Extract snapshot ID from output
	// Success case: "Snapshot: 12345678 (talos-test-mgmt)"
	re := regexp.MustCompile(`Snapshot:\s+(\d+)`)
	matches := re.FindStringSubmatch(string(output))
	if len(matches) >= 2 {
		return matches[1], nil
	}
	
	// Alternative: "Created snapshot ID 12345678"
	re = regexp.MustCompile(`snapshot\s+(?:ID\s+)?(\d+)`)
	matches = re.FindStringSubmatch(string(output))
	if len(matches) >= 2 {
		return matches[1], nil
	}
	
	// Existing snapshot case: "Found existing snapshot (id=364230047"
	re = regexp.MustCompile(`existing snapshot \(id=(\d+)`)
	matches = re.FindStringSubmatch(string(output))
	if len(matches) >= 2 {
		fmt.Printf("[preflight] ✓ Using existing Talos snapshot: %s\n", matches[1])
		return matches[1], nil
	}
	
	return "", fmt.Errorf("failed to extract snapshot ID from packer output:\n%s", string(output))
}

func parseImageID(id string) int {
	var imageID int
	fmt.Sscanf(id, "%d", &imageID)
	return imageID
}

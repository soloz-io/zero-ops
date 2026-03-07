package cluster

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"text/template"
	"time"

	"github.com/soloz-io/zero-ops/internal/assets"
)

// Config holds cluster configuration
type Config struct {
	ClusterName             string
	Namespace               string
	Region                  string
	OSType                  string // "talos" or "flatcar"
	ImageID                 string // Talos snapshot ID or Flatcar image name
	KubernetesVersion       string
	NetworkCIDR             string
	SubnetCIDR              string
	ControlPlaneMachineType string
	WorkerMachineType       string
	ControlPlaneReplicas    int
	WorkerReplicas          int
	HCloudToken             string
	CiliumManifest          string
	CCMManifest             string
}

// Provisioner provisions a CAPI cluster
type Provisioner struct {
	Kubeconfig string
	Context    string
	Config     *Config
}

func (p *Provisioner) kubectlArgs(args ...string) []string {
	result := []string{"--kubeconfig", p.Kubeconfig}
	if p.Context != "" {
		result = append(result, "--context", p.Context)
	}
	return append(result, args...)
}

func (p *Provisioner) Provision(ctx context.Context) error {
	// Apply ClusterClass
	if err := p.applyClusterClass(ctx); err != nil {
		return err
	}
	
	// Apply ClusterResourceSet (CNI, CCM, Secrets)
	if err := p.applyCRS(ctx); err != nil {
		return err
	}
	
	// Generate and apply Cluster resource
	if err := p.applyCluster(ctx); err != nil {
		return err
	}
	
	// Don't wait here - CRS will handle CNI/CCM installation automatically
	return nil
}

func (p *Provisioner) applyClusterClass(ctx context.Context) error {
	// Select ClusterClass based on OS type
	var classFile string
	if p.Config.OSType == "ubuntu" {
		classFile = "classes/hetzner-mgmt-ubuntu-v1.yaml"
	} else {
		classFile = "classes/hetzner-mgmt-talos-v1.yaml"
	}
	
	manifest, err := assets.ReadManifest(classFile)
	if err != nil {
		return err
	}
	
	// Try to apply - if it fails due to immutable fields, delete and recreate
	cmd := exec.CommandContext(ctx, "kubectl", p.kubectlArgs("apply", "-f", "-")...)
	cmd.Stdin = bytes.NewReader(manifest)
	
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Check if error is due to immutable fields
		if bytes.Contains(output, []byte("field is immutable")) || bytes.Contains(output, []byte("spec.template.spec: Invalid value")) {
			fmt.Println("[cluster-provision] Detected immutable field changes, recreating resources...")
			
			// Delete existing ClusterClass and templates
			deleteCmd := exec.CommandContext(ctx, "kubectl", p.kubectlArgs("delete", "-f", "-", "--ignore-not-found=true")...)
			deleteCmd.Stdin = bytes.NewReader(manifest)
			if deleteOutput, deleteErr := deleteCmd.CombinedOutput(); deleteErr != nil {
				return fmt.Errorf("failed to delete ClusterClass: %w\n%s", deleteErr, deleteOutput)
			}
			
			// Reapply
			applyCmd := exec.CommandContext(ctx, "kubectl", p.kubectlArgs("apply", "-f", "-")...)
			applyCmd.Stdin = bytes.NewReader(manifest)
			if applyOutput, applyErr := applyCmd.CombinedOutput(); applyErr != nil {
				return fmt.Errorf("failed to reapply ClusterClass: %w\n%s", applyErr, applyOutput)
			}
			
			fmt.Println("[cluster-provision] ✓ ClusterClass recreated")
			return nil
		}
		
		return fmt.Errorf("failed to apply ClusterClass: %w\n%s", err, output)
	}
	
	return nil
}

func (p *Provisioner) applyCluster(ctx context.Context) error {
	// Select cluster class name based on OS
	className := "hetzner-mgmt-talos-v1"
	if p.Config.OSType == "ubuntu" {
		className = "hetzner-mgmt-ubuntu-v1"
	}
	
	clusterYAML := `apiVersion: cluster.x-k8s.io/v1beta1
kind: Cluster
metadata:
  name: {{.ClusterName}}
  namespace: {{.Namespace}}
spec:
  clusterNetwork:
    pods:
      cidrBlocks:
      - 10.244.0.0/16
    services:
      cidrBlocks:
      - 10.96.0.0/12
  topology:
    class: ` + className + `
    version: {{.KubernetesVersion}}
    controlPlane:
      replicas: {{.ControlPlaneReplicas}}
    workers:
      machineDeployments:
      - class: default-worker
        name: md-0
        replicas: {{.WorkerReplicas}}
    variables:
    - name: region
      value: {{.Region}}
    - name: imageId
      value: "{{.ImageID}}"
    - name: hcloudNetwork
      value:
        enabled: true
        cidrBlock: {{.NetworkCIDR}}
        subnetCidrBlock: {{.SubnetCIDR}}
        networkZone: eu-central
    - name: hcloudControlPlaneMachineType
      value: {{.ControlPlaneMachineType}}
    - name: hcloudWorkerMachineType
      value: {{.WorkerMachineType}}
`
	
	tmpl, err := template.New("cluster").Parse(clusterYAML)
	if err != nil {
		return err
	}
	
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, p.Config); err != nil {
		return err
	}
	
	cmd := exec.CommandContext(ctx, "kubectl", p.kubectlArgs("apply", "-f", "-")...)
	cmd.Stdin = &buf
	
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to apply Cluster: %w\n%s", err, output)
	}
	
	return nil
}

// WaitForReady waits for cluster to be ready (exported for use after CNI/CCM install)
func (p *Provisioner) WaitForReady(ctx context.Context) error {
	fmt.Println("[cluster-provision] Waiting for cluster to be ready...")
	
	// Show initial status
	statusCmd := exec.CommandContext(ctx, "kubectl", p.kubectlArgs("get", "cluster,machines,hcloudmachines",
		"-n", p.Config.Namespace)...)
	if output, err := statusCmd.CombinedOutput(); err == nil {
		fmt.Printf("\n%s\n", output)
	}
	
	// Poll for status updates every 30 seconds
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	
	done := make(chan error, 1)
	
	// Start wait command in background
	go func() {
		cmd := exec.CommandContext(ctx, "kubectl", p.kubectlArgs("wait", "cluster", p.Config.ClusterName,
			"-n", p.Config.Namespace,
			"--for=condition=Ready",
			"--timeout=30m")...)
		
		output, err := cmd.CombinedOutput()
		if err != nil {
			done <- fmt.Errorf("cluster not ready: %w\n%s", err, output)
		} else {
			done <- nil
		}
	}()
	
	// Show periodic status updates
	for {
		select {
		case err := <-done:
			if err != nil {
				// Show final status on error
				fmt.Println("\n[cluster-provision] Cluster not ready. Current status:")
				statusCmd := exec.CommandContext(ctx, "kubectl", p.kubectlArgs("get", "cluster,machines,kubeadmcontrolplane",
					"-n", p.Config.Namespace, "-o", "wide")...)
				if statusOutput, _ := statusCmd.CombinedOutput(); len(statusOutput) > 0 {
					fmt.Printf("%s\n", statusOutput)
				}
				
				// Show control plane status
				fmt.Println("\n[cluster-provision] Control plane status:")
				describeCmd := exec.CommandContext(ctx, "kubectl", p.kubectlArgs("describe", "kubeadmcontrolplane",
					"-n", p.Config.Namespace)...)
				if descOutput, _ := describeCmd.CombinedOutput(); len(descOutput) > 0 {
					fmt.Printf("%s\n", descOutput)
				}
				
				return err
			}
			fmt.Println("[cluster-provision] ✓ Cluster is ready")
			return nil
			
		case <-ticker.C:
			// Show current status
			fmt.Println("\n[cluster-provision] Current status:")
			statusCmd := exec.CommandContext(ctx, "kubectl", p.kubectlArgs("get", "cluster,machines,kubeadmcontrolplane",
				"-n", p.Config.Namespace, "-o", "wide")...)
			if output, err := statusCmd.CombinedOutput(); err == nil {
				fmt.Printf("%s\n", output)
			}
		}
	}
}


func (p *Provisioner) applyCRS(ctx context.Context) error {
	manifest, err := assets.ReadManifest("addons/crs.yaml")
	if err != nil {
		return err
	}
	
	tmpl, err := template.New("crs").Parse(string(manifest))
	if err != nil {
		return err
	}
	
	// Indent manifests for YAML embedding
	ciliumIndented := indentYAML(p.Config.CiliumManifest, 4)
	ccmIndented := indentYAML(p.Config.CCMManifest, 4)
	
	data := struct {
		ClusterName     string
		Namespace       string
		HCloudToken     string
		CiliumManifest  string
		CCMManifest     string
	}{
		ClusterName:    p.Config.ClusterName,
		Namespace:      p.Config.Namespace,
		HCloudToken:    p.Config.HCloudToken,
		CiliumManifest: ciliumIndented,
		CCMManifest:    ccmIndented,
	}
	
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return err
	}
	
	cmd := exec.CommandContext(ctx, "kubectl", p.kubectlArgs("apply", "-f", "-")...)
	cmd.Stdin = &buf
	
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to apply CRS: %w\n%s", err, output)
	}
	
	return nil
}

func indentYAML(content string, spaces int) string {
	lines := bytes.Split([]byte(content), []byte("\n"))
	indent := bytes.Repeat([]byte(" "), spaces)
	
	var result []byte
	for _, line := range lines {
		if len(line) > 0 {
			result = append(result, indent...)
		}
		result = append(result, line...)
		result = append(result, '\n')
	}
	
	return string(result)
}

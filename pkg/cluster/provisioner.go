package cluster

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"text/template"

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
	
	// Generate and apply Cluster resource
	if err := p.applyCluster(ctx); err != nil {
		return err
	}
	
	return p.waitForReady(ctx)
}

func (p *Provisioner) applyClusterClass(ctx context.Context) error {
	// Select ClusterClass based on OS type
	var classFile string
	if p.Config.OSType == "flatcar" {
		classFile = "classes/hetzner-mgmt-flatcar-v1.yaml"
	} else {
		classFile = "classes/hetzner-mgmt-talos-v1.yaml"
	}
	
	manifest, err := assets.ReadManifest(classFile)
	if err != nil {
		return err
	}
	
	cmd := exec.CommandContext(ctx, "kubectl", p.kubectlArgs("apply", "-f", "-")...)
	cmd.Stdin = bytes.NewReader(manifest)
	
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to apply ClusterClass: %w\n%s", err, output)
	}
	
	return nil
}

func (p *Provisioner) applyCluster(ctx context.Context) error {
	// Select cluster class name based on OS
	className := "hetzner-mgmt-talos-v1"
	if p.Config.OSType == "flatcar" {
		className = "hetzner-mgmt-flatcar-v1"
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

func (p *Provisioner) waitForReady(ctx context.Context) error {
	fmt.Println("[cluster-provision] Waiting for cluster to be ready...")
	
	// Wait for Provisioned phase and Ready condition
	cmd := exec.CommandContext(ctx, "kubectl", p.kubectlArgs("wait", "cluster", p.Config.ClusterName,
		"-n", p.Config.Namespace,
		"--for=condition=Ready",
		"--timeout=15m")...)
	
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("cluster not ready: %w\n%s", err, output)
	}
	
	return nil
}

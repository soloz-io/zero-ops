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
	TalosVersion            string
	TalosImageID            string
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
	Config     *Config
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
	manifest, err := assets.ReadManifest("classes/hetzner-mgmt-talos-v1.yaml")
	if err != nil {
		return err
	}
	
	cmd := exec.CommandContext(ctx, "kubectl", "apply",
		"--kubeconfig", p.Kubeconfig,
		"-f", "-",
	)
	cmd.Stdin = bytes.NewReader(manifest)
	
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to apply ClusterClass: %w\n%s", err, output)
	}
	
	return nil
}

func (p *Provisioner) applyCluster(ctx context.Context) error {
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
    class: hetzner-mgmt-talos-v1
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
    - name: talosVersion
      value: {{.TalosVersion}}
    - name: talosImageId
      value: "{{.TalosImageID}}"
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
	
	cmd := exec.CommandContext(ctx, "kubectl", "apply",
		"--kubeconfig", p.Kubeconfig,
		"-f", "-",
	)
	cmd.Stdin = &buf
	
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to apply Cluster: %w\n%s", err, output)
	}
	
	return nil
}

func (p *Provisioner) waitForReady(ctx context.Context) error {
	fmt.Println("[cluster-provision] Waiting for cluster to be ready...")
	
	// Wait for Provisioned phase and Ready condition
	cmd := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", p.Kubeconfig,
		"wait", "cluster", p.Config.ClusterName,
		"-n", p.Config.Namespace,
		"--for=condition=Ready",
		"--timeout=15m",
	)
	
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("cluster not ready: %w\n%s", err, output)
	}
	
	return nil
}

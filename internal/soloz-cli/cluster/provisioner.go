package cluster

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/soloz-io/zero-ops/internal/assets"
	"github.com/soloz-io/zero-ops/internal/soloz-cli/health"
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

	// ControlPlaneSchedulable registers the hub control plane with no taints.
	// Required whenever WorkerReplicas is 0, otherwise platform workloads have
	// nowhere to run. Set only by HybridDriver (ADR-046): the hybrid hub's workers
	// are home-lab Flatcar nodes that join after the bootstrap completes.
	ControlPlaneSchedulable bool

	// GitopsDir is a checkout of the tenant's own repository, when Day-0 is
	// running from one (ADR-072). Empty for the platform's own box.
	//
	// When set, the rendered Cluster is written into clusters/<name>/generated/
	// as well as applied, so the topology the cluster runs is a file the tenant
	// can edit. Without it the Cluster exists only in the API server: the manifest
	// is rendered from Go at Day-0, `Provision` is skipped entirely once the
	// cluster reports Provisioned, and nothing reconciles the object afterwards --
	// so changing worker capacity meant an out-of-band `kubectl edit` that nothing
	// recorded and nothing would restore.
	GitopsDir string

	// CiliumOperatorReplicas overrides the replica count in the Cilium addon.
	// Zero means "leave the manifest alone", which is what every multi-node hub
	// does. A single-node hub sets 1, because the operator's hostPorts stop two
	// replicas from sharing a node.
	CiliumOperatorReplicas int

	// OIDCIssuerURL and OIDCClientID configure the API server to accept tokens
	// from the platform's identity provider, for kubectl and dashboard logins.
	//
	// Both EMPTY at bootstrap, deliberately, and the ClusterClass patch that
	// consumes them is off unless both are set. The issuer runs ON this cluster:
	// at the moment the control plane is created it does not exist and cannot be
	// pointed at, so a bootstrap that configured them would produce an API server
	// trusting an issuer that never answers.
	//
	// They are filled in afterwards, which costs one control-plane rollout and is
	// a deliberate act rather than a bootstrap that half-works. Carried here so
	// that a rebuild of an environment already running an issuer can set them at
	// creation and skip that rollout — and so the values live in configuration
	// rather than in a kubectl patch somebody has to remember.
	OIDCIssuerURL string
	OIDCClientID  string

	// SSHKeyName is the Hetzner SSH key name injected into the management
	// cluster (rescue/emergency access). Wired from the CLI --ssh-key flag.
	SSHKeyName     string
	HCloudToken    string
	CiliumManifest string
	CCMManifest    string

	// HomeWorker carries hybrid-cell home-lab worker configuration (ADR-046 §WS4).
	// Only populated by HybridDriver; zero-value means "no home workers".
	HomeWorker HomeWorkerConfig

	// SpokeAPIFront is the Tailscale MagicDNS hostname of the dedicated
	// HAProxy TCP frontend for stg/prod hybrid spokes. Empty for dev (single
	// CP node tailnet address is used directly).
	SpokeAPIFront string
}

// HomeWorkerConfig holds hybrid-cell home-lab worker settings (ADR-046 §WS4).
type HomeWorkerConfig struct {
	// Enabled activates reconcileHomeWorkerJoin in the hub-operator when true.
	Enabled bool
	// TTL is the kubeadm bootstrap-token TTL (e.g. "24h"). Default: "24h".
	TTL string
	// TailnetName is the Tailscale tailnet for MagicDNS name construction.
	TailnetName string
}

// Provisioner provisions a CAPI cluster
type Provisioner struct {
	Kubeconfig string
	Context    string
	Config     *Config
	Debug      bool
}

func (p *Provisioner) kubectlArgs(args ...string) []string {
	var result []string
	// kubectl v1.34+ bug: explicit --kubeconfig ~/.kube/config breaks context resolution
	// Only add --kubeconfig if it's NOT the default location
	homeDir, _ := os.UserHomeDir()
	defaultKubeconfig := filepath.Join(homeDir, ".kube", "config")
	if p.Kubeconfig != defaultKubeconfig {
		result = append(result, "--kubeconfig", p.Kubeconfig)
	}
	if p.Context != "" {
		result = append(result, "--context", p.Context)
	}
	return append(result, args...)
}

func (p *Provisioner) Provision(ctx context.Context) error {
	if p.Debug {
		fmt.Println("[DEBUG] Provisioner.Provision() started")
		fmt.Printf("[DEBUG] ClusterName: %s, Namespace: %s\n", p.Config.ClusterName, p.Config.Namespace)
	}
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
	rendered, err := p.renderClusterManifest()
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, "kubectl", p.kubectlArgs("apply", "-f", "-")...)
	cmd.Stdin = strings.NewReader(rendered)

	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to apply Cluster: %w\n%s", err, output)
	}

	// Applied first, then written. The apply is what creates the cluster; the file
	// is what lets the tenant change it afterwards, and a file describing a cluster
	// that failed to apply would be a topology nothing is running.
	return p.writeClusterToRepo(rendered)
}

// writeClusterToRepo records the applied topology in the tenant's repository.
//
// This is what makes worker capacity a thing a tenant changes rather than a thing
// fixed at Day-0. Editing `replicas` here and letting the tenant's own ArgoCD sync
// it is the whole mechanism -- deliberately manual, because the alternative is
// cluster-autoscaler owning the field, and CAPI rejects a topology that carries
// both `replicas` and the autoscaler's bounds annotations. Setting both wedged a
// spoke's Cluster at Synced=False until it was reverted (commit bac465a9), and a
// wedged Object blocks every later update to that cluster.
//
// A no-op without a tenant repository: the platform's own box has no file to write
// into, and Day-0 there is run by someone with the working tree in front of them.
func (p *Provisioner) writeClusterToRepo(rendered string) error {
	if p.Config.GitopsDir == "" {
		return nil
	}
	if p.Config.ClusterName == "" {
		return fmt.Errorf("cannot record the cluster topology: no cluster name, and " +
			"this file describes one cluster rather than the repository")
	}

	dir := filepath.Join(p.Config.GitopsDir, "clusters", p.Config.ClusterName, "generated")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	path := filepath.Join(dir, "hub-cluster.yaml")

	header := `# The topology this cluster runs, written by Day-0 and reconciled by this
# cluster's own control plane (ADR-045, ADR-072).
#
# TO CHANGE WORKER CAPACITY, edit replicas below and commit. That is the supported
# way to add cloud workers -- when an on-prem node leaves, for instance -- and to
# take them away again. It is deliberately manual: nothing scales this for you, so
# a node that is briefly unreachable does not become a bill.
#
# Do NOT add cluster-api-autoscaler-node-group-* annotations while replicas is set.
# CAPI rejects a topology carrying both, and the rejection is not soft: it wedges
# this object and blocks every subsequent change to the cluster.
#
# Day-0 rewrites this file on a run that provisions. It does not run again once the
# cluster reports Provisioned, so after that this file is yours.
`
	if err := os.WriteFile(path, []byte(header+rendered), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	fmt.Printf("[cluster-provision] ✓ topology recorded at clusters/%s/generated/hub-cluster.yaml\n",
		p.Config.ClusterName)
	return nil
}

// scaleCiliumOperator rewrites the cilium-operator replica count in the Cilium
// addon. replicas <= 0 returns the manifest untouched, which is the multi-node
// default; a single-node hub passes 1 because the operator's hostPorts prevent
// two replicas from sharing a node.
//
// The edit is anchored to the operator Deployment's own `spec:` block rather than
// applied globally: the addon also contains the cilium DaemonSet and several
// other resources, and a blanket replacement would corrupt them.
func scaleCiliumOperator(manifest string, replicas int) string {
	if replicas <= 0 || manifest == "" {
		return manifest
	}

	const marker = "name: cilium-operator"
	idx := strings.Index(manifest, marker)
	if idx < 0 {
		return manifest
	}

	// Only rewrite the first `replicas:` after the operator's name, and only if it
	// is close enough to belong to that Deployment.
	rest := manifest[idx:]
	rIdx := strings.Index(rest, "replicas:")
	if rIdx < 0 || rIdx > 2000 {
		return manifest
	}
	lineEnd := strings.Index(rest[rIdx:], "\n")
	if lineEnd < 0 {
		return manifest
	}

	replaced := fmt.Sprintf("replicas: %d", replicas)
	return manifest[:idx] + rest[:rIdx] + replaced + rest[rIdx+lineEnd:]
}

// ScaleCiliumOperatorForTest exposes scaleCiliumOperator to tests in sibling
// packages. Not for production use.
func ScaleCiliumOperatorForTest(manifest string, replicas int) string {
	return scaleCiliumOperator(manifest, replicas)
}

// renderClusterManifest builds the Cluster topology YAML. Split out from
// applyCluster so the rendered output can be asserted in tests: a mistake in this
// template is otherwise only visible as a kubectl rejection partway through a
// bootstrap that has already provisioned cloud infrastructure.
func (p *Provisioner) renderClusterManifest() (string, error) {
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
    - name: controlPlaneSchedulable
      value: {{.ControlPlaneSchedulable}}
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
    - name: hcloudSSHKeyName
      value: "{{.SSHKeyName}}"
    - name: oidcIssuerURL
      value: "{{.OIDCIssuerURL}}"
    - name: oidcClientID
      value: "{{.OIDCClientID}}"
`

	tmpl, err := template.New("cluster").Parse(clusterYAML)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, p.Config); err != nil {
		return "", err
	}

	return buf.String(), nil
}

// WaitForReady waits for cluster to be ready (exported for use after CNI/CCM install)
func (p *Provisioner) WaitForReady(ctx context.Context) error {
	fmt.Println("[cluster-provision] Waiting for cluster to be ready...")

	// Show initial status.
	statusCmd := exec.CommandContext(ctx, "kubectl", p.kubectlArgs("get", "cluster,machines,hcloudmachines",
		"-n", p.Config.Namespace)...)
	if output, err := statusCmd.CombinedOutput(); err == nil {
		fmt.Printf("\n%s\n", output)
	}

	// Periodic status dump on a separate goroutine — independent of the
	// health-wait polling, so operators see the cluster evolving even
	// when nothing has gone wrong yet.
	stopStatus := make(chan struct{})
	defer close(stopStatus)
	go p.periodicStatusDump(ctx, stopStatus)

	// Use the health framework to do the actual wait, then on failure
	// dump extra diagnostics (control-plane describe) for operators.
	checker := health.NewCAPIResourceReadyHealth(health.CAPIClusterKind, p.Config.ClusterName, p.Config.Namespace)
	waiter := &health.HealthWaiter{
		Checkers: []health.HealthChecker{checker},
		Interval: 30 * time.Second,
		Timeout:  30 * time.Minute,
	}
	// A kubeconfig alone does not name a cluster when the file holds several.
	// Health checks take only a path, so they resolve the file's current
	// context -- whatever the operator's shell last selected. Every other kubectl
	// call here passes --context and looked at the right cluster, so the status
	// dump reported a healthy provisioned cluster while the check beside it
	// said the resource type did not exist.
	waitKubeconfig, cleanup, err := p.pinnedKubeconfig(ctx)
	if err != nil {
		return err
	}
	defer cleanup()

	if err := waiter.Wait(ctx, waitKubeconfig); err != nil {
		p.dumpDiagnosticsOnFailure(ctx)
		return err
	}

	fmt.Println("[cluster-provision] ✓ Cluster is ready")
	return nil
}

// periodicStatusDump prints a one-line cluster state every 30s.
func (p *Provisioner) periodicStatusDump(ctx context.Context, stop <-chan struct{}) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
			fmt.Println("\n[cluster-provision] Current status:")
			cmd := exec.CommandContext(ctx, "kubectl", p.kubectlArgs("get", "cluster,machines,kubeadmcontrolplane",
				"-n", p.Config.Namespace, "-o", "wide")...)
			if output, err := cmd.CombinedOutput(); err == nil {
				fmt.Printf("%s\n", output)
			}
		}
	}
}

// dumpDiagnosticsOnFailure prints cluster + control-plane state when
// the wait fails, so operators can see why the cluster didn't converge.
func (p *Provisioner) dumpDiagnosticsOnFailure(ctx context.Context) {
	fmt.Println("\n[cluster-provision] Cluster not ready. Current status:")
	statusCmd := exec.CommandContext(ctx, "kubectl", p.kubectlArgs("get", "cluster,machines,kubeadmcontrolplane",
		"-n", p.Config.Namespace, "-o", "wide")...)
	if statusOutput, _ := statusCmd.CombinedOutput(); len(statusOutput) > 0 {
		fmt.Printf("%s\n", statusOutput)
	}

	fmt.Println("\n[cluster-provision] Control plane status:")
	describeCmd := exec.CommandContext(ctx, "kubectl", p.kubectlArgs("describe", "kubeadmcontrolplane",
		"-n", p.Config.Namespace)...)
	if descOutput, _ := describeCmd.CombinedOutput(); len(descOutput) > 0 {
		fmt.Printf("%s\n", descOutput)
	}
}

// kubeconfigPath returns the kubeconfig path used by this provisioner.
// It exists so WaitForReady's HealthWaiter call has a single source of
// truth for the kubeconfig location.
// pinnedKubeconfig returns a kubeconfig naming exactly one cluster: the context
// this provisioner was given.
//
// Returned as a temporary file rather than by adding --context to the health
// framework, because a checker receives a path and nothing else. Pinning the
// file keeps that contract while removing the ambient dependency: a path that
// identifies one cluster cannot resolve to another.
//
// With no context configured there is nothing to pin and the path is returned
// unchanged, which is the single-cluster kubeconfig every other caller passes.
func (p *Provisioner) pinnedKubeconfig(ctx context.Context) (string, func(), error) {
	noop := func() {}
	if p.Context == "" {
		return p.kubeconfigPath(), noop, nil
	}

	out, err := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", p.kubeconfigPath(),
		"--context", p.Context,
		"config", "view", "--minify", "--flatten", "-o", "yaml",
	).Output()
	if err != nil {
		return "", noop, fmt.Errorf("failed to resolve context %q in %s: %w",
			p.Context, p.kubeconfigPath(), err)
	}

	f, err := os.CreateTemp("", "hub-health-kubeconfig-*.yaml")
	if err != nil {
		return "", noop, fmt.Errorf("failed to create a pinned kubeconfig: %w", err)
	}
	cleanup := func() { os.Remove(f.Name()) }
	if _, err := f.Write(out); err != nil {
		f.Close()
		cleanup()
		return "", noop, fmt.Errorf("failed to write the pinned kubeconfig: %w", err)
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", noop, fmt.Errorf("failed to close the pinned kubeconfig: %w", err)
	}
	return f.Name(), cleanup, nil
}

func (p *Provisioner) kubeconfigPath() string {
	if p.Kubeconfig != "" {
		return p.Kubeconfig
	}
	return filepath.Join(os.Getenv("HOME"), ".kube", "config")
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

	// Read node-labeler manifest from assets
	nodeLabelerManifest, err := assets.ReadManifest("addons/node-labeler.yaml")
	if err != nil {
		return fmt.Errorf("failed to read node-labeler manifest: %w", err)
	}

	// Indent manifests for YAML embedding
	ciliumIndented := indentYAML(scaleCiliumOperator(p.Config.CiliumManifest, p.Config.CiliumOperatorReplicas), 4)
	ccmIndented := indentYAML(p.Config.CCMManifest, 4)
	nodeLabelerIndented := indentYAML(string(nodeLabelerManifest), 4)

	data := struct {
		ClusterName         string
		Namespace           string
		HCloudToken         string
		CiliumManifest      string
		CCMManifest         string
		NodeLabelerManifest string
	}{
		ClusterName:         p.Config.ClusterName,
		Namespace:           p.Config.Namespace,
		HCloudToken:         p.Config.HCloudToken,
		CiliumManifest:      ciliumIndented,
		CCMManifest:         ccmIndented,
		NodeLabelerManifest: nodeLabelerIndented,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return err
	}

	// Server-side apply, not client-side.
	//
	// Client-side apply records the entire object in the
	// kubectl.kubernetes.io/last-applied-configuration annotation, and
	// annotations are capped at 256 KiB — well under the 1 MiB Secret limit the
	// payload itself has to satisfy. The cilium addon carries the CNI plus the
	// Gateway API CRDs it requires (~700 KiB), so client-side apply fails on the
	// annotation while the Secret it is trying to write would have been legal:
	//   Secret "<cluster>-cilium-addon" is invalid: metadata.annotations:
	//   Too long: must have at most 262144 bytes
	//
	// Server-side apply keeps ownership in managedFields instead of stuffing a
	// copy of the object into its own metadata, so only the Secret limit applies.
	// --force-conflicts because this is the CLI re-asserting Day-0 ownership of
	// resources it alone writes.
	cmd := exec.CommandContext(ctx, "kubectl",
		p.kubectlArgs("apply", "--server-side", "--force-conflicts", "-f", "-")...)
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

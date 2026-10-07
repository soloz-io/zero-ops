package cluster

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"github.com/soloz-io/zero-ops/internal/soloz-cli/tenant"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/soloz-io/zero-ops/internal/assets"
	"github.com/soloz-io/zero-ops/internal/platform/escrow"
	"github.com/soloz-io/zero-ops/internal/platform/kms"
	"github.com/soloz-io/zero-ops/internal/soloz-cli/health"
)

// EncryptionMode is how a cluster encrypts Secrets at rest.
//
// ADR-003 section 6 adopted `secretbox` as the immediate control and named KMS v2 as
// the target. Both exist here because the target is not finished: ADR-100's status is
// Proposed with its real-cloud gates open, and a default that required a Google Cloud
// project would make every bootstrap depend on an unproven path.
type EncryptionMode string

const (
	// EncryptionSecretbox keeps a 32-byte key on the control-plane host, escrowed
	// because it exists nowhere else (ADR-076). Protects etcd data, backups and disk
	// snapshots; does not protect a compromised control-plane node.
	EncryptionSecretbox EncryptionMode = "secretbox"

	// EncryptionKMSv2 wraps data encryption keys with a non-exportable key in Google
	// Cloud KMS (ADR-100). Closes the compromised-node exposure, and adds the key
	// authority to the availability boundary of a cold read.
	EncryptionKMSv2 EncryptionMode = "kms-v2"
)

// KMSSettings is where the external key lives.
//
// The key NAME is not here: it is derived from the cluster name by
// kms.KeyIDFor, in one place, because two derivations would provision a cluster
// against one key and bootstrap it against another -- and the second would be created
// empty, so nothing would fail until a restore.
type KMSSettings struct {
	ProjectID     string
	ProjectNumber string
	Location      string
	KeyRing       string

	// ProviderName appears in the stored prefix as k8s:enc:kms:v2:<name>: and is what
	// proves the provider is in force.
	ProviderName string
	// SocketPath must match the plugin static pod's --socket.
	SocketPath string
	// Timeout bounds how long a Secret operation may block on the key authority.
	Timeout string
	// NoPlaintextFallback drops the `identity` provider. Never at Day-0: see the
	// template.
	NoPlaintextFallback bool
}

func (k KMSSettings) withDefaults() KMSSettings {
	if k.Location == "" {
		k.Location = "europe-west3"
	}
	if k.KeyRing == "" {
		k.KeyRing = "soloz-etcd"
	}
	if k.ProviderName == "" {
		k.ProviderName = "soloz-kms"
	}
	if k.SocketPath == "" {
		k.SocketPath = "/var/run/kms/soloz-kms.sock"
	}
	if k.Timeout == "" {
		k.Timeout = "3s"
	}
	return k
}

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

	// Encryption selects how this cluster encrypts Secrets at rest.
	//
	// EncryptionSecretbox is the default and stays the default while ADR-100's
	// completion gates are open. The two are not interchangeable at runtime: the
	// stored prefix differs, so switching mode on an existing cluster is a migration
	// with a verification step, not a flag change.
	Encryption EncryptionMode

	// KMS configures the external key authority, and is read only when Encryption is
	// EncryptionKMSv2.
	KMS KMSSettings

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

	// EscrowClient holds the at-rest encryption key outside the cluster it encrypts
	// (ADR-003 section 6, ADR-076).
	//
	// Not optional. Provisioning REFUSES without it rather than skipping the key: a
	// cluster that encrypts etcd with a key held nowhere else is worse off than one
	// that does not encrypt it, because a lost control plane then takes every Secret
	// and every backup of them. ADR-076 makes the same refusal for the secret
	// store's master keys and the reasoning is unchanged here.
	EscrowClient escrow.EscrowClient
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
	// The at-rest encryption key, BEFORE the ClusterClass that references it
	// (ADR-003 section 6). A control plane whose provider configuration Secret does
	// not exist cannot start, so this is not an optional preparatory step: the
	// cluster is created referencing a file that must already have a source.
	if err := p.applyEncryptionConfig(ctx); err != nil {
		return err
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

// renderManifest reads an embedded manifest and executes it as a template.
//
// Shared by both encryption modes so the two cannot drift in how they render or in what
// a template failure looks like.
func renderManifest(path string, data any) ([]byte, error) {
	raw, err := assets.ReadManifest(path)
	if err != nil {
		return nil, err
	}
	tmpl, err := template.New(filepath.Base(path)).Parse(string(raw))
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// applyStdin pipes a rendered manifest to kubectl.
func (p *Provisioner) applyStdin(ctx context.Context, manifest []byte) error {
	cmd := exec.CommandContext(ctx, "kubectl", p.kubectlArgs("apply", "-f", "-")...)
	cmd.Stdin = bytes.NewReader(manifest)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w\n%s", err, out)
	}
	return nil
}

// applyEncryptionConfig creates the Secret the control plane reads its at-rest
// encryption provider configuration from.
//
// TWO MODES, AND THE DEFAULT IS THE PROVEN ONE. `secretbox` (ADR-003 section 6) keeps
// the key on the control-plane host; `kms-v2` (ADR-100) puts it in Google Cloud KMS and
// keeps nothing locally. They are not interchangeable after the fact -- the stored
// prefix differs -- so switching an existing cluster is a migration with a
// verification step rather than a flag change.
func (p *Provisioner) applyEncryptionConfig(ctx context.Context) error {
	if p.Config.ClusterName == "" {
		return fmt.Errorf("at-rest encryption: no cluster name, so the per-cluster key " +
			"cannot be identified")
	}
	switch p.Config.Encryption {
	case EncryptionKMSv2:
		return p.applyKMSEncryptionConfig(ctx)
	case EncryptionSecretbox, "":
		return p.applySecretboxEncryptionConfig(ctx)
	default:
		return fmt.Errorf("at-rest encryption: unknown mode %q; expected %q or %q",
			p.Config.Encryption, EncryptionSecretbox, EncryptionKMSv2)
	}
}

// applyKMSEncryptionConfig ensures the external key exists, then points the control
// plane at the plugin.
//
// IT ESCROWS NOTHING, and that is the decision rather than an omission. ADR-076 escrows
// every root secret that cannot be regenerated; a Cloud KMS key is non-exportable, so a
// copy outside the key store is not a recovery path -- it is the exposure encryption at
// rest exists to remove. The secretbox path below escrows because its key exists nowhere
// else. This one does not because its key exists nowhere here.
//
// It therefore also does NOT require an escrow to be configured. The box still needs one
// for the artefacts that are escrowed -- the kubeconfig, the Zitadel masterkey -- and
// that is enforced where those are created, not here.
//
// THE KEY IS ENSURED, NOT ASSUMED. `soloz kms init` does the same thing and this calls
// the same code: creating a cloud key by hand is how a rotation period gets forgotten
// and an IAM binding lands at the project instead of the key. It is idempotent, so a
// rebuild or a resumed bootstrap confirms the key rather than making a second one.
func (p *Provisioner) applyKMSEncryptionConfig(ctx context.Context) error {
	cfg := p.Config.KMS.withDefaults()
	if cfg.ProjectID == "" {
		return fmt.Errorf("at-rest encryption: --encryption-mode %s needs a Google project; "+
			"pass --kms-project or put it in k8-secrets/gcp/project-id", EncryptionKMSv2)
	}

	client, err := kms.New(ctx, cfg.ProjectID, cfg.ProjectNumber, "")
	if err != nil {
		return fmt.Errorf("at-rest encryption: %w", err)
	}
	provisioned, err := client.Provision(ctx, kms.ProvisionRequest{
		Spec: kms.KeySpec{
			Location: cfg.Location,
			KeyRing:  cfg.KeyRing,
			Key:      kms.KeyIDFor(p.Config.ClusterName),
		},
		ServiceAccountID: kms.ServiceAccountIDFor(p.Config.ClusterName),
		// The API may already be enabled and the operator may not hold
		// serviceusage.services.enable. EnsureAPIEnabled reports the single command to
		// run in that case rather than failing with a bare 403.
		SkipAPIEnable: cfg.ProjectNumber == "",
	})
	if err != nil {
		return fmt.Errorf("at-rest encryption: %w", err)
	}

	rendered, err := renderManifest("secrets/secret-encryption-config-kms.yaml", map[string]any{
		"SecretName":          assets.EncryptionSecretName(p.Config.ClusterName),
		"Namespace":           p.Config.Namespace,
		"ProviderName":        cfg.ProviderName,
		"SocketPath":          cfg.SocketPath,
		"Timeout":             cfg.Timeout,
		"NoPlaintextFallback": cfg.NoPlaintextFallback,
	})
	if err != nil {
		return fmt.Errorf("at-rest encryption: %w", err)
	}
	if err := p.applyStdin(ctx, rendered); err != nil {
		return fmt.Errorf("at-rest encryption: applying the provider configuration: %w", err)
	}

	fmt.Printf("[provision] at-rest encryption: KMS v2 against %s\n", provisioned.CryptoKey)
	fmt.Printf("[provision]   identity %s, %s at the key\n",
		provisioned.ServiceAccount, kms.RoleEncrypterDecrypter)
	fmt.Println("[provision]   nothing escrowed: the key is non-exportable and a copy " +
		"outside Cloud KMS would be the exposure (ADR-076)")
	// STATED, BECAUSE THE FAILURE IS OTHERWISE THREE LAYERS FROM ITS CAUSE. The
	// provider configuration is only half of it: the control-plane template must also
	// carry the plugin static pod and its credential, which is ADR-100's completion
	// gate 3. Without them the API server starts, cannot reach the socket, and reports
	// a KMS provider that is unhealthy -- which reads as a plugin bug rather than as a
	// template that was never updated.
	fmt.Println("[provision]   the control-plane template must carry the plugin static pod " +
		"and its credential (ADR-100 gate 3)")
	return nil
}

// applySecretboxEncryptionConfig is ADR-003 section 6.
//
// RESTORE BEFORE GENERATE, and this is the only reason the key is escrowed.
//
// On a rebuild the escrow holds the key the previous cluster encrypted etcd with.
// Generating a new one here produces a cluster that starts, works, and cannot read
// a single Secret from a restored etcd — so the loss happens during the recovery,
// which is the worst moment available for it.
//
// NO ESCROW, NO KEY. Generating without somewhere outside the cluster to keep it
// produces a value that exists nowhere but inside the thing it decrypts; ADR-076
// refuses that for the secret store's master keys and the reasoning is identical
// here. The difference appears on the day the cluster is gone, and on that day the
// key cannot be added retroactively.
func (p *Provisioner) applySecretboxEncryptionConfig(ctx context.Context) error {
	if p.EscrowClient == nil {
		return fmt.Errorf("at-rest encryption key: no escrow configured, so the key would " +
			"exist nowhere but inside the cluster it decrypts and an etcd restore could " +
			"never be read (ADR-076)")
	}

	key, err := p.EscrowClient.RestoreArtifact(ctx, p.Config.ClusterName, escrow.ArtifactSecretEncryptionKey)
	if err != nil {
		// An unreadable escrow is not an absent one. Treating it as absent would mint
		// a second key for a cluster that already has one.
		return fmt.Errorf("at-rest encryption key: could not read the escrow: %w", err)
	}

	generated := false
	if key == "" {
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return fmt.Errorf("at-rest encryption key: %w", err)
		}
		key = hex.EncodeToString(raw)
		generated = true
	}

	// 32 bytes is what the provider takes; a key of any other length is rejected by
	// the API server at start, which reads as a malformed configuration rather than
	// as a wrong key length.
	rawKey, err := hex.DecodeString(key)
	if err != nil || len(rawKey) != 32 {
		return fmt.Errorf("at-rest encryption key: expected 32 hex-encoded bytes, got %d bytes (decode error: %v)",
			len(rawKey), err)
	}

	rendered, err := renderManifest("secrets/secret-encryption-config.yaml", map[string]any{
		"SecretName": assets.EncryptionSecretName(p.Config.ClusterName),
		"Namespace":  p.Config.Namespace,
		// Day-0 is always generation 1: a cluster being built has nothing written
		// under an earlier key.
		"PrimaryKeyName": assets.EncryptionKeyName(1),
		"PrimaryKeyB64":  base64.StdEncoding.EncodeToString(rawKey),
	})
	if err != nil {
		return fmt.Errorf("at-rest encryption key: %w", err)
	}
	if err := p.applyStdin(ctx, rendered); err != nil {
		return fmt.Errorf("at-rest encryption key: applying the provider configuration: %w", err)
	}

	// Escrowed AFTER the Secret exists, and only when this run generated it.
	//
	// After, because the escrow is the recovery copy of what the cluster uses: a copy
	// of a key the cluster never received would decrypt nothing. Only when generated,
	// because a restored key is already there and rewriting it is one more chance to
	// write something different.
	if generated {
		if err := p.EscrowClient.BackupArtifact(ctx, p.Config.ClusterName, escrow.ArtifactSecretEncryptionKey, key); err != nil {
			// Fatal. The cluster would come up encrypted with a key held nowhere else,
			// which is worse than not encrypting it: a lost control plane would then
			// take every Secret AND every backup of them.
			return fmt.Errorf("at-rest encryption key: generated but could not be escrowed, "+
				"so the cluster would encrypt etcd with a key that exists nowhere else: %w", err)
		}
		fmt.Println("[provision] at-rest encryption key generated and escrowed")
	} else {
		fmt.Println("[provision] at-rest encryption key restored from the escrow")
	}
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

	dir := filepath.Join(p.Config.GitopsDir, tenant.RegistryDir, "clusters", p.Config.ClusterName, "generated")
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

package cluster

import (
	"context"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// ADR-003 section 6 and ADR-100: two at-rest encryption modes, and which one a box gets
// must be a decision rather than an accident.

func TestTheDefaultModeIsSecretboxAndNotAnError(t *testing.T) {
	// EVERY EXISTING BOX HAS AN EMPTY Encryption FIELD. If an empty value were
	// rejected, or silently became KMS, adding this flag would have changed what every
	// bootstrap does -- and the change would surface as a control plane that will not
	// start, after the infrastructure exists.
	p := &Provisioner{Config: &Config{ClusterName: "nutgraf-hub", Namespace: "platform-capi"}}
	err := p.applyEncryptionConfig(context.Background())
	if err == nil {
		t.Fatal("expected the secretbox path to refuse without an escrow")
	}
	// It must fail for the ESCROW reason, which proves it took the secretbox branch.
	if !strings.Contains(err.Error(), "escrow") {
		t.Fatalf("an empty mode did not take the secretbox path: %v", err)
	}
}

func TestAnUnknownModeIsRefusedByName(t *testing.T) {
	p := &Provisioner{Config: &Config{
		ClusterName: "nutgraf-hub", Namespace: "platform-capi", Encryption: "aes-gcm",
	}}
	err := p.applyEncryptionConfig(context.Background())
	if err == nil {
		t.Fatal("an unknown encryption mode was accepted")
	}
	for _, want := range []string{"aes-gcm", "secretbox", "kms-v2"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q: %v", want, err)
		}
	}
}

func TestTheKMSPathDoesNotRequireAnEscrow(t *testing.T) {
	// THE DIFFERENCE BETWEEN THE TWO MODES, AND IT IS EASY TO GET WRONG BY INHERITANCE.
	//
	// The secretbox path refuses without an escrow because its key exists nowhere else.
	// A Cloud KMS key is non-exportable: a copy outside the key store is not a recovery
	// path, it is the exposure encryption at rest exists to remove, and ADR-076
	// deliberately does not escrow it.
	//
	// So the KMS path must not fail for the escrow reason. It fails here for the
	// missing PROJECT, which is the correct complaint.
	p := &Provisioner{Config: &Config{
		ClusterName: "nutgraf-01", Namespace: "platform-capi", Encryption: EncryptionKMSv2,
	}}
	err := p.applyEncryptionConfig(context.Background())
	if err == nil {
		t.Fatal("expected a complaint about the missing project")
	}
	if strings.Contains(err.Error(), "escrow") {
		t.Fatalf("the KMS path inherited the secretbox escrow requirement: %v", err)
	}
	if !strings.Contains(err.Error(), "project") {
		t.Fatalf("the error does not name the missing project: %v", err)
	}
}

func TestKMSSettingsDefaultToTheDecidedValues(t *testing.T) {
	// Each of these appears somewhere a mismatch is invisible until a cold read: the
	// socket path must equal the plugin static pod's --socket, and the provider name is
	// what the stored prefix carries.
	got := KMSSettings{}.withDefaults()
	for _, c := range []struct{ field, got, want string }{
		{"Location", got.Location, "europe-west3"},
		{"KeyRing", got.KeyRing, "soloz-etcd"},
		{"ProviderName", got.ProviderName, "soloz-kms"},
		{"SocketPath", got.SocketPath, "/var/run/kms/soloz-kms.sock"},
		{"Timeout", got.Timeout, "3s"},
	} {
		if c.got != c.want {
			t.Errorf("%s defaulted to %q, want %q", c.field, c.got, c.want)
		}
	}
}

// encryptionConfigFrom pulls the embedded EncryptionConfiguration out of the rendered
// Secret, so the assertions below are about the document the API server reads rather
// than about the YAML around it.
func encYAMLOf(t *testing.T, rendered []byte) string {
	t.Helper()
	var secret struct {
		StringData struct {
			EncYAML string `yaml:"enc.yaml"`
		} `yaml:"stringData"`
	}
	if err := yaml.Unmarshal(rendered, &secret); err != nil {
		t.Fatalf("the rendered Secret is not valid YAML: %v\n%s", err, rendered)
	}
	return secret.StringData.EncYAML
}

func encryptionConfigFrom(t *testing.T, rendered []byte) map[string]any {
	t.Helper()
	var secret struct {
		StringData struct {
			EncYAML string `yaml:"enc.yaml"`
		} `yaml:"stringData"`
	}
	if err := yaml.Unmarshal(rendered, &secret); err != nil {
		t.Fatalf("the rendered Secret is not valid YAML: %v\n%s", err, rendered)
	}
	if strings.TrimSpace(secret.StringData.EncYAML) == "" {
		t.Fatalf("the Secret carries no enc.yaml:\n%s", rendered)
	}
	var cfg map[string]any
	if err := yaml.Unmarshal([]byte(secret.StringData.EncYAML), &cfg); err != nil {
		t.Fatalf("enc.yaml is not valid YAML: %v\n%s", err, secret.StringData.EncYAML)
	}
	return cfg
}

func providersOf(t *testing.T, cfg map[string]any) []map[string]any {
	t.Helper()
	resources, ok := cfg["resources"].([]any)
	if !ok || len(resources) != 1 {
		t.Fatalf("expected exactly one resources entry, got %v", cfg["resources"])
	}
	entry := resources[0].(map[string]any)
	raw, ok := entry["providers"].([]any)
	if !ok {
		t.Fatalf("no providers list: %v", entry)
	}
	out := make([]map[string]any, 0, len(raw))
	for _, p := range raw {
		out = append(out, p.(map[string]any))
	}
	return out
}

func renderKMS(t *testing.T, noFallback bool) []byte {
	t.Helper()
	cfg := KMSSettings{NoPlaintextFallback: noFallback}.withDefaults()
	rendered, err := renderManifest("secrets/secret-encryption-config-kms.yaml", map[string]any{
		"SecretName":          "nutgraf-01-encryption-config",
		"Namespace":           "platform-capi",
		"ProviderName":        cfg.ProviderName,
		"SocketPath":          cfg.SocketPath,
		"Timeout":             cfg.Timeout,
		"NoPlaintextFallback": cfg.NoPlaintextFallback,
	})
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}
	return rendered
}

func TestTheKMSProviderIsFirstAndIdentitySecond(t *testing.T) {
	// ORDER IS THE WHOLE MECHANISM. The API server WRITES with the first provider and
	// can READ with any of them. `kms` first means every new write is wrapped;
	// `identity` second means Secrets kubeadm wrote during bootstrap -- before the
	// plugin existed -- stay readable. Reversed, the configuration looks right and
	// encrypts nothing.
	providers := providersOf(t, encryptionConfigFrom(t, renderKMS(t, false)))
	if len(providers) != 2 {
		t.Fatalf("expected kms then identity, got %d providers: %v", len(providers), providers)
	}
	if _, ok := providers[0]["kms"]; !ok {
		t.Fatalf("the FIRST provider is not kms, so writes are not wrapped: %v", providers[0])
	}
	if _, ok := providers[1]["identity"]; !ok {
		t.Fatalf("the second provider is not identity: %v", providers[1])
	}
}

func TestTheKMSProviderCarriesNoKeyMaterial(t *testing.T) {
	// THE POINT OF ADR-100, ASSERTED. The secretbox sibling carries 32 base64 bytes
	// that ARE the key. This file must carry a socket path and a name and nothing a
	// control-plane node or an etcd backup could leak.
	rendered := renderKMS(t, false)
	providers := providersOf(t, encryptionConfigFrom(t, rendered))
	k := providers[0]["kms"].(map[string]any)

	for _, forbidden := range []string{"secret", "keys", "key"} {
		if _, present := k[forbidden]; present {
			t.Errorf("the kms provider carries a %q field; it must hold no key material", forbidden)
		}
	}
	allowed := map[string]bool{"apiVersion": true, "name": true, "endpoint": true, "timeout": true}
	for field := range k {
		if !allowed[field] {
			t.Errorf("unexpected field %q in the kms provider", field)
		}
	}
	// cachesize is not valid for a v2 provider, and setting it is accepted by YAML and
	// rejected by the API server at start.
	//
	// CHECKED AGAINST enc.yaml AND NOT THE WHOLE MANIFEST. The first version of this
	// scanned the rendered Secret, which includes the template's own comments -- one of
	// which explains why cachesize is absent. It failed on its own documentation. The
	// assertion belongs on the document the API server reads.
	if strings.Contains(encYAMLOf(t, rendered), "cachesize") {
		t.Error("cachesize is present and is not valid for a v2 provider")
	}
	if k["apiVersion"] != "v2" {
		t.Errorf("apiVersion is %v, want v2", k["apiVersion"])
	}
	if k["endpoint"] != "unix:///var/run/kms/soloz-kms.sock" {
		t.Errorf("endpoint is %v; it must match the plugin static pod's --socket", k["endpoint"])
	}
	if k["name"] != "soloz-kms" {
		t.Errorf("name is %v, and it is what the stored prefix k8s:enc:kms:v2:<name>: carries",
			k["name"])
	}
}

func TestDroppingThePlaintextFallbackRemovesIdentityAndNothingElse(t *testing.T) {
	// The dangerous edit is the one that drops identity: afterwards, a Secret the
	// migration missed is UNREADABLE, and unlike a retired secretbox key there is
	// nothing to re-add because the object was never wrapped.
	providers := providersOf(t, encryptionConfigFrom(t, renderKMS(t, true)))
	if len(providers) != 1 {
		t.Fatalf("expected only the kms provider, got %v", providers)
	}
	if _, ok := providers[0]["kms"]; !ok {
		t.Fatalf("the remaining provider is not kms: %v", providers[0])
	}
}

func TestBothModesRenderTheSameSecretNameAndNamespace(t *testing.T) {
	// The ClusterClass patch references this Secret by name. If the two modes named it
	// differently, switching mode would leave the control plane reading a Secret that
	// does not exist -- and the patch is what mounts it, so the failure is a control
	// plane that will not start.
	var secret struct {
		Metadata struct {
			Name      string `yaml:"name"`
			Namespace string `yaml:"namespace"`
		} `yaml:"metadata"`
	}
	if err := yaml.Unmarshal(renderKMS(t, false), &secret); err != nil {
		t.Fatal(err)
	}
	if secret.Metadata.Name != "nutgraf-01-encryption-config" {
		t.Errorf("name is %q", secret.Metadata.Name)
	}
	if secret.Metadata.Namespace != "platform-capi" {
		t.Errorf("namespace is %q", secret.Metadata.Namespace)
	}
}

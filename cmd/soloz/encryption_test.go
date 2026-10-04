package main

import (
	"regexp"
	"strings"
	"testing"

	"github.com/soloz-io/zero-ops/internal/assets"
)

// The command exists because nothing reconciles at-rest encryption onto an existing
// cluster: both ClusterClasses are applied from the CLI's embedded assets, so a
// release puts neither on a running cluster. These pin the two things that would
// make it useless — not finding a class, and finding the wrong one.

func TestBothClusterClassesAreReachable(t *testing.T) {
	// They live in DIFFERENT embeds: the CLI's own assets hold the management
	// cluster's, the packaged platform manifests hold a workload cluster's. A single
	// accessor would silently miss whichever tree it did not look in, and the command
	// would then work for one cluster and fail for the other with "file not found".
	for _, path := range []string{defaultHubClass, defaultSpokeClass} {
		body, err := readClusterClass(path)
		if err != nil {
			t.Fatalf("readClusterClass(%q): %v", path, err)
		}
		if !strings.Contains(string(body), "kind: ClusterClass") {
			t.Fatalf("%q does not contain a ClusterClass", path)
		}
	}
}

func TestBothClassesDeliverTheProviderConfigButDoNotYetRequireIt(t *testing.T) {
	// THE v3 CONTRACT, AND THIS TEST IS WHERE IT IS RECORDED.
	//
	// At-rest encryption needs three things on a control-plane node: the key as a
	// Secret, the file mounted into the API server's static pod, and the
	// `encryption-provider-config` argument that makes the API server read it. The
	// argument is the one that can break the cluster -- a path it cannot read is a
	// control plane that does not start -- and it only takes effect when a node is
	// replaced.
	//
	// v2 shipped all three at once, so the first evidence that the file had rendered
	// correctly would have been an API server that came up, or one that did not. v3
	// ships the mount and the file and NOTHING THAT READS THEM: a node that comes up
	// with the file missing or misnamed is an ordinary healthy node, and `ls` on it is
	// the evidence. The argument arrives in v4.
	//
	// So the argument's ABSENCE is asserted. When v4 lands, this test is the thing
	// that has to be updated, which is the point: the phase change is deliberate and
	// recorded here rather than discovered during a roll.
	// See docs/runbooks/encrypt-secrets-at-rest.md.
	argLine := regexp.MustCompile(`(?m)^\s*encryption-provider-config:\s*\S`)
	mountLine := regexp.MustCompile(`(?m)^\s*mountPath:\s*/etc/kubernetes/enc\s*$`)
	// The name the control plane READS. Built inside CAPI from the cluster's own name,
	// because a ClusterClass is shared by every cluster of its class.
	perCluster := `"name": "{{ .builtin.cluster.name }}` + assets.EncryptionSecretName("")

	for _, path := range []string{defaultHubClass, defaultSpokeClass} {
		body, err := readClusterClass(path)
		if err != nil {
			t.Fatalf("readClusterClass(%q): %v", path, err)
		}
		text := string(body)

		if argLine.MatchString(text) {
			t.Errorf("%q sets encryption-provider-config. v3 deliberately omits it so the file "+
				"can be verified on a real node before the API server depends on it. If this is "+
				"the v4 change, update this test and the runbook together", path)
		}
		if !mountLine.MatchString(text) {
			t.Errorf("%q does not mount /etc/kubernetes/enc into the API server; it runs as a "+
				"static pod and cannot see a path kubeadm does not mount", path)
		}
		if !strings.Contains(text, perCluster) {
			t.Errorf("%q does not deliver the provider configuration under a per-cluster name "+
				"(%s...); a Secret named literally in a ClusterClass is one object for every "+
				"cluster of that class", path, perCluster)
		}
	}
}

func TestTheDay0TemplateTakesItsNameRatherThanSpellingOne(t *testing.T) {
	// The other half of the same name. The Go side CREATES the Secret and the CAPI
	// patch READS it, and the two are written in languages that cannot see each other
	// -- so a disagreement produces a Secret under one name referenced under another,
	// which CAPI reports as a node that never finishes bootstrapping rather than as a
	// name it could not find.
	body, err := readEncryptionConfigTemplate()
	if err != nil {
		t.Fatalf("readEncryptionConfigTemplate: %v", err)
	}
	if !strings.Contains(string(body), "name: {{ .SecretName }}") {
		t.Fatal("the Day-0 Secret template spells a name instead of taking {{ .SecretName }}; " +
			"a literal here is one object for every cluster the management plane builds")
	}
	if got := assets.EncryptionSecretName("nutgraf-01"); got != "nutgraf-01-encryption-config" {
		t.Fatalf("EncryptionSecretName changed shape to %q; the ClusterClass patches build the "+
			"same name from {{ .builtin.cluster.name }} and preflight 88 pins them together", got)
	}
}

func TestAMissingClassNamesBothTreesItLookedIn(t *testing.T) {
	// A "not found" naming one tree sends the next person looking in the wrong place.
	_, err := readClusterClass("classes/does-not-exist.yaml")
	if err == nil {
		t.Fatal("expected an error for a class that does not exist")
	}
	for _, want := range []string{"CLI assets", "platform manifests"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error does not mention %q: %v", want, err)
		}
	}
}

func TestTheProviderConfigTemplateRendersBothProvidersInOrder(t *testing.T) {
	// secretbox first so every WRITE is encrypted; the plaintext provider second so
	// existing reads still succeed. Reversed, nothing is ever encrypted and the
	// cluster looks configured.
	body, err := readEncryptionConfigTemplate()
	if err != nil {
		t.Fatalf("reading the template: %v", err)
	}

	// Comments are stripped first. The header explains WHY the plaintext provider is
	// retained, so a raw string search finds the word "identity" above the providers
	// and reports an order that is not the file's — which is what the first version of
	// this test did.
	var providers []string
	for _, line := range strings.Split(string(body), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "#") {
			continue
		}
		switch {
		case strings.HasPrefix(t, "- secretbox:"):
			providers = append(providers, "secretbox")
		case strings.HasPrefix(t, "- identity:"):
			providers = append(providers, "identity")
		}
	}

	if len(providers) != 2 {
		t.Fatalf("expected exactly two providers, got %v", providers)
	}
	if providers[0] != "secretbox" {
		t.Fatalf("provider order is %v; secretbox must be FIRST or writes are never encrypted", providers)
	}
	if providers[1] != "identity" {
		t.Fatalf("provider order is %v; the plaintext provider must be second so existing reads still succeed", providers)
	}
}

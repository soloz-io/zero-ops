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

func TestBothClassesDeliverTheProviderConfigAndNowRequireIt(t *testing.T) {
	// THE v4 CONTRACT. This test asserted the argument's ABSENCE until 2026-10-05,
	// and flipping it is the recorded phase change -- deliberately a code change, so
	// the boundary could not move by accident.
	//
	// What cleared it: the v3 roll on nutgraf-01 replaced the control-plane node, the
	// node self-assigned providerID hcloud://168731792 with no manual patch, and
	// /etc/kubernetes/enc/enc.yaml was present as root-owned 0600 listing secretbox
	// (key1) before identity. The render was observed on a real replaced node before
	// any API server was made to depend on it, which is what v3 existed to buy.
	//
	// All three pieces are now required together, and that triple is the point: the
	// argument without the mount is an API server that cannot see a file that exists;
	// the argument without a per-cluster Secret is a node that never finishes
	// bootstrapping; the mount and Secret without the argument is v3, which encrypts
	// nothing.
	argLine := regexp.MustCompile(`(?m)^\s*encryption-provider-config:\s*/etc/kubernetes/enc/enc\.yaml\s*$`)
	mountLine := regexp.MustCompile(`(?m)^\s*mountPath:\s*/etc/kubernetes/enc\s*$`)
	perCluster := `"name": "{{ .builtin.cluster.name }}` + assets.EncryptionSecretName("")

	for _, path := range []string{defaultHubClass, defaultSpokeClass} {
		body, err := readClusterClass(path)
		if err != nil {
			t.Fatalf("readClusterClass(%q): %v", path, err)
		}
		text := string(body)

		if !argLine.MatchString(text) {
			t.Errorf("%q does not set encryption-provider-config, so adopting it rolls the "+
				"control plane and encrypts nothing -- the most expensive possible no-op", path)
		}
		if !mountLine.MatchString(text) {
			t.Errorf("%q does not mount /etc/kubernetes/enc into the API server. With the "+
				"argument set and no mount, the API server fails to start on a file that "+
				"EXISTS on the node, which reads as a missing file rather than a missing mount", path)
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

func TestTheClusterClassIsNotAppliedByDefault(t *testing.T) {
	// THE DEFECT THIS PINS. The command applied the ClusterClass unconditionally, on
	// the belief that nothing reconciled it. ArgoCD's infrastructure-provider
	// ApplicationSet syncs manifests/providers/<provider>, whose kustomization pulls
	// in base/ and with it the spoke ClusterClass -- so promoting a bundle puts the
	// class on the box, and 0.1.16-rc.142 did exactly that with nobody running this
	// command.
	//
	// Applying it from here makes the CLI a second writer for a GitOps-owned object.
	// Byte-identical content hides it; the day the two disagree, whoever ran last
	// wins until the next sync, which reverts it and says nothing.
	//
	// Asserted on the FLAG's default rather than by running the command, because the
	// apply path needs a cluster. The flag is the decision; --apply-class exists only
	// for a Day-0 bootstrap cluster that has no ArgoCD yet.
	cmd := newEncryptionCmd()
	sub, _, err := cmd.Find([]string{"enable"})
	if err != nil {
		t.Fatalf("finding `enable`: %v", err)
	}
	f := sub.Flags().Lookup("apply-class")
	if f == nil {
		t.Fatal("--apply-class is gone; if applying the class became unconditional again, " +
			"the CLI is a second writer for an object ArgoCD syncs")
	}
	if f.DefValue != "false" {
		t.Fatalf("--apply-class defaults to %q, so the CLI applies a GitOps-owned "+
			"ClusterClass unless told not to; it must be opt-in", f.DefValue)
	}
}

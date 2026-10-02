package main

import (
	"strings"
	"testing"
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

func TestBothClassesCarryTheEncryptionProvider(t *testing.T) {
	// The whole point of applying a class here. A class without the argument rolls the
	// control plane for nothing, which is the most expensive possible no-op.
	for _, path := range []string{defaultHubClass, defaultSpokeClass} {
		body, err := readClusterClass(path)
		if err != nil {
			t.Fatalf("readClusterClass(%q): %v", path, err)
		}
		if !strings.Contains(string(body), "encryption-provider-config") {
			t.Fatalf("%q carries no encryption-provider-config; applying it would roll the "+
				"control plane and change nothing", path)
		}
		// The file has to be mounted into the static pod, or the API server fails to
		// start on a path that exists on the node.
		if !strings.Contains(string(body), "secret-encryption-config") {
			t.Fatalf("%q does not reference the provider configuration Secret", path)
		}
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

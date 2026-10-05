package keyid

import (
	"strings"
	"testing"
)

// ADR-100 acceptance criterion 1: derived, deterministic and durable.

func TestTheSameVersionAlwaysProducesTheSameIdentifier(t *testing.T) {
	// The criterion this pins: the same key version MUST produce the same identifier on
	// every control-plane node and across every restart. If it did not, the interface
	// would read a changed identifier as a changed key and the API server would
	// establish new encryption state on every restart while nothing had rotated.
	r := Ref{Cluster: "nutgraf-01", Key: "etcd-kek", Version: 1}
	first := r.String()
	for i := 0; i < 100; i++ {
		if got := (Ref{Cluster: "nutgraf-01", Key: "etcd-kek", Version: 1}).String(); got != first {
			t.Fatalf("identifier for the same Ref varied: %q then %q -- it is not derived "+
				"purely from the Ref", first, got)
		}
	}
}

func TestEveryComponentChangesTheIdentifier(t *testing.T) {
	// Each component is part of the identity, so a change in any of them must produce a
	// different identifier. Version is the one that matters for rotation; cluster is
	// what keeps two clusters' version 1 from colliding.
	base := Ref{Cluster: "nutgraf-01", Key: "etcd-kek", Version: 1}
	for _, c := range []struct {
		what string
		ref  Ref
	}{
		{"version", Ref{Cluster: "nutgraf-01", Key: "etcd-kek", Version: 2}},
		{"cluster", Ref{Cluster: "nutgraf-hub", Key: "etcd-kek", Version: 1}},
		{"key", Ref{Cluster: "nutgraf-01", Key: "other-kek", Version: 1}},
	} {
		if c.ref.String() == base.String() {
			t.Errorf("changing the %s did not change the identifier (%q); a rotation or a "+
				"different cluster would be invisible to the API server", c.what, base.String())
		}
	}
}

func TestTheEncodingIsInjective(t *testing.T) {
	// Two different Refs must never produce one identifier. Without the character
	// constraint, a key named "a/v1" under cluster "c" and a key named "a" at version 1
	// under cluster "c" would both render "c/a/v1" -- two keys, one identity, and data
	// wrapped under one reported as the other.
	bad := Ref{Cluster: "c", Key: "a/v1", Version: 1}
	if err := bad.Validate(); err == nil {
		t.Fatal("a key containing the separator was accepted; two different keys could then " +
			"produce the same identifier")
	}
}

func TestAnUnreadVersionIsRefused(t *testing.T) {
	// Version 0 means the active version was never read. Reporting an identifier for it
	// would claim an effective key the plugin has not observed -- the precise silent
	// failure this package exists to prevent.
	err := Ref{Cluster: "c", Key: "k", Version: 0}.Validate()
	if err == nil {
		t.Fatal("version 0 was accepted")
	}
	if !strings.Contains(err.Error(), "never read") {
		t.Fatalf("the error does not say why version 0 is wrong: %v", err)
	}
}

func TestValidateReportsEveryProblemAtOnce(t *testing.T) {
	// Naming one problem at a time turns a misconfiguration into several rounds of
	// deploy-and-read-the-log, on a component that sits in the API server's start path.
	err := Ref{Cluster: "", Key: "bad key", Version: 0}.Validate()
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"cluster", "key", "version"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
}

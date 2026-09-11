package escrow

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// A box must not escrow into its own Infisical.
//
// The master keys being escrowed are the keys that decrypt that Infisical, and it
// is unreachable exactly when they are needed — so a box pointed at itself has no
// escrow while appearing to have one. Refused rather than accepted, because the
// difference only becomes visible on the day it matters.
func TestEscrowRefusesTheBoxItProtects(t *testing.T) {
	const inCluster = "http://infisical-standalone-infisical.platform-security.svc.cluster.local:8080"
	withEnv(t, map[string]string{
		"INFISICAL_ESCROW_URL":           inCluster,
		"INFISICAL_ESCROW_PROJECT_ID":    "p",
		"INFISICAL_ESCROW_CLIENT_ID":     "i",
		"INFISICAL_ESCROW_CLIENT_SECRET": "s",
	})

	_, err := NewEscrowClient(context.Background(), inCluster)
	if err == nil {
		t.Fatal("a box escrowing into its own Infisical was accepted")
	}
	if !strings.Contains(err.Error(), "not an escrow") {
		t.Errorf("the refusal does not say why:\n%v", err)
	}

	// A trailing slash is the same instance, and comparing strings naively would
	// let it through.
	withEnv(t, map[string]string{"INFISICAL_ESCROW_URL": inCluster + "/"})
	if _, err := NewEscrowClient(context.Background(), inCluster); err == nil {
		t.Error("a trailing slash defeated the check")
	}
}

// Complete or absent, never partial. A partial set produces a caller that attempts
// a backup on every reconcile and fails — an escrow that appears to exist.
func TestEscrowIsCompleteOrAbsent(t *testing.T) {
	withEnv(t, map[string]string{})
	if _, err := NewEscrowClient(context.Background(), ""); err == nil ||
		!strings.Contains(err.Error(), "no escrow configured") {
		t.Errorf("an unconfigured escrow was not reported as absent: %v", err)
	}

	withEnv(t, map[string]string{
		"INFISICAL_ESCROW_URL":       "https://app.infisical.com",
		"INFISICAL_ESCROW_CLIENT_ID": "i",
	})
	_, err := NewEscrowClient(context.Background(), "")
	if err == nil {
		t.Fatal("a partially configured escrow was accepted")
	}
	for _, want := range []string{"INFISICAL_ESCROW_PROJECT_ID", "INFISICAL_ESCROW_CLIENT_SECRET"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not name the missing %s:\n%v", want, err)
		}
	}
}

// What is written must be what comes back, and a cluster that has never been
// escrowed must read as empty rather than as an error — the caller treats those
// differently, and confusing them would overwrite a backup that exists.
func TestBackupAndRestoreRoundTrip(t *testing.T) {
	store := map[string]string{}
	srv := fakeInfisical(t, store)
	defer srv.Close()

	withEnv(t, map[string]string{
		"INFISICAL_ESCROW_URL":           srv.URL,
		"INFISICAL_ESCROW_PROJECT_ID":    "proj",
		"INFISICAL_ESCROW_CLIENT_ID":     "id",
		"INFISICAL_ESCROW_CLIENT_SECRET": "secret",
	})

	e, err := NewEscrowClient(context.Background(), "")
	if err != nil {
		t.Fatalf("NewEscrowClient: %v", err)
	}
	ctx := context.Background()

	if got, err := e.RestoreArtifact(ctx, "devbox", ArtifactKubeconfig); err != nil || got != "" {
		t.Fatalf("an un-escrowed cluster read as %q, %v; want empty and no error", got, err)
	}

	if err := e.BackupArtifact(ctx, "devbox", ArtifactKubeconfig, "apiVersion: v1"); err != nil {
		t.Fatalf("BackupArtifact: %v", err)
	}
	got, err := e.RestoreArtifact(ctx, "devbox", ArtifactKubeconfig)
	if err != nil || got != "apiVersion: v1" {
		t.Fatalf("round trip returned %q, %v", got, err)
	}

	// The second backup of a cluster is the normal case, not a conflict.
	if err := e.BackupArtifact(ctx, "devbox", ArtifactKubeconfig, "apiVersion: v2"); err != nil {
		t.Fatalf("re-escrowing failed: %v", err)
	}
	if got, _ := e.RestoreArtifact(ctx, "devbox", ArtifactKubeconfig); got != "apiVersion: v2" {
		t.Errorf("re-escrow left %q", got)
	}

	// Clusters must not collide.
	if got, _ := e.RestoreArtifact(ctx, "other", ArtifactKubeconfig); got != "" {
		t.Errorf("another cluster's escrow returned %q", got)
	}
}

// fakeInfisical is the smallest server that behaves like the parts used here.
//
// It models the two refusals a real Infisical makes and a permissive fake would
// hide: a write to a path no folder exists at is a 404, and a create over an
// existing secret is a conflict.
func fakeInfisical(t *testing.T, store map[string]string) *httptest.Server {
	t.Helper()
	folders := map[string]bool{"": true, "/": true}
	key := func(r *http.Request) string {
		return r.URL.Query().Get("secretPath") + "/" + strings.TrimPrefix(r.URL.Path, "/api/v3/secrets/raw/")
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == pathLogin:
			json.NewEncoder(w).Encode(map[string]string{"accessToken": "t"})

		// Creating a folder fills in any missing parent of its path, and a folder
		// that already exists comes back as a 400.
		case r.URL.Path == pathFolders && r.Method == http.MethodPost:
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			full := strings.TrimSuffix(body["path"], "/") + "/" + body["name"]
			if folders[full] {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			acc := ""
			for _, seg := range strings.Split(strings.Trim(full, "/"), "/") {
				acc += "/" + seg
				folders[acc] = true
			}
			w.WriteHeader(http.StatusOK)

		case r.Method == http.MethodGet:
			v, ok := store[key(r)]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"secret": map[string]string{"secretValue": v}})

		case r.Method == http.MethodPost, r.Method == http.MethodPatch:
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			// Infisical does not create a secret path on write.
			if !folders[body["secretPath"]] {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			k := body["secretPath"] + "/" + strings.TrimPrefix(r.URL.Path, "/api/v3/secrets/raw/")
			// Infisical refuses a create over an existing secret; the client is
			// expected to fall back to a patch.
			if _, exists := store[k]; exists && r.Method == http.MethodPost {
				w.WriteHeader(http.StatusConflict)
				return
			}
			store[k] = body["secretValue"]
			w.WriteHeader(http.StatusOK)

		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
}

func withEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for _, name := range []string{
		"INFISICAL_ESCROW_URL", "INFISICAL_ESCROW_PROJECT_ID",
		"INFISICAL_ESCROW_CLIENT_ID", "INFISICAL_ESCROW_CLIENT_SECRET",
	} {
		os.Unsetenv(name)
	}
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

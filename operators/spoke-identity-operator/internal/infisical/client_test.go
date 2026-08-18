package infisical

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &Client{
		BaseURL:      srv.URL,
		clientID:     "op-client",
		clientSecret: "op-secret",
		HTTP:         srv.Client(),
	}
}

func TestProbeIdentityLockoutHealthy(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != PathAuthUniversalAuthLogin {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"accessToken":"t","expiresIn":60}`))
	})

	locked, err := c.ProbeIdentityLockout(context.Background(), "spoke-client", "spoke-secret")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if locked {
		t.Fatal("expected locked=false for a healthy login")
	}
}

func TestProbeIdentityLockoutLocked(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"This identity auth method is temporarily locked out due to multiple failed login attempts."}`))
	})

	locked, err := c.ProbeIdentityLockout(context.Background(), "spoke-client", "spoke-secret")
	if err != nil {
		t.Fatalf("expected nil error for locked state, got %v", err)
	}
	if !locked {
		t.Fatal("expected locked=true")
	}
}

func TestProbeIdentityLockoutInvalidCredentials(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Invalid credentials"}`))
	})

	_, err := c.ProbeIdentityLockout(context.Background(), "spoke-client", "wrong")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestProbeIdentityLockoutServerError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"boom"}`))
	})

	_, err := c.ProbeIdentityLockout(context.Background(), "spoke-client", "spoke-secret")
	if err == nil {
		t.Fatal("expected error for 500")
	}
	if errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("500 must not classify as invalid credentials: %v", err)
	}
}

func TestClearLockoutSuccess(t *testing.T) {
	var sawLogin, sawClear bool
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case PathAuthUniversalAuthLogin:
			sawLogin = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"accessToken":"op-token","expiresIn":60}`))
		case PathAuthUniversalAuthClearLockout:
			sawClear = true
			var body map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode clear body: %v", err)
			}
			if body["identityId"] != "identity-1" || body["clientId"] != "spoke-client" || body["lockedOut"] != false {
				t.Errorf("unexpected clear payload: %v", body)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer op-token" {
				t.Errorf("expected admin bearer token, got %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"deleted":1}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})

	if err := c.ClearLockout(context.Background(), "identity-1", "spoke-client"); err != nil {
		t.Fatalf("clear lockout: %v", err)
	}
	if !sawLogin || !sawClear {
		t.Fatalf("expected login then clear; sawLogin=%v sawClear=%v", sawLogin, sawClear)
	}
}

func TestClearLockoutIdempotentWhenAlreadyUnlocked(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case PathAuthUniversalAuthLogin:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"accessToken":"op-token","expiresIn":60}`))
		case PathAuthUniversalAuthClearLockout:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"Identity auth method is not locked out"}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})

	if err := c.ClearLockout(context.Background(), "identity-1", "spoke-client"); err != nil {
		t.Fatalf("already-unlocked clear must be idempotent, got %v", err)
	}
}

func TestClearLockoutFailure(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case PathAuthUniversalAuthLogin:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"accessToken":"op-token","expiresIn":60}`))
		case PathAuthUniversalAuthClearLockout:
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"boom"}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})

	err := c.ClearLockout(context.Background(), "identity-1", "spoke-client")
	if err == nil {
		t.Fatal("expected error for 500")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("error should mention status: %v", err)
	}
}

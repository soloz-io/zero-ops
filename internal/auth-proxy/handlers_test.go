package authproxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHandler_ProxyOpenIDConfiguration(t *testing.T) {
	mockHydra := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			t.Errorf("expected path /.well-known/openid-configuration, got %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"issuer":"https://auth.dev.nutgraf.in"}`))
	}))
	defer mockHydra.Close()

	handler := NewHandler(
		mockHydra.URL,
		mockHydra.URL,
		mockHydra.URL,
		mockHydra.URL,
		5*time.Second,
		"mcp-public-client",
		"https://api.dev.nutgraf.in",
		"https://auth.dev.nutgraf.in",
		"https://api.dev.nutgraf.in",
		"https://console.dev.nutgraf.in",
	)

	req := httptest.NewRequest("GET", "/.well-known/openid-configuration", nil)
	rec := httptest.NewRecorder()

	handler.ProxyOpenIDConfiguration(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["issuer"] != "https://auth.dev.nutgraf.in" {
		t.Errorf("expected issuer https://auth.dev.nutgraf.in, got %v", resp["issuer"])
	}
}

func TestHandler_ProxyUserinfo(t *testing.T) {
	mockHydra := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/userinfo" {
			t.Errorf("expected path /userinfo, got %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"sub":"user-123"}`))
	}))
	defer mockHydra.Close()

	handler := NewHandler(
		mockHydra.URL,
		mockHydra.URL,
		mockHydra.URL,
		mockHydra.URL,
		5*time.Second,
		"mcp-public-client",
		"https://api.dev.nutgraf.in",
		"https://auth.dev.nutgraf.in",
		"https://api.dev.nutgraf.in",
		"https://console.dev.nutgraf.in",
	)

	req := httptest.NewRequest("GET", "/userinfo", nil)
	rec := httptest.NewRecorder()

	handler.ProxyUserinfo(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["sub"] != "user-123" {
		t.Errorf("expected sub user-123, got %v", resp["sub"])
	}
}

func TestHandler_ServeAuthServerMetadata(t *testing.T) {
	handler := NewHandler(
		"http://hydra:4444",
		"http://hydra:4445",
		"http://kratos:80",
		"http://kratos:80",
		5*time.Second,
		"mcp-public-client",
		"https://api.dev.nutgraf.in",
		"https://auth.dev.nutgraf.in",
		"https://api.dev.nutgraf.in",
		"https://console.dev.nutgraf.in",
	)

	req := httptest.NewRequest("GET", "/.well-known/oauth-authorization-server", nil)
	rec := httptest.NewRecorder()

	handler.ServeAuthServerMetadata(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["issuer"] != "https://auth.dev.nutgraf.in" {
		t.Errorf("expected issuer https://auth.dev.nutgraf.in, got %v", resp["issuer"])
	}
	if resp["token_endpoint"] != "https://auth.dev.nutgraf.in/oauth2/token" {
		t.Errorf("expected token_endpoint https://auth.dev.nutgraf.in/oauth2/token, got %v", resp["token_endpoint"])
	}
}

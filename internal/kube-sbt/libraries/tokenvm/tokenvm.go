// Package tokenvm provides tenant-scoped credential retrieval from Kubernetes
// Secrets (Tasks 22.1–22.6).
package tokenvm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// Credentials holds tenant-scoped access credentials (22.1).
type Credentials struct {
	AccessKey    string    `json:"access_key"`
	SecretKey    string    `json:"secret_key"`
	Endpoint     string    `json:"endpoint"`
	SessionToken string    `json:"session_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// TVM is the Token Vending Machine — retrieves tenant-scoped credentials
// from Kubernetes Secrets via the K8s API (22.2).
type TVM struct {
	k8sAPIURL string // e.g. https://kubernetes.default.svc
	k8sToken  string // service-account token

	mu    sync.RWMutex
	cache map[string]*Credentials // key: tenantID+":"+resourceType
	ttl   time.Duration

	httpClient *http.Client
}

// New creates a TVM. credTTL controls how long credentials are cached (22.3).
func New(k8sAPIURL, k8sToken string, credTTL time.Duration) *TVM {
	if credTTL == 0 {
		credTTL = time.Hour
	}
	return &TVM{
		k8sAPIURL:  k8sAPIURL,
		k8sToken:   k8sToken,
		cache:      make(map[string]*Credentials),
		ttl:        credTTL,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// GetCredentials returns tenant-scoped credentials for a resource type (22.1).
// resourceType: "s3" or "database".
func (t *TVM) GetCredentials(ctx context.Context, tenantID, resourceType string) (*Credentials, error) {
	key := tenantID + ":" + resourceType
	if c := t.fromCache(key); c != nil {
		return c, nil
	}
	var creds *Credentials
	var err error
	switch resourceType {
	case "s3":
		creds, err = t.fetchS3Credentials(ctx, tenantID) // 22.4
	case "database":
		creds, err = t.fetchDatabaseCredentials(ctx, tenantID) // 22.5
	default:
		return nil, fmt.Errorf("tokenvm: unsupported resource type %q", resourceType)
	}
	if err != nil {
		return nil, err
	}
	t.toCache(key, creds)
	return creds, nil
}

// RotateCredentials clears the cache for a tenant+resource, forcing re-fetch (22.6).
func (t *TVM) RotateCredentials(tenantID, resourceType string) {
	t.mu.Lock()
	delete(t.cache, tenantID+":"+resourceType)
	t.mu.Unlock()
}

// fetchS3Credentials reads the tenant S3 secret from Kubernetes (22.2, 22.4).
func (t *TVM) fetchS3Credentials(ctx context.Context, tenantID string) (*Credentials, error) {
	data, err := t.readK8sSecret(ctx,
		fmt.Sprintf("tenant-%s", tenantID),
		fmt.Sprintf("tenant-%s-s3-creds", tenantID))
	if err != nil {
		return nil, err
	}
	return &Credentials{
		AccessKey: string(data["access_key"]),
		SecretKey: string(data["secret_key"]),
		Endpoint:  string(data["endpoint"]),
		ExpiresAt: time.Now().Add(t.ttl),
	}, nil
}

// fetchDatabaseCredentials reads the tenant DB secret from Kubernetes (22.2, 22.5).
func (t *TVM) fetchDatabaseCredentials(ctx context.Context, tenantID string) (*Credentials, error) {
	data, err := t.readK8sSecret(ctx,
		fmt.Sprintf("tenant-%s", tenantID),
		fmt.Sprintf("tenant-%s-db-creds", tenantID))
	if err != nil {
		return nil, err
	}
	return &Credentials{
		AccessKey: string(data["username"]),
		SecretKey: string(data["password"]),
		Endpoint:  string(data["host"]),
		ExpiresAt: time.Now().Add(t.ttl),
	}, nil
}

// readK8sSecret fetches a Kubernetes Secret via the API server (22.2).
func (t *TVM) readK8sSecret(ctx context.Context, namespace, name string) (map[string][]byte, error) {
	url := fmt.Sprintf("%s/api/v1/namespaces/%s/secrets/%s", t.k8sAPIURL, namespace, name)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+t.k8sToken)
	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tokenvm: k8s secret %s/%s: %w", namespace, name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("tokenvm: k8s secret %s/%s: HTTP %d: %s", namespace, name, resp.StatusCode, body)
	}
	var secret struct {
		Data map[string][]byte `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&secret); err != nil {
		return nil, err
	}
	return secret.Data, nil
}

func (t *TVM) fromCache(key string) *Credentials {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if c, ok := t.cache[key]; ok && time.Now().Before(c.ExpiresAt) {
		cp := *c
		return &cp
	}
	return nil
}

func (t *TVM) toCache(key string, c *Credentials) {
	t.mu.Lock()
	t.cache[key] = c
	t.mu.Unlock()
}

package ory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/soloz-io/zero-ops/internal/opensbt/models"
)

type kratosClient struct {
	adminURL string
	client   *http.Client
}

// kratosIdentity is the Kratos API shape for an identity.
type kratosIdentity struct {
	ID     string `json:"id"`
	State  string `json:"state"` // active | inactive
	Traits struct {
		Email    string   `json:"email"`
		Name     string   `json:"name"`
		TenantID string   `json:"tenant_id"`
		Roles    []string `json:"roles"`
	} `json:"traits"`
	MetadataPublic map[string]interface{} `json:"metadata_public"`
}

func (k *kratosClient) do(ctx context.Context, method, path string, body interface{}) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, k.adminURL+path, r)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return k.client.Do(req)
}

func (k *kratosClient) createIdentity(ctx context.Context, u models.User) error {
	payload := map[string]interface{}{
		"schema_id": "default",
		"traits": map[string]interface{}{
			"email":     u.Email,
			"name":      u.Name,
			"tenant_id": u.TenantID,
			"roles":     u.Roles,
		},
		"metadata_public": u.Metadata,
		"state":           "active",
	}
	resp, err := k.do(ctx, http.MethodPost, "/admin/identities", payload)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("kratos createIdentity: %d %s", resp.StatusCode, b)
	}
	return nil
}

func (k *kratosClient) getIdentity(ctx context.Context, userID string) (*models.User, error) {
	resp, err := k.do(ctx, http.MethodGet, "/admin/identities/"+userID, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("user not found: %s", userID)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("kratos getIdentity: %d %s", resp.StatusCode, b)
	}
	var id kratosIdentity
	if err := json.NewDecoder(resp.Body).Decode(&id); err != nil {
		return nil, err
	}
	return kratosToUser(id), nil
}

func (k *kratosClient) updateIdentity(ctx context.Context, userID string, u models.UserUpdates) error {
	// Fetch current first to merge
	existing, err := k.getIdentity(ctx, userID)
	if err != nil {
		return err
	}
	if u.Name != nil {
		existing.Name = *u.Name
	}
	if u.Roles != nil {
		existing.Roles = *u.Roles
	}
	if u.Metadata != nil {
		existing.Metadata = *u.Metadata
	}
	payload := map[string]interface{}{
		"schema_id": "default",
		"traits": map[string]interface{}{
			"email":     existing.Email,
			"name":      existing.Name,
			"tenant_id": existing.TenantID,
			"roles":     existing.Roles,
		},
		"metadata_public": existing.Metadata,
	}
	resp, err := k.do(ctx, http.MethodPut, "/admin/identities/"+userID, payload)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("kratos updateIdentity: %d %s", resp.StatusCode, b)
	}
	return nil
}

func (k *kratosClient) deleteIdentity(ctx context.Context, userID string) error {
	resp, err := k.do(ctx, http.MethodDelete, "/admin/identities/"+userID, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("kratos deleteIdentity: %d %s", resp.StatusCode, b)
	}
	return nil
}

func (k *kratosClient) setState(ctx context.Context, userID, state string) error {
	payload := map[string]interface{}{"state": state}
	resp, err := k.do(ctx, http.MethodPatch, "/admin/identities/"+userID, payload)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("kratos setState: %d %s", resp.StatusCode, b)
	}
	return nil
}

func (k *kratosClient) listIdentities(ctx context.Context, f models.UserFilters) ([]models.User, error) {
	q := url.Values{}
	if f.Limit > 0 {
		q.Set("page_size", fmt.Sprintf("%d", f.Limit))
	}
	path := "/admin/identities"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	resp, err := k.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("kratos listIdentities: %d %s", resp.StatusCode, b)
	}
	var ids []kratosIdentity
	if err := json.NewDecoder(resp.Body).Decode(&ids); err != nil {
		return nil, err
	}
	var users []models.User
	for _, id := range ids {
		u := kratosToUser(id)
		// Filter by tenant if requested
		if f.TenantID != nil && u.TenantID != *f.TenantID {
			continue
		}
		if f.Active != nil && u.Active != *f.Active {
			continue
		}
		users = append(users, *u)
	}
	return users, nil
}

func kratosToUser(id kratosIdentity) *models.User {
	return &models.User{
		ID:       id.ID,
		Email:    id.Traits.Email,
		Name:     id.Traits.Name,
		TenantID: id.Traits.TenantID,
		Roles:    id.Traits.Roles,
		Metadata: id.MetadataPublic,
		Active:   id.State == "active",
	}
}

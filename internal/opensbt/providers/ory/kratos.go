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

// PlatformGroups name the groups that make an identity platform-scoped, and
// therefore legitimately tenant-less. Kept beside the guard that reads it so the
// two cannot drift; zero-ops-auth carries the same default for the validating
// side (ADR-058).
var PlatformGroups = []string{"platform_admins"}

// validateUserScope enforces the invariant. Separated from createIdentity so it
// can be exercised without a transport: the rule is the valuable part, and a
// test that needs an HTTP client to assert it would not be run.
func validateUserScope(u models.User) error {
	if u.TenantID == "" && !isPlatformScoped(u) {
		return fmt.Errorf(
			"refusing to create identity %s with neither a tenant nor a platform group: "+
				"such a user cannot authenticate, because the tenant_id claim is derived "+
				"from metadata_public and would be absent", u.Email)
	}
	return nil
}

func isPlatformScoped(u models.User) bool {
	for _, g := range u.Groups {
		for _, p := range PlatformGroups {
			if g == p {
				return true
			}
		}
	}
	return false
}

func (k *kratosClient) createIdentity(ctx context.Context, u models.User) error {
	// EVERY user gets exactly one scope: a tenant, or a platform group. Never
	// neither.
	//
	// "Neither" is not a harmless default — it is an account that can never
	// authenticate. The auth-proxy derives the tenant_id claim from
	// metadata_public, so an identity created without a tenant produces a token
	// with no tenant, and every tenant-scoped service rejects it with
	// "Token missing required tenant_id claim". On 2026-09-02 a user was created
	// in exactly this state, the API returned 201, and the failure only surfaced
	// later as a permanent 401 that looked like a session or token problem.
	//
	// The rule is deliberately NOT "every user must have a tenant". A platform
	// admin must not belong to one, and enforcing a tenant unconditionally would
	// make that identity uncreatable — the same contradiction one step earlier.
	if err := validateUserScope(u); err != nil {
		return err
	}

	// traits carry only what the user may assert about themselves, and the identity
	// schema admits exactly one: email, which is also the identifier the password
	// method authenticates against. tenant membership and role are assignments this
	// service makes about a user, so they go to metadata_public, which self-service
	// registration cannot write. ADR-010 specifies this ("creates identity in Ory
	// Kratos with tenant_id in metadata"); writing them as traits contradicted it and
	// is rejected outright by the schema, which sets additionalProperties: false.
	metadata := map[string]interface{}{}
	for k, v := range u.Metadata {
		metadata[k] = v
	}
	metadata["tenant_id"] = u.TenantID
	if len(u.Roles) > 0 {
		// The token claim and the schema are singular; Roles is the caller-facing
		// plural. Collapsing here keeps the wire shape one thing rather than two.
		metadata["role"] = u.Roles[0]
	}
	if len(u.Groups) > 0 {
		metadata["groups"] = u.Groups
	}

	payload := map[string]interface{}{
		"schema_id": "default",
		"traits": map[string]interface{}{
			"email": u.Email,
		},
		"metadata_public": metadata,
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
	if u.Groups != nil {
		existing.Groups = *u.Groups
	}
	if u.Metadata != nil {
		existing.Metadata = *u.Metadata
	}
	// Same split as createIdentity: an update must not push the assignments back
	// into traits, or it would undo the boundary on every edit and fail the schema.
	metadata := map[string]interface{}{}
	for k2, v := range existing.Metadata {
		metadata[k2] = v
	}
	metadata["tenant_id"] = existing.TenantID
	if len(existing.Roles) > 0 {
		metadata["role"] = existing.Roles[0]
	}
	if len(existing.Groups) > 0 {
		metadata["groups"] = existing.Groups
	}

	payload := map[string]interface{}{
		"schema_id": "default",
		"traits": map[string]interface{}{
			"email": existing.Email,
		},
		"metadata_public": metadata,
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

func (k *kratosClient) findUserByEmail(ctx context.Context, email string) (*models.User, error) {
	users, err := k.listIdentities(ctx, models.UserFilters{})
	if err != nil {
		return nil, err
	}
	for _, u := range users {
		if u.Email == email {
			return &u, nil
		}
	}
	return nil, fmt.Errorf("user not found: %s", email)
}

func kratosToUser(id kratosIdentity) *models.User {
	// The assignments are read back from metadata_public, where this service writes
	// them, not from traits. Traits fall back only for identities created before the
	// split; a self-declared trait can never override an assignment, because
	// metadata is consulted first.
	tenantID := id.Traits.TenantID
	if v, ok := id.MetadataPublic["tenant_id"].(string); ok && v != "" {
		tenantID = v
	}
	roles := id.Traits.Roles
	if v, ok := id.MetadataPublic["role"].(string); ok && v != "" {
		roles = []string{v}
	}
	var groups []string
	if g, ok := id.MetadataPublic["groups"].([]interface{}); ok {
		for _, v := range g {
			if s, ok := v.(string); ok {
				groups = append(groups, s)
			}
		}
	}
	return &models.User{
		ID:       id.ID,
		Email:    id.Traits.Email,
		Name:     id.Traits.Name,
		TenantID: tenantID,
		Roles:    roles,
		Groups:   groups,
		Metadata: id.MetadataPublic,
		Active:   id.State == "active",
	}
}

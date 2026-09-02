package authproxy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type KratosClient struct {
	publicURL string
	adminURL  string
	client    *http.Client
}

func NewKratosClient(publicURL, adminURL string) *KratosClient {
	return &KratosClient{
		publicURL: publicURL,
		adminURL:  adminURL,
		client:    &http.Client{},
	}
}

type KratosSession struct {
	ID       string `json:"id"`
	Identity struct {
		ID     string                 `json:"id"`
		Traits map[string]interface{} `json:"traits"`
	} `json:"identity"`
}

func (k *KratosClient) GetSession(cookie string) (*KratosSession, error) {
	req, err := http.NewRequest("GET", k.publicURL+"/sessions/whoami", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Cookie", cookie)

	resp, err := k.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("session validation failed: %d", resp.StatusCode)
	}

	var session KratosSession
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return nil, err
	}

	return &session, nil
}

func (k *KratosClient) GetIdentityTraits(identityID string) (map[string]interface{}, error) {
	req, err := http.NewRequest("GET", k.adminURL+"/admin/identities/"+identityID, nil)
	if err != nil {
		return nil, err
	}

	resp, err := k.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to fetch identity traits: %d - %s", resp.StatusCode, string(body))
	}

	// role and tenant_id are read from metadata_public, not traits.
	//
	// Traits are self-service: whatever the schema puts there, a user can set for
	// themselves during registration. While role lived in traits — with an enum
	// admitting platform_admin — anyone who could reach the signup page could
	// request it, and the value flowed straight into the token claims below and out
	// as X-Auth-Role. Tenant membership and role are assignments the platform makes
	// about a user, not claims a user makes about themselves, so they belong in
	// metadata_public, which self-service flows cannot write.
	//
	// email stays a trait: it IS the user's own claim, and it is the credential
	// identifier the password method authenticates against.
	var identity struct {
		Traits         map[string]interface{} `json:"traits"`
		MetadataPublic map[string]interface{} `json:"metadata_public"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&identity); err != nil {
		return nil, err
	}

	attrs := map[string]interface{}{}
	for k, v := range identity.Traits {
		attrs[k] = v
	}
	// Deliberately last: metadata_public wins over a trait of the same name, so an
	// identity created before this split cannot re-assert a self-declared role.
	for _, k := range []string{"role", "tenant_id", "groups"} {
		if v, ok := identity.MetadataPublic[k]; ok {
			attrs[k] = v
		} else {
			delete(attrs, k)
		}
	}

	return attrs, nil
}

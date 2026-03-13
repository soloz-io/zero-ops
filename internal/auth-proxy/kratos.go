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

	var identity struct {
		Traits map[string]interface{} `json:"traits"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&identity); err != nil {
		return nil, err
	}

	return identity.Traits, nil
}

package authproxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

func RegisterClient(hydraURL, clientID string) error {
	client := map[string]interface{}{
		"client_id":              clientID,
		"grant_types":            []string{"authorization_code", "refresh_token"},
		"response_types":         []string{"code"},
		"token_endpoint_auth_method": "none",
		"redirect_uris":          []string{"http://localhost:3000/callback"},
	}

	body, _ := json.Marshal(client)
	req, _ := http.NewRequest("PUT", fmt.Sprintf("%s/clients/%s", hydraURL, clientID), bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		return fmt.Errorf("failed to register client: %d", resp.StatusCode)
	}
	return nil
}

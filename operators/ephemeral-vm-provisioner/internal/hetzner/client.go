package hetzner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// ManagedBy is the value half of the ownership label. It tracks the component
// name (ADR-041), so it moved with the rename to ephemeral-vm-provisioner.
const ManagedBy = "ephemeral-vm-provisioner"

// Client interacts with the Hetzner Cloud API.
type Client struct {
	token      string
	apiURL     string
	httpClient *http.Client
}

// Server represents a Hetzner Cloud server.
type Server struct {
	ID     string
	Name   string
	IP     string
	Status string
	Labels map[string]string
}

// Volume represents a Hetzner Cloud volume.
type Volume struct {
	ID          string
	Name        string
	Size        int
	LinuxDevice string
	Location    string
	Status      string
	ServerID    string
}

// NewClient creates a new Hetzner API client from the HCLOUD_TOKEN env var.
func NewClient() *Client {
	return &Client{
		token:  os.Getenv("HCLOUD_TOKEN"),
		apiURL: "https://api.hetzner.cloud/v1",
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) doRequest(ctx context.Context, method, path string, body interface{}) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(jsonBody)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.apiURL+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// CreateServer creates a new Hetzner Cloud server with cloud-init user data.
func (c *Client) CreateServer(ctx context.Context, name, cloudInit, serverType string) (*Server, error) {
	if serverType == "" {
		serverType = "cpx22"
	}

	sshKeys := []string{"mac-mini-ssh"}
	if envKeys := os.Getenv("HCLOUD_SSH_KEYS"); envKeys != "" {
		sshKeys = strings.Split(envKeys, ",")
	}

	body := map[string]interface{}{
		"name":        name,
		"server_type": serverType,
		"image":       "ubuntu-24.04",
		"location":    "hel1",
		"user_data":   cloudInit,
		"ssh_keys":    sshKeys,
		"labels": map[string]string{
			"compute.nutgraf.in/job-type":   "ephemeral",
			"compute.nutgraf.in/managed-by": ManagedBy,
		},
	}

	respBody, err := c.doRequest(ctx, "POST", "/servers", body)
	if err != nil {
		return nil, fmt.Errorf("failed to create server: %w", err)
	}

	var result struct {
		Server struct {
			ID        int64  `json:"id"`
			Name      string `json:"name"`
			PublicNet struct {
				IPv4 struct {
					IP string `json:"ip"`
				} `json:"ipv4"`
			} `json:"public_net"`
		} `json:"server"`
	}

	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &Server{
		ID:     fmt.Sprintf("%d", result.Server.ID),
		Name:   result.Server.Name,
		IP:     result.Server.PublicNet.IPv4.IP,
		Status: "creating",
	}, nil
}

// GetServer retrieves a server by ID.
func (c *Client) GetServer(ctx context.Context, id string) (*Server, error) {
	respBody, err := c.doRequest(ctx, "GET", "/servers/"+id, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get server: %w", err)
	}

	var result struct {
		Server struct {
			ID        int64  `json:"id"`
			Name      string `json:"name"`
			Status    string `json:"status"`
			PublicNet struct {
				IPv4 struct {
					IP string `json:"ip"`
				} `json:"ipv4"`
			} `json:"public_net"`
		} `json:"server"`
	}

	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &Server{
		ID:     fmt.Sprintf("%d", result.Server.ID),
		Name:   result.Server.Name,
		IP:     result.Server.PublicNet.IPv4.IP,
		Status: result.Server.Status,
	}, nil
}

// ListServers returns all servers carrying the ephemeral-provisioner managed label.
func (c *Client) ListServers(ctx context.Context) ([]Server, error) {
	labelQuery := "compute.nutgraf.in/managed-by=" + ManagedBy
	var servers []Server

	for page := 1; ; page++ {
		respBody, err := c.doRequest(ctx, "GET", fmt.Sprintf("/servers?label_selector=%s&page=%d&per_page=50", labelQuery, page), nil)
		if err != nil {
			return nil, fmt.Errorf("failed to list servers: %w", err)
		}
		var result struct {
			Servers []struct {
				ID        int64             `json:"id"`
				Name      string            `json:"name"`
				Status    string            `json:"status"`
				Labels    map[string]string `json:"labels"`
				PublicNet struct {
					IPv4 struct {
						IP string `json:"ip"`
					} `json:"ipv4"`
				} `json:"public_net"`
			} `json:"servers"`
			Meta struct {
				Pagination struct {
					Page       int `json:"page"`
					LastPage   int `json:"last_page"`
					TotalPages int `json:"total_pages"`
				} `json:"pagination"`
			} `json:"meta"`
		}
		if err := json.Unmarshal(respBody, &result); err != nil {
			return nil, fmt.Errorf("failed to parse servers response: %w", err)
		}
		for _, s := range result.Servers {
			servers = append(servers, Server{
				ID:     fmt.Sprintf("%d", s.ID),
				Name:   s.Name,
				IP:     s.PublicNet.IPv4.IP,
				Status: s.Status,
				Labels: s.Labels,
			})
		}
		if result.Meta.Pagination.Page >= result.Meta.Pagination.LastPage || result.Meta.Pagination.LastPage == 0 {
			break
		}
	}

	return servers, nil
}

// DeleteServer deletes a server by ID.
func (c *Client) DeleteServer(ctx context.Context, id string) error {
	_, err := c.doRequest(ctx, "DELETE", "/servers/"+id, nil)
	if err != nil {
		return fmt.Errorf("failed to delete server: %w", err)
	}
	return nil
}

// GetVolume retrieves a volume by ID.
func (c *Client) GetVolume(ctx context.Context, id string) (*Volume, error) {
	respBody, err := c.doRequest(ctx, "GET", "/volumes/"+id, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get volume: %w", err)
	}

	var result struct {
		Volume struct {
			ID          int64  `json:"id"`
			Name        string `json:"name"`
			Size        int    `json:"size"`
			LinuxDevice string `json:"linux_device"`
			Location    struct {
				Name string `json:"name"`
			} `json:"location"`
			Status string `json:"status"`
			Server *int   `json:"server"`
		} `json:"volume"`
	}

	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	v := &Volume{
		ID:          fmt.Sprintf("%d", result.Volume.ID),
		Name:        result.Volume.Name,
		Size:        result.Volume.Size,
		LinuxDevice: result.Volume.LinuxDevice,
		Location:    result.Volume.Location.Name,
		Status:      result.Volume.Status,
	}
	if result.Volume.Server != nil {
		v.ServerID = fmt.Sprintf("%d", *result.Volume.Server)
	}

	return v, nil
}

// AttachVolume attaches a volume to a server and waits for attachment completion.
func (c *Client) AttachVolume(ctx context.Context, volumeID, serverID string) error {
	serverIDInt, err := strconv.ParseInt(serverID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid server ID %q: %w", serverID, err)
	}
	body := map[string]interface{}{
		"server": serverIDInt,
	}

	_, err = c.doRequest(ctx, "POST", "/volumes/"+volumeID+"/actions/attach", body)
	if err != nil {
		return fmt.Errorf("failed to attach volume %s to server %s: %w", volumeID, serverID, err)
	}
	if _, err := c.WaitForVolumeAttached(ctx, volumeID, serverID, 30*time.Second); err != nil {
		return fmt.Errorf("failed waiting for volume %s to attach to server %s: %w", volumeID, serverID, err)
	}
	return nil
}

// DetachVolume detaches a volume from its server.
func (c *Client) DetachVolume(ctx context.Context, volumeID string) error {
	_, err := c.doRequest(ctx, "POST", "/volumes/"+volumeID+"/actions/detach", map[string]interface{}{})
	if err != nil {
		return fmt.Errorf("failed to detach volume %s: %w", volumeID, err)
	}
	if _, err := c.WaitForVolumeDetached(ctx, volumeID, 30*time.Second); err != nil {
		return fmt.Errorf("failed waiting for volume %s to detach: %w", volumeID, err)
	}
	return nil
}

// WaitForVolumeAttached waits until volume.ServerID matches the target serverID.
func (c *Client) WaitForVolumeAttached(ctx context.Context, id, serverID string, timeout time.Duration) (*Volume, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		volume, err := c.GetVolume(ctx, id)
		if err != nil {
			return nil, err
		}
		if volume.ServerID == serverID {
			return volume, nil
		}
		time.Sleep(1 * time.Second)
	}
	return nil, fmt.Errorf("timed out waiting for volume %s to attach to server %s", id, serverID)
}

// WaitForVolumeDetached waits until volume.ServerID is empty.
func (c *Client) WaitForVolumeDetached(ctx context.Context, id string, timeout time.Duration) (*Volume, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		volume, err := c.GetVolume(ctx, id)
		if err != nil {
			return nil, err
		}
		if volume.ServerID == "" {
			return volume, nil
		}
		time.Sleep(1 * time.Second)
	}
	return nil, fmt.Errorf("timed out waiting for volume %s to detach", id)
}

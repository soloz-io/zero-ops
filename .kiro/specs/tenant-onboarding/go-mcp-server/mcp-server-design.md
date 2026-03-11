# MCP Server Design: Zero-Ops Tenant Onboarding API

**Version:** 1.0  
**Status:** DRAFT  
**Last Updated:** 2026-03-08  
**Reference Implementation:** kubernetes-mcp-server (Go)

---

## 1. Executive Summary

This document defines the architecture and implementation pattern for building an MCP server that exposes the Zero-Ops Tenant Onboarding API as MCP tools. The design follows the proven patterns from the kubernetes-mcp-server reference implementation, adapted for REST API operations instead of Kubernetes API operations.

**Key Design Principles:**
- Native Go implementation using official MCP Go SDK
- Direct API integration (no CLI wrapper)
- Stdio transport for Goose client integration
- HTTP/SSE transport for web clients (future)
- Stateless operation for scalability
- OAuth 2.1 authentication support
- Comprehensive error handling with MCP logging

---

## 2. Architecture Overview

### 2.1 High-Level Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    Goose MCP Client                          │
│              (Agentic AI Assistant)                          │
└────────────────────────┬────────────────────────────────────┘
                         │ stdio (JSON-RPC)
                         ▼
┌─────────────────────────────────────────────────────────────┐
│              zero-ops-mcp-server (Go Binary)                 │
│  ┌──────────────────────────────────────────────────────┐   │
│  │  MCP Server (github.com/modelcontextprotocol/go-sdk) │   │
│  │  • Tool Registration                                  │   │
│  │  • Request Routing                                    │   │
│  │  • Error Handling                                     │   │
│  │  • Logging (MCP Protocol)                             │   │
│  └──────────────────────────────────────────────────────┘   │
│  ┌──────────────────────────────────────────────────────┐   │
│  │  Toolsets (Organized by Category)                     │   │
│  │  ┌────────────┐  ┌────────────┐  ┌────────────┐     │   │
│  │  │  Tenant    │  │   Auth     │  │  Cluster   │     │   │
│  │  │  Lifecycle │  │  & Tokens  │  │  Mgmt      │     │   │
│  │  └────────────┘  └────────────┘  └────────────┘     │   │
│  │  ┌────────────┐  ┌────────────┐  ┌────────────┐     │   │
│  │  │  Billing   │  │  Quotas    │  │  Audit     │     │   │
│  │  │  & Meter   │  │  & Usage   │  │  & Logs    │     │   │
│  │  └────────────┘  └────────────┘  └────────────┘     │   │
│  └──────────────────────────────────────────────────────┘   │
│  ┌──────────────────────────────────────────────────────┐   │
│  │  API Client (HTTP Client)                             │   │
│  │  • REST API calls to zero-ops-api                     │   │
│  │  • JWT token management                               │   │
│  │  • Retry logic & error handling                       │   │
│  └──────────────────────────────────────────────────────┘   │
└────────────────────────┬────────────────────────────────────┘
                         │ HTTPS (REST API)
                         ▼
┌─────────────────────────────────────────────────────────────┐
│                   zero-ops-api (Go)                          │
│  • Tenant Management                                         │
│  • Authentication & Authorization                            │
│  • Cluster Operations                                        │
│  • Billing & Metering                                        │
│  • PostgreSQL Database                                       │
└─────────────────────────────────────────────────────────────┘
```

### 2.2 Component Layers

**Layer 1: MCP Protocol Layer**
- MCP Go SDK server implementation
- JSON-RPC message handling
- Tool registration and discovery
- Prompt registration (optional)
- Resource registration (optional)

**Layer 2: Toolset Layer**
- Organized by API category (tenant, auth, cluster, billing, etc.)
- Each toolset contains related MCP tools
- Tool validation and parameter parsing
- Business logic orchestration

**Layer 3: API Client Layer**
- HTTP client for zero-ops-api
- JWT token management
- Request/response handling
- Retry logic and circuit breaker
- Error mapping (HTTP → MCP errors)

**Layer 4: Configuration Layer**
- TOML configuration file support
- Environment variable overrides
- CLI flags
- Dynamic configuration reload (SIGHUP)

---

## 3. Project Structure

```
zero-ops-mcp-server/
├── cmd/
│   └── zero-ops-mcp-server/
│       ├── main.go                    # Entry point
│       └── main_test.go
│
├── pkg/
│   ├── api/                           # API client
│   │   ├── client.go                  # HTTP client wrapper
│   │   ├── auth.go                    # JWT token management
│   │   ├── tenant.go                  # Tenant API calls
│   │   ├── cluster.go                 # Cluster API calls
│   │   ├── billing.go                 # Billing API calls
│   │   ├── quota.go                   # Quota API calls
│   │   ├── audit.go                   # Audit API calls
│   │   └── errors.go                  # Error mapping
│   │
│   ├── mcp/                           # MCP server implementation
│   │   ├── server.go                  # MCP server setup
│   │   ├── toolsets.go                # Toolset registry
│   │   ├── logging.go                 # MCP logging capability
│   │   └── errors.go                  # MCP error handling
│   │
│   ├── toolsets/                      # MCP toolsets
│   │   ├── registry.go                # Toolset registration
│   │   ├── tenant/                    # Tenant lifecycle tools
│   │   │   ├── create.go              # tenant_create tool
│   │   │   ├── get.go                 # tenant_get tool
│   │   │   ├── list.go                # tenant_list tool
│   │   │   ├── update.go              # tenant_update tool
│   │   │   ├── delete.go              # tenant_delete tool
│   │   │   └── toolset.go             # Toolset definition
│   │   ├── auth/                      # Authentication tools
│   │   │   ├── login.go               # auth_login tool
│   │   │   ├── token.go               # auth_token_create tool
│   │   │   ├── kubeconfig.go          # auth_kubeconfig_download tool
│   │   │   ├── invite.go              # auth_invite_user tool
│   │   │   └── toolset.go
│   │   ├── cluster/                   # Cluster management tools
│   │   │   ├── create.go              # cluster_create tool
│   │   │   ├── get.go                 # cluster_get tool
│   │   │   ├── list.go                # cluster_list tool
│   │   │   ├── delete.go              # cluster_delete tool
│   │   │   ├── scale.go               # cluster_scale tool
│   │   │   └── toolset.go
│   │   ├── billing/                   # Billing & metering tools
│   │   │   ├── usage.go               # billing_usage_get tool
│   │   │   ├── invoices.go            # billing_invoices_list tool
│   │   │   ├── estimate.go            # billing_estimate tool
│   │   │   └── toolset.go
│   │   ├── quota/                     # Quota management tools
│   │   │   ├── get.go                 # quota_get tool
│   │   │   ├── update.go              # quota_update tool
│   │   │   └── toolset.go
│   │   └── audit/                     # Audit & compliance tools
│   │       ├── logs.go                # audit_logs_list tool
│   │       ├── export.go              # audit_export_data tool
│   │       └── toolset.go
│   │
│   ├── config/                        # Configuration management
│   │   ├── config.go                  # Config struct & defaults
│   │   ├── loader.go                  # TOML file loader
│   │   └── validation.go              # Config validation
│   │
│   └── version/                       # Version information
│       └── version.go
│
├── internal/
│   └── test/                          # Test utilities
│       ├── fixtures.go
│       └── mock_api.go
│
├── configs/                           # Example configurations
│   ├── default.toml                   # Default config
│   └── production.toml                # Production config
│
├── go.mod
├── go.sum
├── Makefile
├── README.md
└── server.json                        # MCP server metadata
```

---

## 4. Core Implementation Patterns

### 4.1 Main Entry Point

```go
// cmd/zero-ops-mcp-server/main.go
package main

import (
	"os"

	"github.com/spf13/pflag"
	"k8s.io/cli-runtime/pkg/genericiooptions"

	"github.com/zero-ops/mcp-server/pkg/cmd"
)

func main() {
	flags := pflag.NewFlagSet("zero-ops-mcp-server", pflag.ExitOnError)
	pflag.CommandLine = flags

	root := cmd.NewMCPServer(genericiooptions.IOStreams{
		In:     os.Stdin,
		Out:    os.Stdout,
		ErrOut: os.Stderr,
	})
	
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
```

### 4.2 MCP Server Initialization

```go
// pkg/mcp/server.go
package mcp

import (
	"context"
	"fmt"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/server"
	
	"github.com/zero-ops/mcp-server/pkg/api"
	"github.com/zero-ops/mcp-server/pkg/config"
	"github.com/zero-ops/mcp-server/pkg/toolsets"
)

type Server struct {
	mcpServer  *mcpsdk.MCPServer
	apiClient  *api.Client
	config     *config.Config
	toolsets   []toolsets.Toolset
}

func NewServer(cfg *config.Config) (*Server, error) {
	// Initialize API client
	apiClient, err := api.NewClient(cfg.APIBaseURL, cfg.APIToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	// Initialize MCP server
	mcpServer := mcpsdk.NewMCPServer(
		"zero-ops-mcp-server",
		"1.0.0",
		mcpsdk.WithLogging(),
	)

	server := &Server{
		mcpServer: mcpServer,
		apiClient: apiClient,
		config:    cfg,
	}

	// Register toolsets
	if err := server.registerToolsets(); err != nil {
		return nil, fmt.Errorf("failed to register toolsets: %w", err)
	}

	return server, nil
}

func (s *Server) registerToolsets() error {
	// Get enabled toolsets from config
	enabledToolsets := s.config.Toolsets
	
	// Register each enabled toolset
	for _, toolsetName := range enabledToolsets {
		toolset, err := toolsets.Get(toolsetName, s.apiClient)
		if err != nil {
			return fmt.Errorf("failed to get toolset %s: %w", toolsetName, err)
		}
		
		// Register tools from toolset
		for _, tool := range toolset.Tools() {
			if err := s.mcpServer.AddTool(tool.Definition, tool.Handler); err != nil {
				return fmt.Errorf("failed to register tool %s: %w", tool.Definition.Name, err)
			}
		}
		
		s.toolsets = append(s.toolsets, toolset)
	}
	
	return nil
}

func (s *Server) ServeStdio(ctx context.Context) error {
	return s.mcpServer.Serve(ctx)
}
```

### 4.3 Toolset Pattern

```go
// pkg/toolsets/tenant/toolset.go
package tenant

import (
	mcpsdk "github.com/modelcontextprotocol/go-sdk/server"
	
	"github.com/zero-ops/mcp-server/pkg/api"
)

type Toolset struct {
	apiClient *api.Client
}

func New(apiClient *api.Client) *Toolset {
	return &Toolset{
		apiClient: apiClient,
	}
}

func (t *Toolset) Name() string {
	return "tenant"
}

func (t *Toolset) Tools() []Tool {
	return []Tool{
		{
			Definition: mcpsdk.Tool{
				Name:        "tenant_create",
				Description: "Create a new tenant (onboarding)",
				InputSchema: mcpsdk.ToolInputSchema{
					Type: "object",
					Properties: map[string]interface{}{
						"name": map[string]interface{}{
							"type":        "string",
							"description": "Tenant name (alphanumeric, hyphens allowed)",
						},
						"email": map[string]interface{}{
							"type":        "string",
							"description": "Admin email address",
						},
						"plan": map[string]interface{}{
							"type":        "string",
							"description": "Subscription plan (free, professional, enterprise)",
							"enum":        []string{"free", "professional", "enterprise"},
						},
					},
					Required: []string{"name", "email", "plan"},
				},
			},
			Handler: t.handleCreate,
		},
		{
			Definition: mcpsdk.Tool{
				Name:        "tenant_get",
				Description: "Retrieve tenant details",
				InputSchema: mcpsdk.ToolInputSchema{
					Type: "object",
					Properties: map[string]interface{}{
						"tenantId": map[string]interface{}{
							"type":        "string",
							"description": "Tenant ID",
						},
					},
					Required: []string{"tenantId"},
				},
			},
			Handler: t.handleGet,
		},
		// ... more tools
	}
}
```

### 4.4 Tool Handler Pattern

```go
// pkg/toolsets/tenant/create.go
package tenant

import (
	"context"
	"encoding/json"
	"fmt"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/server"
	
	"github.com/zero-ops/mcp-server/pkg/api"
)

type CreateTenantRequest struct {
	Name  string                 `json:"name"`
	Email string                 `json:"email"`
	Plan  string                 `json:"plan"`
	Quotas map[string]interface{} `json:"quotas,omitempty"`
}

func (t *Toolset) handleCreate(ctx context.Context, request mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	// Parse arguments
	var req CreateTenantRequest
	argsJSON, err := json.Marshal(request.Params.Arguments)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal arguments: %w", err)
	}
	
	if err := json.Unmarshal(argsJSON, &req); err != nil {
		return nil, fmt.Errorf("failed to parse arguments: %w", err)
	}

	// Validate required fields
	if req.Name == "" || req.Email == "" || req.Plan == "" {
		return nil, fmt.Errorf("name, email, and plan are required")
	}

	// Call API
	tenant, err := t.apiClient.CreateTenant(ctx, &api.CreateTenantRequest{
		Name:   req.Name,
		Email:  req.Email,
		Plan:   req.Plan,
		Quotas: req.Quotas,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create tenant: %w", err)
	}

	// Format response
	response := map[string]interface{}{
		"tenantId":       tenant.TenantID,
		"namespace":      tenant.Namespace,
		"status":         tenant.Status,
		"createdAt":      tenant.CreatedAt,
		"apiToken":       tenant.APIToken,
		"kubeconfigUrl":  tenant.KubeconfigURL,
	}

	responseJSON, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal response: %w", err)
	}

	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{
			{
				Type: "text",
				Text: string(responseJSON),
			},
		},
	}, nil
}
```

### 4.5 API Client Pattern

```go
// pkg/api/client.go
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Client struct {
	baseURL    string
	httpClient *http.Client
	token      string
}

func NewClient(baseURL, token string) (*Client, error) {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		token: token,
	}, nil
}

func (c *Client) doRequest(ctx context.Context, method, path string, body interface{}) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyJSON, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(bodyJSON)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	return resp, nil
}

// pkg/api/tenant.go
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

type CreateTenantRequest struct {
	Name     string                 `json:"name"`
	Email    string                 `json:"email"`
	Plan     string                 `json:"plan"`
	Quotas   map[string]interface{} `json:"quotas,omitempty"`
	Metadata map[string]string      `json:"metadata,omitempty"`
}

type Tenant struct {
	TenantID       string    `json:"tenantId"`
	Name           string    `json:"name"`
	Email          string    `json:"email"`
	Plan           string    `json:"plan"`
	Status         string    `json:"status"`
	Namespace      string    `json:"namespace"`
	APIToken       string    `json:"apiToken,omitempty"`
	KubeconfigURL  string    `json:"kubeconfigUrl,omitempty"`
	CreatedAt      string    `json:"createdAt"`
}

func (c *Client) CreateTenant(ctx context.Context, req *CreateTenantRequest) (*Tenant, error) {
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/tenants", req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var tenant Tenant
	if err := json.NewDecoder(resp.Body).Decode(&tenant); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &tenant, nil
}
```

### 4.6 Configuration Pattern

```go
// pkg/config/config.go
package config

type Config struct {
	// API Configuration
	APIBaseURL string `toml:"api_base_url"`
	APIToken   string `toml:"api_token"`
	
	// MCP Server Configuration
	Port       string   `toml:"port"`        // Empty = stdio, "8080" = HTTP
	LogLevel   int      `toml:"log_level"`
	Toolsets   []string `toml:"toolsets"`
	ReadOnly   bool     `toml:"read_only"`
	Stateless  bool     `toml:"stateless"`
	
	// Authentication
	RequireOAuth bool   `toml:"require_oauth"`
	OAuthAudience string `toml:"oauth_audience"`
}

func Default() *Config {
	return &Config{
		APIBaseURL: "https://api.zero-ops.io",
		LogLevel:   1,
		Toolsets:   []string{"tenant", "auth", "cluster"},
		ReadOnly:   false,
		Stateless:  false,
	}
}
```

---

## 5. Goose Client Configuration

### 5.1 MCP Server Registration

```yaml
# ~/.config/goose/config.yaml
extensions:
  zero-ops:
    command: npx
    args:
      - -y
      - zero-ops-mcp-server@latest
    env:
      ZERO_OPS_API_URL: https://api.zero-ops.io
      ZERO_OPS_API_TOKEN: eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...
```

### 5.2 Alternative: Binary Installation

```yaml
# ~/.config/goose/config.yaml
extensions:
  zero-ops:
    command: /usr/local/bin/zero-ops-mcp-server
    args:
      - --config
      - /etc/zero-ops/config.toml
```

---

## 6. Tool Categories & Mapping

### 6.1 Tenant Lifecycle Toolset

| MCP Tool Name | API Endpoint | HTTP Method | Description |
|---------------|--------------|-------------|-------------|
| `tenant_create` | `/api/v1/tenants` | POST | Create new tenant |
| `tenant_get` | `/api/v1/tenants/{id}` | GET | Get tenant details |
| `tenant_list` | `/api/v1/tenants` | GET | List all tenants |
| `tenant_update` | `/api/v1/tenants/{id}` | PATCH | Update tenant config |
| `tenant_delete` | `/api/v1/tenants/{id}` | DELETE | Delete tenant |

### 6.2 Authentication Toolset

| MCP Tool Name | API Endpoint | HTTP Method | Description |
|---------------|--------------|-------------|-------------|
| `auth_login` | `/api/v1/auth/login` | POST | Tenant authentication |
| `auth_token_create` | `/api/v1/auth/token` | POST | Generate API token |
| `auth_kubeconfig_download` | `/api/v1/auth/kubeconfig` | GET | Download kubeconfig |
| `auth_invite_user` | `/api/v1/auth/invite` | POST | Invite team member |

### 6.3 Cluster Management Toolset

| MCP Tool Name | API Endpoint | HTTP Method | Description |
|---------------|--------------|-------------|-------------|
| `cluster_create` | `/api/v1/clusters` | POST | Create cluster |
| `cluster_get` | `/api/v1/clusters/{id}` | GET | Get cluster details |
| `cluster_list` | `/api/v1/clusters` | GET | List clusters |
| `cluster_delete` | `/api/v1/clusters/{id}` | DELETE | Delete cluster |
| `cluster_scale` | `/api/v1/clusters/{id}/scale` | PATCH | Scale cluster workers |

### 6.4 Billing & Metering Toolset

| MCP Tool Name | API Endpoint | HTTP Method | Description |
|---------------|--------------|-------------|-------------|
| `billing_usage_get` | `/api/v1/billing/usage/{tenantId}` | GET | Get usage summary |
| `billing_invoices_list` | `/api/v1/billing/invoices/{tenantId}` | GET | List invoices |
| `billing_estimate` | `/api/v1/billing/estimate` | GET | Calculate cost estimate |

### 6.5 Quota Management Toolset

| MCP Tool Name | API Endpoint | HTTP Method | Description |
|---------------|--------------|-------------|-------------|
| `quota_get` | `/api/v1/tenants/{tenantId}/quotas` | GET | Get quotas and usage |
| `quota_update` | `/api/v1/tenants/{tenantId}/quotas` | PATCH | Update tenant quotas |

### 6.6 Audit & Compliance Toolset

| MCP Tool Name | API Endpoint | HTTP Method | Description |
|---------------|--------------|-------------|-------------|
| `audit_logs_list` | `/api/v1/audit/logs` | GET | Get audit trail |
| `audit_export_data` | `/api/v1/compliance/export` | POST | GDPR data export |

---

## 7. Error Handling

### 7.1 HTTP to MCP Error Mapping

```go
// pkg/api/errors.go
package api

import (
	"fmt"
	"net/http"
)

type APIError struct {
	StatusCode int
	Code       string
	Message    string
	Details    map[string]interface{}
}

func (e *APIError) Error() string {
	return fmt.Sprintf("[%d] %s: %s", e.StatusCode, e.Code, e.Message)
}

func MapHTTPError(statusCode int, body []byte) error {
	var apiErr APIError
	apiErr.StatusCode = statusCode
	
	// Parse error response
	// ... (parse JSON error body)
	
	return &apiErr
}

// MCP error codes
const (
	MCPErrorInvalidParams   = -32602
	MCPErrorInternalError   = -32603
	MCPErrorResourceNotFound = -32001
	MCPErrorUnauthorized    = -32002
	MCPErrorForbidden       = -32003
)

func ToMCPError(err error) (int, string) {
	apiErr, ok := err.(*APIError)
	if !ok {
		return MCPErrorInternalError, err.Error()
	}
	
	switch apiErr.StatusCode {
	case http.StatusBadRequest:
		return MCPErrorInvalidParams, apiErr.Message
	case http.StatusUnauthorized:
		return MCPErrorUnauthorized, apiErr.Message
	case http.StatusForbidden:
		return MCPErrorForbidden, apiErr.Message
	case http.StatusNotFound:
		return MCPErrorResourceNotFound, apiErr.Message
	default:
		return MCPErrorInternalError, apiErr.Message
	}
}
```

---

## 8. Testing Strategy

### 8.1 Unit Tests

```go
// pkg/toolsets/tenant/create_test.go
package tenant_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	
	"github.com/zero-ops/mcp-server/pkg/api"
	"github.com/zero-ops/mcp-server/pkg/toolsets/tenant"
)

func TestCreateTenant(t *testing.T) {
	// Setup mock API client
	mockClient := new(api.MockClient)
	mockClient.On("CreateTenant", mock.Anything, mock.Anything).
		Return(&api.Tenant{
			TenantID: "tenant-test",
			Name:     "test-corp",
			Status:   "active",
		}, nil)
	
	// Create toolset
	toolset := tenant.New(mockClient)
	
	// Execute tool
	result, err := toolset.Tools()[0].Handler(context.Background(), mcpsdk.CallToolRequest{
		Params: mcpsdk.CallToolParams{
			Arguments: map[string]interface{}{
				"name":  "test-corp",
				"email": "admin@test.com",
				"plan":  "professional",
			},
		},
	})
	
	// Assert
	assert.NoError(t, err)
	assert.NotNil(t, result)
	mockClient.AssertExpectations(t)
}
```

---

## 9. Deployment

### 9.1 NPM Package

```json
{
  "name": "zero-ops-mcp-server",
  "version": "1.0.0",
  "description": "MCP server for Zero-Ops tenant onboarding API",
  "bin": {
    "zero-ops-mcp-server": "./bin/zero-ops-mcp-server"
  },
  "files": [
    "bin/"
  ]
}
```

### 9.2 Docker Container

```dockerfile
FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o zero-ops-mcp-server cmd/zero-ops-mcp-server/main.go

FROM alpine:latest
COPY --from=builder /app/zero-ops-mcp-server /usr/local/bin/
ENTRYPOINT ["zero-ops-mcp-server"]
```

---

## 10. Next Steps

1. Implement core MCP server structure
2. Implement tenant toolset (5 tools)
3. Implement auth toolset (4 tools)
4. Implement cluster toolset (5 tools)
5. Add comprehensive error handling
6. Add unit tests for all toolsets
7. Package as NPM module
8. Test with Goose client
9. Document user experience flows
10. Deploy to production

---

**End of Document**

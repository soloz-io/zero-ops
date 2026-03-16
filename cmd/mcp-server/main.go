package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type tenantListArgs struct{}

func tenantList(_ context.Context, _ *mcp.CallToolRequest, _ tenantListArgs) (*mcp.CallToolResult, any, error) {
	result, _ := json.Marshal([]map[string]string{
		{"id": "tenant-demo", "name": "Demo Tenant", "status": "active"},
	})
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(result)}},
	}, nil, nil
}

func main() {
	server := mcp.NewServer(&mcp.Implementation{Name: "mcp-server", Version: "1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "tenant_list",
		Description: "List all tenants",
	}, tenantList)

	http.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server
	}, nil))
	http.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	log.Println("mcp-server listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

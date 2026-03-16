> 1. Verify endpoints are up
bash
curl -s https://api.nutgraf.in/health
curl -s https://auth.nutgraf.in/.well-known/oauth-authorization-server | python3 -m json.tool
curl -s https://console.nutgraf.in


2. Configure Cursor — add to ~/.cursor/mcp.json:
json
{
  "mcpServers": {
    "zero-ops": {
      "url": "https://api.nutgraf.in/mcp",
      "auth": {
        "type": "oauth2",
        "discovery_url": "https://api.nutgraf.in/.well-known/oauth-protected-resource"
      }
    }
  }
}


3. Trigger auth flow in Cursor
- Restart Cursor
- Open any chat and invoke an MCP tool
- Browser should open to https://console.nutgraf.in/login
- Login: demo@nutgraf.in / Demo1Password!
- No consent screen should appear — browser closes automatically

4. Verify success
- Cursor should show MCP tool response
- Response from demo-echo should contain headers:
  - X-Auth-User-Id
  - X-Auth-Email: demo@nutgraf.in
  - X-Auth-Role: tenant_admin

Want me to run step 1 first to confirm all endpoints are healthy?
# Agent Gateway MCP Onboarding Architecture Analysis

**Date**: March 8, 2026  
**Objective**: Analyze if Agent Gateway supports modern MCP onboarding architecture (registry + OAuth + agent install)

---

## Executive Summary

**VERDICT**: Agent Gateway supports **PARTIAL** modern MCP onboarding architecture.

**SUPPORTED**:
- ✅ OAuth 2.1 with PKCE (RFC 9449)
- ✅ RFC 8707 Resource Indicators
- ✅ Well-known endpoints (`.well-known/oauth-protected-resource`, `.well-known/oauth-authorization-server`)
- ✅ JWT validation (RS256, ES256)
- ✅ Provider adapters (Keycloak, Auth0)
- ✅ Dynamic client registration proxy
- ✅ WWW-Authenticate header with resource metadata

**NOT SUPPORTED**:
- ❌ MCP Server Registry (no discovery/catalog)
- ❌ Agent install flow (no package manager integration)
- ❌ MCP Server metadata publishing
- ❌ Server capability advertisement
- ❌ Version negotiation

---

## Modern MCP Onboarding Architecture

### What AI-Native Tools Expect

```
┌─────────────────────────────────────────────────────────────┐
│                  MCP Onboarding Flow                         │
└─────────────────────────────────────────────────────────────┘

1. DISCOVERY (Registry)
   User → MCP Registry → Browse available servers
   
2. INSTALL (Agent Install)
   User → Install MCP server package (npm, pip, etc.)
   
3. CONFIGURE (OAuth Setup)
   User → Configure OAuth credentials
   MCP Client → Authorization Server → Get tokens
   
4. CONNECT (MCP Protocol)
   MCP Client → MCP Server → Establish connection
```

### Agent Gateway Current Support

```
┌─────────────────────────────────────────────────────────────┐
│              Agent Gateway MCP Support                       │
└─────────────────────────────────────────────────────────────┘

1. DISCOVERY (Registry) ❌
   NOT SUPPORTED - No registry/catalog
   
2. INSTALL (Agent Install) ❌
   NOT SUPPORTED - No package manager integration
   
3. CONFIGURE (OAuth Setup) ✅
   FULLY SUPPORTED - OAuth 2.1 + PKCE + RFC 8707
   
4. CONNECT (MCP Protocol) ✅
   FULLY SUPPORTED - MCP router with RBAC
```

---

## Detailed Analysis

### 1. OAuth 2.1 Authentication (✅ SUPPORTED)

**Implementation**: `crates/agentgateway/src/mcp/auth.rs`

**Features**:
- OAuth 2.1 with PKCE (code_challenge, code_challenge_method)
- RFC 8707 Resource Indicators (resource parameter)
- JWT validation (RS256, ES256)
- Multiple audiences support
- Token introspection

**Well-Known Endpoints**:
```rust
const OAUTH_PROTECTED_RESOURCE_PREFIX: &str = "/.well-known/oauth-protected-resource";
const OAUTH_AUTHORIZATION_SERVER_PREFIX: &str = "/.well-known/oauth-authorization-server";
```

**Flow**:
1. MCP Client → Agent Gateway (no token)
2. Agent Gateway → 401 with `WWW-Authenticate: Bearer resource_metadata="..."`
3. MCP Client → Discovery via `.well-known/oauth-protected-resource`
4. MCP Client → Authorization Server → Get token
5. MCP Client → Agent Gateway (with token) → Access granted

### 2. Provider Adapters (✅ SUPPORTED)

**Supported Providers**:
- Keycloak (with workarounds for RFC 8707, CORS)
- Auth0 (with audience parameter injection)
- Generic OAuth 2.1 servers

**Keycloak Adapter**:
```rust
Some(McpIDP::Keycloak { .. }) => {
    // Proxy client registration to workaround CORS
    let current_uri = req.extensions().get::<filters::OriginalUrl>()...;
    *re = format!("{current_uri}/client-registration");
}
```

**Auth0 Adapter**:
```rust
Some(McpIDP::Auth0 {}) => {
    // Inject audience parameter
    if let Some(aud) = auth.audiences.first() {
        ae.push_str(&format!("?audience={}", aud));
    }
}
```

### 3. Dynamic Client Registration (✅ SUPPORTED)

**Implementation**: Proxies client registration to upstream IdP

```rust
pub(super) async fn client_registration(
    req: &mut Request,
    auth: &McpAuthentication,
    client: PolicyClient,
) -> Result<Response, ProxyError> {
    let issuer = auth.issuer.trim_end_matches('/');
    let ureq = ::http::Request::builder()
        .uri(format!("{issuer}/clients-registrations/openid-connect"))
        .method(Method::POST)
        .body(body)?;
    
    let mut upstream = client.simple_call(ureq).await?;
    // Add CORS headers
    upstream.headers_mut().insert("access-control-allow-origin", "*".parse().unwrap());
    Ok(upstream)
}
```

### 4. MCP Server Registry (❌ NOT SUPPORTED)

**Missing Features**:
- No server catalog/directory
- No server metadata storage
- No server capability advertisement
- No version negotiation
- No server discovery API

**What's Needed**:
```yaml
# Example registry API (NOT IMPLEMENTED)
GET /registry/servers
  → List available MCP servers

GET /registry/servers/{server_id}
  → Get server metadata (capabilities, version, endpoints)

POST /registry/servers
  → Publish new MCP server

GET /registry/servers/{server_id}/install
  → Get installation instructions
```

### 5. Agent Install Flow (❌ NOT SUPPORTED)

**Missing Features**:
- No package manager integration (npm, pip, cargo)
- No server installation automation
- No dependency resolution
- No version management

**What's Needed**:
```bash
# Example install flow (NOT IMPLEMENTED)
# 1. Discover server
agentgateway registry search "github"

# 2. Install server
agentgateway install @modelcontextprotocol/server-github

# 3. Configure OAuth
agentgateway configure github --oauth-client-id=...

# 4. Connect
agentgateway connect github
```

---

## Comparison: Agent Gateway vs. Modern MCP Onboarding

| Feature | Agent Gateway | Modern MCP Onboarding | Gap |
|---------|---------------|----------------------|-----|
| OAuth 2.1 + PKCE | ✅ Full | ✅ Required | None |
| RFC 8707 Resource Indicators | ✅ Full | ✅ Required | None |
| Well-known endpoints | ✅ Full | ✅ Required | None |
| JWT validation | ✅ Full | ✅ Required | None |
| Provider adapters | ✅ Keycloak, Auth0 | ✅ Multiple | None |
| Dynamic client registration | ✅ Proxy | ✅ Required | None |
| **MCP Server Registry** | ❌ None | ✅ Required | **CRITICAL** |
| **Agent install flow** | ❌ None | ✅ Required | **CRITICAL** |
| Server metadata | ❌ None | ✅ Required | **CRITICAL** |
| Capability advertisement | ❌ None | ✅ Required | **CRITICAL** |
| Version negotiation | ❌ None | ✅ Required | **CRITICAL** |

---

## Recommendations

### Short-Term (Use Agent Gateway As-Is)

**Workaround**: Manual server registration + OAuth

```yaml
# config.yaml
binds:
- listeners:
  - routes:
    - backends:
      - mcp:
          targets:
          - name: github-server
            stdio:
              cmd: npx
              args: ['@modelcontextprotocol/server-github']
      matches:
      - path: { exact: /mcp/github }
      policies:
        mcpAuthentication:
          issuer: https://auth.example.com
          audiences: ['http://localhost:3000/mcp/github']
          jwks: { url: 'https://auth.example.com/.well-known/jwks.json' }
```

**Pros**:
- Works today with Agent Gateway
- Full OAuth 2.1 support
- RBAC enforcement

**Cons**:
- Manual server registration
- No discovery/catalog
- No install automation

### Long-Term (Extend Agent Gateway)

**Add MCP Server Registry**:

1. **Registry API** (new module: `crates/agentgateway/src/registry/`)
   ```rust
   // registry/mod.rs
   pub struct ServerRegistry {
       servers: HashMap<String, ServerMetadata>,
   }
   
   pub struct ServerMetadata {
       id: String,
       name: String,
       version: String,
       capabilities: Vec<String>,
       install_command: String,
       oauth_config: OAuthConfig,
   }
   ```

2. **Discovery Endpoints**:
   ```
   GET /registry/servers
   GET /registry/servers/{id}
   POST /registry/servers
   DELETE /registry/servers/{id}
   ```

3. **Install Integration**:
   ```rust
   // registry/install.rs
   pub async fn install_server(server_id: &str) -> Result<()> {
       let metadata = registry.get(server_id)?;
       match metadata.package_manager {
           PackageManager::Npm => {
               Command::new("npm")
                   .args(&["install", "-g", &metadata.package_name])
                   .spawn()?;
           }
           PackageManager::Pip => {
               Command::new("pip")
                   .args(&["install", &metadata.package_name])
                   .spawn()?;
           }
       }
   }
   ```

4. **Capability Advertisement**:
   ```rust
   // mcp/capabilities.rs
   pub struct ServerCapabilities {
       tools: Vec<ToolMetadata>,
       prompts: Vec<PromptMetadata>,
       resources: Vec<ResourceMetadata>,
   }
   ```

---

## Conclusion

**Agent Gateway is PRODUCTION-READY for OAuth 2.1 authentication** but **MISSING registry/install features** for modern MCP onboarding.

**Use Agent Gateway if**:
- You manually configure MCP servers
- You need OAuth 2.1 + PKCE + RFC 8707
- You need RBAC enforcement
- You need provider adapters (Keycloak, Auth0)

**Extend Agent Gateway if**:
- You need MCP server discovery/catalog
- You need automated server installation
- You need capability advertisement
- You need version negotiation

**Alternative**: Use Agent Gateway for auth + build separate registry service.

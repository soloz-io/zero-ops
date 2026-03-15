# MCP OAuth Final Analysis: Nov 2025 Spec Compliance

**Date:** March 11, 2026  
**Spec Version:** MCP 2025-11-25  
**Status:** AUTHORITATIVE

---

## Executive Summary

After comprehensive research including the official MCP Nov 2025 spec, Aaron Parecki's articles (OAuth 2.1 spec editor), production implementations (Upstash, Context7), Cursor forum evidence, and the official Python SDK, this document provides the authoritative OAuth implementation strategy.

**Critical Finding:** Our initial requirements based on RFC 8252 loopback with ephemeral ports do NOT match how MCP clients actually work.

---

## Evidence-Based Findings

### 1. Client Registration: CIMD is Primary, DCR is Deprecated

**Official MCP Spec (Nov 2025):**
> "MCP clients and authorization servers SHOULD support OAuth Client ID Metadata Documents"
> "MCP clients and authorization servers MAY support the OAuth 2.0 Dynamic Client Registration Protocol"

**Priority Order (from spec):**
1. Pre-registered credentials (hardcoded or user-entered)
2. **CIMD (Client ID Metadata Documents)** - SHOULD support
3. DCR (Dynamic Client Registration) - MAY support (backwards compatibility only)

**Aaron Parecki (Nov 2025):**
> "DCR introduces a massive amount of complexity and risk... unbounded database growth, rate limiting issues, security risks"
> "The new MCP spec solves this by adopting Client ID Metadata Documents"

**Evidence:** Official Python SDK implements CIMD:
```python
def should_use_client_metadata_url(
    oauth_metadata: OAuthMetadata | None,
    client_metadata_url: str | None,
) -> bool:
    """Determine if URL-based client ID (CIMD) should be used instead of DCR."""
    if not client_metadata_url:
        return False
    return oauth_metadata.client_id_metadata_document_supported is True
```

**Conclusion:** Use CIMD as primary, DCR as fallback only.

---

### 2. Redirect URIs: Fixed Localhost Ports, NOT Ephemeral

**Official MCP Spec Example:**
```json
"redirect_uris": [
  "http://127.0.0.1:3000/callback",
  "http://localhost:3000/callback"
]
```

**Upstash Production Implementation:**
```json
"redirect_uris": [
  "http://127.0.0.1:54321/callback",
  "cursor://anysphere.cursor-mcp/oauth/callback"
]
```

**MercadoLibre MCP (Antigravity/Cursor):**
```bash
npx mcp-remote https://mcp.mercadolibre.com/mcp 18999
# Port 18999 is FIXED, passed as argument
```

**Cursor Forum Evidence:**
- Cursor uses: `cursor://anysphere.cursor-mcp/oauth/callback`
- mcp-remote uses: `http://localhost:18999` (fixed port)
- Port conflicts occur when multiple clients try same port

**RFC 8252 vs Reality:**
- RFC 8252 Section 7.3: Recommends ephemeral ports for security
- **MCP Reality:** Clients use FIXED ports (3000, 18999, 54321, etc.)
- **Reason:** Clients must pre-register redirect URIs; can't register wildcard ephemeral ports

**Conclusion:** Use fixed localhost ports, NOT ephemeral. Support both 127.0.0.1 and localhost.

---

### 3. Custom URI Schemes: Used in Practice, Not in Spec

**Official MCP Spec:** No mention of custom URI schemes in examples

**Production Evidence:**
- Cursor: `cursor://anysphere.cursor-mcp/oauth/callback`
- Goose: `goose://callback` (referenced in forums)
- Upstash registers BOTH localhost AND custom URI

**Cursor Forum Issues:**
- Linux AppImage: `cursor://` protocol handler broken
- Windows/macOS: Works reliably

**Conclusion:** Support custom URI schemes as OPTIONAL for Cursor/Goose compatibility, but localhost is PRIMARY.

---

### 4. Localhost vs 127.0.0.1: Both Required with Normalization

**Upstash Production Issue:**
> "Some MCP clients register redirect URIs with localhost, but then send 127.0.0.1 during the OAuth flow (or vice versa)"

**Solution (from Upstash):**
```typescript
// Normalize loopback addresses at three points:
// 1. During registration: Expand both localhost and 127.0.0.1
// 2. During authorization: Normalize redirect_uri
// 3. During token exchange: Normalize again (always to localhost)
```

**Official MCP Spec:**
> "All redirect URIs MUST be either localhost or use HTTPS"

**RFC 8252 Section 8.3:**
> "The use of localhost is NOT RECOMMENDED. Specifying a redirect URI with the loopback IP literal (127.0.0.1) avoids inadvertently listening on network interfaces other than the loopback interface."

**Conclusion:** Support BOTH, normalize during validation. Prefer 127.0.0.1 for security but accept localhost for compatibility.

---

## Updated Architecture

### Client Registration Flow (CIMD Primary)

```
┌─────────────────┐
│   MCP Client    │
│  (Cursor/Goose) │
└────────┬────────┘
         │ 1. Hosts client metadata at HTTPS URL
         │    https://cursor.com/.well-known/client-metadata.json
         │
         │ 2. POST /oauth/authorize
         │    client_id=https://cursor.com/.well-known/client-metadata.json
         ▼
┌─────────────────────────────────────────────────────────────┐
│                    identity-service                          │
│  (Proxies Ory Hydra)                                         │
└────────┬────────────────────────────────────────────────────┘
         │ 3. Fetches client metadata from URL
         │    GET https://cursor.com/.well-known/client-metadata.json
         │
         │ 4. Validates:
         │    - client_id in document matches URL
         │    - redirect_uris are valid
         │    - Document structure is valid JSON
         │
         │ 5. Caches metadata (respects HTTP cache headers)
         │
         │ 6. Proceeds with authorization flow
         ▼
```

### Redirect URI Patterns

**Primary Pattern (Localhost with Fixed Port):**
```
http://127.0.0.1:54321/callback
http://localhost:54321/callback
```

**Optional Pattern (Custom URI Schemes):**
```
cursor://anysphere.cursor-mcp/oauth/callback
goose://callback
```

**Client Metadata Example:**
```json
{
  "client_id": "https://cursor.com/.well-known/client-metadata.json",
  "client_name": "Cursor IDE",
  "client_uri": "https://cursor.com",
  "logo_uri": "https://cursor.com/logo.png",
  "redirect_uris": [
    "http://127.0.0.1:54321/callback",
    "http://localhost:54321/callback",
    "cursor://anysphere.cursor-mcp/oauth/callback"
  ],
  "grant_types": ["authorization_code", "refresh_token"],
  "response_types": ["code"],
  "token_endpoint_auth_method": "none"
}
```

---

## Implementation Requirements

### For identity-service (Ory Hydra Proxy)

**1. CIMD Support (Primary):**
```python
async def handle_authorization_request(client_id: str):
    # Check if client_id is HTTPS URL
    if is_valid_client_metadata_url(client_id):
        # Fetch client metadata
        metadata = await fetch_client_metadata(client_id)
        
        # Validate
        if metadata["client_id"] != client_id:
            raise ValueError("client_id mismatch")
        
        # Cache with HTTP cache headers
        cache_client_metadata(client_id, metadata)
        
        # Use metadata for authorization
        return metadata
    else:
        # Fall back to DCR or pre-registered clients
        return await get_registered_client(client_id)
```

**2. Redirect URI Normalization:**
```python
def normalize_redirect_uri(uri: str) -> list[str]:
    """Expand localhost to both localhost and 127.0.0.1"""
    parsed = urlparse(uri)
    
    if parsed.hostname == "localhost":
        # Register both variants
        return [
            uri,
            uri.replace("localhost", "127.0.0.1")
        ]
    elif parsed.hostname == "127.0.0.1":
        # Register both variants
        return [
            uri,
            uri.replace("127.0.0.1", "localhost")
        ]
    else:
        return [uri]
```

**3. Redirect URI Validation:**
```python
def validate_redirect_uri(uri: str) -> bool:
    """Validate per MCP spec"""
    parsed = urlparse(uri)
    
    # HTTPS always allowed
    if parsed.scheme == "https":
        return True
    
    # HTTP only for localhost/127.0.0.1
    if parsed.scheme == "http":
        return parsed.hostname in ["localhost", "127.0.0.1"]
    
    # Custom URI schemes (optional, for Cursor/Goose)
    if parsed.scheme in ["cursor", "goose"]:
        return True
    
    return False
```

**4. Authorization Server Metadata:**
```json
{
  "issuer": "https://auth.nutgraf.in",
  "authorization_endpoint": "https://auth.nutgraf.in/oauth2/auth",
  "token_endpoint": "https://auth.nutgraf.in/oauth2/token",
  "registration_endpoint": "https://auth.nutgraf.in/oauth2/register",
  "jwks_uri": "https://auth.nutgraf.in/.well-known/jwks.json",
  "response_types_supported": ["code"],
  "grant_types_supported": ["authorization_code", "refresh_token"],
  "code_challenge_methods_supported": ["S256"],
  "token_endpoint_auth_methods_supported": ["none"],
  "client_id_metadata_document_supported": true
}
```

---

## Security Considerations

### 1. CIMD Security (from MCP Spec Section 6)

**Server-Side Request Forgery (SSRF):**
- Authorization server fetches URLs provided by untrusted clients
- Could trigger requests to private admin endpoints

**Mitigation:**
- Validate HTTPS scheme only
- Block private IP ranges (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16)
- Implement request timeouts
- Rate limit metadata fetches per client_id

**Localhost Redirect URI Risks:**
- Attacker can claim legitimate client's metadata URL
- Bind to any localhost port
- Receive authorization code when user approves

**Mitigation:**
- Display warnings for localhost-only redirect URIs
- Show redirect URI hostname prominently during authorization
- Consider additional attestation for localhost clients

### 2. Redirect URI Security

**From Upstash:**
> "Clients register localhost but send 127.0.0.1 (or vice versa). OAuth requires exact string matching, so this breaks."

**Solution:** Normalize at three points (registration, authorization, token exchange)

---

## Migration Path

### Phase 1: Add CIMD Support (Week 1-2)
- Implement `client_id_metadata_document_supported: true` in metadata
- Add CIMD validation logic
- Add metadata caching with HTTP cache headers
- Keep DCR as fallback

### Phase 2: Redirect URI Normalization (Week 2-3)
- Implement localhost/127.0.0.1 expansion during registration
- Add normalization during authorization
- Add normalization during token exchange
- Test with Cursor, Goose, mcp-remote

### Phase 3: Custom URI Scheme Support (Week 3-4)
- Add cursor:// and goose:// to allowed schemes
- Test with Cursor on Windows/macOS/Linux
- Document known issues (Linux AppImage)

### Phase 4: Deprecate DCR (Week 4+)
- Monitor CIMD adoption
- Add deprecation warnings to DCR endpoint
- Eventually remove DCR (6-12 months)

---

## References

1. **MCP Specification 2025-11-25:** https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization
2. **Aaron Parecki - Enterprise-Ready MCP:** https://aaronparecki.com/2025/05/12/27/enterprise-ready-mcp
3. **Aaron Parecki - MCP Authorization Spec Update:** https://aaronparecki.com/2025/11/25/1/mcp-authorization-spec-update
4. **Upstash - Implementing MCP OAuth:** https://upstash.com/blog/mcp-oauth-implementation
5. **OAuth Client ID Metadata Documents:** draft-ietf-oauth-client-id-metadata-document-00
6. **RFC 8252 - OAuth 2.0 for Native Apps:** https://datatracker.ietf.org/doc/html/rfc8252
7. **MCP Python SDK:** Official reference implementation
8. **Cursor Forum:** Real-world implementation issues and solutions

---

## Conclusion

The official MCP Nov 2025 spec, combined with production evidence, provides clear guidance:

1. **Use CIMD as primary registration method** (DCR is deprecated)
2. **Use fixed localhost ports** (NOT ephemeral ports from RFC 8252)
3. **Support both localhost AND 127.0.0.1** with normalization
4. **Optionally support custom URI schemes** for Cursor/Goose
5. **Implement SSRF protections** for CIMD metadata fetching

This approach balances security, compatibility, and adherence to the official MCP specification.

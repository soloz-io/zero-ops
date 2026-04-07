# API Gateway Manifests

## Components

- **AgentGateway**: MCP request router with extAuthz integration
- **demo-echo**: Test service that echoes HTTP headers

## Deployment

```bash
kubectl apply -k manifests/api-gateway/
```

## Configuration

AgentGateway is configured via ConfigMap to:
- Route `/mcp/*` requests through extAuthz validation
- Call auth-proxy at `auth-proxy.identity-services.svc.cluster.local:8080/internal/validate`
- Forward X-Auth-* headers to backend services
- Strip Authorization header before forwarding

## Testing

```bash
# Test health endpoint
curl http://agentgateway.api-gateway.svc.cluster.local:3000/health

# Test OAuth metadata
curl http://agentgateway.api-gateway.svc.cluster.local:3000/.well-known/oauth-protected-resource

# Test authenticated request (requires JWT)
curl -H "Authorization: Bearer <jwt>" http://agentgateway.api-gateway.svc.cluster.local:3000/mcp/test
```

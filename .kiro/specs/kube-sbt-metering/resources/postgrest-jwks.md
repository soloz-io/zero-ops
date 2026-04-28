### Root Cause & Architectural Flaw

The current configuration in your Crossplane Composition (`ainativesaas-starter-hetzner.yaml`) attempts to mount a static Kubernetes Secret (`hub-ory-jwt-secret`) into the PostgREST deployment on the **Spoke cluster**. 

This creates a **distributed monolith anti-pattern**:
1. **Secret Leakage:** Requires syncing highly sensitive identity signing keys (Hub's private domain) to loosely-trusted tenant clusters (Spoke domain).
2. **Rotation Nightmare:** Rotating the JWT signing key in Ory Hydra would require synchronizing the new secret across hundreds of Spoke clusters simultaneously, causing downtime.
3. **Cross-Cluster Coupling:** Breaks the independent lifecycle of Hub and Spoke clusters.

---

### The Idiomatic Enterprise Solution: Asymmetric JWKS Validation

Instead of sharing symmetric keys across cluster boundaries, we should leverage **Asymmetric Cryptography (RS256)** and the **JSON Web Key Set (JWKS)** protocol. 

PostgREST (since v9.0) natively supports fetching and caching JWKS from a remote URL. Since you are already exposing Ory Hydra via `https://auth.nutgraf.in` and configuring it to issue JWTs, we simply need to point the Spoke's PostgREST to Hydra's public JWKS endpoint.

#### The Flow:
1. **Hub (Ory Hydra):** Signs JWTs using its private key and exposes public keys at `https://auth.nutgraf.in/.well-known/jwks.json`.
2. **Spoke (PostgREST):** Bootstraps with the `jwks_uri`. Upon receiving a request, it fetches the public keys from the Hub, caches them locally in memory for 1 hour, and cryptographically verifies the JWT signatures without needing the actual secret.

---

### Required Codebase Changes

We need to update the Crossplane Composition that provisions PostgREST in the Spoke cluster. We will replace the `secretKeyRef` with a JSON string containing the `jwks_uri`, and set the correct Audience.

**File:** `manifests/hub-core-services/crossplane/tenant-platform/compositions/ainativesaas-starter-hetzner.yaml`

Find the `postgrest-deployment` resource (around line 147) and update the `env` block for the `postgrest` container:

#### ❌ Remove:
```yaml
                    - name: PGRST_JWT_SECRET
                      valueFrom:
                        secretKeyRef:
                          name: hub-ory-jwt-secret
                          key: secret
```

#### ✅ Add:
```yaml
                    # Use JWKS for stateless, asymmetric JWT validation from the Hub
                    - name: PGRST_JWT_SECRET
                      value: '{"jwks_uri": "https://auth.nutgraf.in/.well-known/jwks.json"}'
                    # Enforce the Audience claim set by Auth Proxy / Agent Gateway
                    - name: PGRST_JWT_AUD
                      value: "https://api.nutgraf.in/mcp"
```

#### Updated PostgREST Container Block:
```yaml
                  containers:
                  - name: postgrest
                    image: postgrest/postgrest:v12.0.2
                    ports:
                    - containerPort: 3000
                      name: http
                    env:
                    - name: PGRST_DB_URI
                      valueFrom:
                        secretKeyRef:
                          name: ""   # Patched: <tenantId>-db-credentials
                          key: url
                    - name: PGRST_DB_SCHEMA
                      value: "public"
                    - name: PGRST_DB_ANON_ROLE
                      value: ""  # Patched: tenant-<tenantId>-user
                    # Enterprise JWKS Configuration
                    - name: PGRST_JWT_SECRET
                      value: '{"jwks_uri": "https://auth.nutgraf.in/.well-known/jwks.json"}'
                    - name: PGRST_JWT_AUD
                      value: "https://api.nutgraf.in/mcp"
                    # Cache public keys locally for 1 hour to prevent flooding Hydra
                    - name: PGRST_JWT_CACHE_MAX_LIFETIME
                      value: "3600"
```

*(Note: The audience `https://api.nutgraf.in/mcp` matches the audience you are injecting in `internal/auth-proxy/handlers.go` via the `acceptConsent` and `proxyDCR` functions).*

---

### Why this is the "Enterprise Grade" approach

1. **Zero Secret Replication:** You no longer need External Secrets Operator (ESO) to sync auth secrets between the Hub and Spokes.
2. **Hitless Key Rotation:** If Ory Hydra rotates its cryptographic keys, the new keys automatically appear at `/.well-known/jwks.json`. PostgREST will fetch the new keys on cache miss, causing zero downtime for tenants.
3. **Stateless Scale:** Spoke clusters can be spun up, destroyed, and scaled instantly without waiting for secrets to propagate across the cluster mesh.
4. **Native Capability:** PostgREST handles the JWKS fetching and caching natively in C/Haskell, eliminating the need to inject a heavy Envoy/OPA sidecar into every tenant namespace just for JWT validation.

### Optional: Sidecar Pattern (When to use it)

While PostgREST handles JWKS perfectly, if your platform evolves to require **Claim Transformation** (e.g., mapping custom Ory Kratos traits into PostgreSQL roles dynamically) or **Rate Limiting per Tenant Route**, you would inject an Envoy or API7 sidecar into the `postgrest-deployment` pod. 

Given your current architecture, the AgentGateway (Rust) at the Hub edge is already handling rate limiting and MCP routing, making the native PostgREST JWKS implementation the most optimal and performant choice for the Spoke layer.
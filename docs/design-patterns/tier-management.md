---
inclusion: always
---
<!------------------------------------------------------------------------------------
   Add rules to this file or a short description and have Kiro refine them for you.
   
   Learn about inclusion modes: https://kiro.dev/docs/steering/#inclusion-modes
-------------------------------------------------------------------------------------> 

# open-sbt Tier Management

## Overview

In SBT-AWS, tiers are **not a separate entity** but rather **attributes on tenants**. This flexible approach allows tiers to be defined implicitly through tenant configuration rather than requiring explicit tier CRUD operations.

## Tier as Tenant Attribute

**Key Insight from SBT-AWS:**
- Tier is passed as part of `tenantData` during tenant registration
- Tier flows through events (e.g., `onboardingRequest` includes `tier` field)
- Provisioning logic can branch based on tier value
- No separate tier management service needed

**Example from SBT-AWS:**
```typescript
// Tenant registration includes tier
{
  "tenantData": {
    "tenantName": "acme-corp",
    "email": "admin@acme.com",
    "tier": "premium"  // ← Tier is just an attribute
  }
}

// Provisioning script receives tier as environment variable
environmentStringVariablesFromIncomingEvent: ['tenantId', 'tier', 'tenantName', 'email']
```

## open-sbt Tier Implementation

### Approach 1: Tier as Tenant Attribute (Recommended)

**Matches SBT-AWS pattern** - Simple and flexible.

```go
// Tenant model includes tier
type Tenant struct {
    ID        string    `json:"id"`
    Name      string    `json:"name"`
    Email     string    `json:"email"`
    Tier      string    `json:"tier"`      // ← Simple attribute
    Status    string    `json:"status"`
    CreatedAt time.Time `json:"createdAt"`
}
```

**Provisioning based on tier:**
```go
func (p *ArgoWorkflowProvisioner) ProvisionTenant(ctx context.Context, req ProvisionRequest) (*ProvisionResult, error) {
    // Branch provisioning logic based on tier
    var resources []ResourceSpec
    
    switch req.Tier {
    case "basic":
        resources = []ResourceSpec{
            {Type: "namespace", Name: fmt.Sprintf("tenant-%s", req.TenantID)},
            {Type: "database", Name: "shared-db", Parameters: map[string]interface{}{"size": "small"}},
        }
    case "premium":
        resources = []ResourceSpec{
            {Type: "namespace", Name: fmt.Sprintf("tenant-%s", req.TenantID)},
            {Type: "database", Name: fmt.Sprintf("tenant-%s-db", req.TenantID), Parameters: map[string]interface{}{"size": "medium"}},
            {Type: "s3bucket", Name: fmt.Sprintf("tenant-%s-storage", req.TenantID)},
        }
    case "enterprise":
        resources = []ResourceSpec{
            {Type: "namespace", Name: fmt.Sprintf("tenant-%s", req.TenantID)},
            {Type: "database", Name: fmt.Sprintf("tenant-%s-db", req.TenantID), Parameters: map[string]interface{}{"size": "large", "replicas": 3}},
            {Type: "s3bucket", Name: fmt.Sprintf("tenant-%s-storage", req.TenantID)},
            {Type: "redis", Name: fmt.Sprintf("tenant-%s-cache", req.TenantID)},
        }
    }
    
    return p.provisionResources(ctx, req.TenantID, resources)
}
```

### Approach 2: Tier Configuration Table (Optional)

If you need centralized tier definitions with quotas and limits:

```go
// ITierManager interface (optional)
type ITierManager interface {
    GetTier(ctx context.Context, tierName string) (*TierConfig, error)
    ListTiers(ctx context.Context) ([]TierConfig, error)
    ValidateTierQuota(ctx context.Context, tierName string, usage ResourceUsage) error
}

type TierConfig struct {
    Name        string                 `json:"name"`
    DisplayName string                 `json:"displayName"`
    Description string                 `json:"description"`
    Quotas      map[string]int64       `json:"quotas"`      // e.g., {"users": 100, "storage_gb": 50}
    Features    []string               `json:"features"`    // e.g., ["sso", "api_access"]
    Pricing     map[string]interface{} `json:"pricing"`
}

// PostgreSQL schema
CREATE TABLE tier_configs (
    name VARCHAR(50) PRIMARY KEY,
    display_name VARCHAR(100) NOT NULL,
    description TEXT,
    quotas JSONB NOT NULL DEFAULT '{}',
    features JSONB NOT NULL DEFAULT '[]',
    pricing JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Example tier configs
INSERT INTO tier_configs (name, display_name, quotas, features, pricing) VALUES
('basic', 'Basic', '{"users": 10, "storage_gb": 10}', '["basic_support"]', '{"monthly_usd": 29}'),
('premium', 'Premium', '{"users": 100, "storage_gb": 100}', '["priority_support", "api_access"]', '{"monthly_usd": 99}'),
('enterprise', 'Enterprise', '{"users": -1, "storage_gb": -1}', '["dedicated_support", "sso", "api_access", "custom_domain"]', '{"monthly_usd": 499}');
```

**Quota enforcement middleware:**
```go
func TierQuotaMiddleware(tierManager ITierManager) gin.HandlerFunc {
    return func(c *gin.Context) {
        tenantID := c.GetString("tenant_id")
        tier := c.GetString("tenant_tier")
        
        // Get tier config
        tierConfig, err := tierManager.GetTier(c.Request.Context(), tier)
        if err != nil {
            c.JSON(500, gin.H{"error": "Failed to get tier config"})
            c.Abort()
            return
        }
        
        // Check quota (example: user creation)
        if c.Request.Method == "POST" && c.Request.URL.Path == "/users" {
            currentUserCount := getCurrentUserCount(c.Request.Context(), tenantID)
            maxUsers := tierConfig.Quotas["users"]
            
            if maxUsers != -1 && currentUserCount >= maxUsers {
                c.JSON(403, gin.H{"error": "User quota exceeded for your tier"})
                c.Abort()
                return
            }
        }
        
        c.Next()
    }
}
```

## Tier Upgrade/Downgrade

**Simple approach (matches SBT-AWS):**
```go
// Update tenant tier
func (s *TenantService) UpdateTenantTier(ctx context.Context, tenantID, newTier string) error {
    // 1. Update tenant record
    err := s.storage.UpdateTenant(ctx, tenantID, TenantUpdates{
        Tier: &newTier,
    })
    if err != nil {
        return err
    }
    
    // 2. Publish tier change event
    return s.eventBus.Publish(ctx, Event{
        DetailType: "opensbt_tierChanged",
        Source:     "zerosbt.control.plane",
        Detail: map[string]interface{}{
            "tenantId": tenantID,
            "oldTier":  oldTier,
            "newTier":  newTier,
        },
    })
}
```

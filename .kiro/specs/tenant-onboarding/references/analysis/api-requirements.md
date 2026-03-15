# API Requirements: Enterprise SaaS Tenant Onboarding

**Version:** 1.0  
**Status:** DRAFT  
**Last Updated:** 2026-03-08

---

## 1. Overview

This document defines the REST API requirements for zero-ops tenant onboarding and lifecycle management. The API enables CLI-driven and future Web UI-driven workflows for multi-tenant Kubernetes cluster provisioning.

**Design Principles:**
- RESTful API design with JSON payloads
- Tenant-scoped authentication via JWT tokens
- Kubernetes-native backend (ServiceAccounts, RBAC, ResourceQuota)
- PostgreSQL for tenant metadata, billing, and audit logs
- Idempotent operations for reliability

---

## 2. API Categories

### 2.1 Tenant Lifecycle Management
Core APIs for tenant onboarding, offboarding, and lifecycle state transitions.

### 2.2 Authentication & Authorization
Token generation, kubeconfig distribution, and team member management.

### 2.3 Quota & Resource Management
Quota enforcement, usage tracking, and limit management.

### 2.4 Billing & Metering
Usage event collection, invoice generation, and payment processing.

### 2.5 Cluster Management (Tenant-Scoped)
Cluster CRUD operations within tenant namespace boundaries.

### 2.6 Provider Credentials
Secure storage and retrieval of cloud provider credentials (Hetzner, AWS).

### 2.7 Audit & Compliance
Activity logging, audit trails, and GDPR compliance endpoints.

### 2.8 Admin Operations
Platform-wide tenant management and monitoring.

---

## 3. API Contracts

### 3.1 Tenant Lifecycle APIs

#### **POST /api/v1/tenants**
**Description:** Create a new tenant (onboarding)

**Request:**
```json
{
  "name": "acme-corp",
  "email": "admin@acme-corp.com",
  "plan": "professional",
  "quotas": {
    "maxClusters": 10,
    "maxNodes": 50,
    "maxCPU": "200",
    "maxMemory": "500Gi"
  },
  "metadata": {
    "company": "Acme Corporation",
    "industry": "Technology"
  }
}
```

**Response (201 Created):**
```json
{
  "tenantId": "tenant-acme-corp",
  "namespace": "tenant-acme-corp",
  "status": "active",
  "createdAt": "2026-03-08T10:00:00Z",
  "apiToken": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "kubeconfigUrl": "/api/v1/tenants/tenant-acme-corp/kubeconfig"
}
```

**Errors:**
- `409 Conflict` - Tenant name already exists
- `400 Bad Request` - Invalid quota values
- `402 Payment Required` - Payment method required for paid plans

---

#### **GET /api/v1/tenants/{tenantId}**
**Description:** Retrieve tenant details

**Response (200 OK):**
```json
{
  "tenantId": "tenant-acme-corp",
  "name": "acme-corp",
  "email": "admin@acme-corp.com",
  "plan": "professional",
  "status": "active",
  "quotas": {
    "maxClusters": 10,
    "maxNodes": 50,
    "maxCPU": "200",
    "maxMemory": "500Gi"
  },
  "usage": {
    "clusters": 3,
    "nodes": 15,
    "cpu": "45",
    "memory": "120Gi"
  },
  "createdAt": "2026-03-08T10:00:00Z",
  "lastActivity": "2026-03-08T14:30:00Z"
}
```

---

#### **PATCH /api/v1/tenants/{tenantId}**
**Description:** Update tenant configuration

**Request:**
```json
{
  "plan": "enterprise",
  "quotas": {
    "maxClusters": 50
  }
}
```

**Response (200 OK):**
```json
{
  "tenantId": "tenant-acme-corp",
  "plan": "enterprise",
  "quotas": {
    "maxClusters": 50,
    "maxNodes": 50,
    "maxCPU": "200",
    "maxMemory": "500Gi"
  },
  "updatedAt": "2026-03-08T15:00:00Z"
}
```

---

#### **DELETE /api/v1/tenants/{tenantId}**
**Description:** Offboard tenant (soft delete with grace period)

**Query Parameters:**
- `force=true` - Immediate deletion (skip grace period)
- `deleteData=true` - Delete all tenant data (clusters, secrets)

**Response (202 Accepted):**
```json
{
  "tenantId": "tenant-acme-corp",
  "status": "deleting",
  "gracePeriodEnds": "2026-03-15T10:00:00Z",
  "message": "Tenant will be permanently deleted after grace period"
}
```

---

#### **GET /api/v1/tenants**
**Description:** List all tenants (admin only)

**Query Parameters:**
- `status=active|suspended|deleting`
- `plan=free|professional|enterprise`
- `page=1&limit=50`

**Response (200 OK):**
```json
{
  "tenants": [
    {
      "tenantId": "tenant-acme-corp",
      "name": "acme-corp",
      "plan": "professional",
      "status": "active",
      "clusters": 3,
      "createdAt": "2026-03-08T10:00:00Z"
    }
  ],
  "pagination": {
    "page": 1,
    "limit": 50,
    "total": 127
  }
}
```

---

### 3.2 Authentication & Authorization APIs

#### **POST /api/v1/auth/login**
**Description:** Tenant authentication (email/password)

**Request:**
```json
{
  "email": "admin@acme-corp.com",
  "password": "SecurePassword123!"
}
```

**Response (200 OK):**
```json
{
  "accessToken": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "refreshToken": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "expiresIn": 3600,
  "tenantId": "tenant-acme-corp",
  "user": {
    "email": "admin@acme-corp.com",
    "role": "admin"
  }
}
```

---

#### **POST /api/v1/auth/token**
**Description:** Generate ServiceAccount token for programmatic access

**Request:**
```json
{
  "tenantId": "tenant-acme-corp",
  "name": "ci-pipeline",
  "expiresIn": "90d"
}
```

**Response (201 Created):**
```json
{
  "tokenId": "sa-token-abc123",
  "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "expiresAt": "2026-06-08T10:00:00Z",
  "permissions": ["clusters:read", "clusters:write"]
}
```

---

#### **GET /api/v1/auth/kubeconfig**
**Description:** Download tenant-scoped kubeconfig

**Headers:**
- `Authorization: Bearer <token>`

**Response (200 OK):**
```yaml
apiVersion: v1
kind: Config
clusters:
- cluster:
    certificate-authority-data: LS0tLS1CRUdJTi...
    server: https://api.nutgraf.in:6443
  name: mothership
contexts:
- context:
    cluster: mothership
    namespace: tenant-acme-corp
    user: tenant-acme-corp
  name: acme-corp@mothership
current-context: acme-corp@mothership
users:
- name: tenant-acme-corp
  user:
    token: eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...
```

---

#### **POST /api/v1/auth/invite**
**Description:** Invite team member to tenant

**Request:**
```json
{
  "email": "developer@acme-corp.com",
  "role": "developer",
  "permissions": ["clusters:read", "clusters:write"]
}
```

**Response (201 Created):**
```json
{
  "inviteId": "inv-xyz789",
  "email": "developer@acme-corp.com",
  "inviteUrl": "https://nutgraf.in/invite/inv-xyz789",
  "expiresAt": "2026-03-15T10:00:00Z"
}
```

---

### 3.3 Quota & Resource Management APIs

#### **GET /api/v1/tenants/{tenantId}/quotas**
**Description:** Get current quotas and usage

**Response (200 OK):**
```json
{
  "tenantId": "tenant-acme-corp",
  "quotas": {
    "maxClusters": 10,
    "maxNodes": 50,
    "maxCPU": "200",
    "maxMemory": "500Gi"
  },
  "usage": {
    "clusters": 3,
    "nodes": 15,
    "cpu": "45",
    "memory": "120Gi"
  },
  "utilization": {
    "clusters": "30%",
    "nodes": "30%",
    "cpu": "22.5%",
    "memory": "24%"
  }
}
```

---

#### **PATCH /api/v1/tenants/{tenantId}/quotas**
**Description:** Update tenant quotas (admin only)

**Request:**
```json
{
  "maxClusters": 20,
  "maxNodes": 100
}
```

**Response (200 OK):**
```json
{
  "tenantId": "tenant-acme-corp",
  "quotas": {
    "maxClusters": 20,
    "maxNodes": 100,
    "maxCPU": "200",
    "maxMemory": "500Gi"
  },
  "updatedAt": "2026-03-08T16:00:00Z"
}
```

---

### 3.4 Billing & Metering APIs

#### **POST /api/v1/billing/meter**
**Description:** Report usage event for metering

**Request:**
```json
{
  "tenantId": "tenant-acme-corp",
  "eventType": "cluster.running",
  "resourceId": "prod-api",
  "metrics": {
    "nodes": 5,
    "cpu": "20",
    "memory": "50Gi"
  },
  "timestamp": "2026-03-08T10:00:00Z"
}
```

**Response (202 Accepted):**
```json
{
  "eventId": "evt-abc123",
  "status": "queued",
  "message": "Usage event queued for processing"
}
```

---

#### **GET /api/v1/billing/usage/{tenantId}**
**Description:** Get usage summary for billing period

**Query Parameters:**
- `startDate=2026-03-01`
- `endDate=2026-03-31`

**Response (200 OK):**
```json
{
  "tenantId": "tenant-acme-corp",
  "period": {
    "start": "2026-03-01T00:00:00Z",
    "end": "2026-03-31T23:59:59Z"
  },
  "usage": {
    "clusterHours": 2160,
    "nodeHours": 10800,
    "cpuHours": 43200,
    "memoryGBHours": 108000
  },
  "cost": {
    "subtotal": 334.98,
    "tax": 33.50,
    "total": 368.48,
    "currency": "EUR"
  }
}
```

---

#### **GET /api/v1/billing/invoices/{tenantId}**
**Description:** List invoices for tenant

**Response (200 OK):**
```json
{
  "invoices": [
    {
      "invoiceId": "inv-2026-03",
      "period": "2026-03-01 to 2026-03-31",
      "amount": 368.48,
      "currency": "EUR",
      "status": "paid",
      "paidAt": "2026-04-01T10:00:00Z",
      "downloadUrl": "/api/v1/billing/invoices/inv-2026-03/pdf"
    }
  ]
}
```

---

#### **POST /api/v1/billing/payment-method**
**Description:** Add payment method (Stripe integration)

**Request:**
```json
{
  "tenantId": "tenant-acme-corp",
  "paymentMethodId": "pm_1234567890",
  "type": "card",
  "default": true
}
```

**Response (201 Created):**
```json
{
  "paymentMethodId": "pm_1234567890",
  "type": "card",
  "last4": "4242",
  "expiryMonth": 12,
  "expiryYear": 2028,
  "default": true
}
```

---

#### **GET /api/v1/billing/estimate**
**Description:** Calculate cost estimate for cluster configuration

**Query Parameters:**
- `clusterClass=hetzner-prod-ubuntu-v1`
- `workers=5`
- `hours=720`

**Response (200 OK):**
```json
{
  "estimate": {
    "clusterClass": "hetzner-prod-ubuntu-v1",
    "configuration": {
      "controlPlane": "3 x CPX31",
      "workers": "5 x CX21"
    },
    "hourlyRate": 0.464,
    "monthlyEstimate": 334.08,
    "currency": "EUR"
  }
}
```

---

### 3.5 Cluster Management APIs (Tenant-Scoped)

#### **POST /api/v1/clusters**
**Description:** Create cluster in tenant namespace

**Request:**
```json
{
  "name": "prod-api",
  "clusterClass": "hetzner-prod-ubuntu-v1",
  "version": "v1.31.0",
  "region": "fsn1",
  "workers": 5,
  "variables": {
    "sshKeys": ["acme-corp-key"]
  }
}
```

**Response (202 Accepted):**
```json
{
  "clusterId": "prod-api",
  "namespace": "tenant-acme-corp",
  "status": "provisioning",
  "estimatedTime": "8-10 minutes",
  "statusUrl": "/api/v1/clusters/prod-api"
}
```

---

#### **GET /api/v1/clusters**
**Description:** List tenant clusters

**Response (200 OK):**
```json
{
  "clusters": [
    {
      "clusterId": "prod-api",
      "clusterClass": "hetzner-prod-ubuntu-v1",
      "status": "ready",
      "version": "v1.31.0",
      "nodes": {
        "controlPlane": 3,
        "workers": 5
      },
      "createdAt": "2026-03-08T10:00:00Z",
      "apiEndpoint": "https://prod-api.acme-corp.clusters.nutgraf.in:6443"
    }
  ]
}
```

---

#### **GET /api/v1/clusters/{clusterId}**
**Description:** Get cluster details

**Response (200 OK):**
```json
{
  "clusterId": "prod-api",
  "namespace": "tenant-acme-corp",
  "clusterClass": "hetzner-prod-ubuntu-v1",
  "status": "ready",
  "version": "v1.31.0",
  "region": "fsn1",
  "nodes": {
    "controlPlane": 3,
    "workers": 5,
    "total": 8
  },
  "resources": {
    "cpu": "20",
    "memory": "50Gi"
  },
  "apiEndpoint": "https://prod-api.acme-corp.clusters.nutgraf.in:6443",
  "kubeconfigUrl": "/api/v1/clusters/prod-api/kubeconfig",
  "createdAt": "2026-03-08T10:00:00Z",
  "readyAt": "2026-03-08T10:08:32Z"
}
```

---

#### **DELETE /api/v1/clusters/{clusterId}**
**Description:** Delete cluster

**Query Parameters:**
- `confirm=true` (required)

**Response (202 Accepted):**
```json
{
  "clusterId": "prod-api",
  "status": "deleting",
  "message": "Cluster deletion in progress"
}
```

---

#### **PATCH /api/v1/clusters/{clusterId}/scale**
**Description:** Scale cluster workers

**Request:**
```json
{
  "workers": 10
}
```

**Response (202 Accepted):**
```json
{
  "clusterId": "prod-api",
  "workers": {
    "current": 5,
    "desired": 10
  },
  "status": "scaling",
  "estimatedTime": "3-5 minutes"
}
```

---

### 3.6 Provider Credentials APIs

#### **POST /api/v1/credentials**
**Description:** Store cloud provider credentials

**Request:**
```json
{
  "provider": "hetzner",
  "name": "hetzner-production",
  "credentials": {
    "token": "hcloud_token_abc123..."
  }
}
```

**Response (201 Created):**
```json
{
  "credentialId": "cred-xyz789",
  "provider": "hetzner",
  "name": "hetzner-production",
  "createdAt": "2026-03-08T10:00:00Z",
  "lastUsed": null
}
```

---

#### **GET /api/v1/credentials**
**Description:** List stored credentials

**Response (200 OK):**
```json
{
  "credentials": [
    {
      "credentialId": "cred-xyz789",
      "provider": "hetzner",
      "name": "hetzner-production",
      "createdAt": "2026-03-08T10:00:00Z",
      "lastUsed": "2026-03-08T14:30:00Z"
    }
  ]
}
```

---

#### **DELETE /api/v1/credentials/{credentialId}**
**Description:** Remove credentials

**Response (204 No Content)**

---

### 3.7 Audit & Compliance APIs

#### **GET /api/v1/audit/logs**
**Description:** Get audit trail (tenant-scoped)

**Query Parameters:**
- `startDate=2026-03-01`
- `endDate=2026-03-08`
- `action=cluster.create|cluster.delete|tenant.update`
- `page=1&limit=100`

**Response (200 OK):**
```json
{
  "logs": [
    {
      "eventId": "evt-abc123",
      "timestamp": "2026-03-08T10:00:00Z",
      "action": "cluster.create",
      "resource": "prod-api",
      "user": "admin@acme-corp.com",
      "ipAddress": "203.0.113.42",
      "status": "success"
    }
  ],
  "pagination": {
    "page": 1,
    "limit": 100,
    "total": 347
  }
}
```

---

#### **GET /api/v1/audit/events/{tenantId}**
**Description:** Tenant activity log (admin only)

**Response (200 OK):**
```json
{
  "tenantId": "tenant-acme-corp",
  "events": [
    {
      "timestamp": "2026-03-08T10:00:00Z",
      "type": "cluster.provisioned",
      "details": {
        "clusterId": "prod-api",
        "duration": "8m32s"
      }
    }
  ]
}
```

---

#### **POST /api/v1/compliance/export**
**Description:** GDPR data export request

**Request:**
```json
{
  "tenantId": "tenant-acme-corp",
  "format": "json"
}
```

**Response (202 Accepted):**
```json
{
  "exportId": "exp-abc123",
  "status": "processing",
  "estimatedTime": "5-10 minutes",
  "downloadUrl": "/api/v1/compliance/export/exp-abc123/download"
}
```

---

### 3.8 Admin Operations APIs

#### **GET /api/v1/admin/tenants**
**Description:** List all tenants with metrics (admin only)

**Response (200 OK):**
```json
{
  "tenants": [
    {
      "tenantId": "tenant-acme-corp",
      "name": "acme-corp",
      "plan": "professional",
      "status": "active",
      "clusters": 3,
      "nodes": 15,
      "monthlyRevenue": 368.48,
      "createdAt": "2026-03-08T10:00:00Z"
    }
  ],
  "summary": {
    "totalTenants": 127,
    "activeTenants": 115,
    "suspendedTenants": 12,
    "totalClusters": 342,
    "monthlyRevenue": 45678.90
  }
}
```

---

#### **POST /api/v1/admin/tenants/{tenantId}/suspend**
**Description:** Suspend tenant (non-payment, policy violation)

**Request:**
```json
{
  "reason": "payment_failed",
  "message": "Payment method declined"
}
```

**Response (200 OK):**
```json
{
  "tenantId": "tenant-acme-corp",
  "status": "suspended",
  "suspendedAt": "2026-03-08T16:00:00Z",
  "reason": "payment_failed"
}
```

---

#### **POST /api/v1/admin/tenants/{tenantId}/activate**
**Description:** Reactivate suspended tenant

**Response (200 OK):**
```json
{
  "tenantId": "tenant-acme-corp",
  "status": "active",
  "activatedAt": "2026-03-08T17:00:00Z"
}
```

---

#### **GET /api/v1/admin/metrics**
**Description:** Platform-wide metrics

**Response (200 OK):**
```json
{
  "platform": {
    "tenants": {
      "total": 127,
      "active": 115,
      "suspended": 12
    },
    "clusters": {
      "total": 342,
      "provisioning": 5,
      "ready": 330,
      "failed": 7
    },
    "resources": {
      "totalNodes": 1542,
      "totalCPU": "6168",
      "totalMemory": "15420Gi"
    },
    "revenue": {
      "monthly": 45678.90,
      "annual": 548146.80,
      "currency": "EUR"
    }
  },
  "timestamp": "2026-03-08T18:00:00Z"
}
```

---

## 4. Authentication & Security

### 4.1 Authentication Methods

**JWT Tokens:**
- Access token: 1 hour expiry
- Refresh token: 30 days expiry
- ServiceAccount tokens: Configurable expiry (default 90 days)

**Headers:**
```
Authorization: Bearer <jwt-token>
X-Tenant-ID: tenant-acme-corp (optional, for admin operations)
```

### 4.2 Rate Limiting

**Per Tenant:**
- 1000 requests/hour (standard plan)
- 5000 requests/hour (professional plan)
- 20000 requests/hour (enterprise plan)

**Response Headers:**
```
X-RateLimit-Limit: 1000
X-RateLimit-Remaining: 847
X-RateLimit-Reset: 1678291200
```

### 4.3 Error Responses

**Standard Error Format:**
```json
{
  "error": {
    "code": "QUOTA_EXCEEDED",
    "message": "Tenant has reached maximum cluster limit (10/10)",
    "details": {
      "current": 10,
      "limit": 10
    },
    "timestamp": "2026-03-08T10:00:00Z"
  }
}
```

**HTTP Status Codes:**
- `400` - Bad Request (validation errors)
- `401` - Unauthorized (missing/invalid token)
- `403` - Forbidden (insufficient permissions)
- `404` - Not Found
- `409` - Conflict (duplicate resource)
- `422` - Unprocessable Entity (business logic error)
- `429` - Too Many Requests (rate limit)
- `500` - Internal Server Error

---

## 5. CLI Command Mapping

```bash
# Tenant Management
zero-ops tenant onboard --name=acme-corp --plan=professional
  → POST /api/v1/tenants

zero-ops tenant list
  → GET /api/v1/tenants

zero-ops tenant show acme-corp
  → GET /api/v1/tenants/tenant-acme-corp

# Authentication
zero-ops login --email=admin@acme-corp.com
  → POST /api/v1/auth/login

zero-ops kubeconfig download
  → GET /api/v1/auth/kubeconfig

# Cluster Management
zero-ops cluster create --name=prod-api --class=hetzner-prod-ubuntu-v1 --workers=5
  → POST /api/v1/clusters

zero-ops cluster list
  → GET /api/v1/clusters

zero-ops cluster scale prod-api --workers=10
  → PATCH /api/v1/clusters/prod-api/scale

zero-ops cluster delete prod-api --confirm
  → DELETE /api/v1/clusters/prod-api

# Credentials
zero-ops credentials add --provider=hetzner --name=production
  → POST /api/v1/credentials

# Billing
zero-ops billing usage --month=2026-03
  → GET /api/v1/billing/usage/tenant-acme-corp?startDate=2026-03-01&endDate=2026-03-31

zero-ops billing invoices
  → GET /api/v1/billing/invoices/tenant-acme-corp
```

---

## 6. Implementation Phases

**Phase 1: Core Tenant APIs (MVP)**
- Tenant CRUD operations
- Authentication (JWT + ServiceAccount tokens)
- Kubeconfig generation
- Basic quota management

**Phase 2: Cluster Management**
- Cluster CRUD operations
- Provider credentials storage
- Cluster scaling

**Phase 3: Billing & Metering**
- Usage event collection
- Invoice generation
- Payment method integration (Stripe)

**Phase 4: Advanced Features**
- Audit logging
- GDPR compliance
- Admin dashboards
- Team member management

---

## 7. Database Schema (PostgreSQL)

**Tables:**
```sql
-- Tenants
CREATE TABLE tenants (
    id UUID PRIMARY KEY,
    name VARCHAR(255) UNIQUE NOT NULL,
    email VARCHAR(255) NOT NULL,
    plan VARCHAR(50) NOT NULL,
    status VARCHAR(50) NOT NULL,
    namespace VARCHAR(255) UNIQUE NOT NULL,
    quotas JSONB NOT NULL,
    metadata JSONB,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

-- Users (team members)
CREATE TABLE users (
    id UUID PRIMARY KEY,
    tenant_id UUID REFERENCES tenants(id),
    email VARCHAR(255) NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    role VARCHAR(50) NOT NULL,
    created_at TIMESTAMP NOT NULL
);

-- Credentials
CREATE TABLE credentials (
    id UUID PRIMARY KEY,
    tenant_id UUID REFERENCES tenants(id),
    provider VARCHAR(50) NOT NULL,
    name VARCHAR(255) NOT NULL,
    encrypted_data TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL,
    last_used_at TIMESTAMP
);

-- Usage Events
CREATE TABLE usage_events (
    id UUID PRIMARY KEY,
    tenant_id UUID REFERENCES tenants(id),
    event_type VARCHAR(100) NOT NULL,
    resource_id VARCHAR(255),
    metrics JSONB NOT NULL,
    timestamp TIMESTAMP NOT NULL
);

-- Invoices
CREATE TABLE invoices (
    id UUID PRIMARY KEY,
    tenant_id UUID REFERENCES tenants(id),
    period_start DATE NOT NULL,
    period_end DATE NOT NULL,
    amount DECIMAL(10,2) NOT NULL,
    currency VARCHAR(3) NOT NULL,
    status VARCHAR(50) NOT NULL,
    paid_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL
);

-- Audit Logs
CREATE TABLE audit_logs (
    id UUID PRIMARY KEY,
    tenant_id UUID REFERENCES tenants(id),
    user_id UUID REFERENCES users(id),
    action VARCHAR(100) NOT NULL,
    resource VARCHAR(255),
    ip_address INET,
    status VARCHAR(50) NOT NULL,
    timestamp TIMESTAMP NOT NULL
);
```

---

## 8. Next Steps

1. Review and approve API contracts
2. Create OpenAPI/Swagger specification
3. Implement zero-ops-api backend (Go)
4. Implement CLI commands with API integration
5. Add integration tests for API endpoints
6. Deploy to staging environment for validation

---

**End of Document**

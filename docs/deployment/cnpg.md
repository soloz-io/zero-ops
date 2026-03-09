# CloudNativePG (CNPG) Production Deployment

This guide covers deploying zero-ops-api with CloudNativePG operator for production PostgreSQL.

## Prerequisites

- Kubernetes cluster (1.25+)
- kubectl configured
- CloudNativePG operator installed

## Install CNPG Operator

```bash
kubectl apply -f \
  https://raw.githubusercontent.com/cloudnative-pg/cloudnative-pg/release-1.22/releases/cnpg-1.22.0.yaml
```

## Deploy PostgreSQL Cluster

```yaml
# postgres-cluster.yaml
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: zero-ops-db
  namespace: zero-ops
spec:
  instances: 3
  
  postgresql:
    parameters:
      max_connections: "200"
      shared_buffers: "256MB"
      effective_cache_size: "1GB"
      work_mem: "16MB"
  
  bootstrap:
    initdb:
      database: zeroops
      owner: zeroops
      secret:
        name: zero-ops-db-credentials
  
  storage:
    size: 20Gi
    storageClass: standard
  
  monitoring:
    enabled: true
  
  backup:
    barmanObjectStore:
      destinationPath: s3://zero-ops-backups/postgres
      s3Credentials:
        accessKeyId:
          name: backup-credentials
          key: ACCESS_KEY_ID
        secretAccessKey:
          name: backup-credentials
          key: SECRET_ACCESS_KEY
      wal:
        compression: gzip
    retentionPolicy: "30d"
```

## Create Database Credentials Secret

```bash
kubectl create secret generic zero-ops-db-credentials \
  --namespace=zero-ops \
  --from-literal=username=zeroops \
  --from-literal=password=$(openssl rand -base64 32)
```

## Run Migrations

```bash
# Get database connection string
DB_HOST=$(kubectl get cluster zero-ops-db -n zero-ops -o jsonpath='{.status.writeService}')
DB_PASSWORD=$(kubectl get secret zero-ops-db-credentials -n zero-ops -o jsonpath='{.data.password}' | base64 -d)

# Run migrations (from local machine or CI/CD)
export DATABASE_URL="postgres://zeroops:${DB_PASSWORD}@${DB_HOST}:5432/zeroops?sslmode=require"
make migrate-up
```

## Deploy API

```yaml
# api-deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: zero-ops-api
  namespace: zero-ops
spec:
  replicas: 3
  selector:
    matchLabels:
      app: zero-ops-api
  template:
    metadata:
      labels:
        app: zero-ops-api
    spec:
      containers:
      - name: api
        image: zero-ops-api:latest
        ports:
        - containerPort: 8080
          name: http
        env:
        - name: SERVER_PORT
          value: "8080"
        - name: DATABASE_URL
          valueFrom:
            secretKeyRef:
              name: zero-ops-db-app-connection
              key: uri
        - name: LOG_LEVEL
          value: "info"
        - name: ENVIRONMENT
          value: "production"
        resources:
          requests:
            memory: "128Mi"
            cpu: "100m"
          limits:
            memory: "512Mi"
            cpu: "500m"
        livenessProbe:
          httpGet:
            path: /healthz
            port: 8080
          initialDelaySeconds: 10
          periodSeconds: 30
        readinessProbe:
          httpGet:
            path: /readyz
            port: 8080
          initialDelaySeconds: 5
          periodSeconds: 10
---
apiVersion: v1
kind: Service
metadata:
  name: zero-ops-api
  namespace: zero-ops
spec:
  selector:
    app: zero-ops-api
  ports:
  - port: 80
    targetPort: 8080
    name: http
  type: ClusterIP
```

## Apply Manifests

```bash
kubectl create namespace zero-ops
kubectl apply -f postgres-cluster.yaml
kubectl apply -f api-deployment.yaml
```

## Monitoring

CNPG automatically exports Prometheus metrics. Configure ServiceMonitor:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: zero-ops-db
  namespace: zero-ops
spec:
  selector:
    matchLabels:
      cnpg.io/cluster: zero-ops-db
  endpoints:
  - port: metrics
```

## Backup & Recovery

### Manual Backup
```bash
kubectl cnpg backup zero-ops-db -n zero-ops
```

### Restore from Backup
```yaml
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: zero-ops-db-restored
spec:
  instances: 3
  bootstrap:
    recovery:
      source: zero-ops-db
      recoveryTarget:
        targetTime: "2026-03-09 10:00:00"
  externalClusters:
  - name: zero-ops-db
    barmanObjectStore:
      destinationPath: s3://zero-ops-backups/postgres
      s3Credentials:
        accessKeyId:
          name: backup-credentials
          key: ACCESS_KEY_ID
        secretAccessKey:
          name: backup-credentials
          key: SECRET_ACCESS_KEY
```

## Connection Pooling

CNPG includes PgBouncer for connection pooling:

```yaml
spec:
  instances: 3
  
  postgresql:
    parameters:
      max_connections: "200"
  
  connectionPooler:
    enabled: true
    instances: 2
    type: pgbouncer
    pgbouncer:
      poolMode: transaction
      parameters:
        max_client_conn: "1000"
        default_pool_size: "25"
```

Update API to use pooler:
```bash
DATABASE_URL="postgres://zeroops:password@zero-ops-db-pooler:5432/zeroops"
```

## High Availability

CNPG provides automatic failover:
- Primary election via Kubernetes leases
- Synchronous replication for zero data loss
- Automatic promotion of standby on primary failure

Monitor cluster status:
```bash
kubectl cnpg status zero-ops-db -n zero-ops
```

## Maintenance

### Upgrade PostgreSQL Version
```yaml
spec:
  imageName: ghcr.io/cloudnative-pg/postgresql:16.1
```

### Scale Replicas
```bash
kubectl cnpg scale zero-ops-db --replicas=5 -n zero-ops
```

## Security

1. **TLS**: Enable SSL for client connections
2. **Network Policies**: Restrict database access to API pods only
3. **RBAC**: Limit CNPG operator permissions
4. **Secrets**: Use external secret managers (Vault, AWS Secrets Manager)

## References

- [CNPG Documentation](https://cloudnative-pg.io/documentation/)
- [CNPG Best Practices](https://cloudnative-pg.io/documentation/current/best_practices/)

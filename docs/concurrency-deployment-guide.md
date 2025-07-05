# Concurrency System Deployment Guide

This guide explains how to deploy and configure the Pipelines-as-Code concurrency system using ConfigMaps and Secrets.

## Overview

The concurrency system can be configured using:

- **ConfigMap**: Contains all configuration settings
- **Secret**: Contains sensitive data like passwords

## Quick Start

### 1. Create the Secret

First, create a secret with your database password:

```bash
# Create the secret with your actual password
kubectl create secret generic pac-concurrency-secret \
  --namespace=pipelines-as-code \
  --from-literal=postgresql-password="your-secure-password"
```

Or use the provided YAML file:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: pac-concurrency-secret
  namespace: pipelines-as-code
type: Opaque
data:
  # Base64 encoded password: echo -n "your-secure-password" | base64
  postgresql-password: eW91ci1zZWN1cmUtcGFzc3dvcmQ=
```

### 2. Update the ConfigMap

The main PAC ConfigMap (`config/302-pac-configmap.yaml`) already includes concurrency settings. Update it with your specific configuration:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: pipelines-as-code
  namespace: pipelines-as-code
data:
  # Enable the concurrency system
  concurrency-enabled: "true"
  
  # Choose your driver
  concurrency-driver: "postgresql"  # or "etcd" or "memory"
  
  # PostgreSQL Configuration
  postgresql-host: "your-postgresql-host"
  postgresql-port: "5432"
  postgresql-database: "pac_concurrency"
  postgresql-username: "pac_user"
  postgresql-ssl-mode: "require"
  postgresql-max-connections: "10"
  postgresql-connection-timeout: "30s"
  postgresql-lease-ttl: "1h"
  
  # The password will be read from the secret
  postgresql-password: ""  # Leave empty to use secret
```

### 3. Deploy the Configuration

```bash
# Apply the secret
kubectl apply -f config/concurrency-secret.yaml

# Apply the updated ConfigMap
kubectl apply -f config/302-pac-configmap.yaml

# Restart the PAC pods to pick up the new configuration
kubectl rollout restart deployment/pipelines-as-code-controller -n pipelines-as-code
kubectl rollout restart deployment/pipelines-as-code-watcher -n pipelines-as-code
```

## Configuration Options

### PostgreSQL Driver

```yaml
# Enable PostgreSQL driver
concurrency-driver: "postgresql"

# Database connection
postgresql-host: "postgresql.example.com"
postgresql-port: "5432"
postgresql-database: "pac_concurrency"
postgresql-username: "pac_user"
postgresql-ssl-mode: "require"  # disable, require, verify-ca, verify-full

# Connection pool settings
postgresql-max-connections: "10"
postgresql-connection-timeout: "30s"

# Lease settings
postgresql-lease-ttl: "1h"
```

### etcd Driver

```yaml
# Enable etcd driver
concurrency-driver: "etcd"

# etcd connection
etcd-endpoints: "etcd.example.com:2379"
etcd-dial-timeout: "5s"
etcd-mode: "etcd"  # etcd, mock, memory

# Authentication (optional)
etcd-username: "etcd_user"
etcd-password: "etcd_password"

# TLS (optional)
etcd-cert-file: "/path/to/cert.pem"
etcd-key-file: "/path/to/key.pem"
etcd-ca-file: "/path/to/ca.pem"
etcd-server-name: "etcd.example.com"
```

### Memory Driver (Testing Only)

```yaml
# Enable memory driver
concurrency-driver: "memory"

# Lease settings
memory-lease-ttl: "30m"
```

## Secret Management

### Option 1: Direct Password in ConfigMap (Not Recommended)

```yaml
# In ConfigMap
postgresql-password: "your-password-here"
```

### Option 2: Using Kubernetes Secret (Recommended)

1. Create the secret:

```bash
kubectl create secret generic pac-concurrency-secret \
  --namespace=pipelines-as-code \
  --from-literal=postgresql-password="your-secure-password"
```

2. Reference it in the ConfigMap:

```yaml
# In ConfigMap
postgresql-password: ""  # Leave empty
# The system will automatically read from the secret
```

### Option 3: External Secret Management

If you're using external secret management tools like:

- HashiCorp Vault
- AWS Secrets Manager
- Azure Key Vault

You can integrate them by:

1. Creating a secret with the external tool
2. Using a sidecar or init container to fetch the secret
3. Mounting it as a file or environment variable

## Database Setup

### PostgreSQL

1. Create the database:

```sql
CREATE DATABASE pac_concurrency;
```

2. Create the user:

```sql
CREATE USER pac_user WITH PASSWORD 'your-secure-password';
GRANT ALL PRIVILEGES ON DATABASE pac_concurrency TO pac_user;
```

3. The tables will be created automatically by the driver.

### etcd

1. Install etcd (if not already installed):

```bash
# Using Helm
helm repo add bitnami https://charts.bitnami.com/bitnami
helm install etcd bitnami/etcd \
  --namespace pipelines-as-code \
  --set auth.enabled=true \
  --set auth.rbac.create=true
```

2. Get the credentials:

```bash
export ETCD_ROOT_PASSWORD=$(kubectl get secret --namespace pipelines-as-code etcd -o jsonpath="{.data.etcd-root-password}" | base64 -d)
export ETCD_USERNAME=$(kubectl get secret --namespace pipelines-as-code etcd -o jsonpath="{.data.etcd-username}" | base64 -d)
```

3. Update the ConfigMap with the credentials.

## Monitoring and Troubleshooting

### Check Configuration

```bash
# Check if the ConfigMap is applied
kubectl get configmap pipelines-as-code -n pipelines-as-code -o yaml

# Check if the secret exists
kubectl get secret pac-concurrency-secret -n pipelines-as-code

# Check PAC logs
kubectl logs -f deployment/pipelines-as-code-watcher -n pipelines-as-code
```

### Common Issues

1. **Connection refused**: Check if the database/etcd is accessible
2. **Authentication failed**: Verify credentials in the secret
3. **Permission denied**: Check database user permissions
4. **Driver not found**: Ensure the driver is correctly specified

### Logs to Monitor

Look for these log messages:

- `"Initialized concurrency system with driver: postgresql"`
- `"acquired concurrency slot for namespace/pipeline-run-1"`
- `"concurrency limit reached for repository namespace/repo"`

## Migration from Existing etcd

If you're migrating from the existing etcd implementation:

1. Update the ConfigMap to use the new concurrency system
2. Set `concurrency-enabled: "true"`
3. Choose your preferred driver
4. Restart the PAC pods
5. The system will automatically migrate existing state

## Security Considerations

1. **Use Secrets**: Never store passwords in ConfigMaps
2. **Network Policies**: Restrict access to your database/etcd
3. **TLS**: Enable TLS for database connections
4. **RBAC**: Use appropriate RBAC for secret access
5. **Audit Logs**: Monitor access to secrets and databases

## Performance Tuning

### PostgreSQL

- Adjust `postgresql-max-connections` based on your workload
- Monitor connection pool usage
- Consider read replicas for high availability

### etcd

- Use multiple etcd nodes for high availability
- Monitor etcd performance metrics
- Consider dedicated etcd cluster for large deployments

### Memory

- Only use for testing/development
- Monitor memory usage
- Set appropriate TTL values

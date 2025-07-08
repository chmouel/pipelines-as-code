# Concurrency System Deployment Guide

This guide explains how to deploy and configure the Pipelines-as-Code concurrency system using ConfigMaps.

## Overview

The concurrency system supports three drivers:

- **etcd**: For distributed, production-grade deployments
- **postgresql**: For distributed deployments using PostgreSQL
- **memory**: For testing and development only

The driver is selected using the `concurrency-driver` field in the ConfigMap. All configuration is managed via ConfigMap.

> **Warning:** The PostgreSQL password must be set directly in the ConfigMap using the `postgresql-password` field. This is not secure for production environments. Consider using a more secure secret management solution if possible.

## Quick Start

### 1. Update the ConfigMap

The main PAC ConfigMap (`config/302-pac-configmap.yaml`) includes concurrency settings. Set the driver and provide configuration for your backend:

#### Example: PostgreSQL Driver

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: pipelines-as-code
  namespace: pipelines-as-code
data:
  concurrency-enabled: "true"
  concurrency-driver: "postgresql"
  postgresql-host: "your-postgresql-host"
  postgresql-port: "5432"
  postgresql-database: "pac_concurrency"
  postgresql-username: "pac_user"
  postgresql-password: "your-secure-password"  # Set password directly here
  postgresql-ssl-mode: "require"  # disable, require, verify-ca, verify-full
  postgresql-max-connections: "10"
  postgresql-connection-timeout: "30s"
  postgresql-lease-ttl: "1h"
```

#### Example: etcd Driver

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: pipelines-as-code
  namespace: pipelines-as-code
data:
  concurrency-enabled: "true"
  concurrency-driver: "etcd"
  etcd-endpoints: "etcd.example.com:2379"
  etcd-dial-timeout: "5s"
  etcd-mode: "etcd"  # etcd, mock, memory
  etcd-username: "etcd_user"         # optional
  etcd-password: "etcd_password"     # optional
  etcd-cert-file: "/path/to/cert.pem" # optional
  etcd-key-file: "/path/to/key.pem"   # optional
  etcd-ca-file: "/path/to/ca.pem"     # optional
  etcd-server-name: "etcd.example.com" # optional
```

#### Example: Memory Driver (Testing Only)

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: pipelines-as-code
  namespace: pipelines-as-code
data:
  concurrency-enabled: "true"
  concurrency-driver: "memory"
  memory-lease-ttl: "30m"
```

### 2. Deploy the Configuration

```bash
# Apply the updated ConfigMap
kubectl apply -f config/302-pac-configmap.yaml

# Restart the PAC pods to pick up the new configuration
kubectl rollout restart deployment/pipelines-as-code-controller -n pipelines-as-code
kubectl rollout restart deployment/pipelines-as-code-watcher -n pipelines-as-code
```

## Updating Configuration with kubectl patch

You can update the concurrency configuration using `kubectl patch` commands without editing the full ConfigMap:

### Enable PostgreSQL Driver

```bash
# Enable concurrency and set PostgreSQL driver
kubectl patch configmap pipelines-as-code -n pipelines-as-code --type='merge' -p='
{
  "data": {
    "concurrency-enabled": "true",
    "concurrency-driver": "postgresql",
    "postgresql-host": "your-postgresql-host",
    "postgresql-port": "5432",
    "postgresql-database": "pac_concurrency",
    "postgresql-username": "pac_user",
    "postgresql-password": "your-secure-password",
    "postgresql-ssl-mode": "disable",
    "postgresql-max-connections": "10",
    "postgresql-connection-timeout": "30s",
    "postgresql-lease-ttl": "1h"
  }
}'
```

### Enable etcd Driver

```bash
# Enable concurrency and set etcd driver
kubectl patch configmap pipelines-as-code -n pipelines-as-code --type='merge' -p='
{
  "data": {
    "concurrency-enabled": "true",
    "concurrency-driver": "etcd",
    "etcd-enabled": "true",
    "etcd-endpoints": "etcd.example.com:2379",
    "etcd-dial-timeout": "5",
    "etcd-mode": "etcd",
    "etcd-username": "etcd_user",
    "etcd-password": "etcd_password"
  }
}'
```

### Enable Memory Driver

```bash
# Enable concurrency and set memory driver
kubectl patch configmap pipelines-as-code -n pipelines-as-code --type='merge' -p='
{
  "data": {
    "concurrency-enabled": "true",
    "concurrency-driver": "memory",
    "memory-lease-ttl": "30m"
  }
}'
```

### Update Individual Settings

You can also update individual settings:

```bash
# Update just the PostgreSQL host
kubectl patch configmap pipelines-as-code -n pipelines-as-code --type='merge' -p='
{
  "data": {
    "postgresql-host": "new-postgresql-host"
  }
}'

# Update just the concurrency driver
kubectl patch configmap pipelines-as-code -n pipelines-as-code --type='merge' -p='
{
  "data": {
    "concurrency-driver": "postgresql"
  }
}'

# Update PostgreSQL password
kubectl patch configmap pipelines-as-code -n pipelines-as-code --type='merge' -p='
{
  "data": {
    "postgresql-password": "new-secure-password"
  }
}'
```

After patching, restart the PAC pods to pick up the changes:

```bash
kubectl rollout restart deployment/pipelines-as-code-controller -n pipelines-as-code
kubectl rollout restart deployment/pipelines-as-code-watcher -n pipelines-as-code
```

## Secret Management

- **Note:** The current implementation does not support reading the PostgreSQL password from a Kubernetes Secret. The password must be set directly in the ConfigMap.
- **Warning:** Storing passwords in ConfigMaps is not secure for production. Consider using a more secure solution if possible.

## Database Setup

### PostgreSQL

- Create the database:

    ```sql
    CREATE DATABASE pac_concurrency;
    ```

- Create the user:

    ```sql
    CREATE USER pac_user WITH PASSWORD 'your-secure-password';
    GRANT ALL PRIVILEGES ON DATABASE pac_concurrency TO pac_user;
    ```

- The tables will be created automatically by the driver.

### etcd setup

- Deploy etcd as per your environment's best practices.
- Provide endpoints and authentication as needed in the ConfigMap.

## Driver Selection Logic

- The system uses the `concurrency-driver` field to select the backend.
- Supported values: `postgresql`, `etcd`, `memory`.
- If not set, falls back to `etcd-enabled` for backward compatibility.

## Troubleshooting

- Check pod logs for driver initialization errors.
- Ensure configmaps are mounted and available.
- For PostgreSQL, ensure network access and credentials are correct.
- For etcd, ensure endpoints are reachable and authentication is correct.

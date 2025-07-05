# Concurrency Control System

This package provides an abstracted concurrency control system for Pipelines-as-Code that supports multiple backend drivers including etcd, PostgreSQL, and in-memory storage.

## Overview

The concurrency control system manages the execution of PipelineRuns within repositories by enforcing concurrency limits. It provides a unified interface that can work with different backend storage systems, making it easy to switch between implementations based on your infrastructure needs.

## Architecture

The system consists of several key components:

- **ConcurrencyDriver Interface**: Defines the core operations for concurrency control
- **QueueManager Interface**: Provides queue management functionality
- **Manager**: Coordinates between drivers and queue managers
- **Driver Implementations**: Concrete implementations for different backends

## Supported Drivers

### 1. etcd Driver

- **Use Case**: Production environments with existing etcd infrastructure
- **Features**:
  - Distributed concurrency control
  - Automatic lease expiration
  - Real-time change notifications
  - High availability

### 2. PostgreSQL Driver

- **Use Case**: Environments with existing PostgreSQL infrastructure
- **Features**:
  - ACID compliance
  - Connection pooling
  - Automatic cleanup of expired leases
  - Polling-based change detection

### 3. Memory Driver

- **Use Case**: Testing and development
- **Features**:
  - In-memory storage
  - Fast performance
  - No external dependencies
  - Automatic cleanup

## Usage

### Basic Usage

```go
import (
    "context"
    "time"
    
    "github.com/openshift-pipelines/pipelines-as-code/pkg/concurrency"
    "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
    "go.uber.org/zap"
)

func main() {
    logger, _ := zap.NewDevelopment()
    sugar := logger.Sugar()

    // Configure the driver
    config := &concurrency.DriverConfig{
        Driver: "etcd", // or "postgresql" or "memory"
        EtcdConfig: &concurrency.EtcdConfig{
            Endpoints:   []string{"localhost:2379"},
            DialTimeout: 5 * time.Second,
            Mode:        "etcd",
        },
    }

    // Create manager
    manager, err := concurrency.NewManager(config, sugar)
    if err != nil {
        sugar.Fatalf("failed to create manager: %v", err)
    }
    defer manager.Close()

    // Use the manager
    repo := &v1alpha1.Repository{
        Spec: v1alpha1.RepositorySpec{
            ConcurrencyLimit: func() *int { limit := 2; return &limit }(),
        },
    }

    ctx := context.Background()
    success, leaseID, err := manager.AcquireSlot(ctx, repo, "namespace/pipeline-run-1")
    if err != nil {
        sugar.Errorf("failed to acquire slot: %v", err)
        return
    }

    if success {
        sugar.Infof("acquired slot with lease ID: %v", leaseID)
        // Do work...
        defer manager.ReleaseSlot(ctx, leaseID, "namespace/pipeline-run-1", "namespace/repo")
    }
}
```

### Using Queue Manager

```go
// Try to acquire slots for multiple pipeline runs
pipelineRuns := []string{
    "namespace/pipeline-run-1",
    "namespace/pipeline-run-2",
    "namespace/pipeline-run-3",
}

acquired, err := manager.queueManager.AddListToRunningQueue(repo, pipelineRuns)
if err != nil {
    sugar.Errorf("failed to add to running queue: %v", err)
}

sugar.Infof("acquired %d slots out of %d pipeline runs", len(acquired), len(pipelineRuns))

// Get running pipeline runs
running := manager.queueManager.RunningPipelineRuns(repo)
sugar.Infof("currently running: %v", running)

// Set up watcher for slot availability
manager.queueManager.SetupWatcher(ctx, repo, func() {
    sugar.Info("slot became available, triggering reconciliation")
})
```

## Configuration

### etcd Configuration

```go
config := &concurrency.DriverConfig{
    Driver: "etcd",
    EtcdConfig: &concurrency.EtcdConfig{
        Endpoints:   []string{"localhost:2379"},
        DialTimeout: 5 * time.Second,
        Username:    "etcd_user",
        Password:    "etcd_password",
        Mode:        "etcd", // "etcd", "mock", or "memory"
        TLSConfig: &concurrency.TLSConfig{
            CertFile:   "/path/to/cert.pem",
            KeyFile:    "/path/to/key.pem",
            CAFile:     "/path/to/ca.pem",
            ServerName: "etcd.example.com",
        },
    },
}
```

### PostgreSQL Configuration

```go
config := &concurrency.DriverConfig{
    Driver: "postgresql",
    PostgreSQLConfig: &concurrency.PostgreSQLConfig{
        Host:             "localhost",
        Port:             5432,
        Database:         "pac_concurrency",
        Username:         "pac_user",
        Password:         "pac_password",
        SSLMode:          "disable", // "disable", "require", "verify-ca", "verify-full"
        MaxConnections:   10,
        ConnectionTimeout: 30 * time.Second,
        LeaseTTL:         1 * time.Hour,
    },
}
```

### Memory Configuration

```go
config := &concurrency.DriverConfig{
    Driver: "memory",
    MemoryConfig: &concurrency.MemoryConfig{
        LeaseTTL: 30 * time.Minute,
    },
}
```

## Database Schema (PostgreSQL)

The PostgreSQL driver automatically creates the following tables:

```sql
-- Concurrency slots table
CREATE TABLE concurrency_slots (
    id SERIAL PRIMARY KEY,
    repository_key VARCHAR(255) NOT NULL,
    pipeline_run_key VARCHAR(255) NOT NULL,
    state VARCHAR(50) NOT NULL DEFAULT 'running',
    acquired_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    UNIQUE(repository_key, pipeline_run_key)
);

-- Repository states table
CREATE TABLE repository_states (
    id SERIAL PRIMARY KEY,
    repository_key VARCHAR(255) NOT NULL UNIQUE,
    state VARCHAR(50) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Pipeline run states table
CREATE TABLE pipeline_run_states (
    id SERIAL PRIMARY KEY,
    pipeline_run_key VARCHAR(255) NOT NULL UNIQUE,
    state VARCHAR(50) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);
```

## Deadlock Prevention

All drivers implement deadlock prevention mechanisms:

1. **Automatic Lease Expiration**: Slots automatically expire after a configurable TTL
2. **Cleanup Goroutines**: Background processes clean up expired leases
3. **Context Support**: Operations respect context timeouts and cancellation
4. **Atomic Operations**: Database transactions ensure consistency

## Migration from Existing etcd Implementation

To migrate from the existing etcd implementation:

1. Replace direct etcd client usage with the new Manager
2. Update configuration to use the new DriverConfig structure
3. The interface remains largely the same, so minimal code changes are required

## Testing

The memory driver is perfect for unit testing as it requires no external dependencies:

```go
func TestConcurrencyControl(t *testing.T) {
    config := &concurrency.DriverConfig{
        Driver: "memory",
        MemoryConfig: &concurrency.MemoryConfig{
            LeaseTTL: 1 * time.Minute,
        },
    }
    
    manager, err := concurrency.NewManager(config, logger)
    // ... test implementation
}
```

## Performance Considerations

- **etcd**: Best for high-frequency operations and real-time notifications
- **PostgreSQL**: Best for environments with existing database infrastructure
- **Memory**: Fastest performance but not suitable for production

## Monitoring

All drivers provide comprehensive logging for monitoring:

- Slot acquisition and release events
- Concurrency limit enforcement
- Cleanup operations
- Error conditions

Use the provided logger to track concurrency control behavior in your application.

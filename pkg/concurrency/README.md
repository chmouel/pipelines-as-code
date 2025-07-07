# Concurrency Control System

This package provides a state-based concurrency control system for Pipelines-as-Code that manages PipelineRun execution order and enforces concurrency limits across repositories. It supports multiple backend drivers (etcd, PostgreSQL, memory) and provides automatic state recovery after restarts.

## Table of Contents

- [Overview](#overview)
- [Architecture](#architecture)
- [Integration Points](#integration-points)
- [Call Flow](#call-flow)
- [State Management](#state-management)
- [Driver Implementations](#driver-implementations)
- [Configuration](#configuration)
- [Usage Examples](#usage-examples)
- [Testing](#testing)
- [Troubleshooting](#troubleshooting)

## Overview

The concurrency system replaces the legacy annotation-based approach with a state-based system that:

- **Manages execution order**: Uses FIFO queues to ensure PipelineRuns execute in creation order
- **Enforces concurrency limits**: Prevents more PipelineRuns from running than the repository's limit
- **Provides persistence**: Survives controller restarts with etcd/PostgreSQL drivers
- **Handles failures gracefully**: Automatic cleanup and recovery mechanisms
- **Supports multiple backends**: Memory (testing), etcd (production), PostgreSQL (enterprise)

## Architecture

### Core Components

```
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────┐
│   Reconciler    │    │   Concurrency    │    │     Driver      │
│                 │    │     Manager      │    │                 │
│ - PipelineRun   │───▶│ - Orchestrates   │───▶│ - etcd          │
│   lifecycle     │    │   slot mgmt      │    │ - PostgreSQL    │
│ - State updates │    │ - Queue mgmt     │    │ - Memory        │
└─────────────────┘    └──────────────────┘    └─────────────────┘
                              │
                              ▼
                       ┌──────────────────┐
                       │   Queue Manager  │
                       │                 │
                       │ - FIFO queues   │
                       │ - State sync    │
                       │ - Watchers      │
                       └──────────────────┘
```

### Key Interfaces

- **`Driver`**: Abstract interface for backend storage (etcd, PostgreSQL, memory)
- **`QueueManager`**: Manages in-memory queues and state synchronization
- **`Manager`**: Coordinates between drivers and queue managers

## Integration Points

### 1. Controller Initialization

The concurrency system is initialized in the controller startup:

```go
// pkg/reconciler/controller.go:55
concurrencyManager, err := concurrency.CreateManagerFromSettings(settingsMap, run.Clients.Log)
if err != nil {
    log.Fatalf("Failed to initialize concurrency system: %v", err)
}

r := &Reconciler{
    // ... other fields
    concurrencyManager: concurrencyManager,
}

// Initialize queues and recover state
if err := concurrencyManager.GetQueueManager().InitQueues(ctx, run.Clients.Tekton, run.Clients.PipelineAsCode); err != nil {
    log.Fatal("failed to init queues", err)
}
```

### 2. PipelineRun Lifecycle Integration

The reconciler integrates concurrency control at key PipelineRun lifecycle points:

#### PipelineRun Creation (Queued State)

```go
// pkg/reconciler/reconciler.go:389-395
func (r *Reconciler) queuePipelineRun(ctx context.Context, logger *zap.SugaredLogger, pr *tektonv1.PipelineRun) error {
    // Add to pending queue
    if err := r.concurrencyManager.GetQueueManager().AddToPendingQueue(repo, []string{prKey}); err != nil {
        return err
    }
    
    // Try to acquire slot
    success, _, err := r.concurrencyManager.AcquireSlot(ctx, repo, prKey)
    if success {
        // Promote to running
        return r.updatePipelineRunToInProgress(ctx, logger, repo, pr)
    }
    // Remain queued
    return nil
}
```

#### PipelineRun Completion (Slot Release)

```go
// pkg/reconciler/reconciler.go:222-258
// Release concurrency slot and process next queued PipelineRun
if r.concurrencyManager != nil {
    prKey := fmt.Sprintf("%s/%s", pr.Namespace, pr.Name)
    repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)

    // Release the slot for the completed PipelineRun
    if err := r.concurrencyManager.ReleaseSlot(ctx, nil, prKey, repoKey); err != nil {
        logger.Errorf("failed to release concurrency slot for %s: %v", prKey, err)
    }

    // Check if there are queued PipelineRuns that can now start
    queuedPRs := r.concurrencyManager.GetQueueManager().QueuedPipelineRuns(repo)
    if len(queuedPRs) > 0 {
        // Try to start the next queued PipelineRun
        for _, nextPRKey := range queuedPRs {
            success, _, err := r.concurrencyManager.AcquireSlot(ctx, repo, nextPRKey)
            if success {
                // Start the PipelineRun
                break
            }
        }
    }
}
```

### 3. Repository Cleanup

The finalizer cleans up concurrency state when repositories are deleted:

```go
// pkg/reconciler/finalizer.go:32-33
if r.concurrencyManager != nil {
    if err := r.concurrencyManager.CleanupRepository(ctx, &v1alpha1.Repository{
        ObjectMeta: metav1.ObjectMeta{
            Namespace: repo.Namespace,
            Name:      repo.Name,
        },
    }); err != nil {
        logger.Errorf("failed to cleanup concurrency state: %v", err)
    }
}
```

## Call Flow

### 1. PipelineRun Creation Flow

```
1. Webhook receives push/PR event
   ↓
2. Reconciler creates PipelineRun with "queued" state
   ↓
3. queuePipelineRun() called
   ↓
4. AddToPendingQueue() - adds to in-memory queue
   ↓
5. AcquireSlot() - tries to get concurrency slot
   ↓
6a. Success: updatePipelineRunToInProgress() - starts execution
   ↓
6b. Failure: PipelineRun remains queued
```

### 2. PipelineRun Completion Flow

```
1. PipelineRun completes (success/failure)
   ↓
2. Reconciler detects completion
   ↓
3. ReleaseSlot() - releases concurrency slot
   ↓
4. QueuedPipelineRuns() - gets queued PipelineRuns
   ↓
5. For each queued PipelineRun:
   ↓
6. AcquireSlot() - tries to acquire slot
   ↓
7. Success: updatePipelineRunToInProgress() - starts execution
```

### 3. State Recovery Flow (After Restart)

```
1. Controller starts
   ↓
2. CreateManagerFromSettings() - creates manager with driver
   ↓
3. InitQueues() - initializes queue manager
   ↓
4. getAllRepositoriesWithState() - discovers repos with state
   ↓
5. For each repository:
   ↓
6. reconstructQueueFromState() - rebuilds in-memory queues
   ↓
7. System resumes normal operation
```

## State Management

### State Persistence

The system maintains state across three layers:

1. **In-Memory Queues**: Fast access for current operations
2. **Driver Storage**: Persistent state (etcd/PostgreSQL)
3. **Kubernetes Resources**: PipelineRun states and annotations

### State Recovery

After a controller restart:

1. **Memory Driver**: All state is lost (expected behavior)
2. **etcd Driver**: Reconstructs queues from etcd keys with timestamps
3. **PostgreSQL Driver**: Reconstructs queues from database with creation times

### State Synchronization

The system ensures consistency through:

- **Atomic Operations**: Driver operations are atomic
- **Queue Reconstruction**: In-memory queues rebuilt from persistent state
- **State Validation**: Cross-checks between memory and persistent state

## Driver Implementations

### Memory Driver

**Use Case**: Testing and development
**Characteristics**:

- No external dependencies
- State lost on restart (expected)
- Fastest performance
- No persistence

```go
config := &concurrency.DriverConfig{
    Driver: "memory",
    MemoryConfig: &concurrency.MemoryConfig{
        LeaseTTL: 30 * time.Minute,
    },
}
```

### etcd Driver

**Use Case**: Production environments with existing etcd
**Characteristics**:

- Distributed concurrency control
- Real-time change notifications
- Automatic lease expiration
- High availability

```go
config := &concurrency.DriverConfig{
    Driver: "etcd",
    EtcdConfig: &concurrency.EtcdConfig{
        Endpoints:   []string{"localhost:2379"},
        DialTimeout: 5 * time.Second,
        Mode:        "etcd",
        TLSConfig: &concurrency.TLSConfig{
            CertFile:   "/path/to/cert.pem",
            KeyFile:    "/path/to/key.pem",
            CAFile:     "/path/to/ca.pem",
        },
    },
}
```

### PostgreSQL Driver

**Use Case**: Enterprise environments with existing PostgreSQL
**Characteristics**:

- ACID compliance
- Connection pooling
- Automatic cleanup
- Polling-based change detection

```go
config := &concurrency.DriverConfig{
    Driver: "postgresql",
    PostgreSQLConfig: &concurrency.PostgreSQLConfig{
        Host:             "localhost",
        Port:             5432,
        Database:         "pac_concurrency",
        Username:         "pac_user",
        Password:         "pac_password",
        SSLMode:          "disable",
        MaxConnections:   10,
        ConnectionTimeout: 30 * time.Second,
        LeaseTTL:         1 * time.Hour,
    },
}
```

## Configuration

### Environment Variables

The system is configured through Pipelines-as-Code settings:

```yaml
# configmap.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: pipelines-as-code
data:
  concurrency-driver: "etcd"  # memory, etcd, postgresql
  concurrency-etcd-endpoints: "localhost:2379"
  concurrency-etcd-username: "etcd_user"
  concurrency-etcd-password: "etcd_password"
  concurrency-postgresql-host: "localhost"
  concurrency-postgresql-database: "pac_concurrency"
  # ... other driver-specific settings
```

### Driver-Specific Settings

Each driver supports specific configuration options:

- **etcd**: Endpoints, TLS, authentication, timeouts
- **PostgreSQL**: Connection string, pooling, SSL, timeouts
- **Memory**: Lease TTL only

## Usage Examples

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
        Driver: "etcd",
        EtcdConfig: &concurrency.EtcdConfig{
            Endpoints:   []string{"localhost:2379"},
            DialTimeout: 5 * time.Second,
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
        defer manager.ReleaseSlot(ctx, leaseID, "namespace/pipeline-run-1", "namespace/repo")
    }
}
```

### Queue Management

```go
// Try to acquire slots for multiple pipeline runs
pipelineRuns := []string{
    "namespace/pipeline-run-1",
    "namespace/pipeline-run-2",
    "namespace/pipeline-run-3",
}

acquired, err := manager.GetQueueManager().AddListToRunningQueue(repo, pipelineRuns)
if err != nil {
    sugar.Errorf("failed to add to running queue: %v", err)
}

sugar.Infof("acquired %d slots out of %d pipeline runs", len(acquired), len(pipelineRuns))

// Get current state
running := manager.GetQueueManager().RunningPipelineRuns(repo)
queued := manager.GetQueueManager().QueuedPipelineRuns(repo)
sugar.Infof("running: %v, queued: %v", running, queued)
```

### State Recovery

```go
// After restart, sync state from driver
if err := manager.SyncStateFromDriver(ctx, repo); err != nil {
    sugar.Errorf("failed to sync state: %v", err)
}

// Get all repositories with state
repos, err := manager.GetQueueManager().GetAllRepositoriesWithState(ctx)
if err != nil {
    sugar.Errorf("failed to get repositories: %v", err)
}

for _, repo := range repos {
    sugar.Infof("recovered state for repository: %s/%s", repo.Namespace, repo.Name)
}
```

## Testing

### Unit Testing with Memory Driver

```go
func TestConcurrencyControl(t *testing.T) {
    config := &concurrency.DriverConfig{
        Driver: "memory",
        MemoryConfig: &concurrency.MemoryConfig{
            LeaseTTL: 1 * time.Minute,
        },
    }
    
    manager, err := concurrency.NewManager(config, logger)
    require.NoError(t, err)
    defer manager.Close()

    repo := &v1alpha1.Repository{
        Spec: v1alpha1.RepositorySpec{
            ConcurrencyLimit: func() *int { limit := 1; return &limit }(),
        },
    }

    // Test slot acquisition
    success, leaseID, err := manager.AcquireSlot(ctx, repo, "test/pr-1")
    assert.NoError(t, err)
    assert.True(t, success)
    assert.NotEmpty(t, leaseID)

    // Test concurrency limit
    success2, _, err := manager.AcquireSlot(ctx, repo, "test/pr-2")
    assert.NoError(t, err)
    assert.False(t, success2) // Should fail due to limit
}
```

### Integration Testing

```go
func TestEtcdDriverIntegration(t *testing.T) {
    // Requires running etcd instance
    config := &concurrency.DriverConfig{
        Driver: "etcd",
        EtcdConfig: &concurrency.EtcdConfig{
            Endpoints:   []string{"localhost:2379"},
            DialTimeout: 5 * time.Second,
        },
    }
    
    manager, err := concurrency.NewManager(config, logger)
    require.NoError(t, err)
    defer manager.Close()

    // Test state persistence across restarts
    // ... test implementation
}
```

## Troubleshooting

### Common Issues

#### 1. State Loss After Restart

**Symptoms**: Queued PipelineRuns disappear after controller restart
**Causes**:

- Using memory driver in production
- Driver configuration issues
- Network connectivity problems

**Solutions**:

- Use etcd or PostgreSQL driver for production
- Verify driver configuration
- Check network connectivity to backend

#### 2. PipelineRuns Stuck in Queued State

**Symptoms**: PipelineRuns remain queued even when slots are available
**Causes**:

- Driver connection issues
- Lease expiration problems
- Queue state corruption

**Solutions**:

- Check driver logs for errors
- Verify lease TTL settings
- Restart controller to trigger state recovery

#### 3. Concurrency Limits Not Enforced

**Symptoms**: More PipelineRuns running than the limit
**Causes**:

- Driver not properly configured
- State synchronization issues
- Race conditions

**Solutions**:

- Verify driver configuration
- Check for state synchronization errors
- Review logs for race condition indicators

### Debugging

#### Enable Debug Logging

```go
logger, _ := zap.NewDevelopment()
sugar := logger.Sugar()
sugar.SetLevel(zap.DebugLevel)
```

#### Check Driver State

```go
// Get current slots in use
slots, err := manager.GetCurrentSlots(ctx, repo)
if err != nil {
    sugar.Errorf("failed to get current slots: %v", err)
}
sugar.Infof("current slots in use: %d", slots)

// Get running PipelineRuns
running, err := manager.GetRunningPipelineRuns(ctx, repo)
if err != nil {
    sugar.Errorf("failed to get running PipelineRuns: %v", err)
}
sugar.Infof("running PipelineRuns: %v", running)
```

#### Monitor Queue State

```go
// Set up watcher for slot availability
manager.GetQueueManager().SetupWatcher(ctx, repo, func() {
    sugar.Info("slot became available, triggering reconciliation")
})
```

### Performance Considerations

- **Memory Driver**: Fastest, no network overhead
- **etcd Driver**: Good for high-frequency operations, real-time notifications
- **PostgreSQL Driver**: Best for environments with existing database infrastructure

### Monitoring

Monitor these metrics:

- Slot acquisition/release rates
- Queue lengths
- Driver operation latencies
- State recovery times
- Error rates by driver type

## Migration from Legacy System

The new concurrency system replaces the legacy annotation-based approach:

### Key Differences

1. **State Management**: State-based vs annotation-based
2. **Persistence**: Optional persistence vs no persistence
3. **Recovery**: Automatic state recovery vs manual intervention
4. **Scalability**: Better performance and reliability

### Migration Steps

1. Update configuration to use new driver settings
2. Deploy new controller version
3. System automatically migrates existing PipelineRuns
4. Monitor for any issues during transition

The new system is backward compatible and will handle existing PipelineRuns gracefully.

# etcd-based Concurrency Implementation

This package implements an etcd-based concurrency control system to replace the existing semaphore-based queue management in Pipelines-as-Code.

## Overview

The etcd-based implementation uses distributed leases instead of in-memory queues and annotation-based state tracking. This provides several advantages:

- **Atomic Operations**: etcd transactions ensure consistent state changes
- **Automatic Cleanup**: Leases expire automatically if controllers crash
- **Strong Consistency**: etcd provides consistency guarantees across distributed controllers
- **No Queue State**: Eliminates complex queue management and serialization
- **Event-driven**: Uses etcd watches for reactive concurrency management

## Key Components

### 1. ConcurrencyManager (`concurrency.go`)

- Manages concurrency slots using etcd leases
- Handles PipelineRun state storage in etcd
- Provides atomic slot acquisition/release operations

### 2. QueueManager (`queue_manager.go`)

- Implements the `sync.QueueManagerInterface` using etcd
- Replaces semaphore-based queue management
- Provides compatibility with existing reconciler code

### 3. StateManager (`state_manager.go`)

- Manages PipelineRun states in etcd instead of annotations
- Provides state transition methods
- Handles state cleanup

### 4. ReconcilerIntegration (`reconciler_integration.go`)

- Provides integration layer for the reconciler
- Handles fallback to annotation-based system when etcd is disabled
- Manages lease lifecycle

### 5. PipelineAsCodeIntegration (`pipelineascode_integration.go`)

- Integration for the main PipelineAsCode controller
- Handles PipelineRun creation with proper concurrency control
- Manages state transitions during PipelineRun lifecycle

## Configuration

The etcd integration is controlled by environment variables:

```bash
# Enable etcd-based concurrency (default: false)
PAC_ETCD_ENABLED=true

# etcd mode: "etcd" for production, "mock" for testing (default: auto-detect)
PAC_ETCD_MODE=etcd

# etcd connection settings
ETCD_ENDPOINTS=localhost:2379,localhost:2380
ETCD_DIAL_TIMEOUT=5
ETCD_USERNAME=user
ETCD_PASSWORD=pass

# TLS settings (optional)
ETCD_CERT_FILE=/path/to/cert.pem
ETCD_KEY_FILE=/path/to/key.pem
ETCD_CA_FILE=/path/to/ca.pem
ETCD_SERVER_NAME=etcd.example.com
```

## How It Works

### 1. PipelineRun Creation

When a new PipelineRun is created:

1. Check if repository has concurrency limit
2. If no limit: set state to "started" and proceed
3. If limit exists: try to acquire etcd lease
4. If lease acquired: set state to "started" and proceed
5. If lease not acquired: set state to "queued" and PipelineRun spec status to "pending"

### 2. PipelineRun Reconciliation

During reconciliation:

1. Get PipelineRun state from etcd (or fallback to annotations)
2. If state is "queued": try to acquire lease
3. If lease acquired: transition to "started" and remove pending status
4. If lease not acquired: remain queued

### 3. PipelineRun Completion

When PipelineRun completes:

1. Set final state in etcd
2. Release etcd lease (automatic slot availability)
3. etcd watch triggers reconciliation of waiting PipelineRuns

### 4. Automatic Cleanup

- Leases expire automatically if controller crashes
- No orphaned queue state
- etcd handles distributed coordination

## Key Differences from Semaphore System

| Aspect | Semaphore System | etcd System |
|--------|------------------|-------------|
| State Storage | Annotations | etcd |
| Queue Management | In-memory queues | Lease-based slots |
| Consistency | Eventually consistent | Strongly consistent |
| Cleanup | Manual cleanup needed | Automatic lease expiration |
| Scalability | Single controller | Distributed controllers |
| Recovery | Complex state reconstruction | Automatic from etcd |

## Migration Strategy

The implementation supports gradual migration:

1. **Phase 1**: Deploy with `PAC_ETCD_ENABLED=false` (default)
   - Uses existing annotation-based system
   - etcd integration is dormant

2. **Phase 2**: Deploy etcd and enable with `PAC_ETCD_ENABLED=true`
   - New PipelineRuns use etcd-based system
   - Existing PipelineRuns continue with annotation system
   - Gradual transition as old PipelineRuns complete

3. **Phase 3**: Remove old semaphore code (future cleanup)

## Testing

The package includes comprehensive tests using mock etcd clients:

```bash
# Run etcd concurrency tests
go test ./pkg/etcd/...

# Run with verbose output
go test -v ./pkg/etcd/...
```

## Performance Considerations

- **Lease Overhead**: Each running PipelineRun holds an etcd lease
- **etcd Load**: Proportional to number of concurrent PipelineRuns
- **Network**: Requires reliable connection to etcd cluster
- **Memory**: Reduced memory usage (no in-memory queues)

## Monitoring

Key metrics to monitor:

- etcd lease count per repository
- Lease acquisition/release rates
- etcd connection health
- Queue wait times (derived from state transition timestamps)

## Troubleshooting

Common issues:

1. **etcd connectivity**: Check `ETCD_ENDPOINTS` and network connectivity
2. **Lease expiration**: Increase lease TTL if controllers restart frequently
3. **State inconsistency**: Check etcd key prefixes and permissions
4. **Performance**: Monitor etcd cluster health and resource usage

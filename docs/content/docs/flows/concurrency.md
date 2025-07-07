---
title: Concurrency Flow
weight: 2
---
# Concurrency Flow

Pipelines-as-Code supports two concurrency management approaches:

1. **Annotation-based (Legacy)**: Uses in-memory queues and annotations for state tracking
2. **etcd-based (Recommended)**: Uses distributed leases and etcd for state management

## etcd-based Concurrency Flow (Recommended)

{{< mermaid >}}

graph TD
    A1[Controller] --> B1(Validate & Process Event)
    B1 --> C1{Is concurrency defined?}
    C1 -->|Not Defined| D1[Create PipelineRun with state='started' in etcd]
    C1 -->|Defined| E1{Try acquire etcd lease}
    E1 -->|Success| F1[Create PipelineRun with state='started' in etcd]
    E1 -->|Failed| G1[Create PipelineRun with pending status and state='queued' in etcd]

    Z[Pipelines-as-Code]

    A[Watcher] --> B(PipelineRun Reconciler)
    B --> C{Check state in etcd}
    C --> |completed| F(Return, nothing to do!)
    C --> |queued| D{Try acquire etcd lease}
    C --> |started| E{Is PipelineRun Done?}
    D --> |Success| H(Update state to 'started' in etcd)
    D --> |Failed| I[Return and wait for lease availability]
    H --> E
    E --> |Yes| G(Report Status to provider)
    E --> |No| J(Requeue Request)
    J --> B
    G --> K(Update status in Repository)
    K --> L(Update state to 'completed' in etcd)
    L --> M(Release etcd lease)
    M --> N[etcd watch triggers next PipelineRun]
    N --> O[Done!]

{{< /mermaid >}}

### Key Features of etcd-based Approach

- **Atomic Operations**: Lease acquisition is atomic, preventing race conditions
- **Automatic Cleanup**: Leases expire if controller crashes, preventing deadlocks
- **Event-driven**: etcd watches trigger reconciliation when slots become available
- **Distributed**: Works across multiple controller instances
- **No Queue State**: Eliminates complex queue serialization and persistence

## Legacy Annotation-based Concurrency Flow

{{< mermaid >}}

graph TD
    A1[Controller] --> B1(Validate & Process Event)
    B1 --> C1{Is concurrency defined?}
    C1 -->|Not Defined| D1[Create PipelineRun with state='started']
    C1 -->|Defined| E1[Create PipelineRun with pending status and state='queued']

    Z[Pipelines-as-Code]

    A[Watcher] --> B(PipelineRun Reconciler)
    B --> C{Check state}
    C --> |completed| F(Return, nothing to do!)
    C --> |queued| D(Create Queue for Repository)
    C --> |started| E{Is PipelineRun Done?}
    D --> O(Add PipelineRun in the queue)
    O --> P{If PipelineRuns running < concurrency_limit}
    P --> |Yes| Q(Start the top most PipelineRun in the Queue)
    Q --> P
    P --> |No| R[Return and wait for your turn]
    E --> |Yes| G(Report Status to provider)
    E --> |No| H(Requeue Request)
    H --> B
    G --> I(Update status in Repository)
    I --> J(Update state to 'completed')
    J --> K{Check if concurrency was defined?}
    K --> |Yes| L(Remove PipelineRun from Queue)
    L --> M(Start the next PipelineRun from Queue)
    M --> N[Done!]
    K --> |No| N

{{< /mermaid >}}

### Repository Concurrency Limit

Both approaches use the same repository configuration:

```yaml
apiVersion: pipelinesascode.tekton.dev/v1alpha1
kind: Repository
metadata:
  name: my-repo
spec:
  url: "https://github.com/owner/repo"
  concurrency: 2  # Allow max 2 concurrent PipelineRuns
```

## Migration Guide

### Phase 1: Deploy with etcd Support (Backward Compatible)

```bash
# Deploy with etcd disabled (default)
export PAC_ETCD_ENABLED=false
```

- Existing behavior preserved
- etcd components available but inactive

### Phase 2: Enable etcd for New PipelineRuns

```bash
# Enable etcd for new PipelineRuns
export PAC_ETCD_ENABLED=true
export ETCD_ENDPOINTS=your-etcd-cluster:2379
```

- New PipelineRuns use etcd-based concurrency
- Existing PipelineRuns continue with annotation system
- Gradual transition as workloads complete

### Phase 3: Full Migration Complete

- All PipelineRuns using etcd-based system
- Remove legacy semaphore code (future release)

## Troubleshooting

### etcd Connectivity Issues

```bash
# Test etcd connection
etcdctl --endpoints=$ETCD_ENDPOINTS endpoint health

# Check controller logs
kubectl logs -n pipelines-as-code-system deployment/pipelines-as-code-controller
```

### State Inconsistencies

```bash
# Check etcd keys for repository
etcdctl --endpoints=$ETCD_ENDPOINTS get /pac/concurrency/namespace/repo-name/ --prefix

# Check lease status
etcdctl --endpoints=$ETCD_ENDPOINTS lease list
```

### Performance Monitoring

Monitor these metrics:

- etcd lease count per repository
- Lease acquisition/release rates
- etcd response times
- Queue wait times (from state transitions)

## Benefits of etcd-based Approach

| Feature | Annotation-based | etcd-based |
|---------|------------------|------------|
| Consistency | Eventually consistent | Strongly consistent |
| Cleanup | Manual | Automatic (lease expiration) |
| Scalability | Single controller | Multiple controllers |
| State Storage | Kubernetes annotations | etcd distributed store |
| Queue Management | In-memory queues | Lease-based slots |
| Recovery | Complex reconstruction | Automatic from etcd |

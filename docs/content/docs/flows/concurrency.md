---
title: Concurrency Flow
weight: 2
---
# Concurrency Flow

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

    %% Queue Health Checker
    QHC[Queue Health Checker Every 10 seconds] --> QHC1{Check all repositories with concurrency limits}
    QHC1 --> QHC2{Has available capacity and pending PipelineRuns?}
    QHC2 -->|No| QHC
    QHC2 -->|Yes| QHC7{High activity detected?}
    QHC7 -->|Yes| QHC
    QHC7 -->|No| QHC3{Are PipelineRuns stuck - pending > 2 minutes?}
    QHC3 -->|Yes| QHC4[Rebuild Queue from cluster reality]
    QHC3 -->|No| QHC5[Trigger reconciliation for pending PipelineRuns]
    QHC4 --> QHC5
    QHC5 --> QHC6[Update state annotation to trigger controller]
    QHC6 --> B
    QHC5 -.->|Rate limited 3 min intervals| QHC4

{{< /mermaid >}}

## Queue Health Checker

The **Queue Health Checker** is a safety mechanism that runs every 10 seconds to detect and resolve stuck queues:

### Key Features

- **Automatic Detection**: Monitors repositories with concurrency limits for stuck pending PipelineRuns
- **Smart Triggering**: Only triggers when there's available capacity and pending work
- **Queue Rebuilding**: Rebuilds queue state from cluster reality when PipelineRuns are stuck (>2 minutes)
- **Rate Limiting**: Prevents excessive rebuilds with 3-minute intervals per repository
- **Activity Detection**: Skips triggering when high reconciler activity is detected
- **Conflict Handling**: Retries annotation updates with exponential backoff for Kubernetes conflicts

### How It Works

1. **Monitoring**: Scans all repositories with concurrency limits every 10 seconds
2. **Capacity Check**: Calculates available capacity based on actual running PipelineRuns
3. **Stuck Detection**: Identifies PipelineRuns pending for more than 2 minutes
4. **Queue Rebuild**: Rebuilds queue state from cluster reality for stuck PipelineRuns
5. **Reconciliation Trigger**: Updates state annotations to trigger the controller
6. **Safety Mechanisms**: Rate limiting and activity detection prevent interference with normal operations

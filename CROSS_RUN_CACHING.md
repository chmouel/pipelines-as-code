# Cross-Run Resource Caching Implementation

## Overview

This implementation extends the existing resource resolution logic in `pkg/resolve/remote.go` to support cross-run caching. Previously, the `alreadyFetchedResource` function only checked in-memory maps that were scoped to the current execution run. Now, resources that have been fetched in previous runs can be recognized and reused from the persistent cache.

## Changes Made

### 1. New Public Methods in `pkg/matcher/annotation_tasks_install.go`

Added two new public methods to the `RemoteTasks` struct:

- `GetCachedTask(ctx context.Context, taskName string) (*tektonv1.Task, bool)`
- `GetCachedPipeline(ctx context.Context, pipelineName string) (*tektonv1.Pipeline, bool)`

These methods:

- Check if content exists in the persistent cache using the same key format as `getRemote`
- Parse cached YAML content into Tekton Task/Pipeline objects
- Return the parsed object and a boolean indicating if found
- Log cache hits/misses for debugging

### 2. New Cache-Aware Helper Functions in `pkg/resolve/remote.go`

Added two new helper functions:

- `alreadyFetchedTaskWithCache(ctx context.Context, rt *matcher.RemoteTasks, tasks map[string]*tektonv1.Task, taskName string) (*tektonv1.Task, bool)`
- `alreadyFetchedPipelineWithCache(ctx context.Context, rt *matcher.RemoteTasks, pipelines map[string]*tektonv1.Pipeline, pipelineName string) (*tektonv1.Pipeline, bool)`

These functions implement a two-tier lookup strategy:

1. **First**: Check in-memory map (fastest, current run optimization)
2. **Second**: Check persistent cache (cross-run optimization)
3. **Third**: If found in cache, add to in-memory map for subsequent lookups

### 3. Updated Resource Resolution Logic

Modified the specific places in `resolveRemoteResources` where remote tasks and pipelines are fetched:

- **Pipeline fetching**: Now uses `alreadyFetchedPipelineWithCache` instead of `alreadyFetchedResource`
- **Task fetching**: Now uses `alreadyFetchedTaskWithCache` instead of `alreadyFetchedResource`

The generic `alreadyFetchedResource` function is preserved for compatibility with other use cases.

## Issue Resolution: Cache Initialization

### Problem

Initially, the `rt.fileCache` was always `nil` because `UpdateCacheConfig` was never called in production code paths. This meant that cross-run caching wasn't working as intended.

### Solution

1. **Added Constructor**: Created `NewRemoteTasks()` function that ensures cache is always initialized
2. **Updated Production Code**: Modified `pkg/resolve/resolve.go` to use the new constructor
3. **Updated Tests**: All test files now use the constructor for consistency
4. **Automatic Cache Setup**: Cache is initialized with default TTL (24 hours) automatically

```go
// NewRemoteTasks creates a new RemoteTasks instance with cache initialized
func NewRemoteTasks(run *params.Run, event *info.Event, providerInterface provider.Interface, logger *zap.SugaredLogger) *RemoteTasks {
 rt := &RemoteTasks{
  Run:               run,
  Event:             event,
  ProviderInterface: providerInterface,
  Logger:            logger,
  fileCache:         cache.New(defaultCacheTTL),
 }
 return rt
}
```

### Cache Lifecycle

- **Initialization**: Automatically set up when creating `RemoteTasks` instances
- **Population**: When `getRemote` fetches content, it stores raw YAML in cache
- **Retrieval**: New helper methods parse cached YAML into objects
- **Expiration**: Follows existing cache TTL configuration
- **Invalidation**: Automatic based on cache TTL or manual cache clear

## How Cross-Run Caching Works

### Cache Key Format

The cache uses the same key format as the existing `getRemote` function:

- Tasks: `"taskName-task-true"`
- Pipelines: `"pipelineName-pipeline-true"`

### Flow Diagram

```
New PipelineRun Processing
         ↓
Check for Task "git-clone"
         ↓
alreadyFetchedTaskWithCache()
         ↓
    ┌─ In-memory map? ────→ YES ────→ Return cached object
    │         ↓
    │        NO
    │         ↓
    └─ Persistent cache? ──→ YES ────→ Parse YAML → Add to in-memory → Return object
              ↓
             NO
              ↓
         Fetch remotely → Cache result → Return object
```

### Performance Benefits

1. **Elimination of redundant network calls**: If a task/pipeline was fetched in a previous run, it won't be fetched again
2. **Faster parsing**: Pre-parsed objects in in-memory maps are returned immediately
3. **Intelligent fallback**: If cache is empty or expired, normal fetch logic is preserved

## Testing

### Unit Tests

- `TestCrossRunCaching`: Tests the new cache-aware helper functions
- `TestAlreadyFetchedResourceGeneric`: Ensures backward compatibility
- All existing tests continue to pass

### Integration

- Works with existing GitHub provider cache
- Compatible with cache TTL configuration
- Maintains precedence rules (PipelineRun → Pipeline → Tekton directory)

## Backward Compatibility

- All existing functionality is preserved
- Generic `alreadyFetchedResource` function remains unchanged
- No breaking changes to public APIs
- Cache behavior is additive (doesn't change existing caching logic)

## Configuration

Cross-run caching leverages the existing cache configuration:

- `PAC_GITHUB_CACHE_ENABLED`: Enable/disable caching
- `PAC_GITHUB_CACHE_TTL`: Cache time-to-live duration
- HTTP headers: Server-provided cache expiry times

## Example Scenarios

### Scenario 1: First Run

1. PipelineRun references task "git-clone"
2. No cache hit → Fetch from GitHub → Store in cache
3. Task is used in PipelineRun execution

### Scenario 2: Subsequent Run (Same Task)

1. Different PipelineRun references same task "git-clone"
2. Cache hit → Parse cached YAML → Use immediately
3. No network call to GitHub required

### Scenario 3: Multiple Tasks

1. PipelineRun references tasks "git-clone", "build", "test"
2. "git-clone" is cache hit (from previous run)
3. "build" and "test" are cache misses → Fetch and cache
4. Future runs benefit from all three cached tasks

This implementation significantly improves performance for environments with repeated use of the same remote tasks and pipelines across different PipelineRun executions.

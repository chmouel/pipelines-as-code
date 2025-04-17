---
title: PipelineRuns Cleanup
weight: 8
---
# PipelineRuns Cleanups

As PipelineRuns accumulate in a user's namespace, Pipelines-as-Code provides an automatic cleanup mechanism to maintain only a specified number of runs.

## Configuring Cleanup via Annotations

You can control the cleanup behavior by adding this annotation to your PipelineRun:

```yaml
pipelinesascode.tekton.dev/max-keep-runs: "maxNumber"
```

When Pipelines-as-Code detects this annotation, it will automatically clean up older runs after a successful PipelineRun execution, keeping only the most recent `maxNumber` of runs.

For example, setting `max-keep-runs: "5"` will ensure that only the 5 most recent successful PipelineRuns are retained, while older ones are automatically deleted.

## Cleanup Behavior

The cleanup process has the following characteristics:

- It triggers after a PipelineRun completes successfully
- It skips PipelineRuns in `Running` or `Pending` status
- It will remove PipelineRuns in `Unknown` status
- It sorts the PipelineRuns by creation timestamp to determine which to delete
- It only cleans up PipelineRuns with the same name pattern
- Cleanup operations are logged in the namespace events

## Global Configuration

{{< hint info >}}
In addition to per-PipelineRun configuration, you can also configure the cleanup behavior globally for a cluster via the [Pipelines-as-Code ConfigMap]({{< relref "/docs/install/settings.md" >}}). This global setting will apply to all PipelineRuns that don't have the annotation specified.

The global configuration option is:

```yaml
pipelinerun-max-keep-runs: "maxNumber"
```

{{< /hint >}}

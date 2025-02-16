---
title: PipelineRuns Cleanup
weight: 8
---
# Cleaning Up Old PipelineRuns

Over time, you might end up with tons of PipelineRuns in your namespace. Pipelines-as-Code can help you keep things manageable by automatically deleting older runs, so you only keep a certain number around.

To tell Pipelines-as-Code how many PipelineRuns to keep, just add this annotation to your PipelineRun:

```yaml
pipelinesascode.tekton.dev/max-keep-runs: "maxNumber"
```

Once you do that, Pipelines-as-Code will kick in after each successful PipelineRun and clean up the older ones, making sure you only have the last `maxNumber` of runs.

It's smart enough to leave any PipelineRuns that are currently `Running` or `Pending` alone. However, it *will* cleanup PipelineRuns with an `Unknown` status.

{{< hint info >}}
 Heads up! You can also set this up for your whole cluster in the [Pipelines-as-Code ConfigMap]({{< relref "/docs/install/settings.md" >}}) if you want it to apply to everyone.
{{< /hint >}}

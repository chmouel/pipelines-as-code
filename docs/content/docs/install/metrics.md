Okay, here's a more human-friendly version of that documentation:

---
title: Metrics
weight: 16
---

# Checking Up on Pipelines-as-Code: Metrics

Want to see how Pipelines-as-Code is doing?  You can!  The `pipelines-as-code-watcher` service is where it's at, and you can find its metrics hanging out on port `9090`.

Pipelines-as-Code can even chat with different monitoring systems.  Think Prometheus, Google Stackdriver, and others.  Want to set these up?  No problem, just take a peek at the [observability configuration](../config/config-observability.yaml) file – it's all explained there.

Here’s a quick rundown of the metrics you can track:

| Metric Name                                          | Type      | What it Tells You                                                   |
|------------------------------------------------------|-----------|---------------------------------------------------------------------|
| `pipelines_as_code_pipelinerun_count`                | Counter   |  Basically, how many PipelineRuns Pipelines-as-Code has kicked off. |
| `pipelines_as_code_pipelinerun_duration_seconds_sum` | Counter   |  The total time all PipelineRuns have been running, in seconds.      |
| `pipelines_as_code_running_pipelineruns_count`       | Gauge     |  How many PipelineRuns are running *right now*.                     |

---
title: PipelineRun Annotations Reference
weight: 10
---

# Annotations Reference Guide

Pipelines-as-Code uses annotations to control when and how PipelineRuns are executed and managed. This reference guide covers all available annotations and their usage.

## Event Matching Annotations

These annotations determine which events will trigger your PipelineRun.

### pipelinesascode.tekton.dev/on-event

**Purpose**: Specifies which Git provider events will trigger this PipelineRun.

**Format**: JSON array of strings.

**Valid values**:

- `pull_request` - Pull/Merge request events
- `push` - Push events
- `tag` - Tag creation events
- `pull_request_closed` - Pull/Merge request closed events

**Example**:

```yaml
pipelinesascode.tekton.dev/on-event: "[pull_request, push]"
```

### pipelinesascode.tekton.dev/on-target-branch

**Purpose**: Filters events based on the target branch.

**Format**: JSON array of strings or regular expressions.

**Example**:

```yaml
pipelinesascode.tekton.dev/on-target-branch: "[main, release-.*, hotfix]"
```

### pipelinesascode.tekton.dev/on-cel-expression

**Purpose**: Advanced filtering using CEL expressions.

**Format**: String containing a valid CEL expression.

**Example**:

```yaml
pipelinesascode.tekton.dev/on-cel-expression: "body.action == 'opened' || body.action == 'synchronize'"
```

### pipelinesascode.tekton.dev/on-comment-command

**Purpose**: Trigger pipeline on specific comment commands.

**Format**: JSON array of strings.

**Example**:

```yaml
pipelinesascode.tekton.dev/on-comment-command: "[/test, /retest]"
```

### pipelinesascode.tekton.dev/on-source-branch

**Purpose**: Filter based on source branch (only for PRs).

**Format**: JSON array of strings or regular expressions.

**Example**:

```yaml
pipelinesascode.tekton.dev/on-source-branch: "[feature-.*, bugfix]"
```

### pipelinesascode.tekton.dev/paths

**Purpose**: Filter based on the files that were changed.

**Format**: JSON array of strings or regular expressions.

**Example**: Run only when files in these directories are changed

```yaml
pipelinesascode.tekton.dev/paths: "[frontend/.*, backend/.*]"
```

### pipelinesascode.tekton.dev/nopaths

**Purpose**: Exclude PipelineRun when files matching these patterns are changed.

**Format**: JSON array of strings or regular expressions.

**Example**: Don't run when only docs or tests are changed

```yaml
pipelinesascode.tekton.dev/nopaths: "[docs/.*, tests/.*]"
```

## Task Resolution Annotations

These annotations control how tasks are resolved and used in the PipelineRun.

### pipelinesascode.tekton.dev/task

**Purpose**: Reference Tekton Hub tasks.

**Format**: JSON array of strings.

**Example**:

```yaml
pipelinesascode.tekton.dev/task: "[git-clone, pylint]"
```

### pipelinesascode.tekton.dev/task-1

**Purpose**: Reference a specific version of a Tekton Hub task.

**Format**: JSON array of strings.

**Example**:

```yaml
pipelinesascode.tekton.dev/task-1: "[git-clone:0.7]"
```

### pipelinesascode.tekton.dev/task-namespace

**Purpose**: Specify the namespace to look for tasks in the cluster.

**Format**: String.

**Example**:

```yaml
pipelinesascode.tekton.dev/task-namespace: "tekton-tasks"
```

### pipelinesascode.tekton.dev/task-bundle

**Purpose**: Reference a task from a bundle.

**Format**: JSON array of strings.

**Example**:

```yaml
pipelinesascode.tekton.dev/task-bundle: "[registry.example.com/tasks/git-clone:v1]"
```

### pipelinesascode.tekton.dev/pipeline-bundle

**Purpose**: Reference a pipeline from a bundle.

**Format**: String.

**Example**:

```yaml
pipelinesascode.tekton.dev/pipeline-bundle: "registry.example.com/pipelines/build:v1"
```

## Execution Control Annotations

These annotations control various aspects of PipelineRun execution.

### pipelinesascode.tekton.dev/timeout

**Purpose**: Set a maximum duration for the PipelineRun.

**Format**: String with duration format (e.g., 1h30m).

**Example**:

```yaml
pipelinesascode.tekton.dev/timeout: "30m"
```

### pipelinesascode.tekton.dev/max-keep-runs

**Purpose**: Set the maximum number of completed PipelineRuns to keep.

**Format**: Integer.

**Example**:

```yaml
pipelinesascode.tekton.dev/max-keep-runs: "5"
```

### pipelinesascode.tekton.dev/concurrency-limit

**Purpose**: Set the maximum number of concurrent PipelineRuns for the repository.

**Format**: Integer.

**Example**:

```yaml
pipelinesascode.tekton.dev/concurrency-limit: "3"
```

### pipelinesascode.tekton.dev/retry

**Purpose**: Set the number of times to retry a failed PipelineRun.

**Format**: Integer.

**Example**:

```yaml
pipelinesascode.tekton.dev/retry: "2"
```

### pipelinesascode.tekton.dev/finally-timeout

**Purpose**: Set a timeout for the finally tasks of a PipelineRun.

**Format**: String with duration format.

**Example**:

```yaml
pipelinesascode.tekton.dev/finally-timeout: "5m"
```

## Provider Integration Annotations

These annotations control integration with Git providers.

### pipelinesascode.tekton.dev/log-url

**Purpose**: Provide a custom URL for viewing logs.

**Format**: String URL.

**Example**:

```yaml
pipelinesascode.tekton.dev/log-url: "https://logs.example.com/{{ repo_owner }}/{{ repo_name }}/{{ pipelinerun.name }}"
```

### pipelinesascode.tekton.dev/log-description

**Purpose**: Provide a custom description for the log URL.

**Format**: String.

**Example**:

```yaml
pipelinesascode.tekton.dev/log-description: "View detailed logs"
```

### pipelinesascode.tekton.dev/on-check-name

**Purpose**: Set a custom name for the GitHub check.

**Format**: String.

**Example**:

```yaml
pipelinesascode.tekton.dev/on-check-name: "Build and Test"
```

### pipelinesascode.tekton.dev/run-name

**Purpose**: Set a custom name for the PipelineRun.

**Format**: String.

**Example**:

```yaml
pipelinesascode.tekton.dev/run-name: "pr-{{ source_branch }}-{{ repo_name }}"
```

## Best Practices

1. **Combine annotations for precise control**: Use multiple annotations to create specific triggers.

   ```yaml
   pipelinesascode.tekton.dev/on-event: "[pull_request]"
   pipelinesascode.tekton.dev/on-target-branch: "[main, release-.*]"
   pipelinesascode.tekton.dev/paths: "[src/.*, Dockerfile]"
   ```

2. **Use CEL expressions for complex conditions**: When simple annotations aren't enough, use CEL expressions.

   ```yaml
   pipelinesascode.tekton.dev/on-cel-expression: "body.action == 'opened' || (body.action == 'synchronize' && pac.source_branch.startsWith('feature/'))"
   ```

3. **Set appropriate timeouts**: Always set reasonable timeouts to prevent stuck PipelineRuns.

   ```yaml
   pipelinesascode.tekton.dev/timeout: "15m"
   pipelinesascode.tekton.dev/finally-timeout: "5m"
   ```

4. **Control resource usage**: Set concurrency limits and max-keep-runs to manage cluster resources.

   ```yaml
   pipelinesascode.tekton.dev/concurrency-limit: "2"
   pipelinesascode.tekton.dev/max-keep-runs: "10"
   ```

## Troubleshooting Annotations

If your PipelineRun isn't being triggered as expected:

1. Check the controller logs for any annotation parsing errors.
2. Verify that your annotation values are valid JSON where required.
3. Test simpler annotations first, then add complexity.
4. For regex patterns, use tools like [regex101.com](https://regex101.com) to validate your patterns.
5. Remember that all conditions from different annotations must be satisfied for the PipelineRun to trigger.

## Usage Examples

### Basic Pull Request and Push Pipelines

```yaml
# Pull Request Pipeline
apiVersion: tekton.dev/v1
kind: PipelineRun
metadata:
  name: pr-checks
  annotations:
    pipelinesascode.tekton.dev/on-event: "[pull_request]"
    pipelinesascode.tekton.dev/on-target-branch: "[main]"
    pipelinesascode.tekton.dev/task: "[git-clone]"
spec:
  # PipelineRun definition...

---
# Push Pipeline (for CI on main branch)
apiVersion: tekton.dev/v1
kind: PipelineRun
metadata:
  name: main-ci
  annotations:
    pipelinesascode.tekton.dev/on-event: "[push]"
    pipelinesascode.tekton.dev/on-target-branch: "[main]"
    pipelinesascode.tekton.dev/task: "[git-clone]"
spec:
  # PipelineRun definition...
```

### Path-Specific Validation Pipeline

```yaml
# Documentation Check Pipeline
apiVersion: tekton.dev/v1
kind: PipelineRun
metadata:
  name: docs-validation
  annotations:
    pipelinesascode.tekton.dev/on-event: "[pull_request]"
    pipelinesascode.tekton.dev/on-target-branch: "[main, release-*]"
    pipelinesascode.tekton.dev/on-path-change: "[docs/**, **.md]"
    pipelinesascode.tekton.dev/task: "[vale-lint, markdown-lint]"
    pipelinesascode.tekton.dev/max-keep-runs: "3"
spec:
  # PipelineRun definition...
```

### Comment-Triggered Deployment Pipeline

```yaml
# Deployment Pipeline
apiVersion: tekton.dev/v1
kind: PipelineRun
metadata:
  name: deploy-to-staging
  annotations:
    pipelinesascode.tekton.dev/on-comment: "^/deploy-to staging"
    pipelinesascode.tekton.dev/task: "[git-clone, deploy-helm]"
spec:
  # PipelineRun definition...
```

### Advanced Event Matching with CEL

```yaml
# Feature Branch Pipeline
apiVersion: tekton.dev/v1
kind: PipelineRun
metadata:
  name: feature-branch-checks
  annotations:
    pipelinesascode.tekton.dev/on-cel-expression: |
      event == "push" && 
      source_branch.matches("^feature-.*") && 
      files.modified.exists(x, x.matches("src/"))
spec:
  # PipelineRun definition...
```

## Annotation Behavior Notes

1. When using both `on-path-change` and `on-path-change-ignore` annotations, `on-path-change-ignore` takes precedence.

2. When multiple PipelineRuns match an event, all matching PipelineRuns will be executed unless:
   - The `cancel-in-progress` annotation is set to `true` on any matching PipelineRun
   - The `concurrency_limit` is set in the Repository CR

3. For `on-cel-expression`, if provided, it takes precedence over the basic `on-event` and `on-target-branch` annotations.

4. The `max-keep-runs` annotation applies per PipelineRun definition (by name) and will clean up old runs after a successful execution.

5. For remote task resolution, the syntax supports:
   - Tekton Hub tasks: `[task-name]` or `[task-name:version]`
   - Remote URLs: `[https://raw.githubusercontent.com/org/repo/main/task.yaml]`
   - Local repository tasks: `[.tekton/tasks/my-task.yaml]`

## Additional Resources

- [Matching Events Guide]({{< relref "/docs/guide/matchingevents.md" >}})
- [Remote Task Resolution]({{< relref "/docs/guide/resolver.md" >}})
- [GitOps Commands]({{< relref "/docs/guide/gitops_commands.md" >}})
- [PipelineRun Cleanup]({{< relref "/docs/guide/cleanups.md" >}})

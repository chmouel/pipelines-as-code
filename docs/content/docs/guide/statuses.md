---
title: PipelineRun status
weight: 6
---

# PipelineRun Status Reporting

Pipelines-as-Code provides comprehensive status reporting to keep you informed about the state of your PipelineRuns. This document explains how status information is displayed in different scenarios.

## GitHub Apps

When using GitHub Apps integration, PipelineRun statuses are reported through GitHub's Checks API. After a PipelineRun completes, its status is displayed in the GitHub Checks tab, providing a concise overview of:

- Overall status (success/failure)
- Each task's name and duration
- Error information for any failed steps

If a task has a [displayName](https://tekton.dev/docs/pipelines/tasks/#specifying-a-display-name) defined, it will be used as the task description; otherwise, the task name is shown.

If any step fails, a portion of the log from the failing step will be included in the output to help with troubleshooting.

### Error Reporting

Pipelines-as-Code provides detailed error feedback at different stages:

1. **PipelineRun Creation Errors**: If an error occurs while creating the PipelineRun on the cluster (such as template errors or resource limitations), the error message from the Tekton Pipeline Controller will be reported directly to the GitHub UI.

2. **Execution Errors**: Any errors that occur during pipeline execution are also reported to the GitHub UI.

3. **Namespace Matching Errors**: If no namespace matches the repository, error messages will only appear in the Pipelines-as-Code Controller logs, as there is no associated GitHub check to update.

This approach ensures that users can quickly identify and troubleshoot issues without having to access the underlying infrastructure.

## Status Reporting for Other Providers (Webhook-based)

For webhook-based providers (like GitLab, Bitbucket, etc.), status reporting works differently:

- **Pull/Merge Requests**: Status information is added as a comment on the pull or merge request.
- **Push Events**: There's no dedicated space to display PipelineRun status for push events. You can configure alternative notification methods as described in the [Notifications](#notifications) section below.

## Log Snippet Reporting

When a task in the pipeline fails, Pipelines-as-Code displays a brief excerpt (the last three lines) from the failed task. Due to API limitations, only the output from the first failed task can be displayed.

### Secret Masking in Log Snippets

To prevent accidental exposure of secrets, Pipelines-as-Code automatically redacts sensitive information in log snippets by:

1. Retrieving all secrets from environment variables associated with tasks and steps
2. Sorting these values by length (longest first)
3. Replacing any matches in the output snippet with `"*****"`

This ensures that log outputs do not contain leaked secrets.

![log snippet example](/images/snippet-failure-message.png)

The secret masking feature does not currently support concealing secrets from `workspaces` and [envFrom](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.23/#envfromsource-v1-core) sources.

### Error Detection from Container Logs as GitHub Annotations

If you enable the `error-detection-from-container-logs` option in the Pipelines-as-Code ConfigMap, the system will attempt to detect errors in container logs and add them as annotations on the corresponding GitHub Pull Request.

Currently, the system supports simple error formats similar to those from tools like `makefile`, `grep`, and other development tools, specifically in this format:

```
filename:line:column: error message
```

Many development tools like `golangci-lint`, `pylint`, and `yamllint` produce errors in this format.

You can see an example of how Pipelines-as-Code itself uses this feature in its [pull_request.yaml](https://github.com/openshift-pipelines/pipelines-as-code/blob/7c9b16409a1a6c93e9480758f069f881e5a50f05/.tekton/pull-request.yaml#L70) file.

The regular expression used for error detection can be customized with the `error-detection-simple-regexp` setting. This regular expression uses [named groups](https://www.regular-expressions.info/named.html) to provide flexibility, with required groups being `filename`, `line`, and `error` (the `column` group is optional).

By default, error detection only examines the last 50 lines of container logs. You can increase this limit by adjusting the `error-detection-max-number-of-lines` value in the ConfigMap. Setting this value to `-1` will search through all available log lines, though this may increase memory usage.

![GitHub annotations example](/images/github-annotation-error-failure-detection.png)

## Namespace Event Stream

When a namespace is matched to a repository, Pipelines-as-Code emits log messages as Kubernetes events within that namespace. This provides another way to monitor activity and troubleshoot issues.

## Repository CRD Status

The Repository custom resource (CR) stores the status of the five most recent PipelineRuns associated with it. You can view this information with:

```console
% kubectl get repo -n pipelines-as-code-ci
NAME                  URL                                                        NAMESPACE             SUCCEEDED   REASON      STARTTIME   COMPLETIONTIME
pipelines-as-code-ci   https://github.com/openshift-pipelines/pipelines-as-code   pipelines-as-code-ci   True        Succeeded   59m         56m
```

For a more detailed view of all PipelineRuns associated with a repository, use the `tkn pac describe` command from the [CLI](../cli/).

## Notifications

Pipelines-as-Code does not directly manage notifications, but you can implement your own notification system using Tekton's [finally](https://tekton.dev/docs/pipelines/pipelines/#adding-finally-to-the-pipeline) feature. This allows you to execute tasks at the end of a pipeline run regardless of its success or failure.

For example, the coverage generation PipelineRun in the Pipelines-as-Code repository demonstrates how to [send a notification to Slack](https://github.com/openshift-pipelines/pipelines-as-code/blob/16596b478f4bce202f9f69de9a4b5a7ca92962c1/.tekton/generate-coverage-release.yaml#L127) using the [guard feature](https://tekton.dev/docs/pipelines/pipelines/#guard-finally-task-execution-using-when-expressions) when any failure occurs in the PipelineRun:

<https://github.com/openshift-pipelines/pipelines-as-code/blob/16596b478f4bce202f9f69de9a4b5a7ca92962c1/.tekton/generate-coverage-release.yaml#L126>

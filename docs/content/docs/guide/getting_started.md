---
title: Getting Started
weight: 1
---

# Getting Started with Pipelines-as-Code

This guide will help you quickly set up and start using Pipelines-as-Code with GitHub.

## Prerequisites

Before you begin, ensure you have:

- A Kubernetes or OpenShift cluster with Tekton Pipelines installed
- `kubectl` or `oc` CLI configured to access your cluster
- A GitHub account or organization where you can create repositories and install GitHub Apps
- The `tkn` CLI installed (optional but recommended)

## Step 1: Install Pipelines-as-Code

The easiest way to install Pipelines-as-Code is using the `tkn pac` CLI:

```bash
# Install the tkn-pac plugin first
curl -sL https://raw.githubusercontent.com/openshift-pipelines/pipelines-as-code/stable/install/cli/install_cli.sh | bash

# Bootstrap Pipelines-as-Code with GitHub App
tkn pac bootstrap
```

Follow the interactive prompts to:

1. Install Pipelines-as-Code on your cluster (if not already installed)
2. Create a GitHub App
3. Install the GitHub App on your repositories

If you prefer a manual installation, follow the [installation guide]({{< relref "/docs/install/installation" >}}).

## Step 2: Create a Repository

Once Pipelines-as-Code and the GitHub App are installed, create a Repository CR to connect your GitHub repository to Pipelines-as-Code:

```bash
# Using the CLI (recommended)
tkn pac create repository

# Or manually create a Repository CR
cat <<EOF | kubectl apply -f -
apiVersion: pipelinesascode.tekton.dev/v1alpha1
kind: Repository
metadata:
  name: my-repo-name
  namespace: my-namespace
spec:
  url: "https://github.com/my-org/my-repo"
EOF
```

## Step 3: Create a PipelineRun Template

In your GitHub repository, create a `.tekton` directory and add a PipelineRun file:

```bash
mkdir -p .tekton
```

Add a basic PipelineRun template in `.tekton/pull-request.yaml`:

```yaml
apiVersion: tekton.dev/v1
kind: PipelineRun
metadata:
  name: pull-request
  annotations:
    # Run this pipeline on pull requests
    pipelinesascode.tekton.dev/on-event: "[pull_request]"
    # Target the main branch
    pipelinesascode.tekton.dev/on-target-branch: "[main]"
    # Use these Tekton Hub tasks
    pipelinesascode.tekton.dev/task: "[git-clone, golang-test]"
spec:
  workspaces:
    - name: source
      volumeClaimTemplate:
        spec:
          accessModes:
            - ReadWriteOnce
          resources:
            requests:
              storage: 1Gi
  pipelineSpec:
    workspaces:
      - name: source
    tasks:
      - name: fetch-repository
        taskRef:
          name: git-clone
        workspaces:
          - name: output
            workspace: source
        params:
          - name: url
            value: "{{ repo_url }}"
          - name: revision
            value: "{{ revision }}"
      - name: test
        runAfter:
          - fetch-repository
        taskRef:
          name: golang-test
        workspaces:
          - name: source
            workspace: source
```

You can also generate a starter template using the CLI:

```bash
tkn pac generate
```

## Step 4: Test Your Setup

1. Create a pull request to your repository
2. Pipelines-as-Code will automatically detect the PR and run your pipeline
3. Check the GitHub Checks tab to see the pipeline progress and results

## Understanding What Happened

When you create a pull request:

1. GitHub sends a webhook event to the Pipelines-as-Code controller
2. The controller matches the event to your Repository CR
3. It looks for PipelineRun definitions in the `.tekton` directory
4. It resolves and runs any PipelineRuns that match the event based on annotations
5. Results are reported back to GitHub via the Checks API

## Next Steps

Now that you have a basic setup working, here are some next steps to explore:

1. **Add More Complex Pipelines**: Learn about [authoring PipelineRuns]({{< relref "/docs/guide/authoringprs" >}})
2. **Customize Event Matching**: Explore [advanced event matching]({{< relref "/docs/guide/matchingevents" >}})
3. **Use Remote Tasks**: Learn about the [resolver]({{< relref "/docs/guide/resolver" >}})
4. **Configure GitOps Commands**: Try [GitOps commands]({{< relref "/docs/guide/gitops_commands" >}})
5. **Work with Private Repos**: Set up [private repository]({{< relref "/docs/guide/privaterepo" >}}) support

## Common Issues

If you encounter problems:

- Check the [troubleshooting guide]({{< relref "/docs/guide/troubleshooting" >}})
- Verify controller logs: `kubectl logs -n pipelines-as-code deployment/pipelines-as-code-controller`
- Join the [#pipelines-as-code](https://tektoncd.slack.com/archives/C04URDDJ9MZ) channel on Tekton Slack

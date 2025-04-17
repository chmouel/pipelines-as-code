---
title: Troubleshooting
weight: 90
---

# Troubleshooting Pipelines-as-Code

This guide helps you diagnose and resolve common issues when using Pipelines-as-Code.

## General Troubleshooting Steps

Before diving into specific issues, try these general troubleshooting steps:

1. **Check controller logs**:

   ```bash
   kubectl logs -n pipelines-as-code deployment/pipelines-as-code-controller
   ```

2. **Verify webhook service**:

   ```bash
   kubectl get service -n pipelines-as-code pipelines-as-code-controller
   ```

3. **Check Repository CR status**:

   ```bash
   kubectl get repository -n YOUR_NAMESPACE YOUR_REPO_NAME -o yaml
   ```

## Common Issues and Solutions

### PipelineRuns Not Triggering

#### Issue: Events aren't triggering any PipelineRuns

**Possible causes**:

- Event not reaching the controller
- Repository CR not configured correctly
- PipelineRun annotations don't match the event

**Solutions**:

1. **Verify webhook events**:
   - Check GitHub/GitLab webhook settings to confirm events are being sent
   - Verify webhook events reach the controller by checking logs:

     ```bash
     kubectl logs -n pipelines-as-code deployment/pipelines-as-code-controller
     ```

2. **Check Repository CR configuration**:

   ```bash
   kubectl get repository -n YOUR_NAMESPACE YOUR_REPO_NAME -o yaml
   ```

   - Ensure `spec.url` matches your Git repository URL exactly
   - Verify the namespace is correct
   - Check if status shows any error messages

3. **Verify PipelineRun annotations**:
   - Confirm that the PipelineRun annotations match the event type:

     ```yaml
     pipelinesascode.tekton.dev/on-event: "[pull_request]"  # Or push, tag, etc.
     pipelinesascode.tekton.dev/on-target-branch: "[main]"  # Branch being targeted
     ```

   - For debugging, temporarily use a catch-all annotation:

     ```yaml
     pipelinesascode.tekton.dev/on-event: "['pull_request', 'push']"
     pipelinesascode.tekton.dev/on-target-branch: ".*"
     ```

### Failed PipelineRun Execution

#### Issue: PipelineRun starts but fails

**Possible causes**:

- Missing task resources
- Permission issues
- Configuration errors
- Workspace issues

**Solutions**:

1. **Check PipelineRun status**:

   ```bash
   kubectl describe pipelinerun -n YOUR_NAMESPACE NAME
   ```

2. **Examine individual TaskRuns**:

   ```bash
   kubectl get taskrun -n YOUR_NAMESPACE -l tekton.dev/pipelineRun=NAME
   kubectl describe taskrun -n YOUR_NAMESPACE FAILING_TASKRUN_NAME
   ```

3. **Check pod logs**:

   ```bash
   kubectl get pods -n YOUR_NAMESPACE | grep FAILING_TASKRUN_NAME
   kubectl logs -n YOUR_NAMESPACE POD_NAME
   ```

4. **Verify remote tasks exist**:
   - For hub tasks, check if they exist in the Tekton Hub
   - For git tasks, verify the repo/path are accessible

### Authentication Issues

#### Issue: Unable to access private repositories

**Possible causes**:

- Missing or incorrect git credentials
- Secret misconfiguration
- GitHub App installation problems

**Solutions**:

1. **Check if Git auth secret was created**:

   ```bash
   kubectl get secrets -n YOUR_NAMESPACE | grep git-auth
   ```

2. **Verify GitHub App installation**:
   - Reinstall the GitHub App on the repository
   - Check installation permissions

3. **Manual secret configuration**:
   - If automatic secret creation is disabled, create a secret:

     ```yaml
     apiVersion: v1
     kind: Secret
     metadata:
       name: git-auth
     stringData:
       .gitconfig: |
         [credential "https://github.com"]
           helper = store
       .git-credentials: |
         https://TOKEN@github.com
     ```

### Webhook Issues

#### Issue: Webhook calls aren't reaching the controller

**Possible causes**:

- Network connectivity issues
- Webhook URL misconfiguration
- TLS/certificate issues

**Solutions**:

1. **Verify webhook service is running**:

   ```bash
   kubectl get service -n pipelines-as-code pipelines-as-code-controller
   ```

2. **Check webhook endpoint**:
   - For GitHub App, verify the webhook URL in the app settings
   - For webhook mode, check Repository CR webhookURL

3. **Network connectivity**:
   - Ensure your cluster can receive incoming webhook calls
   - For private clusters, configure an ingress or route

4. **TLS Issues**:
   - Check if TLS is configured correctly
   - Verify certificate validity

### Debug Mode

For in-depth troubleshooting, enable debug logging:

```bash
kubectl -n pipelines-as-code set env deployment/pipelines-as-code-controller \
  PAC_DEBUG=true PAC_LOG_LEVEL=debug
```

To view detailed logs:

```bash
kubectl -n pipelines-as-code logs -f deployment/pipelines-as-code-controller
```

To disable debug mode:

```bash
kubectl -n pipelines-as-code set env deployment/pipelines-as-code-controller \
  PAC_DEBUG-
```

## Task Resolution Issues

### Issue: Failed to resolve tasks

**Possible causes**:

- Task doesn't exist in the specified location
- Network connectivity issues
- Permissions issues

**Solutions**:

1. **Check logs for resolution errors**:

   ```bash
   kubectl logs -n pipelines-as-code deployment/pipelines-as-code-controller | grep "resolver"
   ```

2. **For Hub tasks**:
   - Verify the task exists in the specified Tekton Hub
   - Check task version compatibility

3. **For Git tasks**:
   - Ensure the repository and path are correct
   - Verify authentication (for private repos)

## Common Error Messages

| Error Message | Possible Cause | Solution |
|---------------|----------------|----------|
| "Failed to match any PipelineRun" | No PipelineRuns match the event | Check on-event/on-target-branch annotations |
| "Error fetching files" | Cannot access repository files | Check authentication and permissions |
| "Failed to resolve task" | Task doesn't exist or isn't accessible | Verify task exists and is correctly referenced |
| "Timeout waiting for PipelineRun completion" | PipelineRun took too long | Adjust timeout or optimize pipeline |
| "Repository not allowed to run CI" | Missing permissions for GitHub App | Check GitHub App installation permissions |

## Getting Help

If you're still stuck:

1. Check the [GitHub issues](https://github.com/openshift-pipelines/pipelines-as-code/issues) for similar problems
2. Join the [#pipelines-as-code](https://tektoncd.slack.com/archives/C04URDDJ9MZ) channel on Tekton Slack
3. Create a [new issue](https://github.com/openshift-pipelines/pipelines-as-code/issues/new) with detailed information:
   - Controller logs
   - Repository CR configuration
   - PipelineRun definition
   - Event type and source
   - Error messages

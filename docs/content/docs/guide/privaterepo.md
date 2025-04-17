---
title: Private Repositories
weight: 7
---
# Private repositories

Pipelines-as-Code supports working with private repositories by automatically managing authentication secrets needed to clone and interact with Git repositories.

## Secret Creation and Management

When running a PipelineRun, Pipelines-as-Code automatically creates a secret in the target namespace with the following naming format:

```
pac-gitauth-REPOSITORY_OWNER-REPOSITORY_NAME-RANDOM_STRING
```

This secret contains:

1. A `.gitconfig` file configuring the Git client
2. A `.git-credentials` file with the authentication token
3. A token key that can be used in tasks for Git provider API operations

The token in this secret comes from either:

- The GitHub application installation (when using GitHub App)
- The secret attached to the Repository CR (when using the webhook method)

The secret is automatically cleaned up when its associated PipelineRun is deleted, thanks to Kubernetes [ownerReferences](https://kubernetes.io/docs/concepts/overview/working-with-objects/owners-dependents/).

{{< hint info >}}
Secret auto-creation can be disabled by setting `secret-auto-creation: false` in the Pipelines-as-Code ConfigMap.
{{< /hint >}}

## Using the Secret in Your PipelineRun

To use this secret with the `git-clone` task, you need to:

1. Reference it as a workspace named "basic-auth"
2. Pass this workspace to the git-clone task

### Example PipelineRun Configuration

```yaml
spec:
  workspaces:
    - name: basic-auth
      secret:
        secretName: "{{ git_auth_secret }}"  # Dynamic variable that resolves to the auto-created secret
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
      - name: basic-auth
    tasks:
      - name: clone-repository
        taskRef:
          name: git-clone
        workspaces:
          - name: output
            workspace: source
          - name: basic-auth
            workspace: basic-auth
        params:
          - name: url
            value: "{{ repo_url }}"
          - name: revision
            value: "{{ revision }}"
```

## Using the Token in Tasks

The secret also contains the token as a separate key, allowing you to use it in your tasks for Git provider API operations as described in the [Using the Temporary GitHub App Token](../authoringprs/#using-the-temporary-github-app-token-for-github-api-operations) section.

## Working with Private Remote Tasks

For information about accessing tasks from private repositories, see the [Remote HTTP URL from a private repository](../resolver/#remote-http-url-from-a-private-repository) section in the resolver documentation.

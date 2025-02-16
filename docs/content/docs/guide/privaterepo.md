---
title: Private Repositories
weight: 7
---
# Private repositories

Want to use private repos with Pipelines as Code? No problem!  It handles this by creating a secret in your namespace. This secret holds the token that the `git-clone` task needs to access your private repositories.

Here's the cool part: when Pipelines as Code spins up a new PipelineRun, it automatically creates a secret for you in the same namespace.  The secret's name looks something like this:

`pac-gitauth-REPOSITORY_OWNER-REPOSITORY_NAME-RANDOM_STRING`

Inside this secret, you'll find two important files: `.gitconfig` and `.git-credentials`. These files are set up to tell Git how to connect to your Git provider (like GitHub at `https://github.com`) using the token. This token comes from either the GitHub App or a secret you've set up in your repo's settings if you're using webhooks.

The secret even has a handy key that points directly to the token. This makes it super easy to grab the token and use it in your tasks if you need to talk to your Git provider for other things.

Want to see how to use this token in action? Check out this section: [Using the temporary GitHub App token for GitHub API operations](../authoringprs/#using-the-temporary-github-app-token-for-github-api-operations).

And here's another neat thing: this secret is linked to the PipelineRun that created it.  So, when you delete the PipelineRun, the secret cleans itself up too!

{{< hint warning >}}
Need to keep the secret around even after the PipelineRun is gone? No worries! You can tweak the `secret-auto-create` setting in the Pipelines as Code ConfigMap. Set it to `false` if you want to disable the auto-deletion.
{{< /hint >}}

## Using the Token in Your PipelineRun

Okay, so how do you actually use this token in your PipelineRun?  The `git-clone` task (check out its docs [here](https://github.com/tektoncd/catalog/blob/main/task/git-clone/0.4/README.md)) expects the secret to be provided as a workspace named "basic-auth".  So, let's get that set up in your PipelineRun.

To make this work, you just need to add a workspace to your PipelineRun that points to the secret.  Like this:

```yaml
  workspace:
  - name: basic-auth
    secret:
      secretName: "{{ git_auth_secret }}"
```

Once you've got that workspace set up, you can then use the `git-clone` task.  The usual way to do this is to add `git-clone` as a step in your Pipeline or PipelineRun and make sure you tell it to use the "basic-auth" workspace. Here's a snippet showing how it all fits together:

```yaml
[…]
workspaces:
  - name basic-auth
params:
    - name: repo_url
    - name: revision
[…]
tasks:
  workspaces:
    - name: basic-auth
      workspace: basic-auth
  […]
  tasks:
  - name: git-clone-from-catalog
      taskRef:
        name: git-clone
      params:
        - name: url
          value: $(params.repo_url)
        - name: revision
          value: $(params.revision)
```

-  Want to see a complete example? Take a peek at [this PipelineRun](https://github.com/openshift-pipelines/pipelines-as-code/blob/main/test/testdata/pipelinerun_git_clone_private.yaml).

## What about fetching tasks from private repos?

If you need to grab tasks from private repositories too,  the [resolver docs](../resolver/#remote-http-url-from-a-private-github-repository) have got you covered with the details.

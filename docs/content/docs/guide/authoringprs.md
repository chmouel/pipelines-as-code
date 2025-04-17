---
title: Authoring PipelineRun
weight: 3
---

# Authoring PipelineRuns in `.tekton/` directory

- Pipelines-as-Code will always try to be as close to the Tekton template as
  possible. Usually, you will write your template and save them with a `.yaml`
  extension, and Pipelines-as-Code will run them.

- The `.tekton` directory must be at the top level of the repo.
  You can reference YAML files in other repos using remote URLs
  (see [Remote HTTP URLs](./resolver.md#remote-http-url) for more information),
  but PipelineRuns will only be triggered by events in the repository containing
  the `.tekton` directory.

- Using its [resolver](../resolver/) Pipelines-as-Code will try to bundle the
  PipelineRun with all its Tasks as a single PipelineRun with no external
  dependencies.

- Inside your pipeline, you need to be able to check out the commit as
  received from the webhook by checking out the repository from that ref. Most of the time
  you want to reuse the
  [git-clone](https://github.com/tektoncd/catalog/blob/main/task/git-clone/)
  task from the [tektoncd/catalog](https://github.com/tektoncd/catalog).

- To be able to specify parameters of your commit and URL, Pipelines-as-Code
  gives you some “dynamic” variables that are defined according to the execution
  of the events. Those variables look like this `{{ var }}` and can be used
  anywhere in your template, see [below](#dynamic-variables) for the list of
  available variables.

- For Pipelines-as-Code to process your `PipelineRun`, you must have either an
  embedded `PipelineSpec` or a separate `Pipeline` object that references a YAML
  file in the `.tekton` directory. The Pipeline object can include `TaskSpecs`,
  which may be defined separately as Tasks in another YAML file in the same
  directory. It's important to give each `PipelineRun` a unique name to avoid
  conflicts. **PipelineRuns with duplicate names will never be matched**.

## Dynamic variables

`Pipelines-as-Code` provides several dynamic variables that you can use in your templates. These variables are replaced at runtime with values specific to the Git event that triggered the pipeline.

These variables use the syntax `{{ variable_name }}` and can be used anywhere in your PipelineRun template.

### Default Parameters

Here's the complete list of available dynamic variables:

| Variable Name | Description | Example Usage | Example Value |
|---------------|-------------|---------------|--------------|
| `repo_name` | The repository name. | `{{repo_name}}` | pipelines-as-code |
| `repo_owner` | The repository owner (user or organization). | `{{repo_owner}}` | openshift-pipelines |
| `repo_url` | The repository's full URL. | `{{repo_url}}` | <https://github.com/openshift-pipelines/pipelines-as-code> |
| `revision` | The commit's full SHA revision. | `{{revision}}` | 1234567890abcdef |
| `sender` | The username (or account ID on some providers) of the commit/PR author. | `{{sender}}` | johndoe |
| `source_branch` | The branch where the event originated. | `{{source_branch}}` | feature-branch |
| `source_url` | The source repository URL from which the event comes (same as `repo_url` for push events). | `{{source_url}}` | <https://github.com/johndoe/pipelines-as-code> |
| `target_branch` | The branch which the event targets (same as `source_branch` for push events). | `{{target_branch}}` | main |
| `target_namespace` | The namespace where the Repository was matched and where the PipelineRun will be created. | `{{target_namespace}}` | my-namespace |
| `pull_request_number` | The pull request number (only available for pull request events). | `{{pull_request_number}}` | 123 |
| `pull_request_title` | The pull request title (only available for pull request events). | `{{pull_request_title}}` | Add feature X |
| `pull_request_body` | The body/description of the pull request (only available for pull request events). | `{{pull_request_body}}` | This PR implements feature X... |
| `pull_request_url` | The URL to the pull request (only available for pull request events). | `{{pull_request_url}}` | <https://github.com/openshift-pipelines/pipelines-as-code/pull/123> |
| `pull_request_labels` | Labels on the pull request, separated by newlines (only available for pull request events). | `{{pull_request_labels}}` | bug\nneeds-review |
| `trigger_comment` | The comment that triggered the PipelineRun when using a GitOps command (like `/test`, `/retest`). | `{{trigger_comment}}` | /merge-pr branch |
| `git_auth_secret` | The name of the secret created for Git authentication. | `{{git_auth_secret}}` | pac-gitauth-owner-repo-a1b2c3 |
| `event_type` | The type of event that triggered the PipelineRun. | `{{event_type}}` | pull_request, push, incoming |
| `event_url` | A URL pointing to the event that triggered the PipelineRun. | `{{event_url}}` | <https://github.com/owner/repo/pull/123> |
| `commit_message` | The commit message of the revision. | `{{commit_message}}` | Fix bug in feature X |

### Using Variables in YAML

When using these variables in YAML, be careful with values that might contain special characters or multiple lines. For YAML block scalars or values that may contain quotes, you should use the YAML block scalar syntax (`>` or `|`):

```yaml
spec:
  params:
    - name: description
      value: >
        {{ pull_request_body }}
```

For more complex objects like JSON values, you might need to use a block literal (`|`) to preserve newlines:

```yaml
spec:
  params:
    - name: pull_request
      value: |
        {{ body.pull_request }}
  pipelineSpec:
    tasks:
      # ...
```

### Defining Parameters with Object Values in YAML

When you need to use a body payload as a parameter value (for example, the entire pull request object), use a block scalar to ensure proper YAML formatting:

```yaml
spec:
  params:
    - name: pull_request
      value: >
        {{ body.pull_request }}
  pipelineSpec:
    tasks:
      # ...
```

Using block format avoids YAML validation errors and ensures that your data is properly structured.

## Matching an event to a PipelineRun

Each `PipelineRun` can match different Git provider events through some special
annotations on the `PipelineRun`.

For example, when you have these metadata in
your `PipelineRun`:

```yaml
metadata:
  name: pipeline-pr-main
annotations:
  pipelinesascode.tekton.dev/on-target-branch: "[main]"
  pipelinesascode.tekton.dev/on-event: "[pull_request]"
```

`Pipelines-as-Code` will match the PipelineRun `pipeline-pr-main` if the Git
provider events target the branch `main` and it's coming from a `[pull_request]`

There are many ways to match an event to a PipelineRun, head over to this patch
[page]({{< relref "/docs/guide/matchingevents.md" >}}) for more details.

## Using the body and headers in a Pipelines-as-Code parameter

Pipelines-as-Code lets you access the full body and headers of the request as a CEL expression.

This allows you to go beyond the standard variables and even play with multiple
conditions and variables to output values.

For example, if you want to get the title of the Pull Request in your PipelineRun you can simply access it like this:

```go
{{ body.pull_request.title }}
```

You can then get creative and for example mix the variable inside a python
script to evaluate the json.

This task, for example, is using python and will check the labels on the PR,
`exit 0` if it has the label called 'bug' on the pull request or `exit 1` if it
doesn't:

```yaml
taskSpec:
  steps:
    - name: check-label
      image: registry.access.redhat.com/ubi9/ubi
      script: |
        #!/usr/bin/env python3
        import json
        labels=json.loads("""{{ body.pull_request.labels }}""")
        for label in labels:
            if label['name'] == 'bug':
              print('This is a PR targeting a BUG')
              exit(0)
        print('This is not a PR targeting a BUG :(')
        exit(1)
```

The expressions are CEL expressions so you can as well make some conditional:

```yaml
- name: bash
  image: registry.access.redhat.com/ubi9/ubi
  script: |
    if {{ body.pull_request.state == "open" }}; then
      echo "PR is Open"
    fi
```

if the PR is open the condition then returns `true` and the shell script sees this
as a valid boolean.

Headers from the payload body can be accessed from the `headers` keyword, note that headers are case-sensitive,
for example, this will show the GitHub event type for a GitHub event:

```yaml
{{ headers['X-Github-Event'] }}
```

and then you can do the same conditional or access as described above for the `body` keyword.

## Using the temporary GitHub APP Token for GitHub API operations

You can use the temporary installation token that is generated by Pipelines as
Code from the GitHub App to access the GitHub API.

The token value is stored in the temporary git-auth secret as generated for [private
repositories](../privaterepo/) in the key `git-provider-token`.

As an example, if you want to add a comment to your pull request, you can use the
[github-add-comment](https://hub.tekton.dev/tekton/task/github-add-comment)
task from the [Tekton Hub](https://hub.tekton.dev)
using a [pipelines as code annotation](../resolver/#remote-http-url):

```yaml
pipelinesascode.tekton.dev/task: "github-add-comment"
```

you can then add the task to your [tasks section](https://tekton.dev/docs/pipelines/pipelines/#adding-tasks-to-the-pipeline) (or [finally](https://tekton.dev/docs/pipelines/pipelines/#adding-finally-to-the-pipeline) tasks) of your PipelineRun :

```yaml
[...]
tasks:
  - name:
      taskRef:
        name: github-add-comment
      params:
        - name: REQUEST_URL
          value: "{{ repo_url }}/pull/{{ pull_request_number }}"
        - name: COMMENT_OR_FILE
          value: "Pipelines-as-Code IS GREAT!"
        - name: GITHUB_TOKEN_SECRET_NAME
          value: "{{ git_auth_secret }}"
        - name: GITHUB_TOKEN_SECRET_KEY
          value: "git-provider-token"
```

Since we are using the dynamic variables we are able to reuse this on any
PullRequest from any repositories.

and for completeness, here is another example of how to set the GITHUB_TOKEN
environment variable on a task step:

```yaml
env:
  - name: GITHUB_TOKEN
    valueFrom:
      secretKeyRef:
        name: "{{ git_auth_secret }}"
        key: "git-provider-token"
```

{{< hint info >}}

- On GitHub apps the generated installation token [will be available for 8 hours](https://docs.github.com/en/developers/apps/building-github-apps/refreshing-user-to-server-access-tokens)
- On GitHub apps the token is scoped to the repository the event (payload) comes
  from unless [configured](/docs/install/settings#pipelines-as-code-configuration-settings) differently on the cluster.

{{< /hint >}}

## Example

`Pipelines as code` test itself, you can see the examples in its
[.tekton](https://github.com/openshift-pipelines/pipelines-as-code/tree/main/.tekton) repository.

---
title: Authoring PipelineRun
weight: 3
---

# Crafting PipelineRuns in Your `.tekton/` Folder

So, you're looking to set up your workflows with Pipelines as Code? Awesome!  The general idea is to keep things as close to standard Tekton as possible. You probably already write your Tekton templates and save them as `.yaml` files, right? Pipelines as Code is designed to just pick those up and run them.

Here's the deal with the `.tekton` directory: it *must* live right at the top level of your repository. Think of it as the command center for your pipelines.  You *can* pull in YAML files from other places using web addresses (check out [Remote HTTP URLs](./resolver.md#remote-http-url) for the nitty-gritty). But remember, PipelineRuns only kick off when something happens in the repo that contains that `.tekton` directory.  So, keep your pipelines close to your code!

Using its clever [resolver](../resolver/), Pipelines as Code tries to package up your PipelineRun and all the Tasks it needs into one neat bundle. This means you get a single PipelineRun without having to worry about external dependencies hanging around. Nice and tidy!

Now, inside your pipeline, you'll almost always want to grab the code that triggered the pipeline run in the first place.  That means checking out the repository at the commit that came in with the webhook.  The easiest way to do this?  Reach for the trusty [git-clone](https://github.com/tektoncd/catalog/blob/main/task/git-clone/) task from the [tektoncd/catalog](https://github.com/tektoncd/catalog). It’s a real workhorse.

To make things flexible, Pipelines as Code gives you some handy "dynamic" variables. These are like magic words that change depending on what event triggered your pipeline. They look like this: `{{ var }}`. You can sprinkle these anywhere in your template.  We've got a list of them [below](#dynamic-variables), so you can see what goodies are available.

For Pipelines as Code to actually *do* anything with your `PipelineRun`, it needs to know what pipeline to run! You've got two main choices here: either embed the `PipelineSpec` directly in your `PipelineRun` file, or create a separate `Pipeline` object that points to a YAML file in your `.tekton` directory.  If you go the separate `Pipeline` route, you can even include `TaskSpecs` right inside it, or define your Tasks separately in other YAML files in the same directory.  One important thing: make sure every `PipelineRun` has a unique name.  **PipelineRuns with the same name? They'll just be ignored.**  Don't let your pipelines get into a naming fight!

## Dynamic Variables: Your Pipeline's Secret Sauce

Here's the rundown on all the dynamic variables Pipelines as Code gives you.  If you're just starting out, the `revision` and `repo_url` variables are probably going to be your best friends. They tell you the commit SHA and the repository URL that's being tested.  Pair these with the [git-clone](https://hub.tekton.dev/tekton/task/git-clone) task, and you're set to checkout the right code.

| Variable            | Description                                                                                                                                                                     | Example                             | Example Output                                                                                                                                                |
|---------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------|
| body                | The whole shebang – the full request payload body. (More on this [below](#using-the-body-and-headers-in-a-pipelines-as-code-parameter))                                                                               | `{{body.pull_request.user.email }}` | <email@domain.com>                                                                                                                                            |
| event_type          | What kind of event kicked this off? (Like `pull_request` or `push`).                                                                                                                                   | `{{event_type}}`                    | pull_request          (Psst! Check out the note for GitOps Comments [here]({{< relref "/docs/guide/gitops_commands.md#event-type-annotation-and-dynamic-variables" >}}) ) |
| git_auth_secret     | A secret name, automatically created, that holds the token for checking out private repos.                                                                                                  | `{{git_auth_secret}}`               | pac-gitauth-xkxkx                                                                                                                                             |
| headers             | All the request headers. (More info [below](#using-the-body-and-headers-in-a-pipelines-as-code-parameter))                                                                                 | `{{headers['x-github-event']}}`     | push                                                                                                                                                          |
| pull_request_number | If it's a pull request event, this is the number of that request.                                                                                      | `{{pull_request_number}}`           | 1                                                                                                                                                             |
| repo_name           | The name of the repository.  Pretty straightforward.                                                                                                                             | `{{repo_name}}`                     | pipelines-as-code                                                                                                                                             |
| repo_owner          | Who owns the repository? This'll tell you.                                                                                                                            | `{{repo_owner}}`                    | openshift-pipelines                                                                                                                                           |
| repo_url            | The full web address of the repository.                                                                                                                                                        | `{{repo_url}}`                      | https:/github.com/repo/owner                                                                                                                                  |
| revision            | The full commit SHA.  Handy for pinpointing a specific commit.                                                                                                                                                   | `{{revision}}`                      | 1234567890abcdef                                                                                                                                              |
| sender              | The username (or account ID) of the person who triggered the commit.                                                                                                             | `{{sender}}`                        | johndoe                                                                                                                                                       |
| source_branch       | The branch where the event originated.                                                                                                                                      | `{{source_branch}}`                 | main                                                                                                                                                          |
| source_url          | The URL of the source repository (usually the same as `repo_url` for push events).                                                                                  | `{{source_url}}`                    | https:/github.com/repo/owner                                                                                                                                  |
| target_branch       | The branch the event is aimed at (usually the same as `source_branch` for push events).                                                                                           | `{{target_branch}}`                 | main                                                                                                                                                          |
| target_namespace    | The Kubernetes namespace where your Repository matched and where the PipelineRun will be created.                                                                                      | `{{target_namespace}}`              | my-namespace                                                                                                                                                  |
| trigger_comment     | If a [GitOps command]({{< relref "/docs/guide/running.md#gitops-command-on-pull-or-merge-request" >}}) like `/test` or `/retest` started this, here's the comment. | `{{trigger_comment}}`               | /merge-pr branch                                                                                                                                              |
| pull_request_labels |  A list of labels on the pull request, each on a new line.                                                                                                                          | `{{pull_request_labels}}`           | bugs\nenhancement                                                                                                                                             |

### YAML Gotcha: Parameters with Object Values

YAML can be a bit picky sometimes, especially when you're setting up parameters. If you want to pass an object or a dynamic variable (like `{{ body }}`) as a parameter value, you might run into trouble if you try to do it inline. YAML's validation rules just don't like it.

For example, this *won't* work:

```yaml
spec:
  params:
    - name: body
      value: {{ body }}  # Nope, YAML says no!
  pipelineSpec:
    tasks:
```

You'll get a YAML validation error because objects and multi-line strings can't be squished inline like that.  The fix? Use the "block format" instead.  Here's how:

```yaml
spec:
  params:
    - name: body
      value: |- # "Pipe" symbol for block format
        {{ body }}
    # Or, you can use ">" too, it also means block format
    - name: pull_request
      value: > # "Greater than" symbol for another block format option
        {{ body.pull_request }}
  pipelineSpec:
    tasks:
```

Using the block format keeps YAML happy and your pipelines running smoothly.

## Matching Events to Your PipelineRun: It's All About Annotations

Each `PipelineRun` can be set up to react to different kinds of events from your Git provider.  The secret? Special annotations on your `PipelineRun`.

Let's say you have this in your `PipelineRun` metadata:

```yaml
metadata:
  name: pipeline-pr-main
annotations:
  pipelinesascode.tekton.dev/on-target-branch: "[main]"
  pipelinesascode.tekton.dev/on-event: "[pull_request]"
```

Pipelines as Code will only run this `pipeline-pr-main` PipelineRun if two things are true: the Git event is aimed at the `main` branch, *and* it's a `pull_request` event.  Simple as that!

There are lots of ways to match events to PipelineRuns. If you want to dive deeper, check out this [page]({{< relref "/docs/guide/matchingevents.md" >}}).

## Digging into Request Body and Headers in Pipelines as Code Parameters

Pipelines as Code lets you get at the full request body and headers using CEL expressions.  Think of it as having superpowers!

This means you're not stuck with just the standard variables. You can get really clever and use conditions and combinations of variables to pull out exactly what you need.

Want to grab the title of a Pull Request in your PipelineRun?  Easy peasy:

```go
{{ body.pull_request.title }}
```

And you can get even fancier!  Mix these variables into scripts, like Python, to process JSON and do all sorts of cool stuff.

For instance, this task uses Python to check the labels on a PR. It'll `exit 0` (success) if it finds a label called 'bug' on the pull request, and `exit 1` (failure) if it doesn't:

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
              print('This PR is about a BUG!')
              exit(0)
        print('Nope, not a bug-fix PR :(')
        exit(1)
```

Since these are CEL expressions, you can even use conditionals.  Check this out:

```yaml
- name: bash
  image: registry.access.redhat.com/ubi9/ubi
  script: |
    if {{ body.pull_request.state == "open" }}; then
      echo "PR is Open"
    fi
```

If the PR is open, that condition becomes `true`, and your shell script sees it as a regular boolean value.

Headers from the payload body are accessed using the `headers` keyword.  Keep in mind that headers are case-sensitive!  For example, to see the GitHub event type for a GitHub event:

```yaml
{{ headers['X-Github-Event'] }}
```

You can then use the same conditional logic and access methods for headers as you do for the `body` keyword.

## Using the Temporary GitHub App Token for GitHub API Magic

Pipelines as Code can generate a temporary installation token from the GitHub App, and you can use this token to talk to the GitHub API.  Pretty neat, huh?

This token lives in that temporary `git-auth` secret we talked about earlier (the one for [private repositories](../privaterepo/)), under the key `git-provider-token`.

Let's say you want to add a comment to your pull request. You can use the [github-add-comment](https://hub.tekton.dev/tekton/task/github-add-comment) task from the [Tekton Hub](https://hub.tekton.dev). Just use a [pipelines as code annotation](../resolver/#remote-http-url) to pull it in:

```yaml
pipelinesascode.tekton.dev/task: "github-add-comment"
```

Then, add this task to the `tasks` (or `finally` tasks) section of your `PipelineRun`:

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

Because we're using those dynamic variables, this will work on any Pull Request in any repository.  Talk about reusable!

And just to show you another way, here's how to set the `GITHUB_TOKEN` environment variable in a task step:

```yaml
env:
  - name: GITHUB_TOKEN
    valueFrom:
      secretKeyRef:
        name: "{{ git_auth_secret }}"
        key: "git-provider-token"
```

{{< hint info >}}

- Heads up: On GitHub Apps, the generated installation token [is good for 8 hours](https://docs.github.com/en/developers/apps/building-github-apps/refreshing-user-to-server-access-tokens).
- Also, on GitHub Apps, the token is limited to the repository that triggered the event, unless you've [changed the settings](/docs/install/settings#pipelines-as-code-configuration-settings) on your cluster.

{{< /hint >}}

## Example? Look No Further Than Pipelines as Code Itself

Want to see Pipelines as Code in action?  Check out how Pipelines as Code tests *itself*.  You can find examples in its own [.tekton](https://github.com/openshift-pipelines/pipelines-as-code/tree/main/.tekton) repository.  It's a great place to see real-world examples!

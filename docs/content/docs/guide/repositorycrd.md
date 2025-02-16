---
title: Repository CR
weight: 1
---

# Repository CR: Tell Pipelines as Code Where to Listen

Think of a `Repository CR` as your way of telling Pipelines as Code, "Hey, pay attention to this Git repo!"  It's how you set things up so that when something happens in your code repository, Pipelines as Code knows it's time to spring into action.

Specifically, a `Repository CR` does a few key things:

* **Points Pipelines as Code to your repo:** It tells Pipelines as Code which repository URL to watch for events.
* **Sets the stage for action:** It defines the namespace where all the `PipelineRuns` (your CI/CD jobs) will actually run.  Think of it as picking the workspace for your pipelines.
* **Handles Git secrets (if needed):** If your Git provider needs extra info like API keys, usernames, or API URLs (especially if you're using webhooks instead of the GitHub App), the Repository CR is where you can specify those.
* **Keeps you in the loop:** It shows you the status of the last few `PipelineRuns` (the 5 most recent ones, by default) for this repository, so you can quickly see how things are going.
* **Lets you customize things:** You can even declare [custom parameters]({{< relref "/docs/guide/customparams" >}}) inside your `PipelineRun`. These parameters can change based on different conditions – pretty handy for tailoring your pipelines.

To get Pipelines as Code working for your project, you *must* create a Repository CR in your own project's namespace.  For example, if your project namespace is `project-repository`, that's where you'd create it.

**Important note:**  Don't try to create a Repository CR in the namespace where Pipelines as Code itself is running (like `openshift-pipelines` or `pipelines-as-code`). It just won't work. Think of it like keeping your project files separate from the system files.

You've got a couple of ways to create a Repository CR:

* **Using the command line (tkn pac):** The easiest way is often with the `tkn pac create repository` command from the [tkn pac CLI]({{< relref "/docs/guide/cli.md" >}}).
* **Using YAML (kubectl):** If you prefer, you can create a YAML file and apply it with `kubectl`. Here's a quick example:

    ```yaml
    cat <<EOF|kubectl create -n project-repository -f-
    apiVersion: "pipelinesascode.tekton.dev/v1alpha1"
    kind: Repository
    metadata:
      name: project-repository
    spec:
      url: "https://github.com/linda/project"
    EOF
    ```

Once you've got this set up, whenever something happens in the `linda/project` repository (like a code push or a pull request), Pipelines as Code will know to pay attention. It'll then check out the code in `linda/project` and look in the `.tekton/` directory to see if there are any `PipelineRun` definitions that match the event.

If it finds a `PipelineRun` that's a match (based on annotations like branch name and event type like `push` or `pull_request`), it'll kick off that `PipelineRun` in the same namespace where you created the `Repository` CR.  Yep, `PipelineRuns` always run in the same namespace as their `Repository` CR.

{{< hint info >}}
**Security heads-up:** Pipelines as Code has a built-in safety net using a Kubernetes Mutating Admission Webhook.  This webhook makes sure there's only one Repository CR for each repository URL across your whole cluster. It also checks that URLs are valid and not just empty strings.

Why is this important?  Well, disabling this webhook isn't a good idea, especially if you're sharing your cluster with people you don't fully trust.  Without it, someone could potentially create their own Repository CR for *your* private repository, hijack your pipelines, and mess with things they shouldn't.

If you *were* to disable the webhook (again, not recommended!), and multiple Repository CRDs existed for the same URL, only the *first* one created would be noticed by Pipelines as Code.  Unless, that is, someone cleverly used the `target-namespace` annotation in their `PipelineRun` to specifically point to a different Repository CR.
{{< /hint >}}

## Extra Security: Pinpointing PipelineRun Locations

Want to add another layer of security? You can use a `PipelineRun` annotation to explicitly say which namespace a `PipelineRun` should be associated with.  Even with this annotation, you still need a `Repository CRD` in that namespace for things to work.

This annotation is really helpful to prevent someone malicious on the cluster from trying to run pipelines in a namespace they shouldn't have access to. It's like saying, "This repo belongs to *this* namespace, and only pipelines in *this* namespace can run for it."

To use this feature, just add this annotation to your pipeline definition:

```yaml
pipelinesascode.tekton.dev/target-namespace: "mynamespace"
```

With this annotation, Pipelines as Code will *only* look for a matching repository in the `mynamespace` namespace. It won't go searching through every repository in the entire cluster.

### Where Should PipelineRun Definitions Come From?

By default, when a `Push` or `Pull Request` event happens, Pipelines as Code grabs the `PipelineRun` definition from the very same branch where the event was triggered.

But you can change this behavior with the `pipelinerun_provenance` setting.  Currently, you have two choices:

* `source`: (This is the default.) Get the `PipelineRun` definition from the branch that triggered the event.
* `default_branch`: Get the `PipelineRun` definition from the repository's default branch (like `main`, `master`, or `trunk` – whatever's set on your Git platform).

Here's an example of how to set this up in your `Repository` CR:

```yaml
apiVersion: "pipelinesascode.tekton.dev/v1alpha1"
kind: Repository
metadata:
  name: my-repo
spec:
  url: "https://github.com/owner/repo"
  settings:
    pipelinerun_provenance: "default_branch"
```

In this example, for the repository `https://github.com/owner/repo`, Pipelines as Code will always look for the `PipelineRun` definition on the default branch, no matter which branch triggers the event.

{{< hint info >}}
Setting the `PipelineRun` definition source to the default branch adds another level of security.  It makes sure that only people who can merge code into the default branch can actually change the pipeline and influence your infrastructure. It's like saying, "Only changes that are officially part of the main codebase can change how we build and deploy."
{{< /hint >}}

## Keeping Things in Check: Concurrency

The `concurrency_limit` setting lets you control how many `PipelineRuns` can be running at the same time for a single repository. It's like setting a maximum number of workers for your CI/CD jobs.

```yaml
spec:
  concurrency_limit: <number>
```

If multiple `PipelineRuns` are triggered by an event, they'll be started one after another in alphabetical order.

Let's say you have three `PipelineRun` files in your `.tekton` directory (`pipeline-a.yaml`, `pipeline-b.yaml`, `pipeline-c.yaml`), and you set `concurrency_limit: 1` in your repository config.  If you create a pull request, all three pipelines will run, but one at a time, in the order `pipeline-a`, then `pipeline-b`, then `pipeline-c`.  Only one will be actively running at any moment, while the others wait in line.

## Expanding the Scope of Your GitHub Token

By default, the GitHub token Pipelines as Code creates is limited to just the repository that triggered the event.  But sometimes, you need that token to reach into other repositories too.

For example, maybe your CI code lives in one repo (say, `ci-repo`), but your actual build process (defined in `pr.yaml`) needs to pull tasks from a separate private repository (like `cd-tasks-repo`).

You can broaden the token's reach in two ways:

* **Globally:** Admins can set up a list of extra repositories that *any* Repository CR in *any* namespace can access.
* **At the Repository level:**  You can specify extra repositories that *only* the current Repository CR (and others in the same namespace) can access.  Both admins and regular users can configure this.

{{< hint info >}}
**Webhook users, take note:** If you're using a GitHub webhook, the token scoping here is similar to what you set up when you created your [fine-grained personal access token](https://github.blog/2022-10-18-introducing-fine-grained-personal-access-tokens-for-github/#creating-personal-access-tokens).
{{</ hint >}}

**Before you start:**

Make sure you've set `secret-github-app-token-scoped` to `false` in the `pipelines-as-code` configmap.  This setting is what enables this extra token scoping magic.

### Global Token Scope Configuration

For a cluster-wide list of extra repositories, admins can edit the `pipelines-as-code` configmap and set the `secret-github-app-scope-extra-repos` key.  Like this:

  ```yaml
  apiVersion: v1
  kind: ConfigMap
  metadata:
    name: pipelines-as-code
    namespace: pipelines-as-code
  data:
    secret-github-app-scope-extra-repos: "owner2/project2, owner3/project3"
  ```

### Repository-Specific Token Scope

If you want to scope the token to extra repositories just for a particular `Repository CR`, you can use the `github_app_token_scope_repos` setting in the `Repository` spec.  These extra repos must be in the *same* namespace as the Repository CR itself.

Here's how to do it:

  ```yaml
  apiVersion: "pipelinesascode.tekton.dev/v1alpha1"
  kind: Repository
  metadata:
    name: test
    namespace: test-repo
  spec:
    url: "https://github.com/linda/project"
    settings:
      github_app_token_scope_repos:
      - "owner/project"
      - "owner1/project1"
  ```

In this example, the `Repository` CR is for `linda/project` in the `test-repo` namespace. The generated GitHub token will now also have access to `owner/project` and `owner1/project1`, in addition to `linda/project`.  All of these must live within the `test-repo` namespace.

**Important:**

If any of the repositories you list *don't* actually exist in the namespace, the token scoping will fail, and you'll see an error message like this:

```console
failed to scope GitHub token as repo owner1/project1 does not exist in namespace test-repo
```

### Combining Global and Repository Scopes

* **Both global and repository settings?** If you set both `secret-github-app-scope-extra-repos` in the configmap *and* `github_app_token_scope_repos` in the `Repository CR`, the token will be scoped to *all* repositories from both lists, plus the original repository.

    For example:

  * `pipelines-as-code` configmap:

      ```yaml
      apiVersion: v1
      kind: ConfigMap
      metadata:
        name: pipelines-as-code
        namespace: pipelines-as-code
      data:
        secret-github-app-scope-extra-repos: "owner2/project2, owner3/project3"
      ```

  * `Repository` CR:

      ```yaml
       apiVersion: "pipelinesascode.tekton.dev/v1alpha1"
       kind: Repository
       metadata:
         name: test
         namespace: test-repo
       spec:
         url: "https://github.com/linda/project"
         settings:
           github_app_token_scope_repos:
           - "owner/project"
           - "owner1/project1"
      ```

      The token will cover: `owner/project`, `owner1/project1`, `owner2/project2`, `owner3/project3`, and `linda/project`.

* **Just global settings?** If you only set `secret-github-app-scope-extra-repos`, the token covers all the listed repositories and the original repository.

* **Just repository settings?** If you only use `github_app_token_scope_repos`, the token covers the listed repositories and the original repository. Remember, all these repos must be in the same namespace as the `Repository CR`.

* **GitHub App not installed?** If you list repos where the GitHub App isn't installed (either globally or at the repository level), token creation will fail with an error like:

    ```text
    failed to scope token to repositories in namespace test-repo with error : could not refresh installation id 36523992's token: received non 2xx response status \"422 Unprocessable Entity\" when fetching https://api.github.com/app/installations/36523992/access_tokens: Post \"https://api.github.com/repos/savitaashture/article/check-runs\
    ```

* **Token scoping fails? CI stops.** If token scoping fails for *any* reason (including if a repository listed at the repository level isn't in the right namespace), the CI process will not run. This includes situations where the same repo is listed both globally and at the repository level, and the repository-level scoping fails because of namespace issues.

    Example: `owner5/project5` is in both global and repository configs:

    ```yaml
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: pipelines-as-code
      namespace: pipelines-as-code
    data:
      secret-github-app-scope-extra-repos: "owner5/project5"
    ```

    ```yaml
    apiVersion: "pipelinesascode.tekton.dev/v1alpha1"
    kind: Repository
    metadata:
      name: test
      namespace: test-repo
    spec:
      url: "https://github.com/linda/project"
      settings:
        github_app_token_scope_repos:
        - "owner5/project5"
    ```

    If `owner5/project5` is *not* in the `test-repo` namespace, you'll get this error, and CI won't run:

    ```yaml
    failed to scope GitHub token as repo owner5/project5 does not exist in namespace test-repo
    ```

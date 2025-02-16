---
title: Resolver
weight: 2
---
# Pipelines-as-Code Resolver: Making Sure Your Pipelines Play Nice

Ever run into trouble where one PipelineRun messes with another?  The Pipelines-as-Code resolver is here to prevent that headache. Think of it as the traffic controller for your pipelines!

Here's the deal: Pipelines-as-Code automatically scans your repository's `.tekton` folder (and any folders inside it) for files ending in `.yaml` or `.yml`. It's on the lookout for Tekton goodies like `Pipeline`, `PipelineRun`, and `Task` definitions.

When it spots a `PipelineRun`, it gets to work "resolving" it.  What does that mean?  Basically, it takes your `PipelineRun` and bundles up everything it needs to run – like the actual steps defined in your `Pipeline` or `Task` – right inside the `PipelineRun` itself.  This "embedding" thing is super handy because it makes sure your PipelineRun is self-contained and has all its dependencies ready to go when it runs on your cluster.  Think of it as packing everything for a trip in one suitcase!

{{< hint info >}}
Just a heads-up: the resolver we're talking about here is different from the standard Tekton resolver. But don't worry, they play nice together! You can even use the Tekton resolver *inside* your Pipelines-as-Code PipelineRuns if you want.
{{< /hint >}}

Now, if Pipelines-as-Code finds any mistakes in your YAML files (typos, wrong formatting, you name it), it'll stop reading files right away and let you know. You'll see error messages in your Git provider (like GitHub or GitLab) and also in the event stream of your Kubernetes namespace.  No silent failures here!

To keep things tidy and prevent naming clashes, the resolver does a little magic. It changes the `Name` of your Pipeline to a `GenerateName`. This basically means each time you run a Pipeline, it gets a unique, auto-generated name.  Neat, huh?

Want to keep your `Pipeline` and `PipelineRun` definitions in separate files? No problem! Just pop them in the `.tekton/` folder (or any subfolders within). And yes, you can totally reference `Pipeline` and `Task` definitions that live somewhere else too (we'll get to remote stuff in a bit).

Okay, now for the exceptions. There are a few types of tasks that the resolver will just leave as they are, without trying to resolve them:

* **`ClusterTask` references:** If you're using a `ClusterTask` (a task that's available cluster-wide), it's used directly.
* **`Task` or `Pipeline` Bundles:**  Bundles are already packaged up, so no need to resolve them further.
* **Tekton `Resolver` references:** If you're already using Tekton's own resolvers, Pipelines-as-Code respects that and doesn't interfere.
* **Custom Tasks (with non-Tekton API versions):** If you're using custom tasks that aren't part of the standard Tekton API, they're also left untouched.

Basically, if it's already "resolved" or handled by another mechanism, Pipelines-as-Code steps back and lets it be.

Now, what if Pipelines-as-Code *can't* find a Task that your `Pipeline` is trying to use? In that case, the PipelineRun will fail *before* it even gets submitted to your cluster.  You'll get notified about the problem on your Git platform and in the events of the namespace where your `Repository` resource lives.  So, you'll know right away if something's missing.

Thinking of testing your `PipelineRun` before committing? Smart move! You can use the `resolve` command in the `tkn-pac` command-line tool.  It lets you check things out locally.  Head over to the [CLI documentation](./cli/#resolve) to learn how to use it.

## Remote Tasks: Pulling in Tasks from Everywhere

Pipelines-as-Code is pretty cool – it can even grab Tasks and Pipelines from remote places! You just need to use annotations in your `PipelineRun`.  It's like saying "Hey, go fetch this Task from over there!"

If the resolver sees a `PipelineRun` that refers to a remote Task or Pipeline (either directly in the `PipelineRun` or inside an embedded `PipelineSpec`), it will automatically pull them in and include them.

If you happen to have multiple annotations pointing to the same Task name, the resolver will just use the first one it finds.

Here's how you can annotate your `PipelineRun` to use a remote Task:

```yaml
pipelinesascode.tekton.dev/task: "git-clone"
```

Or, if you want to fetch multiple remote tasks at once, you can list them like this:

```yaml
pipelinesascode.tekton.dev/task: "[git-clone, pylint]"
```

### Tekton Hub: Your Task Supermarket

```yaml
pipelinesascode.tekton.dev/task: "git-clone"
```

That simple annotation above? It tells Pipelines-as-Code to grab the `git-clone` Task from the [Tekton Hub](https://hub.tekton.dev).  It'll automatically find the latest version and pull it in.  Tekton Hub is like a public library of reusable Tasks – pretty neat!

You can grab multiple Tasks from the Hub in one go, just separate them with commas, or use that bracket syntax like this:

```yaml
pipelinesascode.tekton.dev/task: "[git-clone, golang-test, tkn]"
```

Or, if you prefer, you can list them on separate lines using `-NUMBER` suffixes:

```yaml
  pipelinesascode.tekton.dev/task: "git-clone"
  pipelinesascode.tekton.dev/task-1: "golang-test"
  pipelinesascode.tekton.dev/task-2: "tkn"
```

By default, Pipelines-as-Code assumes you want the `latest` version from the Tekton Hub.

Want a specific version? No problem! Just add a colon `:` and the version number to the Task name, like this:

```yaml
pipelinesascode.tekton.dev/task: "[git-clone:0.1]" #  Get version 0.1 of git-clone from Tekton Hub
```

#### Custom Tekton Hub: Your Private Collection

Maybe your organization has its own Tekton Hub setup? If your cluster admin has configured a custom Hub (see [settings documentation](/docs/install/settings#tekton-hub-support)), you can point to it like this:

```yaml
pipelinesascode.tekton.dev/task: "[anothercatalog://curl]" # Get curl from your custom catalog named "anothercatalog"
```

Important note: if you use a custom Hub, Pipelines-as-Code won't fall back to the default Tekton Hub if it can't find the Task in your custom one.  It'll just fail.

Also, the `tkn pac resolve` command in the CLI doesn't currently support custom Hubs. Just something to keep in mind!

### Remote HTTP URLs: Tasks from Any Web Address

Got a Task definition hosted on a website somewhere?  Pipelines-as-Code can grab it directly from an HTTP or HTTPS URL:

```yaml
  pipelinesascode.tekton.dev/task: "[https://remote.url/task.yaml]"
```

### Private Repository Access: Securely Fetching Tasks

If you're using GitHub or GitLab and your remote Task URL points to a repository on the *same* host as your Repository CRD, Pipelines-as-Code can use the provided token to securely fetch the Task using the Git provider's API.  This is super handy for accessing Tasks in private repositories!

#### GitHub: Private Tasks Made Easy

Let's say your repository is on GitHub, like this:

`<https://github.com/organization/repository>`

And your remote Task URL is a GitHub "blob" URL, like this:

`<https://github.com/organization/repository/blob/mainbranch/path/file>`

If your branch name has a slash (like `feature/branch`), you'll need to use HTML encoding (`%2F`) for the slash:

`<https://github.com/organization/repository/blob/feature%2Fmainbranch/path/file>`

Pipelines-as-Code will use the GitHub API and your token to fetch that file securely.  Boom! Private repository access handled.

GitHub App tokens are scoped to the organization where the repository is. If you're using the GitHub webhook method, you can fetch Tasks from any private or public repository that your personal token has access to.

You can tweak this behavior with settings in the Pipelines-as-Code ConfigMap, specifically `secret-github-app-token-scoped` and `secret-github-app-scope-extra-repos`. Check out the [settings docs](/docs/install/settings) for the details.

#### GitLab: Private Tasks on GitLab Too

The same trick works for GitLab!  If you have a GitLab URL copied from the UI like this:

`<https://gitlab.com/organization/repository/-/blob/mainbranch/path/file>`

or a GitLab raw URL like this:

`<https://gitlab.com/organization/repository/-/raw/mainbranch/path/file>`

Pipelines-as-Code will use the GitLab token you provided in your Repository CR to fetch the file securely.

### Tasks and Pipelines Inside Your Own Repo: Keeping Things Local

You can also reference Tasks or Pipelines that are defined in YAML files *within* your own repository. Just use a relative path to the file:

```yaml
pipelinesascode.tekton.dev/task: "[share/tasks/git-clone.yaml]"
```

This will grab the `git-clone.yaml` file from the `share/tasks` folder in your repository, using the specific commit (SHA) related to the event (like your pull request or branch push).

If anything goes wrong while fetching these remote resources, Pipelines-as-Code will throw an error and stop processing the Pipeline. And if the file it fetches isn't a valid Tekton `Task`, it'll also let you know.

## Remote Pipelines: Sharing Pipelines Across Projects

Want to share a Pipeline across multiple repositories? Remote Pipelines are your answer! You can reference a Pipeline by annotation in your `PipelineRun`.

You can only have *one* remote Pipeline annotation (`pipelinesascode.tekton.dev/pipeline`) on a `PipelineRun`, and it should point to a single Pipeline definition.  Annotations like `pipelinesascode.tekton.dev/pipeline-1` aren't supported for Pipelines.

Here's how to use a remote Pipeline from a URL:

```yaml
pipelinesascode.tekton.dev/pipeline: "https://git.provider/raw/pipeline.yaml"
```

Or from a relative path within your repository:

```yaml
pipelinesascode.tekton.dev/pipeline: "./tasks/pipeline.yaml"
```

### Tekton Hub Pipelines: Pipelines from the Hub

```yaml
pipelinesascode.tekton.dev/pipeline: "[buildpacks]"
```

Just like with Tasks, you can fetch Pipelines from the Tekton Hub!  The example above grabs the `buildpacks` Pipeline from the [Tekton Hub](https://hub.tekton.dev), getting the latest version.

To specify a version, use the colon `:` syntax:

```yaml
pipelinesascode.tekton.dev/pipeline: "[buildpacks:0.1]" # Get version 0.1 of buildpacks Pipeline from Tekton Hub
```

#### Custom Tekton Hub Pipelines: Your Private Pipeline Collection

And yes, you can also use Pipelines from a custom Tekton Hub if your cluster admin has set one up:

```yaml
pipelinesascode.tekton.dev/pipeline: "[anothercatalog://buildpacks:0.1]" # Get buildpacks Pipeline from your custom catalog "anothercatalog"
```

### Overriding Tasks in Remote Pipelines: Customizing Shared Pipelines

{{< tech_preview "Tasks from a remote Pipeline override" >}}

Good news! Remote Task annotations are supported in remote Pipelines too. However, other annotations like `on-target-branch`, `on-event`, or `on-cel-expression` are not supported on remote pipelines.

Want to tweak a Task from a remote Pipeline? You can! Just add a Task with the same name in the annotations of your `PipelineRun`.  Pipelines-as-Code will use your version instead of the one from the remote Pipeline.

For example, say your `PipelineRun` looks like this:

```yaml
kind: PipelineRun
metadata:
  annotations:
    pipelinesascode.tekton.dev/pipeline: "https://git.provider/raw/pipeline.yaml"
    pipelinesascode.tekton.dev/task: "./my-git-clone-task.yaml"
```

And the Pipeline at `<https://git.provider/raw/pipeline.yaml>` has this annotation:

```yaml
kind: Pipeline
metadata:
  annotations:
    pipelinesascode.tekton.dev/task: "git-clone"
```

If `my-git-clone-task.yaml` in your repository root defines a Task named `git-clone`, Pipelines-as-Code will use *your* `git-clone` Task instead of the one from the remote Pipeline (which might be the `git-clone` Task from Tekton Hub, for example).

{{< hint info >}}
Task overriding only works for Tasks referenced by `taskRef` with a `Name`. It doesn't work for Tasks embedded directly using `taskSpec`. Check out the [Tekton documentation](https://tekton.dev/docs/pipelines/pipelines/#adding-tasks-to-the-pipeline) to understand the difference between `taskRef` and `taskSpec`.
{{< /hint >}}

### Task and Pipeline Precedence: Where Does Pipelines-as-Code Look First?

So, when you have Tasks or Pipelines with the same name, which one does Pipelines-as-Code choose?  Here's the order of preference:

For Tasks (when using `taskRef` with a name):

1. **Tasks from `PipelineRun` annotations:**  Tasks defined in your `PipelineRun` annotations take top priority.
2. **Tasks from remote `Pipeline` annotations:** If the Task isn't in the `PipelineRun` annotations, Pipelines-as-Code checks the annotations of the remote Pipeline (if you're using one).
3. **Tasks from the `.tekton` directory:** Finally, if it still hasn't found the Task, it looks in your repository's `.tekton` folder and its subfolders.

For Pipelines (when using `pipelineRef`):

1. **Pipelines from `PipelineRun` annotations:** Pipelines defined in your `PipelineRun` annotations are checked first.
2. **Pipelines from the `.tekton` directory:** If no Pipeline is specified in the annotations, Pipelines-as-Code looks in your repository's `.tekton` folder and subfolders.

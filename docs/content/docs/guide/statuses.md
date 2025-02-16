---
title: PipelineRun status
weight: 6
---

# Checking in on your PipelineRun Status

So, your `PipelineRun` is off and running – great!  Let's talk about how you can see what's happening and if everything's going smoothly.

## GitHub Apps: Your Pipeline's Report Card

Once your `PipelineRun` wraps up, you can get a quick rundown right in GitHub.  Look for the "Check" tabs – that's where Pipelines-as-Code will post a summary.  You'll see:

*   The overall status of the `PipelineRun` (success or failure, naturally!).
*   A list of each task in your pipeline, along with its name and how long it took.  If you've given a task a fancy `displayName` in your Tekton setup, that's what you'll see here. Otherwise, it'll just use the plain task name.

And if something goes wrong?  No worries, GitHub will give you a heads-up.  If a step in your pipeline fails, you’ll even get a little snippet of the logs right there in the GitHub output, which can be super helpful for figuring out what went sideways.

**Cluster Hiccups? GitHub's Got Your Back**

Sometimes, things can go wrong even *before* your pipeline really gets going – maybe there's an issue creating the `PipelineRun` on your Kubernetes cluster.  If that happens, Pipelines-as-Code will pipe the error message from the Pipeline Controller straight to GitHub.  This means you can quickly spot and fix problems without digging around in your cluster logs.  Nice, right?

Any other errors that pop up during the pipeline's execution? Yep, those get reported to GitHub too.  However, if Pipelines-as-Code can't find a matching namespace for your repository (which shouldn't happen often!), those errors will end up in the Pipelines-as-Code Controller's logs instead.

## What About Other Providers (Webhooks, etc.)?

If you're using webhooks for your events, things work a little differently.

*   **Pull Requests? You'll get a comment!**  For pull request events, Pipelines-as-Code is helpful and will post the status as a comment directly on your pull request.  Easy peasy.
*   **Push Events?  A bit trickier.** When it's a push event, there isn't really a dedicated spot in GitHub to show the status in the same way as checks or PR comments. So, for push events, you'll need to use some of the other methods we're talking about here to keep an eye on things.

##  Log Snippets:  Errors in a Nutshell (and Secret-Safe!)

When a task in your pipeline hits a snag, Pipelines-as-Code is helpful and gives you a little taste of what went wrong with a snippet of the logs.  Specifically, it grabs the last three lines from the task that failed.

**Secret Agent Mode: Protecting Your Sensitive Info**

We know you don't want secrets leaking out, so Pipelines-as-Code has a built-in secret-hiding feature. It's pretty clever:

1.  It looks at all the secrets defined as environment variables in your tasks and steps.
2.  It then scans the log snippet for those secret values.
3.  Any matches it finds are replaced with `"*****"` before the snippet is displayed.

This way, you get the error info you need without accidentally exposing passwords or API keys.

**Important Note:**  This secret-hiding magic *doesn't* currently cover secrets from `workspaces` or `envFrom` sources.  Just something to keep in mind!

![Log snippet example](/images/snippet-failure-message.png)

### Error Hunting in Container Logs: GitHub Annotations to the Rescue

Want even *more* error detail right in GitHub?  You can turn on the `error-detection-from-container-logs` option in the Pipelines-as-Code settings.  When you do, Pipelines-as-Code will try to sniff out errors directly from your container logs and flag them as annotations on the pull request where they happened.  Think of it like little error notes attached to your code in GitHub.

**What kind of errors can it find?** Right now, it's looking for errors that are in a simple, standard format – kind of like what you'd see from `makefile` or `grep`.  Specifically, it likes this format:

```console
filename:line:column: error message
```

Good news is, lots of tools already output errors in this format!  Think `golangci-lint`, `pylint`, `yamllint`, and many others.

**Want to see it in action?** Check out the Pipelines-as-Code [pull\_request.yaml](https://github.com/openshift-pipelines/pipelines-as-code/blob/7c9b16409a1a6c93e9480758f069f881e5a50f05/.tekton/pull-request.yaml#L70) file.  That's how the Pipelines-as-Code project itself uses linting and shows errors in this format.

**Customize Error Detection (For the Power Users)**

If you're feeling fancy, you can even tweak *how* Pipelines-as-Code detects errors.  The `error-detection-simple-regexp` setting lets you define a custom regular expression.  Don't worry, you don't have to be a regex wizard!  It uses [named groups](https://www.regular-expressions.info/named.html) to keep things flexible.  You just need to make sure your regex has groups named `filename`, `line`, and `error` (it ignores the `column` group, if you were wondering).  The default regex is already set up in the configuration, so you probably won't need to change it unless you have very specific needs.

**Log Line Limits**

By default, Pipelines-as-Code only scans the *last* 50 lines of container logs for errors.  Why? To keep things speedy and not use too much memory.  But if you need it to look further back, you can adjust this with the `error-detection-max-number-of-lines` setting.  Setting it to `-1` tells it to search through *all* the log lines.  Just be aware that scanning more lines might mean it uses a bit more memory.

![Example of GitHub annotations](/images/github-annotation-error-failure-detection.png)

## Namespace Event Stream:  Logs in Your Namespace

When you link a namespace to a repository, Pipelines-as-Code does a cool thing: it starts sending its log messages as Kubernetes events right inside that repository's namespace.  This can be handy if you're already used to monitoring Kubernetes events for insights.

## Repository CRD:  PipelineRun History

Pipelines-as-Code also keeps a little history for you! It remembers the five most recent statuses of any `PipelineRuns` associated with your repository and stores them in the repository's custom resource (CR).

You can see this history by running:

```console
% kubectl get repo -n pipelines-as-code-ci
NAME                  URL                                                        NAMESPACE             SUCCEEDED   REASON      STARTTIME   COMPLETIONTIME
pipelines-as-code-ci   https://github.com/openshift-pipelines/pipelines-as-code   pipelines-as-code-ci   True        Succeeded   59m         56m
```

Even easier, you can use the `tkn pac describe` command from the Pipelines-as-Code CLI (check out the [cli docs](../cli/)) to get a full view of all the `PipelineRun` statuses and metadata for your repository.  Super convenient!

## Notifications:  You're in Charge!

Pipelines-as-Code itself *doesn't* handle notifications directly.  But don't worry, you've got options!

If you want to get alerts or notifications about your pipeline runs, the best way is to use Tekton Pipelines' [finally tasks](https://github.com/tektoncd/pipeline/blob/main/docs/pipelines.md#adding-finally-to-the-pipeline).  `finally` tasks let you define a set of tasks that will always run at the very end of your pipeline, no matter if it succeeded or failed.

**Example Time!**  Take a look at the Pipelines-as-Code project's own [coverage generation pipeline](https://github.com/openshift-pipelines/pipelines-as-code/blob/16596b478f4bce202f9f69de9a4b5a7ca92962c1/.tekton/generate-coverage-release.yaml#L127).  It uses a `finally` task with the [guard feature](https://tekton.dev/docs/pipelines/pipelines/#guard-finally-task-execution-using-when-expressions) to send a Slack notification if anything goes wrong during the pipeline run.

You can see it in action right here:

<https://github.com/openshift-pipelines/pipelines-as-code/blob/16596b478f4bce202f9f69de9a4b5a7ca92962c1/.tekton/generate-coverage-release.yaml#L126>

So, while Pipelines-as-Code doesn't do notifications for you, it gives you the tools to easily set them up yourself using Tekton's powerful features!

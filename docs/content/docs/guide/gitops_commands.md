---
title: GitOps Commands
weight: 5.1
---
# GitOps Commands: Control Your Pipelines with Comments!

Pipelines as Code has this cool feature called "GitOps commands."  Basically, you can tell Pipelines as Code what to do just by typing special commands in comments on your Pull Requests or even in your commit messages.

Why is this neat?  Well, it keeps a record of everything you've done with your pipelines right there in your Pull Request, next to your code changes.  It's like a pipeline diary!

## GitOps Commands in Pull Requests

Let's say you're looking at a Pull Request and something's acting up. Maybe you want to kick off all the pipelines again.  Easy peasy! Just drop a comment saying `/retest` in the PR. Pipelines as Code will see that and restart all the pipelines linked to that PR.

Here's an example of how you might use it:

```text
Hey team, thanks for the review! This should fix that pesky bug.  Looks like a test failed, but it might just be a hiccup on our end.

/retest
```

If you have a bunch of pipelines running and you only want to restart *one* of them, you can use `/test` followed by the pipeline's name.  Like this:

```text
Alright, tests are running, but "pipeline-build-image" seems stuck. Let's give it a nudge.

/test pipeline-build-image
```

{{< hint info >}}

Quick heads-up:  These GitOps commands, like `/test` and others, won't work on Pull Requests that are already closed or merged.  Gotta catch 'em while they're open!

{{< /hint >}}

## GitOps Commands in Commit Messages

You can also use GitOps commands when you push commits! Just stick the commands right into your commit messages. This is handy if you want to trigger a pipeline action right as you're pushing code. You can restart all pipelines or just specific ones this way too.

To restart *all* pipelines:

1.  Include `/retest` or `/test` in your commit message.

To restart a *specific* pipeline:
2.  Use `/retest <pipelinerun-name>` or `/test <pipelinerun-name>` in your commit message.  Just replace `<pipelinerun-name>` with the actual name of the pipeline you want to restart.

Keep in mind, these commands only work on the very latest commit (HEAD) of your branch.  No going back in time, Marty!

**Important Note:**

If you use GitOps commands on a commit that's in multiple branches when you push, Pipelines as Code will only look at the branch that was updated most recently.

What does this mean in practice?

1. If you just use `/retest` or `/test` (or `/retest <pipelinerun-name>` or `/test <pipelinerun-name>`) in a comment, Pipelines as Code assumes you mean the **default branch** of your repo (like `main` or `master`).

   Examples:
   1. `/retest`
   2. `/test`
   3. `/retest my-pipeline`
   4. `/test my-pipeline`

2. If you want to be specific and target a different branch, you can say something like `/retest branch:testing` or `/test branch:feature-x`. This tells Pipelines as Code to run the command in the context of the `testing` or `feature-x` branch.

   Examples:
   1. `/retest branch:testing`
   2. `/test branch:testing`
   3. `/retest my-pipeline branch:testing`
   4. `/test my-pipeline branch:testing`

Want to add a GitOps command to a commit? Here's how:

1. Head over to your repository.
2. Click on the "Commits" section.
3. Pick the commit you're interested in.
4. Click on the line number where you want to add your GitOps command, just like in this picture:

![GitOps Commits For Comments](/images/gitops-comments-on-commit.png)

Just a heads up:  This commit comment feature is only available for GitHub right now.

## Restarting Pipelines Even if They Don't Match

Here's a cool trick:  Even if a pipeline *isn't* normally triggered by comments, you can still restart it using `/test <pipelinerun-name>` or `/retest <pipelinerun-name>`. This gives you extra control, especially for pipelines that only run when you manually trigger them with a comment in the first place.

## Accessing the Comment That Triggered a Pipeline

When a GitOps command comment starts a pipeline, Pipelines as Code saves the actual comment text in a variable called `{{ trigger_comment }}`.

This is super useful! You can then use this comment text in your pipeline steps.  For example, you could write a script that looks for specific words in the comment and does different things based on what it finds.

One little detail about `{{ trigger_comment }}`:  Pipelines as Code changes any line breaks in your comment to `\n`. This is because multi-line comments can sometimes cause problems in YAML files.

If you need those line breaks back (like in a shell script), you can easily convert `\n` back to a real newline.  For example, in a shell script, `echo -e` does the trick.

Here's a quick example shell script:

```shell
echo -e "{{ trigger_comment }}" > /tmp/comment
grep "important keyword" /tmp/comment
```

## Custom GitOps Commands - Make Your Own!

Want to get even fancier?  You can create your *own* GitOps commands! By using the `[on-comment]({{< relref "/docs/guide/matchingevents.md#matching-a-pipelinerun-on-a-regex-in-a-comment" >}})` annotation in your `PipelineRun`, you can define custom commands that trigger pipelines when you comment on a Pull Request.

Check out the [on-comment]({{< relref "/docs/guide/matchingevents.md#matching-a-pipelinerun-on-a-regex-in-a-comment" >}}) guide for all the details on how to set this up.

For a real-world example, take a peek at how Pipelines as Code itself uses custom commands in its own repo.  Look at the `on-comment` annotation in this file:

<https://github.com/openshift-pipelines/pipelines-as-code/blob/main/.tekton/prow.yaml>

## Cancelling a Pipeline Run - Stop That Train!

Need to stop a pipeline that's running?  You can cancel it by commenting on the Pull Request!

To cancel *all* pipelines linked to a PR, just comment `/cancel`. Pipelines as Code will get the message and stop 'em all.

Example:

```text
Oops, pushed too soon!  Need to make a quick fix. Cancelling current pipelines.

/cancel
```

To cancel just *one* specific pipeline, use `/cancel` followed by the pipeline name:

```text
Hmm, "pipeline-integration-tests" is taking forever and I think I know why. Let's stop it.

/cancel pipeline-integration-tests
```

In the GitHub App, the pipeline status will change to "cancelled."

![PipelineRun Canceled](/images/pr-cancel.png)

### Cancelling Pipelines on Push Requests

You can also cancel pipelines from commit comments when you push code.  Same commands as above apply:

Example:

1. Use `/cancel` to cancel all pipelines.
2. Use `/cancel <pipelinerun-name>` to cancel a specific pipeline.

**Important Note:**

Just like with restarting, when you use GitOps commands to cancel on a commit that's in multiple branches, Pipelines as Code will focus on the branch with the latest commit.

So:

1. If you use `/cancel` (or `/cancel <pipelinerun-name>`) in a comment, it's assumed to be for the **main** branch.

   Examples:
   1. `/cancel`
   2. `/cancel my-pipeline`

2. If you want to target a different branch, use `/cancel branch:testing` (or `/cancel <pipelinerun-name> branch:testing`). This will cancel the pipeline in the context of the `testing` branch.

   Examples:
   1. `/cancel branch:testing`
   2. `/cancel my-pipeline branch:testing`

The GitHub App will show the pipeline status as "cancelled."

![GitOps Commits For Comments For PipelineRun Canceled](/images/gitops-comments-on-commit-cancel.png)

Remember, commit comments for cancelling are also only supported on GitHub for now.

## Passing Parameters to GitOps Commands - Extra Control!

{{< tech_preview "Passing parameters to GitOps commands as arguments" >}}

Want even *more* control? You can pass extra bits of info – parameters – along with your GitOps commands! This lets you tweak those [standard dynamic variables]({{< relref "/docs/guide/authoringprs#dynamic-variables" >}}) or your [custom parameters]({{< relref "/docs/guide/customparams" >}}).

For example, you could do:

```text
/test my-pipeline image_tag=latest
```

This would set a custom parameter named `image_tag` (if you've defined it as a custom parameter) to the value `latest`.

If your comment *doesn't* start with a `/`, Pipelines as Code will ignore it – gotta start with that slash!

You can only change parameters that are already defined as either standard or custom parameters. You can't just invent new ones on the fly.

You can put these `key=value` pairs anywhere in your comment, and Pipelines as Code will find them.

You can use a few different formats for your values, which is handy if you need spaces or even line breaks in your parameter values:

* `key=value` (simple value)
* `key="a value with spaces"` (value with spaces, in quotes)
* `key="a value with \"escaped quotes\""` (value with escaped quotes)
* `key="a value with
  a newline"` (value with a newline, in quotes)

## Event Type Annotation - Knowing What Triggered It

Pipelines as Code adds an annotation called `pipeline.tekton.dev/event-type` to your PipelineRuns. This annotation tells you *which* GitOps command triggered the pipeline.

Here's a list of the possible event types you might see:

* `test-all-comment`: Triggered by a simple `/test` (tests all matching pipelines).
* `test-comment`: Triggered by `/test <PipelineRun>` (tests a specific pipeline).
* `retest-all-comment`: Triggered by `/retest` (retests all matching pipelines).
* `retest-comment`: Triggered by `/retest <PipelineRun>` (retests a specific pipeline).
* `on-comment`: Triggered by a custom comment command.
* `cancel-all-comment`: Triggered by `/cancel` (cancels all matching pipelines).
* `cancel-comment`: Triggered by `/cancel <PipelineRun>` (cancels a specific pipeline).
* `ok-to-test-comment`: Triggered by `/ok-to-test` (allows CI to run for contributors who aren't repo members).

When a repo owner uses `/ok-to-test` on a PR from someone who's not a repo member, and there's no `pull_request` pipeline defined in `.tekton`, Pipelines as Code will create a neutral check-run status on GitHub.  This just means "no pipeline matched, but that's okay." It prevents things like auto-merge workflows from getting stuck.

{{< hint info >}}

Just a note: This neutral check-run status for `/ok-to-test` is currently only a GitHub feature.

{{< /hint >}}

When you use the `{{ event_type }}` [dynamic variable]({{< relref "/docs/guide/authoringprs.md#dynamic-variables" >}}) with these event types:

* `test-all-comment`
* `test-comment`
* `retest-all-comment`
* `retest-comment`
* `cancel-all-comment`
* `ok-to-test-comment`

...it will actually return `pull_request` as the event type.  This is for compatibility with older versions of Pipelines as Code, so things don't break for people who are already using `{{ event_type }}`.

This is just a temporary thing, though.  We're currently showing a warning about this, and eventually, `{{ event_type }}` will return the *specific* GitOps command event type instead of just `pull_request`.  Just something to keep in mind!

---
title: Hooking up PipelineRuns to Git Events
weight: 3
---

# Making Your Pipelines React to Git Events

Want your `PipelineRuns` to kick off automatically when something cool happens in your Git repo?  Like, say, when someone opens a pull request or pushes code? You got it! Pipelines as Code lets you do just that by using special annotations in your `PipelineRun`'s metadata. Think of it as setting up triggers for your pipelines.

For example, imagine you want a pipeline to run whenever there's a pull request targeting your `main` branch.  You'd set up your `PipelineRun` like this:

```yaml
metadata:
  name: pipeline-pr-main # A descriptive name for your pipeline
annotations:
  pipelinesascode.tekton.dev/on-target-branch: "[main]" # Run on the 'main' branch
  pipelinesascode.tekton.dev/on-event: "[pull_request]" # Triggered by pull requests
```

With this setup, Pipelines as Code is smart enough to watch for Git events. If a pull request comes in and it's aimed at the `main` branch, bam!  Your `pipeline-pr-main` will spring into action.

You can even specify multiple branches to watch.  Just list them out, separated by commas, like so:

```yaml
pipelinesascode.tekton.dev/on-target-branch: [main, release-nightly] # Watch both 'main' and 'release-nightly'
```

Pull requests aren't the only thing you can react to. You can also trigger pipelines on `push` events.  Let's say you want a pipeline to run every time someone pushes a commit to the `main` branch. Here's how you'd configure it:

```yaml
metadata:
  name: pipeline-push-on-main # Pipeline for pushes to main
  annotations:
    pipelinesascode.tekton.dev/on-target-branch: "[refs/heads/main]" # Specifically 'refs/heads/main' branch
    pipelinesascode.tekton.dev/on-event: "[push]" # Triggered by pushes
```

Notice you can use the full branch ref like `refs/heads/main` or just the short name like `main`.  And guess what? You can even use globs!  For example, `refs/heads/*` will catch any branch, and `refs/tags/1.*` will match any tag starting with `1.`.

Here's a cool example for when you push a tag, like version `1.0`:

```yaml
metadata:
name: pipeline-push-on-1.0-tags # Pipeline for pushing 1.0 tags
annotations:
  pipelinesascode.tekton.dev/on-target-branch: "[refs/tags/1.0]" # Triggered by tags like 'refs/tags/1.0'
  pipelinesascode.tekton.dev/on-event: "[push]" # On push events
```

This `pipeline-push-on-1.0-tags` pipeline will kick off the moment you push a tag that matches `1.0` to your repo. Neat, huh?

Heads up:  You need to have these matching annotations in place. If they're not there, Pipelines as Code won't automatically run your `PipelineRun`.

If more than one `PipelineRun` matches a Git event, don't worry, Pipelines as Code is smart enough to run them all at the same time! You'll get results posted back to your Git provider as each one finishes.

{{< hint info >}}
Just so you know, Pipelines as Code only pays attention to Git events it understands, like when a pull request is opened or updated, or when someone pushes to a branch.  Other types of Git events won't trigger these pipelines.
{{< /hint >}}

## Only Run Pipelines When Specific Files Change

{{< tech_preview "Matching a PipelineRun to specific path changes via annotation" >}}

Want even more control? You can tell your `PipelineRun` to only run if *specific files* are changed in a Git event.  That's where the `pipelinesascode.tekton.dev/on-path-change` annotation comes in.

You can list multiple paths, separated by commas.  The first path pattern that matches the changed files in a pull request will trigger the `PipelineRun`.  If you have a comma in your file path and it's messing things up, you can use `&#44;` instead of a comma – it's like a secret code for commas!

Remember, you still need to specify the event type (`on-event`) and target branch (`on-target-branch`).  If you're using a fancy [CEL expression](#matching-pipelinerun-by-path-change) (we'll get to that later!), the `on-path-change` annotation will be ignored.

Here's an example:

```yaml
metadata:
  name: pipeline-docs-and-manual # Pipeline for docs and manual changes
  annotations:
    pipelinesascode.tekton.dev/on-target-branch: "[main]" # Target branch is 'main'
    pipelinesascode.tekton.dev/on-event: "[pull_request]" # On pull requests
    pipelinesascode.tekton.dev/on-path-change: "[docs/**.md, manual/**.rst]" # Only if .md files in 'docs' or .rst in 'manual' change
```

This `pipeline-docs-and-manual` pipeline will only fire up when a pull request targets the `main` branch *and* includes changes to Markdown files (`.md`) in the `docs` directory (or any subfolders within it), *or* reStructuredText files (`.rst`) in the `manual` directory. Pretty specific, right?

{{< hint info >}}
Just a heads-up: these path patterns are [glob](https://en.wikipedia.org/wiki/Glob_(programming)) patterns, not regular expressions.  Think of them as simplified wildcards.  Need some examples?  Check out [this page](https://github.com/gobwas/glob?tab=readme-ov-file#example) from the library we use for matching.

Want to test your glob patterns? The `tkn pac` CLI has a handy [command]({{< relref "/docs/guide/cli.md#test-globbing-pattern" >}}) just for that:

```bash
tkn pac info globbing "[PATTERN]" # Test your glob pattern
```

This command will see if `[PATTERN]` matches any files in your current directory. Super useful for debugging!

{{< /hint >}}

### Ignoring Path Changes for Pipeline Triggers

{{< tech_preview "Matching a PipelineRun to ignore specific path changes via annotation" >}}

Following the same idea, you can use `pipelinesascode.tekton.dev/on-path-change-ignore`. This is like the reverse of `on-path-change` – it triggers a `PipelineRun` when changes happen *outside* of the paths you specify.

You still need `on-event` and `on-target-branch`. And just like before, if you're using a [CEL expression](#matching-pipelinerun-by-path-change), `on-path-change-ignore` is ignored.

Here’s a pipeline that runs when changes are made *outside* the `docs` folder:

```yaml
metadata:
  name: pipeline-not-on-docs-change # Pipeline that runs when docs *aren't* changed
  annotations:
    pipelinesascode.tekton.dev/on-target-branch: "[main]" # Target: main branch
    pipelinesascode.tekton.dev/on-event: "[pull_request]" # On pull requests
    pipelinesascode.tekton.dev/on-path-change-ignore: "[docs/***]" # Ignore changes in 'docs' directory
```

You can even use `on-path-change` and `on-path-change-ignore` together!

```yaml
metadata:
  name: pipeline-docs-not-generated # Pipeline for docs, but not generated docs
  annotations:
    pipelinesascode.tekton.dev/on-target-branch: "[main]" # Target: main branch
    pipelinesascode.tekton.dev/on-event: "[pull_request]" # On pull requests
    pipelinesascode.tekton.dev/on-path-change: "[docs/***]" # Run if there are changes in 'docs'
    pipelinesascode.tekton.dev/on-path-change-ignore: "[docs/generated/***]" # ...but *not* in 'docs/generated'
```

This setup will trigger `pipeline-docs-not-generated` when there are changes in the `docs` directory, but *only if* those changes are *not* in the `docs/generated` subdirectory.  Clever, right?

Keep in mind: `on-path-change-ignore` always wins over `on-path-change`.  So, if you have this:

```yaml
metadata:
  name: pipelinerun-go-only-no-markdown-or-yaml # Pipeline for Go changes, but no markdown or YAML
    pipelinesascode.tekton.dev/on-target-branch: "[main]" # Target: main branch
    pipelinesascode.tekton.dev/on-event: "[pull_request]" # On pull requests
    pipelinesascode.tekton.dev/on-path-change: "[***.go]" # Run if Go files change
    pipelinesascode.tekton.dev/on-path-change-ignore: "[***.md, ***.yaml]" # ...but ignore markdown and YAML changes
```

And you have a pull request that changes `.tekton/pipelinerun.yaml`, `README.md`, and `main.go`, the `PipelineRun` will *not* run.  Why? Because `on-path-change-ignore` is telling it to ignore `.md` and `.yaml` files, even though `on-path-change` says to run for `.go` files.  `Ignore` takes priority!

## Trigger Pipelines with Comments Using Regex

{{< tech_preview "Matching PipelineRun on regex in comments" >}}
{{< support_matrix github_app="true" github_webhook="true" gitea="true" gitlab="true" bitbucket_cloud="false" bitbucket_server="false" >}}

Want to trigger pipelines just by leaving a comment on a pull request or a [pushed commit]({{< relref "/docs/guide/running.md#gitops-commands-on-pushed-commits">}})?  You can do that with the `pipelinesascode.tekton.dev/on-comment` annotation!

The comment you specify is treated as a regular expression (regex).  Don't worry about extra spaces or newlines at the beginning or end of your comment – they're automatically trimmed before matching. So, `^` matches the very start of the comment, and `$` matches the very end, without any extra whitespace.

If a new comment on a pull request matches your regex, boom, the `PipelineRun` gets triggered and starts running.  Important: this only works for *new* comments.  Editing or updating existing comments won't kick off a pipeline.

Example time!

```yaml
---
apiVersion: tekton.dev/v1beta1
kind: PipelineRun
metadata:
  name: "merge-pr" # Pipeline to merge a PR
  annotations:
    pipelinesascode.tekton.dev/on-comment: "^/merge-pr" # Triggered by comments starting with "/merge-pr"
```

This `merge-pr` pipeline will start whenever someone adds a comment to a pull request that *starts* with `/merge-pr`.  Handy for GitOps workflows!

When a `PipelineRun` is triggered by `on-comment`, a special variable called `{{ trigger_comment }}` is set. You can find out more about this in the [docs]({{< relref "/docs/guide/gitops_commands.md#accessing-the-comment-triggering-the-pipelinerun" >}}).

Also, keep in mind that `on-comment` respects the pull request [Policy]({{< relref "/docs/guide/policy" >}}) rules. Only users allowed by your policy can trigger a `PipelineRun` with comments.

{{< hint info >}}
The `on-comment` thing works for pull request events. For push events, it's only supported [when targeting the main branch without any extra arguments]({{< relref "/docs/guide/gitops_commands.md#gitops-commands-on-pushed-commits" >}}).
{{< /hint >}}

## Matching Pipelines to Pull Request Labels

{{< tech_preview "Matching PipelineRun to a Pull-Request label" >}}
{{< support_matrix github_app="true" github_webhook="true" gitea="true" gitlab="true" bitbucket_cloud="false" bitbucket_server="false" >}}

You can use `pipelinesascode.tekton.dev/on-label` to trigger a `PipelineRun` based on pull request labels.  For example, if you want a `bugs` pipeline to run whenever a pull request gets the label `bug` or `defect`, you can set it up like this:

```yaml
metadata:
  name: match-bugs-or-defect # Pipeline for bug or defect labels
  annotations:
    pipelinesascode.tekton.dev/on-label: [bug, defect] # Triggered by 'bug' or 'defect' labels
```

Things to know about `on-label`:

- It follows the same [Policy]({{< relref "/docs/guide/policy" >}}) rules as pull requests.
- Right now, it's supported on GitHub, Gitea, and GitLab. Bitbucket Cloud and Server don't support pull request labels.
- When you add a label to a pull request, the matching `PipelineRun` starts right away.  Only *one* `PipelineRun` matching the labels will be triggered per label event.
- If you update the pull request with a new commit and the label is still there, the `PipelineRun` with a matching `on-label` will run again.
- You can grab the pull request labels using the [dynamic variable]({{< relref "/docs/guide/authoringprs#dynamic-variables" >}}) `{{ pull_request_labels }}`.  Labels are separated by newlines (`\n`).  For example, in a shell script, you can print them like this:

  ```bash
   for i in $(echo -e "{{ pull_request_labels }}");do
   echo $i
   done
  ```

## Advanced Matching with CEL Expressions

Need to get *really* specific with your triggers? Pipelines as Code lets you use CEL expressions for super-powered event filtering.

If you add the `pipelinesascode.tekton.dev/on-cel-expression` annotation to your `PipelineRun`, Pipelines as Code will use that CEL expression to decide if the pipeline should run.  If you use CEL, the regular `on-target-branch` and `on-event` annotations are ignored.

Here's an example that triggers on pull requests targeting `main`, but only if they come from a branch called `wip`:

```yaml
pipelinesascode.tekton.dev/on-cel-expression: |
  event == "pull_request" && target_branch == "main" && source_branch == "wip" # Specific conditions for triggering
```

Here's a rundown of the fields you can use in your CEL expressions:

| **Field**         | **What it is**                                                                                                                  |
|-------------------|----------------------------------------------------------------------------------------------------------------------------------|
| `event`           | The type of event: `push`, `pull_request`, or `incoming`.                                                                      |
| `target_branch`   | The branch the event is aimed at.                                                                                                |
| `source_branch`   | Where the pull request is coming from. (For `push` events, this is the same as `target_branch`.)                                 |
| `target_url`      | The URL of the repository being targeted.                                                                                        |
| `source_url`      | The URL of the repository where the pull request is coming from. (For `push` events, same as `target_url`.)                     |
| `event_title`     | The title of the event. For `push`, it's the commit title. For PRs, it's the pull/merge request title. (GitHub, GitLab, and Bitbucket Cloud only.) |
| `body`            | The full event data from the Git provider. Example: `body.pull_request.number` gets the pull request number on GitHub.          |
| `headers`         | All the headers from the Git provider's request. Example: `headers['x-github-event']` gets the event type on GitHub.           |
| `.pathChanged`    | A special function to check if a path (using glob patterns) has changed. (GitHub and GitLab only.)                               |
| `files`           | Lists of changed files (`all`, `added`, `deleted`, `modified`, `renamed`). Example: `files.all` or `files.deleted`. For pull requests, it's *all* files in the PR. |

CEL expressions let you do way more complex filtering than just using `on-target` annotations, opening up all sorts of advanced trigger scenarios.

For instance, if you want a `PipelineRun` for pull requests, but *not* for the `experimental` branch, you could use:

```yaml
pipelinesascode.tekton.dev/on-cel-expression: |
  event == "pull_request" && target_branch != experimental" # PRs, but not experimental branch
```

{{< hint info >}}
Want to dive deeper into the CEL language? Check out the spec here:

<https://github.com/google/cel-spec/blob/master/doc/langdef.md>
{{< /hint >}}

### Matching Branches with Regex in CEL

Inside a CEL expression, you can use regular expressions to match field names.  Say you want to trigger a `PipelineRun` for `pull_request` events where the `source_branch` name contains `feat/`.  You could use this:

```yaml
pipelinesascode.tekton.dev/on-cel-expression: |
  event == "pull_request" && source_branch.matches(".*feat/.*") # PRs from branches containing "feat/"
```

### Matching Pipelines Based on Path Changes (CEL Style)

> *NOTE*: Pipelines as Code offers two ways to check for file changes. The `.pathChanged` function uses [glob patterns](https://github.com/gobwas/glob#example) and doesn't distinguish between types of changes (added, modified, etc.). The `files.` property (`files.all`, `files.added`, etc.) lets you target specific change types and use more complex CEL expressions, like `files.all.exists(x, x.matches('renamed.go'))`.

If you only want a PipelineRun to run when specific paths have changed, use the `.pathChanged` function with a [glob pattern](https://github.com/gobwas/glob#example) in your CEL expression. For example, to match any Markdown file (`.md` suffix) in the `docs` directory:

```yaml
pipelinesascode.tekton.dev/on-cel-expression: |
  event == "pull_request" && "docs/*.md".pathChanged() # PRs with changes in docs/*.md
```

This example matches any change (add, modify, remove, rename) within the `tmp` directory:

```yaml
    pipelinesascode.tekton.dev/on-cel-expression: |
      files.all.exists(x, x.matches('tmp/')) # Any change in tmp/ directory
```

This one triggers if any *added* file is in the `src` or `pkg` directory:

```yaml
    pipelinesascode.tekton.dev/on-cel-expression: |
      files.added.exists(x, x.matches('src/|pkg/')) # Added files in src/ or pkg/
```

And this example triggers for *modified* files named `test.go`:

```yaml
    pipelinesascode.tekton.dev/on-cel-expression: |
      files.modified.exists(x, x.matches('test.go')) # Modified files named test.go
```

### Matching PipelineRuns to Event Titles

Want to trigger pipelines based on the title of a commit or pull request?  Here's how to match pull requests with titles starting with `[DOWNSTREAM]`:

```yaml
pipelinesascode.tekton.dev/on-cel-expression: |
  event == "pull_request && event_title.startsWith("[DOWNSTREAM]") # PRs with titles starting with "[DOWNSTREAM]"
```

`event_title` will be the pull request title for `pull_request` events, and the commit title for `push` events.

### Matching PipelineRuns Based on Event Body

{{< tech_preview "Matching PipelineRun on body payload" >}}

The raw event data from your Git provider is available in CEL as the `body` variable.  This lets you filter based on *anything* the Git provider sends.

For example, this expression (for GitHub events):

```yaml
pipelinesascode.tekton.dev/on-cel-expression: |
  body.pull_request.base.ref == "main" &&
    body.pull_request.user.login == "superuser" &&
    body.action == "synchronize" # Very specific conditions based on body content
```

will *only* trigger if:

- The pull request is targeting the `main` branch (`body.pull_request.base.ref == "main"`)
- The author is `superuser` (`body.pull_request.user.login == "superuser"`)
- The event action is `synchronize` (meaning a pull request update happened) (`body.action == "synchronize"`)

Super fine-grained control!

{{< hint info >}}
Heads up: When you're matching based on the event body in pull requests, GitOps comments like `/retest` might not work as expected.

This is because when a comment triggers a pipeline in this scenario, the `body` variable might contain the comment data instead of the original pull request info.

So, if you're using CEL body matching and want to re-run a pipeline with `/retest`, you might need to make a small dummy change to your pull request.  You can do this by pushing a new commit with:

```bash
# Assuming you're on the branch you want to retest
# and your upstream remote is set up correctly
git commit --amend --no-edit && \
  git push --force-with-lease
```

Or, you can just close and reopen the pull request.
{{< /hint >}}

### Matching PipelineRuns to Request Headers

You can even filter based on HTTP headers sent by your Git provider using the `headers` variable in CEL.

Headers are always lowercase.

For example, to make sure an event is a pull request on [GitHub](https://docs.github.com/en/webhooks/webhook-events-and-payloads#delivery-headers):

```yaml
pipelinesascode.tekton.dev/on-cel-expression: |
  headers['x-github-event'] == "pull_request" # Check for GitHub pull_request event header
```

## Handling Commas in Branch Names

If you need to match multiple branches, and one of them has a comma in its name, you might run into trouble. In that case, use the HTML escape code `&#44;` instead of a regular comma in the branch name.  For example, to match `main` and a branch called `release,nightly`, do this:

```yaml
pipelinesascode.tekton.dev/on-target-branch: [main, release&#44;nightly] # Using &#44; for comma in branch name

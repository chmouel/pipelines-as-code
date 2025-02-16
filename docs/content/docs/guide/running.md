---
title: Running the PipelineRun
weight: 4
---

# Let's Get Those Pipelines Running!

Pipelines as Code (PAC) is your friend when you want to automatically kick off pipelines whenever something interesting happens in your code repository, like someone pushing code or creating a pull request.  Think of it like this: when an event occurs in your repo, PAC jumps into action and tries to find instructions – specifically, `PipelineRun` definitions – in your repo's `.tekton` directory. These instructions tell PAC what pipeline to run for that event.

{{< hint info >}}
Just a heads-up: PAC looks for these `PipelineRun` files in the `.tekton` folder at the very top of your repository, right where the event came from.  *Unless* you've set up something different called "provenance from the default branch" in your Repository settings.  If you did that, it might look somewhere else! [Check out the docs on repository provenance](../repositorycrd/#pipelinerun-definition-provenance) for the nitty-gritty details.
{{< /hint >}}

For example, say you have a `PipelineRun` file with this little annotation in it:

```yaml
pipelinesascode.tekton.dev/on-event: "[pull_request]"
```

What this means is, whenever a pull request is created in your repo, PAC will see this annotation and go, "Aha! This PipelineRun is for pull requests!" and it will try to run it on your cluster.  Of course, there's a little gatekeeper – the person who created the pull request needs to be allowed to run it.

So, who's allowed to run pipelines on your CI?  Glad you asked! Here's the breakdown:  You're in if *any* of these are true:

- You're the owner of the repository.  Makes sense, right?
- You're a collaborator on the repository.  Teamwork!
- You're a member (public or private) of the organization that owns the repository.  Organizational perks!
- You've got permission to push code to branches in the repository.  You're a code contributor!
- You're listed in the `OWNERS` file in the main directory of your repository's default branch (like `main` or `master`) on GitHub (or your Git provider).

   Let's talk about this `OWNERS` file.  It's basically a list of people who are allowed to do things, and it follows a format similar to the one used by Kubernetes Prow (you can see the details here: <https://www.kubernetes.dev/docs/guide/owners/>).  We keep it simple – we look for `approvers` and `reviewers` lists, and we treat them the same when it comes to running pipelines.

   Now, if your `OWNERS` file gets fancy and uses `filters` instead of just simple lists, we only pay attention to the filter that matches *everything* (`.*`).  We grab the `approvers` and `reviewers` from that and ignore any other filters that are specific to certain files or folders.

   We also support `OWNERS_ALIASES`.  This is handy if you want to create nicknames for groups of people. You can map an alias (like "qa-team") to a list of usernames.

   The cool thing is, by adding people to the `approvers` or `reviewers` lists in your `OWNERS` files, you're giving them the green light to run pipelines!

   For example, if your `OWNERS` file in your main branch looks something like this:

   ```yaml
   approvers:
     - approved
   ```

   Then anyone with the username "approved" is good to go!

Now, what if the person who made the pull request *doesn't* have permission?  Don't worry, there's a workaround!  Someone who *does* have permission can simply comment `/ok-to-test` on the pull request.  PAC will see this comment and say, "Okay, someone with authority gave the thumbs up!" and it will run the pipeline.

{{< hint info >}}
GitHub Apps users, listen up! If you're using GitHub Apps and you've installed it on an organization, PAC is a bit picky. It will only trigger pipelines if it finds a Repository CR that matches a repo URL *within* that organization.  If it doesn't find a match, no pipeline will run.  Just something to keep in mind!
{{< /hint >}}

## PipelineRun in Action!

So, where do these pipelines actually run?  They always run in the same namespace as the Repository CRD that's linked to the repository where the event came from.  Think of it as keeping things organized and in the right place.

Want to watch your pipeline run?  The command line is your friend!  If you've got the `tkn pac` CLI tool installed (and you should! [Check out how to install it](../cli/#install)), you can use this command to see what's happening:

```console
tkn pac logs -n my-pipeline-ci -L
```

This will show you the logs for the *last* pipeline run in the `my-pipeline-ci` namespace.  If you want to see logs for an *older* pipeline run, just leave off the `-L`:

```console
tkn pac logs -n my-pipeline-ci
```

PAC will be helpful and ask you to choose which PipelineRun you want to see logs for from the ones related to that repository.

And guess what? If you're using the [Tekton Dashboard](https://github.com/tektoncd/dashboard/) or OpenShift's web console, PAC makes it even easier!  It'll post a link right in the "Checks" tab of your GitHub app.  Just click on it and you can follow the pipeline execution in a nice graphical interface. Pretty neat, huh?

## Need to Stop a Pipeline? Cancelling is Here!

### Cancelling Pipelines That Are Running Right Now

{{< tech_preview "Cancelling in progress PipelineRuns" >}}
{{< support_matrix github_app="true" github_webhook="true" gitea="true" gitlab="true" bitbucket_cloud="true" bitbucket_server="false" >}}

Sometimes, you might want to stop a pipeline that's currently running. Maybe you found a mistake or just need to halt things for some reason.  Good news! You can do that by adding this annotation to your `PipelineRun` definition:  `pipelinesascode.tekton.dev/cancel-in-progress: "true"`.

This only works if the `PipelineRun` is actually running. If it's already finished or been cancelled, this annotation won't do anything (think of it like trying to turn off a light that's already off).  If you want to clean up *old* `PipelineRuns`, you'll want to look into the [max-keep-run annotation]({{< relref "/docs/guide/cleanups.md" >}}) instead.

Now, this cancellation is smart. It only applies to `PipelineRuns` within the current Pull Request or the branch you're pushing to.  So, if you have two different Pull Requests, each with a `PipelineRun` that has the same name and this `cancel-in-progress` annotation, cancelling in one Pull Request won't mess with the pipeline in the other.  They're kept separate.

Also, it's important to know that older `PipelineRuns` are only cancelled *after* the newest `PipelineRun` has successfully started.  This annotation doesn't guarantee that only *one* `PipelineRun` will be running at any given moment.

One more thing: if a `PipelineRun` is running and the Pull Request gets closed or declined, PAC will automatically cancel that `PipelineRun` for you.  Handy!

Just a heads-up:  `cancel-in-progress` and the [concurrency limit]({{< relref "/docs/guide/repositorycrd.md#concurrency" >}}) setting don't play well together right now.  You can't use them both at the same time.

### Cancelling with a GitOps Command

There's another way to cancel a PipelineRun using GitOps commands. You can find out all about it [right here]({{< relref "/docs/guide/gitops_commands.md#cancelling-a-pipelinerun" >}}).

## Need to Run That Pipeline Again? Restarting is Easy!

Sometimes a pipeline might fail, or maybe you just want to run it again without making any code changes.  No problem!  Restarting a `PipelineRun` is a breeze.

### For GitHub Apps Users

If you're using the GitHub Apps method, restarting is super simple. Just head over to the "Checks" tab on your GitHub pull request or commit.  In the upper right corner, you'll see a "Re-Run" button.  Give that button a click!

Clicking "Re-Run" tells Pipelines as Code to jump back into action and run the pipeline again.  You can choose to rerun just that specific pipeline or rerun *all* the checks in the suite.  Your choice!

![github apps rerun check](/images/github-apps-rerun-checks.png)

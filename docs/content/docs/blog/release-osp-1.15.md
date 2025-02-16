---
title: OpenShift Pipelines 1.15
---

Hey folks! OpenShift Pipelines 1.15 is here, and if you're using
Pipelines-as-Code, you're in for some treats. We've got some really neat updates
that should make your life easier. Let's dive into the highlights.

## GitOps Commands Get a Whole Lot Smarter

Remember those handy commands you can use in pull request comments to kick off
pipelines?  Well, they just got a serious upgrade.

You probably already know about `/test` and `/retest` to rerun pipelines.  These
are still there and just as useful as ever.

### Run PipelineRuns Whenever You Want, Annotations or Not

Before, you could only re-run pipelines if they had specific labels, like
`pipelinesascode.tekton.dev/on-event: pull_request`.  Kind of a mouthful, right?
Good news: we've ditched that restriction. Now, you can use `/test` to trigger
*any* PipelineRun, no matter what its labels are.

Why is this cool? Imagine you want to run a specific pipeline just *once* before
merging a pull request, but you don't want it firing off every time you push a
tiny update. This gives you that control and saves resources – no more pipelines
running when you don't need them to!

### Tweak Pipeline Parameters on the Fly with GitOps Commands

When a pipeline runs, it usually gets a bunch of pre-set parameters. But what if
you need to change things up a bit, right there in the PR comment? Now you can!
The updated GitOps commands let you add arguments in a `key=value` format to
tweak those parameters in real-time.  Check this out:

```console
/test pipelinerun revision=main
```

Boom!  Instead of testing the pipeline on the specific commit of your pull request, this command tells it to run on the `main` branch.  Pretty slick, huh? You can mess with both the standard parameters and any custom ones you've set up in your Repository CR.

### Roll Your Own GitOps Commands

Want to get really fancy?  OpenShift Pipelines 1.15 introduces a new annotation called `pipelinesascode.tekton.dev/on-comment`.  This lets you trigger a PipelineRun when a comment matches a pattern you define.  Basically, you can create your *own* GitOps commands!

Plus, we've added a new parameter called `{{ trigger_comment }}`.  This grabs the *entire* comment that kicked off the pipeline. Super useful if you want your custom commands to react to different comment content.

### Errors?  Cleared Automatically on Retest

Ever had an error in Pipelines-as-Code that just wouldn't go away until you pushed a new commit?  Annoying, right?  Those days are over! Now, if you use `/retest`, any lingering errors will be automatically cleared out.  Makes things much smoother and less frustrating.

## YAML Errors? We'll Tell You Exactly What's Wrong

In the past, if your YAML in the `.tekton` directory had issues, things would just... stop.  And the error message?  Not very helpful.  Now, we've got smarter YAML checking.  The system will actually validate your YAML *before* trying to run anything and give you specific error messages right in your Git provider.  No more guessing games!

## Global Repository Settings: Set it Once, Use it Everywhere

You know how you can configure a Repository CR with all sorts of settings? Well, now you can set up a *default* Repository CR that applies those settings across *all* your Repository CRs in the cluster.  Think of it as a master settings panel!

An admin can set this up in the `openshift-pipelines` namespace where the controller lives.  This default Repository CR basically becomes the rulebook for how things should behave.

Any settings in this default Repository CR will be applied to all other Repository CRs unless you specifically say otherwise in those individual CRs.

For example, let's say you want to use the same Git provider info and secret (with your Git token) for all repos in your cluster.  Just set it up in the global Repository CR, and *bam*!  Every other Repository CR that uses a Git provider needing a token and URL will automatically inherit those settings.  Saves a ton of time and keeps things consistent.

## Prow OWNERS_ALIASES Support:  Teamwork Makes the Dream Work

Pipelines-as-Code has been playing nice with `OWNERS` files for a while now.  But we've just added support for `OWNERS_ALIASES` too, borrowing another cool feature from Prow.

What's `OWNERS_ALIASES`?  It lets you define groups of people who are responsible for different parts of your codebase.  So you can say "The 'frontend-team' alias includes Alice, Bob, and Carol," and then use 'frontend-team' in your `OWNERS` files. Makes managing permissions and responsibilities way easier, especially in larger projects!

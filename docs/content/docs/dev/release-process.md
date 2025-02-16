---
title: Pipelines-as-Code Release Process
---

# Releasing Pipelines-as-Code: A Step-by-Step Guide

Before you kick off a release, here are a few quick checks:

* **Make sure your pull requests are all squared away.**  Basically, tidy up any pending PRs.
* **Wait for the CI to connect.**  Gotta make sure the Continuous Integration is hooked up and ready.
* **Double-check the PAC CI cluster is up and running.**  We need our Pipelines-as-Code CI cluster to be online, obviously!
* **Confirm you've got GPG signing set up for your commits.** This is important for security, so [check out this guide](https://docs.github.com/en/authentication/managing-commit-signature-verification/about-commit-signature-verification) if you're not sure.

Okay, let's get to tagging and pushing.

* **Decide on your version number.** Are we doing a major, minor, or patch release?  Choose wisely!

* **Tag it locally.**  Let's say we're going with version `1.2.3`.  Fire up your terminal and tag it:

```shell
git tag v1.2.3
```

* **Push it to the repo.**  You'll need write access for this bit.  Use this command to push the tag:

```shell
% NOTESTS=ci git push git@github.com:openshift-pipelines/pipelines-as-code refs/tags/1.2.3
```

Time to watch the magic happen!

* **Keep an eye on the release process.** You can follow along on the PAC cluster with this command:

```shell
tkn pr logs -n pipelines-as-code-ci -Lf
```

* **Give it some time.**  The `gorelease` process takes a little while.  If all goes well, you should see your new version pop up as a pre-release over here: <https://github.com/openshift-pipelines/pipelines-as-code/releases>

* **Spruce up the release notes.** Just like the other releases, add a quick summary of the highlights for this version.

Spread the word!

* **Shout it from the rooftops (or at least Slack and Twitter).** Let everyone know about the new release on Slack (both upstream and downstream channels) and Twitter.

## Package Updates

* **Arch AUR Package:**  If you're updating the Arch AUR package ([link here](https://aur.archlinux.org/packages/tkn-pac)), give chmouel a nudge to get it updated.

# Uh Oh, Something Went Wrong?

* **Need to re-run the release?**  Sometimes things just don't go as planned. If you need to re-kick the release process, try this:

```shell
   git tag --sign --force v1.2.3
   git push --force git@github.com:openshift-pipelines/pipelines-as-code v1.2.3
```

* **GitHub Token Troubles?**  Double-check your GitHub token.  Expired tokens or tokens with a sneaky newline character can cause issues.
* **Forgot to Fetch?**  Make sure you did a `git fetch -a origin` *before* tagging.  Otherwise, you might be missing the latest commits from `origin/main`, and that's no good!

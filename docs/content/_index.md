---
bookToc: false
title: An opinionated CI based on OpenShift Pipelines / Tekton
---
# Pipelines-as-Code

An opinionated CI built on top of OpenShift Pipelines / Tekton.

## Introduction

Pipelines as code is all about defining your CI/CD pipelines with [Tekton](https://tekton.dev) PipelineRuns and Tasks, right in a file within your source code repository (like GitHub or GitLab).  This file then automatically sets up a pipeline whenever you create a Pull Request or push code to a branch.

Think of it this way: your pipeline definition lives alongside your code.  This makes it way easier to track changes, review updates, and collaborate on pipeline tweaks just like you do with your application code. Plus, you can see how your pipelines are doing and control them directly from your code platform – no more jumping between different systems!

Basically, it brings automation, consistency, teamwork, and version control to your CI/CD using the Git workflow you already know and love.

## Features

{{< columns >}} <!-- begin columns block -->

- **Pull Request Statuses:**  See the status of your pipeline right in GitHub as you work on your pull request.  No more guessing if your changes are passing CI!

- **GitHub Checks API:** Pipeline status updates (including re-checks) are powered by the GitHub Checks API, giving you detailed feedback.

- **GitHub Event Support:** Works with both Pull Request and Push events.  Whatever your workflow, we've got you covered.

<--->

- **GitOps Style PR Actions:**  Want to re-run a test? Just use comments like `/retest` or `/test <pipeline-name>` directly in your pull requests to trigger actions.

- **Automatic Task Resolution:** Pipelines automatically find the Tasks they need, whether they are defined locally in your repo, available on Tekton Hub, or living at a remote URL.

- **Efficient Configuration Retrieval:** Smartly uses GitHub's API to grab just the configurations it needs, making things fast.

<--->

- **Git Event Filtering:** Set up different pipelines for different Git events – giving you more control over what runs when.

- **More than just GitHub:** Also supports GitLab, Bitbucket Server, Bitbucket Cloud, and webhooks from other Git platforms. So, wherever your code lives, Pipelines-as-Code can likely work with it.

- **`tkn-pac` CLI Plugin:** Comes with a handy `tkn-pac` plugin for the Tekton CLI to help you manage your pipelines-as-code and get started quickly. It's like having a toolbox specifically for Pipelines-as-Code!

{{< /columns >}}

## Getting Started

The easiest way to dive in? Use the `tkn pac` CLI and its [bootstrap](/docs/guide/cli/#commands) command!  Seriously, it's super simple.

We suggest playing around with Pipelines-as-Code using your personal [GitHub](https://github.com/) account first.  Install it on your laptop using [Kind](https://kind.sigs.k8s.io/) or [OpenShift Local](https://developers.redhat.com/products/openshift-local/overview) and see how it all works before setting it up on your main cluster. Think of it as your personal sandbox to experiment in.

- Check out this [Getting Started Guide]({{< relref "/docs/install/getting-started.md" >}}) to walk through creating a GitHub App, setting up Pipelines-as-Code, and running your first Pipeline from a Pull Request. We promise it's not as scary as it sounds!
- Prefer watching instead of reading?  This [walkthrough video](https://youtu.be/cNOqPgpRXQY) will show you the ropes.  Grab some popcorn!

## Documentation

Want to know more about different ways to install?  Head over to [the installation document](/docs/install/overview) for all the details and steps. We've got options for everyone.

Ready to use Pipelines-as-Code and create your own PipelineRuns?  The [usage guide](/docs/guide) is your friend! It's packed with everything you need to know to get up and running.

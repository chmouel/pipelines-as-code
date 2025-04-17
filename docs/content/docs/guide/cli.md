---
title: CLI tkn-pac
weight: 100
---
# Pipelines-as-Code CLI

Pipelines-as-Code provides a powerful CLI designed to work as a plug-in to the [Tekton CLI (tkn)](https://github.com/tektoncd/cli).

`tkn pac` allows you to:

* `bootstrap`: Quickly bootstrap a Pipelines-as-Code installation.
* `create`: Create a new Pipelines-as-Code Repository definition.
* `delete`: Delete an existing Pipelines-as-Code Repository definition.
* `generate`: Generate a simple PipelineRun to get you started with Pipelines-as-Code.
* `list`: List Pipelines-as-Code Repositories.
* `logs`: Show the logs of a PipelineRun from a Repository CRD.
* `describe`: Describe a Pipelines-as-Code Repository and the runs associated with it.
* `resolve`: Resolve a PipelineRun as if it were executed by Pipelines-as-Code.
* `webhook`: Update webhook configurations and secrets.
* `info`: Show information about your Pipelines-as-Code installation.

## Install

{{< tabs "installbinary" >}}
{{< tab "Binary" >}}
You can download the latest binary directly for your operating system from the
[releases](https://github.com/openshift-pipelines/pipelines-as-code/releases)
page.

Available operating systems are:

* MacOS - M1 and x86 architecture
* Linux - 64bits - RPM, Debian packages, and tarballs.
* Linux - ARM 64bits - RPM, Debian packages, and tarballs.
* Windows - ARM 64 Bits and x86 architecture.

{{< hint info >}}
On Windows, tkn-pac will look for the Kubernetes config in `%USERPROFILE%\.kube\config`. On Linux and MacOS, it will use the standard $HOME/.kube/config.
{{< /hint >}}

{{< /tab >}}

{{< tab "Homebrew" >}}
tkn pac plug-in is available from HomeBrew as a "Tap". You simply need to run this command to install it:

```shell
brew install openshift-pipelines/pipelines-as-code/tektoncd-pac
```

and if you need to upgrade it:

```shell
brew upgrade openshift-pipelines/pipelines-as-code/tektoncd-pac
```

`tkn pac` plug-in is compatible with [Homebrew on Linux](https://docs.brew.sh/Homebrew-on-Linux)

{{< /tab >}}
{{< tab "Container" >}}
`tkn-pac` is available as a container:

```shell
# use podman or docker
podman run -e KUBECONFIG=/tmp/kube/config -v ${HOME}/.kube:/tmp/kube \
     -it  ghcr.io/openshift-pipelines/pipelines-as-code/tkn-pac:stable tkn-pac help
```

{{< /tab >}}

{{< tab "GO" >}}
If you want to install from the Git repository you can run:

```shell
go install github.com/openshift-pipelines/pipelines-as-code/cmd/tkn-pac
```

{{< /tab >}}

{{< tab "Arch" >}}
You can install the `tkn pac` plugin from the [Arch User
Repository](https://aur.archlinux.org/packages/tkn-pac/) (AUR) with your
favorite AUR installer like `yay`:

```shell
yay -S tkn-pac
```

{{< /tab >}}

{{< /tabs >}}

## Commands

{{< details "tkn pac bootstrap" >}}

### Bootstrap

`tkn pac bootstrap` helps you get started installing and configuring Pipelines-as-Code. It currently supports the following providers:

* GitHub Application on public GitHub
* GitHub Application on GitHub Enterprise

It will first check if you have Pipelines-as-Code installed. If not, it will ask if you want to install (using `kubectl`) the latest stable release. If you add the flag `--nightly`, it will install the latest development release.

Bootstrap automatically detects the OpenShift Route associated with the Pipelines-as-Code controller service and uses this as the endpoint for the created GitHub application.

You can use the `--route-url` flag to specify a custom URL or override the detected OpenShift Route URL. This is useful when using an [Ingress](https://kubernetes.io/docs/concepts/services-networking/ingress/) in a Kubernetes cluster.

On OpenShift, the console is automatically detected. On Kubernetes, `tkn-pac` will try to detect the tekton-dashboard Ingress URL and ask if you want to use it as the endpoint for the GitHub application.

If your cluster is not accessible from the internet, Pipelines-as-Code can install a webhook forwarder called [gosmee](https://github.com/chmouel/gosmee). This forwarder enables connectivity between the Pipelines-as-Code controller and GitHub without requiring an internet-accessible endpoint. It will set up a forwarding URL on <https://hook.pipelinesascode.com> and configure it on GitHub. For OpenShift, this option is only offered when you explicitly specify the `--force-gosmee` flag (which can be useful if you are running [OpenShift Local](https://developers.redhat.com/products/openshift-local/overview)).

{{< /details >}}

{{< details "tkn pac bootstrap github-app" >}}

### Bootstrap GitHub App

If you only want to create a GitHub application without the full `bootstrap` process, you can use `tkn pac bootstrap github-app` directly. This will skip the installation steps and only create the GitHub application and the associated secret in the `pipelines-as-code` namespace.

{{< /details >}}

{{< details "tkn pac create repo" >}}

### Repository Creation

`tkn pac create repo` creates a new Pipelines-as-Code `Repository` custom resource definition for a Git repository. This allows the repository to execute PipelineRuns based on Git events.

The command will also generate a sample [PipelineRun](/docs/guide/authoringprs) in the `.tekton` directory called `pipelinerun.yaml` targeting the `main` branch for both `pull_request` and `push` events. You can customize this by editing the generated file to target different branches or events.

If you haven't configured a provider previously, the command will guide you through setting up a webhook for your chosen Git provider.

{{< /details >}}

{{< details "tkn pac delete repo" >}}

### Repository Deletion

`tkn pac delete repo` deletes a Pipelines-as-Code Repository definition.

You can use the `--cascade` flag to also delete any associated secrets (such as webhook or provider secrets) that are attached to the Repository definition.

{{< /details >}}

{{< details "tkn pac list" >}}

### Repository Listing

`tkn pac list` lists all the Pipelines-as-Code Repository definitions and displays the status (current or last) of any associated PipelineRuns.

Options:
* `-A/--all-namespaces`: List repositories across all namespaces (requires appropriate permissions)
* `-l/--selectors`: Filter repositories by labels
* `--use-realtime`: Display times in RFC3339 format instead of relative time

On modern terminals (such as Terminal.app, iTerm2, Windows Terminal, GNOME Terminal, etc.), the console/dashboard URLs are clickable (typically with Ctrl+click or ⌘+click) and will open in your browser.

{{< /details >}}

{{< details "tkn pac describe" >}}

### Repository Description

`tkn pac describe` provides detailed information about a Pipelines-as-Code Repository definition and its associated runs.

Options:
* `--use-realtime`: Display times in RFC3339 format instead of relative time
* `--target-pipelinerun/-t`: Show failures for a specific PipelineRun instead of the most recent one

For failed PipelineRuns, the command will display the last 10 lines of each failed task, highlighting error patterns.

On modern terminals (such as Terminal.app, iTerm2, Windows Terminal, GNOME Terminal, etc.), the console/dashboard URLs are clickable (typically with Ctrl+click or ⌘+click) and will open in your browser.

{{< /details >}}

{{< details "tkn pac logs" >}}

### Logs

`tkn pac logs` shows the logs for PipelineRuns associated with a Repository.

If you don't specify a repository on the command line, you'll be prompted to select one (or it will be auto-selected if there's only one).

If there are multiple PipelineRuns for the selected repository, you'll be prompted to choose one (or it will be auto-selected if there's only one).

Options:
* `-w`: Open the console or dashboard URL to view the logs in a browser

Note: The [`tkn`](https://github.com/tektoncd/cli) binary must be installed to show logs.

{{< /details >}}

{{< details "tkn pac generate" >}}

### Generate

`tkn pac generate` creates a simple PipelineRun template to help you get started with Pipelines-as-Code. When run from within a Git repository, it will attempt to detect the current Git information automatically.

The command includes basic language detection and will add appropriate tasks based on the detected programming language. For example, if it finds a `setup.py` file at the repository root, it will add the [pylint task](https://hub.tekton.dev/tekton/task/pylint) to the generated PipelineRun.

{{< /details >}}

{{< details "tkn pac resolve" >}}

### Resolve

`tkn pac resolve` processes a PipelineRun as if it were executed by Pipelines-as-Code, resolving all references and dependencies.

Example usage:

```shell
tkn pac resolve -f .tekton/pull-request.yaml -o /tmp/pull-request-resolved.yaml && kubectl create -f /tmp/pull-request-resolved.yaml
```

This is useful for testing your PipelineRuns locally without having to push changes to trigger the CI pipeline. It works well with local Kubernetes environments like [OpenShift Local](https://developers.redhat.com/products/openshift-local/overview) or [Kind](https://kind.sigs.k8s.io/docs/user/quick-start/).

When run from within a Git repository, the command will try to detect parameters (like revision or branch name) automatically.

Options:
* `-f`: Specify file(s) or directory containing PipelineRun definitions (can be used multiple times)
* `-p`: Override parameters (format: `name=value`)
* `-o`: Output file for the resolved PipelineRun
* `--v1beta1/-B`: Output in v1beta1 format instead of v1 (helps with compatibility issues)
* `--no-secret`: Skip secret generation for Git authentication
* `-t/--providerToken`: Explicitly provide a Git provider token

Note: If your PipelineRun contains a `{{ git_auth_secret }}` reference, the command will handle authentication for private repositories by either asking for a token, using an existing secret, or using a token provided through the command line or `PAC_PROVIDER_TOKEN` environment variable.

{{< /details >}}

{{< details "tkn pac webhook add" >}}

### Add Webhook

`tkn pac webhook add [-n namespace]` adds a new webhook secret for a specified Git provider (GitHub, GitLab, or Bitbucket Cloud) and updates the value in the existing `Secret` object used by Pipelines-as-Code.

{{< /details >}}

{{< details "tkn pac webhook update-token" >}}

### Update Provider Token

`tkn pac webhook update-token [-n namespace]` updates the provider token for an existing `Secret` object used to interact with Pipelines-as-Code.

{{< /details >}}

{{< details "tkn pac info install" >}}

### Installation Info

`tkn pac info` displays information about your Pipelines-as-Code installation, including its location and version.

By default, it shows the Pipelines-as-Code controller version and installation namespace. This information is available to all users via a `pipelines-as-code-info` ConfigMap with broad read access.

For cluster administrators, the command will also show:
* An overview of all Repository CRs on the cluster with their URLs
* Details about the GitHub App installation (if configured), including the endpoint URL

You can specify a custom GitHub API URL using the `--github-api-url` argument when working with GitHub Enterprise.

{{< /details >}}

{{< details "tkn pac info globbing" >}}

### Test Globbing Pattern

`tkn pac info globbing` lets you test glob patterns to see if they match specific paths. This is particularly useful when setting up the `on-patch-change` annotation.

Example:

```shell
tkn pac info globbing 'docs/***/*.md'
```

This will match all markdown files in the `docs` directory and its subdirectories within the current directory.

Options:
* `-d/--dir`: Test against a specific directory instead of the current one
* `-s/--string`: Test against a string instead of paths (useful for testing other annotations like `on-target-branch`)

Example with string matching:

```shell
tkn pac info globbing -s "refs/heads/main" "refs/heads/*"
```

This tests whether the pattern `refs/heads/*` matches the string `refs/heads/main`.

{{< /details >}}

## Screenshot

![tkn-plug-in](/images/tkn-pac-cli.png)

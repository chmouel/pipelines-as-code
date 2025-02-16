---
title: CLI tkn-pac
weight: 100
---

# Meet the Pipelines-as-Code CLI: `tkn pac`

Pipelines-as-Code comes with a handy command-line tool, `tkn pac`, that works as a plugin for the [Tekton CLI (`tkn`)](https://github.com/tektoncd/cli). Think of it as your sidekick for all things Pipelines-as-Code!

`tkn pac` gives you superpowers to:

* `bootstrap`: Get Pipelines-as-Code up and running super fast.
* `create`:  Define a new Git repository for Pipelines-as-Code to watch.
* `delete`:  Remove a repository definition you no longer need.
* `generate`:  Create a basic pipeline setup to get your feet wet with Pipelines-as-Code.
* `list`:  See all your Pipelines-as-Code managed repositories at a glance.
* `logs`:  Dive into the logs of a Pipeline run from a Repository.
* `describe`: Get the lowdown on a Repository and its associated runs.
* `resolve`: Test out a PipelineRun locally, just like Pipelines-as-Code would run it.
* `webhook`: Update your webhook secret key.
* `info`:  Get the inside scoop on your Pipelines-as-Code setup (like installation details with `info install`).

## Get it Installed

{{< tabs "installbinary" >}}
{{< tab "Binary" >}}
Want to grab the latest and greatest? You can download the binary for your OS straight from the [releases](https://github.com/openshift-pipelines/pipelines-as-code/releases) page.

We've got binaries for:

* MacOS - Both M1 and x86 architectures
* Linux - 64bits - RPM, Debian packages, and good old tarballs.
* Linux - ARM 64bits - RPM, Debian packages, and tarballs.
* Windows - Arm 64 Bits and x86 architectures.

{{< hint info >}}
Good to know: On Windows, `tkn-pac` looks for your Kubernetes config in `%USERPROFILE%\.kube\config`. On Linux and MacOS, it's the usual `$HOME/.kube/config`.
{{< /hint >}}

{{< /tab >}}

{{< tab "Homebrew" >}}
For macOS and Linux Homebrew users, `tkn pac` is available as a "Tap"!  Just run this command to install:

```shell
brew install openshift-pipelines/pipelines-as-code/tektoncd-pac
```

And if you want to upgrade to the latest version:

```shell
brew upgrade openshift-pipelines/pipelines-as-code/tektoncd-pac
```

Yep, `tkn pac` plays nicely with [Homebrew on Linux](https://docs.brew.sh/Homebrew-on-Linux) too.

{{< /tab >}}
{{< tab "Container" >}}
Feeling container-y?  `tkn-pac` is also available as a Docker container:

```shell
# using podman (or docker, if that's your jam)
podman run -e KUBECONFIG=/tmp/kube/config -v ${HOME}/.kube:/tmp/kube \
     -it  ghcr.io/openshift-pipelines/pipelines-as-code/tkn-pac:stable tkn-pac help
```

{{< /tab >}}

{{< tab "GO" >}}
If you're a Go enthusiast and want to install straight from the source code, go for it:

```shell
go install github.com/openshift-pipelines/pipelines-as-code/cmd/tkn-pac
```

{{< /tab >}}

{{< tab "Arch" >}}
Arch Linux users, we haven't forgotten you!  You can grab the `tkn pac` plugin from the [Arch User Repository](https://aur.archlinux.org/packages/tkn-pac/) (AUR) using your favorite AUR helper like `yay`:

```shell
yay -S tkn-pac
```

{{< /tab >}}

{{< /tabs >}}

## Command Breakdown

{{< details "tkn pac bootstrap" >}}

### `bootstrap` Command

The `tkn pac bootstrap` command is your fast track to setting up Pipelines-as-Code. Right now, it's got your back for these providers:

* GitHub Application on public GitHub
* GitHub Application on GitHub Enterprise

It'll first check if Pipelines-as-Code is already installed. If not, it'll ask if you want to install the latest stable version (using `kubectl`, of course). Throw in the `--nightly` flag if you're feeling adventurous and want the latest code from the CI.

Bootstrap is smart enough to find the OpenShift Route linked to your Pipelines-as-Code controller service and use that as the endpoint for your new GitHub App.

Need to use a different URL? No problem! You can use the `--route-url` flag to override the OpenShift Route URL or point it to a custom URL on an [Ingress](https://kubernetes.io/docs/concepts/services-networking/ingress/) in a Kubernetes cluster.

It also automatically detects the OpenShift console.  On plain Kubernetes, `tkn-pac` will try to find the tekton-dashboard Ingress URL and let you pick it as the endpoint for your GitHub App.

If your cluster isn't reachable from the internet, Pipelines-as-Code has a cool option to install a webhook forwarder called [gosmee](https://github.com/chmouel/gosmee). This lets Pipelines-as-Code talk to GitHub without needing a public internet connection.  In this case, it sets up a forwarding URL at <https://hook.pipelinesascode.com> and configures it on GitHub. For OpenShift, it won't ask unless you specifically use the `--force-gosmee` flag (which can be handy if you're running [OpenShift Local](https://developers.redhat.com/products/openshift-local/overview), for example).

{{< /details >}}

{{< details "tkn pac bootstrap github-app" >}}

### `bootstrap github-app` Command

Just want to create a GitHub App for Pipelines-as-Code without the full `bootstrap` process?  `tkn pac bootstrap github-app` is your friend.  It skips the installation part and just creates the GitHub App and the secret with all the necessary info in the `pipelines-as-code` namespace.

{{< /details >}}

{{< details "tkn pac create repo" >}}

### `create repo` Command

`tkn pac create repo` -  This command is all about creating a new Pipelines-as-Code `Repository` custom resource.  It sets up a link to a Git repo so you can trigger pipeline runs based on Git events.  It'll even whip up a sample [PipelineRun](/docs/guide/authoringprs) file called `pipelinerun.yaml` in your `.tekton` directory. This starter file targets the `main` branch and the `pull_request` and `push` events.  Feel free to tweak this [PipelineRun](/docs/guide/authoringprs) to watch different branches or events.

If you haven't set up a provider before, it'll ask you if you want to configure a webhook for your chosen provider.  Easy peasy!

{{< /details >}}

{{< details "tkn pac delete repo" >}}

### `delete repo` Command

`tkn pac delete repo` -  This one's straightforward: it deletes a Pipelines-as-Code Repository definition.

Want to clean up completely? Use the `--cascade` flag to also delete the associated secrets (like webhook or provider secrets) along with the Repository definition.

{{< /details >}}

{{< details "tkn pac list" >}}

### `list` Command

`tkn pac list` -  Need a quick overview of your Pipelines-as-Code Repositories? This command lists them all, showing you the last or current status (if it's running) of the PipelineRun connected to each one.

Want to see everything across all namespaces?  `-A/--all-namespaces` is your flag (you'll need the right permissions for that, naturally).

You can also filter repositories by labels using the `-l/--selectors` flag.

By default, it shows time in a friendly relative format (like "5 minutes ago").  If you prefer the precise RFC3339 timestamp, use the `--use-realtime` flag.

Cool feature for modern terminals (like OSX Terminal, [iTerm2](https://iterm2.com/), [Windows Terminal](https://github.com/microsoft/terminal), GNOME-terminal, kitty, etc.):  links become clickable! Just control+click or ⌘+click (check your terminal's docs for the exact combo) and it'll open your browser to the console/dashboard URL to see the PipelineRun details. Nice, right?

{{< /details >}}

{{< details "tkn pac describe" >}}

### `describe` Command

`tkn pac describe` -  This command gives you a detailed look at a Pipelines-as-Code Repository definition and the PipelineRuns linked to it.

Like `list`, it uses relative time by default. Use `--use-realtime` if you want RFC3339 timestamps instead.

If the last PipelineRun failed, it'll helpfully print the last 10 lines of each failed task, highlighting `ERROR` or `FAILURE` messages and other relevant patterns to help you debug.

Want to see failures from a different PipelineRun than the latest one? The `--target-pipelinerun` or `-t` flag lets you pick which one to focus on.

And yes, those clickable links in modern terminals work here too! Control+click or ⌘+click to jump to the console/dashboard URL and get more context.

{{< /details >}}

{{< details "tkn pac logs" >}}

### `logs` Command

`tkn pac logs` -  Want to see the logs for a Repository? This command is your ticket.

If you don't specify a repository name, it'll ask you to choose one, or automatically select it if there's only one option.

If there are multiple PipelineRuns for the Repo, it'll ask you to pick one, or auto-select if there's just one.

Add the `-w` flag and it'll open the console or dashboard URL directly to the logs in your browser.

Heads up:  You'll need the [`tkn`](https://github.com/tektoncd/cli) binary installed for `tkn pac logs` to work its magic.

{{< /details >}}

{{< details "tkn pac generate" >}}

### `generate` Command

`tkn pac generate` -  Need a starting point? This command generates a basic PipelineRun to get you started with Pipelines-as-Code. It tries to be clever and figure out Git info if you run it from your source code directory.

It even does some basic language detection and adds extra tasks based on the language.  For example, if it spots a `setup.py` file at the root of your repo, it'll add the [pylint task](https://hub.tekton.dev/tekton/task/pylint) to the generated PipelineRun.  Pretty neat, huh?

{{< /details >}}

{{< details "tkn pac resolve" >}}

### `resolve` Command

`tkn-pac resolve` - Ever wondered how Pipelines-as-Code would run your pipeline? This command lets you test it out locally!

For instance, if you have a PipelineRun in `.tekton/pull-request.yaml`, you can run:

```yaml
tkn pac resolve -f .tekton/pull-request.yaml -o /tmp/pull-request-resolved.yaml && kubectl create -f /tmp/pull-request-resolved.yaml
```

Combine this with a local Kubernetes setup (like [Code Ready Containers](https://developers.redhat.com/products/codeready-containers/overview) or [Kubernetes Kind](https://kind.sigs.k8s.io/docs/user/quick-start/)) and you can see your pipeline in action without even committing any code!

Run the command from your source code repo, and it'll try to grab parameters (like revision or branch name) from your Git info.

Need to tweak things? Override parameters with the `-p` flag.

For example, to use the `main` branch as the revision and a different repo name:

`tkn pac resolve -f .tekton/pr.yaml -p revision=main -p repo_name=othername`

The `-f` flag can take a directory path too, not just a filename. It'll grab all `.yaml` or `.yml` files in that directory. You can also use multiple `-f` flags to specify multiple files.

Important:  Make sure the `git-clone` task (if you're using it) can access the repo at the specified SHA.  If you're testing local code, you'll need to push it first before using `tkn pac resolve | kubectl create -`.

Keep in mind, when running locally, you need to explicitly tell it which files or directories contain your templates.

On some clusters, Tekton might have issues converting from v1beta1 to v1, which can cause errors when applying the resolved PipelineRun on a cluster without the bundle feature.  If you run into this, use the `--v1beta1` flag (or `-B` for short) to output the PipelineRun as v1beta1 and sidestep the issue.

The resolver is also smart about secrets!  If it finds `{{ git_auth_secret }}` in your template, it'll ask you for a Git provider token.

If you already have a secret matching your repo URL in your namespace, it'll use that.

You can also provide a token directly with the `-t` or `--providerToken` flag, or set the `PAC_PROVIDER_TOKEN` environment variable.

Want to skip secret generation altogether? Use the `--no-secret` flag.

Just a heads-up:  Secrets created during `resolve` aren't automatically cleaned up after the run.

{{< /details >}}

{{< details "tkn pac webhook add" >}}

### `webhook add` Command

`tkn-pac webhook add [-n namespace]` -  Need to add a new webhook secret for GitHub, GitLab, or Bitbucket Cloud? This command helps you create one and update the existing `Secret` object Pipelines-as-Code uses.

{{< /details >}}

{{< details "tkn pac webhook update-token" >}}

### `webhook update-token` Command

`tkn pac webhook update-token [-n namespace]` -  Time to update your provider token for an existing webhook? This command lets you update the token in the `Secret` object that Pipelines-as-Code uses.

{{< /details >}}

{{< details "tkn pac info install" >}}

### `info install` Command

The `tkn pac info` command is like a health check for your Pipelines-as-Code installation. It gives you key details like its location and version.

By default, it shows you the version of the Pipelines-as-Code controller and the namespace where it's installed.  This info is publicly available to everyone on the cluster through a special ConfigMap called `pipelines-as-code-info`.

If you're a cluster admin, you get the bonus of seeing an overview of all created Repository CRs on the cluster, along with their URLs.

Admins with a [GitHub App](../../install/github_apps) setup can also see details about the app and other useful info, like the URL endpoint configured for it.  By default, it pulls data from the public GitHub API, but you can point it to a custom GitHub API URL using the `--github-api-url` argument.

{{< /details >}}

{{< details "tkn pac info globbing" >}}

### `info globbing` Command

Want to test out your glob patterns? The `tkn pac info globbing` command is your testing ground, especially handy when using annotations like `on-patch-change`.

Here's how it works (example):

```bash
tkn pac info globbing 'docs/***/*.md'
```

This will check if the glob pattern `'docs/***/*.md'` matches any markdown files in the `docs` directory and its subdirectories in your current location.

It defaults to testing against your current directory unless you use the `-d` or `--dir` flag to specify a different directory.

The first argument is the glob pattern itself (it'll prompt you if you don't provide one).  It uses the [glob library](https://github.com/gobwas/glob?tab=readme-ov-file#example) pattern syntax.

Need to test against a specific string for annotations like `on-target-branch`? Use the `-s` or `--string` flag.

Example:  Does the glob `'refs/heads/*'` match `'refs/heads/main'`?

```bash
tkn pac info globbing -s "refs/heads/main" "refs/heads/*"
```

#### Example in action

```bash
tkn pac info globbing 'docs/**/*.md'
```

This will find all markdown files in the `docs` directory and any folders inside it, assuming they're in your current directory.

To test against a different directory, use the `-d/--dir` flag.

{{< /details >}}

## `tkn pac` in Action!

![tkn-plug-in](/images/tkn-pac-cli.png)

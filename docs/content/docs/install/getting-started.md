---
title: Getting started guide.
weight: 2
---

# Let's Get Started with Pipelines as Code!

Ready to dive into Pipelines as Code? Awesome! This guide will walk you through setting it all up.

We'll start by getting Pipelines as Code installed on your Kubernetes cluster. Then, we'll create a GitHub App to make things smooth, set up a Repository CR (think of it as telling Pipelines as Code which repo to watch), and finally, we'll create a simple Pull Request to see everything in action.  You'll see how Pipelines as Code works its magic!

## What You'll Need

Before we jump in, make sure you've got these things ready:

- A Kubernetes cluster.  [Kind](https://kind.sigs.k8s.io/), [Minikube](https://minikube.sigs.k8s.io/docs/start/), or [OpenShift Local](https://developers.redhat.com/products/openshift-local/overview) are all great for testing things out.
- OpenShift Pipelines Operator installed, or if you're not on OpenShift, [Tekton Pipelines](https://tekton.dev/docs/pipelines/install/) installed on your cluster.  These are the engines that power the pipelines!
- The [Tekton CLI](https://tekton.dev/docs/cli/#installation) (that's `tkn`) installed, and the Pipelines as Code plugin for it ([tkn-pac](https://pipelinesascode.com/docs/guide/cli/#install)). This gives you the `tkn pac` commands we'll be using.

## Installing Pipelines as Code - Easy Peasy!

There are a few ways to get Pipelines as Code onto your cluster.  Honestly, the easiest way is if you're using OpenShift Pipelines Operator – it actually installs Pipelines as Code for you automatically!  Nice, right?

If you're not using the Operator, no worries! You can still install it yourself. You could apply the YAML files directly, but even easier, you can use the `tkn pac bootstrap` command.  This command is your friend for a quick setup.

Now, if you're setting up Pipelines as Code for real-world, production use, you might want to think about using a GitOps tool like [OpenShift GitOps](https://www.openshift.com/learn/topics/gitops/) or [ArgoCD](https://argoproj.github.io/argo-cd/) and follow the manual install steps.  But for this guide, we're taking the express route with `tkn pac bootstrap`.

{{< hint info >}}
Just a heads-up: We're assuming you're using the regular public GitHub here. If you're on GitHub Enterprise, you'll need to tell `tkn pac bootstrap` about it using the `--github-hostname` flag and your GitHub Enterprise hostname.
{{< /hint >}}

### Let's Run the Installation

Open up your terminal and type:

```bash
% tkn pac bootstrap
=> Checking if Pipelines-as-Code is installed.
🕵️ Pipelines-as-Code doesn't seem to be installed in the pipelines-as-code namespace.
? Do you want me to install Pipelines-as-Code v0.19.2? (Y/n)
```

As soon as you run this, `tkn pac bootstrap` checks if Pipelines as Code is already there.  If it's not, it'll ask if you want to install it.  Pretty helpful, huh?

Go ahead and hit `Y` to get it installed.

### Webhook Forwarder -  Making Connections

Next up, it might ask you about something called a "webhook forwarder" – specifically, [gosmee](https://github.com/chmouel/gosmee).  This question usually pops up if you're not on OpenShift.

Don't sweat it if you're not sure what it is right away. It's not absolutely essential, but it makes things much simpler.  Think of it this way: GitHub needs to be able to talk to your Pipelines as Code controller, which might be tucked away in your cluster.  `gosmee` helps bridge that gap from the internet to your cluster without making your controller publicly accessible.

```console
Pipelines-as-Code does not install an Ingress object to allow the controller to be accessed from the internet.
We can install a webhook forwarder called gosmee (https://github.com/chmouel/gosmee) using a https://hook.pipelinesascode.com URL.
This will let your git platform provider (e.g., GitHub) reach the controller without requiring public access.
? Do you want me to install the gosmee forwarder? (Y/n)
💡 Your gosmee forward URL has been generated: https://hook.pipelinesascode.com/zZVuUUOkCzPD
```

### Tekton Dashboard - Pretty Pipeline Views!

One last question might come up if you have the [Tekton Dashboard](https://github.com/tekton/dashboard) installed. It's a cool visual tool for Tekton.  If you have it, `tkn pac bootstrap` can set things up so that links to your PipelineRun logs and details will open right in the Dashboard.  If you're on OpenShift, it's even smarter and will automatically use the OpenShift console. Nice touch!

```console
👀 We have detected a tekton dashboard install on http://dashboard.paac-127-0-0-1.nip.io
? Do you want me to use it? Yes
```

## GitHub Application -  Let's Get Connected

Alright, next step: creating a GitHub Application.  You *could* use Pipelines as Code with just a basic webhook, but using a GitHub App is really the way to go for the best experience.  Trust me, it's worth it!

First, it'll ask you to name your GitHub App. This name needs to be unique across GitHub, so pick something a bit less common to avoid clashes.

```console
? Enter the name of your GitHub application: My PAAC Application
```

As soon as you hit Enter, the CLI will try to open your web browser to <https://localhost:8080>.  Don't worry if it seems a bit weird – it's just a temporary page with a button to create the GitHub App. Click that button, and you'll be whisked away to GitHub. You'll see a screen that looks something like this:

![GitHub Application Creation](/images/github-app-creation-screen.png)

Unless you want to change the app name, just click "Create GitHub App."  Then, head back to your terminal where `tkn pac bootstrap` is running. You should see a message saying the GitHub App is created and a secret token has been generated.  Magic!

```console
🔑 Secret pipelines-as-code-secret has been created in the pipelines-as-code namespace
🚀 You can now add your newly created application to your repository by going to this URL:

https://github.com/apps/my-paac-application

💡 Don't forget to run "tkn pac create repository" to create a new Repository CR on your cluster.
```

Click on that URL to check out your shiny new GitHub App. If you click "App settings" on that page, you can peek under the hood and see how `tkn pac bootstrap` set everything up.  It's done all the basic configuration for you, but you can tweak the advanced settings there if you ever need to.

{{< hint info >}}
Pro-tip: The "Advanced" tab in your GitHub App settings is super handy for debugging.  You can see all the recent deliveries (messages) from GitHub to your Pipelines as Code controller.  If something's not working right, this is a great place to see if GitHub is actually sending events and what's going on.
{{< /hint >}}

### Creating a GitHub Repository -  Time to Test!

As the `tkn pac bootstrap` command hinted, you need to create a Repository CR next.  This basically tells Pipelines as Code *which* repository you want it to work with.

If you don't have a repo handy, no problem!  Here's a super easy template you can use:

<https://github.com/openshift-pipelines/pac-demo/generate>

Just pick your GitHub username (like `yourusername`) and a repository name (like `pac-demo`), then click "Create repository from template". Boom, repo created!

{{< hint info >}}
Pipelines as Code works great with [private repositories]({{< relref "/docs/guide/privaterepo.md" >}}) too, but let's keep things simple for now and stick with a public one.
{{< /hint >}}

Your new repo is now live on GitHub at <https://github.com/yourusername/pac-demo>.

### Installing the GitHub App in Your Repo -  Connecting the Dots

Now we need to link your GitHub App to your new repository.  Here's how:

1. Go to the GitHub App URL that `tkn pac bootstrap` gave you, for example: [https://github.com/apps/my-paac-application](https://github.com/apps/my-paac-application).
2. Click that "Install" button.
3. Choose the repository you just created under your username.

Here’s what it looks like for me:

![GitHub Application Installation](/images/github-app-install-application-on-repo.png)

### Clone Your Repo -  Get Ready to Code!

Let's jump back to the terminal and clone your new repo using `git`:

```bash
git clone https://github.com/$yourusername/pac-demo
cd pac-demo
```

We're navigating into the repo directory because `tkn pac` is clever and uses your git info to make the next commands easier.

## Create a Repository CR -  Tell Pipelines as Code What to Watch

{{< hint info >}}
Okay, so a Repository CR is how you tell Pipelines as Code what to do.  "CR" stands for Custom Resource, which is a way to extend Kubernetes with new types of objects.  Basically, it's a Kubernetes object that's not built-in.  In our case, we're using it to tell Pipelines as Code which Git Repository URL to watch (among other [settings]({{< relref "/docs/guide/repositorycrd.md" >}})).  You also define the namespace where your PipelineRuns will actually run.
{{< /hint >}}

Ready to create that Repository CR?  Just run:

```bash
tkn pac create repository
```

This command tries to be smart and helpful. It figures out your current Git repo info and suggests defaults.  Pretty neat, huh?

```console
? Enter the Git repository url (default: https://github.com/chmouel/pac-demo):
```

Just hit Enter to accept the default repo URL.  Then, it'll ask you about the namespace where you want your CI pipelines to run. Again, you can usually just go with the default:

```console
? Please enter the namespace where the pipeline should run (default: pac-demo-pipelines):
! Namespace pac-demo-pipelines is not found
? Would you like me to create the namespace pac-demo-pipelines? (Y/n)
```

Once you're through these prompts, it's done!  It'll create the `Repository` CR in your cluster and also create a `.tekton` directory in your repo with a `pipelinerun.yaml` file inside.

```console
ℹ Directory .tekton has been created.
✓ We have detected your repository using the programming language Go.
✓ A basic template has been created in /tmp/pac-demo/.tekton/pipelinerun.yaml, feel free to customize it.
ℹ You can test your pipeline by pushing the generated template to your git repository
```

Notice that `tkn pac create repository` figured out you're using Go!  How cool is that? It even created a basic `pipelinerun.yaml` template tailored for Go projects (it includes the [golangci-lint](https://hub.tekton.dev/tekton/task/golangci-lint) linter task).

Go ahead and open up `.tekton/pipelinerun.yaml`.  Take a look around! It's got comments to help you understand what's going on.

## Creating a Pull Request - Let's See it Run!

Okay, we've got our Repository CR set up, our `.tekton/pipelinerun.yaml` is generated... time to see Pipelines as Code in action!

First, let's create a new branch to make a Pull Request from:

```bash
git checkout -b tektonci
```

Now, commit the `.tekton/pipelinerun.yaml` file and push it to your repo:

```bash
git add .
git commit -m "Adding Tekton CI"
git push origin tektonci
```

{{< hint info >}}
Just a reminder, we're assuming you've already set up your system to push to GitHub. If not, GitHub's documentation has you covered: <https://docs.github.com/en/get-started/getting-started-with-git/setting-your-username-in-git>
{{< /hint >}}

Once you've pushed your branch, create a new Pull Request. You can usually do this right from GitHub, or go directly to this URL (make sure to replace `yourusername`): <https://github.com/yourusername/pac-demo/pull/new/tektonci>

As soon as you create the Pull Request, you should see Pipelines as Code spring into action! It'll be triggered and start running on your PR:

![GitHub Application Installation](/images/github-app-install-CI-triggered.png)

Click that "Details" link!  You can see what's happening with your PipelineRun. Pipelines as Code helpfully tells you that you can follow the logs on your Tekton Dashboard, the OpenShift Pipelines Console, or using the [tekton CLI](https://tekton.dev/docs/cli/) (`tkn`) if you prefer the command line.

When the PipelineRun finishes, you might see an error on that "Details" screen:

![GitHub Application Installation](/images/github-app-install-CI-failed.png)

Don't panic! This is actually a good thing and totally on purpose for this demo.  We've got a linting error in the Go code, and GolangCI spotted it.  See how the error message even links to the line of code that's causing trouble? Pipelines as Code is pretty smart – it analyzes the PipelineRun logs and tries to pinpoint the exact line of code with the issue to make fixing it easier.

![GitHub Application Installation](/images/github-app-matching-annotations.png)

### Fixing the Error and Pushing Again -  Let's Make it Green!

Let's fix that error! Back to your terminal.

Edit `main.go`, select all the code, and paste this in:

```go
package main

import (
    "fmt"
    "os"
)

func main() {
    fmt.Fprintf(os.Stdout, "Hello world")
}
```

Commit the change and push it to your branch:

```bash
git commit -a -m "Errare humanum est, ignoscere divinum."
git push origin tektonci
```

Head back to your browser, and you should see the PipelineRun triggered again.  This time... success!  Green checkmark!

![GitHub Application Installation](/images/github-app-install-CI-succeeded.png)

## Conclusion - You Did It!

Awesome job! You've successfully set up Pipelines as Code to run Continuous Integration on your repository.  Pat yourself on the back! Now you're free to [customize]({{< relref "/docs/guide/authoringprs.md" >}}) your `.tekton/pipelinerun.yaml` file to your heart's content and add any extra tasks you need. The sky's the limit!

### Quick Recap

In this guide, we:

- [Installed]({{< relref "/docs/install/installation" >}}) Pipelines as Code on your Kubernetes cluster.
- Created a [GitHub Application]({{< relref "/docs/install/github_apps" >}}) to connect GitHub and Pipelines as Code.
- Created a [Repository CR]({{< relref "/docs/guide/repositorycrd" >}}) to tell Pipelines as Code which repo to watch.
- Generated a Pipelines as Code [PipelineRun]({{< relref "/docs/guide/authoringprs" >}}) template.
- [Ran the PipelineRun]({{< relref "/docs/guide/running" >}}) automatically on your Pull Request.

You're all set! Happy pipelining!

---
title: Running Multiple GitHub Apps
---

# Got more than one GitHub App? No problem!

{{< tech_preview "Multiple GitHub apps support" >}}

Good news! Pipelines-as-Code can handle multiple GitHub applications on the same cluster.  This is super handy if you're using different GitHub setups, like both public GitHub and GitHub Enterprise, and want them all pointing to the same cluster.

## Setting up a Second Controller for a Different GitHub App

If you're adding another GitHub App, it gets its very own controller.  Think of it as each app having its own little traffic controller. This controller comes with its own Service and either an [Ingress](https://kubernetes.io/docs/concepts/services-networking/ingress/) or an [OpenShift Route](https://docs.openshift.com/container-platform/latest/networking/routes/route-configuration.html) to handle incoming requests.

Each of these controllers is self-contained. It can have its own [ConfigMap]({{< relref "/docs/install/settings" >}}) for settings, and definitely needs its own Secret. This Secret holds the GitHub App's `private key`, `application_id`, and `webhook_secret`.  You can find out how to set up these Secrets [here]({{< relref "/docs/install/github_apps#manual-setup" >}}).

To make things tick, each controller uses these environment variables in its container:

| Environment Variable       | What it does                                          | Example Value   |
|----------------------------|-------------------------------------------------------|-----------------|
| `PAC_CONTROLLER_LABEL`     |  A unique name to tell this controller apart          | `ghe`           |
| `PAC_CONTROLLER_SECRET`    |  Points to the Kubernetes Secret with your GitHub App info | `ghe-secret`    |
| `PAC_CONTROLLER_CONFIGMAP` |  Points to the ConfigMap for Pipelines-as-Code settings | `ghe-configmap` |

{{< hint info >}}
Important thing to remember: While you need a controller for each GitHub App, you only need *one* `watcher`.  The watcher is like the overall supervisor that checks on things and updates GitHub.
{{< /hint >}}

## Need a Hand? We've Got a Script!

To make setting up that second controller easier, we've created a handy script.  It's designed to deploy the controller, its Service, and ConfigMap – and even sets those environment variables for you!

You can find it in our source code repo, in the `./hack` directory. The script's name is [second-controller.py](https://github.com/openshift-pipelines/pipelines-as-code/blob/main/hack/second-controller.py).

Here's how to use it:

First, grab the Pipelines-as-Code repository:

```shell
git clone https://github.com/openshift-pipelines/pipelines-as-code
```

Before running the script, you'll need the `python-yaml` module.  You might already have it, but if not, you can install it using your system's package manager or with pip:

```shell
python3 -mpip install PyYAML
```

Now, run the script like this, replacing `LABEL` with a label for your controller (like `ghe` for GitHub Enterprise):

```shell
python3 ./hack/second-controller.py LABEL
```

This will spit out the YAML configuration to your screen.  If it looks good, you can apply it to your Kubernetes cluster directly with `kubectl`:

```shell
python3 ./hack/second-controller.py LABEL | kubectl -f-
```

Want to tweak things? The script has a bunch of options! Just use the `--help` flag to see all the cool things you can do with it.

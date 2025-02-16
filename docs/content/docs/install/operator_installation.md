Okay, here's a human-friendly rewrite of that documentation about installing Pipelines-as-Code with the Operator:

```markdown
---
title: Installing with the Operator (Easy Mode!)
weight: 2.1
---

# Installing with the Operator (Easy Mode!)

Want the simplest way to get Pipelines-as-Code up and running on OpenShift?  Using the [Red Hat OpenShift Pipelines Operator](https://docs.openshift.com/container-platform/latest/cicd/pipelines/installing-pipelines.html) is definitely the way to go.  It handles a lot of the heavy lifting for you!

By default, the OpenShift Pipelines Operator sets up shop in the `openshift-pipelines` namespace.  Just something to keep in mind.

**Heads Up! (Important Note)**

If you happened to install Pipelines-as-Code using the [Tekton Operator](https://github.com/tektoncd/operator), things work a little differently.  In that case, your Pipelines-as-Code settings are actually managed by something called the [TektonConfig Custom Resource](https://github.com/tektoncd/operator/blob/main/docs/TektonConfig.md#openshiftpipelinesascode).

Think of it this way: the Tekton Operator is in charge.  If you try to directly tweak the `pipeline-as-code` configmap or the `OpenShiftPipelinesAsCode` custom resource yourself, the Tekton Operator will just set things back to how it wants them, based on its `TektonConfig`.  So, you'll want to make your changes in the `TektonConfig` instead.

To give you an idea, here's what the default Pipelines-as-Code settings look like inside the `TektonConfig`:

```yaml
apiVersion: operator.tekton.dev/v1alpha1
kind: TektonConfig
metadata:
  name: config
spec:
  platforms:
    openshift:
      pipelinesAsCode:
        enable: true
        settings:
          bitbucket-cloud-check-source-ip: 'true'
          remote-tasks: 'true'
          application-name: Pipelines-as-Code CI
          auto-configure-new-github-repo: 'false'
          error-log-snippet: 'true'
          error-detection-from-container-logs: 'false'
          hub-url: 'https://api.hub.tekton.dev/v1'
          hub-catalog-name: tekton
          error-detection-max-number-of-lines: '50'
          error-detection-simple-regexp: >-
            ^(?P<filename>[^:]*):(?P<line>[0-9]+):(?P<column>[0-9]+):([
            ]*)?(?P<error>.*)
          secret-auto-create: 'true'
          secret-github-app-token-scoped: 'true'
          remember-ok-to-test: 'true'
```

See that `settings` section?  That's where you can add or change any of the Pipelines-as-Code configuration options.  Just update the `TektonConfig` custom resource, and the operator will automatically update the `pipelines-as-code` configmap to match.  Pretty neat, huh?

**Quick Notes on Enabling and Disabling:**

By default, the Tekton Operator is set to install Pipelines-as-Code.  You can see that in the `enable: true` part in the example below:

```yaml
spec:
  platforms:
    openshift:
      pipelinesAsCode:
        enable: true
        settings:
```

If you ever want to turn off Pipelines-as-Code installation (maybe for testing or something?), you can simply set `enable: false` like this:

```yaml
spec:
  platforms:
    openshift:
      pipelinesAsCode:
        enable: false
        settings:
```

And that's all there is to it for installing Pipelines-as-Code via the Operator!  Easy peasy.

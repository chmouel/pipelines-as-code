---
title: Global Repository Settings
weight: 4
---
{{< tech_preview "Global repository settings" >}}

## Pipelines-as-Code: Global Repository Settings

Ever wished you could set up Pipelines-as-Code settings just *once* and have them apply to all your repositories?  Well, with global repository settings, you can! Think of it as a central settings hub for all your repositories on the cluster.  If a repository doesn't have its own specific settings, it'll automatically use these global ones as a fallback.

You'll need to create this global repository in the same namespace where your `pipelines-as-code` controller lives (usually `pipelines-as-code` or `openshift-pipelines`).  Interestingly, this global repository doesn't actually *need* a real URL in its `spec.url` field. You can leave it blank, or even point it to a fake address like `<https://pac.global.repo>` – Pipelines-as-Code won't mind!

By default, name your global repository `pipelines-as-code`.  Unless, of course, you want to get fancy and rename it. In that case, you can tweak the `PAC_CONTROLLER_GLOBAL_REPOSITORY` environment variable in your controller and watcher Deployment.

So, what kind of settings can you actually define globally? Here's the rundown:

- [Concurrency Limit]({{< relref "/docs/guide/repositorycrd.md#concurrency" >}}).
- [PipelineRun Provenance]({{< relref "/docs/guide/repositorycrd.md#pipelinerun-definition-provenance" >}}).
- [Repository Policy]({{< relref "/docs/guide/policy" >}}).
- [Repository GitHub App Token Scope]({{< relref "/docs/guide/repositorycrd.md#scoping-the-github-token-using-global-configuration" >}}).
- Git provider auth settings like user, token, URL, etc.
  -  Just a heads up, the `type` needs to be defined in your namespace repository settings and match the `type` in the global repository (example below!).
- [Custom Parameters]({{< relref "/docs/guide/customparams.md" >}}).
- [Incoming Webhooks Rules]({{< relref "/docs/guide/incoming_webhook.md" >}}).

{{< hint info >}}
**Important Note:** Global settings only kick in when Pipelines-as-Code is triggered by a Git event (like a webhook). If you're using the `tkn pac` command-line tool, these global settings won't apply. Just something to keep in mind!
{{< /hint >}}

### How Global Settings Actually Work: An Example

Let's walk through a quick example to make this clearer. Imagine you have a Repository CR in your `user-namespace` that looks like this:

```yaml
apiVersion: pipelinesascode.tekton.dev/v1alpha1
kind: Repository
metadata:
  name: repo
  namespace: user-namespace
spec:
  url: "https://my.git.com"
  concurrency_limit: 2
  git_provider:
    type: gitlab
```

And then you've got your global Repository CR chilling in the controller's namespace, like so:

```yaml
apiVersion: pipelinesascode.tekton.dev/v1alpha1
kind: Repository
metadata:
  name: pipelines-as-code
  namespace: pipelines-as-code
spec:
  url: "https://paac.repo"
  concurrency_limit: 1
  params:
    - name: custom
      value: "value"
  git_provider:
    type: gitlab
    secret:
      name: "gitlab-token"
    webhook_secret:
      name: gitlab-webhook-secret
```

Now, what happens?  Well, in this case, the `repo` in `user-namespace` will use a concurrency limit of 2.  Why? Because it's defined *locally* and overrides the global setting.  However, the `custom` parameter? That *will* come from the global settings, set to `value` – unless your local repo defines its own parameters.  And because both repos specify `git_provider.type: gitlab`, the Git provider details (like secrets!) will be pulled from the global repository.  The secret itself will be looked up in the namespace where the global repo is defined.

### Global Settings for Webhook-Based Git Providers

These `spec.git_provider.type` settings are specifically for defining your Git provider in global settings. They're used when Pipelines-as-Code receives webhook events or when it's looking at those global repo settings.  Basically, they're for Git providers that work with webhooks (so, everything *except* GitHub Apps installations).  For instance, if you set the `type` to `github`, it means you're using good ol' [GitHub webhooks]({{< relref "/docs/install/github_webhook.md" >}}). Here's the list of types you can use:

- github
- gitlab
- gitea
- bitbucket-cloud
- bitbucket-server

Heads up:  Currently, your global repository settings can only point to *one* type of Git provider across your whole cluster.  If you want to use a different provider for a specific repository, or just want to manage provider details locally, you'll need to define the provider information directly in that repository's CR.

---
title: Incoming Webhook
weight: 50
---

# Incoming webhook

Want to kick off your Tekton pipelines without pushing code every time? Incoming webhooks in Pipelines as Code are your friend! They let you trigger a pipeline run in your repository using a secret URL – no new code commit needed.

## Incoming Webhook URL

To get started with incoming webhooks, you need to tweak your Repository Custom Resource Definition (CRD).  In the `incoming` section, you'll point to a `Secret` (that's your shared password!) and tell Pipelines as Code which branches should respond to these webhook calls.  Once that's set, Pipelines as Code will look in your `.tekton` directory for `PipelineRuns` that are set up to respond to `incoming` or `push` events using the `on-event` annotation.

{{< hint info >}}
**Heads up!** If you're not using the GitHub App (i.e., you're using webhook-based providers), you'll need to add a `git_provider` section in your Repository CRD and specify a token. Also, because Pipelines as Code can't always guess your provider from the URL, you'll need to tell it what type it is in `git_provider.type`.  The options are: `github`, `gitlab`, or `bitbucket-cloud`.  If you *are* using GitHub Apps, you can skip this part.
{{< /hint >}}

### GithubApp

Let's see how this works with GitHub Apps.  Imagine you want to trigger a PipelineRun when something happens (but not a code push!).  First, in your Repository CR, you tell Pipelines as Code you're watching the `main` branch and setting up an incoming webhook with a secret password stored in a Secret called `repo-incoming-secret`:

```yaml
---
apiVersion: "pipelinesascode.tekton.dev/v1alpha1"
kind: Repository
metadata:
  name: repo
  namespace: ns
spec:
  url: "https://github.com/owner/repo"
  incoming:
    - targets:
        - main
      secret:
        name: repo-incoming-secret
      type: webhook-url
```

Next, you need a `PipelineRun` that knows to listen for these incoming webhook events on the `main` branch:

```yaml
apiVersion: tekton.dev/v1
kind: PipelineRun
metadata:
  name: target_pipelinerun
  annotations:
    pipelinesascode.tekton.dev/on-event: "[incoming]"
    pipelinesascode.tekton.dev/on-target-branch: "[main]"
```

And of course, you need that `repo-incoming-secret` Secret with your super-secret password to keep things secure:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: repo-incoming-secret
  namespace: ns
type: Opaque
stringData:
  secret: very-secure-shared-secret
```

Once all that's in place, you can trigger your `PipelineRun` by sending a `POST` request to the Pipelines as Code controller URL, adding `/incoming` at the end.  Make sure to include your secret, repository name (`repo`), branch (`main`), and the name of your `PipelineRun` in the request.  If you want Pipelines as Code to generate a name for your `PipelineRun`, you can use `generateName` but remember to add a hyphen at the end!  Here’s a `curl` example to get you started:

```shell
curl -X POST 'https://control.pac.url/incoming?secret=very-secure-shared-secret&repository=repo&branch=main&pipelinerun=target_pipelinerun'
```

Notice a couple of things in that command: we're using `"/incoming"` in the URL, and it's a `POST` request, not a `GET`.

**Important:** When your webhook triggers a `PipelineRun`, Pipelines as Code treats it *just like* a regular code push event.  This means you still get all the cool status reporting features!  Want to get notified or see the status? You can add a `finally` task to your Pipeline, or check the Repository CR using the `tkn pac CLI`.  Check out the [statuses](/docs/guide/statuses) docs for all the details.

### Passing dynamic parameter value to incoming webhook

Want to get fancy and pass in custom values to your `PipelineRun` when you trigger it with a webhook?  No problem! You can set values for *any* Pipelines as Code parameters, even the built-in ones.  Just list the parameters you want to use in the `params` section of your Repository CR configuration. Then, when you send your webhook request, include those values in JSON format in the request body.  Don't forget to set the `Content-Type` header to `application/json`.  Here's an example Repository CR that lets you pass in a `pull_request_number`:

```yaml
---
apiVersion: "pipelinesascode.tekton.dev/v1alpha1"
kind: Repository
metadata:
  name: repo
  namespace: ns
spec:
  url: "https://github.com/owner/repo"
  incoming:
    - targets:
        - main
      params:
        - pull_request_number
      secret:
        name: repo-incoming-secret
      type: webhook-url
```

And here's how you'd send that `pull_request_number` value with `curl`:

```shell
curl -H "Content-Type: application/json" -X POST "https://control.pac.url/incoming?repository=repo&branch=main&secret=very-secure-shared-secret&pipelinerun=target_pipelinerun" -d '{"params": {"pull_request_number": "12345"}}'
```

Now, anywhere in your `PipelineRun` you use `{{pull_request_number}}`, it'll be replaced with `12345`.  Cool, right?

### Using incoming webhook with GitHub Enterprise application

Using a GitHub App with GitHub Enterprise?  No sweat.  Just need to add one extra header to your webhook requests: `X-GitHub-Enterprise-Host`.  For example, with `curl`:

```shell
curl -H "X-GitHub-Enterprise-Host: github.example.com" -X POST "https://control.pac.url/incoming?repository=repo&branch=main&secret=very-secure-shared-secret&pipelinerun=target_pipelinerun"
```

### Using incoming webhook with webhook based providers

If you're using webhook-based providers like GitHub Webhooks, GitLab, or Bitbucket, incoming webhooks work great too!  They'll use the token you already set up in the `git_provider` section.  Here’s a Repository CR example for a GitHub webhook provider, targeting the `main` branch:

```yaml
apiVersion: "pipelinesascode.tekton.dev/v1alpha1"
kind: Repository
metadata:
  name: repo
  namespace: ns
spec:
  url: "https://github.com/owner/repo"
  git_provider:
    type: github
    secret:
      name: "owner-token"
  incoming:
    - targets:
        - main
      secret:
        name: repo-incoming-secret
      type: webhook-url
```

And just like we said before, you'll still need to set up that `repo-incoming-secret` Secret.

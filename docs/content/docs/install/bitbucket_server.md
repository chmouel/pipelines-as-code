---
title: Bitbucket Server
weight: 15
---
# Get Pipelines-as-Code Running on Bitbucket Server

Good news! Pipelines-as-Code totally plays nice with [Bitbucket Server](https://www.atlassian.com/software/bitbucket/enterprise).  Let's get you set up.

Assuming you've already gone through the main [installation](/docs/install/installation) steps, here's what's next for Bitbucket Server:

* **First things first, you'll need a personal token.**  Think of it as your special key to let Pipelines-as-Code talk to Bitbucket Server.  If you're the project manager, you can create one by following these steps:

<https://confluence.atlassian.com/bitbucketserver/personal-access-tokens-939515499.html>

Make sure this token has both `PROJECT_ADMIN` and `REPOSITORY_ADMIN` permissions.  Why? Because Pipelines-as-Code needs to tinker with project settings and repo stuff.

**Important Note:** This token needs to be able to see forked repositories in pull requests.  Otherwise, it'll be blind to changes in those pull requests, and things won't work as expected.

Pro-tip: Jot down that token somewhere safe!  If you lose it, you'll have to create a new one.

* **Next up, let's set up a Webhook in your repository.**  Webhooks are like little messengers that tell Pipelines-as-Code whenever something interesting happens in your repo (like code being pushed or a pull request opening).  Here’s Atlassian's guide on how to create one:

<https://support.atlassian.com/bitbucket-cloud/docs/manage-webhooks/>

* **Time for a Secret!**  You can either use a secret you already have or generate a random one.  If you're going random, this command will do the trick:

```shell
  head -c 30 /dev/random | base64
```

* **Point that Webhook to Pipelines-as-Code.** You'll need to give the Webhook the public URL of your Pipelines-as-Code setup.  If you're running on OpenShift, you can grab that URL with this command:

  ```shell
  echo https://$(oc get route -n pipelines-as-code pipelines-as-code-controller -o jsonpath='{.spec.host}')
  ```

* **Check out [this screenshot](/images/bitbucket-server-create-webhook.png)** to see which events your Webhook needs to watch out for.  Specifically, you'll want to select these:

  * Repository -> Push
  * Repository -> Modified
  * Pull Request -> Opened
  * Pull Request -> Source branch updated
  * Pull Request -> Comments added

  Basically, we want to know when code is pushed, repos are tweaked, pull requests pop up, branches in pull requests change, and when people comment on pull requests.

  * **Now, let's create a secret in your `target-namespace`** to store that personal token we made earlier.  Run this command, replacing `TOKEN_AS_GENERATED_PREVIOUSLY` and `SECRET_AS_SET_IN_WEBHOOK_CONFIGURATION` with your actual token and webhook secret:

  ```shell
  kubectl -n target-namespace create secret generic bitbucket-server-webhook-config \
    --from-literal provider.token="TOKEN_AS_GENERATED_PREVIOUSLY" \
    --from-literal webhook.secret="SECRET_AS_SET_IN_WEBHOOK_CONFIGURATION"
  ```

* **Last step: create a Repository CRD and tell it to use that secret.**  This is how Pipelines-as-Code knows where to find your Bitbucket Server details.

  * Here’s an example of what your Repository CRD might look like:

```yaml
  ---
  apiVersion: "pipelinesascode.tekton.dev/v1alpha1"
  kind: Repository
  metadata:
    name: my-repo
    namespace: target-namespace
  spec:
    url: "https://bitbucket.com/workspace/repo"
    git_provider:
      # Double-check your Bitbucket Server API URL. It's usually your base URL with "/rest" at the end, *not* "/api/v1.0".
      # A default install will often have a "/rest" suffix.
      url: "https://bitbucket.server.api.url/rest"
      user: "your-bitbucket-username"
      secret:
        name: "bitbucket-server-webhook-config"
        # If your token key in the secret is different from "provider.token", specify it here:
        # key: "provider.token"
      webhook_secret:
        name: "bitbucket-server-webhook-config"
        # If your webhook secret key in the secret is different from "webhook.secret", specify it here:
        # key: "webhook.secret"
```

## Quick Notes

* Heads up! The `git_provider.secret` needs to be in the *same namespace* as your Repository CRD. Pipelines-as-Code always assumes they're neighbors.

* Just so you know, the `tkn-pac create` and `bootstrap` commands aren't currently supported on Bitbucket Server. You'll need to set things up manually as described above.

{{< hint danger >}}

* One more thing about owners:  When you're setting up owners, you can only refer to users by their `ACCOUNT_ID`. Keep that in mind!

{{< /hint >}}

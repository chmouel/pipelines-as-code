---
title: GitLab
weight: 13
---

# GitLab and Pipelines-as-Code: Better Together!

Want to use Pipelines-as-Code with GitLab? Awesome! You can totally hook them up using a webhook.

First things first, make sure you've got Pipelines-as-Code installed on your Kubernetes cluster. If you haven't already, jump over to the [installation guide](/docs/install/installation) and get that sorted.

## Grab a GitLab Personal Access Token

You'll need a personal access token in GitLab to make this work. Think of it as a password just for Pipelines-as-Code to talk to GitLab.

*  Head over to this GitLab page to create one:

   <https://docs.gitlab.com/ee/user/profile/personal_access_tokens.html>

   **Important Note:**  When you're creating this token, you might be tempted to scope it just to your project.  However, for things to run smoothly – especially when dealing with Merge Requests from forked repos – the token needs broader `api` access. If you use a project-scoped token, it might stumble when trying to update the Merge Request status directly. Don't worry, even if it can't update the MR directly, Pipelines-as-Code is smart enough to still show you the pipeline status as a comment on your Merge Request. Pretty neat, huh?

## Setting up your Repository and Webhook: Two Ways to Roll

You've got a couple of options to get your `Repository` set up and the webhook configured. Pick whichever way feels more your style!

### Option 1: The Speedy `tkn pac` Tool Method

* If you're a command-line ninja, the [`tkn pac create repo`](/docs/guide/cli) command is your friend. It'll whip up a `Repository` custom resource (CR) and configure the webhook for you in a flash.

  For this to work its magic, you'll need that personal access token we talked about, but this time it needs to have `admin:repo_hook` scope.  `tkn pac` uses this token to set up the webhook and tucks it away securely in your cluster as a secret. Pipelines-as-Code controller will then use this secret to access your repository.  Think of it as giving Pipelines-as-Code the keys to the GitLab kingdom, but only for the parts it needs!

  Here’s a sneak peek at how the `tkn pac create repo` command looks in action:

  ```shell script
  $ tkn pac create repo

  ? Enter the Git repository url (default: https://gitlab.com/repositories/project):
  ? Please enter the namespace where the pipeline should run (default: project-pipelines):
  ! Namespace project-pipelines is not found
  ? Would you like me to create the namespace project-pipelines? Yes
  ✓ Repository repositories-project has been created in project-pipelines namespace
  ✓ Setting up GitLab Webhook for Repository https://gitlab.com/repositories/project
  ? Please enter the project ID for the repository you want to be configured,
    project ID refers to an unique ID (e.g. 34405323) shown at the top of your GitLab project : 17103
  👀 I have detected a controller url: https://pipelines-as-code-controller-openshift-pipelines.apps.awscl2.aws.ospqa.com
  ? Do you want me to use it? Yes
  ? Please enter the secret to configure the webhook for payload validation (default: lFjHIEcaGFlF):  lFjHIEcaGFlF
  ℹ ️You now need to create a GitLab personal access token with `api` scope
  ℹ ️Go to this URL to generate one https://gitlab.com/-/profile/personal_access_tokens, see https://is.gd/rOEo9B for documentation
  ? Please enter the GitLab access token:  **************************
  ? Please enter your GitLab API URL:  https://gitlab.com
  ✓ Webhook has been created on your repository
  🔑 Webhook Secret repositories-project has been created in the project-pipelines namespace.
  🔑 Repository CR repositories-project has been updated with webhook secret in the project-pipelines namespace
  ℹ Directory .tekton has been created.
  ✓ A basic template has been created in /home/Go/src/gitlab.com/repositories/project/.tekton/pipelinerun.yaml, feel free to customize it.
  ℹ You can test your pipeline by pushing the generated template to your git repository
  ```

### Option 2: The Manual, Hands-On Approach

* Prefer doing things yourself? No problem! You can manually set up the webhook in GitLab and then create the `Repository` CR.

  *  In your GitLab project, look for **Settings** in the left sidebar, and then click on **Webhooks**.

  *  You'll need the public URL of your Pipelines-as-Code controller. If you're on OpenShift, you can grab it with this command:

    ```shell
    echo https://$(oc get route -n pipelines-as-code pipelines-as-code-controller -o jsonpath='{.spec.host}')
    ```

  *  Next, you need to create a secret for webhook validation. You can either come up with your own super-secret string or generate a random one using this command:

    ```shell
    head -c 30 /dev/random | base64
    ```

  *  Take a peek at [this screenshot](/images/gitlab-add-webhook.png) for a visual guide on setting up the webhook.

    Make sure to select these individual events to trigger your pipelines:

    * Merge request Events
    * Push Events
    * Comments
    * Tag push events

  *  Finally, hit **Add webhook**. You're halfway there!

*  Now, let's create that [`Repository CRD`](/docs/guide/repositorycrd).  This CR will need to know about two Kubernetes Secrets: one for your Personal Access Token and another for the webhook secret you just set in GitLab. This is how Pipelines-as-Code knows it's talking to the right GitLab repo and that the webhook calls are legit.

*  Time to create those secrets in the namespace where you plan to run your CI pipelines (let's call it `target-namespace`):

  ```shell
  kubectl -n target-namespace create secret generic gitlab-webhook-config \
    --from-literal provider.token="YOUR_GITLAB_PERSONAL_ACCESS_TOKEN" \
    --from-literal webhook.secret="YOUR_WEBHOOK_SECRET"
  ```
  **Important:** Replace `YOUR_GITLAB_PERSONAL_ACCESS_TOKEN` and `YOUR_WEBHOOK_SECRET` with the actual values you generated earlier.

*  Lastly, create the `Repository` CR, referencing the secret you just made. Here’s an example:

  ```yaml
  ---
  apiVersion: "pipelinesascode.tekton.dev/v1alpha1"
  kind: Repository
  metadata:
    name: my-repo
    namespace: target-namespace
  spec:
    url: "https://gitlab.com/group/project"
    git_provider:
      # url: "https://gitlab.example.com/ # If you're using a private GitLab instance, uncomment and set this
      secret:
        name: "gitlab-webhook-config"
        # key: "provider.token" # If your secret key is different, uncomment and set this
      webhook_secret:
        name: "gitlab-webhook-config"
        # key: "webhook.secret" # If your secret key is different, uncomment and set this
  ```

## Quick Notes to Keep in Mind

*  Using a private GitLab instance? Pipelines-as-Code isn't automatically going to figure that out *just yet*. You'll need to tell it the API URL explicitly by setting `git_provider.url` in your `Repository` spec.

*  Want to override the default GitLab API URL for any reason?  Just pop it into that `git_provider.url` field. Easy peasy.

*  Heads up! The `git_provider.secret` has to live in the *same* namespace as your `Repository` CR. Pipelines-as-Code always assumes they're neighbors.

## Need to Add a Webhook Secret Later?

*  If you've got an existing `Repository` and you need to add a webhook secret (maybe it got deleted, or you're setting up a new webhook in GitLab), `tkn pac webhook add` to the rescue!  This command adds the webhook to your GitLab project settings and updates the `webhook.secret` in your existing `Secret` object – all without messing with your `Repository` CR.

  Here’s how it looks:

  ```shell script
  $ tkn pac webhook add -n project-pipelines

  ✓ Setting up GitLab Webhook for Repository https://gitlab.com/repositories/project
  ? Please enter the project ID for the repository you want to be configured,
    project ID refers to an unique ID (e.g. 34405323) shown at the top of your GitLab project : 17103
  👀 I have detected a controller url: https://pipelines-as-code-controller-openshift-pipelines.apps.awscl2.aws.ospqa.com
  ? Do you want me to use it? Yes
  ? Please enter the secret to configure the webhook for payload validation (default: TXArbGNDHTXU):  TXArbGNDHTXU
  ✓ Webhook has been created on your repository
  🔑 Secret repositories-project has been updated with webhook secret in the project-pipelines namespace.
  ```

**Remember:** If your `Repository` isn't in the `default` namespace, use `tkn pac webhook add [-n namespace]`. In the example above, the `Repository` is in `project-pipelines`, so we use `-n project-pipelines`.

## Time for a Token Refresh?

Got a new personal access token?  No sweat, here's how to update it.

### Option 1: `tkn pac webhook update-token` to the Rescue!

*  The [`tkn pac webhook update-token`](/docs/guide/cli) command is your quick and easy way to swap out the old token for the new one in your `Repository` CR.

  Like this:

  ```shell script
  $ tkn pac webhook update-token -n repo-pipelines

  ? Please enter your personal access token:  **************************
  🔑 Secret repositories-project has been updated with new personal access token in the project-pipelines namespace.
  ```

**Again:** If your `Repository` is in a namespace other than `default`, don't forget to use `tkn pac webhook add [-n namespace]`.

### Option 2:  YAML Edit or `kubectl patch` – The Manual Update

If you prefer a more hands-on approach, or just like editing YAML directly, you can update the token that way too.

First, you'll need to find the name of the secret in your `Repository` CR.  It's usually under `spec.git_provider.secret.name`.

  ```yaml
  spec:
    git_provider:
      # url: "https://gitlab.example.com/ # Set this if you are using a private GitLab instance
      secret:
        name: "gitlab-webhook-config"
  ```

Then, replace `$NEW_TOKEN` and `$target_namespace` in the command below with your new token and the namespace where your secret lives:

```shell
kubectl -n $target_namespace patch secret gitlab-webhook-config -p "{\"data\": {\"provider.token\": \"$(echo -n $NEW_TOKEN|base64 -w0)\"}}"
```

And that's it! You're all set to use Pipelines-as-Code with GitLab webhooks. Happy pipelining! 🎉

---
title: GitHub Webhook
weight: 12
---

# Pipelines-as-Code with GitHub Webhooks (No App Needed!)

Want to use Pipelines-as-Code but can't (or don't want to) set up a full GitHub Application? No problem! You can totally get things rolling using good ol' [GitHub Webhooks](https://docs.github.com/en/developers/webhooks-and-events/webhooks/creating-webhooks) directly on your repository.

Now, heads up: going the webhook route means you won't have access to the fancy [GitHub CheckRun API](https://docs.github.com/en/rest/guides/getting-started-with-the-checks-api).  Instead of seeing your task statuses in the "Checks" tab, they'll show up as good old comments right on your Pull Request.  Still easy to follow, just a bit different.

Also, those handy `gitops` commands like `/retest` or `/ok-to-test`?  They won't work with webhooks, unfortunately.  If you need to kick off your CI again, you'll need to make a fresh commit.  Don't worry, it can be super quick!  Here's a little command snippet to speed things up (just swap out `branchname` with your actual branch name):

```console
git commit --amend -a --no-edit && git push --force-with-lease origin branchname
```

## First Step: Grab a GitHub Personal Access Token

After you've got Pipelines-as-Code [installed](/docs/install/installation), you're going to need a GitHub personal access token.  This is basically a key that lets Pipelines-as-Code talk to the GitHub API and do its thing.

GitHub has a guide to walk you through creating one of these tokens right here:

<https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/creating-a-personal-access-token>

### Fancy Fine-Grained Tokens (Recommended!)

If you're all about extra security (and who isn't?), you can create a "fine-grained" token.  These are cooler because you can limit them to only work on the specific repository you want to test.

Here's the permission checklist you'll need:

| Name            | Access         |
|:---------------:|:--------------:|
| Administration  | Read Only      |
| Metadata        | Read Only      |
| Content         | Read Only      |
| Commit statuses | Read and Write |
| Pull request    | Read and Write |
| Webhooks        | Read and Write |

### Old-School "Classic" Tokens

If you're going with the "classic" token route, the permissions depend on whether your repository is public or private:

* **Public Repos:** You just need the `public_repo` scope. Easy peasy!
* **Private Repos:** You'll need the full `repo` scope.

{{< hint info >}}
Want to skip a step?  Click this link – it'll pre-select the permissions you need for a classic token!
<https://github.com/settings/tokens/new?description=pipelines-as-code-token&scopes=repo>
{{< /hint  >}}

Make sure you jot down that token somewhere safe right after you create it. GitHub only shows it to you once! If you lose it, you'll have to make a new one.

For better security, you probably want to set your token to expire after a while (like the default 30 days). GitHub will send you a heads-up email when it's about to expire.  Don't worry, we'll cover how to [update your token](#update-token) when that happens.

**Important Note:**  Planning to use the command-line tool (`tkn pac`) to set up your webhook?  Then you'll also need to add the `admin:repo_hook` scope to your token.

## Setting Up Your Repository and Webhook

Alright, let's get your repository hooked up! You've got two main ways to do this:

### Option 1: The Speedy `tkn pac` Tool Method

* The easiest way is to use the [`tkn pac create repo`](/docs/guide/cli) command.  This handy tool will not only create the `Repository` resource in Kubernetes but also automatically configure the webhook for you!

  Remember that personal access token you made earlier?  If you're using `tkn pac create repo`, you'll need one with the `admin:repo_hook` scope.  `tkn pac` uses this to set up the webhook initially and then stores it securely in your cluster.  After the webhook is set up, you can actually switch to a token with fewer permissions (just the ones we talked about in the [token section](#create-github-personal-access-token)).

Here's a sneak peek at what using `tkn pac create repo` looks like:

```shell script
$ tkn pac create repo

? Enter the Git repository url (default: https://github.com/owner/repo):
? Please enter the namespace where the pipeline should run (default: repo-pipelines):
! Namespace repo-pipelines is not found
? Would you like me to create the namespace repo-pipelines? Yes
✓ Repository owner-repo has been created in repo-pipelines namespace
✓ Setting up GitHub Webhook for Repository https://github.com/owner/repo
👀 I have detected a controller url: https://pipelines-as-code-controller-openshift-pipelines.apps.awscl2.aws.ospqa.com
? Do you want me to use it? Yes
? Please enter the secret to configure the webhook for payload validation (default: sJNwdmTifHTs):  sJNwdmTifHTs
ℹ ️You now need to create a GitHub personal access token, please checkout the docs at https://is.gd/KJ1dDH for the required scopes
? Please enter the GitHub access token:  ****************************************
✓ Webhook has been created on repository owner/repo
🔑 Webhook Secret owner-repo has been created in the repo-pipelines namespace.
🔑 Repository CR owner-repo has been updated with webhook secret in the repo-pipelines namespace
ℹ Directory .tekton has been created.
✓ We have detected your repository using the programming language Go.
✓ A basic template has been created in /home/Go/src/github.com/owner/repo/.tekton/pipelinerun.yaml, feel free to customize it.
ℹ You can test your pipeline by pushing the generated template to your git repository

```

### Option 2: The Manual Webhook Setup

* If you prefer to do things by hand, head over to your repository (or organization) **Settings** then **Webhooks** and hit that **Add webhook** button.

  * For the **Payload URL**, you'll need the public address of your Pipelines-as-Code controller.  If you're on OpenShift, you can grab it with this command:

    ```shell
    echo https://$(oc get route -n pipelines-as-code pipelines-as-code-controller -o jsonpath='{.spec.host}')
    ```

  * Make sure to set the **Content type** to **application/json**.

  * Now for the **Webhook secret**. You can either type in your own secret or generate a random one using this command (and remember to keep it safe!):

    ```shell
    head -c 30 /dev/random | base64
    ```

  * Under "Which events would you like to trigger this webhook?", choose "Let me select individual events" and check these boxes:
    * Commit comments
    * Issue comments
    * Pull request
    * Pushes

    {{< hint info >}}
    Double-check your webhook settings against [this screenshot](/images/pac-direct-webhook-create.png) just to be sure everything is set up right.
    {{< /hint >}}

  * Finally, click **Add webhook**.

* Next up, you need to create a [`Repository CRD`](/docs/guide/repositorycrd) in Kubernetes. This tells Pipelines-as-Code about your repository and how to connect to it.

  This `Repository` resource will need to point to two Kubernetes **Secrets**: one holding your personal access token and the other holding the webhook secret you just set up.

* Let's create those Secrets in the namespace where you want your pipelines to run (we'll call it `target-namespace`):

  ```shell
  kubectl -n target-namespace create secret generic github-webhook-config \
    --from-literal provider.token="YOUR_GITHUB_TOKEN_HERE" \
    --from-literal webhook.secret="YOUR_WEBHOOK_SECRET_HERE"
  ```

  **(Replace `YOUR_GITHUB_TOKEN_HERE` and `YOUR_WEBHOOK_SECRET_HERE` with the actual values!)**

* Now, create the `Repository CRD` itself.  Here's a basic example:

  ```yaml
  ---
  apiVersion: "pipelinesascode.tekton.dev/v1alpha1"
  kind: Repository
  metadata:
    name: my-repo
    namespace: target-namespace
  spec:
    url: "https://github.com/owner/repo"
    git_provider:
      secret:
        name: "github-webhook-config"
        # Uncomment and set this if your token key in the secret is different
        # key: "provider.token"
      webhook_secret:
        name: "github-webhook-config"
        # Uncomment and set this if your webhook secret key is different
        # key: "webhook.secret"
  ```

## Webhook Secret Notes

* Just a heads-up: Pipelines-as-Code always expects the `Secret` to be in the same namespace as the `Repository` resource. Keep that in mind when you're setting things up!

## Adding a Webhook Secret Later

* Oops, forgot to add a webhook secret, or need to update it?  No sweat!  If you've already got a `Repository` set up, you can use the `tkn pac webhook add` command.  This will add a webhook to your GitHub project settings and update the `webhook.secret` key in your existing Kubernetes `Secret` – all without messing with your `Repository` resource.

Here's how `tkn pac webhook add` works:

```shell script
$ tkn pac webhook add -n repo-pipelines

✓ Setting up GitHub Webhook for Repository https://github.com/owner/repo
👀 I have detected a controller url: https://pipelines-as-code-controller-openshift-pipelines.apps.awscl2.aws.ospqa.com
? Do you want me to use it? Yes
? Please enter the secret to configure the webhook for payload validation (default: AeHdHTJVfAeH):  AeHdHTJVfAeH
✓ Webhook has been created on repository owner/repo
🔑 Secret owner-repo has been updated with webhook secret in the repo-pipelines namespace.

```

**Important:** If your `Repository` isn't in the `default` namespace, remember to use `tkn pac webhook add [-n namespace]`.  In the example above, the `Repository` was in the `repo-pipelines` namespace, so that's where the webhook got added.

## Updating Your GitHub Token

Tokens expire, it happens!  When yours does, here's how to update it:

### Easy Update with `tkn pac`

* The simplest way is to use the [`tkn pac webhook update-token`](/docs/guide/cli) command.  It'll update the token in your existing `Repository` resource in a flash.

Here's a quick example:

```shell script
$ tkn pac webhook update-token -n repo-pipelines

? Please enter your personal access token:  ****************************************
🔑 Secret owner-repo has been updated with new personal access token in the repo-pipelines namespace.

```

**Remember:** If your `Repository` isn't in the `default` namespace, use `tkn pac webhook update-token [-n namespace]`.  Just like before, the example above shows updating the token in the `repo-pipelines` namespace.

### Manual Update via YAML or `kubectl patch`

If you're more of a hands-on type, or just prefer using `kubectl`, you can update the token directly.

First, find the name of the Secret in your `Repository` resource's YAML:

  ```yaml
  spec:
    git_provider:
      secret:
        name: "github-webhook-config"
  ```

Then, replace `$NEW_TOKEN` and `$target_namespace` in the command below with your new token and the correct namespace, and run this:

```shell
kubectl -n $target_namespace patch secret github-webhook-config -p "{\"data\": {\"provider.token\": \"$(echo -n $NEW_TOKEN|base64 -w0)\"}}"
```

That should get you all set! Let me know if anything is unclear or if you have
more questions.

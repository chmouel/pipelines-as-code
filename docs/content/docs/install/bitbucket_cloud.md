---
title: Bitbucket Cloud
weight: 14
---
# Pipelines-as-Code? Works Great with Bitbucket Cloud!

Want to use Pipelines-as-Code with Bitbucket Cloud? Awesome! It's totally doable through a webhook.

First things first, you'll need to get Pipelines-as-Code set up on your Kubernetes cluster.  Just follow the [installation guide](/docs/install/installation) – it'll walk you through everything.

## Let's Make an App Password in Bitbucket Cloud

Heads up! You're gonna need to create an app password in Bitbucket Cloud. Think of it like a special key just for Pipelines-as-Code to do its thing.

Atlassian has a handy guide for this, right here:

<https://support.atlassian.com/bitbucket-cloud/docs/app-passwords/>

When you're making that app password, make sure to tick these boxes to give it the right permissions.  We need these so Pipelines-as-Code can interact with your repos and pull requests:

- Account: `Email`, `Read`
- Workspace membership: `Read`, `Write`
- Projects: `Read`, `Write`
- Issues: `Read`, `Write`
- Pull requests: `Read`, `Write`

**Important for CLI users:** If you plan on setting up your webhook using the command line (CLI), you'll also need to add this extra permission:

- Webhooks: `Read and write`

Take a peek at this [screenshot](/images/bitbucket-cloud-create-secrete.png) to double-check you've got the app password permissions right.  It's worth getting this step spot-on!

Once Bitbucket Cloud gives you that shiny new token, jot it down somewhere safe.  Seriously, keep it handy, because if you lose it, you'll have to make a new one.  Nobody wants that hassle!

## Setting Up Your Repo and Webhook – Two Ways to Roll

Alright, time to get your repository linked up and that webhook humming.  You've got a couple of choices here: the quick command-line method or the slightly more hands-on manual approach.

### Option 1:  Quick Setup with the `tkn pac` Tool

- The easiest way? Use the [`tkn pac create repo`](/docs/guide/cli) command.  This little tool does a bunch of the heavy lifting for you.

  Remember that app password you created?  `tkn pac` will use it to set up the webhook for you and stash it securely as a secret in your Kubernetes cluster. Pipelines-as-Code will then use this secret to access your repository – clever, right?

Here’s how a typical `tkn pac create repo` session looks in your terminal:

```shell script
$ tkn pac create repo

? Enter the Git repository url (default: https://bitbucket.org/workspace/repo):
? Please enter the namespace where the pipeline should run (default: repo-pipelines):
! Namespace repo-pipelines is not found
? Would you like me to create the namespace repo-pipelines? Yes
✓ Repository workspace-repo has been created in repo-pipelines namespace
✓ Setting up Bitbucket Webhook for Repository https://bitbucket.org/workspace/repo
? Please enter your bitbucket cloud username:  <username>
ℹ ️You now need to create a Bitbucket Cloud app password, please checkout the docs at https://is.gd/fqMHiJ for the required permissions
? Please enter the Bitbucket Cloud app password:  ************************************
👀 I have detected a controller url: https://pipelines-as-code-controller-openshift-pipelines.apps.awscl2.aws.ospqa.com
? Do you want me to use it? Yes
✓ Webhook has been created on repository workspace/repo
🔑 Webhook Secret workspace-repo has been created in the repo-pipelines namespace.
🔑 Repository CR workspace-repo has been updated with webhook secret in the repo-pipelines namespace
ℹ Directory .tekton has been created.
✓ A basic template has been created in /home/Go/src/bitbucket/repo/.tekton/pipelinerun.yaml, feel free to customize it.
ℹ You can test your pipeline by pushing the generated template to your git repository

```

### Option 2:  Manual Webhook Setup – For Those Who Like to Tweak

- If you prefer doing things yourself, head over to your Bitbucket Cloud repository. On the left-hand side, find **Repository settings**, then click on the **Webhooks** tab, and finally, hit **Add webhook**.

  - Give your webhook a **Title** – something like "Pipelines-as-Code" is perfect.

  - For the **URL**, you'll need the public address of your Pipelines-as-Code controller. If you're on OpenShift, you can grab it with this command:

    ```shell
    echo https://$(oc get route -n pipelines-as-code pipelines-as-code-controller -o jsonpath='{.spec.host}')
    ```

  - Now, for the events – these are the triggers that will kick off your pipelines.  Make sure you select these:
    - Repository -> Push
    - Repository -> Updated
    - Repository -> Commit comment created
    - Pull Request -> Created
    - Pull Request -> Updated
    - Pull Request -> Merged
    - Pull Request -> Declined
    - Pull Request -> Comment created
    - Pull Request -> Comment updated

Check out this [screenshot](/images/bitbucket-cloud-create-webhook.png) to make sure your webhook settings match up.  Visual confirmation is always good!

- Hit **Save** and you're halfway there!

- Next, you need to create a [`Repository CRD`](/docs/guide/repositorycrd). Think of this as telling Pipelines-as-Code about your repository.  In this CRD, you'll need to include:
  - Your **Username** for Bitbucket (that's your Bitbucket username).
  - A reference to a Kubernetes **Secret** that holds the App Password you made earlier.  This lets Pipelines-as-Code securely access your repo.

- Let's create that secret in your `target-namespace`.  Replace `APP_PASSWORD_AS_GENERATED_PREVIOUSLY` with your actual app password and `target-namespace` with the namespace you're using:

  ```shell
  kubectl -n target-namespace create secret generic bitbucket-cloud-token \
          --from-literal provider.token="APP_PASSWORD_AS_GENERATED_PREVIOUSLY"
  ```

- Finally, create the `Repository` CRD.  Make sure the `secret` part points to the secret you just created.  Here's an example:

```yaml
  ---
  apiVersion: "pipelinesascode.tekton.dev/v1alpha1"
  kind: Repository
  metadata:
    name: my-repo
    namespace: target-namespace
  spec:
    url: "https://bitbucket.com/workspace/repo"
    branch: "main"
    git_provider:
      user: "yourbitbucketusername"
      secret:
        name: "bitbucket-cloud-token"
        # If your secret key isn't "provider.token", uncomment and set it here:
        # key: “provider.token“
```

## Bitbucket Cloud:  Things to Keep in Mind

- **Secrets Stay Local:** The `git_provider.secret` has to live in the same namespace as your `Repository` CR. Pipelines-as-Code always looks for secrets in the same neighborhood.

- **`tkn pac create` & `bootstrap` for Bitbucket Server? Nope:**  Just a heads-up, the `tkn pac create` and `tkn pac bootstrap` commands are not currently supported for Bitbucket *Server*, only for Bitbucket *Cloud*.

{{< hint info >}}
**User IDs:** When you're setting up owners in an owner file, you need to use the `ACCOUNT_ID` for Bitbucket Cloud users, not their usernames.  Bitbucket changed things up a bit for privacy reasons.  More info here:

<https://developer.atlassian.com/cloud/bitbucket/bitbucket-api-changes-gdpr/#introducing-atlassian-account-id-and-nicknames>
{{< /hint >}}

{{< hint danger >}}

- **No Webhook Secrets in Bitbucket Cloud (Boo!):**  Bitbucket Cloud doesn't offer webhook secrets directly.  To keep things secure and stop anyone from messing with your CI, Pipelines-as-Code does a clever trick: it grabs the list of IP addresses that Bitbucket Cloud uses from <https://ip-ranges.atlassian.com/> and only accepts webhook calls coming from those IPs.  Pretty neat, huh?

- **Extra IPs? No Problem:** If you need to add more IP addresses or network ranges to this list, you can tweak the `bitbucket-cloud-additional-source-ip` setting in the `pipelines-as-code` `ConfigMap` (in the `pipelines-as-code` namespace).  Just separate multiple IPs or networks with commas.

- **Want to Turn Off IP Checking? You Can:** If, for some reason, you want to disable this IP address checking, you can set the `bitbucket-cloud-check-source-ip` setting to `false` in the same `pipelines-as-code` `ConfigMap`.  But, you know, security is usually a good thing!
{{< /hint >}}

## Need to Add a Webhook Secret Later?

- If you already have a `Repository` set up and either the webhook secret got deleted or you want to add a webhook to your project settings, the `tkn pac webhook add` command is your friend!  It'll add a webhook to your repository settings and update the `webhook.secret` key in your existing `Secret` without messing with your `Repository` CR.

Here’s a sample run of `tkn pac webhook add`:

```shell script
$ tkn pac webhook add -n repo-pipelines

✓ Setting up Bitbucket Webhook for Repository https://bitbucket.org/workspace/repo
? Please enter your bitbucket cloud username:  <username>
👀 I have detected a controller url: https://pipelines-as-code-controller-openshift-pipelines.apps.awscl2.aws.ospqa.com
? Do you want me to use it? Yes
✓ Webhook has been created on repository workspace/repo
🔑 Secret workspace-repo has been updated with webhook secret in the repo-pipelines namespace.

```

**Important Note:** If your `Repository` isn't in the `default` namespace, you'll need to tell `tkn pac webhook add` which namespace to use with `tkn pac webhook add [-n namespace]`.  In the example above, we used `-n repo-pipelines` because the `Repository` was in the `repo-pipelines` namespace.

## Updating Your Token – Easy Peasy

Got a new app password?  No sweat!  Here are a couple of ways to update it in your cluster:

### Option 1:  `tkn pac webhook update-token` to the Rescue!

- The [`tkn pac webhook update-token`](/docs/guide/cli) command makes updating your token a breeze. It'll update the token in your existing `Repository` CR.

Here's how it looks:

```shell script
$ tkn pac webhook update-token -n repo-pipelines

? Please enter your personal access token:  ************************************
🔑 Secret workspace-repo has been updated with new personal access token in the repo-pipelines namespace.

```

**Remember the Namespace:** Just like with `webhook add`, if your `Repository` isn't in the `default` namespace, use `tkn pac webhook update-token [-n namespace]`.  The example above uses `-n repo-pipelines` to target the `repo-pipelines` namespace.

### Option 2:  YAML Edit or `kubectl patch` – For the Hands-On Approach

If you've regenerated your app password, you'll need to update it in Kubernetes.  Just replace `$password` with your new password and `$target_namespace` with your namespace in the commands below.

You can find the secret name in your `Repository` CR – look for the `spec.git_provider.secret.name` field:

  ```yaml
  spec:
    git_provider:
      secret:
        name: "bitbucket-cloud-token"
  ```

Then, use `kubectl patch` to update the secret with your new password:

```shell
kubectl -n $target_namespace patch secret bitbucket-cloud-token -p "{\"data\": {\"provider.token\": \"$(echo -n $password|base64 -w0)\"}}"
```

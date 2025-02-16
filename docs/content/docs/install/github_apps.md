---
title: GitHub Apps
weight: 10
---

# Setting up a Pipelines-as-Code GitHub App

Using a GitHub App is a bit different than other ways to connect Pipelines-as-Code, but it's a neat way to tie your Git workflow directly into Tekton pipelines.  Think of it as the central hub for your Git stuff and your pipelines.  Usually, you just need one GitHub App per cluster, and it's typically something your admin sets up.

You'll need to make sure your GitHub App webhook points to your Pipelines-as-Code controller's address (that route or ingress URL). This is how Pipelines-as-Code knows when things happen in your Git repositories.

There are two main ways to get your GitHub App up and running:

## The Speedy Way with `tkn pac cli`

The quickest path is using the [`tkn pac bootstrap`](/docs/guide/cli) command. This command is like a little helper that will create a GitHub App for you, walk you through setting it up with your Git repo, and even handle creating those necessary secret keys.  Once it's done, you just need to install the GitHub App in the specific repositories you want to use with Pipelines-as-Code.

If you're feeling more hands-on, or just curious about the details, you can also set things up manually, and the steps are right here: [Manual Setup](#setup-manually)

## The Manual Route

* First, head over to GitHub Apps settings. You can find it at <https://github.com/settings/apps> (or go to *Settings > Developer settings > GitHub Apps*) and click that shiny **New GitHub App** button.

* Now, you'll need to fill out the GitHub App form with these details:
  * **GitHub Application Name**: How about `OpenShift Pipelines`?  Something easy to recognize.
  * **Homepage URL**:  Pop in your OpenShift Console URL here.
  * **Webhook URL**: This is important!  Paste the Pipelines-as-Code route or ingress URL you grabbed earlier. This tells GitHub where to send event notifications.
  * **Webhook secret**:  Think of this as a password for your webhook. You can make up something random – a quick way to generate one is by running `head -c 30 /dev/random | base64` in your terminal.

* Next up, permissions!  You need to tell the GitHub App what it's allowed to do.  Select these repository permissions:
  * **Checks**:  `Read & Write` (Pipelines-as-Code needs to create check runs to report pipeline status)
  * **Contents**: `Read & Write` (So it can access your code, obviously!)
  * **Issues**: `Read & Write` (For commenting on issues, maybe?)
  * **Metadata**: `Readonly` (Just needs to know a little bit *about* the repo)
  * **Pull request**: `Read & Write` (Essential for PR workflows)

* And these organization permissions:
  * **Members**: `Readonly` (To know who's who, perhaps?)
  * **Plan**: `Readonly` (Probably not super critical, but let's include it)

* Almost there! Now, tell GitHub App which events to pay attention to by subscribing to these:
  * Check run
  * Check suite
  * Issue comment
  * Commit comment
  * Pull request
  * Push

{{< hint info >}}
> Just to be sure we're on the same page, here's a [screenshot](https://user-images.githubusercontent.com/98980/124132813-7e53f580-da81-11eb-9eb4-e4f1487cf7a0.png) of what the permissions should look like.
{{< /hint >}}

* Okay, all set?  Click on **Create GitHub App**.

* Awesome, you've got a GitHub App!  On the app's details page, jot down the **App ID** at the top. You'll need this in a sec.

* Now, in the **Private keys** section, hit **Generate Private key**.  This will download a `.pem` file to your computer. Keep this safe! You'll need it in the next step and if you ever want to move your app to a different cluster.

### Hooking up Pipelines-as-Code to your GitHub App

To let Pipelines-as-Code actually *use* your GitHub App, you need to create a Kubernetes secret. This secret basically holds the private key for your GitHub App and that webhook secret you created earlier.  Think of it as providing Pipelines-as-Code with the credentials it needs to talk to GitHub securely and verify that webhook requests are legit.

Run this command, but make sure to replace these placeholders with your actual values:

* `APP_ID`:  That **App ID** you wrote down from the GitHub App page.
* `WEBHOOK_SECRET`: The webhook secret you set when creating the GitHub App.
* `PATH_PRIVATE_KEY`: The path to that `.pem` private key file you downloaded.

```bash
kubectl -n pipelines-as-code create secret generic pipelines-as-code-secret \
        --from-literal github-private-key="$(cat $PATH_PRIVATE_KEY)" \
        --from-literal github-application-id="APP_ID" \
        --from-literal webhook.secret="WEBHOOK_SECRET"
```

And finally, the last step – install the GitHub App on any Git repositories you want to use with Pipelines-as-Code.  Go to your repo settings, find "GitHub Apps," and install the "OpenShift Pipelines" app you just created.

## Got GitHub Enterprise?

Pipelines-as-Code plays nicely with GitHub Enterprise too.

You really don't have to do anything special to make it work with GHE.  Pipelines-as-Code is smart enough to detect if you're using GitHub Enterprise and will automatically use the right API endpoints for authentication instead of the public GitHub ones.  Pretty neat, huh?

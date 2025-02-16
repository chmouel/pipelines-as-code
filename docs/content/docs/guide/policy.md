---
title: Policy on actions
weight: 50
---

# Let's Talk About Action Policies

Pipelines as Code lets you set up policies to control what actions different teams in your organization can take. These teams are defined in your Git provider, like GitHub or Gitea (we support those right now).

{{< support_matrix github_app="true" github_webhook="true" gitea="true" gitlab="false" bitbucket_cloud="false" bitbucket_server="false" >}}

## What Actions Can You Control?

* `pull_request` - This one's about kicking off CI (Continuous Integration) in Pipelines as Code. If you set a team for this action, only people in that team can trigger CI, even if they're repo owners or collaborators.  *However*, anyone listed in your `OWNERS` file can *always* trigger it.

* `ok_to_test` -  This action is for letting specific teams authorize CI runs on pull requests using the `/ok-to-test` comment.  This is super handy for letting folks who aren't official collaborators on your repo contribute code and have it tested! It also works for `/test` and `/retest`.  Important:  `ok_to_test` trumps the `pull_request` policy.

## How to Set Up Policies in your Repository CR

To get these policies working, you'll need to add this config to your Repository CR:

```yaml
apiVersion: "pipelinesascode.tekton.dev/v1alpha1"
kind: Repository
metadata:
  name: repository1
spec:
  url: "https://github.com/org/repo"
  settings:
    policy:
      ok_to_test:
        - ci-admins
      pull_request:
        - ci-users
```

Let's break down this example:

* Folks in the `ci-admins` team can give the green light for CI to run on pull requests from *anyone*.
* People in the `ci-users` team can run CI on their *own* pull requests.

---
title: Manual Installation
weight: 2
---
# Installation

So, you wanna get Pipelines as Code up and running? Sweet! You've got a couple of ways to do it, and this page is all about going the manual route.

## Operator Install

If you're looking for the easy button, you might want to peek at the [Operator Installation](./operator_installation.md) guide. Seriously, it's the way to go if you're on OpenShift and want a smoother ride. It takes care of a lot of the behind-the-scenes stuff for you.

## Manual Install

Alright, manual it is! No sweat. Let's get this show on the road.

### Prerequisite

First thing's first, you *need* to have [Tekton Pipelines](https://github.com/tektoncd/pipeline) installed. Think of Tekton Pipelines as the engine that makes Pipelines as Code go vroom vroom.  Grab the latest version with this command:

```shell
  kubectl apply --filename https://storage.googleapis.com/tekton-releases/pipeline/latest/release.yaml
```

{{< hint info >}}
Quick heads-up!  If you're not aiming for the absolute newest Tekton, just make sure you're running Tekton Pipeline version v0.44.0 or higher. Older versions might throw a tantrum.
{{< /hint >}}

Ready to install Pipelines as Code the old-fashioned way? Here's how for a stable release. Just copy, paste, and hit enter in your terminal:

```shell
# OpenShift
kubectl patch tektonconfig config --type="merge" -p '{"spec": {"platforms": {"openshift":{"pipelinesAsCode": {"enable": false}}}}}'
kubectl apply -f https://raw.githubusercontent.com/openshift-pipelines/pipelines-as-code/stable/release.yaml

# Kubernetes
kubectl apply -f https://raw.githubusercontent.com/openshift-pipelines/pipelines-as-code/stable/release.k8s.yaml
```

Want to live on the wild side and try out the very latest, hot-off-the-press version? Go for it! Install the nightly build like this:

```shell
# OpenShift
kubectl apply -f https://raw.githubusercontent.com/openshift-pipelines/pipelines-as-code/nightly/release.yaml

# Kubernetes
kubectl apply -f https://raw.githubusercontent.com/openshift-pipelines/pipelines-as-code/nightly/release.k8s.yaml
```

Running those commands will drop `release.yaml` into your cluster. This sets up the `pipelines-as-code` namespace (that's where all the magic happens!), plus all the necessary roles and whatnot.

Just a friendly FYI, the `pipelines-as-code` namespace is like the VIP lounge for Pipelines as Code. Admins only, please! 😉

### OpenShift

Good news for you OpenShift folks! When you run `release.yaml`, it automatically whips up a Route URL for the Pipelines-as-Code Controller. You'll need this URL later when you're setting up your Git provider (like GitHub, GitLab, the usual suspects).

To snag that Route URL, just punch this into your terminal:

```shell
echo https://$(oc get route -n pipelines-as-code pipelines-as-code-controller -o jsonpath='{.spec.host}')
```

### Kubernetes

Kubernetes users, your setup is a *tiny* bit more involved. But hey, don't sweat it, we've got your back!  Jump over [here](/docs/install/kubernetes) for the full rundown and step-by-step instructions.

## RBAC

By default, only `system:admin` users are allowed to create those `Repository` custom resources in namespaces.  If you want to let other users join the party, you gotta give them the thumbs up.

To do that, you create a `RoleBinding` in their namespace that points to the `openshift-pipeline-as-code-clusterrole`.

Let's say you've got a user named `user` and you want them to be able to create `Repository` resources in the `user-ci` namespace. If you're using the OpenShift `oc` command-line tool, here's how you do it:

```shell
oc adm policy add-role-to-user openshift-pipeline-as-code-clusterrole user -n user-ci
```

Or, if you're more of a `kubectl` and YAML person, you can apply this:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: openshift-pipeline-as-code-clusterrole
  namespace: user-ci
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: openshift-pipeline-as-code-clusterrole
subjects:
- apiGroup: rbac.authorization.k8s.io
  kind: User
  name: user
```

## CLI

Pipelines as Code comes with a super handy command-line interface (CLI) that works as a plugin for `tkn`. Think of it as your personal shortcut to bossing around Pipelines as Code right from your terminal. To get it installed, check out the [CLI](/docs/guide/cli) docs for all the juicy details.

## Controller TLS Setup

Want to crank up the security with HTTPS?  Pipelines As Code Controller speaks both HTTP and HTTPS. Usually, you'd handle TLS at the ingress/Route level. But, if you're feeling a bit more hardcore and want to set up TLS directly on the controller, here's the lowdown:

First up, you need to create a secret that's gonna hold your TLS certificates.  Run this command, but make sure to swap out `/path/to/crt/file` and `/path/to/key/file` with the real paths to your certificate and key files:

```shell
  kubectl create secret generic -n pipelines-as-code pipelines-as-code-tls-secret \
    --from-file=cert=/path/to/crt/file \
    --from-file=key=/path/to/key/file
```

Once that secret is cooked up, just give the `pipelines-as-code-controller` pod in the `pipelines-as-code` namespace a quick restart.  When it wakes back up, it'll automatically grab those TLS secrets and use 'em.  Piece of cake!

**Important Stuff to Know:**

-  Gotta name your secret `pipelines-as-code-tls-secret`. The controller is specifically looking for that name. If you're feeling rebellious and want a different name, you'll need to tweak the controller deployment itself.
- If your secret uses key names that aren't `cert` and `key`, you'll also need to update the controller deployment's environment variables. Keep this in mind for future upgrades (you might want to use tools like [kustomize](https://kustomize.io/) to manage these kinds of tweaks).

If you ever need to mess with those environment variables on the controller, you can use this command:

```shell
  kubectl set env deployment pipelines-as-code-controller -n pipelines-as-code TLS_KEY=<key> TLS_CERT=<cert>
```

## Proxy service for PAC controller

Need to get your Pipelines-as-Code controller out in the open? That's key for it to get those sweet events from Git providers. If you're working locally (like with Minikube or kind) or just don't want to dive into setting up an ingress on your cluster, a proxy service can be your new best friend. It lets you show off the `pipelines-as-code-controller` service without all the fuss.

### Proxying with hook.pipelinesascode.com

For local setups like Minikube/kind, [hook.pipelinesascode.com](https://hook.pipelinesascode.com/) is a seriously handy proxy service. Let's give it a whirl!

- First, whip up your own unique URL by heading over to [hook.pipelinesascode.com/new](https://hook.pipelinesascode.com/new).
- Copy the "Webhook Proxy URL" you see there.
- Now, you need to drop this URL into the container arguments in your `deployment.yaml` file. Find the bit that says `'<replace Webhook Proxy URL>'` and swap it out with your actual URL, like `'https://hook.pipelinesascode.com/oLHu7IjUV4wGm2tJ'`.

Here's a little snippet from `deployment.yaml` to show you what it looks like:

```yaml
kind: Deployment
apiVersion: apps/v1
metadata:
  name: gosmee-client
spec:
  replicas: 1
  selector:
    matchLabels:
      app: gosmee-client
  template:
    metadata:
      creationTimestamp: null
      labels:
        app: gosmee-client
    spec:
      containers:
        - name: gosmee-client
          image: 'ghcr.io/chmouel/gosmee:main'
          args:
            - '<replace Webhook Proxy URL>'
            - $(SVC)
          env:
            - name: SVC
              value: >-
                http://pipelines-as-code-controller.pipelines-as-code.svc.cluster.local:8080
      restartPolicy: Always
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxUnavailable: 25%
      maxSurge: 25%
  revisionHistoryLimit: 10
  progressDeadlineSeconds: 600
```

-  Deploy it to your cluster by running:

```yaml
kubectl create -f deployment.yaml -n pipelines-as-code
```

- And boom! You can now use your "Webhook Proxy URL" when you're setting up webhooks in GitHub, GitLab, Bitbucket, or wherever your code hangs out.

Basically, anywhere you'd normally use the `pipelines-as-code-controller` service URL, just use your "Webhook Proxy URL" instead. You're all set!

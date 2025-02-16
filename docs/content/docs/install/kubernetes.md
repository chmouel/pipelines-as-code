---
title: Kubernetes
weight: 20
---
# Kubernetes - Running Pipelines as Code on Kube

Pipelines as Code is built to run just about anywhere Kubernetes goes - whether that's Kubernetes itself, minikube for local testing, or kind for spinning up clusters in Docker.  It's pretty flexible!

## First Things First: Prerequisites

Before we get rolling, you'll need a couple of things prepped and ready:

*   **Tekton Pipelines:** You gotta have Tekton Pipelines installed on your Kubernetes cluster. Think of it as the engine that powers Pipelines as Code.  Grab the [release.yaml](https://storage.googleapis.com/tekton-releases/pipeline/latest/release.yaml) file and apply it to your cluster.

*   **Kubernetes Version:** Make sure your Kubernetes is up to date. You'll need at least version 1.23 or newer to play nicely with Pipelines as Code.

## Let's Get it Installed!

Ready to get Pipelines as Code up and running?  Choose your flavor:

For the **stable release** (recommended for most folks):

```shell
kubectl apply -f https://raw.githubusercontent.com/openshift-pipelines/pipelines-as-code/stable/release.k8s.yaml
```

Want to live on the edge and try out the **nightly build** (for testing latest features, might be a bit rough around the edges):

```shell
kubectl apply -f https://raw.githubusercontent.com/openshift-pipelines/pipelines-as-code/nightly/release.k8s.yaml
```

## Quick Check: Is it Working?

Let's make sure everything spun up correctly.  Run this command to see the status of the Pipelines as Code components:

```shell
$ kubectl get deployment -n pipelines-as-code
```

You should see something like this, with all deployments showing `READY   1/1`:

```
NAME                           READY   UP-TO-DATE   AVAILABLE   AGE
pipelines-as-code-controller   1/1     1            1           43h
pipelines-as-code-watcher      1/1     1            1           43h
pipelines-as-code-webhook      1/1     1            1           43h
```

Make sure all three are reporting `READY` before moving on to the next step – setting up Ingress.

## Making it Public: Ingress Setup

To let GitHub, GitLab, or other Git providers talk to your Pipelines as Code controller, you'll need to set up an [`Ingress`](https://kubernetes.io/docs/concepts/services-networking/ingress/).  Think of it as creating a public doorway to your controller.

The exact way you configure Ingress depends on your Kubernetes setup.  Here are a couple of examples to give you a head start.

You'll need either the hostname or the IP address of your Ingress to use as the webhook URL later.  You can usually find this by running: `kubectl get ingress pipelines-as-code -n pipelines-as-code`.

**Quick & Dirty Testing (No Ingress Needed)**

If you're just kicking the tires and don't want to mess with Ingress just yet, the `tkn pac bootstrap` [cli](../../guide/cli) command has a neat trick. It can set up a [gosmee](https://github.com/chmouel/gosmee) deployment for you, using `https://hook.pipelinesascode.com` as a webhook forwarder.  Handy for quick experiments!

### Example Ingress Configs

Here are a couple of common Ingress examples to get you going:

### Google Kubernetes Engine (GKE)

If you're running on GKE, this should get you started:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  labels:
    pipelines-as-code/route: controller
  name: pipelines-as-code
  namespace: pipelines-as-code
  annotations:
    kubernetes.io/ingress.class: gce
spec:
  defaultBackend:
    service:
      name: pipelines-as-code-controller
      port:
        number: 8080
```

### Nginx Ingress Controller

Using the popular Nginx Ingress Controller?  Here's a sample config:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  labels:
    pipelines-as-code/route: controller
  name: pipelines-as-code
  namespace: pipelines-as-code
spec:
  ingressClassName: nginx
  rules:
  - host: webhook.host.tld
    http:
      paths:
      - backend:
          service:
            name: pipelines-as-code-controller
            port:
              number: 8080
        path: /
        pathType: Prefix
```

**Important:**  In this example, `webhook.host.tld` is just a placeholder! You'll need to replace it with the actual hostname you're using for your Pipelines as Code controller.  This is the URL you'll punch in when setting up webhooks in your Git provider.

## Tekton Dashboard Love

If you're using the [Tekton Dashboard](https://github.com/tektoncd/dashboard) (and you totally should, it's awesome!), you can make Pipelines as Code even better!  Just add the `tekton-dashboard-url` key to the `pipelines-as-code` config map.  Point it to the full URL of your Ingress for the dashboard, and bam! You'll get direct links to Tekton Dashboard logs right from Pipelines as Code.  Sweet, right?

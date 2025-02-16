---
title: Custom Certificates for Private Git Repos
weight: 4
---
# Dealing with Custom Certificates

Got a private Git repo that needs a special certificate to access?  No problem! If you're using Pipelines as Code and your Git repository uses a privately signed or custom certificate, you'll need to let Pipelines as Code know about it. This guide will show you how.

## OpenShift Users

Using OpenShift? Awesome! If you installed Pipelines as Code using the OpenShift Pipelines operator, the easiest way to handle custom certificates is through OpenShift itself.  Just [add your certificate to your cluster's Proxy object](https://docs.openshift.com/container-platform/4.11/networking/configuring-a-custom-pki.html#nw-proxy-configure-object_configuring-a-custom-pki).  OpenShift will then make sure that certificate is available everywhere, including Pipelines as Code.  Easy peasy!

## Kubernetes Folks

If you're running Kubernetes directly, here's how to get those custom certificates working.  It's a few more steps than OpenShift, but still straightforward:

### Step 1: Create a ConfigMap for your Certificate

First, we need to get your certificate into Kubernetes.  Think of a ConfigMap as a place to store configuration files. Run this command, replacing `<path to ca.crt>` with the actual path to your certificate file:

```shell
kubectl -n pipelines-as-code create configmap git-repo-cert --from-file=git.crt=<path to ca.crt>
```

This creates a ConfigMap named `git-repo-cert` in the `pipelines-as-code` namespace.

### Step 2: Mount that ConfigMap into Pipelines as Code Pods

Now, we need to tell Pipelines as Code where to find this certificate. We do this by mounting the ConfigMap into the `pipelines-as-code-controller` and `pipelines-as-code-watcher` pods.  Kubernetes has a great guide on [how to mount ConfigMaps](https://kubernetes.io/docs/tasks/configure-pod-container/configure-pod-configmap/#add-configmap-data-to-a-volume) if you need a refresher. You'll want to mount it in the `pipelines-as-code` namespace.

### Step 3: Point Pipelines as Code to Your Certificate Directory

Let's say you mounted the ConfigMap at `/pac-custom-certs` inside the pods (you pick the location when mounting).  We need to tell Pipelines as Code to look there for certificates.  We do this by setting the `SSL_CERT_DIR` environment variable in the `pipelines-as-code-controller` and `pipelines-as-code-watcher` deployments.

Run this command, making sure `/pac-custom-certs` matches where you mounted your ConfigMap:

```shell
kubectl set env deployment pipelines-as-code-controller pipelines-as-code-watcher -n pipelines-as-code SSL_CERT_DIR=/pac-custom-certs:/etc/ssl/certs:/etc/pki/tls/certs:/system/etc/security/cacerts
```

This command updates the deployments to include your custom certificate directory in the list of places where certificates are checked.

### All Set!

That's it! Pipelines as Code should now be able to access your repository using your custom certificate.  If you run into any issues, double-check the paths and make sure the ConfigMap is mounted correctly. Good luck!

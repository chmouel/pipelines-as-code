---
title: GitHub Apps
weight: 10
---

# Configure Pipelines-as-Code with a GitHub Application

The recommended way to use Pipelines-as-Code is through a GitHub App. This method provides several advantages over webhook-based approaches:

- Better security with fine-grained permissions
- No need for personal access tokens
- Automatic token rotation
- Support for the GitHub Checks API

## Setting up a GitHub App

### Prerequisites

- A Kubernetes cluster with Pipelines-as-Code installed
- Admin access to a GitHub organization or account
- `kubectl` access to your cluster

### Step 1: Create the GitHub App

1. Go to your GitHub organization's settings page or your personal GitHub settings.
2. Navigate to "Developer Settings" > "GitHub Apps" > "New GitHub App".
3. Configure the application with the following settings:

   **Basic Information:**
   - **GitHub App name**: Choose a descriptive name (e.g., "My-Org Pipelines-as-Code")
   - **Homepage URL**: Your organization's URL or the cluster URL
   - **Webhook URL**: Your controller endpoint URL (see below for details)
   - **Webhook Secret**: Generate a secure random string (keep it safe, you'll need it later)

   **Permissions:**
   - **Repository permissions:**
     - **Checks**: Read & write
     - **Contents**: Read & write (needed for cloning repositories)
     - **Metadata**: Read-only
     - **Pull requests**: Read & write
     - **Commit statuses**: Read & write

   **Subscribe to events:**
   - **Check run**
   - **Pull request**
   - **Push**

   **Where can this GitHub App be installed?**
   - Select "Only on this account" for your organization GitHub App

4. After creating the app, you'll be redirected to the app's settings page.
5. Note the **App ID** at the top of the page.
6. Under "Private keys," click "Generate a private key" and save the downloaded `.pem` file.

### Step 2: Configure the Webhook URL

The Webhook URL is the endpoint where GitHub will send event notifications. You have several options:

#### Option A: Public Kubernetes Cluster with External Route/Ingress

If your cluster is publicly accessible, create an Ingress or Route pointing to the Pipelines-as-Code controller service:

```bash
# For standard Kubernetes with Ingress:
kubectl create ingress pipelines-as-code-controller \
  --rule="your-ingress-domain.com//*=pipelines-as-code-controller:8080"

# For OpenShift:
oc create route edge pipelines-as-code-controller \
  --service=pipelines-as-code-controller \
  --port=8080
```

Use the resulting URL as your Webhook URL in the GitHub App settings.

#### Option B: Using a Webhook Forwarder for Development/Testing

For development environments or private clusters, you can use a webhook forwarder service:

```bash
# Install the CLI tool
go install github.com/chmouel/gosmee@latest

# Start forwarding
gosmee client https://hook.pipelinesascode.com/forward/RANDOM_TOKEN http://localhost:8080

# In another terminal, port-forward the controller:
kubectl port-forward -n pipelines-as-code svc/pipelines-as-code-controller 8080
```

Use the forwarding URL (e.g., `https://hook.pipelinesascode.com/forward/RANDOM_TOKEN`) as your Webhook URL.

#### Option C: Testing with ngrok

For temporary testing purposes, you can use ngrok:

```bash
# Start port forwarding to the controller
kubectl port-forward -n pipelines-as-code svc/pipelines-as-code-controller 8080

# In another terminal, run ngrok
ngrok http 8080
```

Use the ngrok forwarding URL (e.g., `https://abcd1234.ngrok.io`) as your Webhook URL.

### Step 3: Configure Pipelines-as-Code with GitHub App Details

Create a Secret with your GitHub App information:

```bash
kubectl create secret generic pipelines-as-code-secret \
  -n pipelines-as-code \
  --from-literal github-application-id=<YOUR_APP_ID> \
  --from-file github-private-key=/path/to/downloaded/private-key.pem \
  --from-literal webhook.secret=<YOUR_WEBHOOK_SECRET>
```

Update the Pipelines-as-Code controller ConfigMap to use the GitHub App:

```bash
kubectl patch configmap pipelines-as-code \
  -n pipelines-as-code \
  --type=merge \
  -p '{"data": {"application-name": "Your Application Name"}}'
```

### Step 4: Install the GitHub App

1. Navigate to your GitHub App's public page: `https://github.com/apps/<your-app-name>`
2. Click "Install" or "Configure"
3. Choose which repositories the app can access:
   - Select "All repositories" to allow access to all repositories in your organization
   - Or select "Only select repositories" and choose specific repositories

### Step 5: Create a Repository CR

For each repository where you want to use Pipelines-as-Code, create a Repository Custom Resource:

```yaml
apiVersion: "pipelinesascode.tekton.dev/v1alpha1"
kind: Repository
metadata:
  name: my-repo-name
  namespace: my-pipelines-namespace
spec:
  url: "https://github.com/org/repo"
```

You can use the `tkn pac` CLI to create this more easily:

```bash
tkn pac create repository
```

### Step 6: Test Your Setup

1. Create a `.tekton` directory in your repository with a PipelineRun definition that includes appropriate annotations.
2. Create a pull request or push a commit to trigger the pipeline.
3. Check the GitHub Checks tab to see if your pipeline runs correctly.

## Troubleshooting

### Webhook Delivery Issues

If webhooks aren't reaching your controller:

1. Check the GitHub App's Advanced tab for recent webhook delivery logs
2. Verify your webhook URL is accessible from GitHub's servers
3. Ensure the webhook secret matches between GitHub and your Kubernetes secret

### Authentication Issues

If you're seeing authentication errors:

1. Verify the App ID and private key are correct in your secret
2. Check that the app is properly installed on the repository
3. Ensure the app has the necessary permissions

### Pipeline Execution Problems

If PipelineRuns aren't being created:

1. Check the controller logs: `kubectl logs -n pipelines-as-code deployment/pipelines-as-code-controller`
2. Verify your Repository CR matches the correct GitHub repository URL
3. Ensure your PipelineRun annotations correctly match the event type and branch

## Additional Resources

- [GitHub Apps Documentation](https://docs.github.com/en/developers/apps)
- [Pipelines-as-Code CLI Guide]({{< relref "/docs/guide/cli.md" >}})
- [PipelineRun Authoring Guide]({{< relref "/docs/guide/authoringprs.md" >}})

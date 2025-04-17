# Getting Started with Pipelines-as-Code

This tutorial will guide you through your first Pipelines-as-Code implementation, taking you from installation to running your first CI pipeline.

## Prerequisites

Before you begin, ensure you have:

- A Kubernetes cluster or OpenShift cluster running
- `kubectl` or `oc` CLI tools installed
- Access to a GitHub repository where you have admin permissions
- Tekton Pipelines installed (v0.41.0+)

## Step 1: Install Pipelines-as-Code

### On Kubernetes

```bash
kubectl apply -f https://raw.githubusercontent.com/openshift-pipelines/pipelines-as-code/stable/release.yaml
```

### On OpenShift

```bash
oc apply -f https://raw.githubusercontent.com/openshift-pipelines/pipelines-as-code/stable/release.yaml
```

Verify the installation:

```bash
kubectl get pods -n pipelines-as-code
```

You should see the controller pod running:

```
NAME                                        READY   STATUS    RESTARTS   AGE
pipelines-as-code-controller-7bc4d87858-xr56j   1/1     Running   0          1m
```

## Step 2: Create a Namespace for Your Project

```bash
kubectl create namespace my-pipeline-demo
kubectl config set-context --current --namespace=my-pipeline-demo
```

## Step 3: Set Up GitHub Integration

You can use either GitHub App (recommended) or Webhook integration.

### Option A: GitHub App (Recommended)

1. Run the Pipelines-as-Code GitHub App setup utility:

   ```bash
   kubectl run -n pipelines-as-code pac-bootstrap \
   --restart=Never -i --tty --rm \
   --image=ghcr.io/openshift-pipelines/pipelines-as-code-controller:stable
   ```

2. Select "Create GitHub Application" when prompted.

3. Follow the interactive prompts to:
   - Enter your GitHub organization or username
   - Set up the GitHub App
   - Generate and upload webhook secrets

4. Note the GitHub App ID and save the private key.

### Option B: Webhook Setup

1. In your GitHub repository, go to Settings > Webhooks > Add webhook.

2. Get the webhook URL:

   ```bash
   echo "https://$(kubectl get route -n pipelines-as-code pipelines-as-code-controller -o jsonpath='{.spec.host}')/incoming"
   ```

3. Configure the webhook:
   - Payload URL: Use the URL from the previous step
   - Content type: application/json
   - Secret: Generate a random string
   - Enable SSL verification
   - Select "Send me everything" for events

## Step 4: Create Repository CR

Create a file named `repository.yaml`:

```yaml
apiVersion: pipelinesascode.tekton.dev/v1alpha1
kind: Repository
metadata:
  name: my-repo-demo
  namespace: my-pipeline-demo
spec:
  url: https://github.com/your-org/your-repo
  git_provider:
    # For GitHub App
    github:
      organization: your-org
      repository: your-repo
      app_id: "YOUR_APP_ID"
      webhook_secret:
        name: "github-webhook-config"
        key: "webhook.secret"
    # For Webhook (uncomment if using webhook)
    # webhook:
    #   secret:
    #     name: "webhook-secret"
    #     key: "token"
```

Apply the configuration:

```bash
kubectl apply -f repository.yaml
```

## Step 5: Create Your First PipelineRun Template

In your GitHub repository, create the following file at `.tekton/pull-request.yaml`:

```yaml
apiVersion: tekton.dev/v1
kind: PipelineRun
metadata:
  name: simple-pipeline-run
  annotations:
    # Run on pull requests
    pipelinesascode.tekton.dev/on-event: "[pull_request]"
    # Target the main branch
    pipelinesascode.tekton.dev/on-target-branch: "[main]"
    # Use git-clone from Tekton Hub 
    pipelinesascode.tekton.dev/task: "[git-clone]"
spec:
  pipelineSpec:
    tasks:
      - name: fetch-repository
        taskRef:
          name: git-clone
        params:
          - name: url
            value: "$(params.repo_url)"
          - name: revision
            value: "$(params.revision)"
          - name: deleteExisting
            value: "true"
        workspaces:
          - name: output
            workspace: shared-workspace
            
      - name: hello-world
        runAfter:
          - fetch-repository
        workspaces:
          - name: source
            workspace: shared-workspace
        taskSpec:
          workspaces:
            - name: source
          steps:
            - name: say-hello
              image: registry.access.redhat.com/ubi9/ubi-minimal:latest
              script: |
                #!/usr/bin/env bash
                echo "Hello from Pipelines as Code!"
                echo "Repository URL: $(params.repo_url)"
                echo "Branch: $(params.revision)"
                ls -la $(workspaces.source.path)

  params:
    - name: repo_url
      value: "{{ repo_url }}"
    - name: revision
      value: "{{ revision }}"
  workspaces:
    - name: shared-workspace
      volumeClaimTemplate:
        spec:
          accessModes:
            - ReadWriteOnce
          resources:
            requests:
              storage: 1Gi
```

## Step 6: Create a Pull Request

1. Commit and push the file to your repository.
2. Create a new branch:

   ```bash
   git checkout -b test-pac
   ```

3. Make a simple change to any file.
4. Commit and push to your branch:

   ```bash
   git add .
   git commit -m "Test Pipelines-as-Code"
   git push origin test-pac
   ```

5. Open a pull request on GitHub from your branch to the main branch.

## Step 7: Monitor the Pipeline Execution

1. Check if the Repository CR detected the PR:

   ```bash
   kubectl get repository my-repo-demo -o yaml
   ```

   Look for events in the `status` section.

2. Check your PipelineRun:

   ```bash
   kubectl get pipelineruns
   ```

3. View the logs:

   ```bash
   tkn pipelinerun logs -f
   ```

4. Go to the GitHub PR page to see the status check from Pipelines-as-Code.

## Step 8: Advanced Configuration

Once your basic pipeline is working, you can enhance it with more advanced features:

### Adding Multiple Pipeline Triggers

Create a file `.tekton/push-main.yaml` for push events:

```yaml
apiVersion: tekton.dev/v1
kind: PipelineRun
metadata:
  name: main-branch-pipeline
  annotations:
    pipelinesascode.tekton.dev/on-event: "[push]"
    pipelinesascode.tekton.dev/on-target-branch: "[main]"
    pipelinesascode.tekton.dev/task: "[git-clone]"
spec:
  # Pipeline spec similar to above
  # ...
```

### Using Remote Tasks

Update your annotations to use multiple remote tasks:

```yaml
pipelinesascode.tekton.dev/task: "[git-clone, buildah]"
```

### Path Filtering

Only run when specific files change:

```yaml
pipelinesascode.tekton.dev/paths: "[src/**, Dockerfile]"
```

## Troubleshooting

If your pipeline doesn't trigger:

1. Check the controller logs:

   ```bash
   kubectl logs -n pipelines-as-code deployment/pipelines-as-code-controller
   ```

2. Verify webhook events are being received:

   ```bash
   kubectl get events -n my-pipeline-demo
   ```

3. Check your Repository CR status:

   ```bash
   kubectl describe repository my-repo-demo
   ```

4. Verify GitHub App installation permissions (if using GitHub App).

## Next Steps

Now that you have a basic pipeline working, you can:

1. Add more tasks to your pipeline
2. Set up branch protection rules in GitHub to require passing Pipelines-as-Code checks
3. Create more complex PipelineRuns for different events
4. Explore advanced features like CEL expressions and concurrency limits

For more advanced usage, explore our other tutorials:

- [Using Variables in PipelineRuns]({{< relref "/docs/guide/variables.md" >}})
- [Working with Private Repositories]({{< relref "/docs/guide/private_repos.md" >}})
- [Setting Up Tekton Dashboard Integration]({{< relref "/docs/guide/dashboard.md" >}})

Happy CI/CD with Pipelines-as-Code!

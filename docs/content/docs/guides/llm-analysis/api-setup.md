---
title: API Keys and Credentials
weight: 2
---

This page explains how to store credentials for each AI backend as Kubernetes Secrets and how to reference them from your Repository CR. Complete these steps before enabling LLM-powered analysis.

{{< callout type="warning" >}}
Create the Secret in the same namespace as the Repository CR.
{{< /callout >}}

## Backend Credentials

Each backend reads its API key from a specific environment variable. Pipelines-as-Code injects the secret value under the right variable name automatically based on the `backend` field.

| Backend | Environment variable | Where to get the key |
|---------|---------------------|----------------------|
| `claude` | `ANTHROPIC_API_KEY` | [Anthropic Console](https://console.anthropic.com/settings/keys) |
| `codex` | `OPENAI_API_KEY` | [OpenAI Platform](https://platform.openai.com/api-keys) |
| `gemini` | `GEMINI_API_KEY` | [Google AI Studio](https://aistudio.google.com/app/apikey) |
| `claude-vertex` | GCP service account JSON (see [Vertex AI](#vertex-ai)) | GCP IAM |
| `opencode` | GCP service account JSON (see [Vertex AI](#vertex-ai)) | GCP IAM |

### `claude` (Anthropic)

1. Get an API key from the [Anthropic Console](https://console.anthropic.com/settings/keys).

2. Create a Kubernetes Secret:

```bash
kubectl create secret generic anthropic-api-key \
  --from-literal=token="sk-ant-your-key-here" \
  -n <namespace>
```

3. Reference it in your Repository CR:

```yaml
settings:
  ai:
    enabled: true
    backend: "claude"
    image: "ghcr.io/openshift-pipelines/ai-agents:latest"
    secret_ref:
      name: anthropic-api-key
      key: token
```

### `codex` (OpenAI)

1. Get an API key from the [OpenAI Platform](https://platform.openai.com/api-keys).

2. Create a Kubernetes Secret:

```bash
kubectl create secret generic openai-api-key \
  --from-literal=token="sk-your-openai-key" \
  -n <namespace>
```

3. Reference it in your Repository CR:

```yaml
settings:
  ai:
    enabled: true
    backend: "codex"
    image: "ghcr.io/openshift-pipelines/ai-agents:latest"
    secret_ref:
      name: openai-api-key
      key: token
```

### `gemini` (Google Gemini CLI)

1. Get an API key from [Google AI Studio](https://aistudio.google.com/app/apikey).

2. Create a Kubernetes Secret:

```bash
kubectl create secret generic gemini-api-key \
  --from-literal=token="your-gemini-api-key" \
  -n <namespace>
```

3. Reference it in your Repository CR:

```yaml
settings:
  ai:
    enabled: true
    backend: "gemini"
    image: "ghcr.io/openshift-pipelines/ai-agents:latest"
    secret_ref:
      name: gemini-api-key
      key: token
```

## Vertex AI

The `claude-vertex` backend runs Anthropic Claude through Google Cloud Vertex AI instead of the Anthropic API. Use this when you want to keep traffic inside GCP or need enterprise billing through GCP.

### Prerequisites

- A GCP project with the Vertex AI API enabled
- A service account with the `roles/aiplatform.user` role

### Creating the Service Account

```bash
# Create service account
gcloud iam service-accounts create pac-ai-analysis \
  --display-name "PAC AI Analysis"

# Grant Vertex AI user role
gcloud projects add-iam-policy-binding YOUR_PROJECT_ID \
  --member "serviceAccount:pac-ai-analysis@YOUR_PROJECT_ID.iam.gserviceaccount.com" \
  --role "roles/aiplatform.user"

# Download a JSON key
gcloud iam service-accounts keys create /tmp/sa-key.json \
  --iam-account pac-ai-analysis@YOUR_PROJECT_ID.iam.gserviceaccount.com
```

### Storing the Credentials

```bash
kubectl create secret generic gcp-sa-key \
  --from-file=token=/tmp/sa-key.json \
  -n <namespace>
```

### Repository CR Configuration

```yaml
settings:
  ai:
    enabled: true
    backend: "claude-vertex"
    image: "ghcr.io/openshift-pipelines/ai-agents:latest"
    secret_ref:
      name: gcp-sa-key
      key: token
    vertex_project_id: "your-gcp-project-id"
    vertex_region: "us-east5"   # optional, defaults to "global"
```

`vertex_region` is optional. Supported values depend on which regions Vertex AI Claude models are available in; check the [Vertex AI model garden](https://cloud.google.com/vertex-ai/generative-ai/docs/partner-models/use-claude) for current availability.

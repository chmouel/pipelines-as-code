---
title: AI/LLM-Powered Pipeline Analysis
weight: 8
---

{{< tech_preview >}}

Pipelines-as-Code can analyze your CI/CD pipeline runs using Large Language Models (LLMs). When a PipelineRun completes, PAC can spin up a child PipelineRun that runs an AI CLI tool against your repository and posts human-readable analysis directly on your pull requests.

Use this feature when you want automated root-cause analysis of pipeline failures without manually reading through logs.

## Overview

LLM-powered analysis lets you:

- **Analyze failed pipelines** automatically and get root-cause analysis
- **Generate actionable recommendations** for fixing issues
- **Post insights as PR comments or check-run annotations** so your team sees them immediately
- **Configure custom analysis scenarios** using different prompts and triggers
- **Define roles inside your repository** so contributors can ship their own repo roles alongside pipeline code
- **Apply AI-generated fixes** with one click using the "Apply Suggestions" action on GitHub check-runs

## Supported Backends

Pipelines-as-Code runs the analysis inside a Kubernetes PipelineRun using a container image that has CLI AI tools pre-installed. The `backend` field selects which tool to invoke, and `image` points to the container image that provides it.

| Backend | CLI invoked | API token env var |
|---------|-------------|-------------------|
| `claude` | `claude` (Anthropic Claude Code) | `ANTHROPIC_API_KEY` |
| `codex` | `codex` (OpenAI Codex) | `OPENAI_API_KEY` |
| `gemini` | `gemini` (Google Gemini CLI) | `GEMINI_API_KEY` |
| `claude-vertex` | `claude` via Vertex AI | GCP service account JSON (see [Vertex AI]({{< relref "/docs/guides/llm-analysis/api-setup#vertex-ai" >}})) |
| `opencode` | `opencode` | GCP service account JSON |

The pre-built image `ghcr.io/openshift-pipelines/ai-agents:latest` ships all of these tools. You can also build your own image as long as the selected backend's CLI binary is on `$PATH`.

See [Model Selection]({{< relref "/docs/guides/llm-analysis/model-and-triggers#model-selection" >}}) for guidance on choosing the right model for each backend.

## Configuration

To enable LLM-powered analysis, configure the `spec.settings.ai` section in your Repository CR.

```yaml
apiVersion: pipelinesascode.tekton.dev/v1alpha1
kind: Repository
metadata:
  name: my-repo
spec:
  url: "https://github.com/org/repo"
  settings:
    ai:
      enabled: true
      backend: "claude"
      image: "ghcr.io/openshift-pipelines/ai-agents:latest"
      timeout_seconds: 300
      max_tokens: 2000
      secret_ref:
        name: "anthropic-api-key"
        key: "token"
      roles:
        - name: "failure-analysis"
          prompt: |
            You are a DevOps expert. Analyze this failed pipeline and:
            1. Identify the root cause
            2. Suggest specific fixes
            3. Recommend preventive measures
          context_items:
            error_content: true
            container_logs:
              enabled: true
              max_lines: 100
            diff_content: true
            files:
              - "AGENTS.md"
          output: "check-run"
```

## Configuration Fields

The following tables describe every field available in the `spec.settings.ai` configuration block.

### Top-Level Settings

| Field                | Type    | Required | Description                                                                                    |
| -------------------- | ------- | -------- | ---------------------------------------------------------------------------------------------- |
| `enabled`            | boolean | Yes      | Enable/disable LLM analysis                                                                    |
| `backend`            | string  | Yes      | CLI backend to run: `claude`, `codex`, `gemini`, `claude-vertex`, or `opencode`                |
| `image`              | string  | Yes      | Container image containing the backend CLI binary                                              |
| `secret_ref`         | object  | Yes*     | Reference to Kubernetes secret with API key. Not required for `claude-vertex` (uses GCP creds) |
| `timeout_seconds`    | integer | No       | Total PipelineRun timeout in seconds (1-900, default: 30)                                      |
| `max_tokens`         | integer | No       | Maximum response tokens (1-4000, default: 1000)                                                |
| `vertex_project_id`  | string  | Yes**    | GCP project ID. Required when `backend` is `claude-vertex`                                     |
| `vertex_region`      | string  | No       | GCP region for Vertex AI (default: `global`)                                                   |
| `roles`              | array   | No       | List of analysis scenarios. Optional when repo roles are present in `.tekton/ai/`              |

\* `secret_ref` is not used for `claude-vertex`; provide GCP credentials via `secret_ref` as a service account JSON file instead (see [Vertex AI setup]({{< relref "/docs/guides/llm-analysis/api-setup#vertex-ai" >}})).  
\*\* Required only when `backend: claude-vertex`.

### Analysis Roles

Each role defines a specific analysis scenario. You can configure multiple roles to handle different types of pipeline events (for example, one role for security failures and another for general build failures).

| Field           | Type    | Required | Description                                                                                     |
| --------------- | ------- | -------- | ----------------------------------------------------------------------------------------------- |
| `name`          | string  | Yes      | Unique identifier for this role                                                                 |
| `prompt`        | string  | Yes      | Prompt template for the LLM                                                                     |
| `model`         | string  | No       | Model name (see backend docs for available models). Uses backend default if not specified.      |
| `max_tokens`    | integer | No       | Override the top-level `max_tokens` for this role (1-4000)                                      |
| `on_cel`        | string  | No       | CEL expression for conditional triggering. If not specified, runs only for failed PipelineRuns. |
| `output`        | string  | No       | Output destination: `pr-comment` (default) or `check-run`                                       |
| `context_items` | object  | No       | Configuration for context inclusion                                                             |

### Context Items

Context items control what information Pipelines-as-Code sends to the LLM provider. Choose carefully, because more context means higher token usage and cost.

| Field                      | Type    | Description                                                                   |
| -------------------------- | ------- | ----------------------------------------------------------------------------- |
| `commit_content`           | boolean | Include commit information (see Commit Fields below)                          |
| `pr_content`               | boolean | Include PR title, description, metadata                                       |
| `error_content`            | boolean | Include error messages and failures                                           |
| `container_logs.enabled`   | boolean | Include container/task logs                                                   |
| `container_logs.max_lines` | integer | Limit log lines (1-1000, default: 50). ⚠️ High values may impact performance  |
| `diff_content`             | boolean | Include the pull request code diff (truncated at 10,000 characters)           |
| `files`                    | array   | List of repository file paths to include verbatim (e.g., `AGENTS.md`)         |

#### Commit Fields

When you set `commit_content: true`, Pipelines-as-Code includes the following fields in the data sent to the LLM provider:

| Field | Type | Description | Example |
| --- | --- | --- | --- |
| `commit.sha` | string | Commit SHA hash | `"abc123def456..."` |
| `commit.message` | string | Commit title (first line/paragraph) | `"feat: add new feature"` |
| `commit.url` | string | Web URL to view the commit | `"https://github.com/org/repo/commit/abc123"` |
| `commit.full_message` | string | Complete commit message (if different from title) | `"feat: add new feature\n\nDetailed description..."` |
| `commit.author.name` | string | Author's name | `"John Doe"` |
| `commit.author.date` | timestamp | When the commit was authored | `"2024-01-15T10:30:00Z"` |
| `commit.committer.name` | string | Committer's name (may differ from author) | `"GitHub"` |
| `commit.committer.date` | timestamp | When the commit was committed | `"2024-01-15T10:31:00Z"` |

**Privacy and Security Notes:**

- Pipelines-as-Code **intentionally excludes email addresses** from the commit context to protect personally identifiable information (PII) when sending data to external LLM providers.
- Fields appear only if your Git provider makes them available. Some providers supply limited information (for example, Bitbucket Cloud provides only the author name).
- Author and committer may be the same person or different (for example, when using `git commit --amend` or rebasing).

#### Code Diff

When you set `diff_content: true`, Pipelines-as-Code fetches the pull request diff and includes it in the context sent to the LLM provider. This gives the model visibility into what code was actually changed, which is useful for root-cause analysis when a test or build fails due to a recent change.

- The diff is truncated at **10,000 characters** to stay within token budgets. If the diff exceeds this limit, it is cut with a `[diff truncated]` marker.
- For push events or when no pull request is associated, the diff will be empty.
- Supported providers: GitHub, Gitea/Forgejo. Other providers return an empty diff.

#### Repository Files

The `files` field lets you include the content of specific files from your repository in the LLM context. This is useful for providing standing instructions (such as an `AGENTS.md` file) or project-specific guidelines that help the LLM produce more relevant analysis.

```yaml
context_items:
  files:
    - "AGENTS.md"
    - "docs/runbook.md"
```

- Each file is fetched from the repository at the pull request's head commit SHA.
- If a file does not exist, it is silently skipped with a warning in the controller logs.
- Be mindful of file sizes — large files consume tokens and increase cost.

## Repo Roles

In addition to roles defined in the Repository CR, you can ship AI analysis roles directly in your repository as Markdown files. This lets contributors tune prompts and triggers through a pull request, without needing cluster access.

### File Format

Create files under `.tekton/ai/<name>.md`. Each file uses YAML frontmatter for metadata and a Markdown body for the prompt:

```markdown
---
name: failure-analysis
output: check-run
max_tokens: 500
on_cel: 'body.pipelineRun.status.conditions[0].reason == "Failed"'
context_items:
  error_content: true
  container_logs:
    enabled: true
    max_lines: 100
---
You are a CI expert. Analyze the pipeline failure above and provide:
1. The likely root cause
2. The exact steps needed to fix it
3. How to prevent this class of failure in future
```

The frontmatter fields mirror the CR role fields:

| Field           | Type    | Required | Description                                                                                 |
| --------------- | ------- | -------- | ------------------------------------------------------------------------------------------- |
| `name`          | string  | Yes      | Must match the filename (without `.md`). Only letters, digits, `_`, and `-` are allowed.   |
| `output`        | string  | No       | `pr-comment` or `check-run`. Defaults to `pr-comment`.                                     |
| `model`         | string  | No       | Model override for this role. Falls back to the backend default if omitted.                 |
| `max_tokens`    | integer | No       | Response length cap for this role (1-4000). Falls back to the CR-level `max_tokens`.        |
| `on_cel`        | string  | No       | CEL expression controlling when this role runs. Defaults to failed PipelineRuns only.       |
| `context_items` | object  | No       | Same fields as CR roles: `error_content`, `container_logs`, `diff_content`, etc.            |

The Markdown body (everything after the closing `---`) is the prompt. It must not be empty.

### Auto-discovery

Pipelines-as-Code automatically discovers all `.md` files under `.tekton/ai/` on every PipelineRun completion. No annotation or opt-in is required — simply adding a file to the directory is enough. Each file's `on_cel` expression controls whether it actually runs for a given PipelineRun.

### Merging with CR Roles

Repo roles are merged with any roles already defined in the Repository CR. Both sets are evaluated independently against the same CEL context. A CR role and a repo role can have the same trigger but different prompts, and both will produce their own analysis.

### Validation

Pipelines-as-Code validates each repo role file before running it:

- The `name` in the frontmatter must match the filename.
- The prompt body must not be empty.
- The `output` field, when set, must be `pr-comment` or `check-run`.
- The `on_cel` expression is type-checked at load time — syntax errors are caught before the PipelineRun is submitted.
- If a file is invalid or missing, Pipelines-as-Code logs a warning and skips that role; other repo roles and CR roles continue to run.

## Apply Suggestions Action

When a role uses `output: check-run`, Pipelines-as-Code posts the analysis result as a GitHub check-run. If the AI backend produced a concrete code fix while analysing the repository, PAC also attaches an **"Apply Suggestions"** action button to that check-run.

{{< callout type="warning" >}}
The Apply Suggestions action requires a GitHub App installation. Personal access tokens do not support check-run request actions.
{{< /callout >}}

### How It Works

1. The analysis PipelineRun completes and the backend has edited files in its checkout.
2. PAC captures the `git diff` as a gzip+base64 machine patch embedded in the PipelineRun logs.
3. PAC posts the check-run with the analysis text and an "Apply Suggestions" action button.
4. When a user clicks **Apply Suggestions**, GitHub sends a `check_run.requested_action` webhook to PAC.
5. PAC validates the webhook, retrieves the machine patch from the original analysis PipelineRun, and creates a **fix PipelineRun**.
6. The fix PipelineRun clones the repository, applies the patch with `git apply`, commits the changes, and pushes them to the PR branch.
7. PAC posts an **"AI Fix"** check-run on the PR reporting the result (success or failure).

### Requirements

- `output: check-run` must be set on the role.
- The AI backend must have edited files in the repository checkout during analysis. If the backend only produced a prose response without any file edits, no patch is captured and the "Apply Suggestions" button does not appear.
- The repository must be hosted on GitHub and PAC must be installed as a GitHub App.

### Example Configuration

```yaml
roles:
  - name: "failure-fixer"
    prompt: |
      Analyze this CI failure. If the root cause has a clear, safe fix,
      edit the relevant files in the repository checkout directly.
      Do not commit or push — just modify the files.
    output: "check-run"
    context_items:
      error_content: true
      container_logs:
        enabled: true
        max_lines: 150
      diff_content: true
```

### Limitations

- Only one fix PipelineRun is created per analysis check-run. Clicking "Apply Suggestions" multiple times has no additional effect once the fix PipelineRun exists.
- The fix PipelineRun pushes directly to the PR head branch. Make sure contributors understand that clicking "Apply Suggestions" will add a commit to their branch.
- Large or binary diffs may be skipped if encoding exceeds limits.

---
title: Model Selection and Triggers
weight: 1
---

This page explains how to choose the right LLM model for each analysis role and how to use CEL expressions to control when Pipelines-as-Code triggers analysis. Use model selection to balance cost and capability, and use CEL triggers to limit analysis to the events that matter.

## Model Selection

Each analysis role can specify a different model. If you do not set `model`, the backend CLI uses its own default. Choosing the right model lets you balance cost against analysis depth.

Backend model names follow each CLI's own convention:

| Backend | Example models |
|---------|---------------|
| `claude` | `claude-opus-4-5`, `claude-sonnet-4-5`, `claude-haiku-4-5` |
| `codex` | `codex-mini-latest`, `gpt-4.1`, `o4-mini` |
| `gemini` | `gemini-2.5-pro`, `gemini-2.5-flash` |

Consult the documentation for your chosen backend to find current model names.

### Example: Assigning Different Models per Role

The following example uses the `claude` backend and assigns a different model to each role:

```yaml
settings:
  ai:
    enabled: true
    backend: "claude"
    image: "ghcr.io/openshift-pipelines/ai-agents:latest"
    secret_ref:
      name: "anthropic-api-key"
      key: "token"
    roles:
      # Use the most capable model for deep security analysis
      - name: "security-analysis"
        model: "claude-opus-4-5"
        prompt: "Analyze security failures..."

      # Use default model for general analysis
      - name: "general-failure"
        # No model specified - uses backend default
        prompt: "Analyze this failure..."

      # Use a fast, economical model for quick checks
      - name: "quick-check"
        model: "claude-haiku-4-5"
        prompt: "Quick diagnosis..."
```

## Image Selection

By default every role shares the single `image` defined at the top level of `ai_analysis`. When
different roles require different container images — for example a lightweight image for quick
failure summaries and a heavier image with additional tools for code rewrites — you can override
the image per role using the `image` field.

When `image` is set on a role, Pipelines-as-Code uses that image for the child PipelineRun it
creates for that role. All other roles without an `image` field continue to use the global default.

### Example: Per-Role Image Override

```yaml
settings:
  ai_analysis:
    enabled: true
    backend: "claude"
    image: "ghcr.io/openshift-pipelines/ai-agents:latest"   # default for all roles
    secret_ref:
      name: "anthropic-api-key"
      key: "token"
    roles:
      # This role uses the shared default image
      - name: "failure-summary"
        prompt: "Summarise the failure in one paragraph."

      # This role overrides the image with a custom one
      - name: "code-rewrite"
        image: "ghcr.io/chmouel/agents-image:latest"
        prompt: "Rewrite the failing code to fix the issue."
        output: "check-run"
```

The same `image` field is available in repo roles loaded from `.tekton/ai/<name>.md`:

```markdown
---
name: code-rewrite
image: ghcr.io/chmouel/agents-image:latest
output: check-run
---
Rewrite the failing code to fix the issue.
```

## CEL Expressions for Triggers

By default, Pipelines-as-Code runs LLM analysis only for failed PipelineRuns. CEL (Common Expression Language) expressions in the `on_cel` field let you refine this behavior -- for example, restricting analysis to a specific branch or enabling it for successful runs too.

If you omit `on_cel`, the role executes for all failed PipelineRuns.

### Overriding the Default Behavior

To run analysis for **all PipelineRuns** (both successful and failed), set `on_cel: 'true'`:

```yaml
roles:
  - name: "pipeline-summary"
    prompt: "Generate a summary of this pipeline run..."
    on_cel: 'true'  # Runs for ALL pipeline runs, not just failures
    output: "pr-comment"
```

This is useful when you want to:

- Generate summaries for every PipelineRun
- Track metrics for successful runs
- Post automated messages on successful builds
- Report on build performance

### Example CEL Expressions

```yaml
# Run on ALL pipeline runs (overrides default failed-only behavior)
on_cel: 'true'

# Only on successful runs (e.g., for generating success reports)
on_cel: 'body.pipelineRun.status.conditions[0].reason == "Succeeded"'

# Only on pull requests (in addition to default failed-only check)
on_cel: 'body.event.event_type == "pull_request"'

# Only on main branch
on_cel: 'body.event.base_branch == "main"'

# Only on default branch (works across repos with different default branches)
on_cel: 'body.event.base_branch == body.event.default_branch'

# Skip analysis for bot users
on_cel: 'body.event.sender != "dependabot[bot]"'

# Only for PRs with specific labels
on_cel: '"needs-review" in body.event.pull_request_labels'

# Only when triggered by comment
on_cel: 'body.event.trigger_comment.startsWith("/analyze")'

# Combine conditions
on_cel: 'body.pipelineRun.status.conditions[0].reason == "Failed" && body.event.event_type == "pull_request"'
```

### Available CEL Context Fields

The following tables list all fields you can reference in `on_cel` expressions. Pipelines-as-Code populates these fields from the PipelineRun status, the Repository CR, and the Git provider event.

#### Top-Level Context

| Field              | Type              | Description                                      |
| ------------------ | ----------------- | ------------------------------------------------ |
| `body.pipelineRun` | object            | Full PipelineRun object with status and metadata |
| `body.repository`  | object            | Full Repository CR object                        |
| `body.event`       | object            | Event information (see Event Fields below)       |
| `pac`              | map[string]string | PAC parameters map                               |

#### Event Fields (`body.event.*`)

**Event Type and Trigger:**

| Field            | Type   | Description                              | Example                                            |
| ---------------- | ------ | ---------------------------------------- | -------------------------------------------------- |
| `event_type`     | string | Event type from provider                 | `"pull_request"`, `"push"`, `"Merge Request Hook"` |
| `trigger_target` | string | Normalized trigger type across providers | `"pull_request"`, `"push"`                         |

**Branch and Commit Information:**

| Field            | Type   | Description                               | Example                   |
| ---------------- | ------ | ----------------------------------------- | ------------------------- |
| `sha`            | string | Commit SHA                                | `"abc123def456..."`       |
| `sha_title`      | string | Commit title/message                      | `"feat: add new feature"` |
| `base_branch`    | string | Target branch for PR (or branch for push) | `"main"`                  |
| `head_branch`    | string | Source branch for PR (or branch for push) | `"feature-branch"`        |
| `default_branch` | string | Default branch of the repository          | `"main"` or `"master"`    |

**Repository Information:**

| Field          | Type   | Description             | Example     |
| -------------- | ------ | ----------------------- | ----------- |
| `organization` | string | Organization/owner name | `"my-org"`  |
| `repository`   | string | Repository name         | `"my-repo"` |

**URLs:**

| Field      | Type   | Description            | Example                                       |
| ---------- | ------ | ---------------------- | --------------------------------------------- |
| `url`      | string | Web URL to repository  | `"https://github.com/org/repo"`               |
| `sha_url`  | string | Web URL to commit      | `"https://github.com/org/repo/commit/abc123"` |
| `base_url` | string | Web URL to base branch | `"https://github.com/org/repo/tree/main"`     |
| `head_url` | string | Web URL to head branch | `"https://github.com/org/repo/tree/feature"`  |

**User Information:**

| Field    | Type   | Description                  | Example                          |
| -------- | ------ | ---------------------------- | -------------------------------- |
| `sender` | string | User who triggered the event | `"user123"`, `"dependabot[bot]"` |

**Pull Request Fields (only populated for PR events):**

| Field                 | Type     | Description          | Example                           |
| --------------------- | -------- | -------------------- | --------------------------------- |
| `pull_request_number` | int      | PR/MR number         | `42`                              |
| `pull_request_title`  | string   | PR/MR title          | `"Add new feature"`               |
| `pull_request_labels` | []string | List of PR/MR labels | `["enhancement", "needs-review"]` |

**Comment Trigger Fields (only when triggered by comment):**

| Field             | Type   | Description                    | Example                |
| ----------------- | ------ | ------------------------------ | ---------------------- |
| `trigger_comment` | string | Comment that triggered the run | `"/test"`, `"/retest"` |

**Webhook Fields:**

| Field                | Type   | Description                              | Example             |
| -------------------- | ------ | ---------------------------------------- | ------------------- |
| `target_pipelinerun` | string | Target PipelineRun for incoming webhooks | `"my-pipeline-run"` |

#### Excluded Fields

Pipelines-as-Code **intentionally excludes** the following fields from the CEL context for security and architectural reasons:

- **`event.Provider`** -- Contains sensitive API tokens and webhook secrets.
- **`event.Request`** -- Contains raw HTTP headers and payload, which may include secrets.
- **`event.InstallationID`**, **`AccountID`**, **`GHEURL`**, **`CloneURL`** -- Provider-specific internal identifiers and URLs.
- **`event.SourceProjectID`**, **`TargetProjectID`** -- GitLab-specific internal identifiers.
- **`event.State`** -- Internal state management fields.
- **`event.Event`** -- Raw provider event object (already represented in the structured fields above).

### Output Destinations

Output destinations control where Pipelines-as-Code posts the LLM analysis results.

#### PR Comment

Posts analysis as a comment on the pull request:

```yaml
output: "pr-comment"
```

Benefits of PR comments:

- Visible to all developers working on the pull request.
- Pipelines-as-Code can update the comment with new analysis on subsequent runs.
- Easy to discuss and follow up on directly in the PR conversation.

#### Check Run

Posts analysis as a GitHub check-run on the pull request:

```yaml
output: "check-run"
```

{{< callout type="warning" >}}
Check-run output requires a GitHub App installation. It is not available when using a personal access token.
{{< /callout >}}

Benefits of check-runs:

- Analysis appears in the **Checks** tab alongside your CI results.
- The check-run title shows the role name, making it easy to distinguish multiple analysis roles.
- When the backend produces a concrete fix, an **"Apply Suggestions"** action button appears on the check-run. Clicking it triggers PAC to apply the AI-generated patch and push it to the PR branch. See [Apply Suggestions Action]({{< relref "/docs/guides/llm-analysis#apply-suggestions-action" >}}) for details.

When to choose `check-run` over `pr-comment`:

- You want the analysis result separated from the PR conversation thread.
- You want to offer the one-click "Apply Suggestions" capability to contributors.
- You are running multiple analysis roles and want each result to be independently re-runnable.

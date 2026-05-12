---
title: "Settings"
weight: 5
---

This page documents every field available under the Repository CR `settings` block. Use this reference when you need to configure authorization policies, provider-specific behavior, or provenance settings at the repository level.

## Settings fields

{{< param name="pipelinerun_provenance" type="string" id="param-pipelinerun-provenance" >}}
Controls where Pipelines-as-Code fetches PipelineRun definitions from. Options:

- `source` - Fetch definitions from the event source branch/SHA (default)
- `default_branch` - Fetch definitions from the repository default branch

```yaml
settings:
  pipelinerun_provenance: "source"
```

{{< /param >}}

{{< param name="github_app_token_scope_repos" type="[]string" id="param-github-app-token-scope-repos" >}}
Lists additional repositories that Pipelines-as-Code includes in the GitHub App token scope. Use this when your PipelineRuns need to access other repositories on the same GitHub App installation, such as shared libraries or common task repositories.

```yaml
settings:
  github_app_token_scope_repos:
    - "organization/shared-library"
    - "organization/common-tasks"
```

{{< /param >}}

{{< param name="gitops_command_prefix" type="string" id="param-gitops-command-prefix" >}}
Sets a custom prefix for GitOps commands such as `/test`, `/retest`, and `/cancel`.
Use a plain word such as `pac`; Pipelines-as-Code adds the leading slash
automatically. For command behavior and examples, see the
[GitOps commands guide]({{< relref "/docs/guides/gitops-commands/advanced" >}}).

```yaml
settings:
  gitops_command_prefix: "pac"
```

{{< /param >}}

{{< param name="policy" type="Policy" >}}
Defines authorization policies for the repository. These policies control which users can trigger PipelineRuns under different conditions.

{{< param-group label="Show Policy Fields" >}}

{{< param name="policy.ok_to_test" type="[]string" id="param-policy-ok-to-test" >}}
Lists the team names whose members can trigger PipelineRuns on pull requests from external contributors by commenting `/ok-to-test`. These are typically maintainers or trusted contributors who can authorize CI for external contributions.

```yaml
settings:
  policy:
    ok_to_test:
      - "maintainer-username"
      - "trusted-contributor"
```

{{< /param >}}

{{< param name="policy.pull_request" type="[]string" id="param-policy-pull-request" >}}
Lists the team names whose members can trigger PipelineRuns on their own pull requests, even if they would not normally have permission. Use this to grant specific external contributors the ability to run CI.

```yaml
settings:
  policy:
    pull_request:
      - "external-contributor"
      - "community-member"
```

{{< /param >}}

{{< /param-group >}}

```yaml
settings:
  policy:
    ok_to_test:
      - "team-lead"
      - "senior-dev"
    pull_request:
      - "trusted-external"
```

{{< /param >}}

## Provider-specific settings

### GitHub settings

{{< param name="github" type="GithubSettings" >}}
Configures GitHub-specific behavior for repositories hosted on GitHub.

{{< param-group label="Show GitHub Settings Fields" >}}

{{< param name="github.comment_strategy" type="string" id="param-github-comment-strategy" >}}
Controls how Pipelines-as-Code posts comments on GitHub pull requests. Options:

- `""` (empty) - Default behavior (create new comments)
- `disable_all` - Disables all comments on pull requests
- `update` - Updates a single comment per PipelineRun on every trigger

```yaml
settings:
  github:
    comment_strategy: "update"
```

{{< /param >}}

{{< /param-group >}}
{{< /param >}}

### GitLab settings

{{< param name="gitlab" type="GitlabSettings" >}}
Configures GitLab-specific behavior for repositories hosted on GitLab.

{{< param-group label="Show GitLab Settings Fields" >}}

{{< param name="gitlab.comment_strategy" type="string" id="param-gitlab-comment-strategy" >}}
Controls how Pipelines-as-Code posts comments on GitLab merge requests. Options:

- `""` (empty) - Default behavior (create new comments)
- `disable_all` - Disables all comments on merge requests
- `update` - Updates a single comment per PipelineRun on every trigger

```yaml
settings:
  gitlab:
    comment_strategy: "update"
```

{{< /param >}}

{{< /param-group >}}
{{< /param >}}

### Forgejo/Gitea settings

{{< param name="forgejo" type="ForgejoSettings" >}}
Configures Forgejo- and Gitea-specific behavior for repositories hosted on Forgejo or Gitea.

{{< param-group label="Show Forgejo Settings Fields" >}}

{{< param name="forgejo.user_agent" type="string" id="param-forgejo-user-agent" >}}
Sets the User-Agent header on API requests to the Gitea/Forgejo instance. This is useful when the instance is behind an AI scraping protection proxy (e.g., Anubis) that blocks requests without a recognized User-Agent. Defaults to `pipelines-as-code/<version>` when left empty.

```yaml
settings:
  forgejo:
    user_agent: "MyCustomBot/1.0"
```

{{< /param >}}

{{< param name="forgejo.comment_strategy" type="string" id="param-forgejo-comment-strategy" >}}
Controls how Pipelines-as-Code posts comments on Forgejo/Gitea pull requests. Options:

- `""` (empty) - Default behavior (create new comments)
- `disable_all` - Disables all comments on pull requests
- `update` - Updates a single comment per PipelineRun on every trigger

```yaml
settings:
  forgejo:
    comment_strategy: "update"
```

{{< /param >}}

{{< /param-group >}}
{{< /param >}}

## AI analysis settings

{{< param name="ai" type="AIAnalysisConfig" >}}
Configures AI/LLM-powered analysis of pipeline failures and pull request content.

{{< param-group label="Show AIAnalysisConfig Fields" >}}

{{< param name="ai.enabled" type="boolean" required="true" id="param-ai-enabled" >}}
Enables or disables AI analysis for this repository.

```yaml
settings:
  ai:
    enabled: true
```

{{< /param >}}

{{< param name="ai.backend" type="string" required="true" id="param-ai-backend" >}}
Selects the CLI backend to run for analysis. Supported values:

- `claude` — Anthropic Claude Code CLI (uses `ANTHROPIC_API_KEY`)
- `codex` — OpenAI Codex CLI (uses `OPENAI_API_KEY`)
- `gemini` — Google Gemini CLI (uses `GEMINI_API_KEY`)
- `claude-vertex` — Anthropic Claude via Google Cloud Vertex AI (uses GCP service account JSON)
- `opencode` — OpenCode CLI (uses GCP service account JSON)

```yaml
settings:
  ai:
    backend: "claude"
```

{{< /param >}}

{{< param name="ai.image" type="string" required="true" id="param-ai-image" >}}
Container image that contains the selected backend CLI binary. The pre-built image
`ghcr.io/openshift-pipelines/ai-agents:latest` ships all supported backends.

```yaml
settings:
  ai:
    image: "ghcr.io/openshift-pipelines/ai-agents:latest"
```

{{< /param >}}

{{< param name="ai.secret_ref" type="Secret" required="true" id="param-ai-secret-ref" >}}
References the Kubernetes Secret containing the backend API token (or GCP service account JSON for Vertex AI backends). See [API Keys and Credentials]({{< relref "/docs/guides/llm-analysis/api-setup" >}}) for backend-specific instructions.

```yaml
settings:
  ai:
    secret_ref:
      name: anthropic-api-key
      key: token
```

{{< /param >}}

{{< param name="ai.vertex_project_id" type="string" id="param-ai-vertex-project-id" >}}
GCP project ID. Required when `backend` is `claude-vertex`.

```yaml
settings:
  ai:
    vertex_project_id: "my-gcp-project"
```

{{< /param >}}

{{< param name="ai.vertex_region" type="string" id="param-ai-vertex-region" >}}
GCP region for Vertex AI (default: `global`). Only used when `backend` is `claude-vertex`.

```yaml
settings:
  ai:
    vertex_region: "us-east5"
```

{{< /param >}}

{{< param name="ai.timeout_seconds" type="integer" id="param-ai-timeout-seconds" >}}
Sets the maximum time in seconds for the analysis PipelineRun (default: 30). Valid range: 1-900.

```yaml
settings:
  ai:
    timeout_seconds: 60
```

{{< /param >}}

{{< param name="ai.max_tokens" type="integer" id="param-ai-max-tokens" >}}
Sets the maximum response length from the LLM (default: 1000). Valid range: 1-4000 tokens.

```yaml
settings:
  ai:
    max_tokens: 2000
```

{{< /param >}}

{{< param name="ai.roles" type="[]AnalysisRole" id="param-ai-roles" >}}
Defines the analysis scenarios and their configurations. This field is optional when repo roles are present in `.tekton/ai/`. See [Repo Roles]({{< relref "/docs/guides/llm-analysis#repo-roles" >}}).

{{< param-group label="Show AnalysisRole Fields" >}}

{{< param name="roles[].name" type="string" required="true" id="param-roles-name" >}}
Sets a unique identifier for this analysis role.
{{< /param >}}

{{< param name="roles[].prompt" type="string" required="true" id="param-roles-prompt" >}}
Defines the base prompt template that Pipelines-as-Code sends to the LLM.
{{< /param >}}

{{< param name="roles[].image" type="string" id="param-roles-image" >}}
Overrides the top-level `image` for this role only. When set, the child PipelineRun for this role
uses this container image instead of the shared default. This lets you use a heavier or specialised
agent image for certain roles (e.g. a code-rewrite role) while keeping a lighter image as the default.
{{< /param >}}

{{< param name="roles[].model" type="string" id="param-roles-model" >}}
Specifies the model for this role. If omitted, the backend CLI uses its own default model. Consult each backend's documentation for available model names.
{{< /param >}}

{{< param name="roles[].on_cel" type="string" id="param-roles-on-cel" >}}
Defines a CEL expression that determines when Pipelines-as-Code triggers this role.
Use the structured LLM CEL context, such as `body.event.*`,
`body.pipelineRun.*`, and `body.repository.*`.
{{< /param >}}

{{< param name="roles[].output" type="string" id="param-roles-output" >}}
Specifies where Pipelines-as-Code sends the analysis results. Supported values:

- `pr-comment` (default) — posts the result as a pull request comment
- `check-run` — posts the result as a GitHub check-run annotation (GitHub App only)
{{< /param >}}

{{< param name="roles[].context_items" type="ContextConfig" id="param-roles-context-items" >}}
Configures what context data Pipelines-as-Code includes in the analysis request.

{{< param-group label="Show ContextConfig Fields" >}}

{{< param name="context_items.commit_content" type="boolean" id="param-context-items-commit-content" >}}
Includes commit message and diff information in the analysis context.
{{< /param >}}

{{< param name="context_items.pr_content" type="boolean" id="param-context-items-pr-content" >}}
Includes pull request title, description, and metadata in the analysis context.
{{< /param >}}

{{< param name="context_items.error_content" type="boolean" id="param-context-items-error-content" >}}
Includes error messages and failure summaries in the analysis context.
{{< /param >}}

{{< param name="context_items.container_logs" type="ContainerLogsConfig" id="param-context-items-container-logs" >}}
Configures whether Pipelines-as-Code includes container/task logs in the analysis context.

{{< param-group label="Show ContainerLogsConfig Fields" >}}

{{< param name="container_logs.enabled" type="boolean" id="param-container-logs-enabled" >}}
Enables or disables container log inclusion.
{{< /param >}}

{{< param name="container_logs.max_lines" type="integer" id="param-container-logs-max-lines" >}}
Sets the maximum number of log lines to include (default: 50). Valid range: 1-1000 lines.
{{< /param >}}

{{< /param-group >}}
{{< /param >}}

{{< /param-group >}}
{{< /param >}}

{{< /param-group >}}

```yaml
settings:
  ai:
    roles:
      - name: "failure-analysis"
        prompt: "Analyze the following CI/CD failure and suggest fixes"
        model: "gpt-4"
        on_cel: 'body.event.event_type == "pull_request" && body.pipelineRun.status.conditions[0].status == "False"'
        context_items:
          commit_content: true
          error_content: true
          container_logs:
            enabled: true
            max_lines: 100
```

{{< /param >}}

{{< /param-group >}}
{{< /param >}}

## Complete example

```yaml
apiVersion: pipelinesascode.tekton.dev/v1alpha1
kind: Repository
metadata:
  name: example-repo
  namespace: pipelines-as-code
spec:
  url: "https://github.com/organization/repository"
  settings:
    # Provenance configuration
    pipelinerun_provenance: "source"

    # GitHub App token scoping
    github_app_token_scope_repos:
      - "organization/shared-tasks"
      - "organization/common-library"

    # Authorization policies
    policy:
      ok_to_test:
        - "team-lead"
        - "senior-engineer"
        - "trusted-maintainer"
      pull_request:
        - "approved-contributor"

    # GitHub-specific settings
    github:
      comment_strategy: "update"

    # AI analysis configuration
    ai:
      enabled: true
      backend: "claude"
      image: "ghcr.io/openshift-pipelines/ai-agents:latest"
      secret_ref:
        name: anthropic-api-key
        key: token
      timeout_seconds: 300
      max_tokens: 2000
      roles:
        - name: "pr-failure-analysis"
          prompt: |
            You are a CI/CD expert. Analyze the following pipeline failure and provide:
            1. Root cause analysis
            2. Specific fix recommendations
            3. Prevention strategies
          on_cel: 'body.event.event_type == "pull_request" && body.pipelineRun.status.conditions[0].status == "False"'
          output: "check-run"
          context_items:
            commit_content: true
            pr_content: true
            error_content: true
            container_logs:
              enabled: true
              max_lines: 100
        - name: "security-review"
          prompt: "Review this change for potential security issues"
          on_cel: '"security-review" in body.event.pull_request_labels'
          context_items:
            commit_content: true
            pr_content: true
```

## Settings inheritance

You can define settings at the global level (in the ConfigMap) or the repository level (in the Repository CR). When both exist, repository-level settings override global settings.

## Related resources

- [Repository Spec]({{< relref "repository-spec" >}}) -- Complete Repository specification
- [ConfigMap Reference]({{< relref "configmap" >}}) -- Global configuration settings

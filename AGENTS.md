# Pipelines-as-Code Project Guide

## Project Overview

- Tekton pipeline integration with Git providers: GitHub, GitLab, Bitbucket, Gitea
- Targets arm64 and amd64 architectures
- Uses `ko` for building container images in development

## Code Quality Workflow

### After Editing Code

**Go files**: `make fumpt` to format
**Python files**: `make fix-python-errors`
**Markdown files**: `make fix-markdownlint && make fix-trailing-spaces`

### Before Committing

1. `make test` - Run test suite
2. `make lint` - Check code quality
3. `make check` - Combined lint + test

**Pre-commit hooks**: Install with `pre-commit install`

- Runs on push (skip with `git push --no-verify`)
- Linters: golangci-lint, yamllint, markdownlint, ruff, shellcheck, vale, codespell
- Skip specific hook: `SKIP=hook-name git push`

## Testing

### Test Structure

- **Use table-driven tests**: Anonymous struct pattern with `tests := []struct{...}{...}`
- **No underscores in test names**: Use PascalCase like `TestGetTektonDir`, not `TestGetTektonDir_Something`
- **Subtest naming**: Use descriptive `name` field for `t.Run(tt.name, ...)`
- **Complex setup**: Add `setup func(t *testing.T, ...)` field to test struct instead of creating separate test functions
- **Example**:

  ```go
  func TestGetTektonDir(t *testing.T) {
      tests := []struct {
          name  string
          event *info.Event
          setup func(t *testing.T, mux *http.ServeMux)
      }{
          {
              name: "simple case",
              event: &info.Event{...},
              setup: func(t *testing.T, mux *http.ServeMux) {
                  t.Helper()
                  mux.HandleFunc("/repos/...", ...)
              },
          },
      }
      for _, tt := range tests {
          t.Run(tt.name, func(t *testing.T) { ... })
      }
  }
  ```

### Assertions

- **Use `gotest.tools/v3/assert`** (never testify or pkg/assert)
  - `assert.NilError(t, err)` - verify no error
  - `assert.Assert(t, condition, msg)` - general assertions
  - `assert.Equal(t, expected, actual)` - equality checks
  - `assert.ErrorContains(t, err, substring)` - error validation

### Test Types

- **Unit tests**: Fast, focused, use mocks from `pkg/test/github`
- **E2E tests**: Require specific setup - always ask user to run and copy output
- **Gitea tests**: Most comprehensive and self-contained

### Commands

- `make test` - Run test suite
- `make test-no-cache` - Run without cache
- `make html-coverage` - Generate coverage report

## Dependencies

- Add: `go get -u dependency`
- **Always run after**: `make vendor` (required!)
- Update Go version: `go mod tidy -go=1.20`
- Remove unnecessary `replace` clauses in go.mod

## Documentation

- Preview: `make dev-docs` (<http://localhost:1313>)
- Hugo with hugo-book theme
- Custom shortcodes: `tech_preview`, `support_matrix`

## Useful Commands

- `make help` - Show all make targets
- `make all` - Build, test, lint
- `make clean` - Clean artifacts
- `make fix-linters` - Auto-fix most linting issues

## Skills

For complex workflows, use these repo-local skills:

- **Commit messages**: Conventional commits with Jira integration, line length validation, required footers. Trigger: "create commit", "commit changes", "generate commit message"
- **Jira tickets**: SRVKP story and bug templates with Jira markdown formatting. Trigger: "create Jira story", "create Jira bug", "SRVKP ticket"

## Current Work in Progress — LLM/AI Pull Request Analysis (`new-ai` branch)

### What This Feature Does

When a Tekton PipelineRun completes (success or failure) PAC can optionally spawn
child PipelineRuns that invoke an LLM backend (claude, codex, gemini, opencode) and
post the analysis result back to the PR as a comment or GitHub check-run annotation.

### Commits Landed

| SHA | Summary |
|-----|---------|
| `8419abf4f` | Run AI analysis as a Kubernetes PipelineRun job |
| `ae336700c` | Include PR diffs and repository files as AI context |
| `71393f115` | Add CLI agent mode (any command-line AI tool can be used) |
| `92899ed12` | Post results as GitHub check-run annotations |

### Key Files

| File | Role |
|------|------|
| `pkg/llm/analyzer.go` | Main orchestration: role triggers, context, result posting |
| `pkg/llm/pipelinerun.go` | Child PipelineRun construction |
| `pkg/llm/context/assembler.go` | Context collection: logs, diffs, errors, files |
| `pkg/llm/templates/analysis.sh` | Shell script executed inside child PipelineRun |
| `pkg/llm/cli/runner.go` | External CLI invocation (JSON stdin/stdout protocol) |
| `pkg/apis/pipelinesascode/v1alpha1/types_llm.go` | CRD schema (`AIAnalysisConfig`) |
| `pkg/reconciler/reconciler.go` | Entry point: calls `llm.ExecuteAnalysis()` |
| `pkg/provider/github/status.go` | Posts check-run annotations |

### Architecture at a Glance

```
Parent PipelineRun completes
  → ReconcileKind() → llm.ExecuteAnalysis()
    → Evaluate CEL per Role → Build context (logs, diffs, errors, files)
    → Create child PipelineRun (fetch-repo + run-analysis)
      → analysis.sh runs CLI tool → outputs AnalysisEnvelope JSON
    → Next reconcile: parse envelope → post PR comment or check-run
```

### Configuration Example

```yaml
spec:
  settings:
    ai_analysis:
      enabled: true
      backend: claude          # claude | codex | gemini | opencode | claude-vertex
      image: ghcr.io/openshift-pipelines/ai-agents:latest
      secret_ref: { name: llm-api-key, key: token }
      timeout_seconds: 900
      roles:
        - name: failure-analyzer
          prompt: "Analyze this CI failure and suggest a fix."
          on_cel: "body.pipelineRun.status.conditions[0].reason == 'Failed'"
          output: pr-comment    # pr-comment | check-run
          context_items:
            error_content: true
            container_logs: { enabled: true, max_lines: 100 }
            diff_content: true
```

### Status

- [x] PipelineRun-based execution
- [x] Context assembly (logs, diffs, errors, PR info, arbitrary files)
- [x] CLI agent mode
- [x] GitHub check-run annotation posting
- [x] Auto-inject `.claude/skills` / `.agents/skills` into the analysis prompt

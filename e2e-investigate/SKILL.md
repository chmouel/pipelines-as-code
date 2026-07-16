---
name: e2e-investigate
description: Investigate Pipelines-as-Code E2E test failures and downloaded artifacts by locating logs, extracting failed tests, normalizing component logs, and correlating test output, controller logs, webhook logs, watcher logs, Kubernetes resources, API instrumentation, and CI artifacts. Use when debugging PAC E2E failures, inspecting logs-e2e-tests artifacts, downloading GitHub Actions E2E logs, or explaining why an E2E job failed.
---

# E2E Test Failure Investigation

## Goal

Identify the failing test, locate the relevant namespace or resource, correlate
the available logs and resource dumps, and summarize the most likely root cause
with evidence. Separate confirmed facts from inference.

## Step 1: Locate Or Prepare Artifacts

First determine whether artifacts are:

- already downloaded locally
- available from GitHub Actions
- packaged in a zip file that still needs extracting

Artifacts usually end up under:

```text
tmp/e2e/logs-e2e-tests-{pattern}-{timestamp}/
```

If the PAC repo has a local `e2e-logs` helper in `PATH` or `.git/bin/e2e-logs`,
prefer it because it downloads or extracts artifacts, normalizes PAC component
logs, prints failed tests, and lists the exact log paths to investigate.

If no local helper is available, use this skill's bundled script:

```bash
<path-to-skill>/scripts/e2e-logs -h
<path-to-skill>/scripts/e2e-logs -u
<path-to-skill>/scripts/e2e-logs \
  "https://github.com/OWNER/REPO/actions/runs/RUN_ID/job/JOB_ID?pr=PR"
<path-to-skill>/scripts/e2e-logs -f \
  "https://github.com/OWNER/REPO/actions/runs/RUN_ID?pr=PR"
```

Before invoking the helper, use the current agent's writable session or artifact
directory when one is available:

```bash
OUTPUT_DIR="<agent-session-directory>/e2e" <path-to-skill>/scripts/e2e-logs ...
```

Do not write artifacts into the repository when a session directory is
available. Copilot, Claude, Codex, and other agents may expose their session
directory differently, so use the path supplied by the active runtime rather
than assuming one vendor-specific layout. The helper also recognizes
`AGENT_SESSION_DIR`, common vendor session-directory variables, and Copilot's
`COPILOT_AGENT_SESSION_ID`. It falls back to `tmp/e2e` only when no session
directory or explicit `OUTPUT_DIR` is available.

The helper is non-interactive. Use `-u` for the newest local
`~/Downloads/logs-e2e-tests*.zip`, or pass a zip path after `-u`.

When the user provides a GitHub Actions job URL containing `/job/JOB_ID`, pass
that URL directly to the helper.

When the user provides a run URL without a job ID:

1. Extract `OWNER/REPO` and `RUN_ID` from the URL.
2. Query failed jobs:

   ```bash
   gh run view RUN_ID --repo OWNER/REPO --json jobs \
     --jq '.jobs[] | select(.conclusion == "failure") | [.name, .url] | @tsv'
   ```

3. Keep only E2E matrix jobs whose names end with an artifact pattern in
   parentheses.
4. If there are no failed E2E jobs, tell the user and stop.
5. Ask the user to choose one failed job with `ask_user`, showing the job name
   and using its full job URL as the selected value.
6. Pass the selected job URL to the helper.

Only use `-f RUN_URL` when the user explicitly asks to download every failed E2E
job from that run.

For only a map of the artifact bundle, read:

```text
<path-to-skill>/references/artifacts-structure.md
```

## Step 2: Find The Failing Test

Start with `e2e-test-output.json` when present:

```bash
jq -r 'select(.Action == "fail") | .Test' e2e-test-output.json
jq -r 'select(.Action == "output" and (.Output | test("assertion failed|FAIL:|panic:|Error "))) | "[\(.Test // "?")] \(.Output)"' e2e-test-output.json
```

Fall back to `e2e-test-output.log`:

```bash
grep -B10 -- "--- FAIL.*Test" e2e-test-output.log
```

Extract:

- test name
- namespace such as `pac-e2e-ns-xxxxx`
- primary error text

## Step 3: Correlate Across Component Logs

Search the namespace or other unique identifiers through the main logs.

### Controller

```bash
grep "pac-e2e-ns-xxxxx" pipelines-as-code-controller.log | head -100
```

Look for reconcile failures, API errors, rate limits, and auth problems.

### Watcher

```bash
grep "pac-e2e-ns-xxxxx" pipelines-as-code-watcher.log
```

Look for PipelineRun transitions and terminal states.

### Webhook

```bash
grep "pac-e2e-ns-xxxxx" pipelines-as-code-webhook.log
```

Look for missing deliveries, parsing errors, or validation failures.

## Step 4: Check Cluster Resources

Inspect Kubernetes events and dumped resources:

```bash
grep -i "error\\|failed\\|warning" events
```

Focus on:

- pod scheduling failures
- quota or image pull problems
- failed PipelineRuns
- broken repository configuration

## Step 5: Check API And Relay Data

Use `api-instrumentation/` for API rate-limit and response issues. Use
`gosmee/main.log` for webhook relay failures or replay problems.

## Step 6: Summarize Findings

Report:

1. which test failed
2. what the error looked like
3. the strongest evidence from logs or resources
4. the likely root cause
5. the next debugging or fix step

Separate confirmed evidence from inference.

## References

Consult the bundled references when needed:

- `<path-to-skill>/references/artifacts-structure.md`
- `<path-to-skill>/references/log-correlation.md`
- `<path-to-skill>/references/common-failures.md`

Use `<path-to-skill>/scripts/e2e-logs` for artifact download, extraction, and
component log normalization when a repo-local helper is not available.

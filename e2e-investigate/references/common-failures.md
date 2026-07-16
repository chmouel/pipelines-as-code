# Common E2E Test Failure Patterns

This document catalogs common failure patterns seen in E2E tests and how to identify them.

## Rate Limiting

### Symptoms

- Test timeouts after waiting for webhook/status
- Controller logs show 403 or 429 errors
- `api-instrumentation/` JSON shows rate limit responses

### Log Pattern

```bash
"msg": "rate limit exceeded"
"status": 403
"x-ratelimit-remaining": "0"
```

### Rate Investigation

```bash
grep -i "rate.limit\|403\|429" pipelines-as-code-controller.log
cat api-instrumentation/*.json | grep -i ratelimit
```

### Rate Causes

- Too many API calls in quick succession
- Token with insufficient permissions
- GitHub secondary rate limits

## Webhook Delivery Failures

### Webhook Symptoms

- Test hangs waiting for webhook
- No entries in controller log for the namespace
- gosmee shows delivery errors

### Log Pattern (gosmee)

```bash
"error": "connection refused"
"status": 502
"msg": "failed to forward webhook"
```

### Webhook Investigation

```bash
grep -i "error\|failed" gosmee/main.log
# Check if webhook was received at all
grep "pac-e2e-ns-xxxxx" pipelines-as-code-webhook.log
```

### Webhook Causes

- Webhook endpoint not ready
- Network issues in test cluster
- gosmee relay problems

## PipelineRun Timeouts

### PipelineRun Symptoms

- Test timeout with "waiting for PipelineRun"
- PipelineRun stuck in Running state
- No completion event

### PipelineRun Investigation

```bash
# Check PipelineRun status
grep "pac-e2e-ns-xxxxx" pac-pipelineruns.yaml | head -50

# Look for timeout in watcher
grep "timeout\|deadline" pipelines-as-code-watcher.log
```

### PipelineRun Causes

- Slow image pulls
- Resource constraints
- Task failures within pipeline

## Authentication Failures

### Auth Symptoms

- 401 errors in logs
- "bad credentials" messages
- Token refresh failures

### Auth Pattern

```bash
"msg": "authentication failed"
"status": 401
"error": "bad credentials"
```

### Auth Investigation

```bash
grep -i "401\|auth\|credential\|token" pipelines-as-code-controller.log
```

### Auth Causes

- Expired tokens
- Incorrect secret configuration
- Revoked app installation

## Resource Creation Failures

### Creation Symptoms

- "already exists" errors
- Namespace conflicts
- PipelineRun not created

### Creation Pattern

```bash
"error": "resource already exists"
"msg": "failed to create PipelineRun"
```

### Creation Investigation

```bash
grep -i "already exists\|conflict\|failed to create" pipelines-as-code-controller.log
grep -i "error\|warning" events
```

### Creation Causes

- Test cleanup didn't complete
- Namespace collision
- CRD not installed

## Git Provider API Errors

### Provider Symptoms

- Cannot fetch pipeline files
- Status update failures
- Comment creation fails

### Provider Pattern

```bash
"msg": "failed to get file content"
"msg": "failed to create status"
"status": 404
```

### Provider Investigation

```bash
grep -i "failed to\|could not\|404\|500" pipelines-as-code-controller.log | grep -v "level.*debug"
```

### Provider Causes

- File not in expected location
- Branch/ref doesn't exist
- Repository permissions

## Kubernetes Events Errors

### Event Symptoms

- Pods not starting
- ImagePullBackOff
- OOMKilled

### Event Investigation

```bash
grep -i "error\|failed\|backoff\|oom" events
```

### Event Causes

- Image not available
- Resource quotas exceeded
- Node pressure

## Flaky Test Indicators

### Timing-Related

- Test passes on retry
- Failure message includes "timeout" or "deadline"
- Different failure point on each run

### Resource Contention

- Multiple tests using same namespace prefix
- Parallel test interference
- Shared cluster state

### Investigation for Flakiness

```bash
# Check if failure is consistent
grep "--- FAIL" e2e-test-output.log

# Look for retry patterns
grep -i "retry\|retrying" pipelines-as-code-controller.log
```

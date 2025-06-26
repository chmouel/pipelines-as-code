# LLM Integration for Pipelines as Code

This package provides LLM (Large Language Model) integration for Pipelines as Code, enabling natural language interaction with CI/CD pipelines.

## Features

### 1. Smart Comment Analysis

Users can interact with pipelines using natural language commands:

- **Action Commands**: `/llm restart the go test pipeline`
- **Informational Queries**: `/llm what is the push to production pipeline`

### 2. Supported Actions

- `test` - Run specific or all test pipelines
- `retest` - Restart specific or all test pipelines  
- `cancel` - Cancel running pipelines
- `query` - Get information about pipelines

### 3. Supported Providers

- OpenAI (GPT-4, GPT-3.5-turbo)
- Anthropic (Claude)
- Extensible for other providers

## Configuration

### Environment Variables

```bash
# LLM Provider Configuration
PAC_LLM_ENABLED=true
PAC_LLM_PROVIDER=openai  # or "anthropic"
PAC_LLM_API_KEY=your-api-key
PAC_LLM_API_ENDPOINT=https://api.openai.com/v1/chat/completions
PAC_LLM_MODEL=gpt-4
PAC_LLM_MAX_TOKENS=1000
PAC_LLM_TEMPERATURE=0.7
```

### Repository Configuration

```yaml
apiVersion: pipelinesascode.tekton.dev/v1alpha1
kind: Repository
metadata:
  name: my-repo
spec:
  url: https://github.com/org/repo
  settings:
    llm:
      enabled: true
      provider: openai
      apiKey: your-api-key
      model: gpt-4
```

## Usage Examples

### Action Commands

```
/llm restart the go test pipeline
/llm run all tests
/llm cancel everything
/llm test the python pipeline
```

### Informational Queries

```
/llm what is the push to production pipeline
/llm which pipeline handles deployment
/llm show me all available pipelines
/llm what does the security scan pipeline do
```

## How It Works

1. **Comment Detection**: The system detects `/llm` comments using regex patterns
2. **LLM Analysis**: The comment is sent to the configured LLM provider for analysis
3. **Pipeline Matching**: The LLM response is used to match specific pipelines based on names, descriptions, and tasks
4. **Action Execution**: For action commands, the matched pipelines are executed
5. **Response Generation**: For queries, a detailed response is posted as a comment

## Pipeline Matching

The LLM uses the following information to match pipelines:

- Pipeline names
- Pipeline descriptions (from annotations)
- Task names within pipelines
- Event types and branch information

## Error Handling

- **LLM Unavailable**: Falls back to standard comment processing
- **Low Confidence**: Logs warning and uses default behavior
- **No Matches**: Returns appropriate error message
- **Provider Errors**: Graceful degradation with logging

## Security Considerations

- API keys are stored securely in Kubernetes secrets
- LLM responses are validated before execution
- Confidence thresholds prevent unintended actions
- All actions are logged for audit purposes

## Testing

Run the test suite:

```bash
go test ./pkg/llm/...
```

## Contributing

To add support for new LLM providers:

1. Implement the provider interface in `client.go`
2. Add configuration options
3. Update tests
4. Update documentation

# LLM Integration for Pipelines as Code

This document describes the Large Language Model (LLM) integration feature in Pipelines as Code (PAC), which enables natural language commands in repository comments to trigger pipeline operations and answer informational queries.

## Overview

The LLM integration allows users to interact with CI/CD pipelines using natural language commands in comments. Users can:

- **Execute Actions**: Use commands like `/llm restart the test` or `/llm cancel all pipelines`
- **Ask Questions**: Query pipeline information like `/llm what is the push to production pipeline?`

## Supported LLM Providers

The integration supports multiple LLM providers:

- **OpenAI**: Uses GPT models (gpt-3.5-turbo, gpt-4, etc.)
- **Anthropic**: Uses Claude models (claude-3-sonnet, etc.)
- **Google Gemini**: Uses Gemini models (gemini-pro, etc.)

## Configuration

### ConfigMap Configuration

LLM settings can be configured through the global `pipelines-as-code` ConfigMap in the `pipelines-as-code` namespace:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: pipelines-as-code
  namespace: pipelines-as-code
data:
  # Enable or disable LLM integration
  llm-enabled: "false"
  
  # LLM provider to use (openai, anthropic, gemini)
  llm-provider: "openai"
  
  # LLM model to use
  llm-model: "gpt-3.5-turbo"
  
  # Maximum number of tokens for LLM responses
  llm-max-tokens: "1000"
  
  # Temperature for LLM responses (0.0 = deterministic, 1.0 = creative)
  llm-temperature: "0.1"
  
  # Timeout in seconds for LLM API calls
  llm-timeout-seconds: "30"
```

#### Kubectl

```shell
kubectl patch configmap -n pipelines-as-code --type merge -p '{"data":{"llm-enabled": "true", "llm-provider": "gemini"}}' pipelines-as-code
```

### Environment Variables

API keys for LLM providers must be set as environment variables:

- **OpenAI**: `OPENAI_API_KEY`
- **Anthropic**: `ANTHROPIC_API_KEY`
- **Google Gemini**: `GEMINI_API_KEY`

### Command line example

```zsh
for i (watcher controller) oc set env -n pipelines-as-code deployment/pipelines-as-code-${i} GEMINI_API_KEY="$(pass show redhat/gemini-api-credentials)"
```

### Provider-Specific Configuration

#### OpenAI

- **Endpoint**: `https://api.openai.com/v1/chat/completions`
- **Models**: gpt-3.5-turbo, gpt-4, gpt-4-turbo, etc.
- **Authentication**: Bearer token via `OPENAI_API_KEY`

#### Anthropic

- **Endpoint**: `https://api.anthropic.com/v1/messages`
- **Models**: claude-3-sonnet, claude-3-haiku, claude-3-opus, etc.
- **Authentication**: API key via `ANTHROPIC_API_KEY`

#### Google Gemini

- **Endpoint**: `https://generativelanguage.googleapis.com/v1beta/models/{model}:generateContent`
- **Models**: gemini-pro, gemini-pro-vision, etc.
- **Authentication**: API key via `GEMINI_API_KEY`

## Usage Examples

### Action Commands

Users can execute pipeline operations using natural language:

```console
/llm restart the failing test
/llm cancel all running pipelines
/llm run the deployment pipeline
/llm retest the integration tests
```

### Informational Queries

Users can ask questions about available pipelines:

```console
/llm what pipelines are available?
/llm show me the test results
/llm what is the status of the build?
/llm which pipeline handles deployment?
/llm what is the push to production pipeline?
```

### Pull Request Analysis

Users can analyze pull requests for issues and security vulnerabilities:

```console
/llm analyze this pull request for security issues
/llm review this pull request for any problems
/llm check this PR for security vulnerabilities
/llm are there any issues with this pull request?
/llm code review this pull request
/llm analyze this PR for security bugs
```

The PR analysis feature will:

- Analyze the pull request title, description, and commit messages
- Review changed files and their modifications
- Identify potential security vulnerabilities
- Highlight code quality issues
- Provide recommendations for improvement
- Return a detailed analysis with confidence score

**Example Response:**

```
🤖 **AI Assistant Response**

## 🔍 Pull Request Analysis

This pull request introduces a new authentication system with several areas that need attention...

## ⚠️ Issues Found

1. Missing input validation on user credentials
2. Hardcoded database connection string
3. No error handling for failed authentication attempts

## 🔒 Security Concerns

1. Potential SQL injection vulnerability in user query
2. Sensitive data logged in plain text
3. Weak password hashing algorithm used

## 💡 Recommendations

1. Implement proper input sanitization
2. Use environment variables for configuration
3. Add comprehensive error handling
4. Upgrade to bcrypt for password hashing
5. Add security headers to responses

**Confidence Score:** 85.0%
```

## How It Works

1. **Comment Detection**: The system detects `/llm` commands in repository comments
2. **LLM Analysis**: The comment is sent to the configured LLM provider for analysis
3. **Action Determination**: The LLM determines the intended action (test, retest, cancel, query)
4. **Pipeline Matching**: For actions, the system matches the request to available pipelines
5. **Execution**: The matched pipelines are executed or information is returned

### Comment Processing

The system processes comments in the following format:

- **Action Commands**: `/llm <action> <target>`
- **Queries**: `/llm <question>`

Examples:

- `/llm restart the go test pipeline` → Retests the "go-test" pipeline
- `/llm run all tests` → Runs all available test pipelines
- `/llm cancel everything` → Cancels all running pipelines
- `/llm what is the deployment pipeline?` → Returns information about deployment pipelines

## Security Considerations

- **API Key Management**: API keys are stored as environment variables and should be properly secured
- **Access Control**: LLM commands respect existing repository access controls
- **Rate Limiting**: The system includes timeout and rate limiting to prevent abuse
- **Error Handling**: Failed LLM requests are gracefully handled without affecting pipeline execution

## Troubleshooting

### Common Issues

1. **LLM Not Responding**
   - Check if LLM is enabled in the ConfigMap
   - Verify API key is set correctly
   - Check network connectivity to LLM provider

2. **Incorrect Pipeline Matching**
   - Ensure pipeline names are clear and descriptive
   - Check that pipelines have proper annotations
   - Review LLM model configuration

3. **Timeout Errors**
   - Increase `llm-timeout-seconds` in ConfigMap
   - Check network latency to LLM provider
   - Consider using a different LLM model

### Logs and Debugging

LLM operations are logged with appropriate levels:

- **Info**: Successful LLM operations
- **Warn**: Configuration issues or missing API keys
- **Error**: Failed LLM requests or processing errors

## Best Practices

1. **Pipeline Naming**: Use clear, descriptive names for pipelines to improve LLM matching
2. **Model Selection**: Choose appropriate models based on your use case and budget
3. **Temperature Settings**: Use lower temperature (0.1-0.3) for more deterministic responses
4. **Token Limits**: Set appropriate max tokens based on your pipeline complexity
5. **Testing**: Test LLM commands in a development environment before production use

## Future Enhancements

- Support for additional LLM providers
- Enhanced pipeline matching algorithms
- Custom prompt templates
- Multi-language support
- Integration with pipeline templates

package llm

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/llm/ltypes"
	"go.uber.org/zap"
)

// CircuitBreakerState represents the state of a circuit breaker.
type CircuitBreakerState int

const (
	StateClosed CircuitBreakerState = iota
	StateOpen
	StateHalfOpen
)

// CircuitBreaker implements a circuit breaker pattern for LLM requests.
type CircuitBreaker struct {
	mu                sync.RWMutex
	state             CircuitBreakerState
	failureCount      int
	lastFailureTime   time.Time
	failureThreshold  int
	timeout           time.Duration
	halfOpenMaxCalls  int
	halfOpenCallCount int
	halfOpenSuccesses int
	halfOpenThreshold int
	logger            *zap.SugaredLogger
}

// NewCircuitBreaker creates a new circuit breaker with the given configuration.
func NewCircuitBreaker(failureThreshold int, timeout time.Duration, logger *zap.SugaredLogger) *CircuitBreaker {
	return &CircuitBreaker{
		state:             StateClosed,
		failureThreshold:  failureThreshold,
		timeout:           timeout,
		halfOpenMaxCalls:  5,
		halfOpenThreshold: 3,
		logger:            logger,
	}
}

// Execute executes a function with circuit breaker protection.
func (cb *CircuitBreaker) Execute(_ context.Context, fn func() (*ltypes.AnalysisResponse, error)) (*ltypes.AnalysisResponse, error) {
	if !cb.canExecute() {
		return nil, &ltypes.AnalysisError{
			Type:      "circuit_breaker_open",
			Message:   "circuit breaker is open",
			Retryable: true,
		}
	}

	response, err := fn()
	cb.recordResult(err)

	return response, err
}

// canExecute checks if a request can be executed based on the circuit breaker state.
func (cb *CircuitBreaker) canExecute() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateClosed:
		return true
	case StateOpen:
		if time.Since(cb.lastFailureTime) > cb.timeout {
			cb.state = StateHalfOpen
			cb.halfOpenCallCount = 0
			cb.halfOpenSuccesses = 0
			cb.logger.Info("Circuit breaker transitioning to half-open state")
			return true
		}
		return false
	case StateHalfOpen:
		return cb.halfOpenCallCount < cb.halfOpenMaxCalls
	default:
		return false
	}
}

// recordResult records the result of an operation and updates the circuit breaker state.
func (cb *CircuitBreaker) recordResult(err error) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if err != nil {
		cb.onFailure()
	} else {
		cb.onSuccess()
	}
}

// onFailure handles a failed operation.
func (cb *CircuitBreaker) onFailure() {
	cb.lastFailureTime = time.Now()

	switch cb.state {
	case StateClosed:
		cb.failureCount++
		if cb.failureCount >= cb.failureThreshold {
			cb.state = StateOpen
			cb.logger.With(
				"failure_count", cb.failureCount,
				"threshold", cb.failureThreshold,
			).Warn("Circuit breaker opened due to repeated failures")
		}
	case StateHalfOpen:
		cb.state = StateOpen
		cb.logger.Info("Circuit breaker returned to open state after failure in half-open")
	case StateOpen:
		// Already open, no action needed
	}
}

// onSuccess handles a successful operation.
func (cb *CircuitBreaker) onSuccess() {
	switch cb.state {
	case StateClosed:
		cb.failureCount = 0
	case StateHalfOpen:
		cb.halfOpenCallCount++
		cb.halfOpenSuccesses++

		if cb.halfOpenSuccesses >= cb.halfOpenThreshold {
			cb.state = StateClosed
			cb.failureCount = 0
			cb.logger.Info("Circuit breaker returned to closed state")
		}
	case StateOpen:
		// Success in open state shouldn't happen, but no action needed
	}
}

// GetState returns the current state of the circuit breaker.
func (cb *CircuitBreaker) GetState() CircuitBreakerState {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// GetFailureCount returns the current failure count.
func (cb *CircuitBreaker) GetFailureCount() int {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.failureCount
}

// RetryConfig defines configuration for retry logic.
type RetryConfig struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
	Multiplier  float64
}

// DefaultRetryConfig returns a default retry configuration.
func DefaultRetryConfig() *RetryConfig {
	return &RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Second,
		MaxDelay:    10 * time.Second,
		Multiplier:  2.0,
	}
}

// ResilientClient wraps an LLM client with retry and circuit breaker functionality.
type ResilientClient struct {
	client         ltypes.Client
	circuitBreaker *CircuitBreaker
	retryConfig    *RetryConfig
	logger         *zap.SugaredLogger
}

// NewResilientClient creates a new resilient LLM client.
func NewResilientClient(client ltypes.Client, logger *zap.SugaredLogger) *ResilientClient {
	return &ResilientClient{
		client:         client,
		circuitBreaker: NewCircuitBreaker(5, 30*time.Second, logger), // 5 failures, 30s timeout
		retryConfig:    DefaultRetryConfig(),
		logger:         logger,
	}
}

// NewResilientClientWithConfig creates a new resilient LLM client with custom configuration.
func NewResilientClientWithConfig(client ltypes.Client, retryConfig *RetryConfig, cbFailureThreshold int, cbTimeout time.Duration, logger *zap.SugaredLogger) *ResilientClient {
	return &ResilientClient{
		client:         client,
		circuitBreaker: NewCircuitBreaker(cbFailureThreshold, cbTimeout, logger),
		retryConfig:    retryConfig,
		logger:         logger,
	}
}

// Analyze performs LLM analysis with retry and circuit breaker protection.
func (rc *ResilientClient) Analyze(ctx context.Context, request *ltypes.AnalysisRequest) (*ltypes.AnalysisResponse, error) {
	var lastErr error

	for attempt := 1; attempt <= rc.retryConfig.MaxAttempts; attempt++ {
		attemptLogger := rc.logger.With(
			"attempt", attempt,
			"max_attempts", rc.retryConfig.MaxAttempts,
			"provider", rc.client.GetProviderName(),
		)

		response, err := rc.circuitBreaker.Execute(ctx, func() (*ltypes.AnalysisResponse, error) {
			return rc.client.Analyze(ctx, request)
		})

		if err == nil {
			if attempt > 1 {
				attemptLogger.Info("LLM analysis succeeded after retry")
			}
			return response, nil
		}

		lastErr = err

		// Check if error is retryable
		var analysisErr *ltypes.AnalysisError
		if errors.As(err, &analysisErr) && !analysisErr.Retryable {
			attemptLogger.With("error", err).Debug("Non-retryable error, not attempting retry")
			break
		}

		// Check if we should retry
		if attempt < rc.retryConfig.MaxAttempts {
			delay := rc.calculateDelay(attempt)
			attemptLogger.With(
				"error", err,
				"retry_delay", delay,
			).Warn("LLM analysis failed, retrying")

			select {
			case <-time.After(delay):
				// Continue to next attempt
			case <-ctx.Done():
				return nil, fmt.Errorf("context cancelled during retry: %w", ctx.Err())
			}
		} else {
			attemptLogger.With("error", err).Error("LLM analysis failed after all retry attempts")
		}
	}

	return nil, fmt.Errorf("LLM analysis failed after %d attempts: %w", rc.retryConfig.MaxAttempts, lastErr)
}

// calculateDelay calculates the delay for exponential backoff.
func (rc *ResilientClient) calculateDelay(attempt int) time.Duration {
	delay := time.Duration(float64(rc.retryConfig.BaseDelay) * pow(rc.retryConfig.Multiplier, float64(attempt-1)))
	if delay > rc.retryConfig.MaxDelay {
		delay = rc.retryConfig.MaxDelay
	}
	return delay
}

// pow calculates base^exp for float64 values (simple implementation).
func pow(base, exp float64) float64 {
	result := 1.0
	for i := 0; i < int(exp); i++ {
		result *= base
	}
	return result
}

// GetProviderName returns the underlying client's provider name.
func (rc *ResilientClient) GetProviderName() string {
	return rc.client.GetProviderName()
}

// ValidateConfig validates the underlying client's configuration.
func (rc *ResilientClient) ValidateConfig() error {
	return rc.client.ValidateConfig()
}

// GetCircuitBreakerState returns the current state of the circuit breaker.
func (rc *ResilientClient) GetCircuitBreakerState() CircuitBreakerState {
	return rc.circuitBreaker.GetState()
}

// GetCircuitBreakerFailureCount returns the current failure count of the circuit breaker.
func (rc *ResilientClient) GetCircuitBreakerFailureCount() int {
	return rc.circuitBreaker.GetFailureCount()
}

// ResetCircuitBreaker manually resets the circuit breaker to closed state.
func (rc *ResilientClient) ResetCircuitBreaker() {
	rc.circuitBreaker.mu.Lock()
	defer rc.circuitBreaker.mu.Unlock()

	rc.circuitBreaker.state = StateClosed
	rc.circuitBreaker.failureCount = 0
	rc.circuitBreaker.halfOpenCallCount = 0
	rc.circuitBreaker.halfOpenSuccesses = 0

	rc.logger.Info("Circuit breaker manually reset to closed state")
}

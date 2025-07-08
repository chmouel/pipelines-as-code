package concurrency

import (
	"context"
	"fmt"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
)

// RepoKey generates a unique key for a repository.
func RepoKey(repo *v1alpha1.Repository) string {
	return fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)
}

// PrKey generates a unique key for a PipelineRun.
func PrKey(run *tektonv1.PipelineRun) string {
	return fmt.Sprintf("%s/%s", run.Namespace, run.Name)
}

// ParseRepositoryKey parses a repository key into namespace and name.
func ParseRepositoryKey(repoKey string) (namespace, name string, err error) {
	parts := splitKey(repoKey)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid repository key format: %s", repoKey)
	}
	return parts[0], parts[1], nil
}

// splitKey splits a key by "/" separator.
func splitKey(key string) []string {
	result := make([]string, 0, 2)
	start := 0
	for i, char := range key {
		if char == '/' {
			if i > start {
				result = append(result, key[start:i])
			}
			start = i + 1
		}
	}
	if start < len(key) {
		result = append(result, key[start:])
	}
	return result
}

// WatcherConfig holds configuration for the adaptive polling watcher.
type WatcherConfig struct {
	InitialInterval   time.Duration
	MaxInterval       time.Duration
	BackoffMultiplier float64
	BackoffThreshold  int
	DriverName        string
}

// DefaultMemoryWatcherConfig returns default configuration for memory driver watcher.
func DefaultMemoryWatcherConfig() WatcherConfig {
	return WatcherConfig{
		InitialInterval:   1 * time.Second,
		MaxInterval:       15 * time.Second,
		BackoffMultiplier: 1.3,
		BackoffThreshold:  5,
		DriverName:        "memory",
	}
}

// DefaultPostgreSQLWatcherConfig returns default configuration for PostgreSQL driver watcher.
func DefaultPostgreSQLWatcherConfig() WatcherConfig {
	return WatcherConfig{
		InitialInterval:   2 * time.Second,
		MaxInterval:       30 * time.Second,
		BackoffMultiplier: 1.5,
		BackoffThreshold:  3,
		DriverName:        "postgresql",
	}
}

// SlotCounter defines the interface for getting current slot count.
type SlotCounter interface {
	GetCurrentSlots(ctx context.Context, repo *v1alpha1.Repository) (int, error)
}

// AdaptiveWatcher implements an adaptive polling watcher that adjusts polling intervals
// based on activity. It uses exponential backoff when no changes are detected.
func AdaptiveWatcher(ctx context.Context, repo *v1alpha1.Repository, callback func(),
	counter SlotCounter, config WatcherConfig, logger *zap.SugaredLogger,
) {
	repoKey := RepoKey(repo)

	go func() {
		currentInterval := config.InitialInterval
		ticker := time.NewTicker(currentInterval)
		defer ticker.Stop()

		lastCount := -1
		consecutiveNoChanges := 0

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				count, err := counter.GetCurrentSlots(ctx, repo)
				if err != nil {
					logger.Errorf("failed to get current slots for watching: %v", err)
					continue
				}

				switch {
				case lastCount != -1 && count < lastCount:
					// A slot was released - trigger callback and reset to fast polling
					logger.Debugf("slot released in repository %s (count: %d -> %d), triggering callback", repoKey, lastCount, count)
					callback()

					// Reset to fast polling after detecting change
					currentInterval = config.InitialInterval
					consecutiveNoChanges = 0
					ticker.Reset(currentInterval)

				case lastCount == count:
					// No change detected - gradually increase polling interval
					consecutiveNoChanges++
					if consecutiveNoChanges >= config.BackoffThreshold && currentInterval < config.MaxInterval {
						// Exponential backoff up to max interval
						currentInterval = time.Duration(float64(currentInterval) * config.BackoffMultiplier)
						currentInterval = min(currentInterval, config.MaxInterval)
						ticker.Reset(currentInterval)
						logger.Debugf("no changes detected for repository %s, increased polling interval to %v", repoKey, currentInterval)
					}

				default:
					// Count changed (but not decreased) - reset backoff
					consecutiveNoChanges = 0
				}

				lastCount = count
			}
		}
	}()
}

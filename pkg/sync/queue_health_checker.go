package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	apipac "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/generated/clientset/versioned"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	tektonVersionedClient "github.com/tektoncd/pipeline/pkg/client/clientset/versioned"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// QueueHealthChecker periodically scans all repositories for stuck pending PipelineRuns
// and automatically triggers reconciliation when repositories have available capacity.
type QueueHealthChecker struct {
	logger           *zap.SugaredLogger
	queueManager     QueueManagerInterface
	tektonClient     tektonVersionedClient.Interface
	pacClient        versioned.Interface
	stopCh           chan struct{}
	wg               sync.WaitGroup
	lastTriggerTimes map[string]time.Time // Track last trigger time per repository
	triggerMutex     sync.RWMutex         // Protect lastTriggerTimes map
	settings         *settings.Settings   // Configuration settings
	settingsMutex    sync.RWMutex         // Protect settings access
}

// NewQueueHealthChecker creates a new QueueHealthChecker instance.
func NewQueueHealthChecker(logger *zap.SugaredLogger) *QueueHealthChecker {
	defaultSettings := settings.DefaultSettings()
	return &QueueHealthChecker{
		logger:           logger,
		stopCh:           make(chan struct{}),
		lastTriggerTimes: make(map[string]time.Time),
		settings:         &defaultSettings,
	}
}

// SetClients sets the Tekton and PAC clients.
func (qhc *QueueHealthChecker) SetClients(tektonClient tektonVersionedClient.Interface, pacClient versioned.Interface) {
	qhc.tektonClient = tektonClient
	qhc.pacClient = pacClient
}

// SetQueueManager sets the queue manager instance.
func (qhc *QueueHealthChecker) SetQueueManager(queueManager QueueManagerInterface) {
	qhc.queueManager = queueManager
}

// UpdateSettings updates the health checker settings thread-safely.
func (qhc *QueueHealthChecker) UpdateSettings(newSettings *settings.Settings) {
	qhc.settingsMutex.Lock()
	defer qhc.settingsMutex.Unlock()
	qhc.settings = newSettings
}

// getSettings returns a copy of the current settings thread-safely.
func (qhc *QueueHealthChecker) getSettings() *settings.Settings {
	qhc.settingsMutex.RLock()
	defer qhc.settingsMutex.RUnlock()
	return qhc.settings
}

// isEnabled checks if the health checker is enabled based on configuration.
func (qhc *QueueHealthChecker) isEnabled() bool {
	settings := qhc.getSettings()
	return settings.QueueHealthCheckInterval > 0 && settings.QueueHealthStuckThreshold > 0
}

// getCheckInterval returns the configured check interval.
func (qhc *QueueHealthChecker) getCheckInterval() time.Duration {
	settings := qhc.getSettings()
	return time.Duration(settings.QueueHealthCheckInterval) * time.Second
}

// getStuckThreshold returns the configured stuck threshold.
func (qhc *QueueHealthChecker) getStuckThreshold() time.Duration {
	settings := qhc.getSettings()
	return time.Duration(settings.QueueHealthStuckThreshold) * time.Second
}

// Start begins the periodic health checking in a background goroutine.
func (qhc *QueueHealthChecker) Start(ctx context.Context) {
	if qhc.queueManager == nil || qhc.tektonClient == nil || qhc.pacClient == nil {
		qhc.logger.Error("HEALTH-CHECKER: not properly initialized - missing dependencies")
		return
	}

	if !qhc.isEnabled() {
		qhc.logger.Info("HEALTH-CHECKER: disabled via configuration (queue-health-check-interval=0 or queue-health-stuck-threshold=0)")
		return
	}

	checkInterval := qhc.getCheckInterval()
	qhc.logger.Infof("HEALTH-CHECKER: Starting queue health checker with %v interval", checkInterval)

	qhc.wg.Add(1)
	go func() {
		defer qhc.wg.Done()
		ticker := time.NewTicker(checkInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				qhc.logger.Info("HEALTH-CHECKER: stopped due to context cancellation")
				return
			case <-qhc.stopCh:
				qhc.logger.Info("HEALTH-CHECKER: stopped")
				return
			case <-ticker.C:
				// Check if still enabled (settings might have changed)
				if !qhc.isEnabled() {
					qhc.logger.Info("HEALTH-CHECKER: disabled via configuration update, stopping")
					return
				}

				// Update ticker interval if it changed
				newInterval := qhc.getCheckInterval()
				if newInterval != checkInterval {
					qhc.logger.Infof("HEALTH-CHECKER: Updating check interval from %v to %v", checkInterval, newInterval)
					ticker.Reset(newInterval)
					checkInterval = newInterval
				}

				qhc.checkAllRepositories(ctx)
			}
		}
	}()
}

// Stop gracefully stops the health checker.
func (qhc *QueueHealthChecker) Stop() {
	qhc.logger.Info("HEALTH-CHECKER: Stopping queue health checker...")
	close(qhc.stopCh)
	qhc.wg.Wait()
	qhc.logger.Info("HEALTH-CHECKER: stopped")
}

// checkAllRepositories scans all repositories for stuck pending PipelineRuns.
func (qhc *QueueHealthChecker) checkAllRepositories(ctx context.Context) {
	repos, err := qhc.pacClient.PipelinesascodeV1alpha1().Repositories("").List(ctx, metav1.ListOptions{})
	if err != nil {
		qhc.logger.Errorf("HEALTH-CHECKER: Failed to list repositories: %v", err)
		return
	}

	totalChecked := 0
	totalReposWithStuck := 0
	totalTriggered := 0

	for _, repo := range repos.Items {
		if repo.Spec.ConcurrencyLimit != nil && *repo.Spec.ConcurrencyLimit > 0 {
			totalChecked++
			triggered, err := qhc.checkRepositoryHealth(ctx, &repo)
			if err != nil {
				qhc.logger.Errorf("HEALTH-CHECKER: Failed to check repository %s/%s: %v", repo.Namespace, repo.Name, err)
				continue
			}
			if triggered > 0 {
				totalReposWithStuck++
				totalTriggered += triggered
			}
		}
	}

	if totalTriggered > 0 {
		qhc.logger.Infof("HEALTH-CHECKER: Queue health check completed: checked %d repositories, found stuck PipelineRuns in %d repositories, triggered %d PipelineRuns",
			totalChecked, totalReposWithStuck, totalTriggered)
	} else {
		qhc.logger.Debugf("HEALTH-CHECKER: Queue health check completed: checked %d repositories, no stuck PipelineRuns found", totalChecked)
	}
}

// checkRepositoryHealth checks a single repository for stuck pending PipelineRuns
// and rebuilds the queue if necessary, then triggers reconciliation.
func (qhc *QueueHealthChecker) checkRepositoryHealth(ctx context.Context, repo *apipac.Repository) (int, error) {
	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)

	// Check if repository has a concurrency limit
	concurrencyLimit := 0
	if repo.Spec.ConcurrencyLimit != nil {
		concurrencyLimit = *repo.Spec.ConcurrencyLimit
	}

	// If no concurrency limit is set, we can't determine capacity
	if concurrencyLimit == 0 {
		qhc.logger.Debugf("HEALTH-CHECKER: Repository %s/%s has no concurrency limit set, skipping health check", repo.Namespace, repo.Name)
		return 0, nil
	}

	// Query for actually running PipelineRuns in the cluster
	actualRunningPRs, err := qhc.tektonClient.TektonV1().PipelineRuns(repo.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s,%s=%s", keys.Repository, repo.Name, keys.State, kubeinteraction.StateStarted),
	})
	if err != nil {
		return 0, fmt.Errorf("failed to list running PipelineRuns for repository %s/%s: %w", repo.Namespace, repo.Name, err)
	}

	// Calculate available capacity based on actual running PipelineRuns
	actualRunningCount := len(actualRunningPRs.Items)
	availableCapacity := concurrencyLimit - actualRunningCount

	// Query for pending PipelineRuns for this repository
	pendingPRs, err := qhc.tektonClient.TektonV1().PipelineRuns(repo.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s,%s=%s", keys.Repository, repo.Name, keys.State, kubeinteraction.StateQueued),
	})
	if err != nil {
		return 0, fmt.Errorf("failed to list pending PipelineRuns for repository %s/%s: %w", repo.Namespace, repo.Name, err)
	}

	// Enhanced logging for debugging
	qhc.logger.Infof("HEALTH-CHECKER: Repository %s/%s status: concurrency_limit=%d, running=%d, available_capacity=%d, pending=%d",
		repo.Namespace, repo.Name, concurrencyLimit, actualRunningCount, availableCapacity, len(pendingPRs.Items))

	// Log details about running PipelineRuns
	if actualRunningCount > 0 {
		runningNames := make([]string, len(actualRunningPRs.Items))
		for i, pr := range actualRunningPRs.Items {
			runningNames[i] = pr.Name
		}
		qhc.logger.Infof("HEALTH-CHECKER: Repository %s/%s running PipelineRuns: %v", repo.Namespace, repo.Name, runningNames)
	}

	// Log details about pending PipelineRuns
	if len(pendingPRs.Items) > 0 {
		pendingNames := make([]string, len(pendingPRs.Items))
		pendingAges := make([]string, len(pendingPRs.Items))
		for i, pr := range pendingPRs.Items {
			pendingNames[i] = pr.Name
			age := time.Since(pr.CreationTimestamp.Time)
			pendingAges[i] = fmt.Sprintf("%s(age:%v,pending:%v)", pr.Name, age.Round(time.Second), pr.Spec.Status == tektonv1.PipelineRunSpecStatusPending)
		}
		qhc.logger.Infof("HEALTH-CHECKER: Repository %s/%s pending PipelineRuns: %v", repo.Namespace, repo.Name, pendingAges)
	}

	if availableCapacity <= 0 {
		qhc.logger.Infof("HEALTH-CHECKER: Repository %s/%s at capacity (%d/%d), no health check needed",
			repo.Namespace, repo.Name, actualRunningCount, concurrencyLimit)
		return 0, nil
	}

	if len(pendingPRs.Items) == 0 {
		qhc.logger.Debugf("HEALTH-CHECKER: Repository %s/%s has no pending PipelineRuns, no health check needed", repo.Namespace, repo.Name)
		return 0, nil
	}

	// Check for high activity (many recently started PipelineRuns)
	recentlyStartedPRs, err := qhc.tektonClient.TektonV1().PipelineRuns(repo.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s,%s=%s", keys.Repository, repo.Name, keys.State, kubeinteraction.StateStarted),
	})
	if err != nil {
		return 0, fmt.Errorf("failed to list recently started PipelineRuns for repository %s/%s: %w", repo.Namespace, repo.Name, err)
	}

	// Count recently started PipelineRuns (started within the last 2 minutes)
	recentlyStartedCount := 0
	for _, pr := range recentlyStartedPRs.Items {
		if time.Since(pr.CreationTimestamp.Time) < 2*time.Minute {
			recentlyStartedCount++
		}
	}

	// If there are many recently started PipelineRuns, the reconciler is likely active
	if recentlyStartedCount > 5 {
		qhc.logger.Infof("HEALTH-CHECKER: Repository %s/%s has high activity (%d recently started PipelineRuns), skipping trigger to avoid interference",
			repo.Namespace, repo.Name, recentlyStartedCount)
		return 0, nil
	}

	// Check if we need to rebuild the queue (if any PipelineRuns are stuck)
	stuckThreshold := qhc.getStuckThreshold()
	hasStuckPRs := false
	for _, pr := range pendingPRs.Items {
		if pr.Spec.Status == tektonv1.PipelineRunSpecStatusPending {
			age := time.Since(pr.CreationTimestamp.Time)
			if age > stuckThreshold {
				hasStuckPRs = true
				break
			}
		}
	}

	needsRebuild := false
	if hasStuckPRs {
		// Check rate limiting for rebuilds (3 minutes)
		qhc.triggerMutex.RLock()
		lastTrigger, exists := qhc.lastTriggerTimes[repoKey]
		qhc.triggerMutex.RUnlock()

		if !exists || time.Since(lastTrigger) > 3*time.Minute {
			needsRebuild = true
			qhc.triggerMutex.Lock()
			qhc.lastTriggerTimes[repoKey] = time.Now()
			qhc.triggerMutex.Unlock()
		}
	}

	switch {
	case needsRebuild:
		qhc.logger.Infof("HEALTH-CHECKER: Repository %s/%s has %d stuck PipelineRuns with %d available capacity - rebuilding queue",
			repo.Namespace, repo.Name, len(pendingPRs.Items), availableCapacity)

		// Rebuild the queue to fix any state inconsistencies
		_, err := qhc.queueManager.RebuildQueuesForNamespace(ctx, repo.Namespace, qhc.tektonClient, qhc.pacClient)
		if err != nil {
			return 0, fmt.Errorf("failed to rebuild queue for repository %s/%s: %w", repo.Namespace, repo.Name, err)
		}
		qhc.logger.Infof("HEALTH-CHECKER: Successfully rebuilt queue for repository %s/%s", repo.Namespace, repo.Name)
	default:
		qhc.logger.Infof("HEALTH-CHECKER: Repository %s/%s has %d pending PipelineRuns with %d available capacity - triggering processing (no rebuild needed)",
			repo.Namespace, repo.Name, len(pendingPRs.Items), availableCapacity)
	}

	// Trigger reconciliation for one pending PipelineRun to start queue processing
	if err := qhc.triggerReconciliationForPendingPipelineRuns(ctx, repo.Namespace, repo.Name); err != nil {
		return 0, fmt.Errorf("failed to trigger reconciliation for repository %s/%s: %w", repo.Namespace, repo.Name, err)
	}

	return len(pendingPRs.Items), nil
}

// triggerReconciliationForPendingPipelineRuns updates the state annotation on one pending PipelineRun
// for the specific repository to trigger reconciliation by the controller. The reconciler will then
// process the queue and move items from pending to running based on concurrency limits.
func (qhc *QueueHealthChecker) triggerReconciliationForPendingPipelineRuns(ctx context.Context, namespace, repoName string) error {
	// Get all PipelineRuns in "queued" state with pending status for this specific repository
	pipelineRuns, err := qhc.tektonClient.TektonV1().PipelineRuns(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s,%s=%s", keys.Repository, repoName, keys.State, kubeinteraction.StateQueued),
	})
	if err != nil {
		return fmt.Errorf("failed to list pending PipelineRuns for repository %s/%s: %w", namespace, repoName, err)
	}

	// Filter to only pending PipelineRuns
	var pendingPRs []tektonv1.PipelineRun
	for _, pr := range pipelineRuns.Items {
		if pr.Spec.Status == tektonv1.PipelineRunSpecStatusPending {
			pendingPRs = append(pendingPRs, pr)
		}
	}

	if len(pendingPRs) == 0 {
		qhc.logger.Debugf("HEALTH-CHECKER: No pending PipelineRuns found for repository %s/%s", namespace, repoName)
		return nil
	}

	// Trigger the first pending PipelineRun by updating its state annotation
	// This will cause the controller to re-examine it and process the queue
	pr := &pendingPRs[0]

	// Retry logic for handling conflicts
	maxRetries := 3
	for attempt := 1; attempt <= maxRetries; attempt++ {
		// Update the state annotation to trigger reconciliation
		mergePatch := map[string]any{
			"metadata": map[string]any{
				"annotations": map[string]string{
					keys.State: kubeinteraction.StateQueued, // Re-set the same state to trigger controller
				},
			},
		}

		patchBytes, err := json.Marshal(mergePatch)
		if err != nil {
			return fmt.Errorf("failed to marshal patch for PipelineRun %s: %w", pr.Name, err)
		}

		_, err = qhc.tektonClient.TektonV1().PipelineRuns(namespace).Patch(ctx, pr.Name, types.MergePatchType, patchBytes, metav1.PatchOptions{})
		if err != nil {
			if attempt < maxRetries && (strings.Contains(err.Error(), "the object has been modified") || strings.Contains(err.Error(), "Operation cannot be fulfilled")) {
				qhc.logger.Debugf("HEALTH-CHECKER: Conflict updating PipelineRun %s (attempt %d/%d): %v", pr.Name, attempt, maxRetries, err)
				// Exponential backoff
				time.Sleep(time.Duration(attempt) * 100 * time.Millisecond)
				continue
			}
			if strings.Contains(err.Error(), "not found") {
				qhc.logger.Debugf("HEALTH-CHECKER: PipelineRun %s not found, may have been deleted", pr.Name)
				return nil
			}
			return fmt.Errorf("failed to patch PipelineRun %s after %d attempts: %w", pr.Name, attempt, err)
		}

		qhc.logger.Infof("HEALTH-CHECKER: Triggered reconciliation for PipelineRun %s (repository %s) - this will kick off queue processing", pr.Name, repoName)
		return nil
	}

	return fmt.Errorf("failed to patch PipelineRun %s after %d attempts due to conflicts", pr.Name, maxRetries)
}

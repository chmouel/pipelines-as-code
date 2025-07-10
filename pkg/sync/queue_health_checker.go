package sync

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	apipac "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/generated/clientset/versioned"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	tektonVersionedClient "github.com/tektoncd/pipeline/pkg/client/clientset/versioned"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// QueueHealthCheckInterval is how often we check for stuck pending PipelineRuns
	QueueHealthCheckInterval = 1 * time.Minute

	// StuckPipelineRunThreshold is how long a PipelineRun can be pending before considered stuck
	StuckPipelineRunThreshold = 2 * time.Minute
)

// QueueHealthChecker periodically scans all repositories for stuck pending PipelineRuns
// and automatically triggers reconciliation when repositories have available capacity
type QueueHealthChecker struct {
	logger           *zap.SugaredLogger
	queueManager     QueueManagerInterface
	tektonClient     tektonVersionedClient.Interface
	pacClient        versioned.Interface
	stopCh           chan struct{}
	wg               sync.WaitGroup
	lastTriggerTimes map[string]time.Time // Track last trigger time per repository
	triggerMutex     sync.RWMutex         // Protect lastTriggerTimes map
}

// NewQueueHealthChecker creates a new QueueHealthChecker instance
func NewQueueHealthChecker(logger *zap.SugaredLogger) *QueueHealthChecker {
	return &QueueHealthChecker{
		logger:           logger,
		stopCh:           make(chan struct{}),
		lastTriggerTimes: make(map[string]time.Time),
	}
}

// SetClients sets the Tekton and PAC clients
func (qhc *QueueHealthChecker) SetClients(tektonClient tektonVersionedClient.Interface, pacClient versioned.Interface) {
	qhc.tektonClient = tektonClient
	qhc.pacClient = pacClient
}

// SetQueueManager sets the queue manager instance
func (qhc *QueueHealthChecker) SetQueueManager(queueManager QueueManagerInterface) {
	qhc.queueManager = queueManager
}

// Start begins the periodic health checking in a background goroutine
func (qhc *QueueHealthChecker) Start(ctx context.Context) {
	if qhc.queueManager == nil || qhc.tektonClient == nil || qhc.pacClient == nil {
		qhc.logger.Error("HEALTH-CHECKER: not properly initialized - missing dependencies")
		return
	}

	qhc.logger.Infof("HEALTH-CHECKER: Starting queue health checker with %v interval", QueueHealthCheckInterval)

	qhc.wg.Add(1)
	go func() {
		defer qhc.wg.Done()
		ticker := time.NewTicker(QueueHealthCheckInterval)
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
				qhc.checkAllRepositories(ctx)
			}
		}
	}()
}

// Stop gracefully stops the health checker
func (qhc *QueueHealthChecker) Stop() {
	qhc.logger.Info("HEALTH-CHECKER: Stopping queue health checker...")
	close(qhc.stopCh)
	qhc.wg.Wait()
	qhc.logger.Info("HEALTH-CHECKER: stopped")
}

// checkAllRepositories scans all repositories for stuck pending PipelineRuns
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
// and rebuilds the queue if necessary, then triggers reconciliation
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

	// If there are no pending PipelineRuns, no need to check further
	if len(pendingPRs.Items) == 0 {
		qhc.logger.Infof("HEALTH-CHECKER: Repository %s/%s: %d running, 0 pending - no action needed",
			repo.Namespace, repo.Name, actualRunningCount)
		return 0, nil
	}

	// Filter for actually stuck PipelineRuns
	stuckPRs := qhc.findStuckPipelineRuns(pendingPRs.Items)

	// Check if we have capacity and pending PipelineRuns (stuck or not)
	// If we have capacity and pending PipelineRuns, we should trigger processing
	if availableCapacity > 0 && len(pendingPRs.Items) > 0 {
		// Check rate limiting only for rebuild operations - don't rate limit simple triggers
		qhc.triggerMutex.RLock()
		lastTrigger, exists := qhc.lastTriggerTimes[repoKey]
		qhc.triggerMutex.RUnlock()

		needsRebuild := len(stuckPRs) > 0
		minTriggerInterval := 3 * time.Minute // Minimum time between rebuilds for same repo

		if needsRebuild {
			// Only rate limit rebuilds, not simple triggers
			if exists && time.Since(lastTrigger) < minTriggerInterval {
				qhc.logger.Infof("HEALTH-CHECKER: Repository %s was recently rebuilt (%v ago), skipping rebuild but will try simple trigger",
					repoKey, time.Since(lastTrigger))
				needsRebuild = false
			}
		}

		if needsRebuild {
			qhc.logger.Infof("HEALTH-CHECKER: Repository %s/%s has %d stuck PipelineRuns with %d available capacity - rebuilding queue",
				repo.Namespace, repo.Name, len(stuckPRs), availableCapacity)

			// Update rate limiting tracker for rebuilds
			qhc.triggerMutex.Lock()
			qhc.lastTriggerTimes[repoKey] = time.Now()
			qhc.triggerMutex.Unlock()

			// Rebuild the queue for this repository's namespace
			// This will fix any queue state inconsistencies and allow natural processing
			stats, err := qhc.queueManager.RebuildQueuesForNamespace(ctx, repo.Namespace, qhc.tektonClient, qhc.pacClient)
			if err != nil {
				qhc.logger.Errorf("HEALTH-CHECKER: Failed to rebuild queue for repository %s/%s: %v", repo.Namespace, repo.Name, err)
				return 0, err
			}

			qhc.logger.Infof("HEALTH-CHECKER: Successfully rebuilt queue for repository %s/%s: %v", repo.Namespace, repo.Name, stats)
		} else {
			qhc.logger.Infof("HEALTH-CHECKER: Repository %s/%s has %d pending PipelineRuns with %d available capacity - triggering processing (no rebuild needed)",
				repo.Namespace, repo.Name, len(pendingPRs.Items), availableCapacity)
		}

		// Always trigger reconciliation when we have capacity and pending PipelineRuns
		err = qhc.triggerReconciliationForPendingPipelineRuns(ctx, repo.Namespace, repo.Name)
		if err != nil {
			qhc.logger.Warnf("HEALTH-CHECKER: Failed to trigger reconciliation for pending PipelineRuns: %v", err)
			// Don't fail the health check, just log the warning
		}

		// Return the number of PipelineRuns that should be processed
		return len(pendingPRs.Items), nil
	}

	// No action needed
	qhc.logger.Infof("HEALTH-CHECKER: Repository %s/%s: %d running, %d pending, %d stuck - no action needed (no capacity or no pending)",
		repo.Namespace, repo.Name, actualRunningCount, len(pendingPRs.Items), len(stuckPRs))
	return 0, nil
}

// findStuckPipelineRuns filters PipelineRuns that are actually stuck
func (qhc *QueueHealthChecker) findStuckPipelineRuns(pipelineRuns []tektonv1.PipelineRun) []tektonv1.PipelineRun {
	var stuckPRs []tektonv1.PipelineRun
	now := time.Now()

	for _, pr := range pipelineRuns {
		// Only consider PipelineRuns that are actually pending
		if pr.Spec.Status != tektonv1.PipelineRunSpecStatusPending {
			continue
		}

		// Check if it's been pending long enough to be considered stuck
		if pr.CreationTimestamp.Add(StuckPipelineRunThreshold).Before(now) {
			stuckPRs = append(stuckPRs, pr)
		}
	}

	return stuckPRs
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
		return fmt.Errorf("failed to list queued PipelineRuns for repository %s: %w", repoName, err)
	}

	// Check if there are any PipelineRuns currently transitioning (recently modified)
	// This helps avoid conflicts with ongoing reconciliation
	recentlyModified := 0
	now := time.Now()
	for _, pr := range pipelineRuns.Items {
		if pr.Status.StartTime != nil && pr.Status.StartTime.Time.After(now.Add(-30*time.Second)) {
			recentlyModified++
		}
	}

	// If many PipelineRuns were recently modified, the reconciler might already be active
	if recentlyModified > 3 {
		qhc.logger.Debugf("HEALTH-CHECKER: Skipping reconciliation trigger for repository %s: %d PipelineRuns recently modified, reconciler likely active", repoName, recentlyModified)
		return nil
	}

	// Find the first pending PipelineRun and trigger reconciliation for it only
	// The reconciler will then process the entire queue and move items from pending to running
	// based on the repository's concurrency limit
	for _, pr := range pipelineRuns.Items {
		// Only trigger for PipelineRuns that are actually pending
		if pr.Spec.Status != tektonv1.PipelineRunSpecStatusPending {
			continue
		}

		// Update the state annotation to trigger reconciliation
		// We add a small timestamp to ensure the annotation value changes and triggers the controller
		timestamp := fmt.Sprintf("%d", time.Now().Unix())
		patch := fmt.Sprintf(`{"metadata":{"annotations":{"pipelinesascode.tekton.dev/state":"%s","pipelinesascode.tekton.dev/reconcile-trigger":"%s"}}}`, kubeinteraction.StateQueued, timestamp)

		// Retry the patch operation to handle conflicts gracefully
		maxRetries := 3
		for attempt := 1; attempt <= maxRetries; attempt++ {
			_, err := qhc.tektonClient.TektonV1().PipelineRuns(namespace).Patch(ctx, pr.Name,
				"application/merge-patch+json", []byte(patch), metav1.PatchOptions{})
			if err == nil {
				qhc.logger.Infof("HEALTH-CHECKER: Triggered reconciliation for PipelineRun %s (repository %s) - this will kick off queue processing", pr.Name, repoName)
				return nil // Successfully triggered one, let the queue management handle the rest
			}

			// Check if it's a conflict error
			if strings.Contains(err.Error(), "the object has been modified") || strings.Contains(err.Error(), "Operation cannot be fulfilled") {
				if attempt < maxRetries {
					qhc.logger.Debugf("HEALTH-CHECKER: Conflict updating PipelineRun %s (attempt %d/%d), retrying: %v", pr.Name, attempt, maxRetries, err)
					time.Sleep(time.Duration(attempt) * 100 * time.Millisecond) // Exponential backoff
					continue
				}
			}

			// Check if the PipelineRun was deleted
			if strings.Contains(err.Error(), "not found") {
				qhc.logger.Debugf("HEALTH-CHECKER: PipelineRun %s was deleted during reconciliation trigger, trying next one", pr.Name)
				break // Try the next PipelineRun
			}

			qhc.logger.Warnf("HEALTH-CHECKER: Failed to trigger reconciliation for PipelineRun %s (attempt %d/%d): %v", pr.Name, attempt, maxRetries, err)
			if attempt == maxRetries {
				break // Try the next PipelineRun
			}
		}
	}

	qhc.logger.Infof("HEALTH-CHECKER: No pending PipelineRuns found to trigger reconciliation for repository %s", repoName)
	return nil
}

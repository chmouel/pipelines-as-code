package sync

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/generated/clientset/versioned"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/sort"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	versioned2 "github.com/tektoncd/pipeline/pkg/client/clientset/versioned"
	"go.uber.org/zap"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	creationTimestamp = "{.metadata.creationTimestamp}"
)

// QueueManager manages queues for multiple repositories
type QueueManager struct {
	logger *zap.SugaredLogger
	queues map[string]Semaphore
	lock   *sync.Mutex
}

// NewQueueManager creates a new QueueManager
func NewQueueManager(logger *zap.SugaredLogger) *QueueManager {
	return &QueueManager{
		logger: logger,
		queues: make(map[string]Semaphore),
		lock:   &sync.Mutex{},
	}
}

// getSemaphore returns existing semaphore created for repository or create
// a new one with limit provided in repository
// Semaphore: nothing but a waiting and a running queue for a repository
// with limit deciding how many should be running at a time.
func (qm *QueueManager) getSemaphore(repo *v1alpha1.Repository) (Semaphore, error) {
	repoKey := RepoKey(repo)

	if sema, found := qm.queues[repoKey]; found {
		if err := qm.checkAndUpdateSemaphoreSize(repo, sema); err != nil {
			return nil, err
		}
		return sema, nil
	}

	// create a new semaphore; can't assume callers have checked that ConcurrencyLimit is set
	limit := 0
	if repo.Spec.ConcurrencyLimit != nil {
		limit = *repo.Spec.ConcurrencyLimit
	}
	qm.queues[repoKey] = newSemaphore(repoKey, limit)

	return qm.queues[repoKey], nil
}

func (qm *QueueManager) checkAndUpdateSemaphoreSize(repo *v1alpha1.Repository, semaphore Semaphore) error {
	limit := *repo.Spec.ConcurrencyLimit
	if limit != semaphore.getLimit() {
		if semaphore.resize(limit) {
			return nil
		}
		return fmt.Errorf("failed to resize semaphore")
	}
	return nil
}

// AddListToRunningQueue adds the pipelineRun to the waiting queue of the repository
// and if it is at the top and ready to run which means currently running pipelineRun < limit
// then move it to running queue
// This adds the pipelineRuns in the same order as in the list.
func (qm *QueueManager) AddListToRunningQueue(repo *v1alpha1.Repository, list []string) ([]string, error) {
	qm.lock.Lock()
	defer qm.lock.Unlock()

	sema, err := qm.getSemaphore(repo)
	if err != nil {
		return []string{}, err
	}

	for _, pr := range list {
		if sema.addToQueue(pr, time.Now()) {
			qm.logger.Infof("added pipelineRun (%s) to running queue for repository (%s)", pr, RepoKey(repo))
		}
	}

	// it is possible something besides PAC set the PipelineRun to Pending; if concurrency limit has not
	// been set, return all the pending PipelineRuns; also, if the limit is zero, that also means do not throttle,
	// so we return all the PipelinesRuns, the for loop below skips that case as well
	if repo.Spec.ConcurrencyLimit == nil || *repo.Spec.ConcurrencyLimit == 0 {
		return sema.getCurrentPending(), nil
	}

	acquiredList := []string{}
	for i := 0; i < *repo.Spec.ConcurrencyLimit; i++ {
		acquired := sema.acquireLatest()
		if acquired != "" {
			qm.logger.Infof("moved (%s) to running for repository (%s)", acquired, RepoKey(repo))
			acquiredList = append(acquiredList, acquired)
		}
	}

	return acquiredList, nil
}

func (qm *QueueManager) AddToPendingQueue(repo *v1alpha1.Repository, list []string) error {
	qm.lock.Lock()
	defer qm.lock.Unlock()

	sema, err := qm.getSemaphore(repo)
	if err != nil {
		return err
	}

	for _, pr := range list {
		if sema.addToPendingQueue(pr, time.Now()) {
			qm.logger.Infof("added pipelineRun (%s) to pending queue for repository (%s)", pr, RepoKey(repo))
		}
	}
	return nil
}

func (qm *QueueManager) RemoveFromQueue(repoKey, prKey string) bool {
	qm.lock.Lock()
	defer qm.lock.Unlock()

	sema, found := qm.queues[repoKey]
	if !found {
		return false
	}

	sema.release(prKey)
	sema.removeFromQueue(prKey)
	qm.logger.Infof("removed (%s) for repository (%s)", prKey, repoKey)
	return true
}

func (qm *QueueManager) RemoveAndTakeItemFromQueue(repo *v1alpha1.Repository, run *tektonv1.PipelineRun) string {
	repoKey := RepoKey(repo)
	prKey := PrKey(run)
	if !qm.RemoveFromQueue(repoKey, prKey) {
		return ""
	}
	sema, found := qm.queues[repoKey]
	if !found {
		return ""
	}

	if next := sema.acquireLatest(); next != "" {
		qm.logger.Infof("moved (%s) to running for repository (%s)", next, repoKey)
		return next
	}
	return ""
}

// FilterPipelineRunByInProgress filters the given list of PipelineRun names to only include those
// that are in a "queued" state and have a pending status. It retrieves the PipelineRun objects
// from the Tekton API and checks their annotations and status to determine if they should be included.
//
// Returns A list of PipelineRun names that are in a "queued" state and have a pending status.
func FilterPipelineRunByState(ctx context.Context, tekton versioned2.Interface, orderList []string, wantedStatus, wantedState string) []string {
	orderedList := []string{}
	for _, prName := range orderList {
		prKey := strings.Split(prName, "/")
		pr, err := tekton.TektonV1().PipelineRuns(prKey[0]).Get(ctx, prKey[1], v1.GetOptions{})
		if err != nil {
			continue
		}

		state, exist := pr.GetAnnotations()[keys.State]
		if !exist {
			continue
		}

		if state == wantedState {
			if wantedStatus != "" && pr.Spec.Status != tektonv1.PipelineRunSpecStatus(wantedStatus) {
				continue
			}
			orderedList = append(orderedList, prName)
		}
	}
	return orderedList
}

// InitQueues rebuild all the queues for all repository if concurrency is defined before
// reconciler started reconciling them.
func (qm *QueueManager) InitQueues(ctx context.Context, tekton versioned2.Interface, pac versioned.Interface) error {
	// fetch all repos
	repos, err := pac.PipelinesascodeV1alpha1().Repositories("").List(ctx, v1.ListOptions{})
	if err != nil {
		return err
	}

	// pipelineRuns from the namespace where repository is present
	// those are required for creating queues
	for _, repo := range repos.Items {
		if repo.Spec.ConcurrencyLimit == nil || *repo.Spec.ConcurrencyLimit == 0 {
			continue
		}

		// add all pipelineRuns in started state to pending queue
		prs, err := tekton.TektonV1().PipelineRuns(repo.Namespace).
			List(ctx, v1.ListOptions{
				LabelSelector: fmt.Sprintf("%s=%s", keys.State, kubeinteraction.StateStarted),
			})
		if err != nil {
			return err
		}

		// sort the pipelinerun by creation time before adding to queue
		sortedPRs := sortPipelineRunsByCreationTimestamp(prs.Items)

		for _, pr := range sortedPRs {
			order, exist := pr.GetAnnotations()[keys.ExecutionOrder]
			if !exist {
				// if the pipelineRun doesn't have order label then wait
				return nil
			}
			orderedList := FilterPipelineRunByState(ctx, tekton, strings.Split(order, ","), "", kubeinteraction.StateStarted)

			_, err = qm.AddListToRunningQueue(&repo, orderedList)
			if err != nil {
				qm.logger.Error("failed to init queue for repo: ", repo.GetName())
			}
		}

		// now fetch all queued pipelineRun
		prs, err = tekton.TektonV1().PipelineRuns(repo.Namespace).
			List(ctx, v1.ListOptions{
				LabelSelector: fmt.Sprintf("%s=%s", keys.State, kubeinteraction.StateQueued),
			})
		if err != nil {
			return err
		}

		// sort the pipelinerun by creation time before adding to queue
		sortedPRs = sortPipelineRunsByCreationTimestamp(prs.Items)

		for _, pr := range sortedPRs {
			order, exist := pr.GetAnnotations()[keys.ExecutionOrder]
			if !exist {
				// if the pipelineRun doesn't have order label then wait
				return nil
			}
			orderedList := FilterPipelineRunByState(ctx, tekton, strings.Split(order, ","), tektonv1.PipelineRunSpecStatusPending, kubeinteraction.StateQueued)
			if err := qm.AddToPendingQueue(&repo, orderedList); err != nil {
				qm.logger.Error("failed to init queue for repo: ", repo.GetName())
			}
		}
	}

	return nil
}

func (qm *QueueManager) RemoveRepository(repo *v1alpha1.Repository) {
	qm.lock.Lock()
	defer qm.lock.Unlock()

	repoKey := RepoKey(repo)
	delete(qm.queues, repoKey)
}

func (qm *QueueManager) QueuedPipelineRuns(repo *v1alpha1.Repository) []string {
	qm.lock.Lock()
	defer qm.lock.Unlock()

	repoKey := RepoKey(repo)
	if sema, ok := qm.queues[repoKey]; ok {
		return sema.getCurrentPending()
	}
	return []string{}
}

func (qm *QueueManager) RunningPipelineRuns(repo *v1alpha1.Repository) []string {
	qm.lock.Lock()
	defer qm.lock.Unlock()

	repoKey := RepoKey(repo)
	if sema, ok := qm.queues[repoKey]; ok {
		return sema.getCurrentRunning()
	}
	return []string{}
}

func sortPipelineRunsByCreationTimestamp(prs []tektonv1.PipelineRun) []*tektonv1.PipelineRun {
	runTimeObj := []runtime.Object{}
	for i := range prs {
		runTimeObj = append(runTimeObj, &prs[i])
	}
	sort.ByField(creationTimestamp, runTimeObj)
	sortedPRs := []*tektonv1.PipelineRun{}
	for _, run := range runTimeObj {
		pr, _ := run.(*tektonv1.PipelineRun)
		sortedPRs = append(sortedPRs, pr)
	}
	return sortedPRs
}

// ResetAll resets all queues in the QueueManager
// Returns a map of repository keys to the number of items that were cleared
func (qm *QueueManager) ResetAll() map[string]int {
	qm.lock.Lock()
	defer qm.lock.Unlock()

	resetStats := make(map[string]int)

	for repoKey, sema := range qm.queues {
		// Count items before reset
		runningCount := len(sema.getCurrentRunning())
		pendingCount := len(sema.getCurrentPending())
		totalCount := runningCount + pendingCount

		// Reset the semaphore
		sema.resetAll()

		// Store the count of items that were cleared
		resetStats[repoKey] = totalCount

		qm.logger.Infof("Reset queue for repository %s: cleared %d running + %d pending = %d total items",
			repoKey, runningCount, pendingCount, totalCount)
	}

	qm.logger.Infof("Reset completed for %d repositories, total items cleared: %d",
		len(resetStats), func() int {
			total := 0
			for _, count := range resetStats {
				total += count
			}
			return total
		}())

	return resetStats
}

// RebuildQueuesForNamespace analyzes all pending PipelineRuns in a namespace and rebuilds
// the queues according to the repository concurrency settings.
// This is useful for operational scenarios where queues may have gotten out of sync.
func (qm *QueueManager) RebuildQueuesForNamespace(ctx context.Context, namespace string, tekton versioned2.Interface, pac versioned.Interface) (map[string]interface{}, error) {
	qm.lock.Lock()
	defer qm.lock.Unlock()

	rebuildStats := make(map[string]interface{})
	rebuildStats["namespace"] = namespace
	rebuildStats["repositories_processed"] = 0
	rebuildStats["repositories_rebuilt"] = make(map[string]map[string]int)
	rebuildStats["errors"] = []string{}

	// Get all repositories in the namespace
	repos, err := pac.PipelinesascodeV1alpha1().Repositories(namespace).List(ctx, v1.ListOptions{})
	if err != nil {
		return rebuildStats, fmt.Errorf("failed to list repositories in namespace %s: %w", namespace, err)
	}

	qm.logger.Infof("Starting queue rebuild for namespace %s with %d repositories", namespace, len(repos.Items))

	for _, repo := range repos.Items {
		rebuildStats["repositories_processed"] = rebuildStats["repositories_processed"].(int) + 1

		// Skip repositories without concurrency limits
		if repo.Spec.ConcurrencyLimit == nil || *repo.Spec.ConcurrencyLimit == 0 {
			qm.logger.Infof("Skipping repository %s/%s: no concurrency limit set", repo.Namespace, repo.Name)
			continue
		}

		repoStats := make(map[string]int)
		repoKey := RepoKey(&repo)

		// Clear existing queue for this repository
		if existingSema, found := qm.queues[repoKey]; found {
			runningCount := len(existingSema.getCurrentRunning())
			pendingCount := len(existingSema.getCurrentPending())
			repoStats["cleared_running"] = runningCount
			repoStats["cleared_pending"] = pendingCount

			existingSema.resetAll()
			qm.logger.Infof("Cleared existing queue for repo %s: %d running + %d pending", repoKey, runningCount, pendingCount)
		}

		// Create fresh semaphore for this repository
		qm.queues[repoKey] = newSemaphore(repoKey, *repo.Spec.ConcurrencyLimit)
		sema := qm.queues[repoKey]

		// Get all PipelineRuns in "started" state (currently running)
		startedPRs, err := tekton.TektonV1().PipelineRuns(namespace).List(ctx, v1.ListOptions{
			LabelSelector: fmt.Sprintf("%s=%s,%s=%s",
				keys.Repository, repo.Name,
				keys.State, kubeinteraction.StateStarted),
		})
		if err != nil {
			errMsg := fmt.Sprintf("failed to list started PipelineRuns for repo %s: %v", repoKey, err)
			rebuildStats["errors"] = append(rebuildStats["errors"].([]string), errMsg)
			continue
		}

		// Get all PipelineRuns in "queued" state (pending)
		queuedPRs, err := tekton.TektonV1().PipelineRuns(namespace).List(ctx, v1.ListOptions{
			LabelSelector: fmt.Sprintf("%s=%s,%s=%s",
				keys.Repository, repo.Name,
				keys.State, kubeinteraction.StateQueued),
		})
		if err != nil {
			errMsg := fmt.Sprintf("failed to list queued PipelineRuns for repo %s: %v", repoKey, err)
			rebuildStats["errors"] = append(rebuildStats["errors"].([]string), errMsg)
			continue
		}

		// Process started PipelineRuns (add to running queue)
		startedList := []string{}
		for _, pr := range startedPRs.Items {
			// Validate that the PipelineRun has execution order
			if order, exists := pr.GetAnnotations()[keys.ExecutionOrder]; exists {
				orderedList := FilterPipelineRunByState(ctx, tekton, strings.Split(order, ","), "", kubeinteraction.StateStarted)
				startedList = append(startedList, orderedList...)
			} else {
				startedList = append(startedList, PrKey(&pr))
			}
		}

		// Add started PipelineRuns to running queue
		if len(startedList) > 0 {
			acquired, err := qm.addListToRunningQueueInternal(&repo, startedList, sema)
			if err != nil {
				errMsg := fmt.Sprintf("failed to add started PipelineRuns to running queue for repo %s: %v", repoKey, err)
				rebuildStats["errors"] = append(rebuildStats["errors"].([]string), errMsg)
			} else {
				repoStats["rebuilt_running"] = len(acquired)
				qm.logger.Infof("Added %d started PipelineRuns to running queue for repo %s", len(acquired), repoKey)
			}
		}

		// Process queued PipelineRuns (add to pending queue)
		queuedList := []string{}
		for _, pr := range queuedPRs.Items {
			// Only include PipelineRuns that are actually pending
			if pr.Spec.Status == tektonv1.PipelineRunSpecStatusPending {
				if order, exists := pr.GetAnnotations()[keys.ExecutionOrder]; exists {
					orderedList := FilterPipelineRunByState(ctx, tekton, strings.Split(order, ","), tektonv1.PipelineRunSpecStatusPending, kubeinteraction.StateQueued)
					queuedList = append(queuedList, orderedList...)
				} else {
					queuedList = append(queuedList, PrKey(&pr))
				}
			}
		}

		// Add queued PipelineRuns to pending queue
		if len(queuedList) > 0 {
			err := qm.addToPendingQueueInternal(&repo, queuedList, sema)
			if err != nil {
				errMsg := fmt.Sprintf("failed to add queued PipelineRuns to pending queue for repo %s: %v", repoKey, err)
				rebuildStats["errors"] = append(rebuildStats["errors"].([]string), errMsg)
			} else {
				repoStats["rebuilt_pending"] = len(queuedList)
				qm.logger.Infof("Added %d queued PipelineRuns to pending queue for repo %s", len(queuedList), repoKey)
			}
		}

		// Store repository rebuild stats
		rebuildStats["repositories_rebuilt"].(map[string]map[string]int)[repoKey] = repoStats

		qm.logger.Infof("Completed queue rebuild for repo %s: %d running, %d pending",
			repoKey, len(sema.getCurrentRunning()), len(sema.getCurrentPending()))
	}

	qm.logger.Infof("Completed queue rebuild for namespace %s: processed %d repositories",
		namespace, rebuildStats["repositories_processed"].(int))

	return rebuildStats, nil
}

// addListToRunningQueueInternal is an internal version that works with an existing semaphore
func (qm *QueueManager) addListToRunningQueueInternal(repo *v1alpha1.Repository, list []string, sema Semaphore) ([]string, error) {
	for _, pr := range list {
		if sema.addToQueue(pr, time.Now()) {
			qm.logger.Infof("added pipelineRun (%s) to running queue for repository (%s)", pr, RepoKey(repo))
		}
	}

	// if concurrency limit is not set or is zero, return all pending PipelineRuns
	if repo.Spec.ConcurrencyLimit == nil || *repo.Spec.ConcurrencyLimit == 0 {
		return sema.getCurrentPending(), nil
	}

	acquiredList := []string{}
	for i := 0; i < *repo.Spec.ConcurrencyLimit; i++ {
		acquired := sema.acquireLatest()
		if acquired != "" {
			qm.logger.Infof("moved (%s) to running for repository (%s)", acquired, RepoKey(repo))
			acquiredList = append(acquiredList, acquired)
		}
	}

	return acquiredList, nil
}

// addToPendingQueueInternal is an internal version that works with an existing semaphore
func (qm *QueueManager) addToPendingQueueInternal(repo *v1alpha1.Repository, list []string, sema Semaphore) error {
	for _, pr := range list {
		if sema.addToPendingQueue(pr, time.Now()) {
			qm.logger.Infof("added pipelineRun (%s) to pending queue for repository (%s)", pr, RepoKey(repo))
		}
	}
	return nil
}

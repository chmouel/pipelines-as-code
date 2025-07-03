package sync

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/generated/clientset/versioned"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	tektonVersionedClient "github.com/tektoncd/pipeline/pkg/client/clientset/versioned"
	"go.uber.org/zap"
)

// QueueManager now wraps a SQLiteQueueManager for all queue operations.
type QueueManager struct {
	db     *SQLiteQueueManager
	lock   *sync.Mutex
	logger *zap.SugaredLogger
}

func NewQueueManager(logger *zap.SugaredLogger, db *SQLiteQueueManager) *QueueManager {
	return &QueueManager{
		db:     db,
		lock:   &sync.Mutex{},
		logger: logger,
	}
}

// AddListToRunningQueue adds PipelineRuns to the queue and tries to acquire up to the concurrency limit.
func (qm *QueueManager) AddListToRunningQueue(repo *v1alpha1.Repository, list []string) ([]string, error) {
	qm.lock.Lock()
	defer qm.lock.Unlock()

	repoKey := RepoKey(repo)
	if repo.Spec.ConcurrencyLimit == nil {
		return nil, fmt.Errorf("concurrency limit not set for repo %s", repoKey)
	}
	limit := *repo.Spec.ConcurrencyLimit
	if err := qm.db.SetLimit(repoKey, limit); err != nil {
		return nil, err
	}

	for _, pr := range list {
		// Use current time as priority for FIFO
		_ = qm.db.AddToQueue(repoKey, pr, time.Now().UnixNano(), time.Now())
	}

	acquiredList := []string{}
	for i := 0; i < limit; i++ {
		acquired, err := qm.db.AcquireNext(repoKey)
		if err != nil || acquired == "" {
			break
		}
		acquiredList = append(acquiredList, acquired)
	}
	return acquiredList, nil
}

func (qm *QueueManager) AddToPendingQueue(repo *v1alpha1.Repository, list []string) error {
	qm.lock.Lock()
	defer qm.lock.Unlock()

	repoKey := RepoKey(repo)
	for _, pr := range list {
		_ = qm.db.AddToQueue(repoKey, pr, time.Now().UnixNano(), time.Now())
	}
	return nil
}

func (qm *QueueManager) RemoveFromQueue(repoKey, prKey string) bool {
	qm.lock.Lock()
	defer qm.lock.Unlock()
	if qm.logger != nil {
		qm.logger.Infof("[DEBUG] RemoveFromQueue called with repoKey=%s, prKey=%s", repoKey, prKey)
	}
	_ = qm.db.Release(repoKey, prKey)
	_ = qm.db.RemoveFromQueue(repoKey, prKey)
	qm.logger.Infof("removed (%s) for repository (%s)", prKey, repoKey)
	return true
}

func (qm *QueueManager) RemoveAndTakeItemFromQueue(repo *v1alpha1.Repository, run *tektonv1.PipelineRun) string {
	repoKey := RepoKey(repo)
	prKey := PrKey(run)
	qm.logger.Debugf("RemoveAndTakeItemFromQueue called with repoKey=%s, prKey=%s", repoKey, prKey)
	if !qm.RemoveFromQueue(repoKey, prKey) {
		return ""
	}
	acquired, err := qm.db.AcquireNext(repoKey)
	if err != nil {
		return ""
	}
	return acquired
}

// FilterPipelineRunByState is now a no-op or can be removed, as state is in SQLite.
func FilterPipelineRunByState(_ context.Context, _ tektonVersionedClient.Interface, orderList []string, _, _ string) []string {
	// This function is obsolete with SQLite-based queue.
	return orderList
}

// InitQueues is now a no-op or can be used to sync DB state from existing PipelineRuns if needed.
func (qm *QueueManager) InitQueues(_ context.Context, _ tektonVersionedClient.Interface, _ versioned.Interface) error {
	// Optionally, scan all PipelineRuns and add to DB if not present.
	return nil
}

// QueuedPipelineRuns returns all pending PipelineRuns for a repo.
func (qm *QueueManager) QueuedPipelineRuns(repo *v1alpha1.Repository) []string {
	repoKey := RepoKey(repo)
	pending, err := qm.db.GetCurrentPending(repoKey)
	if err != nil {
		return nil
	}
	return pending
}

// RunningPipelineRuns returns all running PipelineRuns for a repo.
func (qm *QueueManager) RunningPipelineRuns(repo *v1alpha1.Repository) []string {
	repoKey := RepoKey(repo)
	running, err := qm.db.GetCurrentRunning(repoKey)
	if err != nil {
		return nil
	}
	return running
}

// RemoveRepository removes all queue state for a repo.
func (qm *QueueManager) RemoveRepository(repo *v1alpha1.Repository) {
	qm.lock.Lock()
	defer qm.lock.Unlock()
	repoKey := RepoKey(repo)
	_ = qm.db.RemoveRepository(repoKey)
	qm.logger.Infof("removed repository (%s) from queue", repoKey)
}

// SyncPipelineRunState syncs a PipelineRun's state from annotations to SQLite
func (qm *QueueManager) SyncPipelineRunState(repo, prID, state string) error {
	qm.lock.Lock()
	defer qm.lock.Unlock()
	// repo parameter now contains the full repo key (namespace/name)
	return qm.db.SyncPipelineRunState(repo, prID, state)
}

// GetPipelineRunState gets the state of a PipelineRun from SQLite
func (qm *QueueManager) GetPipelineRunState(repo, prID string) (string, error) {
	qm.lock.Lock()
	defer qm.lock.Unlock()
	fmt.Printf("[DEBUG] QueueManager.GetPipelineRunState: repo=%s, prID=%s\n", repo, prID)
	// repo parameter now contains the full repo key (namespace/name)
	state, err := qm.db.GetPipelineRunState(repo, prID)
	fmt.Printf("[DEBUG] QueueManager.GetPipelineRunState: result state=%s, err=%v\n", state, err)
	return state, err
}

// GetAllPipelineRunStates gets all PipelineRun states for a repository
func (qm *QueueManager) GetAllPipelineRunStates(repo string) (map[string]string, error) {
	qm.lock.Lock()
	defer qm.lock.Unlock()
	// repo parameter now contains the full repo key (namespace/name)
	return qm.db.GetAllPipelineRunStates(repo)
}

// Helper functions for repo and pr keys
func RepoKey(repo *v1alpha1.Repository) string {
	return fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)
}

func PrKey(pr *tektonv1.PipelineRun) string {
	return fmt.Sprintf("%s/%s", pr.Namespace, pr.Name)
}

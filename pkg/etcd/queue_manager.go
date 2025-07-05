package etcd

import (
	"context"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/generated/clientset/versioned"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/sync"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	tektonVersionedClient "github.com/tektoncd/pipeline/pkg/client/clientset/versioned"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// QueueManager implements the sync.QueueManagerInterface using etcd.
type QueueManager struct {
	concurrencyManager *ConcurrencyManager
	logger             *zap.SugaredLogger
	// Store lease IDs for cleanup
	leases map[string]clientv3.LeaseID
}

// NewQueueManager creates a new etcd-based queue manager.
func NewQueueManager(etcdClient Client, logger *zap.SugaredLogger) *QueueManager {
	return &QueueManager{
		concurrencyManager: NewConcurrencyManager(etcdClient, logger),
		logger:             logger,
		leases:             make(map[string]clientv3.LeaseID),
	}
}

// InitQueues initializes queues for all repositories.
func (qm *QueueManager) InitQueues(_ context.Context, _ tektonVersionedClient.Interface, _ versioned.Interface) error {
	qm.logger.Info("initializing etcd-based concurrency queues")

	// In etcd-based implementation, we don't need to pre-populate queues
	// since state is maintained in etcd and slots are acquired on-demand

	// We could optionally scan for existing PipelineRuns and register their leases
	// but for now, we'll let them naturally acquire slots when reconciled

	return nil
}

// RemoveRepository cleans up all etcd state for a repository.
func (qm *QueueManager) RemoveRepository(repo *v1alpha1.Repository) {
	ctx := context.Background()
	if err := qm.concurrencyManager.CleanupRepository(ctx, repo); err != nil {
		qm.logger.Errorf("failed to cleanup repository %s/%s: %v", repo.Namespace, repo.Name, err)
	}
}

// QueuedPipelineRuns returns empty list since we don't maintain explicit queues.
// In etcd-based approach, PipelineRuns are either running (have a lease) or waiting (no lease).
func (qm *QueueManager) QueuedPipelineRuns(_ *v1alpha1.Repository) []string {
	// In the etcd-based approach, we don't maintain explicit queues
	// PipelineRuns that don't have a lease are effectively "queued"
	return []string{}
}

// RunningPipelineRuns returns the list of PipelineRuns that currently hold concurrency slots.
func (qm *QueueManager) RunningPipelineRuns(repo *v1alpha1.Repository) []string {
	ctx := context.Background()
	running, err := qm.concurrencyManager.GetRunningPipelineRuns(ctx, repo)
	if err != nil {
		qm.logger.Errorf("failed to get running pipeline runs for %s/%s: %v", repo.Namespace, repo.Name, err)
		return []string{}
	}
	return running
}

// AddListToRunningQueue attempts to acquire concurrency slots for the provided PipelineRuns.
// Returns the list of PipelineRuns that successfully acquired slots.
func (qm *QueueManager) AddListToRunningQueue(repo *v1alpha1.Repository, list []string) ([]string, error) {
	ctx := context.Background()
	acquired := []string{}

	for _, prKey := range list {
		success, leaseID, err := qm.concurrencyManager.AcquireSlot(ctx, repo, prKey)
		if err != nil {
			qm.logger.Errorf("failed to acquire slot for %s: %v", prKey, err)
			continue
		}

		if success {
			acquired = append(acquired, prKey)
			qm.leases[prKey] = leaseID // Store for cleanup

			// Store lease ID in etcd for later retrieval
			if leaseID != 0 {
				if err := qm.concurrencyManager.SetPipelineRunLease(ctx, repo, prKey, leaseID); err != nil {
					qm.logger.Errorf("failed to store lease ID for %s: %v", prKey, err)
				}
			}

			qm.logger.Infof("acquired concurrency slot for %s in repository %s/%s", prKey, repo.Namespace, repo.Name)
		} else {
			qm.logger.Infof("concurrency limit reached, %s will wait for available slot", prKey)
		}
	}

	return acquired, nil
}

// AddToPendingQueue is a no-op in etcd-based implementation.
// PipelineRuns are either running (have lease) or waiting (no lease).
func (qm *QueueManager) AddToPendingQueue(_ *v1alpha1.Repository, _ []string) error {
	// In etcd-based approach, there's no explicit pending queue
	// PipelineRuns without leases are effectively pending
	qm.logger.Debugf("etcd-based queue manager: no explicit pending queue in etcd-based implementation")
	return nil
}

// RemoveFromQueue releases the concurrency slot for a PipelineRun.
func (qm *QueueManager) RemoveFromQueue(repoKey, prKey string) bool {
	ctx := context.Background()

	// Get lease ID for this PipelineRun
	leaseID, exists := qm.leases[prKey]
	if !exists {
		// Try to get from etcd
		_, _, err := ParsePipelineRunKey(prKey)
		if err != nil {
			qm.logger.Errorf("invalid pipeline run key: %s", prKey)
			return false
		}

		repoNamespace, repoName, err := ParsePipelineRunKey(repoKey)
		if err != nil {
			qm.logger.Errorf("invalid repo key: %s", repoKey)
			return false
		}

		repo := &v1alpha1.Repository{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: repoNamespace,
				Name:      repoName,
			},
		}

		leaseID, err = qm.concurrencyManager.GetPipelineRunLease(ctx, repo, prKey)
		if err != nil {
			qm.logger.Errorf("failed to get lease for %s: %v", prKey, err)
			return false
		}
	}

	// Release the lease
	if err := qm.concurrencyManager.ReleaseSlot(ctx, leaseID, prKey, repoKey); err != nil {
		qm.logger.Errorf("failed to release slot for %s: %v", prKey, err)
		return false
	}

	// Clean up stored lease ID
	delete(qm.leases, prKey)

	qm.logger.Infof("released concurrency slot for %s", prKey)
	return true
}

// RemoveAndTakeItemFromQueue releases a slot and returns the next item to process.
// In etcd-based approach, this is simplified since there's no explicit queue.
func (qm *QueueManager) RemoveAndTakeItemFromQueue(repo *v1alpha1.Repository, run *tektonv1.PipelineRun) string {
	prKey := sync.PrKey(run)
	repoKey := sync.RepoKey(repo)

	// Release the current slot
	qm.RemoveFromQueue(repoKey, prKey)

	// In etcd-based approach, we don't return a "next" item since there's no explicit queue
	// PipelineRuns will naturally try to acquire slots when reconciled
	// The watcher pattern will trigger reconciliation of waiting PipelineRuns

	return ""
}

// TryAcquireSlot attempts to acquire a concurrency slot for a PipelineRun.
// This is a new method specific to the etcd implementation.
func (qm *QueueManager) TryAcquireSlot(ctx context.Context, repo *v1alpha1.Repository, prKey string) (bool, clientv3.LeaseID, error) {
	return qm.concurrencyManager.AcquireSlot(ctx, repo, prKey)
}

// SetupWatcher sets up a watcher for slot availability changes.
// This can be used to trigger reconciliation when slots become available.
func (qm *QueueManager) SetupWatcher(ctx context.Context, repo *v1alpha1.Repository, callback func()) {
	qm.concurrencyManager.WatchSlotAvailability(ctx, repo, callback)
}

// GetConcurrencyManager returns the underlying concurrency manager.
// This allows other components to access etcd-specific functionality.
func (qm *QueueManager) GetConcurrencyManager() *ConcurrencyManager {
	return qm.concurrencyManager
}

package concurrency

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Manager provides a unified interface for concurrency control
type Manager struct {
	driver       ConcurrencyDriver
	queueManager QueueManager
	logger       *zap.SugaredLogger
	driverType   string
}

// NewManager creates a new concurrency manager with the specified driver
func NewManager(config *DriverConfig, logger *zap.SugaredLogger) (*Manager, error) {
	driver, err := NewDriver(config, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create driver: %w", err)
	}

	queueManager, err := NewQueueManager(driver, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create queue manager: %w", err)
	}

	return &Manager{
		driver:       driver,
		queueManager: queueManager,
		logger:       logger,
		driverType:   config.Driver,
	}, nil
}

// NewDriver creates a new concurrency driver based on configuration
func NewDriver(config *DriverConfig, logger *zap.SugaredLogger) (ConcurrencyDriver, error) {
	switch config.Driver {
	case "etcd":
		if config.EtcdConfig == nil {
			return nil, fmt.Errorf("etcd configuration is required for etcd driver")
		}
		return NewEtcdDriver(config.EtcdConfig, logger)
	case "postgresql":
		if config.PostgreSQLConfig == nil {
			return nil, fmt.Errorf("postgresql configuration is required for postgresql driver")
		}
		return NewPostgreSQLDriver(config.PostgreSQLConfig, logger)
	case "memory":
		return NewMemoryDriver(config.MemoryConfig, logger)
	default:
		return nil, fmt.Errorf("unsupported driver: %s", config.Driver)
	}
}

// NewQueueManager creates a new queue manager with the specified driver
func NewQueueManager(driver ConcurrencyDriver, logger *zap.SugaredLogger) (QueueManager, error) {
	return &queueManager{
		driver:        driver,
		logger:        logger,
		pendingQueues: make(map[string]*PriorityQueue),
	}, nil
}

// queueManager implements QueueManager interface
type queueManager struct {
	driver        ConcurrencyDriver
	logger        *zap.SugaredLogger
	pendingQueues map[string]*PriorityQueue // repoKey -> pending queue
	mutex         sync.RWMutex
}

// Manager methods delegate to the underlying driver and queue manager
func (m *Manager) AcquireSlot(ctx context.Context, repo *v1alpha1.Repository, pipelineRunKey string) (bool, LeaseID, error) {
	success, leaseID, err := m.driver.AcquireSlot(ctx, repo, pipelineRunKey)
	if err != nil {
		return false, nil, err
	}

	if success {
		// Update the pipeline run state to running
		if err := m.driver.SetPipelineRunState(ctx, pipelineRunKey, "running", repo); err != nil {
			m.logger.Errorf("failed to set pipeline run state to running for %s: %v", pipelineRunKey, err)
		}
	}

	return success, leaseID, nil
}

func (m *Manager) ReleaseSlot(ctx context.Context, leaseID LeaseID, pipelineRunKey, repoKey string) error {
	err := m.driver.ReleaseSlot(ctx, leaseID, pipelineRunKey, repoKey)
	if err != nil {
		return err
	}

	// Clean up the pipeline run state
	// Parse repoKey to get namespace and name
	parts := strings.Split(repoKey, "/")
	if len(parts) == 2 {
		repo := &v1alpha1.Repository{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: parts[0],
				Name:      parts[1],
			},
		}
		if err := m.driver.SetPipelineRunState(ctx, pipelineRunKey, "released", repo); err != nil {
			m.logger.Errorf("failed to set pipeline run state to released for %s: %v", pipelineRunKey, err)
		}
	}

	return nil
}

func (m *Manager) GetCurrentSlots(ctx context.Context, repo *v1alpha1.Repository) (int, error) {
	return m.driver.GetCurrentSlots(ctx, repo)
}

func (m *Manager) GetRunningPipelineRuns(ctx context.Context, repo *v1alpha1.Repository) ([]string, error) {
	return m.driver.GetRunningPipelineRuns(ctx, repo)
}

func (m *Manager) WatchSlotAvailability(ctx context.Context, repo *v1alpha1.Repository, callback func()) {
	m.driver.WatchSlotAvailability(ctx, repo, callback)
}

func (m *Manager) SetRepositoryState(ctx context.Context, repo *v1alpha1.Repository, state string) error {
	return m.driver.SetRepositoryState(ctx, repo, state)
}

func (m *Manager) GetRepositoryState(ctx context.Context, repo *v1alpha1.Repository) (string, error) {
	return m.driver.GetRepositoryState(ctx, repo)
}

func (m *Manager) SetPipelineRunState(ctx context.Context, pipelineRunKey, state string) error {
	// This method needs to be updated to include repository information
	// For now, we'll return an error indicating this method needs to be updated
	return fmt.Errorf("SetPipelineRunState method needs repository information, use SetPipelineRunStateWithRepo instead")
}

func (m *Manager) GetPipelineRunState(ctx context.Context, pipelineRunKey string) (string, error) {
	return m.driver.GetPipelineRunState(ctx, pipelineRunKey)
}

func (m *Manager) CleanupRepository(ctx context.Context, repo *v1alpha1.Repository) error {
	return m.driver.CleanupRepository(ctx, repo)
}

func (m *Manager) Close() error {
	return m.driver.Close()
}

// GetDriverType returns the type of driver being used
func (m *Manager) GetDriverType() string {
	return m.driverType
}

// GetQueueManager returns the queue manager
func (m *Manager) GetQueueManager() QueueManager {
	return m.queueManager
}

// QueueManager interface implementation
func (qm *queueManager) InitQueues(ctx context.Context, tektonClient, pacClient interface{}) error {
	qm.logger.Info("initializing concurrency queues with state-based approach")

	// Type assert the clients to get the proper interfaces
	// Note: We don't need the Tekton client for state-based initialization
	// as we load state directly from the concurrency driver

	pac, ok := pacClient.(interface {
		PipelinesascodeV1alpha1() interface {
			Repositories(namespace string) interface {
				List(ctx context.Context, opts interface{}) (interface{}, error)
			}
		}
	})
	if !ok {
		return fmt.Errorf("invalid pac client type")
	}

	// Fetch all repositories
	reposResp, err := pac.PipelinesascodeV1alpha1().Repositories("").List(ctx, map[string]interface{}{})
	if err != nil {
		return fmt.Errorf("failed to list repositories: %w", err)
	}

	// Type assert to get the repositories
	repos, ok := reposResp.(interface{ Items() []interface{} })
	if !ok {
		return fmt.Errorf("invalid repositories response type")
	}

	// Process each repository
	for _, repoItem := range repos.Items() {
		repo, ok := repoItem.(*v1alpha1.Repository)
		if !ok {
			qm.logger.Warnf("invalid repository type, skipping")
			continue
		}

		// Skip repositories without concurrency limits
		if repo.Spec.ConcurrencyLimit == nil || *repo.Spec.ConcurrencyLimit == 0 {
			continue
		}

		qm.logger.Infof("initializing queues for repository %s/%s", repo.Namespace, repo.Name)

		// Load existing queued PipelineRuns from the concurrency driver
		queuedPRs, err := qm.driver.GetQueuedPipelineRuns(ctx, repo)
		if err != nil {
			qm.logger.Errorf("failed to get queued pipeline runs for %s/%s: %v", repo.Namespace, repo.Name, err)
			continue
		}

		// Load existing running PipelineRuns from the concurrency driver
		runningPRs, err := qm.driver.GetRunningPipelineRuns(ctx, repo)
		if err != nil {
			qm.logger.Errorf("failed to get running pipeline runs for %s/%s: %v", repo.Namespace, repo.Name, err)
			continue
		}

		// Initialize the priority queue for this repository
		repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)
		qm.mutex.Lock()
		if qm.pendingQueues[repoKey] == nil {
			qm.pendingQueues[repoKey] = NewPriorityQueue()
		}
		qm.mutex.Unlock()

		// Add queued PipelineRuns to the priority queue
		now := time.Now()
		for _, prKey := range queuedPRs {
			qm.pendingQueues[repoKey].Add(prKey, now)
			qm.logger.Infof("restored queued pipeline run %s to queue for repository %s", prKey, repoKey)
		}

		// Log the restored state
		qm.logger.Infof("restored state for repository %s: %d queued, %d running",
			repoKey, len(queuedPRs), len(runningPRs))
	}

	qm.logger.Info("concurrency queues initialized successfully with state-based approach")
	return nil
}

func (qm *queueManager) RemoveRepository(repo *v1alpha1.Repository) {
	ctx := context.Background()
	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)

	qm.mutex.Lock()
	defer qm.mutex.Unlock()

	// Remove pending queue for this repository
	delete(qm.pendingQueues, repoKey)

	// Cleanup driver state
	if err := qm.driver.CleanupRepository(ctx, repo); err != nil {
		qm.logger.Errorf("failed to cleanup repository %s/%s: %v", repo.Namespace, repo.Name, err)
	}
}

func (qm *queueManager) QueuedPipelineRuns(repo *v1alpha1.Repository) []string {
	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)

	qm.mutex.RLock()
	defer qm.mutex.RUnlock()

	if queue, exists := qm.pendingQueues[repoKey]; exists {
		return queue.GetPendingItems()
	}
	return []string{}
}

func (qm *queueManager) RunningPipelineRuns(repo *v1alpha1.Repository) []string {
	ctx := context.Background()
	running, err := qm.driver.GetRunningPipelineRuns(ctx, repo)
	if err != nil {
		qm.logger.Errorf("failed to get running pipeline runs for %s/%s: %v", repo.Namespace, repo.Name, err)
		return []string{}
	}
	return running
}

func (qm *queueManager) AddListToRunningQueue(repo *v1alpha1.Repository, list []string) ([]string, error) {
	ctx := context.Background()
	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)

	qm.mutex.Lock()
	defer qm.mutex.Unlock()

	// Initialize pending queue for this repository if not exists
	if qm.pendingQueues[repoKey] == nil {
		qm.pendingQueues[repoKey] = NewPriorityQueue()
	}

	// Add all PipelineRuns to pending queue with current time as priority
	now := time.Now()
	for _, prKey := range list {
		qm.pendingQueues[repoKey].Add(prKey, now)
		qm.logger.Infof("added pipelineRun (%s) to pending queue for repository (%s)", prKey, repoKey)
	}

	// If no concurrency limit, return all pending items
	if repo.Spec.ConcurrencyLimit == nil || *repo.Spec.ConcurrencyLimit == 0 {
		return qm.pendingQueues[repoKey].GetPendingItems(), nil
	}

	// Try to acquire slots up to the concurrency limit
	limit := *repo.Spec.ConcurrencyLimit
	acquired := []string{}

	for i := 0; i < limit; i++ {
		item := qm.pendingQueues[repoKey].PopItem()
		if item == nil {
			break // No more items in queue
		}

		success, _, err := qm.driver.AcquireSlot(ctx, repo, item.Key)
		if err != nil {
			qm.logger.Errorf("failed to acquire slot for %s: %v", item.Key, err)
			// Put it back in the queue
			qm.pendingQueues[repoKey].Add(item.Key, item.CreationTime)
			continue
		}

		if success {
			acquired = append(acquired, item.Key)
			qm.logger.Infof("moved (%s) to running for repository (%s)", item.Key, repoKey)
		} else {
			// Put it back in the queue if acquisition failed
			qm.pendingQueues[repoKey].Add(item.Key, item.CreationTime)
			qm.logger.Infof("concurrency limit reached, %s will wait for available slot", item.Key)
		}
	}

	return acquired, nil
}

func (qm *queueManager) AddToPendingQueue(repo *v1alpha1.Repository, list []string) error {
	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)

	qm.mutex.Lock()
	defer qm.mutex.Unlock()

	// Initialize pending queue for this repository if not exists
	if qm.pendingQueues[repoKey] == nil {
		qm.pendingQueues[repoKey] = NewPriorityQueue()
	}

	// Add all PipelineRuns to pending queue with current time as priority
	now := time.Now()
	for _, prKey := range list {
		qm.pendingQueues[repoKey].Add(prKey, now)

		// Store the queued state in the driver for persistence
		if err := qm.driver.SetPipelineRunState(context.Background(), prKey, "queued", repo); err != nil {
			qm.logger.Errorf("failed to set pipeline run state for %s: %v", prKey, err)
		}

		qm.logger.Infof("added pipelineRun (%s) to pending queue for repository (%s)", prKey, repoKey)
	}

	return nil
}

func (qm *queueManager) RemoveFromQueue(repoKey, prKey string) bool {
	ctx := context.Background()

	qm.mutex.Lock()
	defer qm.mutex.Unlock()

	// Remove from pending queue
	if queue, exists := qm.pendingQueues[repoKey]; exists {
		queue.Remove(prKey)
	}

	// For memory driver, we need to find the slot and release it properly
	// Since we don't have the leaseID, we'll use a special method to release by pipeline run key
	if err := qm.driver.ReleaseSlot(ctx, 0, prKey, repoKey); err != nil {
		qm.logger.Errorf("failed to release slot for %s: %v", prKey, err)
		return false
	}

	qm.logger.Infof("removed (%s) for repository (%s)", prKey, repoKey)
	return true
}

func (qm *queueManager) RemoveAndTakeItemFromQueue(repo *v1alpha1.Repository, run interface{}) string {
	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)

	qm.mutex.Lock()
	defer qm.mutex.Unlock()

	// Remove the current item from queue
	if queue, exists := qm.pendingQueues[repoKey]; exists {
		// Get the next item before removing current
		nextItem := queue.Peek()
		if nextItem != nil {
			queue.PopItem() // Remove the current item
			qm.logger.Infof("moved (%s) to running for repository (%s)", nextItem.Key, repoKey)
			return nextItem.Key
		}
	}

	return ""
}

func (qm *queueManager) TryAcquireSlot(ctx context.Context, repo *v1alpha1.Repository, prKey string) (bool, LeaseID, error) {
	return qm.driver.AcquireSlot(ctx, repo, prKey)
}

func (qm *queueManager) SetupWatcher(ctx context.Context, repo *v1alpha1.Repository, callback func()) {
	qm.driver.WatchSlotAvailability(ctx, repo, callback)
}

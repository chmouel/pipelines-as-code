package etcd

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
)

const (
	// etcd key prefixes.
	concurrencyPrefix = "/pac/concurrency"
	leasePrefix       = "/pac/leases"

	// default lease TTL in seconds.
	defaultLeaseTTL = 3600 // 1 hour
)

// ConcurrencyManager handles concurrency control using etcd leases.
type ConcurrencyManager struct {
	client Client
	logger *zap.SugaredLogger
}

// NewConcurrencyManager creates a new etcd-based concurrency manager.
func NewConcurrencyManager(client Client, logger *zap.SugaredLogger) *ConcurrencyManager {
	return &ConcurrencyManager{
		client: client,
		logger: logger,
	}
}

// AcquireSlot tries to acquire a concurrency slot for a PipelineRun in a repository.
// Returns true if slot was acquired, false if concurrency limit reached.
func (cm *ConcurrencyManager) AcquireSlot(ctx context.Context, repo *v1alpha1.Repository, pipelineRunKey string) (bool, clientv3.LeaseID, error) {
	if repo.Spec.ConcurrencyLimit == nil || *repo.Spec.ConcurrencyLimit == 0 {
		// No concurrency limit, always allow
		return true, 0, nil
	}

	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)
	leaseKeyPrefix := fmt.Sprintf("%s/%s/", leasePrefix, repoKey)
	limit := *repo.Spec.ConcurrencyLimit

	// Create a lease first
	leaseResp, err := cm.client.Grant(ctx, defaultLeaseTTL)
	if err != nil {
		return false, 0, fmt.Errorf("failed to create lease: %w", err)
	}

	leaseID := leaseResp.ID
	leaseKey := fmt.Sprintf("%s%s", leaseKeyPrefix, pipelineRunKey)

	// Atomic transaction: check current lease count and acquire if under limit
	txn := cm.client.Txn(ctx)

	// Get all current leases for this repository
	getResp, err := cm.client.Get(ctx, leaseKeyPrefix, clientv3.WithPrefix(), clientv3.WithCountOnly())
	if err != nil {
		_, revokeErr := cm.client.Revoke(ctx, leaseID)
		if revokeErr != nil {
			cm.logger.Errorf("failed to revoke lease %x after get error: %v", leaseID, revokeErr)
		}
		return false, 0, fmt.Errorf("failed to get current leases: %w", err)
	}

	currentCount := getResp.Count

	if currentCount >= int64(limit) {
		// Concurrency limit reached, revoke lease and return false
		_, revokeErr := cm.client.Revoke(ctx, leaseID)
		if revokeErr != nil {
			cm.logger.Errorf("failed to revoke lease %x after concurrency limit: %v", leaseID, revokeErr)
		}
		cm.logger.Infof("concurrency limit reached for repository %s: %d/%d", repoKey, currentCount, limit)
		return false, 0, nil
	}

	// Try to acquire slot by putting our lease
	resp, err := txn.
		If(clientv3.Compare(clientv3.CreateRevision(leaseKey), "=", 0)). // Key doesn't exist
		Then(clientv3.OpPut(leaseKey, pipelineRunKey, clientv3.WithLease(leaseID))).
		Commit()
	if err != nil {
		_, revokeErr := cm.client.Revoke(ctx, leaseID)
		if revokeErr != nil {
			cm.logger.Errorf("failed to revoke lease %x after txn error: %v", leaseID, revokeErr)
		}
		return false, 0, fmt.Errorf("failed to acquire slot: %w", err)
	}

	if !resp.Succeeded {
		// Key already exists (race condition), revoke lease
		_, revokeErr := cm.client.Revoke(ctx, leaseID)
		if revokeErr != nil {
			cm.logger.Errorf("failed to revoke lease %x after race: %v", leaseID, revokeErr)
		}
		return false, 0, nil
	}

	// Keep lease alive
	keepAliveCh, err := cm.client.KeepAlive(ctx, leaseID)
	if err != nil {
		_, revokeErr := cm.client.Revoke(ctx, leaseID)
		if revokeErr != nil {
			cm.logger.Errorf("failed to revoke lease %x after keepalive error: %v", leaseID, revokeErr)
		}
		return false, 0, fmt.Errorf("failed to start lease keepalive: %w", err)
	}

	// Start keepalive goroutine
	go func() {
		// nolint: revive
		for range keepAliveCh {
			// Just consume keepalive responses.
		}
	}()

	cm.logger.Infof("acquired concurrency slot for %s in repository %s (lease: %x)", pipelineRunKey, repoKey, leaseID)
	return true, leaseID, nil
}

// ReleaseSlot releases a concurrency slot by revoking the lease.
func (cm *ConcurrencyManager) ReleaseSlot(ctx context.Context, leaseID clientv3.LeaseID, pipelineRunKey, repoKey string) error {
	if leaseID == 0 {
		// No lease to revoke
		return nil
	}

	_, err := cm.client.Revoke(ctx, leaseID)
	if err != nil {
		cm.logger.Errorf("failed to revoke lease %x for %s: %v", leaseID, pipelineRunKey, err)
		return err
	}

	cm.logger.Infof("released concurrency slot for %s in repository %s (lease: %x)", pipelineRunKey, repoKey, leaseID)
	return nil
}

// GetCurrentSlots returns the current number of slots in use for a repository.
func (cm *ConcurrencyManager) GetCurrentSlots(ctx context.Context, repo *v1alpha1.Repository) (int, error) {
	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)
	leaseKeyPrefix := fmt.Sprintf("%s/%s/", leasePrefix, repoKey)

	resp, err := cm.client.Get(ctx, leaseKeyPrefix, clientv3.WithPrefix(), clientv3.WithCountOnly())
	if err != nil {
		return 0, fmt.Errorf("failed to get current slots: %w", err)
	}

	return int(resp.Count), nil
}

// GetRunningPipelineRuns returns the list of currently running PipelineRuns for a repository.
func (cm *ConcurrencyManager) GetRunningPipelineRuns(ctx context.Context, repo *v1alpha1.Repository) ([]string, error) {
	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)
	leaseKeyPrefix := fmt.Sprintf("%s/%s/", leasePrefix, repoKey)

	resp, err := cm.client.Get(ctx, leaseKeyPrefix, clientv3.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("failed to get running pipeline runs: %w", err)
	}

	pipelineRuns := []string{}
	if resp.Count > 0 {
		pipelineRuns = make([]string, 0, resp.Count)
	}
	for _, kv := range resp.Kvs {
		pipelineRuns = append(pipelineRuns, string(kv.Value))
	}

	return pipelineRuns, nil
}

// WatchSlotAvailability watches for slot availability changes in a repository.
func (cm *ConcurrencyManager) WatchSlotAvailability(ctx context.Context, repo *v1alpha1.Repository, callback func()) {
	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)
	leaseKeyPrefix := fmt.Sprintf("%s/%s/", leasePrefix, repoKey)

	watchCh := cm.client.Watch(ctx, leaseKeyPrefix, clientv3.WithPrefix())

	go func() {
		for watchResp := range watchCh {
			for _, event := range watchResp.Events {
				if event.Type == clientv3.EventTypeDelete {
					// A slot was released
					cm.logger.Debugf("slot released in repository %s, triggering callback", repoKey)
					callback()
				}
			}
		}
	}()
}

// Repository state tracking using etcd.

// SetRepositoryState sets the overall state for a repository's concurrency.
func (cm *ConcurrencyManager) SetRepositoryState(ctx context.Context, repo *v1alpha1.Repository, state string) error {
	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)
	stateKey := fmt.Sprintf("%s/%s/state", concurrencyPrefix, repoKey)

	_, err := cm.client.Put(ctx, stateKey, state)
	if err != nil {
		return fmt.Errorf("failed to set repository state: %w", err)
	}

	return nil
}

// GetRepositoryState gets the current state for a repository's concurrency.
func (cm *ConcurrencyManager) GetRepositoryState(ctx context.Context, repo *v1alpha1.Repository) (string, error) {
	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)
	stateKey := fmt.Sprintf("%s/%s/state", concurrencyPrefix, repoKey)

	resp, err := cm.client.Get(ctx, stateKey)
	if err != nil {
		return "", fmt.Errorf("failed to get repository state: %w", err)
	}

	if len(resp.Kvs) == 0 {
		return "", nil
	}

	return string(resp.Kvs[0].Value), nil
}

// SetPipelineRunState sets the concurrency state for a specific PipelineRun.
func (cm *ConcurrencyManager) SetPipelineRunState(ctx context.Context, pipelineRunKey, state string) error {
	stateKey := fmt.Sprintf("%s/pr/%s/state", concurrencyPrefix, pipelineRunKey)

	_, err := cm.client.Put(ctx, stateKey, state)
	if err != nil {
		return fmt.Errorf("failed to set pipeline run state: %w", err)
	}

	return nil
}

// GetPipelineRunState gets the concurrency state for a specific PipelineRun.
func (cm *ConcurrencyManager) GetPipelineRunState(ctx context.Context, pipelineRunKey string) (string, error) {
	stateKey := fmt.Sprintf("%s/pr/%s/state", concurrencyPrefix, pipelineRunKey)

	resp, err := cm.client.Get(ctx, stateKey)
	if err != nil {
		return "", fmt.Errorf("failed to get pipeline run state: %w", err)
	}

	if len(resp.Kvs) == 0 {
		return "", nil
	}

	return string(resp.Kvs[0].Value), nil
}

// GetPipelineRunLease gets the lease ID for a specific PipelineRun.
func (cm *ConcurrencyManager) GetPipelineRunLease(ctx context.Context, repo *v1alpha1.Repository, pipelineRunKey string) (clientv3.LeaseID, error) {
	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)
	leaseKey := fmt.Sprintf("%s/%s/%s", leasePrefix, repoKey, pipelineRunKey)

	// Get lease info from a separate key
	leaseInfoKey := fmt.Sprintf("%s/info", leaseKey)
	resp, err := cm.client.Get(ctx, leaseInfoKey)
	if err != nil {
		return 0, fmt.Errorf("failed to get pipeline run lease: %w", err)
	}

	if len(resp.Kvs) == 0 {
		return 0, nil
	}

	leaseIDStr := string(resp.Kvs[0].Value)
	leaseID, err := strconv.ParseInt(leaseIDStr, 16, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse lease ID: %w", err)
	}

	return clientv3.LeaseID(leaseID), nil
}

// SetPipelineRunLease stores the lease ID for a specific PipelineRun.
func (cm *ConcurrencyManager) SetPipelineRunLease(ctx context.Context, repo *v1alpha1.Repository, pipelineRunKey string, leaseID clientv3.LeaseID) error {
	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)
	leaseKey := fmt.Sprintf("%s/%s/%s", leasePrefix, repoKey, pipelineRunKey)

	// Store lease info in a separate key
	leaseInfoKey := fmt.Sprintf("%s/info", leaseKey)
	leaseIDStr := fmt.Sprintf("%x", leaseID)

	_, err := cm.client.Put(ctx, leaseInfoKey, leaseIDStr)
	if err != nil {
		return fmt.Errorf("failed to set pipeline run lease: %w", err)
	}

	return nil
}

// CleanupRepository removes all etcd state for a repository.
func (cm *ConcurrencyManager) CleanupRepository(ctx context.Context, repo *v1alpha1.Repository) error {
	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)

	// Clean up lease keys
	leaseKeyPrefix := fmt.Sprintf("%s/%s/", leasePrefix, repoKey)
	_, err := cm.client.Delete(ctx, leaseKeyPrefix, clientv3.WithPrefix())
	if err != nil {
		cm.logger.Errorf("failed to cleanup lease keys for repository %s: %v", repoKey, err)
	}

	// Clean up concurrency state keys
	stateKeyPrefix := fmt.Sprintf("%s/%s/", concurrencyPrefix, repoKey)
	_, err = cm.client.Delete(ctx, stateKeyPrefix, clientv3.WithPrefix())
	if err != nil {
		cm.logger.Errorf("failed to cleanup state keys for repository %s: %v", repoKey, err)
	}

	cm.logger.Infof("cleaned up etcd state for repository %s", repoKey)
	return nil
}

// Helper functions.

// PipelineRunKey creates a consistent key for a PipelineRun.
func PipelineRunKey(namespace, name string) string {
	return fmt.Sprintf("%s/%s", namespace, name)
}

// ParsePipelineRunKey parses a PipelineRun key into namespace and name.
func ParsePipelineRunKey(key string) (namespace, name string, err error) {
	parts := strings.Split(key, "/")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid pipeline run key format: %s", key)
	}
	return parts[0], parts[1], nil
}

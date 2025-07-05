package concurrency

import (
	"context"
	"fmt"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/etcd"
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

// EtcdDriver implements ConcurrencyDriver using etcd
type EtcdDriver struct {
	client etcd.Client
	logger *zap.SugaredLogger
}

// NewEtcdDriver creates a new etcd-based concurrency driver
func NewEtcdDriver(config *EtcdConfig, logger *zap.SugaredLogger) (ConcurrencyDriver, error) {
	etcdConfig := &etcd.Config{
		Endpoints:   config.Endpoints,
		DialTimeout: config.DialTimeout,
		Username:    config.Username,
		Password:    config.Password,
		TLSConfig:   convertTLSConfig(config.TLSConfig),
		Enabled:     true,
		Mode:        config.Mode,
	}

	client, err := etcd.NewClient(etcdConfig, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create etcd client: %w", err)
	}

	return &EtcdDriver{
		client: client,
		logger: logger,
	}, nil
}

// convertTLSConfig converts our TLSConfig to etcd.TLSConfig
func convertTLSConfig(tls *TLSConfig) *etcd.TLSConfig {
	if tls == nil {
		return nil
	}
	return &etcd.TLSConfig{
		CertFile:   tls.CertFile,
		KeyFile:    tls.KeyFile,
		CAFile:     tls.CAFile,
		ServerName: tls.ServerName,
	}
}

// AcquireSlot tries to acquire a concurrency slot for a PipelineRun in a repository.
// Returns true if slot was acquired, false if concurrency limit reached.
func (ed *EtcdDriver) AcquireSlot(ctx context.Context, repo *v1alpha1.Repository, pipelineRunKey string) (bool, LeaseID, error) {
	if repo.Spec.ConcurrencyLimit == nil || *repo.Spec.ConcurrencyLimit == 0 {
		// No concurrency limit, always allow
		return true, 0, nil
	}

	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)
	leaseKeyPrefix := fmt.Sprintf("%s/%s/", leasePrefix, repoKey)
	limit := *repo.Spec.ConcurrencyLimit

	// Create a lease first
	leaseResp, err := ed.client.Grant(ctx, defaultLeaseTTL)
	if err != nil {
		return false, 0, fmt.Errorf("failed to create lease: %w", err)
	}

	leaseID := leaseResp.ID
	leaseKey := fmt.Sprintf("%s%s", leaseKeyPrefix, pipelineRunKey)

	// Atomic transaction: check current lease count and acquire if under limit
	txn := ed.client.Txn(ctx)

	// Get all current leases for this repository
	getResp, err := ed.client.Get(ctx, leaseKeyPrefix, clientv3.WithPrefix(), clientv3.WithCountOnly())
	if err != nil {
		_, revokeErr := ed.client.Revoke(ctx, leaseID)
		if revokeErr != nil {
			ed.logger.Errorf("failed to revoke lease %x after get error: %v", leaseID, revokeErr)
		}
		return false, 0, fmt.Errorf("failed to get current leases: %w", err)
	}

	currentCount := getResp.Count

	if currentCount >= int64(limit) {
		// Concurrency limit reached, revoke lease and return false
		_, revokeErr := ed.client.Revoke(ctx, leaseID)
		if revokeErr != nil {
			ed.logger.Errorf("failed to revoke lease %x after concurrency limit: %v", leaseID, revokeErr)
		}
		ed.logger.Infof("concurrency limit reached for repository %s: %d/%d", repoKey, currentCount, limit)
		return false, 0, nil
	}

	// Try to acquire slot by putting our lease
	resp, err := txn.
		If(clientv3.Compare(clientv3.CreateRevision(leaseKey), "=", 0)). // Key doesn't exist
		Then(clientv3.OpPut(leaseKey, pipelineRunKey, clientv3.WithLease(leaseID))).
		Commit()
	if err != nil {
		_, revokeErr := ed.client.Revoke(ctx, leaseID)
		if revokeErr != nil {
			ed.logger.Errorf("failed to revoke lease %x after txn error: %v", leaseID, revokeErr)
		}
		return false, 0, fmt.Errorf("failed to acquire slot: %w", err)
	}

	if !resp.Succeeded {
		// Key already exists (race condition), revoke lease
		_, revokeErr := ed.client.Revoke(ctx, leaseID)
		if revokeErr != nil {
			ed.logger.Errorf("failed to revoke lease %x after race: %v", leaseID, revokeErr)
		}
		return false, 0, nil
	}

	// Keep lease alive
	keepAliveCh, err := ed.client.KeepAlive(ctx, leaseID)
	if err != nil {
		_, revokeErr := ed.client.Revoke(ctx, leaseID)
		if revokeErr != nil {
			ed.logger.Errorf("failed to revoke lease %x after keepalive error: %v", leaseID, revokeErr)
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

	ed.logger.Infof("acquired concurrency slot for %s in repository %s (lease: %x)", pipelineRunKey, repoKey, leaseID)
	return true, leaseID, nil
}

// ReleaseSlot releases a concurrency slot by revoking the lease.
func (ed *EtcdDriver) ReleaseSlot(ctx context.Context, leaseID LeaseID, pipelineRunKey, repoKey string) error {
	if leaseID == nil || leaseID == 0 {
		// No lease to revoke
		return nil
	}

	// Convert LeaseID to clientv3.LeaseID
	var etcdLeaseID clientv3.LeaseID
	switch id := leaseID.(type) {
	case clientv3.LeaseID:
		etcdLeaseID = id
	case int64:
		etcdLeaseID = clientv3.LeaseID(id)
	default:
		return fmt.Errorf("invalid lease ID type: %T", leaseID)
	}

	_, err := ed.client.Revoke(ctx, etcdLeaseID)
	if err != nil {
		ed.logger.Errorf("failed to revoke lease %x for %s: %v", etcdLeaseID, pipelineRunKey, err)
		return err
	}

	ed.logger.Infof("released concurrency slot for %s in repository %s (lease: %x)", pipelineRunKey, repoKey, etcdLeaseID)
	return nil
}

// GetCurrentSlots returns the current number of slots in use for a repository.
func (ed *EtcdDriver) GetCurrentSlots(ctx context.Context, repo *v1alpha1.Repository) (int, error) {
	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)
	leaseKeyPrefix := fmt.Sprintf("%s/%s/", leasePrefix, repoKey)

	resp, err := ed.client.Get(ctx, leaseKeyPrefix, clientv3.WithPrefix(), clientv3.WithCountOnly())
	if err != nil {
		return 0, fmt.Errorf("failed to get current slots: %w", err)
	}

	return int(resp.Count), nil
}

// GetRunningPipelineRuns returns the list of currently running PipelineRuns for a repository.
func (ed *EtcdDriver) GetRunningPipelineRuns(ctx context.Context, repo *v1alpha1.Repository) ([]string, error) {
	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)
	leaseKeyPrefix := fmt.Sprintf("%s/%s/", leasePrefix, repoKey)

	resp, err := ed.client.Get(ctx, leaseKeyPrefix, clientv3.WithPrefix())
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
func (ed *EtcdDriver) WatchSlotAvailability(ctx context.Context, repo *v1alpha1.Repository, callback func()) {
	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)
	leaseKeyPrefix := fmt.Sprintf("%s/%s/", leasePrefix, repoKey)

	watchCh := ed.client.Watch(ctx, leaseKeyPrefix, clientv3.WithPrefix())

	go func() {
		for watchResp := range watchCh {
			for _, event := range watchResp.Events {
				if event.Type == clientv3.EventTypeDelete {
					// A slot was released
					ed.logger.Debugf("slot released in repository %s, triggering callback", repoKey)
					callback()
				}
			}
		}
	}()
}

// SetRepositoryState sets the overall state for a repository's concurrency.
func (ed *EtcdDriver) SetRepositoryState(ctx context.Context, repo *v1alpha1.Repository, state string) error {
	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)
	stateKey := fmt.Sprintf("%s/%s/state", concurrencyPrefix, repoKey)

	_, err := ed.client.Put(ctx, stateKey, state)
	if err != nil {
		return fmt.Errorf("failed to set repository state: %w", err)
	}

	ed.logger.Debugf("set repository state for %s: %s", repoKey, state)
	return nil
}

// GetRepositoryState gets the overall state for a repository's concurrency.
func (ed *EtcdDriver) GetRepositoryState(ctx context.Context, repo *v1alpha1.Repository) (string, error) {
	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)
	stateKey := fmt.Sprintf("%s/%s/state", concurrencyPrefix, repoKey)

	resp, err := ed.client.Get(ctx, stateKey)
	if err != nil {
		return "", fmt.Errorf("failed to get repository state: %w", err)
	}

	if resp.Count == 0 {
		return "", nil
	}

	return string(resp.Kvs[0].Value), nil
}

// SetPipelineRunState sets the state for a specific PipelineRun.
func (ed *EtcdDriver) SetPipelineRunState(ctx context.Context, pipelineRunKey, state string) error {
	stateKey := fmt.Sprintf("%s/%s/state", concurrencyPrefix, pipelineRunKey)

	_, err := ed.client.Put(ctx, stateKey, state)
	if err != nil {
		return fmt.Errorf("failed to set pipeline run state: %w", err)
	}

	ed.logger.Debugf("set pipeline run state for %s: %s", pipelineRunKey, state)
	return nil
}

// GetPipelineRunState gets the state for a specific PipelineRun.
func (ed *EtcdDriver) GetPipelineRunState(ctx context.Context, pipelineRunKey string) (string, error) {
	stateKey := fmt.Sprintf("%s/%s/state", concurrencyPrefix, pipelineRunKey)

	resp, err := ed.client.Get(ctx, stateKey)
	if err != nil {
		return "", fmt.Errorf("failed to get pipeline run state: %w", err)
	}

	if resp.Count == 0 {
		return "", nil
	}

	return string(resp.Kvs[0].Value), nil
}

// CleanupRepository cleans up all etcd state for a repository.
func (ed *EtcdDriver) CleanupRepository(ctx context.Context, repo *v1alpha1.Repository) error {
	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)
	leaseKeyPrefix := fmt.Sprintf("%s/%s/", leasePrefix, repoKey)
	concurrencyKeyPrefix := fmt.Sprintf("%s/%s/", concurrencyPrefix, repoKey)

	// Delete all lease keys for this repository
	_, err := ed.client.Delete(ctx, leaseKeyPrefix, clientv3.WithPrefix())
	if err != nil {
		ed.logger.Errorf("failed to delete lease keys for repository %s: %v", repoKey, err)
	}

	// Delete all concurrency state keys for this repository
	_, err = ed.client.Delete(ctx, concurrencyKeyPrefix, clientv3.WithPrefix())
	if err != nil {
		ed.logger.Errorf("failed to delete concurrency keys for repository %s: %v", repoKey, err)
	}

	ed.logger.Infof("cleaned up etcd state for repository %s", repoKey)
	return nil
}

// Close closes the etcd client.
func (ed *EtcdDriver) Close() error {
	return ed.client.Close()
}

// Helper functions for pipeline run key management
func PipelineRunKey(namespace, name string) string {
	return fmt.Sprintf("%s/%s", namespace, name)
}

func ParsePipelineRunKey(key string) (namespace, name string, err error) {
	parts := strings.Split(key, "/")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid pipeline run key format: %s", key)
	}
	return parts[0], parts[1], nil
}

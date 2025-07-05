package concurrency

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"go.uber.org/zap"
)

// MemoryDriver implements ConcurrencyDriver using in-memory storage
type MemoryDriver struct {
	mu               sync.RWMutex
	slots            map[string]map[string]*slotInfo // repoKey -> pipelineRunKey -> slotInfo
	repositoryStates map[string]string               // repoKey -> state
	pipelineStates   map[string]string               // pipelineRunKey -> state
	nextSlotID       int
	config           *MemoryConfig
	logger           *zap.SugaredLogger
}

type slotInfo struct {
	id         int
	repoKey    string
	prKey      string
	state      string
	acquiredAt time.Time
	expiresAt  time.Time
}

// NewMemoryDriver creates a new in-memory concurrency driver
func NewMemoryDriver(config *MemoryConfig, logger *zap.SugaredLogger) (ConcurrencyDriver, error) {
	if config == nil {
		config = &MemoryConfig{
			LeaseTTL: 1 * time.Hour,
		}
	}

	driver := &MemoryDriver{
		slots:            make(map[string]map[string]*slotInfo),
		repositoryStates: make(map[string]string),
		pipelineStates:   make(map[string]string),
		nextSlotID:       1,
		config:           config,
		logger:           logger,
	}

	// Start cleanup goroutine for expired leases
	go driver.cleanupExpiredLeases()

	return driver, nil
}

// cleanupExpiredLeases periodically removes expired concurrency slots
func (md *MemoryDriver) cleanupExpiredLeases() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		md.mu.Lock()
		now := time.Now()
		cleaned := 0

		for repoKey, repoSlots := range md.slots {
			for prKey, slot := range repoSlots {
				if slot.expiresAt.Before(now) {
					delete(repoSlots, prKey)
					cleaned++
				}
			}
			// Remove empty repository entries
			if len(repoSlots) == 0 {
				delete(md.slots, repoKey)
			}
		}

		md.mu.Unlock()

		if cleaned > 0 {
			md.logger.Debugf("cleaned up %d expired concurrency slots", cleaned)
		}
	}
}

// AcquireSlot tries to acquire a concurrency slot for a PipelineRun in a repository.
func (md *MemoryDriver) AcquireSlot(ctx context.Context, repo *v1alpha1.Repository, pipelineRunKey string) (bool, LeaseID, error) {
	if repo.Spec.ConcurrencyLimit == nil || *repo.Spec.ConcurrencyLimit == 0 {
		// No concurrency limit, always allow
		return true, 0, nil
	}

	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)
	limit := *repo.Spec.ConcurrencyLimit

	md.mu.Lock()
	defer md.mu.Unlock()

	// Initialize repository slots if not exists
	if md.slots[repoKey] == nil {
		md.slots[repoKey] = make(map[string]*slotInfo)
	}

	// Check if slot already exists
	if existingSlot, exists := md.slots[repoKey][pipelineRunKey]; exists {
		if existingSlot.expiresAt.After(time.Now()) {
			// Slot already exists and is still valid
			return false, 0, nil
		}
		// Slot exists but is expired, remove it
		delete(md.slots[repoKey], pipelineRunKey)
	}

	// Count current running slots
	currentCount := 0
	for _, slot := range md.slots[repoKey] {
		if slot.state == "running" && slot.expiresAt.After(time.Now()) {
			currentCount++
		}
	}

	if currentCount >= limit {
		md.logger.Infof("concurrency limit reached for repository %s: %d/%d", repoKey, currentCount, limit)
		return false, 0, nil
	}

	// Create new slot
	slotID := md.nextSlotID
	md.nextSlotID++

	slot := &slotInfo{
		id:         slotID,
		repoKey:    repoKey,
		prKey:      pipelineRunKey,
		state:      "running",
		acquiredAt: time.Now(),
		expiresAt:  time.Now().Add(md.config.LeaseTTL),
	}

	md.slots[repoKey][pipelineRunKey] = slot

	md.logger.Infof("acquired concurrency slot for %s in repository %s (slot ID: %d)", pipelineRunKey, repoKey, slotID)
	return true, slotID, nil
}

// ReleaseSlot releases a concurrency slot.
func (md *MemoryDriver) ReleaseSlot(ctx context.Context, leaseID LeaseID, pipelineRunKey, repoKey string) error {
	if leaseID == nil || leaseID == 0 {
		// No lease to release
		return nil
	}

	md.mu.Lock()
	defer md.mu.Unlock()

	// Convert LeaseID to int
	var slotID int
	switch id := leaseID.(type) {
	case int:
		slotID = id
	case int64:
		slotID = int(id)
	default:
		return fmt.Errorf("invalid lease ID type: %T", leaseID)
	}

	// Find and remove the slot
	if repoSlots, exists := md.slots[repoKey]; exists {
		if slot, exists := repoSlots[pipelineRunKey]; exists && slot.id == slotID {
			delete(repoSlots, pipelineRunKey)

			// Remove empty repository entries
			if len(repoSlots) == 0 {
				delete(md.slots, repoKey)
			}

			md.logger.Infof("released concurrency slot for %s in repository %s (slot ID: %d)", pipelineRunKey, repoKey, slotID)
			return nil
		}
	}

	md.logger.Warnf("slot not found for release: ID=%d, repo=%s, pipeline=%s", slotID, repoKey, pipelineRunKey)
	return nil
}

// GetCurrentSlots returns the current number of slots in use for a repository.
func (md *MemoryDriver) GetCurrentSlots(ctx context.Context, repo *v1alpha1.Repository) (int, error) {
	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)

	md.mu.RLock()
	defer md.mu.RUnlock()

	if repoSlots, exists := md.slots[repoKey]; exists {
		count := 0
		now := time.Now()
		for _, slot := range repoSlots {
			if slot.state == "running" && slot.expiresAt.After(now) {
				count++
			}
		}
		return count, nil
	}

	return 0, nil
}

// GetRunningPipelineRuns returns the list of currently running PipelineRuns for a repository.
func (md *MemoryDriver) GetRunningPipelineRuns(ctx context.Context, repo *v1alpha1.Repository) ([]string, error) {
	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)

	md.mu.RLock()
	defer md.mu.RUnlock()

	if repoSlots, exists := md.slots[repoKey]; exists {
		var pipelineRuns []string
		now := time.Now()
		for _, slot := range repoSlots {
			if slot.state == "running" && slot.expiresAt.After(now) {
				pipelineRuns = append(pipelineRuns, slot.prKey)
			}
		}
		return pipelineRuns, nil
	}

	return []string{}, nil
}

// WatchSlotAvailability watches for slot availability changes in a repository.
// For memory driver, this is a simplified polling implementation.
func (md *MemoryDriver) WatchSlotAvailability(ctx context.Context, repo *v1alpha1.Repository, callback func()) {
	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)

	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		lastCount := -1
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				count, err := md.GetCurrentSlots(ctx, repo)
				if err != nil {
					md.logger.Errorf("failed to get current slots for watching: %v", err)
					continue
				}

				if lastCount != -1 && count < lastCount {
					// A slot was released
					md.logger.Debugf("slot released in repository %s, triggering callback", repoKey)
					callback()
				}
				lastCount = count
			}
		}
	}()
}

// SetRepositoryState sets the overall state for a repository's concurrency.
func (md *MemoryDriver) SetRepositoryState(ctx context.Context, repo *v1alpha1.Repository, state string) error {
	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)

	md.mu.Lock()
	defer md.mu.Unlock()

	md.repositoryStates[repoKey] = state
	md.logger.Debugf("set repository state for %s: %s", repoKey, state)
	return nil
}

// GetRepositoryState gets the overall state for a repository's concurrency.
func (md *MemoryDriver) GetRepositoryState(ctx context.Context, repo *v1alpha1.Repository) (string, error) {
	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)

	md.mu.RLock()
	defer md.mu.RUnlock()

	state, exists := md.repositoryStates[repoKey]
	if !exists {
		return "", nil
	}

	return state, nil
}

// SetPipelineRunState sets the state for a specific PipelineRun.
func (md *MemoryDriver) SetPipelineRunState(ctx context.Context, pipelineRunKey, state string) error {
	md.mu.Lock()
	defer md.mu.Unlock()

	md.pipelineStates[pipelineRunKey] = state
	md.logger.Debugf("set pipeline run state for %s: %s", pipelineRunKey, state)
	return nil
}

// GetPipelineRunState gets the state for a specific PipelineRun.
func (md *MemoryDriver) GetPipelineRunState(ctx context.Context, pipelineRunKey string) (string, error) {
	md.mu.RLock()
	defer md.mu.RUnlock()

	state, exists := md.pipelineStates[pipelineRunKey]
	if !exists {
		return "", nil
	}

	return state, nil
}

// CleanupRepository cleans up all memory state for a repository.
func (md *MemoryDriver) CleanupRepository(ctx context.Context, repo *v1alpha1.Repository) error {
	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)

	md.mu.Lock()
	defer md.mu.Unlock()

	// Remove all slots for this repository
	delete(md.slots, repoKey)

	// Remove repository state
	delete(md.repositoryStates, repoKey)

	md.logger.Infof("cleaned up memory state for repository %s", repoKey)
	return nil
}

// Close closes the memory driver.
func (md *MemoryDriver) Close() error {
	// Nothing to close for memory driver
	return nil
}

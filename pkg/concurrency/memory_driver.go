package concurrency

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"go.uber.org/zap"
)

// MemoryDriver implements Driver using in-memory storage.
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

// NewMemoryDriver creates a new in-memory concurrency driver.
func NewMemoryDriver(config *MemoryConfig, logger *zap.SugaredLogger) (Driver, error) {
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

// cleanupExpiredLeases periodically removes expired concurrency slots.
func (md *MemoryDriver) cleanupExpiredLeases() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		md.mu.Lock()
		now := time.Now()
		cleaned := 0
		var expiredPipelineKeys []string

		for repoKey, repoSlots := range md.slots {
			for prKey, slot := range repoSlots {
				if slot.expiresAt.Before(now) {
					expiredPipelineKeys = append(expiredPipelineKeys, prKey)
					delete(repoSlots, prKey)
					cleaned++
				}
			}
			// Remove empty repository entries
			if len(repoSlots) == 0 {
				delete(md.slots, repoKey)
			}
		}

		// Clean up pipeline states for expired slots
		for _, prKey := range expiredPipelineKeys {
			delete(md.pipelineStates, prKey)
		}

		md.mu.Unlock()

		if cleaned > 0 {
			md.logger.Debugf("cleaned up %d expired concurrency slots and %d pipeline states", cleaned, len(expiredPipelineKeys))
		}
	}
}

// AcquireSlot tries to acquire a concurrency slot for a PipelineRun in a repository.
// Returns true if slot was acquired, false if concurrency limit reached.
func (md *MemoryDriver) AcquireSlot(_ context.Context, repo *v1alpha1.Repository, pipelineRunKey string) (bool, LeaseID, error) {
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

	now := time.Now()

	// Check if slot already exists
	if existingSlot, exists := md.slots[repoKey][pipelineRunKey]; exists {
		if existingSlot.expiresAt.After(now) {
			// Slot already exists and is still valid
			if existingSlot.state == "running" {
				// Already running, return success
				return true, existingSlot.id, nil
			}
			// Slot exists but is not running (e.g., queued), check if we can promote it
			if existingSlot.state == "queued" {
				// Count current running slots in single pass
				currentRunning := md.countRunningSlots(repoKey, now)

				if currentRunning >= limit {
					md.logger.Infof("concurrency limit reached for repository %s: %d/%d", repoKey, currentRunning, limit)
					return false, 0, nil
				}

				// Promote queued slot to running
				existingSlot.state = "running"
				existingSlot.acquiredAt = now
				existingSlot.expiresAt = now.Add(md.config.LeaseTTL)

				md.logger.Infof("promoted queued slot to running for %s in repository %s (slot ID: %d)", pipelineRunKey, repoKey, existingSlot.id)
				return true, existingSlot.id, nil
			}
		}
		// Slot exists but is expired, remove it
		delete(md.slots[repoKey], pipelineRunKey)
	}

	// Count current running slots in single pass
	currentRunning := md.countRunningSlots(repoKey, now)

	if currentRunning >= limit {
		md.logger.Infof("concurrency limit reached for repository %s: %d/%d", repoKey, currentRunning, limit)
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
		acquiredAt: now,
		expiresAt:  now.Add(md.config.LeaseTTL),
	}

	md.slots[repoKey][pipelineRunKey] = slot

	md.logger.Infof("acquired concurrency slot for %s in repository %s (slot ID: %d)", pipelineRunKey, repoKey, slotID)
	return true, slotID, nil
}

// countRunningSlots counts running slots for a repository at a given time
// Must be called with md.mu held.
func (md *MemoryDriver) countRunningSlots(repoKey string, now time.Time) int {
	count := 0
	if repoSlots, exists := md.slots[repoKey]; exists {
		for _, slot := range repoSlots {
			if slot.state == "running" && slot.expiresAt.After(now) {
				count++
			}
		}
	}
	return count
}

// ReleaseSlot releases a concurrency slot by removing the lease.
func (md *MemoryDriver) ReleaseSlot(_ context.Context, leaseID LeaseID, pipelineRunKey, repoKey string) error {
	md.mu.Lock()
	defer md.mu.Unlock()

	// Helper function to release slot by pipeline run key
	releaseByKey := func() {
		if repoSlots, exists := md.slots[repoKey]; exists {
			if slot, exists := repoSlots[pipelineRunKey]; exists {
				slotID := slot.id
				delete(repoSlots, pipelineRunKey)

				// Remove empty repository entries
				if len(repoSlots) == 0 {
					delete(md.slots, repoKey)
				}

				md.logger.Infof("released concurrency slot for %s in repository %s (slot ID: %d)", pipelineRunKey, repoKey, slotID)
				return
			}
		}
		md.logger.Warnf("slot not found for release by pipeline key: repo=%s, pipeline=%s", repoKey, pipelineRunKey)
	}

	// If leaseID is nil, 0, or empty, find the slot by pipeline run key
	if leaseID == nil {
		releaseByKey()
		return nil
	}

	// Convert LeaseID to int for comparison
	var slotID int
	switch id := leaseID.(type) {
	case int:
		slotID = id
	case int64:
		slotID = int(id)
	default:
		return fmt.Errorf("invalid lease ID type: %T", leaseID)
	}

	// If lease ID is 0, use key-based lookup
	if slotID == 0 {
		releaseByKey()
		return nil
	}

	// Find and remove the slot by lease ID
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
func (md *MemoryDriver) GetCurrentSlots(_ context.Context, repo *v1alpha1.Repository) (int, error) {
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
func (md *MemoryDriver) GetRunningPipelineRuns(_ context.Context, repo *v1alpha1.Repository) ([]string, error) {
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

// GetQueuedPipelineRuns returns the list of currently queued PipelineRuns for a repository.
func (md *MemoryDriver) GetQueuedPipelineRuns(_ context.Context, repo *v1alpha1.Repository) ([]string, error) {
	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)

	md.mu.RLock()
	defer md.mu.RUnlock()

	if repoSlots, exists := md.slots[repoKey]; exists {
		var pipelineRuns []string
		now := time.Now()
		for _, slot := range repoSlots {
			if slot.state == "queued" && slot.expiresAt.After(now) {
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

// SetRepositoryState sets the state of a repository.
func (md *MemoryDriver) SetRepositoryState(_ context.Context, repo *v1alpha1.Repository, state string) error {
	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)

	md.mu.Lock()
	defer md.mu.Unlock()

	md.repositoryStates[repoKey] = state
	md.logger.Debugf("set repository state for %s: %s", repoKey, state)
	return nil
}

// GetRepositoryState gets the state of a repository.
func (md *MemoryDriver) GetRepositoryState(_ context.Context, repo *v1alpha1.Repository) (string, error) {
	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)

	md.mu.RLock()
	defer md.mu.RUnlock()

	state, exists := md.repositoryStates[repoKey]
	if !exists {
		return "", nil
	}

	return state, nil
}

// SetPipelineRunState sets the state of a PipelineRun.
func (md *MemoryDriver) SetPipelineRunState(_ context.Context, pipelineRunKey, state string, repo *v1alpha1.Repository) error {
	md.mu.Lock()
	defer md.mu.Unlock()

	// Always update the pipeline state
	md.pipelineStates[pipelineRunKey] = state

	// Handle slot creation/update for queued state
	if state == "queued" && repo != nil {
		repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)

		// Initialize the repository slots map if it doesn't exist
		if md.slots[repoKey] == nil {
			md.slots[repoKey] = make(map[string]*slotInfo)
		}

		// Check if slot already exists
		if existingSlot, exists := md.slots[repoKey][pipelineRunKey]; exists {
			// Update existing slot state instead of creating new one
			existingSlot.state = "queued"
			existingSlot.expiresAt = time.Now().Add(md.config.LeaseTTL)
			md.logger.Debugf("updated existing slot state to queued for %s", pipelineRunKey)
		} else {
			// Create a new queued slot only if it doesn't exist
			slotID := md.nextSlotID
			md.nextSlotID++

			slot := &slotInfo{
				id:         slotID,
				repoKey:    repoKey,
				prKey:      pipelineRunKey,
				state:      "queued",
				acquiredAt: time.Now(),
				expiresAt:  time.Now().Add(md.config.LeaseTTL),
			}
			md.slots[repoKey][pipelineRunKey] = slot
			md.logger.Debugf("created new queued slot for %s (slot ID: %d)", pipelineRunKey, slotID)
		}
	}

	// Handle slot cleanup for completed/failed states
	if (state == "completed" || state == "failed" || state == "cancelled") && repo != nil {
		repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)
		if repoSlots, exists := md.slots[repoKey]; exists {
			if slot, exists := repoSlots[pipelineRunKey]; exists {
				delete(repoSlots, pipelineRunKey)
				md.logger.Debugf("cleaned up slot for completed/failed pipeline %s (slot ID: %d)", pipelineRunKey, slot.id)

				// Remove empty repository entries
				if len(repoSlots) == 0 {
					delete(md.slots, repoKey)
				}
			}
		}
	}

	md.logger.Debugf("set pipeline run state for %s: %s", pipelineRunKey, state)
	return nil
}

// GetPipelineRunState gets the state of a PipelineRun.
func (md *MemoryDriver) GetPipelineRunState(_ context.Context, pipelineRunKey string) (string, error) {
	md.mu.RLock()
	defer md.mu.RUnlock()

	state, exists := md.pipelineStates[pipelineRunKey]
	if !exists {
		return "", nil
	}

	return state, nil
}

// CleanupRepository removes all data for a repository.
func (md *MemoryDriver) CleanupRepository(_ context.Context, repo *v1alpha1.Repository) error {
	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)

	md.mu.Lock()
	defer md.mu.Unlock()

	// Collect pipeline keys that belong to this repository before cleanup
	var pipelineKeysToClean []string
	if repoSlots, exists := md.slots[repoKey]; exists {
		for prKey := range repoSlots {
			pipelineKeysToClean = append(pipelineKeysToClean, prKey)
		}
	}

	// Remove all slots for this repository
	delete(md.slots, repoKey)

	// Remove repository state
	delete(md.repositoryStates, repoKey)

	// Clean up individual pipeline states for this repository
	for _, prKey := range pipelineKeysToClean {
		delete(md.pipelineStates, prKey)
	}

	md.logger.Infof("cleaned up memory state for repository %s (removed %d pipeline states)", repoKey, len(pipelineKeysToClean))
	return nil
}

// Close closes the memory driver.
func (md *MemoryDriver) Close() error {
	// Nothing to close for memory driver
	return nil
}

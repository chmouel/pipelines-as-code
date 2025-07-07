package concurrency

import (
	"context"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
)

// LeaseID represents a unique identifier for a concurrency slot lease.
type LeaseID interface{}

// Driver defines the interface for concurrency control implementations.
type Driver interface {
	// AcquireSlot tries to acquire a concurrency slot for a PipelineRun
	// Returns (success, leaseID, error)
	AcquireSlot(ctx context.Context, repo *v1alpha1.Repository, pipelineRunKey string) (bool, LeaseID, error)

	// ReleaseSlot releases a concurrency slot by revoking the lease
	ReleaseSlot(ctx context.Context, leaseID LeaseID, pipelineRunKey, repoKey string) error

	// GetCurrentSlots returns the current number of slots in use for a repository
	GetCurrentSlots(ctx context.Context, repo *v1alpha1.Repository) (int, error)

	// GetRunningPipelineRuns returns the list of currently running PipelineRuns for a repository
	GetRunningPipelineRuns(ctx context.Context, repo *v1alpha1.Repository) ([]string, error)

	// GetQueuedPipelineRuns returns the list of currently queued PipelineRuns for a repository
	GetQueuedPipelineRuns(ctx context.Context, repo *v1alpha1.Repository) ([]string, error)

	// WatchSlotAvailability watches for slot availability changes in a repository
	WatchSlotAvailability(ctx context.Context, repo *v1alpha1.Repository, callback func())

	// SetRepositoryState sets the overall state for a repository's concurrency
	SetRepositoryState(ctx context.Context, repo *v1alpha1.Repository, state string) error

	// GetRepositoryState gets the overall state for a repository's concurrency
	GetRepositoryState(ctx context.Context, repo *v1alpha1.Repository) (string, error)

	// SetPipelineRunState sets the state for a specific PipelineRun
	SetPipelineRunState(ctx context.Context, pipelineRunKey, state string, repo *v1alpha1.Repository) error

	// GetPipelineRunState gets the state for a specific PipelineRun
	GetPipelineRunState(ctx context.Context, pipelineRunKey string) (string, error)

	// CleanupRepository removes all data for a repository
	CleanupRepository(ctx context.Context, repo *v1alpha1.Repository) error

	// Close closes the driver and releases resources
	Close() error

	// GetAllRepositoriesWithState returns all repositories that have concurrency state
	// This is used for state recovery on restart
	GetAllRepositoriesWithState(ctx context.Context) ([]*v1alpha1.Repository, error)
}

// QueueManager defines the interface for queue management.
type QueueManager interface {
	// InitQueues initializes queues for all repositories
	InitQueues(ctx context.Context, tektonClient, pacClient interface{}) error

	// RemoveRepository cleans up all state for a repository
	RemoveRepository(repo *v1alpha1.Repository)

	// QueuedPipelineRuns returns the list of queued PipelineRuns for a repository
	QueuedPipelineRuns(repo *v1alpha1.Repository) []string

	// RunningPipelineRuns returns the list of running PipelineRuns for a repository
	RunningPipelineRuns(repo *v1alpha1.Repository) []string

	// AddListToRunningQueue attempts to acquire concurrency slots for the provided PipelineRuns
	AddListToRunningQueue(repo *v1alpha1.Repository, list []string) ([]string, error)

	// AddToPendingQueue adds PipelineRuns to the pending queue
	AddToPendingQueue(repo *v1alpha1.Repository, list []string) error

	// RemoveFromQueue removes a PipelineRun from the queue and releases its slot
	RemoveFromQueue(repoKey, prKey string) bool

	// RemoveAndTakeItemFromQueue removes a PipelineRun and returns the next item to process
	RemoveAndTakeItemFromQueue(repo *v1alpha1.Repository, run interface{}) string

	// TryAcquireSlot attempts to acquire a concurrency slot for a PipelineRun
	TryAcquireSlot(ctx context.Context, repo *v1alpha1.Repository, prKey string) (bool, LeaseID, error)

	// SetupWatcher sets up a watcher for slot availability changes
	SetupWatcher(ctx context.Context, repo *v1alpha1.Repository, callback func())

	// SyncStateFromDriver synchronizes the in-memory queue state with the persistent driver state
	SyncStateFromDriver(ctx context.Context, repo *v1alpha1.Repository) error
}

// DriverConfig holds configuration for concurrency drivers.
type DriverConfig struct {
	// Driver type: "etcd", "postgresql", "memory"
	Driver string

	// Driver-specific configuration
	EtcdConfig       *EtcdConfig
	PostgreSQLConfig *PostgreSQLConfig
	MemoryConfig     *MemoryConfig
}

// EtcdConfig holds etcd-specific configuration.
type EtcdConfig struct {
	Endpoints   []string
	DialTimeout time.Duration
	Username    string
	Password    string
	TLSConfig   *TLSConfig
	Mode        string // "memory", "mock", or "etcd"
}

// PostgreSQLConfig holds PostgreSQL-specific configuration.
type PostgreSQLConfig struct {
	Host              string
	Port              int
	Database          string
	Username          string
	Password          string
	SSLMode           string
	MaxConnections    int
	ConnectionTimeout time.Duration
	LeaseTTL          time.Duration
}

// MemoryConfig holds in-memory driver configuration.
type MemoryConfig struct {
	LeaseTTL time.Duration
}

// TLSConfig holds TLS configuration.
type TLSConfig struct {
	CertFile   string
	KeyFile    string
	CAFile     string
	ServerName string
}

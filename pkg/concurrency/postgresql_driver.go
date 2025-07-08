package concurrency

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/lib/pq" // PostgreSQL driver
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PostgreSQLDriver implements Driver using PostgreSQL.
type PostgreSQLDriver struct {
	db     *sql.DB
	config *PostgreSQLConfig
	logger *zap.SugaredLogger
}

// NewPostgreSQLDriver creates a new PostgreSQL-based concurrency driver.
func NewPostgreSQLDriver(config *PostgreSQLConfig, logger *zap.SugaredLogger) (Driver, error) {
	// Build connection string
	connStr := fmt.Sprintf("host=%s port=%d dbname=%s user=%s password=%s sslmode=%s connect_timeout=%d",
		config.Host, config.Port, config.Database, config.Username, config.Password,
		config.SSLMode, int(config.ConnectionTimeout.Seconds()))

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to open database connection: %w", err)
	}

	// Configure connection pool
	db.SetMaxOpenConns(config.MaxConnections)
	db.SetMaxIdleConns(config.MaxConnections / 2)
	db.SetConnMaxLifetime(time.Hour)

	// Test connection
	if err := db.PingContext(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	driver := &PostgreSQLDriver{
		db:     db,
		config: config,
		logger: logger,
	}

	// Initialize database schema
	if err := driver.initSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to initialize database schema: %w", err)
	}

	return driver, nil
}

// initSchema creates the necessary database tables.
func (pd *PostgreSQLDriver) initSchema() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS concurrency_slots (
			id SERIAL PRIMARY KEY,
			repository_key VARCHAR(255) NOT NULL,
			pipeline_run_key VARCHAR(255) NOT NULL,
			state VARCHAR(50) NOT NULL DEFAULT 'running',
			acquired_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			UNIQUE(repository_key, pipeline_run_key)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_concurrency_slots_repo ON concurrency_slots(repository_key)`,
		`CREATE INDEX IF NOT EXISTS idx_concurrency_slots_expires ON concurrency_slots(expires_at)`,
		`CREATE INDEX IF NOT EXISTS idx_concurrency_slots_state ON concurrency_slots(state)`,
		`CREATE TABLE IF NOT EXISTS repository_states (
			id SERIAL PRIMARY KEY,
			repository_key VARCHAR(255) NOT NULL UNIQUE,
			state VARCHAR(50) NOT NULL,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS pipeline_run_states (
			id SERIAL PRIMARY KEY,
			pipeline_run_key VARCHAR(255) NOT NULL UNIQUE,
			state VARCHAR(50) NOT NULL,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		)`,
	}

	for _, query := range queries {
		if _, err := pd.db.ExecContext(context.Background(), query); err != nil {
			return fmt.Errorf("failed to execute schema query: %w", err)
		}
	}

	// Start cleanup goroutine for expired leases
	go pd.cleanupExpiredLeases()

	return nil
}

// cleanupExpiredLeases periodically removes expired concurrency slots.
func (pd *PostgreSQLDriver) cleanupExpiredLeases() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

		query := `DELETE FROM concurrency_slots WHERE expires_at < NOW()`
		result, err := pd.db.ExecContext(ctx, query)
		cancel() // Call cancel immediately after use
		if err != nil {
			pd.logger.Errorf("failed to cleanup expired leases: %v", err)
			continue
		}

		if rowsAffected, _ := result.RowsAffected(); rowsAffected > 0 {
			pd.logger.Debugf("cleaned up %d expired concurrency slots", rowsAffected)
		}
	}
}

// AcquireSlot tries to acquire a concurrency slot for a PipelineRun in a repository.
func (pd *PostgreSQLDriver) AcquireSlot(ctx context.Context, repo *v1alpha1.Repository, pipelineRunKey string) (bool, LeaseID, error) {
	if repo.Spec.ConcurrencyLimit == nil || *repo.Spec.ConcurrencyLimit == 0 {
		// No concurrency limit, always allow
		return true, 0, nil
	}

	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)
	limit := *repo.Spec.ConcurrencyLimit
	expiresAt := time.Now().Add(pd.config.LeaseTTL)

	// Use a transaction to ensure atomicity
	tx, err := pd.db.BeginTx(ctx, nil)
	if err != nil {
		return false, 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil {
			pd.logger.Errorf("Failed to rollback transaction: %v", err)
		}
	}()

	// Check current count of running slots
	var currentCount int
	countQuery := `SELECT COUNT(*) FROM concurrency_slots 
		WHERE repository_key = $1 AND state = 'running' AND expires_at > NOW()`

	err = tx.QueryRowContext(ctx, countQuery, repoKey).Scan(&currentCount)
	if err != nil {
		return false, 0, fmt.Errorf("failed to count current slots: %w", err)
	}

	if currentCount >= limit {
		pd.logger.Infof("concurrency limit reached for repository %s: %d/%d", repoKey, currentCount, limit)
		return false, 0, nil
	}

	// Try to insert the new slot or update existing one to 'running' state
	// This handles the case where a slot already exists (e.g., from queued state)
	insertQuery := `INSERT INTO concurrency_slots 
		(repository_key, pipeline_run_key, state, expires_at) 
		VALUES ($1, $2, 'running', $3) 
		ON CONFLICT (repository_key, pipeline_run_key) 
		DO UPDATE SET 
			state = 'running',
			expires_at = $3,
			updated_at = NOW()
		WHERE concurrency_slots.state != 'running'`

	result, err := tx.ExecContext(ctx, insertQuery, repoKey, pipelineRunKey, expiresAt)
	if err != nil {
		return false, 0, fmt.Errorf("failed to insert/update concurrency slot: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, 0, fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		// Slot already exists and is already in 'running' state
		// This could happen if the same PipelineRun tries to acquire a slot multiple times
		pd.logger.Debugf("slot already exists in running state for %s in repository %s", pipelineRunKey, repoKey)
	}

	// Get the inserted slot ID
	var slotID int
	err = tx.QueryRowContext(ctx,
		`SELECT id FROM concurrency_slots WHERE repository_key = $1 AND pipeline_run_key = $2`,
		repoKey, pipelineRunKey).Scan(&slotID)
	if err != nil {
		return false, 0, fmt.Errorf("failed to get slot ID: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return false, 0, fmt.Errorf("failed to commit transaction: %w", err)
	}

	pd.logger.Infof("acquired concurrency slot for %s in repository %s (slot ID: %d)", pipelineRunKey, repoKey, slotID)
	return true, slotID, nil
}

// ReleaseSlot releases a concurrency slot by marking it as released.
func (pd *PostgreSQLDriver) ReleaseSlot(ctx context.Context, leaseID LeaseID, pipelineRunKey, repoKey string) error {
	if leaseID == nil || leaseID == 0 {
		// No lease to release
		return nil
	}

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

	// Update the slot state to 'released'
	query := `UPDATE concurrency_slots 
		SET state = 'released', updated_at = NOW() 
		WHERE id = $1 AND repository_key = $2 AND pipeline_run_key = $3`

	result, err := pd.db.ExecContext(ctx, query, slotID, repoKey, pipelineRunKey)
	if err != nil {
		return fmt.Errorf("failed to release slot: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		pd.logger.Warnf("slot not found for release: ID=%d, repo=%s, pipeline=%s", slotID, repoKey, pipelineRunKey)
		return nil
	}

	pd.logger.Infof("released concurrency slot for %s in repository %s (slot ID: %d)", pipelineRunKey, repoKey, slotID)
	return nil
}

// GetCurrentSlots returns the current number of slots in use for a repository.
func (pd *PostgreSQLDriver) GetCurrentSlots(ctx context.Context, repo *v1alpha1.Repository) (int, error) {
	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)

	query := `SELECT COUNT(*) FROM concurrency_slots 
		WHERE repository_key = $1 AND state = 'running' AND expires_at > NOW()`

	var count int
	err := pd.db.QueryRowContext(ctx, query, repoKey).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to get current slots: %w", err)
	}

	return count, nil
}

// GetRunningPipelineRuns returns the list of currently running PipelineRuns for a repository.
func (pd *PostgreSQLDriver) GetRunningPipelineRuns(ctx context.Context, repo *v1alpha1.Repository) ([]string, error) {
	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)

	query := `SELECT pipeline_run_key FROM concurrency_slots 
		WHERE repository_key = $1 AND state = 'running' AND expires_at > NOW()`

	rows, err := pd.db.QueryContext(ctx, query, repoKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get running pipeline runs: %w", err)
	}
	defer rows.Close()

	var pipelineRuns []string
	for rows.Next() {
		var prKey string
		if err := rows.Scan(&prKey); err != nil {
			return nil, fmt.Errorf("failed to scan pipeline run key: %w", err)
		}
		pipelineRuns = append(pipelineRuns, prKey)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating over rows: %w", err)
	}

	return pipelineRuns, nil
}

// GetQueuedPipelineRuns returns the list of currently queued PipelineRuns for a repository.
func (pd *PostgreSQLDriver) GetQueuedPipelineRuns(ctx context.Context, repo *v1alpha1.Repository) ([]string, error) {
	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)

	query := `SELECT pipeline_run_key FROM concurrency_slots 
		WHERE repository_key = $1 AND state = 'queued' AND expires_at > NOW()`

	rows, err := pd.db.QueryContext(ctx, query, repoKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get queued pipeline runs: %w", err)
	}
	defer rows.Close()

	var pipelineRuns []string
	for rows.Next() {
		var prKey string
		if err := rows.Scan(&prKey); err != nil {
			return nil, fmt.Errorf("failed to scan pipeline run key: %w", err)
		}
		pipelineRuns = append(pipelineRuns, prKey)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating over rows: %w", err)
	}

	return pipelineRuns, nil
}

// GetQueuedPipelineRunsWithTimestamps returns queued PipelineRuns with their creation timestamps.
// This is used for proper FIFO ordering during state recovery.
func (pd *PostgreSQLDriver) GetQueuedPipelineRunsWithTimestamps(ctx context.Context, repo *v1alpha1.Repository) (map[string]time.Time, error) {
	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)

	query := `SELECT pipeline_run_key, created_at FROM concurrency_slots 
		WHERE repository_key = $1 AND state = 'queued' AND expires_at > NOW()
		ORDER BY created_at ASC`

	rows, err := pd.db.QueryContext(ctx, query, repoKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get queued pipeline runs with timestamps: %w", err)
	}
	defer rows.Close()

	result := make(map[string]time.Time)
	for rows.Next() {
		var prKey string
		var createdAt time.Time
		if err := rows.Scan(&prKey, &createdAt); err != nil {
			return nil, fmt.Errorf("failed to scan pipeline run key and timestamp: %w", err)
		}
		result[prKey] = createdAt
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating over rows: %w", err)
	}

	return result, nil
}

// WatchSlotAvailability watches for slot availability changes in a repository.
// Uses optimized polling with exponential backoff when no changes are detected.
func (pd *PostgreSQLDriver) WatchSlotAvailability(ctx context.Context, repo *v1alpha1.Repository, callback func()) {
	config := DefaultPostgreSQLWatcherConfig()
	AdaptiveWatcher(ctx, repo, callback, pd, config, pd.logger)
}

// SetRepositoryState sets the overall state for a repository's concurrency.
func (pd *PostgreSQLDriver) SetRepositoryState(ctx context.Context, repo *v1alpha1.Repository, state string) error {
	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)

	query := `INSERT INTO repository_states (repository_key, state) 
		VALUES ($1, $2) 
		ON CONFLICT (repository_key) 
		DO UPDATE SET state = $2, updated_at = NOW()`

	_, err := pd.db.ExecContext(ctx, query, repoKey, state)
	if err != nil {
		return fmt.Errorf("failed to set repository state: %w", err)
	}

	pd.logger.Debugf("set repository state for %s: %s", repoKey, state)
	return nil
}

// GetRepositoryState gets the overall state for a repository's concurrency.
func (pd *PostgreSQLDriver) GetRepositoryState(ctx context.Context, repo *v1alpha1.Repository) (string, error) {
	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)

	query := `SELECT state FROM repository_states WHERE repository_key = $1`

	var state string
	err := pd.db.QueryRowContext(ctx, query, repoKey).Scan(&state)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to get repository state: %w", err)
	}

	return state, nil
}

// SetPipelineRunState sets the state for a specific PipelineRun.
func (pd *PostgreSQLDriver) SetPipelineRunState(ctx context.Context, pipelineRunKey, state string, repo *v1alpha1.Repository) error {
	query := `INSERT INTO pipeline_run_states (pipeline_run_key, state) 
		VALUES ($1, $2) 
		ON CONFLICT (pipeline_run_key) 
		DO UPDATE SET state = $2, updated_at = NOW()`

	_, err := pd.db.ExecContext(ctx, query, pipelineRunKey, state)
	if err != nil {
		return fmt.Errorf("failed to set pipeline run state: %w", err)
	}

	// If the state is "queued", also store it in the queue
	if state == "queued" && repo != nil {
		repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)
		queueQuery := `INSERT INTO concurrency_slots (repository_key, pipeline_run_key, state, expires_at) 
			VALUES ($1, $2, 'queued', NOW() + INTERVAL '1 hour')
			ON CONFLICT (repository_key, pipeline_run_key) 
			DO UPDATE SET state = 'queued', expires_at = NOW() + INTERVAL '1 hour'`

		_, err = pd.db.ExecContext(ctx, queueQuery, repoKey, pipelineRunKey)
		if err != nil {
			pd.logger.Errorf("failed to add pipeline run to queue: %v", err)
		}
	}

	pd.logger.Debugf("set pipeline run state for %s: %s", pipelineRunKey, state)
	return nil
}

// GetPipelineRunState gets the state for a specific PipelineRun.
func (pd *PostgreSQLDriver) GetPipelineRunState(ctx context.Context, pipelineRunKey string) (string, error) {
	query := `SELECT state FROM pipeline_run_states WHERE pipeline_run_key = $1`

	var state string
	err := pd.db.QueryRowContext(ctx, query, pipelineRunKey).Scan(&state)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to get pipeline run state: %w", err)
	}

	return state, nil
}

// CleanupRepository cleans up all PostgreSQL state for a repository.
func (pd *PostgreSQLDriver) CleanupRepository(ctx context.Context, repo *v1alpha1.Repository) error {
	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)

	// Delete all concurrency slots for this repository
	_, err := pd.db.ExecContext(ctx,
		`DELETE FROM concurrency_slots WHERE repository_key = $1`, repoKey)
	if err != nil {
		pd.logger.Errorf("failed to delete concurrency slots for repository %s: %v", repoKey, err)
	}

	// Delete repository state
	_, err = pd.db.ExecContext(ctx,
		`DELETE FROM repository_states WHERE repository_key = $1`, repoKey)
	if err != nil {
		pd.logger.Errorf("failed to delete repository state for %s: %v", repoKey, err)
	}

	pd.logger.Infof("cleaned up PostgreSQL state for repository %s", repoKey)
	return nil
}

// Close closes the PostgreSQL connection.
func (pd *PostgreSQLDriver) Close() error {
	return pd.db.Close()
}

// GetAllRepositoriesWithState returns all repositories that have concurrency state.
func (pd *PostgreSQLDriver) GetAllRepositoriesWithState(ctx context.Context) ([]*v1alpha1.Repository, error) {
	// Get all unique repository keys from concurrency_slots table
	query := `SELECT DISTINCT repository_key FROM concurrency_slots WHERE expires_at > NOW()`

	rows, err := pd.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to get repositories with state: %w", err)
	}
	defer rows.Close()

	repos := make([]*v1alpha1.Repository, 0)
	for rows.Next() {
		var repoKey string
		if err := rows.Scan(&repoKey); err != nil {
			return nil, fmt.Errorf("failed to scan repository key: %w", err)
		}

		// Parse repository key (namespace/name)
		parts := strings.Split(repoKey, "/")
		if len(parts) == 2 {
			repo := &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: parts[0],
					Name:      parts[1],
				},
			}
			repos = append(repos, repo)
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating over repository rows: %w", err)
	}

	pd.logger.Debugf("found %d repositories with concurrency state", len(repos))
	return repos, nil
}

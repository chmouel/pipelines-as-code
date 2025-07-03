package sync

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sync"
	"time"

	// sqlite3 driver.
	_ "github.com/mattn/go-sqlite3"
)

type QueueState string

const (
	StatePending  QueueState = "pending"
	StateRunning  QueueState = "running"
	StateFinished QueueState = "finished"
)

type SQLiteQueueManager struct {
	db   *sql.DB
	lock sync.Mutex
}

func NewSQLiteQueueManager(dbPath string) (*SQLiteQueueManager, error) {
	// Ensure the directory exists
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}
	mgr := &SQLiteQueueManager{db: db}
	if err := mgr.migrate(); err != nil {
		return nil, err
	}
	return mgr, nil
}

func (q *SQLiteQueueManager) migrate() error {
	_, err := q.db.ExecContext(context.Background(), `
	CREATE TABLE IF NOT EXISTS queue (
		id TEXT PRIMARY KEY,
		repo TEXT NOT NULL,
		state TEXT NOT NULL,
		priority INTEGER NOT NULL,
		creation_time INTEGER NOT NULL,
		start_time INTEGER,
		end_time INTEGER
	);
	CREATE INDEX IF NOT EXISTS idx_queue_repo_state_priority ON queue(repo, state, priority);
	CREATE TABLE IF NOT EXISTS repo_limits (
		repo TEXT PRIMARY KEY,
		concurrency_limit INTEGER NOT NULL
	);
	`)
	return err
}

// AddToQueue inserts a new item into the queue.
func (q *SQLiteQueueManager) AddToQueue(repo, id string, priority int64, creationTime time.Time) error {
	q.lock.Lock()
	defer q.lock.Unlock()
	_, err := q.db.ExecContext(context.Background(), `INSERT INTO queue (id, repo, state, priority, creation_time) VALUES (?, ?, ?, ?, ?)`,
		id, repo, StatePending, priority, creationTime.UnixNano())
	return err
}

// AcquireNext atomically moves the next pending item to running, respecting concurrency limit.
func (q *SQLiteQueueManager) AcquireNext(repo string) (string, error) {
	q.lock.Lock()
	defer q.lock.Unlock()
	// Check concurrency limit
	var limit, running int
	err := q.db.QueryRowContext(context.Background(), `SELECT concurrency_limit FROM repo_limits WHERE repo = ?`, repo).Scan(&limit)
	if err != nil {
		return "", err
	}
	err = q.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM queue WHERE repo = ? AND state = ?`, repo, StateRunning).Scan(&running)
	if err != nil {
		return "", err
	}
	if running >= limit {
		return "", nil // limit reached
	}
	// Find next pending
	row := q.db.QueryRowContext(context.Background(), `SELECT id FROM queue WHERE repo = ? AND state = ? ORDER BY priority ASC LIMIT 1`, repo, StatePending)
	var id string
	if err := row.Scan(&id); err != nil {
		return "", nil // nothing pending
	}
	// Move to running
	_, err = q.db.ExecContext(context.Background(), `UPDATE queue SET state = ?, start_time = ? WHERE id = ?`, StateRunning, time.Now().UnixNano(), id)
	if err != nil {
		return "", err
	}
	return id, nil
}

// Release moves a running item to finished.
func (q *SQLiteQueueManager) Release(repo, id string) error {
	q.lock.Lock()
	defer q.lock.Unlock()
	_, err := q.db.ExecContext(context.Background(), `UPDATE queue SET state = ?, end_time = ? WHERE id = ? AND repo = ? AND state = ?`,
		StateFinished, time.Now().UnixNano(), id, repo, StateRunning)
	return err
}

// GetCurrentPending returns all pending IDs for a repo.
func (q *SQLiteQueueManager) GetCurrentPending(repo string) ([]string, error) {
	q.lock.Lock()
	defer q.lock.Unlock()
	rows, err := q.db.QueryContext(context.Background(), `SELECT id FROM queue WHERE repo = ? AND state = ? ORDER BY priority ASC`, repo, StatePending)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// GetCurrentRunning returns all running IDs for a repo.
func (q *SQLiteQueueManager) GetCurrentRunning(repo string) ([]string, error) {
	q.lock.Lock()
	defer q.lock.Unlock()
	rows, err := q.db.QueryContext(context.Background(), `SELECT id FROM queue WHERE repo = ? AND state = ? ORDER BY start_time ASC`, repo, StateRunning)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// GetLimit returns the concurrency limit for a repo.
func (q *SQLiteQueueManager) GetLimit(repo string) (int, error) {
	q.lock.Lock()
	defer q.lock.Unlock()
	var limit int
	err := q.db.QueryRowContext(context.Background(), `SELECT concurrency_limit FROM repo_limits WHERE repo = ?`, repo).Scan(&limit)
	if err != nil {
		return 0, err
	}
	return limit, nil
}

// SetLimit sets the concurrency limit for a repo.
func (q *SQLiteQueueManager) SetLimit(repo string, n int) error {
	q.lock.Lock()
	defer q.lock.Unlock()
	_, err := q.db.ExecContext(context.Background(), `INSERT INTO repo_limits (repo, concurrency_limit) VALUES (?, ?) ON CONFLICT(repo) DO UPDATE SET concurrency_limit = excluded.concurrency_limit`, repo, n)
	return err
}

// RemoveFromQueue removes an item from the queue (any state).
func (q *SQLiteQueueManager) RemoveFromQueue(repo, id string) error {
	q.lock.Lock()
	defer q.lock.Unlock()
	_, err := q.db.ExecContext(context.Background(), `DELETE FROM queue WHERE repo = ? AND id = ?`, repo, id)
	return err
}

// ResetRunning moves all running items for a repo back to pending (for recovery).
func (q *SQLiteQueueManager) ResetRunning(repo string) error {
	q.lock.Lock()
	defer q.lock.Unlock()
	_, err := q.db.ExecContext(context.Background(), `UPDATE queue SET state = ?, start_time = NULL WHERE repo = ? AND state = ?`, StatePending, repo, StateRunning)
	return err
}

// RemoveRepository removes all queue entries and limits for a repo.
func (q *SQLiteQueueManager) RemoveRepository(repo string) error {
	q.lock.Lock()
	defer q.lock.Unlock()
	_, err := q.db.ExecContext(context.Background(), `DELETE FROM queue WHERE repo = ?`, repo)
	if err != nil {
		return err
	}
	_, err = q.db.ExecContext(context.Background(), `DELETE FROM repo_limits WHERE repo = ?`, repo)
	return err
}

// SyncPipelineRunState syncs a PipelineRun's state from annotations to SQLite.
func (q *SQLiteQueueManager) SyncPipelineRunState(repo, prID, state string) error {
	q.lock.Lock()
	defer q.lock.Unlock()

	// Convert annotation state to SQLite state
	sqliteState := q.convertAnnotationStateToSQLite(state)

	// Use INSERT OR REPLACE for better compatibility
	_, err := q.db.ExecContext(context.Background(), `
		INSERT OR REPLACE INTO queue (id, repo, state, priority, creation_time) 
		VALUES (?, ?, ?, ?, ?)
	`, prID, repo, sqliteState, time.Now().UnixNano(), time.Now().UnixNano())

	return err
}

// GetPipelineRunState gets the state of a PipelineRun from SQLite.
func (q *SQLiteQueueManager) GetPipelineRunState(repo, prID string) (string, error) {
	q.lock.Lock()
	defer q.lock.Unlock()

	var state string
	err := q.db.QueryRowContext(context.Background(),
		`SELECT state FROM queue WHERE repo = ? AND id = ?`, repo, prID).Scan(&state)
	if err != nil {
		return "", err
	}

	// Convert SQLite state back to annotation state
	return q.convertSQLiteStateToAnnotation(state), nil
}

// GetAllPipelineRunStates gets all PipelineRun states for a repository.
func (q *SQLiteQueueManager) GetAllPipelineRunStates(repo string) (map[string]string, error) {
	q.lock.Lock()
	defer q.lock.Unlock()

	rows, err := q.db.QueryContext(context.Background(),
		`SELECT id, state FROM queue WHERE repo = ?`, repo)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	states := make(map[string]string)
	for rows.Next() {
		var id, state string
		if err := rows.Scan(&id, &state); err != nil {
			return nil, err
		}
		states[id] = q.convertSQLiteStateToAnnotation(state)
	}

	return states, nil
}

// convertAnnotationStateToSQLite converts annotation state to SQLite state.
func (q *SQLiteQueueManager) convertAnnotationStateToSQLite(annotationState string) QueueState {
	switch annotationState {
	case "queued":
		return StatePending
	case "started", "running":
		return StateRunning
	case "completed", "failed", "cancelled":
		return StateFinished
	default:
		return StatePending
	}
}

// convertSQLiteStateToAnnotation converts SQLite state back to annotation state.
func (q *SQLiteQueueManager) convertSQLiteStateToAnnotation(sqliteState string) string {
	switch QueueState(sqliteState) {
	case StatePending:
		return "queued"
	case StateRunning:
		return "started"
	case StateFinished:
		return "completed"
	default:
		return "queued"
	}
}

// Close closes the DB connection.
func (q *SQLiteQueueManager) Close() error {
	return q.db.Close()
}

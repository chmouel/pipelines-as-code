package sync

import (
	"time"
)

type Semaphore interface {
	AddToQueue(repo, id string, priority int64, creationTime time.Time) error
	AcquireNext(repo string) (string, error)
	Release(repo, id string) error
	RemoveFromQueue(repo, id string) error
	GetCurrentPending(repo string) ([]string, error)
	GetCurrentRunning(repo string) ([]string, error)
	GetLimit(repo string) (int, error)
	SetLimit(repo string, n int) error
	ResetRunning(repo string) error
	Close() error
}

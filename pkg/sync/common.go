package sync

import (
	"time"
)

type Semaphore interface {
	acquire(string) bool
	acquireLatest() string
	tryAcquire(string) (bool, string)
	release(string) bool
	resize(int) bool
	addToQueue(string, time.Time) bool
	addToFailedQueue(string, time.Time) bool
	getFailureQueue() []string
	removeFromQueue(string)
	getName() string
	getLimit() int
	getCurrentRunning() []string
	getCurrentPending() []string
}

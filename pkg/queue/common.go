package queue

import (
	"slices"
	"strings"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
)

type Semaphore interface {
	acquire(string) bool
	acquireLatest() string
	tryAcquire(string) (bool, string)
	release(string) bool
	resize(int) bool
	addToQueue(string, time.Time) bool
	addToPendingQueue(string, time.Time) bool
	removeFromQueue(string)
	getName() string
	getLimit() int
	getCurrentRunning() []string
	getCurrentPending() []string
}

func IsRecoverableQueuedPipelineRun(pr *tektonv1.PipelineRun) bool {
	if pr == nil {
		return false
	}
	if pr.GetAnnotations()[keys.State] != kubeinteraction.StateQueued {
		return false
	}
	if pr.Spec.Status != tektonv1.PipelineRunSpecStatusPending {
		return false
	}
	if pr.IsDone() || pr.IsCancelled() {
		return false
	}
	_, ok := executionOrderIndex(pr)
	return ok
}

func HasActiveLeaseQueueClaim(pr *tektonv1.PipelineRun, now time.Time, ttl time.Duration) bool {
	if pr == nil {
		return false
	}

	claimedBy, claimAge := LeaseQueueClaimInfo(pr, now)
	return claimedBy != "" && claimAge != unknownDuration() && claimAge <= ttl
}

func ExecutionOrderList(pr *tektonv1.PipelineRun) []string {
	order := pr.GetAnnotations()[keys.ExecutionOrder]
	if order == "" {
		return nil
	}
	return strings.Split(order, ",")
}

func ExecutionOrderIndex(pr *tektonv1.PipelineRun) (int, bool) {
	order := ExecutionOrderList(pr)
	if len(order) == 0 {
		return 0, false
	}

	key := PrKey(pr)
	index := slices.Index(order, key)
	if index < 0 {
		return 0, false
	}
	return index, true
}

func executionOrderList(pr *tektonv1.PipelineRun) []string {
	return ExecutionOrderList(pr)
}

func executionOrderIndex(pr *tektonv1.PipelineRun) (int, bool) {
	return ExecutionOrderIndex(pr)
}

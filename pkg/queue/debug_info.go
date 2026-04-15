package queue

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/action"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	tektonclient "github.com/tektoncd/pipeline/pkg/client/clientset/versioned"
	"go.uber.org/zap"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	QueueDecisionWaitingForSlot    = "waiting_for_slot"
	QueueDecisionClaimActive       = "claim_active"
	QueueDecisionClaimedForPromote = "claimed_for_promotion"
	QueueDecisionPromotionFailed   = "promotion_failed"
	QueueDecisionRecoveryRequeued  = "recovery_requeued"
	QueueDecisionMissingOrder      = "missing_execution_order"
	QueueDecisionNotRecoverable    = "not_recoverable"
)

const unknownQueueDebugValue = -1

type DebugSnapshot struct {
	Backend      string
	RepoKey      string
	Position     int
	Running      int
	Claimed      int
	Queued       int
	Limit        int
	ClaimedBy    string
	ClaimAge     time.Duration
	LastDecision string
}

func (d DebugSnapshot) Summary() string {
	backend := d.Backend
	if backend == "" {
		backend = settings.ConcurrencyBackendLease
	}

	return fmt.Sprintf(
		"backend=%s repo=%s position=%s running=%s claimed=%s queued=%s limit=%s claimedBy=%s claimAge=%s lastDecision=%s",
		backend,
		formatQueueDebugString(d.RepoKey),
		formatQueueDebugInt(d.Position),
		formatQueueDebugInt(d.Running),
		formatQueueDebugInt(d.Claimed),
		formatQueueDebugInt(d.Queued),
		formatQueueDebugInt(d.Limit),
		formatQueueDebugString(d.ClaimedBy),
		formatQueueDebugDuration(d.ClaimAge),
		formatQueueDebugString(d.LastDecision),
	)
}

func SyncQueueDebugAnnotations(
	ctx context.Context,
	logger *zap.SugaredLogger,
	tekton tektonclient.Interface,
	pr *tektonv1.PipelineRun,
	snapshot DebugSnapshot,
) error {
	if tekton == nil || pr == nil {
		return nil
	}

	latest, err := tekton.TektonV1().PipelineRuns(pr.GetNamespace()).Get(ctx, pr.GetName(), metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	if !IsQueueOnlyAnnotationRelevant(latest) {
		if logger != nil {
			logger.Debugf(
				"skipping queue debug annotation update for pipelinerun %s because latest state=%s spec.status=%s done=%t cancelled=%t",
				PrKey(latest), latest.GetAnnotations()[keys.State], latest.Spec.Status, latest.IsDone(), latest.IsCancelled(),
			)
		}
		if hasQueueDebugAnnotations(latest) {
			return ClearQueueDebugAnnotations(ctx, logger, tekton, latest)
		}
		return nil
	}

	summary := snapshot.Summary()
	currentAnnotations := latest.GetAnnotations()
	if currentAnnotations[keys.QueueDecision] == snapshot.LastDecision &&
		currentAnnotations[keys.QueueDebugSummary] == summary {
		return nil
	}

	if logger != nil {
		logger.Debugf(
			"updating queue debug annotations for pipelinerun %s: decision=%s summary=%q",
			PrKey(pr), snapshot.LastDecision, summary,
		)
	}

	_, err = action.PatchPipelineRun(ctx, logger, "queue debug", tekton, latest, map[string]any{
		"metadata": map[string]any{
			"annotations": map[string]any{
				keys.QueueDecision:     snapshot.LastDecision,
				keys.QueueDebugSummary: summary,
			},
		},
	})
	return err
}

func ClearQueueDebugAnnotations(
	ctx context.Context,
	logger *zap.SugaredLogger,
	tekton tektonclient.Interface,
	pr *tektonv1.PipelineRun,
) error {
	if tekton == nil || pr == nil {
		return nil
	}

	if !hasQueueDebugAnnotations(pr) {
		return nil
	}

	if logger != nil {
		logger.Debugf("clearing queue debug annotations for pipelinerun %s", PrKey(pr))
	}

	_, err := action.PatchPipelineRun(ctx, logger, "queue debug cleanup", tekton, pr, map[string]any{
		"metadata": map[string]any{
			"annotations": map[string]any{
				keys.QueueDecision:     nil,
				keys.QueueDebugSummary: nil,
			},
		},
	})
	return err
}

func LeaseQueueCleanupAnnotations() map[string]any {
	return map[string]any{
		keys.QueueClaimedBy:        nil,
		keys.QueueClaimedAt:        nil,
		keys.QueueDecision:         nil,
		keys.QueueDebugSummary:     nil,
		keys.QueuePromotionRetries: nil,
		keys.QueuePromotionBlocked: nil,
		keys.QueuePromotionLastErr: nil,
	}
}

func hasQueueDebugAnnotations(pr *tektonv1.PipelineRun) bool {
	if pr == nil {
		return false
	}
	annotations := pr.GetAnnotations()
	return annotations[keys.QueueDecision] != "" || annotations[keys.QueueDebugSummary] != ""
}

func IsQueueOnlyAnnotationRelevant(pr *tektonv1.PipelineRun) bool {
	if pr == nil {
		return false
	}
	if pr.GetAnnotations()[keys.State] != kubeinteraction.StateQueued {
		return false
	}
	return pr.Spec.Status == tektonv1.PipelineRunSpecStatusPending && !pr.IsDone() && !pr.IsCancelled()
}

func LeaseQueueClaimInfo(pr *tektonv1.PipelineRun, now time.Time) (string, time.Duration) {
	if pr == nil {
		return "", unknownDuration()
	}

	annotations := pr.GetAnnotations()
	claimedBy := annotations[keys.QueueClaimedBy]
	claimedAt := annotations[keys.QueueClaimedAt]
	if claimedBy == "" || claimedAt == "" {
		return claimedBy, unknownDuration()
	}

	claimedTime, err := time.Parse(time.RFC3339Nano, claimedAt)
	if err != nil {
		return claimedBy, unknownDuration()
	}

	age := now.Sub(claimedTime)
	if age < 0 {
		age = 0
	}
	return claimedBy, age
}

func formatQueueDebugInt(v int) string {
	if v == unknownQueueDebugValue {
		return "n/a"
	}
	return strconv.Itoa(v)
}

func formatQueueDebugString(v string) string {
	if v == "" {
		return "n/a"
	}
	return v
}

func formatQueueDebugDuration(v time.Duration) string {
	if v == unknownDuration() {
		return "n/a"
	}
	return v.Truncate(time.Second).String()
}

func unknownDuration() time.Duration {
	return -1
}

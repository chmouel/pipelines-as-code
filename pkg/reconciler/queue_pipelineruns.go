package reconciler

import (
	"context"
	"fmt"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	pacAPIv1alpha1 "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/sync"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (r *Reconciler) queuePipelineRun(ctx context.Context, logger *zap.SugaredLogger, pr *tektonv1.PipelineRun) error {
	_, exist := pr.GetAnnotations()[keys.ExecutionOrder]
	if !exist {
		// if the pipelineRun doesn't have order label then wait
		return nil
	}

	// check if annotation exist
	repoName, exist := pr.GetAnnotations()[keys.Repository]
	if !exist {
		return fmt.Errorf("no %s annotation found", keys.Repository)
	}
	if repoName == "" {
		return fmt.Errorf("annotation %s is empty", keys.Repository)
	}
	repo, err := r.repoLister.Repositories(pr.Namespace).Get(repoName)
	if err != nil {
		// if repository is not found, then skip processing the pipelineRun and return nil
		if errors.IsNotFound(err) {
			r.qm.RemoveRepository(&pacAPIv1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      repoName,
					Namespace: pr.Namespace,
				},
			})
			return nil
		}
		return fmt.Errorf("error getting PipelineRun: %w", err)
	}

	// merge local repo with global repo here in order to derive settings from global
	// for further concurrency and other operations.
	if r.globalRepo, err = r.repoLister.Repositories(r.run.Info.Kube.Namespace).Get(r.run.Info.Controller.GlobalRepository); err == nil && r.globalRepo != nil {
		logger.Info("Merging global repository settings with local repository settings")
		repo.Spec.Merge(r.globalRepo.Spec)
	}

	// if concurrency was set and later removed or changed to zero
	// then remove pipelineRun from Queue and update pending state to running
	if repo.Spec.ConcurrencyLimit != nil && *repo.Spec.ConcurrencyLimit == 0 {
		_ = r.qm.RemoveAndTakeItemFromQueue(repo, pr)
		if err := r.updatePipelineRunToInProgress(ctx, logger, repo, pr); err != nil {
			return fmt.Errorf("failed to update PipelineRun to in_progress: %w", err)
		}
		return nil
	}

	// Try to acquire a slot for this specific PipelineRun
	prKey := fmt.Sprintf("%s/%s", pr.Namespace, pr.Name)

	// Check if concurrency manager is available
	if r.concurrencyManager == nil {
		// Fall back to legacy system or skip concurrency control
		logger.Info("concurrency manager not available, skipping concurrency control")
		if err := r.updatePipelineRunToInProgress(ctx, logger, repo, pr); err != nil {
			return fmt.Errorf("failed to update PipelineRun to in_progress: %w", err)
		}
		return nil
	}

	success, leaseID, err := r.concurrencyManager.AcquireSlot(ctx, repo, prKey)
	if err != nil {
		return fmt.Errorf("failed to acquire concurrency slot: %w", err)
	}

	if success {
		// Slot acquired, update PipelineRun to in progress
		if err := r.updatePipelineRunToInProgress(ctx, logger, repo, pr); err != nil {
			// Release the slot if we can't update the PipelineRun
			if releaseErr := r.concurrencyManager.ReleaseSlot(ctx, leaseID, prKey, fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)); releaseErr != nil {
				logger.Errorf("failed to release slot after update failure: %v", releaseErr)
			}
			return fmt.Errorf("failed to update PipelineRun to in_progress: %w", err)
		}

		// Set up watcher for slot availability to trigger reconciliation of waiting PipelineRuns
		r.concurrencyManager.WatchSlotAvailability(ctx, repo, func() {
			logger.Info("slot became available, triggering reconciliation")
			// This could trigger reconciliation of waiting PipelineRuns
		})

		logger.Infof("acquired concurrency slot for %s in repository %s/%s", prKey, repo.Namespace, repo.Name)
		return nil
	}

	// Slot not available, PipelineRun remains in queued state
	logger.Infof("concurrency limit reached for repository %s/%s, PipelineRun %s will wait", repo.Namespace, repo.Name, prKey)
	return nil
}

// queuePipelineRunWithNewSystem uses the new abstracted concurrency system
func (r *Reconciler) queuePipelineRunWithNewSystem(ctx context.Context, logger *zap.SugaredLogger, pr *tektonv1.PipelineRun, repo *pacAPIv1alpha1.Repository, order string) error {
	// Try to acquire a slot for this specific PipelineRun
	prKey := fmt.Sprintf("%s/%s", pr.Namespace, pr.Name)
	success, leaseID, err := r.concurrencyManager.AcquireSlot(ctx, repo, prKey)
	if err != nil {
		return fmt.Errorf("failed to acquire concurrency slot: %w", err)
	}

	if success {
		// Slot acquired, update PipelineRun to in progress
		if err := r.updatePipelineRunToInProgress(ctx, logger, repo, pr); err != nil {
			// Release the slot if we can't update the PipelineRun
			if releaseErr := r.concurrencyManager.ReleaseSlot(ctx, leaseID, prKey, fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)); releaseErr != nil {
				logger.Errorf("failed to release slot after update failure: %v", releaseErr)
			}
			return fmt.Errorf("failed to update PipelineRun to in_progress: %w", err)
		}

		// Set up watcher for slot availability to trigger reconciliation of waiting PipelineRuns
		r.concurrencyManager.WatchSlotAvailability(ctx, repo, func() {
			logger.Info("slot became available, triggering reconciliation")
			// This could trigger reconciliation of waiting PipelineRuns
		})

		logger.Infof("acquired concurrency slot for %s in repository %s/%s", prKey, repo.Namespace, repo.Name)
		return nil
	}

	// Slot not available, PipelineRun remains in queued state
	logger.Infof("concurrency limit reached for repository %s/%s, PipelineRun %s will wait", repo.Namespace, repo.Name, prKey)
	return nil
}

// queuePipelineRunWithLegacySystem uses the legacy queue manager system
func (r *Reconciler) queuePipelineRunWithLegacySystem(ctx context.Context, logger *zap.SugaredLogger, pr *tektonv1.PipelineRun, repo *pacAPIv1alpha1.Repository, order string) error {
	var processed bool
	var itered int
	maxIterations := 5

	orderedList := sync.FilterPipelineRunByState(ctx, r.run.Clients.Tekton, strings.Split(order, ","), tektonv1.PipelineRunSpecStatusPending, kubeinteraction.StateQueued)
	for {
		acquired, err := r.qm.AddListToRunningQueue(repo, orderedList)
		if err != nil {
			return fmt.Errorf("failed to add to queue: %s: %w", pr.GetName(), err)
		}
		if len(acquired) == 0 {
			logger.Infof("no new PipelineRun acquired for repo %s", repo.GetName())
			break
		}

		for _, prKeys := range acquired {
			nsName := strings.Split(prKeys, "/")
			repoKey := sync.RepoKey(repo)
			pr, err = r.run.Clients.Tekton.TektonV1().PipelineRuns(nsName[0]).Get(ctx, nsName[1], metav1.GetOptions{})
			if err != nil {
				logger.Info("failed to get pr with namespace and name: ", nsName[0], nsName[1])
				_ = r.qm.RemoveFromQueue(repoKey, prKeys)
			} else {
				if err := r.updatePipelineRunToInProgress(ctx, logger, repo, pr); err != nil {
					logger.Errorf("failed to update pipelineRun to in_progress: %w", err)
					_ = r.qm.RemoveFromQueue(repoKey, prKeys)
				} else {
					processed = true
				}
			}
		}
		if processed {
			break
		}
		if itered >= maxIterations {
			return fmt.Errorf("max iterations reached of %d times trying to get a pipelinerun started for %s", maxIterations, repo.GetName())
		}
		itered++
	}
	return nil
}

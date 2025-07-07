package reconciler

import (
	"context"
	"fmt"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/logging"
	pkgreconciler "knative.dev/pkg/reconciler"
)

func (r *Reconciler) FinalizeKind(ctx context.Context, pr *tektonv1.PipelineRun) pkgreconciler.Event {
	logger := logging.FromContext(ctx)
	state, exist := pr.GetAnnotations()[keys.State]
	if !exist || state == kubeinteraction.StateCompleted {
		return nil
	}

	if state == kubeinteraction.StateQueued || state == kubeinteraction.StateStarted {
		repoName, ok := pr.GetAnnotations()[keys.Repository]
		if !ok {
			return nil
		}
		repo, err := r.repoLister.Repositories(pr.Namespace).Get(repoName)
		// if repository is not found then cleanup concurrency state
		if errors.IsNotFound(err) {
			if r.concurrencyManager != nil {
				if err := r.concurrencyManager.CleanupRepository(ctx, &v1alpha1.Repository{
					ObjectMeta: metav1.ObjectMeta{Name: repoName, Namespace: pr.Namespace},
				}); err != nil {
					logger.Errorf("failed to cleanup concurrency state for repository %s/%s: %v", pr.Namespace, repoName, err)
				}
			}
			return nil
		}
		if err != nil {
			return err
		}
		r.secretNS = repo.GetNamespace()
		if r.globalRepo, err = r.repoLister.Repositories(r.run.Info.Kube.Namespace).Get(r.run.Info.Controller.GlobalRepository); err == nil && r.globalRepo != nil {
			if repo.Spec.GitProvider != nil && repo.Spec.GitProvider.Secret == nil && r.globalRepo.Spec.GitProvider != nil && r.globalRepo.Spec.GitProvider.Secret != nil {
				r.secretNS = r.globalRepo.GetNamespace()
			}
			repo.Spec.Merge(r.globalRepo.Spec)
		}
		logger = logger.With("namespace", repo.Namespace)

		// Release concurrency slot if using new system
		if r.concurrencyManager != nil {
			prKey := fmt.Sprintf("%s/%s", pr.Namespace, pr.Name)
			repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)
			// Note: We don't have the leaseID here, so the driver will need to handle this
			// by looking up the slot based on the pipeline run key
			if err := r.concurrencyManager.ReleaseSlot(ctx, nil, prKey, repoKey); err != nil {
				logger.Warnf("failed to release concurrency slot for %s: %v", prKey, err)
			}
		}

		// Check if there are queued PipelineRuns that can now start
		if r.concurrencyManager != nil {
			queuedPRs := r.concurrencyManager.GetQueueManager().QueuedPipelineRuns(repo)
			if len(queuedPRs) > 0 {
				// Try to start the next queued PipelineRun
				for _, nextPRKey := range queuedPRs {
					parts := strings.Split(nextPRKey, "/")
					if len(parts) != 2 {
						continue
					}

					nextPR, err := r.run.Clients.Tekton.TektonV1().PipelineRuns(parts[0]).Get(ctx, parts[1], metav1.GetOptions{})
					if err != nil {
						logger.Errorf("cannot get pipeline for next in queue: %w", err)
						continue
					}

					// Try to acquire a slot for this PipelineRun
					success, _, err := r.concurrencyManager.AcquireSlot(ctx, repo, nextPRKey)
					if err != nil {
						logger.Errorf("failed to acquire slot for %s: %v", nextPRKey, err)
						continue
					}

					if success {
						if err := r.updatePipelineRunToInProgress(ctx, logger, repo, nextPR); err != nil {
							logger.Errorf("failed to update status: %w", err)
							// Release the slot if we can't update the PipelineRun
							repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)
							if releaseErr := r.concurrencyManager.ReleaseSlot(ctx, nil, nextPRKey, repoKey); releaseErr != nil {
								logger.Errorf("failed to release slot after update failure: %v", releaseErr)
							}
							return err
						}
						logger.Infof("started next queued PipelineRun: %s", nextPRKey)
						return nil
					}
				}
			}
		}
	}
	return nil
}

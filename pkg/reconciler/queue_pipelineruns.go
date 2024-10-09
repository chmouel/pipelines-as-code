package reconciler

import (
	"context"
	"fmt"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (r *Reconciler) processQ(ctx context.Context, logger *zap.SugaredLogger, pr *tektonv1.PipelineRun) error {
	repoName := pr.GetAnnotations()[keys.Repository]
	repo, err := r.repoLister.Repositories(pr.Namespace).Get(repoName)
	if err != nil {
		// if repository is not found, then skip processing the pipelineRun and return nil
		if errors.IsNotFound(err) {
			r.qm.RemoveRepository(&v1alpha1.Repository{ObjectMeta: metav1.ObjectMeta{
				Name:      repoName,
				Namespace: pr.Namespace,
			}})
			return nil
		}
		return fmt.Errorf("updateError: %w", err)
	}

	// merge local repo with global repo here in order to derive settings from global
	// for further concurrency and other operations.
	if r.globalRepo, err = r.repoLister.Repositories(r.run.Info.Kube.Namespace).Get(r.run.Info.Controller.GlobalRepository); err == nil && r.globalRepo != nil {
		repo.Spec.Merge(r.globalRepo.Spec)
	}

	queue, err := r.run.Clients.DB.GetQueuedPRs(*repo.Spec.ConcurrencyLimit, pr.GetNamespace(), repo.GetName())
	if err != nil {
		return fmt.Errorf("failed to get queued PRs: %w", err)
	}
	if len(queue) == 0 {
		return nil
	}
	// if concurrency was set and later removed or changed to zero
	// then remove pipelineRun from Queue and update pending state to running
	if repo.Spec.ConcurrencyLimit != nil && *repo.Spec.ConcurrencyLimit == 0 {
		// TODO(newreconciler)
		_ = r.qm.RemoveFromQueue(repo, pr)
		if err := r.updatePipelineRunToInProgress(ctx, logger, repo, pr); err != nil {
			return fmt.Errorf("failed to update PipelineRun to in_progress: %w", err)
		}
		return nil
	}

	for _, q := range queue {
		pr, err = r.run.Clients.Tekton.TektonV1().PipelineRuns(q.Namespace).Get(ctx, q.Name, metav1.GetOptions{})
		if err != nil {
			logger.Info("failed to get pr with namespace and name: ", q.Namespace, q.Name)
			return err
		}
		if pr.Spec.Status != tektonv1.PipelineRunSpecStatusPending {
			continue
		}
		if err := r.updatePipelineRunToInProgress(ctx, logger, repo, pr); err != nil {
			return fmt.Errorf("failed to update pipelineRun to in_progress: %w", err)
		}
	}
	return nil
}

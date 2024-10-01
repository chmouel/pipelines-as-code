package reconciler

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/go-github/v64/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/db"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (r *Reconciler) processQ(ctx context.Context, repo *v1alpha1.Repository, pr *tektonv1.PipelineRun, logger *zap.SugaredLogger) error {
	var orderedList []string

	orderedList, err := r.run.Clients.DB.GetQueue(pr)
	fmt.Printf("orderedList: %v\n", orderedList)
	if err != nil {
		return fmt.Errorf("db: failed to get queue: %w", err)
	}
	redo := false
	// we start cleaning the queue if the queue is out of sync with what on cluster
	// we can't do this in a single transaction as we need to check each item in the queue
	// TODO: we can probably optimize with a List instead of Get each items everytime
	for _, prKeys := range orderedList {
		nsName := strings.Split(prKeys, "/")
		_, err := r.run.Clients.Tekton.TektonV1().PipelineRuns(nsName[0]).Get(ctx, nsName[1], metav1.GetOptions{})
		if err != nil {
			logger.Infof("removing PipelineRun from queue: %s/%s, %v", nsName[0], nsName[1], err)
			_, _ = r.run.Clients.DB.RemovePipelineRun(repo, nsName[1])
			redo = true
		}
	}
	if redo {
		return r.processQ(ctx, repo, pr, logger)
	}
	acquired, err := r.getNextPRInQueue(repo, orderedList)
	if err != nil {
		return fmt.Errorf("db: failed to get queue: %w", err)
	}
	for _, prKeys := range acquired {
		nsName := strings.Split(prKeys, "/")
		pr, err := r.run.Clients.Tekton.TektonV1().PipelineRuns(nsName[0]).Get(ctx, nsName[1], metav1.GetOptions{})
		if err != nil {
			return r.processQ(ctx, repo, pr, logger)
		}
		if err := r.updatePipelineRunToInProgress(ctx, logger, repo, pr); err != nil {
			return fmt.Errorf("failed to update pipelineRun to in_progress: %w", err)
		}
	}
	return nil
}

func (r *Reconciler) getNextPRInQueue(repo *v1alpha1.Repository, orderedList []string) ([]string, error) {
	acquiredList := []string{}
	for i := 0; i < *repo.Spec.ConcurrencyLimit; i++ {
		// get the first item of the ordredList and remove it
		if len(orderedList) == 0 {
			break
		}
		acquired := orderedList[0]
		orderedList = orderedList[1:]
		if acquired != "" {
			acquiredList = append(acquiredList, acquired)
		}
	}
	return acquiredList, nil
}

func (r *Reconciler) queuePipelineRun(ctx context.Context, logger *zap.SugaredLogger, pr *tektonv1.PipelineRun) error {
	var err error

	repoName := pr.GetAnnotations()[keys.Repository]
	repo, err := r.repoLister.Repositories(pr.Namespace).Get(repoName)
	if err != nil {
		// if repository is not found, then skip processing the pipelineRun and return nil
		if errors.IsNotFound(err) {
			r.qm.RemoveRepository(&v1alpha1.Repository{ObjectMeta: metav1.ObjectMeta{
				Name:      repoName,
				Namespace: pr.Namespace,
			}})
			if err := r.run.Clients.DB.RemoveRepository(repoName, pr.GetNamespace()); err != nil {
				return fmt.Errorf("db: failed to remove repository: %w", err)
			}
			return nil
		}
		return fmt.Errorf("updateError: %w", err)
	}

	// merge local repo with global repo here in order to derive settings from global
	// for further concurrency and other operations.
	if r.globalRepo, err = r.repoLister.Repositories(r.run.Info.Kube.Namespace).Get(r.run.Info.Controller.GlobalRepository); err == nil && r.globalRepo != nil {
		repo.Spec.Merge(r.globalRepo.Spec)
	}

	// if concurrency was set and later removed or changed to zero
	// then remove pipelineRun from Queue and update pending state to running
	if repo.Spec.ConcurrencyLimit != nil && *repo.Spec.ConcurrencyLimit == 0 {
		_ = r.qm.RemoveFromQueue(repo, pr)
		if err := r.run.Clients.DB.CreatedUpdatePR(pr, &db.Queue{Queued: github.Bool(false)}); err != nil {
			return fmt.Errorf("db: failed to update PipelineRun: %w", err)
		}
		if err := r.updatePipelineRunToInProgress(ctx, logger, repo, pr); err != nil {
			return fmt.Errorf("failed to update PipelineRun to in_progress: %w", err)
		}
	}
	return r.processQ(ctx, repo, pr, logger)
}

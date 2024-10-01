package reconciler

import (
	"context"
	"fmt"
	"strconv"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/apis"
)

func (r *Reconciler) cleanupPipelineRuns(ctx context.Context, logger *zap.SugaredLogger, pacInfo *info.PacOpts, repo *v1alpha1.Repository, pr *tektonv1.PipelineRun) error {
	keepMaxPipeline, ok := pr.Annotations[keys.MaxKeepRuns]
	if ok {
		maxVal, err := strconv.Atoi(keepMaxPipeline)
		if err != nil {
			return err
		}
		// if annotation value is more than max limit defined in config then use from config
		if pacInfo.MaxKeepRunsUpperLimit > 0 && maxVal > pacInfo.MaxKeepRunsUpperLimit {
			logger.Infof("max-keep-run value in annotation (%v) is more than max-keep-run-upper-limit (%v), so using upper-limit", maxVal, pacInfo.MaxKeepRunsUpperLimit)
			maxVal = pacInfo.MaxKeepRunsUpperLimit
		}

		queues, err := r.run.Clients.DB.GetPipelineRunToCleanup(pr, maxVal)
		if err != nil {
			return err
		}

		for _, queue := range queues {
			dPrun, err := r.run.Clients.Tekton.TektonV1().PipelineRuns(queue.Repository).Get(ctx, queue.Name, metav1.GetOptions{})
			if errors.IsNotFound(err) {
				logger.Infof("pipelinerun %s has already been deleted", queue.Name)
			} else if err != nil {
				return err
			} else {
				prReason := dPrun.GetStatusCondition().GetCondition(apis.ConditionSucceeded).GetReason()
				if prReason == tektonv1.PipelineRunReasonRunning.String() || prReason == tektonv1.PipelineRunReasonPending.String() {
					logger.Infof("skipping cleaning PipelineRun %s since the conditions.reason is %s", dPrun.GetName(), prReason)
					continue
				}

				logger.Infof("cleaning old PipelineRun: %s", dPrun.GetName())
				err := r.run.Clients.Tekton.TektonV1().PipelineRuns(queue.Repository).Delete(ctx, dPrun.GetName(), metav1.DeleteOptions{})
				if err != nil {
					return err
				}

				if _, err := r.run.Clients.DB.RemovePipelineRun(repo, dPrun.GetName()); err != nil {
					return fmt.Errorf("db: failed to remove pipeline run: %w", err)
				}
			}
		}

		// err = r.kinteract.CleanupPipelines(ctx, logger, repo, pr, maxVal)
		// if err != nil {
		// 	return err
		// }
		return nil
	}

	// if annotation is not defined but default max-keep-run value is defined then use that
	if pacInfo.DefaultMaxKeepRuns > 0 {
		maxVal := pacInfo.DefaultMaxKeepRuns

		err := r.kinteract.CleanupPipelines(ctx, logger, repo, pr, maxVal)
		if err != nil {
			return err
		}
	}
	return nil
}

package service

import (
	"github.com/tektoncd/pipeline/pkg/apis/pipeline"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
)

// executionFinished reports whether the PipelineRun and everything it
// started, including finally and cancelled tasks, has stopped running. A child
// confirmed missing counts as finished; lookup errors are returned and never
// establish completion.
func (c *Controller) executionFinished(pr *tektonv1.PipelineRun) (bool, error) {
	if pr.Status.CompletionTime == nil || !pr.IsDone() {
		return false, nil
	}
	uid := string(pr.UID)
	for _, ref := range pr.Status.ChildReferences {
		switch ref.Kind {
		case "TaskRun":
			tr, err := c.taskRuns.TaskRuns(pr.Namespace).Get(ref.Name)
			if apierrors.IsNotFound(err) {
				continue
			}
			if err != nil {
				return false, err
			}
			if tr.Labels[pipeline.PipelineRunUIDLabelKey] == uid && !tr.IsDone() {
				return false, nil
			}
		case "CustomRun":
			cr, err := c.customRuns.CustomRuns(pr.Namespace).Get(ref.Name)
			if apierrors.IsNotFound(err) {
				continue
			}
			if err != nil {
				return false, err
			}
			if cr.Labels[pipeline.PipelineRunUIDLabelKey] == uid && !cr.IsDone() {
				return false, nil
			}
		}
	}
	pods, err := c.pods.Pods(pr.Namespace).List(labels.SelectorFromSet(labels.Set{pipeline.PipelineRunUIDLabelKey: uid}))
	if err != nil {
		return false, err
	}
	for _, p := range pods {
		if p.Status.Phase == corev1.PodPending || p.Status.Phase == corev1.PodRunning {
			return false, nil
		}
	}
	return true, nil
}

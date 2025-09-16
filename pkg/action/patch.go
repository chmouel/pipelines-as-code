package action

import (
	"context"
	"encoding/json"
	"fmt"

	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/tektoncd/pipeline/pkg/client/clientset/versioned"
	"go.uber.org/zap"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
)

// PatchPipelineRun patches a Tekton PipelineRun resource with the provided merge patch.
// It retries the patch operation on conflict, doubling the default retry parameters.
//
// Parameters:
// - ctx: The context for the patch operation.
// - logger: A SugaredLogger instance for logging information.
// - whatPatching: A string describing what is being patched, used for logging purposes.
// - tekton: A Tekton client interface for interacting with Tekton resources.
// - pr: The PipelineRun resource to be patched. If nil, the function returns nil.
// - mergePatch: A map representing the JSON merge patch to apply to the PipelineRun.
//
// Returns:
// - *tektonv1.PipelineRun: The patched PipelineRun resource, or the original PipelineRun if an error occurs.
// - error: An error if the patch operation fails after retries, or nil if successful.
//
// The function doubles the default retry parameters (steps, duration, factor, jitter) to handle conflicts more robustly.
// If the patch operation fails after retries, the original PipelineRun is returned along with the error.
func PatchPipelineRun(ctx context.Context, logger *zap.SugaredLogger, whatPatching string, tekton versioned.Interface, pr *tektonv1.PipelineRun, mergePatch map[string]any) (*tektonv1.PipelineRun, error) {
	if pr == nil {
		return nil, nil
	}

	patchBytes, err := json.Marshal(mergePatch)
	if err != nil {
		return pr, fmt.Errorf("marshal merge patch for %q: %w", whatPatching, err)
	}

	// Double steps and duration; leaving factor/jitter as defaults to avoid overly long backoff.
	backoff := retry.DefaultRetry
	backoff.Steps *= 2
	backoff.Duration *= 2

	var patchedPR *tektonv1.PipelineRun
	err = retry.OnError(backoff, apierrors.IsConflict, func() error {
		out, patchErr := tekton.TektonV1().PipelineRuns(pr.Namespace).Patch(
			ctx, pr.Name, types.MergePatchType, patchBytes, metav1.PatchOptions{},
		)
		if patchErr != nil {
			if apierrors.IsConflict(patchErr) {
				logger.Infof("conflict patching PipelineRun %s/%s with %s: %v; retrying",
					pr.Namespace, pr.Name, whatPatching, patchErr)
			} else {
				logger.Infof("failed to patch PipelineRun %s/%s with %s: %v",
					pr.Namespace, pr.Name, whatPatching, patchErr)
			}
			return patchErr
		}
		patchedPR = out
		logger.Infof("patched PipelineRun with %s: %s/%s", whatPatching, out.Namespace, out.Name)
		return nil
	})
	if err != nil {
		// Fix placeholder order
		return pr, fmt.Errorf("failed to patch PipelineRun %s/%s with %s: %w", pr.Namespace, pr.Name, whatPatching, err)
	}
	return patchedPR, nil
}

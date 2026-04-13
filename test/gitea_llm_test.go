//go:build e2e

package test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v3"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	tgitea "github.com/openshift-pipelines/pipelines-as-code/test/pkg/gitea"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestGiteaLLM tests the LLM analysis feature with a failing PipelineRun.
// Note: The YAML file is a PipelineRun definition that will fail at runtime (exit 1).
// The LLM analysis is triggered only after the PipelineRun executes and its status
// condition becomes False (see pkg/reconciler/reconciler.go:243).
func TestGiteaLLM(t *testing.T) {
	llmRoleName := "make the failure a beautiful success"
	topts := &tgitea.TestOpts{
		ExpectEvents: false,
		TargetEvent:  triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			// This PipelineRun will fail at runtime due to 'exit 1', triggering LLM analysis
			".tekton/pr.yaml": "testdata/failures/pipelinerun-exit-1.yaml",
		},
		CreateSecret: []corev1.Secret{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "llm-secret",
				},
				Data: map[string][]byte{
					"token": []byte("sk-xxxx"),
				},
			},
		},
		Settings: &v1alpha1.Settings{
			AIAnalysis: &v1alpha1.AIAnalysisConfig{
				Enabled:  true,
				Provider: "openai",
				APIURL:   "http://nonoai.pipelines-as-code:8765/v1",
				TokenSecretRef: &v1alpha1.Secret{
					Name: "llm-secret",
					Key:  "token",
				},
				Roles: []v1alpha1.AnalysisRole{
					{
						Name:         llmRoleName,
						Prompt:       "what is the meaning of life",
						ContextItems: &v1alpha1.ContextConfig{},
						Output:       "pr-comment",
					},
				},
			},
		},
	}
	ctx, f := tgitea.TestPR(t, topts)
	defer f()
	topts.Regexp = regexp.MustCompile(fmt.Sprintf(".*%s.*", llmRoleName))
	tgitea.WaitForPullRequestCommentGoldenMatch(t, topts, "gitea-llm-comment.golden")

	// Wait for the first PipelineRun to be fully reconciled (repo status updated).
	_, err := twait.UntilRepositoryUpdated(ctx, topts.ParamsRun.Clients, twait.Opts{
		RepoName:            topts.TargetNS,
		Namespace:           topts.TargetNS,
		MinNumberStatus:     1,
		PollTimeout:         twait.DefaultTimeout,
		TargetSHA:           topts.SHA,
		FailOnRepoCondition: "no-match",
	})
	assert.NilError(t, err)

	// Trigger a second PipelineRun via /retest.
	tgitea.PostCommentOnPullRequest(t, topts, "/retest")

	// Wait for the second PipelineRun to complete (repo gets 2 status entries).
	_, err = twait.UntilRepositoryUpdated(ctx, topts.ParamsRun.Clients, twait.Opts{
		RepoName:            topts.TargetNS,
		Namespace:           topts.TargetNS,
		MinNumberStatus:     2,
		PollTimeout:         twait.DefaultTimeout,
		TargetSHA:           topts.SHA,
		FailOnRepoCondition: "no-match",
	})
	assert.NilError(t, err)

	// The second LLM analysis should have updated the existing comment, not
	// created a duplicate. Verify exactly one comment carries the marker.
	comments, _, err := topts.GiteaCNX.Client().ListRepoIssueComments(
		topts.PullRequest.Base.Repository.Owner.UserName,
		topts.PullRequest.Base.Repository.Name,
		forgejo.ListIssueCommentOptions{})
	assert.NilError(t, err)

	llmMarker := fmt.Sprintf("<!-- llm-analysis-%s -->", llmRoleName)
	llmCommentCount := 0
	for _, c := range comments {
		if strings.Contains(c.Body, llmMarker) {
			llmCommentCount++
		}
	}
	assert.Equal(t, llmCommentCount, 1,
		"expected exactly 1 LLM analysis comment after retest, but found %d", llmCommentCount)
}

// Local Variables:
// compile-command: "go test -tags=e2e -v -run TestGiteaLLM ."
// End:

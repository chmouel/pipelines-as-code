//go:build e2e
// +build e2e

package test

import (
	"context"
	"net/http"
	"os"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/cctx"
	tgitlab "github.com/openshift-pipelines/pipelines-as-code/test/pkg/gitlab"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/payload"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/scm"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
	"github.com/tektoncd/pipeline/pkg/names"
	clientGitlab "gitlab.com/gitlab-org/api/client-go"
	"gotest.tools/v3/assert"
)

// TestGitlabOkToTestInThreadReply verifies that a /ok-to-test command placed
// in a reply within a discussion thread on a Merge Request is honored.
func TestGitlabOkToTestInThreadReply(t *testing.T) {
	if os.Getenv("NIGHTLY_E2E_TEST") != "true" {
		t.Skip("Skipping test since only enabled for nightly")
	}

	targetNS := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-ns")
	ctx := context.Background()
	runcnx, opts, glprovider, err := tgitlab.Setup(ctx)
	assert.NilError(t, err)
	ctx, err = cctx.GetControllerCtxInfo(ctx, runcnx)
	assert.NilError(t, err)
	runcnx.Clients.Log.Info("Testing GitLab /ok-to-test in thread replies")

	projectinfo, resp, err := glprovider.Client().Projects.GetProject(opts.ProjectID, nil)
	assert.NilError(t, err)
	if resp != nil && resp.StatusCode == http.StatusNotFound {
		t.Errorf("Repository %s not found in %s", opts.Organization, opts.Repo)
	}

	// Create Repository CRD
	err = tgitlab.CreateCRD(ctx, projectinfo, runcnx, opts, targetNS, nil)
	assert.NilError(t, err)

	// Create a basic PipelineRun
	entries, err := payload.GetEntries(map[string]string{
		".tekton/pipelinerun.yaml": "testdata/pipelinerun.yaml",
	}, targetNS, projectinfo.DefaultBranch, "Merge Request", map[string]string{})
	assert.NilError(t, err)

	// Create a branch with files and open a merge request
	targetRefName := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-test")
	gitCloneURL, err := scm.MakeGitCloneURL(projectinfo.WebURL, opts.UserName, opts.Password)
	assert.NilError(t, err)
	commitTitle := "Committing files from test on " + targetRefName
	scmOpts := &scm.Opts{
		GitURL:        gitCloneURL,
		CommitTitle:   commitTitle,
		Log:           runcnx.Clients.Log,
		WebURL:        projectinfo.WebURL,
		TargetRefName: targetRefName,
		BaseRefName:   projectinfo.DefaultBranch,
	}
	_ = scm.PushFilesToRefGit(t, scmOpts, entries)
	mrTitle := "TestMergeRequest - " + targetRefName
	mrID, err := tgitlab.CreateMR(glprovider.Client(), opts.ProjectID, targetRefName, projectinfo.DefaultBranch, mrTitle)
	assert.NilError(t, err)
	defer tgitlab.TearDown(ctx, t, runcnx, glprovider, mrID, targetRefName, targetNS, opts.ProjectID)

	// Create a discussion thread with an initial note
	disc, _, err := glprovider.Client().Discussions.CreateMergeRequestDiscussion(opts.ProjectID, mrID, &clientGitlab.CreateMergeRequestDiscussionOptions{
		Body: clientGitlab.Ptr("random initial note"),
	})
	assert.NilError(t, err)

	// Add a reply to the discussion containing /ok-to-test
	_, _, err = glprovider.Client().Discussions.AddMergeRequestDiscussionNote(opts.ProjectID, mrID, disc.ID, &clientGitlab.AddMergeRequestDiscussionNoteOptions{
		Body: clientGitlab.Ptr("/ok-to-test"),
	})
	assert.NilError(t, err)

	// Wait for repository status to reflect a successful run triggered by the comment
	waitOpts := twait.Opts{
		RepoName:        targetNS,
		Namespace:       targetNS,
		MinNumberStatus: 1,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       "",
	}
	_, err = twait.UntilRepositoryUpdated(ctx, runcnx.Clients, waitOpts)
	assert.NilError(t, err)
}

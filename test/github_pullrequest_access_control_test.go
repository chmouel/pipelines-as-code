//go:build e2e
// +build e2e

package test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	ghlib "github.com/google/go-github/v70/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	tgithub "github.com/openshift-pipelines/pipelines-as-code/test/pkg/github"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/options"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/payload"
	"github.com/tektoncd/pipeline/pkg/names"
	"gotest.tools/v3/assert"
)

func TestGithubSecondPullAccessControl(t *testing.T) {
	randomname := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-ns")
	ctx := context.TODO()
	ctx, runcnx, opts, ghcnx, err := tgithub.SetupPrimary(ctx, false)
	assert.NilError(t, err)

	ctx, _, ncopts, ncghcnx, err := tgithub.SetupSecondaryNonContributor(ctx, false)
	assert.NilError(t, err)
	repoinfo, resp, err := ncghcnx.Client.Repositories.Get(ctx, ncopts.Organization, ncopts.Repo)
	assert.NilError(t, err)
	if resp != nil && resp.StatusCode == http.StatusNotFound {
		t.Errorf("Repository %s not found in %s", ncopts.Organization, ncopts.Repo)
	}

	err = tgithub.CreateCRD(ctx, t, repoinfo, runcnx, opts, randomname)
	assert.NilError(t, err)

	yamlEntries := map[string]string{
		".tekton/pull_request.yaml": "testdata/pipelinerun.yaml",
	}
	entries, err := payload.GetEntries(yamlEntries, randomname, options.MainBranch, triggertype.PullRequest.String(),
		map[string]string{"TargetURL": repoinfo.GetHTMLURL(), "SourceURL": repoinfo.GetHTMLURL()})
	assert.NilError(t, err)
	commitTitle := "Testing Access Control between users"

	targetRefName := fmt.Sprintf("refs/heads/%s", randomname)
	sha, vref, err := tgithub.PushFilesToRef(ctx, ncghcnx.Client, commitTitle,
		repoinfo.GetDefaultBranch(), targetRefName, ncopts.Organization, ncopts.Repo, entries)
	assert.NilError(t, err)
	fmt.Println(fmt.Sprintf("%s:%s\n", ncopts.Organization, randomname))
	ghpr := &ghlib.NewPullRequest{
		Title: ghlib.Ptr(commitTitle),
		Head:  ghlib.Ptr(fmt.Sprintf("%s:%s", ncopts.Organization, randomname)),
		Base:  ghlib.Ptr(ghrepoinfo.GetDefaultBranch()),
		Body:  ghlib.Ptr("Add a new PR for testing"),
	}
	fmt.Printf("ghpr: %+v\n", ghpr)
	pr, _, err := ghcnx.Client.PullRequests.Create(ctx, opts.Organization, opts.Repo, ghpr)
	assert.NilError(t, err)
	runcnx.Clients.Log.Infof("Pull request created: %s", pr.GetHTMLURL())
	fmt.Printf("pr.GetNumber(): %v\n", pr.GetNumber())
	assert.NilError(t, err)
	fmt.Printf("sha: %v\n", sha)
	fmt.Printf("vref: %v\n", vref)
	fmt.Printf("prnum: %v\n", pr.GetNumber())
}

package test

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/opscomments"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/configmap"
	tgithub "github.com/openshift-pipelines/pipelines-as-code/test/pkg/github"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/options"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"

	"github.com/google/go-github/v71/github"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
	"gotest.tools/v3/golden"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/apis"
)

func TestGithubPullRequest(t *testing.T) {
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label:     "Github PullRequest",
		YamlFiles: []string{"testdata/pipelinerun.yaml"},
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)
}

func TestGithubPullRequestSecondController(t *testing.T) {
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label:            "Github Rerequest",
		YamlFiles:        []string{"testdata/pipelinerun.yaml"},
		SecondController: true,
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)
}

func TestGithubPullRequestMultiples(t *testing.T) {
	if os.Getenv("NIGHTLY_E2E_TEST") != "true" {
		t.Skip("Skipping test since only enabled for nightly")
	}
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label:     "Github multiple PullRequest",
		YamlFiles: []string{"testdata/pipelinerun.yaml", "testdata/pipelinerun-clone.yaml"},
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)
}

func TestGithubPullRequestMatchOnCEL(t *testing.T) {
	if os.Getenv("NIGHTLY_E2E_TEST") != "true" {
		t.Skip("Skipping test since only enabled for nightly")
	}
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label:     "Github CEL Match",
		YamlFiles: []string{"testdata/pipelinerun-cel-annotation.yaml"},
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)
}

func TestGithubPullRequestOnLabel(t *testing.T) {
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label:         "Github On Label",
		YamlFiles:     []string{"testdata/pipelinerun-on-label.yaml"},
		NoStatusCheck: true,
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)

	// wait a bit that GitHub processed or we will get double events
	time.Sleep(5 * time.Second)

	g.Cnx.Clients.Log.Infof("Creating a label bug on PullRequest")
	_, _, err := g.Provider.Client().Issues.AddLabelsToIssue(ctx,
		g.Options.Organization,
		g.Options.Repo, g.PRNumber,
		[]string{"bug"})
	assert.NilError(t, err)

	sopt := twait.SuccessOpt{
		Title:           g.CommitTitle,
		OnEvent:         triggertype.PullRequestLabeled.String(),
		TargetNS:        g.TargetNamespace,
		NumberofPRMatch: len(g.YamlFiles),
		SHA:             g.SHA,
	}
	twait.Succeeded(ctx, t, g.Cnx, g.Options, sopt)

	opt := github.ListOptions{}
	res := &github.ListCheckRunsResults{}
	resp := &github.Response{}
	counter := 0
	for {
		res, resp, err = g.Provider.Client().Checks.ListCheckRunsForRef(ctx, g.Options.Organization, g.Options.Repo, g.SHA, &github.ListCheckRunsOptions{
			AppID:       g.Provider.ApplicationID,
			ListOptions: opt,
		})
		assert.NilError(t, err)
		assert.Equal(t, resp.StatusCode, 200)
		if len(res.CheckRuns) > 0 {
			break
		}
		g.Cnx.Clients.Log.Infof("Waiting for the check run to be created")
		if counter > 10 {
			t.Errorf("Check run not created after 10 tries")
			break
		}
		time.Sleep(5 * time.Second)
	}
	assert.Equal(t, len(res.CheckRuns), 1)
	expected := fmt.Sprintf("%s / %s", settings.PACApplicationNameDefaultValue, "pipelinerun-on-label-")
	checkName := res.CheckRuns[0].GetName()
	assert.Assert(t, strings.HasPrefix(checkName, expected), "checkName %s != expected %s", checkName, expected)
}

func TestGithubPullRequestCELMatchOnTitle(t *testing.T) {
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label:     "Github CEL Match on Title",
		YamlFiles: []string{"testdata/pipelinerun-cel-annotation-for-title-match.yaml"},
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)
}

func TestGithubPullRequestWebhook(t *testing.T) {
	if os.Getenv("NIGHTLY_E2E_TEST") != "true" {
		t.Skip("Skipping test since only enabled for nightly")
	}
	if os.Getenv("TEST_GITHUB_REPO_OWNER_WEBHOOK") == "" {
		t.Skip("TEST_GITHUB_REPO_OWNER_WEBHOOK is not set")
		return
	}
	ctx := context.Background()

	g := &tgithub.PRTest{
		Label:     "Github PullRequest onWebhook",
		YamlFiles: []string{"testdata/pipelinerun.yaml"},
		Webhook:   true,
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)
}

func TestGithubSecondPullRequestBadYaml(t *testing.T) {
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label:            "Github PullRequest Bad Yaml",
		YamlFiles:        []string{"testdata/failures/bad-yaml.yaml"},
		SecondController: true,
		NoStatusCheck:    true,
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)

	maxLoop := 10
	for i := 0; i < maxLoop; i++ {
		comments, _, err := g.Provider.Client().Issues.ListComments(
			ctx, g.Options.Organization, g.Options.Repo, g.PRNumber,
			&github.IssueListCommentsOptions{})
		assert.NilError(t, err)

		if len(comments) > 0 {
			assert.Assert(t, len(comments) == 1, "Should have only one comment created we got way too many: %+v", comments)
			golden.Assert(t, comments[0].GetBody(), strings.ReplaceAll(fmt.Sprintf("%s.golden", t.Name()), "/", "-"))
			return
		}

		g.Cnx.Clients.Log.Infof("Looping %d/%d waiting for a comment to appear", i, maxLoop)
		time.Sleep(6 * time.Second)
	}

	t.Fatal("No comments with the pipelinerun error found on the pull request")
}

// TestGithubPullRequestInvalidSpecValues tests invalid field values of a PipelinRun and
// ensures that these validation errors are reported on UI.
func TestGithubPullRequestInvalidSpecValues(t *testing.T) {
	if os.Getenv("NIGHTLY_E2E_TEST") != "true" {
		t.Skip("Skipping test since only enabled for nightly")
	}
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label:            "Github Invalid Yaml",
		YamlFiles:        []string{"testdata/failures/invalid-timeouts-values-pipelinerun.yaml"},
		SecondController: true,
		NoStatusCheck:    true,
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)

	opt := github.ListOptions{}
	res := &github.ListCheckRunsResults{}
	resp := &github.Response{}
	var err error
	counter := 0
	for {
		res, resp, err = g.Provider.Client().Checks.ListCheckRunsForRef(ctx, g.Options.Organization, g.Options.Repo, g.SHA, &github.ListCheckRunsOptions{
			AppID:       g.Provider.ApplicationID,
			Status:      github.Ptr("completed"),
			ListOptions: opt,
		})
		assert.NilError(t, err)
		assert.Equal(t, resp.StatusCode, 200)
		if len(res.CheckRuns) > 0 {
			break
		}
		g.Cnx.Clients.Log.Infof("Waiting for the check run to be created")
		if counter > 10 {
			t.Errorf("Check run not created after 10 tries")
			break
		}
		time.Sleep(5 * time.Second)
	}

	assert.Equal(t, len(res.CheckRuns), 1)
	assert.Equal(t, res.CheckRuns[0].GetOutput().GetTitle(), "pipelinerun start failure")
	reg := regexp.MustCompile("Pipelines as Code.* has <b>failed</b>.")
	assert.Assert(t, cmp.Regexp(reg, res.CheckRuns[0].GetOutput().GetSummary()))
	reg = regexp.MustCompile(options.InvalidYamlErrorPattern)
	assert.Assert(t, cmp.Regexp(reg, res.CheckRuns[0].GetOutput().GetText()))
}

func TestGithubSecondTestExplicitelyNoMatchedPipelineRun(t *testing.T) {
	ctx := context.Background()
	g := tgithub.PRTest{
		Label:            "Github test implicit comment",
		YamlFiles:        []string{"testdata/pipelinerun-nomatch.yaml"},
		SecondController: true,
		NoStatusCheck:    true,
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)

	g.Cnx.Clients.Log.Infof("Creating /test no-match on PullRequest")
	_, _, err := g.Provider.Client().Issues.CreateComment(ctx,
		g.Options.Organization,
		g.Options.Repo, g.PRNumber,
		&github.IssueComment{Body: github.Ptr("/test no-match")})
	assert.NilError(t, err)
	sopt := twait.SuccessOpt{
		Title:           fmt.Sprintf("Testing %s with Github APPS integration on %s", g.Label, g.TargetNamespace),
		OnEvent:         opscomments.TestSingleCommentEventType.String(),
		TargetNS:        g.TargetNamespace,
		NumberofPRMatch: len(g.YamlFiles),
	}
	twait.Succeeded(ctx, t, g.Cnx, g.Options, sopt)
}

func TestGithubSecondCancelInProgress(t *testing.T) {
	ctx := context.Background()
	g := tgithub.PRTest{
		Label:            "Github cancel in progress",
		YamlFiles:        []string{"testdata/pipelinerun-cancel-in-progress.yaml"},
		SecondController: true,
		NoStatusCheck:    true,
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)

	g.Cnx.Clients.Log.Infof("Waiting for one pipelinerun to be created")
	waitOpts := twait.Opts{
		RepoName:        g.TargetNamespace,
		Namespace:       g.TargetNamespace,
		MinNumberStatus: 1,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       g.SHA,
	}
	err := twait.UntilPipelineRunCreated(ctx, g.Cnx.Clients, waitOpts)
	assert.NilError(t, err)
	time.Sleep(10 * time.Second)

	g.Cnx.Clients.Log.Infof("Creating /retest on PullRequest")
	_, _, err = g.Provider.Client().Issues.CreateComment(ctx, g.Options.Organization, g.Options.Repo, g.PRNumber,
		&github.IssueComment{Body: github.Ptr("/retest")})
	assert.NilError(t, err)

	g.Cnx.Clients.Log.Infof("Waiting for the two pipelinerun to be created")
	waitOpts = twait.Opts{
		RepoName:        g.TargetNamespace,
		Namespace:       g.TargetNamespace,
		MinNumberStatus: 2,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       g.SHA,
	}
	err = twait.UntilPipelineRunCreated(ctx, g.Cnx.Clients, waitOpts)
	assert.NilError(t, err)

	g.Cnx.Clients.Log.Infof("Sleeping for 10 seconds to let the pipelinerun to be canceled")

	i := 0
	foundCancelled := false
	for i < 10 {
		prs, err := g.Cnx.Clients.Tekton.TektonV1().PipelineRuns(g.TargetNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("%s=%s", keys.SHA, g.SHA),
		})
		assert.NilError(t, err)

		for _, pr := range prs.Items {
			if pr.GetStatusCondition() == nil {
				continue
			}
			if len(pr.Status.Conditions) == 0 {
				continue
			}
			if pr.Status.Conditions[0].Reason == "Cancelled" {
				g.Cnx.Clients.Log.Infof("PipelineRun %s has been canceled", pr.Name)
				foundCancelled = true
				break
			}
		}
		if foundCancelled {
			break
		}

		time.Sleep(5 * time.Second)
		i++
	}
	assert.Assert(t, foundCancelled, "No Pipelines has been found cancedl in NS %s", g.TargetNamespace)
}

func TestGithubSecondCancelInProgressPRClosed(t *testing.T) {
	ctx := context.Background()
	g := tgithub.PRTest{
		Label:            "Github cancel in progress while pr is closed",
		YamlFiles:        []string{"testdata/pipelinerun-cancel-in-progress.yaml"},
		SecondController: true,
		NoStatusCheck:    true,
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)

	g.Cnx.Clients.Log.Infof("Waiting for the two pipelinerun to be created")
	waitOpts := twait.Opts{
		RepoName:        g.TargetNamespace,
		Namespace:       g.TargetNamespace,
		MinNumberStatus: 1,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       g.SHA,
	}
	err := twait.UntilPipelineRunCreated(ctx, g.Cnx.Clients, waitOpts)
	assert.NilError(t, err)

	g.Cnx.Clients.Log.Infof("Closing the PullRequest")
	_, _, err = g.Provider.Client().PullRequests.Edit(ctx, g.Options.Organization, g.Options.Repo, g.PRNumber, &github.PullRequest{
		State: github.Ptr("closed"),
	})
	assert.NilError(t, err)

	g.Cnx.Clients.Log.Infof("Sleeping for 10 seconds to let the pipelinerun to be canceled")
	time.Sleep(10 * time.Second)

	g.Cnx.Clients.Log.Infof("Checking that the pipelinerun has been canceled")

	prs, err := g.Cnx.Clients.Tekton.TektonV1().PipelineRuns(g.TargetNamespace).List(context.Background(), metav1.ListOptions{})
	assert.NilError(t, err)
	assert.Equal(t, len(prs.Items), 1, "should have only one pipelinerun, but we have: %d", len(prs.Items))

	assert.Equal(t, prs.Items[0].GetStatusCondition().GetCondition(apis.ConditionSucceeded).GetReason(), "Cancelled", "should have been canceled")

	res, resp, err := g.Provider.Client().Checks.ListCheckRunsForRef(ctx, g.Options.Organization, g.Options.Repo, g.SHA, &github.ListCheckRunsOptions{
		AppID:       g.Provider.ApplicationID,
		ListOptions: github.ListOptions{},
	})
	assert.NilError(t, err)
	assert.Equal(t, resp.StatusCode, 200)

	assert.Equal(t, res.CheckRuns[0].GetConclusion(), "cancelled")
}

func TestGithubPullRequestNoOnLabelAnnotation(t *testing.T) {
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label:     "Github PullRequest",
		YamlFiles: []string{"testdata/pipelinerun-pr-cel-expression.yaml"},
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)

	g.Cnx.Clients.Log.Infof("Creating a label bug on PullRequest")
	_, _, err := g.Provider.Client().Issues.AddLabelsToIssue(ctx,
		g.Options.Organization,
		g.Options.Repo, g.PRNumber,
		[]string{"bug"})
	assert.NilError(t, err)

	// let's wait 10 secs and check every second that a PipelineRun is created or not.
	for i := 0; i < 10; i++ {
		prs, err := g.Cnx.Clients.Tekton.TektonV1().PipelineRuns(g.TargetNamespace).List(ctx, metav1.ListOptions{})
		assert.NilError(t, err)
		// after adding a label on the PR we need to make sure that it doesn't trigger another PipelineRun.
		assert.Equal(t, len(prs.Items), 1)
		time.Sleep(1 * time.Second)
	}
}

func TestGithubPullRequestNoPipelineRunCancelledOnPRClosed(t *testing.T) {
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label:         "Github PullRequest",
		YamlFiles:     []string{"testdata/pipelinerun-gitops.yaml"},
		NoStatusCheck: true,
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)

	g.Cnx.Clients.Log.Infof("Waiting for the two pipelinerun to be created")
	waitOpts := twait.Opts{
		RepoName:        g.TargetNamespace,
		Namespace:       g.TargetNamespace,
		MinNumberStatus: 1,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       g.SHA,
	}
	err := twait.UntilPipelineRunCreated(ctx, g.Cnx.Clients, waitOpts)
	assert.NilError(t, err)

	g.Cnx.Clients.Log.Infof("Closing the PullRequest")
	_, _, err = g.Provider.Client().PullRequests.Edit(ctx, g.Options.Organization, g.Options.Repo, g.PRNumber, &github.PullRequest{
		State: github.Ptr("closed"),
	})
	assert.NilError(t, err)

	isCancelled := false
	var prReason string

	for range 10 {
		prs, err := g.Cnx.Clients.Tekton.TektonV1().PipelineRuns(g.TargetNamespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			t.Logf("failed to list PipelineRuns: %v", err)
			time.Sleep(1 * time.Second)
			continue // try again
		}
		if len(prs.Items) != 1 {
			t.Logf("expected 1 PipelineRun, got %d", len(prs.Items))
			time.Sleep(1 * time.Second)
			continue // try again
		}
		// Check all conditions, get the right one
		conditions := prs.Items[0].Status.GetConditions()
		for _, c := range conditions {
			if c.Type == apis.ConditionSucceeded {
				prReason = c.Reason
				if prReason != string(tektonv1.PipelineRunReasonRunning) {
					isCancelled = true
					t.Logf("expected PipelineRun `Running`, got %s", prReason)
					break
				}
			}
		}
		time.Sleep(1 * time.Second)
	}
	assert.Equal(t, false, isCancelled, fmt.Sprintf("PipelineRun got cancelled while we wanted it `Running`, last reason: %v", prReason))
}

func TestGithubCancelInProgressSettingFromConfigMapOnPR(t *testing.T) {
	ctx := context.Background()
	ctx, runcnx, _, _, err := tgithub.Setup(ctx, false, false)
	assert.NilError(t, err)

	patchData := map[string]string{
		"enable-cancel-in-progress-on-pull-requests": "true",
	}

	configMapTearDown := configmap.ChangeGlobalConfig(ctx, t, runcnx, patchData)
	defer configMapTearDown()

	g := &tgithub.PRTest{
		Label:         "Github PullRequest",
		YamlFiles:     []string{"testdata/pipelinerun-gitops.yaml"},
		NoStatusCheck: true,
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)

	_, _, err = g.Provider.Client().Issues.CreateComment(ctx,
		g.Options.Organization,
		g.Options.Repo, g.PRNumber,
		&github.IssueComment{Body: github.Ptr("/retest")})
	assert.NilError(t, err)

	g.Cnx.Clients.Log.Infof("Waiting for the two pipelinerun to be created")
	waitOpts := twait.Opts{
		RepoName:        g.TargetNamespace,
		Namespace:       g.TargetNamespace,
		MinNumberStatus: 2,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       g.SHA,
	}

	err = twait.UntilPipelineRunCreated(ctx, g.Cnx.Clients, waitOpts)
	assert.NilError(t, err)

	// we want one PipelineRun to be cancelled
	waitOpts.MinNumberStatus = 1

	err = twait.UntilPipelineRunHasReason(ctx, g.Cnx.Clients, tektonv1.PipelineRunReasonCancelled, waitOpts)
	assert.NilError(t, err)
}

func TestGithubCancelInProgressSettingFromConfigMapOnPush(t *testing.T) {
	ctx := context.Background()
	ctx, runcnx, _, _, err := tgithub.Setup(ctx, false, false)
	assert.NilError(t, err)

	patchData := map[string]string{
		"enable-cancel-in-progress-on-push": "true",
	}

	configMapTearDown := configmap.ChangeGlobalConfig(ctx, t, runcnx, patchData)
	defer configMapTearDown()

	g := &tgithub.PRTest{
		Label:         "Github PullRequest",
		YamlFiles:     []string{"testdata/pipelinerun-gitops.yaml"},
		NoStatusCheck: true,
	}
	g.RunPushRequest(ctx, t)
	defer g.TearDown(ctx, t)

	comment := fmt.Sprintf("/retest branch:%s", g.TargetNamespace)
	_, _, err = g.Provider.Client().Repositories.CreateComment(ctx,
		g.Options.Organization,
		g.Options.Repo, g.SHA,
		&github.RepositoryComment{Body: github.Ptr(comment)})
	assert.NilError(t, err)

	g.Cnx.Clients.Log.Infof("Waiting for the two pipelinerun to be created")
	waitOpts := twait.Opts{
		RepoName:        g.TargetNamespace,
		Namespace:       g.TargetNamespace,
		MinNumberStatus: 2,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       g.SHA,
	}

	err = twait.UntilPipelineRunCreated(ctx, g.Cnx.Clients, waitOpts)
	assert.NilError(t, err)

	// we want one PipelineRun to be cancelled
	waitOpts.MinNumberStatus = 1

	err = twait.UntilPipelineRunHasReason(ctx, g.Cnx.Clients, tektonv1.PipelineRunReasonCancelled, waitOpts)
	assert.NilError(t, err)
}

func TestGithubPullandPushMatchTriggerOnlyPull(t *testing.T) {
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label:     "Github PullRequest",
		YamlFiles: []string{"testdata/pipelinerun-match-push-pullr.yaml"},
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)

	globalNs, _, err := params.GetInstallLocation(ctx, g.Cnx)
	assert.NilError(t, err)
	ctx = info.StoreNS(ctx, globalNs)

	reg := regexp.MustCompile(fmt.Sprintf("commit.*is part of pull request #%d.*skipping push event", g.PRNumber))
	maxLines := int64(100)
	err = twait.RegexpMatchingInControllerLog(ctx, g.Cnx, *reg, 20, "controller", &maxLines)
	assert.NilError(t, err)
}

func TestGithubDisableCommentsOnPR(t *testing.T) {
	if os.Getenv("TEST_GITHUB_REPO_OWNER_WEBHOOK") == "" {
		t.Skip("TEST_GITHUB_REPO_OWNER_WEBHOOK is not set")
		return
	}
	ctx := context.Background()

	g := &tgithub.PRTest{
		Label:     "Github PullRequest onWebhook",
		YamlFiles: []string{"testdata/pipelinerun.yaml"},
		Webhook:   true,
	}

	commentStrategy := v1alpha1.Settings{
		Github: &v1alpha1.GithubSettings{
			CommentStrategy: "disable_all",
		},
	}

	g.Options.Settings = commentStrategy
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)

	comments, _, _ := g.Provider.Client().Issues.ListComments(ctx, g.Options.Organization, g.Options.Repo, g.PRNumber, nil)
	commentRegexp := regexp.MustCompile(`.*Pipelines as Code CI/*`)
	successCommentsPost := 0
	for _, n := range comments {
		if commentRegexp.MatchString(*n.Body) {
			successCommentsPost++
		}
	}
	assert.Equal(t, 0, successCommentsPost)
}

// Local Variables:
// compile-command: "go test -tags=e2e -v -info TestGithubPullRequest$ ."
// End:

package github

import (
	"context"
	"os"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/github"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/options"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/setup"
)

func setupCommon(ctx context.Context, githubURL, githubRepoOwner, githubToken, controllerURL string, viaDirectWebhook bool) (context.Context, *params.Run, options.E2E, *github.Provider, error) {
	split := strings.Split(githubRepoOwner, "/")

	run := params.New()
	if err := run.Clients.NewClients(ctx, &run.Info); err != nil {
		return ctx, nil, options.E2E{}, github.New(), err
	}
	run.Info.Controller = info.GetControllerInfoFromEnvOrDefault()
	e2eoptions := options.E2E{
		Organization:  split[0],
		Repo:          split[1],
		DirectWebhook: viaDirectWebhook,
		ControllerURL: controllerURL,
	}
	gprovider := github.New()
	gprovider.Run = run
	event := info.NewEvent()
	event.Provider = &info.Provider{
		Token: githubToken,
		URL:   githubURL,
	}
	gprovider.Token = &githubToken

	if err := gprovider.SetClient(ctx, nil, event, nil, nil); err != nil {
		return ctx, nil, options.E2E{}, github.New(), err
	}

	return ctx, run, e2eoptions, gprovider, nil
}

func SetupPrimary(ctx context.Context, viaDirectWebhook bool) (context.Context, *params.Run, options.E2E, *github.Provider, error) {
	if err := setup.RequireEnvs(
		"TEST_EL_URL",
		"TEST_GITHUB_API_URL",
		"TEST_GITHUB_TOKEN",
		"TEST_GITHUB_REPO_OWNER_GITHUBAPP",
		"TEST_EL_WEBHOOK_SECRET",
	); err != nil {
		return ctx, nil, options.E2E{}, github.New(), err
	}

	githubURL := os.Getenv("TEST_GITHUB_API_URL")
	controllerURL := os.Getenv("TEST_EL_URL")
	githubRepoOwner := os.Getenv("TEST_GITHUB_REPO_OWNER_GITHUBAPP")
	if viaDirectWebhook {
		githubRepoOwner = os.Getenv("TEST_GITHUB_REPO_OWNER_WEBHOOK")
	}
	githubToken := os.Getenv("TEST_GITHUB_TOKEN")

	return setupCommon(ctx, githubURL, githubRepoOwner, githubToken, controllerURL, viaDirectWebhook)
}

func SetupSecondary(ctx context.Context, viaDirectWebhook bool) (context.Context, *params.Run, options.E2E, *github.Provider, error) {
	if err := setup.RequireEnvs(
		"TEST_GITHUB_SECOND_API_URL",
		"TEST_GITHUB_SECOND_REPO_OWNER_GITHUBAPP",
		"TEST_GITHUB_SECOND_TOKEN",
		"TEST_GITHUB_SECOND_EL_URL",
	); err != nil {
		return ctx, nil, options.E2E{}, github.New(), err
	}

	githubURL := os.Getenv("TEST_GITHUB_SECOND_API_URL")
	controllerURL := os.Getenv("TEST_GITHUB_SECOND_EL_URL")
	githubRepoOwner := os.Getenv("TEST_GITHUB_SECOND_REPO_OWNER_GITHUBAPP")
	githubToken := os.Getenv("TEST_GITHUB_SECOND_TOKEN")

	return setupCommon(ctx, githubURL, githubRepoOwner, githubToken, controllerURL, viaDirectWebhook)
}

func SetupSecondaryNonContributor(ctx context.Context, viaDirectWebhook bool) (context.Context, *params.Run, options.E2E, *github.Provider, error) {
	if err := setup.RequireEnvs(
		"TEST_GITHUB_SECOND_API_URL",
		"TEST_GITHUB_SECOND_NON_CONTRIBUTOR_REPO_OWNER",
		"TEST_GITHUB_SECOND_NON_CONTRIBUTOR_TOKEN",
		"TEST_GITHUB_SECOND_EL_URL",
	); err != nil {
		return ctx, nil, options.E2E{}, github.New(), err
	}

	githubURL := os.Getenv("TEST_GITHUB_SECOND_API_URL")
	controllerURL := os.Getenv("TEST_GITHUB_SECOND_EL_URL")
	githubRepoOwner := os.Getenv("TEST_GITHUB_SECOND_NON_CONTRIBUTOR_REPO_OWNER")
	githubToken := os.Getenv("TEST_GITHUB_SECOND_NON_CONTRIBUTOR_TOKEN")

	return setupCommon(ctx, githubURL, githubRepoOwner, githubToken, controllerURL, viaDirectWebhook)
}

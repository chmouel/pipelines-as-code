package settings

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"go.uber.org/zap"
)

const (
	ApplicationNameKey = "application-name"
	HubURLKey          = "hub-url"
	HubCatalogNameKey  = "hub-catalog-name"
	//nolint: gosec
	MaxKeepRunUpperLimitKey               = "max-keep-run-upper-limit"
	DefaultMaxKeepRunsKey                 = "default-max-keep-runs"
	RemoteTasksKey                        = "remote-tasks"
	BitbucketCloudCheckSourceIPKey        = "bitbucket-cloud-check-source-ip"
	BitbucketCloudAdditionalSourceIPKey   = "bitbucket-cloud-additional-source-ip"
	TektonDashboardURLKey                 = "tekton-dashboard-url"
	AutoConfigureNewGitHubRepoKey         = "auto-configure-new-github-repo"
	AutoConfigureRepoNamespaceTemplateKey = "auto-configure-repo-namespace-template"

	CustomConsoleNameKey      = "custom-console-name"
	CustomConsoleURLKey       = "custom-console-url"
	CustomConsolePRDetailKey  = "custom-console-url-pr-details"
	CustomConsolePRTaskLogKey = "custom-console-url-pr-tasklog"

	SecretAutoCreateKey                          = "secret-auto-create"
	secretAutoCreateDefaultValue                 = "true"
	SecretGhAppTokenRepoScopedKey                = "secret-github-app-token-scoped" //nolint: gosec
	secretGhAppTokenRepoScopedDefaultValue       = "true"
	SecretGhAppTokenScopedExtraReposKey          = "secret-github-app-scope-extra-repos" //nolint: gosec
	secretGhAppTokenScopedExtraReposDefaultValue = ""                                    //nolint: gosec

	remoteTasksDefaultValue                 = "true"
	bitbucketCloudCheckSourceIPDefaultValue = "true"
	PACApplicationNameDefaultValue          = "Pipelines as Code CI"
	HubURLDefaultValue                      = "https://api.hub.tekton.dev/v1"
	HubCatalogNameDefaultValue              = "tekton"
	AutoConfigureNewGitHubRepoDefaultValue  = "false"

	ErrorLogSnippetKey   = "error-log-snippet"
	errorLogSnippetValue = "true"

	ErrorDetectionKey   = "error-detection-from-container-logs"
	errorDetectionValue = "true"

	ErrorDetectionNumberOfLinesKey   = "error-detection-max-number-of-lines"
	errorDetectionNumberOfLinesValue = 50

	ErrorDetectionSimpleRegexpKey   = "error-detection-simple-regexp"
	errorDetectionSimpleRegexpValue = `^(?P<filename>[^:]*):(?P<line>[0-9]+):(?P<column>[0-9]+):([ ]*)?(?P<error>.*)`

	RememberOKToTestKey   = "remember-ok-to-test"
	rememberOKToTestValue = "true"
)

var (
	TknBinaryName       = `tkn`
	hubCatalogNameRegex = regexp.MustCompile(`^catalog-(\d+)-`)
)

type HubCatalog struct {
	ID   string
	Name string
	URL  string
}

type Settings struct {
	ApplicationName string
	// HubURL                             string
	// HubCatalogName                     string
	HubCatalogs                        *sync.Map
	RemoteTasks                        bool
	MaxKeepRunsUpperLimit              int
	DefaultMaxKeepRuns                 int
	BitbucketCloudCheckSourceIP        bool
	BitbucketCloudAdditionalSourceIP   string
	TektonDashboardURL                 string
	AutoConfigureNewGitHubRepo         bool
	AutoConfigureRepoNamespaceTemplate string

	SecretAutoCreation               bool
	SecretGHAppRepoScoped            bool
	SecretGhAppTokenScopedExtraRepos string

	ErrorLogSnippet             bool
	ErrorDetection              bool
	ErrorDetectionNumberOfLines int
	ErrorDetectionSimpleRegexp  string

	CustomConsoleName      string
	CustomConsoleURL       string
	CustomConsolePRdetail  string
	CustomConsolePRTaskLog string

	RememberOKToTest bool
}

func ConfigToSettings(logger *zap.SugaredLogger, setting *Settings, config map[string]string) error {
	// pass through defaulting
	SetDefaults(config)
	setting.HubCatalogs = getHubCatalogs(logger, config)

	// validate fields
	if err := Validate(config); err != nil {
		return fmt.Errorf("config validation failed: %w", err)
	}

	if setting.ApplicationName != config[ApplicationNameKey] {
		setting.ApplicationName = config[ApplicationNameKey]
	}

	secretAutoCreate := StringToBool(config[SecretAutoCreateKey])
	if setting.SecretAutoCreation != secretAutoCreate {
		setting.SecretAutoCreation = secretAutoCreate
	}

	secretGHAppRepoScoped := StringToBool(config[SecretGhAppTokenRepoScopedKey])
	if setting.SecretGHAppRepoScoped != secretGHAppRepoScoped {
		setting.SecretGHAppRepoScoped = secretGHAppRepoScoped
	}

	secretGHAppScopedExtraRepos := config[SecretGhAppTokenScopedExtraReposKey]
	if setting.SecretGhAppTokenScopedExtraRepos != secretGHAppScopedExtraRepos {
		setting.SecretGhAppTokenScopedExtraRepos = secretGHAppScopedExtraRepos
	}

	value, _ := setting.HubCatalogs.Load("default")
	catalogDefault, ok := value.(HubCatalog)
	if ok {
		if catalogDefault.URL != config[HubURLKey] {
			catalogDefault.URL = config[HubURLKey]
		}
		if catalogDefault.Name != config[HubCatalogNameKey] {
			catalogDefault.Name = config[HubCatalogNameKey]
		}
	}
	setting.HubCatalogs.Store("default", catalogDefault)
	// TODO: detect changes in extra hub catalogs

	remoteTask := StringToBool(config[RemoteTasksKey])
	if setting.RemoteTasks != remoteTask {
		setting.RemoteTasks = remoteTask
	}
	maxKeepRunUpperLimit, _ := strconv.Atoi(config[MaxKeepRunUpperLimitKey])
	if setting.MaxKeepRunsUpperLimit != maxKeepRunUpperLimit {
		setting.MaxKeepRunsUpperLimit = maxKeepRunUpperLimit
	}
	defaultMaxKeepRun, _ := strconv.Atoi(config[DefaultMaxKeepRunsKey])
	if setting.DefaultMaxKeepRuns != defaultMaxKeepRun {
		setting.DefaultMaxKeepRuns = defaultMaxKeepRun
	}
	check := StringToBool(config[BitbucketCloudCheckSourceIPKey])
	if setting.BitbucketCloudCheckSourceIP != check {
		setting.BitbucketCloudCheckSourceIP = check
	}
	if setting.BitbucketCloudAdditionalSourceIP != config[BitbucketCloudAdditionalSourceIPKey] {
		setting.BitbucketCloudAdditionalSourceIP = config[BitbucketCloudAdditionalSourceIPKey]
	}
	if setting.TektonDashboardURL != config[TektonDashboardURLKey] {
		setting.TektonDashboardURL = config[TektonDashboardURLKey]
	}
	autoConfigure := StringToBool(config[AutoConfigureNewGitHubRepoKey])
	if setting.AutoConfigureNewGitHubRepo != autoConfigure {
		setting.AutoConfigureNewGitHubRepo = autoConfigure
	}
	if setting.AutoConfigureRepoNamespaceTemplate != config[AutoConfigureRepoNamespaceTemplateKey] {
		setting.AutoConfigureRepoNamespaceTemplate = config[AutoConfigureRepoNamespaceTemplateKey]
	}

	errorLogSnippet := StringToBool(config[ErrorLogSnippetKey])
	if setting.ErrorLogSnippet != errorLogSnippet {
		setting.ErrorLogSnippet = errorLogSnippet
	}

	errorDetection := StringToBool(config[ErrorDetectionKey])
	if setting.ErrorDetection != errorDetection {
		setting.ErrorDetection = errorDetection
	}

	errorDetectNumberOfLines, _ := strconv.Atoi(config[ErrorDetectionNumberOfLinesKey])
	if setting.ErrorDetection && setting.ErrorDetectionNumberOfLines != errorDetectNumberOfLines {
		setting.ErrorDetectionNumberOfLines = errorDetectNumberOfLines
	}

	if setting.ErrorDetection && setting.ErrorDetectionSimpleRegexp != strings.TrimSpace(config[ErrorDetectionSimpleRegexpKey]) {
		// replace double backslash with single backslash because kube configmap is giving us things double backslashes
		setting.ErrorDetectionSimpleRegexp = strings.TrimSpace(config[ErrorDetectionSimpleRegexpKey])
	}

	if setting.CustomConsoleName != config[CustomConsoleNameKey] {
		setting.CustomConsoleName = config[CustomConsoleNameKey]
	}

	if setting.CustomConsoleURL != config[CustomConsoleURLKey] {
		setting.CustomConsoleURL = config[CustomConsoleURLKey]
	}

	if setting.CustomConsolePRdetail != config[CustomConsolePRDetailKey] {
		setting.CustomConsolePRdetail = config[CustomConsolePRDetailKey]
	}

	if setting.CustomConsolePRTaskLog != config[CustomConsolePRTaskLogKey] {
		setting.CustomConsolePRTaskLog = config[CustomConsolePRTaskLogKey]
	}

	rememberOKToTest := StringToBool(config[RememberOKToTestKey])
	if setting.RememberOKToTest != rememberOKToTest {
		setting.RememberOKToTest = rememberOKToTest
	}

	return nil
}

func StringToBool(s string) bool {
	if strings.ToLower(s) == "true" ||
		strings.ToLower(s) == "yes" || s == "1" {
		return true
	}
	return false
}

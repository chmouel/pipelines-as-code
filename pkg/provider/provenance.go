package provider

import (
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"go.uber.org/zap"
)

var DefaultBranchSetting = "default_branch"

func GetProvenance(event *info.Event, provenance string, logger *zap.SugaredLogger) string {
	revision := event.SHA

	if provenance == DefaultBranchSetting {
		revision = event.DefaultBranch
		logger.Infof("Using PipelineRun definition from default_branch: %s", event.DefaultBranch)
	} else if provenance != "" {
		revision = provenance
		logger.Infof("Using PipelineRun definition from source branch %s", provenance)
	} else if event.EventType == triggertype.PullRequest.String() {
		logger.Infof("Using PipelineRun definition from source pull request %s/%s#%d SHA on %s", event.Organization, event.Repository, event.PullRequestNumber, event.SHA)
	} else {
		logger.Infof("Using definition from %s/%s SHA: %s", event.Organization, event.Repository, event.SHA)
	}
	return revision
}

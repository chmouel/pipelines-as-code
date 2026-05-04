package customparams

import (
	"context"
	"fmt"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/changedfiles"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/opscomments"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"go.uber.org/zap"
)

func (p *CustomParams) getChangedFiles(ctx context.Context) changedfiles.ChangedFiles {
	if p.vcx == nil {
		return changedfiles.ChangedFiles{}
	}
	changedFiles, err := p.vcx.GetFiles(ctx, p.event)
	if err != nil {
		p.eventEmitter.EmitMessage(p.repo, zap.ErrorLevel, "ParamsError", fmt.Sprintf("error getting changed files: %s", err.Error()))
		return changedfiles.ChangedFiles{}
	}
	changedFiles.RemoveDuplicates()
	return changedFiles
}

// MakeStandardParams returns the standard PAC params derived from event and repository metadata.
func MakeStandardParams(event *info.Event, repo *v1alpha1.Repository) map[string]string {
	repoURL := event.URL
	// On bitbucket data center you are have a special url for checking it out, they
	// seemed to fix it in 2.0 but i guess we have to live with this until then.
	if event.CloneURL != "" {
		repoURL = event.CloneURL
	}

	triggerCommentAsSingleLine := strings.ReplaceAll(strings.ReplaceAll(event.TriggerComment, "\r\n", "\\n"), "\n", "\\n")
	pullRequestLabels := strings.Join(event.PullRequestLabel, "\\n")

	gitTag := ""
	if strings.HasPrefix(event.BaseBranch, "refs/tags/") {
		gitTag = strings.TrimPrefix(event.BaseBranch, "refs/tags/")
	}

	eventType := event.EventType
	if eventType != opscomments.OnCommentEventType.String() && opscomments.IsAnyOpsEventType(eventType) {
		eventType = triggertype.PullRequest.String()
	}

	targetNamespace := ""
	if repo != nil {
		targetNamespace = repo.GetNamespace()
	}

	params := map[string]string{
		"revision":            event.SHA,
		"repo_url":            repoURL,
		"repo_owner":          strings.ToLower(event.Organization),
		"repo_name":           strings.ToLower(event.Repository),
		"target_branch":       formatting.SanitizeBranch(event.BaseBranch),
		"source_branch":       formatting.SanitizeBranch(event.HeadBranch),
		"git_tag":             gitTag,
		"source_url":          event.HeadURL,
		"sender":              strings.ToLower(event.Sender),
		"target_namespace":    targetNamespace,
		"event_type":          eventType,
		"trigger_comment":     triggerCommentAsSingleLine,
		"pull_request_labels": pullRequestLabels,
	}
	if event.PullRequestNumber != 0 {
		params["pull_request_number"] = fmt.Sprintf("%d", event.PullRequestNumber)
	}
	return params
}

// makeStandardParamsFromEvent will create a map of standard params out of the event.
func (p *CustomParams) makeStandardParamsFromEvent(ctx context.Context) (map[string]string, map[string]any) {
	changedFiles := p.getChangedFiles(ctx)
	params := MakeStandardParams(p.event, p.repo)
	if p.event.EventType != opscomments.OnCommentEventType.String() && opscomments.IsAnyOpsEventType(p.event.EventType) && p.eventEmitter != nil {
		p.eventEmitter.EmitMessage(p.repo, zap.WarnLevel, "DeprecatedOpsComment",
			fmt.Sprintf("the %s event type is deprecated, this will be changed to %s in the future",
				p.event.EventType, triggertype.PullRequest.String()))
	}

	return params, map[string]any{
		"all":      changedFiles.All,
		"added":    changedFiles.Added,
		"deleted":  changedFiles.Deleted,
		"modified": changedFiles.Modified,
		"renamed":  changedFiles.Renamed,
	}
}

package cel

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/google/go-github/v71/github"
	pkgcel "github.com/openshift-pipelines/pipelines-as-code/pkg/cel"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"github.com/spf13/cobra"
)

const (
	bodyFileFlag    = "body"
	headersFileFlag = "headers"
	providerFlag    = "provider"
)

func eventFromGitHub(body []byte, headers map[string]string) (*info.Event, error) {
	event := info.NewEvent()
	event.EventType = headers["X-GitHub-Event"]
	event.Request.Payload = body
	event.Request.Header = http.Header{}
	for k, v := range headers {
		event.Request.Header.Set(k, v)
	}

	ghEvent, err := github.ParseWebHook(event.EventType, body)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(body, &ghEvent)

	switch e := ghEvent.(type) {
	case *github.PushEvent:
		event.TriggerTarget = triggertype.Push
		event.Organization = e.GetRepo().GetOwner().GetLogin()
		event.Repository = e.GetRepo().GetName()
		event.DefaultBranch = e.GetRepo().GetDefaultBranch()
		event.URL = e.GetRepo().GetHTMLURL()
		sha := e.GetHeadCommit().GetID()
		if sha == "" {
			sha = e.GetAfter()
		}
		event.SHA = sha
		event.SHAURL = e.GetHeadCommit().GetURL()
		event.SHATitle = e.GetHeadCommit().GetMessage()
		event.Sender = e.GetSender().GetLogin()
		event.BaseBranch = e.GetRef()
		event.HeadBranch = event.BaseBranch
		event.BaseURL = event.URL
		event.HeadURL = event.URL
	case *github.PullRequestEvent:
		event.TriggerTarget = triggertype.PullRequest
		event.Organization = e.GetRepo().GetOwner().GetLogin()
		event.Repository = e.GetRepo().GetName()
		event.DefaultBranch = e.GetRepo().GetDefaultBranch()
		event.URL = e.GetRepo().GetHTMLURL()
		event.SHA = e.GetPullRequest().Head.GetSHA()
		event.BaseBranch = e.GetPullRequest().Base.GetRef()
		event.HeadBranch = e.GetPullRequest().Head.GetRef()
		event.BaseURL = e.GetPullRequest().Base.GetRepo().GetHTMLURL()
		event.HeadURL = e.GetPullRequest().Head.GetRepo().GetHTMLURL()
		event.Sender = e.GetPullRequest().GetUser().GetLogin()
		event.PullRequestNumber = e.GetPullRequest().GetNumber()
		event.PullRequestTitle = e.GetPullRequest().GetTitle()
		for _, l := range e.GetPullRequest().Labels {
			event.PullRequestLabel = append(event.PullRequestLabel, l.GetName())
		}
	case *github.IssueCommentEvent:
		event.TriggerTarget = triggertype.PullRequest
		if e.GetRepo() != nil {
			event.Organization = e.GetRepo().GetOwner().GetLogin()
			event.Repository = e.GetRepo().GetName()
			event.DefaultBranch = e.GetRepo().GetDefaultBranch()
			event.URL = e.GetRepo().GetHTMLURL()
		}
		event.Sender = e.GetSender().GetLogin()
		event.TriggerComment = e.GetComment().GetBody()
		if pr := e.GetIssue().GetPullRequestLinks(); pr != nil {
			num, err := strconv.Atoi(path.Base(pr.GetHTMLURL()))
			if err == nil {
				event.PullRequestNumber = num
			}
		}
	case *github.CommitCommentEvent:
		event.TriggerTarget = triggertype.Push
		event.Organization = e.GetRepo().GetOwner().GetLogin()
		event.Repository = e.GetRepo().GetName()
		event.DefaultBranch = e.GetRepo().GetDefaultBranch()
		event.URL = e.GetRepo().GetHTMLURL()
		event.Sender = e.GetSender().GetLogin()
		event.SHA = e.GetComment().GetCommitID()
		event.SHAURL = e.GetComment().GetHTMLURL()
		event.HeadBranch = event.DefaultBranch
		event.BaseBranch = event.DefaultBranch
		event.HeadURL = event.URL
		event.BaseURL = event.URL
		event.TriggerComment = e.GetComment().GetBody()
	default:
		return nil, fmt.Errorf("unsupported github event %T", e)
	}
	return event, nil
}

func pacParamsFromEvent(event *info.Event) map[string]string {
	repoURL := event.URL
	if event.CloneURL != "" {
		repoURL = event.CloneURL
	}
	gitTag := ""
	if strings.HasPrefix(event.BaseBranch, "refs/tags/") {
		gitTag = strings.TrimPrefix(event.BaseBranch, "refs/tags/")
	}
	triggerComment := strings.ReplaceAll(strings.ReplaceAll(event.TriggerComment, "\r\n", "\\n"), "\n", "\\n")
	pullRequestLabels := strings.Join(event.PullRequestLabel, "\n")
	return map[string]string{
		"revision":            event.SHA,
		"repo_url":            repoURL,
		"repo_owner":          strings.ToLower(event.Organization),
		"repo_name":           strings.ToLower(event.Repository),
		"target_branch":       formatting.SanitizeBranch(event.BaseBranch),
		"source_branch":       formatting.SanitizeBranch(event.HeadBranch),
		"git_tag":             gitTag,
		"source_url":          event.HeadURL,
		"sender":              strings.ToLower(event.Sender),
		"target_namespace":    "",
		"event_type":          event.EventType,
		"trigger_comment":     triggerComment,
		"pull_request_labels": pullRequestLabels,
	}
}

func Command(ioStreams *cli.IOStreams) *cobra.Command {
	var bodyFile, headersFile, provider string

	cmd := &cobra.Command{
		Use:   "cel",
		Short: "Evaluate CEL expressions interactively",
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]any{}
			headers := map[string]string{}
			var bodyBytes []byte

			if bodyFile != "" {
				b, err := os.ReadFile(bodyFile)
				if err != nil {
					return err
				}
				bodyBytes = b
				if err := json.Unmarshal(b, &body); err != nil {
					return err
				}
			}

			if headersFile != "" {
				b, err := os.ReadFile(headersFile)
				if err != nil {
					return err
				}
				if err := json.Unmarshal(b, &headers); err != nil {
					return err
				}
			}

			pacParams := map[string]string{}
			switch provider {
			case "github":
				event, err := eventFromGitHub(bodyBytes, headers)
				if err != nil {
					return err
				}
				pacParams = pacParamsFromEvent(event)
			default:
				return fmt.Errorf("unsupported provider %s", provider)
			}

			for {
				var expr string
				if err := survey.AskOne(&survey.Input{Message: "CEL expression"}, &expr); err != nil {
					return err
				}
				if expr == "" {
					break
				}
				val, err := pkgcel.Value(expr, body, headers, pacParams, map[string]any{})
				if err != nil {
					fmt.Fprintln(ioStreams.Out, err)
				} else {
					fmt.Fprintf(ioStreams.Out, "%v\n", val)
				}
			}
			return nil
		},
		Annotations: map[string]string{"commandType": "main"},
	}

	cmd.Flags().StringVarP(&bodyFile, bodyFileFlag, "b", "", "path to JSON body file")
	cmd.Flags().StringVarP(&headersFile, headersFileFlag, "H", "", "path to JSON headers file")
	cmd.Flags().StringVarP(&provider, providerFlag, "p", "github", "payload provider (github)")
	return cmd
}

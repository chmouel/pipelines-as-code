package adapter

import (
	"bytes"
	"context"
	"fmt"
	"net/http"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/pipelineascode"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	"go.uber.org/zap"
)

type sinker struct {
	run         *params.Run
	vcx         provider.Interface
	kint        kubeinteraction.Interface
	event       *info.Event
	logger      *zap.SugaredLogger
	payload     []byte
	pacInfo     *info.PacOpts
	globalRepo  *v1alpha1.Repository
	minimalInfo *provider.MinimalEventInfo
}

func (s *sinker) processEventPayload(ctx context.Context, request *http.Request) error {
	var err error
	s.event, err = s.vcx.ParsePayload(ctx, s.run, request, string(s.payload))
	if err != nil {
		s.logger.Errorf("failed to parse event: %v", err)
		// Report the parse error to the user via Git provider or controller events
		s.reportParseError(ctx, err)
		return err
	}

	// Enhanced structured logging with source repository context for operators
	logFields := []interface{}{
		"event-sha", s.event.SHA,
		"event-type", s.event.EventType,
		"source-repo-url", s.event.URL,
	}

	// Add branch information if available
	if s.event.BaseBranch != "" {
		logFields = append(logFields, "target-branch", s.event.BaseBranch)
	}
	// For PRs, also include source branch if different
	if s.event.HeadBranch != "" && s.event.HeadBranch != s.event.BaseBranch {
		logFields = append(logFields, "source-branch", s.event.HeadBranch)
	}

	s.logger = s.logger.With(logFields...)
	s.vcx.SetLogger(s.logger)

	s.event.Request = &info.Request{
		Header:  request.Header,
		Payload: bytes.TrimSpace(s.payload),
	}
	return nil
}

func (s *sinker) processEvent(ctx context.Context, request *http.Request) error {
	if s.event.EventType == "incoming" {
		if request.Header.Get("X-GitHub-Enterprise-Host") != "" {
			s.event.Provider.URL = request.Header.Get("X-GitHub-Enterprise-Host")
			s.event.GHEURL = request.Header.Get("X-GitHub-Enterprise-Host")
		}
	} else {
		if err := s.processEventPayload(ctx, request); err != nil {
			return err
		}
	}

	p := pipelineascode.NewPacs(s.event, s.vcx, s.run, s.pacInfo, s.kint, s.logger, s.globalRepo)
	return p.Run(ctx)
}

// reportParseError reports a payload parsing error to the user.
// It attempts to post a status to the Git provider if we have enough info (token, SHA, org, repo).
// It also emits a Kubernetes event to the controller namespace for visibility.
func (s *sinker) reportParseError(ctx context.Context, parseErr error) {
	if s.minimalInfo == nil {
		// No minimal info available, just emit to controller namespace
		emitter := events.NewEventEmitter(s.run.Clients.Kube, s.logger)
		emitter.SetControllerNamespace(s.run.Info.Kube.Namespace)
		emitter.EmitControllerEvent("PayloadParseError", parseErr.Error(), "", "")
		return
	}

	// Try to post status to Git provider if we have a token and SHA
	// Note: For GitLab, Organization contains the full path (org/repo) and Repository is empty
	// For other providers, both Organization and Repository are populated
	if s.minimalInfo.Token != "" && s.minimalInfo.SHA != "" &&
		(s.minimalInfo.Organization != "" || s.minimalInfo.Repository != "") {
		// Create a minimal event for CreateStatus
		event := &info.Event{
			Organization: s.minimalInfo.Organization,
			Repository:   s.minimalInfo.Repository,
			SHA:          s.minimalInfo.SHA,
			URL:          s.minimalInfo.URL,
			EventType:    s.minimalInfo.EventType,
			Provider: &info.Provider{
				Token: s.minimalInfo.Token,
				URL:   s.minimalInfo.GHEURL,
			},
		}

		status := provider.StatusOpts{
			Status:     "completed",
			Conclusion: "failure",
			Title:      "Webhook Processing Error",
			Text:       fmt.Sprintf("Failed to process webhook payload: %v", parseErr),
		}

		if err := s.vcx.CreateStatus(ctx, event, status); err != nil {
			s.logger.Warnf("failed to create status for parse error: %v", err)
		} else {
			s.logger.Info("posted error status to Git provider for payload parse error")
		}
	}

	// Also emit to controller namespace for visibility
	emitter := events.NewEventEmitter(s.run.Clients.Kube, s.logger)
	emitter.SetControllerNamespace(s.run.Info.Kube.Namespace)
	emitter.EmitControllerEvent("PayloadParseError", parseErr.Error(), s.minimalInfo.URL, s.minimalInfo.SHA)
}

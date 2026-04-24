package pipelineascode

import (
	"context"
	"fmt"
	"strings"

	apipac "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	ktypes "github.com/openshift-pipelines/pipelines-as-code/pkg/secrets/types"
	"go.uber.org/zap"
)

const (
	DefaultGitProviderSecretKey                  = "provider.token"
	DefaultGitProviderWebhookSecretKey           = "webhook.secret"
	defaultPipelinesAscodeSecretWebhookSecretKey = "webhook.secret"
)

type SecretFromRepository struct {
	K8int       kubeinteraction.Interface
	Config      *info.ProviderConfig
	Event       *info.Event
	Repo        *apipac.Repository
	WebhookType string
	Namespace   string
	Logger      *zap.SugaredLogger
}

// Get grab the secret from the repository CRD.
func (s *SecretFromRepository) Get(ctx context.Context) error {
	var err error
	if s.Repo.Spec.GitProvider == nil {
		return fmt.Errorf("failed to find git_provider details in repository spec: %v/%v", s.Repo.Namespace, s.Repo.Name)
	}
	if s.Repo.Spec.GitProvider.URL == "" {
		s.Repo.Spec.GitProvider.URL = s.Config.APIURL
	} else {
		s.Event.Provider.URL = s.Repo.Spec.GitProvider.URL
	}

	if s.Repo.Spec.GitProvider.Secret == nil {
		return fmt.Errorf("failed to find secret in git_provider section in repository spec: %v/%v", s.Repo.Namespace, s.Repo.Name)
	}
	gitProviderSecretKey := s.Repo.Spec.GitProvider.Secret.Key
	if gitProviderSecretKey == "" {
		gitProviderSecretKey = DefaultGitProviderSecretKey
	}

	if s.Event.Provider.Token, err = s.K8int.GetSecret(ctx, ktypes.GetSecretOpt{
		Namespace: s.Namespace,
		Name:      s.Repo.Spec.GitProvider.Secret.Name,
		Key:       gitProviderSecretKey,
	}); err != nil {
		return err
	}

	if s.Event.Provider.Token == "" {
		return nil
	}
	s.Event.Provider.User = s.Repo.Spec.GitProvider.User

	if s.Repo.Spec.GitProvider.WebhookSecret == nil {
		return nil
	}

	gitProviderWebhookSecretKey := s.Repo.Spec.GitProvider.WebhookSecret.Key
	if gitProviderWebhookSecretKey == "" {
		gitProviderWebhookSecretKey = DefaultGitProviderWebhookSecretKey
	}
	logmsg := fmt.Sprintf("Using git provider %s: apiurl=%s user=%s token-secret=%s token-key=%s",
		s.WebhookType,
		s.Repo.Spec.GitProvider.URL,
		s.Repo.Spec.GitProvider.User,
		s.Repo.Spec.GitProvider.Secret.Name,
		gitProviderSecretKey)
	if s.Event.Provider.WebhookSecret, err = s.K8int.GetSecret(ctx, ktypes.GetSecretOpt{
		Namespace: s.Namespace,
		Name:      s.Repo.Spec.GitProvider.WebhookSecret.Name,
		Key:       gitProviderWebhookSecretKey,
	}); err != nil {
		return err
	}
	if s.Event.Provider.WebhookSecret != "" {
		s.Event.Provider.WebhookSecretFromRepo = true
		logmsg += fmt.Sprintf(" webhook-secret=%s webhook-key=%s",
			s.Repo.Spec.GitProvider.WebhookSecret.Name,
			gitProviderWebhookSecretKey)
	} else {
		logmsg += " webhook-secret=NOTFOUND"
	}
	s.Logger.Infof(logmsg)
	return nil
}

// GetCurrentNSWebhookSecret get secret from namespace as stored on context.
func GetCurrentNSWebhookSecret(ctx context.Context, k8int kubeinteraction.Interface, run *params.Run) (string, error) {
	ns := info.GetNS(ctx)
	s, err := k8int.GetSecret(ctx, ktypes.GetSecretOpt{
		Namespace: ns,
		Name:      run.Info.Controller.Secret,
		Key:       defaultPipelinesAscodeSecretWebhookSecretKey,
	})
	return strings.TrimSpace(s), err
}

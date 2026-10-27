package pipelineascode

import (
	"context"
	"fmt"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/credentials"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/matcher"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/random"
)

// maxGeneratedNameBase matches the apiserver limit applied to generateName.
const maxGeneratedNameBase = 58

// usesCredentialService reports whether this build's git auth secret is
// delegated to the standalone credential service (proof of concept).
func (p *PacRun) usesCredentialService(match matcher.Match) bool {
	if !p.pacInfo.SecretAutoCreation || p.event.InstallationID <= 0 {
		return false
	}
	if _, ok := p.vcx.(provider.TriggerRepositoryIDGetter); !ok {
		return false
	}
	return p.credentialsHandoff.Applies(match.Repo.Spec.URL)
}

// prepareServiceCredentials fixes the build name, submits the credential
// request and waits for fresh credentials. The build must not be created
// when it fails.
func (p *PacRun) prepareServiceCredentials(ctx context.Context, match matcher.Match, secretName string) (*credentials.Preparer, credentials.Request, error) {
	var repoID int64
	if getter, ok := p.vcx.(provider.TriggerRepositoryIDGetter); ok {
		repoID = getter.GetTriggerRepositoryID()
	}
	if repoID <= 0 {
		return nil, credentials.Request{}, fmt.Errorf("the ID of the triggering repository is unknown")
	}
	if p.pacInfo.SecretGhAppTokenScopedExtraRepos != "" ||
		(match.Repo.Spec.Settings != nil && len(match.Repo.Spec.Settings.GithubAppTokenScopeRepos) > 0) {
		return nil, credentials.Request{}, fmt.Errorf("the credential service only issues tokens for the triggering repository, remove the extra repository token scoping or exclude this repository from %s", credentials.EnvRepositories)
	}

	pr := match.PipelineRun
	if pr.GetName() == "" {
		base := pr.GetGenerateName()
		if base == "" {
			return nil, credentials.Request{}, fmt.Errorf("pipelinerun has neither a name nor a generateName")
		}
		if len(base) > maxGeneratedNameBase {
			base = base[:maxGeneratedNameBase]
		}
		pr.SetName(base + strings.ToLower(random.AlphaString(5)))
	}
	if pr.Annotations == nil {
		pr.Annotations = map[string]string{}
	}
	pr.Annotations[credentials.AnnotationSecretOwner] = credentials.SecretOwnerService

	ns := p.credentialsHandoff.Namespace
	if ns == "" {
		ns = info.GetNS(ctx)
	}
	preparer := &credentials.Preparer{Kube: p.run.Clients.Kube, Namespace: ns, Wait: p.credentialsHandoff.Wait}
	req := credentials.Request{
		RepositoryID:    repoID,
		TargetNamespace: match.Repo.GetNamespace(),
		SecretName:      secretName,
		PipelineRunName: pr.GetName(),
	}
	p.logger.Infof("requesting build credentials for pipelinerun %s/%s from the credential service", req.TargetNamespace, req.PipelineRunName)
	return preparer, req, preparer.Prepare(ctx, req)
}

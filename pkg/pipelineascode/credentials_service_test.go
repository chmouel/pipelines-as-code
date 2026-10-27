package pipelineascode

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/credentials"
	ghprovider "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/github"
	kitesthelper "github.com/openshift-pipelines/pipelines-as-code/pkg/test/kubernetestint"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kubefake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

type triggerRepoProvider struct {
	*ghprovider.Provider
	id int64
}

func (p triggerRepoProvider) GetTriggerRepositoryID() int64 { return p.id }

func TestStartPRCredentialService(t *testing.T) {
	const (
		controlNS = "pipelines-as-code"
		repoURL   = "https://github.com/org/repo"
		prName    = "fixed-build"
	)
	enabled := credentials.HandoffSettings{Enabled: true, AllRepos: true, Namespace: controlNS, Wait: 200 * time.Millisecond}
	freshStatus := func() map[string]string {
		return map[string]string{
			credentials.AnnotationState:     string(credentials.StateReady),
			credentials.AnnotationExpiresAt: credentials.FormatTime(time.Now().Add(time.Hour)),
		}
	}
	sameRequest := credentials.Request{RepositoryID: 42, TargetNamespace: "test-namespace", SecretName: "test-git-secret", PipelineRunName: prName}

	tests := []struct {
		name               string
		handoff            credentials.HandoffSettings
		installationID     int64
		triggerRepoID      int64
		noAutoCreation     bool
		extraRepos         string
		repoScopeRepos     []string
		prName             string
		spoofOwner         bool
		serviceStatus      map[string]string
		existingRequest    *credentials.Request
		legacySecretErrors bool
		wantErr            string
		wantServiceOwned   bool
		wantRequest        bool
	}{
		{
			name:               "service owned credentials skip legacy secret writes",
			handoff:            enabled,
			installationID:     1,
			triggerRepoID:      42,
			serviceStatus:      freshStatus(),
			legacySecretErrors: true,
			wantServiceOwned:   true,
			wantRequest:        true,
		},
		{
			name:             "matching existing request is reused on retry",
			handoff:          enabled,
			installationID:   1,
			triggerRepoID:    42,
			prName:           prName,
			existingRequest:  &sameRequest,
			serviceStatus:    freshStatus(),
			wantServiceOwned: true,
			wantRequest:      true,
		},
		{
			name:            "existing request for a different build is refused",
			handoff:         enabled,
			installationID:  1,
			triggerRepoID:   42,
			prName:          prName,
			existingRequest: &credentials.Request{RepositoryID: 42, TargetNamespace: "test-namespace", SecretName: "other", PipelineRunName: prName},
			wantErr:         "already exists for a different build",
			wantRequest:     true,
		},
		{
			name:           "service not ready prevents launch",
			handoff:        enabled,
			installationID: 1,
			triggerRepoID:  42,
			wantErr:        "not ready after",
			wantRequest:    true,
		},
		{
			name:           "rejected request prevents launch",
			handoff:        enabled,
			installationID: 1,
			triggerRepoID:  42,
			serviceStatus: map[string]string{
				credentials.AnnotationState:   string(credentials.StateRejected),
				credentials.AnnotationMessage: "repository 42 is not permitted",
			},
			wantErr:     "repository 42 is not permitted",
			wantRequest: true,
		},
		{
			name:           "nearly expired credentials prevent launch",
			handoff:        enabled,
			installationID: 1,
			triggerRepoID:  42,
			serviceStatus: map[string]string{
				credentials.AnnotationState:     string(credentials.StateReady),
				credentials.AnnotationExpiresAt: credentials.FormatTime(time.Now().Add(time.Minute)),
			},
			wantErr:     "too soon to launch",
			wantRequest: true,
		},
		{
			name:           "extra repository scoping is refused",
			handoff:        enabled,
			installationID: 1,
			triggerRepoID:  42,
			extraRepos:     "org/other",
			wantErr:        "only issues tokens for the triggering repository",
		},
		{
			name:           "repository setting token scoping is refused",
			handoff:        enabled,
			installationID: 1,
			triggerRepoID:  42,
			repoScopeRepos: []string{"org/other"},
			wantErr:        "only issues tokens for the triggering repository",
		},
		{
			name:           "unknown repository ID is refused",
			handoff:        enabled,
			installationID: 1,
			wantErr:        "ID of the triggering repository is unknown",
		},
		{
			name:           "disabled secret auto creation creates nothing",
			handoff:        enabled,
			installationID: 1,
			triggerRepoID:  42,
			noAutoCreation: true,
		},
		{
			name:               "repository outside the handoff scope uses the legacy path",
			handoff:            credentials.HandoffSettings{Enabled: true, Repositories: []string{"https://github.com/org/other"}, Namespace: controlNS},
			installationID:     1,
			triggerRepoID:      42,
			legacySecretErrors: true,
			wantErr:            "creating basic auth secret",
		},
		{
			name:               "non App event uses the legacy path",
			handoff:            enabled,
			triggerRepoID:      42,
			legacySecretErrors: true,
			wantErr:            "creating basic auth secret",
		},
		{
			name:           "owner annotation set by the PipelineRun author is removed",
			installationID: 1,
			triggerRepoID:  42,
			spoofOwner:     true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := defaultStartPRTestConfig()
			cfg.secretAutoCreation = !tt.noAutoCreation
			fixture := setupStartPRTestWithConfig(t, cfg)
			defer fixture.teardown()
			if tt.legacySecretErrors {
				fixture.kint = &kitesthelper.KinterfaceTest{
					ConsoleURL:        cfg.consoleURL,
					CreateSecretError: errors.New("legacy create called"),
					UpdateSecretError: errors.New("legacy update called"),
				}
			}
			fixture.pacInfo.SecretGhAppTokenScopedExtraRepos = tt.extraRepos
			fixture.event.InstallationID = tt.installationID
			ghvcx, ok := fixture.vcx.(*ghprovider.Provider)
			assert.Assert(t, ok)
			vcx := triggerRepoProvider{Provider: ghvcx, id: tt.triggerRepoID}
			pac := NewPacs(fixture.event, vcx, fixture.cs, fixture.pacInfo, fixture.kint, fixture.logger, nil)
			pac.credentialsHandoff = tt.handoff

			kube, ok := fixture.cs.Clients.Kube.(*kubefake.Clientset)
			assert.Assert(t, ok)
			if tt.existingRequest != nil {
				cm := credentials.NewRequestConfigMap(controlNS, credentials.RequestName(tt.existingRequest.TargetNamespace, tt.existingRequest.PipelineRunName), *tt.existingRequest)
				cm.Annotations = tt.serviceStatus
				_, err := kube.CoreV1().ConfigMaps(controlNS).Create(context.Background(), cm, metav1.CreateOptions{})
				assert.NilError(t, err)
			} else if tt.serviceStatus != nil {
				kube.PrependReactor("create", "configmaps", func(action k8stesting.Action) (bool, runtime.Object, error) {
					obj, _ := action.(k8stesting.CreateAction).GetObject().(*corev1.ConfigMap)
					obj.Annotations = tt.serviceStatus
					return false, nil, nil
				})
			}

			match := createTestMatch(true, nil)
			match.Repo.Spec.URL = repoURL
			if len(tt.repoScopeRepos) > 0 {
				match.Repo.Spec.Settings = &v1alpha1.Settings{GithubAppTokenScopeRepos: tt.repoScopeRepos}
			}
			if tt.prName != "" {
				match.PipelineRun.Name = tt.prName
			}
			if tt.spoofOwner {
				match.PipelineRun.Annotations[credentials.AnnotationSecretOwner] = credentials.SecretOwnerService
			}

			pr, err := pac.startPR(fixture.ctx, match)
			requests, lerr := kube.CoreV1().ConfigMaps(controlNS).List(context.Background(), metav1.ListOptions{})
			assert.NilError(t, lerr)
			assert.Equal(t, len(requests.Items) > 0, tt.wantRequest)

			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				prs, lerr := fixture.cs.Clients.Tekton.TektonV1().PipelineRuns("test-namespace").List(context.Background(), metav1.ListOptions{})
				assert.NilError(t, lerr)
				assert.Equal(t, len(prs.Items), 0, "build launched despite failed preparation")
				return
			}
			assert.NilError(t, err)
			_, owned := pr.GetAnnotations()[credentials.AnnotationSecretOwner]
			assert.Equal(t, owned, tt.wantServiceOwned)
			if tt.wantRequest {
				req, perr := credentials.ParseRequest(&requests.Items[0])
				assert.NilError(t, perr)
				assert.Equal(t, req.PipelineRunName, pr.GetName())
				assert.Equal(t, req.SecretName, pr.GetAnnotations()[keys.GitAuthSecret])
				assert.Equal(t, req.RepositoryID, int64(42))
				assert.Equal(t, requests.Items[0].Name, credentials.RequestName("test-namespace", pr.GetName()))
			}
		})
	}
}

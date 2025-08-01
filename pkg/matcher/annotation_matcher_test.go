package matcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v71/github"
	"github.com/jonboulle/clockwork"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/opscomments"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	ghprovider "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/github"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	ghtesthelper "github.com/openshift-pipelines/pipelines-as-code/pkg/test/github"
	testnewrepo "github.com/openshift-pipelines/pipelines-as-code/pkg/test/repository"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/golden"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

const pipelineTargetNSName = "pipeline-target-ns"

type annotationTestArgs struct {
	fileChanged []struct {
		FileName    string
		Status      string
		NewFile     bool
		RenamedFile bool
		DeletedFile bool
	}
	pruns    []*tektonv1.PipelineRun
	runevent info.Event
	data     testclient.Data
}

type annotationTest struct {
	name, wantPRName, wantRepoName, wantLog string
	args                                    annotationTestArgs
	wantErr                                 bool
}

func makePipelineRunTargetNS(event, targetNS string) *tektonv1.PipelineRun {
	a := map[string]string{
		keys.OnEvent:        fmt.Sprintf("[%s]", event),
		keys.OnTargetBranch: fmt.Sprintf("[%s]", mainBranch),
		keys.MaxKeepRuns:    "2",
	}
	if targetNS != "" {
		a[keys.TargetNamespace] = targetNS
	}
	return &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:        pipelineTargetNSName,
			Annotations: a,
		},
	}
}

func TestMatchPipelinerunAnnotationAndRepositories(t *testing.T) {
	cw := clockwork.NewFakeClock()

	filesChanged := []struct {
		FileName    string
		Status      string
		NewFile     bool
		RenamedFile bool
		DeletedFile bool
	}{
		{
			FileName:    "src/added.go",
			Status:      "added",
			NewFile:     true,
			RenamedFile: false,
			DeletedFile: false,
		},
		{
			FileName:    "src/modified.go",
			Status:      "modified",
			NewFile:     false,
			RenamedFile: false,
			DeletedFile: false,
		},
		{
			FileName:    "src/removed.go",
			Status:      "removed",
			NewFile:     false,
			RenamedFile: false,
			DeletedFile: true,
		},

		{
			FileName:    "src/renamed.go",
			Status:      "renamed",
			NewFile:     false,
			RenamedFile: true,
			DeletedFile: false,
		},
	}

	tests := []annotationTest{
		{
			name:       "match a repository with target NS",
			wantPRName: pipelineTargetNSName,
			args: annotationTestArgs{
				pruns: []*tektonv1.PipelineRun{
					makePipelineRunTargetNS("pull_request", targetNamespace),
				},
				runevent: info.Event{
					URL: targetURL, TriggerTarget: "pull_request", EventType: "pull_request",
					BaseBranch: mainBranch,
				},
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						testnewrepo.NewRepo(
							testnewrepo.RepoTestcreationOpts{
								Name:             "test-good",
								URL:              targetURL,
								InstallNamespace: targetNamespace,
							},
						),
					},
				},
			},
		},

		{
			name:       "cel/match source/target",
			wantPRName: pipelineTargetNSName,
			args: annotationTestArgs{
				pruns: []*tektonv1.PipelineRun{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: pipelineTargetNSName,
							Annotations: map[string]string{
								keys.OnCelExpression: "event == \"pull_request" +
									"\" && target_branch == \"" + mainBranch + "\" && source_branch == \"unittests\"",
							},
						},
					},
				},
				runevent: info.Event{
					URL:           targetURL,
					TriggerTarget: "pull_request",
					EventType:     "pull_request",
					BaseBranch:    mainBranch,
					HeadBranch:    "unittests",
				},
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						testnewrepo.NewRepo(
							testnewrepo.RepoTestcreationOpts{
								Name:             "test-good",
								URL:              targetURL,
								InstallNamespace: targetNamespace,
							},
						),
					},
				},
			},
		},
		{
			name:       "cel/match body payload",
			wantPRName: pipelineTargetNSName,
			args: annotationTestArgs{
				pruns: []*tektonv1.PipelineRun{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: pipelineTargetNSName,
							Annotations: map[string]string{
								keys.OnCelExpression: "body.foo == 'bar'",
							},
						},
					},
				},
				runevent: info.Event{
					URL:           targetURL,
					TriggerTarget: "pull_request",
					EventType:     "pull_request",
					BaseBranch:    mainBranch,
					HeadBranch:    "unittests",
					Event: map[string]any{
						"foo": "bar",
					},
				},
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						testnewrepo.NewRepo(
							testnewrepo.RepoTestcreationOpts{
								Name:             "test-good",
								URL:              targetURL,
								InstallNamespace: targetNamespace,
							},
						),
					},
				},
			},
		},
		{
			name:       "cel/match request header",
			wantPRName: pipelineTargetNSName,
			args: annotationTestArgs{
				pruns: []*tektonv1.PipelineRun{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: pipelineTargetNSName,
							Annotations: map[string]string{
								keys.OnCelExpression: "headers['foo'] == 'bar'",
							},
						},
					},
				},
				runevent: info.Event{
					URL:           targetURL,
					TriggerTarget: "pull_request",
					EventType:     "pull_request",
					BaseBranch:    mainBranch,
					HeadBranch:    "unittests",
					Request: &info.Request{
						Header: http.Header{
							"Foo": []string{"bar"},
						},
					},
				},
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						testnewrepo.NewRepo(
							testnewrepo.RepoTestcreationOpts{
								Name:             "test-good",
								URL:              targetURL,
								InstallNamespace: targetNamespace,
							},
						),
					},
				},
			},
		},
		{
			name:       "cel/match path by glob",
			wantPRName: pipelineTargetNSName,
			args: annotationTestArgs{
				fileChanged: []struct {
					FileName    string
					Status      string
					NewFile     bool
					RenamedFile bool
					DeletedFile bool
				}{
					{
						FileName:    ".tekton/pull_request.yaml",
						Status:      "added",
						NewFile:     true,
						RenamedFile: false,
						DeletedFile: false,
					},
				},
				pruns: []*tektonv1.PipelineRun{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: pipelineTargetNSName,
							Annotations: map[string]string{
								keys.OnCelExpression: "\".tekton/*yaml\"." +
									"pathChanged()",
							},
						},
					},
				},
				runevent: info.Event{
					URL:               targetURL,
					TriggerTarget:     "pull_request",
					EventType:         "pull_request",
					BaseBranch:        mainBranch,
					HeadBranch:        "unittests",
					PullRequestNumber: 1000,
					Organization:      "mylittle",
					Repository:        "pony",
				},
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						testnewrepo.NewRepo(
							testnewrepo.RepoTestcreationOpts{
								Name:             "test-good",
								URL:              targetURL,
								InstallNamespace: targetNamespace,
							},
						),
					},
				},
			},
		},
		{
			name:       "cel/match path title pr",
			wantPRName: pipelineTargetNSName,
			args: annotationTestArgs{
				pruns: []*tektonv1.PipelineRun{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: pipelineTargetNSName,
							Annotations: map[string]string{
								keys.OnCelExpression: "event_title.startsWith(\"[UPSTREAM]\")",
							},
						},
					},
				},
				runevent: info.Event{
					URL:               targetURL,
					TriggerTarget:     "pull_request",
					EventType:         "pull_request",
					BaseBranch:        mainBranch,
					HeadBranch:        "unittests",
					PullRequestNumber: 1000,
					PullRequestTitle:  "[UPSTREAM] test me cause i'm famous",
					Organization:      "mylittle",
					Repository:        "pony",
				},
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						testnewrepo.NewRepo(
							testnewrepo.RepoTestcreationOpts{
								Name:             "test-good",
								URL:              targetURL,
								InstallNamespace: targetNamespace,
							},
						),
					},
				},
			},
		},

		{
			name:       "cel/match path title push",
			wantPRName: pipelineTargetNSName,
			args: annotationTestArgs{
				pruns: []*tektonv1.PipelineRun{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: pipelineTargetNSName,
							Annotations: map[string]string{
								keys.OnCelExpression: "event_title.startsWith(\"[UPSTREAM]\")",
							},
						},
					},
				},
				runevent: info.Event{
					URL:           targetURL,
					TriggerTarget: "push",
					EventType:     "push",
					BaseBranch:    mainBranch,
					HeadBranch:    "unittests",
					SHATitle:      "[UPSTREAM] test me cause i'm famous",
					Organization:  "mylittle",
					Repository:    "pony",
				},
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						testnewrepo.NewRepo(
							testnewrepo.RepoTestcreationOpts{
								Name:             "test-good",
								URL:              targetURL,
								InstallNamespace: targetNamespace,
							},
						),
					},
				},
			},
		},

		{
			name:    "cel/no match path title pr",
			wantErr: true,
			args: annotationTestArgs{
				pruns: []*tektonv1.PipelineRun{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: pipelineTargetNSName,
							Annotations: map[string]string{
								keys.OnCelExpression: "event_title.startsWith(\"[UPSTREAM]\")",
							},
						},
					},
				},
				runevent: info.Event{
					URL:               targetURL,
					TriggerTarget:     "pull_request",
					EventType:         "pull_request",
					BaseBranch:        mainBranch,
					HeadBranch:        "unittests",
					PullRequestNumber: 1000,
					PullRequestTitle:  "[DOWNSTREAM] don't test me cause i'm famous",
					Organization:      "mylittle",
					Repository:        "pony",
				},
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						testnewrepo.NewRepo(
							testnewrepo.RepoTestcreationOpts{
								Name:             "test-good",
								URL:              targetURL,
								InstallNamespace: targetNamespace,
							},
						),
					},
				},
			},
		},
		{
			name:    "cel/no match path by glob",
			wantErr: true,
			args: annotationTestArgs{
				fileChanged: []struct {
					FileName    string
					Status      string
					NewFile     bool
					RenamedFile bool
					DeletedFile bool
				}{
					{
						FileName:    ".tekton/foo.json",
						Status:      "added",
						NewFile:     true,
						RenamedFile: false,
						DeletedFile: false,
					},
				},
				pruns: []*tektonv1.PipelineRun{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: pipelineTargetNSName,
							Annotations: map[string]string{
								keys.OnCelExpression: "\".tekton/*yaml\"." +
									"pathChanged()",
							},
						},
					},
				},
				runevent: info.Event{
					URL:               targetURL,
					TriggerTarget:     "pull_request",
					EventType:         "pull_request",
					BaseBranch:        mainBranch,
					HeadBranch:        "unittests",
					PullRequestNumber: 1000,
					Organization:      "mylittle",
					Repository:        "pony",
				},
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						testnewrepo.NewRepo(
							testnewrepo.RepoTestcreationOpts{
								Name:             "test-good",
								URL:              targetURL,
								InstallNamespace: targetNamespace,
							},
						),
					},
				},
			},
		},

		{
			name:       "cel/match by direct path",
			wantPRName: pipelineTargetNSName,
			args: annotationTestArgs{
				fileChanged: []struct {
					FileName    string
					Status      string
					NewFile     bool
					RenamedFile bool
					DeletedFile bool
				}{
					{
						FileName:    ".tekton/pull_request.yaml",
						Status:      "added",
						NewFile:     true,
						RenamedFile: false,
						DeletedFile: false,
					},
				},
				pruns: []*tektonv1.PipelineRun{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: pipelineTargetNSName,
							Annotations: map[string]string{
								keys.OnCelExpression: "\".tekton/pull_request.yaml\"." +
									"pathChanged()",
							},
						},
					},
				},
				runevent: info.Event{
					URL:               targetURL,
					TriggerTarget:     "pull_request",
					EventType:         "pull_request",
					BaseBranch:        mainBranch,
					HeadBranch:        "unittests",
					PullRequestNumber: 1000,
					Organization:      "mylittle",
					Repository:        "pony",
				},
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						testnewrepo.NewRepo(
							testnewrepo.RepoTestcreationOpts{
								Name:             "test-good",
								URL:              targetURL,
								InstallNamespace: targetNamespace,
							},
						),
					},
				},
			},
		},
		{ //nolint:dupl
			name:       "match/on-path-change-ignore/with commas",
			wantLog:    "Skipping pipelinerun with name: pipeline-target-ns",
			wantPRName: pipelineTargetNSName,
			args: annotationTestArgs{
				fileChanged: []struct {
					FileName    string
					Status      string
					NewFile     bool
					RenamedFile bool
					DeletedFile bool
				}{
					{
						FileName:    "doc/gen,foo,bar.md",
						Status:      "added",
						NewFile:     true,
						RenamedFile: false,
						DeletedFile: false,
					},
				},
				pruns: []*tektonv1.PipelineRun{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: pipelineTargetNSName,
							Annotations: map[string]string{
								keys.OnTargetBranch: mainBranch,
								keys.OnEvent:        "[pull_request]",
								keys.OnPathChange:   "[doc/gen&#44;*]",
							},
						},
					},
				},
				runevent: info.Event{
					URL:               targetURL,
					TriggerTarget:     "pull_request",
					EventType:         "pull_request",
					BaseBranch:        mainBranch,
					HeadBranch:        "unittests",
					PullRequestNumber: 1000,
					Organization:      "mylittle",
					Repository:        "pony",
				},
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						testnewrepo.NewRepo(
							testnewrepo.RepoTestcreationOpts{
								Name:             "test-good",
								URL:              targetURL,
								InstallNamespace: targetNamespace,
							},
						),
					},
				},
			},
		},
		{ //nolint:dupl
			name:    "ignored/on-path-change-ignore/no path change",
			wantLog: "Skipping pipelinerun with name: pipeline-target-ns",
			wantErr: true,
			args: annotationTestArgs{
				fileChanged: []struct {
					FileName    string
					Status      string
					NewFile     bool
					RenamedFile bool
					DeletedFile bool
				}{
					{
						FileName:    "foo/generated/gen.md",
						Status:      "added",
						NewFile:     true,
						RenamedFile: false,
						DeletedFile: false,
					},
				},
				pruns: []*tektonv1.PipelineRun{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: pipelineTargetNSName,
							Annotations: map[string]string{
								keys.OnTargetBranch: mainBranch,
								keys.OnEvent:        "[pull_request]",
								keys.OnPathChange:   "[doc/***]",
							},
						},
					},
				},
				runevent: info.Event{
					URL:               targetURL,
					TriggerTarget:     "pull_request",
					EventType:         "pull_request",
					BaseBranch:        mainBranch,
					HeadBranch:        "unittests",
					PullRequestNumber: 1000,
					Organization:      "mylittle",
					Repository:        "pony",
				},
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						testnewrepo.NewRepo(
							testnewrepo.RepoTestcreationOpts{
								Name:             "test-good",
								URL:              targetURL,
								InstallNamespace: targetNamespace,
							},
						),
					},
				},
			},
		},
		{
			name:    "ignored/on-path-change-ignore/include and ignore path",
			wantLog: "Skipping pipelinerun with name: pipeline-target-ns",
			wantErr: true,
			args: annotationTestArgs{
				fileChanged: []struct {
					FileName    string
					Status      string
					NewFile     bool
					RenamedFile bool
					DeletedFile bool
				}{
					{
						FileName:    "doc/generated/gen.md",
						Status:      "added",
						NewFile:     true,
						RenamedFile: false,
						DeletedFile: false,
					},
				},
				pruns: []*tektonv1.PipelineRun{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: pipelineTargetNSName,
							Annotations: map[string]string{
								keys.OnTargetBranch:     mainBranch,
								keys.OnEvent:            "[pull_request]",
								keys.OnPathChange:       "[doc/***]",
								keys.OnPathChangeIgnore: "[doc/generated/*]",
							},
						},
					},
				},
				runevent: info.Event{
					URL:               targetURL,
					TriggerTarget:     "pull_request",
					EventType:         "pull_request",
					BaseBranch:        mainBranch,
					HeadBranch:        "unittests",
					PullRequestNumber: 1000,
					Organization:      "mylittle",
					Repository:        "pony",
				},
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						testnewrepo.NewRepo(
							testnewrepo.RepoTestcreationOpts{
								Name:             "test-good",
								URL:              targetURL,
								InstallNamespace: targetNamespace,
							},
						),
					},
				},
			},
		},
		{
			name:       "match/on-path-change-ignore/include and ignore path",
			wantPRName: pipelineTargetNSName,
			args: annotationTestArgs{
				fileChanged: []struct {
					FileName    string
					Status      string
					NewFile     bool
					RenamedFile bool
					DeletedFile bool
				}{
					{
						FileName:    "doc/added.md",
						Status:      "added",
						NewFile:     true,
						RenamedFile: false,
						DeletedFile: false,
					},
				},
				pruns: []*tektonv1.PipelineRun{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: pipelineTargetNSName,
							Annotations: map[string]string{
								keys.OnTargetBranch:     mainBranch,
								keys.OnEvent:            "[pull_request]",
								keys.OnPathChange:       "[doc/***]",
								keys.OnPathChangeIgnore: "[doc/generated/*]",
							},
						},
					},
				},
				runevent: info.Event{
					URL:               targetURL,
					TriggerTarget:     "pull_request",
					EventType:         "pull_request",
					BaseBranch:        mainBranch,
					HeadBranch:        "unittests",
					PullRequestNumber: 1000,
					Organization:      "mylittle",
					Repository:        "pony",
				},
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						testnewrepo.NewRepo(
							testnewrepo.RepoTestcreationOpts{
								Name:             "test-good",
								URL:              targetURL,
								InstallNamespace: targetNamespace,
							},
						),
					},
				},
			},
		},
		{
			name:       "match/on-path-change-ignore/ignore path",
			wantPRName: pipelineTargetNSName,
			args: annotationTestArgs{
				fileChanged: []struct {
					FileName    string
					Status      string
					NewFile     bool
					RenamedFile bool
					DeletedFile bool
				}{
					{
						FileName:    ".tekton/pull_request.yaml",
						Status:      "added",
						NewFile:     true,
						RenamedFile: false,
						DeletedFile: false,
					},
				},
				pruns: []*tektonv1.PipelineRun{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: pipelineTargetNSName,
							Annotations: map[string]string{
								keys.OnTargetBranch:     mainBranch,
								keys.OnEvent:            "[pull_request]",
								keys.OnPathChangeIgnore: "[doc/*md]",
							},
						},
					},
				},
				runevent: info.Event{
					URL:               targetURL,
					TriggerTarget:     "pull_request",
					EventType:         "pull_request",
					BaseBranch:        mainBranch,
					HeadBranch:        "unittests",
					PullRequestNumber: 1000,
					Organization:      "mylittle",
					Repository:        "pony",
				},
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						testnewrepo.NewRepo(
							testnewrepo.RepoTestcreationOpts{
								Name:             "test-good",
								URL:              targetURL,
								InstallNamespace: targetNamespace,
							},
						),
					},
				},
			},
		},
		{
			name:       "match/on-path-change/match path by glob",
			wantPRName: pipelineTargetNSName,
			args: annotationTestArgs{
				fileChanged: []struct {
					FileName    string
					Status      string
					NewFile     bool
					RenamedFile bool
					DeletedFile bool
				}{
					{
						FileName:    ".tekton/pull_request.yaml",
						Status:      "added",
						NewFile:     true,
						RenamedFile: false,
						DeletedFile: false,
					},
				},
				pruns: []*tektonv1.PipelineRun{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: pipelineTargetNSName,
							Annotations: map[string]string{
								keys.OnTargetBranch: mainBranch,
								keys.OnEvent:        "[pull_request]",
								keys.OnPathChange:   "[.tekton/*yaml]",
							},
						},
					},
				},
				runevent: info.Event{
					URL:               targetURL,
					TriggerTarget:     "pull_request",
					EventType:         "pull_request",
					BaseBranch:        mainBranch,
					HeadBranch:        "unittests",
					PullRequestNumber: 1000,
					Organization:      "mylittle",
					Repository:        "pony",
				},
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						testnewrepo.NewRepo(
							testnewrepo.RepoTestcreationOpts{
								Name:             "test-good",
								URL:              targetURL,
								InstallNamespace: targetNamespace,
							},
						),
					},
				},
			},
		},
		{
			name:    "error/match/on-path-change/match path no match event",
			wantErr: true,
			args: annotationTestArgs{
				fileChanged: []struct {
					FileName    string
					Status      string
					NewFile     bool
					RenamedFile bool
					DeletedFile bool
				}{
					{
						FileName:    ".tekton/pull_request.yaml",
						Status:      "added",
						NewFile:     true,
						RenamedFile: false,
						DeletedFile: false,
					},
				},
				pruns: []*tektonv1.PipelineRun{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: pipelineTargetNSName,
							Annotations: map[string]string{
								keys.OnTargetBranch: mainBranch,
								keys.OnEvent:        "[push]",
								keys.OnPathChange:   "[.tekton/*yaml]",
							},
						},
					},
				},
				runevent: info.Event{
					URL:               targetURL,
					TriggerTarget:     "pull_request",
					EventType:         "pull_request",
					BaseBranch:        mainBranch,
					HeadBranch:        "unittests",
					PullRequestNumber: 1000,
					Organization:      "mylittle",
					Repository:        "pony",
				},
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						testnewrepo.NewRepo(
							testnewrepo.RepoTestcreationOpts{
								Name:             "test-good",
								URL:              targetURL,
								InstallNamespace: targetNamespace,
							},
						),
					},
				},
			},
		},

		{
			name: "match TargetPipelineRun",
			args: annotationTestArgs{
				pruns: []*tektonv1.PipelineRun{
					{
						ObjectMeta: metav1.ObjectMeta{
							GenerateName: fmt.Sprintf("%s-", pipelineTargetNSName),
						},
					},
				},
				runevent: info.Event{
					URL:               targetURL,
					TargetPipelineRun: pipelineTargetNSName,
				},
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						testnewrepo.NewRepo(
							testnewrepo.RepoTestcreationOpts{
								Name:             "test-good",
								URL:              targetURL,
								InstallNamespace: targetNamespace,
							},
						),
					},
				},
			},
		},
		{
			name:    "cel/bad expression",
			wantErr: true,
			args: annotationTestArgs{
				pruns: []*tektonv1.PipelineRun{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: pipelineTargetNSName,
							Annotations: map[string]string{
								keys.OnCelExpression: "BADDDDDDDx'ax\\\a",
							},
						},
					},
				},
				runevent: info.Event{
					URL:           targetURL,
					TriggerTarget: "pull_request",
					EventType:     "pull_request",
					BaseBranch:    mainBranch,
					HeadBranch:    "unittests",
				},
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						testnewrepo.NewRepo(
							testnewrepo.RepoTestcreationOpts{
								Name:             "test-good",
								URL:              targetURL,
								InstallNamespace: targetNamespace,
							},
						),
					},
				},
			},
		},
		{
			name: "matching incoming webhook event on push target",
			args: annotationTestArgs{
				pruns: []*tektonv1.PipelineRun{
					makePipelineRunTargetNS(triggertype.Push.String(), ""),
				},
				runevent: info.Event{
					URL:        targetURL,
					EventType:  triggertype.Incoming.String(),
					BaseBranch: mainBranch,
				},
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						testnewrepo.NewRepo(
							testnewrepo.RepoTestcreationOpts{
								Name:             "test-good",
								URL:              targetURL,
								InstallNamespace: pipelineTargetNSName,
							},
						),
					},
				},
			},
		},
		{
			name: "matching incoming webhook event on incoming target",
			args: annotationTestArgs{
				pruns: []*tektonv1.PipelineRun{
					makePipelineRunTargetNS(triggertype.Incoming.String(), ""),
				},
				runevent: info.Event{
					URL:        targetURL,
					EventType:  triggertype.Incoming.String(),
					BaseBranch: mainBranch,
				},
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						testnewrepo.NewRepo(
							testnewrepo.RepoTestcreationOpts{
								Name:             "test-good",
								URL:              targetURL,
								InstallNamespace: pipelineTargetNSName,
							},
						),
					},
				},
			},
		},
		{
			// this unit test seems wrong, you don't get a repo match from annotations but early one
			// the targetNamespace will always explicitly target the oldest one
			// we keep as is but something to improve later on
			name:         "match same webhook on multiple repos takes the oldest one",
			wantPRName:   pipelineTargetNSName,
			wantRepoName: "test-oldest",
			args: annotationTestArgs{
				pruns: []*tektonv1.PipelineRun{
					makePipelineRunTargetNS("pull_request", targetNamespace),
				},
				runevent: info.Event{
					URL: targetURL, TriggerTarget: "pull_request", EventType: "pull_request",
					BaseBranch: mainBranch,
				},
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						testnewrepo.NewRepo(
							testnewrepo.RepoTestcreationOpts{
								Name:             "test-oldest",
								URL:              targetURL,
								InstallNamespace: targetNamespace,
								CreateTime:       metav1.Time{Time: cw.Now().Add(-55 * time.Minute)},
							},
						),
						testnewrepo.NewRepo(
							testnewrepo.RepoTestcreationOpts{
								Name:             "test-newest",
								URL:              targetURL,
								InstallNamespace: targetNamespace,
								CreateTime:       metav1.Time{Time: cw.Now().Add(-50 * time.Minute)},
							},
						),
					},
				},
			},
		},
		{
			name:    "error on only when on annotation",
			wantErr: true,
			args: annotationTestArgs{
				pruns: []*tektonv1.PipelineRun{
					{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: targetNamespace,
							Name:      "only-one-annotation",
							Annotations: map[string]string{
								keys.OnEvent: "[pull_request]",
							},
						},
					},
				},
				runevent: info.Event{URL: targetURL, EventType: "pull_request", BaseBranch: mainBranch},
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						testnewrepo.NewRepo(
							testnewrepo.RepoTestcreationOpts{
								Name:             "test-oldest",
								URL:              targetURL,
								InstallNamespace: targetNamespace,
								CreateTime:       metav1.Time{Time: cw.Now().Add(-55 * time.Minute)},
							},
						),
					},
				},
			},
		},
		{
			name:    "error when no pac annotation has been set",
			wantErr: true,
			args: annotationTestArgs{
				pruns: []*tektonv1.PipelineRun{
					{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: targetNamespace,
							Name:      "no pac annotation",
							Annotations: map[string]string{
								"foo": "bar",
							},
						},
					},
				},
				runevent: info.Event{URL: targetURL, EventType: "pull_request", BaseBranch: mainBranch},
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						testnewrepo.NewRepo(
							testnewrepo.RepoTestcreationOpts{
								Name:             "test-oldest",
								URL:              targetURL,
								InstallNamespace: targetNamespace,
								CreateTime:       metav1.Time{Time: cw.Now().Add(-55 * time.Minute)},
							},
						),
					},
				},
			},
		},
		{
			name:    "error when pac annotation has been set but empty",
			wantErr: true,
			args: annotationTestArgs{
				pruns: []*tektonv1.PipelineRun{
					{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: targetNamespace,
							Name:      "no pac annotation",
							Annotations: map[string]string{
								keys.OnEvent:        "",
								keys.OnTargetBranch: "",
							},
						},
					},
				},
				runevent: info.Event{URL: targetURL, EventType: "pull_request", BaseBranch: mainBranch},
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						testnewrepo.NewRepo(
							testnewrepo.RepoTestcreationOpts{
								Name:             "test-oldest",
								URL:              targetURL,
								InstallNamespace: targetNamespace,
								CreateTime:       metav1.Time{Time: cw.Now().Add(-55 * time.Minute)},
							},
						),
					},
				},
			},
		},
		{
			name:    "no match a repository with target NS",
			wantErr: true,
			wantLog: "could not find Repository CRD in branch targetNamespace, the pipelineRun pipeline-target-ns has a label that explicitly targets it",
			args: annotationTestArgs{
				pruns: []*tektonv1.PipelineRun{
					makePipelineRunTargetNS("pull_request", targetNamespace),
				},
				runevent: info.Event{
					URL: targetURL, TriggerTarget: "pull_request", EventType: "pull_request", BaseBranch: mainBranch,
				},
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						testnewrepo.NewRepo(
							testnewrepo.RepoTestcreationOpts{
								Name:             "test-good",
								URL:              targetURL,
								InstallNamespace: "otherNS",
							},
						),
					},
				},
			},
		},
		{
			name:       "cel/match path by glob along with push event and target_branch info",
			wantPRName: pipelineTargetNSName,
			args: annotationTestArgs{
				fileChanged: []struct {
					FileName    string
					Status      string
					NewFile     bool
					RenamedFile bool
					DeletedFile bool
				}{
					{
						FileName:    ".tekton/push.yaml",
						Status:      "added",
						NewFile:     true,
						RenamedFile: false,
						DeletedFile: false,
					},
				},
				pruns: []*tektonv1.PipelineRun{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: pipelineTargetNSName,
							Annotations: map[string]string{
								keys.OnCelExpression: fmt.Sprintf(`event == "push" && target_branch == "%s" && ".tekton/*yaml".pathChanged()`, mainBranch),
							},
						},
					},
				},
				runevent: info.Event{
					URL:           targetURL,
					TriggerTarget: "push",
					EventType:     "push",
					BaseBranch:    "refs/heads/" + mainBranch,
					HeadBranch:    mainBranch,
					Organization:  "mylittle",
					Repository:    "pony",
					SHATitle:      "verifying push event",
					SHA:           "shacommitinfo",
				},
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						testnewrepo.NewRepo(
							testnewrepo.RepoTestcreationOpts{
								Name:             "test-good",
								URL:              targetURL,
								InstallNamespace: targetNamespace,
							},
						),
					},
				},
			},
		},

		{
			name:    "cel match on all changed files",
			wantErr: false,
			args: annotationTestArgs{
				fileChanged: filesChanged,
				pruns: []*tektonv1.PipelineRun{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: pipelineTargetNSName,
							Annotations: map[string]string{
								keys.OnCelExpression: "files.all.exists(x, x.matches(\"added.go\"))",
							},
						},
					},
				},
				runevent: info.Event{
					URL:               targetURL,
					TriggerTarget:     "pull_request",
					EventType:         "pull_request",
					BaseBranch:        mainBranch,
					HeadBranch:        "unittests",
					PullRequestNumber: 1000,
					PullRequestTitle:  "[DOWNSTREAM] don't test me cause i'm famous",
					Organization:      "mylittle",
					Repository:        "pony",
				},
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						testnewrepo.NewRepo(
							testnewrepo.RepoTestcreationOpts{
								Name:             "test-good",
								URL:              targetURL,
								InstallNamespace: targetNamespace,
							},
						),
					},
				},
			},
		},

		{
			name:    "cel NOT match on all changed files",
			wantErr: true,
			args: annotationTestArgs{
				fileChanged: filesChanged,
				pruns: []*tektonv1.PipelineRun{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: pipelineTargetNSName,
							Annotations: map[string]string{
								keys.OnCelExpression: "files.all.exists(x, x.matches(\"notmatch.go\"))",
							},
						},
					},
				},
				runevent: info.Event{
					URL:               targetURL,
					TriggerTarget:     "pull_request",
					EventType:         "pull_request",
					BaseBranch:        mainBranch,
					HeadBranch:        "unittests",
					PullRequestNumber: 1000,
					PullRequestTitle:  "[DOWNSTREAM] don't test me cause i'm famous",
					Organization:      "mylittle",
					Repository:        "pony",
				},
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						testnewrepo.NewRepo(
							testnewrepo.RepoTestcreationOpts{
								Name:             "test-good",
								URL:              targetURL,
								InstallNamespace: targetNamespace,
							},
						),
					},
				},
			},
		},

		{
			name:    "cel match on added, modified, deleted and renamed  files",
			wantErr: false,
			args: annotationTestArgs{
				fileChanged: filesChanged,
				pruns: []*tektonv1.PipelineRun{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: pipelineTargetNSName,
							Annotations: map[string]string{
								keys.OnCelExpression: "files.added.exists(x, x.matches(\"added.go\")) && files.deleted.exists(x, x.matches(\"removed.go\")) && files.modified.exists(x, x.matches(\"modified.go\")) && files.renamed.exists(x, x.matches(\"renamed.go\"))",
							},
						},
					},
				},
				runevent: info.Event{
					URL:               targetURL,
					TriggerTarget:     "pull_request",
					EventType:         "pull_request",
					BaseBranch:        mainBranch,
					HeadBranch:        "unittests",
					PullRequestNumber: 1000,
					PullRequestTitle:  "[DOWNSTREAM] don't test me cause i'm famous",
					Organization:      "mylittle",
					Repository:        "pony",
				},
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						testnewrepo.NewRepo(
							testnewrepo.RepoTestcreationOpts{
								Name:             "test-good",
								URL:              targetURL,
								InstallNamespace: targetNamespace,
							},
						),
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			fakeclient, mux, ghTestServerURL, teardown := ghtesthelper.SetupGH()
			defer teardown()
			vcx := &ghprovider.Provider{
				Token: github.Ptr("None"),
			}
			vcx.SetGithubClient(fakeclient)
			if tt.args.runevent.Request == nil {
				tt.args.runevent.Request = &info.Request{Header: http.Header{}, Payload: nil}
			}
			if len(tt.args.fileChanged) > 0 {
				commitFiles := []*github.CommitFile{}
				for _, v := range tt.args.fileChanged {
					commitFiles = append(commitFiles, &github.CommitFile{
						Filename: github.Ptr(v.FileName),
						Status:   github.Ptr(v.Status),
					})
				}
				if tt.args.runevent.TriggerTarget == "push" {
					mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/commits/%s",
						tt.args.runevent.Organization, tt.args.runevent.Repository, tt.args.runevent.SHA), func(w http.ResponseWriter, _ *http.Request) {
						c := &github.RepositoryCommit{
							Files: commitFiles,
						}
						jeez, err := json.Marshal(c)
						assert.NilError(t, err)
						_, _ = w.Write(jeez)
					})
				}

				if tt.args.runevent.TriggerTarget == "pull_request" {
					url := fmt.Sprintf("/repos/%s/%s/pulls/%d/files", tt.args.runevent.Organization,
						tt.args.runevent.Repository, tt.args.runevent.PullRequestNumber)
					mux.HandleFunc(url, func(w http.ResponseWriter, _ *http.Request) {
						jeez, err := json.Marshal(commitFiles)
						assert.NilError(t, err)
						_, _ = w.Write(jeez)
					})
				}
			}

			tt.args.runevent.Provider = &info.Provider{
				URL:   ghTestServerURL,
				Token: "NONE",
			}

			ghCs, _ := testclient.SeedTestData(t, ctx, tt.args.data)
			runTest(ctx, t, tt, vcx, ghCs)

			tt.args.runevent.Provider = &info.Provider{
				Token: "NONE",
			}

			runTest(ctx, t, tt, vcx, ghCs)
		})
	}
}

func runTest(ctx context.Context, t *testing.T, tt annotationTest, vcx provider.Interface, cs testclient.Clients) {
	t.Helper()
	observer, log := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()
	vcx.SetLogger(logger)
	client := &params.Run{
		Clients: clients.Clients{PipelineAsCode: cs.PipelineAsCode},
		Info:    info.Info{},
	}

	eventEmitter := events.NewEventEmitter(cs.Kube, logger)
	matches, err := MatchPipelinerunByAnnotation(ctx, logger,
		tt.args.pruns,
		client, &tt.args.runevent, vcx, eventEmitter, nil,
	)

	if tt.wantLog != "" {
		assert.Assert(t, log.FilterMessage(tt.wantLog) != nil, "We didn't get the expected log message")
	}

	if tt.wantErr {
		assert.Assert(t, err != nil, "We should have get an error")
	}

	if !tt.wantErr {
		assert.NilError(t, err)
	}

	if tt.wantRepoName != "" {
		assert.Assert(t, len(matches) > 0, "We should have get matches")
		assert.Assert(t, matches[0].Repo != nil, "We should have get a repo matching")
		assert.Assert(t, tt.wantRepoName == matches[0].Repo.GetName())
	}
	if tt.wantPRName != "" {
		assert.Assert(t, tt.wantPRName == matches[0].PipelineRun.GetName())
	}
}

func TestMatchPipelinerunByAnnotation(t *testing.T) {
	pipelineGood := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pipeline-good",
			Annotations: map[string]string{
				keys.OnEvent:        "[pull_request]",
				keys.OnTargetBranch: "[main]",
			},
		},
	}

	pipelineCel := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pipeline-cel",
			Annotations: map[string]string{
				keys.OnCelExpression: `event == "pull_request"`,
			},
		},
	}

	pipelinePush := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pipeline-push",
			Annotations: map[string]string{
				keys.OnEvent:        "[push]",
				keys.OnTargetBranch: "[main]",
			},
		},
	}

	pipelineOnComment := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pipeline-on-comment",
			Annotations: map[string]string{
				keys.OnComment: "^/hello-world$",
			},
		},
	}

	pipelineOther := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pipeline-other",
			Annotations: map[string]string{
				keys.OnEvent:        "[pull_request]",
				keys.OnTargetBranch: "[main]",
			},
		},
	}

	pipelineWithSlashInBranchName := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pipeline-withslashesinbranch",
			Annotations: map[string]string{
				keys.OnEvent:        "[pull_request, push]",
				keys.OnTargetBranch: "[test/main]",
			},
		},
	}

	pipelineRefAll := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pipeline-other",
			Annotations: map[string]string{
				keys.OnEvent:        "[pull_request,push]",
				keys.OnTargetBranch: "[refs/heads/*]",
			},
		},
	}

	pipelineRefRegex := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pipeline-regex",
			Annotations: map[string]string{
				keys.OnEvent:        "[pull_request]",
				keys.OnTargetBranch: "[refs/heads/release-*]",
			},
		},
	}

	observer, log := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	type args struct {
		pruns    []*tektonv1.PipelineRun
		runevent info.Event
	}
	tests := []struct {
		name       string
		args       args
		wantErr    bool
		wantPrName string
		wantLog    []string
		logLevel   int
	}{
		{
			name: "good-match-with-only-one",
			args: args{
				pruns: []*tektonv1.PipelineRun{pipelineGood},
				runevent: info.Event{
					URL:               "https://hello/moto",
					TriggerTarget:     "pull_request",
					EventType:         "pull_request",
					HeadBranch:        "source",
					BaseBranch:        "main",
					PullRequestNumber: 10,
				},
			},
			wantErr:    false,
			wantPrName: "pipeline-good",
			wantLog:    []string{"matching pipelineruns to event: URL=https://hello/moto, target-branch=main, source-branch=source, target-event=pull_request, pull-request=10"},
		},
		{
			name: "good-match-on-label",
			args: args{
				pruns: []*tektonv1.PipelineRun{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "pipeline-label",
							Annotations: map[string]string{
								keys.OnEvent:        "[pull_request]",
								keys.OnTargetBranch: "[main]",
								keys.OnLabel:        "[bug]",
							},
						},
					},
					pipelineGood,
				},
				runevent: info.Event{
					URL:               "https://hello/moto",
					TriggerTarget:     "pull_request",
					EventType:         "pull_request",
					HeadBranch:        "source",
					BaseBranch:        "main",
					PullRequestNumber: 10,
					PullRequestLabel:  []string{"bug", "documentation"},
				},
			},
			wantErr:    false,
			wantPrName: "pipeline-label",
			wantLog: []string{
				"matching pipelineruns to event: URL=https://hello/moto, target-branch=main, source-branch=source, target-event=pull_request, labels=bug|documentation, pull-request=10",
				`matched PipelineRun with name: pipeline-label, annotation Label: "[bug]"`,
			},
		},
		{
			name: "first-one-match-with-two-good-ones",
			args: args{
				pruns:    []*tektonv1.PipelineRun{pipelineGood, pipelineOther},
				runevent: info.Event{TriggerTarget: "pull_request", EventType: "pull_request", BaseBranch: "main"},
			},
			wantErr:    false,
			wantPrName: "pipeline-good",
		},
		{
			name: "match-on-cel-expression",
			args: args{
				pruns: []*tektonv1.PipelineRun{pipelineCel},
				runevent: info.Event{
					TriggerTarget: "pull_request",
					EventType:     "pull_request",
					BaseBranch:    "main",
					Request: &info.Request{
						Header: http.Header{},
					},
				},
			},
			wantErr:    false,
			wantPrName: pipelineCel.GetName(),
		},
		{
			name: "cel-expression-takes-precedence-over-annotations",
			args: args{
				pruns: []*tektonv1.PipelineRun{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "pipeline-on-cel-test",
							Annotations: map[string]string{
								keys.OnEvent:         "[pull_request]",
								keys.OnTargetBranch:  "[main]",
								keys.OnCelExpression: `event == "pull_request" && target_branch == "main" && source_branch == "warn-for-cel"`,
							},
						},
					},
				},
				runevent: info.Event{
					URL:               "https://hello/moto",
					TriggerTarget:     "pull_request",
					EventType:         "pull_request",
					BaseBranch:        "main",
					HeadBranch:        "warn-for-cel",
					PullRequestNumber: 10,
					Request: &info.Request{
						Header: http.Header{},
					},
				},
			},
			wantErr: false,
			wantLog: []string{
				`Warning: The PipelineRun 'pipeline-on-cel-test' has 'on-cel-expression' defined along with [on-event, on-target-branch] annotation(s). The 'on-cel-expression' will take precedence and these annotations will be ignored`,
			},
		},
		{
			name: "no-match-on-label",
			args: args{
				pruns: []*tektonv1.PipelineRun{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "pipeline-label",
							Annotations: map[string]string{
								keys.OnEvent:        "[pull_request]",
								keys.OnTargetBranch: "[main]",
								keys.OnLabel:        "[bug]",
							},
						},
					},
					pipelineGood,
				},
				runevent: info.Event{
					URL:               "https://hello/moto",
					TriggerTarget:     "pull_request",
					EventType:         "pull_request",
					HeadBranch:        "source",
					BaseBranch:        "main",
					PullRequestNumber: 10,
					PullRequestLabel:  []string{"documentation"},
				},
			},
			wantErr: false,
			wantLog: []string{
				"matching pipelineruns to event: URL=https://hello/moto, target-branch=main, source-branch=source, target-event=pull_request, labels=documentation, pull-request=10",
			},
		},
		{
			name: "no-on-label-annotation-on-pr",
			args: args{
				pruns: []*tektonv1.PipelineRun{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "pipeline-label",
							Annotations: map[string]string{
								keys.OnEvent:        "[pull_request]",
								keys.OnTargetBranch: "[main]",
							},
						},
					},
					pipelineGood,
				},
				runevent: info.Event{
					URL:               "https://hello/moto",
					TriggerTarget:     triggertype.PullRequest,
					EventType:         string(triggertype.PullRequestLabeled),
					HeadBranch:        "source",
					BaseBranch:        "main",
					PullRequestNumber: 10,
					PullRequestLabel:  []string{"documentation"},
				},
			},
			wantErr: true,
			wantLog: []string{
				"label update event, PipelineRun pipeline-label does not have a on-label for any of those labels: documentation",
				"label update event, PipelineRun pipeline-good does not have a on-label for any of those labels: documentation",
			},
		},
		{
			name: "match-on-comment",
			args: args{
				pruns: []*tektonv1.PipelineRun{pipelineGood, pipelineOnComment},
				runevent: info.Event{
					TriggerComment: "   /hello-world   \r\n",
					TriggerTarget:  "pull_request",
					EventType:      opscomments.OnCommentEventType.String(),
					BaseBranch:     "main",
				},
			},
			wantErr:    false,
			wantPrName: pipelineOnComment.GetName(),
		},
		{
			name: "no-match-on-the-comment-should-not-match-the-other-pruns",
			args: args{
				pruns: []*tektonv1.PipelineRun{pipelineGood, pipelineOnComment},
				runevent: info.Event{
					TriggerComment: "good morning",
					TriggerTarget:  "pull_request",
					EventType:      opscomments.OnCommentEventType.String(),
					BaseBranch:     "main",
				},
			},
			wantErr: true,
		},
		{
			name: "no-match-on-event",
			args: args{
				pruns:    []*tektonv1.PipelineRun{pipelineGood, pipelineOther},
				runevent: info.Event{TriggerTarget: "push", EventType: "push", BaseBranch: "main"},
			},
			wantErr: true,
		},
		{
			name: "no-match-on-target-branch",
			args: args{
				pruns:    []*tektonv1.PipelineRun{pipelineGood, pipelineOther},
				runevent: info.Event{TriggerTarget: "pull_request", EventType: "pull_request", BaseBranch: "other"},
			},
			wantErr: true,
		},
		{
			name: "no-annotation",
			args: args{
				pruns: []*tektonv1.PipelineRun{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "pipeline-other",
						},
					},
				},
				runevent: info.Event{TriggerTarget: "push", EventType: "push", BaseBranch: "main"},
			},
			wantErr: true,
		},
		{
			name: "single-event-annotation",
			args: args{
				runevent: info.Event{TriggerTarget: "pull_request", EventType: "pull_request", BaseBranch: "main"},
				pruns: []*tektonv1.PipelineRun{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "single-event-annotation",
							Annotations: map[string]string{
								keys.OnEvent:        "pull_request",
								keys.OnTargetBranch: "[main]",
							},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "single-target-branch-annotation",
			args: args{
				runevent: info.Event{TriggerTarget: "pull_request", EventType: "pull_request", BaseBranch: "main"},
				pruns: []*tektonv1.PipelineRun{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "single-target-branch-annotation",
							Annotations: map[string]string{
								keys.OnEvent:        "[pull_request]",
								keys.OnTargetBranch: "main",
							},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "empty-annotation",
			args: args{
				runevent: info.Event{TriggerTarget: "pull_request", EventType: "pull_request", BaseBranch: "main"},
				pruns: []*tektonv1.PipelineRun{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "bad-target-branch-annotation",
							Annotations: map[string]string{
								keys.OnEvent:        "[]",
								keys.OnTargetBranch: "[]",
							},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "match-branch-matching",
			args: args{
				runevent: info.Event{TriggerTarget: "push", EventType: "push", BaseBranch: "refs/heads/main"},
				pruns: []*tektonv1.PipelineRun{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "branch-matching",
							Annotations: map[string]string{
								keys.OnEvent:        "[push]",
								keys.OnTargetBranch: "[main]",
							},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "base-does-not-compare",
			args: args{
				runevent: info.Event{
					TriggerTarget: "push", EventType: "push",
					BaseBranch: "refs/heads/main/foobar",
				},
				pruns: []*tektonv1.PipelineRun{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "branch-base-matching-not-compare",
							Annotations: map[string]string{
								keys.OnEvent:        "[push]",
								keys.OnTargetBranch: "[main]",
							},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "branch-glob-matching",
			args: args{
				runevent: info.Event{TriggerTarget: "push", EventType: "push", BaseBranch: "refs/heads/main"},
				pruns: []*tektonv1.PipelineRun{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "branch-base-matching-not-compare",
							Annotations: map[string]string{
								keys.OnEvent:        "[push]",
								keys.OnTargetBranch: "[refs/heads/*]",
							},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "ref-heads-*--allow-any-branch",
			args: args{
				pruns:    []*tektonv1.PipelineRun{pipelineRefAll},
				runevent: info.Event{TriggerTarget: "pull_request", EventType: "pull_request", BaseBranch: "main"},
			},
			wantErr:    false,
			wantPrName: "pipeline-other",
		},
		{
			name: "ref-heads-regex-allow",
			args: args{
				pruns:    []*tektonv1.PipelineRun{pipelineRefRegex},
				runevent: info.Event{TriggerTarget: "pull_request", EventType: "pull_request", BaseBranch: "release-0.1"},
			},
			wantErr:    false,
			wantPrName: "pipeline-regex",
		},
		{
			name: "ref-heads-regex-not-match",
			args: args{
				pruns:    []*tektonv1.PipelineRun{pipelineRefRegex},
				runevent: info.Event{TriggerTarget: "pull_request", EventType: "pull_request", BaseBranch: "main"},
			},
			wantErr: true,
		},
		{
			name: "ref-heads-main-push-rerequested-case",
			args: args{
				pruns:    []*tektonv1.PipelineRun{pipelineGood},
				runevent: info.Event{TriggerTarget: "pull_request", EventType: "pull_request", BaseBranch: "refs/heads/main"},
			},
			wantErr: false,
		},
		{
			name: "branch-matching-doesnot-match-for-push-event",
			args: args{
				runevent: info.Event{TriggerTarget: "push", EventType: "push", BaseBranch: "refs/heads/someothername/then/main"},
				pruns:    []*tektonv1.PipelineRun{pipelineGood, pipelinePush},
			},
			wantErr: true,
		},
		{
			name: "branch-matching-doesnot-match-for-pull-request",
			args: args{
				runevent: info.Event{TriggerTarget: "pull_request", EventType: "pull_request", BaseBranch: "someothername/then/main"},
				pruns:    []*tektonv1.PipelineRun{pipelineGood, pipelinePush},
			},
			wantErr: true,
		},
		{
			name: "branch-matching-match-for-push-when-there-are-slashes-in-between-branch-name",
			args: args{
				runevent: info.Event{TriggerTarget: "push", EventType: "push", BaseBranch: "refs/heads/test/main"},
				pruns:    []*tektonv1.PipelineRun{pipelineWithSlashInBranchName},
			},
			wantErr: false,
		},
		{
			name: "branch-matching-match-for-pull_request-when-there-are-slashes-in-between-branch-name",
			args: args{
				runevent: info.Event{TriggerTarget: "pull_request", EventType: "pull_request", BaseBranch: "refs/heads/test/main"},
				pruns:    []*tektonv1.PipelineRun{pipelineWithSlashInBranchName},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			cs := &params.Run{
				Clients: clients.Clients{},
				Info:    info.Info{},
			}

			eventEmitter := events.NewEventEmitter(cs.Clients.Kube, logger)
			matches, err := MatchPipelinerunByAnnotation(ctx, logger, tt.args.pruns, cs, &tt.args.runevent, &ghprovider.Provider{}, eventEmitter, nil)
			if (err != nil) != tt.wantErr {
				t.Errorf("MatchPipelinerunByAnnotation() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantPrName != "" {
				assert.Assert(t, matches[0].PipelineRun.GetName() == tt.wantPrName, "Pipelinerun hasn't been matched: %+v",
					matches[0].PipelineRun.GetName(), tt.wantPrName)
			}
			if len(tt.wantLog) > 0 {
				assert.Assert(t, log.Len() > 0, "We didn't get any log message")
				all := log.TakeAll()
				for _, wantLog := range tt.wantLog {
					matched := false
					for _, entry := range all {
						if entry.Message == wantLog {
							matched = true
						}
					}
					assert.Assert(t, matched, "We didn't get the expected log message: %s\n%s", wantLog, all)
				}
			}
		})
	}
}

func Test_getAnnotationValues(t *testing.T) {
	type args struct {
		annotation string
	}
	tests := []struct {
		name    string
		args    args
		want    []string
		wantErr bool
	}{
		{
			name: "get-annotation-string",
			args: args{
				annotation: "foo",
			},
			want:    []string{"foo"},
			wantErr: false,
		},
		{
			name: "get-annotation-simple",
			args: args{
				annotation: "[foo]",
			},
			want:    []string{"foo"},
			wantErr: false,
		},
		{
			name: "get-annotation-string-html-encoded-comma-list",
			args: args{
				annotation: "[foo&#44;,bar]",
			},
			want:    []string{"foo,", "bar"},
			wantErr: false,
		},
		{
			name: "get-annotation-string-html-encoded-comma",
			args: args{
				annotation: "foo&#44;bar",
			},
			want:    []string{"foo,bar"},
			wantErr: false,
		},
		{
			name: "get-annotation-multiples",
			args: args{
				annotation: "[foo, bar]",
			},
			want:    []string{"foo", "bar"},
			wantErr: false,
		},
		{
			name: "get-annotation-multiple-string-bad-syntax",
			args: args{
				annotation: "foo, bar",
			},
			wantErr: true,
		},
		{
			name: "get-annotation-bad-syntax",
			args: args{
				annotation: "foo]",
			},
			wantErr: true,
		},
		{
			name: "get-annotation-error-empty",
			args: args{
				annotation: "[]",
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getAnnotationValues(tt.args.annotation)
			if (err != nil) != tt.wantErr {
				t.Errorf("getAnnotationValues() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("getAnnotationValues() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBranchMatch(t *testing.T) {
	tests := []struct {
		name       string
		baseBranch string
		prunBranch string
		output     bool
	}{
		{
			name:       "both names",
			baseBranch: "main",
			prunBranch: "main",
			output:     true,
		},
		{
			name:       "baseBranch refs/head",
			baseBranch: "refs/heads/main",
			prunBranch: "main",
			output:     true,
		},
		{
			name:       "prunBranch refs/head",
			baseBranch: "main",
			prunBranch: "refs/heads/main",
			output:     true,
		},
		{
			name:       "both refs/heads",
			baseBranch: "refs/heads/main",
			prunBranch: "refs/heads/main",
			output:     true,
		},
		{
			name:       "baseBranch refs/tags",
			baseBranch: "refs/tags/v0.20.0",
			prunBranch: "main",
			output:     false,
		},
		{
			name:       "prunBranch refs/tags",
			baseBranch: "main",
			prunBranch: "refs/tags/*",
			output:     false,
		},
		{
			name:       "baseBranch refs/tags and prunBranch refs/head",
			baseBranch: "refs/tags/v0.20.0",
			prunBranch: "refs/heads/main",
			output:     false,
		},
		{
			name:       "baseBranch refs/head and prunBranch refs/tags",
			baseBranch: "refs/heads/main",
			prunBranch: "refs/tags/*",
			output:     false,
		},
		{
			name:       "both refs/tags",
			baseBranch: "refs/tags/v0.20.0",
			prunBranch: "refs/tags/*",
			output:     true,
		},
		{
			name:       "different names",
			baseBranch: "main",
			prunBranch: "test",
			output:     false,
		},
		{
			name:       "base value of path is same",
			baseBranch: "refs/heads/foo/test",
			prunBranch: "test",
			output:     false,
		},
		{
			name:       "base value of path is same opposite",
			baseBranch: "test",
			prunBranch: "refs/heads/foo/test",
			output:     false,
		},
		{
			name:       "base value of path is same in both",
			baseBranch: "refs/heads/bar/test",
			prunBranch: "refs/heads/foo/test",
			output:     false,
		},
		{
			name:       "different refs/tags",
			baseBranch: "refs/tags/v0.20.0",
			prunBranch: "refs/tags/v0.19.0",
			output:     false,
		},
		{
			name:       "different refs/heads",
			baseBranch: "refs/heads/main",
			prunBranch: "refs/heads/mains",
			output:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := branchMatch(tt.prunBranch, tt.baseBranch)
			assert.Equal(t, got, tt.output)
		})
	}
}

func TestMatchRunningPipelineRunForIncomingWebhook(t *testing.T) {
	tests := []struct {
		name              string
		runevent          info.Event
		pruns             []*tektonv1.PipelineRun
		wantedPrunsNumber int
	}{
		{
			name: "return all pipelineruns if event type is other than incoming",
			runevent: info.Event{
				EventType:         "pull_request",
				TargetPipelineRun: "pr1",
			},
			pruns: []*tektonv1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "pr1",
						Annotations: map[string]string{
							keys.OnEvent:        "[pull_request]",
							keys.OnTargetBranch: "main",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "pr2",
						Annotations: map[string]string{
							keys.OnEvent:        "[push]",
							keys.OnTargetBranch: "main",
						},
					},
				},
			},
			wantedPrunsNumber: 2,
		},
		{
			name: "return all pipelineruns if pipelinerun name is empty for incoming event",
			runevent: info.Event{
				EventType:         "incoming",
				TargetPipelineRun: "",
			},
			pruns: []*tektonv1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "pr1",
						Annotations: map[string]string{
							keys.OnEvent:        "[pull_request]",
							keys.OnTargetBranch: "main",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "pr2",
						Annotations: map[string]string{
							keys.OnEvent:        "[push]",
							keys.OnTargetBranch: "main",
						},
					},
				},
			},
			wantedPrunsNumber: 2,
		},
		{
			name: "return all pipelineruns if event type is different and incoming pipelinerun name is empty",
			runevent: info.Event{
				EventType:         "pull_request",
				TargetPipelineRun: "",
			},
			pruns: []*tektonv1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "pr1",
						Annotations: map[string]string{
							keys.OnEvent:        "[pull_request]",
							keys.OnTargetBranch: "main",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "pr2",
						Annotations: map[string]string{
							keys.OnEvent:        "[push]",
							keys.OnTargetBranch: "main",
						},
					},
				},
			},
			wantedPrunsNumber: 2,
		},
		{
			name: "return matched pipelinerun for matching pipelinerun name",
			runevent: info.Event{
				EventType:         "incoming",
				TargetPipelineRun: "pr1",
			},
			pruns: []*tektonv1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "pr1",
						Annotations: map[string]string{
							keys.OnEvent:        "[incoming]",
							keys.OnTargetBranch: "main",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "pr2",
						Annotations: map[string]string{
							keys.OnEvent:        "[incoming]",
							keys.OnTargetBranch: "main",
						},
					},
				},
			},
			wantedPrunsNumber: 1,
		},
		{
			name: "return matched pipelinerun for matching pipelinerun generateName",
			runevent: info.Event{
				EventType:         "incoming",
				TargetPipelineRun: "pr1-",
			},
			pruns: []*tektonv1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "pr1-",
						Annotations: map[string]string{
							keys.OnEvent:        "[incoming]",
							keys.OnTargetBranch: "main",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "pr2",
						Annotations: map[string]string{
							keys.OnEvent:        "[pull_request]",
							keys.OnTargetBranch: "main",
						},
					},
				},
			},
			wantedPrunsNumber: 1,
		},
		{
			name: "return nil when failing to match with an event type or a pipelinerun name",
			runevent: info.Event{
				EventType:         "incoming",
				TargetPipelineRun: "pr1",
			},
			pruns: []*tektonv1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "pr3",
						Annotations: map[string]string{
							keys.OnEvent:        "[incoming]",
							keys.OnTargetBranch: "main",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "pr2",
						Annotations: map[string]string{
							keys.OnEvent:        "[incoming]",
							keys.OnTargetBranch: "main",
						},
					},
				},
			},
			wantedPrunsNumber: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outPruns := MatchRunningPipelineRunForIncomingWebhook(tt.runevent.EventType, tt.runevent.TargetPipelineRun, tt.pruns)
			assert.Equal(t, len(outPruns), tt.wantedPrunsNumber)
		})
	}
}

func TestBuildAvailableMatchingAnnotationErr(t *testing.T) {
	tests := []struct {
		name  string
		pruns []*tektonv1.PipelineRun
		event *info.Event
	}{
		{
			name: "Test with one PipelineRun and one annotation",
			pruns: []*tektonv1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-pipeline",
						Annotations: map[string]string{
							keys.OnEvent: "pull_request",
							keys.Task:    "test-task",
						},
					},
				},
			},
			event: &info.Event{
				EventType:  "pull_request",
				HeadBranch: "feature",
				BaseBranch: "main",
			},
		},
		// Add more test cases as needed
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildAvailableMatchingAnnotationErr(tt.event, tt.pruns)
			golden.Assert(t, got, strings.ReplaceAll(fmt.Sprintf("%s.golden", t.Name()), "/", "-"))
		})
	}
}

func TestGetTargetBranch(t *testing.T) {
	tests := []struct {
		name           string
		prun           *tektonv1.PipelineRun
		event          *info.Event
		expectedMatch  bool
		expectedEvent  string
		expectedBranch string
		expectedError  string
	}{
		{
			name: "Test with pull_request event",
			prun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						keys.OnEvent:        "pull_request",
						keys.OnTargetBranch: "main",
					},
				},
			},
			event: &info.Event{
				TriggerTarget: triggertype.PullRequest,
				EventType:     triggertype.PullRequest.String(),
				BaseBranch:    "main",
			},
			expectedMatch:  true,
			expectedEvent:  "pull_request",
			expectedBranch: "main",
		},
		{
			name: "Test with pull_request event",
			prun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						keys.OnEvent:        "pull_request",
						keys.OnTargetBranch: "main",
					},
				},
			},
			event: &info.Event{
				TriggerTarget: triggertype.PullRequest,
				EventType:     triggertype.PullRequest.String(),
				BaseBranch:    "main",
			},
			expectedMatch:  true,
			expectedEvent:  "pull_request",
			expectedBranch: "main",
		},
		{
			name: "Test with incoming event",
			prun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						keys.OnEvent:        "incoming",
						keys.OnTargetBranch: "main",
					},
				},
			},
			event: &info.Event{
				TriggerTarget: triggertype.Incoming,
				EventType:     triggertype.Incoming.String(),
				BaseBranch:    "main",
			},
			expectedMatch:  true,
			expectedEvent:  "incoming",
			expectedBranch: "main",
		},
		{
			name: "Test with no match",
			prun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						keys.OnEvent:        "push",
						keys.OnTargetBranch: "develop",
					},
				},
			},
			event: &info.Event{
				TriggerTarget: triggertype.PullRequest,
				EventType:     triggertype.PullRequest.String(),
				BaseBranch:    "main",
			},
			expectedMatch:  false,
			expectedEvent:  "",
			expectedBranch: "",
		},
		{
			name: "Test empty array onEvent",
			prun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						keys.OnEvent:        "[]",
						keys.OnTargetBranch: "main",
					},
				},
			},
			event: &info.Event{
				TriggerTarget: triggertype.PullRequest,
				EventType:     "pull_request",
				BaseBranch:    "main",
			},
			expectedError: fmt.Sprintf("annotation %s is empty", keys.OnEvent),
		},
		{
			name: "Test empty array onTargetBranch",
			prun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						keys.OnEvent:        "pull_request",
						keys.OnTargetBranch: "[]",
					},
				},
			},
			event: &info.Event{
				TriggerTarget: triggertype.PullRequest,
				EventType:     "pull_request",
				BaseBranch:    "main",
			},
			expectedError: fmt.Sprintf("annotation %s is empty", keys.OnTargetBranch),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matched, targetEvent, targetBranch, err := getTargetBranch(tt.prun, tt.event)
			if tt.expectedError != "" {
				assert.Assert(t, err != nil)
				assert.Error(t, err, tt.expectedError, err.Error())
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, tt.expectedMatch, matched)
			assert.Equal(t, tt.expectedEvent, targetEvent)
			assert.Equal(t, tt.expectedBranch, targetBranch)
		})
	}
}

func TestGetName(t *testing.T) {
	tests := []struct {
		name     string
		prun     *tektonv1.PipelineRun
		expected string
	}{
		{
			name: "Test with name",
			prun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
			},
			expected: "test-pipeline",
		},
		{
			name: "Test with generateName",
			prun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "test-pipeline-",
				},
			},
			expected: "test-pipeline-",
		},
		{
			name: "Test with generateName and name",
			prun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:         "test-pipeline",
					GenerateName: "generate-pipeline-",
				},
			},
			expected: "generate-pipeline-",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := getName(tt.prun)
			assert.Equal(t, tt.expected, name)
		})
	}
}

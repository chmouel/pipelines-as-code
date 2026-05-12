package customparams

import (
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/opscomments"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	testprovider "github.com/openshift-pipelines/pipelines-as-code/pkg/test/provider"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rectesting "knative.dev/pkg/reconciler/testing"
)

func TestMakeStandardParams(t *testing.T) {
	tests := []struct {
		name  string
		event *info.Event
		repo  *v1alpha1.Repository
		want  map[string]string
	}{
		{
			name: "basic event test",
			event: &info.Event{
				SHA:               "1234567890",
				Organization:      "Org",
				Repository:        "Repo",
				BaseBranch:        "main",
				HeadBranch:        "foo",
				EventType:         "pull_request",
				Sender:            "SENDER",
				URL:               "https://paris.com",
				HeadURL:           "https://india.com",
				TriggerComment:    "\n/test me\nHelp me obiwan kenobi\r\n\r\n\r\nTo test or not to test, is the question?\n\n\n",
				PullRequestLabel:  []string{"bugs", "enhancements"},
				PullRequestNumber: 17,
			},
			repo: &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "myname",
					Namespace: "myns",
				},
			},
			want: map[string]string{
				"event_type":          "pull_request",
				"git_tag":             "",
				"pull_request_labels": "bugs\\nenhancements",
				"pull_request_number": "17",
				"repo_name":           "repo",
				"repo_owner":          "org",
				"repo_url":            "https://paris.com",
				"revision":            "1234567890",
				"sender":              "sender",
				"source_branch":       "foo",
				"source_url":          "https://india.com",
				"target_branch":       "main",
				"target_namespace":    "myns",
				"trigger_comment":     `\n/test me\nHelp me obiwan kenobi\n\n\nTo test or not to test, is the question?\n\n\n`,
			},
		},
		{
			name: "event with different clone URL",
			event: &info.Event{
				SHA:              "1234567890",
				Organization:     "Org",
				Repository:       "Repo",
				BaseBranch:       "main",
				HeadBranch:       "foo",
				EventType:        "pull_request",
				Sender:           "SENDER",
				URL:              "https://paris.com",
				HeadURL:          "https://india.com",
				TriggerComment:   "/test me\nHelp me obiwan kenobi",
				PullRequestLabel: []string{"bugs", "enhancements"},
				CloneURL:         "https://blahblah",
			},
			repo: &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "myname",
					Namespace: "myns",
				},
			},
			want: map[string]string{
				"event_type":          "pull_request",
				"git_tag":             "",
				"pull_request_labels": "bugs\\nenhancements",
				"repo_name":           "repo",
				"repo_owner":          "org",
				"repo_url":            "https://blahblah",
				"revision":            "1234567890",
				"sender":              "sender",
				"source_branch":       "foo",
				"source_url":          "https://india.com",
				"target_branch":       "main",
				"target_namespace":    "myns",
				"trigger_comment":     "/test me\\nHelp me obiwan kenobi",
			},
		},
		{
			name: "git tag push test event",
			event: &info.Event{
				SHA:            "1234567890",
				Organization:   "Org",
				Repository:     "Repo",
				BaseBranch:     "refs/tags/v1.0",
				HeadBranch:     "refs/tags/v1.0",
				EventType:      "push",
				Sender:         "SENDER",
				URL:            "https://paris.com",
				HeadURL:        "https://india.com",
				TriggerComment: "/test me\nHelp me obiwan kenobi",
				CloneURL:       "https://blahblah",
			},
			repo: &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "myname",
					Namespace: "myns",
				},
			},
			want: map[string]string{
				"event_type":          "push",
				"git_tag":             "v1.0",
				"pull_request_labels": "",
				"repo_name":           "repo",
				"repo_owner":          "org",
				"repo_url":            "https://blahblah",
				"revision":            "1234567890",
				"sender":              "sender",
				"source_branch":       "refs/tags/v1.0",
				"source_url":          "https://india.com",
				"target_branch":       "refs/tags/v1.0",
				"target_namespace":    "myns",
				"trigger_comment":     "/test me\\nHelp me obiwan kenobi",
			},
		},
		{
			name: "ops comment event maps to pull request",
			event: &info.Event{
				EventType: opscomments.TestSingleCommentEventType.String(),
			},
			repo: &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "myns",
				},
			},
			want: map[string]string{
				"event_type":          "pull_request",
				"git_tag":             "",
				"pull_request_labels": "",
				"repo_name":           "",
				"repo_owner":          "",
				"repo_url":            "",
				"revision":            "",
				"sender":              "",
				"source_branch":       "",
				"source_url":          "",
				"target_branch":       "",
				"target_namespace":    "myns",
				"trigger_comment":     "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := MakeStandardParams(tt.event, tt.repo)
			assert.DeepEqual(t, params, tt.want)
		})
	}
}

func TestMakeStandardParamsFromEvent(t *testing.T) {
	ctx, _ := rectesting.SetupFakeContext(t)
	event := &info.Event{
		SHA:               "1234567890",
		Organization:      "Org",
		Repository:        "Repo",
		BaseBranch:        "main",
		HeadBranch:        "foo",
		EventType:         "pull_request",
		Sender:            "SENDER",
		URL:               "https://paris.com",
		HeadURL:           "https://india.com",
		TriggerComment:    "/test me\nHelp me obiwan kenobi",
		PullRequestLabel:  []string{"bugs", "enhancements"},
		PullRequestNumber: 17,
	}
	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "myname",
			Namespace: "myns",
		},
	}
	vcx := &testprovider.TestProviderImp{
		WantAllChangedFiles: []string{"added.go", "deleted.go", "modified.go", "renamed.go"},
		WantAddedFiles:      []string{"added.go"},
		WantDeletedFiles:    []string{"deleted.go"},
		WantModifiedFiles:   []string{"modified.go"},
		WantRenamedFiles:    []string{"renamed.go"},
	}

	p := NewCustomParams(event, repo, nil, nil, nil, vcx)
	params, changedFiles := p.makeStandardParamsFromEvent(ctx)

	assert.Equal(t, params["pull_request_number"], "17")
	assert.Equal(t, params["repo_url"], "https://paris.com")
	assert.DeepEqual(t, changedFiles["all"], vcx.WantAllChangedFiles)
	assert.DeepEqual(t, changedFiles["added"], vcx.WantAddedFiles)
	assert.DeepEqual(t, changedFiles["deleted"], vcx.WantDeletedFiles)
	assert.DeepEqual(t, changedFiles["modified"], vcx.WantModifiedFiles)
	assert.DeepEqual(t, changedFiles["renamed"], vcx.WantRenamedFiles)
}

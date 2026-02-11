package github

import (
	"context"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"gotest.tools/v3/assert"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestParseAPICallLog(t *testing.T) {
	// Test with the real log format from the user's example
	testCases := []struct {
		name     string
		logLine  string
		expected *InstrumentationAPICall
	}{
		{
			name:    "parse valid JSON log line",
			logLine: `API Call 1: {"level":"debug","ts":"2025-08-05T16:12:17.508Z","logger":"pipelinesascode","caller":"github/profiler.go:131","msg":"GitHub API call completed","commit":"bacf698","provider":"github","event-id":"f4698b50-7216-11f0-9c6b-443ea2de733f","event-sha":"62a0b25ea7bdc3ef0e8789abff8cd797ab6cac25","event-type":"no-ops-comment","source-repo-url":"https://ghe.pipelinesascode.com/chmouel/e2e-gapps","target-branch":"main","source-branch":"pac-e2e-test-mf7r6","namespace":"pac-e2e-ns-jhh9f","operation":"get_commit","duration_ms":156,"provider":"github","repo":"pac-e2e-ns-jhh9f/pac-e2e-ns-jhh9f","url_path":"/api/v3/repos/chmouel/e2e-gapps/git/commits/62a0b25ea7bdc3ef0e8789abff8cd797ab6cac25","rate_limit_remaining":"","status_code":200}`,
			expected: &InstrumentationAPICall{
				Operation:          "get_commit",
				DurationMs:         156,
				URLPath:            "/api/v3/repos/chmouel/e2e-gapps/git/commits/62a0b25ea7bdc3ef0e8789abff8cd797ab6cac25",
				RateLimitRemaining: "",
				StatusCode:         200,
				Provider:           "github",
				Repo:               "pac-e2e-ns-jhh9f/pac-e2e-ns-jhh9f",
			},
		},
		{
			name:    "parse log line with rate limit",
			logLine: `API Call 2: {"level":"debug","ts":"2025-08-05T16:12:17.665Z","logger":"pipelinesascode","caller":"github/profiler.go:131","msg":"GitHub API call completed","operation":"get_root_tree","duration_ms":157,"provider":"github","repo":"pac-e2e-ns-jhh9f/pac-e2e-ns-jhh9f","url_path":"/api/v3/repos/chmouel/e2e-gapps/git/trees/62a0b25ea7bdc3ef0e8789abff8cd797ab6cac25","rate_limit_remaining":"4999","status_code":200}`,
			expected: &InstrumentationAPICall{
				Operation:          "get_root_tree",
				DurationMs:         157,
				URLPath:            "/api/v3/repos/chmouel/e2e-gapps/git/trees/62a0b25ea7bdc3ef0e8789abff8cd797ab6cac25",
				RateLimitRemaining: "4999",
				StatusCode:         200,
				Provider:           "github",
				Repo:               "pac-e2e-ns-jhh9f/pac-e2e-ns-jhh9f",
			},
		},
		{
			name:     "parse invalid log line",
			logLine:  "This is not a valid log line",
			expected: nil,
		},
		{
			name:     "parse log line without JSON",
			logLine:  "API Call 1: This is not JSON",
			expected: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := parseAPICallLog(tc.logLine)

			if tc.expected == nil {
				assert.Assert(t, result == nil, "Expected nil result, got %+v", result)
				return
			}

			assert.Assert(t, result != nil, "Expected result, got nil")
			assert.Equal(t, tc.expected.Operation, result.Operation)
			assert.Equal(t, tc.expected.DurationMs, result.DurationMs)
			assert.Equal(t, tc.expected.URLPath, result.URLPath)
			assert.Equal(t, tc.expected.RateLimitRemaining, result.RateLimitRemaining)
			assert.Equal(t, tc.expected.StatusCode, result.StatusCode)
			assert.Equal(t, tc.expected.Provider, result.Provider)
			assert.Equal(t, tc.expected.Repo, result.Repo)
		})
	}
}

func TestResolveControllerNamespace(t *testing.T) {
	t.Run("uses namespace from context when available", func(t *testing.T) {
		g := &PRTest{}
		ctx := info.StoreNS(context.Background(), "from-context")

		got := g.resolveControllerNamespace(ctx)
		assert.Equal(t, "from-context", got)
	})

	t.Run("falls back to install location when context has no namespace", func(t *testing.T) {
		kube := fake.NewSimpleClientset(&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pipelines-as-code-controller",
				Namespace: "openshift-pipelines",
			},
		})
		g := &PRTest{
			Cnx: &params.Run{
				Clients: clients.Clients{
					Kube: kube,
				},
			},
		}

		got := g.resolveControllerNamespace(context.Background())
		assert.Equal(t, "openshift-pipelines", got)
	})

	t.Run("returns empty when namespace cannot be resolved", func(t *testing.T) {
		g := &PRTest{}

		got := g.resolveControllerNamespace(context.Background())
		assert.Equal(t, "", got)
	})
}

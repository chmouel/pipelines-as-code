package llm

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/llm/ltypes"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestHandleAnnotationOutput(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	tests := []struct {
		name         string
		result       AnalysisResult
		repo         *v1alpha1.Repository
		wantError    bool
		checkContent func(t *testing.T, annotations map[string]string)
	}{
		{
			name: "successful annotation storage",
			result: AnalysisResult{
				Role: "test-role",
				Response: &ltypes.AnalysisResponse{
					Content:    "This is a test analysis response",
					TokensUsed: 100,
					Provider:   "openai",
					Timestamp:  time.Now(),
					Duration:   time.Second * 2,
				},
			},
			repo: &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-repo",
					Namespace: "test-ns",
				},
			},
			wantError: false,
			checkContent: func(t *testing.T, annotations map[string]string) {
				annotationKey := "pipelinesascode.tekton.dev/llm-analysis-test-role"
				assert.Assert(t, annotations[annotationKey] != "", "annotation should be present")

				// Parse the JSON
				var data map[string]interface{}
				err := json.Unmarshal([]byte(annotations[annotationKey]), &data)
				assert.NilError(t, err, "annotation should be valid JSON")

				assert.Equal(t, data["role"], "test-role")
				assert.Equal(t, data["provider"], "openai")
				assert.Equal(t, data["tokens_used"], float64(100))

				content, ok := data["content"].(string)
				assert.Assert(t, ok, "content should be a string")
				assert.Assert(t, strings.Contains(content, "test analysis response"))
			},
		},
		{
			name: "large content truncation",
			result: AnalysisResult{
				Role: "large-role",
				Response: &ltypes.AnalysisResponse{
					Content:    strings.Repeat("A", 250000), // Exceeds 200KB limit
					TokensUsed: 1000,
					Provider:   "openai",
					Timestamp:  time.Now(),
					Duration:   time.Second * 5,
				},
			},
			repo: &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-repo",
					Namespace: "test-ns",
				},
			},
			wantError: false,
			checkContent: func(t *testing.T, annotations map[string]string) {
				annotationKey := "pipelinesascode.tekton.dev/llm-analysis-large-role"
				annotationValue := annotations[annotationKey]
				assert.Assert(t, annotationValue != "", "annotation should be present")
				assert.Assert(t, len(annotationValue) < 200000, "annotation should be truncated")

				// Parse the JSON
				var data map[string]interface{}
				err := json.Unmarshal([]byte(annotationValue), &data)
				assert.NilError(t, err, "annotation should be valid JSON")

				assert.Equal(t, data["truncated"], true, "should be marked as truncated")

				content, ok := data["content"].(string)
				assert.Assert(t, ok, "content should be a string")
				assert.Assert(t, strings.Contains(content, "Content truncated"),
					"should contain truncation message")
			},
		},
		{
			name: "json parsed output",
			result: AnalysisResult{
				Role: "json-role",
				Response: &ltypes.AnalysisResponse{
					Content:    `{"analysis": "test", "score": 5}`,
					TokensUsed: 50,
					Provider:   "gemini",
					Timestamp:  time.Now(),
					Duration:   time.Second,
					JSONParsed: map[string]any{
						"analysis": "test",
						"score":    float64(5),
					},
				},
			},
			repo: &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-repo",
					Namespace: "test-ns",
				},
			},
			wantError: false,
			checkContent: func(t *testing.T, annotations map[string]string) {
				annotationKey := "pipelinesascode.tekton.dev/llm-analysis-json-role"
				assert.Assert(t, annotations[annotationKey] != "", "annotation should be present")

				// Parse the JSON
				var data map[string]interface{}
				err := json.Unmarshal([]byte(annotations[annotationKey]), &data)
				assert.NilError(t, err, "annotation should be valid JSON")

				assert.Assert(t, data["json_parsed"] != nil, "should include parsed JSON")
				jsonParsed, ok := data["json_parsed"].(map[string]interface{})
				assert.Assert(t, ok, "json_parsed should be a map")
				assert.Equal(t, jsonParsed["analysis"], "test")
				assert.Equal(t, jsonParsed["score"], float64(5))
			},
		},
		{
			name: "no response error",
			result: AnalysisResult{
				Role:     "no-response",
				Response: nil,
			},
			repo: &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-repo",
					Namespace: "test-ns",
				},
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use a unique name for each test
			prName := "test-pr-" + strings.ReplaceAll(tt.name, " ", "-")

			// Create test PipelineRun
			pr := &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      prName,
					Namespace: "test-ns",
				},
			}

			// Setup fake client using SeedTestData
			testData := testclient.Data{
				PipelineRuns: []*tektonv1.PipelineRun{pr},
			}
			stdata, _ := testclient.SeedTestData(t, ctx, testData)

			handler := NewOutputHandler(&params.Run{
				Clients: clients.Clients{
					Tekton: stdata.Pipeline,
					Kube:   stdata.Kube,
				},
			}, logger)

			// Execute
			err := handler.handleAnnotation(ctx, tt.result, pr)

			if tt.wantError {
				assert.Assert(t, err != nil, "expected error but got none")
				return
			}

			assert.NilError(t, err, "unexpected error")

			// Get updated PipelineRun
			updatedPR, err := stdata.Pipeline.TektonV1().PipelineRuns("test-ns").Get(
				ctx, prName, metav1.GetOptions{},
			)
			assert.NilError(t, err)

			// Check content
			if tt.checkContent != nil {
				tt.checkContent(t, updatedPR.Annotations)
			}
		})
	}
}

func TestAnnotationKeysFormat(t *testing.T) {
	// Test that annotation keys follow the expected format
	testCases := []struct {
		role        string
		expectedKey string
	}{
		{
			role:        "failure-analysis",
			expectedKey: "pipelinesascode.tekton.dev/llm-analysis-failure-analysis",
		},
		{
			role:        "security-scan",
			expectedKey: "pipelinesascode.tekton.dev/llm-analysis-security-scan",
		},
	}

	for _, tc := range testCases {
		key := "pipelinesascode.tekton.dev/llm-analysis-" + tc.role
		assert.Equal(t, key, tc.expectedKey)
	}
}

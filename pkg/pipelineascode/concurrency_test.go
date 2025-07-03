package pipelineascode

import (
	"testing"

	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestConcurrencyManager_Basic(t *testing.T) {
	cm := NewConcurrencyManager()
	cm.Enable()
	assert.Equal(t, cm.enabled, true)
}

func TestConcurrencyManager_AddPipelineRun(t *testing.T) {
	cm := NewConcurrencyManager()
	cm.Enable()

	testNs := "test"
	abcPR := &tektonv1.PipelineRun{ObjectMeta: metav1.ObjectMeta{Name: "abc", Namespace: testNs}}

	cm.AddPipelineRun(abcPR)
	assert.Equal(t, len(cm.pipelineRuns), 1)
}

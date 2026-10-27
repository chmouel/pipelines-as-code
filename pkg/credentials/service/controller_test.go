package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	testlogger "github.com/openshift-pipelines/pipelines-as-code/pkg/test/logger"
	tektonfake "github.com/tektoncd/pipeline/pkg/client/clientset/versioned/fake"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubefake "k8s.io/client-go/kubernetes/fake"
)

// TestControllerEnqueuesOnBuildEvents checks that build changes reach the
// request without waiting for the renewal timer.
func TestControllerEnqueuesOnBuildEvents(t *testing.T) {
	logger, _ := testlogger.GetLogger()
	kube := kubefake.NewClientset(request(bound))
	tekton := tektonfake.NewSimpleClientset(pipelineRun(prUID, now, false))
	cfg := &Config{ControlNS: ctrlNS, OutputNamespace: outNS, Resync: time.Hour}
	c, err := NewController(cfg, kube, tekton, &fakeIssuer{}, logger)
	assert.NilError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	assert.NilError(t, c.Start(ctx))

	drain := func() {
		for c.queue.Len() > 0 {
			k, _ := c.queue.Get()
			c.queue.Done(k)
		}
	}
	waitFor := func(what string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for c.queue.Len() == 0 {
			assert.Assert(t, time.Now().Before(deadline), "no enqueue after %s", what)
			time.Sleep(10 * time.Millisecond)
		}
		k, _ := c.queue.Get()
		assert.Equal(t, k, ctrlNS+"/"+reqName)
		c.queue.Done(k)
	}
	waitFor("initial list")
	drain()

	pr, err := tekton.TektonV1().PipelineRuns(outNS).Get(ctx, prName, metav1.GetOptions{})
	assert.NilError(t, err)
	pr.Labels = map[string]string{"changed": "true"}
	_, err = tekton.TektonV1().PipelineRuns(outNS).Update(ctx, pr, metav1.UpdateOptions{})
	assert.NilError(t, err)
	waitFor("pipelinerun update")

	_, err = kube.CoreV1().Pods(outNS).Create(ctx, pod(corev1.PodRunning), metav1.CreateOptions{})
	assert.NilError(t, err)
	waitFor("pod creation")

	assert.NilError(t, tekton.TektonV1().PipelineRuns(outNS).Delete(ctx, prName, metav1.DeleteOptions{}))
	waitFor("pipelinerun deletion")
}

func TestHealthHandler(t *testing.T) {
	ctx := context.Background()
	c := &Controller{}
	h := c.HealthHandler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, "/healthz", nil))
	assert.Equal(t, rec.Code, http.StatusOK)
	assert.Equal(t, rec.Body.String(), "ok")

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, "/readyz", nil))
	assert.Equal(t, rec.Code, http.StatusServiceUnavailable)

	c.ready.Store(true)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, "/readyz", nil))
	assert.Equal(t, rec.Code, http.StatusOK)
	assert.Equal(t, rec.Body.String(), "ok")
}

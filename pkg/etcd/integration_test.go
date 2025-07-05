package etcd

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestEtcdIntegration tests the full etcd integration flow.
func TestEtcdIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	// Skip if etcd is not available
	settings := map[string]string{
		"etcd-enabled": "true",
		"etcd-mode":    "mock",
	}

	if !IsEtcdEnabled(settings) {
		t.Skip("etcd is not enabled, set etcd-enabled=true to run integration tests")
	}

	logger, _ := zap.NewDevelopment()
	sugar := logger.Sugar()

	// Load configuration
	config, err := LoadConfigFromSettings(settings)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}
	if !config.Enabled {
		t.Fatal("Config should be enabled")
	}

	// Create etcd client
	client, err := NewClient(config, sugar)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Test components
	t.Run("ConcurrencyManager", func(t *testing.T) {
		testConcurrencyManager(t, client, sugar)
	})

	t.Run("QueueManager", func(t *testing.T) {
		testQueueManager(t, client, sugar)
	})

	t.Run("StateManager", func(t *testing.T) {
		testStateManager(t, client, sugar)
	})

	t.Run("EndToEndFlow", func(t *testing.T) {
		testEndToEndFlow(t, client, sugar)
	})
}

func testConcurrencyManager(t *testing.T, client Client, logger *zap.SugaredLogger) {
	cm := NewConcurrencyManager(client, logger)

	// Create test repository
	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-repo",
			Namespace: "test-namespace",
		},
		Spec: v1alpha1.RepositorySpec{
			ConcurrencyLimit: func() *int { i := 1; return &i }(),
		},
	}

	prKey := "test-pr/test-namespace"

	// Test slot acquisition
	acquired, leaseID, err := cm.AcquireSlot(context.Background(), repo, prKey)
	if err != nil {
		t.Errorf("Failed to acquire slot: %v", err)
	}
	if !acquired {
		t.Error("Expected slot to be acquired")
	}

	// Test slot release
	err = cm.ReleaseSlot(context.Background(), leaseID, "test-repo/test-namespace", prKey)
	if err != nil {
		t.Errorf("Failed to release slot: %v", err)
	}

	// Test state management
	err = cm.SetPipelineRunState(context.Background(), prKey, kubeinteraction.StateStarted)
	if err != nil {
		t.Errorf("Failed to set state: %v", err)
	}

	state, err := cm.GetPipelineRunState(context.Background(), prKey)
	if err != nil {
		t.Errorf("Failed to get state: %v", err)
	}
	if state != kubeinteraction.StateStarted {
		t.Errorf("Expected state %s, got %s", kubeinteraction.StateStarted, state)
	}

	// Cleanup
	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)
	_, err = client.Delete(context.Background(), repoKey)
	if err != nil {
		t.Errorf("Failed to cleanup repo: %v", err)
	}
	_, err = client.Delete(context.Background(), prKey)
	if err != nil {
		t.Errorf("Failed to cleanup pr: %v", err)
	}
}

func testQueueManager(t *testing.T, client Client, logger *zap.SugaredLogger) {
	qm := NewQueueManager(client, logger)

	// Create a test repository
	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-repo",
			Namespace: "test-namespace",
		},
		Spec: v1alpha1.RepositorySpec{
			ConcurrencyLimit: func() *int { i := 1; return &i }(),
		},
	}

	// Create test pipeline run key strings
	prKeys := []string{
		"test-pr-1/test-namespace",
		"test-pr-2/test-namespace",
	}

	// Initialize queues
	err := qm.InitQueues(context.Background(), nil, nil)
	if err != nil {
		t.Errorf("Failed to init queues: %v", err)
	}

	// Test adding to queue
	acquired, err := qm.AddListToRunningQueue(repo, prKeys)
	if err != nil {
		t.Errorf("Failed to add to queue: %v", err)
	}
	if len(acquired) != 1 {
		t.Errorf("Expected 1 acquired, got %d", len(acquired))
	}

	// Test removing from queue
	removed := qm.RemoveFromQueue("test-repo/test-namespace", "test-pr-1/test-namespace")
	if !removed {
		t.Error("Expected removal to succeed")
	}

	// Cleanup
	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)
	_, err = client.Delete(context.Background(), repoKey)
	if err != nil {
		t.Errorf("Failed to cleanup: %v", err)
	}
}

func testStateManager(t *testing.T, client Client, logger *zap.SugaredLogger) {
	sm := NewStateManager(client, logger)

	// Create test pipeline run
	pr := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pr",
			Namespace: "test-namespace",
		},
	}

	// Test setting state
	err := sm.SetPipelineRunState(context.Background(), pr, kubeinteraction.StateQueued)
	if err != nil {
		t.Errorf("Failed to set state: %v", err)
	}

	// Test getting state
	state, err := sm.GetPipelineRunState(context.Background(), pr)
	if err != nil {
		t.Errorf("Failed to get state: %v", err)
	}
	if state != kubeinteraction.StateQueued {
		t.Errorf("Expected state %s, got %s", kubeinteraction.StateQueued, state)
	}

	// Test updating state
	err = sm.SetPipelineRunState(context.Background(), pr, kubeinteraction.StateStarted)
	if err != nil {
		t.Errorf("Failed to update state: %v", err)
	}

	state, err = sm.GetPipelineRunState(context.Background(), pr)
	if err != nil {
		t.Errorf("Failed to get updated state: %v", err)
	}
	if state != kubeinteraction.StateStarted {
		t.Errorf("Expected state %s, got %s", kubeinteraction.StateStarted, state)
	}

	// Cleanup
	prKey := PipelineRunKey(pr.Namespace, pr.Name)
	_, err = client.Delete(context.Background(), prKey)
	if err != nil {
		t.Errorf("Failed to cleanup: %v", err)
	}
}

func testEndToEndFlow(t *testing.T, client Client, logger *zap.SugaredLogger) {
	// This test simulates a simple end-to-end flow

	// Create managers
	cm := NewConcurrencyManager(client, logger)
	sm := NewStateManager(client, logger)

	// Create repository with concurrency limit
	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "integration-test-repo",
			Namespace: "integration-test",
		},
		Spec: v1alpha1.RepositorySpec{
			ConcurrencyLimit: func() *int { i := 2; return &i }(),
		},
	}

	// Create pipeline run
	pr := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "integration-test-pr-1",
			Namespace: "integration-test",
		},
		Spec: tektonv1.PipelineRunSpec{
			Status: tektonv1.PipelineRunSpecStatusPending,
		},
	}

	// Test slot acquisition
	prKey := PipelineRunKey(pr.Namespace, pr.Name)

	acquired, leaseID, err := cm.AcquireSlot(context.Background(), repo, prKey)
	if err != nil {
		t.Errorf("Failed to acquire slot: %v", err)
	}
	if !acquired {
		t.Error("Expected slot to be acquired")
	}

	// Set state to started
	err = sm.SetPipelineRunState(context.Background(), pr, kubeinteraction.StateStarted)
	if err != nil {
		t.Errorf("Failed to set state: %v", err)
	}

	// Complete the pipeline run
	err = sm.SetPipelineRunState(context.Background(), pr, kubeinteraction.StateCompleted)
	if err != nil {
		t.Errorf("Failed to set completed state: %v", err)
	}

	// Release the slot
	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)
	err = cm.ReleaseSlot(context.Background(), leaseID, repoKey, prKey)
	if err != nil {
		t.Errorf("Failed to release slot: %v", err)
	}

	// Cleanup
	_, err = client.Delete(context.Background(), repoKey)
	if err != nil {
		t.Errorf("Failed to cleanup repo: %v", err)
	}
	_, err = client.Delete(context.Background(), prKey)
	if err != nil {
		t.Errorf("Failed to cleanup pr: %v", err)
	}
}

// TestEtcdConfigValidation tests configuration validation.
func TestEtcdConfigValidation(t *testing.T) {
	// Test valid config
	cfg := &Config{
		Endpoints:   []string{"localhost:2379"},
		DialTimeout: 5 * time.Second,
		Enabled:     true,
		Mode:        "etcd",
	}

	err := ValidateConfig(cfg)
	if err != nil {
		t.Errorf("Valid config should not fail validation: %v", err)
	}

	// Test invalid config - empty endpoints
	cfg = &Config{
		Endpoints:   []string{},
		DialTimeout: 5 * time.Second,
		Enabled:     true,
		Mode:        "etcd",
	}

	err = ValidateConfig(cfg)
	if err == nil {
		t.Error("Expected validation to fail for empty endpoints")
	}

	// Test invalid config - zero timeout
	cfg = &Config{
		Endpoints:   []string{"localhost:2379"},
		DialTimeout: 0,
		Enabled:     true,
		Mode:        "etcd",
	}

	err = ValidateConfig(cfg)
	if err == nil {
		t.Error("Expected validation to fail for zero timeout")
	}

	// Test disabled config - should not validate
	cfg = &Config{
		Endpoints:   []string{},
		DialTimeout: 0,
		Enabled:     false,
		Mode:        "memory",
	}

	err = ValidateConfig(cfg)
	if err != nil {
		t.Errorf("Disabled config should not fail validation: %v", err)
	}
}

// TestEtcdDisabled tests behavior when etcd is disabled.
func TestEtcdDisabled(t *testing.T) {
	// Test with etcd disabled in settings
	settings := map[string]string{
		"etcd-enabled": "false",
	}

	// Test that etcd is disabled
	enabled := IsEtcdEnabled(settings)
	if enabled {
		t.Error("Expected etcd to be disabled")
	}

	// Test config loading
	cfg, err := LoadConfigFromSettings(settings)
	if err != nil {
		t.Errorf("Failed to load config: %v", err)
	}
	if cfg.Enabled {
		t.Error("Expected config to be disabled")
	}
}

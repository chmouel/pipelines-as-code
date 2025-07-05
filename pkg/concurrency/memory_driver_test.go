package concurrency

import (
	"context"
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestMemoryDriver_AcquireSlot(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	sugar := logger.Sugar()

	config := &MemoryConfig{
		LeaseTTL: 1 * time.Minute,
	}

	driver, err := NewMemoryDriver(config, sugar)
	if err != nil {
		t.Fatalf("failed to create memory driver: %v", err)
	}
	defer driver.Close()

	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-repo",
			Namespace: "test-namespace",
		},
		Spec: v1alpha1.RepositorySpec{
			ConcurrencyLimit: func() *int { limit := 2; return &limit }(),
		},
	}

	ctx := context.Background()

	// Test acquiring first slot
	success, leaseID, err := driver.AcquireSlot(ctx, repo, "test-pr-1")
	if err != nil {
		t.Fatalf("failed to acquire first slot: %v", err)
	}
	if !success {
		t.Fatalf("expected to acquire first slot")
	}
	if leaseID == nil {
		t.Fatalf("expected non-nil lease ID")
	}

	// Test acquiring second slot
	success, _, err = driver.AcquireSlot(ctx, repo, "test-pr-2")
	if err != nil {
		t.Fatalf("failed to acquire second slot: %v", err)
	}
	if !success {
		t.Fatalf("expected to acquire second slot")
	}

	// Test trying to acquire third slot (should fail due to limit)
	success, _, err = driver.AcquireSlot(ctx, repo, "test-pr-3")
	if err != nil {
		t.Fatalf("unexpected error when trying to acquire third slot: %v", err)
	}
	if success {
		t.Fatalf("expected to fail acquiring third slot due to concurrency limit")
	}

	// Test releasing a slot
	err = driver.ReleaseSlot(ctx, leaseID, "test-pr-1", "test-namespace/test-repo")
	if err != nil {
		t.Fatalf("failed to release slot: %v", err)
	}

	// Test acquiring slot again after release
	success, _, err = driver.AcquireSlot(ctx, repo, "test-pr-3")
	if err != nil {
		t.Fatalf("failed to acquire slot after release: %v", err)
	}
	if !success {
		t.Fatalf("expected to acquire slot after release")
	}
}

func TestMemoryDriver_GetCurrentSlots(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	sugar := logger.Sugar()

	config := &MemoryConfig{
		LeaseTTL: 1 * time.Minute,
	}

	driver, err := NewMemoryDriver(config, sugar)
	if err != nil {
		t.Fatalf("failed to create memory driver: %v", err)
	}
	defer driver.Close()

	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-repo",
			Namespace: "test-namespace",
		},
		Spec: v1alpha1.RepositorySpec{
			ConcurrencyLimit: func() *int { limit := 3; return &limit }(),
		},
	}

	ctx := context.Background()

	// Initially should have 0 slots
	count, err := driver.GetCurrentSlots(ctx, repo)
	if err != nil {
		t.Fatalf("failed to get current slots: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 slots initially, got %d", count)
	}

	// Acquire a slot
	success, _, err := driver.AcquireSlot(ctx, repo, "test-pr-1")
	if err != nil {
		t.Fatalf("failed to acquire slot: %v", err)
	}
	if !success {
		t.Fatalf("expected to acquire slot")
	}

	// Should now have 1 slot
	count, err = driver.GetCurrentSlots(ctx, repo)
	if err != nil {
		t.Fatalf("failed to get current slots: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 slot, got %d", count)
	}
}

func TestMemoryDriver_GetRunningPipelineRuns(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	sugar := logger.Sugar()

	config := &MemoryConfig{
		LeaseTTL: 1 * time.Minute,
	}

	driver, err := NewMemoryDriver(config, sugar)
	if err != nil {
		t.Fatalf("failed to create memory driver: %v", err)
	}
	defer driver.Close()

	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-repo",
			Namespace: "test-namespace",
		},
		Spec: v1alpha1.RepositorySpec{
			ConcurrencyLimit: func() *int { limit := 2; return &limit }(),
		},
	}

	ctx := context.Background()

	// Initially should have no running pipeline runs
	running, err := driver.GetRunningPipelineRuns(ctx, repo)
	if err != nil {
		t.Fatalf("failed to get running pipeline runs: %v", err)
	}
	if len(running) != 0 {
		t.Fatalf("expected no running pipeline runs initially, got %d", len(running))
	}

	// Acquire slots
	success, _, err := driver.AcquireSlot(ctx, repo, "test-pr-1")
	if err != nil {
		t.Fatalf("failed to acquire first slot: %v", err)
	}
	if !success {
		t.Fatalf("expected to acquire first slot")
	}

	success, _, err = driver.AcquireSlot(ctx, repo, "test-pr-2")
	if err != nil {
		t.Fatalf("failed to acquire second slot: %v", err)
	}
	if !success {
		t.Fatalf("expected to acquire second slot")
	}

	// Should now have 2 running pipeline runs
	running, err = driver.GetRunningPipelineRuns(ctx, repo)
	if err != nil {
		t.Fatalf("failed to get running pipeline runs: %v", err)
	}
	if len(running) != 2 {
		t.Fatalf("expected 2 running pipeline runs, got %d", len(running))
	}

	// Check that both pipeline runs are in the list
	found1, found2 := false, false
	for _, pr := range running {
		if pr == "test-pr-1" {
			found1 = true
		}
		if pr == "test-pr-2" {
			found2 = true
		}
	}
	if !found1 || !found2 {
		t.Fatalf("expected to find both pipeline runs in running list")
	}
}

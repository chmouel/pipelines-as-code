package concurrency

import (
	"context"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"go.uber.org/zap"
)

// Example usage of the abstracted concurrency system

// ExampleEtcdUsage demonstrates how to use the etcd driver.
func ExampleEtcdUsage() {
	logger, _ := zap.NewDevelopment()
	sugar := logger.Sugar()

	// Configure etcd driver
	config := &DriverConfig{
		Driver: "etcd",
		EtcdConfig: &EtcdConfig{
			Endpoints:   []string{"localhost:2379"},
			DialTimeout: 5 * time.Second,
			Mode:        "etcd", // or "mock" for testing
		},
	}

	// Create manager
	manager, err := NewManager(config, sugar)
	if err != nil {
		sugar.Fatalf("failed to create manager: %v", err)
	}
	defer manager.Close()

	// Use the manager
	repo := &v1alpha1.Repository{
		Spec: v1alpha1.RepositorySpec{
			ConcurrencyLimit: func() *int { limit := 2; return &limit }(),
		},
	}

	ctx := context.Background()
	success, leaseID, err := manager.AcquireSlot(ctx, repo, "namespace/pipeline-run-1")
	if err != nil {
		sugar.Errorf("failed to acquire slot: %v", err)
	}

	if success {
		sugar.Infof("acquired slot with lease ID: %v", leaseID)
		// Do work...
		defer func() {
			if err := manager.ReleaseSlot(ctx, leaseID, "namespace/pipeline-run-1", "namespace/repo"); err != nil {
				sugar.Errorf("Failed to release slot: %v", err)
			}
		}()
	}
}

// ExamplePostgreSQLUsage demonstrates how to use the PostgreSQL driver.
func ExamplePostgreSQLUsage() {
	logger, _ := zap.NewDevelopment()
	sugar := logger.Sugar()

	// Configure PostgreSQL driver
	config := &DriverConfig{
		Driver: "postgresql",
		PostgreSQLConfig: &PostgreSQLConfig{
			Host:              "localhost",
			Port:              5432,
			Database:          "pac_concurrency",
			Username:          "pac_user",
			Password:          "pac_password",
			SSLMode:           "disable",
			MaxConnections:    10,
			ConnectionTimeout: 30 * time.Second,
			LeaseTTL:          1 * time.Hour,
		},
	}

	// Create manager
	manager, err := NewManager(config, sugar)
	if err != nil {
		sugar.Fatalf("failed to create manager: %v", err)
	}
	defer manager.Close()

	// Use the manager (same interface as etcd)
	repo := &v1alpha1.Repository{
		Spec: v1alpha1.RepositorySpec{
			ConcurrencyLimit: func() *int { limit := 3; return &limit }(),
		},
	}

	ctx := context.Background()
	success, leaseID, err := manager.AcquireSlot(ctx, repo, "namespace/pipeline-run-1")
	if err != nil {
		sugar.Errorf("failed to acquire slot: %v", err)
	}

	if success {
		sugar.Infof("acquired slot with lease ID: %v", leaseID)
		// Do work...
		defer func() {
			if err := manager.ReleaseSlot(ctx, leaseID, "namespace/pipeline-run-1", "namespace/repo"); err != nil {
				sugar.Errorf("Failed to release slot: %v", err)
			}
		}()
	}
}

// ExampleMemoryUsage demonstrates how to use the memory driver (for testing).
func ExampleMemoryUsage() {
	logger, _ := zap.NewDevelopment()
	sugar := logger.Sugar()

	// Configure memory driver
	config := &DriverConfig{
		Driver: "memory",
		MemoryConfig: &MemoryConfig{
			LeaseTTL: 30 * time.Minute,
		},
	}

	// Create manager
	manager, err := NewManager(config, sugar)
	if err != nil {
		sugar.Fatalf("failed to create manager: %v", err)
	}
	defer manager.Close()

	// Use the manager (same interface as other drivers)
	repo := &v1alpha1.Repository{
		Spec: v1alpha1.RepositorySpec{
			ConcurrencyLimit: func() *int { limit := 1; return &limit }(),
		},
	}

	ctx := context.Background()
	success, leaseID, err := manager.AcquireSlot(ctx, repo, "namespace/pipeline-run-1")
	if err != nil {
		sugar.Errorf("failed to acquire slot: %v", err)
	}

	if success {
		sugar.Infof("acquired slot with lease ID: %v", leaseID)
		// Do work...
		defer func() {
			if err := manager.ReleaseSlot(ctx, leaseID, "namespace/pipeline-run-1", "namespace/repo"); err != nil {
				sugar.Errorf("Failed to release slot: %v", err)
			}
		}()
	}
}

// ExampleQueueManagerUsage demonstrates how to use the queue manager.
func ExampleQueueManagerUsage() {
	logger, _ := zap.NewDevelopment()
	sugar := logger.Sugar()

	// Configure any driver
	config := &DriverConfig{
		Driver: "memory", // or "etcd" or "postgresql"
		MemoryConfig: &MemoryConfig{
			LeaseTTL: 1 * time.Hour,
		},
	}

	// Create manager
	manager, err := NewManager(config, sugar)
	if err != nil {
		sugar.Fatalf("failed to create manager: %v", err)
	}
	defer manager.Close()

	repo := &v1alpha1.Repository{
		Spec: v1alpha1.RepositorySpec{
			ConcurrencyLimit: func() *int { limit := 2; return &limit }(),
		},
	}

	// Use queue manager functionality
	pipelineRuns := []string{
		"namespace/pipeline-run-1",
		"namespace/pipeline-run-2",
		"namespace/pipeline-run-3",
	}

	ctx := context.Background()

	// Try to acquire slots for multiple pipeline runs
	acquired, err := manager.queueManager.AddListToRunningQueue(repo, pipelineRuns)
	if err != nil {
		sugar.Errorf("failed to add to running queue: %v", err)
	}

	sugar.Infof("acquired %d slots out of %d pipeline runs", len(acquired), len(pipelineRuns))

	// Get running pipeline runs
	running := manager.queueManager.RunningPipelineRuns(repo)
	sugar.Infof("currently running: %v", running)

	// Set up watcher for slot availability
	manager.queueManager.SetupWatcher(ctx, repo, func() {
		sugar.Info("slot became available, triggering reconciliation")
	})
}

package tknpac

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/sync"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func queueCommand(p *params.Run) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "queue",
		Short: "Manage PAC concurrency queues",
		Long:  "Commands for managing and troubleshooting PAC concurrency queues",
	}

	cmd.AddCommand(
		validateQueueCommand(p),
		repairQueueCommand(p),
		statusQueueCommand(p),
	)

	return cmd
}

func validateQueueCommand(p *params.Run) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate queue consistency",
		Long:  "Validate that the in-memory queue state is consistent with PipelineRun states in the cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			// Initialize clients
			if err := p.Clients.NewClients(ctx, &p.Info); err != nil {
				return fmt.Errorf("failed to initialize clients: %w", err)
			}

			// Create queue manager
			qm := sync.NewQueueManager(p.Clients.Log)

			// Validate queue consistency
			results, err := qm.ValidateQueueConsistency(ctx, p.Clients.Tekton, p.Clients.PipelineAsCode)
			if err != nil {
				return fmt.Errorf("failed to validate queue consistency: %w", err)
			}

			// Print results
			w := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)
			fmt.Fprintln(w, "REPOSITORY\tSTATUS\tRUNNING\tPENDING\tLIMIT\tERRORS\tWARNINGS")

			hasErrors := false
			for _, result := range results {
				status := "OK"
				if !result.IsValid {
					status = "ERROR"
					hasErrors = true
				}

				errorCount := len(result.Errors)
				warningCount := len(result.Warnings)

				fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%d\t%d\t%d\n",
					result.RepositoryKey, status, result.RunningCount, result.PendingCount,
					result.ExpectedCount, errorCount, warningCount)

				// Print detailed errors and warnings
				if len(result.Errors) > 0 {
					for _, err := range result.Errors {
						fmt.Fprintf(w, "\t\t\t\t\t\t%s\n", err)
					}
				}
				if len(result.Warnings) > 0 {
					for _, warning := range result.Warnings {
						fmt.Fprintf(w, "\t\t\t\t\t\t\t%s\n", warning)
					}
				}
			}
			w.Flush()

			if hasErrors {
				fmt.Fprintf(os.Stderr, "\nQueue validation found errors. Use 'tkn pac queue repair' to attempt automatic repair.\n")
				return fmt.Errorf("queue validation failed")
			}

			fmt.Fprintf(os.Stdout, "\nQueue validation completed successfully.\n")
			return nil
		},
	}

	return cmd
}

func repairQueueCommand(p *params.Run) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "repair",
		Short: "Repair queue inconsistencies",
		Long:  "Attempt to repair queue inconsistencies by removing invalid entries and rebuilding queue state",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			// Initialize clients
			if err := p.Clients.NewClients(ctx, &p.Info); err != nil {
				return fmt.Errorf("failed to initialize clients: %w", err)
			}

			// Create queue manager
			qm := sync.NewQueueManager(p.Clients.Log)

			// First validate to show current state
			fmt.Fprintf(os.Stdout, "Validating queue consistency before repair...\n")
			results, err := qm.ValidateQueueConsistency(ctx, p.Clients.Tekton, p.Clients.PipelineAsCode)
			if err != nil {
				return fmt.Errorf("failed to validate queue consistency: %w", err)
			}

			hasErrors := false
			for _, result := range results {
				if !result.IsValid {
					hasErrors = true
					break
				}
			}

			if !hasErrors {
				fmt.Fprintf(os.Stdout, "No queue inconsistencies found. Nothing to repair.\n")
				return nil
			}

			// Perform repair
			fmt.Fprintf(os.Stdout, "Repairing queue inconsistencies...\n")
			if err := qm.RepairQueue(ctx, p.Clients.Tekton, p.Clients.PipelineAsCode); err != nil {
				return fmt.Errorf("failed to repair queue: %w", err)
			}

			// Validate again to confirm repair
			fmt.Fprintf(os.Stdout, "Validating queue consistency after repair...\n")
			results, err = qm.ValidateQueueConsistency(ctx, p.Clients.Tekton, p.Clients.PipelineAsCode)
			if err != nil {
				return fmt.Errorf("failed to validate queue consistency after repair: %w", err)
			}

			hasErrors = false
			for _, result := range results {
				if !result.IsValid {
					hasErrors = true
					break
				}
			}

			if hasErrors {
				fmt.Fprintf(os.Stderr, "Queue repair completed but some inconsistencies remain.\n")
				return fmt.Errorf("queue repair incomplete")
			}

			fmt.Fprintf(os.Stdout, "Queue repair completed successfully.\n")
			return nil
		},
	}

	return cmd
}

func statusQueueCommand(p *params.Run) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show queue status",
		Long:  "Show the current status of all concurrency queues",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			// Initialize clients
			if err := p.Clients.NewClients(ctx, &p.Info); err != nil {
				return fmt.Errorf("failed to initialize clients: %w", err)
			}

			// Get all repositories with concurrency limits
			repos, err := p.Clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories("").List(ctx, metav1.ListOptions{})
			if err != nil {
				return fmt.Errorf("failed to list repositories: %w", err)
			}

			// Create queue manager and initialize queues
			qm := sync.NewQueueManager(p.Clients.Log)
			if err := qm.InitQueues(ctx, p.Clients.Tekton, p.Clients.PipelineAsCode); err != nil {
				return fmt.Errorf("failed to initialize queues: %w", err)
			}

			// Print queue status
			w := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)
			fmt.Fprintln(w, "REPOSITORY\tCONCURRENCY LIMIT\tRUNNING\tPENDING\tUTILIZATION")

			for _, repo := range repos.Items {
				if repo.Spec.ConcurrencyLimit == nil || *repo.Spec.ConcurrencyLimit == 0 {
					continue
				}

				running := qm.RunningPipelineRuns(&repo)
				pending := qm.QueuedPipelineRuns(&repo)

				utilization := float64(len(running)) / float64(*repo.Spec.ConcurrencyLimit) * 100

				fmt.Fprintf(w, "%s/%s\t%d\t%d\t%d\t%.1f%%\n",
					repo.Namespace, repo.Name, *repo.Spec.ConcurrencyLimit,
					len(running), len(pending), utilization)

				// Show running PipelineRuns
				if len(running) > 0 {
					for _, pr := range running {
						fmt.Fprintf(w, "\t\t\t%s\n", pr)
					}
				}

				// Show pending PipelineRuns (first few)
				if len(pending) > 0 {
					showCount := len(pending)
					if showCount > 3 {
						showCount = 3
					}
					for i := 0; i < showCount; i++ {
						fmt.Fprintf(w, "\t\t\t\t%s\n", pending[i])
					}
					if len(pending) > 3 {
						fmt.Fprintf(w, "\t\t\t\t... and %d more\n", len(pending)-3)
					}
				}
			}
			w.Flush()

			return nil
		},
	}

	return cmd
}

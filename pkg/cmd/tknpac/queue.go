package tknpac

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
)

func queueCommand(_ *params.Run) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "queue",
		Short: "Queue management commands (deprecated - SQLite-based queue system is now used)",
		Long:  "Queue management commands are no longer available as the system now uses a persistent SQLite-based queue.",
		RunE: func(_ *cobra.Command, _ []string) error {
			fmt.Fprintf(os.Stderr, "Queue management commands are deprecated. The system now uses a persistent SQLite-based queue that doesn't require manual management.\n")
			return nil
		},
	}

	return cmd
}

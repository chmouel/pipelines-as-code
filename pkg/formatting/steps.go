package formatting

import (
    "fmt"

    tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
)

// StepStateEmoji returns a human readable status for a step with emoji.
// - Terminated ExitCode==0: 🟢 Succeeded
// - Terminated ExitCode!=0: 🔴 Failed
// - Running: 🟡 Running
// - Otherwise: 🔄 Pending
func StepStateEmoji(s tektonv1.StepState) string {
    if s.Terminated != nil {
        if s.Terminated.ExitCode == 0 {
            return "🟢 Succeeded"
        }
        // include reason if available
        if s.Terminated.Reason != "" {
            return fmt.Sprintf("🔴 Failed (%s)", s.Terminated.Reason)
        }
        return "🔴 Failed"
    }
    if s.Running != nil {
        return "🟡 Running"
    }
    return "🔄 Pending"
}

// StepDuration formats the duration of a step using the ContainerStateTerminated timestamps if available.
// Returns a placeholder if not applicable.
func StepDuration(s tektonv1.StepState) string {
    if s.Terminated == nil || s.Terminated.StartedAt.IsZero() || s.Terminated.FinishedAt.IsZero() {
        return nonAttributedStr
    }
    // Reuse the same human readable duration formatting as other durations.
    return Duration(&s.Terminated.StartedAt, &s.Terminated.FinishedAt)
}


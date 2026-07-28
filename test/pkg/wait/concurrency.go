package wait

import (
	"fmt"
	"sort"
	"testing"
	"time"

	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"gotest.tools/v3/assert"
)

// Interval is the window a single PipelineRun occupied a concurrency slot for.
type Interval struct {
	Name  string
	Start time.Time
	End   time.Time
}

// Overlap is the busiest moment observed across a set of PipelineRuns: how many
// ran at once, when, and which ones they were.
type Overlap struct {
	Peak  int
	At    time.Time
	Names []string
}

// MaxConcurrency computes the largest number of PipelineRuns that were running
// at the same instant, from their recorded start and completion times.
//
// This is deliberately retrospective rather than a poll of the live cluster: a
// poller samples, and an overshoot that lasts less than the polling interval is
// invisible to it. Working from the recorded intervals cannot miss a window.
//
// PipelineRuns that never started are ignored. One still running when the
// snapshot was taken is treated as running until the latest end seen, which is
// the least favourable reading and so cannot hide an overshoot. Kubernetes
// timestamps have second granularity, so an interval that ends in the same
// second it started is treated as a point and cannot overlap anything.
func MaxConcurrency(prs []v1.PipelineRun) Overlap {
	intervals := make([]Interval, 0, len(prs))
	var latestEnd time.Time
	for i := range prs {
		pr := &prs[i]
		if pr.Status.StartTime == nil || pr.Status.StartTime.IsZero() {
			continue
		}
		iv := Interval{Name: pr.GetName(), Start: pr.Status.StartTime.Time}
		if pr.Status.CompletionTime != nil && !pr.Status.CompletionTime.IsZero() {
			iv.End = pr.Status.CompletionTime.Time
		}
		if iv.End.After(latestEnd) {
			latestEnd = iv.End
		}
		if iv.Start.After(latestEnd) {
			latestEnd = iv.Start
		}
		intervals = append(intervals, iv)
	}

	type edge struct {
		at    time.Time
		delta int
	}
	edges := make([]edge, 0, len(intervals)*2)
	for _, iv := range intervals {
		end := iv.End
		if end.IsZero() {
			// still running when we looked: assume it ran to the very end
			end = latestEnd
		}
		if !end.After(iv.Start) {
			// finished within the same second it started, so it never overlapped
			// anything we can prove
			continue
		}
		edges = append(edges, edge{at: iv.Start, delta: 1}, edge{at: end, delta: -1})
	}

	// ends before starts at the same instant, so a run that finishes exactly as
	// another begins is not counted as an overlap
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].at.Equal(edges[j].at) {
			return edges[i].delta < edges[j].delta
		}
		return edges[i].at.Before(edges[j].at)
	})

	var result Overlap
	current := 0
	for _, e := range edges {
		current += e.delta
		if current > result.Peak {
			result.Peak = current
			result.At = e.at
		}
	}

	for _, iv := range intervals {
		end := iv.End
		if end.IsZero() {
			end = latestEnd
		}
		if !iv.Start.After(result.At) && end.After(result.At) {
			result.Names = append(result.Names, iv.Name)
		}
	}
	sort.Strings(result.Names)
	return result
}

// AssertMaxConcurrency fails the test if more than limit PipelineRuns were ever
// running at the same time.
//
// Asserting that queued PipelineRuns all eventually succeed says nothing about
// whether the limit was honoured, since a queue that ignores the limit entirely
// also finishes every run. This is the assertion that actually tests the queue.
func AssertMaxConcurrency(t *testing.T, prs []v1.PipelineRun, limit int) {
	t.Helper()
	got := MaxConcurrency(prs)
	assert.Assert(t, got.Peak <= limit,
		"concurrency limit of %d exceeded: %d PipelineRuns were running at once at %s: %v\n%s",
		limit, got.Peak, got.At.Format(time.RFC3339), got.Names, formatIntervals(prs))
}

func formatIntervals(prs []v1.PipelineRun) string {
	out := "observed PipelineRun windows:\n"
	sorted := make([]v1.PipelineRun, len(prs))
	copy(sorted, prs)
	SortPipelineRunsByCreationMillis(sorted)
	for i := range sorted {
		pr := &sorted[i]
		start, end := "never started", "still running"
		if pr.Status.StartTime != nil && !pr.Status.StartTime.IsZero() {
			start = pr.Status.StartTime.Format(time.RFC3339)
		}
		if pr.Status.CompletionTime != nil && !pr.Status.CompletionTime.IsZero() {
			end = pr.Status.CompletionTime.Format(time.RFC3339)
		}
		out += fmt.Sprintf("  %-50s %s -> %s\n", pr.GetName(), start, end)
	}
	return out
}

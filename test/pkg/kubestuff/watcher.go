package kubestuff

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	watcherNamespace  = "pipelines-as-code"
	watcherSelector   = "app=pipelines-as-code-watcher"
	watcherDeployment = "pipelines-as-code-watcher"
)

// WatcherHealth is a snapshot of how many times the watcher has restarted.
type WatcherHealth struct {
	restarts map[string]int32
	runcnx   *params.Run
}

// SnapshotWatcherHealth records the current restart count of every watcher pod.
//
// A watcher that aborts is restarted by Kubernetes and silently rebuilds its
// queues, so a crash usually leaves the test passing and no assertion looking
// at it. Comparing restart counts across the test is what makes it visible.
func SnapshotWatcherHealth(ctx context.Context, t *testing.T, runcnx *params.Run) *WatcherHealth {
	t.Helper()
	h := &WatcherHealth{restarts: map[string]int32{}, runcnx: runcnx}
	pods := watcherPods(ctx, t, runcnx)
	for i := range pods {
		h.restarts[pods[i].GetName()] = totalRestarts(&pods[i])
	}
	return h
}

// Assert fails the test if any watcher pod restarted since the snapshot, or if
// its log contains a Go panic or an unrecoverable runtime error.
func (h *WatcherHealth) Assert(ctx context.Context, t *testing.T) {
	t.Helper()
	pods := watcherPods(ctx, t, h.runcnx)
	for i := range pods {
		pod := &pods[i]
		name := pod.GetName()
		now := totalRestarts(pod)
		before, seen := h.restarts[name]
		if !seen {
			// a pod that appeared during the test means the previous one went away
			assert.Assert(t, now == 0,
				"watcher pod %s appeared mid-test and has already restarted %d times:\n%s",
				name, now, watcherLog(ctx, h.runcnx, name))
			continue
		}
		assert.Assert(t, now == before,
			"watcher pod %s restarted %d time(s) during the test, it most likely crashed:\n%s",
			name, now-before, watcherLog(ctx, h.runcnx, name))
	}
	assertNoWatcherCrash(ctx, t, h.runcnx, pods)
}

func assertNoWatcherCrash(ctx context.Context, t *testing.T, runcnx *params.Run, pods []corev1.Pod) {
	t.Helper()
	for i := range pods {
		log := watcherLog(ctx, runcnx, pods[i].GetName())
		for _, needle := range []string{"panic: ", "fatal error: ", "WARNING: DATA RACE"} {
			assert.Assert(t, !strings.Contains(log, needle),
				"watcher pod %s log contains %q:\n%s", pods[i].GetName(), needle, log)
		}
	}
}

// BounceWatcher scales the watcher down to zero and back up again, waiting for
// it to come back ready. This is how a test forces the queue to be rebuilt from
// what is already in the cluster.
func BounceWatcher(ctx context.Context, t *testing.T, runcnx *params.Run) {
	t.Helper()
	ScaleDeployment(ctx, t, runcnx, 0, watcherDeployment, watcherNamespace)
	waitForWatcherReplicas(ctx, t, runcnx, 0)
	ScaleDeployment(ctx, t, runcnx, 1, watcherDeployment, watcherNamespace)
	waitForWatcherReplicas(ctx, t, runcnx, 1)
}

func waitForWatcherReplicas(ctx context.Context, t *testing.T, runcnx *params.Run, want int32) {
	t.Helper()
	for range 60 {
		dep, err := runcnx.Clients.Kube.AppsV1().Deployments(watcherNamespace).Get(ctx, watcherDeployment, metav1.GetOptions{})
		assert.NilError(t, err)
		if dep.Status.ReadyReplicas == want && dep.Status.Replicas == want {
			runcnx.Clients.Log.Infof("watcher is at %d ready replica(s)", want)
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("watcher did not settle at %d ready replica(s) within 2 minutes", want)
}

func watcherPods(ctx context.Context, t *testing.T, runcnx *params.Run) []corev1.Pod {
	t.Helper()
	pods, err := runcnx.Clients.Kube.CoreV1().Pods(watcherNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: watcherSelector,
	})
	assert.NilError(t, err)
	assert.Assert(t, len(pods.Items) > 0, "no watcher pod found in %s with selector %s", watcherNamespace, watcherSelector)
	return pods.Items
}

func totalRestarts(pod *corev1.Pod) int32 {
	var total int32
	for _, cs := range pod.Status.ContainerStatuses {
		total += cs.RestartCount
	}
	return total
}

// watcherLog returns the previous container log when there is one, since that
// is where a crash is recorded, and falls back to the current one.
func watcherLog(ctx context.Context, runcnx *params.Run, podName string) string {
	for _, previous := range []bool{true, false} {
		req := runcnx.Clients.Kube.CoreV1().Pods(watcherNamespace).GetLogs(podName, &corev1.PodLogOptions{
			Previous: previous,
		})
		stream, err := req.Stream(ctx)
		if err != nil {
			continue
		}
		out, err := io.ReadAll(stream)
		_ = stream.Close()
		if err != nil || len(out) == 0 {
			continue
		}
		return fmt.Sprintf("--- watcher log (previous=%v) ---\n%s", previous, string(out))
	}
	return "(no watcher log available)"
}

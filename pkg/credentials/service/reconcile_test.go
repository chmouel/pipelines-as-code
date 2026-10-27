package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/credentials"
	testlogger "github.com/openshift-pipelines/pipelines-as-code/pkg/test/logger"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	tektonv1beta1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	tektonfake "github.com/tektoncd/pipeline/pkg/client/clientset/versioned/fake"
	tektonv1listers "github.com/tektoncd/pipeline/pkg/client/listers/pipeline/v1"
	tektonv1beta1listers "github.com/tektoncd/pipeline/pkg/client/listers/pipeline/v1beta1"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kubefake "k8s.io/client-go/kubernetes/fake"
	corev1listers "k8s.io/client-go/listers/core/v1"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
)

const (
	ctrlNS     = "pipelines-as-code"
	outNS      = "pac-credentials-poc"
	repoID     = 42
	reqName    = "private-clone-request"
	secretName = "private-clone-auth"
	prName     = "private-clone"
	reqUID     = "request-uid"
	prUID      = "pr-uid"
)

var (
	now        = time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	reqCreated = metav1.NewTime(now.Add(-time.Minute))
)

type fakeIssuer struct {
	calls int
	err   error
	now   time.Time
}

func (f *fakeIssuer) Issue(context.Context) (Token, error) {
	f.calls++
	if f.err != nil {
		return Token{}, f.err
	}
	return Token{Value: "token-" + string(rune('0'+f.calls)), ExpiresAt: f.now.Add(time.Hour)}, nil
}

type harness struct {
	kube                     *kubefake.Clientset
	tekton                   *tektonfake.Clientset
	issuer                   *fakeIssuer
	c                        *Controller
	cms, prs, trs, crs, pods cache.Indexer
}

func newIndexer() cache.Indexer {
	return cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
}

func newHarness(kubeObjs, tektonObjs []runtime.Object) *harness {
	logger, _ := testlogger.GetLogger()
	h := &harness{
		kube:   kubefake.NewClientset(kubeObjs...),
		tekton: tektonfake.NewSimpleClientset(tektonObjs...),
		issuer: &fakeIssuer{now: now},
		cms:    newIndexer(), prs: newIndexer(), trs: newIndexer(), crs: newIndexer(), pods: newIndexer(),
	}
	h.c = &Controller{
		cfg: &Config{
			RepositoryID: repoID, RepositoryURL: "https://github.com/org/private-repo",
			ControlNS: ctrlNS, OutputNamespace: outNS, RenewBefore: 15 * time.Minute, UnboundWarnAfter: 10 * time.Minute,
		},
		kube: h.kube, tekton: h.tekton, issuer: h.issuer, logger: logger,
		now:          func() time.Time { return now },
		requests:     corev1listers.NewConfigMapLister(h.cms),
		pods:         corev1listers.NewPodLister(h.pods),
		pipelineRuns: tektonv1listers.NewPipelineRunLister(h.prs),
		taskRuns:     tektonv1listers.NewTaskRunLister(h.trs),
		customRuns:   tektonv1beta1listers.NewCustomRunLister(h.crs),
	}
	return h
}

// sync copies the fake API state into the listers, like informers would.
func (h *harness) sync(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	cms, err := h.kube.CoreV1().ConfigMaps(ctrlNS).List(ctx, metav1.ListOptions{})
	assert.NilError(t, err)
	pods, err := h.kube.CoreV1().Pods(outNS).List(ctx, metav1.ListOptions{})
	assert.NilError(t, err)
	prs, err := h.tekton.TektonV1().PipelineRuns(outNS).List(ctx, metav1.ListOptions{})
	assert.NilError(t, err)
	trs, err := h.tekton.TektonV1().TaskRuns(outNS).List(ctx, metav1.ListOptions{})
	assert.NilError(t, err)
	crs, err := h.tekton.TektonV1beta1().CustomRuns(outNS).List(ctx, metav1.ListOptions{})
	assert.NilError(t, err)
	assert.NilError(t, h.cms.Replace(asObjects(cms.Items), ""))
	assert.NilError(t, h.pods.Replace(asObjects(pods.Items), ""))
	assert.NilError(t, h.prs.Replace(asObjects(prs.Items), ""))
	assert.NilError(t, h.trs.Replace(asObjects(trs.Items), ""))
	assert.NilError(t, h.crs.Replace(asObjects(crs.Items), ""))
}

func asObjects[T any](items []T) []any {
	objs := make([]any, 0, len(items))
	for i := range items {
		objs = append(objs, &items[i])
	}
	return objs
}

func (h *harness) reconcile(t *testing.T) (time.Duration, error) {
	t.Helper()
	h.sync(t)
	return h.c.reconcile(context.Background(), ctrlNS+"/"+reqName)
}

func (h *harness) request(t *testing.T) *corev1.ConfigMap {
	t.Helper()
	cm, err := h.kube.CoreV1().ConfigMaps(ctrlNS).Get(context.Background(), reqName, metav1.GetOptions{})
	assert.NilError(t, err)
	return cm
}

func (h *harness) secret(t *testing.T) *corev1.Secret {
	t.Helper()
	s, err := h.kube.CoreV1().Secrets(outNS).Get(context.Background(), secretName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	assert.NilError(t, err)
	return s
}

func request(mods ...func(*corev1.ConfigMap)) *corev1.ConfigMap {
	cm := credentials.NewRequestConfigMap(ctrlNS, reqName, credentials.Request{
		RepositoryID: repoID, TargetNamespace: outNS, SecretName: secretName, PipelineRunName: prName,
	})
	cm.UID = reqUID
	cm.CreationTimestamp = reqCreated
	cm.Annotations = map[string]string{}
	for _, m := range mods {
		m(cm)
	}
	return cm
}

func bound(cm *corev1.ConfigMap) {
	cm.Annotations[credentials.AnnotationBoundUID] = prUID
	cm.Annotations[credentials.AnnotationState] = string(credentials.StateBound)
}

func completed(cm *corev1.ConfigMap) {
	bound(cm)
	cm.Annotations[credentials.AnnotationState] = string(credentials.StateCompleted)
}

func ownedSecret(expiry time.Time, mods ...func(*corev1.Secret)) *corev1.Secret {
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: secretName, Namespace: outNS, UID: "secret-uid",
			Labels: map[string]string{credentials.ManagedByLabel: credentials.ManagedByValue},
			Annotations: map[string]string{
				credentials.AnnotationRequest:    ctrlNS + "/" + reqName,
				credentials.AnnotationRequestUID: reqUID,
				credentials.AnnotationExpiresAt:  credentials.FormatTime(expiry),
			},
		},
		Data: map[string][]byte{"git-provider-token": []byte("token-old")},
	}
	for _, m := range mods {
		m(s)
	}
	return s
}

type childRef struct{ kind, name string }

func pipelineRun(uid string, created time.Time, done bool, children ...childRef) *tektonv1.PipelineRun {
	pr := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{Name: prName, Namespace: outNS, UID: types.UID(uid), CreationTimestamp: metav1.NewTime(created)},
	}
	if done {
		pr.Status.CompletionTime = &metav1.Time{Time: now.Add(-time.Minute)}
		pr.Status.SetCondition(&apis.Condition{Type: apis.ConditionSucceeded, Status: corev1.ConditionTrue})
	}
	for _, c := range children {
		pr.Status.ChildReferences = append(pr.Status.ChildReferences, tektonv1.ChildStatusReference{
			TypeMeta: runtime.TypeMeta{Kind: c.kind}, Name: c.name,
		})
	}
	return pr
}

func doneStatus(done bool) duckv1.Status {
	s := duckv1.Status{}
	status := corev1.ConditionUnknown
	if done {
		status = corev1.ConditionTrue
	}
	s.SetConditions(apis.Conditions{{Type: apis.ConditionSucceeded, Status: status}})
	return s
}

func childLabels() map[string]string {
	return map[string]string{pipeline.PipelineRunLabelKey: prName, pipeline.PipelineRunUIDLabelKey: prUID}
}

func taskRun(name string, done bool) *tektonv1.TaskRun {
	tr := &tektonv1.TaskRun{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: outNS, Labels: childLabels()}}
	tr.Status.Status = doneStatus(done)
	return tr
}

func customRun(name string, done bool) *tektonv1beta1.CustomRun {
	cr := &tektonv1beta1.CustomRun{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: outNS, Labels: childLabels()}}
	cr.Status.Status = doneStatus(done)
	return cr
}

func pod(phase corev1.PodPhase) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "private-clone-pod", Namespace: outNS, Labels: childLabels()},
		Status:     corev1.PodStatus{Phase: phase},
	}
}

func hasOwnerRef(s *corev1.Secret, uid string) bool {
	for _, r := range s.OwnerReferences {
		if string(r.UID) == uid {
			return true
		}
	}
	return false
}

func state(cm *corev1.ConfigMap) credentials.State {
	return credentials.State(cm.Annotations[credentials.AnnotationState])
}

func TestReconcile(t *testing.T) {
	freshExpiry := now.Add(50 * time.Minute)
	dueExpiry := now.Add(10 * time.Minute)
	overdue := now.Add(-10 * time.Minute)
	prCreated := now

	tests := []struct {
		name       string
		kubeObjs   []runtime.Object
		tektonObjs []runtime.Object
		setup      func(h *harness)
		wantErr    string
		wantMints  int
		check      func(t *testing.T, h *harness, after time.Duration)
	}{
		{
			name:      "new request creates secret before reporting ready",
			kubeObjs:  []runtime.Object{request()},
			wantMints: 1,
			check: func(t *testing.T, h *harness, after time.Duration) {
				s := h.secret(t)
				assert.Assert(t, s != nil)
				assert.Assert(t, strings.Contains(string(s.Data[".git-credentials"]), "token-1@github.com/org/private-repo"))
				assert.Equal(t, string(s.Data["git-provider-token"]), "token-1")
				assert.Assert(t, s.Data[".gitconfig"] != nil)
				assert.Equal(t, s.Annotations[credentials.AnnotationExpiresAt], credentials.FormatTime(now.Add(time.Hour)))
				cm := h.request(t)
				assert.Equal(t, state(cm), credentials.StateReady)
				assert.Equal(t, cm.Annotations[credentials.AnnotationExpiresAt], credentials.FormatTime(now.Add(time.Hour)))
				assert.Equal(t, cm.Annotations[credentials.AnnotationUnboundSince], credentials.FormatTime(now))
				assert.Equal(t, after, 45*time.Minute)
			},
		},
		{
			name:     "restart adopts stored credentials without minting",
			kubeObjs: []runtime.Object{request(), ownedSecret(freshExpiry)},
			check: func(t *testing.T, h *harness, after time.Duration) {
				assert.Equal(t, string(h.secret(t).Data["git-provider-token"]), "token-old")
				assert.Equal(t, h.request(t).Annotations[credentials.AnnotationExpiresAt], credentials.FormatTime(freshExpiry))
				assert.Equal(t, after, 35*time.Minute)
			},
		},
		{
			name:      "restart renews overdue credentials immediately",
			kubeObjs:  []runtime.Object{request(), ownedSecret(overdue)},
			wantMints: 1,
			check: func(t *testing.T, h *harness, _ time.Duration) {
				s := h.secret(t)
				assert.Equal(t, string(s.Data["git-provider-token"]), "token-1")
				assert.Equal(t, s.Annotations[credentials.AnnotationExpiresAt], credentials.FormatTime(now.Add(time.Hour)))
				assert.Equal(t, h.request(t).Annotations[credentials.AnnotationRenewedAt], credentials.FormatTime(now))
			},
		},
		{
			name:       "renews bound credentials inside the renewal window",
			kubeObjs:   []runtime.Object{request(bound), ownedSecret(dueExpiry)},
			tektonObjs: []runtime.Object{pipelineRun(prUID, prCreated, false)},
			wantMints:  1,
			check: func(t *testing.T, h *harness, after time.Duration) {
				assert.Equal(t, string(h.secret(t).Data["git-provider-token"]), "token-1")
				assert.Equal(t, state(h.request(t)), credentials.StateBound)
				assert.Equal(t, after, 45*time.Minute)
			},
		},
		{
			name:      "GitHub failure keeps previous credentials and expiry",
			kubeObjs:  []runtime.Object{request(), ownedSecret(dueExpiry)},
			setup:     func(h *harness) { h.issuer.err = errors.New("github is down") },
			wantErr:   "github is down",
			wantMints: 1,
			check: func(t *testing.T, h *harness, _ time.Duration) {
				s := h.secret(t)
				assert.Equal(t, string(s.Data["git-provider-token"]), "token-old")
				assert.Equal(t, s.Annotations[credentials.AnnotationExpiresAt], credentials.FormatTime(dueExpiry))
				cm := h.request(t)
				assert.Equal(t, state(cm), credentials.StateError)
				assert.Equal(t, cm.Annotations[credentials.AnnotationExpiresAt], credentials.FormatTime(dueExpiry))
				assert.Assert(t, strings.Contains(cm.Annotations[credentials.AnnotationMessage], "github is down"))
			},
		},
		{
			name:     "failed secret write keeps previous expiry",
			kubeObjs: []runtime.Object{request(), ownedSecret(dueExpiry)},
			setup: func(h *harness) {
				h.kube.PrependReactor("update", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
					return true, nil, errors.New("etcd unavailable")
				})
			},
			wantErr:   "etcd unavailable",
			wantMints: 1,
			check: func(t *testing.T, h *harness, _ time.Duration) {
				assert.Equal(t, string(h.secret(t).Data["git-provider-token"]), "token-old")
				cm := h.request(t)
				assert.Equal(t, state(cm), credentials.StateError)
				assert.Equal(t, cm.Annotations[credentials.AnnotationExpiresAt], credentials.FormatTime(dueExpiry))
			},
		},
		{
			name:     "request for another repository is rejected",
			kubeObjs: []runtime.Object{request(func(cm *corev1.ConfigMap) { cm.Data[credentials.DataRepositoryID] = "7" })},
			check: func(t *testing.T, h *harness, _ time.Duration) {
				assert.Assert(t, h.secret(t) == nil)
				cm := h.request(t)
				assert.Equal(t, state(cm), credentials.StateRejected)
				assert.Assert(t, strings.Contains(cm.Annotations[credentials.AnnotationMessage], "repository 7"))
			},
		},
		{
			name:     "request for another namespace is rejected",
			kubeObjs: []runtime.Object{request(func(cm *corev1.ConfigMap) { cm.Data[credentials.DataTargetNamespace] = "other" })},
			check: func(t *testing.T, h *harness, _ time.Duration) {
				assert.Equal(t, state(h.request(t)), credentials.StateRejected)
			},
		},
		{
			name:     "mutable request is rejected",
			kubeObjs: []runtime.Object{request(func(cm *corev1.ConfigMap) { cm.Immutable = nil })},
			check: func(t *testing.T, h *harness, _ time.Duration) {
				assert.Equal(t, state(h.request(t)), credentials.StateRejected)
				assert.Assert(t, h.secret(t) == nil)
			},
		},
		{
			name: "foreign secret is never overwritten",
			kubeObjs: []runtime.Object{request(), &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: outNS},
				Data:       map[string][]byte{"git-provider-token": []byte("someone-else")},
			}},
			check: func(t *testing.T, h *harness, _ time.Duration) {
				assert.Equal(t, string(h.secret(t).Data["git-provider-token"]), "someone-else")
				assert.Equal(t, state(h.request(t)), credentials.StateConflict)
			},
		},
		{
			name: "secret owned by another request is a conflict",
			kubeObjs: []runtime.Object{request(), ownedSecret(overdue, func(s *corev1.Secret) {
				s.Annotations[credentials.AnnotationRequestUID] = "other-request"
			})},
			check: func(t *testing.T, h *harness, _ time.Duration) {
				assert.Equal(t, string(h.secret(t).Data["git-provider-token"]), "token-old")
				assert.Equal(t, state(h.request(t)), credentials.StateConflict)
			},
		},
		{
			name: "binds the intended build and attaches the secret",
			kubeObjs: []runtime.Object{request(func(cm *corev1.ConfigMap) {
				cm.Annotations[credentials.AnnotationUnboundSince] = credentials.FormatTime(now.Add(-time.Minute))
			}), ownedSecret(freshExpiry)},
			tektonObjs: []runtime.Object{pipelineRun(prUID, prCreated, false)},
			check: func(t *testing.T, h *harness, _ time.Duration) {
				cm := h.request(t)
				assert.Equal(t, state(cm), credentials.StateBound)
				assert.Equal(t, cm.Annotations[credentials.AnnotationBoundUID], prUID)
				assert.Equal(t, cm.Annotations[credentials.AnnotationUnboundSince], "")
				s := h.secret(t)
				assert.Assert(t, hasOwnerRef(s, prUID))
				assert.Equal(t, *s.OwnerReferences[0].BlockOwnerDeletion, false)
			},
		},
		{
			name:       "build created before the request is not bound",
			kubeObjs:   []runtime.Object{request(), ownedSecret(freshExpiry)},
			tektonObjs: []runtime.Object{pipelineRun(prUID, reqCreated.Add(-time.Hour), false)},
			check: func(t *testing.T, h *harness, _ time.Duration) {
				cm := h.request(t)
				assert.Equal(t, state(cm), credentials.StateReady)
				assert.Equal(t, cm.Annotations[credentials.AnnotationBoundUID], "")
				assert.Equal(t, len(h.secret(t).OwnerReferences), 0)
			},
		},
		{
			name: "keeps other owner references",
			kubeObjs: []runtime.Object{request(bound), ownedSecret(freshExpiry, func(s *corev1.Secret) {
				s.OwnerReferences = []metav1.OwnerReference{{APIVersion: "v1", Kind: "ConfigMap", Name: "other", UID: "other-uid"}}
			})},
			tektonObjs: []runtime.Object{pipelineRun(prUID, prCreated, false)},
			check: func(t *testing.T, h *harness, _ time.Duration) {
				s := h.secret(t)
				assert.Equal(t, len(s.OwnerReferences), 2)
				assert.Assert(t, hasOwnerRef(s, "other-uid"))
				assert.Assert(t, hasOwnerRef(s, prUID))
			},
		},
		{
			name:       "finished build stops renewal even when overdue",
			kubeObjs:   []runtime.Object{request(bound), ownedSecret(overdue)},
			tektonObjs: []runtime.Object{pipelineRun(prUID, prCreated, true, childRef{"TaskRun", "clone"}), taskRun("clone", true)},
			check: func(t *testing.T, h *harness, after time.Duration) {
				assert.Equal(t, state(h.request(t)), credentials.StateCompleted)
				assert.Equal(t, string(h.secret(t).Data["git-provider-token"]), "token-old")
				assert.Equal(t, after, time.Duration(0))
			},
		},
		{
			name:       "running child task keeps renewing",
			kubeObjs:   []runtime.Object{request(bound), ownedSecret(dueExpiry)},
			tektonObjs: []runtime.Object{pipelineRun(prUID, prCreated, true, childRef{"TaskRun", "finally"}), taskRun("finally", false)},
			wantMints:  1,
			check: func(t *testing.T, h *harness, _ time.Duration) {
				assert.Equal(t, state(h.request(t)), credentials.StateBound)
			},
		},
		{
			name:       "running pod keeps renewing",
			kubeObjs:   []runtime.Object{request(bound), ownedSecret(dueExpiry), pod(corev1.PodRunning)},
			tektonObjs: []runtime.Object{pipelineRun(prUID, prCreated, true)},
			wantMints:  1,
			check: func(t *testing.T, h *harness, _ time.Duration) {
				assert.Equal(t, state(h.request(t)), credentials.StateBound)
			},
		},
		{
			name:       "custom run must finish before completion",
			kubeObjs:   []runtime.Object{request(bound), ownedSecret(dueExpiry)},
			tektonObjs: []runtime.Object{pipelineRun(prUID, prCreated, true, childRef{"CustomRun", "approval"}), customRun("approval", false)},
			wantMints:  1,
			check: func(t *testing.T, h *harness, _ time.Duration) {
				assert.Equal(t, state(h.request(t)), credentials.StateBound)
			},
		},
		{
			name:       "finished custom run and succeeded pod complete the request",
			kubeObjs:   []runtime.Object{request(bound), ownedSecret(dueExpiry), pod(corev1.PodSucceeded)},
			tektonObjs: []runtime.Object{pipelineRun(prUID, prCreated, true, childRef{"CustomRun", "approval"}), customRun("approval", true)},
			check: func(t *testing.T, h *harness, _ time.Duration) {
				assert.Equal(t, state(h.request(t)), credentials.StateCompleted)
			},
		},
		{
			name:       "deleted child counts as finished",
			kubeObjs:   []runtime.Object{request(bound), ownedSecret(dueExpiry)},
			tektonObjs: []runtime.Object{pipelineRun(prUID, prCreated, true, childRef{"TaskRun", "gone"})},
			check: func(t *testing.T, h *harness, _ time.Duration) {
				assert.Equal(t, state(h.request(t)), credentials.StateCompleted)
			},
		},
		{
			name:       "unbound request for an already finished build gets no new token",
			kubeObjs:   []runtime.Object{request()},
			tektonObjs: []runtime.Object{pipelineRun(prUID, prCreated, true)},
			check: func(t *testing.T, h *harness, _ time.Duration) {
				cm := h.request(t)
				assert.Equal(t, state(cm), credentials.StateCompleted)
				assert.Equal(t, cm.Annotations[credentials.AnnotationBoundUID], prUID)
				assert.Assert(t, h.secret(t) == nil)
			},
		},
		{
			name:       "completed request never recreates a deleted secret",
			kubeObjs:   []runtime.Object{request(completed)},
			tektonObjs: []runtime.Object{pipelineRun(prUID, prCreated, true)},
			check: func(t *testing.T, h *harness, _ time.Duration) {
				assert.Assert(t, h.secret(t) == nil)
				assert.Equal(t, state(h.request(t)), credentials.StateCompleted)
			},
		},
		{
			name:     "deleted build cleans up during a GitHub outage",
			kubeObjs: []runtime.Object{request(bound), ownedSecret(overdue)},
			setup:    func(h *harness) { h.issuer.err = errors.New("github is down") },
			check: func(t *testing.T, h *harness, _ time.Duration) {
				assert.Assert(t, h.secret(t) == nil)
				_, err := h.kube.CoreV1().ConfigMaps(ctrlNS).Get(context.Background(), reqName, metav1.GetOptions{})
				assert.Assert(t, apierrors.IsNotFound(err))
			},
		},
		{
			name:       "name reused by another build is never adopted",
			kubeObjs:   []runtime.Object{request(bound), ownedSecret(freshExpiry)},
			tektonObjs: []runtime.Object{pipelineRun("new-build-uid", prCreated, false)},
			check: func(t *testing.T, h *harness, _ time.Duration) {
				assert.Assert(t, h.secret(t) == nil)
				_, err := h.kube.CoreV1().ConfigMaps(ctrlNS).Get(context.Background(), reqName, metav1.GetOptions{})
				assert.Assert(t, apierrors.IsNotFound(err))
			},
		},
		{
			name:       "ownerRef is repaired during a GitHub outage",
			kubeObjs:   []runtime.Object{request(bound), ownedSecret(overdue)},
			tektonObjs: []runtime.Object{pipelineRun(prUID, prCreated, false)},
			setup:      func(h *harness) { h.issuer.err = errors.New("github is down") },
			wantErr:    "github is down",
			wantMints:  1,
			check: func(t *testing.T, h *harness, _ time.Duration) {
				assert.Assert(t, hasOwnerRef(h.secret(t), prUID))
			},
		},
		{
			name:     "lookup errors never trigger cleanup",
			kubeObjs: []runtime.Object{request(bound), ownedSecret(freshExpiry)},
			setup: func(h *harness) {
				h.tekton.PrependReactor("get", "pipelineruns", func(k8stesting.Action) (bool, runtime.Object, error) {
					return true, nil, errors.New("apiserver timeout")
				})
			},
			wantErr: "apiserver timeout",
			check: func(t *testing.T, h *harness, _ time.Duration) {
				assert.Assert(t, h.secret(t) != nil)
				assert.Equal(t, state(h.request(t)), credentials.StateBound)
			},
		},
		{
			name:       "missing secret of a running bound build is recreated and attached",
			kubeObjs:   []runtime.Object{request(bound)},
			tektonObjs: []runtime.Object{pipelineRun(prUID, prCreated, false)},
			wantMints:  1,
			check: func(t *testing.T, h *harness, _ time.Duration) {
				s := h.secret(t)
				assert.Assert(t, s != nil)
				assert.Assert(t, hasOwnerRef(s, prUID))
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(tt.kubeObjs, tt.tektonObjs)
			if tt.setup != nil {
				tt.setup(h)
			}
			after, err := h.reconcile(t)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
			} else {
				assert.NilError(t, err)
			}
			assert.Equal(t, h.issuer.calls, tt.wantMints)
			if tt.check != nil {
				tt.check(t, h, after)
			}
		})
	}
}

// TestReconcileInterrupted runs reconcile twice to show that a second pass
// after a partial write converges without minting again.
func TestReconcileInterrupted(t *testing.T) {
	h := newHarness([]runtime.Object{request()}, nil)
	failStatus := true
	h.kube.PrependReactor("update", "configmaps", func(k8stesting.Action) (bool, runtime.Object, error) {
		if failStatus {
			return true, nil, errors.New("status write lost")
		}
		return false, nil, nil
	})
	_, err := h.reconcile(t)
	assert.ErrorContains(t, err, "status write lost")
	assert.Assert(t, h.secret(t) != nil)

	failStatus = false
	_, err = h.reconcile(t)
	assert.NilError(t, err)
	assert.Equal(t, h.issuer.calls, 1)
	assert.Equal(t, state(h.request(t)), credentials.StateReady)

	// The build appears and finishes before the service sees it.
	_, err = h.tekton.TektonV1().PipelineRuns(outNS).Create(context.Background(), pipelineRun(prUID, now, true), metav1.CreateOptions{})
	assert.NilError(t, err)
	_, err = h.reconcile(t)
	assert.NilError(t, err)
	assert.Equal(t, h.issuer.calls, 1)
	assert.Equal(t, state(h.request(t)), credentials.StateCompleted)
	assert.Assert(t, hasOwnerRef(h.secret(t), prUID))
}

package service

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/credentials"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline"
	tektonclient "github.com/tektoncd/pipeline/pkg/client/clientset/versioned"
	tektoninformers "github.com/tektoncd/pipeline/pkg/client/informers/externalversions"
	tektonv1listers "github.com/tektoncd/pipeline/pkg/client/listers/pipeline/v1"
	tektonv1beta1listers "github.com/tektoncd/pipeline/pkg/client/listers/pipeline/v1beta1"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

const pipelineRunIndex = "pipelinerun"

// Controller watches credential requests and the objects they depend on.
type Controller struct {
	cfg    *Config
	kube   kubernetes.Interface
	tekton tektonclient.Interface
	issuer TokenIssuer
	logger *zap.SugaredLogger
	now    func() time.Time

	requestInformer cache.SharedIndexInformer
	requests        corev1listers.ConfigMapLister
	pods            corev1listers.PodLister
	pipelineRuns    tektonv1listers.PipelineRunLister
	taskRuns        tektonv1listers.TaskRunLister
	customRuns      tektonv1beta1listers.CustomRunLister

	synced []cache.InformerSynced
	start  []func(<-chan struct{})
	queue  workqueue.TypedRateLimitingInterface[string]
	ready  atomic.Bool
}

func NewController(cfg *Config, kube kubernetes.Interface, tekton tektonclient.Interface, issuer TokenIssuer, logger *zap.SugaredLogger) (*Controller, error) {
	c := &Controller{
		cfg:    cfg,
		kube:   kube,
		tekton: tekton,
		issuer: issuer,
		logger: logger,
		now:    time.Now,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.NewTypedItemExponentialFailureRateLimiter[string](5*time.Second, 5*time.Minute),
			workqueue.TypedRateLimitingQueueConfig[string]{Name: "credential-requests"},
		),
	}

	requestFactory := informers.NewSharedInformerFactoryWithOptions(kube, cfg.Resync,
		informers.WithNamespace(cfg.ControlNS),
		informers.WithTweakListOptions(func(o *metav1.ListOptions) { o.LabelSelector = credentials.RequestLabel + "=true" }))
	secretFactory := informers.NewSharedInformerFactoryWithOptions(kube, cfg.Resync,
		informers.WithNamespace(cfg.OutputNamespace),
		informers.WithTweakListOptions(func(o *metav1.ListOptions) { o.LabelSelector = credentials.ManagedByLabel }))
	podFactory := informers.NewSharedInformerFactoryWithOptions(kube, cfg.Resync,
		informers.WithNamespace(cfg.OutputNamespace),
		informers.WithTweakListOptions(func(o *metav1.ListOptions) { o.LabelSelector = pipeline.PipelineRunLabelKey }))
	tektonFactory := tektoninformers.NewSharedInformerFactoryWithOptions(tekton, cfg.Resync,
		tektoninformers.WithNamespace(cfg.OutputNamespace))

	cmInformer := requestFactory.Core().V1().ConfigMaps()
	if err := cmInformer.Informer().AddIndexers(cache.Indexers{pipelineRunIndex: indexRequestByPipelineRun}); err != nil {
		return nil, err
	}
	c.requestInformer = cmInformer.Informer()
	c.requests = cmInformer.Lister()
	secretInformer := secretFactory.Core().V1().Secrets()
	podInformer := podFactory.Core().V1().Pods()
	c.pods = podInformer.Lister()
	prInformer := tektonFactory.Tekton().V1().PipelineRuns()
	c.pipelineRuns = prInformer.Lister()
	trInformer := tektonFactory.Tekton().V1().TaskRuns()
	c.taskRuns = trInformer.Lister()
	crInformer := tektonFactory.Tekton().V1beta1().CustomRuns()
	c.customRuns = crInformer.Lister()

	handlers := []struct {
		informer cache.SharedIndexInformer
		keys     func(obj any) []string
	}{
		{cmInformer.Informer(), c.requestKeys},
		{secretInformer.Informer(), c.secretKeys},
		{prInformer.Informer(), c.pipelineRunKeys},
		{trInformer.Informer(), c.childKeys},
		{crInformer.Informer(), c.childKeys},
		{podInformer.Informer(), c.childKeys},
	}
	for _, h := range handlers {
		enqueue := func(obj any) {
			if tomb, ok := obj.(cache.DeletedFinalStateUnknown); ok {
				obj = tomb.Obj
			}
			for _, k := range h.keys(obj) {
				c.queue.Add(k)
			}
		}
		if _, err := h.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc:    enqueue,
			UpdateFunc: func(_, obj any) { enqueue(obj) },
			DeleteFunc: enqueue,
		}); err != nil {
			return nil, err
		}
		c.synced = append(c.synced, h.informer.HasSynced)
	}
	c.start = []func(<-chan struct{}){requestFactory.Start, secretFactory.Start, podFactory.Start, tektonFactory.Start}
	return c, nil
}

func indexRequestByPipelineRun(obj any) ([]string, error) {
	cm, ok := obj.(*corev1.ConfigMap)
	if !ok {
		return nil, nil
	}
	ns, name := cm.Data[credentials.DataTargetNamespace], cm.Data[credentials.DataPipelineRunName]
	if ns == "" || name == "" {
		return nil, nil
	}
	return []string{ns + "/" + name}, nil
}

func (c *Controller) requestKeys(obj any) []string {
	key, err := cache.MetaNamespaceKeyFunc(obj)
	if err != nil {
		return nil
	}
	return []string{key}
}

func (c *Controller) secretKeys(obj any) []string {
	m, err := meta.Accessor(obj)
	if err != nil {
		return nil
	}
	if key := m.GetAnnotations()[credentials.AnnotationRequest]; key != "" {
		return []string{key}
	}
	return nil
}

func (c *Controller) pipelineRunKeys(obj any) []string {
	m, err := meta.Accessor(obj)
	if err != nil {
		return nil
	}
	return c.keysForPipelineRun(m.GetNamespace(), m.GetName())
}

func (c *Controller) childKeys(obj any) []string {
	m, err := meta.Accessor(obj)
	if err != nil {
		return nil
	}
	name := m.GetLabels()[pipeline.PipelineRunLabelKey]
	if name == "" {
		return nil
	}
	return c.keysForPipelineRun(m.GetNamespace(), name)
}

func (c *Controller) keysForPipelineRun(ns, name string) []string {
	objs, err := c.requestInformer.GetIndexer().ByIndex(pipelineRunIndex, ns+"/"+name)
	if err != nil {
		return nil
	}
	var keys []string
	for _, o := range objs {
		keys = append(keys, c.requestKeys(o)...)
	}
	return keys
}

// Start starts informers and waits for all caches to sync.
func (c *Controller) Start(ctx context.Context) error {
	for _, s := range c.start {
		s(ctx.Done())
	}
	if !cache.WaitForCacheSync(ctx.Done(), c.synced...) {
		return errors.New("timed out waiting for caches to sync")
	}
	c.ready.Store(true)
	return nil
}

// Run starts informers, waits for caches and processes requests until ctx is done.
func (c *Controller) Run(ctx context.Context, workers int) error {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()
	if err := c.Start(ctx); err != nil {
		return err
	}
	c.logger.Infof("caches synced, managing requests in %s for namespace %s", c.cfg.ControlNS, c.cfg.OutputNamespace)
	for range workers {
		go func() {
			for c.processNext(ctx) {
			}
		}()
	}
	<-ctx.Done()
	return nil
}

func (c *Controller) processNext(ctx context.Context) bool {
	key, quit := c.queue.Get()
	if quit {
		return false
	}
	defer c.queue.Done(key)
	after, err := c.reconcile(ctx, key)
	if err != nil {
		c.logger.Warnw("reconcile failed, retrying with backoff", "request", key, "error", err)
		c.queue.AddRateLimited(key)
		return true
	}
	c.queue.Forget(key)
	if after > 0 {
		c.queue.AddAfter(key, after)
	}
	return true
}

// HealthHandler serves /healthz and /readyz (ready once caches have synced).
func (c *Controller) HealthHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !c.ready.Load() {
			http.Error(w, "caches not synced", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}

package reconciler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	pipelinerunreconciler "github.com/tektoncd/pipeline/pkg/client/injection/reconciler/pipeline/v1/pipelinerun"
	tektonv1lister "github.com/tektoncd/pipeline/pkg/client/listers/pipeline/v1"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/logging"
	pkgreconciler "knative.dev/pkg/reconciler"
	"knative.dev/pkg/system"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/action"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/customparams"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	pacapi "github.com/openshift-pipelines/pipelines-as-code/pkg/generated/listers/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/metrics"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	pac "github.com/openshift-pipelines/pipelines-as-code/pkg/pipelineascode"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/sync"
)

// Reconciler implements controller.Reconciler for PipelineRun resources.
type Reconciler struct {
	run               *params.Run
	repoLister        pacapi.RepositoryLister
	pipelineRunLister tektonv1lister.PipelineRunLister
	kinteract         kubeinteraction.Interface
	qm                sync.QueueManagerInterface
	metrics           *metrics.Recorder
	eventEmitter      *events.EventEmitter
	globalRepo        *v1alpha1.Repository
	secretNS          string
}

var (
	_ pipelinerunreconciler.Interface = (*Reconciler)(nil)
	_ pipelinerunreconciler.Finalizer = (*Reconciler)(nil)
)

// ReconcileKind is the main entry point for reconciling PipelineRun resources.
func (r *Reconciler) ReconcileKind(ctx context.Context, pr *tektonv1.PipelineRun) pkgreconciler.Event {
	ctx = info.StoreNS(ctx, system.Namespace())
	logger := logging.FromContext(ctx).With("namespace", pr.GetNamespace())

	// Get state from SQLite with fallback to annotations
	state, exist := r.getPipelineRunState(ctx, logger, pr)

	// if pipelineRun is in completed or failed state then return
	if exist && (state == kubeinteraction.StateCompleted || state == kubeinteraction.StateFailed) {
		return nil
	}

	reason := ""
	if len(pr.Status.GetConditions()) > 0 {
		reason = pr.Status.GetConditions()[0].GetReason()
	}
	// This condition handles cases where the PipelineRun has entered a "Running" state,
	// but its status in the Git provider remains "queued" (e.g., due to updates made by
	// another controller outside PaC). To maintain consistency between the PipelineRun
	// status and the Git provider status, we update both the PipelineRun resource and
	// the corresponding status on the Git provider here.
	if reason == string(tektonv1.PipelineRunReasonRunning) && state == kubeinteraction.StateQueued {
		repoName := pr.GetAnnotations()[keys.Repository]
		repo, err := r.repoLister.Repositories(pr.Namespace).Get(repoName)
		if err != nil {
			return fmt.Errorf("failed to get repository CR: %w", err)
		}
		return r.updatePipelineRunToInProgress(ctx, logger, repo, pr)
	}

	// if its a GitHub App pipelineRun PR then process only if check run id is added otherwise wait
	if _, ok := pr.Annotations[keys.InstallationID]; ok {
		if _, ok := pr.Annotations[keys.CheckRunID]; !ok {
			return nil
		}
	}

	// queue pipelines which are in queued state and pending status
	// if status is not pending, it could be canceled so let it be reported, even if state is queued
	if state == kubeinteraction.StateQueued && pr.Spec.Status == tektonv1.PipelineRunSpecStatusPending {
		return r.queuePipelineRun(ctx, logger, pr)
	}

	if !pr.IsDone() && !pr.IsCancelled() {
		return nil
	}

	// make sure we have the latest pipelinerun to reconcile, since there is something updating at the same time
	lpr, err := r.run.Clients.Tekton.TektonV1().PipelineRuns(pr.GetNamespace()).Get(ctx, pr.GetName(), metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("cannot get pipelineRun: %w", err)
	}

	if lpr.GetResourceVersion() != pr.GetResourceVersion() {
		return nil
	}

	// If we have a controllerInfo annotation, then we need to get the
	// configmap configuration for it
	//
	// The annotation is a json string with a label, the pac controller
	// configmap and the GitHub app secret .
	//
	// We always assume the controller is in the same namespace as the original
	// controller but that may changes
	if controllerInfo, ok := pr.GetAnnotations()[keys.ControllerInfo]; ok {
		var parsedControllerInfo *info.ControllerInfo
		if err := json.Unmarshal([]byte(controllerInfo), &parsedControllerInfo); err != nil {
			return fmt.Errorf("failed to parse controllerInfo: %w", err)
		}
		r.run.Info.Controller = parsedControllerInfo
	} else {
		r.run.Info.Controller = info.GetControllerInfoFromEnvOrDefault()
	}

	ctx = info.StoreCurrentControllerName(ctx, r.run.Info.Controller.Name)

	logger = logger.With(
		"pipeline-run", pr.GetName(),
		"event-sha", pr.GetAnnotations()[keys.SHA],
	)
	logger.Infof("pipelineRun %v/%v is done, reconciling to report status!  ", pr.GetNamespace(), pr.GetName())
	r.eventEmitter.SetLogger(logger)

	// use same pac opts across the reconciliation
	pacInfo := r.run.Info.GetPacOpts()

	detectedProvider, event, err := r.detectProvider(ctx, logger, pr)
	if err != nil {
		msg := fmt.Sprintf("detectProvider: %v", err)
		r.eventEmitter.EmitMessage(nil, zap.ErrorLevel, "RepositoryDetectProvider", msg)
		return nil
	}
	detectedProvider.SetPacInfo(&pacInfo)

	if repo, err := r.reportFinalStatus(ctx, logger, &pacInfo, event, pr, detectedProvider); err != nil {
		msg := fmt.Sprintf("report status: %v", err)
		r.eventEmitter.EmitMessage(repo, zap.ErrorLevel, "RepositoryReportFinalStatus", msg)
		return err
	}
	return nil
}

func (r *Reconciler) reportFinalStatus(ctx context.Context, logger *zap.SugaredLogger, pacInfo *info.PacOpts, event *info.Event, pr *tektonv1.PipelineRun, provider provider.Interface) (*v1alpha1.Repository, error) {
	repoName := pr.GetAnnotations()[keys.Repository]
	repo, err := r.repoLister.Repositories(pr.Namespace).Get(repoName)
	if err != nil {
		return nil, fmt.Errorf("reportFinalStatus: %w", err)
	}

	r.secretNS = repo.GetNamespace()
	if r.globalRepo, err = r.repoLister.Repositories(r.run.Info.Kube.Namespace).Get(r.run.Info.Controller.GlobalRepository); err == nil && r.globalRepo != nil {
		if repo.Spec.GitProvider != nil && repo.Spec.GitProvider.Secret == nil && r.globalRepo.Spec.GitProvider != nil && r.globalRepo.Spec.GitProvider.Secret != nil {
			r.secretNS = r.globalRepo.GetNamespace()
		}
		repo.Spec.Merge(r.globalRepo.Spec)
	}

	cp := customparams.NewCustomParams(event, repo, r.run, r.kinteract, r.eventEmitter, nil)
	maptemplate, _, err := cp.GetParams(ctx)
	if err != nil {
		r.eventEmitter.EmitMessage(repo, zap.ErrorLevel, "ParamsError",
			fmt.Sprintf("error processing repository CR custom params: %s", err.Error()))
	}
	r.run.Clients.ConsoleUI().SetParams(maptemplate)

	if event.InstallationID > 0 {
		event.Provider.WebhookSecret, _ = pac.GetCurrentNSWebhookSecret(ctx, r.kinteract, r.run)
	} else {
		secretFromRepo := pac.SecretFromRepository{
			K8int:       r.kinteract,
			Config:      provider.GetConfig(),
			Event:       event,
			Repo:        repo,
			WebhookType: pacInfo.WebhookType,
			Logger:      logger,
			Namespace:   r.secretNS,
		}
		if err := secretFromRepo.Get(ctx); err != nil {
			return repo, fmt.Errorf("cannot get secret from repository: %w", err)
		}
	}

	if r.run.Clients.Log == nil {
		r.run.Clients.Log = logger
	}
	err = provider.SetClient(ctx, r.run, event, repo, r.eventEmitter)
	if err != nil {
		return repo, fmt.Errorf("cannot set client: %w", err)
	}

	finalState := kubeinteraction.StateCompleted
	newPr, err := r.postFinalStatus(ctx, logger, pacInfo, provider, event, pr)
	if err != nil {
		logger.Errorf("failed to post final status, moving on: %v", err)
		finalState = kubeinteraction.StateFailed
	}

	if err := r.updateRepoRunStatus(ctx, logger, newPr, repo, event); err != nil {
		return repo, fmt.Errorf("cannot update run status: %w", err)
	}

	if _, err := r.updatePipelineRunState(ctx, logger, pr, finalState); err != nil {
		return repo, fmt.Errorf("cannot update state: %w", err)
	}

	if err := r.emitMetrics(pr); err != nil {
		logger.Error("failed to emit metrics: ", err)
	}

	// remove pipelineRun from Queue and start the next one
	// Get the next PipelineRun from the queue
	nextPrKey := r.qm.RemoveAndTakeItemFromQueue(repo, pr)
	if nextPrKey != "" {
		key := strings.Split(nextPrKey, "/")
		if len(key) == 2 {
			nextPr, err := r.run.Clients.Tekton.TektonV1().PipelineRuns(key[0]).Get(ctx, key[1], metav1.GetOptions{})
			if err != nil {
				logger.Errorf("cannot get pipeline for next in queue: %w", err)
			} else {
				if err := r.updatePipelineRunToInProgress(ctx, logger, repo, nextPr); err != nil {
					logger.Errorf("failed to update status: %w", err)
					_ = r.qm.RemoveFromQueue(sync.RepoKey(repo), nextPrKey)
				}
			}
		}
	}

	if err := r.cleanupPipelineRuns(ctx, logger, pacInfo, repo, pr); err != nil {
		return repo, fmt.Errorf("error cleaning pipelineruns: %w", err)
	}

	return repo, nil
}

func (r *Reconciler) updatePipelineRunToInProgress(ctx context.Context, logger *zap.SugaredLogger, repo *v1alpha1.Repository, pr *tektonv1.PipelineRun) error {
	pr, err := r.updatePipelineRunState(ctx, logger, pr, kubeinteraction.StateStarted)
	if err != nil {
		return fmt.Errorf("cannot update state: %w", err)
	}
	pacInfo := r.run.Info.GetPacOpts()
	detectedProvider, event, err := r.detectProvider(ctx, logger, pr)
	if err != nil {
		logger.Error(err)
		return nil
	}
	detectedProvider.SetPacInfo(&pacInfo)

	if event.InstallationID > 0 {
		event.Provider.WebhookSecret, _ = pac.GetCurrentNSWebhookSecret(ctx, r.kinteract, r.run)
	} else {
		// secretNS is needed when git provider is other than Github.
		secretNS := repo.GetNamespace()
		if repo.Spec.GitProvider != nil && repo.Spec.GitProvider.Secret == nil && r.globalRepo != nil && r.globalRepo.Spec.GitProvider != nil && r.globalRepo.Spec.GitProvider.Secret != nil {
			secretNS = r.globalRepo.GetNamespace()
		}

		secretFromRepo := pac.SecretFromRepository{
			K8int:       r.kinteract,
			Config:      detectedProvider.GetConfig(),
			Event:       event,
			Repo:        repo,
			WebhookType: pacInfo.WebhookType,
			Logger:      logger,
			Namespace:   secretNS,
		}
		if err := secretFromRepo.Get(ctx); err != nil {
			return fmt.Errorf("cannot get secret from repository: %w", err)
		}
	}

	err = detectedProvider.SetClient(ctx, r.run, event, repo, r.eventEmitter)
	if err != nil {
		return fmt.Errorf("cannot set client: %w", err)
	}

	consoleURL := r.run.Clients.ConsoleUI().DetailURL(pr)

	mt := formatting.MessageTemplate{
		PipelineRunName: pr.GetName(),
		Namespace:       repo.GetNamespace(),
		ConsoleName:     r.run.Clients.ConsoleUI().GetName(),
		ConsoleURL:      consoleURL,
		TknBinary:       settings.TknBinaryName,
		TknBinaryURL:    settings.TknBinaryURL,
	}
	msg, err := mt.MakeTemplate(detectedProvider.GetTemplate(provider.StartingPipelineType))
	if err != nil {
		return fmt.Errorf("cannot create message template: %w", err)
	}
	status := provider.StatusOpts{
		Status:                  "in_progress",
		Conclusion:              "pending",
		Text:                    msg,
		DetailsURL:              consoleURL,
		PipelineRunName:         pr.GetName(),
		PipelineRun:             pr,
		OriginalPipelineRunName: pr.GetAnnotations()[keys.OriginalPRName],
	}

	if err := createStatusWithRetry(ctx, logger, detectedProvider, event, status); err != nil {
		// if failed to report status for running state, let the pipelineRun continue,
		// pipelineRun is already started so we will try again once it completes
		logger.Errorf("failed to report status to running on provider continuing! error: %v", err)
		return nil
	}

	logger.Info("updated in_progress status on provider platform for pipelineRun ", pr.GetName())
	return nil
}

func (r *Reconciler) updatePipelineRunState(ctx context.Context, logger *zap.SugaredLogger, pr *tektonv1.PipelineRun, state string) (*tektonv1.PipelineRun, error) {
	// Only update spec status if state is started (remove pending status)
	var mergePatch map[string]any
	if state == kubeinteraction.StateStarted {
		mergePatch = map[string]any{
			"spec": map[string]any{
				"status": "",
			},
		}
		actionLog := state + " state"
		patchedPR, err := action.PatchPipelineRun(ctx, logger, actionLog, r.run.Clients.Tekton, pr, mergePatch)
		if err != nil {
			return pr, fmt.Errorf("error patching the pipelinerun: %w", err)
		}
		pr = patchedPR
	}

	// Sync state to SQLite (primary state storage)
	if repoName, exists := pr.GetAnnotations()[keys.Repository]; exists && repoName != "" {
		prKey := sync.PrKey(pr)
		// Use the annotation value directly as it now contains the full repo key
		repoKey := repoName
		logger.Debugf("getPipelineRunState: looking up state for repoKey=%s, prKey=%s", repoKey, prKey)
		if err := r.qm.SyncPipelineRunState(repoKey, prKey, state); err != nil {
			logger.Warnf("failed to sync PipelineRun state to SQLite: %v", err)
		} else {
			logger.Infof("synced PipelineRun %s state '%s' to SQLite", pr.GetName(), state)
		}
	}

	return pr, nil
}

// queuePipelineRun handles PipelineRuns that are in queued state and pending status
// using the SQLite-based queue manager.
func (r *Reconciler) queuePipelineRun(ctx context.Context, logger *zap.SugaredLogger, pr *tektonv1.PipelineRun) error {
	// check if repository annotation exists
	repoName, exist := pr.GetAnnotations()[keys.Repository]
	if !exist {
		return fmt.Errorf("no %s annotation found", keys.Repository)
	}
	if repoName == "" {
		return fmt.Errorf("annotation %s is empty", keys.Repository)
	}

	repo, err := r.repoLister.Repositories(pr.Namespace).Get(repoName)
	if err != nil {
		// if repository is not found, then skip processing the pipelineRun and return nil
		if errors.IsNotFound(err) {
			r.qm.RemoveRepository(&v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      repoName,
					Namespace: pr.Namespace,
				},
			})
			return nil
		}
		return fmt.Errorf("error getting repository: %w", err)
	}

	// merge local repo with global repo here in order to derive settings from global
	// for further concurrency and other operations.
	if r.globalRepo, err = r.repoLister.Repositories(r.run.Info.Kube.Namespace).Get(r.run.Info.Controller.GlobalRepository); err == nil && r.globalRepo != nil {
		logger.Info("Merging global repository settings with local repository settings")
		repo.Spec.Merge(r.globalRepo.Spec)
	}

	// if concurrency was set and later removed or changed to zero
	// then remove pipelineRun from Queue and update pending state to running
	if repo.Spec.ConcurrencyLimit != nil && *repo.Spec.ConcurrencyLimit == 0 {
		_ = r.qm.RemoveAndTakeItemFromQueue(repo, pr)
		if err := r.updatePipelineRunToInProgress(ctx, logger, repo, pr); err != nil {
			return fmt.Errorf("failed to update PipelineRun to in_progress: %w", err)
		}
		return nil
	}

	// Add the PipelineRun to the queue
	prKey := sync.PrKey(pr)
	err = r.qm.AddToPendingQueue(repo, []string{prKey})
	if err != nil {
		return fmt.Errorf("failed to add PipelineRun to queue: %w", err)
	}

	// Sync initial state to SQLite
	repoKey := sync.RepoKey(repo)
	if err := r.qm.SyncPipelineRunState(repoKey, prKey, kubeinteraction.StateQueued); err != nil {
		logger.Warnf("failed to sync initial state to SQLite for PipelineRun %s: %v", pr.GetName(), err)
	} else {
		logger.Infof("synced initial state 'queued' to SQLite for PipelineRun %s", pr.GetName())
	}

	// Try to start the next PipelineRun from the queue
	var processed bool
	var iterated int
	maxIterations := 5

	for {
		acquired, err := r.qm.AddListToRunningQueue(repo, []string{prKey})
		if err != nil {
			return fmt.Errorf("failed to add to running queue: %s: %w", pr.GetName(), err)
		}
		if len(acquired) == 0 {
			logger.Infof("no new PipelineRun acquired for repo %s", repo.GetName())
			break
		}

		for _, acquiredPrKey := range acquired {
			nsName := strings.Split(acquiredPrKey, "/")
			if len(nsName) != 2 {
				logger.Errorf("invalid PipelineRun key format: %s", acquiredPrKey)
				continue
			}

			acquiredPr, err := r.run.Clients.Tekton.TektonV1().PipelineRuns(nsName[0]).Get(ctx, nsName[1], metav1.GetOptions{})
			if err != nil {
				logger.Errorf("failed to get PipelineRun %s/%s: %v", nsName[0], nsName[1], err)
				_ = r.qm.RemoveFromQueue(sync.RepoKey(repo), acquiredPrKey)
				continue
			}

			if err := r.updatePipelineRunToInProgress(ctx, logger, repo, acquiredPr); err != nil {
				logger.Errorf("failed to update PipelineRun to in_progress: %w", err)
				_ = r.qm.RemoveFromQueue(sync.RepoKey(repo), acquiredPrKey)
			} else {
				processed = true
			}
		}

		if processed {
			break
		}

		if iterated >= maxIterations {
			return fmt.Errorf("max iterations reached of %d times trying to get a pipelinerun started for %s", maxIterations, repo.GetName())
		}
		iterated++
	}

	return nil
}

// getPipelineRunState gets the state from SQLite only
func (r *Reconciler) getPipelineRunState(ctx context.Context, logger *zap.SugaredLogger, pr *tektonv1.PipelineRun) (string, bool) {
	// Get state from SQLite only
	if repoName, exists := pr.GetAnnotations()[keys.Repository]; exists && repoName != "" {
		prKey := sync.PrKey(pr)
		// Use the annotation value directly as it now contains the full repo key
		repoKey := repoName
		fmt.Printf("[DEBUG] getPipelineRunState: looking up state for repoKey=%s, prKey=%s\n", repoKey, prKey)
		if state, err := r.qm.GetPipelineRunState(repoKey, prKey); err == nil && state != "" {
			logger.Debugf("got state '%s' from SQLite for PipelineRun %s", state, pr.GetName())
			return state, true
		}
		logger.Debugf("getPipelineRunState: no state found for repoKey=%s, prKey=%s", repoKey, prKey)
	}

	// No state found in SQLite
	return "", false
}

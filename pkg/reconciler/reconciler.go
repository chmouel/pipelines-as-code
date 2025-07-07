package reconciler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	pipelinerunreconciler "github.com/tektoncd/pipeline/pkg/client/injection/reconciler/pipeline/v1/pipelinerun"
	tektonv1lister "github.com/tektoncd/pipeline/pkg/client/listers/pipeline/v1"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	knative "knative.dev/pkg/logging"
	pkgreconciler "knative.dev/pkg/reconciler"
	"knative.dev/pkg/system"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/action"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/concurrency"
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
	"knative.dev/pkg/logging"
)

// Reconciler implements controller.Reconciler for PipelineRun resources.
type Reconciler struct {
	run               *params.Run
	repoLister        pacapi.RepositoryLister
	pipelineRunLister tektonv1lister.PipelineRunLister
	kinteract         kubeinteraction.Interface

	metrics            *metrics.Recorder
	eventEmitter       *events.EventEmitter
	globalRepo         *v1alpha1.Repository
	secretNS           string
	concurrencyManager *concurrency.Manager // new abstracted concurrency manager

	// Lease tracking for proper slot management
	activeLeases map[string]concurrency.LeaseID // prKey -> leaseID
	leaseMutex   sync.RWMutex
}

var (
	_ pipelinerunreconciler.Interface = (*Reconciler)(nil)
	_ pipelinerunreconciler.Finalizer = (*Reconciler)(nil)
)

// ReconcileKind is the main entry point for reconciling PipelineRun resources.
func (r *Reconciler) ReconcileKind(ctx context.Context, pr *tektonv1.PipelineRun) pkgreconciler.Event {
	ctx = info.StoreNS(ctx, system.Namespace())
	logger := knative.FromContext(ctx).With("namespace", pr.GetNamespace())
	// if pipelineRun is in completed or failed state then return
	state, exist := pr.GetAnnotations()[keys.State]
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

	// Release concurrency slot and process next queued PipelineRun
	if r.concurrencyManager != nil {
		prKey := fmt.Sprintf("%s/%s", pr.Namespace, pr.Name)
		repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)

		// Get the lease ID for proper release
		r.leaseMutex.RLock()
		leaseID, hasLease := r.activeLeases[prKey]
		r.leaseMutex.RUnlock()

		// Release the slot for the completed PipelineRun
		if err := r.concurrencyManager.ReleaseSlot(ctx, leaseID, prKey, repoKey); err != nil {
			logger.Errorf("failed to release concurrency slot for %s: %v", prKey, err)
		}

		// Remove from lease tracking
		if hasLease {
			r.leaseMutex.Lock()
			delete(r.activeLeases, prKey)
			r.leaseMutex.Unlock()
		}

		// Note: We don't manually process queued PipelineRuns here anymore
		// The watcher will automatically trigger processQueuedPipelineRuns when the slot is released
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
	mergePatch := map[string]any{
		"metadata": map[string]any{
			"labels": map[string]string{
				keys.State: state,
			},
			"annotations": map[string]string{
				keys.State: state,
			},
		},
	}
	// if state is started then remove pipelineRun pending status
	if state == kubeinteraction.StateStarted {
		mergePatch["spec"] = map[string]any{
			"status": "",
		}
	}
	actionLog := state + " state"
	patchedPR, err := action.PatchPipelineRun(ctx, logger, actionLog, r.run.Clients.Tekton, pr, mergePatch)
	if err != nil {
		return pr, fmt.Errorf("error patching the pipelinerun: %w", err)
	}
	return patchedPR, nil
}

// queuePipelineRun handles a PipelineRun in the queued state using the new concurrency system.
func (r *Reconciler) queuePipelineRun(ctx context.Context, logger *zap.SugaredLogger, pr *tektonv1.PipelineRun) error {
	repoName := pr.GetAnnotations()[keys.Repository]
	repo, err := r.repoLister.Repositories(pr.Namespace).Get(repoName)
	if err != nil {
		return fmt.Errorf("failed to get repository CR: %w", err)
	}

	prKey := concurrency.PrKey(pr)

	// Add to pending queue (idempotent)
	if err := r.concurrencyManager.GetQueueManager().AddToPendingQueue(repo, []string{prKey}); err != nil {
		logger.Errorf("failed to add PipelineRun to pending queue: %v", err)
		return err
	}

	// Try to acquire a slot
	success, leaseID, err := r.concurrencyManager.AcquireSlot(ctx, repo, prKey)
	if err != nil {
		logger.Errorf("failed to acquire concurrency slot: %v", err)
		return err
	}

	if success {
		// Store the lease ID for proper tracking
		r.leaseMutex.Lock()
		if r.activeLeases == nil {
			r.activeLeases = make(map[string]concurrency.LeaseID)
		}
		r.activeLeases[prKey] = leaseID
		r.leaseMutex.Unlock()

		// Promote to running: update state and status
		if err := r.updatePipelineRunToInProgress(ctx, logger, repo, pr); err != nil {
			logger.Errorf("failed to update PipelineRun to in-progress: %v", err)
			// Release the slot if we can't update the PipelineRun
			repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)
			if releaseErr := r.concurrencyManager.ReleaseSlot(ctx, leaseID, prKey, repoKey); releaseErr != nil {
				logger.Errorf("failed to release slot after update failure: %v", releaseErr)
			}
			// Remove from lease tracking
			r.leaseMutex.Lock()
			delete(r.activeLeases, prKey)
			r.leaseMutex.Unlock()
			return err
		}
		logger.Infof("PipelineRun %s promoted to running", prKey)
	} else {
		// Remain queued: ensure state and status are correct
		if _, err := r.updatePipelineRunState(ctx, logger, pr, kubeinteraction.StateQueued); err != nil {
			logger.Errorf("failed to update PipelineRun state to queued: %v", err)
			return err
		}
		logger.Infof("PipelineRun %s remains queued", prKey)
	}
	return nil
}

// processQueuedPipelineRuns processes queued PipelineRuns when slots become available.
// This is called by the watcher when a slot is released.
func (r *Reconciler) processQueuedPipelineRuns(ctx context.Context, repo *v1alpha1.Repository) {
	logger := logging.FromContext(ctx).With("namespace", repo.GetNamespace(), "repository", repo.GetName())

	// Get queued PipelineRuns
	queuedPRs := r.concurrencyManager.GetQueueManager().QueuedPipelineRuns(repo)
	if len(queuedPRs) == 0 {
		return
	}

	logger.Infof("processing %d queued PipelineRuns after slot became available", len(queuedPRs))

	// Try to start the next queued PipelineRun
	for _, nextPRKey := range queuedPRs {
		parts := strings.Split(nextPRKey, "/")
		if len(parts) != 2 {
			logger.Errorf("invalid PipelineRun key format: %s", nextPRKey)
			continue
		}

		nextPR, err := r.run.Clients.Tekton.TektonV1().PipelineRuns(parts[0]).Get(ctx, parts[1], metav1.GetOptions{})
		if err != nil {
			logger.Errorf("cannot get PipelineRun %s: %v", nextPRKey, err)
			// Remove from queue if PipelineRun doesn't exist
			r.concurrencyManager.GetQueueManager().RemoveFromQueue(fmt.Sprintf("%s/%s", repo.Namespace, repo.Name), nextPRKey)
			continue
		}

		// Try to acquire a slot for this PipelineRun
		success, leaseID, err := r.concurrencyManager.AcquireSlot(ctx, repo, nextPRKey)
		if err != nil {
			logger.Errorf("failed to acquire slot for %s: %v", nextPRKey, err)
			continue
		}

		if success {
			// Store the lease ID for proper tracking
			r.leaseMutex.Lock()
			if r.activeLeases == nil {
				r.activeLeases = make(map[string]concurrency.LeaseID)
			}
			r.activeLeases[nextPRKey] = leaseID
			r.leaseMutex.Unlock()

			if err := r.updatePipelineRunToInProgress(ctx, logger, repo, nextPR); err != nil {
				logger.Errorf("failed to update PipelineRun %s to in-progress: %v", nextPRKey, err)
				// Release the slot if we can't update the PipelineRun
				repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)
				if releaseErr := r.concurrencyManager.ReleaseSlot(ctx, leaseID, nextPRKey, repoKey); releaseErr != nil {
					logger.Errorf("failed to release slot after update failure: %v", releaseErr)
				}
				// Remove from lease tracking
				r.leaseMutex.Lock()
				delete(r.activeLeases, nextPRKey)
				r.leaseMutex.Unlock()
				continue
			}
			logger.Infof("started queued PipelineRun: %s", nextPRKey)
			break // Only start one PipelineRun per slot availability notification
		}
	}
}

package events

import (
	"context"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func NewEventEmitter(client kubernetes.Interface, logger *zap.SugaredLogger) *EventEmitter {
	return &EventEmitter{
		client: client,
		logger: logger,
	}
}

type EventEmitter struct {
	client              kubernetes.Interface
	logger              *zap.SugaredLogger
	controllerNamespace string
}

func (e *EventEmitter) SetLogger(logger *zap.SugaredLogger) {
	e.logger = logger
}

// SetControllerNamespace sets the controller namespace for emitting events
// when no Repository CR is available.
func (e *EventEmitter) SetControllerNamespace(ns string) {
	e.controllerNamespace = ns
}

// EmitControllerEvent emits a Kubernetes event in the controller namespace
// when no Repository CR is available (e.g., before repo matching or on parse errors).
// This allows visibility into webhook processing errors even without a matched repository.
func (e *EventEmitter) EmitControllerEvent(reason, message, sourceURL, sha string) {
	// Always log the message
	if e.logger != nil {
		e.logger.Warnf("%s: %s", reason, message)
	}

	// Create K8s event in controller namespace if possible
	if e.client == nil || e.controllerNamespace == "" {
		return
	}

	annotations := map[string]string{
		keys.ControllerInfo: "true",
	}
	if sourceURL != "" {
		annotations[keys.SourceRepoURL] = sourceURL
	}
	if sha != "" {
		annotations[keys.SHA] = sha
	}

	event := &v1.Event{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "pac-webhook-",
			Namespace:    e.controllerNamespace,
			Labels: map[string]string{
				"pipelinesascode.tekton.dev/event-type": "webhook-error",
			},
			Annotations: annotations,
		},
		Message: message,
		Reason:  reason,
		Type:    v1.EventTypeWarning,
		// InvolvedObject references a generic resource in the controller namespace
		// since we don't have a specific Repository CR to reference
		InvolvedObject: v1.ObjectReference{
			APIVersion: "v1",
			Kind:       "Namespace",
			Name:       e.controllerNamespace,
		},
		Source: v1.EventSource{
			Component: "Pipelines As Code",
		},
	}

	if _, err := e.client.CoreV1().Events(e.controllerNamespace).Create(context.Background(), event, metav1.CreateOptions{}); err != nil {
		if e.logger != nil {
			e.logger.Infof("Cannot create controller event: %s", err.Error())
		}
	}
}

func (e *EventEmitter) EmitMessage(repo *v1alpha1.Repository, loggerLevel zapcore.Level, reason, message string) {
	if repo != nil && e.client != nil {
		event := makeEvent(repo, loggerLevel, reason, message)
		if _, err := e.client.CoreV1().Events(event.Namespace).Create(context.Background(), event, metav1.CreateOptions{}); err != nil {
			if e.logger != nil {
				e.logger.Infof("Cannot create event: %s", err.Error())
			}
		}
	}

	if e.logger != nil {
		//nolint
		switch loggerLevel {
		case zapcore.DebugLevel:
			e.logger.Debug(message)
		case zapcore.ErrorLevel:
			e.logger.Error(message)
		case zapcore.InfoLevel:
			e.logger.Info(message)
		case zapcore.WarnLevel:
			e.logger.Warn(message)
		}
	}
}

func makeEvent(repo *v1alpha1.Repository, loggerLevel zapcore.Level, reason, message string) *v1.Event {
	event := &v1.Event{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: repo.Name + "-",
			Namespace:    repo.Namespace,
			Labels: map[string]string{
				keys.Repository: formatting.CleanValueKubernetes(repo.Name),
			},
			Annotations: map[string]string{
				keys.Repository: repo.Name,
			},
		},
		Message: message,
		Reason:  reason,
		Type:    v1.EventTypeWarning,
		InvolvedObject: v1.ObjectReference{
			APIVersion:      pipelinesascode.V1alpha1Version,
			Kind:            pipelinesascode.RepositoryKind,
			Namespace:       repo.Namespace,
			Name:            repo.Name,
			UID:             repo.UID,
			ResourceVersion: repo.ResourceVersion,
		},
		Source: v1.EventSource{
			Component: "Pipelines As Code",
		},
	}
	if loggerLevel == zap.InfoLevel {
		event.Type = v1.EventTypeNormal
	}
	return event
}

// Package credentials holds the request contract shared by Pipelines-as-Code
// and the standalone GitHub build credential service (proof of concept).
package credentials

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
)

const (
	// RequestLabel selects credential request ConfigMaps in the control namespace.
	RequestLabel = pipelinesascode.GroupName + "/github-credential-request"

	// ManagedByLabel marks Secrets written by the credential service.
	ManagedByLabel = pipelinesascode.GroupName + "/credential-managed-by"
	ManagedByValue = "pipelines-as-code-credentials"

	DataRepositoryID    = "repository-id"
	DataTargetNamespace = "target-namespace"
	DataSecretName      = "secret-name"
	DataPipelineRunName = "pipelinerun-name"

	AnnotationState        = pipelinesascode.GroupName + "/credential-state"
	AnnotationExpiresAt    = pipelinesascode.GroupName + "/credential-expires-at"
	AnnotationRenewedAt    = pipelinesascode.GroupName + "/credential-renewed-at"
	AnnotationMessage      = pipelinesascode.GroupName + "/credential-message"
	AnnotationBoundUID     = pipelinesascode.GroupName + "/credential-bound-pipelinerun-uid"
	AnnotationUnboundSince = pipelinesascode.GroupName + "/credential-unbound-since"

	// AnnotationRequest and AnnotationRequestUID link a managed Secret to the
	// request that owns it.
	AnnotationRequest    = pipelinesascode.GroupName + "/credential-request"
	AnnotationRequestUID = pipelinesascode.GroupName + "/credential-request-uid"

	// AnnotationSecretOwner on a PipelineRun records that its git auth secret
	// belongs to the credential service, so PaC must not write or delete it.
	AnnotationSecretOwner = pipelinesascode.GroupName + "/git-auth-secret-owner"
	SecretOwnerService    = "credentials-service"
)

// State is the lifecycle state published on a request.
type State string

const (
	StatePending   State = "pending"
	StateReady     State = "ready"
	StateBound     State = "bound"
	StateCompleted State = "completed"
	StateError     State = "error"
	StateRejected  State = "rejected"
	StateConflict  State = "conflict"
)

var requestDataKeys = []string{DataRepositoryID, DataTargetNamespace, DataSecretName, DataPipelineRunName}

// Request is a parsed credential request.
type Request struct {
	RepositoryID    int64
	TargetNamespace string
	SecretName      string
	PipelineRunName string
}

// ParseRequest validates the structure of a request ConfigMap. Policy checks
// (permitted repository and namespace) are done by the service.
func ParseRequest(cm *corev1.ConfigMap) (*Request, error) {
	if cm.Labels[RequestLabel] != "true" {
		return nil, fmt.Errorf("missing label %s=true", RequestLabel)
	}
	if cm.Immutable == nil || !*cm.Immutable {
		return nil, fmt.Errorf("request must be immutable")
	}
	if len(cm.BinaryData) > 0 {
		return nil, fmt.Errorf("binaryData is not supported")
	}
	var unknown []string
	for k := range cm.Data {
		if !slices.Contains(requestDataKeys, k) {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		slices.Sort(unknown)
		return nil, fmt.Errorf("unsupported request fields: %s", strings.Join(unknown, ", "))
	}
	for _, k := range requestDataKeys {
		if strings.TrimSpace(cm.Data[k]) == "" {
			return nil, fmt.Errorf("missing request field %s", k)
		}
	}
	repoID, err := strconv.ParseInt(strings.TrimSpace(cm.Data[DataRepositoryID]), 10, 64)
	if err != nil || repoID <= 0 {
		return nil, fmt.Errorf("invalid %s %q", DataRepositoryID, cm.Data[DataRepositoryID])
	}
	r := &Request{
		RepositoryID:    repoID,
		TargetNamespace: cm.Data[DataTargetNamespace],
		SecretName:      cm.Data[DataSecretName],
		PipelineRunName: cm.Data[DataPipelineRunName],
	}
	if errs := validation.IsDNS1123Label(r.TargetNamespace); len(errs) > 0 {
		return nil, fmt.Errorf("invalid %s: %s", DataTargetNamespace, strings.Join(errs, "; "))
	}
	if errs := validation.IsDNS1123Subdomain(r.SecretName); len(errs) > 0 {
		return nil, fmt.Errorf("invalid %s: %s", DataSecretName, strings.Join(errs, "; "))
	}
	if errs := validation.IsDNS1123Subdomain(r.PipelineRunName); len(errs) > 0 {
		return nil, fmt.Errorf("invalid %s: %s", DataPipelineRunName, strings.Join(errs, "; "))
	}
	return r, nil
}

// RequestName derives a stable request name for a build so that retries
// reuse the same request.
func RequestName(targetNamespace, pipelineRunName string) string {
	sum := sha256.Sum256([]byte(targetNamespace + "/" + pipelineRunName))
	prefix := pipelineRunName
	if len(prefix) > 40 {
		prefix = prefix[:40]
	}
	prefix = strings.Trim(prefix, "-.")
	if prefix == "" {
		prefix = "build"
	}
	return prefix + "-ghcred-" + hex.EncodeToString(sum[:])[:12]
}

// NewRequestConfigMap builds the immutable request ConfigMap.
func NewRequestConfigMap(namespace, name string, r Request) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{RequestLabel: "true"},
		},
		Immutable: new(true),
		Data: map[string]string{
			DataRepositoryID:    strconv.FormatInt(r.RepositoryID, 10),
			DataTargetNamespace: r.TargetNamespace,
			DataSecretName:      r.SecretName,
			DataPipelineRunName: r.PipelineRunName,
		},
	}
}

// Status is the lifecycle information published on a request.
type Status struct {
	State        State
	ExpiresAt    time.Time
	RenewedAt    time.Time
	Message      string
	BoundUID     string
	UnboundSince time.Time
}

// StatusFromAnnotations reads the published status of a request.
func StatusFromAnnotations(ann map[string]string) Status {
	return Status{
		State:        State(ann[AnnotationState]),
		ExpiresAt:    ParseTime(ann[AnnotationExpiresAt]),
		RenewedAt:    ParseTime(ann[AnnotationRenewedAt]),
		Message:      ann[AnnotationMessage],
		BoundUID:     ann[AnnotationBoundUID],
		UnboundSince: ParseTime(ann[AnnotationUnboundSince]),
	}
}

// Apply writes the status into annotations, removing empty values.
func (s Status) Apply(ann map[string]string) {
	set := func(k, v string) {
		if v == "" {
			delete(ann, k)
			return
		}
		ann[k] = v
	}
	set(AnnotationState, string(s.State))
	set(AnnotationExpiresAt, FormatTime(s.ExpiresAt))
	set(AnnotationRenewedAt, FormatTime(s.RenewedAt))
	set(AnnotationMessage, s.Message)
	set(AnnotationBoundUID, s.BoundUID)
	set(AnnotationUnboundSince, FormatTime(s.UnboundSince))
}

// ParseTime parses an annotation timestamp, returning zero when invalid.
func ParseTime(v string) time.Time {
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}
	}
	return t
}

// FormatTime formats an annotation timestamp; zero becomes empty.
func FormatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

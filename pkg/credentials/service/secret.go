package service

import (
	"net/url"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/credentials"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/secrets"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func (c *Controller) newSecret(cm *corev1.ConfigMap, req *credentials.Request, tok Token, boundUID string) (*corev1.Secret, error) {
	u, err := url.Parse(c.cfg.RepositoryURL)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	event := info.NewEvent()
	event.URL = c.cfg.RepositoryURL
	event.Organization = parts[0]
	event.Repository = parts[len(parts)-1]
	event.Provider.Token = tok.Value
	s, err := secrets.MakeBasicAuthSecret(event, req.SecretName)
	if err != nil {
		return nil, err
	}
	s.Data = make(map[string][]byte, len(s.StringData))
	for k, v := range s.StringData {
		s.Data[k] = []byte(v)
	}
	s.StringData = nil
	s.Namespace = req.TargetNamespace
	s.Type = corev1.SecretTypeOpaque
	s.Labels[credentials.ManagedByLabel] = credentials.ManagedByValue
	s.Annotations[credentials.AnnotationRequest] = cm.Namespace + "/" + cm.Name
	s.Annotations[credentials.AnnotationRequestUID] = string(cm.UID)
	s.Annotations[credentials.AnnotationExpiresAt] = credentials.FormatTime(tok.ExpiresAt)
	if boundUID != "" {
		s.OwnerReferences = []metav1.OwnerReference{ownerRef(req.PipelineRunName, boundUID)}
	}
	return s, nil
}

// ownedBy reports whether the secret carries this request's markers.
func ownedBy(s *corev1.Secret, cm *corev1.ConfigMap) bool {
	return s.Labels[credentials.ManagedByLabel] == credentials.ManagedByValue &&
		s.Annotations[credentials.AnnotationRequestUID] == string(cm.UID)
}

func ownerRef(prName, uid string) metav1.OwnerReference {
	return metav1.OwnerReference{
		APIVersion:         tektonv1.SchemeGroupVersion.String(),
		Kind:               "PipelineRun",
		Name:               prName,
		UID:                types.UID(uid),
		Controller:         new(false),
		BlockOwnerDeletion: new(false),
	}
}

// withOwnerRef returns the secret's owner references with ours added, keeping
// every other reference. It reports false when nothing changes.
func withOwnerRef(s *corev1.Secret, prName, uid string) ([]metav1.OwnerReference, bool) {
	for _, r := range s.OwnerReferences {
		if string(r.UID) == uid {
			return nil, false
		}
	}
	refs := append([]metav1.OwnerReference{}, s.OwnerReferences...)
	return append(refs, ownerRef(prName, uid)), true
}

func truncate(msg string) string {
	const limit = 512
	if len(msg) > limit {
		return msg[:limit] + "..."
	}
	return msg
}

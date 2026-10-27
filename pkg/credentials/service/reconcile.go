package service

import (
	"context"
	"fmt"
	"maps"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/credentials"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
)

// minRequeue avoids a tight loop when GitHub returns a token whose lifetime
// is shorter than the renewal window.
const minRequeue = 30 * time.Second

// reconcile handles one request. Lifecycle (binding, completion, cleanup)
// is resolved before any Secret maintenance, so a finished or deleted build
// never gets a new token. It returns when the request should be checked again.
func (c *Controller) reconcile(ctx context.Context, key string) (time.Duration, error) {
	ns, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		return 0, nil //nolint:nilerr // a malformed key never succeeds, so it is dropped
	}
	cached, err := c.requests.ConfigMaps(ns).Get(name)
	if apierrors.IsNotFound(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	cm := cached.DeepCopy()
	log := c.logger.With("request", key)
	st := credentials.StatusFromAnnotations(cm.Annotations)
	if st.State == credentials.StateRejected || st.State == credentials.StateConflict {
		return 0, nil
	}

	req, err := credentials.ParseRequest(cm)
	if err == nil {
		err = c.cfg.authorize(req)
	}
	if err != nil {
		log.Warnw("rejecting credential request", "reason", err.Error())
		st.State, st.Message = credentials.StateRejected, truncate(err.Error())
		return 0, c.writeStatus(ctx, cm, st)
	}
	log = log.With("namespace", req.TargetNamespace, "secret", req.SecretName, "pipelinerun", req.PipelineRunName)

	var pr *tektonv1.PipelineRun
	if st.BoundUID == "" {
		pr, err = c.pipelineRuns.PipelineRuns(req.TargetNamespace).Get(req.PipelineRunName)
		switch {
		case apierrors.IsNotFound(err):
			pr = nil
		case err != nil:
			return 0, err
		case pr.CreationTimestamp.Before(&cm.CreationTimestamp):
			log.Warnw("ignoring a PipelineRun created before the request", "uid", pr.UID)
			pr = nil
		default:
			// Persist the binding before touching the Secret so a restart
			// can never attach the credentials to another build.
			st.BoundUID, st.UnboundSince = string(pr.UID), time.Time{}
			if st.State != credentials.StateCompleted {
				st.State = credentials.StateBound
			}
			if err := c.writeStatus(ctx, cm, st); err != nil {
				return 0, err
			}
			log.Infow("request bound to PipelineRun", "uid", pr.UID)
		}
	} else {
		var gone bool
		pr, gone, err = c.boundPipelineRun(ctx, req, st.BoundUID, false)
		if err != nil {
			return 0, err
		}
		if gone {
			return 0, c.cleanup(ctx, cm, req)
		}
	}

	if pr != nil && st.State != credentials.StateCompleted {
		done, err := c.executionFinished(pr)
		if err != nil {
			return 0, err
		}
		if done {
			st.State = credentials.StateCompleted
			log.Infow("build execution finished, renewal stopped")
		}
	}

	sec, err := c.kube.CoreV1().Secrets(req.TargetNamespace).Get(ctx, req.SecretName, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		sec = nil
	case err != nil:
		return 0, err
	case !ownedBy(sec, cm):
		log.Warnw("secret exists and is not owned by this request, refusing to overwrite it")
		st.State, st.Message = credentials.StateConflict, fmt.Sprintf("secret %s/%s exists and is not owned by this request", req.TargetNamespace, req.SecretName)
		return 0, c.writeStatus(ctx, cm, st)
	}

	if sec != nil && st.BoundUID != "" {
		if refs, changed := withOwnerRef(sec, req.PipelineRunName, st.BoundUID); changed {
			upd := sec.DeepCopy()
			upd.OwnerReferences = refs
			if sec, err = c.kube.CoreV1().Secrets(req.TargetNamespace).Update(ctx, upd, metav1.UpdateOptions{}); err != nil {
				return 0, err
			}
			log.Infow("secret attached to PipelineRun", "uid", st.BoundUID)
		}
	}

	if st.State == credentials.StateCompleted {
		st.ExpiresAt, st.Message = secretExpiry(sec), ""
		return 0, c.writeStatus(ctx, cm, st)
	}

	now := c.now()
	expiry := secretExpiry(sec)
	if sec == nil || expiry.IsZero() || !now.Before(expiry.Add(-c.cfg.RenewBefore)) {
		if sec == nil && st.BoundUID != "" {
			_, gone, err := c.boundPipelineRun(ctx, req, st.BoundUID, true)
			if err != nil {
				return 0, err
			}
			if gone {
				return 0, c.cleanup(ctx, cm, req)
			}
		}
		if sec, err = c.renew(ctx, cm, req, sec, st.BoundUID); err != nil {
			return c.fail(ctx, cm, st, sec, err)
		}
		st.RenewedAt = now
		log.Infow("credentials renewed", "expiresAt", credentials.FormatTime(secretExpiry(sec)))
	}

	st.ExpiresAt, st.Message = secretExpiry(sec), ""
	if st.BoundUID != "" {
		st.State = credentials.StateBound
	} else {
		st.State = credentials.StateReady
		if st.UnboundSince.IsZero() {
			st.UnboundSince = now
		}
		if age := now.Sub(st.UnboundSince); age > c.cfg.UnboundWarnAfter {
			log.Warnw("request still not bound to a PipelineRun, resolve or delete it manually", "unboundFor", age.Round(time.Second).String())
		}
	}
	if err := c.writeStatus(ctx, cm, st); err != nil {
		return 0, err
	}
	return max(st.ExpiresAt.Add(-c.cfg.RenewBefore).Sub(now), minRequeue), nil
}

// renew mints a token and writes data and expiry in a single create or
// update. On failure the stored Secret, and therefore its expiry, is unchanged.
func (c *Controller) renew(ctx context.Context, cm *corev1.ConfigMap, req *credentials.Request, sec *corev1.Secret, boundUID string) (*corev1.Secret, error) {
	tok, err := c.issuer.Issue(ctx)
	if err != nil {
		return sec, err
	}
	desired, err := c.newSecret(cm, req, tok, boundUID)
	if err != nil {
		return sec, err
	}
	secrets := c.kube.CoreV1().Secrets(req.TargetNamespace)
	if sec == nil {
		created, err := secrets.Create(ctx, desired, metav1.CreateOptions{})
		if err != nil {
			return nil, fmt.Errorf("creating secret: %w", err)
		}
		return created, nil
	}
	upd := sec.DeepCopy()
	upd.Data = desired.Data
	upd.StringData = nil
	upd.Annotations[credentials.AnnotationExpiresAt] = credentials.FormatTime(tok.ExpiresAt)
	updated, err := secrets.Update(ctx, upd, metav1.UpdateOptions{})
	if err != nil {
		return sec, fmt.Errorf("updating secret: %w", err)
	}
	return updated, nil
}

func (c *Controller) fail(ctx context.Context, cm *corev1.ConfigMap, st credentials.Status, sec *corev1.Secret, cause error) (time.Duration, error) {
	st.State, st.Message, st.ExpiresAt = credentials.StateError, truncate(cause.Error()), secretExpiry(sec)
	log := c.logger.With("request", cm.Namespace+"/"+cm.Name)
	log.Warnw("credential renewal failed", "error", cause.Error())
	if !st.ExpiresAt.IsZero() && !c.now().Before(st.ExpiresAt) {
		log.Errorw("credentials overdue: expiry passed without a successful renewal", "expiresAt", credentials.FormatTime(st.ExpiresAt))
	}
	if err := c.writeStatus(ctx, cm, st); err != nil {
		log.Warnw("could not publish error status", "error", err.Error())
	}
	return 0, cause
}

// boundPipelineRun returns the bound PipelineRun, or gone=true only when a
// live lookup confirms it was deleted or replaced by another build.
func (c *Controller) boundPipelineRun(ctx context.Context, req *credentials.Request, uid string, live bool) (*tektonv1.PipelineRun, bool, error) {
	if !live {
		pr, err := c.pipelineRuns.PipelineRuns(req.TargetNamespace).Get(req.PipelineRunName)
		if err == nil && string(pr.UID) == uid {
			return pr, false, nil
		}
		if err != nil && !apierrors.IsNotFound(err) {
			return nil, false, err
		}
	}
	pr, err := c.tekton.TektonV1().PipelineRuns(req.TargetNamespace).Get(ctx, req.PipelineRunName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	if string(pr.UID) != uid {
		return nil, true, nil
	}
	return pr, false, nil
}

// cleanup runs once the bound build is confirmed gone. It needs no GitHub
// access, so it also works during an outage.
func (c *Controller) cleanup(ctx context.Context, cm *corev1.ConfigMap, req *credentials.Request) error {
	secrets := c.kube.CoreV1().Secrets(req.TargetNamespace)
	sec, err := secrets.Get(ctx, req.SecretName, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
	case err != nil:
		return err
	case ownedBy(sec, cm):
		err = secrets.Delete(ctx, sec.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &sec.UID}})
		if err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	err = c.kube.CoreV1().ConfigMaps(cm.Namespace).Delete(ctx, cm.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &cm.UID}})
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	c.logger.Infow("bound PipelineRun deleted, removed credentials and request", "request", cm.Namespace+"/"+cm.Name)
	return nil
}

func (c *Controller) writeStatus(ctx context.Context, cm *corev1.ConfigMap, st credentials.Status) error {
	ann := maps.Clone(cm.Annotations)
	if ann == nil {
		ann = map[string]string{}
	}
	st.Apply(ann)
	if maps.Equal(ann, cm.Annotations) {
		return nil
	}
	upd := cm.DeepCopy()
	upd.Annotations = ann
	out, err := c.kube.CoreV1().ConfigMaps(cm.Namespace).Update(ctx, upd, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("publishing request status: %w", err)
	}
	*cm = *out
	return nil
}

func secretExpiry(sec *corev1.Secret) time.Time {
	if sec == nil {
		return time.Time{}
	}
	return credentials.ParseTime(sec.Annotations[credentials.AnnotationExpiresAt])
}

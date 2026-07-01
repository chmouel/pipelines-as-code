package gitlab

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/secrets"
	gitlab "gitlab.com/gitlab-org/api/client-go"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
)

const (
	rotationThreshold = 7 * 24 * time.Hour
	rotationNewExpiry = 30 * 24 * time.Hour
)

var tokenRotationMu sync.Mutex

func (v *Provider) isTokenAutoRotationEnabled() bool {
	if v.repo == nil || v.repo.Spec.Settings == nil || v.repo.Spec.Settings.Gitlab == nil {
		return true
	}
	if v.repo.Spec.Settings.Gitlab.TokenAutoRotation == nil {
		return true
	}
	return *v.repo.Spec.Settings.Gitlab.TokenAutoRotation
}

func (v *Provider) introspectToken() (*gitlab.PersonalAccessToken, error) {
	pat, resp, err := v.Client().PersonalAccessTokens.GetSinglePersonalAccessToken()
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			return nil, fmt.Errorf("token is invalid or expired")
		}
		return nil, fmt.Errorf("introspect token: %w", err)
	}
	return pat, nil
}

func needsRotation(pat *gitlab.PersonalAccessToken) bool {
	if pat.ExpiresAt == nil {
		return false
	}
	if !pat.Active {
		return false
	}
	expiresAt := time.Time(*pat.ExpiresAt)
	return time.Until(expiresAt) < rotationThreshold
}

var errMissingSelfRotateScope = fmt.Errorf("token lacks 'api' or 'self_rotate' scope required for auto-rotation — disable with settings.gitlab.token_auto_rotation: false")

func (v *Provider) rotateToken() (*gitlab.PersonalAccessToken, error) {
	newExpiry := gitlab.ISOTime(time.Now().Add(rotationNewExpiry))
	opts := &gitlab.RotatePersonalAccessTokenOptions{
		ExpiresAt: &newExpiry,
	}

	pat, resp, err := v.Client().PersonalAccessTokens.RotatePersonalAccessTokenSelf(opts)
	if err == nil {
		return pat, nil
	}

	if resp != nil && resp.StatusCode == http.StatusForbidden {
		return nil, errMissingSelfRotateScope
	}

	// If PAT self-rotate fails with other 4xx, try project access token self-rotate.
	if resp != nil && resp.StatusCode >= 400 && resp.StatusCode < 500 && v.targetProjectID != 0 {
		v.Logger.Debugf("PAT self-rotate returned %d, trying project access token rotation for project %d", resp.StatusCode, v.targetProjectID)
		projectOpts := &gitlab.RotateProjectAccessTokenOptions{
			ExpiresAt: &newExpiry,
		}
		projectPat, projectResp, projectErr := v.Client().ProjectAccessTokens.RotateProjectAccessTokenSelf(v.targetProjectID, projectOpts)
		if projectErr == nil {
			return &projectPat.PersonalAccessToken, nil
		}
		if projectResp != nil && projectResp.StatusCode == http.StatusForbidden {
			return nil, errMissingSelfRotateScope
		}
		return nil, fmt.Errorf("PAT rotation failed (%w), project token rotation also failed: %w", err, projectErr)
	}

	return nil, fmt.Errorf("rotate token: %w", err)
}

func (v *Provider) updateKubeSecret(ctx context.Context, newToken string) error {
	if v.repo == nil || v.repo.Spec.GitProvider == nil || v.repo.Spec.GitProvider.Secret == nil {
		return fmt.Errorf("repository CR has no git_provider.secret configured")
	}

	secretName := v.repo.Spec.GitProvider.Secret.Name
	secretKey := v.repo.Spec.GitProvider.Secret.Key
	if secretKey == "" {
		secretKey = secrets.DefaultGitProviderSecretKey
	}
	secretNS := v.repo.GetNamespace()

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		secret, err := v.run.Clients.Kube.CoreV1().Secrets(secretNS).Get(ctx, secretName, metav1.GetOptions{})
		if err != nil {
			return err
		}
		secret.Data[secretKey] = []byte(newToken)
		_, err = v.run.Clients.Kube.CoreV1().Secrets(secretNS).Update(ctx, secret, metav1.UpdateOptions{})
		return err
	})
}

func (v *Provider) maybeRotateToken(ctx context.Context) (string, error) {
	tokenRotationMu.Lock()
	defer tokenRotationMu.Unlock()

	pat, err := v.introspectToken()
	if err != nil {
		return "", fmt.Errorf("introspect: %w", err)
	}

	if !needsRotation(pat) {
		return "", nil
	}

	expiresAt := time.Time(*pat.ExpiresAt)
	v.Logger.Infof("gitlab token expires at %s (within %v threshold), rotating", expiresAt.Format(time.RFC3339), rotationThreshold)

	newPat, err := v.rotateToken()
	if err != nil {
		return "", fmt.Errorf("rotate: %w", err)
	}

	if err := v.updateKubeSecret(ctx, newPat.Token); err != nil {
		v.Logger.Errorf("CRITICAL: gitlab token was rotated but failed to update kubernetes secret: %v — old token is revoked, manual intervention required", err)
		return "", fmt.Errorf("token rotated but secret update failed (old token revoked): %w", err)
	}

	newExpiresAt := time.Time(*newPat.ExpiresAt)
	v.eventEmitter.EmitMessage(v.repo, zap.InfoLevel, "GitLabTokenRotated",
		fmt.Sprintf("GitLab access token rotated, new expiry: %s", newExpiresAt.Format(time.RFC3339)))

	return newPat.Token, nil
}

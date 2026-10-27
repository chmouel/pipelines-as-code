package credentials

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
)

// Environment variables enabling the proof-of-concept handoff in the PaC
// controller. Unset means disabled and the legacy secret path is used.
const (
	EnvEnabled      = "PAC_GITHUB_CREDENTIALS_SERVICE"
	EnvRepositories = "PAC_GITHUB_CREDENTIALS_SERVICE_REPOSITORIES"
	EnvNamespace    = "PAC_GITHUB_CREDENTIALS_SERVICE_NAMESPACE"
	EnvWait         = "PAC_GITHUB_CREDENTIALS_SERVICE_WAIT"

	defaultWait = 30 * time.Second
	// FreshnessMargin is how much token lifetime must remain before PaC
	// launches a build with service-owned credentials.
	FreshnessMargin = 5 * time.Minute
)

// HandoffSettings is the PaC side opt-in configuration.
type HandoffSettings struct {
	Enabled      bool
	AllRepos     bool
	Repositories []string
	Namespace    string
	Wait         time.Duration
}

func normalizeRepoURL(u string) string {
	return strings.TrimSuffix(strings.TrimSuffix(strings.ToLower(strings.TrimSpace(u)), "/"), ".git")
}

// HandoffSettingsFromEnv reads the opt-in configuration.
func HandoffSettingsFromEnv() HandoffSettings {
	s := HandoffSettings{Namespace: os.Getenv(EnvNamespace), Wait: defaultWait}
	if os.Getenv(EnvEnabled) != "true" {
		return s
	}
	for _, r := range strings.Split(os.Getenv(EnvRepositories), ",") {
		switch r = normalizeRepoURL(r); r {
		case "":
		case "*":
			s.AllRepos = true
		default:
			s.Repositories = append(s.Repositories, r)
		}
	}
	s.Enabled = s.AllRepos || len(s.Repositories) > 0
	if d, err := time.ParseDuration(os.Getenv(EnvWait)); err == nil && d > 0 {
		s.Wait = d
	}
	return s
}

// Applies reports whether builds for repoURL use the credential service.
func (s HandoffSettings) Applies(repoURL string) bool {
	if !s.Enabled {
		return false
	}
	if s.AllRepos {
		return true
	}
	return slices.Contains(s.Repositories, normalizeRepoURL(repoURL))
}

// Preparer submits credential requests and waits for the service.
type Preparer struct {
	Kube      kubernetes.Interface
	Namespace string
	Wait      time.Duration
	Interval  time.Duration
	Now       func() time.Time
}

func (p *Preparer) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

var errTerminal = errors.New("credential request refused")

// Prepare creates the request, or reuses an identical one, then waits until
// the service reports fresh credentials that are already stored in the Secret.
func (p *Preparer) Prepare(ctx context.Context, r Request) error {
	name := RequestName(r.TargetNamespace, r.PipelineRunName)
	cms := p.Kube.CoreV1().ConfigMaps(p.Namespace)
	if _, err := cms.Create(ctx, NewRequestConfigMap(p.Namespace, name, r), metav1.CreateOptions{}); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("creating credential request %s/%s: %w", p.Namespace, name, err)
		}
		existing, err := cms.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("reading existing credential request %s/%s: %w", p.Namespace, name, err)
		}
		got, err := ParseRequest(existing)
		if err != nil || *got != r {
			return fmt.Errorf("credential request %s/%s already exists for a different build", p.Namespace, name)
		}
	}

	interval := p.Interval
	if interval <= 0 {
		interval = time.Second
	}
	var lastErr error
	err := wait.PollUntilContextTimeout(ctx, interval, p.Wait, true, func(ctx context.Context) (bool, error) {
		lastErr = p.Check(ctx, r)
		if errors.Is(lastErr, errTerminal) {
			return false, lastErr
		}
		return lastErr == nil, nil
	})
	if errors.Is(err, errTerminal) {
		return err
	}
	if err != nil {
		return fmt.Errorf("credentials for %s/%s not ready after %s: %w", r.TargetNamespace, r.SecretName, p.Wait, lastErr)
	}
	return nil
}

// Check verifies the published status of an existing request.
func (p *Preparer) Check(ctx context.Context, r Request) error {
	name := RequestName(r.TargetNamespace, r.PipelineRunName)
	cm, err := p.Kube.CoreV1().ConfigMaps(p.Namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("reading credential request %s/%s: %w", p.Namespace, name, err)
	}
	st := StatusFromAnnotations(cm.Annotations)
	switch st.State {
	case StateReady, StateBound:
	case StateRejected, StateConflict:
		return fmt.Errorf("%w: %s: %s", errTerminal, st.State, st.Message)
	default:
		state := string(st.State)
		if state == "" {
			state = "not processed yet"
		}
		if st.Message != "" {
			return fmt.Errorf("request %s: %s", state, st.Message)
		}
		return fmt.Errorf("request %s", state)
	}
	if !st.ExpiresAt.After(p.now().Add(FreshnessMargin)) {
		return fmt.Errorf("credentials expire at %s, too soon to launch", FormatTime(st.ExpiresAt))
	}
	return nil
}

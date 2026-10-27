package service

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	ghinstallation "github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v91/github"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Token is a minted installation token and GitHub's reported expiry.
type Token struct {
	Value     string
	ExpiresAt time.Time
}

// TokenIssuer mints build tokens. Direct GitHub issuance is a replaceable
// part of the proof of concept.
type TokenIssuer interface {
	Issue(ctx context.Context) (Token, error)
}

// GitHubIssuer mints installation tokens restricted to one repository with
// contents:read. The App key is read before every mint so a rotated key is
// picked up without restarting.
type GitHubIssuer struct {
	cfg       *Config
	kube      kubernetes.Interface
	transport http.RoundTripper
	now       func() time.Time

	mu       sync.Mutex
	keyRV    string
	ghClient *github.Client
}

func NewGitHubIssuer(cfg *Config, kube kubernetes.Interface) *GitHubIssuer {
	return &GitHubIssuer{cfg: cfg, kube: kube, transport: http.DefaultTransport, now: time.Now}
}

func (g *GitHubIssuer) client(ctx context.Context) (*github.Client, error) {
	ns := g.cfg.KeySecretNamespace
	if ns == "" {
		ns = g.cfg.ControlNS
	}
	sec, err := g.kube.CoreV1().Secrets(ns).Get(ctx, g.cfg.KeySecretName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("reading GitHub App key secret %s/%s: %w", ns, g.cfg.KeySecretName, err)
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if g.ghClient != nil && g.keyRV == sec.ResourceVersion {
		return g.ghClient, nil
	}

	appID := g.cfg.AppID
	if appID == 0 {
		raw := strings.TrimSpace(string(sec.Data[g.cfg.AppIDSecretKey]))
		if appID, err = strconv.ParseInt(raw, 10, 64); err != nil || appID <= 0 {
			return nil, fmt.Errorf("no valid GitHub App ID configured or found in key %s of secret %s/%s", g.cfg.AppIDSecretKey, ns, g.cfg.KeySecretName)
		}
	}
	key := sec.Data[g.cfg.KeySecretKey]
	if len(key) == 0 {
		return nil, fmt.Errorf("key %s missing from secret %s/%s", g.cfg.KeySecretKey, ns, g.cfg.KeySecretName)
	}
	atr, err := ghinstallation.NewAppsTransport(g.transport, appID, key)
	if err != nil {
		return nil, fmt.Errorf("loading GitHub App private key: %w", err)
	}
	opts := []github.ClientOptionsFunc{github.WithTransport(atr), github.WithTimeout(30 * time.Second)}
	if g.cfg.APIURL != "" {
		base := strings.TrimSuffix(g.cfg.APIURL, "/") + "/"
		opts = append(opts, github.WithURLs(&base, &base))
	}
	client, err := github.NewClient(opts...)
	if err != nil {
		return nil, err
	}
	g.ghClient, g.keyRV = client, sec.ResourceVersion
	return client, nil
}

func (g *GitHubIssuer) Issue(ctx context.Context) (Token, error) {
	client, err := g.client(ctx)
	if err != nil {
		return Token{}, err
	}
	tok, _, err := client.Apps.CreateInstallationToken(ctx, g.cfg.InstallationID, &github.InstallationTokenOptions{
		RepositoryIDs: []int64{g.cfg.RepositoryID},
		Permissions:   &github.InstallationPermissions{Contents: new("read")},
	})
	if err != nil {
		return Token{}, fmt.Errorf("creating installation token: %w", err)
	}
	if tok.GetToken() == "" {
		return Token{}, fmt.Errorf("GitHub returned an empty installation token")
	}
	if tok.ExpiresAt == nil || !tok.ExpiresAt.After(g.now()) {
		return Token{}, fmt.Errorf("GitHub returned a missing or past token expiry")
	}
	if p := tok.GetPermissions(); p != nil && p.GetContents() != "read" {
		return Token{}, fmt.Errorf("GitHub returned unexpected contents permission %q", p.GetContents())
	}
	for _, r := range tok.Repositories {
		if r.GetID() != g.cfg.RepositoryID {
			return Token{}, fmt.Errorf("GitHub returned a token for unexpected repository %d", r.GetID())
		}
	}
	return Token{Value: tok.GetToken(), ExpiresAt: tok.ExpiresAt.Time}, nil
}

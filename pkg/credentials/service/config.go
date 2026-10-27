// Package service implements the standalone GitHub build credential service
// (proof of concept). It creates build git-auth Secrets from credential
// requests, renews them before expiry while the build is unfinished, binds
// them to the intended PipelineRun, and cleans up after the build is deleted.
package service

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/credentials"
)

// Config limits the service to one GitHub App installation, one repository
// and one output namespace.
type Config struct {
	AppID           int64
	InstallationID  int64
	RepositoryID    int64
	RepositoryURL   string
	APIURL          string
	ControlNS       string
	OutputNamespace string

	KeySecretNamespace string
	KeySecretName      string
	KeySecretKey       string
	AppIDSecretKey     string

	RenewBefore      time.Duration
	UnboundWarnAfter time.Duration
	Resync           time.Duration
	HealthAddr       string
}

func (c *Config) Validate() error {
	var missing []string
	if c.InstallationID <= 0 {
		missing = append(missing, "installation-id")
	}
	if c.RepositoryID <= 0 {
		missing = append(missing, "repository-id")
	}
	if c.RepositoryURL == "" {
		missing = append(missing, "repository-url")
	}
	if c.ControlNS == "" {
		missing = append(missing, "control-namespace")
	}
	if c.OutputNamespace == "" {
		missing = append(missing, "output-namespace")
	}
	if c.KeySecretName == "" {
		missing = append(missing, "key-secret-name")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required configuration: %s", strings.Join(missing, ", "))
	}
	if c.ControlNS == c.OutputNamespace {
		return fmt.Errorf("output namespace must differ from the control namespace")
	}
	u, err := url.Parse(c.RepositoryURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("repository-url must be https://host/owner/repo without credentials")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.ContainsAny(c.RepositoryURL, " \t\r\n") {
		return fmt.Errorf("repository-url must be https://host/owner/repo without credentials")
	}
	if c.RenewBefore <= 0 {
		return fmt.Errorf("renew-before must be positive")
	}
	return nil
}

// authorize applies the configured policy to a structurally valid request.
func (c *Config) authorize(r *credentials.Request) error {
	if r.RepositoryID != c.RepositoryID {
		return fmt.Errorf("repository %d is not permitted", r.RepositoryID)
	}
	if r.TargetNamespace != c.OutputNamespace {
		return fmt.Errorf("namespace %s is not permitted", r.TargetNamespace)
	}
	return nil
}

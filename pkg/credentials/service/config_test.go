package service

import (
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

func TestConfigValidate(t *testing.T) {
	valid := func() Config {
		return Config{
			InstallationID:  1,
			RepositoryID:    42,
			RepositoryURL:   "https://github.com/org/repo",
			ControlNS:       "pipelines-as-code",
			OutputNamespace: "builds",
			KeySecretName:   "pipelines-as-code-secret",
			RenewBefore:     15 * time.Minute,
		}
	}
	tests := []struct {
		name    string
		mod     func(*Config)
		wantErr string
	}{
		{name: "valid configuration"},
		{
			name:    "missing required fields",
			mod:     func(c *Config) { *c = Config{} },
			wantErr: "missing required configuration: installation-id, repository-id, repository-url, control-namespace, output-namespace, key-secret-name",
		},
		{
			name:    "control namespace equals output namespace",
			mod:     func(c *Config) { c.OutputNamespace = c.ControlNS },
			wantErr: "output namespace must differ from the control namespace",
		},
		{
			name:    "non-https repository URL",
			mod:     func(c *Config) { c.RepositoryURL = "http://github.com/org/repo" },
			wantErr: "repository-url must be https://host/owner/repo without credentials",
		},
		{
			name:    "missing host in repository URL",
			mod:     func(c *Config) { c.RepositoryURL = "https:///org/repo" },
			wantErr: "repository-url must be https://host/owner/repo without credentials",
		},
		{
			name:    "credentials in repository URL",
			mod:     func(c *Config) { c.RepositoryURL = "https://user:pass@github.com/org/repo" },
			wantErr: "repository-url must be https://host/owner/repo without credentials",
		},
		{
			name:    "query string in repository URL",
			mod:     func(c *Config) { c.RepositoryURL = "https://github.com/org/repo?foo=bar" },
			wantErr: "repository-url must be https://host/owner/repo without credentials",
		},
		{
			name:    "fragment in repository URL",
			mod:     func(c *Config) { c.RepositoryURL = "https://github.com/org/repo#frag" },
			wantErr: "repository-url must be https://host/owner/repo without credentials",
		},
		{
			name:    "too few path segments in repository URL",
			mod:     func(c *Config) { c.RepositoryURL = "https://github.com/org" },
			wantErr: "repository-url must be https://host/owner/repo without credentials",
		},
		{
			name:    "empty path segment in repository URL",
			mod:     func(c *Config) { c.RepositoryURL = "https://github.com/org//repo" },
			wantErr: "repository-url must be https://host/owner/repo without credentials",
		},
		{
			name:    "whitespace in repository URL",
			mod:     func(c *Config) { c.RepositoryURL = "https://github.com/org/repo name" },
			wantErr: "repository-url must be https://host/owner/repo without credentials",
		},
		{
			name:    "non-positive renew-before",
			mod:     func(c *Config) { c.RenewBefore = 0 },
			wantErr: "renew-before must be positive",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid()
			if tt.mod != nil {
				tt.mod(&cfg)
			}
			err := cfg.Validate()
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)
		})
	}
}

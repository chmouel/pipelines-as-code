package cli

import (
	"context"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	testki "github.com/openshift-pipelines/pipelines-as-code/pkg/test/kubernetestint"
	"gotest.tools/v3/assert"
)

func TestResolveEnvVars(t *testing.T) {
	tests := []struct {
		name    string
		envVars []v1alpha1.EnvVar
		secrets map[string]string
		wantEnv []string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "no env vars returns base env only",
			envVars: nil,
			wantErr: false,
		},
		{
			name: "literal value",
			envVars: []v1alpha1.EnvVar{
				{Name: "MY_VAR", Value: "hello"},
			},
			wantEnv: []string{"MY_VAR=hello"},
		},
		{
			name: "secret reference",
			envVars: []v1alpha1.EnvVar{
				{
					Name: "API_KEY",
					SecretRef: &v1alpha1.Secret{
						Name: "my-secret",
						Key:  "api-key",
					},
				},
			},
			secrets: map[string]string{
				"my-secret": "super-secret-value",
			},
			wantEnv: []string{"API_KEY=super-secret-value"},
		},
		{
			name: "secret reference with default key",
			envVars: []v1alpha1.EnvVar{
				{
					Name: "TOKEN",
					SecretRef: &v1alpha1.Secret{
						Name: "my-secret",
					},
				},
			},
			secrets: map[string]string{
				"my-secret": "token-value",
			},
			wantEnv: []string{"TOKEN=token-value"},
		},
		{
			name: "mixed literal and secret",
			envVars: []v1alpha1.EnvVar{
				{Name: "LITERAL", Value: "lit"},
				{
					Name: "SECRET",
					SecretRef: &v1alpha1.Secret{
						Name: "s1",
						Key:  "k1",
					},
				},
			},
			secrets: map[string]string{
				"s1": "sec",
			},
			wantEnv: []string{"LITERAL=lit", "SECRET=sec"},
		},
		{
			name: "empty name",
			envVars: []v1alpha1.EnvVar{
				{Name: "", Value: "hello"},
			},
			wantErr: true,
			errMsg:  "environment variable name is required",
		},
		{
			name: "secret not found",
			envVars: []v1alpha1.EnvVar{
				{
					Name: "MISSING",
					SecretRef: &v1alpha1.Secret{
						Name: "nonexistent",
						Key:  "key",
					},
				},
			},
			secrets: map[string]string{},
			wantErr: true,
			errMsg:  "failed to resolve secret",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kint := &testki.KinterfaceTest{
				GetSecretResult: tt.secrets,
			}

			result, err := ResolveEnvVars(context.Background(), tt.envVars, "test-ns", kint)

			if tt.wantErr {
				assert.Assert(t, err != nil, "expected error but got none")
				if tt.errMsg != "" {
					assert.ErrorContains(t, err, tt.errMsg)
				}
				return
			}

			assert.NilError(t, err)

			// Base env should always be present (PATH, HOME, LC_ALL, LANG)
			assert.Assert(t, len(result) >= 4, "expected at least 4 base env vars, got %d", len(result))

			// Check that our custom env vars are present at the end
			if tt.wantEnv != nil {
				offset := len(result) - len(tt.wantEnv)
				for i, want := range tt.wantEnv {
					assert.Equal(t, result[offset+i], want)
				}
			}
		})
	}
}

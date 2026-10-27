package credentials

import (
	"strings"
	"testing"
	"time"

	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
)

func TestParseRequest(t *testing.T) {
	valid := func() *corev1.ConfigMap {
		return NewRequestConfigMap("pipelines-as-code", "req", Request{
			RepositoryID: 42, TargetNamespace: "builds", SecretName: "auth", PipelineRunName: "build",
		})
	}
	tests := []struct {
		name    string
		mod     func(*corev1.ConfigMap)
		wantErr string
	}{
		{name: "valid request"},
		{name: "missing label", mod: func(cm *corev1.ConfigMap) { cm.Labels = nil }, wantErr: "missing label"},
		{name: "mutable", mod: func(cm *corev1.ConfigMap) { cm.Immutable = new(false) }, wantErr: "immutable"},
		{name: "unknown field", mod: func(cm *corev1.ConfigMap) { cm.Data["permissions"] = "write" }, wantErr: "unsupported request fields: permissions"},
		{name: "binary data", mod: func(cm *corev1.ConfigMap) { cm.BinaryData = map[string][]byte{"x": nil} }, wantErr: "binaryData"},
		{name: "missing field", mod: func(cm *corev1.ConfigMap) { delete(cm.Data, DataSecretName) }, wantErr: "missing request field secret-name"},
		{name: "bad repository id", mod: func(cm *corev1.ConfigMap) { cm.Data[DataRepositoryID] = "-1" }, wantErr: "invalid repository-id"},
		{name: "bad namespace", mod: func(cm *corev1.ConfigMap) { cm.Data[DataTargetNamespace] = "Bad_NS" }, wantErr: "invalid target-namespace"},
		{name: "bad secret name", mod: func(cm *corev1.ConfigMap) { cm.Data[DataSecretName] = "Bad_Secret" }, wantErr: "invalid secret-name"},
		{name: "bad pipelinerun name", mod: func(cm *corev1.ConfigMap) { cm.Data[DataPipelineRunName] = "Bad_PR" }, wantErr: "invalid pipelinerun-name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm := valid()
			if tt.mod != nil {
				tt.mod(cm)
			}
			r, err := ParseRequest(cm)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)
			assert.DeepEqual(t, *r, Request{RepositoryID: 42, TargetNamespace: "builds", SecretName: "auth", PipelineRunName: "build"})
		})
	}
}

func TestRequestName(t *testing.T) {
	a := RequestName("ns", "build-abc")
	assert.Equal(t, a, RequestName("ns", "build-abc"))
	assert.Assert(t, a != RequestName("other", "build-abc"))
	long := RequestName("ns", strings.Repeat("a", 39)+"-"+strings.Repeat("b", 200))
	assert.Assert(t, len(long) <= 63, long)
	assert.Assert(t, strings.HasPrefix(long, strings.Repeat("a", 39)+"-ghcred-"), long)
	emptyPrefix := RequestName("ns", "---")
	assert.Assert(t, strings.HasPrefix(emptyPrefix, "build-ghcred-"), emptyPrefix)
}

func TestHandoffSettingsFromEnv(t *testing.T) {
	tests := []struct {
		name     string
		env      map[string]string
		repo     string
		want     bool
		wantWait time.Duration
	}{
		{
			name:     "disabled without the switch",
			env:      map[string]string{EnvRepositories: "*"},
			repo:     "https://github.com/org/repo",
			wantWait: defaultWait,
		},
		{
			name:     "enabled without repositories stays disabled",
			env:      map[string]string{EnvEnabled: "true"},
			repo:     "https://github.com/org/repo",
			wantWait: defaultWait,
		},
		{
			name:     "wildcard applies to every repository",
			env:      map[string]string{EnvEnabled: "true", EnvRepositories: "*", EnvWait: "10s"},
			repo:     "https://github.com/org/anything",
			want:     true,
			wantWait: 10 * time.Second,
		},
		{
			name:     "listed repository matches after normalization",
			env:      map[string]string{EnvEnabled: "true", EnvRepositories: "https://github.com/org/other, https://GitHub.com/Org/Repo.git"},
			repo:     "https://github.com/org/repo/",
			want:     true,
			wantWait: defaultWait,
		},
		{
			name:     "unlisted repository keeps the legacy path",
			env:      map[string]string{EnvEnabled: "true", EnvRepositories: "https://github.com/org/other"},
			repo:     "https://github.com/org/repo",
			wantWait: defaultWait,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range []string{EnvEnabled, EnvRepositories, EnvNamespace, EnvWait} {
				t.Setenv(k, tt.env[k])
			}
			s := HandoffSettingsFromEnv()
			assert.Equal(t, s.Applies(tt.repo), tt.want)
			assert.Equal(t, s.Wait, tt.wantWait)
		})
	}
}

package params

import (
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/consoleui"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubefake "k8s.io/client-go/kubernetes/fake"
	rtesting "knative.dev/pkg/reconciler/testing"

	"go.uber.org/zap"
	"gotest.tools/v3/assert"
)

func TestUpdatePacConfigAndDetectBackendChange(t *testing.T) {
	tests := []struct {
		name           string
		initialBackend string
		configMapName  string
		configData     map[string]string
		wantOld        string
		wantNew        string
		wantChanged    bool
		wantErr        string
	}{
		{
			name:           "detects backend change",
			initialBackend: settings.ConcurrencyBackendMemory,
			configMapName:  "pac-config",
			configData: map[string]string{
				"concurrency-backend":  settings.ConcurrencyBackendLease,
				"tekton-dashboard-url": "https://dashboard.example.test",
			},
			wantOld:     settings.ConcurrencyBackendMemory,
			wantNew:     settings.ConcurrencyBackendLease,
			wantChanged: true,
		},
		{
			name:           "ignores unchanged backend",
			initialBackend: settings.ConcurrencyBackendLease,
			configMapName:  "pac-config",
			configData: map[string]string{
				"concurrency-backend":  settings.ConcurrencyBackendLease,
				"tekton-dashboard-url": "https://dashboard.example.test",
			},
			wantOld:     settings.ConcurrencyBackendLease,
			wantNew:     settings.ConcurrencyBackendLease,
			wantChanged: false,
		},
		{
			name:           "does not report change when previous backend is empty",
			initialBackend: "",
			configMapName:  "pac-config",
			configData: map[string]string{
				"concurrency-backend":  settings.ConcurrencyBackendLease,
				"tekton-dashboard-url": "https://dashboard.example.test",
			},
			wantOld:     "",
			wantNew:     settings.ConcurrencyBackendLease,
			wantChanged: false,
		},
		{
			name:           "returns update error when configmap is missing",
			initialBackend: settings.ConcurrencyBackendLease,
			configMapName:  "missing-config",
			configData: map[string]string{
				"concurrency-backend": settings.ConcurrencyBackendLease,
			},
			wantOld: settings.ConcurrencyBackendLease,
			wantNew: settings.ConcurrencyBackendLease,
			wantErr: "configmaps \"missing-config\" not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			ctx = info.StoreNS(ctx, "pac")

			run := &Run{
				Clients: clients.Clients{
					Kube: kubefake.NewSimpleClientset(&corev1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "pac-config",
							Namespace: "pac",
						},
						Data: tt.configData,
					}),
					Log: zap.NewNop().Sugar(),
				},
				Info: info.Info{
					Pac: &info.PacOpts{
						Settings: settings.Settings{
							ConcurrencyBackend: tt.initialBackend,
						},
					},
					Controller: &info.ControllerInfo{
						Configmap: tt.configMapName,
					},
				},
			}
			run.Clients.InitClients()
			run.Clients.SetConsoleUI(consoleui.FallBackConsole{})

			oldBackend, newBackend, changed, err := updatePacConfigAndDetectBackendChange(ctx, run)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				assert.Equal(t, oldBackend, tt.wantOld)
				assert.Equal(t, newBackend, tt.wantNew)
				assert.Equal(t, changed, false)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, oldBackend, tt.wantOld)
			assert.Equal(t, newBackend, tt.wantNew)
			assert.Equal(t, changed, tt.wantChanged)
		})
	}
}

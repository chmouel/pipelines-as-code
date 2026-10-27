package service

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubefake "k8s.io/client-go/kubernetes/fake"
)

func privateKeyPEM(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	assert.NilError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
}

func TestGitHubIssuer(t *testing.T) {
	tests := []struct {
		name     string
		response map[string]any
		status   int
		wantErr  string
	}{
		{
			name:     "restricted token with GitHub expiry",
			response: map[string]any{"token": "ghs_test", "expires_at": now.Add(time.Hour).Format(time.RFC3339), "permissions": map[string]string{"contents": "read"}},
		},
		{
			name:    "GitHub error",
			status:  http.StatusForbidden,
			wantErr: "creating installation token",
		},
		{
			name:     "missing expiry",
			response: map[string]any{"token": "ghs_test"},
			wantErr:  "missing or past token expiry",
		},
		{
			name:     "broader permission than requested",
			response: map[string]any{"token": "ghs_test", "expires_at": now.Add(time.Hour).Format(time.RFC3339), "permissions": map[string]string{"contents": "write"}},
			wantErr:  "unexpected contents permission",
		},
		{
			name: "token for another repository",
			response: map[string]any{
				"token": "ghs_test", "expires_at": now.Add(time.Hour).Format(time.RFC3339),
				"repositories": []map[string]any{{"id": 7}},
			},
			wantErr: "unexpected repository 7",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, r.URL.Path, "/app/installations/99/access_tokens")
				assert.NilError(t, json.NewDecoder(r.Body).Decode(&body))
				if tt.status != 0 {
					w.WriteHeader(tt.status)
					return
				}
				w.WriteHeader(http.StatusCreated)
				assert.NilError(t, json.NewEncoder(w).Encode(tt.response))
			}))
			defer srv.Close()

			kube := kubefake.NewClientset(&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "app-key", Namespace: ctrlNS},
				Data:       map[string][]byte{"github-private-key": privateKeyPEM(t), "github-application-id": []byte("12345")},
			})
			cfg := &Config{
				InstallationID: 99, RepositoryID: repoID, APIURL: srv.URL, ControlNS: ctrlNS,
				KeySecretName: "app-key", KeySecretKey: "github-private-key", AppIDSecretKey: "github-application-id",
			}
			g := NewGitHubIssuer(cfg, kube)
			g.now = func() time.Time { return now }
			tok, err := g.Issue(context.Background())
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, tok.Value, "ghs_test")
			assert.Assert(t, tok.ExpiresAt.Equal(now.Add(time.Hour)))
			assert.DeepEqual(t, body["repository_ids"], []any{float64(repoID)})
			assert.DeepEqual(t, body["permissions"], map[string]any{"contents": "read"})
		})
	}
}

func TestGitHubIssuerReloadsRotatedKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"token": "ghs_test", "expires_at": now.Add(time.Hour).Format(time.RFC3339)})
	}))
	defer srv.Close()
	key := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "app-key", Namespace: ctrlNS, ResourceVersion: "1"},
		Data:       map[string][]byte{"k": privateKeyPEM(t)},
	}
	kube := kubefake.NewClientset(key)
	g := NewGitHubIssuer(&Config{AppID: 1, InstallationID: 99, RepositoryID: repoID, APIURL: srv.URL, ControlNS: ctrlNS, KeySecretName: "app-key", KeySecretKey: "k"}, kube)
	g.now = func() time.Time { return now }
	ctx := context.Background()

	_, err := g.Issue(ctx)
	assert.NilError(t, err)
	first := g.ghClient
	_, err = g.Issue(ctx)
	assert.NilError(t, err)
	assert.Assert(t, g.ghClient == first, "client rebuilt without a key change")

	key.Data["k"], key.ResourceVersion = privateKeyPEM(t), "2"
	_, err = kube.CoreV1().Secrets(ctrlNS).Update(ctx, key, metav1.UpdateOptions{})
	assert.NilError(t, err)
	_, err = g.Issue(ctx)
	assert.NilError(t, err)
	assert.Assert(t, g.ghClient != first, "rotated key not loaded")
}

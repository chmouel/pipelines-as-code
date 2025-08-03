package github

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
)

func TestProfilingTransport(t *testing.T) {
	observer, logs := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "5000")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewProfingClient(logger)
	_, err := client.Get(server.URL)
	assert.NilError(t, err)

	assert.Equal(t, 1, logs.Len())
	log := logs.All()[0]
	assert.Assert(t, log.Level == zap.InfoLevel)
	assert.Assert(t, strings.Contains(log.Message, "GitHub API call to"))
	assert.Assert(t, strings.Contains(log.Message, "ratelimit-remaining: 5000"))
}

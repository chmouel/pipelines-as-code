package github

import (
	"net/http"
	"time"

	"go.uber.org/zap"
)

type ProfilingTransport struct {
	Transport http.RoundTripper
	Logger    *zap.SugaredLogger
	Counter   int
}

func (t *ProfilingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	resp, err := t.Transport.RoundTrip(req)
	duration := time.Since(start)
	t.Counter++
	if resp != nil {
		remaining := resp.Header.Get("X-RateLimit-Remaining")
		t.Logger.Debugf("GitHub API call to %s took %v, ratelimit-remaining: %s", req.URL, duration, remaining)
	} else {
		t.Logger.Debugf("GitHub API call to %s took %v", req.URL, duration)
	}
	return resp, err
}

func NewProfingClient(logger *zap.SugaredLogger) *http.Client {
	return &http.Client{
		Transport: &ProfilingTransport{
			Transport: http.DefaultTransport,
			Logger:    logger,
		},
	}
}

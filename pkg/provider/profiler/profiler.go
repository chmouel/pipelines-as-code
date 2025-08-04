package profiler

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
		t.Logger.Debugf("API call to %s took %v", req.URL, duration)
	} else {
		t.Logger.Debugf("API call to %s took %v", req.URL, duration)
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

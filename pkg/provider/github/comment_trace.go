package github

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/random"
)

const (
	commentRequestIDHeader     = "X-PAC-Comment-Request-ID"
	commentRequestMarkerPrefix = "<!-- pac-comment-request-id: "
	commentRequestMarkerSuffix = " -->"
)

type commentRequestIDKey struct{}

type commentTraceTransport struct {
	base http.RoundTripper
}

func newCommentTraceTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &commentTraceTransport{base: base}
}

func withCommentRequestID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, commentRequestIDKey{}, id)
}

func commentRequestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(commentRequestIDKey{}).(string)
	return id
}

func commentRequestMarker(id string) string {
	return fmt.Sprintf("%s%s%s", commentRequestMarkerPrefix, id, commentRequestMarkerSuffix)
}

func appendCommentRequestMarker(body, id string) string {
	if id == "" {
		return body
	}
	body = removeCommentRequestMarkers(body)
	return fmt.Sprintf("%s\n%s", body, commentRequestMarker(id))
}

func extractCommentRequestID(body string) string {
	start := strings.Index(body, commentRequestMarkerPrefix)
	if start == -1 {
		return ""
	}
	start += len(commentRequestMarkerPrefix)
	end := strings.Index(body[start:], commentRequestMarkerSuffix)
	if end == -1 {
		return ""
	}
	return body[start : start+end]
}

func removeCommentRequestMarkers(body string) string {
	for {
		start := strings.Index(body, commentRequestMarkerPrefix)
		if start == -1 {
			return body
		}
		end := strings.Index(body[start+len(commentRequestMarkerPrefix):], commentRequestMarkerSuffix)
		if end == -1 {
			return body
		}
		end = start + len(commentRequestMarkerPrefix) + end + len(commentRequestMarkerSuffix)
		// Trim a preceding newline to avoid blank lines.
		if start > 0 && body[start-1] == '\n' {
			start--
		}
		body = body[:start] + body[end:]
	}
}

func (t *commentTraceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil {
		return t.base.RoundTrip(req)
	}

	if isIssueCommentRequest(req) && req.Header.Get(commentRequestIDHeader) == "" {
		cloned := cloneRequestWithHeaders(req)
		requestID := commentRequestIDFromContext(req.Context())
		if requestID == "" {
			requestID = newCommentRequestID()
		}
		cloned.Header.Set(commentRequestIDHeader, requestID)
		return t.base.RoundTrip(cloned)
	}

	return t.base.RoundTrip(req)
}

func isIssueCommentRequest(req *http.Request) bool {
	path := req.URL.Path
	switch req.Method {
	case http.MethodPost:
		return strings.Contains(path, "/issues/") && strings.HasSuffix(path, "/comments")
	case http.MethodPatch:
		return strings.Contains(path, "/issues/comments/")
	default:
		return false
	}
}

func newCommentRequestID() string {
	return fmt.Sprintf("pac-%d-%s", time.Now().UnixNano(), random.AlphaString(6))
}

func cloneRequestWithHeaders(req *http.Request) *http.Request {
	cloned := new(http.Request)
	*cloned = *req
	cloned.Header = make(http.Header, len(req.Header))
	for k, v := range req.Header {
		cloned.Header[k] = append([]string(nil), v...)
	}
	return cloned
}

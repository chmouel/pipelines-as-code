package github

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	ghtesthelper "github.com/openshift-pipelines/pipelines-as-code/pkg/test/github"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestGetObjectCaching(t *testing.T) {
	// Test setup
	ctx, _ := rtesting.SetupFakeContext(t)
	fakeclient, mux, _, teardown := ghtesthelper.SetupGH()
	defer teardown()

	// Create test event
	event := info.NewEvent()
	event.Organization = "owner"
	event.Repository = "repo"

	// Setup mocks
	sha := "0123456789abcdef0123456789abcdef01234567" // Valid SHA format
	content := "test content"
	encodedContent := base64.StdEncoding.EncodeToString([]byte(content))

	// Track API calls
	apiCallCount := 0

	// Create handler for Git.GetBlob calls
	mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/git/blobs/%s", event.Organization, event.Repository, sha),
		func(rw http.ResponseWriter, _ *http.Request) {
			apiCallCount++
			fmt.Fprintf(rw, `{"content": "%s", "encoding": "base64"}`, encodedContent)
		})

	// Setup logger to capture debug logs
	observer, observedLogs := zapobserver.New(zap.DebugLevel)
	fakelogger := zap.New(observer).Sugar()

	// Create provider with caching enabled
	provider := &Provider{
		ghClient: fakeclient,
		Logger:   fakelogger,
	}

	// Initialize cache with a short TTL for testing
	provider.UpdateCacheConfig(true, 50*time.Millisecond)
	// Test 1: First call should be a cache miss and make an API call
	data1, err := provider.getObject(ctx, sha, event)
	assert.NilError(t, err)
	assert.Equal(t, string(data1), content)
	assert.Equal(t, apiCallCount, 1, "Expected 1 API call on first request")

	// Check for cache miss log - less strict matching as logger formatting might differ
	foundMissLog := false
	for _, entry := range observedLogs.All() {
		if strings.Contains(entry.Message, "Cache miss for") &&
			strings.Contains(entry.Message, event.Organization) &&
			strings.Contains(entry.Message, event.Repository) &&
			strings.Contains(entry.Message, sha) {
			foundMissLog = true
			break
		}
	}
	assert.Assert(t, foundMissLog, "Expected cache miss log not found")

	// Reset observed logs
	observedLogs.TakeAll()

	// Test 2: Second call should be a cache hit and not make an API call
	data2, err := provider.getObject(ctx, sha, event)
	assert.NilError(t, err)
	assert.Equal(t, string(data2), content)
	assert.Equal(t, apiCallCount, 1, "Expected no additional API calls on second request")

	// Check for cache hit log
	foundHitLog := false
	for _, entry := range observedLogs.All() {
		if strings.Contains(entry.Message, "Cache hit for") &&
			strings.Contains(entry.Message, event.Organization) &&
			strings.Contains(entry.Message, event.Repository) &&
			strings.Contains(entry.Message, sha) {
			foundHitLog = true
			break
		}
	}
	assert.Assert(t, foundHitLog, "Expected cache hit log not found")

	// Reset observed logs
	observedLogs.TakeAll()

	// Test 3: Wait for cache expiration, then call again - should make another API call
	time.Sleep(100 * time.Millisecond) // Wait for cache to expire

	data3, err := provider.getObject(ctx, sha, event)
	assert.NilError(t, err)
	assert.Equal(t, string(data3), content)
	assert.Equal(t, apiCallCount, 2, "Expected another API call after cache expiration")

	// Test 4: Non-SHA reference should skip cache
	nonSha := "main" // Branch name, not a SHA

	// Setup handler for non-SHA reference
	mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/git/blobs/%s", event.Organization, event.Repository, nonSha),
		func(rw http.ResponseWriter, _ *http.Request) {
			apiCallCount++
			fmt.Fprintf(rw, `{"content": "%s", "encoding": "base64"}`, encodedContent)
		})

	// Reset observed logs
	observedLogs.TakeAll()

	// First call with non-SHA
	_, err = provider.getObject(ctx, nonSha, event)
	assert.NilError(t, err)

	// Second call with non-SHA should still make an API call
	_, err = provider.getObject(ctx, nonSha, event)
	assert.NilError(t, err)

	// Should have made 2 more API calls (one for each non-SHA call)
	assert.Equal(t, apiCallCount, 4, "Expected API calls for both non-SHA requests")

	// Check for skipped cache log
	foundSkipLog := false
	for _, entry := range observedLogs.All() {
		if strings.Contains(entry.Message, "Skipping cache for non-SHA reference:") &&
			strings.Contains(entry.Message, nonSha) {
			foundSkipLog = true
			break
		}
	}
	assert.Assert(t, foundSkipLog, "Expected skip cache log not found")
}

func TestEnvironmentConfig(t *testing.T) {
	// Setup for testing environment variables
	os.Setenv("PAC_GITHUB_CACHE_TTL", "1h")
	os.Setenv("PAC_GITHUB_CACHE_ENABLED", "true")
	defer func() {
		os.Unsetenv("PAC_GITHUB_CACHE_TTL")
		os.Unsetenv("PAC_GITHUB_CACHE_ENABLED")
	}()

	// Setup logger to capture debug logs
	observer, observedLogs := zapobserver.New(zap.DebugLevel)
	fakelogger := zap.New(observer).Sugar()

	// Create provider
	provider := &Provider{
		Logger: fakelogger,
	}

	// Test SetClient reads environment variables
	ctx, _ := rtesting.SetupFakeContext(t)
	fakeclient, _, _, teardown := ghtesthelper.SetupGH()
	defer teardown()

	// Create fake event for SetClient
	event := info.NewEvent()
	event.Provider.URL = "https://github.com"
	event.Provider.Token = "faketoken"

	// Create provider with client
	provider.ghClient = fakeclient

	// Call SetClient to initialize cache from environment
	err := provider.SetClient(ctx, nil, event, nil, nil)
	assert.NilError(t, err)

	// Check if cache was initialized with correct TTL
	assert.Assert(t, provider.fileCache != nil, "Cache should be initialized")
	assert.Assert(t, provider.cacheConfig != nil, "Cache config should be initialized")
	assert.Equal(t, provider.cacheConfig.TTL, 1*time.Hour, "Cache TTL should be 1h")
	assert.Equal(t, provider.cacheConfig.Enabled, true, "Cache should be enabled")

	// Check for debug log
	foundTTLLog := false
	for _, entry := range observedLogs.All() {
		if entry.Message == "Using GitHub cache TTL from environment: 1h0m0s" {
			foundTTLLog = true
			break
		}
	}
	assert.Assert(t, foundTTLLog, "Expected TTL log not found")

	// Test 2: Disabled via environment
	os.Setenv("PAC_GITHUB_CACHE_ENABLED", "false")

	// Reset observed logs
	observedLogs.TakeAll()

	// New provider
	provider2 := &Provider{
		Logger:   fakelogger,
		ghClient: fakeclient,
	}

	// Call SetClient to initialize cache from environment
	err = provider2.SetClient(ctx, nil, event, nil, nil)
	assert.NilError(t, err)

	// Check if cache config reflects disabled setting
	assert.Equal(t, provider2.cacheConfig.Enabled, false, "Cache should be disabled")

	// Check for debug log
	foundDisabledLog := false
	for _, entry := range observedLogs.All() {
		if entry.Message == "GitHub cache disabled based on environment setting" {
			foundDisabledLog = true
			break
		}
	}
	assert.Assert(t, foundDisabledLog, "Expected disabled log not found")

	// Test 3: Invalid TTL
	os.Setenv("PAC_GITHUB_CACHE_TTL", "invalid")
	os.Setenv("PAC_GITHUB_CACHE_ENABLED", "true")

	// Reset observed logs
	observedLogs.TakeAll()

	// New provider
	provider3 := &Provider{
		Logger:   fakelogger,
		ghClient: fakeclient,
	}

	// Call SetClient to initialize cache from environment
	err = provider3.SetClient(ctx, nil, event, nil, nil)
	assert.NilError(t, err)

	// Check if cache was initialized with default TTL
	assert.Equal(t, provider3.cacheConfig.TTL, defaultCacheTTL, "Cache TTL should be default")

	// Check for warning log
	foundWarningLog := false
	for _, entry := range observedLogs.All() {
		if strings.Contains(entry.Message, "Invalid PAC_GITHUB_CACHE_TTL value: invalid") {
			foundWarningLog = true
			break
		}
	}
	assert.Assert(t, foundWarningLog, "Expected warning log not found")
}

func TestGetFileInsideRepoCaching(t *testing.T) {
	// Test setup
	ctx, _ := rtesting.SetupFakeContext(t)
	fakeclient, mux, _, teardown := ghtesthelper.SetupGH()
	defer teardown()

	// Create test event
	event := info.NewEvent()
	event.Organization = "owner"
	event.Repository = "repo"
	event.HeadBranch = "main"

	// Setup mocks
	sha := "0123456789abcdef0123456789abcdef01234567" // Valid SHA format
	path := "path/to/file.yaml"
	content := "test file content"
	encodedContent := base64.StdEncoding.EncodeToString([]byte(content))

	// Track API calls
	apiCallCount := 0

	// Create handler for fetching file path
	mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/contents/%s", event.Organization, event.Repository, path),
		func(rw http.ResponseWriter, r *http.Request) {
			apiCallCount++
			fmt.Fprintf(rw, `{"name": "file.yaml", "path": "%s", "sha": "%s", "content": "%s", "encoding": "base64"}`, path, sha, encodedContent)
		})

	// Setup logger to capture debug logs
	observer, observedLogs := zapobserver.New(zap.DebugLevel)
	fakelogger := zap.New(observer).Sugar()

	// Create provider with caching enabled
	provider := &Provider{
		ghClient: fakeclient,
		Logger:   fakelogger,
	}

	// Initialize cache with a short TTL for testing
	provider.UpdateCacheConfig(true, 50*time.Millisecond)

	// Test 1: First call should make API calls
	fileContent1, err := provider.GetFileInsideRepo(ctx, event, path, sha)
	assert.NilError(t, err)
	assert.Equal(t, fileContent1, content)

	// First call should make API calls
	assert.Equal(t, apiCallCount, 1, "Expected API calls on first request")

	// Reset observed logs
	observedLogs.TakeAll()

	// Test 2: Second call should use cache
	fileContent2, err := provider.GetFileInsideRepo(ctx, event, path, sha)
	assert.NilError(t, err)
	assert.Equal(t, fileContent2, content)

	// Should use cache and not make additional API calls
	assert.Equal(t, apiCallCount, 1, "Expected no additional API calls on second request")

	// Check for cache hit log
	foundHitLog := false
	for _, entry := range observedLogs.All() {
		if strings.Contains(entry.Message, "Cache hit for") && strings.Contains(entry.Message, path) {
			foundHitLog = true
			break
		}
	}
	assert.Assert(t, foundHitLog, "Expected cache hit log not found")

	// Test 3: Wait for cache expiration, then call again
	time.Sleep(100 * time.Millisecond) // Wait for cache to expire

	fileContent3, err := provider.GetFileInsideRepo(ctx, event, path, sha)
	assert.NilError(t, err)
	assert.Equal(t, fileContent3, content)

	// Should make API calls again after cache expiration
	assert.Equal(t, apiCallCount, 2, "Expected API calls after cache expiration")
}

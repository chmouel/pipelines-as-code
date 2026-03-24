package pprofserver

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEnabledFromEnv(t *testing.T) {
	t.Setenv(EnvEnable, "true")
	if !EnabledFromEnv() {
		t.Fatal("EnabledFromEnv() = false, want true")
	}

	t.Setenv(EnvEnable, "false")
	if EnabledFromEnv() {
		t.Fatal("EnabledFromEnv() = true, want false")
	}
}

func TestAddrFromEnv(t *testing.T) {
	if got := AddrFromEnv(); got != DefaultAddr {
		t.Fatalf("AddrFromEnv() = %q, want %q", got, DefaultAddr)
	}

	t.Setenv(EnvAddr, "127.0.0.1:7777")
	if got := AddrFromEnv(); got != "127.0.0.1:7777" {
		t.Fatalf("AddrFromEnv() = %q, want %q", got, "127.0.0.1:7777")
	}
}

func TestNewMuxServesPprofIndex(t *testing.T) {
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/debug/pprof/", nil)
	rec := httptest.NewRecorder()

	NewMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.Len() == 0 {
		t.Fatal("empty pprof index response")
	}
}

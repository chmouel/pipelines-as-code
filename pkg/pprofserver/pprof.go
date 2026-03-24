package pprofserver

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/pprof"
	"os"
	"strings"
	"time"
)

const (
	EnvEnable   = "PAC_ENABLE_PPROF"
	EnvAddr     = "PAC_PPROF_ADDR"
	DefaultAddr = "127.0.0.1:6060"
)

func EnabledFromEnv() bool {
	value, ok := os.LookupEnv(EnvEnable)
	return ok && strings.EqualFold(value, "true")
}

func AddrFromEnv() string {
	if value, ok := os.LookupEnv(EnvAddr); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return DefaultAddr
}

func NewMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	mux.Handle("/debug/pprof/allocs", pprof.Handler("allocs"))
	mux.Handle("/debug/pprof/block", pprof.Handler("block"))
	mux.Handle("/debug/pprof/goroutine", pprof.Handler("goroutine"))
	mux.Handle("/debug/pprof/heap", pprof.Handler("heap"))
	mux.Handle("/debug/pprof/mutex", pprof.Handler("mutex"))
	mux.Handle("/debug/pprof/threadcreate", pprof.Handler("threadcreate"))
	return mux
}

func Start(ctx context.Context, component string) *http.Server {
	if !EnabledFromEnv() {
		return nil
	}

	srv := &http.Server{
		Addr:              AddrFromEnv(),
		Handler:           NewMux(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("%s pprof server listening on http://%s/debug/pprof/", component, srv.Addr)

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("%s pprof shutdown failed: %v", component, err)
		}
	}()

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("%s pprof server failed: %v", component, err)
		}
	}()

	return srv
}

package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	pacversioned "github.com/openshift-pipelines/pipelines-as-code/pkg/generated/clientset/versioned"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/reconciler"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/sync"
	tektonversioned "github.com/tektoncd/pipeline/pkg/client/clientset/versioned"
	"go.uber.org/zap"
	"k8s.io/client-go/rest"
	"knative.dev/pkg/injection"
	"knative.dev/pkg/injection/sharedmain"
	"knative.dev/pkg/signals"
)

const globalProbesPort = "8080"

func main() {
	probesPort := globalProbesPort
	envProbePort := os.Getenv("PAC_WATCHER_PORT")
	if envProbePort != "" {
		probesPort = envProbePort
	}

	// Create logger for logging
	logger, _ := zap.NewProduction()
	defer logger.Sync()
	sugar := logger.Sugar()

	mux := http.NewServeMux()
	mux.HandleFunc("/live", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "ok")
	})

	// This parses flags.
	cfg := injection.ParseAndGetRESTConfigOrDie()

	if cfg.QPS == 0 {
		cfg.QPS = 2 * rest.DefaultQPS
	}
	if cfg.Burst == 0 {
		cfg.Burst = rest.DefaultBurst
	}

	// multiply by no of controllers being created
	cfg.QPS = 5 * cfg.QPS
	cfg.Burst = 5 * cfg.Burst
	ctx := signals.NewContext()
	if val, ok := os.LookupEnv("PAC_DISABLE_HA"); ok {
		if strings.ToLower(val) == "true" {
			ctx = sharedmain.WithHADisabled(ctx)
		}
	}

	if val, ok := os.LookupEnv("PAC_DISABLE_HEALTH_PROBE"); ok {
		if strings.ToLower(val) == "true" {
			ctx = sharedmain.WithHealthProbesDisabled(ctx)
		}
	}

	// Create clients
	tektonClient, err := tektonversioned.NewForConfig(cfg)
	if err != nil {
		log.Fatalf("Failed to create Tekton client: %v", err)
	}

	pacClient, err := pacversioned.NewForConfig(cfg)
	if err != nil {
		log.Fatalf("Failed to create PAC client: %v", err)
	}

	// Create and register queue manager and clients globally so endpoints can access them
	queueManager := sync.NewQueueManager(sugar)
	sync.RegisterQueueManager(queueManager)
	sync.RegisterClients(tektonClient, pacClient)

	// Create and setup queue health checker
	healthChecker := sync.NewQueueHealthChecker(sugar)
	healthChecker.SetClients(tektonClient, pacClient)
	healthChecker.SetQueueManager(queueManager)

	// Start the health checker in the background
	go healthChecker.Start(ctx)

	// Create and register queue management endpoints
	queueEndpoints := sync.NewQueueEndpoints(sugar)
	queueEndpoints.RegisterHandlers(mux)

	c := make(chan struct{})
	go func() {
		log.Println("started goroutine for watcher")
		c <- struct{}{}
		// start the web server on port and accept requests
		log.Printf("Readiness and health check server listening on port %s", probesPort)
		// timeout values same as default one from triggers eventlistener
		// https://github.com/tektoncd/triggers/blame/b5b0ee1249402187d1ceff68e0b9d4e49f2ee957/pkg/sink/initialization.go#L48-L52
		srv := &http.Server{
			ReadTimeout:  5 * time.Second,
			WriteTimeout: 40 * time.Second,
			Addr:         ":" + probesPort,
			Handler:      mux,
		}

		go func() {
			<-ctx.Done()
			sugar.Info("Shutting down watcher...")

			// Stop the health checker
			healthChecker.Stop()

			// Shutdown the HTTP server
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := srv.Shutdown(shutdownCtx); err != nil {
				sugar.Errorf("Server forced to shutdown: %v", err)
			}
			sugar.Info("Watcher stopped")
		}()

		log.Fatal(srv.ListenAndServe())
	}()
	<-c

	sharedmain.MainWithConfig(ctx, "pac-watcher", cfg, reconciler.NewController())
}

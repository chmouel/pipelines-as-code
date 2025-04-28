package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/mcp"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"go.uber.org/zap"
)

func main() {
	// Parse command-line flags
	port := flag.String("port", mcp.DefaultPort, "Port for MCP API server")
	listenAddr := flag.String("listen-address", mcp.DefaultListenAddress, "Address to listen on")
	kubeconfig := flag.String("kubeconfig", "", "Path to kubeconfig file")
	flag.Parse()

	// Set up a context that is canceled on SIGTERM or SIGINT
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set up signal handling
	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-signalCh
		log.Println("Received shutdown signal")
		cancel()
	}()

	// Set up logger
	logger, err := zap.NewProduction()
	if err != nil {
		log.Fatalf("Failed to create logger: %v", err)
	}
	sugar := logger.Sugar()
	defer logger.Sync()

	// Initialize Kubernetes clients
	run := &params.Run{
		Info: info.Info{
			Kube: &info.Kube{},
		},
	}

	if *kubeconfig != "" {
		run.Info.Kube.ConfigPath = *kubeconfig
	}

	// Initialize clients
	if err := run.Clients.NewClients(ctx, &run.Info); err != nil {
		sugar.Fatalf("Failed to initialize Kubernetes clients: %v", err)
	}

	// Create and start MCP server
	server := mcp.NewServer(run, sugar)
	server.SetPort(*port)
	server.SetListenAddress(*listenAddr)

	// Start the server
	sugar.Infof("Starting MCP API server on %s:%s", *listenAddr, *port)
	if err := server.Start(ctx); err != nil {
		sugar.Fatalf("Failed to start server: %v", err)
	}

	// Wait for server to fully shutdown
	time.Sleep(1 * time.Second)
	sugar.Info("MCP API server shutdown complete")
}

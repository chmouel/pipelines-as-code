// Command pipelines-as-code-credentials is a proof-of-concept service that
// keeps GitHub build credentials fresh while builds wait in a queue.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/credentials/service"
	tektonclient "github.com/tektoncd/pipeline/pkg/client/clientset/versioned"
	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func envString(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func envInt(name string) int64 {
	v, _ := strconv.ParseInt(os.Getenv(name), 10, 64)
	return v
}

func envDuration(name string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(os.Getenv(name)); err == nil {
		return d
	}
	return def
}

func main() {
	cfg := &service.Config{}
	var kubeconfig string
	flag.Int64Var(&cfg.AppID, "app-id", envInt("GITHUB_APP_ID"), "GitHub App ID (read from the key secret when unset)")
	flag.Int64Var(&cfg.InstallationID, "installation-id", envInt("GITHUB_INSTALLATION_ID"), "GitHub App installation ID")
	flag.Int64Var(&cfg.RepositoryID, "repository-id", envInt("GITHUB_REPOSITORY_ID"), "permitted repository ID")
	flag.StringVar(&cfg.RepositoryURL, "repository-url", os.Getenv("GITHUB_REPOSITORY_URL"), "permitted repository URL")
	flag.StringVar(&cfg.APIURL, "api-url", os.Getenv("GITHUB_API_URL"), "GitHub API URL (defaults to https://api.github.com/)")
	flag.StringVar(&cfg.ControlNS, "control-namespace", envString("CONTROL_NAMESPACE", "pipelines-as-code"), "namespace holding credential requests")
	flag.StringVar(&cfg.OutputNamespace, "output-namespace", os.Getenv("OUTPUT_NAMESPACE"), "only namespace where build secrets are written")
	flag.StringVar(&cfg.KeySecretNamespace, "key-secret-namespace", os.Getenv("KEY_SECRET_NAMESPACE"), "namespace of the App key secret (defaults to the control namespace)")
	flag.StringVar(&cfg.KeySecretName, "key-secret-name", envString("KEY_SECRET_NAME", "pipelines-as-code-secret"), "secret holding the App private key")
	flag.StringVar(&cfg.KeySecretKey, "key-secret-key", envString("KEY_SECRET_KEY", "github-private-key"), "key of the private key in the secret")
	flag.StringVar(&cfg.AppIDSecretKey, "app-id-secret-key", envString("APP_ID_SECRET_KEY", "github-application-id"), "key of the App ID in the secret")
	flag.DurationVar(&cfg.RenewBefore, "renew-before", envDuration("RENEW_BEFORE", 15*time.Minute), "renew this long before expiry")
	flag.DurationVar(&cfg.UnboundWarnAfter, "unbound-warn-after", envDuration("UNBOUND_WARN_AFTER", 10*time.Minute), "warn about requests unbound for longer than this")
	flag.DurationVar(&cfg.Resync, "resync", envDuration("RESYNC", 5*time.Minute), "informer resync period")
	flag.StringVar(&cfg.HealthAddr, "health-addr", envString("HEALTH_ADDR", ":8080"), "address for /healthz and /readyz")
	flag.StringVar(&kubeconfig, "kubeconfig", os.Getenv("KUBECONFIG"), "kubeconfig for out-of-cluster use")
	flag.Parse()

	zl, err := zap.NewProduction()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	logger := zl.Sugar()
	if err := run(cfg, kubeconfig, logger); err != nil {
		logger.Fatal(err)
	}
}

func run(cfg *service.Config, kubeconfig string, logger *zap.SugaredLogger) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	var restCfg *rest.Config
	var err error
	if kubeconfig != "" {
		restCfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		restCfg, err = rest.InClusterConfig()
	}
	if err != nil {
		return err
	}
	kube, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return err
	}
	tekton, err := tektonclient.NewForConfig(restCfg)
	if err != nil {
		return err
	}

	ctrl, err := service.NewController(cfg, kube, tekton, service.NewGitHubIssuer(cfg, kube), logger)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	srv := &http.Server{Addr: cfg.HealthAddr, Handler: ctrl.HealthHandler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Errorw("health server failed", "error", err)
			cancel()
		}
	}()
	defer func() { _ = srv.Shutdown(context.Background()) }()

	// One worker keeps renewals for a request strictly sequential.
	return ctrl.Run(ctx, 1)
}

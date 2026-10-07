// Command nodeexec is the node-local Deploy Compose executor.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/candacelabs/csf/app/nodeexec/internal/config"
	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/pkg/telemetry"
	deployv1 "github.com/candacelabs/csf/proto/candace/deploy/v1"
	telemetryv1 "github.com/candacelabs/csf/proto/candace/telemetry/v1"
	"github.com/candacelabs/csf/services/nodeexec"
	"github.com/candacelabs/csf/web/nodeexec/httpapi"
)

var version = "dev"

func main() {
	os.Exit(run())
}

func run() int {
	logger, err := telemetry.NewJSONLLogger(os.Stdout, "nodeexec", "node-executor")
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "constructing telemetry logger: %v\n", err)
		return 1
	}
	logContext := context.Background()
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		logEvent(logger, logContext, telemetryv1.Severity_SEVERITY_ERROR, "nodeexec.configuration.invalid", "invalid configuration", map[string]string{"error": err.Error()})
		return 1
	}
	launcher, err := proc.NewHostLauncher()
	if err != nil {
		logEvent(logger, logContext, telemetryv1.Severity_SEVERITY_ERROR, "nodeexec.runner.failed", "could not construct process launcher", map[string]string{"error": err.Error()})
		return 1
	}
	runner, err := nodeexec.NewDockerComposeRunnerWithSourceSync(
		launcher, cfg.DockerBin, cfg.Workspace, cfg.RevisionRoot,
		&deployv1.RevisionLimits{MaxEntries: cfg.RevisionMaxEntries, MaxBytes: cfg.RevisionMaxBytes},
		sourceSyncConfig(cfg),
		logger,
		cfg.DryRun,
	)
	if err != nil {
		logEvent(logger, logContext, telemetryv1.Severity_SEVERITY_ERROR, "nodeexec.runner.failed", "could not construct Compose runner", map[string]string{"error": err.Error()})
		return 1
	}
	reconciler, err := nodeexec.NewReconciler(nodeexec.NewFileStore(cfg.StateFile), runner)
	if err != nil {
		logEvent(logger, logContext, telemetryv1.Severity_SEVERITY_ERROR, "nodeexec.state.restore_failed", "could not restore agent state", map[string]string{"error": err.Error()})
		return 1
	}

	server := &http.Server{
		Addr:              cfg.Bind,
		Handler:           httpapi.New(cfg.NodeID, cfg.Token, reconciler),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		// Compose pulls and starts may legitimately take minutes. The request
		// context still cancels execution when the caller disconnects.
		WriteTimeout:   0,
		IdleTimeout:    60 * time.Second,
		MaxHeaderBytes: 16 << 10,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serverErr := make(chan error, 1)
	go func() {
		logEvent(logger, logContext, telemetryv1.Severity_SEVERITY_INFO, "nodeexec.started", "deploy agent started", map[string]string{
			"version":   version,
			"node_id":   cfg.NodeID,
			"bind":      cfg.Bind,
			"workspace": runner.Workspace(),
			"dry_run":   fmt.Sprintf("%t", cfg.DryRun),
		})
		serverErr <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logEvent(logger, logContext, telemetryv1.Severity_SEVERITY_ERROR, "nodeexec.shutdown.failed", "HTTP server shutdown failed", map[string]string{"error": err.Error()})
			return 1
		}
		err = <-serverErr
	case err = <-serverErr:
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		logEvent(logger, logContext, telemetryv1.Severity_SEVERITY_ERROR, "nodeexec.server.failed", "HTTP server stopped unexpectedly", map[string]string{"error": err.Error()})
		return 1
	}
	logEvent(logger, logContext, telemetryv1.Severity_SEVERITY_INFO, "nodeexec.stopped", "deploy agent stopped", nil)
	return 0
}

func sourceSyncConfig(cfg config.Config) *deployv1.SourceSync {
	if cfg.SourceRemote == "" {
		return nil
	}
	return &deployv1.SourceSync{
		Remote: cfg.SourceRemote, Repository: cfg.SourceRepository,
		FetchTimeoutNanoseconds: int64(cfg.SourceFetchTimeout),
	}
}

func logEvent(logger *telemetry.JSONLLogger, ctx context.Context, severity telemetryv1.Severity, event, message string, attributes map[string]string) {
	if err := logger.Log(ctx, severity, event, message, attributes); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "writing telemetry event %q: %v\n", event, err)
	}
}

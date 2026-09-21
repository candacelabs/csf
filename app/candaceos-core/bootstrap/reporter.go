package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"

	"github.com/candacelabs/csf/pkg/redact"
	"github.com/candacelabs/csf/pkg/telemetry"
	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	telemetryv1 "github.com/candacelabs/csf/proto/candace/telemetry/v1"
	"github.com/candacelabs/csf/services/candaceos/store"
)

type startupComponent string

const (
	startupComponentConfig     startupComponent = "configuration"
	startupComponentDatabase   startupComponent = "database"
	startupComponentFleet      startupComponent = "fleet"
	startupComponentAgent      startupComponent = "node-agent"
	startupComponentReconciler startupComponent = "reconciler"
	startupComponentHarness    startupComponent = "harness"
	startupComponentRuntime    startupComponent = "runtime"
	startupComponentHTTP       startupComponent = "http"
	startupComponentExtension  startupComponent = "extension"
)

type coreEvent string

const (
	eventStartupFailed      coreEvent = "startup.failed"
	eventDatabaseRecovered  coreEvent = "database.recovered"
	eventStarted            coreEvent = "started"
	eventShutdownFailed     coreEvent = "shutdown.failed"
	eventHTTPFailed         coreEvent = "http.failed"
	eventHarnessCloseFailed coreEvent = "harness.close_failed"
	eventStopped            coreEvent = "stopped"
)

func (event coreEvent) name() string { return "core." + string(event) }

type reporter struct {
	logger     *telemetry.JSONLLogger
	includePII bool
	redactor   redact.Redactor
}

func newReporter(includePII bool) (*reporter, error) {
	logger, err := telemetry.NewJSONLLogger(os.Stdout, "candaceos-core", "runtime")
	if err != nil {
		return nil, err
	}
	return &reporter{logger: logger, includePII: includePII}, nil
}

func (reporter *reporter) withConfig(cfg *candaceosv1.CoreConfig) *reporter {
	configured := *reporter
	if !configured.includePII {
		configured.redactor = redact.NewRedactor(configuredSecrets(cfg)...)
	}
	return &configured
}

func configuredSecrets(cfg *candaceosv1.CoreConfig) []string {
	if cfg == nil {
		return nil
	}
	secrets := []string{
		cfg.GetDatabaseUrl(),
		cfg.GetAgentToken(),
		cfg.GetCopilotConnectionToken(),
		cfg.GetGithubToken(),
	}
	databaseURL, err := url.Parse(cfg.GetDatabaseUrl())
	if err != nil {
		return secrets
	}
	if databaseURL.User != nil {
		if password, present := databaseURL.User.Password(); present {
			secrets = append(secrets, password)
		}
	}
	if password := databaseURL.Query().Get("password"); password != "" {
		secrets = append(secrets, password)
	}
	return secrets
}

func (reporter *reporter) startupFailure(
	ctx context.Context,
	component startupComponent,
	cause error,
) error {
	return reporter.attributedStartupFailure(ctx, component, "", cause)
}

// extensionStartupFailure attributes a registered component's failure to the
// extension boundary and names that component in its own attribute.
func (reporter *reporter) extensionStartupFailure(
	ctx context.Context,
	extension string,
	cause error,
) error {
	return reporter.attributedStartupFailure(ctx, startupComponentExtension, extension, cause)
}

func (reporter *reporter) attributedStartupFailure(
	ctx context.Context,
	component startupComponent,
	extension string,
	cause error,
) error {
	diagnostic := reporter.diagnostic(cause)
	attributes := map[string]string{
		"component": string(component),
		"error":     diagnostic.Error(),
	}
	if extension != "" {
		attributes["extension"] = extension
	}
	logErr := reporter.event(
		ctx,
		telemetryv1.Severity_SEVERITY_FATAL,
		eventStartupFailed,
		"CandaceOS Core startup failed",
		attributes,
	)
	return errors.Join(diagnostic, logErr)
}

// componentEvent writes one registered component's INFO record under the
// component event namespace, redacted by Core's configured policy.
func (reporter *reporter) componentEvent(
	ctx context.Context,
	name string,
	event string,
	message string,
) error {
	qualified := "component." + name + "." + event
	err := reporter.logger.Log(
		ctx,
		telemetryv1.Severity_SEVERITY_INFO,
		qualified,
		reporter.redact(message),
		nil,
	)
	if err != nil {
		return fmt.Errorf("writing %s event: %w", qualified, err)
	}
	return nil
}

func (reporter *reporter) redact(message string) string {
	if reporter.includePII {
		return message
	}
	return reporter.redactor.String(message)
}

func (reporter *reporter) failure(
	ctx context.Context,
	severity telemetryv1.Severity,
	event coreEvent,
	message string,
	cause error,
) error {
	diagnostic := reporter.diagnostic(cause)
	logErr := reporter.event(ctx, severity, event, message, map[string]string{
		"error": diagnostic.Error(),
	})
	return errors.Join(diagnostic, logErr)
}

func (reporter *reporter) event(
	ctx context.Context,
	severity telemetryv1.Severity,
	event coreEvent,
	message string,
	attributes map[string]string,
) error {
	if err := reporter.logger.Log(ctx, severity, event.name(), message, attributes); err != nil {
		return fmt.Errorf("writing %s event: %w", event.name(), err)
	}
	return nil
}

func (reporter *reporter) recovered(ctx context.Context, recovery store.StartupRecovery) error {
	if recovery.InterruptedRuns == 0 && recovery.InterruptedDeploymentRuns == 0 &&
		recovery.ExpiredApprovals == 0 {
		return nil
	}
	return reporter.event(
		ctx,
		telemetryv1.Severity_SEVERITY_WARN,
		eventDatabaseRecovered,
		"Recovered interrupted operator work",
		map[string]string{
			"interrupted_runs":            strconv.Itoa(recovery.InterruptedRuns),
			"interrupted_deployment_runs": strconv.Itoa(recovery.InterruptedDeploymentRuns),
			"expired_approvals":           strconv.Itoa(recovery.ExpiredApprovals),
			"receipts":                    strconv.Itoa(len(recovery.ReceiptIDs)),
		},
	)
}

func (reporter *reporter) started(
	ctx context.Context,
	bind string,
	harnessBackend string,
	harnessImplementation string,
	version string,
	extensions string,
) error {
	attributes := map[string]string{
		"bind":                   bind,
		"harness_backend":        harnessBackend,
		"harness_implementation": harnessImplementation,
		"version":                version,
	}
	if extensions != "" {
		attributes["extensions"] = extensions
	}
	return reporter.event(
		ctx,
		telemetryv1.Severity_SEVERITY_INFO,
		eventStarted,
		"CandaceOS Core started",
		attributes,
	)
}

func (reporter *reporter) shutdownFailure(ctx context.Context, cause error) error {
	return reporter.failure(
		ctx, telemetryv1.Severity_SEVERITY_ERROR, eventShutdownFailed,
		"Shutting down HTTP server", cause,
	)
}

func (reporter *reporter) httpFailure(ctx context.Context, cause error) error {
	return reporter.failure(
		ctx, telemetryv1.Severity_SEVERITY_ERROR, eventHTTPFailed,
		"HTTP server stopped", cause,
	)
}

func (reporter *reporter) harnessCloseFailure(ctx context.Context, cause error) error {
	return reporter.failure(
		ctx, telemetryv1.Severity_SEVERITY_ERROR, eventHarnessCloseFailed,
		"Closing agent harness", cause,
	)
}

// extensionStopFailure attributes a composed component's teardown failure to
// the shutdown event, naming the component so it never reads as a Core step.
func (reporter *reporter) extensionStopFailure(
	ctx context.Context,
	extension string,
	cause error,
) error {
	diagnostic := reporter.diagnostic(cause)
	logErr := reporter.event(
		ctx, telemetryv1.Severity_SEVERITY_ERROR, eventShutdownFailed,
		"Stopping composed component",
		map[string]string{"extension": extension, "error": diagnostic.Error()},
	)
	return errors.Join(diagnostic, logErr)
}

func (reporter *reporter) stopped(ctx context.Context) error {
	return reporter.event(
		ctx,
		telemetryv1.Severity_SEVERITY_INFO,
		eventStopped,
		"CandaceOS Core stopped",
		nil,
	)
}

func (reporter *reporter) diagnostic(cause error) error {
	if cause == nil || reporter.includePII {
		return cause
	}
	message := reporter.redactor.String(cause.Error())
	if message == cause.Error() {
		return cause
	}
	return &diagnosticError{message: message, cause: cause}
}

type diagnosticError struct {
	message string
	cause   error
}

func (err *diagnosticError) Error() string { return err.message }

func (err *diagnosticError) Unwrap() error { return err.cause }

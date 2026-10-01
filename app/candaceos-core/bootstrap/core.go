package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/candacelabs/csf/pkg/telemetry"
	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	"github.com/candacelabs/csf/services/candaceos/component"
	"github.com/candacelabs/csf/services/candaceos/config"
	"github.com/candacelabs/csf/services/candaceos/control"
	"github.com/candacelabs/csf/services/candaceos/fleet"
	"github.com/candacelabs/csf/services/candaceos/operator"
	"github.com/candacelabs/csf/services/candaceos/store"
)

// Core is one fully assembled CandaceOS control plane. Run consumes the
// assembled resources, blocks for their complete lifecycle, and may be called
// exactly once.
type Core struct {
	config     *candaceosv1.CoreConfig
	reporter   *reporter
	version    string
	store      *store.Store
	fleet      *fleet.Client
	controller *operator.Controller
	runtime    *control.Runtime
	server     *http.Server
	extensions []*component.Definition

	harnessCloseErr       error
	extensionStopFailures []extensionStopFailure

	stateMu  sync.Mutex
	state    coreState
	closeErr error
}

// extensionStopFailure records one composed component's teardown failure so
// Run can attribute it to the component rather than to a Core step.
type extensionStopFailure struct {
	name  string
	cause error
}

type coreState uint8

const (
	coreReady coreState = iota
	coreRunning
	coreClosed
)

// Run starts the assembled workers and HTTP listener, then releases every
// owned dependency after cancellation or failure. A Core cannot be restarted.
func (core *Core) Run(ctx context.Context) error {
	if err := core.beginRun(); err != nil {
		return err
	}
	if ctx == nil {
		return errors.Join(
			fmt.Errorf("running CandaceOS Core: context is required"),
			core.finish(),
		)
	}
	lifecycle, cancel := context.WithCancel(ctx)
	defer cancel()

	for _, extension := range core.extensions {
		if err := extension.Start(lifecycle); err != nil {
			return errors.Join(
				core.reporter.extensionStartupFailure(lifecycle, extension.Name(), err),
				core.finish(),
			)
		}
	}

	if err := core.controller.Start(lifecycle); err != nil {
		// Controller.Start closes a harness that fails to start or activate.
		core.controller = nil
		return errors.Join(
			core.reporter.startupFailure(lifecycle, startupComponentHarness, err),
			core.finish(),
		)
	}

	listener, err := (&net.ListenConfig{}).Listen(lifecycle, "tcp", core.server.Addr)
	if err != nil {
		return core.failStartup(lifecycle, startupComponentHTTP, err)
	}
	core.server.BaseContext = func(listener net.Listener) context.Context { return lifecycle }

	fleetDone := make(chan struct{})
	go func() {
		defer close(fleetDone)
		core.fleet.Run(lifecycle, config.FleetPollInterval(core.config), func(snapshot fleet.Snapshot) {
			core.runtime.RecordFleetContext(lifecycle, snapshot)
		})
	}()
	serverDone := make(chan error, 1)
	go func() { serverDone <- core.server.Serve(listener) }()

	runErr := core.reporter.started(
		lifecycle,
		core.config.GetBind(),
		core.controller.HarnessBackendName(),
		core.controller.HarnessIdentity().GetImplementation(),
		core.version,
		core.extensionOrder(),
	)
	var serveErr error
	if runErr == nil {
		select {
		case <-lifecycle.Done():
		case serveErr = <-serverDone:
		}
	}

	cancel()
	if err := core.server.Shutdown(context.Background()); err != nil {
		runErr = errors.Join(runErr, core.reporter.shutdownFailure(context.Background(), err))
	}
	if serveErr == nil {
		serveErr = <-serverDone
	}
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		runErr = errors.Join(runErr, core.reporter.httpFailure(context.Background(), serveErr))
	}
	<-fleetDone
	if err := core.finish(); err != nil {
		runErr = errors.Join(runErr, core.reportReleaseFailure(context.Background(), err))
	}
	if runErr != nil {
		return runErr
	}
	return core.reporter.stopped(context.Background())
}

func (core *Core) beginRun() error {
	core.stateMu.Lock()
	defer core.stateMu.Unlock()
	switch core.state {
	case coreReady:
		core.state = coreRunning
		return nil
	case coreRunning:
		return fmt.Errorf("CandaceOS Core is already running")
	default:
		return fmt.Errorf("CandaceOS Core is closed")
	}
}

func (core *Core) failStartup(
	ctx context.Context,
	component startupComponent,
	cause error,
) error {
	return errors.Join(
		core.reporter.startupFailure(ctx, component, cause),
		core.finish(),
	)
}

func (core *Core) failExtension(ctx context.Context, extension string, cause error) error {
	return errors.Join(
		core.reporter.extensionStartupFailure(ctx, extension, cause),
		core.finish(),
	)
}

func (core *Core) extensionOrder() string {
	if len(core.extensions) == 0 {
		return ""
	}
	names := make([]string, 0, len(core.extensions))
	for _, extension := range core.extensions {
		names = append(names, extension.Name())
	}
	return boundedJoin(names, telemetry.MaxAttributeValueBytes)
}

// boundedJoin joins names within one telemetry attribute value, replacing the
// names that do not fit with a ",+<count>" tail so a large registered set can
// never invalidate the started record.
func boundedJoin(names []string, bound int) string {
	joined := strings.Join(names, ",")
	if len(joined) <= bound {
		return joined
	}
	for kept := len(names) - 1; kept > 0; kept-- {
		tail := ",+" + strconv.Itoa(len(names)-kept)
		joined = strings.Join(names[:kept], ",")
		if len(joined)+len(tail) <= bound {
			return joined + tail
		}
	}
	return "+" + strconv.Itoa(len(names))
}

// reportReleaseFailure attributes teardown failures to their owners: a harness
// close failure keeps its Core event, while a composed component's stop
// failure is reported against the shutdown event with the component named in
// its own attribute, so it never reads as a Core step.
func (core *Core) reportReleaseFailure(ctx context.Context, cause error) error {
	if len(core.extensionStopFailures) == 0 {
		return core.reporter.harnessCloseFailure(ctx, cause)
	}
	var err error
	if core.harnessCloseErr != nil {
		err = core.reporter.harnessCloseFailure(ctx, core.harnessCloseErr)
	}
	for _, failure := range core.extensionStopFailures {
		err = errors.Join(err, core.reporter.extensionStopFailure(ctx, failure.name, failure.cause))
	}
	return err
}

// Close releases an assembled Core that will not be run. Run owns resources
// while active, so Close reports an error until Run reaches its terminal state.
// At either terminal boundary, repeated Close calls return the same result.
func (core *Core) Close() error {
	return core.close(false)
}

func (core *Core) finish() error {
	return core.close(true)
}

func (core *Core) close(allowRunning bool) error {
	core.stateMu.Lock()
	defer core.stateMu.Unlock()
	if core.state == coreClosed {
		return core.closeErr
	}
	if core.state == coreRunning && !allowRunning {
		return fmt.Errorf("CandaceOS Core Run owns the active lifecycle")
	}
	core.closeErr = core.reporter.diagnostic(core.releaseResources())
	core.state = coreClosed
	return core.closeErr
}

func (core *Core) releaseResources() error {
	var err error
	if core.controller != nil {
		if closeErr := core.controller.Close(); closeErr != nil {
			core.harnessCloseErr = closeErr
			err = closeErr
		}
		core.controller = nil
	}
	for index := len(core.extensions) - 1; index >= 0; index-- {
		stopErr := core.extensions[index].Stop(context.Background())
		if stopErr == nil {
			continue
		}
		name := core.extensions[index].Name()
		core.extensionStopFailures = append(
			core.extensionStopFailures,
			extensionStopFailure{name: name, cause: stopErr},
		)
		err = errors.Join(err, fmt.Errorf("stopping composed component %q: %w", name, stopErr))
	}
	core.extensions = nil
	if core.store != nil {
		core.store.Close()
		core.store = nil
	}
	return err
}

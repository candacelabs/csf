// Package bootstrap assembles and runs CandaceOS Core.
//
// Core owns its infrastructure and lifecycle. Embedding binaries customize
// only the deliberately public options supplied to AssembleCore or Run.
package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	"github.com/candacelabs/csf/services/candaceos/agentclient"
	"github.com/candacelabs/csf/services/candaceos/component"
	"github.com/candacelabs/csf/services/candaceos/config"
	"github.com/candacelabs/csf/services/candaceos/control"
	"github.com/candacelabs/csf/services/candaceos/fleet"
	"github.com/candacelabs/csf/services/candaceos/harness"
	"github.com/candacelabs/csf/services/candaceos/httpapi"
	"github.com/candacelabs/csf/services/candaceos/httpserver"
	"github.com/candacelabs/csf/services/candaceos/operator"
	"github.com/candacelabs/csf/services/candaceos/reconcile"
	"github.com/candacelabs/csf/services/candaceos/store"
	"github.com/candacelabs/csf/services/candaceos/webui"
)

// Option changes one public Core assembly policy.
type Option func(settings *assemblyOptions) error

type assemblyOptions struct {
	harnessFactory harness.IFactory
	httpServices   []IHTTPService
	includePII     bool
	components     []*component.Definition
	brand          webui.Brand
	uiOverlay      fs.FS
	navItems       []webui.NavItem
}

// IHTTPService is one optional component that mounts routes on Core's single
// caller-owned Gin engine.
type IHTTPService interface {
	Register(router gin.IRouter)
}

// WithHTTPService adds a caller-supplied HTTP component beside Core's default
// API and Web UI on the same Gin engine.
func WithHTTPService(service IHTTPService) Option {
	return func(settings *assemblyOptions) error {
		if service == nil {
			return fmt.Errorf("CandaceOS HTTP service is required")
		}
		settings.httpServices = append(settings.httpServices, service)
		return nil
	}
}

// WithBrand replaces the stock CandaceOS identity: the product name, the agent
// name, the wordmark, and the web UI's design tokens. Unset Brand fields keep
// their stock values.
//
// The brand is applied in one place and read in two: Core stamps the two
// brand-bearing names into every snapshot it produces, and the web UI renders
// the wordmark and serves the palette. Both receive the same resolved value,
// so the pages and the browser client cannot disagree about who they are.
func WithBrand(brand webui.Brand) Option {
	return func(settings *assemblyOptions) error {
		if err := brand.Validate(); err != nil {
			return err
		}
		settings.brand = brand.Resolved()
		return nil
	}
}

// WithUIOverlay resolves the operator UI's templates and assets against a
// caller-supplied filesystem before the embedded one. The overlay carries a
// templates/ subtree whose files redefine the shipped named blocks and an
// assets/ subtree whose files replace the embedded asset of the same name;
// anything the overlay does not name keeps shipping from Core. The supported
// block names and the data each receives are documented on the webui package.
//
// Only one overlay may be supplied, and a nil one is rejected here rather than
// silently ignored. A template that does not parse fails Core's HTTP startup
// component, since that is where the pages are built.
func WithUIOverlay(overlay fs.FS) Option {
	return func(settings *assemblyOptions) error {
		if overlay == nil {
			return fmt.Errorf("CandaceOS UI overlay is required")
		}
		if settings.uiOverlay != nil {
			return fmt.Errorf("CandaceOS accepts only one UI overlay")
		}
		settings.uiOverlay = overlay
		return nil
	}
}

// WithNavItem appends one entry to the operator sidebar, after Core's own Home,
// Apps, Fleet, and Activity entries. It is repeatable, entries render in
// registration order, and an entry that cannot be rendered as one labeled link
// fails assembly before any infrastructure is opened.
//
// A registered entry is a plain link to somewhere the embedding product already
// serves — a page it mounted with WithHTTPService, for instance. Core's own
// routes, including the /claws/... paths, are unchanged by one.
func WithNavItem(item webui.NavItem) Option {
	return func(settings *assemblyOptions) error {
		if err := item.Validate(); err != nil {
			return err
		}
		settings.navItems = append(settings.navItems, item)
		return nil
	}
}

// WithHarnessFactory replaces the configuration-selected built-in harness.
// Core continues to own persistence, fleet observation, reconciliation, and
// the HTTP surface around the supplied implementation.
func WithHarnessFactory(factory harness.IFactory) Option {
	return func(settings *assemblyOptions) error {
		if factory == nil {
			return fmt.Errorf("CandaceOS harness factory is required")
		}
		settings.harnessFactory = factory
		return nil
	}
}

// WithPII permits raw configuration-derived values in returned errors and
// structured diagnostics. Without this option, Core redacts its known secrets.
func WithPII() Option {
	return func(settings *assemblyOptions) error {
		settings.includePII = true
		return nil
	}
}

// Run assembles Core, owns SIGINT and SIGTERM, and blocks until shutdown.
// Returned errors retain their causes so the embedding command can choose how
// process failures are rendered.
func Run(version string, functionalOptions ...Option) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	core, err := AssembleCore(ctx, version, functionalOptions...)
	if err != nil {
		return err
	}
	return core.Run(ctx)
}

// AssembleCore builds the service in dependency order without starting any
// worker or listener. The returned Core owns every assembled dependency; its
// Run method releases them on every exit path and may be called exactly once.
func AssembleCore(
	ctx context.Context,
	version string,
	functionalOptions ...Option,
) (*Core, error) {
	if ctx == nil {
		return nil, fmt.Errorf("assembling CandaceOS Core: context is required")
	}
	settings, err := applyOptions(functionalOptions)
	if err != nil {
		return nil, err
	}
	reporter, err := newReporter(settings.includePII)
	if err != nil {
		return nil, fmt.Errorf("constructing CandaceOS reporter: %w", err)
	}
	assembly := &coreAssembly{version: version, settings: settings, reporter: reporter}
	ordered, err := assembly.resolve()
	if err != nil {
		return nil, err
	}
	for _, definition := range ordered {
		if err := assembly.assemble(ctx, definition); err != nil {
			return nil, err
		}
	}
	return assembly.core, nil
}

// coreAssembly carries the bring-up state shared by Core's built-in component
// definitions. Each step below is one hand-ordered block of Core's frozen
// startup sequence together with its exact failure attribution.
type coreAssembly struct {
	version    string
	settings   assemblyOptions
	reporter   *reporter
	core       *Core
	agents     *agentclient.Client
	labels     map[string]map[string]string
	reconciler *reconcile.Service
}

func (assembly *coreAssembly) resolve() ([]*component.Definition, error) {
	definitions, err := assembly.definitions()
	if err != nil {
		return nil, fmt.Errorf("declaring CandaceOS components: %w", err)
	}
	ordered, err := component.Order(definitions...)
	if err != nil {
		return nil, fmt.Errorf("ordering CandaceOS components: %w", err)
	}
	return ordered, nil
}

// definitions declares Core's built-in bring-up graph. Every edge points at an
// earlier declaration, so the resolver's registration-order tie-break reproduces
// this exact sequence. Registered components occupy the positions between the
// reconciler and the harness, which places them after every Core dependency a
// component may observe and before the agent harness is constructed.
func (assembly *coreAssembly) definitions() ([]*component.Definition, error) {
	configuration, err := component.New(
		"configuration",
		component.WithAssemble(assembly.loadConfiguration),
	)
	if err != nil {
		return nil, err
	}
	database, err := component.New(
		"database",
		component.WithAssemble(assembly.openStore),
		component.WithRequires(configuration),
	)
	if err != nil {
		return nil, err
	}
	recovery, err := component.New(
		"database-recovery",
		component.WithAssemble(assembly.recoverOperatorWork),
		component.WithRequires(database),
	)
	if err != nil {
		return nil, err
	}
	fleetClient, err := component.New(
		"fleet",
		component.WithAssemble(assembly.newFleetClient),
		component.WithRequires(configuration),
	)
	if err != nil {
		return nil, err
	}
	nodeAgent, err := component.New(
		"node-agent",
		component.WithAssemble(assembly.newNodeAgentClient),
		component.WithRequires(configuration),
	)
	if err != nil {
		return nil, err
	}
	reconciler, err := component.New(
		"reconciler",
		component.WithAssemble(assembly.newReconciler),
		component.WithRequires(configuration, database, fleetClient, nodeAgent),
	)
	if err != nil {
		return nil, err
	}
	agentHarness, err := component.New(
		"harness",
		component.WithAssemble(assembly.newController),
		component.WithRequires(configuration, recovery, fleetClient, reconciler),
	)
	if err != nil {
		return nil, err
	}
	runtime, err := component.New(
		"runtime",
		component.WithAssemble(assembly.newRuntime),
		component.WithRequires(database, fleetClient, nodeAgent, agentHarness),
	)
	if err != nil {
		return nil, err
	}
	httpAPI, err := component.New(
		"http",
		component.WithAssemble(assembly.buildHTTPAPI),
		component.WithRequires(runtime),
	)
	if err != nil {
		return nil, err
	}
	declared := []*component.Definition{
		configuration, database, recovery, fleetClient, nodeAgent, reconciler,
	}
	declared = append(declared, assembly.settings.components...)
	return append(declared, agentHarness, runtime, httpAPI), nil
}

func (assembly *coreAssembly) loadConfiguration(
	ctx context.Context,
	capabilities component.ICapabilities,
) error {
	cfg, err := loadConfig(assembly.settings.harnessFactory)
	if err != nil {
		return assembly.reporter.startupFailure(ctx, startupComponentConfig, err)
	}
	assembly.reporter = assembly.reporter.withConfig(cfg)
	assembly.core = &Core{config: cfg, reporter: assembly.reporter, version: assembly.version}
	return nil
}

func (assembly *coreAssembly) openStore(
	ctx context.Context,
	capabilities component.ICapabilities,
) error {
	core, cfg := assembly.core, assembly.core.config
	var err error
	core.store, err = store.OpenControlStore(ctx, cfg.GetDatabaseUrl())
	if err != nil {
		return core.failStartup(ctx, startupComponentDatabase, err)
	}
	return nil
}

func (assembly *coreAssembly) recoverOperatorWork(
	ctx context.Context,
	capabilities component.ICapabilities,
) error {
	core, reporter := assembly.core, assembly.reporter
	recovery, err := core.store.RecoverInterruptedOperatorWork(ctx, time.Now().UTC())
	if err != nil {
		return core.failStartup(ctx, startupComponentDatabase, err)
	}
	if err := reporter.recovered(ctx, recovery); err != nil {
		return errors.Join(reporter.diagnostic(err), core.finish())
	}
	return nil
}

func (assembly *coreAssembly) newFleetClient(
	ctx context.Context,
	capabilities component.ICapabilities,
) error {
	core, cfg := assembly.core, assembly.core.config
	var err error
	core.fleet, err = fleet.NewWardenClient(cfg.GetWardenUrl(), nil)
	if err != nil {
		return core.failStartup(ctx, startupComponentFleet, err)
	}
	return nil
}

func (assembly *coreAssembly) newNodeAgentClient(
	ctx context.Context,
	capabilities component.ICapabilities,
) error {
	core, cfg := assembly.core, assembly.core.config
	agents, err := agentclient.NewNodeAgentClient(
		cfg.GetAgentUrl(), cfg.GetAgentToken(), int(cfg.GetAgentPort()), nil,
	)
	if err != nil {
		return core.failStartup(ctx, startupComponentAgent, err)
	}
	assembly.agents = agents
	assembly.labels = config.NodeLabels(cfg)
	return nil
}

func (assembly *coreAssembly) newReconciler(
	ctx context.Context,
	capabilities component.ICapabilities,
) error {
	core, cfg := assembly.core, assembly.core.config
	reconciler, err := reconcile.NewService(
		cfg.GetWorkspace(), assembly.labels, core.fleet, assembly.agents, core.store,
	)
	if err != nil {
		return core.failStartup(ctx, startupComponentReconciler, err)
	}
	assembly.reconciler = reconciler
	return nil
}

func (assembly *coreAssembly) newController(
	ctx context.Context,
	capabilities component.ICapabilities,
) error {
	core, cfg := assembly.core, assembly.core.config
	var err error
	core.controller, err = operator.NewControllerWithHarness(
		cfg, core.fleet, assembly.reconciler, assembly.settings.harnessFactory,
	)
	if err != nil {
		return core.failStartup(ctx, startupComponentHarness, err)
	}
	return nil
}

func (assembly *coreAssembly) newRuntime(
	ctx context.Context,
	capabilities component.ICapabilities,
) error {
	core, cfg := assembly.core, assembly.core.config
	var err error
	core.runtime, err = control.NewRuntime(
		core.store, core.fleet, core.controller, assembly.labels,
		cfg.GetFleetPollInterval(), assembly.version,
		control.WithBrand(assembly.settings.brand),
	)
	if err != nil {
		return core.failStartup(ctx, startupComponentRuntime, err)
	}
	return nil
}

func (assembly *coreAssembly) buildHTTPAPI(
	ctx context.Context,
	capabilities component.ICapabilities,
) error {
	core, cfg := assembly.core, assembly.core.config
	router := httpserver.NewEngine()
	ui, err := webui.New(core.runtime, assembly.presentation()...)
	if err != nil {
		return core.failStartup(ctx, startupComponentHTTP, err)
	}
	api, err := httpapi.New(core.runtime)
	if err != nil {
		return core.failStartup(ctx, startupComponentHTTP, err)
	}
	ui.Register(router)
	api.Register(router)
	for _, service := range assembly.settings.httpServices {
		service.Register(router)
	}
	core.server = &http.Server{Addr: cfg.GetBind(), Handler: router}
	return nil
}

// presentation translates the assembled UI policy into the web UI's own
// options, in the order the package applies them: identity, then the overlay
// that may redefine what renders it, then the sidebar entries.
func (assembly *coreAssembly) presentation() []webui.Option {
	options := []webui.Option{webui.WithBrand(assembly.settings.brand)}
	if assembly.settings.uiOverlay != nil {
		options = append(options, webui.WithUIOverlay(assembly.settings.uiOverlay))
	}
	for _, item := range assembly.settings.navItems {
		options = append(options, webui.WithNavItem(item))
	}
	return options
}

func applyOptions(functionalOptions []Option) (assemblyOptions, error) {
	settings := assemblyOptions{brand: webui.DefaultBrand()}
	for index, option := range functionalOptions {
		if option == nil {
			return assemblyOptions{}, fmt.Errorf("CandaceOS option %d is nil", index+1)
		}
		if err := option(&settings); err != nil {
			return assemblyOptions{}, fmt.Errorf("applying CandaceOS option %d: %w", index+1, err)
		}
	}
	return settings, nil
}

func loadConfig(factory harness.IFactory) (*candaceosv1.CoreConfig, error) {
	if factory == nil {
		return config.Load()
	}
	return config.LoadForHarness(candaceosv1.HarnessBackend_HARNESS_BACKEND_EMBEDDED)
}

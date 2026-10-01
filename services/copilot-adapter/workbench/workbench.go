package workbench

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"sync/atomic"

	cronpostgres "github.com/candacelabs/csf/pkg/cron/postgres"
	"github.com/gin-gonic/gin"

	"github.com/candacelabs/csf/pkg/sqlmigrate"
	"github.com/candacelabs/csf/pkg/workcontinuity"
	copilotadapter "github.com/candacelabs/csf/services/copilot-adapter"
	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
	"github.com/candacelabs/csf/services/copilot-adapter/kanban"
	"github.com/candacelabs/csf/services/copilot-adapter/store"
	"github.com/candacelabs/csf/services/copilot-adapter/terminaladapter"
	"github.com/candacelabs/csf/services/copilot-adapter/worktreeadapter"
)

const (
	cronMigrationPrefix  = "candace-cron"
	defaultRepositoryID  = "workspace"
	defaultRepositoryRef = "HEAD"
)

// Workbench shares one adapter and its SQLC store between HTTP and inspection.
// The caller owns the database and CLI bridge and mounts Adapter.Register.
type Workbench struct {
	Adapter *copilotadapter.CopilotAdapter
	Store   *store.PostgresStore
	ready   atomic.Bool
	board   *kanban.Board
}
type settings struct {
	repository, worktrees, shell string
	bridge                       copilotadapter.ICopilotBridge
	logger                       *slog.Logger
	tasks                        *workcontinuity.Continuity
	origins                      []string
}
type Option func(config *settings)

func WithRepository(path string) Option { return func(config *settings) { config.repository = path } }
func WithWorktrees(path string) Option  { return func(config *settings) { config.worktrees = path } }
func WithShell(path string) Option      { return func(config *settings) { config.shell = path } }
func WithBridge(bridge copilotadapter.ICopilotBridge) Option {
	return func(config *settings) { config.bridge = bridge }
}
func WithLogger(logger *slog.Logger) Option { return func(config *settings) { config.logger = logger } }

func WithTaskContinuity(tasks *workcontinuity.Continuity) Option {
	return func(config *settings) { config.tasks = tasks }
}

// WithKanbanOrigins mounts the live board on the same router as the adapter.
// The host supplies its actual allowed browser origins; no listener is created.
func WithKanbanOrigins(origins ...string) Option {
	return func(config *settings) { config.origins = append([]string(nil), origins...) }
}

// NewWorkbench initializes durable stores and composes existing service libraries.
// It never starts a listener or a provider process, or restores sessions implicitly.
func NewWorkbench(ctx context.Context, db *sql.DB, options ...Option) (*Workbench, error) {
	settings := settings{shell: "/bin/bash", logger: slog.Default()}
	for _, option := range options {
		if option != nil {
			option(&settings)
		}
	}
	if db == nil || settings.bridge == nil || settings.repository == "" {
		return nil, fmt.Errorf("workbench requires database, bridge and repository")
	}
	if err := store.ApplyMigrations(ctx, db); err != nil {
		return nil, err
	}
	cronSchema := cronpostgres.EmbeddedMigrations()
	if err := sqlmigrate.ApplyPrefixed(ctx, db, cronSchema.Files, cronSchema.Directory, cronMigrationPrefix); err != nil {
		return nil, fmt.Errorf("copilot-adapter: apply cron migrations: %w", err)
	}

	repositoryRoot, err := CanonicalRepositoryRoot(ctx, settings.repository)
	if err != nil {
		return nil, err
	}
	if settings.worktrees == "" {
		settings.worktrees = filepath.Join(filepath.Dir(repositoryRoot), filepath.Base(repositoryRoot)+"-worktrees")
	}
	worktrees, err := worktreeadapter.NewWorktreeManager(worktreeadapter.Config{
		Repositories: []copilotadapter.Repository{{
			ID: defaultRepositoryID, DisplayName: filepath.Base(repositoryRoot), Root: repositoryRoot, DefaultRef: defaultRepositoryRef,
		}},
		WorktreeRoot: settings.worktrees,
	})
	if err != nil {
		return nil, err
	}
	adapterConfig := copilotadapter.DefaultAdapterConfig()
	terminals, err := terminaladapter.NewTerminalManager(terminaladapter.Config{
		Shell: settings.shell, ExitedHistoryLimit: int(adapterConfig.GetTerminalHistoryLimit()),
	})
	if err != nil {
		return nil, err
	}
	adapterStore, err := store.NewPostgresStore(db)
	if err != nil {
		return nil, err
	}
	scheduleStore, err := cronpostgres.NewStore(db)
	if err != nil {
		return nil, err
	}

	adapterOptions := []copilotadapter.Option{
		copilotadapter.WithBridge(settings.bridge),
		copilotadapter.WithStore(adapterStore),
		copilotadapter.WithWorktreeManager(worktrees),
		copilotadapter.WithTerminalManager(terminals),
		copilotadapter.WithScheduleStore(scheduleStore),
		copilotadapter.WithLogger(settings.logger),
		copilotadapter.WithConfig(adapterConfig),
	}
	if settings.tasks != nil {
		adapterOptions = append(adapterOptions, copilotadapter.WithTaskContinuity(settings.tasks))
	}
	adapter, err := copilotadapter.NewCopilotAdapter(adapterOptions...)
	if err != nil {
		return nil, err
	}
	workbench := &Workbench{Adapter: adapter, Store: adapterStore}
	if len(settings.origins) > 0 {
		workbench.board, err = kanban.NewBoard(adapter, settings.origins, settings.logger)
		if err != nil {
			return nil, err
		}
	}
	return workbench, nil
}

// Restore reconnects persisted sessions before enabling the Workbench HTTP API.
// The host may already serve sibling routes (including MCP) during restoration.
func (workbench *Workbench) Restore(ctx context.Context) error {
	if err := workbench.Adapter.RestoreSessions(ctx); err != nil {
		return err
	}
	workbench.ready.Store(true)
	return nil
}

// Register mounts the generated adapter API, returning 503 until Restore succeeds.
func (workbench *Workbench) Register(router gin.IRouter) error {
	group := router.Group("")
	group.Use(func(ctx *gin.Context) {
		if !workbench.ready.Load() {
			ctx.Header("Retry-After", "1")
			ctx.AbortWithStatusJSON(http.StatusServiceUnavailable, api.Error{Code: "workbench_restoring", Message: "Workbench sessions are restoring; retry shortly."})
			return
		}
		ctx.Next()
	})
	if err := workbench.Adapter.Register(group); err != nil {
		return err
	}
	if workbench.board != nil {
		workbench.board.Register(group)
	}
	return nil
}

// Close drains browser subscriptions before closing their adapter. The caller
// continues to own the database and provider bridge.
func (workbench *Workbench) Close(ctx context.Context) error {
	var boardErr error
	if workbench.board != nil {
		boardErr = workbench.board.Close(ctx)
	}
	return errors.Join(boardErr, workbench.Adapter.Close())
}

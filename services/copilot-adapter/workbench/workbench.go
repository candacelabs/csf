package workbench

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"sync/atomic"

	"github.com/candacelabs/csf/ipc/db/csfpg"
	"github.com/candacelabs/csf/ipc/proc"
	"github.com/gin-gonic/gin"

	copilotadapter "github.com/candacelabs/csf/services/copilot-adapter"
	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
	"github.com/candacelabs/csf/services/copilot-adapter/kanban"
	"github.com/candacelabs/csf/services/copilot-adapter/store"
	"github.com/candacelabs/csf/services/copilot-adapter/terminaladapter"
	"github.com/candacelabs/csf/services/copilot-adapter/worktreeadapter"
	cron "github.com/candacelabs/csf/services/cron"
	"github.com/candacelabs/csf/services/workcontinuity"
)

const (
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
	launcher                     proc.ILauncher
	bridge                       copilotadapter.ICopilotBridge
	logger                       *slog.Logger
	tasks                        *workcontinuity.Continuity
	origins                      []string
	schedules                    cron.IStore
}
type Option func(config *settings)

func WithRepository(path string) Option { return func(config *settings) { config.repository = path } }
func WithWorktrees(path string) Option  { return func(config *settings) { config.worktrees = path } }
func WithShell(path string) Option      { return func(config *settings) { config.shell = path } }
func WithBridge(bridge copilotadapter.ICopilotBridge) Option {
	return func(config *settings) { config.bridge = bridge }
}
func WithLogger(logger *slog.Logger) Option { return func(config *settings) { config.logger = logger } }

// WithLauncher grants the process capability that Git and terminal shells
// start through. It is required: the Workbench never starts a process itself.
func WithLauncher(launcher proc.ILauncher) Option {
	return func(config *settings) { config.launcher = launcher }
}

func WithTaskContinuity(tasks *workcontinuity.Continuity) Option {
	return func(config *settings) { config.tasks = tasks }
}

// WithScheduleStore grants the cron store chat schedules fire through: the
// binary builds it with cron.NewStore over a pool it applied CSF's schema
// to. It is required: the Workbench opens no database of its own.
func WithScheduleStore(schedules cron.IStore) Option {
	return func(config *settings) { config.schedules = schedules }
}

// WithKanbanOrigins mounts the live board on the same router as the adapter.
// The host supplies its actual allowed browser origins; no listener is created.
func WithKanbanOrigins(origins ...string) Option {
	return func(config *settings) { config.origins = append([]string(nil), origins...) }
}

// NewWorkbench composes existing service libraries over the database
// capability the binary granted, which already carries the adapter's schema
// (store.Migrations). It never opens a pool, starts a listener or a provider
// process, or restores sessions implicitly.
func NewWorkbench(ctx context.Context, db csfpg.IDB, options ...Option) (*Workbench, error) {
	settings := settings{shell: "/bin/bash", logger: slog.Default()}
	for _, option := range options {
		if option != nil {
			option(&settings)
		}
	}
	if db == nil || settings.bridge == nil || settings.repository == "" || settings.launcher == nil || settings.schedules == nil {
		return nil, fmt.Errorf("workbench requires database, bridge, repository, process launcher and schedule store")
	}

	repositoryRoot, err := CanonicalRepositoryRoot(ctx, settings.launcher, settings.repository)
	if err != nil {
		return nil, err
	}
	if settings.worktrees == "" {
		settings.worktrees = filepath.Join(filepath.Dir(repositoryRoot), filepath.Base(repositoryRoot)+"-worktrees")
	}
	worktrees, err := worktreeadapter.NewWorktreeManager(worktreeadapter.Config{
		Launcher: settings.launcher,
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
		Launcher: settings.launcher,
		Shell:    settings.shell, ExitedHistoryLimit: int(adapterConfig.GetTerminalHistoryLimit()),
	})
	if err != nil {
		return nil, err
	}
	adapterStore, err := store.NewPostgresStore(db)
	if err != nil {
		return nil, err
	}

	adapterOptions := []copilotadapter.Option{
		copilotadapter.WithBridge(settings.bridge),
		copilotadapter.WithStore(adapterStore),
		copilotadapter.WithWorktreeManager(worktrees),
		copilotadapter.WithTerminalManager(terminals),
		copilotadapter.WithScheduleStore(settings.schedules),
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

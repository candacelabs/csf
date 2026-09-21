package copilotadapter

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/candacelabs/csf/pkg/cron"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/candacelabs/csf/pkg/httpserver"
	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
	copilotv1 "github.com/candacelabs/csf/services/copilot-adapter/proto/candace/copilot/v1"
)

// CopilotAdapter is the adapter, mounted into a binary's existing Gin engine
// through Register. It owns no process concerns and never opens a listener:
// a service is an option slid into a pre-existing binary (CS-10).
type CopilotAdapter struct {
	bridge            ICopilotBridge
	store             IStore
	logger            *slog.Logger
	version           string
	config            *copilotv1.AdapterConfig
	sessions          *sessionRegistry
	worktrees         IWorktreeManager
	terminals         ITerminalManager
	scheduleStore     cron.IStore
	scheduleReload    chan scheduleReloadRequest
	scheduleControls  *mutationRegistry[uuid.UUID]
	scheduleRunMutex  sync.Mutex
	scheduleRunActive bool
	scheduleRunDone   chan struct{}
	mutations         *mutationRegistry[uuid.UUID]
	creations         *mutationRegistry[uuid.UUID]
	worktreeMutations *mutationRegistry[string]
	changes           *workspaceChanges
	tasks             *workspaceTasks
}

// NewCopilotAdapter validates the whole option set, then builds the adapter.
func NewCopilotAdapter(options ...Option) (*CopilotAdapter, error) {
	resolved, err := resolve(options)
	if err != nil {
		return nil, err
	}
	changes := &workspaceChanges{}
	return &CopilotAdapter{
		bridge:  resolved.bridge,
		store:   &notifyingStore{IStore: resolved.store, changes: changes},
		logger:  resolved.logger,
		version: resolved.version,
		config:  resolved.config,
		sessions: newSessionRegistry(
			time.Duration(resolved.config.GetDurableTransitionTimeoutMillis()) * time.Millisecond,
		),
		worktrees:         resolved.worktrees,
		terminals:         resolved.terminals,
		scheduleStore:     resolved.scheduleStore,
		scheduleReload:    make(chan scheduleReloadRequest, 1),
		scheduleControls:  newMutationRegistry[uuid.UUID](),
		mutations:         newMutationRegistry[uuid.UUID](),
		creations:         newMutationRegistry[uuid.UUID](),
		worktreeMutations: newMutationRegistry[string](),
		changes:           changes,
		tasks:             &workspaceTasks{source: resolved.taskContinuity, cache: make(map[string]taskObservation)},
	}, nil
}

// Register installs OpenAPI request validation and the generated strict
// handlers onto router. It is the service's only mount point.
func (adapter *CopilotAdapter) Register(router gin.IRouter) error {
	specification, err := api.GetSwagger()
	if err != nil {
		return fmt.Errorf("copilot-adapter: load the embedded contract: %w", err)
	}
	specification.Servers = nil
	validator, err := httpserver.ValidateOpenAPIRequests(specification, openapi3filter.Options{
		// The contract's bearer scheme is optional and unimplemented (the
		// loopback deployment carries no credential), so authentication is a
		// no-op here and a proxy adds one where it is wanted.
		AuthenticationFunc: openapi3filter.NoopAuthenticationFunc,
		// Validation failures answer with the contract's Error shape, not
		// the middleware's plain-text default.
	}, func(context *gin.Context, message string, statusCode int) {
		context.AbortWithStatusJSON(statusCode, errorBody(errorCodeInvalidRequest, message))
	})
	if err != nil {
		return fmt.Errorf("copilot-adapter: validate the embedded contract: %w", err)
	}
	// The middlewares are scoped to the generated routes: a service mounted
	// into a binary's engine never installs anything engine-wide, and the
	// validator would otherwise 404 every path the contract does not declare.
	api.RegisterHandlersWithOptions(router, api.NewStrictHandler(adapter, []api.StrictMiddlewareFunc{renderFailures}), api.GinServerOptions{
		Middlewares: []api.MiddlewareFunc{promoteLastEventIDQuery, api.MiddlewareFunc(validator)},
	})
	return nil
}

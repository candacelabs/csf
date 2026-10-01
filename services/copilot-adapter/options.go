package copilotadapter

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/candacelabs/csf/pkg/cron"

	"github.com/candacelabs/csf/pkg/workcontinuity"
	copilotv1 "github.com/candacelabs/csf/services/copilot-adapter/proto/candace/copilot/v1"
)

// Option configures a Service before New builds anything (CS-6, CS-10).
type Option func(configuration *configuration) error

type configuration struct {
	bridge         ICopilotBridge
	store          IStore
	worktrees      IWorktreeManager
	terminals      ITerminalManager
	scheduleStore  cron.IStore
	logger         *slog.Logger
	version        string
	config         *copilotv1.AdapterConfig
	taskContinuity *workcontinuity.Continuity
}

// WithTerminalManager supplies the process-owned PTY boundary.
func WithTerminalManager(terminals ITerminalManager) Option {
	return func(configuration *configuration) error {
		if terminals == nil {
			return errors.New("copilot-adapter: WithTerminalManager needs a manager")
		}
		configuration.terminals = terminals
		return nil
	}
}

// WithWorktreeManager supplies the configured repository/git boundary.
func WithWorktreeManager(worktrees IWorktreeManager) Option {
	return func(configuration *configuration) error {
		if worktrees == nil {
			return errors.New("copilot-adapter: WithWorktreeManager needs a manager")
		}
		configuration.worktrees = worktrees
		return nil
	}
}

// WithScheduleStore supplies candace/pkg/cron's occurrence and lease store.
func WithScheduleStore(scheduleStore cron.IStore) Option {
	return func(configuration *configuration) error {
		if scheduleStore == nil {
			return errors.New("copilot-adapter: WithScheduleStore needs a store")
		}
		configuration.scheduleStore = scheduleStore
		return nil
	}
}

// WithConfig supplies the adapter's tunables. The message and its bounds are
// generated from proto/candace/copilot/v1/adapter.proto and the defaults come
// from config/defaults.json, so a caller overrides a DECLARED value rather
// than a constant in source. Optional; DefaultAdapterConfig is the fallback.
func WithConfig(config *copilotv1.AdapterConfig) Option {
	return func(configuration *configuration) error {
		if config == nil {
			return errors.New("copilot-adapter: WithConfig needs a configuration")
		}
		if err := copilotv1.ValidateAdapterConfig(config); err != nil {
			return fmt.Errorf("copilot-adapter: WithConfig: %w", err)
		}
		configuration.config = config
		return nil
	}
}

// WithBridge supplies the Copilot CLI seam. Required.
func WithBridge(bridge ICopilotBridge) Option {
	return func(configuration *configuration) error {
		if bridge == nil {
			return errors.New("copilot-adapter: WithBridge needs a bridge")
		}
		configuration.bridge = bridge
		return nil
	}
}

// WithStore supplies the persistence seam. Required.
func WithStore(store IStore) Option {
	return func(configuration *configuration) error {
		if store == nil {
			return errors.New("copilot-adapter: WithStore needs a store")
		}
		configuration.store = store
		return nil
	}
}

// WithLogger supplies the structured logger. Optional; the service falls back
// to slog.Default.
func WithLogger(logger *slog.Logger) Option {
	return func(configuration *configuration) error {
		if logger == nil {
			return errors.New("copilot-adapter: WithLogger needs a logger")
		}
		configuration.logger = logger
		return nil
	}
}

// WithVersion labels the build in the health response.
func WithVersion(version string) Option {
	return func(configuration *configuration) error {
		if version == "" {
			return errors.New("copilot-adapter: WithVersion needs a version")
		}
		configuration.version = version
		return nil
	}
}

// defaultVersion is what GetHealth reports when the binary passes no
// WithVersion.
const defaultVersion = "dev"

func resolve(options []Option) (configuration, error) {
	resolved := configuration{logger: slog.Default(), version: defaultVersion, config: DefaultAdapterConfig()}
	for index, option := range options {
		if option == nil {
			return configuration{}, fmt.Errorf("copilot-adapter: option %d is nil", index)
		}
		if err := option(&resolved); err != nil {
			return configuration{}, err
		}
	}
	if resolved.bridge == nil {
		return configuration{}, errors.New("copilot-adapter: WithBridge is required")
	}
	if resolved.store == nil {
		return configuration{}, errors.New("copilot-adapter: WithStore is required")
	}
	if resolved.worktrees == nil {
		return configuration{}, errors.New("copilot-adapter: WithWorktreeManager is required")
	}
	if resolved.terminals == nil {
		return configuration{}, errors.New("copilot-adapter: WithTerminalManager is required")
	}
	if resolved.scheduleStore == nil {
		return configuration{}, errors.New("copilot-adapter: WithScheduleStore is required")
	}
	return resolved, nil
}

package copilotadapter

import (
	"errors"
	"fmt"
	"log/slog"

	copilotv1 "github.com/candacelabs/csf/services/copilot-adapter/proto/candace/copilot/v1"
	"github.com/candacelabs/csf/services/cron"
	"github.com/candacelabs/csf/services/workcontinuity"
)

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

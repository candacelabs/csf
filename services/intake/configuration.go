// Copyright 2026 Candace Labs

package intake

import (
	"fmt"
	"log/slog"
	"slices"
	"time"

	iohttp "github.com/candacelabs/csf/io/net/http"
	intakev1 "github.com/candacelabs/csf/proto/candace/intake/v1"
	"github.com/candacelabs/csf/services/relay"
)

type configuration struct {
	client        iohttp.IHTTPClient
	directory     IAgentDirectory
	messenger     relay.IMessenger[*intakev1.Event]
	routes        IRoutes
	queue         IEventQueue
	repositories  []string
	baseURL       string
	token         string
	interval      time.Duration
	maxBackoff    time.Duration
	since         time.Time
	ignoredActors map[string]struct{}
	sourceAgent   relay.AgentID
	logger        *slog.Logger
	clock         Clock
}

// configure applies options over the defaults and checks that every required
// grant is present.
func configure(options []Option) (configuration, error) {
	configured := configuration{
		baseURL:       DefaultAPIBaseURL,
		interval:      DefaultPollInterval,
		maxBackoff:    DefaultMaxBackoff,
		ignoredActors: map[string]struct{}{},
		sourceAgent:   DefaultSourceAgent,
		logger:        slog.New(slog.DiscardHandler),
		clock:         SystemClock(),
	}
	for index, option := range options {
		if option == nil {
			return configuration{}, fmt.Errorf("intake: option %d is nil", index)
		}
		if err := option(&configured); err != nil {
			return configuration{}, err
		}
	}
	switch {
	case configured.client == nil:
		return configuration{}, fmt.Errorf("%w: WithHTTPClient", ErrMissingOption)
	case configured.directory == nil:
		return configuration{}, fmt.Errorf("%w: WithRelay", ErrMissingOption)
	case configured.routes == nil:
		return configuration{}, fmt.Errorf("%w: WithRoutes", ErrMissingOption)
	case len(configured.repositories) == 0:
		return configuration{}, fmt.Errorf("%w: WithRepositories", ErrMissingOption)
	}
	slices.Sort(configured.repositories)
	configured.repositories = slices.Compact(configured.repositories)
	if configured.queue == nil {
		queue, err := NewMemoryEventQueue()
		if err != nil {
			return configuration{}, err
		}
		configured.queue = queue
	}
	return configured, nil
}

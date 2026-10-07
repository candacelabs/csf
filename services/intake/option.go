// Copyright 2026 Candace Labs

package intake

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	iohttp "github.com/candacelabs/csf/io/net/http"
	intakev1 "github.com/candacelabs/csf/proto/candace/intake/v1"
	"github.com/candacelabs/csf/services/relay"
)

const (
	// DefaultAPIBaseURL is GitHub's REST API.
	DefaultAPIBaseURL = "https://api.github.com"
	// DefaultPollInterval is the wait between polling rounds when GitHub asks
	// for no longer one.
	DefaultPollInterval = time.Minute
	// DefaultMaxBackoff caps any single wait, however far away GitHub says the
	// rate-limit reset is, so a skewed clock cannot park the poller for hours.
	DefaultMaxBackoff = time.Hour
	// DefaultSourceAgent is the relay agent identifier the intake registers
	// itself under and sends from.
	DefaultSourceAgent relay.AgentID = "intake"
)

// Option configures an [EventIntake] before [NewEventIntake] builds anything.
type Option func(configuration *configuration) error

// WithHTTPClient grants the HTTP capability the GitHub API is reached
// through — normally iohttp.NewHTTPClient over an ipc/net dialer. Required.
func WithHTTPClient(client iohttp.IHTTPClient) Option {
	return func(configuration *configuration) error {
		if client == nil {
			return errors.New("intake: WithHTTPClient needs a client")
		}
		configuration.client = client
		return nil
	}
}

// WithRelay supplies the relay events are delivered through: the directory
// the intake registers with and resolves owners in, and the messenger it
// sends on. Required.
func WithRelay(directory IAgentDirectory, messenger relay.IMessenger[*intakev1.Event]) Option {
	return func(configuration *configuration) error {
		if directory == nil || messenger == nil {
			return errors.New("intake: WithRelay needs a directory and a messenger")
		}
		configuration.directory = directory
		configuration.messenger = messenger
		return nil
	}
}

// WithRoutes supplies the table naming each subject's owning agent. Required.
func WithRoutes(routes IRoutes) Option {
	return func(configuration *configuration) error {
		if routes == nil {
			return errors.New("intake: WithRoutes needs routes")
		}
		configuration.routes = routes
		return nil
	}
}

// WithRepositories names the owner/name repositories to poll. Required.
func WithRepositories(repositories ...string) Option {
	return func(configuration *configuration) error {
		for _, repository := range repositories {
			if err := ValidateRepository(repository); err != nil {
				return err
			}
		}
		configuration.repositories = append(configuration.repositories, repositories...)
		return nil
	}
}

// WithQueue replaces the in-memory queue. Optional.
func WithQueue(queue IEventQueue) Option {
	return func(configuration *configuration) error {
		if queue == nil {
			return errors.New("intake: WithQueue needs a queue")
		}
		configuration.queue = queue
		return nil
	}
}

// WithAPIBaseURL points the poller at a GitHub Enterprise server or a double.
func WithAPIBaseURL(base string) Option {
	return func(configuration *configuration) error {
		parsed, err := url.Parse(base)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return fmt.Errorf("intake: API base URL %q must be absolute", base)
		}
		configuration.baseURL = parsed.String()
		return nil
	}
}

// WithToken authenticates every request. Without one GitHub allows 60
// requests an hour, which is too few for more than one repository.
func WithToken(token string) Option {
	return func(configuration *configuration) error {
		configuration.token = token
		return nil
	}
}

// WithPollInterval sets the wait between rounds. GitHub's X-Poll-Interval
// lengthens it, never shortens it.
func WithPollInterval(interval time.Duration) Option {
	return func(configuration *configuration) error {
		if interval <= 0 {
			return fmt.Errorf("intake: poll interval must be positive, got %s", interval)
		}
		configuration.interval = interval
		return nil
	}
}

// WithMaxBackoff caps any single wait.
func WithMaxBackoff(backoff time.Duration) Option {
	return func(configuration *configuration) error {
		if backoff <= 0 {
			return fmt.Errorf("intake: maximum backoff must be positive, got %s", backoff)
		}
		configuration.maxBackoff = backoff
		return nil
	}
}

// WithBacklogSince accepts events that happened at or after since. The
// default is the moment the intake starts, so a restart does not re-wake
// every agent with the feed's history.
func WithBacklogSince(since time.Time) Option {
	return func(configuration *configuration) error {
		configuration.since = since
		return nil
	}
}

// WithIgnoredActors drops events caused by these logins — typically the
// agents' own bot account, so an agent is not woken by its own comment.
func WithIgnoredActors(logins ...string) Option {
	return func(configuration *configuration) error {
		for _, login := range logins {
			configuration.ignoredActors[login] = struct{}{}
		}
		return nil
	}
}

// WithSourceAgent sets the relay agent identifier the intake sends from.
func WithSourceAgent(agent relay.AgentID) Option {
	return func(configuration *configuration) error {
		if err := agent.Validate(); err != nil {
			return err
		}
		configuration.sourceAgent = agent
		return nil
	}
}

// WithLogger receives one record per failed poll, rate-limit backoff and
// undeliverable event.
func WithLogger(logger *slog.Logger) Option {
	return func(configuration *configuration) error {
		if logger == nil {
			return errors.New("intake: WithLogger needs a logger")
		}
		configuration.logger = logger
		return nil
	}
}

// WithClock replaces the wall clock.
func WithClock(clock Clock) Option {
	return func(configuration *configuration) error {
		if clock.Now == nil || clock.After == nil {
			return errors.New("intake: WithClock needs Now and After")
		}
		configuration.clock = clock
		return nil
	}
}

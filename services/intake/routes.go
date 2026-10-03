// Copyright 2026 Candace Labs

package intake

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	intakev1 "github.com/candacelabs/csf/proto/candace/intake/v1"
	"github.com/candacelabs/csf/services/relay"
)

var (
	// ErrUnrouted reports an event whose subject no route names.
	ErrUnrouted = errors.New("intake: no agent owns this subject")
	// ErrInvalidRoute reports a route without a repository or a valid agent.
	ErrInvalidRoute = errors.New("intake: invalid route")
)

// repositoryPattern is the owner/name grammar the Subject contract checks.
var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// ValidateRepository reports whether repository is an owner/name pair.
func ValidateRepository(repository string) error {
	if !repositoryPattern.MatchString(repository) {
		return fmt.Errorf("intake: repository %q must be owner/name", repository)
	}
	return nil
}

// IRoutes names the agent that owns an event's subject. The static table is
// the implementation until agents' ownership claims are stored; a claims
// implementation answers the same question from them.
type IRoutes interface {
	// Route returns the owning agent, or an error wrapping [ErrUnrouted].
	Route(ctx context.Context, subject *intakev1.Subject) (relay.AgentID, error)
}

// Route assigns the events of one repository, or of one pull request or issue
// in it, to an agent.
type Route struct {
	Repository string
	// Number is a pull request or issue number; zero routes every subject in
	// the repository that has no route of its own.
	Number uint64
	Agent  relay.AgentID
}

type routeKey struct {
	repository string
	number     uint64
}

// StaticRoutes is a fixed routing table from configuration.
type StaticRoutes struct {
	routes map[routeKey]relay.AgentID
}

var _ IRoutes = (*StaticRoutes)(nil)

// NewStaticRoutes validates routes and builds the table. Two routes for the
// same subject are rejected rather than resolved by order.
func NewStaticRoutes(routes ...Route) (*StaticRoutes, error) {
	table := make(map[routeKey]relay.AgentID, len(routes))
	for _, route := range routes {
		if err := ValidateRepository(route.Repository); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalidRoute, err)
		}
		if err := route.Agent.Validate(); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalidRoute, err)
		}
		key := routeKey{repository: route.Repository, number: route.Number}
		if holder, taken := table[key]; taken {
			return nil, fmt.Errorf("%w: %s#%d is routed to both %q and %q", ErrInvalidRoute, route.Repository, route.Number, holder, route.Agent)
		}
		table[key] = route.Agent
	}
	return &StaticRoutes{routes: table}, nil
}

// Repositories lists every repository the table routes, sorted and each
// once: the repositories worth polling.
func (routes *StaticRoutes) Repositories() []string {
	repositories := make([]string, 0, len(routes.routes))
	for key := range routes.routes {
		repositories = append(repositories, key.repository)
	}
	slices.Sort(repositories)
	return slices.Compact(repositories)
}

// Route specification separators: "owner/name#12=agent,owner/name=agent".
const (
	routeListSeparator   = ","
	routeAgentSeparator  = "="
	routeNumberSeparator = "#"
)

// ParseRoutes reads a comma-separated route list in which each entry is
// owner/name=agent (every subject in the repository) or owner/name#N=agent
// (pull request or issue N). It validates the syntax only; [NewStaticRoutes]
// validates the table.
func ParseRoutes(specification string) ([]Route, error) {
	var routes []Route
	for entry := range strings.SplitSeq(specification, routeListSeparator) {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		subject, agent, found := strings.Cut(entry, routeAgentSeparator)
		if !found {
			return nil, fmt.Errorf("%w: %q needs %s and an agent", ErrInvalidRoute, entry, routeAgentSeparator)
		}
		route := Route{Repository: strings.TrimSpace(subject), Agent: relay.AgentID(strings.TrimSpace(agent))}
		if repository, number, numbered := strings.Cut(route.Repository, routeNumberSeparator); numbered {
			parsed, err := strconv.ParseUint(number, 10, 64)
			if err != nil || parsed == 0 {
				return nil, fmt.Errorf("%w: %q has no positive number after %s", ErrInvalidRoute, entry, routeNumberSeparator)
			}
			route.Repository, route.Number = repository, parsed
		}
		routes = append(routes, route)
	}
	if len(routes) == 0 {
		return nil, fmt.Errorf("%w: the route list is empty", ErrInvalidRoute)
	}
	return routes, nil
}

// Route prefers the subject's own route, then its repository's.
func (routes *StaticRoutes) Route(ctx context.Context, subject *intakev1.Subject) (relay.AgentID, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if subject == nil {
		return "", fmt.Errorf("%w: the event has no subject", ErrUnrouted)
	}
	if subject.GetNumber() != 0 {
		if agent, routed := routes.routes[routeKey{repository: subject.GetRepository(), number: subject.GetNumber()}]; routed {
			return agent, nil
		}
	}
	if agent, routed := routes.routes[routeKey{repository: subject.GetRepository()}]; routed {
		return agent, nil
	}
	return "", fmt.Errorf("%w: %s#%d", ErrUnrouted, subject.GetRepository(), subject.GetNumber())
}

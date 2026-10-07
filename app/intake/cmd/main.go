// Copyright 2026 Candace Labs

// Command intake polls GitHub for actionable events on the configured pull
// requests and issues and delivers each to its owning agent through an
// in-process relay.
//
// It is the smallest composition of services/intake: a host runtime, the
// relay, a journal and the intake. The journal registers every routed agent at
// an in-process address and logs each event it receives, one JSON line per
// event on standard output, so the binary shows deliveries end to end with no
// agent attached. Composed into a binary that also serves the relay's
// authenticated agent MCP endpoint, the same events reach real host and
// network agents instead.
//
// The binary opens no listener: it only dials out to the GitHub API, so it
// needs no ingress of any kind.
//
//	CSF_INTAKE_GITHUB_TOKEN=… go run ./app/intake/cmd \
//	    --routes 'candacelabs/example#12=reviewer,candacelabs/example=triage'
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/candacelabs/csf/io"
	"github.com/candacelabs/csf/io/net/model"
	ionet "github.com/candacelabs/csf/io/net"
	iohttp "github.com/candacelabs/csf/io/net/http"
	intakev1 "github.com/candacelabs/csf/proto/candace/intake/v1"
	"github.com/candacelabs/csf/runtime"
	"github.com/candacelabs/csf/runtime/config"
	"github.com/candacelabs/csf/services/intake"
	"github.com/candacelabs/csf/services/relay"
)

const (
	hostName      = "intake"
	tokenVariable = "CSF_INTAKE_GITHUB_TOKEN"
	// journalProvider names the journal's address type.
	journalProvider  = "journal"
	actorSeparator   = ","
	relayMount       = "relay"
	journalMount     = "journal"
	intakeMount      = "intake"
	routesFlag       = "routes"
	pollIntervalFlag = "poll-interval"
	apiBaseURLFlag   = "api-base-url"
	ignoreActorsFlag = "ignore-actors"
)

// journalAddress is an agent the journal stands in for, in this runtime.
type journalAddress struct {
	model.InProcessTier
	agent relay.AgentID
}

func (address journalAddress) Provider() string { return journalProvider }

func (address journalAddress) Key() string {
	return model.AddressKey(journalProvider, io.TierInProcess, string(address.agent))
}

// journal registers each agent and logs every event delivered to it until its
// scope ends.
func journal(core *relay.Relay[*intakev1.Event], messenger *relay.Messenger[*intakev1.Event], agents []relay.AgentID, logger *slog.Logger) runtime.ServiceFunc {
	return func(scope *runtime.Scope) error {
		for _, agent := range agents {
			if err := core.Register(scope.Context(), relay.Registration{Agent: agent, Address: journalAddress{agent: agent}}); err != nil {
				return err
			}
			if err := scope.GoOwner(journalMount+" "+string(agent), func(ctx context.Context) error {
				for {
					envelope, err := messenger.Receive(ctx, agent)
					if err != nil {
						if ctx.Err() != nil {
							return nil
						}
						return err
					}
					event := envelope.Body
					logger.Info("event delivered", "agent", string(agent), "event", event.GetId(),
						"kind", event.GetKind().String(), "repository", event.GetSubject().GetRepository(),
						"number", event.GetSubject().GetNumber(), "actor", event.GetActor(), "url", event.GetUrl())
				}
			}); err != nil {
				return err
			}
		}
		return nil
	}
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "intake:", err)
		os.Exit(1)
	}
}

func run() error {
	routeList := flag.String(routesFlag, "", "comma-separated routes: owner/name=agent or owner/name#N=agent (required)")
	interval := flag.Duration(pollIntervalFlag, intake.DefaultPollInterval, "wait between polling rounds; GitHub's X-Poll-Interval can lengthen it")
	apiBase := flag.String(apiBaseURLFlag, intake.DefaultAPIBaseURL, "GitHub REST API base URL")
	ignored := flag.String(ignoreActorsFlag, "", "comma-separated logins whose events never wake an agent")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	token := config.OSEnvironment().Raw(tokenVariable)
	if token == "" {
		logger.Warn("no token; GitHub allows 60 unauthenticated requests an hour", "variable", tokenVariable)
	}
	parsed, err := intake.ParseRoutes(*routeList)
	if err != nil {
		return err
	}
	routes, err := intake.NewStaticRoutes(parsed...)
	if err != nil {
		return err
	}
	client, err := iohttp.NewHTTPClient(ionet.NewHostNetwork())
	if err != nil {
		return err
	}
	defer client.CloseIdleConnections()
	core, err := relay.NewRelay[*intakev1.Event]()
	if err != nil {
		return err
	}
	messenger, err := relay.NewMessenger[*intakev1.Event](core)
	if err != nil {
		return err
	}
	options := []intake.Option{
		intake.WithHTTPClient(client),
		intake.WithRelay(core, messenger),
		intake.WithRoutes(routes),
		intake.WithRepositories(routes.Repositories()...),
		intake.WithAPIBaseURL(*apiBase),
		intake.WithPollInterval(*interval),
		intake.WithToken(token),
		intake.WithLogger(logger),
	}
	if *ignored != "" {
		options = append(options, intake.WithIgnoredActors(strings.Split(*ignored, actorSeparator)...))
	}
	service, err := intake.NewEventIntake(options...)
	if err != nil {
		return err
	}

	agents := make([]relay.AgentID, 0, len(parsed))
	for _, route := range parsed {
		agents = append(agents, route.Agent)
	}
	return serve(core, messenger, service, uniqueAgents(agents), logger)
}

// serve mounts the relay, the journal and the intake, in that order, and runs
// them until SIGINT or SIGTERM.
func serve(core *relay.Relay[*intakev1.Event], messenger *relay.Messenger[*intakev1.Event], service *intake.EventIntake, agents []relay.AgentID, logger *slog.Logger) error {
	host, err := runtime.NewHostRuntime(runtime.WithHostName(hostName), runtime.WithLogger(logger))
	if err != nil {
		return err
	}
	mounts := []struct {
		name    string
		service runtime.IService
	}{
		{relayMount, core},
		{journalMount, journal(core, messenger, agents, logger)},
		{intakeMount, service},
	}
	for _, mounted := range mounts {
		if err := host.Mount(mounted.name, mounted.service); err != nil {
			return err
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := host.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// uniqueAgents drops repeated agents, keeping the first occurrence's order.
func uniqueAgents(agents []relay.AgentID) []relay.AgentID {
	seen := map[relay.AgentID]struct{}{}
	unique := agents[:0]
	for _, agent := range agents {
		if _, repeated := seen[agent]; !repeated {
			seen[agent] = struct{}{}
			unique = append(unique, agent)
		}
	}
	return unique
}

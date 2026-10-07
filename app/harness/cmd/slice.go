// Copyright 2026 Candace Labs

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/candacelabs/csf/csf"
	"github.com/candacelabs/csf/io/ipc/db/csfpg"
	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/pkg/atomicfile"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
	dispatchservice "github.com/candacelabs/csf/services/dispatch"
	"github.com/candacelabs/csf/services/harness"
	"github.com/candacelabs/csf/services/harness/provider"
	"github.com/candacelabs/csf/services/harness/session"
	"github.com/candacelabs/csf/services/ouroboros"
)

// The slice verbs: thin clients of the slice dispatcher's operations on the
// running host. add puts one slice in the graph from a recipe directory (the
// agent.json and brief.md the orchestrator writes); hold and release name
// one slice; pause and resume the dispatcher; status prints its snapshot.
const (
	verbSlice   = "slice"
	verbAdd     = "add"
	verbHold    = "hold"
	verbRelease = "release"
	verbPause   = "pause"
	verbResume  = "resume"
	verbStatus  = "status"
	// record prints a run's slice record (#420); verify launches the
	// verifier station on it.
	verbRecord    = "record"
	verbVerify    = "verify"
	sliceFlag     = "slice"
	titleFlag     = "title"
	dependsOnFlag = "depends-on"
	contendsFlag  = "contends"
	hotspotFlag   = "hotspot"
	pathFlag      = "path"
	reasonFlag    = "reason"
	// recipeFileName is the recipe a recipe directory holds; its
	// workspace.brief_path names the brief beside it.
	recipeFileName    = "agent.json"
	dispatchMountName = "dispatch"
	contentTypeHeader = "Content-Type"
	contentTypeJSON   = "application/json"
	maxResponseBytes  = 8 << 20
)

var errUsageSlice = errors.New("usage: csf slice add -ticket URL -recipe DIR [-slice ID] [-title T] [-depends-on ID]... [-contends ID]... [-hotspot NAME]... [-path GLOB]... | hold|release -slice ID -reason TEXT | pause|resume -reason TEXT | status | record|verify -assignment ID")

// newDispatcher grants the slice dispatcher this host's capabilities: the
// sessions it launches onto and whose launch check it reads, the slice graph
// on the pool, the session logs its rate limit reads, the mining loop's
// daily budget when the loop is mounted, gh for the merge check, and the
// snapshot file under the state directory.
func newDispatcher(pool csfpg.IDB, state string, launcher *proc.HostLauncher, logger *slog.Logger,
	sessions *harness.AgentSessionService, loop *ouroboros.Loop) (*dispatchservice.DispatchService, error) {
	database, err := dispatchservice.NewPostgresDispatchDatabase(pool)
	if err != nil {
		return nil, err
	}
	logs, err := iofs.NewHostFiles(state)
	if err != nil {
		return nil, err
	}
	providerBaseURL, err := activeProvider(state)
	if err != nil {
		return nil, err
	}
	options := []dispatchservice.Option{
		dispatchservice.WithSessions(sessions),
		dispatchservice.WithDatabase(database),
		dispatchservice.WithLimit(dispatchservice.RateLimit(logs, harness.SystemClock{}, providerBaseURL)),
		dispatchservice.WithMergeCheck(mergeCheck(launcher)),
		dispatchservice.WithSnapshotSink(func(snapshot dispatchservice.Snapshot) error {
			content, err := json.MarshalIndent(snapshot, "", "  ")
			if err != nil {
				return err
			}
			return atomicfile.WriteFile(filepath.Join(state, dispatchservice.SnapshotFile), content, hostFileMode)
		}),
		dispatchservice.WithLogger(logger),
	}
	if loop != nil {
		options = append(options, dispatchservice.WithLimit(dispatchservice.DailyBudgetLimit(loop.DailyBudget)))
	} else {
		logger.Warn("harness: no -" + ouroborosRepositoryFlag + ", so the slice dispatcher has no daily budget to read")
	}
	return dispatchservice.NewDispatchService(options...)
}

// activeProvider is the base URL of the provider the state directory records
// in providers.json, which the rate limit is keyed by, or empty when the host
// records no router entry and runs sessions on the executor's own provider. An
// entry that cannot be used is an error, as it is for the runner.
func activeProvider(state string) (string, error) {
	router, err := provider.ReadRouter(state)
	if errors.Is(err, provider.ErrNoRouter) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return router.BaseURL(), nil
}

// mergeCheck reads a recorded pull request's state through gh, in the
// repository its URL names.
func mergeCheck(launcher *proc.HostLauncher) dispatchservice.MergeCheck {
	return func(ctx context.Context, url string) (bool, error) {
		slug, number, err := ouroboros.PullRequestOf(url)
		if err != nil {
			return false, err
		}
		tickets, err := ouroboros.NewGitHubTickets(launcher, slug)
		if err != nil {
			return false, err
		}
		pull, err := tickets.PullRequest(ctx, number)
		if err != nil {
			return false, err
		}
		return pull.Merged(), nil
	}
}

// sliceVerbs runs one slice verb against the running host.
func sliceVerbs(ctx context.Context, launcher *proc.HostLauncher, arguments []string, output io.Writer) error {
	if len(arguments) == 0 {
		return errUsageSlice
	}
	verb := arguments[0]
	flags := flag.NewFlagSet(commandName+" "+verbSlice+" "+verb, flag.ContinueOnError)
	endpoint := flags.String(endpointFlag, "", "harness address (default: the one <state>/"+HostRecordFile+" records)")
	stateDirectory := flags.String(stateFlag, "", "state directory holding "+HostRecordFile+" (default ~/"+defaultStateDirectory+")")
	slice := flags.String(sliceFlag, "", "slice identifier (add: default the recipe directory's name)")
	reason := flags.String(reasonFlag, "", "why, recorded with the control")
	ticket := flags.String(ticketFlag, "", "ticket URL the slice delivers")
	recipe := flags.String(recipeFlag, "", "recipe directory holding "+recipeFileName+" and its brief")
	title := flags.String(titleFlag, "", "slice title (default: the recipe's pull request title)")
	assignment := flags.String(assignmentFlag, "", "author run's assignment identifier (record, verify)")
	var dependsOn, contends, hotspots, paths listFlag
	flags.Var(&dependsOn, dependsOnFlag, "slice that must merge first; repeat for several")
	flags.Var(&contends, contendsFlag, "slice never run beside this one; repeat for several")
	flags.Var(&hotspots, hotspotFlag, "named hotspot the slice touches; repeat for several")
	flags.Var(&paths, pathFlag, "path glob the slice touches; repeat for several")
	if err := flags.Parse(arguments[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errUsageSlice
	}
	if verb == verbRecord || verb == verbVerify {
		return sliceRecordVerbs(ctx, launcher, verb, *endpoint, *stateDirectory, *assignment, output)
	}
	target, err := resolveEndpoint(*endpoint, *stateDirectory)
	if err != nil {
		return err
	}
	base := strings.TrimRight(target, "/")
	switch verb {
	case verbAdd:
		if *ticket == "" || *recipe == "" {
			return errUsageSlice
		}
		read, err := readRecipe(filepath.Join(*recipe, recipeFileName))
		if err != nil {
			return err
		}
		id := *slice
		if id == "" {
			id = filepath.Base(filepath.Clean(*recipe))
		}
		return requestDispatcher(ctx, http.MethodPost, base+dispatchservice.AddPath, dispatchservice.AddSliceInput{
			SliceID: id, Title: *title, TicketURL: *ticket, Recipe: read,
			DependsOn: dependsOn, Contends: contends, Hotspots: hotspots, Paths: paths,
		}, output)
	case verbHold, verbRelease:
		path := map[string]string{verbHold: dispatchservice.HoldPath, verbRelease: dispatchservice.ReleasePath}[verb]
		return requestDispatcher(ctx, http.MethodPost, base+path, dispatchservice.SliceControlInput{SliceID: *slice, Reason: *reason}, output)
	case verbPause, verbResume:
		path := map[string]string{verbPause: dispatchservice.PausePath, verbResume: dispatchservice.ResumePath}[verb]
		return requestDispatcher(ctx, http.MethodPost, base+path, dispatchservice.DispatcherControlInput{Reason: *reason}, output)
	case verbStatus:
		return requestDispatcher(ctx, http.MethodGet, base+dispatchservice.SnapshotPath, dispatchservice.SnapshotInput{}, output)
	}
	return errUsageSlice
}

// sliceRecordVerbs prints an author run's slice record, or launches the
// verifier station on it through the running host and prints its receipt.
func sliceRecordVerbs(ctx context.Context, launcher *proc.HostLauncher, verb string, endpoint string, stateDirectory string,
	assignment string, output io.Writer) error {
	if assignment == "" {
		return errNoAssignment
	}
	state, err := stateRoot(stateDirectory)
	if err != nil {
		return err
	}
	record, err := session.ReadSliceRecord(ctx, launcher, state, assignment)
	if err != nil {
		return err
	}
	if verb == verbRecord {
		content, err := json.MarshalIndent(record, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(output, string(content))
		return err
	}
	content, err := os.ReadFile(filepath.Join(session.RunDirectory(state, assignment), session.RecipeFile))
	if err != nil {
		return err
	}
	author := &pb.AgentAssignmentRecipe{}
	if err := protojson.Unmarshal(content, author); err != nil {
		return fmt.Errorf("decode %s: %w", session.RecipeFile, err)
	}
	recipe, err := session.VerifierRecipe(record, author)
	if err != nil {
		return err
	}
	target, err := resolveEndpoint(endpoint, stateDirectory)
	if err != nil {
		return err
	}
	client, err := csf.NewClient(target, &http.Client{Timeout: clientTimeout}, clientWarnings)
	if err != nil {
		return err
	}
	response, err := client.SubmitAgentSession(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: recipe})
	return print(output, response, err)
}

// requestDispatcher sends one operation's input to its route and prints the
// JSON it answers, indented.
func requestDispatcher[Input any](ctx context.Context, method string, url string, input Input, output io.Writer) error {
	var body io.Reader
	if method != http.MethodGet {
		content, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(content)
	}
	request, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return err
	}
	request.Header.Set(contentTypeHeader, contentTypeJSON)
	response, err := (&http.Client{Timeout: clientTimeout}).Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	answer, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("harness returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(answer)))
	}
	var indented bytes.Buffer
	if err := json.Indent(&indented, answer, "", "  "); err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, indented.String())
	return err
}

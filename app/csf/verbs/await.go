// Copyright 2026 Candace Labs

package verbs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/candacelabs/csf/csf"
	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/io/kernel/clock"
	iofs "github.com/candacelabs/csf/io/kernel/fs"
	ionet "github.com/candacelabs/csf/io/net"
	iohttp "github.com/candacelabs/csf/io/net/http"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
	"github.com/candacelabs/csf/services/await"
	"github.com/candacelabs/csf/services/harness"
)

const (
	verbAwait       = "await"
	deadlineFlag    = "deadline"
	pidFlag         = "pid"
	phaseFlag       = "phase"
	turnFlag        = "turn"
	pullRequestFlag = "pull-request"
	urlFlag         = "url"
	statusFlag      = "status"
	loadFlag        = "load"

	// lifecycleDeadline bounds csf serve -detach and csf stop by default.
	// Derived 2026-10-05 from the six restarts in this host's harness.log:
	// a stop took 0 to 32 s (the 30 s ones are the close budget) and a stop
	// to serving took at most 18 s; four times the longest observed is about
	// two minutes, which also covers a stop's bound by construction, the
	// 60 s interrupt budget plus the 30 s close budget.
	lifecycleDeadline = 2 * time.Minute
	// awaitClientMargin is how much longer than its deadline the client lets
	// an await operation's response take: the host answers at the deadline.
	awaitClientMargin = 30 * time.Second
	logTailLines      = 20
)

var errNotMet = errors.New("the condition did not hold")

// lifecycleConditions are awaited by the client itself: they are about
// whether the host runs, so the host cannot answer them.
var lifecycleConditions = map[await.Condition]bool{await.ConditionHarnessReady: true, await.ConditionHarnessStopped: true}

// awaitVerb is csf await CONDITION -deadline D [operands]: the host's Await
// operation, or for harness-ready and harness-stopped the same wait run
// here. It prints the typed result and fails unless the condition held.
func awaitVerb(ctx context.Context, arguments []string, output io.Writer) error {
	if len(arguments) == 0 || strings.HasPrefix(arguments[0], "-") {
		return fmt.Errorf("usage: csf await CONDITION -deadline DURATION [operands]; conditions: %s", conditionNames())
	}
	request := await.Request{Condition: await.Condition(arguments[0])}
	flags := flag.NewFlagSet(commandName+" "+verbAwait, flag.ContinueOnError)
	flags.StringVar(&request.Deadline, deadlineFlag, "", "how long to wait at most, such as 90s or 10m (required)")
	flags.IntVar(&request.PID, pidFlag, 0, "harness-ready, harness-stopped: the host process (default: the recorded one)")
	flags.StringVar(&request.Assignment, assignmentFlag, "", "session-phase, turn-finished: the session's assignment")
	flags.StringVar(&request.Phase, phaseFlag, "", "session-phase: the phase, such as OPEN")
	turn := flags.Uint(turnFlag, 0, "turn-finished: the turn that must have finished (default: the latest)")
	flags.StringVar(&request.PullRequest, pullRequestFlag, "", "pull-request-merged: its URL, or its number with -repository")
	flags.StringVar(&request.Repository, repositoryFlag, "", "pull-request-merged: OWNER/NAME")
	flags.StringVar(&request.URL, urlFlag, "", "url-status: the URL to GET")
	flags.IntVar(&request.Status, statusFlag, 0, "url-status: the status awaited (default 200)")
	flags.Float64Var(&request.Load, loadFlag, 0, "load-below: the level the one-minute load average must fall below")
	endpoint := flags.String(endpointFlag, "", "harness address (default: the one <state>/"+HostRecordFile+" records)")
	stateDirectory := flags.String(stateFlag, "", "state directory holding "+HostRecordFile+" (default ~/"+defaultStateDirectory+")")
	if err := flags.Parse(arguments[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errUsage
	}
	request.Turn = uint32(*turn)
	deadline, err := await.Validate(request)
	if err != nil {
		return err
	}
	var result await.Result
	if lifecycleConditions[request.Condition] {
		result, err = awaitHere(ctx, *stateDirectory, request)
	} else {
		result, err = awaitOnHost(ctx, *endpoint, *stateDirectory, deadline, request)
	}
	if err != nil {
		return err
	}
	return printResult(output, result)
}

// awaitHere runs a lifecycle wait in this process, over the state directory
// and the process table.
func awaitHere(ctx context.Context, stateDirectory string, request await.Request) (await.Result, error) {
	state, err := stateRoot(stateDirectory)
	if err != nil {
		return await.Result{}, err
	}
	awaiter, err := lifecycleAwaiter(state)
	if err != nil {
		return await.Result{}, err
	}
	return awaiter.Await(ctx, request)
}

// lifecycleAwaiter grants a wait the state directory and the process table.
func lifecycleAwaiter(state string) (*await.Awaiter, error) {
	stateFiles, err := iofs.NewHostFiles(state)
	if err != nil {
		return nil, err
	}
	processes, err := iofs.NewHostFiles(processTableDirectory)
	if err != nil {
		return nil, err
	}
	return await.NewAwaiter(await.WithClock(clock.NewSystemClock()), await.WithHostState(stateFiles), await.WithProcessTable(processes))
}

// awaitOnHost sends the wait to the host's Await operation.
func awaitOnHost(ctx context.Context, endpoint string, stateDirectory string, deadline time.Duration, request await.Request) (await.Result, error) {
	var result await.Result
	target, err := resolveEndpoint(endpoint, stateDirectory)
	if err != nil {
		return result, err
	}
	content, err := json.Marshal(request)
	if err != nil {
		return result, err
	}
	call, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(target, "/")+await.AwaitPath, bytes.NewReader(content))
	if err != nil {
		return result, err
	}
	call.Header.Set(contentTypeHeader, contentTypeJSON)
	response, err := (&http.Client{Timeout: deadline + awaitClientMargin}).Do(call)
	if err != nil {
		return result, err
	}
	defer func() { _ = response.Body.Close() }()
	answer, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return result, err
	}
	if response.StatusCode != http.StatusOK {
		return result, fmt.Errorf("harness returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(answer)))
	}
	return result, json.Unmarshal(answer, &result)
}

// printResult prints the typed result and fails unless the condition held.
func printResult(output io.Writer, result await.Result) error {
	content, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(output, string(content)); err != nil {
		return err
	}
	if !result.Met() {
		return fmt.Errorf("%w: %s %s after %d ms: %s", errNotMet, result.Condition, result.Outcome, result.ElapsedMS, result.Observation)
	}
	return nil
}

func conditionNames() string {
	names := make([]string, 0)
	for _, condition := range await.Conditions() {
		names = append(names, string(condition))
	}
	return strings.Join(names, ", ")
}

// lifecycleWait waits for a lifecycle condition of one host process, here,
// with the lifecycle deadline.
func lifecycleWait(ctx context.Context, state string, condition await.Condition, pid int, deadline time.Duration) (await.Result, error) {
	awaiter, err := lifecycleAwaiter(state)
	if err != nil {
		return await.Result{}, err
	}
	return awaiter.Await(ctx, await.Request{Condition: condition, Deadline: deadline.String(), PID: pid})
}

// hostAwaiter grants the host's Await operation every capability its
// conditions read: the state directory, the process table, the session
// service, the launcher gh runs through and an HTTP client.
func hostAwaiter(state string, processes iofs.IFiles, launcher proc.ILauncher, sessions *harness.AgentSessionService) (*await.Awaiter, error) {
	stateFiles, err := iofs.NewHostFiles(state)
	if err != nil {
		return nil, err
	}
	client, err := iohttp.NewHTTPClient(ionet.NewHostNetwork())
	if err != nil {
		return nil, err
	}
	return await.NewAwaiter(
		await.WithClock(clock.NewSystemClock()),
		await.WithHostState(stateFiles),
		await.WithProcessTable(processes),
		await.WithLauncher(launcher),
		await.WithHTTPClient(client),
		await.WithSessionState(func(ctx context.Context, assignment string) (*harnessv1.AgentSessionState, error) {
			response, err := sessions.Get(ctx, &harnessv1.GetAgentSessionRequest{AssignmentId: assignment})
			return response.GetSession(), err
		}),
	)
}

// stopHost is csf stop: it asks the host to stop and, when the host it
// asked is the one this state directory records, returns once that process
// has exited.
func stopHost(ctx context.Context, client *csf.Client, target string, stateDirectory string, deadline time.Duration, output io.Writer) error {
	state, err := stateRoot(stateDirectory)
	if err != nil {
		return err
	}
	stateFiles, err := iofs.NewHostFiles(state)
	if err != nil {
		return err
	}
	record, recorded, err := await.ReadHostRecord(stateFiles)
	if err != nil {
		return err
	}
	response, err := client.StopHarness(ctx, &harnessv1.StopHarnessRequest{})
	if err := print(output, response, err); err != nil {
		return err
	}
	if !recorded || record.Endpoint != target {
		fmt.Fprintf(output, "%s: %s is not the host %s records, so its exit is not awaited\n", commandName, target, filepath.Join(state, HostRecordFile))
		return nil
	}
	result, err := lifecycleWait(ctx, state, await.ConditionHarnessStopped, record.PID, deadline)
	if err != nil {
		return err
	}
	return printResult(output, result)
}

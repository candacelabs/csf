// Copyright 2026 Candace Labs

// Command csf is the agent harness: one process per machine that runs every
// agent session, and the thin client that talks to it. It was called harness;
// invoked by that name it still works and says so.
//
//	csf init [-repo DIR] [-state DIR] [-listen ADDR]
//	csf serve -listen 127.0.0.1:14120 [-listen ADDR]... [-state DIR] [-claude PATH] [-database-config FILE] [-detach]
//	csf submit -recipe agent.json
//	csf send -assignment ID -message TEXT | -message-file FILE
//	csf chat -assignment ID
//	csf list | get -assignment ID | cancel -assignment ID | events -assignment ID [-from N] | stop
//	csf housekeeping -trigger NAME [-trigger NAME]... [-dry-run] [-scratch DIR]
//	csf view -listen 127.0.0.1:14121 [-listen ADDR]... [-state DIR] [-detach | -stop]
//
// init, run at the root of any git repository, writes the sample assignment
// under .csf/assignments/sample and registers with the running host, or
// starts one. chat prints a session's chat address. serve is the host app: it builds the one host runtime, mounts the session
// service, the cron service with the default housekeeping triggers (when
// -database-config names the csfpg database its store lives in), the chat
// and one HTTP listener per address, and records where it listens in
// <state>/harness.json. housekeeping runs named triggers once, now, against
// the running host's sessions, and prints every record it writes. view is a
// second host app: it mounts the ops view, the live page of every session's
// card read from the state directory, and records where it listens in
// <state>/view.json; -stop ends the recorded one. Every other verb is a client
// of the generated harness operations, reaching the host at the recorded
// endpoint or -endpoint. Claude Code calls the same binary for each session gate:
//
//	csf gate PreToolUse <run directory>   < hook input
//
// A run lives in <state>/<assignment id>: events.jsonl, settings.json,
// run.json, the worktree and, while the session runs, its Bazel output base.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/candacelabs/csf/csf"
	"github.com/candacelabs/csf/ipc/db/csfpg"
	"github.com/candacelabs/csf/ipc/docker"
	ipcfs "github.com/candacelabs/csf/ipc/fs"
	"github.com/candacelabs/csf/ipc/model/claudecode"
	ipcnet "github.com/candacelabs/csf/ipc/net"
	ipchttp "github.com/candacelabs/csf/ipc/net/http"
	"github.com/candacelabs/csf/ipc/proc"
	"github.com/candacelabs/csf/pkg/httpserver"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
	"github.com/candacelabs/csf/runtime"
	cronservice "github.com/candacelabs/csf/services/cron"
	"github.com/candacelabs/csf/services/harness"
	"github.com/candacelabs/csf/services/harness/chat"
	"github.com/candacelabs/csf/services/harness/routing"
	"github.com/candacelabs/csf/services/harness/session"
	"github.com/candacelabs/csf/services/harness/sessiongate"
	"github.com/candacelabs/csf/services/housekeeping"
)

const (
	commandName = "csf"
	// deprecatedName is the binary's former name, kept working for the
	// session gates and scripts that still call it.
	deprecatedName = "harness"

	verbServe  = "serve"
	verbSubmit = "submit"
	verbSend   = "send"
	verbList   = "list"
	verbGet    = "get"
	verbCancel = "cancel"
	verbEvents = "events"
	verbStop   = "stop"
	verbGate   = "gate"

	verbHousekeeping = "housekeeping"
	dryRunFlag       = "dry-run"
	triggerFlag      = "trigger"
	scratchFlag      = "scratch"
	// databaseConfigFlag names the csfpg settings file the cron store opens.
	databaseConfigFlag = "database-config"
	// HousekeepingRecordFile is where every housekeeping record is appended,
	// one JSON object per line.
	HousekeepingRecordFile = "housekeeping.jsonl"
	processTableDirectory  = "/proc"

	recipeFlag      = "recipe"
	stateFlag       = "state"
	claudeFlag      = "claude"
	listenFlag      = "listen"
	originFlag      = "origin"
	detachFlag      = "detach"
	endpointFlag    = "endpoint"
	assignmentFlag  = "assignment"
	messageFlag     = "message"
	messageFileFlag = "message-file"
	fromFlag        = "from"

	defaultStateDirectory = ".local/state/csf/harness"
	defaultListen         = "127.0.0.1:14120"
	// HostRecordFile is where serve records its endpoint and pid, for the
	// client verbs and for the operator.
	HostRecordFile = "harness.json"
	hostLogFile    = "harness.log"
	httpService    = "harness"
	hostName       = "harness"
	mcpPath        = "/mcp"
	metricsPath    = "/metrics"
	cachePath      = "/api/harness/cache"
	schemeHTTP     = "http://"
	clientTimeout  = 30 * time.Second
	hostFileMode   = 0o600

	// exitBlocked is the hook exit status Claude Code reads as "block this
	// call": a gate that cannot read its input fails closed.
	exitBlocked = 2
	exitFailed  = 1
)

var (
	errUsage     = errors.New("usage: csf init|serve|submit|send|chat|list|get|cancel|events|stop|view|housekeeping ..., or csf gate EVENT RUN_DIRECTORY")
	errNoTrigger = errors.New("-trigger is required: " + strings.Join([]string{
		housekeeping.TriggerDiskFloor, housekeeping.TriggerSessions, housekeeping.TriggerDocker, housekeeping.TriggerSharedCache}, ", "))
	// errBriefConflict reports a recipe with both an inline task and a brief.
	errBriefConflict = errors.New("the recipe sets both task and workspace.brief_path; keep one")
	errMessageBoth   = errors.New("give -message or -message-file, not both")
	errNoMessage     = errors.New("give -message or -message-file")
	errNoAssignment  = errors.New("-assignment is required")
	errNoHost        = errors.New("no harness is recorded as running; run csf init in your repository, or give -endpoint")
)

// hostRecord is what serve writes to <state>/harness.json while it runs.
type hostRecord struct {
	PID       int       `json:"pid"`
	Endpoint  string    `json:"endpoint"`
	Listen    []string  `json:"listen"`
	StartedAt time.Time `json:"started_at"`
}

// listFlag collects a repeatable flag.
type listFlag []string

func (list *listFlag) String() string { return strings.Join(*list, ",") }

func (list *listFlag) Set(value string) error {
	if value == "" {
		return errors.New("empty value")
	}
	*list = append(*list, value)
	return nil
}

func main() {
	// The gate stays silent: its output is the hook's decision.
	if filepath.Base(os.Args[0]) == deprecatedName && (len(os.Args) < 2 || os.Args[1] != verbGate) {
		fmt.Fprintf(os.Stderr, "%s: %s is now %s; the old name is deprecated\n", deprecatedName, deprecatedName, commandName)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := dispatch(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func dispatch(ctx context.Context, arguments []string, input io.Reader, output io.Writer, diagnostics io.Writer) int {
	launcher, err := proc.NewHostLauncher()
	if err != nil {
		fmt.Fprintf(diagnostics, "%s: %v\n", commandName, err)
		return exitFailed
	}
	if len(arguments) == 0 {
		fmt.Fprintln(diagnostics, errUsage)
		return exitFailed
	}
	verb, rest := arguments[0], arguments[1:]
	if verb == verbGate {
		return gate(ctx, launcher, rest, input, output, diagnostics)
	}
	if err := run(ctx, launcher, verb, rest, input, output, diagnostics); err != nil {
		fmt.Fprintf(diagnostics, "%s %s: %v\n", commandName, verb, err)
		return exitFailed
	}
	return 0
}

func run(ctx context.Context, launcher *proc.HostLauncher, verb string, arguments []string, input io.Reader, output io.Writer, diagnostics io.Writer) error {
	switch verb {
	case verbInit:
		return initRepository(ctx, launcher, arguments, output)
	case verbChat:
		return chatAddress(arguments, output)
	case verbServe:
		return serve(ctx, launcher, arguments, output, diagnostics)
	case verbView:
		return view(ctx, launcher, arguments, output, diagnostics)
	case verbSubmit, verbSend, verbList, verbGet, verbCancel, verbEvents, verbStop:
		return call(ctx, verb, arguments, input, output)
	case verbHousekeeping:
		return housekeep(ctx, launcher, arguments, output, diagnostics)
	}
	return errUsage
}

// serve is the host app.
func serve(ctx context.Context, launcher *proc.HostLauncher, arguments []string, output io.Writer, diagnostics io.Writer) error {
	flags := flag.NewFlagSet(commandName+" "+verbServe, flag.ContinueOnError)
	var listens, origins listFlag
	flags.Var(&listens, listenFlag, "address to serve on; repeat for several (default "+defaultListen+")")
	flags.Var(&origins, originFlag, "extra browser Origin to accept for the chat; each listen address is accepted already")
	stateDirectory := flags.String(stateFlag, "", "directory holding each run as <assignment id>/ (default ~/"+defaultStateDirectory+")")
	claude := flags.String(claudeFlag, claudecode.DefaultExecutable, "Claude Code executable")
	detach := flags.Bool(detachFlag, false, "start the host in the background, logging to <state>/"+hostLogFile+", and return")
	databaseConfig := flags.String(databaseConfigFlag, "", "csfpg settings JSON; mounts the cron service with the housekeeping triggers")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errUsage
	}
	if len(listens) == 0 {
		listens = listFlag{defaultListen}
	}
	state, err := stateRoot(*stateDirectory)
	if err != nil {
		return err
	}
	if *detach {
		return detachHost(launcher, state, verbServe, hostLogFile, arguments, output)
	}
	logger := slog.New(slog.NewJSONHandler(diagnostics, nil))
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate the gate command: %w", err)
	}
	router, err := routing.NewRouter(routing.WithStateDirectory(state))
	if err != nil {
		return err
	}
	runner, err := session.NewAgentSessionRunner(
		session.WithLauncher(launcher),
		session.WithStateDirectory(state),
		session.WithGateCommand(executable, verbGate),
		session.WithClaudeExecutable(*claude),
		session.WithRouter(router),
	)
	if err != nil {
		return err
	}
	// The host owns its shutdown: StopHarness is granted this cancel.
	serving, requestStop := context.WithCancelCause(ctx)
	defer requestStop(nil)
	sessions, err := harness.NewAgentSessionService(
		harness.WithSessionRunner(runner),
		harness.WithServiceLogger(logger),
		harness.WithHostPID(os.Getpid()),
		harness.WithStopRequest(func() { requestStop(errors.New("stop requested through StopHarness")) }),
	)
	if err != nil {
		return err
	}
	api, err := csf.New(csf.WithAgentSessions(sessions))
	if err != nil {
		return err
	}
	for _, address := range listens {
		origins = append(origins, schemeHTTP+address)
	}
	chatPage, err := chat.NewChat(sessions, origins, logger)
	if err != nil {
		return err
	}
	engine := httpserver.NewEngine(httpService, httpserver.WithRequestLogging())
	api.Register(engine)
	sessions.Register(engine)
	chatPage.Register(engine)
	engine.Any(mcpPath, gin.WrapH(api.MCPHandler()))
	registerCacheMeasurement(engine, state)

	host, err := runtime.NewHostRuntime(runtime.WithHostName(hostName), runtime.WithLogger(logger))
	if err != nil {
		return err
	}
	if err := host.Mount("sessions", sessions); err != nil {
		return err
	}
	if *databaseConfig == "" {
		logger.Warn("harness: no -" + databaseConfigFlag + ", so the cron service and its housekeeping triggers are not mounted")
	} else {
		closeCron, err := mountHousekeeping(ctx, host, *databaseConfig, state, launcher, logger, sessions)
		if err != nil {
			return err
		}
		defer closeCron()
	}
	if err := host.Mount("chat", chatPage); err != nil {
		return err
	}
	network := ipcnet.NewHostNetwork()
	for _, address := range listens {
		listener, err := ipchttp.NewHTTPListener(network, address, engine)
		if err != nil {
			return err
		}
		if err := host.Mount("http "+address, listener); err != nil {
			return err
		}
	}
	// Mounted last, so it runs once every listener is bound and is removed
	// first on shutdown: the record is true exactly while the host answers.
	record := hostRecord{PID: os.Getpid(), Endpoint: schemeHTTP + listens[0], Listen: listens, StartedAt: time.Now().UTC()}
	if err := host.Mount("record", runtime.ServiceFunc(func(scope *runtime.Scope) error {
		if err := writeHostRecord(state, HostRecordFile, record); err != nil {
			return err
		}
		fmt.Fprintf(output, "%s: pid %d serving on %s\n", commandName, record.PID, strings.Join(listens, " "))
		return scope.GoOwner("record", func(ctx context.Context) error {
			<-ctx.Done()
			return os.Remove(filepath.Join(state, HostRecordFile))
		})
	})); err != nil {
		return err
	}
	return host.Run(serving)
}

// registerCacheMeasurement serves the fleet's prompt-cache measurement: the
// gauges at metricsPath and the dashboard's JSON at cachePath, both measured
// from the runs' result events at each request.
func registerCacheMeasurement(engine gin.IRouter, state string) {
	registry := prometheus.NewRegistry()
	registry.MustRegister(routing.NewCollector(state))
	engine.GET(metricsPath, gin.WrapH(promhttp.HandlerFor(registry, promhttp.HandlerOpts{})))
	engine.GET(cachePath, func(request *gin.Context) {
		report, err := routing.Measure(state)
		if err != nil {
			request.String(http.StatusInternalServerError, "%s", err.Error())
			return
		}
		request.JSON(http.StatusOK, report)
	})
}

// mountHousekeeping mounts the cron service, after the sessions it lists,
// with the default housekeeping triggers, which hold the sessions'
// admission while free disk is below their floor. The cron store is CSF's
// schema on the database the settings file names; the returned function
// closes the pool and the Docker client after the host has stopped.
func mountHousekeeping(ctx context.Context, host *runtime.HostRuntime, settingsPath string, state string,
	launcher *proc.HostLauncher, logger *slog.Logger, sessions *harness.AgentSessionService) (func(), error) {
	content, err := os.ReadFile(settingsPath)
	if err != nil {
		return nil, err
	}
	var settings csfpg.Settings
	if err := json.Unmarshal(content, &settings); err != nil {
		return nil, fmt.Errorf("decode %s: %w", settingsPath, err)
	}
	pool, err := csfpg.OpenPool(ctx, settings)
	if err != nil {
		return nil, err
	}
	containers, err := docker.NewContainerHost()
	if err != nil {
		pool.Close()
		return nil, err
	}
	closeAll := func() {
		_ = containers.Close()
		pool.Close()
	}
	schemaHandle := pool.OpenSQL()
	err = errors.Join(csfpg.ApplySchema(ctx, schemaHandle), schemaHandle.Close())
	if err == nil {
		err = mountScheduler(host, pool, state, launcher, containers, logger, sessions)
	}
	if err != nil {
		closeAll()
		return nil, err
	}
	return closeAll, nil
}

func mountScheduler(host *runtime.HostRuntime, pool csfpg.IDB, state string, launcher *proc.HostLauncher,
	containers *docker.ContainerHost, logger *slog.Logger, sessions *harness.AgentSessionService) error {
	housekeeper, err := newHousekeeper(state, launcher, containers, logger,
		func(ctx context.Context) (*harnessv1.ListAgentSessionsResponse, error) {
			return sessions.List(ctx, &harnessv1.ListAgentSessionsRequest{})
		},
		housekeeping.WithAdmission(sessions))
	if err != nil {
		return err
	}
	triggers, err := housekeeper.Triggers()
	if err != nil {
		return err
	}
	store, err := cronservice.NewStore(pool)
	if err != nil {
		return err
	}
	scheduler, err := cronservice.NewScheduler(append([]cronservice.Option{cronservice.WithStore(store)}, triggers...)...)
	if err != nil {
		return err
	}
	return host.Mount("cron", scheduler)
}

// newHousekeeper grants the housekeeping service this host's capabilities:
// the state directory, the process table as /proc, the launcher, the
// Docker Engine and the record file every record is appended to.
func newHousekeeper(state string, launcher *proc.HostLauncher, containers *docker.ContainerHost, logger *slog.Logger,
	list housekeeping.SessionList, options ...housekeeping.HousekeeperOption) (*housekeeping.Housekeeper, error) {
	if err := os.MkdirAll(state, 0o700); err != nil {
		return nil, err
	}
	stateFiles, err := ipcfs.NewHostFiles(state)
	if err != nil {
		return nil, err
	}
	processes, err := ipcfs.NewHostFiles(processTableDirectory)
	if err != nil {
		return nil, err
	}
	recordPath := filepath.Join(state, HousekeepingRecordFile)
	return housekeeping.NewHousekeeper(append([]housekeeping.HousekeeperOption{
		housekeeping.WithState(state, stateFiles),
		housekeeping.WithSessions(list),
		housekeeping.WithLauncher(launcher),
		housekeeping.WithContainers(containers),
		housekeeping.WithProcessTable(processes),
		housekeeping.WithLogger(logger),
		housekeeping.WithRecordSink(func(record housekeeping.Record) error { return appendRecord(recordPath, record) }),
	}, options...)...)
}

// appendRecord appends one housekeeping record to the record file.
func appendRecord(path string, record housekeeping.Record) error {
	content, err := json.Marshal(record)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, hostFileMode)
	if err != nil {
		return err
	}
	_, err = file.Write(append(content, '\n'))
	return errors.Join(err, file.Close())
}

// housekeep runs the named housekeeping triggers once, now, against the
// running host's sessions, and prints every record as it is written. With
// -dry-run it deletes nothing and prints the plan.
func housekeep(ctx context.Context, launcher *proc.HostLauncher, arguments []string, output io.Writer, diagnostics io.Writer) error {
	flags := flag.NewFlagSet(commandName+" "+verbHousekeeping, flag.ContinueOnError)
	var triggers listFlag
	flags.Var(&triggers, triggerFlag, "trigger to run once; repeat for several")
	dryRun := flags.Bool(dryRunFlag, false, "measure and print the plan; delete nothing")
	scratch := flags.String(scratchFlag, "", "scratch directory whose entries are reported, never deleted")
	endpoint := flags.String(endpointFlag, "", "harness address (default: the one <state>/"+HostRecordFile+" records)")
	stateDirectory := flags.String(stateFlag, "", "state directory (default ~/"+defaultStateDirectory+")")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errUsage
	}
	if len(triggers) == 0 {
		return errNoTrigger
	}
	state, err := stateRoot(*stateDirectory)
	if err != nil {
		return err
	}
	target, err := resolveEndpoint(*endpoint, state)
	if err != nil {
		return err
	}
	client, err := csf.NewClient(target, &http.Client{Timeout: clientTimeout})
	if err != nil {
		return err
	}
	containers, err := docker.NewContainerHost()
	if err != nil {
		return err
	}
	defer func() { _ = containers.Close() }()
	options := []housekeeping.HousekeeperOption{}
	if *dryRun {
		options = append(options, housekeeping.WithDryRun())
	}
	if *scratch != "" {
		scratchFiles, err := ipcfs.NewHostFiles(*scratch)
		if err != nil {
			return err
		}
		options = append(options, housekeeping.WithScratch(scratchFiles.Directory(), scratchFiles))
	}
	recordPath := filepath.Join(state, HousekeepingRecordFile)
	encoder := json.NewEncoder(output)
	options = append(options, housekeeping.WithRecordSink(func(record housekeeping.Record) error {
		return errors.Join(encoder.Encode(record), appendRecord(recordPath, record))
	}))
	housekeeper, err := newHousekeeper(state, launcher, containers, slog.New(slog.NewJSONHandler(diagnostics, nil)),
		func(ctx context.Context) (*harnessv1.ListAgentSessionsResponse, error) {
			return client.ListAgentSessions(ctx, &harnessv1.ListAgentSessionsRequest{})
		}, options...)
	if err != nil {
		return err
	}
	var failures []error
	for _, trigger := range triggers {
		failures = append(failures, housekeeper.Run(ctx, trigger))
	}
	return errors.Join(failures...)
}

// detachHost starts verb again, without -detach, as a child in its own
// process group that outlives this command, logging to logFile under the
// state directory, and reports its pid.
func detachHost(launcher *proc.HostLauncher, state string, verb string, logFile string, arguments []string, output io.Writer) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(state, 0o700); err != nil {
		return err
	}
	logPath := filepath.Join(state, logFile)
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, hostFileMode)
	if err != nil {
		return err
	}
	defer func() { _ = log.Close() }()
	child := []string{verb}
	for _, argument := range arguments {
		if argument == "-"+detachFlag || argument == "--"+detachFlag {
			continue
		}
		child = append(child, argument)
	}
	process, err := launcher.Start(context.Background(), proc.Command{Executable: executable, Arguments: child, Stdout: log, Stderr: log})
	if err != nil {
		return err
	}
	stop := commandName + " " + verbStop
	if verb == verbView {
		stop = commandName + " " + verbView + " -" + stopFlag
	}
	fmt.Fprintf(output, "%s: serving in the background as pid %d; log %s; stop with `%s`\n", commandName, process.Pid(), logPath, stop)
	return nil
}

// writeHostRecord replaces the named record under the state directory
// atomically, so a reader never sees a half-written one.
func writeHostRecord(state string, name string, record hostRecord) error {
	content, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomically(filepath.Join(state, name), string(content), hostFileMode)
}

// call is every client verb: one generated operation, or the event stream.
func call(ctx context.Context, verb string, arguments []string, input io.Reader, output io.Writer) error {
	flags := flag.NewFlagSet(commandName+" "+verb, flag.ContinueOnError)
	endpoint := flags.String(endpointFlag, "", "harness address (default: the one <state>/"+HostRecordFile+" records)")
	stateDirectory := flags.String(stateFlag, "", "state directory holding "+HostRecordFile+" (default ~/"+defaultStateDirectory+")")
	recipePath := flags.String(recipeFlag, "", "typed agent.json recipe to submit")
	assignment := flags.String(assignmentFlag, "", "assignment identifier")
	message := flags.String(messageFlag, "", "message to send into the session")
	messageFile := flags.String(messageFileFlag, "", "file holding the message")
	from := flags.Int(fromFlag, 0, "skip this many records of the event stream")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errUsage
	}
	target, err := resolveEndpoint(*endpoint, *stateDirectory)
	if err != nil {
		return err
	}
	client, err := csf.NewClient(target, &http.Client{Timeout: clientTimeout})
	if err != nil {
		return err
	}
	switch verb {
	case verbSubmit:
		if *recipePath == "" {
			return errUsage
		}
		recipe, err := readRecipe(*recipePath)
		if err != nil {
			return err
		}
		response, err := client.SubmitAgentSession(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: recipe})
		return print(output, response, err)
	case verbSend:
		if *assignment == "" {
			return errNoAssignment
		}
		text, err := messageText(*message, *messageFile, input)
		if err != nil {
			return err
		}
		response, err := client.SendAgentSessionMessage(ctx, &harnessv1.SendAgentSessionMessageRequest{AssignmentId: *assignment, Message: text})
		return print(output, response, err)
	case verbList:
		response, err := client.ListAgentSessions(ctx, &harnessv1.ListAgentSessionsRequest{})
		return print(output, response, err)
	case verbGet:
		if *assignment == "" {
			return errNoAssignment
		}
		response, err := client.GetAgentSession(ctx, &harnessv1.GetAgentSessionRequest{AssignmentId: *assignment})
		return print(output, response, err)
	case verbCancel:
		if *assignment == "" {
			return errNoAssignment
		}
		response, err := client.CancelAgentSession(ctx, &harnessv1.CancelAgentSessionRequest{AssignmentId: *assignment})
		return print(output, response, err)
	case verbStop:
		response, err := client.StopHarness(ctx, &harnessv1.StopHarnessRequest{})
		return print(output, response, err)
	case verbEvents:
		if *assignment == "" {
			return errNoAssignment
		}
		latestTurn := func(ctx context.Context) (int64, error) {
			response, err := client.GetAgentSession(ctx, &harnessv1.GetAgentSessionRequest{AssignmentId: *assignment})
			return int64(response.GetSession().GetTurns()), err
		}
		return followEvents(ctx, target, *assignment, *from, latestTurn, output)
	}
	return errUsage
}

// print writes an operation's response as proto JSON, or returns its error.
func print[Response proto.Message](output io.Writer, response Response, err error) error {
	if err != nil {
		return err
	}
	content, err := (protojson.MarshalOptions{UseProtoNames: true, Indent: "  "}).Marshal(response)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, string(content))
	return err
}

// followEvents prints the session's records, one JSON object per line, until
// the session's latest turn finishes, the stream ends or ctx does. A finished
// session stays open for its next message, so the stream alone never ends:
// at each finished turn, latestTurn says whether a newer one has started.
func followEvents(ctx context.Context, endpoint string, assignment string, from int,
	latestTurn func(ctx context.Context) (int64, error), output io.Writer) error {
	url := fmt.Sprintf("%s/api/harness/sessions/%s/events?from=%d", strings.TrimRight(endpoint, "/"), assignment, from)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("harness returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 0, 64<<10), 8<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if data, found := strings.CutPrefix(line, "data:"); found {
			data = strings.TrimSpace(data)
			if _, err := fmt.Fprintln(output, data); err != nil {
				return err
			}
			finished, err := latestTurnFinished(ctx, data, latestTurn)
			if err != nil || finished {
				return err
			}
		}
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}

// latestTurnFinished reports whether the record finishes the session's
// latest turn: a run-finished record whose turn no newer turn has followed.
func latestTurnFinished(ctx context.Context, data string, latestTurn func(ctx context.Context) (int64, error)) (bool, error) {
	var record struct {
		EventType string `json:"event_type"`
		Turn      int64  `json:"turn"`
	}
	if json.Unmarshal([]byte(data), &record) != nil || record.EventType != session.EventTypeRunFinished {
		return false, nil
	}
	latest, err := latestTurn(ctx)
	if err != nil {
		return false, err
	}
	return record.Turn >= latest, nil
}

// resolveEndpoint is -endpoint, or the endpoint the running host recorded.
func resolveEndpoint(endpoint string, stateDirectory string) (string, error) {
	if endpoint != "" {
		return endpoint, nil
	}
	state, err := stateRoot(stateDirectory)
	if err != nil {
		return "", err
	}
	content, err := os.ReadFile(filepath.Join(state, HostRecordFile))
	if errors.Is(err, os.ErrNotExist) {
		return "", errNoHost
	}
	if err != nil {
		return "", err
	}
	var record hostRecord
	if err := json.Unmarshal(content, &record); err != nil {
		return "", fmt.Errorf("decode %s: %w", HostRecordFile, err)
	}
	if record.Endpoint == "" {
		return "", errNoHost
	}
	return record.Endpoint, nil
}

// readRecipe reads the recipe and, when its workspace names a brief file,
// the task from that file, so the plan's fingerprint covers the brief.
func readRecipe(path string) (*pb.AgentAssignmentRecipe, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read recipe: %w", err)
	}
	recipe := &pb.AgentAssignmentRecipe{}
	if err := protojson.Unmarshal(content, recipe); err != nil {
		return nil, fmt.Errorf("decode recipe %s: %w", path, err)
	}
	brief := recipe.GetWorkspace().GetBriefPath()
	if brief == "" {
		return recipe, nil
	}
	if recipe.GetTask() != "" {
		return nil, errBriefConflict
	}
	if !filepath.IsAbs(brief) {
		brief = filepath.Join(filepath.Dir(path), brief)
	}
	task, err := os.ReadFile(brief)
	if err != nil {
		return nil, fmt.Errorf("read brief: %w", err)
	}
	recipe.Task = string(task)
	return recipe, nil
}

func stateRoot(directory string) (string, error) {
	if directory == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("default state directory: %w", err)
		}
		directory = filepath.Join(home, defaultStateDirectory)
	}
	return filepath.Abs(directory)
}

// messageText is -message, the contents of -message-file, or standard input
// when the file is "-".
func messageText(message string, messageFile string, input io.Reader) (string, error) {
	switch {
	case message != "" && messageFile != "":
		return "", errMessageBoth
	case message != "":
		return message, nil
	case messageFile == "-":
		content, err := io.ReadAll(input)
		return string(content), err
	case messageFile != "":
		content, err := os.ReadFile(messageFile)
		if err != nil {
			return "", fmt.Errorf("read message: %w", err)
		}
		return string(content), nil
	}
	return "", errNoMessage
}

// gate answers one hook call from Claude Code. Its output, when there is
// one, is the decision; a gate that cannot read its input or its run blocks
// the call.
func gate(ctx context.Context, launcher *proc.HostLauncher, arguments []string, input io.Reader, output io.Writer, diagnostics io.Writer) int {
	if len(arguments) != 2 {
		fmt.Fprintln(diagnostics, errUsage)
		return exitBlocked
	}
	event, directory := arguments[0], arguments[1]
	sessionGate, err := sessiongate.NewSessionGate(sessiongate.WithLauncher(launcher), sessiongate.WithRunDirectory(directory))
	if err != nil {
		fmt.Fprintf(diagnostics, "%s gate: %v\n", commandName, err)
		return exitBlocked
	}
	hookInput, err := io.ReadAll(input)
	if err != nil {
		fmt.Fprintf(diagnostics, "%s gate: read hook input: %v\n", commandName, err)
		return exitBlocked
	}
	decision, err := sessionGate.Handle(ctx, event, hookInput)
	if decision != nil {
		content, encodeErr := json.Marshal(decision)
		if encodeErr != nil {
			fmt.Fprintf(diagnostics, "%s gate: %v\n", commandName, encodeErr)
			return exitBlocked
		}
		fmt.Fprintln(output, string(content))
		return 0
	}
	if err != nil {
		fmt.Fprintf(diagnostics, "%s gate: %v\n", commandName, strings.TrimSpace(err.Error()))
		return exitBlocked
	}
	return 0
}

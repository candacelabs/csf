// Copyright 2026 Candace Labs

// Package verbs is every verb of csf, the one application (#600): one process
// per machine that runs every agent session, and the thin client that talks
// to it. app/csf/cmd is only the process around [Main]. The harness and the
// knowledge, simulation and Copilot Workbench host were two binaries, both
// installed as csf; they are this one, the host's entry points under csf host.
//
//	csf host serve|mcp|initialize [flags] | call OPERATION | link | links ... | spine
//	csf bootstrap -repo DIR -local | csf ARCHIVE.tar.gz [ARGS...]
//	csf decide ... | csf scoreboard ...
//	csf init [-repo DIR] [-state DIR] [-listen ADDR]
//	csf serve -listen 127.0.0.1:14120 [-listen ADDR]... [-redirect ADDR]... [-state DIR] [-claude PATH] [-copilot PATH] [-executor claude-code|copilot] [-model MODEL] [-database-config FILE] [-ouroboros-repository DIR] [-ouroboros-fixers] [-ouroboros-corpus DIR] [-workbench-recipes DIR] [-detach]
//	csf submit -recipe agent.json | check -recipe agent.json
//	csf send -assignment ID [-operator] -message TEXT | -message-file FILE
//	csf propose -assignment ID -diff FILE -message TEXT
//	csf executor [-executor claude-code|copilot [-model MODEL]]
//	csf chat -assignment ID
//	csf list | get -assignment ID | cancel -assignment ID | ready -assignment ID | merge -assignment ID | events -assignment ID [-from N] | stop [-deadline D]
//	csf await CONDITION -deadline D [-pid N] [-assignment ID] [-phase P] [-turn N] [-pull-request PR] [-repository R] [-url U] [-status S] [-load L]
//	csf housekeeping -trigger NAME [-trigger NAME]... [-dry-run] [-scratch DIR]
//	csf ouroboros precheck -repository DIR [-state DIR] [-ticket N]...
//	csf slice add -ticket URL -recipe DIR [-slice ID] [-title T] [-depends-on ID]... [-contends ID]... [-hotspot NAME]... [-path GLOB]...
//	csf slice hold|release -slice ID -reason TEXT | pause|resume -reason TEXT | status
//	csf slice record|verify -assignment ID [-state DIR]
//	csf label -repo OWNER/NAME -model-container NAME (-ticket N | -tickets-file FILE)... [-model NAME] [-model-endpoint URL] [-checkout DIR] [-miner NAME=EXE]... [-batches N] [-think]
//	csf db status|start|stop [-state DIR]
//	csf docs link [-repo DIR] [PATH...]
//	csf endpoint retire -address HOST:PORT -moved-to HOST:PORT -acknowledgement TEXT [-state DIR]
//	csf upgrade [-commit SHA | -rollback] [-binary PATH] [-state DIR]
//	csf status [AGENT]... [-state DIR]
//	csf tail [AGENT]... [-kinds KINDS] [-n N] [-state DIR]
//
// init, run at the root of any git repository, writes the sample assignment
// under .csf/assignments/sample and registers with the running host, or
// starts one, recording the repository as the mining loop's checkout when
// none is recorded yet. chat prints a session's chat address. serve is the
// host app: it brings up CSF's own database (a PostgreSQL container it owns,
// recorded in <state>/database.json; -database-config names someone else's
// instead), builds the one host runtime, mounts the session service, the cron
// service with the default housekeeping triggers (backups of the owned
// database hourly, and the -widgets checkout fast-forwarded every minute and
// after every merge), the mining loop with its four triggers (its checkout
// and corpus from -ouroboros-repository and -ouroboros-corpus, or as
// recorded in <state>/ouroboros-settings.json; -ouroboros-fixers lets it
// launch fixer sessions), the slice dispatcher and its pass every minute
// (csf slice add puts work in its graph), the chat, the Workbench at / (the operator's
// control plane: every session's card, live from the state directory, with
// its actions, and the launch form over the -workbench-recipes templates),
// the generated operations over HTTP and at /mcp, and one HTTP listener per
// address, plus one per -redirect alias that redirects every request to the
// same path on the first address's port, and records where it listens in
// <state>/harness.json. It refuses to start, before detaching, when an address
// of the endpoint registry (<state>/endpoints/registry.json) is neither served
// nor retired, and registers every address it serves once it listens; endpoint
// retire is the operator's record that an address is retired. Every
// session is given that /mcp as its csf MCP server. housekeeping runs named
// triggers once, now, against the running host's sessions, and prints every
// record it writes. label runs the ouroboros labeler over the mining tickets
// named, with the local model, and records every label under
// <state>/labeler.jsonl and the run under <state>/labeler.json for the
// Workbench's panel. db reports, starts or stops the owned database; stop
// leaves it running. stop returns once the host process has exited, as
// serve -detach returns once the host is ready; await waits for one
// condition up to its deadline, through the host's Await operation or, for
// harness-ready and harness-stopped, in the client. docs link links the ontology terms the README lint
// reports, in the named files or every tracked README. upgrade installs the
// csf binary CI released for a commit, or latest main, after checking its
// SHA-256, keeps the one it replaced for upgrade -rollback, and restarts the
// running host with the argument vector harness.json records, returning once
// the Workbench answers. status prints every session as one table row over the
// session_progress projection, and tail prints the lines its event logs fold
// to, labelled by agent; both read the state directory directly, never the
// host. Every other verb is a
// client of the generated harness
// operations, reaching the host at the recorded endpoint or -endpoint. Claude
// Code calls the same binary for each session gate:
//
//	csf gate PreToolUse <run directory>   < hook input
//
// A Copilot session calls it the same way, through a plugin holding the same
// hooks. csf install-gates [-home DIR] hooks the operator's own Claude Code
// and Copilot sessions up to the gate, called on the home directory, which
// holds no run, so the gate answers in operator mode:
//
//	csf gate PreToolUse <home>   < hook input
//
// A run lives in <state>/<assignment id>: events.jsonl, settings.json,
// run.json, the worktree and, while the session runs, its Bazel output base.
package verbs

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/candacelabs/csf/csf"
	"github.com/candacelabs/csf/csf/githubtools"
	"github.com/candacelabs/csf/io/ipc/db/csfpg"
	"github.com/candacelabs/csf/io/ipc/docker"
	"github.com/candacelabs/csf/io/ipc/proc"
	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/io/kernel/sandbox"
	ionet "github.com/candacelabs/csf/io/net"
	"github.com/candacelabs/csf/io/net/github"
	iohttp "github.com/candacelabs/csf/io/net/http"
	"github.com/candacelabs/csf/io/net/model/claudecode"
	"github.com/candacelabs/csf/io/net/model/copilotcli"
	"github.com/candacelabs/csf/pkg/atomicfile"
	"github.com/candacelabs/csf/pkg/httpserver"
	"github.com/candacelabs/csf/pkg/pretty"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
	"github.com/candacelabs/csf/runtime"
	"github.com/candacelabs/csf/runtime/config"
	"github.com/candacelabs/csf/services/await"
	"github.com/candacelabs/csf/services/bootstrap"
	cronservice "github.com/candacelabs/csf/services/cron"
	"github.com/candacelabs/csf/services/database"
	dispatchservice "github.com/candacelabs/csf/services/dispatch"
	"github.com/candacelabs/csf/services/harness"
	"github.com/candacelabs/csf/services/harness/chat"
	"github.com/candacelabs/csf/services/harness/endpoint"
	"github.com/candacelabs/csf/services/harness/provider"
	"github.com/candacelabs/csf/services/harness/routing"
	"github.com/candacelabs/csf/services/harness/session"
	"github.com/candacelabs/csf/services/harness/sessiongate"
	"github.com/candacelabs/csf/services/housekeeping"
	"github.com/candacelabs/csf/services/opsview"
	"github.com/candacelabs/csf/services/ouroboros"
	"github.com/candacelabs/csf/services/ouroboros/evaluate"
)

const (
	commandName = "csf"

	verbServe   = "serve"
	verbSubmit  = "submit"
	verbSend    = "send"
	verbPropose = "propose"
	verbList    = "list"
	verbGet     = "get"
	verbInbox   = "inbox"
	verbQueue   = "queue"
	verbCancel  = "cancel"
	verbEvents  = "events"
	verbStop    = "stop"
	verbGate    = "gate"
	// verbExecutor reads or switches the default executor.
	verbExecutor = "executor"
	// verbInstallGates hooks the operator's own sessions up to the wait gate.
	verbInstallGates = "install-gates"
	verbCheck        = "check"
	verbReady        = "ready"
	verbMerge        = "merge"
	// recipesFlag names the directory of recipe templates the Workbench's
	// launch form offers, one <name>/agent.json each.
	recipesFlag = "workbench-recipes"

	verbHousekeeping = "housekeeping"
	dryRunFlag       = "dry-run"
	triggerFlag      = "trigger"
	scratchFlag      = "scratch"
	// databaseConfigFlag names the csfpg settings file the cron store opens.
	databaseConfigFlag = "database-config"
	// ouroborosRepositoryFlag names the checkout the mining loop reads its
	// miners from, creates fixer worktrees from and runs the merge path in;
	// giving it mounts the loop, which needs the database too.
	ouroborosRepositoryFlag = "ouroboros-repository"
	// ouroborosFixersFlag is the loop's always-on switch: without it the loop
	// detects, pre-checks, measures and merges but launches no fixer.
	ouroborosFixersFlag = "ouroboros-fixers"
	// ouroborosCorpusFlag names the state directory the miners read when it
	// is not this host's own: a second host measuring another host's runs.
	ouroborosCorpusFlag = "ouroboros-corpus"
	// mergerIdentityFormat names this host as the merge train's principal,
	// which NO-SELF-MERGE compares with the author sessions of a pull request.
	mergerIdentityFormat = "csf-serve/%d"
	ouroborosMountName   = "ouroboros"
	gitDirectory         = "-C"
	gitRemote            = "remote"
	gitGetURL            = "get-url"
	gitOrigin            = "origin"
	// HousekeepingRecordFile is where every housekeeping record is appended,
	// one JSON object per line.
	HousekeepingRecordFile = "housekeeping.jsonl"
	processTableDirectory  = "/proc"

	recipeFlag      = "recipe"
	stateFlag       = "state"
	claudeFlag      = "claude"
	homeFlag        = "home"
	copilotFlag     = "copilot"
	executorFlag    = "executor"
	listenFlag      = "listen"
	originFlag      = "origin"
	detachFlag      = "detach"
	endpointFlag    = "endpoint"
	assignmentFlag  = "assignment"
	messageFlag     = "message"
	messageFileFlag = "message-file"
	operatorFlag    = "operator"
	questionFlag    = "question-wanted"
	diffFileFlag    = "diff"
	fromFlag        = "from"
	outputFlag      = "pretty"

	// launchFlag selects where sessions run: launchContainer (the default)
	// or launchHost, which -sandbox confines.
	launchFlag             = "launch"
	sessionImageFlag       = "session-image"
	executorCredentialHome = ".claude/.credentials.json"
	ghConfigHome           = ".config/gh"
	// executorConversationStore is where the executor saves conversations
	// under this user's home, and the path the provider's harness-owned
	// configuration directory links its projects/ to. Relative to the home,
	// so no operator path is recorded.
	executorConversationStore = ".claude/projects"

	sandboxFlag         = "sandbox"
	sandboxLauncherFlag = "sandbox-launcher"
	sandboxProxyFlag    = "sandbox-proxy"
	sandboxDockerFlag   = "sandbox-docker"
	sandboxImageFlag    = "sandbox-image"
	sandboxReadOnlyFlag = "sandbox-ro"

	defaultStateDirectory = ".local/state/csf/harness"
	defaultListen         = "127.0.0.1:14120"
	// HostRecordFile is where serve records its endpoint and pid, for the
	// client verbs and for the operator.
	HostRecordFile = "harness.json"
	hostLogFile    = "harness.log"
	httpService    = "harness"
	hostName       = "harness"
	mcpPath        = "/mcp"
	cachePath      = "/api/harness/cache"
	schemeHTTP     = "http://"
	clientTimeout  = 30 * time.Second
	// mergeTimeout bounds csf merge: the merge path builds and checks the
	// merged tree before it merges.
	mergeTimeout = 45 * time.Minute
	hostFileMode = 0o600

	// exitBlocked is the hook exit status Claude Code reads as "block this
	// call": a gate that cannot read its input fails closed.
	exitBlocked = 2
	exitFailed  = 1
)

var (
	errUsage     = errors.New("usage: csf init|serve|submit|send|propose|chat|list|get|inbox|cancel|events|stop|await|housekeeping|ouroboros|slice|burst|label|costs|rulings|db|docs|endpoint|upgrade|eval|status|tail|host|bootstrap|decide|scoreboard|deadcode|bazel|publish ..., or csf ARCHIVE.tar.gz [ARGS...], or csf gate EVENT RUN_DIRECTORY, or csf install-gates [-home DIR]")
	errNoTrigger = errors.New("-trigger is required: " + strings.Join([]string{
		housekeeping.TriggerDiskFloor, housekeeping.TriggerSessions, housekeeping.TriggerDocker, housekeeping.TriggerSharedCache,
		housekeeping.TriggerDatabaseBackup, housekeeping.TriggerLiveCheckout}, ", "))
	// errBriefConflict reports a recipe with both an inline task and a brief.
	errBriefConflict = errors.New("the recipe sets both task and workspace.brief_path; keep one")
	errMessageBoth   = errors.New("give -message or -message-file, not both")
	errNoMessage     = errors.New("give -message or -message-file")
	errNoAssignment  = errors.New("-assignment is required")
	errNoHost        = errors.New("no harness is recorded as running; run csf init in your repository, or give -endpoint")
)

// ouroborosSettings is what serve passes the mining loop: the checkout it
// works in and whether it may launch fixers. An empty repository mounts no
// loop.
type ouroborosSettings struct {
	repository string
	corpus     string
	fixers     bool
}

// hostRecord is what serve writes to <state>/harness.json while it runs.
// Argv and Directory are the host's full argument vector and working
// directory, which csf upgrade starts it again with; Binary is the path of
// the executable it started from, read at start, which csf upgrade replaces
// even after the file was replaced underneath the running host.
type hostRecord struct {
	PID       int       `json:"pid"`
	Endpoint  string    `json:"endpoint"`
	Listen    []string  `json:"listen"`
	StartedAt time.Time `json:"started_at"`
	Argv      []string  `json:"argv,omitempty"`
	Binary    string    `json:"binary,omitempty"`
	Directory string    `json:"directory,omitempty"`
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

// Main runs one csf invocation, arguments after the binary name, and returns
// its exit status. The process's signals, streams and exit belong to the
// binary that calls it.
func Main(ctx context.Context, arguments []string, input io.Reader, output io.Writer, diagnostics io.Writer) int {
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
	if verb == verbBazel {
		code, err := bazelVerb(ctx, launcher, config.OSEnvironment(), rest, input, output, diagnostics)
		if err != nil {
			fmt.Fprintf(diagnostics, "%s %s: %v\n", commandName, verb, err)
		}
		return code
	}
	if verb == verbPublish {
		workspace, err := os.MkdirTemp("", commandName+"-"+verbPublish+"-")
		if err == nil {
			defer os.RemoveAll(workspace)
			err = publishVerb(ctx, launcher, workspace, rest, output, diagnostics)
		}
		if err != nil {
			fmt.Fprintf(diagnostics, "%s %s: %v\n", commandName, verb, err)
			return exitFailed
		}
		return 0
	}
	if isArchive(verb) {
		code, err := runArchive(ctx, verb, rest, input, output, diagnostics)
		if err != nil {
			fmt.Fprintf(diagnostics, "%s %s: %v\n", commandName, verb, err)
			if code == 0 {
				code = exitFailed
			}
		}
		return code
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
	case verbSubmit, verbSend, verbPropose, verbList, verbGet, verbInbox, verbCancel, verbEvents, verbStop, verbCheck, verbReady, verbMerge, verbExecutor:
		return call(ctx, verb, arguments, input, output)
	case verbAwait:
		return awaitVerb(ctx, arguments, output)
	case verbHousekeeping:
		return housekeep(ctx, launcher, arguments, output, diagnostics)
	case verbRelay:
		return relay(arguments, output)
	case verbOuroboros:
		return ouroborosVerbs(ctx, launcher, arguments, output)
	case verbSlice:
		return sliceVerbs(ctx, launcher, arguments, output)
	case verbLabel:
		return label(ctx, launcher, arguments, output, diagnostics)
	case verbDatabase:
		return databaseVerbs(ctx, arguments, output)
	case verbBurst:
		return burstVerbs(ctx, arguments, output)
	case verbDocs:
		return docsVerbs(ctx, launcher, arguments, output, diagnostics)
	case verbEndpoint:
		return endpointVerbs(arguments, output)
	case verbCosts:
		return costsVerb(arguments, output)
	case verbObserveStatus:
		return statusVerb(ctx, launcher, arguments, output)
	case verbTail:
		return tailVerb(arguments, output)
	case verbRulings:
		return rulingsVerb(arguments, output)
	case verbInstallGates:
		return installGates(arguments, output)
	case verbUpgrade:
		return upgradeVerb(ctx, launcher, arguments, output)
	case verbGitHub:
		return githubVerb(ctx, launcher, arguments, input, output, diagnostics)
	case verbEval:
		return evalVerbs(ctx, launcher, arguments, output, diagnostics)
	case verbHost:
		return hostVerbs(ctx, arguments, input, output)
	case bootstrapCommand:
		return bootstrapLocal(ctx, arguments, output)
	case decideCommand:
		return decide(ctx, arguments, input, output)
	case scoreboardCommand:
		return scoreboard(arguments, output)
	case verbDeadcode:
		return deadcodeVerb(arguments, output)
	}
	return errUsage
}

// installGates hooks the operator's own Claude Code and Copilot sessions up
// to this binary's wait gate, under -home or the user's home directory.
func installGates(arguments []string, output io.Writer) error {
	flags := flag.NewFlagSet(commandName+" "+verbInstallGates, flag.ContinueOnError)
	home := flags.String(homeFlag, "", "home directory whose executor settings get the gate (default: the user's)")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *home == "" {
		directory, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		*home = directory
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	written, err := session.InstallOperatorGates(*home, []string{executable, verbGate})
	if err != nil {
		return err
	}
	for _, path := range written {
		fmt.Fprintf(output, "gate hook installed: %s\n", path)
	}
	return nil
}

// serve is the host app.
func serve(ctx context.Context, launcher *proc.HostLauncher, arguments []string, output io.Writer, diagnostics io.Writer) error {
	flags := flag.NewFlagSet(commandName+" "+verbServe, flag.ContinueOnError)
	var listens, origins, redirects listFlag
	flags.Var(&listens, listenFlag, "address to serve on; repeat for several (default "+defaultListen+")")
	flags.Var(&redirects, redirectFlag, "alias address that redirects every request to the same path on the first -"+listenFlag+" address's port, so a retired address keeps working; repeat for several")
	flags.Var(&origins, originFlag, "extra browser Origin to accept for the chat; each listen address is accepted already")
	stateDirectory := flags.String(stateFlag, "", "directory holding each run as <assignment id>/ (default ~/"+defaultStateDirectory+")")
	claude := flags.String(claudeFlag, claudecode.DefaultExecutable, "Claude Code executable")
	defaultExecutor := flags.String(executorFlag, string(session.ExecutorClaudeCode), "turn executor a recipe naming none runs on, until switched with SetAgentExecutorDefault: "+string(session.ExecutorClaudeCode)+" or "+string(session.ExecutorCopilot))
	defaultModel := flags.String(modelFlag, "", "model such a recipe runs on, spelled as that executor spells it; empty keeps each recipe's own (Claude Code only)")
	copilot := flags.String(copilotFlag, copilotcli.DefaultExecutable, "GitHub Copilot CLI executable of host sessions, for recipes whose executor is copilot (measured on "+copilotcli.PinnedVersion+"); a container session runs the session image's")
	detach := flags.Bool(detachFlag, false, "start the host in the background, logging to <state>/"+hostLogFile+", and return once it is ready")
	readyDeadline := flags.Duration(deadlineFlag, lifecycleDeadline, "with -"+detachFlag+": how long to wait for the host to be ready")
	databaseConfig := flags.String(databaseConfigFlag, "", "csfpg settings JSON of a database someone else runs (default: CSF's own, provisioned and recorded in <state>/"+database.RecordFile+")")
	_ = flags.Bool(outputFlag, false, "render output as a markdown table instead of JSON (not yet implemented)")
	ouroborosRepository := flags.String(ouroborosRepositoryFlag, "", "checkout the mining loop works in, recorded for later starts (default: the recorded one); mounts the loop")
	ouroborosFixers := flags.Bool(ouroborosFixersFlag, false, "let the mining loop launch fixer sessions; without it the loop only detects, pre-checks, measures and merges")
	ouroborosCorpus := flags.String(ouroborosCorpusFlag, "", "state directory the miners read, recorded for later starts (default: the recorded one, else this host's own)")
	widgets := flags.String(widgetsFlag, "", "widgets directory of the checkout the widget operations read and the Workbench installs from (default ./"+opsview.WidgetsDirectory+"; none served when it does not exist)")
	viewsRepository := flags.String(viewsRepositoryFlag, "", "repository, OWNER/NAME, whose merges and ontology alignment score /metrics reads (default: the mining loop checkout's origin)")
	recipes := flags.String(recipesFlag, "", "directory of recipe templates the Workbench's launch form offers, one <name>/"+recipeFile+" each; without it the page launches nothing")
	launch := flags.String(launchFlag, session.LaunchContainer, "where sessions run: "+session.LaunchContainer+
		" (each in its own container of the session image, no Docker socket) or "+session.LaunchHost+" (on the host, recorded so on each receipt)")
	sessionImageFallback := flags.String(sessionImageFlag, session.DefaultSessionImage, "session image for a worktree that pins none in "+session.SessionImageFile+", such as one created before the pin existed")
	sandboxEnabled := flags.Bool(sandboxFlag, false, "confine each host session in a per-session cgroup under Landlock, seccomp and a filtering Docker proxy; requires -"+launchFlag+" "+session.LaunchHost+" and a delegated cgroup")
	sandboxLauncher := flags.String(sandboxLauncherFlag, "", "path to the csf-session-launcher binary (required with -"+sandboxFlag+")")
	sandboxProxy := flags.String(sandboxProxyFlag, "", "path to the csf-docker-proxy binary (required with -"+sandboxFlag+")")
	sandboxDocker := flags.String(sandboxDockerFlag, session.DefaultDockerUpstream, "the host Docker Engine socket the per-session proxy forwards to")
	var sandboxImages, sandboxReadOnly listFlag
	flags.Var(&sandboxImages, sandboxImageFlag, "a pinned image reference a session may run; repeat for several")
	cloudFlags := addCloudFlags(flags)
	flags.Var(&sandboxReadOnly, sandboxReadOnlyFlag, "an extra read-only path a session may read beyond the system defaults; repeat for several")
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
	// The registry is checked before detaching, so a serve that would retire
	// an address fails in the caller's own terminal.
	served := endpoint.ServeEndpoints(listens, redirects)
	previous, err := readEndpointRegistry(state)
	if err != nil {
		return err
	}
	if err := checkServed(previous, served); err != nil {
		return err
	}
	if *detach {
		return detachHost(ctx, launcher, state, verbServe, hostLogFile, arguments, *readyDeadline, output)
	}
	registry := previous.Register(served, today())
	registry.HostPID = os.Getpid()
	_, primaryPort, err := net.SplitHostPort(listens[0])
	if err != nil {
		return fmt.Errorf("-%s %s: %w", listenFlag, listens[0], err)
	}
	logger := slog.New(slog.NewJSONHandler(diagnostics, nil))
	startedAt := time.Now().UTC()
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate the gate command: %w", err)
	}
	runnerOptions := []session.AgentSessionRunnerOption{
		session.WithLauncher(launcher),
		session.WithStateDirectory(state),
	}
	providerOptions, err := providerSessions(state, *launch == session.LaunchContainer)
	if err != nil {
		return err
	}
	runnerOptions = append(runnerOptions, providerOptions...)
	switch *launch {
	case session.LaunchContainer:
		if *sandboxEnabled {
			return fmt.Errorf("-%s confines host sessions; give -%s %s", sandboxFlag, launchFlag, session.LaunchHost)
		}
		containerOptions, closeContainers, err := containerSessions(*sessionImageFallback)
		if err != nil {
			return err
		}
		defer closeContainers()
		runnerOptions = append(runnerOptions, containerOptions...)
		logger.Info("harness: container sessions", "fallback_image", *sessionImageFallback)
	case session.LaunchHost:
		// A conversation lives in the home it was recorded in, so only host
		// sessions, which share one home, are routed onto each other's.
		router, err := routing.NewRouter(routing.WithStateDirectory(state))
		if err != nil {
			return err
		}
		runnerOptions = append(runnerOptions, session.WithGateCommand(executable, verbGate),
			session.WithClaudeExecutable(*claude), session.WithCopilotExecutable(*copilot), session.WithRouter(router))
		logger.Warn("harness: host sessions, by -" + launchFlag + " " + session.LaunchHost)
	default:
		return fmt.Errorf("-%s is %s or %s, not %q", launchFlag, session.LaunchContainer, session.LaunchHost, *launch)
	}
	// Every session reaches this host's MCP server, so an agent drives CSF
	// through its typed operations rather than a script. A container session
	// reaches the host only on a non-loopback listen address.
	if address, found := mcpAddress(listens, *launch == session.LaunchContainer); found {
		runnerOptions = append(runnerOptions, session.WithMCPServer(schemeHTTP+address+mcpPath))
	} else {
		logger.Warn("harness: no listen address a container session can reach, so sessions get no MCP server; add a -" + listenFlag + " on a non-loopback address")
	}
	if *sandboxEnabled {
		manager, err := buildSandbox(*sandboxLauncher, *sandboxProxy, sandboxImages, sandboxReadOnly)
		if err != nil {
			return err
		}
		runnerOptions = append(runnerOptions, session.WithSandbox(manager, *sandboxDocker))
		logger.Info("harness: sandboxed launch enabled", "launcher", *sandboxLauncher, "proxy", *sandboxProxy)
	}
	runner, err := session.NewAgentSessionRunner(runnerOptions...)
	if err != nil {
		return err
	}
	// The host owns its shutdown: StopHarness is granted this cancel.
	serving, requestStop := context.WithCancelCause(ctx)
	defer requestStop(nil)
	// The slice dispatcher is built after the sessions it launches onto and
	// observes them through this variable, set before the host runs.
	var dispatcher *dispatchservice.DispatchService
	// The process table, granted as /proc: the session service closes idle
	// executors by it and samples what the harness and its executors hold.
	processes, err := iofs.NewHostFiles(processTableDirectory)
	if err != nil {
		return err
	}
	sessions, err := harness.NewAgentSessionService(
		harness.WithSessionRunner(runner),
		harness.WithServiceLogger(logger),
		harness.WithHostPID(os.Getpid()),
		harness.WithProcessTable(processes),
		harness.WithStopRequest(func() { requestStop(errors.New("stop requested through StopHarness")) }),
		harness.WithSessionObserver(func(state *harnessv1.AgentSessionState) {
			if dispatcher != nil {
				dispatcher.ObserveSession(state)
			}
		}),
		harness.WithLauncher(launcher),
		harness.WithExecutorDefault(session.Executor(*defaultExecutor), *defaultModel),
	)
	if err != nil {
		return err
	}
	// The Workbench's theme is <state>/workbench-theme.css, overriding the
	// page's tokens; a missing file keeps the defaults.
	apiOptions := []csf.Option{csf.WithAgentSessions(sessions), csf.WithWorkbenchThemeDirectory(state)}
	containers, err := docker.NewContainerHost()
	if err != nil {
		return err
	}
	defer func() { _ = containers.Close() }()
	settings, err := databaseSettings(ctx, *databaseConfig, state, containers, logger)
	if err != nil {
		return err
	}
	pool, err := openDatabase(ctx, settings)
	if err != nil {
		return err
	}
	defer pool.Close()
	widgetsRoot, widgetsExist, err := widgetsDirectory(*widgets)
	if err != nil {
		return err
	}
	// The housekeeper is built with the scheduler, after the loop whose merges
	// fast-forward the live checkout through it.
	var housekeeper *housekeeping.Housekeeper
	recorded, err := recordLoopSettings(state, loopSettings{Repository: *ouroborosRepository, Corpus: *ouroborosCorpus})
	if err != nil {
		return err
	}
	var loop *ouroboros.Loop
	if recorded.Repository != "" {
		loop, err = newOuroborosLoop(ctx, pool, state, launcher, logger, sessions,
			ouroborosSettings{repository: recorded.Repository, corpus: recorded.Corpus, fixers: *ouroborosFixers},
			ouroboros.WithMergeObserver(func(ctx context.Context, pull ouroboros.PullRequest) {
				if _, err := dispatcher.Dispatch(ctx); err != nil {
					logger.Warn("harness: dispatcher pass after a merge incomplete", "pull_request", pull.Number, "error", err)
				}
				if widgetsExist {
					if err := housekeeper.Run(ctx, housekeeping.TriggerLiveCheckout); err != nil {
						logger.Warn("harness: live checkout after a merge incomplete", "pull_request", pull.Number, "error", err)
					}
				}
			}))
		if err != nil {
			return err
		}
	} else {
		logger.Warn("harness: no mining loop checkout is recorded or given with -" + ouroborosRepositoryFlag + ", so the loop is not mounted")
	}
	dispatcher, err = newDispatcher(pool, state, launcher, logger, sessions, loop)
	if err != nil {
		return err
	}
	apiOptions = append(apiOptions, dispatcher.Tools()...)
	awaiter, err := hostAwaiter(state, processes, launcher, sessions)
	if err != nil {
		return err
	}
	apiOptions = append(apiOptions, awaiter.Tools()...)
	hostControl, hostTools, closeEngine, err := hostOperations(state, dispatcher)
	if err != nil {
		return err
	}
	defer closeEngine()
	apiOptions = append(apiOptions, hostTools...)
	cloudJobs, err := newCloudJobs(ctx, cloudFlags, state, launcher, dispatcher, logger)
	if err != nil {
		return err
	}
	if cloudJobs != nil {
		apiOptions = append(apiOptions, cloudJobs.Tools()...)
	}
	// The GitHub protocol client, when a token is found: the GitHub tools on
	// this host's MCP endpoint and the Workbench's widget proposals use it.
	githubClient, err := newGitHubClient(config.OSEnvironment())
	switch {
	case errors.Is(err, github.ErrNoToken):
		logger.Warn("harness: no " + GitHubTokenVariable + ", " + GHTokenVariable + " or token in gh's " + github.GhHostsFile + ", so the GitHub tools are not served")
	case err != nil:
		return err
	}
	webhook, err := newWebhookReceiver(state, logger, sessions, dispatcher, githubClient,
		webhookRepositories(ctx, launcher, *viewsRepository, recorded.Repository))
	if err != nil {
		return err
	}
	var operations *opsview.WidgetOperations
	if widgetsExist {
		var tools []csf.Option
		if operations, tools, err = widgetOperations(widgetsRoot, state, launcher, githubClient); err != nil {
			return err
		}
		apiOptions = append(apiOptions, tools...)
	} else {
		logger.Warn("harness: no widgets directory, so the widget operations are not served", "directory", widgetsRoot)
	}
	var githubTools *githubtools.GitHubTools
	if githubClient != nil {
		githubOptions := []githubtools.GitHubToolsOption{
			githubtools.WithClient(githubClient),
			githubtools.WithStateDirectory(state),
			githubtools.WithLogger(logger),
			githubtools.WithMergeGuard(dispatcher.HeldBranch),
		}
		if recorded.Repository != "" {
			githubOptions = append(githubOptions, githubtools.WithGoldenMetrics(newGoldenMeasurer(launcher, recorded.Repository, bootstrap.Measure)))
		}
		if githubTools, err = githubtools.NewGitHubTools(githubOptions...); err != nil {
			return err
		}
		apiOptions = append(apiOptions, githubTools.Tools()...)
	}
	api, err := csf.New(apiOptions...)
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
	installed := []opsview.Option{opsview.WithHostOperations(hostControl)}
	if cloudJobs != nil {
		installed = append(installed, opsview.WithCloudStop(cloudJobs.Stop))
	}
	if widgetsExist {
		definitions, err := installedWidgets(widgetsRoot)
		if err != nil {
			return err
		}
		installed = append(installed, definitions)
	}
	installed = append(installed, opsview.WithEndpoints(registry), opsview.WithGitHub(launcher), opsview.WithHostStart(startedAt),
		opsview.WithBuild(opsview.BuildOf(debug.ReadBuildInfo())))
	workbench, err := newWorkbench(state, *recipes, origins, logger, api, installed...)
	if err != nil {
		return err
	}
	engine := httpserver.NewEngine(httpService, httpserver.WithRequestLogging())
	api.Register(engine)
	sessions.Register(engine)
	chatPage.Register(engine)
	dispatcher.Register(engine)
	awaiter.Register(engine)
	hostControl.Register(engine)
	if operations != nil {
		operations.Register(engine)
	}
	if githubTools != nil {
		githubTools.Register(engine)
	}
	workbench.Register(engine)
	if cloudJobs != nil {
		cloudJobs.Register(engine)
	}
	webhook.Register(engine)
	engine.Any(mcpPath, gin.WrapH(api.MCPHandler()))
	registerCacheMeasurement(engine, state)
	measurements, err := newViews(ctx, state, listens, containers, launcher, logger, sessions, dispatcher, *viewsRepository, recorded.Repository,
		append(cloudCollectors(cloudJobs, state), githubtools.NewCallCollector(state), chatPage, suiteCollector(pool), webhook.Counts())...)
	if err != nil {
		return err
	}
	measurements.Register(engine)

	host, err := runtime.NewHostRuntime(runtime.WithHostName(hostName), runtime.WithLogger(logger))
	if err != nil {
		return err
	}
	if err := host.Mount("sessions", sessions); err != nil {
		return err
	}
	if err := host.Mount(workbenchMountName, workbench); err != nil {
		return err
	}
	if err := host.Mount(viewsMountName, measurements); err != nil {
		return err
	}
	if cloudJobs != nil {
		if err := host.Mount(cloudMountName, cloudJobs); err != nil {
			return err
		}
	}
	housekeepingOptions := []housekeeping.HousekeeperOption{housekeeping.WithAdmission(sessions)}
	if widgetsExist {
		housekeepingOptions = append(housekeepingOptions, housekeeping.WithLiveCheckout(widgetsRoot))
	}
	housekeeper, err = newHousekeeper(state, launcher, containers, logger,
		func(ctx context.Context) (*harnessv1.ListAgentSessionsResponse, error) {
			return sessions.List(ctx, &harnessv1.ListAgentSessionsRequest{})
		}, housekeepingOptions...)
	if err != nil {
		return err
	}
	if err := mountTriggers(host, engine, pool, housekeeper, loop, dispatcher); err != nil {
		return err
	}
	logger.Info("harness: mounted cron with the housekeeping triggers, the slice dispatcher and the mining loop", "loop", loop != nil, "live_checkout", widgetsExist)
	if err := host.Mount("chat", chatPage); err != nil {
		return err
	}
	if err := host.Mount(githubWebhookMountName, webhook); err != nil {
		return err
	}
	network := ionet.NewHostNetwork()
	for _, address := range listens {
		listener, err := iohttp.NewHTTPListener(network, address, engine)
		if err != nil {
			return err
		}
		if err := host.Mount("http "+address, listener); err != nil {
			return err
		}
	}
	for _, address := range redirects {
		listener, err := iohttp.NewHTTPListener(network, address, endpoint.NewAliasRedirect(primaryPort))
		if err != nil {
			return err
		}
		if err := host.Mount("redirect "+address, listener); err != nil {
			return err
		}
	}
	// Mounted last, so it runs once every listener is bound, and written once
	// the first pass over the open runs is done: the record means ready
	// (listening, database healthy, open runs resumed or queued), and
	// csf serve -detach returns on it. It is removed first on shutdown, so it
	// is true exactly while the host answers.
	directory, err := os.Getwd()
	if err != nil {
		return err
	}
	record := hostRecord{PID: os.Getpid(), Endpoint: schemeHTTP + listens[0], Listen: listens, StartedAt: startedAt,
		Argv: os.Args, Binary: executable, Directory: directory}
	if err := host.Mount("record", runtime.ServiceFunc(func(scope *runtime.Scope) error {
		return scope.GoOwner("record", func(ctx context.Context) error {
			select {
			case <-sessions.Resumed():
			case <-ctx.Done():
				return nil
			}
			if err := writeHostRecord(state, HostRecordFile, record); err != nil {
				return err
			}
			if err := writeEndpointRegistry(state, registry); err != nil {
				return err
			}
			fmt.Fprintf(output, "%s: pid %d serving on %s\n", commandName, record.PID, strings.Join(listens, " "))
			<-ctx.Done()
			return os.Remove(filepath.Join(state, HostRecordFile))
		})
	})); err != nil {
		return err
	}
	return host.Run(serving)
}

// providerSessions grants the runner the inference provider the state
// directory records: the operator's router entry in providers.json, applied on
// every start, so a restart or an upgrade keeps the switch. It creates the
// harness-owned executor configuration directory and links its projects/ to the
// real conversation history, so a conversation started before the switch
// resumes on the provider. A state directory recording no router leaves the
// runner on the executor's own provider. A container session cannot reach the
// router's configuration directory, so it is refused rather than served
// half-switched.
func providerSessions(state string, container bool) ([]session.AgentSessionRunnerOption, error) {
	router, err := provider.ReadRouter(state)
	if errors.Is(err, provider.ErrNoRouter) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if container {
		return nil, fmt.Errorf("-%s %s cannot reach the provider's configuration directory; run -%s %s",
			launchFlag, session.LaunchContainer, launchFlag, session.LaunchHost)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("locate the conversation history: %w", err)
	}
	if err := router.PrepareConfigDirectory(filepath.Join(home, executorConversationStore)); err != nil {
		return nil, err
	}
	return []session.AgentSessionRunnerOption{session.WithProvider(router)}, nil
}

// containerSessions grants the runner the container capability: every session
// runs in its own container of the session image, where the executor and the
// gates are the image's own and the executor's and gh's credentials are
// mounted read-only from this user's home. The executor's conversations are
// saved in this user's own store, so container and host sessions resume each
// other's. The returned func closes the Engine client.
func containerSessions(fallbackImage string) ([]session.AgentSessionRunnerOption, func(), error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil, fmt.Errorf("locate the credentials: %w", err)
	}
	containers, err := docker.NewContainerHost()
	if err != nil {
		return nil, nil, err
	}
	options := []session.AgentSessionRunnerOption{
		session.WithGateCommand(commandName, verbGate),
		session.WithClaudeExecutable(session.ContainerExecutor),
		session.WithCopilotExecutable(session.ContainerCopilotExecutor),
		session.WithContainerSessions(containers, session.ContainerSettings{
			Image: fallbackImage,
			Credentials: []session.Credential{
				{Source: filepath.Join(home, executorCredentialHome), Home: executorCredentialHome},
				{Source: filepath.Join(home, ghConfigHome), Home: ghConfigHome},
			},
			ConversationStore: filepath.Join(home, executorConversationStore),
		}),
	}
	return options, func() { _ = containers.Close() }, nil
}

// mcpAddress is the listen address sessions reach this host's MCP server on:
// the first one for host sessions, and for container sessions the first that
// is neither loopback nor unspecified, since a container's loopback is its
// own.
func mcpAddress(listens []string, container bool) (string, bool) {
	for _, address := range listens {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			continue
		}
		ip := net.ParseIP(host)
		if !container || (ip != nil && !ip.IsLoopback() && !ip.IsUnspecified()) {
			return address, true
		}
	}
	return "", false
}

// defaultSandboxReadOnlyRoots are the system and toolchain paths every
// sandboxed session may read and execute. A host whose toolchains read
// credentials or configuration from elsewhere (gh, git and the executor's own
// config under the home directory) names those with -sandbox-ro.
var defaultSandboxReadOnlyRoots = []string{"/usr", "/bin", "/lib", "/lib64", "/etc", "/proc", "/sys", "/dev"}

// buildSandbox constructs the sandbox capability and prepares its sessions
// cgroup. It fails with a typed error naming the operator's fix when the harness
// is not in a delegated cgroup.
func buildSandbox(launcherPath string, proxyPath string, images []string, extraReadOnly []string) (*sandbox.Manager, error) {
	if launcherPath == "" || proxyPath == "" {
		return nil, fmt.Errorf("-%s requires -%s and -%s", sandboxFlag, sandboxLauncherFlag, sandboxProxyFlag)
	}
	roots := append(append([]string{}, defaultSandboxReadOnlyRoots...), extraReadOnly...)
	manager, err := sandbox.NewManager(launcherPath,
		sandbox.WithProxy(proxyPath),
		sandbox.WithAllowedImages(images...),
		sandbox.WithReadOnlyRoots(roots...))
	if err != nil {
		return nil, err
	}
	if err := manager.Prepare(); err != nil {
		return nil, err
	}
	return manager, nil
}

// registerCacheMeasurement serves the cache dashboard's JSON at cachePath,
// measured from the runs' event logs at each request; its gauges are on the
// views' /metrics.
func registerCacheMeasurement(engine gin.IRouter, state string) {
	engine.GET(cachePath, func(request *gin.Context) {
		report, err := routing.Measure(state)
		if err != nil {
			request.String(http.StatusInternalServerError, "%s", err.Error())
			return
		}
		request.JSON(http.StatusOK, report)
	})
}

// databaseSettings is the database the settings file names, or, with none,
// CSF's own: provisioned on the first start, reused on every later one, and
// healthy before it returns.
func databaseSettings(ctx context.Context, settingsPath string, state string, containers *docker.ContainerHost, logger *slog.Logger) (csfpg.Settings, error) {
	var settings csfpg.Settings
	if settingsPath == "" {
		owned, err := ownedDatabase(state, containers)
		if err != nil {
			return settings, err
		}
		record, err := owned.Ensure(ctx)
		if err != nil {
			return settings, err
		}
		logger.Info("harness: own database healthy", "container", record.Container, "volume", record.Volume, "port", record.Port, "image", record.Image)
		return database.Settings(record), nil
	}
	content, err := os.ReadFile(settingsPath)
	if err != nil {
		return settings, err
	}
	if err := json.Unmarshal(content, &settings); err != nil {
		return settings, fmt.Errorf("decode %s: %w", settingsPath, err)
	}
	return settings, nil
}

// openDatabase opens the csfpg database and brings it up to CSF's schema:
// the cron store, the mining loop's ledger and the slice graph all live in
// it. The caller closes the pool after the host has stopped.
func openDatabase(ctx context.Context, settings csfpg.Settings) (*csfpg.Pool, error) {
	pool, err := csfpg.OpenPool(ctx, settings)
	if err != nil {
		return nil, err
	}
	schemaHandle := pool.OpenSQL()
	if err := errors.Join(csfpg.ApplySchema(ctx, schemaHandle), schemaHandle.Close()); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// mountTriggers mounts, after the sessions they use, the slice dispatcher
// and the cron service with the default housekeeping triggers (which hold
// the sessions' admission while free disk is below their floor, back up the
// owned database and keep the live checkout current), the dispatcher's pass,
// and the mining loop's triggers when the loop was built.
func mountTriggers(host *runtime.HostRuntime, engine gin.IRouter, pool csfpg.IDB, housekeeper *housekeeping.Housekeeper,
	loop *ouroboros.Loop, dispatcher *dispatchservice.DispatchService) error {
	triggers, err := housekeeper.Triggers()
	if err != nil {
		return err
	}
	triggers = append(triggers, dispatcher.Trigger())
	if loop != nil {
		triggers = append(triggers, loop.Triggers()...)
	}
	if err := host.Mount(dispatchMountName, dispatcher); err != nil {
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
	if err := host.Mount("cron", scheduler); err != nil {
		return err
	}
	if loop == nil {
		return nil
	}
	loop.Register(engine)
	return host.Mount(ouroborosMountName, loop)
}

// newOuroborosLoop grants the mining loop this host's capabilities: the
// ledger on the pool, the sessions, the launcher, the tickets of the
// repository origin points at, and the file and watch capabilities over the
// state directory, which is both its corpus and where its fixers run.
func newOuroborosLoop(ctx context.Context, pool csfpg.IDB, state string, launcher *proc.HostLauncher, logger *slog.Logger,
	sessions *harness.AgentSessionService, settings ouroborosSettings, extra ...ouroboros.LoopOption) (*ouroboros.Loop, error) {
	stateFiles, err := iofs.NewHostFiles(state)
	if err != nil {
		return nil, err
	}
	corpus := settings.corpus
	if corpus == "" {
		corpus = state
	}
	corpusFiles, err := iofs.NewHostFiles(corpus)
	if err != nil {
		return nil, err
	}
	watcher, err := iofs.NewHostWatcher(corpus)
	if err != nil {
		return nil, err
	}
	repositoryFiles, err := iofs.NewHostFiles(settings.repository)
	if err != nil {
		return nil, err
	}
	origin, err := launcher.Run(ctx, proc.Command{Executable: gitExecutable, Arguments: []string{gitDirectory, repositoryFiles.Directory(), gitRemote, gitGetURL, gitOrigin}})
	if err != nil {
		return nil, fmt.Errorf("read the repository's origin: %w", err)
	}
	slug, err := ouroboros.RepositorySlug(string(origin.Stdout))
	if err != nil {
		return nil, err
	}
	tickets, err := ouroboros.NewGitHubTickets(launcher, slug)
	if err != nil {
		return nil, err
	}
	ledger, err := ouroboros.NewLedger(pool)
	if err != nil {
		return nil, err
	}
	suiteStore, err := evaluate.NewSuiteStore(pool)
	if err != nil {
		return nil, err
	}
	return ouroboros.NewLoop(append([]ouroboros.LoopOption{
		ouroboros.WithLedger(ledger),
		ouroboros.WithSessions(sessions),
		ouroboros.WithLauncher(launcher),
		ouroboros.WithTickets(tickets),
		ouroboros.WithCorpus(corpusFiles.Directory(), corpusFiles, watcher),
		ouroboros.WithState(stateFiles.Directory(), stateFiles),
		ouroboros.WithRepository(repositoryFiles.Directory(), repositoryFiles),
		ouroboros.WithFixerLaunch(settings.fixers),
		ouroboros.WithMergerIdentity(fmt.Sprintf(mergerIdentityFormat, os.Getpid())),
		ouroboros.WithLogger(logger),
		ouroboros.WithHidden(func(ctx context.Context) (map[string]bool, error) {
			tickets, err := suiteStore.HiddenTickets(ctx)
			if err != nil {
				return nil, err
			}
			return evaluate.HiddenAssignments(corpusFiles, tickets)
		}),
	}, extra...)...)
}

// newHousekeeper grants the housekeeping service this host's capabilities:
// the state directory, the process table as /proc, the launcher, the
// Docker Engine and the record file every record is appended to.
func newHousekeeper(state string, launcher *proc.HostLauncher, containers *docker.ContainerHost, logger *slog.Logger,
	list housekeeping.SessionList, options ...housekeeping.HousekeeperOption) (*housekeeping.Housekeeper, error) {
	if err := os.MkdirAll(state, 0o700); err != nil {
		return nil, err
	}
	stateFiles, err := iofs.NewHostFiles(state)
	if err != nil {
		return nil, err
	}
	processes, err := iofs.NewHostFiles(processTableDirectory)
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
	_ = flags.Bool(outputFlag, false, "render output as a markdown table instead of JSON (not yet implemented)")
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
	client, err := csf.NewClient(target, &http.Client{Timeout: clientTimeout}, clientWarnings)
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
		scratchFiles, err := iofs.NewHostFiles(*scratch)
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
// state directory, and returns once the child is ready. A child that exits
// first, or is not ready by the deadline, fails the command with the result
// and the log lines the child wrote.
func detachHost(ctx context.Context, launcher *proc.HostLauncher, state string, verb string, logFile string, arguments []string,
	deadline time.Duration, output io.Writer) error {
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
	logged, err := log.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
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
	fmt.Fprintf(output, "%s: started pid %d in the background; log %s; awaiting %s\n", commandName, process.Pid(), logPath, await.ConditionHarnessReady)
	result, err := lifecycleWait(ctx, state, await.ConditionHarnessReady, process.Pid(), deadline)
	if err != nil {
		return err
	}
	if err := printResult(output, result); err != nil {
		return fmt.Errorf("%w\nthe log since the start:\n%s", err, strings.Join(logTail(logPath, logged, logTailLines), "\n"))
	}
	fmt.Fprintf(output, "%s: pid %d ready; stop with `%s %s`\n", commandName, process.Pid(), commandName, verbStop)
	return nil
}

// writeHostRecord replaces the named record under the state directory
// atomically, so a reader never sees a half-written one.
func writeHostRecord(state string, name string, record hostRecord) error {
	content, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(filepath.Join(state, name), content, hostFileMode)
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
	stopDeadline := flags.Duration(deadlineFlag, lifecycleDeadline, "stop: how long to wait for the host process to exit")
	operator := flags.Bool(operatorFlag, false, "the message is the operator's own words, verbatim: the harness records its unvetted terms and the reply gate holds the reply to them")
	questionWanted := flags.Bool(questionFlag, false, "the operator wanted the question the question gate last refused in this session: recorded as an override of the gate")
	diffFile := flags.String(diffFileFlag, "", "file holding the unified diff")
	from := flags.Int(fromFlag, 0, "skip this many records of the event stream")
	executor := flags.String(executorFlag, "", "turn executor: "+string(session.ExecutorClaudeCode)+" or "+string(session.ExecutorCopilot))
	model := flags.String(modelFlag, "", "model, spelled as the executor spells it")
	pretty := flags.Bool(outputFlag, false, "render output as a markdown table instead of JSON")
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
	timeout := clientTimeout
	if verb == verbMerge {
		timeout = mergeTimeout
	}
	client, err := csf.NewClient(target, &http.Client{Timeout: timeout}, clientWarnings)
	if err != nil {
		return err
	}
	switch verb {
	case verbSubmit, verbCheck:
		if *recipePath == "" {
			return errUsage
		}
		recipe, err := readRecipe(*recipePath)
		if err != nil {
			return err
		}
		if verb == verbCheck {
			response, err := client.CheckAgentSessionAdmission(ctx, &harnessv1.CheckAgentSessionAdmissionRequest{Recipe: recipe})
			return print(output, response, err)
		}
		response, err := client.SubmitAgentSession(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: recipe})
		return printFormat(output, response, err, *pretty)
	case verbReady:
		if *assignment == "" {
			return errNoAssignment
		}
		response, err := client.ReadyAgentSessionPullRequest(ctx, &harnessv1.ReadyAgentSessionPullRequestRequest{AssignmentId: *assignment})
		return print(output, response, err)
	case verbMerge:
		if *assignment == "" {
			return errNoAssignment
		}
		response, err := client.MergeAgentSessionPullRequest(ctx, &harnessv1.MergeAgentSessionPullRequestRequest{AssignmentId: *assignment})
		return print(output, response, err)
	case verbSend:
		if *assignment == "" {
			return errNoAssignment
		}
		text, err := messageText(*message, *messageFile, input)
		if err != nil {
			return err
		}
		response, err := client.SendAgentSessionMessage(ctx, &harnessv1.SendAgentSessionMessageRequest{AssignmentId: *assignment, Message: text, OperatorAuthored: *operator, QuestionWanted: *questionWanted})
		return printFormat(output, response, err, *pretty)
	case verbPropose:
		if *assignment == "" {
			return errNoAssignment
		}
		if *diffFile == "" {
			return errors.New("-diff is required")
		}
		diff, err := os.ReadFile(*diffFile)
		if err != nil {
			return fmt.Errorf("read diff: %w", err)
		}
		text, err := messageText(*message, *messageFile, input)
		if err != nil {
			return err
		}
		proposal := &harnessv1.Proposal{
			AssignmentId: *assignment,
			Diff:         string(diff),
			Message:      text,
			ProposedAt:   timestamppb.Now(),
		}
		response, err := client.ProposeProposal(ctx, &harnessv1.ProposeProposalRequest{Proposal: proposal})
		return printFormat(output, response, err, *pretty)
	case verbList:
		start := time.Now()
		response, err := client.ListAgentSessions(ctx, &harnessv1.ListAgentSessionsRequest{})
		if err != nil {
			return err
		}
		elapsed := time.Since(start).Milliseconds()
		return printSessionTable(output, response, elapsed)
	case verbGet:
		if *assignment == "" {
			return errNoAssignment
		}
		response, err := client.GetAgentSession(ctx, &harnessv1.GetAgentSessionRequest{AssignmentId: *assignment})
		return printFormat(output, response, err, *pretty)
	case verbInbox:
		if *assignment == "" {
			return errNoAssignment
		}
		response, err := client.ListInbox(ctx, &harnessv1.ListInboxRequest{AssignmentId: *assignment})
		return print(output, response, err)
	case verbCancel:
		if *assignment == "" {
			return errNoAssignment
		}
		response, err := client.CancelAgentSession(ctx, &harnessv1.CancelAgentSessionRequest{AssignmentId: *assignment})
		return printFormat(output, response, err, *pretty)
	case verbStop:
		return stopHost(ctx, client, target, *stateDirectory, *stopDeadline, output)
	case verbExecutor:
		if *executor == "" {
			response, err := client.GetAgentExecutorDefault(ctx, &harnessv1.GetAgentExecutorDefaultRequest{})
			return print(output, response, err)
		}
		response, err := client.SetAgentExecutorDefault(ctx, &harnessv1.SetAgentExecutorDefaultRequest{
			ExecutorDefault: &harnessv1.AgentExecutorDefault{Executor: *executor, Model: *model}})
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
	return printFormat(output, response, err, false)
}

// printFormat writes an operation's response as proto JSON or pretty table format.
func printFormat[Response proto.Message](output io.Writer, response Response, err error, prettyFormat bool) error {
	if err != nil {
		return err
	}
	if prettyFormat {
		return pretty.Render(output, response)
	}
	content, err := (protojson.MarshalOptions{UseProtoNames: true, Indent: "  "}).Marshal(response)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, string(content))
	return err
}

// printSessionTable renders sessions as an activity table sorted by events in last 10m.
func printSessionTable(output io.Writer, response *harnessv1.ListAgentSessionsResponse, elapsedMs int64) error {
	fmt.Fprintf(output, "source: session table, read lock, %dms\n\n", elapsedMs)
	sessions := response.GetSessions()
	slices.SortStableFunc(sessions, func(a, b *harnessv1.AgentSessionState) int {
		if a.EventsLast_10M != b.EventsLast_10M {
			return int(b.EventsLast_10M) - int(a.EventsLast_10M)
		}
		return strings.Compare(a.AssignmentId, b.AssignmentId)
	})
	fmt.Fprintf(output, "%-10s %-15s %-8s %-8s %-6s %-20s\n",
		"Agent", "Phase", "Events", "Inbox", "Tool", "Last Command")
	fmt.Fprintln(output, strings.Repeat("-", 80))
	for _, s := range sessions {
		tool := s.LastTool
		if tool == "" {
			tool = "-"
		}
		cmd := s.LastCommand
		if cmd == "" {
			cmd = "-"
		}
		if len(cmd) > 20 {
			cmd = cmd[:17] + "..."
		}
		fmt.Fprintf(output, "%-10s %-15s %-8d %-8d %-6s %-20s\n",
			s.AgentId[:min(10, len(s.AgentId))],
			s.Phase.String()[:min(15, len(s.Phase.String()))],
			s.EventsLast_10M, s.InboxDepth, tool, cmd)
	}
	return nil
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

// gate answers one hook call from a turn executor, Claude Code or Copilot.
// With a run directory it answers for that run; with the event alone it
// answers for an operator's own session, which install-gates hooked up. Its
// output, when there is one, is the decision; a gate that cannot read its
// input or its run blocks the call.
func gate(ctx context.Context, launcher *proc.HostLauncher, arguments []string, input io.Reader, output io.Writer, diagnostics io.Writer) int {
	if len(arguments) != 2 {
		fmt.Fprintln(diagnostics, errUsage)
		return exitBlocked
	}
	event, directory := arguments[0], arguments[1]

	// Handle relay hooks separately from session gates.
	if event == "UserPromptSubmit" {
		return relayGate(ctx, input, output, diagnostics)
	}

	registry, err := gateRegistry(directory)
	if err != nil {
		fmt.Fprintf(diagnostics, "%s gate: %v\n", commandName, err)
		return exitBlocked
	}
	gateOptions := []sessiongate.SessionGateOption{sessiongate.WithLauncher(launcher), sessiongate.WithRunDirectory(directory),
		sessiongate.WithEndpointRegistry(registry)}
	// The commit gate opens draft pull requests through the GitHub protocol
	// client; without a token it pushes and says what is missing.
	if client, err := newGitHubClient(config.OSEnvironment()); err == nil {
		gateOptions = append(gateOptions, sessiongate.WithGitHub(client))
	}
	sessionGate, err := sessiongate.NewSessionGate(gateOptions...)
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

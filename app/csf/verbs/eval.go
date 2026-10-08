// Copyright 2026 Candace Labs

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
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/candacelabs/csf/csf"
	"github.com/candacelabs/csf/io/ipc/db/csfpg"
	"github.com/candacelabs/csf/io/ipc/docker"
	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/io/kernel/clock"
	iofs "github.com/candacelabs/csf/io/kernel/fs"
	ionet "github.com/candacelabs/csf/io/net"
	iohttp "github.com/candacelabs/csf/io/net/http"
	"github.com/candacelabs/csf/io/net/model/jev"
	evaluation "github.com/candacelabs/csf/pkg/evaluate"
	"github.com/candacelabs/csf/services/cloud"
	"github.com/candacelabs/csf/services/ouroboros"
	"github.com/candacelabs/csf/services/ouroboros/evaluate"
)

// The eval verbs (EVAL-SUITE, #416): select or rotate the held-out suite,
// score the host's build on it sharded across nodes, replay a shard on a
// node that has no database, record a burst shard's replays, show a score
// with its citation line, and check a pull request's improvement claims.
const (
	verbEval       = "eval"
	evalSuiteVerb  = "suite"
	evalScoreVerb  = "score"
	evalReplayVerb = "replay"
	evalRecordVerb = "record"
	evalShowVerb   = "show"
	evalCiteVerb   = "cite"

	evalForceFlag     = "force"
	evalTicketsFlag   = "tickets"
	evalBuildFlag     = "build"
	evalVersusFlag    = "vs"
	evalBurstFlag     = "burst"
	evalDryRunFlag    = "dry-run"
	evalFlavorFlag    = "flavor"
	evalCapFlag       = "cap-usd"
	evalParallelFlag  = "parallel"
	evalPollFlag      = "poll"
	evalWorkFlag      = "work"
	evalSuiteFileFlag = "suite-file"
	evalJobsFileFlag  = "jobs-file"
	evalNodeFlag      = "node"
	evalOutFlag       = "out"
	evalFileFlag      = "file"
	evalBodyFlag      = "body"
	evalPullFlag      = "pr"

	// evalModel is the model the operator's ruling allows every real
	// session (2026-10-05): every replay runs on it.
	evalModel = "claude-opus-5-5"
	// defaultEvalWork holds the replays' repository: a bare copy of the
	// checkout every replay pushes to, and a clone of it the replays branch
	// from, so no replay reaches the real repository.
	defaultEvalWork = ".local/state/csf/eval"
	evalOrigin      = "origin.git"
	evalRepository  = "repository"
	// defaultEvalPoll is how often a replay's event log is read: a replay
	// spends its budget over minutes, so a read every 30 s overshoots it by
	// at most one turn's calls.
	defaultEvalPoll = 30 * time.Second
	defaultFlavor   = "cpu-xl"
	// defaultShardCap is one burst shard's cap: what the provider's timeout
	// is bought from, so the job cannot outspend it.
	defaultShardCap = 20.0

	ghPullList      = "pr"
	ghListVerb      = "list"
	ghViewVerb      = "view"
	ghRepoFlag      = "--repo"
	ghStateFlag     = "--state"
	ghLimitFlag     = "--limit"
	ghPullLimit     = "1000"
	ghJSONFlag      = "--json"
	ghPullFields    = "number,title,mergeCommit,files"
	ghBodyFields    = "body"
	ghJQFlag        = "--jq"
	ghBodyQuery     = ".body"
	gitRevParse     = "rev-parse"
	gitFirstParent  = "^1"
	gitFetch        = "fetch"
	gitQuiet        = "--quiet"
	gitClone        = "clone"
	gitBare         = "--bare"
	gitMainRefspecs = "+refs/heads/*:refs/heads/*"
	buildPattern    = `^[0-9a-f]{12}$`

	// modelEvalTimeout bounds one model's whole held-out run. The first
	// question loads the model into memory before it answers.
	modelEvalTimeout = 30 * time.Minute
)

var (
	errUsageEval = errors.New("usage: csf eval suite|score [builds|models]|replay|record|show|cite [flags]")
	// errBuildUnknown reports a binary that does not carry a clean 12-hex
	// commit stamp, which cannot be scored or cited.
	errBuildUnknown = errors.New("eval: the binary's build is not a clean 12-character commit")
	buildExpression = regexp.MustCompile(buildPattern)
)

// evalVerbs runs one eval verb.
func evalVerbs(ctx context.Context, launcher *proc.HostLauncher, arguments []string, output io.Writer, diagnostics io.Writer) error {
	if len(arguments) == 0 {
		return errUsageEval
	}
	logger := slog.New(slog.NewJSONHandler(diagnostics, nil))
	verb, rest := arguments[0], arguments[1:]
	switch verb {
	case evalSuiteVerb:
		return evalSuite(ctx, launcher, rest, output)
	case evalScoreVerb:
		return evalScore(ctx, launcher, rest, output, logger)
	case evalReplayVerb:
		return evalReplay(ctx, launcher, rest, logger)
	case evalRecordVerb:
		return evalRecord(ctx, rest, output)
	case evalShowVerb:
		return evalShow(ctx, rest, output)
	case evalCiteVerb:
		return evalCite(ctx, launcher, rest, output)
	}
	return errUsageEval
}

// openStatePool opens the state directory's own database, brought up to
// CSF's schema; the caller closes it.
func openStatePool(ctx context.Context, state string) (*csfpg.Pool, error) {
	containers, err := docker.NewContainerHost()
	if err != nil {
		return nil, err
	}
	defer func() { _ = containers.Close() }()
	settings, err := databaseSettings(ctx, "", state, containers, slog.New(slog.DiscardHandler))
	if err != nil {
		return nil, err
	}
	return openDatabase(ctx, settings)
}

// openSuiteStore opens the state directory's database and the suite store
// over it; close releases the pool.
func openSuiteStore(ctx context.Context, state string) (store *evaluate.SuiteStore, close func(), err error) {
	pool, err := openStatePool(ctx, state)
	if err != nil {
		return nil, nil, err
	}
	store, err = evaluate.NewSuiteStore(pool)
	if err != nil {
		pool.Close()
		return nil, nil, err
	}
	return store, pool.Close, nil
}

// hostBuild is the build the host recorded in state runs: its executable's
// commit stamp.
func hostBuild(state string) (string, error) {
	record, running, err := runningHost(state)
	if err != nil {
		return "", err
	}
	if !running {
		return "", fmt.Errorf("eval: no host is running in %s", state)
	}
	executable, err := os.Readlink(processFile(record.PID, processExecutable))
	if err != nil {
		return "", err
	}
	return binaryBuild(executable)
}

// binaryBuild is a binary's commit stamp, refused unless it is a clean
// 12-character commit.
func binaryBuild(binary string) (string, error) {
	version, err := csf.BinaryVersion(binary)
	if err != nil {
		return "", err
	}
	if !buildExpression.MatchString(version) {
		return "", fmt.Errorf("%w: %s is %q", errBuildUnknown, binary, version)
	}
	return version, nil
}

// suiteSummary is what csf eval suite prints: everything but the tickets,
// which are printed only on request, because this command's own output may
// land in a session's event log, which the miners read.
type suiteSummary struct {
	Version      int                 `json:"version"`
	SelectedAt   time.Time           `json:"selected_at"`
	RotatesAt    time.Time           `json:"rotates_at"`
	Model        string              `json:"model"`
	Size         int                 `json:"size"`
	Evolution    int                 `json:"evolution_tickets"`
	Derivation   evaluate.Derivation `json:"derivation"`
	HiddenRuns   int                 `json:"hidden_runs"`
	Disjoint     bool                `json:"disjoint"`
	Rotated      bool                `json:"rotated"`
	Tickets      []int64             `json:"tickets,omitempty"`
	HiddenProbes []string            `json:"hidden_probe,omitempty"`
}

// evalSuite rotates the suite when it is due (or -force) and prints it.
func evalSuite(ctx context.Context, launcher *proc.HostLauncher, arguments []string, output io.Writer) error {
	flags := flag.NewFlagSet(commandName+" "+verbEval+" "+evalSuiteVerb, flag.ContinueOnError)
	stateDirectory := flags.String(stateFlag, "", "harness state directory, the corpus of runs (default ~/"+defaultStateDirectory+")")
	repository := flags.String(repositoryFlag, "", "checkout whose origin holds the merged pull requests (default: the mining loop's)")
	force := flags.Bool(evalForceFlag, false, "rotate now even when the current version is not due")
	showTickets := flags.Bool(evalTicketsFlag, false, "print the suite's ticket numbers too (keep this out of session logs)")
	model := flags.String(modelFlag, evalModel, "model every replay of a new version runs on")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	state, err := stateRoot(*stateDirectory)
	if err != nil {
		return err
	}
	checkout, err := loopCheckout(state, *repository)
	if err != nil {
		return err
	}
	store, closeStore, err := openSuiteStore(ctx, state)
	if err != nil {
		return err
	}
	defer closeStore()
	corpus, err := iofs.NewHostFiles(state)
	if err != nil {
		return err
	}
	current, err := store.LatestSuite(ctx)
	var latest *evaluate.Suite
	switch {
	case errors.Is(err, evaluate.ErrNoSuite):
	case err != nil:
		return err
	default:
		latest = &current
	}
	rotated := false
	now := time.Now().UTC()
	if latest.Due(now) || *force {
		pulls, err := mergedPulls(ctx, launcher, checkout)
		if err != nil {
			return err
		}
		pool, err := evaluate.Pool(corpus, pulls)
		if err != nil {
			return err
		}
		evolution, err := evolutionTickets(ctx, launcher, checkout, state)
		if err != nil {
			return err
		}
		version := 1
		if latest != nil {
			version = latest.Version + 1
		}
		selected, err := evaluate.Select(pool, evolution, version, *model, now)
		if err != nil {
			return err
		}
		if err := store.RecordSuite(ctx, selected); err != nil {
			return err
		}
		current, rotated = selected, true
	}
	hiddenTickets, err := store.HiddenTickets(ctx)
	if err != nil {
		return err
	}
	hidden, err := evaluate.HiddenAssignments(corpus, hiddenTickets)
	if err != nil {
		return err
	}
	summary := suiteSummary{
		Version: current.Version, SelectedAt: current.SelectedAt, RotatesAt: current.RotatesAt, Model: current.Model,
		Size: len(current.Tickets), Evolution: len(current.EvolutionSet), Derivation: current.Derivation,
		HiddenRuns: len(hidden), Disjoint: evaluate.Disjoint(current, current.EvolutionSet) == nil, Rotated: rotated,
	}
	if *showTickets {
		for _, ticket := range current.Tickets {
			summary.Tickets = append(summary.Tickets, ticket.Number)
		}
	}
	return printJSON(output, summary)
}

// loopCheckout is -repository, else the mining loop's recorded checkout.
func loopCheckout(state string, repository string) (string, error) {
	if repository != "" {
		return filepath.Abs(repository)
	}
	recorded, err := readLoopSettings(state)
	if err != nil {
		return "", err
	}
	if recorded.Repository == "" {
		return "", fmt.Errorf("eval: no -%s and no mining loop checkout is recorded in %s", repositoryFlag, state)
	}
	return recorded.Repository, nil
}

// pullListing is one merged pull request as gh lists it.
type pullListing struct {
	Number      int64  `json:"number"`
	Title       string `json:"title"`
	MergeCommit struct {
		OID string `json:"oid"`
	} `json:"mergeCommit"`
	Files []struct {
		Path string `json:"path"`
	} `json:"files"`
}

// mergedPulls lists the merged pull requests of the checkout's origin with
// the commit each started from: its merge commit's first parent, read from
// the checkout after fetching main.
func mergedPulls(ctx context.Context, launcher *proc.HostLauncher, checkout string) ([]evaluate.MergedPull, error) {
	origin, err := gitOutput(ctx, launcher, checkout, gitDirectory, checkout, gitRemote, gitGetURL, gitOrigin)
	if err != nil {
		return nil, err
	}
	slug, err := ouroboros.RepositorySlug(origin)
	if err != nil {
		return nil, err
	}
	if _, err := gitOutput(ctx, launcher, checkout, gitDirectory, checkout, gitFetch, gitQuiet, gitOrigin); err != nil {
		return nil, err
	}
	listed, err := launcher.Run(ctx, proc.Command{Executable: ghExecutable, Directory: checkout,
		Arguments: []string{ghPullList, ghListVerb, ghRepoFlag, slug, ghStateFlag, ghMerged, ghLimitFlag, ghPullLimit, ghJSONFlag, ghPullFields}})
	if err != nil {
		return nil, err
	}
	var listings []pullListing
	if err := json.Unmarshal(listed.Stdout, &listings); err != nil {
		return nil, fmt.Errorf("eval: decode the merged pull requests: %w", err)
	}
	pulls := make([]evaluate.MergedPull, 0, len(listings))
	for _, listing := range listings {
		if listing.MergeCommit.OID == "" || evaluate.TicketOfTitle(listing.Title) == 0 {
			continue
		}
		base, err := gitOutput(ctx, launcher, checkout, gitDirectory, checkout, gitRevParse, listing.MergeCommit.OID+gitFirstParent)
		if err != nil {
			continue
		}
		files := make([]string, 0, len(listing.Files))
		for _, file := range listing.Files {
			files = append(files, file.Path)
		}
		pulls = append(pulls, evaluate.MergedPull{Number: listing.Number, Title: listing.Title, MergeCommit: listing.MergeCommit.OID, BaseCommit: base, Files: files})
	}
	return pulls, nil
}

// evolutionTickets are the tickets the loop evolves the harness on: every
// open mining ticket and every ticket a fixer session has worked.
func evolutionTickets(ctx context.Context, launcher *proc.HostLauncher, checkout string, state string) ([]int64, error) {
	origin, err := gitOutput(ctx, launcher, checkout, gitDirectory, checkout, gitRemote, gitGetURL, gitOrigin)
	if err != nil {
		return nil, err
	}
	slug, err := ouroboros.RepositorySlug(origin)
	if err != nil {
		return nil, err
	}
	tickets, err := ouroboros.NewGitHubTickets(launcher, slug)
	if err != nil {
		return nil, err
	}
	mining, err := tickets.ListMiningTickets(ctx)
	if err != nil {
		return nil, err
	}
	var evolution []int64
	for _, ticket := range mining {
		evolution = append(evolution, ticket.Number)
	}
	fixers, err := fixerTickets(ctx, state)
	if err != nil {
		return nil, err
	}
	evolution = append(evolution, fixers...)
	slices.Sort(evolution)
	return slices.Compact(evolution), nil
}

// fixerTickets are the tickets the loop's fixer sessions worked, from its
// ledger in the state directory's database.
func fixerTickets(ctx context.Context, state string) ([]int64, error) {
	pool, err := openStatePool(ctx, state)
	if err != nil {
		return nil, err
	}
	defer pool.Close()
	ledger, err := ouroboros.NewLedger(pool)
	if err != nil {
		return nil, err
	}
	fixers, err := ledger.FixersStartedSince(ctx, time.Time{})
	if err != nil {
		return nil, err
	}
	tickets := make([]int64, 0, len(fixers))
	for _, fixer := range fixers {
		tickets = append(tickets, fixer.Ticket)
	}
	return tickets, nil
}

// The candidate kinds csf eval score measures: each is one evaluator
// (pkg/evaluate) over the held-out set it owns.
const (
	scoreBuilds = "builds"
	scoreModels = "models"
)

// scoreKinds registers each candidate kind's evaluation; builds is the
// default when no kind is named.
var scoreKinds = map[string]func(ctx context.Context, launcher *proc.HostLauncher, arguments []string, output io.Writer, logger *slog.Logger) error{
	scoreBuilds: evalScoreBuilds,
	scoreModels: evalScoreModels,
}

// evalScore runs csf eval score [builds|models] [flags].
func evalScore(ctx context.Context, launcher *proc.HostLauncher, arguments []string, output io.Writer, logger *slog.Logger) error {
	kind := scoreBuilds
	if len(arguments) > 0 && !strings.HasPrefix(arguments[0], "-") {
		kind, arguments = arguments[0], arguments[1:]
	}
	score, found := scoreKinds[kind]
	if !found {
		return fmt.Errorf("%w: no candidate kind %q (%s or %s)", errUsageEval, kind, scoreBuilds, scoreModels)
	}
	return score(ctx, launcher, arguments, output, logger)
}

// evalScoreBuilds scores the host's build on the current suite: the last
// -burst tickets go to a burst shard (a dry run when asked or when the
// credentials are missing, their replays then run here and the output says
// so), the rest replay on this host, and every replay is recorded centrally.
func evalScoreBuilds(ctx context.Context, launcher *proc.HostLauncher, arguments []string, output io.Writer, logger *slog.Logger) error {
	flags := flag.NewFlagSet(commandName+" "+verbEval+" "+evalScoreVerb+" "+scoreBuilds, flag.ContinueOnError)
	stateDirectory := flags.String(stateFlag, "", "state directory of the host whose build is scored (default ~/"+defaultStateDirectory+")")
	endpoint := flags.String(endpointFlag, "", "harness address (default: the one <state>/"+HostRecordFile+" records)")
	repository := flags.String(repositoryFlag, "", "checkout the replays' repository is copied from (default: the mining loop's)")
	work := flags.String(evalWorkFlag, "", "directory holding the replays' repository (default ~/"+defaultEvalWork+")")
	burst := flags.Int(evalBurstFlag, 0, "suite tickets to replay on a cloud burst node")
	dryRun := flags.Bool(evalDryRunFlag, false, "show the burst shard's job and launch nothing")
	flavor := flags.String(evalFlavorFlag, defaultFlavor, "the burst node's hardware flavor")
	capUSD := flags.Float64(evalCapFlag, defaultShardCap, "the burst shard's spend cap in dollars")
	parallel := flags.Int(evalParallelFlag, 1, "replays running at once on this host")
	poll := flags.Duration(evalPollFlag, defaultEvalPoll, "how often a replay's event log is read")
	cloudFlags := addCloudFlags(flags)
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	state, err := stateRoot(*stateDirectory)
	if err != nil {
		return err
	}
	build, err := hostBuild(state)
	if err != nil {
		return err
	}
	store, closeStore, err := openSuiteStore(ctx, state)
	if err != nil {
		return err
	}
	defer closeStore()
	suite, err := store.LatestSuite(ctx)
	if err != nil {
		return err
	}
	checkout, err := loopCheckout(state, *repository)
	if err != nil {
		return err
	}
	replayRepository, err := prepareReplayRepository(ctx, launcher, checkout, *work)
	if err != nil {
		return err
	}
	corpus, err := iofs.NewHostFiles(state)
	if err != nil {
		return err
	}
	replayer, err := newHostReplayer(state, *endpoint, launcher, *poll, *parallel, logger)
	if err != nil {
		return err
	}
	// The last -burst jobs go to a burst shard; a dry run leaves them here.
	shard := func(ctx context.Context, jobs []evaluate.Job) ([]evaluate.Job, error) {
		split := max(0, len(jobs)-max(0, *burst))
		hostJobs, burstJobs := jobs[:split], jobs[split:]
		if len(burstJobs) == 0 {
			return hostJobs, nil
		}
		launched, err := launchSuiteShard(ctx, cloudFlags, state, *endpoint, launcher, logger, build, suite, burstJobs, *flavor, *capUSD, *dryRun)
		if err != nil {
			return nil, err
		}
		if err := printJSON(output, launched); err != nil {
			return nil, err
		}
		if launched.DryRun {
			fmt.Fprintf(output, "burst shard of %d replays was a dry run (missing: %s); they replay on this host instead\n", len(burstJobs), strings.Join(launched.Missing, ", "))
			return jobs, nil
		}
		return hostJobs, nil
	}
	evaluator, err := evaluate.NewBuildEvaluator(store, replayer, suite, corpus, replayRepository, evaluate.WithShard(shard))
	if err != nil {
		return err
	}
	rows := evaluation.Each(ctx, evaluator, []string{build})
	if rows[0].Err != nil {
		return rows[0].Err
	}
	return printJSON(output, rows[0].Result)
}

// evalScoreModels measures every declared decision model, or the one -model
// names, on the committed held-out tree questions and prints the eval table:
// one row per measured model with its accuracy, expected calibration error,
// median latency and decisions per second. The table is the one committed
// under io/net/model/jev; the endpoint is named on the command line rather
// than compiled in, so no server address is tracked. A model the server
// cannot load is left out of the table rather than failing the run.
func evalScoreModels(ctx context.Context, _ *proc.HostLauncher, arguments []string, output io.Writer, logger *slog.Logger) error {
	flags := flag.NewFlagSet(commandName+" "+verbEval+" "+evalScoreVerb+" "+scoreModels, flag.ContinueOnError)
	endpoint := flags.String(endpointFlag, jev.DefaultOllamaEndpoint, "Ollama server URL")
	modelName := flags.String(modelFlag, "", "one declared model to measure (default: every declared model)")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("%w: unexpected %q", errUsageEval, flags.Arg(0))
	}
	candidates, err := jev.EvaluationCandidates(*modelName)
	if err != nil {
		return err
	}
	client, err := iohttp.NewHTTPClient(ionet.NewHostNetwork(), iohttp.WithClientTimeout(modelEvalTimeout))
	if err != nil {
		return err
	}
	evaluator, err := jev.NewModelEvaluator(client, jev.WithEvaluatorEndpoint(*endpoint),
		jev.WithUnloadFailures(func(model string, err error) { logger.Warn("unload", "model", model, "error", err) }))
	if err != nil {
		return err
	}
	table := jev.EvalTable{}
	for _, row := range evaluation.Each(ctx, evaluator, candidates) {
		if row.Err != nil {
			logger.Warn("not measured", "model", row.Candidate.Name, "error", row.Err)
			continue
		}
		table = append(table, row.Result)
	}
	if len(table) == 0 {
		return jev.ErrNoMeasuredModel
	}
	return printJSON(output, table)
}

// prepareReplayRepository keeps the replays' repository current: a bare copy
// of the checkout's branches, and a clone of it whose origin is that copy,
// so a replay's push lands there and nowhere else.
func prepareReplayRepository(ctx context.Context, launcher *proc.HostLauncher, checkout string, work string) (string, error) {
	if work == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		work = filepath.Join(home, defaultEvalWork)
	}
	origin, repository := filepath.Join(work, evalOrigin), filepath.Join(work, evalRepository)
	if _, err := os.Stat(origin); errors.Is(err, os.ErrNotExist) {
		if _, err := gitOutput(ctx, launcher, checkout, gitClone, gitQuiet, gitBare, checkout, origin); err != nil {
			return "", err
		}
	}
	if _, err := gitOutput(ctx, launcher, origin, gitDirectory, origin, gitFetch, gitQuiet, checkout, gitMainRefspecs); err != nil {
		return "", err
	}
	if _, err := os.Stat(repository); errors.Is(err, os.ErrNotExist) {
		if _, err := gitOutput(ctx, launcher, work, gitClone, gitQuiet, origin, repository); err != nil {
			return "", err
		}
	}
	if _, err := gitOutput(ctx, launcher, repository, gitDirectory, repository, gitFetch, gitQuiet, gitOrigin); err != nil {
		return "", err
	}
	return repository, nil
}

// newHostReplayer grants a replayer the harness host at state through its
// client.
func newHostReplayer(state string, endpoint string, launcher *proc.HostLauncher, poll time.Duration, parallel int, logger *slog.Logger) (*evaluate.Replayer, error) {
	target, err := resolveEndpoint(endpoint, state)
	if err != nil {
		return nil, err
	}
	client, err := csf.NewClient(target, &http.Client{Timeout: clientTimeout}, clientWarnings)
	if err != nil {
		return nil, err
	}
	files, err := iofs.NewHostFiles(state)
	if err != nil {
		return nil, err
	}
	return evaluate.NewReplayer(
		evaluate.WithHost(evaluate.Host{Admit: client.CheckAgentSessionAdmission, Submit: client.SubmitAgentSession, Get: client.GetAgentSession, Cancel: client.CancelAgentSession}),
		evaluate.WithLauncher(launcher), evaluate.WithState(state, files), evaluate.WithClock(clock.NewSystemClock(), poll),
		evaluate.WithParallel(parallel), evaluate.WithLogger(logger),
	)
}

// launchSuiteShard sends burst jobs to a cloud node. The job is built here
// as a dry run first, which needs no running service; only when a launch is
// asked for and nothing is missing does the host's cloud service, the one
// owner of the cloud record, launch it. Otherwise the dry run is the answer
// and the caller replays those tickets here.
func launchSuiteShard(ctx context.Context, settings *cloudSettings, state string, endpoint string, launcher *proc.HostLauncher, logger *slog.Logger,
	build string, suite evaluate.Suite, jobs []evaluate.Job, flavor string, capUSD float64, dryRun bool) (cloud.LaunchBurstOutput, error) {
	cloudJobs, err := newCloudJobs(ctx, settings, state, launcher, nil, logger)
	if err != nil {
		return cloud.LaunchBurstOutput{}, err
	}
	if cloudJobs == nil {
		return cloud.LaunchBurstOutput{DryRun: true, Missing: []string{"a reachable cloud provider (-" + cloudProviderFlag + ")"}}, nil
	}
	encodedSuite, err := json.Marshal(suite)
	if err != nil {
		return cloud.LaunchBurstOutput{}, err
	}
	encodedJobs, err := json.Marshal(jobs)
	if err != nil {
		return cloud.LaunchBurstOutput{}, err
	}
	tickets := make([]string, 0, len(jobs))
	for _, job := range jobs {
		tickets = append(tickets, fmt.Sprintf("#%d", job.Ticket.Number))
	}
	input := cloud.LaunchSuiteShardInput{Build: build, Suite: encodedSuite, Jobs: encodedJobs, Tickets: tickets, Flavor: flavor, CapUSD: capUSD, DryRun: true}
	shard, err := cloudJobs.LaunchSuiteShard(ctx, input)
	if err != nil || dryRun || len(shard.Missing) > 0 {
		return shard, err
	}
	target, err := resolveEndpoint(endpoint, state)
	if err != nil {
		return shard, err
	}
	input.DryRun = false
	var launched cloud.LaunchBurstOutput
	var printed strings.Builder
	err = requestCloud(ctx, http.MethodPost, strings.TrimRight(target, "/")+cloud.SuitePath, input, &launched, &printed)
	return launched, err
}

// evalReplay runs a shard's replays on the host at state with no database:
// the burst node's half of csf eval score. Each replay is appended to -out
// as one JSON line for csf eval record.
func evalReplay(ctx context.Context, launcher *proc.HostLauncher, arguments []string, logger *slog.Logger) error {
	flags := flag.NewFlagSet(commandName+" "+verbEval+" "+evalReplayVerb, flag.ContinueOnError)
	stateDirectory := flags.String(stateFlag, "", "state directory of the host the replays run on")
	suiteFile := flags.String(evalSuiteFileFlag, "", "the suite, as csf eval score encodes it")
	jobsFile := flags.String(evalJobsFileFlag, "", "the replay jobs, as csf eval score encodes them")
	build := flags.String(evalBuildFlag, "", "the build the replays run on")
	node := flags.String(evalNodeFlag, evaluate.NodeBurst, "the node kind the replays are recorded under")
	repository := flags.String(repositoryFlag, "", "the repository the replays branch from on this node")
	out := flags.String(evalOutFlag, "", "file each replay is appended to, one JSON line")
	poll := flags.Duration(evalPollFlag, defaultEvalPoll, "how often a replay's event log is read")
	parallel := flags.Int(evalParallelFlag, 1, "replays running at once")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *suiteFile == "" || *jobsFile == "" || *out == "" || !buildExpression.MatchString(*build) {
		return errUsageEval
	}
	state, err := stateRoot(*stateDirectory)
	if err != nil {
		return err
	}
	var suite evaluate.Suite
	if err := readJSONFile(*suiteFile, &suite); err != nil {
		return err
	}
	var jobs []evaluate.Job
	if err := readJSONFile(*jobsFile, &jobs); err != nil {
		return err
	}
	if *repository != "" {
		if jobs, err = evaluate.Relocate(jobs, *repository); err != nil {
			return err
		}
	}
	replayer, err := newHostReplayer(state, "", launcher, *poll, *parallel, logger)
	if err != nil {
		return err
	}
	return replayer.Run(ctx, *build, suite, *node, jobs, func(_ context.Context, replay evaluate.Replay) error {
		line, err := json.Marshal(replay)
		if err != nil {
			return err
		}
		return appendLine(*out, line)
	})
}

// evalRecord records the replays a burst shard wrote, one JSON line each.
func evalRecord(ctx context.Context, arguments []string, output io.Writer) error {
	flags := flag.NewFlagSet(commandName+" "+verbEval+" "+evalRecordVerb, flag.ContinueOnError)
	stateDirectory := flags.String(stateFlag, "", "harness state directory whose database records the replays")
	file := flags.String(evalFileFlag, "", "replays.jsonl a shard wrote")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *file == "" {
		return errUsageEval
	}
	state, err := stateRoot(*stateDirectory)
	if err != nil {
		return err
	}
	store, closeStore, err := openSuiteStore(ctx, state)
	if err != nil {
		return err
	}
	defer closeStore()
	handle, err := os.Open(*file)
	if err != nil {
		return err
	}
	defer func() { _ = handle.Close() }()
	scanner := bufio.NewScanner(handle)
	recorded := 0
	for scanner.Scan() {
		var replay evaluate.Replay
		if err := json.Unmarshal(scanner.Bytes(), &replay); err != nil {
			return fmt.Errorf("eval: line %d of %s: %w", recorded+1, *file, err)
		}
		if err := store.RecordReplay(ctx, replay); err != nil {
			return err
		}
		recorded++
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "recorded %d replays from %s\n", recorded, *file)
	return err
}

// shown is what csf eval show prints: the score and its citation line.
type shown struct {
	Score    evaluate.Score `json:"score"`
	Citation string         `json:"citation,omitempty"`
	Reason   string         `json:"no_citation_because,omitempty"`
}

// evalShow prints a build's score on the current suite and, when it and the
// build it is measured against are both complete, its citation line.
func evalShow(ctx context.Context, arguments []string, output io.Writer) error {
	flags := flag.NewFlagSet(commandName+" "+verbEval+" "+evalShowVerb, flag.ContinueOnError)
	stateDirectory := flags.String(stateFlag, "", "harness state directory (default ~/"+defaultStateDirectory+")")
	build := flags.String(evalBuildFlag, "", "build to show (default: the running host's)")
	versus := flags.String(evalVersusFlag, "", "build the delta is measured against (default: the running host's)")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	state, err := stateRoot(*stateDirectory)
	if err != nil {
		return err
	}
	if *build == "" || *versus == "" {
		live, err := hostBuild(state)
		if err != nil {
			return err
		}
		if *build == "" {
			*build = live
		}
		if *versus == "" {
			*versus = live
		}
	}
	store, closeStore, err := openSuiteStore(ctx, state)
	if err != nil {
		return err
	}
	defer closeStore()
	suite, err := store.LatestSuite(ctx)
	if err != nil {
		return err
	}
	score, err := store.ScoreOn(ctx, *build, suite)
	if err != nil {
		return err
	}
	result := shown{Score: score}
	if line, err := store.Recompute(ctx, *build, suite.Version, *versus); err == nil {
		result.Citation = line
	} else {
		result.Reason = err.Error()
	}
	return printJSON(output, result)
}

// evalCite runs the proof check on a pull request's body, from -body or
// from pull request -pr of the loop checkout's origin.
func evalCite(ctx context.Context, launcher *proc.HostLauncher, arguments []string, output io.Writer) error {
	flags := flag.NewFlagSet(commandName+" "+verbEval+" "+evalCiteVerb, flag.ContinueOnError)
	stateDirectory := flags.String(stateFlag, "", "harness state directory (default ~/"+defaultStateDirectory+")")
	bodyFile := flags.String(evalBodyFlag, "", "file holding the pull request body")
	pull := flags.String(evalPullFlag, "", "pull request number whose body to check")
	repository := flags.String(repositoryFlag, "", "checkout whose origin holds -pr (default: the mining loop's)")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if (*bodyFile == "") == (*pull == "") {
		return errUsageEval
	}
	state, err := stateRoot(*stateDirectory)
	if err != nil {
		return err
	}
	var body string
	if *bodyFile != "" {
		content, err := os.ReadFile(*bodyFile)
		if err != nil {
			return err
		}
		body = string(content)
	} else {
		checkout, err := loopCheckout(state, *repository)
		if err != nil {
			return err
		}
		origin, err := gitOutput(ctx, launcher, checkout, gitDirectory, checkout, gitRemote, gitGetURL, gitOrigin)
		if err != nil {
			return err
		}
		slug, err := ouroboros.RepositorySlug(origin)
		if err != nil {
			return err
		}
		viewed, err := launcher.Run(ctx, proc.Command{Executable: ghExecutable, Directory: checkout,
			Arguments: []string{ghPullList, ghViewVerb, *pull, ghRepoFlag, slug, ghJSONFlag, ghBodyFields, ghJQFlag, ghBodyQuery}})
		if err != nil {
			return err
		}
		body = string(viewed.Stdout)
	}
	if err := citationCheck(state)(ctx, body); err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "proof check passed: %d improvement claims, %d citations\n", len(evaluate.Claims(body)), len(evaluate.Citations(body)))
	return err
}

// citationCheck is the proof check against the state directory's records;
// the database is opened only for a body that claims an improvement.
func citationCheck(state string) func(ctx context.Context, body string) error {
	return func(ctx context.Context, body string) error {
		if len(evaluate.Claims(body)) == 0 {
			return nil
		}
		store, closeStore, err := openSuiteStore(ctx, state)
		if err != nil {
			return fmt.Errorf("%w: the records could not be read: %w", evaluate.ErrUncited, err)
		}
		defer closeStore()
		return evaluate.CheckClaims(ctx, body, store.Recompute)
	}
}

// scoreBeforeLive is csf upgrade's score check: the staged binary's build
// must have a complete score on the current suite and must not regress
// beyond the bound against the running host's build.
func scoreBeforeLive(state string, progress func(line string)) func(ctx context.Context, staged string) error {
	return func(ctx context.Context, staged string) error {
		candidate, err := binaryBuild(staged)
		if err != nil {
			return err
		}
		store, closeStore, err := openSuiteStore(ctx, state)
		if err != nil {
			return err
		}
		defer closeStore()
		suite, err := store.LatestSuite(ctx)
		if err != nil {
			return err
		}
		candidateReplays, err := store.Replays(ctx, candidate, suite.Version)
		if err != nil {
			return err
		}
		score := evaluate.ScoreOf(candidate, suite, candidateReplays, 0)
		progress(fmt.Sprintf("score before live: build %s on suite v%d: %d of %d replays", candidate, suite.Version, score.Replays, score.Expected))
		live, err := hostBuild(state)
		if err != nil {
			live = ""
		}
		liveReplays, err := store.Replays(ctx, live, suite.Version)
		if err != nil {
			return err
		}
		comparison, err := evaluate.Admit(suite, candidateReplays, liveReplays, candidate, live)
		if err != nil {
			return err
		}
		if score.PerK != nil {
			progress(fmt.Sprintf("score before live: %.1f per 1k tool calls [%.1f, %.1f]", *score.PerK, *score.Low, *score.High))
		}
		if comparison != nil {
			progress(fmt.Sprintf("score before live: %+.1f per 1k against live %s, bound %.1f over %d paired tickets: admitted", comparison.Delta, live, comparison.Bound, comparison.Paired))
		} else {
			progress("score before live: the live build has no complete score on this suite to compare with: admitted on its own score")
		}
		return nil
	}
}

// hiddenRunsOf lists the held-out run directories under state from the
// state directory's records, opening the database for each read.
func hiddenRunsOf(state string, corpus iofs.IFiles) func(ctx context.Context) (map[string]bool, error) {
	return func(ctx context.Context) (map[string]bool, error) {
		store, closeStore, err := openSuiteStore(ctx, state)
		if err != nil {
			return nil, err
		}
		defer closeStore()
		tickets, err := store.HiddenTickets(ctx)
		if err != nil {
			return nil, err
		}
		return evaluate.HiddenAssignments(corpus, tickets)
	}
}

// suiteCollector is the suite's series on /metrics, read from the pool serve
// holds.
func suiteCollector(pool *csfpg.Pool) prometheus.Collector {
	return evaluate.NewSuiteCollector(func(ctx context.Context) ([]evaluate.Score, error) {
		store, err := evaluate.NewSuiteStore(pool)
		if err != nil {
			return nil, err
		}
		return store.Scores(ctx)
	})
}

// readJSONFile decodes a JSON file into value; value is any because the
// caller owns its type, as with encoding/json.
func readJSONFile(path string, value any) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(content, value)
}

func printJSON(output io.Writer, value any) error {
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, string(content))
	return err
}

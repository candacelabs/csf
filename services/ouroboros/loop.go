// Copyright 2026 Candace Labs

// Package ouroboros is the mining loop: the service that runs every accepted
// miner over the harness corpus all the time, decides for free whether a
// mining ticket can be fixed, launches one fixer session per ticket through
// the harness under a daily budget, merges ready pull requests through the
// merge path, and measures the compounding number the loop exists for. It
// replaces the ad hoc scripts that launched fixers outside the session gates.
//
// It is a service in the harness host app: it owns no listener and no
// process. The binary grants it the ledger over csfpg, the harness's session
// capability, the process capability, the ticket capability over gh, the
// clock, the file and watch capabilities over the corpus and the repository,
// and mounts its four triggers into the cron service. Its own goroutine is
// the corpus watch, started on the scope the runtime hands it.
package ouroboros

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	stdfs "io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
	// NewLoop loads ScheduleLocation, an IANA zone that Go finds by name in
	// the host's time zone database. A host without one, such as the pinned
	// Bazel image or a scratch container, answers "unknown time zone" and
	// NewLoop fails. time/tzdata embeds the database as a fallback that is
	// consulted only when the host has no copy, at a cost of about 450 KB
	// in every binary that mounts the service.
	_ "time/tzdata"

	"github.com/gin-gonic/gin"

	"github.com/candacelabs/csf/csf"
	"github.com/candacelabs/csf/io/kernel/clock"
	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/pkg/atomicfile"
	grammar "github.com/candacelabs/csf/pkg/cron"
	"github.com/candacelabs/csf/runtime"
	cronservice "github.com/candacelabs/csf/services/cron"
	"github.com/candacelabs/csf/services/harness/session"
)

// The triggers the loop declares, as csfcron names them, and their cadence.
// Each interval is derived from a measurement recorded in the README: the
// detector sweep is the catch-up behind the watch, the fixer and merge
// cadences follow the mean fixer session (21.8 min), and the measure follows
// the day the compounding number is computed over.
const (
	// TriggerDetector sweeps every miner over the corpus items that grew
	// since they were last mined; the watch runs the same sweep the moment a
	// log changes, so this is the catch-up after a restart or a missed
	// notification.
	TriggerDetector = "ouroboros.detector"
	// TriggerFixers reconciles the running fixer sessions, pre-checks every
	// open mining ticket and launches fixers under the daily budget.
	TriggerFixers = "ouroboros.fixers"
	// TriggerMergeTrain runs the merge path over every ready pull request,
	// oldest first; one run of the path builds the checkers and takes about
	// ten minutes, so the train runs every thirty.
	TriggerMergeTrain = "ouroboros.merge_train"
	// TriggerMeasure computes the struggle rate, its compounding rate and the
	// loop's own yields, records them as series and projects them for the
	// ops view.
	TriggerMeasure = "ouroboros.measure"
	// ScheduleLocation is the time zone the triggers, the budget day and the
	// measured days are declared in: the one #330's measurement uses.
	ScheduleLocation = "America/Los_Angeles"

	detectorInterval   = 15 * time.Minute
	fixersInterval     = 10 * time.Minute
	mergeTrainInterval = 30 * time.Minute
	measureInterval    = time.Hour
	// watchSettle is how long the watch waits after the last change to a log
	// before mining it: a running executor logs several lines a second and the
	// template miner reads every log in 1.3 s, so one settle bounds the
	// reruns to one per burst rather than one per line.
	watchSettle = 10 * time.Second

	// SnapshotFile is where the loop projects its numbers, under the state
	// directory, for the ops view that follows that directory.
	SnapshotFile = "ouroboros.json"
	// DefaultDailyBudgetUSDMicros is the operator's hard cap on fixer spend:
	// $100 per day, in integer microdollars like every amount in csfpg.
	DefaultDailyBudgetUSDMicros = 100_000_000
	// DefaultFixerModel is the model fixers run on: every real session runs
	// claude-opus-5-5 (operator, 2026-10-05), which the harness's model
	// policy enforces at submit.
	DefaultFixerModel = "claude-opus-5-5"
	// MinersDirectory holds one directory per miner under the repository.
	MinersDirectory = "services/ouroboros/miners"
	// MinerExecutables holds each miner's built executable, as Bazel publishes
	// it under the repository.
	MinerExecutables = "bazel-bin/services/ouroboros/miners"

	// APIPath serves the latest snapshot as JSON on the host's router.
	APIPath = "/api/ouroboros"

	watchOwner        = "corpus watch"
	rootName          = "."
	snapshotFileMode  = 0o600
	maxMergerBytes    = 128
	maxFixerModelLen  = 200
	usdMicrosPerUSD   = 1_000_000
	recentRowLimit    = 200
	recentDaysShown   = 14
	defaultHTTPStatus = http.StatusOK
)

var (
	// ErrInvalidOption reports a nil option or a value the service cannot use.
	ErrInvalidOption = errors.New("ouroboros: invalid option")
	// ErrMissingCapability reports a loop built without a required capability.
	ErrMissingCapability = errors.New("ouroboros: a required capability is missing")
	// ErrAlreadyStarted reports a second Start of one loop.
	ErrAlreadyStarted = errors.New("ouroboros: the loop is already started")
	// ErrBudgetSpent reports a launch refused because the day's budget is
	// spent or reserved by the fixers already running.
	ErrBudgetSpent = errors.New("ouroboros: the daily fixer budget is spent")
	// ErrFixersDisabled reports a launch refused because the loop was built
	// without the fixer switch.
	ErrFixersDisabled = errors.New("ouroboros: fixers are not enabled")
	// ErrSelfMerge reports a pull request the train refused to merge because
	// its commits carry the merger's own session: NO-SELF-MERGE (#249).
	ErrSelfMerge = errors.New("ouroboros: the merger is the author session")
)

// directoryCapability is one host directory the loop was granted: its
// absolute path, for the programs that take paths, and read access to it.
type directoryCapability struct {
	directory string
	files     iofs.IFiles
}

// Loop is the mining loop. Its state between occurrences is the ledger; the
// only in-process datum is the latest snapshot, published whole.
type Loop struct {
	store      IStore
	sessions   csf.IAgentSessions
	launcher   proc.ILauncher
	tickets    ITickets
	clock      clock.IClock
	corpus     directoryCapability
	hidden     HiddenRuns
	watcher    iofs.IWatcher
	state      directoryCapability
	repository directoryCapability
	launch     bool
	budget     int64
	model      string
	merger     string
	merged     MergeObserver
	location   *time.Location
	logger     *slog.Logger
	prechecker *Prechecker
	started    atomic.Bool
	snapshot   atomic.Pointer[Snapshot]
}

var _ runtime.IService = (*Loop)(nil)

// LoopOption configures a [Loop].
type LoopOption func(loop *Loop) error

// WithLedger grants the ledger. Required.
func WithLedger(store IStore) LoopOption {
	return func(loop *Loop) error {
		if store == nil {
			return fmt.Errorf("%w: nil ledger", ErrInvalidOption)
		}
		loop.store = store
		return nil
	}
}

// WithSessions grants the harness's session capability, through which every
// fixer is launched and observed. Required.
func WithSessions(sessions csf.IAgentSessions) LoopOption {
	return func(loop *Loop) error {
		if sessions == nil {
			return fmt.Errorf("%w: nil sessions", ErrInvalidOption)
		}
		loop.sessions = sessions
		return nil
	}
}

// WithLauncher grants the process capability: miners, git and the merge
// path run through it. Required.
func WithLauncher(launcher proc.ILauncher) LoopOption {
	return func(loop *Loop) error {
		if launcher == nil {
			return fmt.Errorf("%w: nil launcher", ErrInvalidOption)
		}
		loop.launcher = launcher
		return nil
	}
}

// WithTickets grants the ticket capability over the repository's issues and
// pull requests. Required.
func WithTickets(tickets ITickets) LoopOption {
	return func(loop *Loop) error {
		if tickets == nil {
			return fmt.Errorf("%w: nil tickets", ErrInvalidOption)
		}
		loop.tickets = tickets
		return nil
	}
}

// WithClock replaces the host's clock, which stamps every ledger row and
// decides the budget day.
func WithClock(source clock.IClock) LoopOption {
	return func(loop *Loop) error {
		if source == nil {
			return fmt.Errorf("%w: nil clock", ErrInvalidOption)
		}
		loop.clock = source
		return nil
	}
}

// WithCorpus grants the harness state directory the miners read: its
// absolute path, read access and change notification. Required.
func WithCorpus(directory string, files iofs.IFiles, watcher iofs.IWatcher) LoopOption {
	return func(loop *Loop) error {
		if directory == "" || files == nil || watcher == nil {
			return fmt.Errorf("%w: the corpus needs a path, its files and its watcher", ErrInvalidOption)
		}
		loop.corpus = directoryCapability{directory: directory, files: files}
		loop.watcher = watcher
		return nil
	}
}

// HiddenRuns lists the run directories of the corpus no miner and no
// measure may read: the evaluation suite's tickets, held out (#416).
type HiddenRuns func(ctx context.Context) (map[string]bool, error)

// WithHidden grants the list of held-out run directories; every corpus
// reader of the loop skips them. Without it nothing is hidden.
func WithHidden(hidden HiddenRuns) LoopOption {
	return func(loop *Loop) error {
		if hidden == nil {
			return fmt.Errorf("%w: nil hidden runs", ErrInvalidOption)
		}
		loop.hidden = hidden
		return nil
	}
}

// hiddenRuns reads the held-out run directories; none without the option.
func (loop *Loop) hiddenRuns(ctx context.Context) (map[string]bool, error) {
	if loop.hidden == nil {
		return map[string]bool{}, nil
	}
	return loop.hidden(ctx)
}

// WithState grants the state directory of the harness host the loop runs
// in: where fixer sessions run and where the snapshot is written. Required.
func WithState(directory string, files iofs.IFiles) LoopOption {
	return func(loop *Loop) error {
		if directory == "" || files == nil {
			return fmt.Errorf("%w: the state directory needs a path and its files", ErrInvalidOption)
		}
		loop.state = directoryCapability{directory: directory, files: files}
		return nil
	}
}

// WithRepository grants the checkout the miners are read from, fixer
// worktrees are created from and the merge path runs in. Required.
func WithRepository(directory string, files iofs.IFiles) LoopOption {
	return func(loop *Loop) error {
		if directory == "" || files == nil {
			return fmt.Errorf("%w: the repository needs a path and its files", ErrInvalidOption)
		}
		loop.repository = directoryCapability{directory: directory, files: files}
		return nil
	}
}

// WithFixerLaunch is the always-on switch: fixers are launched only when it
// is on. Until the harness reports that sandboxed launch is available, the
// binary turns it on only from an explicit flag.
func WithFixerLaunch(enabled bool) LoopOption {
	return func(loop *Loop) error {
		loop.launch = enabled
		return nil
	}
}

// WithDailyBudget sets the hard cap on fixer spend per day, in USD
// microdollars. The default is [DefaultDailyBudgetUSDMicros].
func WithDailyBudget(usdMicros int64) LoopOption {
	return func(loop *Loop) error {
		if usdMicros <= 0 {
			return fmt.Errorf("%w: the daily budget must be positive", ErrInvalidOption)
		}
		loop.budget = usdMicros
		return nil
	}
}

// WithFixerModel sets the model fixers run on.
func WithFixerModel(model string) LoopOption {
	return func(loop *Loop) error {
		if model == "" || len(model) > maxFixerModelLen {
			return fmt.Errorf("%w: the fixer model must be 1 to %d bytes", ErrInvalidOption, maxFixerModelLen)
		}
		loop.model = model
		return nil
	}
}

// WithMergerIdentity names the principal the merge train merges as, which
// NO-SELF-MERGE compares with the sessions a pull request's commits carry.
// Required.
func WithMergerIdentity(identity string) LoopOption {
	return func(loop *Loop) error {
		identity = strings.TrimSpace(identity)
		if identity == "" || len(identity) > maxMergerBytes {
			return fmt.Errorf("%w: the merger identity must be 1 to %d bytes", ErrInvalidOption, maxMergerBytes)
		}
		loop.merger = identity
		return nil
	}
}

// MergeObserver is told about every pull request the merge train merged, once
// the merge is recorded.
type MergeObserver func(ctx context.Context, pull PullRequest)

// WithMergeObserver tells observer about every pull request the merge train
// merges: the slice dispatcher releases what depended on it.
func WithMergeObserver(observer MergeObserver) LoopOption {
	return func(loop *Loop) error {
		if observer == nil {
			return fmt.Errorf("%w: nil merge observer", ErrInvalidOption)
		}
		loop.merged = observer
		return nil
	}
}

// WithLogger receives the loop's own records.
func WithLogger(logger *slog.Logger) LoopOption {
	return func(loop *Loop) error {
		if logger == nil {
			return fmt.Errorf("%w: nil logger", ErrInvalidOption)
		}
		loop.logger = logger
		return nil
	}
}

// NewLoop validates the whole option set and returns a stopped loop: it
// reads nothing and starts no goroutine until mounted.
func NewLoop(options ...LoopOption) (*Loop, error) {
	location, err := time.LoadLocation(ScheduleLocation)
	if err != nil {
		return nil, fmt.Errorf("ouroboros: load %s: %w", ScheduleLocation, err)
	}
	loop := &Loop{
		clock:    clock.NewSystemClock(),
		budget:   DefaultDailyBudgetUSDMicros,
		model:    DefaultFixerModel,
		location: location,
		logger:   slog.New(slog.DiscardHandler),
	}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("%w: nil option", ErrInvalidOption)
		}
		if err := option(loop); err != nil {
			return nil, err
		}
	}
	if loop.store == nil || loop.sessions == nil || loop.launcher == nil || loop.tickets == nil ||
		loop.corpus.files == nil || loop.watcher == nil || loop.state.files == nil || loop.repository.files == nil || loop.merger == "" {
		return nil, ErrMissingCapability
	}
	prechecker, err := NewPrechecker(loop.corpus.files, loop.tickets, loop.launcher, loop.repository.directory)
	if err != nil {
		return nil, err
	}
	loop.prechecker = prechecker
	return loop, nil
}

// Triggers declares the loop's four triggers for the cron service, in
// [ScheduleLocation]. Every one skips an occurrence while the previous one
// still runs, so a long merge never overlaps the next.
func (loop *Loop) Triggers() []cronservice.Option {
	return []cronservice.Option{
		cronservice.WithTrigger(TriggerDetector, grammar.Spec(grammar.Every(detectorInterval)).In(loop.location), loop.detectOccurrence),
		cronservice.WithTrigger(TriggerFixers, grammar.Spec(grammar.Every(fixersInterval)).In(loop.location), loop.fixersOccurrence),
		cronservice.WithTrigger(TriggerMergeTrain, grammar.Spec(grammar.Every(mergeTrainInterval)).In(loop.location), loop.mergeTrainOccurrence),
		cronservice.WithTrigger(TriggerMeasure, grammar.Spec(grammar.Every(measureInterval)).In(loop.location), loop.measureOccurrence),
	}
}

func (loop *Loop) detectOccurrence(ctx context.Context, _ cronservice.Occurrence) error {
	return loop.Detect(ctx, nil)
}

func (loop *Loop) fixersOccurrence(ctx context.Context, _ cronservice.Occurrence) error {
	return loop.Fixers(ctx)
}

func (loop *Loop) mergeTrainOccurrence(ctx context.Context, _ cronservice.Occurrence) error {
	return loop.MergeTrain(ctx)
}

func (loop *Loop) measureOccurrence(ctx context.Context, _ cronservice.Occurrence) error {
	_, err := loop.Measure(ctx)
	return err
}

// Start starts the corpus watch on scope, so every change to a run's event
// log mines that log once the change has settled, and runs the detector
// and the measure once, so the numbers are there as soon as the host is.
func (loop *Loop) Start(scope *runtime.Scope) error {
	if !loop.started.CompareAndSwap(false, true) {
		return ErrAlreadyStarted
	}
	if err := scope.GoOwner(watchOwner, loop.watch); err != nil {
		return err
	}
	return scope.Go(func(ctx context.Context) error {
		if err := loop.Detect(ctx, nil); err != nil && ctx.Err() == nil {
			loop.logger.Warn("ouroboros: first sweep incomplete", "error", err)
		}
		if _, err := loop.Measure(ctx); err != nil && ctx.Err() == nil {
			loop.logger.Warn("ouroboros: first measure incomplete", "error", err)
		}
		return nil
	})
}

// Register mounts the snapshot route on the caller's router.
func (loop *Loop) Register(router gin.IRouter) {
	router.GET(APIPath, func(request *gin.Context) {
		snapshot := loop.snapshot.Load()
		if snapshot == nil {
			snapshot = &Snapshot{}
		}
		request.JSON(defaultHTTPStatus, snapshot)
	})
}

// watch follows the corpus: the state directory itself, so a new run
// directory is adopted, and every run directory, so a line landing in its
// event log is seen. Changed logs are mined together once no change has
// arrived for watchSettle.
func (loop *Loop) watch(ctx context.Context) error {
	watch, err := loop.watcher.Watch(ctx)
	if err != nil {
		return err
	}
	if err := watch.Add(rootName); err != nil {
		return err
	}
	entries, err := loop.corpus.files.ReadDir(rootName)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			loop.adopt(watch.Add, entry.Name())
		}
	}
	pending := map[string]struct{}{}
	var settled <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return nil
		case change, open := <-watch.Changes:
			if !open {
				return nil
			}
			if change.Err != nil {
				return change.Err
			}
			assignment, file := path.Split(change.Name)
			assignment = strings.TrimSuffix(assignment, "/")
			switch {
			case assignment == "" && loop.isRunDirectory(file):
				loop.adopt(watch.Add, file)
			case file == session.EventsFile && !strings.Contains(assignment, "/"):
				pending[assignment] = struct{}{}
				if settled == nil {
					settled = loop.clock.After(watchSettle)
				}
			}
		case <-settled:
			settled = nil
			items := make([]string, 0, len(pending))
			for assignment := range pending {
				items = append(items, assignment)
			}
			pending = map[string]struct{}{}
			if err := loop.Detect(ctx, items); err != nil && ctx.Err() == nil {
				loop.logger.Warn("ouroboros: changed logs not mined", "error", err)
			}
		}
	}
}

// adopt watches one run directory; a directory gone before it could be
// watched is not an error.
func (loop *Loop) adopt(add func(name string) error, assignment string) {
	if err := add(assignment); err != nil && !errors.Is(err, os.ErrNotExist) {
		loop.logger.Warn("ouroboros: run directory not watched", "assignment", assignment, "error", err)
	}
}

func (loop *Loop) isRunDirectory(name string) bool {
	if name == "" {
		return false
	}
	info, err := stdfs.Stat(loop.corpus.files, name)
	return err == nil && info.IsDir()
}

// writeSnapshot publishes the measure in process and projects it to the
// state directory for the ops view, replacing the file atomically so a
// reader never sees a half-written one.
func (loop *Loop) writeSnapshot(snapshot *Snapshot) error {
	loop.snapshot.Store(snapshot)
	content, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	target := filepath.Join(loop.state.directory, SnapshotFile)
	if err := atomicfile.WriteFile(target, content, snapshotFileMode); err != nil {
		return fmt.Errorf("ouroboros: replace %s: %w", SnapshotFile, err)
	}
	return nil
}

// day is the calendar date of an instant in the loop's zone, as the series
// and the budget spell it.
func (loop *Loop) day(at time.Time) string {
	return at.In(loop.location).Format(time.DateOnly)
}

// dayStart is the first instant of the day holding at, in the loop's zone.
func (loop *Loop) dayStart(at time.Time) time.Time {
	local := at.In(loop.location)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loop.location)
}

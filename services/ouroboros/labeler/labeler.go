// Copyright 2026 Candace Labs

// Package labeler is the ouroboros labeler (csf_staging#318): for a mining
// ticket that names no real instance, it finds candidate spans in the corpus
// the ticket's miner would read, asks a local model to label them, and keeps
// only the labels that reproduce. Labels were the bottleneck of the mining
// loop: on 2026-10-03, 85 of 107 fixer sessions invented an instance because
// their ticket named none. The labeler proposes real ones, at no model spend,
// on a GPU that would otherwise idle.
//
// One ticket is one [Label] call, in four steps. A deterministic prefilter
// turns the ticket's proposal into search terms and scans the corpus
// (harness event logs through the file capability; issues, pull requests
// and commits through the process capability) for lines that carry them,
// ranked by inverse document frequency. The local model, behind the brain
// contract, labels each batch of candidates with a verbatim quote and a
// reason. The acceptance rule, with no human in the loop, keeps a positive
// only when it reproduces: through the ticket's miner when one is
// registered, otherwise by its quote being on the line it names. Every
// label is a typed record (ProposedLabel) handed to the sink the binary
// grants, and the run's measurements (LabelerRun) are published after every
// batch for the ops view.
//
// The GPU is shared. Before each batch the labeler yields while another
// container created with the GPU is running, and every request carries a
// keep-alive derived from the measured gap between batches, so the model
// unloads soon after a run ends. Every threshold here is derived from a
// measurement, and the derivation stands beside it.
//
// It is a service in the house sense: no process, no flags, no environment,
// no listener; the harness binary's label verb composes it.
package labeler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/candacelabs/csf/io/kernel/clock"
	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/io/net/model"
	"github.com/candacelabs/csf/io/net/model/ollama"
	"github.com/candacelabs/csf/io/ipc/proc"
	ouroborosv1 "github.com/candacelabs/csf/proto/candace/ouroboros/v1"
)

// The phases a run record reports.
const (
	PhaseLabeling = "labeling"
	PhaseYielding = "yielding"
	PhaseFinished = "finished"
)

// The derived defaults, each with the measurement it comes from.
const (
	// DefaultBatches is how many batches of candidates one ticket gets. A
	// batch of 44 to 51 candidates measured 34 to 37 s of model time on
	// qwen3:8b at a 16k window (2026-10-04), so two batches put the 85-ticket
	// proof run at about 100 minutes, and the second batch is where a ticket
	// whose best-ranked lines are all one run's finds its other instances.
	DefaultBatches = 2
	// DefaultContextWindow is the window the brain is built with, which the
	// batch size is derived from. The first run on 2026-10-04 at 8192 put
	// 7674 prompt tokens and an 824-token answer in one batch, past the
	// window; 16384 holds a batch of a few dozen candidates with room, and
	// its KV cache costs qwen3:8b about 1.2 GB more of the 12 GB GPU.
	DefaultContextWindow = 16384
	// DefaultYieldWait is how long the labeler waits before looking again at
	// the GPU's other consumers: one batch's worth of model time (34 s and
	// 37 s measured on 2026-10-04 at the 16k window), so a consumer that
	// leaves is noticed about as fast as a batch would run.
	DefaultYieldWait = 35 * time.Second
	// answerEnvelopeTokens is the answer's JSON object around its labels.
	answerEnvelopeTokens = 32
	// answerTokens is the smallest window a labeler accepts: room for the
	// envelope and one label.
	answerTokens = 128
	// initialCharsPerToken stands until the first answer is measured: the
	// first run of 2026-10-04 evaluated 16.2k prompt characters as 7674
	// tokens. Every answer replaces it with its own prompt's ratio.
	initialCharsPerToken = 2.1
	// initialTokensPerCandidate and initialAnswerPerLabel stand until the
	// first answer is measured, and every answer raises them to its own
	// measurement when that is higher: the runs of 2026-10-04 cost 222 to 265
	// prompt tokens per candidate (event-log lines the low end, issue bodies
	// the high) and 39 to 57 answer tokens per label. The running maximum is
	// what keeps a later batch inside the window when the corpus mix shifts.
	initialTokensPerCandidate = 265
	initialAnswerPerLabel     = 57
	// excerptChars is the window of a candidate's text the model reads.
	excerptChars = 320

	unmeasured = -1
)

var (
	// ErrInvalidOption reports a nil option or a value the labeler cannot use.
	ErrInvalidOption = errors.New("labeler: invalid option")
	// ErrMissingCapability reports a labeler built without a required
	// capability.
	ErrMissingCapability = errors.New("labeler: a required capability is missing")
	// ErrNoRequest reports a Label of a nil request or one naming no ticket.
	ErrNoRequest = errors.New("labeler: a label request naming a ticket is required")
	// ErrNoRepository reports a request naming no repository when the labeler
	// was built without a default one.
	ErrNoRepository = errors.New("labeler: the request names no repository and the labeler has no default")
	// ErrMalformedAnswer reports a model answer that is not the schema's
	// object, which the server should have prevented.
	ErrMalformedAnswer = errors.New("labeler: the model's answer is not the labels object")
)

// LabelSink receives every proposed label as it is judged.
type LabelSink func(label *ouroborosv1.ProposedLabel) error

// RunSink receives the run record after every batch and phase change.
type RunSink func(run *ouroborosv1.LabelerRun) error

// corpusReader reads one corpus kind into a scan.
type corpusReader func(ctx context.Context, scan *corpusScan, request *ouroborosv1.LabelRequest) error

// Labeler labels mining tickets. One value serves one run, ticket after
// ticket, on the caller's goroutine; it starts none of its own.
type Labeler struct {
	brain          model.IBrain[*ollama.Prompt, ollama.Answer]
	modelName      string
	stateDirectory string
	state          iofs.IFiles
	hidden         func(ctx context.Context) (map[string]bool, error)
	launcher       proc.ILauncher
	containers     IContainers
	modelServer    string
	checkout       string
	repository     string
	clock          clock.IClock
	logger         *slog.Logger
	miners         map[string]string
	batches        int
	contextWindow  int
	yieldWait      time.Duration
	labels         LabelSink
	runs           RunSink
	readers        map[string]corpusReader
	gpuByContainer map[string]bool
	measured       measurements
}

// measurements is what the run has measured so far; the run record is a
// projection of it.
type measurements struct {
	started            time.Time
	batchStarts        []time.Time
	load               time.Duration
	charsPerToken      float64
	tokensPerCandidate float64
	answerPerLabel     float64
	gpu                []gpuSample
	keepAlive          time.Duration
	tickets            int64
	withInstance       int64
	proposed           int64
	positives          int64
	accepted           int64
	rejected           int64
	batches            int64
	yields             int64
}

// LabelerOption configures a [Labeler].
type LabelerOption func(labeler *Labeler) error

// WithBrain grants the model, behind the brain contract, and names it for
// the records. Required.
func WithBrain(brain model.IBrain[*ollama.Prompt, ollama.Answer], modelName string) LabelerOption {
	return func(labeler *Labeler) error {
		if brain == nil || modelName == "" {
			return fmt.Errorf("%w: the brain needs a value and a model name", ErrInvalidOption)
		}
		labeler.brain, labeler.modelName = brain, modelName
		return nil
	}
}

// WithState grants the harness state directory: its absolute path, for the
// miners that read event logs, and read access to it. Required.
func WithState(directory string, files iofs.IFiles) LabelerOption {
	return func(labeler *Labeler) error {
		if directory == "" || files == nil {
			return fmt.Errorf("%w: the state directory needs a path and its files", ErrInvalidOption)
		}
		labeler.stateDirectory, labeler.state = directory, files
		return nil
	}
}

// WithHidden grants the list of held-out run directories (the evaluation
// suite's tickets, #416); the event-log corpus skips them. Without it
// nothing is hidden.
func WithHidden(hidden func(ctx context.Context) (map[string]bool, error)) LabelerOption {
	return func(labeler *Labeler) error {
		if hidden == nil {
			return fmt.Errorf("%w: nil hidden runs", ErrInvalidOption)
		}
		labeler.hidden = hidden
		return nil
	}
}

// WithLauncher grants the process capability, which reads the issue, pull
// request and commit corpora, measures the GPU and runs miners. Required.
func WithLauncher(launcher proc.ILauncher) LabelerOption {
	return func(labeler *Labeler) error {
		if launcher == nil {
			return fmt.Errorf("%w: nil launcher", ErrInvalidOption)
		}
		labeler.launcher = launcher
		return nil
	}
}

// WithContainers grants the container capability and names the model
// server's own container, which is never counted as another GPU consumer.
// Required.
func WithContainers(containers IContainers, modelServer string) LabelerOption {
	return func(labeler *Labeler) error {
		if containers == nil || modelServer == "" {
			return fmt.Errorf("%w: the containers need a value and the model server's name", ErrInvalidOption)
		}
		labeler.containers, labeler.modelServer = containers, modelServer
		return nil
	}
}

// WithCheckout names the git checkout the commit corpus is read from.
// Without it the file_at corpus is skipped.
func WithCheckout(directory string) LabelerOption {
	return func(labeler *Labeler) error {
		if directory == "" {
			return fmt.Errorf("%w: empty checkout", ErrInvalidOption)
		}
		labeler.checkout = directory
		return nil
	}
}

// WithRepository names the owner/name repository a request that names none
// is read from: the loop's queue rows carry a ticket number alone.
func WithRepository(repository string) LabelerOption {
	return func(labeler *Labeler) error {
		if repository == "" {
			return fmt.Errorf("%w: empty repository", ErrInvalidOption)
		}
		labeler.repository = repository
		return nil
	}
}

// WithMiner registers the executable of a miner that exists, so a positive
// for its ticket must reproduce through its extractor's facts.
func WithMiner(name string, executable string) LabelerOption {
	return func(labeler *Labeler) error {
		if name == "" || executable == "" {
			return fmt.Errorf("%w: a miner needs a name and an executable", ErrInvalidOption)
		}
		labeler.miners[name] = executable
		return nil
	}
}

// WithClock replaces the host's clock, which times the batches.
func WithClock(source clock.IClock) LabelerOption {
	return func(labeler *Labeler) error {
		if source == nil {
			return fmt.Errorf("%w: nil clock", ErrInvalidOption)
		}
		labeler.clock = source
		return nil
	}
}

// WithLogger receives one line per label and per yield.
func WithLogger(logger *slog.Logger) LabelerOption {
	return func(labeler *Labeler) error {
		if logger == nil {
			return fmt.Errorf("%w: nil logger", ErrInvalidOption)
		}
		labeler.logger = logger
		return nil
	}
}

// WithBatches bounds the batches one ticket gets.
func WithBatches(batches int) LabelerOption {
	return func(labeler *Labeler) error {
		if batches <= 0 {
			return fmt.Errorf("%w: batches must be positive, got %d", ErrInvalidOption, batches)
		}
		labeler.batches = batches
		return nil
	}
}

// WithContextWindow names the brain's context window in tokens, which the
// batch size is derived from.
func WithContextWindow(tokens int) LabelerOption {
	return func(labeler *Labeler) error {
		if tokens <= answerTokens {
			return fmt.Errorf("%w: context window must exceed the %d-token answer reserve, got %d", ErrInvalidOption, answerTokens, tokens)
		}
		labeler.contextWindow = tokens
		return nil
	}
}

// WithYieldWait sets how long the labeler waits between looks at the GPU's
// other consumers.
func WithYieldWait(wait time.Duration) LabelerOption {
	return func(labeler *Labeler) error {
		if wait <= 0 {
			return fmt.Errorf("%w: yield wait must be positive, got %s", ErrInvalidOption, wait)
		}
		labeler.yieldWait = wait
		return nil
	}
}

// WithLabelSink grants where labels go.
func WithLabelSink(sink LabelSink) LabelerOption {
	return func(labeler *Labeler) error {
		if sink == nil {
			return fmt.Errorf("%w: nil label sink", ErrInvalidOption)
		}
		labeler.labels = sink
		return nil
	}
}

// WithRunSink grants where the run record goes.
func WithRunSink(sink RunSink) LabelerOption {
	return func(labeler *Labeler) error {
		if sink == nil {
			return fmt.Errorf("%w: nil run sink", ErrInvalidOption)
		}
		labeler.runs = sink
		return nil
	}
}

// NewLabeler validates the whole option set and returns the labeler. The
// brain, the state directory, the launcher and the containers are required.
func NewLabeler(options ...LabelerOption) (*Labeler, error) {
	labeler := &Labeler{
		clock:          clock.NewSystemClock(),
		logger:         slog.New(slog.DiscardHandler),
		miners:         map[string]string{},
		batches:        DefaultBatches,
		contextWindow:  DefaultContextWindow,
		yieldWait:      DefaultYieldWait,
		labels:         func(label *ouroborosv1.ProposedLabel) error { return nil },
		runs:           func(run *ouroborosv1.LabelerRun) error { return nil },
		gpuByContainer: map[string]bool{},
		measured:       measurements{charsPerToken: initialCharsPerToken, tokensPerCandidate: initialTokensPerCandidate, answerPerLabel: initialAnswerPerLabel},
	}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("%w: nil option", ErrInvalidOption)
		}
		if err := option(labeler); err != nil {
			return nil, err
		}
	}
	if labeler.brain == nil || labeler.state == nil || labeler.launcher == nil || labeler.containers == nil {
		return nil, ErrMissingCapability
	}
	labeler.readers = map[string]corpusReader{
		CorpusEvents:       labeler.readEvents,
		CorpusIssues:       labeler.readIssues,
		CorpusPullRequests: labeler.readPullRequests,
		CorpusCommits:      labeler.readCommits,
	}
	return labeler, nil
}

// Label labels one ticket: it completes a request the loop queued by reading
// the ticket, scans the corpus the request names, asks the model batch by
// batch, judges every answer and returns the typed labels it recorded. It
// yields the GPU to any other consumer before each batch. An error after
// some batches returns the labels recorded before it.
func (labeler *Labeler) Label(ctx context.Context, request *ouroborosv1.LabelRequest) ([]*ouroborosv1.ProposedLabel, error) {
	if request == nil || request.GetTicket() <= 0 {
		return nil, ErrNoRequest
	}
	if err := labeler.complete(ctx, request); err != nil {
		return nil, err
	}
	if err := ouroborosv1.ValidateLabelRequest(request); err != nil {
		return nil, fmt.Errorf("labeler: ticket %d: %w", request.GetTicket(), err)
	}
	if labeler.measured.started.IsZero() {
		labeler.measured.started = labeler.clock.Now()
	}
	labeler.measured.tickets++
	terms := extractTerms(request)
	candidates, err := labeler.scan(ctx, request, terms)
	if err != nil {
		return nil, err
	}
	labeler.logger.Info("labeler: ticket scanned", "ticket", request.GetTicket(), "terms", len(terms), "candidates", len(candidates))
	if err := labeler.publish(PhaseLabeling, request.GetTicket()); err != nil {
		return nil, err
	}
	var recorded []*ouroborosv1.ProposedLabel
	fixed := fixedPromptChars(request)
	withInstance := false
	for batch := 0; batch < labeler.batches && len(candidates) > 0; batch++ {
		size := min(batchSize(labeler.contextWindow, labeler.measured.charsPerToken, fixed, labeler.measured.tokensPerCandidate, labeler.measured.answerPerLabel), len(candidates))
		current := candidates[:size]
		candidates = candidates[size:]
		if err := labeler.yield(ctx, request.GetTicket()); err != nil {
			return recorded, err
		}
		labels, err := labeler.ask(ctx, request, terms, current)
		if err != nil {
			return recorded, fmt.Errorf("labeler: ticket %d: %w", request.GetTicket(), err)
		}
		proposed, unplaced := labeler.accept(ctx, request, current, labels)
		labeler.measured.proposed += int64(len(proposed) + unplaced)
		labeler.measured.rejected += int64(unplaced)
		for _, label := range proposed {
			labeler.count(label)
			withInstance = withInstance || reproduced(label)
			labeler.logger.Info("labeler: label", "ticket", request.GetTicket(), "label", describe(label))
			if err := labeler.labels(label); err != nil {
				return recorded, fmt.Errorf("labeler: record label: %w", err)
			}
			recorded = append(recorded, label)
		}
		if sample, ok := labeler.sampleGPU(ctx); ok {
			labeler.measured.gpu = append(labeler.measured.gpu, sample)
		}
		if err := labeler.publish(PhaseLabeling, request.GetTicket()); err != nil {
			return recorded, err
		}
	}
	if withInstance {
		labeler.measured.withInstance++
	}
	return recorded, labeler.publish(PhaseLabeling, request.GetTicket())
}

// Finish publishes the run record one last time, as finished.
func (labeler *Labeler) Finish() error {
	return labeler.publish(PhaseFinished, 0)
}

// scan reads every corpus kind the request names, or the event logs and the
// commits when it names none, and ranks what matched.
func (labeler *Labeler) scan(ctx context.Context, request *ouroborosv1.LabelRequest, terms []term) ([]candidate, error) {
	scan := newCorpusScan(terms)
	if len(terms) == 0 {
		return nil, nil
	}
	kinds := request.GetCorpus()
	if len(kinds) == 0 {
		kinds = []string{CorpusEvents, CorpusCommits}
	}
	for _, kind := range kinds {
		reader, known := labeler.readers[kind]
		if !known {
			labeler.logger.Info("labeler: corpus kind not read", "ticket", request.GetTicket(), "kind", kind)
			continue
		}
		if err := reader(ctx, scan, request); err != nil {
			return nil, err
		}
	}
	return scan.ranked(request.GetInstances()), nil
}

func (labeler *Labeler) readEvents(ctx context.Context, scan *corpusScan, _ *ouroborosv1.LabelRequest) error {
	return labeler.scanEvents(ctx, scan)
}

func (labeler *Labeler) readIssues(ctx context.Context, scan *corpusScan, request *ouroborosv1.LabelRequest) error {
	return labeler.scanRecords(ctx, scan, request, sourceKindIssue)
}

func (labeler *Labeler) readPullRequests(ctx context.Context, scan *corpusScan, request *ouroborosv1.LabelRequest) error {
	return labeler.scanRecords(ctx, scan, request, sourceKindPull)
}

func (labeler *Labeler) readCommits(ctx context.Context, scan *corpusScan, request *ouroborosv1.LabelRequest) error {
	if labeler.checkout == "" {
		labeler.logger.Info("labeler: no checkout; the commit corpus is skipped", "ticket", request.GetTicket())
		return nil
	}
	return labeler.scanCommits(ctx, scan)
}

// ask sends one batch to the model with the derived keep-alive, measures the
// answer and reads its labels.
func (labeler *Labeler) ask(ctx context.Context, request *ouroborosv1.LabelRequest, terms []term, batch []candidate) ([]answerLabel, error) {
	labeler.measured.batchStarts = append(labeler.measured.batchStarts, labeler.clock.Now())
	labeler.measured.keepAlive = keepAliveFor(labeler.gaps(), labeler.measured.load)
	decision := prompt(request, terms, batch, excerptChars, labeler.measured.keepAlive)
	proposal, err := labeler.brain.Propose(ctx, decision)
	if err != nil {
		return nil, err
	}
	answer, err := proposal.Only()
	if err != nil {
		return nil, err
	}
	labeler.measured.batches++
	if answer.Usage.Load > 0 {
		labeler.measured.load = answer.Usage.Load
	}
	labels, err := parseAnswer(answer.Content)
	labeler.measure(decision, answer.Usage, len(batch), len(labels))
	labeler.logger.Info("labeler: batch answered", "ticket", request.GetTicket(), "candidates", len(batch), "labels", len(labels),
		"prompt_tokens", answer.Usage.PromptTokens, "answer_tokens", answer.Usage.Tokens, "total", answer.Usage.Total, "keep_alive", labeler.measured.keepAlive)
	if answer.Usage.PromptTokens+answer.Usage.Tokens > labeler.contextWindow {
		labeler.logger.Warn("labeler: the batch overflowed the context window", "ticket", request.GetTicket(),
			"prompt_tokens", answer.Usage.PromptTokens, "answer_tokens", answer.Usage.Tokens, "window", labeler.contextWindow)
	}
	return labels, err
}

// measure folds one answer's usage into the ratios the next batch is sized
// by: the prompt's characters per token, the prompt tokens one candidate
// cost beyond the fixed part, and the answer tokens one label cost. The two
// per-item costs keep their running maximum, so a batch over a denser
// corpus never sizes the next one past the window.
func (labeler *Labeler) measure(decision *ollama.Prompt, usage ollama.Usage, candidates int, labels int) {
	if usage.PromptTokens <= 0 {
		return
	}
	promptChars := len(decision.System) + len(decision.User)
	labeler.measured.charsPerToken = float64(promptChars) / float64(usage.PromptTokens)
	fixedTokens := float64(len(decision.System)+len(answerSchema)) / labeler.measured.charsPerToken
	if candidates > 0 {
		labeler.measured.tokensPerCandidate = max(labeler.measured.tokensPerCandidate, (float64(usage.PromptTokens)-fixedTokens)/float64(candidates))
	}
	if labels > 0 && usage.Tokens > 0 {
		labeler.measured.answerPerLabel = max(labeler.measured.answerPerLabel, float64(usage.Tokens)/float64(labels))
	}
}

// yield waits while another GPU consumer is running. An Engine that cannot
// be asked does not stop labeling: the failure is logged and the batch runs.
func (labeler *Labeler) yield(ctx context.Context, ticket int64) error {
	for {
		consumers, err := labeler.gpuConsumers(ctx)
		if err != nil {
			labeler.logger.Warn("labeler: GPU consumers unknown; labeling anyway", "error", err)
			return nil
		}
		if len(consumers) == 0 {
			return nil
		}
		labeler.measured.yields++
		labeler.logger.Info("labeler: yielding the GPU", "ticket", ticket, "consumers", consumers, "wait", labeler.yieldWait)
		if err := labeler.publish(PhaseYielding, ticket); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-labeler.clock.After(labeler.yieldWait):
		}
	}
}

// count folds one judged label into the measurements.
func (labeler *Labeler) count(label *ouroborosv1.ProposedLabel) {
	if !label.GetPositive() {
		return
	}
	labeler.measured.positives++
	if reproduced(label) {
		labeler.measured.accepted++
		return
	}
	labeler.measured.rejected++
}

// reproduced reports an accepted positive.
func reproduced(label *ouroborosv1.ProposedLabel) bool {
	acceptance := label.GetAcceptance()
	return acceptance == ouroborosv1.Acceptance_ACCEPTANCE_REPRODUCED_SPAN || acceptance == ouroborosv1.Acceptance_ACCEPTANCE_REPRODUCED_FACT
}

// gaps are the intervals between consecutive batch starts.
func (labeler *Labeler) gaps() []time.Duration {
	starts := labeler.measured.batchStarts
	gaps := make([]time.Duration, 0, len(starts))
	for index := 1; index < len(starts); index++ {
		gaps = append(gaps, starts[index].Sub(starts[index-1]))
	}
	return gaps
}

// publish projects the measurements into the run record and hands it to
// the sink.
func (labeler *Labeler) publish(phase string, current int64) error {
	now := labeler.clock.Now()
	measured := labeler.measured
	record := &ouroborosv1.LabelerRun{
		Model:               labeler.modelName,
		Phase:               phase,
		Current:             current,
		Tickets:             measured.tickets,
		TicketsWithInstance: measured.withInstance,
		Proposed:            measured.proposed,
		Accepted:            measured.accepted,
		Rejected:            measured.rejected,
		Batches:             measured.batches,
		HeldOut:             measured.positives,
		GpuUtilization:      unmeasured,
		KeepAliveSeconds:    int64(measured.keepAlive / time.Second),
		BatchGapSeconds:     int64(median(labeler.gaps()) / time.Second),
		LoadSeconds:         int64(measured.load / time.Second),
		Yields:              measured.yields,
		Started:             measured.started.Unix(),
		Updated:             now.Unix(),
	}
	if hours := now.Sub(measured.started).Hours(); hours > 0 {
		record.LabelsPerHour = float64(measured.proposed) / hours
	}
	if measured.positives > 0 {
		record.Precision = float64(measured.accepted) / float64(measured.positives)
	}
	if len(measured.gpu) > 0 {
		var total float64
		for _, sample := range measured.gpu {
			total += sample.utilization
		}
		record.GpuUtilization = total / float64(len(measured.gpu))
		record.GpuMemoryBytes = measured.gpu[len(measured.gpu)-1].memoryBytes
	}
	return labeler.runs(record)
}

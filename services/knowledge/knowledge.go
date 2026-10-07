// Copyright 2026 Candace Labs

// Package knowledge keeps CSF's search index fed and measured: the
// repository at the head of main, the harness state (its JSON key names, never
// their values), each run's conversation (user and assistant text, never tool
// output) and the repository's tickets and pull requests, ingested through the
// knowledge service's own IngestDocument so the store stays the evidence and
// the index its projection. A source already ingested at its revision is
// skipped, a repository file deleted since the last indexed commit is
// forgotten, and after every pass the held-out lookups (lookups.json) are
// answered twice, in fused and in reranked order, giving hit@k.
//
// The owned containers the index and its reranker run in are here too
// (stack.go), and the embedder that reaches the LABELER's Ollama server
// (embed.go).
//
// It is a service in the house sense: no process, no flags, no environment,
// no listener; the harness binary composes it and runs its trigger.
package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/io/ipc/proc"
	grammar "github.com/candacelabs/csf/pkg/cron"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	cronservice "github.com/candacelabs/csf/services/cron"
)

// TriggerRefresh is the cron trigger that feeds and measures the index.
const TriggerRefresh = "knowledge.refresh"

// refreshInterval is how soon a merge to main is searchable: the live checkout
// fast-forwards every minute, and the next pass indexes what it brought.
const refreshInterval = 5 * time.Minute

const (
	mediaText = "text/plain"
	// licenseRepository is the repository's own license; other sources make
	// no assertion.
	licenseRepository = "Apache-2.0"
	licenseUnknown    = "NOASSERTION"
)

var (
	// ErrInvalidOption reports a nil option or one the feeder cannot use.
	ErrInvalidOption = errors.New("knowledge: invalid option")
	// ErrMissingCapability reports a feeder built without the knowledge
	// service or the index.
	ErrMissingCapability = errors.New("knowledge: the knowledge service and the index are required")
)

// IKnowledge is the part of the knowledge service the feeder uses: the
// ingest path every document takes, and the store it asks whether a revision
// is already in.
type IKnowledge interface {
	IngestDocument(ctx context.Context, request *pb.IngestDocumentRequest) (*pb.IngestDocumentResponse, error)
	GetDocument(ctx context.Context, request *pb.GetDocumentRequest) (*pb.GetDocumentResponse, error)
}

// IIndex is the part of the search projection the feeder uses: forgetting a
// source that is gone, and answering a lookup in both orders.
type IIndex interface {
	Forget(ctx context.Context, sourceID string) error
	Compare(ctx context.Context, request *pb.SearchRequest) (*pb.SearchResult, *pb.SearchResult, error)
}

// document is one source revision a source reads, ready to ingest.
type document struct {
	sourceID string
	revision string
	title    string
	uri      string
	license  string
	text     string
}

// sourceRead is one source's pass: the documents it holds now and the
// source ids that are gone.
type sourceRead func(ctx context.Context) (documents []document, gone []string, err error)

// source is one kind of thing the index holds.
type source struct {
	name string
	read sourceRead
	// done runs after every document of a pass was ingested, so a source
	// records how far it got only once the index has it.
	done func() error
}

// Feeder feeds the index from its sources and measures it. One trigger runs
// its passes, so a pass never overlaps another.
type Feeder struct {
	knowledge  IKnowledge
	index      IIndex
	launcher   proc.ILauncher
	logger     *slog.Logger
	now        func() time.Time
	repository repositorySource
	github     string
	state      iofs.IFiles
	stateRoot  string
	lookups    []Lookup
	sources    []source
	// runs is each run's events.jsonl size at the last pass, so an unchanged
	// run is not read again.
	runs map[string]int64

	measured atomic.Pointer[Measured]
}

// Measured is what the last pass did and the index's hit@k after it.
type Measured struct {
	At         time.Time
	Ingested   map[string]int64
	Forgotten  int64
	Failed     map[string]int64
	Evaluation Evaluation
}

// FeederOption configures a [Feeder].
type FeederOption func(feeder *Feeder) error

// WithKnowledge grants the knowledge service documents are ingested through.
// Required.
func WithKnowledge(knowledge IKnowledge) FeederOption {
	return func(feeder *Feeder) error {
		if knowledge == nil {
			return fmt.Errorf("%w: nil knowledge service", ErrInvalidOption)
		}
		feeder.knowledge = knowledge
		return nil
	}
}

// WithIndex grants the search projection. Required.
func WithIndex(index IIndex) FeederOption {
	return func(feeder *Feeder) error {
		if index == nil {
			return fmt.Errorf("%w: nil index", ErrInvalidOption)
		}
		feeder.index = index
		return nil
	}
}

// WithLauncher grants the programs the repository and GitHub sources run:
// git and gh.
func WithLauncher(launcher proc.ILauncher) FeederOption {
	return func(feeder *Feeder) error {
		if launcher == nil {
			return fmt.Errorf("%w: nil launcher", ErrInvalidOption)
		}
		feeder.launcher = launcher
		return nil
	}
}

// WithRepository indexes main of the git checkout at directory, whose
// origin is the repository slug (owner/name), and remembers the commit it
// indexed through load and save, so a file deleted while the host was down
// is still forgotten.
func WithRepository(directory string, slug string, load func() (string, error), save func(commit string) error) FeederOption {
	return func(feeder *Feeder) error {
		if directory == "" || load == nil || save == nil {
			return fmt.Errorf("%w: the repository needs a checkout and a commit record", ErrInvalidOption)
		}
		feeder.repository = repositorySource{directory: directory, slug: slug, load: load, save: save}
		return nil
	}
}

// WithGitHub indexes the tickets and pull requests of the repository slug.
func WithGitHub(slug string) FeederOption {
	return func(feeder *Feeder) error {
		if slug == "" {
			return fmt.Errorf("%w: empty repository slug", ErrInvalidOption)
		}
		feeder.github = slug
		return nil
	}
}

// WithHarnessState indexes the harness state directory at root, read
// through files: its JSON files' key names and each run's conversation.
func WithHarnessState(root string, files iofs.IFiles) FeederOption {
	return func(feeder *Feeder) error {
		if root == "" || files == nil {
			return fmt.Errorf("%w: the harness state needs a path and its files", ErrInvalidOption)
		}
		feeder.stateRoot, feeder.state = root, files
		return nil
	}
}

// WithLogger receives one line per pass and per failed source.
func WithLogger(logger *slog.Logger) FeederOption {
	return func(feeder *Feeder) error {
		if logger == nil {
			return fmt.Errorf("%w: nil logger", ErrInvalidOption)
		}
		feeder.logger = logger
		return nil
	}
}

// WithClock replaces the wall clock the documents' retrieval times read.
func WithClock(now func() time.Time) FeederOption {
	return func(feeder *Feeder) error {
		if now == nil {
			return fmt.Errorf("%w: nil clock", ErrInvalidOption)
		}
		feeder.now = now
		return nil
	}
}

// NewFeeder builds the feeder from its options; the sources it reads are the
// ones granted.
func NewFeeder(options ...FeederOption) (*Feeder, error) {
	feeder := &Feeder{logger: slog.New(slog.DiscardHandler), now: time.Now, runs: map[string]int64{}, lookups: Lookups()}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("%w: nil option", ErrInvalidOption)
		}
		if err := option(feeder); err != nil {
			return nil, err
		}
	}
	if feeder.knowledge == nil || feeder.index == nil {
		return nil, ErrMissingCapability
	}
	if (feeder.repository.directory != "" || feeder.github != "") && feeder.launcher == nil {
		return nil, fmt.Errorf("%w: the repository and GitHub sources need a launcher", ErrInvalidOption)
	}
	if feeder.repository.directory != "" {
		feeder.repository.launcher = feeder.launcher
		feeder.sources = append(feeder.sources, source{name: sourceRepository, read: feeder.repository.read, done: feeder.repository.done})
	}
	if feeder.github != "" {
		feeder.sources = append(feeder.sources, source{name: sourceGitHub, read: feeder.readGitHub})
	}
	if feeder.state != nil {
		feeder.sources = append(feeder.sources,
			source{name: sourceState, read: feeder.readState}, source{name: sourceRuns, read: feeder.readRuns})
	}
	return feeder, nil
}

// Trigger is the cron trigger that runs a pass every refreshInterval.
func (feeder *Feeder) Trigger() cronservice.Option {
	return cronservice.WithTrigger(TriggerRefresh, grammar.Spec(grammar.Every(refreshInterval)), func(ctx context.Context, _ cronservice.Occurrence) error {
		_, err := feeder.Refresh(ctx)
		return err
	})
}

// Measured is the last pass's measurements; nil before the first.
func (feeder *Feeder) Measured() *Measured { return feeder.measured.Load() }

// Refresh runs one pass: every source read, every new revision ingested, every
// source gone forgotten, then the lookups answered. A failing source is
// logged and counted, and the others still run.
func (feeder *Feeder) Refresh(ctx context.Context) (*Measured, error) {
	measured := &Measured{Ingested: map[string]int64{}, Failed: map[string]int64{}}
	for _, from := range feeder.sources {
		ingested, forgotten, err := feeder.feed(ctx, from)
		measured.Ingested[from.name] += ingested
		measured.Forgotten += forgotten
		if err != nil {
			if ctx.Err() != nil {
				return nil, context.Cause(ctx)
			}
			measured.Failed[from.name]++
			feeder.logger.Warn("knowledge: source incomplete", "source", from.name, "error", err)
		}
	}
	evaluation, err := Evaluate(ctx, feeder.index, feeder.lookups)
	if err != nil {
		feeder.logger.Warn("knowledge: lookups unanswered", "error", err)
	}
	measured.Evaluation, measured.At = evaluation, feeder.now()
	feeder.measured.Store(measured)
	feeder.logger.Info("knowledge: pass", "ingested", measured.Ingested, "forgotten", measured.Forgotten, "hit_at_3", evaluation.Reranked[3])
	return measured, nil
}

// feed ingests one source's new revisions and forgets what is gone.
func (feeder *Feeder) feed(ctx context.Context, from source) (int64, int64, error) {
	documents, gone, err := from.read(ctx)
	if err != nil {
		return 0, 0, err
	}
	var ingested, forgotten int64
	var failures []error
	for _, item := range documents {
		known, err := feeder.known(ctx, item)
		if err != nil {
			return ingested, forgotten, err
		}
		if known {
			continue
		}
		if err := feeder.ingest(ctx, item); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", item.sourceID, err))
			continue
		}
		ingested++
	}
	for _, sourceID := range gone {
		if err := feeder.index.Forget(ctx, sourceID); err != nil {
			failures = append(failures, fmt.Errorf("forget %s: %w", sourceID, err))
			continue
		}
		forgotten++
	}
	if len(failures) > 0 {
		return ingested, forgotten, errors.Join(failures...)
	}
	if from.done != nil {
		return ingested, forgotten, from.done()
	}
	return ingested, forgotten, nil
}

// known reports whether the store already holds the document's revision. A
// lookup that fails for any reason other than the context reads as unknown:
// ingesting a revision twice is harmless, missing one is not.
func (feeder *Feeder) known(ctx context.Context, item document) (bool, error) {
	_, err := feeder.knowledge.GetDocument(ctx, &pb.GetDocumentRequest{Request: &pb.DocumentRequest{SourceId: item.sourceID, Revision: item.revision}})
	if ctx.Err() != nil {
		return false, context.Cause(ctx)
	}
	return err == nil, nil
}

// ingest sends one document through the knowledge service.
func (feeder *Feeder) ingest(ctx context.Context, item document) error {
	digest := sha256.Sum256([]byte(item.text))
	hash := hex.EncodeToString(digest[:])
	_, err := feeder.knowledge.IngestDocument(ctx, &pb.IngestDocumentRequest{Text: item.text, Document: &pb.SourceDocument{
		SourceId: item.sourceID, Revision: item.revision, ContentHash: hash, RawSourceContentHash: hash,
		SourceUri: item.uri, Title: item.title, MediaType: mediaText, License: item.license,
		RetrievedAt: feeder.now().UTC().Format(time.RFC3339Nano),
	}})
	return err
}

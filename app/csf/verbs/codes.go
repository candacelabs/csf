// Copyright 2026 Candace Labs

package verbs

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	ionet "github.com/candacelabs/csf/io/net"
	iohttp "github.com/candacelabs/csf/io/net/http"
	"github.com/candacelabs/csf/io/net/model/ollama"
	"github.com/candacelabs/csf/services/ouroboros/codes"
	"github.com/candacelabs/csf/services/ouroboros/labeler"
)

// The codes verb: every operator correction in the Claude Code transcripts
// named, coded with the failure codes by the local model, counted per code,
// edge and side, and recorded under the state directory for the views.
const (
	verbCodes = "codes"

	transcriptsFlag = "transcripts"
	sinceFlag       = "since"
	untilFlag       = "until"
	referenceFlag   = "reference"
	seedOnlyFlag    = "seed-only"
	induceFlag      = "induce"
	batchFlag       = "batch"

	inducedKey    = "induced"
	complaintsKey = "complaints"

	transcriptSuffix = ".jsonl"
	// codesKeepAlive keeps the model loaded between one batch and the next;
	// the labeler measured a batch at 35 s, so a minute spans the gap.
	codesKeepAlive = time.Minute
)

var errNoTranscripts = errors.New("-transcripts GLOB is required and must match at least one transcript")

// codesReport is what the verb prints: the tally, and, when a reference was
// given, the agreement of the codes and their sides with it.
type codesReport struct {
	Model     string                  `json:"model"`
	Catalogue int                     `json:"catalogue"`
	Summary   codes.Summary           `json:"summary"`
	Agreement *codes.ClusterAgreement `json:"agreement,omitempty"`
	SideKappa *float64                `json:"side_kappa,omitempty"`
	// SideMatching is the share of complaints whose side matches the
	// reference's, over SideShared complaints.
	SideMatching *float64 `json:"side_matching,omitempty"`
	SideShared   int      `json:"side_shared,omitempty"`
	// ReaderRecall is how many of the reference's messages pkg/affect reads
	// as corrections.
	ReaderRecall *codes.ReaderRecall `json:"reader_recall,omitempty"`
}

// codesVerb codes the complaints of the transcripts the flags name. A
// seed-only run codes with MAST's modes alone, the reference vocabulary the
// induced catalogue is measured against, and records nothing.
func codesVerb(ctx context.Context, arguments []string, output io.Writer) error {
	flags := flag.NewFlagSet(commandName+" "+verbOuroboros+" "+verbCodes, flag.ContinueOnError)
	pattern := flags.String(transcriptsFlag, "", "glob of Claude Code session transcripts (<session>.jsonl)")
	since := flags.String(sinceFlag, "", "RFC 3339 time: only complaints sent at or after it")
	until := flags.String(untilFlag, "", "RFC 3339 time: only complaints sent before it")
	reference := flags.String(referenceFlag, "", "hand clustering to measure against: ref, class and side per line, tab-separated")
	seedOnly := flags.Bool(seedOnlyFlag, false, "code with MAST's 14 seed modes only, and record nothing")
	induce := flags.Bool(induceFlag, false, "induce new codes from the complaints instead of coding them, and record nothing")
	batch := flags.Int(batchFlag, codes.DefaultBatch, "complaints per model request")
	think := flags.Bool(thinkFlag, false, "let the model reason before it answers")
	modelName := flags.String(modelFlag, defaultModel, "the local model")
	endpoint := flags.String(modelEndpointFlag, ollama.DefaultEndpoint, "the model server's URL")
	stateDirectory := flags.String(stateFlag, "", "state directory the records are appended under (default ~/"+defaultStateDirectory+")")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	window, err := parseWindow(*since, *until)
	if err != nil {
		return err
	}
	classes, sides, err := readReference(*reference)
	if err != nil {
		return err
	}
	messages, err := readMessages(*pattern, window)
	if err != nil {
		return err
	}
	complaints, recall := codes.Select(messages, classes)
	catalogue := codes.Catalogue
	if *seedOnly {
		catalogue = codes.Seed()
	}
	client, err := iohttp.NewHTTPClient(ionet.NewHostNetwork(), iohttp.WithClientTimeout(modelClientTimeout))
	if err != nil {
		return err
	}
	brainOptions := []ollama.OllamaBrainOption{ollama.WithEndpoint(*endpoint), ollama.WithContextWindow(labeler.DefaultContextWindow)}
	if *think {
		brainOptions = append(brainOptions, ollama.WithThinking())
	}
	brain, err := ollama.NewOllamaBrain(client, *modelName, brainOptions...)
	if err != nil {
		return err
	}
	coder, err := codes.NewComplaintCoder(codes.WithBrain(brain), codes.WithCatalogue(catalogue), codes.WithBatch(*batch), codes.WithKeepAlive(codesKeepAlive))
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if *induce {
		induced, err := coder.Induce(ctx, complaints)
		if err != nil {
			return err
		}
		return encoder.Encode(map[string]any{inducedKey: induced, complaintsKey: len(complaints)})
	}
	coded, err := coder.Code(ctx, complaints)
	if err != nil {
		return err
	}
	report := codesReport{Model: ollama.ProviderName + "/" + *modelName, Catalogue: len(catalogue), Summary: codes.Tally(coded, catalogue)}
	if *reference != "" {
		measure(&report, classes, sides, coded, catalogue)
		report.ReaderRecall = &recall
	}
	if !*seedOnly {
		if err := recordCoded(*stateDirectory, coded, catalogue, report.Model); err != nil {
			return err
		}
	}
	return encoder.Encode(report)
}

// codesWindow is the [since, until) the complaints are read in; a zero end
// is open.
type codesWindow struct{ since, until time.Time }

func parseWindow(since string, until string) (codesWindow, error) {
	var window codesWindow
	for _, bound := range []struct {
		text   string
		target *time.Time
	}{{since, &window.since}, {until, &window.until}} {
		if bound.text == "" {
			continue
		}
		parsed, err := time.Parse(time.RFC3339, bound.text)
		if err != nil {
			return window, err
		}
		*bound.target = parsed
	}
	return window, nil
}

// readMessages reads the operator's messages of every transcript pattern
// matches, each named by its session, the file name without .jsonl.
func readMessages(pattern string, window codesWindow) ([]codes.Complaint, error) {
	if pattern == "" {
		return nil, errNoTranscripts
	}
	paths, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, errNoTranscripts
	}
	complaints := []codes.Complaint{}
	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		read, err := codes.ReadTranscript(strings.TrimSuffix(filepath.Base(path), transcriptSuffix), file, window.since, window.until)
		_ = file.Close()
		if err != nil {
			return nil, err
		}
		complaints = append(complaints, read...)
	}
	return complaints, nil
}

// readReference reads the hand clustering at path; no path is no reference.
func readReference(path string) (classes map[string]string, sides map[string]string, err error) {
	if path == "" {
		return nil, nil, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = file.Close() }()
	return codes.ReadReference(file)
}

// measure adds the agreement of the coding with the reference clustering,
// and of the codes' sides with the reference's sides.
func measure(report *codesReport, classes map[string]string, sides map[string]string, coded []codes.Coded, catalogue []codes.Code) {
	agreement := codes.Agree(codes.Clusters(coded), classes)
	coderSides := codes.SidesOf(coded, catalogue)
	kappa := codes.Kappa(coderSides, sides)
	matching, shared := codes.Matching(coderSides, sides)
	report.Agreement, report.SideKappa, report.SideMatching, report.SideShared = &agreement, &kappa, &matching, shared
}

// recordCoded appends one record per coded correction to the state
// directory's failure codes file; a message coded only because the
// reference labels it is measured, not recorded, since the views count
// every record as an operator correction.
func recordCoded(stateDirectory string, coded []codes.Coded, catalogue []codes.Code, modelName string) error {
	state, err := stateRoot(stateDirectory)
	if err != nil {
		return err
	}
	path := filepath.Join(state, codes.RecordFile)
	for _, entry := range coded {
		if !entry.Complaint.Correction {
			continue
		}
		line, err := json.Marshal(codes.NewRecord(entry, catalogue, modelName))
		if err != nil {
			return err
		}
		if err := appendLine(path, line); err != nil {
			return fmt.Errorf("record %s: %w", path, err)
		}
	}
	return nil
}

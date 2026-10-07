// Copyright 2026 Candace Labs

package labeler

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	stdfs "io/fs"
	"math"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/candacelabs/csf/io/ipc/proc"
	ouroborosv1 "github.com/candacelabs/csf/proto/candace/ouroboros/v1"
	"github.com/candacelabs/csf/services/harness/session"
)

// The programs the issue, pull request and commit corpora are read with,
// and their argument vocabulary.
const (
	programGH  = "gh"
	programGit = "git"

	ghIssue        = "issue"
	ghPullRequest  = "pr"
	ghList         = "list"
	ghView         = "view"
	ghRepoFlag     = "--repo"
	ghStateFlag    = "--state"
	ghStateAll     = "all"
	ghLimitFlag    = "--limit"
	ghLimit        = "500"
	ghJSONFlag     = "--json"
	ghJSONFields   = "number,title,body"
	ghTicketFields = "title,body,comments"

	gitDirectoryFlag = "-C"
	gitLog           = "log"
	gitNoMerges      = "--no-merges"
	gitCountFlag     = "-n"
	gitCount         = "5000"
	// gitFormat separates commits with the ASCII record separator and the
	// hash, subject and body with the unit separator, so a body's own lines
	// stay intact.
	gitFormat          = "--format=%x1e%H%x1f%s%x1f%b"
	commitSeparator    = "\x1e"
	fieldSeparator     = "\x1f"
	shortHashLength    = 12
	sourceKindIssue    = "issue"
	sourceKindPull     = "pull_request"
	sourceKindCommit   = "commit"
	sourceReference    = "#"
	sourceKindSep      = ":"
	rootName           = "."
	maxLineBytes       = 4 << 20
	kindUnknown        = "-"
	excerptEllipsis    = "…"
	excerptHeaderParts = 3
)

// candidate is one corpus line the prefilter kept: the instance it belongs
// to, where it is, its text, and which terms matched it.
type candidate struct {
	instance string
	source   string
	line     int
	text     string
	// kind is the harness event type, or the corpus kind for other sources.
	kind string
	// terms indexes the request's terms that matched, in term order, and
	// positions is where each first occurs in the text.
	terms     []int
	positions []int
	// score is the sum of the matched terms' inverse document frequencies,
	// and focus is where the most informative of them occurs: the excerpt
	// the model reads is centred there.
	score float64
	focus int
}

// span is the candidate's place in the contract's span record.
func (found candidate) span() *ouroborosv1.Span {
	return &ouroborosv1.Span{Source: found.source, Line: int64(found.line)}
}

// corpusScan accumulates candidates and the per-term document frequencies
// one request's scan needs to score them.
type corpusScan struct {
	terms      []term
	lines      int
	frequency  []int
	candidates []candidate
}

func newCorpusScan(terms []term) *corpusScan {
	return &corpusScan{terms: terms, frequency: make([]int, len(terms))}
}

// read judges one corpus line: a line no term matches counts toward the
// total and nothing else.
func (scan *corpusScan) read(instance string, source string, line int, text string, kind string) {
	scan.lines++
	lowered := strings.ToLower(text)
	var matched, positions []int
	for index, searched := range scan.terms {
		if position := searched.index(lowered, text); position >= 0 {
			matched = append(matched, index)
			positions = append(positions, position)
			scan.frequency[index]++
		}
	}
	if len(matched) == 0 {
		return
	}
	scan.candidates = append(scan.candidates, candidate{instance: instance, source: source, line: line, text: text, kind: kind, terms: matched, positions: positions})
}

// ranked scores every candidate by inverse document frequency, the
// parameter-free weighting under which a term found on every line counts for
// nothing and a rare one for much, and orders them best first, round-robin
// across instances so one verbose run cannot fill a batch. Instances the
// ticket names come first. No threshold is picked: the batch size decides
// how many are read.
func (scan *corpusScan) ranked(named []string) []candidate {
	for index := range scan.candidates {
		found := &scan.candidates[index]
		best := -1.0
		for position, term := range found.terms {
			weight := math.Log(float64(scan.lines) / float64(scan.frequency[term]))
			found.score += weight
			if weight > best {
				best, found.focus = weight, found.positions[position]
			}
		}
	}
	slices.SortStableFunc(scan.candidates, func(a candidate, b candidate) int {
		if a.score != b.score {
			if a.score > b.score {
				return -1
			}
			return 1
		}
		return strings.Compare(a.source+sourceKindSep+strconv.Itoa(a.line), b.source+sourceKindSep+strconv.Itoa(b.line))
	})
	byInstance := map[string][]candidate{}
	var order []string
	for _, found := range scan.candidates {
		if _, known := byInstance[found.instance]; !known {
			order = append(order, found.instance)
		}
		byInstance[found.instance] = append(byInstance[found.instance], found)
	}
	slices.SortStableFunc(order, func(a string, b string) int {
		namedA, namedB := slices.Contains(named, a), slices.Contains(named, b)
		if namedA != namedB {
			if namedA {
				return -1
			}
			return 1
		}
		return 0
	})
	interleaved := make([]candidate, 0, len(scan.candidates))
	for round := 0; len(interleaved) < len(scan.candidates); round++ {
		for _, instance := range order {
			if round < len(byInstance[instance]) {
				interleaved = append(interleaved, byInstance[instance][round])
			}
		}
	}
	return interleaved
}

// eventHeader is the part of an event log line the excerpt names.
type eventHeader struct {
	Time      string `json:"time"`
	EventType string `json:"event_type"`
	Message   string `json:"msg"`
}

// scanEvents reads every run directory's event log under the state
// directory: the instance is the assignment, the source the log's name
// relative to the state directory, as the file capability spells it. The
// held-out runs are skipped.
func (labeler *Labeler) scanEvents(ctx context.Context, scan *corpusScan) error {
	entries, err := labeler.state.ReadDir(rootName)
	if err != nil {
		return fmt.Errorf("labeler: list the state directory: %w", err)
	}
	hidden := map[string]bool{}
	if labeler.hidden != nil {
		if hidden, err = labeler.hidden(ctx); err != nil {
			return fmt.Errorf("labeler: list the held-out runs: %w", err)
		}
	}
	for _, entry := range entries {
		if !entry.IsDir() || hidden[entry.Name()] {
			continue
		}
		if err := labeler.scanEventLog(scan, entry.Name()); err != nil {
			return err
		}
	}
	return nil
}

func (labeler *Labeler) scanEventLog(scan *corpusScan, assignment string) error {
	source := path.Join(assignment, session.EventsFile)
	file, err := labeler.state.Open(source)
	if errors.Is(err, stdfs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("labeler: open %s: %w", source, err)
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64<<10), maxLineBytes)
	for line := 1; scanner.Scan(); line++ {
		text := scanner.Text()
		if strings.TrimSpace(text) == "" {
			continue
		}
		var header eventHeader
		_ = json.Unmarshal([]byte(text), &header)
		kind := header.EventType
		if kind == "" {
			kind = kindUnknown
		}
		scan.read(assignment, source, line, text, kind)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("labeler: read %s: %w", source, err)
	}
	return nil
}

// ghRecord is one issue or pull request as gh lists it.
type ghRecord struct {
	Number int64  `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
}

// scanRecords reads every issue (or pull request) of the request's
// repository through gh: the instance is #number, the source
// issue:#number, and the lines are the title then the body's lines. The
// request's own ticket is not a candidate for itself.
func (labeler *Labeler) scanRecords(ctx context.Context, scan *corpusScan, request *ouroborosv1.LabelRequest, kind string) error {
	verb := ghIssue
	if kind == sourceKindPull {
		verb = ghPullRequest
	}
	result, err := labeler.launcher.Run(ctx, proc.Command{Executable: programGH, Arguments: []string{
		verb, ghList, ghRepoFlag, request.GetRepository(), ghStateFlag, ghStateAll, ghLimitFlag, ghLimit, ghJSONFlag, ghJSONFields,
	}})
	if err != nil {
		return fmt.Errorf("labeler: list the %s corpus: %w", kind, err)
	}
	var records []ghRecord
	if err := json.Unmarshal(result.Stdout, &records); err != nil {
		return fmt.Errorf("labeler: decode the %s corpus: %w", kind, err)
	}
	for _, record := range records {
		if kind == sourceKindIssue && record.Number == request.GetTicket() {
			continue
		}
		instance := sourceReference + strconv.FormatInt(record.Number, 10)
		source := kind + sourceKindSep + instance
		for index, text := range append([]string{record.Title}, strings.Split(record.Body, "\n")...) {
			if strings.TrimSpace(text) == "" {
				continue
			}
			scan.read(instance, source, index+1, text, kind)
		}
	}
	return nil
}

// scanCommits reads the checkout's commit messages through git: the
// instance is the short hash, the source commit:<hash>, and the lines are the
// subject then the body's lines.
func (labeler *Labeler) scanCommits(ctx context.Context, scan *corpusScan) error {
	result, err := labeler.launcher.Run(ctx, proc.Command{Executable: programGit, Arguments: []string{
		gitDirectoryFlag, labeler.checkout, gitLog, gitNoMerges, gitCountFlag, gitCount, gitFormat,
	}})
	if err != nil {
		return fmt.Errorf("labeler: read the commit corpus: %w", err)
	}
	for _, record := range strings.Split(string(result.Stdout), commitSeparator) {
		fields := strings.SplitN(strings.TrimSpace(record), fieldSeparator, excerptHeaderParts)
		if len(fields) < 2 || len(fields[0]) < shortHashLength {
			continue
		}
		instance := fields[0][:shortHashLength]
		source := sourceKindCommit + sourceKindSep + instance
		lines := []string{fields[1]}
		if len(fields) == excerptHeaderParts {
			lines = append(lines, strings.Split(fields[2], "\n")...)
		}
		for index, text := range lines {
			if strings.TrimSpace(text) == "" {
				continue
			}
			scan.read(instance, source, index+1, text, sourceKindCommit)
		}
	}
	return nil
}

// excerpt is what the model reads of a candidate: for an event log line its
// time, type and message, then a window of the text centred on the most
// informative term matched. The quote the model returns must still be
// verbatim in the whole line, which is what acceptance checks.
func (found candidate) excerpt(window int) string {
	var header string
	if strings.HasPrefix(found.text, "{") {
		var event eventHeader
		if json.Unmarshal([]byte(found.text), &event) == nil {
			header = strings.Join([]string{event.Time, event.EventType, event.Message}, " ") + "\n"
		}
	}
	if len(found.text) <= window {
		return header + found.text
	}
	start := max(found.focus-window/2, 0)
	end := min(start+window, len(found.text))
	start = max(end-window, 0)
	piece := found.text[start:end]
	if start > 0 {
		piece = excerptEllipsis + piece
	}
	if end < len(found.text) {
		piece += excerptEllipsis
	}
	return header + piece
}

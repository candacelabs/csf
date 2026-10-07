// Copyright 2026 Candace Labs

package ouroboros

import (
	"context"
	"errors"
	stdfs "io/fs"
	"path"
	"regexp"
	"strconv"
	"strings"

	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/services/harness/session"
)

// The corpus kinds a proposal declares, as the miner contract's Corpus
// readers name them, and the instance kind each one reads.
const (
	CorpusEvents      = "jsonl"
	CorpusIssue       = "issue"
	CorpusPullRequest = "pull_request"
	CorpusFile        = "file_at"

	InstanceRun         = "run"
	InstanceIssue       = "issue"
	InstancePullRequest = "pull_request"
	InstanceCommit      = "commit"

	// The rows of the proposal comment the overnight loop posted on every
	// mining ticket, read back here. The labeled-instance table is the
	// proposal's own statement of which corpus items it is about.
	proposalMinerRow    = "| miner |"
	proposalCorpusRow   = "| corpus |"
	proposalLabelsHead  = "| labeled instance |"
	tableSeparatorRow   = "|---"
	tableCell           = "|"
	backtick            = "`"
	corpusSeparators    = ",; "
	gitExecutable       = "git"
	gitDirectory        = "-C"
	gitCatFile          = "cat-file"
	gitExists           = "-e"
	gitCommitSuffix     = "^{commit}"
	reasonNoProposal    = "the ticket carries no mining proposal"
	reasonNoInstance    = "no labeled instance exists in the corpus"
	reasonFound         = "named instance exists in the corpus"
	reasonSeparator     = ": "
	instanceSeparator   = ", "
	maxInstanceLength   = 256
	referenceNumberBase = 10
)

var (
	runExpression    = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	numberExpression = regexp.MustCompile(`#([0-9]+)`)
	bareNumber       = regexp.MustCompile(`^[0-9]+$`)
	commitExpression = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)
)

// Proposal is one mining proposal as a ticket carries it: the miner it
// names, the corpus kinds its extractor reads and the instances it labels.
type Proposal struct {
	ID        string
	Miner     string
	Corpus    []string
	Instances []string
}

// Reference is one thing a labeled instance may name in the corpus.
type Reference struct {
	Kind  string
	Value string
}

// Precheck is the pre-check's result on one proposal.
type Precheck struct {
	Verdict Verdict
	Reason  string
	Found   []Reference
}

// ParseProposal reads a proposal from a comment body: the miner and corpus
// rows of its field table and every row of its labeled-instance table. A
// comment without the labeled-instance table is not a proposal.
func ParseProposal(comment Comment) (Proposal, bool) {
	proposal := Proposal{ID: comment.ID}
	inLabels := false
	for _, line := range strings.Split(comment.Body, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, proposalMinerRow):
			proposal.Miner = strings.Trim(cell(line, 2), backtick)
		case strings.HasPrefix(line, proposalCorpusRow):
			proposal.Corpus = CorpusKinds(cell(line, 2))
		case strings.HasPrefix(line, proposalLabelsHead):
			inLabels = true
		case inLabels && strings.HasPrefix(line, tableSeparatorRow):
		case inLabels && strings.HasPrefix(line, tableCell):
			instance := strings.Trim(cell(line, 1), backtick)
			if instance != "" && len(instance) <= maxInstanceLength {
				proposal.Instances = append(proposal.Instances, instance)
			}
		case inLabels:
			inLabels = false
		}
	}
	if proposal.Miner == "" || len(proposal.Instances) == 0 {
		return Proposal{}, false
	}
	return proposal, true
}

// cell is the index-th cell of a Markdown table row, trimmed; the first
// cell is index 1.
func cell(row string, index int) string {
	cells := strings.Split(row, tableCell)
	if index >= len(cells) {
		return ""
	}
	return strings.TrimSpace(cells[index])
}

// CorpusKinds reads the corpus kinds a proposal declares, such as
// "pull_request, jsonl".
func CorpusKinds(declared string) []string {
	var kinds []string
	for _, kind := range strings.FieldsFunc(declared, func(r rune) bool { return strings.ContainsRune(corpusSeparators, r) }) {
		kind = strings.Trim(kind, backtick)
		if kind != "" {
			kinds = append(kinds, kind)
		}
	}
	return kinds
}

// LatestProposal is the newest proposal among a ticket's comments.
func LatestProposal(comments []Comment) (Proposal, bool) {
	for index := len(comments) - 1; index >= 0; index-- {
		if proposal, ok := ParseProposal(comments[index]); ok {
			return proposal, true
		}
	}
	return Proposal{}, false
}

// References reads what a labeled instance may name: harness runs by their
// assignment identifier, issues and pull requests by a #number, and commits
// by a hex revision. It is pure; existence is the pre-check's question.
func References(instance string) []Reference {
	var references []Reference
	seen := map[Reference]bool{}
	add := func(reference Reference) {
		if !seen[reference] {
			seen[reference] = true
			references = append(references, reference)
		}
	}
	for _, run := range runExpression.FindAllString(instance, -1) {
		add(Reference{Kind: InstanceRun, Value: run})
	}
	for _, match := range numberExpression.FindAllStringSubmatch(instance, -1) {
		add(Reference{Kind: InstanceIssue, Value: match[1]})
	}
	if bareNumber.MatchString(strings.TrimSpace(instance)) {
		add(Reference{Kind: InstanceIssue, Value: strings.TrimSpace(instance)})
	}
	withoutRuns := runExpression.ReplaceAllString(instance, "")
	for _, commit := range commitExpression.FindAllString(withoutRuns, -1) {
		if !bareNumber.MatchString(commit) {
			add(Reference{Kind: InstanceCommit, Value: commit})
		}
	}
	return references
}

// Prechecker is the free pre-check: it decides, without a model, whether a
// proposal's miner has anything to backtest on. It reads the corpus, asks
// the ticket capability what a number is and git whether a commit exists.
type Prechecker struct {
	corpus     iofs.IFiles
	tickets    ITickets
	launcher   proc.ILauncher
	repository string
}

// NewPrechecker grants the pre-check the corpus, the tickets, the process
// capability and the repository checkout commits are resolved in.
func NewPrechecker(corpus iofs.IFiles, tickets ITickets, launcher proc.ILauncher, repository string) (*Prechecker, error) {
	if corpus == nil || tickets == nil || launcher == nil || repository == "" {
		return nil, ErrMissingCapability
	}
	return &Prechecker{corpus: corpus, tickets: tickets, launcher: launcher, repository: repository}, nil
}

// Check decides whether the proposal's miner has anything to backtest on:
// at least one labeled instance must exist in the corpus and be of a kind
// the miner's extractor reads. A run must have a run directory, an issue or
// pull request must exist in the repository and not be the ticket itself,
// and a commit must be in the repository.
func (checker *Prechecker) Check(ctx context.Context, ticket int64, proposal Proposal) (Precheck, error) {
	result := Precheck{Verdict: VerdictNeedsLabels, Reason: reasonNoInstance}
	reads := map[string]bool{}
	for _, kind := range proposal.Corpus {
		reads[kind] = true
	}
	for _, instance := range proposal.Instances {
		for _, reference := range References(instance) {
			found, err := checker.exists(ctx, ticket, reads, reference)
			if err != nil {
				return Precheck{}, err
			}
			if found.Kind != "" {
				result.Found = append(result.Found, found)
			}
		}
	}
	if len(result.Found) > 0 {
		names := make([]string, 0, len(result.Found))
		for _, reference := range result.Found {
			names = append(names, reference.Kind+" "+reference.Value)
		}
		result.Verdict = VerdictLaunch
		result.Reason = reasonFound + reasonSeparator + strings.Join(names, instanceSeparator)
	}
	return result, nil
}

// exists resolves one reference against the corpus and reports it with its
// resolved kind, or an empty reference when it names nothing the miner
// reads.
func (checker *Prechecker) exists(ctx context.Context, ticket int64, reads map[string]bool, reference Reference) (Reference, error) {
	switch reference.Kind {
	case InstanceRun:
		if !reads[CorpusEvents] {
			return Reference{}, nil
		}
		_, err := stdfs.Stat(checker.corpus, path.Join(reference.Value, session.EventsFile))
		if errors.Is(err, stdfs.ErrNotExist) {
			return Reference{}, nil
		}
		if err != nil {
			return Reference{}, err
		}
		return reference, nil
	case InstanceIssue:
		if !reads[CorpusIssue] && !reads[CorpusPullRequest] {
			return Reference{}, nil
		}
		number, err := strconv.ParseInt(reference.Value, referenceNumberBase, 64)
		if err != nil || number == ticket {
			return Reference{}, nil
		}
		kind, err := checker.tickets.Reference(ctx, number)
		if err != nil {
			return Reference{}, err
		}
		switch {
		case kind == ReferenceIssue && reads[CorpusIssue]:
			return Reference{Kind: InstanceIssue, Value: reference.Value}, nil
		case kind == ReferencePullRequest && reads[CorpusPullRequest]:
			return Reference{Kind: InstancePullRequest, Value: reference.Value}, nil
		}
		return Reference{}, nil
	case InstanceCommit:
		if !reads[CorpusFile] {
			return Reference{}, nil
		}
		_, err := checker.launcher.Run(ctx, proc.Command{Executable: gitExecutable, Arguments: []string{
			gitDirectory, checker.repository, gitCatFile, gitExists, reference.Value + gitCommitSuffix,
		}})
		var exit *proc.ExitError
		if errors.As(err, &exit) {
			return Reference{}, nil
		}
		if err != nil {
			return Reference{}, err
		}
		return reference, nil
	}
	return Reference{}, nil
}

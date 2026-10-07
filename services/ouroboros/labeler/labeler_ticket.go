// Copyright 2026 Candace Labs

package labeler

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/candacelabs/csf/io/ipc/proc"
	ouroborosv1 "github.com/candacelabs/csf/proto/candace/ouroboros/v1"
)

// The marks a mining ticket's proposal comment carries, as the overnight
// ticket loop posted them (services/ouroboros/README.md, first mining run).
const (
	proposalMinerRow  = "| miner |"
	proposalCorpusRow = "| corpus |"
	factsHeading      = "Facts:"
	bulletMark        = "- "
	factSeparator     = " from "
	factDashSeparator = " - "
	rowSeparator      = "|"
	corpusSeparator   = ","
	backtick          = "`"
	// caseInsensitive prefixes a quoted regular expression, so it matches
	// the corpus the way a literal does.
	caseInsensitive = "(?i)"
	// predicateChars bounds the body the model reads; a ticket's predicate is
	// stated first, and what follows is discussion.
	predicateChars = 1500
)

// The corpus kinds a proposal names, as Corpus (contract/corpus.mli) reads
// them. Transcripts are never read: nothing from an operator's transcript may
// enter a record.
const (
	CorpusEvents       = "jsonl"
	CorpusIssues       = "issue"
	CorpusPullRequests = "pull_request"
	CorpusCommits      = "file_at"
)

var (
	// instancePattern is a harness assignment identifier named in a ticket.
	instancePattern = regexp.MustCompile(`\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	// quotedPattern is a literal a fact line quotes, such as 'until !'.
	quotedPattern = regexp.MustCompile(`'([^']{2,80})'`)
	// identifierPattern is a snake_case name: an event type, a field, a flag's
	// value, the vocabulary a corpus line spells exactly.
	identifierPattern = regexp.MustCompile(`\b[a-z][a-z0-9]*(?:_[a-z0-9]+)+\b`)
	// flagPattern is a command-line flag a fact line names.
	flagPattern = regexp.MustCompile(`(?:^|\s)(--?[a-z][a-z0-9-]{2,})`)
	// codePattern is a backticked span in the ticket body.
	codePattern = regexp.MustCompile("`([^`\n]{3,48})`")
	// regexMetaPattern tells a quoted regular expression from a literal.
	regexMetaPattern = regexp.MustCompile(`[\[\](){}|*+?^$\\]`)
)

// TicketComment is one comment of a ticket as the ticket API lists it: its
// identifier and its body.
type TicketComment struct {
	ID   string `json:"id"`
	Body string `json:"body"`
}

// ticketView is what gh issue view returns for one ticket.
type ticketView struct {
	Title    string          `json:"title"`
	Body     string          `json:"body"`
	Comments []TicketComment `json:"comments"`
}

// ParseTicket reads a mining ticket into its label request: the predicate
// (title and the body's first paragraphs), the latest proposal comment's
// identifier, miner, corpus kinds and fact lines, and the assignment
// identifiers the ticket itself names. A ticket with no proposal comment
// yields a request with no proposal and no miner, which the contract's
// validation refuses: the loop queues only proposals.
func ParseTicket(repository string, number int64, title string, body string, comments []TicketComment) *ouroborosv1.LabelRequest {
	request := &ouroborosv1.LabelRequest{
		Repository: repository,
		Ticket:     number,
		Predicate:  strings.TrimSpace(title + "\n" + truncate(body, predicateChars)),
		Instances:  uniqueMatches(instancePattern, body),
	}
	for _, comment := range comments {
		if !strings.Contains(comment.Body, proposalMinerRow) {
			continue
		}
		request.Proposal = comment.ID
		request.Miner = strings.Trim(rowValue(comment.Body, proposalMinerRow), backtick)
		request.Corpus = nil
		for _, kind := range strings.Split(rowValue(comment.Body, proposalCorpusRow), corpusSeparator) {
			if kind = strings.TrimSpace(kind); kind != "" {
				request.Corpus = append(request.Corpus, kind)
			}
		}
		request.Facts = factLines(comment.Body)
	}
	return request
}

// complete fills a request the loop queued with the labeler's own reading
// of the ticket: it reads the ticket through gh and takes the predicate, the
// facts and whatever the row left empty (proposal, miner, corpus,
// repository) from it, keeping every value the row carries. A request that
// already states its predicate is left as it is.
func (labeler *Labeler) complete(ctx context.Context, request *ouroborosv1.LabelRequest) error {
	if request.GetPredicate() != "" {
		return nil
	}
	if request.GetRepository() == "" {
		request.Repository = labeler.repository
	}
	if request.GetRepository() == "" {
		return ErrNoRepository
	}
	result, err := labeler.launcher.Run(ctx, proc.Command{Executable: programGH, Arguments: []string{
		ghIssue, ghView, strconv.FormatInt(request.GetTicket(), 10), ghRepoFlag, request.GetRepository(), ghJSONFlag, ghTicketFields,
	}})
	if err != nil {
		return fmt.Errorf("labeler: read ticket %d: %w", request.GetTicket(), err)
	}
	var view ticketView
	if err := json.Unmarshal(result.Stdout, &view); err != nil {
		return fmt.Errorf("labeler: decode ticket %d: %w", request.GetTicket(), err)
	}
	parsed := ParseTicket(request.GetRepository(), request.GetTicket(), view.Title, view.Body, view.Comments)
	request.Predicate, request.Facts = parsed.GetPredicate(), parsed.GetFacts()
	if request.GetProposal() == "" {
		request.Proposal = parsed.GetProposal()
	}
	if request.GetMiner() == "" {
		request.Miner = parsed.GetMiner()
	}
	if len(request.GetCorpus()) == 0 {
		request.Corpus = parsed.GetCorpus()
	}
	for _, instance := range parsed.GetInstances() {
		if !slices.Contains(request.GetInstances(), instance) {
			request.Instances = append(request.Instances, instance)
		}
	}
	return nil
}

// rowValue is the value cell of a two-column Markdown table row whose label
// cell is mark, or empty.
func rowValue(text string, mark string) string {
	_, after, found := strings.Cut(text, mark)
	if !found {
		return ""
	}
	value, _, _ := strings.Cut(after, rowSeparator)
	return strings.TrimSpace(value)
}

// factLines are the bullet lines under the proposal's Facts heading, with
// their bullets and backticks removed.
func factLines(comment string) []string {
	_, after, found := strings.Cut(comment, factsHeading)
	if !found {
		return nil
	}
	var facts []string
	for _, line := range strings.Split(after, "\n")[1:] {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, bulletMark) {
			if trimmed == "" && len(facts) > 0 {
				break
			}
			if trimmed != "" {
				break
			}
			continue
		}
		facts = append(facts, strings.Trim(strings.TrimPrefix(trimmed, bulletMark), backtick))
	}
	return facts
}

// term is one thing the prefilter looks for in a corpus line: a literal,
// matched without case, or a regular expression a fact line quoted.
type term struct {
	literal string
	pattern *regexp.Regexp
}

// String is the term as the run's log and the model's prompt show it.
func (searched term) String() string {
	if searched.pattern != nil {
		return searched.pattern.String()
	}
	return searched.literal
}

// matches reports whether line, already lowercased, carries the term.
func (searched term) matches(lowered string, line string) bool {
	if searched.pattern != nil {
		return searched.pattern.MatchString(line)
	}
	return strings.Contains(lowered, searched.literal)
}

// index is where the term first occurs in line, or -1.
func (searched term) index(lowered string, line string) int {
	if searched.pattern != nil {
		if location := searched.pattern.FindStringIndex(line); location != nil {
			return location[0]
		}
		return -1
	}
	return strings.Index(lowered, searched.literal)
}

// extractTerms is the deterministic prefilter's vocabulary for one request:
// what the proposal's fact lines say they read from the corpus (quoted
// literals, snake_case names, flags), and the backticked spans and
// snake_case names of the predicate. A fact's relation name is the miner's
// own word, not the corpus's, so only the text after its separator counts.
func extractTerms(request *ouroborosv1.LabelRequest) []term {
	var terms []term
	seen := map[string]bool{}
	add := func(candidate term) {
		key := candidate.String()
		if len(key) < 3 || seen[key] {
			return
		}
		seen[key] = true
		terms = append(terms, candidate)
	}
	for _, fact := range request.GetFacts() {
		description := factDescription(fact)
		for _, quoted := range uniqueMatches(quotedPattern, description) {
			if regexMetaPattern.MatchString(quoted) {
				if pattern, err := regexp.Compile(caseInsensitive + quoted); err == nil {
					add(term{pattern: pattern})
					continue
				}
			}
			add(term{literal: strings.ToLower(quoted)})
		}
		for _, identifier := range uniqueMatches(identifierPattern, description) {
			add(term{literal: identifier})
		}
		for _, flag := range uniqueMatches(flagPattern, description) {
			add(term{literal: strings.ToLower(flag)})
		}
	}
	for _, code := range uniqueMatches(codePattern, request.GetPredicate()) {
		add(term{literal: strings.ToLower(code)})
	}
	for _, identifier := range uniqueMatches(identifierPattern, request.GetPredicate()) {
		add(term{literal: identifier})
	}
	return terms
}

// factDescription is the part of a fact line that names where in the corpus
// the relation is read from: after " from ", or after " - " in the other
// spelling the proposals used.
func factDescription(fact string) string {
	if _, after, found := strings.Cut(fact, factSeparator); found {
		return after
	}
	if _, after, found := strings.Cut(fact, factDashSeparator); found {
		return after
	}
	return ""
}

// uniqueMatches are the first capture (or the whole match) of every match
// of pattern in text, in order, without repeats.
func uniqueMatches(pattern *regexp.Regexp, text string) []string {
	var matches []string
	seen := map[string]bool{}
	for _, match := range pattern.FindAllStringSubmatch(text, -1) {
		value := match[0]
		if len(match) > 1 {
			value = match[1]
		}
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		matches = append(matches, value)
	}
	return matches
}

// truncate cuts text to at most limit bytes on a line boundary.
func truncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := text[:limit]
	if last := strings.LastIndexByte(cut, '\n'); last > 0 {
		cut = cut[:last]
	}
	return cut
}

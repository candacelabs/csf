// Copyright 2026 Candace Labs

package sessiongate

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"

	"github.com/candacelabs/csf/csf/githubtools"
	"github.com/candacelabs/csf/pkg/affect"
	"github.com/candacelabs/csf/pkg/argv"
	"github.com/candacelabs/csf/pkg/terms"
	"github.com/candacelabs/csf/services/harness/session"
)

// The reply gate's rules: what a reply to the operator may not do.
const (
	// RuleResearchCheck: a reply to a message with unvetted terms carries a
	// complete research check for each, or it is refused.
	RuleResearchCheck Rule = "research_check"
	// RuleCommitment: a reply that commits the agent's future behaviour in
	// the first person ("I'll", "from now on", "going forward", "until then I
	// will", "every time") with no enforcing artifact produced in the same
	// turn is refused (#47, commitment without action).
	RuleCommitment Rule = "commitment"
	// RuleWaitWithoutChild: a reply that says the turn's work continues when
	// a background result arrives ("waiting on", "will follow", "once the run
	// finishes") while the session runs no background task is refused: no
	// completion will wake the session, so the wait is a commitment without
	// action (#47, #324).
	RuleWaitWithoutChild Rule = "wait_without_child"
	// RuleStrain: a reply to an operator message whose strain reads high is
	// at most affect.ReplyWordLimit words, the length past which the
	// operator's corrections rise (pkg/affect derives it).
	RuleStrain Rule = "strain"
)

// The enforcing artifacts a turn can produce, as the decision names them.
const (
	ArtifactCommit     = "commit"
	ArtifactGateOrHook = "gate_or_hook_change"
	ArtifactTicketItem = "ticket_item_with_owner"
)

const (
	// replyRefusalLimit bounds the refusals of one turn: once a turn's reply
	// has been refused this many times the next one passes, recorded as
	// limit. It is a loop guard, not a measured threshold; the limit records
	// are the data for setting one.
	replyRefusalLimit = 2

	decisionBlock = "block"

	keyMissingTerms = "missing_terms"
	keyCommitments  = "commitments"
	keyArtifacts    = "artifacts"
	keyRefusals     = "refusals"

	toolEdit       = "Edit"
	toolWrite      = "Write"
	toolMultiEdit  = "MultiEdit"
	inputCommand   = "command"
	inputFilePath  = "file_path"
	sentenceLimit  = 160
	ellipsis       = "…"
	listSeparator  = ", "
	replyRejection = "Rejected by the CSF reply gate. "
)

// commitmentMarkers are the first-person future commitments #47 lists.
var commitmentMarkers = regexp.MustCompile(`(?i)\b(i'll|i’ll|from now on|going forward|until then i will|every time)\b`)

// waitMarkers are the ways a reply says its turn's work continues when a
// background result arrives. They were read off the 935 final replies the
// harness had recorded by 2026-10-05: 202 carry one. Of the 36 that did while
// the executor reported no background task running, 34 were never woken by a
// completion (31 waited for a Send, 3 for nothing), a precision of 0.944; of
// the 166 that did with a task running, 106 were woken by its completion.
var waitMarkers = regexp.MustCompile(`(?i)\b(waiting (on|for)|will follow|once (the|it|they|that|this|those|these) [\w-]+( [\w-]+)? (finish|finishes|completes|complete|lands|land|ends|end|returns|return|reports|report)|when (the|it|they) [\w-]+( [\w-]+)? (finish|finishes|completes|complete|lands|land)|completion notification|notif(y|ies) me)\b`)

// sentenceEnd splits a reply into sentences for the finding's quotation.
var sentenceEnd = regexp.MustCompile(`[.!?\n]`)

// researchCheckBlock is a fenced block tagged as a research check.
var researchCheckBlock = regexp.MustCompile("(?s)```" + session.ResearchCheckFence + "[ \t]*\n(.*?)\n[ \t]*```")

// gateAndHookPaths are the places a gate or hook change lands in a CSF
// repository: the session gates, the house lint, the merge path's checks,
// the executor's hook settings, git hooks and the workflows.
var gateAndHookPaths = []string{
	"services/harness/sessiongate/", "tools/house_lint/", "tools/check-", ".claude/settings", "/hooks/", ".githooks/", ".github/workflows/",
}

// editingTools are the tools that change a file in place.
var editingTools = map[string]bool{toolEdit: true, toolWrite: true, toolMultiEdit: true}

// Artifact is one enforcing artifact a turn produced: its kind and what it
// was.
type Artifact struct {
	Kind   string
	Detail string
}

// Commitment is one first-person future commitment found in a reply.
type Commitment struct {
	Marker   string
	Sentence string
}

// ReplyFinding is one reason the reply gate refuses a reply.
type ReplyFinding struct {
	Rule    Rule
	Message string
}

// artifactRule reads one tool call for one kind of artifact.
type artifactRule struct {
	kind    string
	matches func(tool session.ToolUse) (detail string, found bool)
}

// artifactRules are the enforcing artifacts, in the order the decision lists
// them.
var artifactRules = []artifactRule{
	{kind: ArtifactCommit, matches: commitArtifact},
	{kind: ArtifactGateOrHook, matches: gateOrHookArtifact},
	{kind: ArtifactTicketItem, matches: ticketItemArtifact},
}

// JudgeReply applies the reply gate's rules to one reply: unvetted are the
// turn's unvetted terms, tools the tool calls the turn made, background the
// tasks the executor runs in the background as the turn ends and strain the
// operator's strain as the turn's message read. No finding means the reply
// passes.
func JudgeReply(reply string, unvetted []terms.Term, tools []session.ToolUse, background []session.BackgroundTask, strain affect.Strain) []ReplyFinding {
	findings := []ReplyFinding{}
	checks, malformed := ParseResearchChecks(reply)
	if missing := MissingResearchChecks(unvetted, checks); len(missing) > 0 {
		findings = append(findings, ReplyFinding{Rule: RuleResearchCheck, Message: researchCheckMessage(missing, malformed)})
	}
	commitments := FindCommitments(reply)
	if len(commitments) > 0 {
		if artifacts := EnforcingArtifacts(tools); len(artifacts) == 0 {
			findings = append(findings, ReplyFinding{Rule: RuleCommitment, Message: commitmentMessage(commitments)})
		}
	}
	if waits := FindWaits(reply); len(waits) > 0 && len(background) == 0 {
		findings = append(findings, ReplyFinding{Rule: RuleWaitWithoutChild, Message: waitMessage(waits)})
	}
	if words := len(strings.Fields(reply)); strain == affect.StrainHigh && words > affect.ReplyWordLimit {
		findings = append(findings, ReplyFinding{Rule: RuleStrain, Message: fmt.Sprintf(
			"%s: the operator's strain reads high and the reply runs %d words; answer in at most %d", RuleStrain, words, affect.ReplyWordLimit)})
	}
	return findings
}

// FindWaits returns every sentence of reply that says its work continues when
// a background result arrives, with the first marker found there. Fenced and
// inline code are not prose and never match.
func FindWaits(reply string) []Commitment {
	return findMarked(reply, waitMarkers)
}

// ParseResearchChecks reads every research check a reply carries: each fenced
// block tagged research-check holding one JSON object or an array of them. A
// block that is neither is returned among the errors and skipped.
func ParseResearchChecks(reply string) ([]session.ResearchCheck, []error) {
	checks := []session.ResearchCheck{}
	var malformed []error
	for index, match := range researchCheckBlock.FindAllStringSubmatch(reply, -1) {
		body := strings.TrimSpace(match[1])
		var one session.ResearchCheck
		if json.Unmarshal([]byte(body), &one) == nil {
			checks = append(checks, one)
			continue
		}
		var several []session.ResearchCheck
		if err := json.Unmarshal([]byte(body), &several); err != nil {
			malformed = append(malformed, fmt.Errorf("research-check block %d is not a JSON object or array: %w", index+1, err))
			continue
		}
		checks = append(checks, several...)
	}
	return checks, malformed
}

// MissingResearchChecks returns the unvetted terms no complete check covers,
// in order. A check covers a term when the two share a stem.
func MissingResearchChecks(unvetted []terms.Term, checks []session.ResearchCheck) []terms.Term {
	missing := []terms.Term{}
	for _, term := range unvetted {
		covered := slices.ContainsFunc(checks, func(check session.ResearchCheck) bool {
			return terms.Same(terms.Term(check.Term), term) && len(check.Incomplete()) == 0
		})
		if !covered {
			missing = append(missing, term)
		}
	}
	return missing
}

// FindCommitments returns every first-person future commitment in reply, one
// per sentence, with the sentence it stands in and the first marker found
// there. Fenced and inline code are not prose and never match.
func FindCommitments(reply string) []Commitment {
	return findMarked(reply, commitmentMarkers)
}

// findMarked returns, once per sentence, every sentence of reply's prose that
// markers match, with the first marker found there.
func findMarked(reply string, markers *regexp.Regexp) []Commitment {
	prose := terms.StripCode(reply)
	commitments := []Commitment{}
	quoted := map[int]bool{}
	for _, span := range markers.FindAllStringIndex(prose, -1) {
		from, to := sentenceAround(prose, span[0], span[1])
		if quoted[from] {
			continue
		}
		quoted[from] = true
		commitments = append(commitments, Commitment{Marker: strings.ToLower(prose[span[0]:span[1]]), Sentence: quotation(prose[from:to])})
	}
	return commitments
}

// sentenceAround is the span of the sentence of text holding [start, end),
// without its terminal punctuation.
func sentenceAround(text string, start int, end int) (from int, to int) {
	if previous := sentenceEnd.FindAllStringIndex(text[:start], -1); len(previous) > 0 {
		from = previous[len(previous)-1][1]
	}
	to = len(text)
	if next := sentenceEnd.FindStringIndex(text[end:]); next != nil {
		to = end + next[0]
	}
	return from, to
}

// quotation is sentence trimmed and bounded for the reason.
func quotation(sentence string) string {
	sentence = strings.TrimSpace(sentence)
	if len(sentence) > sentenceLimit {
		sentence = sentence[:sentenceLimit] + ellipsis
	}
	return sentence
}

// EnforcingArtifacts returns the enforcing artifacts among the tool calls a
// turn made: a commit, a change to a gate or hook, or a ticket item with an
// owner.
func EnforcingArtifacts(tools []session.ToolUse) []Artifact {
	artifacts := []Artifact{}
	for _, tool := range tools {
		for _, rule := range artifactRules {
			if detail, found := rule.matches(tool); found {
				artifacts = append(artifacts, Artifact{Kind: rule.kind, Detail: detail})
			}
		}
	}
	return artifacts
}

// commitArtifact is a Bash call that runs git commit.
func commitArtifact(tool session.ToolUse) (string, bool) {
	command, ok := inputString(tool, session.ToolBash, inputCommand)
	if !ok {
		return "", false
	}
	if commits, err := RunsGitCommit(command); err != nil || !commits {
		return "", false
	}
	return firstLine(command), true
}

// gateOrHookArtifact is an edit of a file where gates and hooks live.
func gateOrHookArtifact(tool session.ToolUse) (string, bool) {
	if !editingTools[tool.Name] {
		return "", false
	}
	path, ok := inputString(tool, tool.Name, inputFilePath)
	if !ok {
		return "", false
	}
	for _, fragment := range gateAndHookPaths {
		if strings.Contains(path, fragment) {
			return path, true
		}
	}
	return "", false
}

// ticketItemArtifact is a call that creates or edits a ticket with an owner
// assigned: the GitHub tools' IssuesCreate or IssuesUpdate, or the gh command
// the GitHub gate now refuses.
func ticketItemArtifact(tool session.ToolUse) (string, bool) {
	if ownedIssueTools[tool.Name] {
		return ownedIssueCall(tool)
	}
	command, ok := inputString(tool, session.ToolBash, inputCommand)
	if !ok {
		return "", false
	}
	if owned, err := RunsOwnedTicketItem(command); err != nil || !owned {
		return "", false
	}
	return firstLine(command), true
}

// ownedIssueTools are the GitHub tools that create or edit an issue.
var ownedIssueTools = map[string]bool{
	mcpToolPrefix + githubtools.ToolIssuesCreate: true,
	mcpToolPrefix + githubtools.ToolIssuesUpdate: true,
}

// ownedIssueCall is an IssuesCreate or IssuesUpdate call whose body names an
// assignee.
func ownedIssueCall(tool session.ToolUse) (string, bool) {
	var input struct {
		Owner string `json:"owner"`
		Repo  string `json:"repo"`
		Body  struct {
			Assignee  string   `json:"assignee"`
			Assignees []string `json:"assignees"`
		} `json:"body"`
	}
	if json.Unmarshal(tool.Input, &input) != nil || (input.Body.Assignee == "" && len(input.Body.Assignees) == 0) {
		return "", false
	}
	return strings.TrimPrefix(tool.Name, mcpToolPrefix) + " " + input.Owner + "/" + input.Repo, true
}

// inputString is the string field key of a call to tool; ok is false for
// another tool or a missing field.
func inputString(tool session.ToolUse, name string, key string) (string, bool) {
	if tool.Name != name {
		return "", false
	}
	var input map[string]json.RawMessage
	if json.Unmarshal(tool.Input, &input) != nil {
		return "", false
	}
	var value string
	if json.Unmarshal(input[key], &value) != nil || value == "" {
		return "", false
	}
	return value, true
}

func firstLine(text string) string {
	return strings.TrimSpace(strings.SplitN(text, "\n", 2)[0])
}

// ghIssueAssignee are gh's ways of naming a ticket's owner.
var ghIssueAssignee = []argv.Flag{{Short: 'a', Long: "assignee"}, {Long: "add-assignee"}}

const (
	ghNounIssue = "issue"
	ghVerbEdit  = "edit"
)

// RunsOwnedTicketItem reports whether command creates or edits a GitHub issue
// with an assignee anywhere in it, including inside sh -c.
func RunsOwnedTicketItem(command string) (bool, error) {
	return runsCommand(command, func(name string, arguments []string) bool {
		if name != commandGh || len(arguments) < 2 || arguments[0] != ghNounIssue || (arguments[1] != ghCreate && arguments[1] != ghVerbEdit) {
			return false
		}
		return slices.ContainsFunc(ghIssueAssignee, func(flag argv.Flag) bool { return argv.HasFlag(arguments, flag) })
	})
}

func researchCheckMessage(missing []terms.Term, malformed []error) string {
	spelled := make([]string, 0, len(missing))
	for _, term := range missing {
		spelled = append(spelled, string(term))
	}
	message := fmt.Sprintf("%s: no complete research check for the unvetted term(s) %s. For each, add to your reply a fenced block tagged %s holding %s, every field filled and the enumerated ones set to one of the values shown",
		RuleResearchCheck, strings.Join(spelled, listSeparator), session.ResearchCheckFence, session.ResearchCheckSkeleton(missing[0]))
	for _, err := range malformed {
		message += "; " + err.Error()
	}
	return message
}

func commitmentMessage(commitments []Commitment) string {
	quoted := make([]string, 0, len(commitments))
	for _, commitment := range commitments {
		quoted = append(quoted, fmt.Sprintf("%q", commitment.Sentence))
	}
	return fmt.Sprintf("%s: %s commits your future behaviour with no enforcing artifact produced in this turn (a commit, a gate or hook change, or a ticket item with an owner). Produce the artifact and name it in the reply, or withdraw the commitment",
		RuleCommitment, strings.Join(quoted, listSeparator))
}

func waitMessage(waits []Commitment) string {
	quoted := make([]string, 0, len(waits))
	for _, wait := range waits {
		quoted = append(quoted, fmt.Sprintf("%q", wait.Sentence))
	}
	return fmt.Sprintf("%s: %s waits on a background result, but this session runs no background task, so no completion will wake it. Do the work in this turn, start it with run_in_background before ending the turn, or remove the wait",
		RuleWaitWithoutChild, strings.Join(quoted, listSeparator))
}

// reply is the Stop gate: the turn executor has finished its reply to the
// operator, and the reply is read back from the run's event log with the
// turn's unvetted terms and tool calls. A reply the rules refuse is blocked
// with a typed reason the turn continues from; after replyRefusalLimit
// refusals in one turn the reply passes and the limit is recorded. A turn
// whose reply the log does not yet hold is skipped, and so is a log that
// cannot be read: the gate never holds a turn over its own failure. The
// background tasks a wait needs are read from the whole log, since a task
// started in an earlier turn can still be the one that wakes the session.
func (call *gateCall) reply(ctx context.Context) *HookOutput {
	records, err := session.ReadRecords(call.gate.directory, func(_ *session.Record) bool { return true })
	if err != nil {
		call.failure(ctx, GateReply, DecisionFailed, err)
		return nil
	}
	turn := turnOf(session.TurnRecords(records))
	if turn.reply == "" {
		call.record(ctx, GateReply, DecisionSkip, slog.String(keyReason, "no reply recorded for the turn"))
		return nil
	}
	findings := JudgeReply(turn.reply, turn.unvetted, turn.tools, session.LiveBackgroundTasks(records), turn.strain)
	verdicts, questionFindings := JudgeQuestions(ReplyQuestions(turn.reply), turn.rulings)
	findings = append(append(findings, questionFindings...), JudgeMeasurement(turn.operatorMessage(), turn.reply)...)
	if len(findings) == 0 {
		call.record(ctx, GateReply, DecisionAllow, slog.Any(KeyQuestions, verdicts))
		return nil
	}
	rules := make([]string, 0, len(findings))
	messages := make([]string, 0, len(findings))
	for _, finding := range findings {
		rules = append(rules, string(finding.Rule))
		messages = append(messages, finding.Message)
	}
	attributes := []slog.Attr{slog.Any(keyRules, rules), slog.Int(keyRefusals, turn.refusals), slog.Any(KeyQuestions, verdicts)}
	if turn.refusals >= replyRefusalLimit {
		call.record(ctx, GateReply, DecisionLimit, attributes...)
		return nil
	}
	reason := replyRejection + strings.Join(messages, "; ") + "."
	call.record(ctx, GateReply, DecisionDeny, append(attributes, slog.String(keyReason, reason))...)
	return &HookOutput{Decision: decisionBlock, Reason: reason}
}

// turnState is what the reply gate reads from the turn's records.
type turnState struct {
	unvetted []terms.Term
	strain   affect.Strain
	reply    string
	tools    []session.ToolUse
	refusals int
	// message is the turn's message and operator whether the operator wrote
	// it: the harness records unvetted terms for the operator's turns only.
	message  string
	operator bool
	rulings  []session.Ruling
}

// operatorMessage is the turn's message when the operator wrote it.
func (turn turnState) operatorMessage() string {
	if !turn.operator {
		return ""
	}
	return turn.message
}

// turnOf reads the turn: its unvetted terms, the operator's strain, the text
// of its last assistant message that said anything, every tool it called, and
// how often the reply gate has refused it.
func turnOf(records []session.Record) turnState {
	turn := turnState{unvetted: []terms.Term{}, strain: affect.StrainLow, tools: []session.ToolUse{}, rulings: []session.Ruling{}}
	for index := range records {
		record := &records[index]
		switch {
		case record.EventType == session.EventTypeUnvettedTerms:
			turn.unvetted = append(turn.unvetted, record.Terms...)
			turn.operator = true
		case record.EventType == session.EventTypeRulings:
			turn.rulings = record.Rulings
		case record.EventType == session.EventTypeUser && record.Direction == session.DirectionIn && turn.message == "":
			turn.message = strings.Join(record.Text(), "\n\n")
		case record.EventType == session.EventTypeOperatorAffect && record.Affect != nil:
			turn.strain = record.Affect.Strain
		case record.EventType == session.EventTypeAssistant && record.Direction == session.DirectionOut:
			text, tools := record.Assistant()
			turn.tools = append(turn.tools, tools...)
			if len(text) > 0 {
				turn.reply = strings.Join(text, "\n\n")
			}
		case record.EventType == session.EventTypeGateDecision && record.Gate == GateReply && record.Decision == DecisionDeny:
			turn.refusals++
		}
	}
	return turn
}

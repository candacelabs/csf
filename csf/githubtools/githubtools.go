// Copyright 2026 Candace Labs

// Package githubtools registers the GitHub protocol client's operations as
// typed MCP tools and HTTP routes on the CSF MCP endpoint csf serve already
// has. tools.gen.go is generated from ipc/github/api.github.com.json, the
// same file the client is generated from, so a tool and the client call
// behind it cannot drift: the tool's name, description and input and output
// types all come from GitHub's own description. This package adds no
// process and starts no goroutine.
//
// Every call is recorded with its actor: a virtual session, named by the
// assignment its request carries in [AssignmentHeader], or the operator.
// The record goes to the host's GitHub log (<state>/github.jsonl), which the
// Grafana panel's collector reads, and, for a virtual session, to its run's
// event log, which its Workbench card shows. Merging follows a [MergePolicy]
// held as data with the operator ruling it carries out.
package githubtools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/candacelabs/csf/csf"
	"github.com/candacelabs/csf/csf/prod"
	"github.com/candacelabs/csf/io/kernel/clock"
	"github.com/candacelabs/csf/io/net/github"
	"github.com/candacelabs/csf/pkg/atomicfile"
	"github.com/candacelabs/csf/services/harness/session"
)

const (
	// AssignmentHeader names the virtual session a request acts for: its
	// assignment identifier, which the harness puts on every session's MCP
	// requests. A request without it acts for the operator.
	AssignmentHeader = session.AssignmentHeader
	// LogFile is the host's GitHub log under the state directory.
	LogFile = "github.jsonl"
	// RoutePrefix is where the tools' HTTP routes live: RoutePrefix plus
	// the operationId, such as /api/github/pulls/merge.
	RoutePrefix = "/api/github/"
	// ToolMarkPullRequestReady takes a draft out of draft: the one tool not
	// in GitHub's REST description, so the one not generated.
	ToolMarkPullRequestReady = session.ReadyToolName
	readyRoute               = "pulls/ready"

	// ActorOperator and ActorVirtualSession are who made a call.
	ActorOperator       ActorKind = "operator"
	ActorVirtualSession ActorKind = "virtual_session"

	// OutcomeOK and OutcomeFailed are how a call ended.
	OutcomeOK     = "ok"
	OutcomeFailed = "failed"

	logFileMode = 0o600
	logFlags    = os.O_CREATE | os.O_APPEND | os.O_WRONLY

	keyRepository = "repository"
	keyNumber     = "number"
	keyOutcome    = "outcome"
)

var (
	// ErrNoClient reports tools built without the GitHub protocol client.
	ErrNoClient = errors.New("github tools: a GitHub client is required")
	// ErrNoStateDirectory reports tools built without the state directory.
	ErrNoStateDirectory = errors.New("github tools: a state directory is required")
	// ErrInvalidOption reports a nil option or a value the tools cannot use.
	ErrInvalidOption = errors.New("github tools: invalid option")
	// ErrChecksRequired reports a merge the policy refuses because a check
	// run on the head has not passed.
	ErrChecksRequired = errors.New("github tools: the merge policy requires passing checks")
	// ErrHeldBranch reports a merge the guard refuses because the pull
	// request's head branch belongs to a held slice. It reads as the first
	// words of the refusal the agent is given.
	ErrHeldBranch = errors.New("this branch is held")
	// ErrGoldenMetrics reports a merge the golden metrics refuse: the merge
	// result lowers the seed files, breaks a directory main's seed held clean,
	// or raises the chief violations.
	ErrGoldenMetrics = errors.New("github tools: the golden metrics refuse the merge")
)

// ActorKind is who made a GitHub call.
type ActorKind string

// MergePolicy is how the PullsMerge tool merges, held as data with the
// operator ruling it carries out.
type MergePolicy struct {
	Method github.PullsmergeJSONBodyMergeMethod `json:"method"`
	// RequireChecks refuses a merge unless every check run on the head has
	// completed and none failed.
	RequireChecks bool `json:"require_checks"`
	// Ruling is the operator's ruling, verbatim, and RuledAt when it was made.
	Ruling  string `json:"ruling"`
	RuledAt string `json:"ruled_at"`
}

// DefaultMergePolicy is the ruling in force: merge every pull request as
// soon as it has content, squashed, with no checks. The repository has no
// branch protection, so an administrator's merge and a plain one are the
// same REST call.
var DefaultMergePolicy = MergePolicy{
	Method:        github.PullsmergeJSONBodyMergeMethodSquash,
	RequireChecks: false,
	Ruling:        "WE'RE BUILDING THE WHOLE THING AND THEN FIXING ALL THE BUGS, WE'RE MERGING EVERYTHING, NO CHECKS",
	RuledAt:       "2026-10-05T02:09Z",
}

// CallRecord is one GitHub call as the host's GitHub log records it.
type CallRecord struct {
	Time       time.Time `json:"time"`
	Operation  string    `json:"operation"`
	Actor      ActorKind `json:"actor"`
	Assignment string    `json:"assignment,omitempty"`
	Repository string    `json:"repository"`
	Number     int       `json:"number,omitempty"`
	Outcome    string    `json:"outcome"`
	Error      string    `json:"error,omitempty"`
	// RateLimit and RateRemaining are the core rate limit GitHub reported on
	// the call's response.
	RateLimit     int          `json:"rate_limit,omitempty"`
	RateRemaining int          `json:"rate_remaining,omitempty"`
	Policy        *MergePolicy `json:"policy,omitempty"`
}

// MergeGuard is the held-branch check every merge runs: given a pull request's
// head branch, it reports whether the branch is held and why. The host wires
// the slice dispatcher here, so a held slice's branch cannot merge. A nil
// guard checks nothing; when it runs, a pull request that cannot be read
// fails the merge closed, and a branch it does not report held merges.
type MergeGuard func(ctx context.Context, branch string) (reason string, held bool, err error)

// GoldenMeasurer measures the golden metrics at one revision: the per
// directory chief counts the merge quality gate compares between main and the
// merge result (csf/prod). The host wires the tree's measurer here. A nil
// measurer checks nothing; when it runs, a revision that cannot be measured
// fails the merge closed.
type GoldenMeasurer func(ctx context.Context, owner, repo, revision string) (prod.Reading, error)

// GitHubTools serves the GitHub operations as tools and records every call.
type GitHubTools struct {
	client   *github.GitHubClient
	state    string
	clock    clock.IClock
	policy   MergePolicy
	guard    MergeGuard
	measurer GoldenMeasurer
	logger   *slog.Logger
	// schemas are each operation's tool schemas, from GitHub's description.
	schemas map[string]toolSchemas
}

// GitHubToolsOption configures [GitHubTools].
type GitHubToolsOption func(tools *GitHubTools) error

// WithClient grants the GitHub protocol client. Required.
func WithClient(client *github.GitHubClient) GitHubToolsOption {
	return func(tools *GitHubTools) error {
		if client == nil {
			return ErrNoClient
		}
		tools.client = client
		return nil
	}
}

// WithStateDirectory names the harness state directory: the GitHub log is
// written there, and each virtual session's run is found there. Required.
func WithStateDirectory(directory string) GitHubToolsOption {
	return func(tools *GitHubTools) error {
		if directory == "" {
			return ErrNoStateDirectory
		}
		tools.state = directory
		return nil
	}
}

// WithClock replaces the system clock the records are stamped with.
func WithClock(source clock.IClock) GitHubToolsOption {
	return func(tools *GitHubTools) error {
		if source == nil {
			return fmt.Errorf("%w: nil clock", ErrInvalidOption)
		}
		tools.clock = source
		return nil
	}
}

// WithMergePolicy replaces [DefaultMergePolicy].
func WithMergePolicy(policy MergePolicy) GitHubToolsOption {
	return func(tools *GitHubTools) error {
		if !policy.Method.Valid() || strings.TrimSpace(policy.Ruling) == "" {
			return fmt.Errorf("%w: a merge policy needs a merge method and the ruling it carries out", ErrInvalidOption)
		}
		tools.policy = policy
		return nil
	}
}

// WithMergeGuard grants the held-branch check every merge runs on the pull
// request's head branch: a branch the guard reports held is refused with its
// reason. Without it no branch is checked.
func WithMergeGuard(guard MergeGuard) GitHubToolsOption {
	return func(tools *GitHubTools) error {
		if guard == nil {
			return fmt.Errorf("%w: nil merge guard", ErrInvalidOption)
		}
		tools.guard = guard
		return nil
	}
}

// WithGoldenMetrics grants the golden metrics gate every merge runs: the
// pull request's base revision is main, its head revision is the merge result,
// and a merge that lowers the seed files, breaks a directory main's seed held
// clean, or raises the chief violations is refused. Without it no golden
// metrics are measured.
func WithGoldenMetrics(measurer GoldenMeasurer) GitHubToolsOption {
	return func(tools *GitHubTools) error {
		if measurer == nil {
			return fmt.Errorf("%w: nil golden measurer", ErrInvalidOption)
		}
		tools.measurer = measurer
		return nil
	}
}

// WithLogger logs records the tools could not write.
func WithLogger(logger *slog.Logger) GitHubToolsOption {
	return func(tools *GitHubTools) error {
		if logger == nil {
			return fmt.Errorf("%w: nil logger", ErrInvalidOption)
		}
		tools.logger = logger
		return nil
	}
}

// NewGitHubTools validates the whole option set before building the tools.
func NewGitHubTools(options ...GitHubToolsOption) (*GitHubTools, error) {
	tools := &GitHubTools{clock: clock.NewSystemClock(), policy: DefaultMergePolicy, logger: slog.New(slog.DiscardHandler)}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("%w: nil option", ErrInvalidOption)
		}
		if err := option(tools); err != nil {
			return nil, err
		}
	}
	switch {
	case tools.client == nil:
		return nil, ErrNoClient
	case tools.state == "":
		return nil, ErrNoStateDirectory
	}
	schemas, err := schemasOf(github.Description)
	if err != nil {
		return nil, err
	}
	for _, operation := range generatedOperations {
		if _, ok := schemas[operation]; !ok {
			return nil, fmt.Errorf("github tools: %s is generated but not in the description", operation)
		}
	}
	tools.schemas = schemas
	return tools, nil
}

// tool is one generated tool with its input and output schemas from
// GitHub's description.
func (tools *GitHubTools) tool(name string, operation string, description string) mcp.Tool {
	schemas := tools.schemas[operation]
	return mcp.Tool{Name: name, Description: description, InputSchema: schemas.input, OutputSchema: schemas.output}
}

// MergePolicy is the policy the PullsMerge tool follows.
func (tools *GitHubTools) MergePolicy() MergePolicy { return tools.policy }

// Tools is every GitHub operation as an MCP tool for csf.New.
func (tools *GitHubTools) Tools() []csf.Option { return csf.OperationTools(tools.operations()) }

// Register mounts every GitHub operation's HTTP route on the caller's router.
func (tools *GitHubTools) Register(router gin.IRouter) {
	csf.RegisterOperations(router, tools.operations())
}

// Invoke calls the tool named name in-process with its JSON input, as the
// operator: what the csf github verb does.
func (tools *GitHubTools) Invoke(ctx context.Context, name string, input []byte) ([]byte, error) {
	return csf.InvokeOperation(ctx, tools.operations(), name, input)
}

// Names lists every tool's name.
func (tools *GitHubTools) Names() []string {
	operations := tools.operations()
	names := make([]string, 0, len(operations))
	for _, operation := range operations {
		names = append(names, operation.Name)
	}
	return names
}

func (tools *GitHubTools) operations() []csf.Operation {
	return append(tools.generated(), csf.NewOperation(ToolMarkPullRequestReady,
		"Mark a draft pull request ready for review (GitHub's GraphQL markPullRequestReadyForReview; REST has no such operation). A pull request that is not a draft is left as it is. The session's ready gate runs the merge checks first.",
		http.MethodPost, RoutePrefix+readyRoute, tools.handleMarkPullRequestReady))
}

// MarkPullRequestReadyInput is the input of the MarkPullRequestReady tool.
type MarkPullRequestReadyInput struct {
	Owner      string `json:"owner" jsonschema:"The account owner of the repository"`
	Repo       string `json:"repo" jsonschema:"The name of the repository without the '.git' extension"`
	PullNumber int    `json:"pull_number" jsonschema:"The number that identifies the pull request"`
}

func (tools *GitHubTools) handleMarkPullRequestReady(ctx context.Context, input MarkPullRequestReadyInput) (github.ReadyPullRequest, error) {
	call := toolCall{operation: ToolMarkPullRequestReady, owner: input.Owner, repo: input.Repo, number: input.PullNumber}
	if err := tools.prepare(ctx, &call, &input); err != nil {
		return github.ReadyPullRequest{}, err
	}
	ready, rate, err := tools.client.MarkPullRequestReady(ctx, input.Owner, input.Repo, input.PullNumber)
	tools.record(ctx, call, rate, err)
	return ready, classify(err)
}

// toolCall is one tool call being made: what the record says about it.
type toolCall struct {
	operation  string
	owner      string
	repo       string
	number     int
	actor      ActorKind
	assignment string
	policy     *MergePolicy
}

// iPolicyFollower is an input a merge policy applies to.
type iPolicyFollower interface {
	follow(policy MergePolicy)
}

// follow makes a merge input carry the policy's method, whatever it asked.
func (input *PullsMergeInput) follow(policy MergePolicy) {
	method := policy.Method
	input.Body.MergeMethod = &method
}

// prepare names the call's actor from the request in ctx, refuses an
// incomplete repository and applies the merge policy to a merge.
func (tools *GitHubTools) prepare(ctx context.Context, call *toolCall, input any) error {
	call.actor = ActorOperator
	if assignment := strings.TrimSpace(csf.RequestHeader(ctx).Get(AssignmentHeader)); assignment != "" {
		call.actor, call.assignment = ActorVirtualSession, assignment
	}
	if call.owner == "" || call.repo == "" {
		err := fmt.Errorf("%w: owner and repo are required", csf.ErrInvalidRequest)
		tools.record(ctx, *call, github.RateLimit{}, err)
		return err
	}
	follower, merges := input.(iPolicyFollower)
	if !merges {
		return nil
	}
	policy := tools.policy
	call.policy = &policy
	follower.follow(policy)
	if err := tools.refuseHeldBranch(ctx, call); err != nil {
		tools.record(ctx, *call, github.RateLimit{}, err)
		return err
	}
	if err := tools.refuseGoldenMetrics(ctx, call); err != nil {
		tools.record(ctx, *call, github.RateLimit{}, err)
		return err
	}
	if !policy.RequireChecks {
		return nil
	}
	if err := tools.requirePassingChecks(ctx, call.owner, call.repo, call.number); err != nil {
		tools.record(ctx, *call, github.RateLimit{}, err)
		return err
	}
	return nil
}

// refuseHeldBranch refuses a merge whose head branch the guard reports held.
// The head branch is the pull request's, read from GitHub; a pull request
// that cannot be read fails the merge closed, so a hold never fails open.
func (tools *GitHubTools) refuseHeldBranch(ctx context.Context, call *toolCall) error {
	if tools.guard == nil {
		return nil
	}
	pull, err := tools.client.PullsgetWithResponse(ctx, call.owner, call.repo, call.number)
	if err != nil {
		return err
	}
	if pull.JSON200 == nil {
		return &github.StatusError{Status: pull.StatusCode(), Message: string(pull.Body)}
	}
	reason, held, err := tools.guard(ctx, pull.JSON200.Head.Ref)
	if err != nil {
		return err
	}
	if !held {
		return nil
	}
	return fmt.Errorf("%w for %s; push to it, do not merge it", ErrHeldBranch, reason)
}

// refuseGoldenMetrics refuses a merge the golden metrics refuse. The pull
// request's base revision is main and its head revision is the merge result;
// the gate compares the two and refuses a merge that lowers the seed files,
// breaks a directory main's seed held clean, raises the chief violations, or
// lowers the derived share (the fraction of the tree that is source or
// reproduced, measured by reproduction). It writes the verdict to
// golden_metrics.json either way, for the ops view panel, and refuses without
// writing nothing. A pull request or revision that cannot be read or measured
// fails the merge closed, so the gate never fails open.
func (tools *GitHubTools) refuseGoldenMetrics(ctx context.Context, call *toolCall) error {
	if tools.measurer == nil {
		return nil
	}
	pull, err := tools.client.PullsgetWithResponse(ctx, call.owner, call.repo, call.number)
	if err != nil {
		return err
	}
	if pull.JSON200 == nil {
		return &github.StatusError{Status: pull.StatusCode(), Message: string(pull.Body)}
	}
	main, err := tools.measurer(ctx, call.owner, call.repo, pull.JSON200.Base.Sha)
	if err != nil {
		return err
	}
	merge, err := tools.measurer(ctx, call.owner, call.repo, pull.JSON200.Head.Sha)
	if err != nil {
		return err
	}
	snapshot := prod.SnapshotOf(pull.JSON200.Base.Sha, pull.JSON200.Head.Sha, main, merge)
	if err := tools.writeGoldenSnapshot(snapshot); err != nil {
		return err
	}
	if len(snapshot.Reasons) == 0 {
		return nil
	}
	reasons := make([]string, 0, len(snapshot.Reasons))
	for _, reason := range snapshot.Reasons {
		reasons = append(reasons, string(reason))
	}
	return fmt.Errorf("%w: %s", ErrGoldenMetrics, strings.Join(reasons, ", "))
}

// writeGoldenSnapshot writes the merge quality gate's verdict, whole, for the
// ops view panel to read.
func (tools *GitHubTools) writeGoldenSnapshot(snapshot prod.Snapshot) error {
	content, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(filepath.Join(tools.state, prod.FileName), content, logFileMode)
}

func (tools *GitHubTools) requirePassingChecks(ctx context.Context, owner string, repo string, number int) error {
	pull, err := tools.client.PullsgetWithResponse(ctx, owner, repo, number)
	if err != nil {
		return err
	}
	if pull.JSON200 == nil {
		return &github.StatusError{Status: pull.StatusCode(), Message: string(pull.Body)}
	}
	checks, err := tools.client.CheckslistForRefWithResponse(ctx, owner, repo, pull.JSON200.Head.Sha, nil)
	if err != nil {
		return err
	}
	if checks.JSON200 == nil {
		return &github.StatusError{Status: checks.StatusCode(), Message: string(checks.Body)}
	}
	for _, run := range checks.JSON200.CheckRuns {
		if run.Conclusion == nil || !passing[*run.Conclusion] {
			return fmt.Errorf("%w: %s is %s", ErrChecksRequired, run.Name, run.Status)
		}
	}
	return nil
}

// passing are the check conclusions a merge that requires checks accepts.
var passing = map[github.CheckRunConclusion]bool{
	github.CheckRunConclusionSuccess: true, github.CheckRunConclusionNeutral: true, github.CheckRunConclusionSkipped: true,
}

// finish turns a generated call's outcome into the tool's: GitHub's own
// response body, which the tool's output schema validates, or the failure,
// recorded either way. succeeded is whether the response had the status the
// operation succeeds with.
func finish(ctx context.Context, tools *GitHubTools, call toolCall, response *http.Response, body []byte, succeeded bool, err error) (json.RawMessage, error) {
	if err == nil && !succeeded {
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		err = &github.StatusError{Status: status, Message: strings.TrimSpace(string(body))}
	}
	tools.record(ctx, call, github.RateLimitOf(response), err)
	if err != nil {
		return nil, classify(err)
	}
	return body, nil
}

// ListOutput is a list GitHub answered with, as the object an MCP result is.
type ListOutput struct {
	Items json.RawMessage `json:"items"`
}

func listed(body json.RawMessage, err error) (ListOutput, error) {
	if err != nil {
		return ListOutput{}, err
	}
	return ListOutput{Items: body}, nil
}

// classify maps GitHub's refusals onto CSF's transport classifications.
func classify(err error) error {
	var status *github.StatusError
	if !errors.As(err, &status) {
		return err
	}
	switch status.Status {
	case http.StatusNotFound:
		return fmt.Errorf("%w: %w", csf.ErrNotFound, err)
	case http.StatusConflict:
		return fmt.Errorf("%w: %w", csf.ErrConflict, err)
	case http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusMethodNotAllowed:
		return fmt.Errorf("%w: %w", csf.ErrInvalidRequest, err)
	}
	return err
}

// record appends the call to the host's GitHub log and, for a virtual
// session, to its run's event log. A record that cannot be written is
// logged; it never changes the call's outcome.
func (tools *GitHubTools) record(ctx context.Context, call toolCall, rate github.RateLimit, failure error) {
	record := CallRecord{
		Time: tools.clock.Now().UTC(), Operation: call.operation, Actor: call.actor, Assignment: call.assignment,
		Repository: call.owner + "/" + call.repo, Number: call.number, Outcome: OutcomeOK,
		RateLimit: rate.Limit, RateRemaining: rate.Remaining, Policy: call.policy,
	}
	if failure != nil {
		record.Outcome, record.Error = OutcomeFailed, failure.Error()
	}
	if err := tools.appendLog(record); err != nil {
		tools.logger.Warn("github tools: call not recorded", "operation", record.Operation, "error", err)
	}
	if record.Actor == ActorVirtualSession {
		tools.recordOnSession(ctx, record)
	}
}

func (tools *GitHubTools) recordOnSession(ctx context.Context, record CallRecord) {
	directory := session.RunDirectory(tools.state, record.Assignment)
	state, err := session.ReadRunState(directory)
	if err != nil {
		return
	}
	log, err := session.OpenEventLog(directory, session.RunTrace(state))
	if err != nil {
		tools.logger.Warn("github tools: call not recorded on the session", "assignment", record.Assignment, "error", err)
		return
	}
	defer func() { _ = log.Close() }()
	attributes := []slog.Attr{
		slog.String(session.KeyAction, record.Operation), slog.String(keyRepository, record.Repository),
		slog.Int(keyNumber, record.Number), slog.String(keyOutcome, record.Outcome),
	}
	message := "github call: " + record.Operation
	if record.Error != "" {
		log.Failure(ctx, state.SessionID, state.Turns, session.EventTypeGitHubCall, message, errors.New(record.Error), attributes...)
		return
	}
	log.Record(ctx, state.SessionID, state.Turns, session.EventTypeGitHubCall, message, attributes...)
}

// appendLog writes one line to the host's GitHub log, whole.
func (tools *GitHubTools) appendLog(record CallRecord) error {
	line, err := json.Marshal(record)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(tools.state, LogFile), logFlags, logFileMode)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(line, '\n')); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

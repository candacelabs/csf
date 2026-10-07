// Copyright 2026 Candace Labs

// Package await is CSF's wait primitive: one typed operation that waits for
// a condition from a closed set, up to a required deadline, and reports what
// it saw. The CSF service serves it over MCP and HTTP, csf await is its thin
// client, and csf serve -detach and csf stop return through it.
//
// Every wait polls through [eventually.Until] on the granted clock, so a
// spec moves time itself. A condition reads only the capabilities the
// binary grants; a condition whose capability is missing is refused, never
// waited on.
package await

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	iofs "github.com/candacelabs/csf/io/kernel/fs"
	iohttp "github.com/candacelabs/csf/io/net/http"
	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/pkg/eventually"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
)

// Condition names one thing a wait can wait for. The set is closed.
type Condition string

// The conditions, as csf await and the operation spell them.
const (
	// ConditionHarnessReady holds once the host has written its record: it
	// listens, its database is healthy and its open runs are resumed.
	ConditionHarnessReady Condition = "harness-ready"
	// ConditionHarnessStopped holds once the host process has exited.
	ConditionHarnessStopped Condition = "harness-stopped"
	// ConditionSessionPhase holds once a session is in the named phase.
	ConditionSessionPhase Condition = "session-phase"
	// ConditionTurnFinished holds once a session has no turn running and none
	// queued, at or after the named turn.
	ConditionTurnFinished Condition = "turn-finished"
	// ConditionPullRequestMerged holds once a pull request is merged.
	ConditionPullRequestMerged Condition = "pull-request-merged"
	// ConditionURLStatus holds once a URL answers GET with the named status.
	ConditionURLStatus Condition = "url-status"
	// ConditionLoadBelow holds once the one-minute load average is below the
	// named level.
	ConditionLoadBelow Condition = "load-below"
)

// Outcome is how a wait ended.
type Outcome string

const (
	// OutcomeMet is a condition that held before the deadline.
	OutcomeMet Outcome = "met"
	// OutcomeDeadline is a deadline reached with the condition not held.
	OutcomeDeadline Outcome = "deadline"
	// OutcomeUnreachable is a condition that can no longer hold: the process
	// awaited exited, the session ended in another phase, the pull request
	// was closed.
	OutcomeUnreachable Outcome = "unreachable"
)

// The refusals: each is a misuse the caller fixes, wrapped with what was
// wrong.
var (
	ErrUnknownCondition = errors.New("await: unknown condition")
	ErrNoDeadline       = errors.New("await: a positive deadline is required")
	ErrMissingOperand   = errors.New("await: the condition is missing an operand")
	ErrUnavailable      = errors.New("await: this host grants no capability for the condition")
	ErrInvalidOption    = errors.New("await: invalid option")
)

// Request is one wait: the condition, its deadline and the operands the
// condition reads. Only the operands of the named condition are read.
type Request struct {
	Condition Condition `json:"condition" jsonschema:"one of harness-ready, harness-stopped, session-phase, turn-finished, pull-request-merged, url-status, load-below"`
	Deadline  string    `json:"deadline" jsonschema:"how long to wait at most, a Go duration such as 90s or 10m; required"`

	// PID names the host process for harness-ready and harness-stopped;
	// zero means the one the host record names.
	PID int `json:"pid,omitempty" jsonschema:"harness-ready, harness-stopped: the host process; default the one the host record names"`
	// Assignment names the session for session-phase and turn-finished.
	Assignment string `json:"assignment,omitempty" jsonschema:"session-phase, turn-finished: the session's assignment identifier"`
	// Phase is the session phase awaited, such as OPEN or RUNNING.
	Phase string `json:"phase,omitempty" jsonschema:"session-phase: STARTING, RUNNING, OPEN, CANCELING, CANCELED, FAILED or CLOSED"`
	// Turn is the turn that must have finished; zero means the latest.
	Turn uint32 `json:"turn,omitempty" jsonschema:"turn-finished: the turn that must have finished; default the latest"`
	// PullRequest is the pull request's URL, or its number with Repository.
	PullRequest string `json:"pull_request,omitempty" jsonschema:"pull-request-merged: the pull request's URL, or its number with repository"`
	Repository  string `json:"repository,omitempty" jsonschema:"pull-request-merged: OWNER/NAME when pull_request is a number"`
	// URL and Status name the GET and the status it must answer.
	URL    string `json:"url,omitempty" jsonschema:"url-status: the URL to GET"`
	Status int    `json:"status,omitempty" jsonschema:"url-status: the HTTP status awaited; default 200"`
	// Load is the level the one-minute load average must fall below.
	Load float64 `json:"load,omitempty" jsonschema:"load-below: the one-minute load average must fall below this"`
}

// Result is what a wait reports: the condition, how it ended, how long it
// took and the last thing it observed.
type Result struct {
	Condition   Condition `json:"condition"`
	Outcome     Outcome   `json:"outcome"`
	ElapsedMS   int64     `json:"elapsed_ms"`
	Polls       int       `json:"polls"`
	Observation string    `json:"observation"`
}

// Met is whether the condition held.
func (result Result) Met() bool { return result.Outcome == OutcomeMet }

// observation is one poll's reading of a condition.
type observation struct {
	met bool
	// final is a reading after which the condition can no longer hold.
	final bool
	text  string
}

// interval is how often every condition is polled. A wait is for a process,
// a session or a remote service to change state, which takes seconds; a
// second keeps a wait's latency under its granularity while costing one
// read a second.
const interval = time.Second

// condition is one entry of the closed set: the operands it requires, and
// the poll it builds from a valid request.
type condition struct {
	validate func(request Request) error
	poll     func(awaiter *Awaiter, request Request) (func(ctx context.Context) observation, error)
}

// conditions is the closed set, in the order csf await lists it.
var conditions = map[Condition]condition{
	ConditionHarnessReady:      {validate: noOperands, poll: (*Awaiter).harnessReady},
	ConditionHarnessStopped:    {validate: noOperands, poll: (*Awaiter).harnessStopped},
	ConditionSessionPhase:      {validate: needsAssignmentAndPhase, poll: (*Awaiter).sessionPhase},
	ConditionTurnFinished:      {validate: needsAssignment, poll: (*Awaiter).turnFinished},
	ConditionPullRequestMerged: {validate: needsPullRequest, poll: (*Awaiter).pullRequestMerged},
	ConditionURLStatus:         {validate: needsURL, poll: (*Awaiter).urlStatus},
	ConditionLoadBelow:         {validate: needsLoad, poll: (*Awaiter).loadBelow},
}

// Conditions lists the closed set of conditions, sorted.
func Conditions() []Condition {
	names := make([]Condition, 0, len(conditions))
	for name := range conditions {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// SessionState reads one session's state by its assignment identifier.
type SessionState func(ctx context.Context, assignment string) (*harnessv1.AgentSessionState, error)

// Awaiter waits for conditions with the capabilities its binary granted.
type Awaiter struct {
	clock     eventually.IClock
	state     iofs.IFiles
	processes iofs.IFiles
	sessions  SessionState
	launcher  proc.ILauncher
	client    iohttp.IHTTPClient
}

// Option grants an [Awaiter] one capability.
type Option func(awaiter *Awaiter) error

// WithClock grants the clock every deadline is read on. It is required.
func WithClock(clock eventually.IClock) Option {
	return func(awaiter *Awaiter) error {
		if clock == nil {
			return fmt.Errorf("%w: nil clock", ErrInvalidOption)
		}
		awaiter.clock = clock
		return nil
	}
}

// WithHostState grants the host's state directory, which holds the host
// record harness-ready and harness-stopped read.
func WithHostState(state iofs.IFiles) Option {
	return func(awaiter *Awaiter) error {
		if state == nil {
			return fmt.Errorf("%w: nil state directory", ErrInvalidOption)
		}
		awaiter.state = state
		return nil
	}
}

// WithProcessTable grants the process table, as /proc: whether a process
// runs, and the load average.
func WithProcessTable(processes iofs.IFiles) Option {
	return func(awaiter *Awaiter) error {
		if processes == nil {
			return fmt.Errorf("%w: nil process table", ErrInvalidOption)
		}
		awaiter.processes = processes
		return nil
	}
}

// WithSessionState grants the read of a session's state.
func WithSessionState(sessions SessionState) Option {
	return func(awaiter *Awaiter) error {
		if sessions == nil {
			return fmt.Errorf("%w: nil session state", ErrInvalidOption)
		}
		awaiter.sessions = sessions
		return nil
	}
}

// WithLauncher grants the launcher gh runs through for pull-request-merged.
func WithLauncher(launcher proc.ILauncher) Option {
	return func(awaiter *Awaiter) error {
		if launcher == nil {
			return fmt.Errorf("%w: nil launcher", ErrInvalidOption)
		}
		awaiter.launcher = launcher
		return nil
	}
}

// WithHTTPClient grants the client url-status sends its GET through.
func WithHTTPClient(client iohttp.IHTTPClient) Option {
	return func(awaiter *Awaiter) error {
		if client == nil {
			return fmt.Errorf("%w: nil HTTP client", ErrInvalidOption)
		}
		awaiter.client = client
		return nil
	}
}

// NewAwaiter validates the whole option set before building the awaiter.
func NewAwaiter(options ...Option) (*Awaiter, error) {
	awaiter := &Awaiter{}
	for _, option := range options {
		if err := option(awaiter); err != nil {
			return nil, err
		}
	}
	if awaiter.clock == nil {
		return nil, fmt.Errorf("%w: a clock is required", ErrInvalidOption)
	}
	return awaiter, nil
}

// Validate refuses a request that names no known condition, no positive
// deadline or not every operand its condition reads, and returns the
// deadline.
func Validate(request Request) (time.Duration, error) {
	entry, known := conditions[request.Condition]
	if !known {
		return 0, fmt.Errorf("%w %q: one of %s", ErrUnknownCondition, request.Condition, conditionList())
	}
	if request.Deadline == "" {
		return 0, fmt.Errorf("%w: give one such as 90s or 10m", ErrNoDeadline)
	}
	deadline, err := time.ParseDuration(request.Deadline)
	if err != nil || deadline <= 0 {
		return 0, fmt.Errorf("%w: %q is not a positive duration such as 90s or 10m", ErrNoDeadline, request.Deadline)
	}
	if err := entry.validate(request); err != nil {
		return 0, fmt.Errorf("%w: %s %w", ErrMissingOperand, request.Condition, err)
	}
	return deadline, nil
}

// Await waits for the request's condition until its deadline and reports
// the outcome. Only a misuse is an error; a deadline reached is a result.
func (awaiter *Awaiter) Await(ctx context.Context, request Request) (Result, error) {
	deadline, err := Validate(request)
	if err != nil {
		return Result{}, err
	}
	poll, err := conditions[request.Condition].poll(awaiter, request)
	if err != nil {
		return Result{}, err
	}
	outcome, err := eventually.Until(ctx, awaiter.clock, eventually.Budget{Within: deadline, Interval: interval}, poll,
		func(seen observation) bool { return seen.met },
		func(seen observation) bool { return seen.final })
	result := Result{
		Condition:   request.Condition,
		Outcome:     OutcomeDeadline,
		ElapsedMS:   outcome.Elapsed.Milliseconds(),
		Polls:       outcome.Polls,
		Observation: outcome.Last.text,
	}
	switch {
	case outcome.Met:
		result.Outcome = OutcomeMet
	case outcome.Final:
		result.Outcome = OutcomeUnreachable
	}
	return result, err
}

func conditionList() string {
	names := make([]string, 0, len(conditions))
	for _, name := range Conditions() {
		names = append(names, string(name))
	}
	return strings.Join(names, ", ")
}

func noOperands(request Request) error { return nil }

func needsAssignment(request Request) error {
	if request.Assignment == "" {
		return errors.New("needs assignment")
	}
	return nil
}

func needsAssignmentAndPhase(request Request) error {
	if err := needsAssignment(request); err != nil {
		return err
	}
	if _, known := phaseValue(request.Phase); !known {
		return fmt.Errorf("needs a phase, one of %s", strings.Join(phaseNames(), ", "))
	}
	return nil
}

func needsPullRequest(request Request) error {
	if request.PullRequest == "" {
		return errors.New("needs pull_request")
	}
	return nil
}

func needsURL(request Request) error {
	if request.URL == "" {
		return errors.New("needs url")
	}
	return nil
}

func needsLoad(request Request) error {
	if request.Load <= 0 {
		return errors.New("needs a positive load")
	}
	return nil
}

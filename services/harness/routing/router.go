// Copyright 2026 Candace Labs

// Package routing maps virtual sessions onto real sessions. A virtual session
// is a CSF conversation: one assignment the harness runs. A real session is a
// Claude Code conversation, which carries the server-side prompt cache; Claude
// Code keeps doing its own compaction and caching.
//
// The first router has two arms. A virtual session attaches to an existing
// real session it fits well in, and resumes that conversation; otherwise it
// opens a new one. It fits when the ticket (the topic key) is the same, or
// else when the pull request titles overlap at least the threshold; a warm
// conversation, used within the cache lifetime, is preferred. Only a
// conversation whose holder has closed is attached, so no two processes ever
// write one conversation. Every decision is appended to
// <state>/routing/decisions.jsonl so it can be compared with the cache use the
// endpoint reported, and mined.
package routing

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/candacelabs/csf/services/harness/session"
)

const (
	// Directory is the router's directory under the harness state directory.
	Directory = "routing"
	// DecisionsFile holds one decision per line.
	DecisionsFile = "decisions.jsonl"
	// RealSessionsFile holds the real-session table, keyed by conversation.
	RealSessionsFile = "real_sessions.json"
	// DefaultTTL is how long a conversation counts as warm. Claude Code
	// writes its prefix with a one-hour cache lifetime (usage.cache_creation
	// reports ephemeral_1h_input_tokens); the margin keeps an attach from
	// landing on an expired entry.
	DefaultTTL = 50 * time.Minute
	// DefaultThreshold is the title overlap at which a virtual session fits
	// a conversation with another ticket. It is v1's starting value; every
	// decision logs its score so the miner can measure where it belongs.
	DefaultThreshold = 0.5

	fileMode      = 0o600
	directoryMode = 0o700
)

// Arm is what a decision does with a virtual session.
type Arm string

const (
	// ArmAttach resumes an existing real session.
	ArmAttach Arm = "attach"
	// ArmNew opens a new real session.
	ArmNew Arm = "new"
)

// Reason says why a decision took its arm.
type Reason string

const (
	// ReasonKey: a closed conversation on the same ticket.
	ReasonKey Reason = "key"
	// ReasonSummary: a closed conversation whose title overlaps enough.
	ReasonSummary Reason = "summary"
	// ReasonNoFit: no closed conversation fits.
	ReasonNoFit Reason = "no-fit"
)

var (
	// ErrInvalidOption reports a nil or out-of-range option.
	ErrInvalidOption = errors.New("harness routing: invalid option")
	// ErrNoStateDirectory reports a router built without an absolute state
	// directory.
	ErrNoStateDirectory = errors.New("harness routing: an absolute state directory is required")
)

// Decision is one routing decision, as logged.
type Decision struct {
	Time    time.Time `json:"time"`
	Virtual string    `json:"v"`
	Real    string    `json:"r"`
	Arm     Arm       `json:"arm"`
	Reason  Reason    `json:"reason"`
	// Score is the fit: 1 for the same ticket, else the title overlap.
	Score float64 `json:"s"`
	// PredictedHit is the attached conversation's warmth, 1 - age/TTL, and
	// 0 for a new one: whether its prefix should still be cached.
	PredictedHit float64 `json:"predicted_hit"`
}

// RealSession is one conversation in the table.
type RealSession struct {
	ID       string    `json:"id"`
	Model    string    `json:"model"`
	Agent    string    `json:"agent"`
	Key      string    `json:"key"`
	Summary  string    `json:"summary"`
	Holder   string    `json:"holder"`
	LastUsed time.Time `json:"last_used"`
}

// Router decides which real session a virtual session runs on.
type Router struct {
	state     string
	ttl       time.Duration
	threshold float64
	now       func() time.Time
	// turn serializes decisions: the table is one file that concurrent
	// opens read and rewrite, and the token passed through this channel is
	// held by exactly one decision at a time.
	turn chan struct{}
}

// RouterOption configures a [Router].
type RouterOption func(router *Router) error

// WithStateDirectory names the harness state directory. Required.
func WithStateDirectory(directory string) RouterOption {
	return func(router *Router) error {
		if !filepath.IsAbs(directory) {
			return ErrNoStateDirectory
		}
		router.state = directory
		return nil
	}
}

// WithTTL sets how long a conversation counts as warm.
func WithTTL(ttl time.Duration) RouterOption {
	return func(router *Router) error {
		if ttl <= 0 {
			return fmt.Errorf("%w: TTL must be positive, got %s", ErrInvalidOption, ttl)
		}
		router.ttl = ttl
		return nil
	}
}

// WithThreshold sets the title overlap at which a virtual session fits.
func WithThreshold(threshold float64) RouterOption {
	return func(router *Router) error {
		if threshold <= 0 || threshold > 1 {
			return fmt.Errorf("%w: threshold must be in (0, 1], got %v", ErrInvalidOption, threshold)
		}
		router.threshold = threshold
		return nil
	}
}

// WithClock sets the clock decisions are timed by.
func WithClock(now func() time.Time) RouterOption {
	return func(router *Router) error {
		if now == nil {
			return fmt.Errorf("%w: nil clock", ErrInvalidOption)
		}
		router.now = now
		return nil
	}
}

// NewRouter builds a router. The state directory is required.
func NewRouter(options ...RouterOption) (*Router, error) {
	router := &Router{ttl: DefaultTTL, threshold: DefaultThreshold, now: time.Now, turn: make(chan struct{}, 1)}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("%w: nil option", ErrInvalidOption)
		}
		if err := option(router); err != nil {
			return nil, err
		}
	}
	if router.state == "" {
		return nil, ErrNoStateDirectory
	}
	return router, nil
}

var _ session.IRouter = (*Router)(nil)

// Route decides the virtual session's real session, records the decision
// and the table, and returns the conversation to run on: an existing one to
// resume, or request.Session.
func (router *Router) Route(ctx context.Context, request session.RouteRequest) (string, error) {
	select {
	case router.turn <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-router.turn }()
	table, err := router.readTable()
	if err != nil {
		return "", err
	}
	now := router.now()
	decision := router.decide(table, request, now)
	chosen := table[decision.Real]
	if decision.Arm == ArmNew {
		chosen = RealSession{ID: request.Session, Model: request.Model, Agent: request.Agent, Key: request.Key, Summary: request.Summary}
	}
	chosen.Holder, chosen.LastUsed = request.Virtual, now
	table[chosen.ID] = chosen
	if err := router.writeTable(table); err != nil {
		return "", err
	}
	if err := router.record(decision); err != nil {
		return "", err
	}
	return decision.Real, nil
}

// candidate is a conversation that fits, with its score and warmth.
type candidate struct {
	real   RealSession
	reason Reason
	score  float64
	warmth float64
}

func (router *Router) decide(table map[string]RealSession, request session.RouteRequest, now time.Time) Decision {
	words := titleWords(request.Summary)
	candidates := []candidate{}
	for _, real := range table {
		// A resumed conversation keeps its first system prompt: only the same
		// model and the same agent identity may attach.
		if real.Model != request.Model || real.Agent != request.Agent || !router.closed(real.Holder) {
			continue
		}
		fit := candidate{real: real, reason: ReasonKey, score: 1, warmth: max(0, 1-float64(now.Sub(real.LastUsed))/float64(router.ttl))}
		if real.Key != request.Key || request.Key == "" {
			fit.reason, fit.score = ReasonSummary, overlap(words, titleWords(real.Summary))
		}
		if fit.score >= router.threshold {
			candidates = append(candidates, fit)
		}
	}
	if len(candidates) == 0 {
		return Decision{Time: now, Virtual: request.Virtual, Real: request.Session, Arm: ArmNew, Reason: ReasonNoFit}
	}
	// Warm first, then the better fit, then the most recently used.
	best := slices.MaxFunc(candidates, func(left candidate, right candidate) int {
		switch {
		case (left.warmth > 0) != (right.warmth > 0):
			return compareBool(left.warmth > 0, right.warmth > 0)
		case left.score != right.score:
			return compareFloat(left.score, right.score)
		}
		return left.real.LastUsed.Compare(right.real.LastUsed)
	})
	return Decision{Time: now, Virtual: request.Virtual, Real: best.real.ID, Arm: ArmAttach, Reason: best.reason,
		Score: best.score, PredictedHit: best.warmth}
}

// closed reports whether the holder's session has closed its turn executor
// and not been reopened since, so its conversation has no writer: its last
// lifecycle record (started, resumed or closed) is a close. A harness restart
// closes every session and then resumes it, so an earlier close says nothing.
func (router *Router) closed(holder string) bool {
	file, err := os.Open(filepath.Join(router.state, holder, session.EventsFile))
	if err != nil {
		return false
	}
	defer func() { _ = file.Close() }()
	marker := func(kind string) []byte { return []byte(`"` + session.KeyEventType + `":"` + kind + `"`) }
	closedMarker := marker(session.EventTypeSessionClosed)
	startedMarker := marker(session.EventTypeRunStarted)
	resumedMarker := marker(session.EventTypeRunResumed)
	last := false
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1<<16), maxRecordBytes)
	for scanner.Scan() {
		line := scanner.Bytes()
		switch {
		case bytes.Contains(line, closedMarker):
			last = true
		case bytes.Contains(line, startedMarker), bytes.Contains(line, resumedMarker):
			last = false
		}
	}
	return last
}

// titleWords is a title's distinct lower-case words.
func titleWords(title string) map[string]bool {
	words := map[string]bool{}
	for _, word := range strings.FieldsFunc(strings.ToLower(title), func(character rune) bool {
		return !(character >= 'a' && character <= 'z' || character >= '0' && character <= '9')
	}) {
		words[word] = true
	}
	return words
}

// overlap is the Jaccard index of two word sets.
func overlap(left map[string]bool, right map[string]bool) float64 {
	shared := 0
	for word := range left {
		if right[word] {
			shared++
		}
	}
	union := len(left) + len(right) - shared
	if union == 0 {
		return 0
	}
	return float64(shared) / float64(union)
}

func compareBool(left bool, right bool) int {
	if left == right {
		return 0
	}
	if left {
		return 1
	}
	return -1
}

func compareFloat(left float64, right float64) int {
	switch {
	case left > right:
		return 1
	case left < right:
		return -1
	}
	return 0
}

func (router *Router) readTable() (map[string]RealSession, error) {
	content, err := os.ReadFile(filepath.Join(router.state, Directory, RealSessionsFile))
	table := map[string]RealSession{}
	if errors.Is(err, os.ErrNotExist) {
		return table, nil
	}
	if err != nil {
		return nil, fmt.Errorf("harness routing: read real sessions: %w", err)
	}
	if err := json.Unmarshal(content, &table); err != nil {
		return nil, fmt.Errorf("harness routing: decode real sessions: %w", err)
	}
	return table, nil
}

// writeTable replaces the table through a temporary file, so a reader never
// sees half of it.
func (router *Router) writeTable(table map[string]RealSession) error {
	directory := filepath.Join(router.state, Directory)
	if err := os.MkdirAll(directory, directoryMode); err != nil {
		return fmt.Errorf("harness routing: %w", err)
	}
	content, err := json.MarshalIndent(table, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(directory, RealSessionsFile)
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, content, fileMode); err != nil {
		return fmt.Errorf("harness routing: write real sessions: %w", err)
	}
	return os.Rename(temporary, path)
}

func (router *Router) record(decision Decision) error {
	content, err := json.Marshal(decision)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(router.state, Directory, DecisionsFile), os.O_CREATE|os.O_APPEND|os.O_WRONLY, fileMode)
	if err != nil {
		return fmt.Errorf("harness routing: open decisions: %w", err)
	}
	_, err = file.Write(append(content, '\n'))
	return errors.Join(err, file.Close())
}

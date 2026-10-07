// Copyright 2026 Candace Labs

package codes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/candacelabs/csf/io/net/model"
	"github.com/candacelabs/csf/io/net/model/ollama"
	"github.com/candacelabs/csf/pkg/textbound"
)

// CodeNone is the code a model answers when no catalogue code fits; the
// answer then carries its proposal for a new code.
const CodeNone = "none"

// DefaultBatch is how many complaints one model request codes. The window
// is not the bound: qwen3:8b answers fewer complaints than a batch holds as
// the catalogue grows. Measured 2026-10-05 on the 28 complaints of the night
// before, 16k window, no thinking: with the 24-code catalogue a batch of 24
// left 8 of the 16 corrections unanswered and a batch of 8 left 4; with the
// 14-code seed a batch of 8 left 1 of 28 complaints rejected.
const DefaultBatch = 8

// The bounds on what one complaint puts into the prompt, in bytes: the
// operator's message and the agent's reply it answers. A complaint is short
// (half the operator's messages are under 100 bytes); the bounds stop a
// pasted log from filling the window.
const (
	textBytes    = 1200
	contextBytes = 400
)

// The reasons an answer is not kept.
const (
	RejectedNoQuote     = "quote not in the complaint"
	RejectedUnknownCode = "code not in the catalogue"
	RejectedUnanswered  = "the model gave no answer for the complaint"
)

var (
	// ErrNoBrain reports a coder built without the model.
	ErrNoBrain = errors.New("failure codes: a brain is required")
	// ErrInvalidOption reports a nil option or a value the coder cannot use.
	ErrInvalidOption = errors.New("failure codes: invalid option")
)

// Complaint is one operator correction to code: where it came from, what the
// operator wrote and what the agent had said just before.
type Complaint struct {
	// Source names the corpus item, such as a transcript's session.
	Source string
	// Ref identifies the complaint within its source.
	Ref  string
	At   time.Time
	Text string
	// Context is the agent's reply the complaint answers; empty when none
	// was recorded.
	Context string
	// Correction is pkg/affect reading the message as an operator
	// correction; a message read only because a reference labels it is not.
	Correction bool
}

// Proposal is a model's proposal for a new code, for a complaint no
// catalogue code fits.
type Proposal struct {
	Name       string `json:"name"`
	Definition string `json:"definition"`
	Edge       Edge   `json:"edge"`
	Side       Side   `json:"side"`
}

// Coded is one complaint and the code it was given.
type Coded struct {
	Complaint Complaint
	// Code is the catalogue code kept; empty when the answer was rejected or
	// proposes a new code.
	Code string
	// Quote is the model's verbatim evidence from the complaint.
	Quote string
	// Proposal is the new code proposed when no catalogue code fits.
	Proposal *Proposal
	// Rejected is why the answer was not kept; empty when it was.
	Rejected string
}

// ComplaintCoder codes complaints with the catalogue through a local model.
// It holds no state between calls.
type ComplaintCoder struct {
	brain     model.IBrain[*ollama.Prompt, ollama.Answer]
	catalogue []Code
	batch     int
	keepAlive time.Duration
}

// ComplaintCoderOption configures a [ComplaintCoder].
type ComplaintCoderOption func(coder *ComplaintCoder) error

// WithBrain grants the model, behind the brain contract.
func WithBrain(brain model.IBrain[*ollama.Prompt, ollama.Answer]) ComplaintCoderOption {
	return func(coder *ComplaintCoder) error {
		coder.brain = brain
		return nil
	}
}

// WithCatalogue codes with catalogue instead of [Catalogue].
func WithCatalogue(catalogue []Code) ComplaintCoderOption {
	return func(coder *ComplaintCoder) error {
		if len(catalogue) == 0 {
			return fmt.Errorf("%w: empty catalogue", ErrInvalidOption)
		}
		coder.catalogue = catalogue
		return nil
	}
}

// WithBatch codes size complaints per model request instead of
// [DefaultBatch].
func WithBatch(size int) ComplaintCoderOption {
	return func(coder *ComplaintCoder) error {
		if size <= 0 {
			return fmt.Errorf("%w: batch must be positive, got %d", ErrInvalidOption, size)
		}
		coder.batch = size
		return nil
	}
}

// WithKeepAlive asks the model server to keep the model loaded for duration
// after each answer; the server's default applies otherwise.
func WithKeepAlive(duration time.Duration) ComplaintCoderOption {
	return func(coder *ComplaintCoder) error {
		coder.keepAlive = duration
		return nil
	}
}

// NewComplaintCoder builds the coder; the brain is required.
func NewComplaintCoder(options ...ComplaintCoderOption) (*ComplaintCoder, error) {
	coder := &ComplaintCoder{catalogue: Catalogue, batch: DefaultBatch}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("%w: nil option", ErrInvalidOption)
		}
		if err := option(coder); err != nil {
			return nil, err
		}
	}
	if coder.brain == nil {
		return nil, ErrNoBrain
	}
	return coder, nil
}

// Code codes every complaint, in order, one batch per model request. A
// failed request ends the run with the complaints coded so far.
func (coder *ComplaintCoder) Code(ctx context.Context, complaints []Complaint) ([]Coded, error) {
	coded := make([]Coded, 0, len(complaints))
	for start := 0; start < len(complaints); start += coder.batch {
		batch := complaints[start:min(start+coder.batch, len(complaints))]
		answered, err := coder.ask(ctx, batch)
		if err != nil {
			return coded, err
		}
		coded = append(coded, answered...)
	}
	return coded, nil
}

// answer is one complaint's coding as the schema constrains the model to
// write it.
type answer struct {
	Index      int    `json:"index"`
	Code       string `json:"code"`
	Quote      string `json:"quote"`
	Name       string `json:"name"`
	Definition string `json:"definition"`
	EdgeFrom   string `json:"edge_from"`
	EdgeTo     string `json:"edge_to"`
	Side       Side   `json:"side"`
}

func (coder *ComplaintCoder) ask(ctx context.Context, batch []Complaint) ([]Coded, error) {
	schema, err := coder.schema()
	if err != nil {
		return nil, err
	}
	proposal, err := coder.brain.Propose(ctx, &ollama.Prompt{System: systemPrompt, User: coder.prompt(batch), Schema: schema, KeepAlive: coder.keepAlive})
	if err != nil {
		return nil, err
	}
	reply, err := proposal.Only()
	if err != nil {
		return nil, err
	}
	var decoded struct {
		Codes []answer `json:"codes"`
	}
	if err := json.Unmarshal([]byte(reply.Content), &decoded); err != nil {
		return nil, fmt.Errorf("failure codes: decode the model's answer: %w", err)
	}
	answers := attach(batch, decoded.Codes)
	coded := make([]Coded, 0, len(batch))
	for index, complaint := range batch {
		entry, found := answers[index]
		if !found {
			coded = append(coded, Coded{Complaint: complaint, Rejected: RejectedUnanswered})
			continue
		}
		coded = append(coded, coder.judge(complaint, entry))
	}
	return coded, nil
}

// attach gives each complaint of the batch, by position, at most one answer:
// the answer numbered for it when its quote is in it, otherwise the first
// unattached answer whose quote is in it, otherwise the one numbered for it.
// The quote decides because a model numbers its answers from 0 as readily as
// from the 1 the prompt shows, which shifts every answer onto its neighbour.
func attach(batch []Complaint, answers []answer) map[int]answer {
	attached := map[int]answer{}
	used := make([]bool, len(answers))
	claim := func(position int, accept func(entry answer) bool) {
		for index, entry := range answers {
			if _, done := attached[position]; done || used[index] || !accept(entry) {
				continue
			}
			attached[position], used[index] = entry, true
		}
	}
	for position, complaint := range batch {
		claim(position, func(entry answer) bool {
			return entry.Index == position+1 && quoted(complaint.Text, entry.Quote)
		})
	}
	for position, complaint := range batch {
		claim(position, func(entry answer) bool { return quoted(complaint.Text, entry.Quote) })
	}
	for position := range batch {
		claim(position, func(entry answer) bool { return entry.Index == position+1 })
	}
	return attached
}

// judge keeps an answer whose quote is in the complaint and whose code is in
// the catalogue, or whose proposal names a new code.
func (coder *ComplaintCoder) judge(complaint Complaint, entry answer) Coded {
	result := Coded{Complaint: complaint, Quote: entry.Quote}
	switch {
	case !quoted(complaint.Text, entry.Quote):
		result.Rejected = RejectedNoQuote
	case entry.Code == CodeNone:
		result.Proposal = &Proposal{Name: entry.Name, Definition: entry.Definition,
			Edge: Edge{From: entry.EdgeFrom, To: entry.EdgeTo}, Side: entry.Side}
	case !coder.known(entry.Code):
		result.Rejected = RejectedUnknownCode
	default:
		result.Code = entry.Code
	}
	return result
}

func (coder *ComplaintCoder) known(id string) bool {
	for _, code := range coder.catalogue {
		if code.ID == id {
			return true
		}
	}
	return false
}

// operatorLabel is the label a prompt puts before a complaint's words, which
// a model copies into its quote as often as not.
const operatorLabel = "operator:"

// quoted reports a nonblank quote that is in text, ignoring case, runs of
// whitespace and the prompt's own operator label copied in front of it.
func quoted(text string, quote string) bool {
	fold := func(value string) string { return strings.Join(strings.Fields(strings.ToLower(value)), " ") }
	needle := strings.TrimSpace(strings.TrimPrefix(fold(quote), operatorLabel))
	return needle != "" && strings.Contains(fold(text), needle)
}

const systemPrompt = `You code an operator's complaints about an AI agent harness with failure codes.
For each numbered complaint, choose the one catalogue code that names the failure the operator is complaining about: what went wrong, not the topic of the work.
Use a catalogue code only when its definition describes this failure; otherwise answer "none".
Copy a short quote, three to twelve words, verbatim from the complaint as the evidence.
When no catalogue code fits, answer code "none" and propose a new code: a short UPPER-KEBAB name for the class of failure, a one-sentence definition, the two components between which the failure started (edge_from, edge_to) and the side that owns the repair.
For a catalogue code, copy that code's edge and side.`

func (coder *ComplaintCoder) prompt(batch []Complaint) string {
	var builder strings.Builder
	builder.WriteString("Catalogue:\n")
	for _, code := range coder.catalogue {
		fmt.Fprintf(&builder, "- %s: %s. %s (edge %s, side %s)\n", code.ID, code.Name, code.Definition, code.Edge, code.Side)
	}
	builder.WriteString("\n" + componentsAndSides())
	builder.WriteString("\nComplaints:\n")
	for index, complaint := range batch {
		fmt.Fprintf(&builder, "[%d] %s\n", index+1, complaintLine(complaint))
	}
	return builder.String()
}

// componentsAndSides is the part of a prompt naming what an edge and a side
// may be.
func componentsAndSides() string {
	var builder strings.Builder
	builder.WriteString("Components:\n")
	for _, name := range componentNames() {
		fmt.Fprintf(&builder, "- %s: %s\n", name, Components[name])
	}
	builder.WriteString("\nSides:\n")
	for _, side := range Sides {
		fmt.Fprintf(&builder, "- %s: repaired by %s\n", side, side.Route())
	}
	return builder.String()
}

// complaintLine is one complaint as a prompt shows it: the operator's words
// and the agent's reply they answer, each bounded.
func complaintLine(complaint Complaint) string {
	line := operatorLabel + " " + textbound.Prefix(complaint.Text, textBytes)
	if complaint.Context != "" {
		line += "\n    replying to the agent: " + textbound.Prefix(complaint.Context, contextBytes)
	}
	return line
}

func (coder *ComplaintCoder) schema() (json.RawMessage, error) {
	ids := make([]string, 0, len(coder.catalogue)+1)
	for _, code := range coder.catalogue {
		ids = append(ids, code.ID)
	}
	ids = append(ids, CodeNone)
	components := componentNames()
	enum := func(values any) map[string]any { return map[string]any{"type": "string", "enum": values} }
	text := map[string]any{"type": "string"}
	item := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"index": map[string]any{"type": "integer"}, "code": enum(ids), "quote": text,
			"name": text, "definition": text, "edge_from": enum(components), "edge_to": enum(components), "side": enum(Sides),
		},
		"required": []string{"index", "code", "quote", "name", "definition", "edge_from", "edge_to", "side"},
	}
	return json.Marshal(map[string]any{
		"type":       "object",
		"properties": map[string]any{"codes": map[string]any{"type": "array", "items": item}},
		"required":   []string{"codes"},
	})
}

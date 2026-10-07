// Copyright 2026 Candace Labs

package codes

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/candacelabs/csf/io/net/model/ollama"
)

// Induced is one code the model induced from a set of complaints, with the
// complaints it covers: those holding one of its quotes verbatim. A code
// with no member is kept, so a run shows what the model proposed even when
// none of its quotes reproduces.
type Induced struct {
	Proposal Proposal `json:"proposal"`
	Members  []string `json:"members"`
	Quotes   []string `json:"quotes"`
}

// induction is the model's answer, as the schema constrains it.
type induction struct {
	Codes []struct {
		Name       string   `json:"name"`
		Definition string   `json:"definition"`
		EdgeFrom   string   `json:"edge_from"`
		EdgeTo     string   `json:"edge_to"`
		Side       Side     `json:"side"`
		Quotes     []string `json:"quotes"`
	} `json:"codes"`
}

const inductionPrompt = `You induce a failure taxonomy for an AI agent harness from an operator's complaints, as AdaMAST induces one from traces.
Read all the complaints together and propose the smallest set of failure codes that covers them: each code names a recurring class of failure (what went wrong), not the topic of the work or the operator's mood.
For each code give a short UPPER-KEBAB name, a one-sentence definition, the two components between which the failure starts, the side that owns the repair, and quotes copied verbatim from the complaints it covers, one quote per complaint.`

// Induce asks the model, in one request, for the codes that cover
// complaints, and reads each code's members: the complaints holding one of
// its quotes. A complaint may belong to more than one code.
func (coder *ComplaintCoder) Induce(ctx context.Context, complaints []Complaint) ([]Induced, error) {
	schema, err := inductionSchema()
	if err != nil {
		return nil, err
	}
	var builder strings.Builder
	builder.WriteString(componentsAndSides())
	builder.WriteString("\nComplaints:\n")
	for index, complaint := range complaints {
		fmt.Fprintf(&builder, "[%d] %s\n", index+1, complaintLine(complaint))
	}
	proposal, err := coder.brain.Propose(ctx, &ollama.Prompt{System: inductionPrompt, User: builder.String(), Schema: schema, KeepAlive: coder.keepAlive})
	if err != nil {
		return nil, err
	}
	reply, err := proposal.Only()
	if err != nil {
		return nil, err
	}
	var decoded induction
	if err := json.Unmarshal([]byte(reply.Content), &decoded); err != nil {
		return nil, fmt.Errorf("failure codes: decode the model's induction: %w", err)
	}
	induced := []Induced{}
	for _, code := range decoded.Codes {
		members := []string{}
		for _, complaint := range complaints {
			for _, quote := range code.Quotes {
				if quoted(complaint.Text, quote) {
					members = append(members, complaint.Ref)
					break
				}
			}
		}
		induced = append(induced, Induced{Members: members, Quotes: code.Quotes, Proposal: Proposal{Name: code.Name,
			Definition: code.Definition, Edge: Edge{From: code.EdgeFrom, To: code.EdgeTo}, Side: code.Side}})
	}
	return induced, nil
}

func inductionSchema() (json.RawMessage, error) {
	text := map[string]any{"type": "string"}
	// The schema is a JSON document, so its values are any.
	enum := func(values any) map[string]any { return map[string]any{"type": "string", "enum": values} }
	components := componentNames()
	item := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": text, "definition": text, "edge_from": enum(components), "edge_to": enum(components),
			"side": enum(Sides), "quotes": map[string]any{"type": "array", "items": text},
		},
		"required": []string{"name", "definition", "edge_from", "edge_to", "side", "quotes"},
	}
	return json.Marshal(map[string]any{
		"type":       "object",
		"properties": map[string]any{"codes": map[string]any{"type": "array", "items": item}},
		"required":   []string{"codes"},
	})
}

// Copyright 2026 Candace Labs

package labeler

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/candacelabs/csf/io/net/model/ollama"
	ouroborosv1 "github.com/candacelabs/csf/proto/candace/ouroboros/v1"
)

// The labels the model may answer with, as the ticket loop's tables spell
// them, and the prompt's section headings.
const (
	labelPositive = "+"
	labelNegative = "-"

	factsHeadingPrompt      = "\nFacts the miner would extract from the corpus:\n"
	candidatesHeadingPrompt = "\nCandidates:\n"
)

// systemPrompt is the model's standing instruction. It asks for a verbatim
// quote because the acceptance rule checks the quote against the line: a
// label whose quote is not on the line is rejected without a human.
const systemPrompt = `You label instances for a mining ticket about an AI coding-agent harness. The ticket states a predicate: a kind of offense or pattern in the harness's own records. You receive numbered candidates, each one real line from a harness event log, a ticket, a pull request or a commit message, with the instance it belongs to and the search terms that matched it. Label a candidate "+" only when the line itself shows the concrete evidence the predicate describes: the offending command, value or event, visible in the text. A line that merely shares vocabulary with the ticket, or is the kind of record one of the facts would be read from, is "-"; when in doubt, "-". For a "+", copy the exact characters from the candidate's text that show the evidence into "quote": verbatim, never paraphrased, never invented, and never across the mark … that shows where the text was cut. For a "-", quote the characters that decided it or leave the quote empty. Give a one-sentence "reason". Label every candidate exactly once and answer only with the JSON object the schema asks for.`

// answerSchema is the JSON schema the server constrains every answer to.
var answerSchema = json.RawMessage(`{"type":"object","properties":{"labels":{"type":"array","items":{"type":"object","properties":{"candidate":{"type":"integer"},"label":{"type":"string","enum":["+","-"]},"quote":{"type":"string"},"reason":{"type":"string"}},"required":["candidate","label","quote","reason"]}}},"required":["labels"]}`)

// answer is the model's reply, in the schema's shape.
type answer struct {
	Labels []answerLabel `json:"labels"`
}

type answerLabel struct {
	Candidate int    `json:"candidate"`
	Label     string `json:"label"`
	Quote     string `json:"quote"`
	Reason    string `json:"reason"`
}

// userPrompt writes the ticket and one batch of candidates for the model.
func userPrompt(request *ouroborosv1.LabelRequest, terms []term, batch []candidate, window int) string {
	var text strings.Builder
	fmt.Fprintf(&text, "Ticket #%d of %s\n\nPredicate:\n%s\n", request.GetTicket(), request.GetRepository(), request.GetPredicate())
	if facts := request.GetFacts(); len(facts) > 0 {
		text.WriteString(factsHeadingPrompt)
		for _, fact := range facts {
			text.WriteString(bulletMark + fact + "\n")
		}
	}
	text.WriteString(candidatesHeadingPrompt)
	for index, found := range batch {
		matched := make([]string, 0, len(found.terms))
		for _, term := range found.terms {
			matched = append(matched, terms[term].String())
		}
		fmt.Fprintf(&text, "\n[%d] instance=%s source=%s:%d kind=%s matched=%s\n%s\n",
			index+1, found.instance, found.source, found.line, found.kind, strings.Join(matched, ", "), found.excerpt(window))
	}
	return text.String()
}

// prompt is the whole decision for one batch.
func prompt(request *ouroborosv1.LabelRequest, terms []term, batch []candidate, window int, keepAlive time.Duration) *ollama.Prompt {
	return &ollama.Prompt{System: systemPrompt, User: userPrompt(request, terms, batch, window), Schema: answerSchema, KeepAlive: keepAlive}
}

// parseAnswer reads the model's JSON. A content the schema should have
// prevented is an error of the model server, not a label.
func parseAnswer(content string) ([]answerLabel, error) {
	var parsed answer
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMalformedAnswer, err)
	}
	return parsed.Labels, nil
}

// fixedPromptChars is the size of the prompt without its candidates, which
// the batch size is derived from.
func fixedPromptChars(request *ouroborosv1.LabelRequest) int {
	return len(systemPrompt) + len(userPrompt(request, nil, nil, 0)) + len(answerSchema)
}

// batchSize is how many candidates fit one context window: the window less
// the fixed part of the prompt (its characters at the measured ratio) and
// the answer's envelope, over the tokens one candidate costs in the prompt
// and in the answer, both measured. Never fewer than one.
func batchSize(contextWindow int, charsPerToken float64, fixedChars int, tokensPerCandidate float64, answerPerLabel float64) int {
	available := float64(contextWindow) - float64(fixedChars)/charsPerToken - answerEnvelopeTokens
	return max(int(available/(tokensPerCandidate+answerPerLabel)), 1)
}

// The files a binary records a run in, under the harness state directory:
// the run record the ops view's panel reads, replaced whole, and the labels,
// one per line.
const (
	RunFile    = "labeler.json"
	RecordFile = "labeler.jsonl"
)

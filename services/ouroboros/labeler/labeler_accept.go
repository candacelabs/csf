// Copyright 2026 Candace Labs

package labeler

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/candacelabs/csf/io/ipc/proc"
	ouroborosv1 "github.com/candacelabs/csf/proto/candace/ouroboros/v1"
)

// The miner command line (contract.mli: [facts ITEM...]) and the rejections
// the acceptance rule writes.
const (
	minerFactsVerb = "facts"

	rejectionNoCandidate  = "names no candidate of its batch"
	rejectionEmptyQuote   = "quotes nothing"
	rejectionQuoteAbsent  = "quote is not verbatim on the line"
	rejectionNotExtracted = "the miner's extractor emits no fact for the instance"
)

// minerFact is one line of a miner's facts output, as contract.ml writes it.
type minerFact struct {
	Args []struct {
		Text string `json:"text"`
	} `json:"args"`
}

// accept applies the acceptance rule to one batch's answer and returns the
// typed labels. A positive stands only when it reproduces: through the
// ticket's miner when one is registered (its extractor emits a fact naming
// the instance from the candidate's source), and otherwise by its quote
// being verbatim on the line it names. A negative is recorded unchecked. A
// label naming no candidate carries no instance and no span, so it is
// counted as rejected and logged rather than recorded.
func (labeler *Labeler) accept(ctx context.Context, request *ouroborosv1.LabelRequest, batch []candidate, labels []answerLabel) ([]*ouroborosv1.ProposedLabel, int) {
	var proposed []*ouroborosv1.ProposedLabel
	var extracted map[string]bool
	unplaced := 0
	for _, label := range labels {
		if label.Candidate < 1 || label.Candidate > len(batch) {
			unplaced++
			labeler.logger.Warn("labeler: label rejected", "ticket", request.GetTicket(), "candidate", label.Candidate, "rejection", rejectionNoCandidate)
			continue
		}
		found := batch[label.Candidate-1]
		record := &ouroborosv1.ProposedLabel{
			Ticket:   request.GetTicket(),
			Instance: found.instance,
			Positive: label.Label == labelPositive,
			Span:     found.span(),
			Quote:    quoted(label.Quote),
			Reason:   label.Reason,
			Model:    labeler.modelName,
		}
		switch {
		case !record.Positive:
			record.Acceptance = ouroborosv1.Acceptance_ACCEPTANCE_UNCHECKED
		case labeler.miners[request.GetMiner()] != "":
			if extracted == nil {
				extracted = labeler.extracted(ctx, request.GetMiner(), batch)
			}
			record.Acceptance, record.Rejection = judgeFact(extracted, found.instance)
		default:
			record.Acceptance, record.Rejection = judgeSpan(found.text, record.Quote)
		}
		proposed = append(proposed, record)
	}
	return proposed, unplaced
}

// quoted is the model's quote without the excerpt's own cut marks and the
// whitespace around it: what is left must be the line's own characters.
func quoted(quote string) string {
	return strings.TrimSpace(strings.Trim(quote, excerptEllipsis))
}

// judgeSpan is the rule for a ticket with no extractor yet: every character
// the quote carries is on the line, in order. The quote may span the
// excerpt's cut mark, so it is read as fragments; and an event-log line is
// JSON, so the line is read both as written and with its string escapes
// decoded, the characters the agent wrote rather than their JSON spelling.
func judgeSpan(line string, quote string) (ouroborosv1.Acceptance, string) {
	switch {
	case quote == "":
		return ouroborosv1.Acceptance_ACCEPTANCE_REJECTED, rejectionEmptyQuote
	case !verbatim(line, quote) && !verbatim(decodedEscapes.Replace(line), quote):
		return ouroborosv1.Acceptance_ACCEPTANCE_REJECTED, rejectionQuoteAbsent
	}
	return ouroborosv1.Acceptance_ACCEPTANCE_REPRODUCED_SPAN, ""
}

// verbatim reports whether every fragment of quote, split at the excerpt's
// cut mark, is on line in the order quoted.
func verbatim(line string, quote string) bool {
	rest := line
	for _, fragment := range strings.Split(quote, excerptEllipsis) {
		fragment = strings.TrimSpace(fragment)
		if fragment == "" {
			continue
		}
		index := strings.Index(rest, fragment)
		if index < 0 {
			return false
		}
		rest = rest[index+len(fragment):]
	}
	return true
}

// decodedEscapes reads a JSON line's string escapes as the characters they
// spell. Every key is two characters starting with a backslash, so the
// replacement is unambiguous left to right.
var decodedEscapes = strings.NewReplacer(`\\`, `\`, `\"`, `"`, `\n`, "\n", `\t`, "\t", `\/`, `/`)

// judgeFact is the rule for a ticket whose miner exists: the extractor names
// the instance in a fact.
func judgeFact(extracted map[string]bool, instance string) (ouroborosv1.Acceptance, string) {
	if !extracted[instance] {
		return ouroborosv1.Acceptance_ACCEPTANCE_REJECTED, rejectionNotExtracted
	}
	return ouroborosv1.Acceptance_ACCEPTANCE_REPRODUCED_FACT, ""
}

// extracted runs the miner's extractor over the batch's sources and returns
// every instance a fact names. An extractor that fails names nothing, so
// every positive of the batch is rejected and the failure is logged.
func (labeler *Labeler) extracted(ctx context.Context, miner string, batch []candidate) map[string]bool {
	var items []string
	seen := map[string]bool{}
	for _, found := range batch {
		item := filepath.Join(labeler.stateDirectory, filepath.FromSlash(found.source))
		if !strings.Contains(found.source, sourceKindSep) && !seen[item] {
			seen[item] = true
			items = append(items, item)
		}
	}
	instances := map[string]bool{}
	if len(items) == 0 {
		return instances
	}
	result, err := labeler.launcher.Run(ctx, proc.Command{Executable: labeler.miners[miner], Arguments: append([]string{minerFactsVerb}, items...)})
	if err != nil {
		labeler.logger.Warn("labeler: the miner's extractor failed", "miner", miner, "error", err)
		return instances
	}
	scanner := bufio.NewScanner(bytes.NewReader(result.Stdout))
	scanner.Buffer(make([]byte, 0, 64<<10), maxLineBytes)
	for scanner.Scan() {
		var fact minerFact
		if json.Unmarshal(scanner.Bytes(), &fact) != nil {
			continue
		}
		for _, argument := range fact.Args {
			if argument.Text != "" {
				instances[argument.Text] = true
			}
		}
	}
	return instances
}

// describe is one label as the run's log and the proof list spell it.
func describe(label *ouroborosv1.ProposedLabel) string {
	sign := labelNegative
	if label.GetPositive() {
		sign = labelPositive
	}
	return fmt.Sprintf("%s %s %s:%d %s", sign, label.GetInstance(), label.GetSpan().GetSource(), label.GetSpan().GetLine(), label.GetAcceptance())
}

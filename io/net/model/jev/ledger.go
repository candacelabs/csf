// Copyright 2026 Candace Labs

package jev

import (
	"encoding/json"
	"time"
)

// DecideLedgerFile is the record of the local decision model's runs: one file
// under the harness state directory, replaced whole on every run, which the
// Workbench's JEV panel follows. `csf decide` writes it when a run names it.
const DecideLedgerFile = "jev.json"

// DecisionRun is one decision run: when it ran, the declared model it was
// answered by, how many questions it decided, and the tokens the Ollama server
// counted for it.
type DecisionRun struct {
	At           time.Time `json:"at"`
	Model        string    `json:"model"`
	Decisions    int64     `json:"decisions"`
	PromptTokens int64     `json:"prompt_tokens"`
	EvalTokens   int64     `json:"eval_tokens"`
}

// Tokens is the run's total tokens, the prompt read and the tokens generated.
func (run DecisionRun) Tokens() int64 { return run.PromptTokens + run.EvalTokens }

// DecideLedger is the record: every run, newest last, and when it was last
// written.
type DecideLedger struct {
	Runs      []DecisionRun `json:"runs"`
	UpdatedAt time.Time     `json:"updated_at"`
}

// TokensOn is the total tokens of every run on day's UTC date.
func (ledger DecideLedger) TokensOn(day time.Time) int64 {
	total := int64(0)
	for _, run := range ledger.Runs {
		if onUTCDay(run.At, day) {
			total += run.Tokens()
		}
	}
	return total
}

// DecisionsOn is how many questions were decided on day's UTC date.
func (ledger DecideLedger) DecisionsOn(day time.Time) int64 {
	total := int64(0)
	for _, run := range ledger.Runs {
		if onUTCDay(run.At, day) {
			total += run.Decisions
		}
	}
	return total
}

// onUTCDay reports whether at falls on day's UTC date.
func onUTCDay(at, day time.Time) bool {
	start := day.UTC().Truncate(24 * time.Hour)
	return !at.Before(start) && at.Before(start.Add(24*time.Hour))
}

// ReadDecideLedger decodes a record; an empty or unreadable one is an empty
// record.
func ReadDecideLedger(content []byte) DecideLedger {
	ledger := DecideLedger{}
	if json.Unmarshal(content, &ledger) != nil {
		ledger = DecideLedger{}
	}
	if ledger.Runs == nil {
		ledger.Runs = []DecisionRun{}
	}
	return ledger
}

// Record appends one run and stamps the record with its time, returning the
// record so a caller can write it whole.
func (ledger DecideLedger) Record(run DecisionRun) DecideLedger {
	ledger.Runs = append(ledger.Runs, run)
	ledger.UpdatedAt = run.At
	return ledger
}

// Encode renders the record as the file's content.
func (ledger DecideLedger) Encode() ([]byte, error) {
	content, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(content, '\n'), nil
}

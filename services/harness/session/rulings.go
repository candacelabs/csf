// Copyright 2026 Candace Labs

package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// RulingsFile holds every ruling the operator recorded, one JSON object per
// line, in the order recorded, under the harness's state directory.
const RulingsFile = "rulings.jsonl"

// EventTypeRulings records, at the start of every turn, the rulings in force
// when the turn was sent: the question gate checks the turn's questions and
// the alternatives they offer against them.
const EventTypeRulings = "rulings"

// KeyRulings is the rulings a rulings record carries.
const KeyRulings = "rulings"

// Unenforced is how a ruling no gate enforces is flagged wherever rulings are
// shown.
const Unenforced = "UNENFORCED"

const rulingsFileMode = 0o600

// Ruling is one recorded operator ruling: what it decides, the operator's own
// words, the day, whom it binds, why, the gate that enforces it or the one
// that will, the alternatives it rules out, and the ruling it replaces. An
// agent question, or an alternative it offers, that names an excluded
// alternative re-litigates the ruling.
type Ruling struct {
	ID          string    `json:"ruling_id"`
	Statement   string    `json:"statement"`
	Excludes    []string  `json:"excludes"`
	Supersedes  string    `json:"supersedes,omitempty"`
	Quote       string    `json:"quote,omitempty"`
	RuledOn     string    `json:"ruled_on,omitempty"`
	Scope       string    `json:"scope,omitempty"`
	Why         string    `json:"why,omitempty"`
	EnforcedBy  string    `json:"enforced_by,omitempty"`
	PendingGate string    `json:"pending_gate,omitempty"`
	RecordedAt  time.Time `json:"recorded_at"`
}

// Enforced reports whether a gate enforces the ruling. A ruling whose gate
// is only pending is not enforced.
func (ruling Ruling) Enforced() bool { return ruling.EnforcedBy != "" }

// RulingCoverage is how many of the rulings in force a gate enforces, out of
// all of them.
type RulingCoverage struct {
	Enforced int
	Total    int
}

// Unenforced is the rulings in force that no gate enforces.
func (coverage RulingCoverage) Unenforced() int { return coverage.Total - coverage.Enforced }

// CoverageOf counts the rulings a gate enforces.
func CoverageOf(rulings []Ruling) RulingCoverage {
	coverage := RulingCoverage{Total: len(rulings)}
	for _, ruling := range rulings {
		if ruling.Enforced() {
			coverage.Enforced++
		}
	}
	return coverage
}

// AppendRuling records ruling after every earlier one under stateDirectory.
func AppendRuling(stateDirectory string, ruling Ruling) error {
	line, err := json.Marshal(ruling)
	if err != nil {
		return fmt.Errorf("harness session: encode ruling: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(stateDirectory, RulingsFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, rulingsFileMode)
	if err != nil {
		return fmt.Errorf("harness session: open rulings: %w", err)
	}
	if _, err := file.Write(append(line, '\n')); err != nil {
		_ = file.Close()
		return fmt.Errorf("harness session: record ruling: %w", err)
	}
	return file.Close()
}

// RulingsInForce returns the rulings in force under stateDirectory, in the
// order first recorded. No rulings file is no rulings.
func RulingsInForce(stateDirectory string) ([]Ruling, error) {
	content, err := os.ReadFile(filepath.Join(stateDirectory, RulingsFile))
	if errors.Is(err, os.ErrNotExist) {
		return []Ruling{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("harness session: read rulings: %w", err)
	}
	return InForce(content)
}

// InForce folds the content of a rulings file into the rulings in force, in
// the order first recorded: the latest record of each identifier, without
// the ones a later ruling supersedes.
func InForce(content []byte) ([]Ruling, error) {
	latest := map[string]Ruling{}
	order := []string{}
	superseded := map[string]bool{}
	scanner := bufio.NewScanner(bytes.NewReader(content))
	scanner.Buffer(make([]byte, 0, recordBuffer), MaxRecordBytes)
	for scanner.Scan() {
		var ruling Ruling
		if err := json.Unmarshal(scanner.Bytes(), &ruling); err != nil {
			return nil, fmt.Errorf("harness session: decode ruling: %w", err)
		}
		if _, seen := latest[ruling.ID]; !seen {
			order = append(order, ruling.ID)
		}
		latest[ruling.ID] = ruling
		if ruling.Supersedes != "" {
			superseded[ruling.Supersedes] = true
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("harness session: read rulings: %w", err)
	}
	order = slices.DeleteFunc(order, func(id string) bool { return superseded[id] })
	inForce := make([]Ruling, 0, len(order))
	for _, id := range order {
		inForce = append(inForce, latest[id])
	}
	return inForce, nil
}

// WriteRulingsView writes the human-readable view of the rulings in force,
// generated from the records: the coverage first, then each ruling under its
// identifier with its gate or the UNENFORCED flag, its statement, the
// operator's words quoted and its facts.
func WriteRulingsView(writer io.Writer, rulings []Ruling) error {
	coverage := CoverageOf(rulings)
	var view strings.Builder
	view.WriteString("# Operator rulings\n\n")
	view.WriteString("Generated by `csf rulings` from the ruling records under the harness state directory. Record a ruling with the RecordRuling operation; never edit this text.\n\n")
	fmt.Fprintf(&view, "Enforced by a gate: %d of %d. %s: %d.\n", coverage.Enforced, coverage.Total, Unenforced, coverage.Unenforced())
	for _, ruling := range rulings {
		fmt.Fprintf(&view, "\n## %s: %s\n\n%s\n\n", ruling.ID, ruling.Enforcement(), ruling.Statement)
		if ruling.Quote != "" {
			fmt.Fprintf(&view, "> %s\n\n", strings.ReplaceAll(strings.TrimSpace(ruling.Quote), "\n", "\n> "))
		}
		facts := [][2]string{
			{"Ruled on", ruling.RuledOn}, {"Scope", ruling.Scope}, {"Why", ruling.Why},
			{"Excludes", strings.Join(ruling.Excludes, "; ")}, {"Supersedes", ruling.Supersedes},
		}
		for _, fact := range facts {
			if fact[1] != "" {
				fmt.Fprintf(&view, "- %s: %s\n", fact[0], fact[1])
			}
		}
	}
	_, err := io.WriteString(writer, view.String())
	return err
}

// Enforcement is the ruling's gate, or the UNENFORCED flag with the gate
// pending when one is named.
func (ruling Ruling) Enforcement() string {
	switch {
	case ruling.Enforced():
		return "enforced by " + ruling.EnforcedBy
	case ruling.PendingGate != "":
		return Unenforced + " (pending " + ruling.PendingGate + ")"
	}
	return Unenforced
}

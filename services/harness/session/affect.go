// Copyright 2026 Candace Labs

package session

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/candacelabs/csf/pkg/affect"
)

// EventTypeOperatorAffect records, for a turn whose message is the operator's
// own words, the message's operator affect as pkg/affect reads it: strain,
// kind, whether it is an operator correction, whether the operator reports
// strain, and the features. It is the label of the agent turn the message
// answers, which the record names as its target turn.
const EventTypeOperatorAffect = "operator_affect"

// The keys of an operator affect record beyond the common ones.
const (
	// KeyAffect is the reading, an affect.Reading object.
	KeyAffect = "affect"
	// KeyTargetTurn is the turn whose reply the message answers; 0 when the
	// message opens the session.
	KeyTargetTurn = "target_turn"
)

// strainNotice is the block appended to an operator's message that reads as
// strained, naming the bound the reply gate holds the reply to.
func strainNotice() string {
	return fmt.Sprintf("\n\n---\nCSF operator affect: the operator's strain reads high. Answer in at most %d words; the reply gate refuses a longer reply, and the turn continues.",
		affect.ReplyWordLimit)
}

// affectMarker is the cheap test a line must pass before it is decoded as an
// operator affect record.
var affectMarker = []byte(`"` + EventTypeOperatorAffect + `"`)

// OperatorCorrection is one operator correction a run recorded: the run, the
// turn whose reply it answers, and when the operator sent it.
type OperatorCorrection struct {
	Assignment string
	TargetTurn int
	At         time.Time
}

// affectRecord is the part of an operator affect record the corrections read.
type affectRecord struct {
	Record
	TargetTurn int `json:"target_turn"`
}

// OperatorCorrections is every operator correction recorded in any run under
// stateDirectory, oldest first. A run whose log cannot be read contributes
// nothing.
func OperatorCorrections(stateDirectory string) ([]OperatorCorrection, error) {
	paths, err := filepath.Glob(filepath.Join(stateDirectory, "*", EventsFile))
	if err != nil {
		return nil, fmt.Errorf("harness session: list event logs: %w", err)
	}
	corrections := []OperatorCorrection{}
	for _, path := range paths {
		assignment := filepath.Base(filepath.Dir(path))
		_ = scanLines(path, func(line []byte) error {
			if !bytes.Contains(line, affectMarker) {
				return nil
			}
			var record affectRecord
			if json.Unmarshal(line, &record) == nil && record.EventType == EventTypeOperatorAffect && record.Affect != nil && record.Affect.Correction {
				corrections = append(corrections, OperatorCorrection{Assignment: assignment, TargetTurn: record.TargetTurn, At: record.Time})
			}
			return nil
		})
	}
	slices.SortStableFunc(corrections, func(left OperatorCorrection, right OperatorCorrection) int { return left.At.Compare(right.At) })
	return corrections, nil
}

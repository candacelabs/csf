// Copyright 2026 Candace Labs

package session

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/candacelabs/csf/pkg/terms"
)

// EventTypeUnvettedTerms records, for a turn whose message is the operator's
// own words, the operator's unvetted terms: the terms of the message that
// appear in none of the operator's earlier messages. The vocabulary of
// earlier messages is the union of every such record under the state
// directory, since every term is unvetted exactly once, at its first
// appearance.
const EventTypeUnvettedTerms = "unvetted_terms"

// The keys of an unvetted terms record beyond the common ones.
const (
	// KeyTerms is the unvetted terms, as the operator spelled them.
	KeyTerms = "terms"
	// keyMessageTerms is how many terms the message carried in all.
	keyMessageTerms = "message_terms"
	// keyVocabulary is how many stems the operator's vocabulary held.
	keyVocabulary = "vocabulary"
)

// The research check protocol between the message and the reply gate: what a
// reply carries for each unvetted term before acting on it.
const (
	// ResearchCheckFence tags the fenced block holding one research check as
	// a JSON object, or several as a JSON array.
	ResearchCheckFence = "research-check"

	FitsGoalYes    = "yes"
	FitsGoalNo     = "no"
	FitsGoalPartly = "partly"

	LikelySourceAgentOutput = "agent output"
	LikelySourcePaper       = "paper"
	LikelySourcePerson      = "person"
	LikelySourceUnknown     = "unknown"

	valueSeparator = "|"
	termSeparator  = ", "
)

// The values the enumerated fields of a research check may hold.
var (
	FitsGoalValues     = []string{FitsGoalYes, FitsGoalNo, FitsGoalPartly}
	LikelySourceValues = []string{LikelySourceAgentOutput, LikelySourcePaper, LikelySourcePerson, LikelySourceUnknown}
)

// ResearchCheck is the typed record a reply carries for each unvetted term.
type ResearchCheck struct {
	Term            string `json:"term"`
	WhatItIs        string `json:"what_it_is"`
	WhatItDoesNotDo string `json:"what_it_does_not_do"`
	// FitsGoal is one of FitsGoalValues: whether the term fits the
	// operator's stated goal.
	FitsGoal string `json:"fits_goal"`
	Why      string `json:"why"`
	// LikelySource is one of LikelySourceValues: where the term most likely
	// came from.
	LikelySource string `json:"likely_source"`
}

// Incomplete names the fields a check leaves blank or outside their values;
// a complete check has none.
func (check ResearchCheck) Incomplete() []string {
	missing := []string{}
	for _, field := range []struct {
		name  string
		value string
		among []string
	}{
		{"term", check.Term, nil},
		{"what_it_is", check.WhatItIs, nil},
		{"what_it_does_not_do", check.WhatItDoesNotDo, nil},
		{"fits_goal", check.FitsGoal, FitsGoalValues},
		{"why", check.Why, nil},
		{"likely_source", check.LikelySource, LikelySourceValues},
	} {
		switch {
		case strings.TrimSpace(field.value) == "":
			missing = append(missing, field.name)
		case field.among != nil && !slices.Contains(field.among, field.value):
			missing = append(missing, field.name)
		}
	}
	return missing
}

// ResearchCheckSkeleton is the check for term with its answers blank and its
// enumerations spelled out, as the message shows it to the agent.
func ResearchCheckSkeleton(term terms.Term) string {
	skeleton, _ := json.Marshal(ResearchCheck{
		Term:         string(term),
		FitsGoal:     strings.Join(FitsGoalValues, valueSeparator),
		LikelySource: strings.Join(LikelySourceValues, valueSeparator),
	})
	return string(skeleton)
}

// unvettedMarker is the cheap test a line must pass before it is decoded as
// an unvetted terms record.
var unvettedMarker = []byte(`"` + EventTypeUnvettedTerms + `"`)

// OperatorVocabulary is every term recorded as unvetted in any run under
// stateDirectory: the operator's vocabulary so far. A run whose log cannot be
// read contributes nothing.
func OperatorVocabulary(stateDirectory string) (*terms.Vocabulary, error) {
	paths, err := filepath.Glob(filepath.Join(stateDirectory, "*", EventsFile))
	if err != nil {
		return nil, fmt.Errorf("harness session: list event logs: %w", err)
	}
	vocabulary := terms.NewVocabulary()
	for _, path := range paths {
		_ = scanLines(path, func(line []byte) error {
			if !bytes.Contains(line, unvettedMarker) {
				return nil
			}
			var record Record
			if json.Unmarshal(line, &record) == nil && record.EventType == EventTypeUnvettedTerms {
				vocabulary.Add(record.Terms...)
			}
			return nil
		})
	}
	return vocabulary, nil
}

// unvettedNotice is the block appended to an operator's message naming its
// unvetted terms and the research check the reply must carry for each.
func unvettedNotice(novel []terms.Term) string {
	spelled := make([]string, 0, len(novel))
	for _, term := range novel {
		spelled = append(spelled, string(term))
	}
	return fmt.Sprintf("\n\n---\nCSF unvetted terms: %s. These appear in none of the operator's earlier messages. "+
		"Before acting on them, your reply must carry one research check per term: a fenced block tagged %s holding one JSON object "+
		"(or one array of them), its enumerated fields set to one of the values shown:\n```%s\n%s\n```\n"+
		"The reply gate refuses a reply without a complete check for each term, and the turn continues.",
		strings.Join(spelled, termSeparator), ResearchCheckFence, ResearchCheckFence, ResearchCheckSkeleton(novel[0]))
}

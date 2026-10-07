// Copyright 2026 Candace Labs

package views

import (
	"context"
	_ "embed"
	"encoding/json"
	"time"
)

// The values of the source label of the orchestrator-quality families.
const (
	SourceHandLabeled = "hand_labeled"
	SourceRuns        = "runs"
	SourceAffect      = "affect"
)

// Correction is one operator-flagged mistake, classed by the ticket or gate
// that owns its class.
type Correction struct {
	Class string    `json:"class"`
	What  string    `json:"what"`
	At    time.Time `json:"at"`
}

// CorrectionSource lists the typed corrections AFFECT (#124) finds, which
// export as source affect.
type CorrectionSource func(ctx context.Context) ([]Correction, error)

// HandLabeled is corrections.json: the window labeled by hand before AFFECT
// types corrections, with the operator-attention hours counted for it.
type HandLabeled struct {
	Source         string       `json:"source"`
	Provenance     string       `json:"provenance"`
	From           time.Time    `json:"from"`
	Through        time.Time    `json:"through"`
	AttentionHours int64        `json:"attention_hours"`
	MergedSlices   []int        `json:"merged_slices"`
	Corrections    []Correction `json:"corrections"`
}

//go:embed corrections.json
var correctionsDocument []byte

var handLabeled = mustDecodeHandLabeled(correctionsDocument)

func mustDecodeHandLabeled(document []byte) HandLabeled {
	var decoded HandLabeled
	if err := json.Unmarshal(document, &decoded); err != nil {
		panic("views: decode corrections.json: " + err.Error())
	}
	return decoded
}

// ByClass counts corrections by class.
func ByClass(corrections []Correction) map[string]int64 {
	counts := map[string]int64{}
	for _, correction := range corrections {
		counts[correction.Class]++
	}
	return counts
}

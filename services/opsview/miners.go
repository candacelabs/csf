// Copyright 2026 Candace Labs

package opsview

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/a-h/templ"

	"github.com/candacelabs/csf/pkg/gotth/live"
)

// The miners panel: every ouroboros miner's mutation score as a series, read
// from one file at the root of the state directory. A miner's mutate verb
// prints the record (Mutation in services/ouroboros/contract/records.proto)
// as one JSON line, and whoever runs it appends the line:
//
//	miner.exe mutate --json LABELS ITEM... >> <state>/mutation.jsonl
const (
	// MutationFile is the series file, one record per line, newest last.
	MutationFile = "mutation.jsonl"
	// MinersRegion is the panel's region.
	MinersRegion = "opsview.miners"
	// EventMutation carries the whole series, as JSON in the series field. It
	// is internal: the follow effect emits it and a browser may not.
	EventMutation = "opsview.mutation"
	FieldSeries   = "series"

	minersTemplate   = "miners"
	scoreFormat      = "%d/%d = %.2f"
	seriesTimeFormat = "01-02 15:04"
)

// MutationPoint is one measurement of one miner's test.
type MutationPoint struct {
	At        time.Time `json:"at"`
	Killed    int       `json:"killed"`
	Survived  int       `json:"survived"`
	Excluded  int       `json:"excluded"`
	Score     float64   `json:"score"`
	Accepted  bool      `json:"accepted"`
	Surviving []string  `json:"surviving"`
}

// MinerSeries is one miner's measurements in file order.
type MinerSeries struct {
	Miner  string          `json:"miner"`
	Points []MutationPoint `json:"points"`
}

// mutationRecord is the part of one mutation.jsonl line this view reads.
type mutationRecord struct {
	Miner     string  `json:"miner"`
	At        int64   `json:"at"`
	Killed    int     `json:"killed"`
	Survived  int     `json:"survived"`
	Excluded  int     `json:"excluded"`
	Score     float64 `json:"score"`
	Accepted  bool    `json:"accepted"`
	Surviving []struct {
		Description string `json:"description"`
	} `json:"surviving"`
}

// ReadSeries folds the series file into one series per miner, miners in
// name order and points in file order. A line that is not a record is
// skipped: the file is appended by hand and by the loop, and one bad line
// must not hide every other miner. It is pure.
func ReadSeries(content []byte) []MinerSeries {
	byMiner := map[string]*MinerSeries{}
	scanner := bufio.NewScanner(bytes.NewReader(content))
	for scanner.Scan() {
		var parsed mutationRecord
		if json.Unmarshal(scanner.Bytes(), &parsed) != nil || parsed.Miner == "" {
			continue
		}
		series, known := byMiner[parsed.Miner]
		if !known {
			series = &MinerSeries{Miner: parsed.Miner}
			byMiner[parsed.Miner] = series
		}
		surviving := make([]string, 0, len(parsed.Surviving))
		for _, mutant := range parsed.Surviving {
			surviving = append(surviving, mutant.Description)
		}
		series.Points = append(series.Points, MutationPoint{
			At: time.Unix(parsed.At, 0).UTC(), Killed: parsed.Killed, Survived: parsed.Survived, Excluded: parsed.Excluded,
			Score: parsed.Score, Accepted: parsed.Accepted, Surviving: surviving,
		})
	}
	result := make([]MinerSeries, 0, len(byMiner))
	for _, series := range byMiner {
		result = append(result, *series)
	}
	slices.SortFunc(result, func(a, b MinerSeries) int { return strings.Compare(a.Miner, b.Miner) })
	return result
}

// SeriesEvent is the event the follow effect emits when the series file
// changed, addressed to the panel.
func SeriesEvent(series []MinerSeries) (live.Event, error) {
	encoded, err := json.Marshal(series)
	if err != nil {
		return live.Event{}, err
	}
	return live.Event{Name: EventMutation, FragmentID: MinersRegion, Fields: live.NewFields(map[string]string{FieldSeries: string(encoded)})}, nil
}

// seriesEqual compares two series by their wire form; no series at all and
// an empty one are the same series.
func seriesEqual(previous []MinerSeries, next []MinerSeries) bool {
	if len(previous) == 0 && len(next) == 0 {
		return true
	}
	before, _ := json.Marshal(previous)
	after, _ := json.Marshal(next)
	return string(before) == string(after)
}

// minersView is the panel's data: every miner with its rendered points.
type minersView struct {
	Region string
	Miners []minerView
}

// minerView is one miner's row: its latest measurement and every point.
type minerView struct {
	Miner     string
	Latest    pointView
	Points    []pointView
	Surviving []string
}

type pointView struct {
	Time     string
	Label    string
	Accepted bool
}

func pointOf(point MutationPoint) pointView {
	return pointView{
		Time:     point.At.Format(seriesTimeFormat),
		Label:    fmt.Sprintf(scoreFormat, point.Killed, point.Killed+point.Survived, point.Score),
		Accepted: point.Accepted,
	}
}

// renderMiners draws the panel: nothing inside it until a record exists.
func renderMiners(state viewState) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error {
		panel := minersView{Region: MinersRegion}
		for _, series := range state.miners {
			if len(series.Points) == 0 {
				continue
			}
			row := minerView{Miner: series.Miner}
			for _, point := range series.Points {
				row.Points = append(row.Points, pointOf(point))
			}
			latest := series.Points[len(series.Points)-1]
			row.Latest, row.Surviving = pointOf(latest), latest.Surviving
			panel.Miners = append(panel.Miners, row)
		}
		return views.ExecuteTemplate(writer, minersTemplate, panel)
	})
}

// minersChanged reports whether the panel's markup moved.
func minersChanged(previous viewState, next viewState) bool {
	return !seriesEqual(previous.miners, next.miners)
}

// minersFragment is the panel as the live library mounts it.
func minersFragment() live.Fragment[viewState] {
	return live.Fragment[viewState]{ID: MinersRegion, Render: renderMiners, Dirty: minersChanged}
}

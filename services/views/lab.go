// Copyright 2026 Candace Labs

package views

import (
	_ "embed"
	"encoding/json"
	"time"
)

// LabEntry is a dated lab entry's results, as lab.json records them: each
// a sample of one family, at the time the entry landed.
type LabEntry struct {
	Entry  string      `json:"entry"`
	Slice  string      `json:"slice"`
	At     time.Time   `json:"at"`
	Note   string      `json:"note"`
	Series []LabSample `json:"series"`
}

// LabSample is one result: a family, its labels and the value.
type LabSample struct {
	Metric string            `json:"metric"`
	Labels map[string]string `json:"labels"`
	Value  float64           `json:"value"`
}

// labValues are the sample's label values, in the family's label order.
func (sample LabSample) labelValues() []string {
	metric, _ := catalog.Lookup(sample.Metric)
	values := make([]string, 0, len(metric.Labels))
	for _, label := range metric.Labels {
		values = append(values, sample.Labels[label])
	}
	return values
}

//go:embed lab.json
var labDocument []byte

var lab = mustDecodeLab(labDocument)

func mustDecodeLab(document []byte) []LabEntry {
	var decoded struct {
		Entries []LabEntry `json:"entries"`
	}
	if err := json.Unmarshal(document, &decoded); err != nil {
		panic("views: decode lab.json: " + err.Error())
	}
	return decoded.Entries
}

// LabEntries is every lab entry lab.json records.
func LabEntries() []LabEntry { return append([]LabEntry(nil), lab...) }

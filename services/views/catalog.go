// Copyright 2026 Candace Labs

package views

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
)

// CatalogFile is the catalog's path in this package, which the metric panel
// check reads from a checkout.
const CatalogFile = "catalog.json"

// MetricType is a metric's Prometheus type as the catalog spells it.
type MetricType string

// The metric types the catalog uses. A histogram's panel queries its
// _bucket, _sum or _count series.
const (
	Gauge     MetricType = "gauge"
	Counter   MetricType = "counter"
	Histogram MetricType = "histogram"
)

// histogramSeries are the series suffixes a histogram exports.
var histogramSeries = []string{"_bucket", "_sum", "_count"}

// LookupSeries finds the family a queried series belongs to: the family of
// that name, or the histogram whose _bucket, _sum or _count series it is.
func (decoded Catalog) LookupSeries(series string) (Metric, bool) {
	if metric, found := decoded.Lookup(series); found {
		return metric, true
	}
	for _, suffix := range histogramSeries {
		if name, cut := strings.CutSuffix(series, suffix); cut {
			if metric, found := decoded.Lookup(name); found && metric.Type == Histogram {
				return metric, true
			}
		}
	}
	return Metric{}, false
}

// Metric is one family CSF exports: its name, type, labels, the dashboard row
// its panel sits in, and its definition, which is both the family's help text
// and what its panel's description must carry. Pending names the slice whose
// landing gives the family its first samples; until then it is described and
// panelled but empty.
type Metric struct {
	Name       string     `json:"name"`
	Type       MetricType `json:"type"`
	Row        string     `json:"row"`
	Labels     []string   `json:"labels"`
	Definition string     `json:"definition"`
	Pending    string     `json:"pending,omitempty"`
}

// Catalog is every family CSF exports at /metrics.
type Catalog struct {
	Metrics []Metric `json:"metrics"`
}

//go:embed catalog.json
var catalogDocument []byte

// catalog is the embedded catalog, decoded once; a catalog that does not
// decode is a build defect, which the package's specs catch.
var catalog = mustDecodeCatalog(catalogDocument)

// ReadCatalog decodes a catalog document.
func ReadCatalog(document []byte) (Catalog, error) {
	var decoded Catalog
	if err := json.Unmarshal(document, &decoded); err != nil {
		return Catalog{}, fmt.Errorf("views: decode %s: %w", CatalogFile, err)
	}
	return decoded, nil
}

func mustDecodeCatalog(document []byte) Catalog {
	decoded, err := ReadCatalog(document)
	if err != nil {
		panic(err)
	}
	return decoded
}

// Metrics is the embedded catalog's families, in catalog order.
func Metrics() []Metric { return append([]Metric(nil), catalog.Metrics...) }

// Lookup finds a family by name.
func (decoded Catalog) Lookup(name string) (Metric, bool) {
	for _, metric := range decoded.Metrics {
		if metric.Name == name {
			return metric, true
		}
	}
	return Metric{}, false
}

// MetricChatSettle is the family services/harness/chat observes: how long a
// session's event takes to reach the transcripts watching it.
const MetricChatSettle = "csf_chat_settle_seconds"

// HistogramOpts are a histogram family's options, built from the catalog so
// the service observing it cannot drift from its name and definition.
func HistogramOpts(name string, buckets []float64) prometheus.HistogramOpts {
	metric, found := catalog.Lookup(name)
	if !found || metric.Type != Histogram {
		panic("views: " + name + " is not a histogram in " + CatalogFile)
	}
	return prometheus.HistogramOpts{Name: metric.Name, Help: metric.Definition, Buckets: buckets}
}

// descriptor is the family's Prometheus descriptor, built from the catalog
// so the exported name, labels and help cannot drift from it.
func descriptor(name string) *prometheus.Desc {
	metric, found := catalog.Lookup(name)
	if !found {
		panic("views: " + name + " is not in " + CatalogFile)
	}
	return prometheus.NewDesc(metric.Name, metric.Definition, metric.Labels, nil)
}

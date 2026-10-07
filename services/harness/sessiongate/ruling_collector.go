// Copyright 2026 Candace Labs

package sessiongate

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/candacelabs/csf/services/harness/session"
)

// The rulings series: the operator rulings in force, split by whether a gate
// enforces them. Their sum is every ruling; the unenforced count is driven to
// zero by gating rulings, never by recording fewer.
const (
	rulingsMetric    = "csf_rulings_in_force"
	labelEnforcement = "enforcement"

	// EnforcementEnforced and EnforcementUnenforced are the series' two
	// values of enforcement.
	EnforcementEnforced   = "enforced"
	EnforcementUnenforced = "unenforced"
)

// RulingCollector exports the rulings in force, read afresh from the state
// directory's ruling records at each scrape.
type RulingCollector struct {
	state   string
	rulings *prometheus.Desc
}

// NewRulingCollector builds the collector over the ruling records under
// state.
func NewRulingCollector(state string) *RulingCollector {
	return &RulingCollector{
		state:   state,
		rulings: prometheus.NewDesc(rulingsMetric, "Operator rulings in force, from the harness's ruling records, by whether a gate enforces them (enforced) or none does (unenforced).", []string{labelEnforcement}, nil),
	}
}

// Describe implements prometheus.Collector.
func (collector *RulingCollector) Describe(output chan<- *prometheus.Desc) {
	output <- collector.rulings
}

// Collect implements prometheus.Collector.
func (collector *RulingCollector) Collect(output chan<- prometheus.Metric) {
	rulings, err := session.RulingsInForce(collector.state)
	if err != nil {
		output <- prometheus.NewInvalidMetric(collector.rulings, err)
		return
	}
	coverage := session.CoverageOf(rulings)
	output <- prometheus.MustNewConstMetric(collector.rulings, prometheus.GaugeValue, float64(coverage.Enforced), EnforcementEnforced)
	output <- prometheus.MustNewConstMetric(collector.rulings, prometheus.GaugeValue, float64(coverage.Unenforced()), EnforcementUnenforced)
}

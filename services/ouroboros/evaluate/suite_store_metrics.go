// Copyright 2026 Candace Labs

package evaluate

import (
	"context"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// The suite's series, read from the records at each scrape; the CSF
// dashboard's Evaluation suite row draws them.
const (
	MetricScore       = "csf_eval_struggle_rate_per_1k_tool_calls"
	MetricWallSeconds = "csf_eval_score_wall_seconds"
	MetricCostUSD     = "csf_eval_score_cost_usd"
	MetricShards      = "csf_eval_shards"

	labelBuild = "build"
	labelSuite = "suite"
	labelBound = "bound"
	labelNode  = "node"
	boundValue = "value"
	boundLow   = "low"
	boundHigh  = "high"

	// scrapeTimeout bounds one scrape's read of the records.
	scrapeTimeout = 5 * time.Second
)

var (
	scoreDescription = prometheus.NewDesc(MetricScore,
		"A csf build's score on the held-out evaluation suite: struggle episodes per 1,000 tool calls over its replays at the suite's budget, with the 95% Garwood interval as bound low and high.",
		[]string{labelBuild, labelSuite, labelBound}, nil)
	wallDescription = prometheus.NewDesc(MetricWallSeconds,
		"Wall-clock seconds to score a build: from the first replay launched to the last recorded.",
		[]string{labelBuild, labelSuite}, nil)
	costDescription = prometheus.NewDesc(MetricCostUSD,
		"Dollars to score a build: the model cost of its replays.",
		[]string{labelBuild, labelSuite}, nil)
	shardsDescription = prometheus.NewDesc(MetricShards,
		"Replays of a build's score run on each node kind: this host or a cloud burst job.",
		[]string{labelBuild, labelSuite, labelNode}, nil)
)

// Collector measures every recorded score at each scrape.
type Collector struct {
	scores func(ctx context.Context) ([]Score, error)
}

var _ prometheus.Collector = (*Collector)(nil)

// NewSuiteCollector measures the scores scores returns.
func NewSuiteCollector(scores func(ctx context.Context) ([]Score, error)) *Collector {
	return &Collector{scores: scores}
}

// Describe sends every series' description.
func (collector *Collector) Describe(descriptions chan<- *prometheus.Desc) {
	for _, description := range []*prometheus.Desc{scoreDescription, wallDescription, costDescription, shardsDescription} {
		descriptions <- description
	}
}

// Collect reads the scores and sends every series; a record that cannot be
// read sends none.
func (collector *Collector) Collect(metrics chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), scrapeTimeout)
	defer cancel()
	scores, err := collector.scores(ctx)
	if err != nil {
		return
	}
	for _, score := range scores {
		suite := strconv.Itoa(score.SuiteVersion)
		for bound, value := range map[string]*float64{boundValue: score.PerK, boundLow: score.Low, boundHigh: score.High} {
			if value != nil {
				metrics <- prometheus.MustNewConstMetric(scoreDescription, prometheus.GaugeValue, *value, score.Build, suite, bound)
			}
		}
		metrics <- prometheus.MustNewConstMetric(wallDescription, prometheus.GaugeValue, score.WallSeconds, score.Build, suite)
		metrics <- prometheus.MustNewConstMetric(costDescription, prometheus.GaugeValue, score.CostUSD, score.Build, suite)
		for node, count := range score.Nodes {
			metrics <- prometheus.MustNewConstMetric(shardsDescription, prometheus.GaugeValue, float64(count), score.Build, suite, node)
		}
	}
}

// Scores is every recorded scoring run's score.
func (store *SuiteStore) Scores(ctx context.Context) ([]Score, error) {
	runs, err := store.Runs(ctx)
	if err != nil {
		return nil, err
	}
	suites, err := store.Suites(ctx)
	if err != nil {
		return nil, err
	}
	byVersion := map[int]Suite{}
	for _, suite := range suites {
		byVersion[suite.Version] = suite
	}
	scores := make([]Score, 0, len(runs))
	for _, run := range runs {
		suite, known := byVersion[run.SuiteVersion]
		if !known {
			continue
		}
		score, err := store.ScoreOn(ctx, run.Build, suite)
		if err != nil {
			return nil, err
		}
		scores = append(scores, score)
	}
	return scores, nil
}

// Copyright 2026 Candace Labs

package routing

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/candacelabs/csf/services/harness/session"
)

const (
	// resultEvent is Claude Code's end-of-turn event, which carries the
	// endpoint's usage for the turn.
	resultEvent = "result"
	// Offenders is how many rows the report names as the largest offenders.
	Offenders = 5

	metricPrefix    = "csf_harness_cache_"
	decisionsMetric = "csf_harness_routing_decisions"
	labelScope      = "scope"
	labelAssignment = "assignment"
	labelAgent      = "agent"
	labelReal       = "real_session"
	labelModel      = "model"
	labelArm        = "arm"
	labelReason     = "reason"
	scopeFleet      = "fleet"
	scopeVirtual    = "virtual_session"
	scopeReal       = "real_session"
	maxRecordBytes  = 8 << 20
)

// Usage is the endpoint's prompt-cache accounting for one or more turns.
type Usage struct {
	Read     int64 `json:"cache_read_input_tokens"`
	Creation int64 `json:"cache_creation_input_tokens"`
	Input    int64 `json:"input_tokens"`
}

// Total is every input token the turns submitted.
func (usage Usage) Total() int64 { return usage.Read + usage.Creation + usage.Input }

// HitRatio is the share of submitted input tokens read from the cache.
func (usage Usage) HitRatio() float64 {
	if usage.Total() == 0 {
		return 0
	}
	return float64(usage.Read) / float64(usage.Total())
}

func (usage Usage) add(other Usage) Usage {
	return Usage{Read: usage.Read + other.Read, Creation: usage.Creation + other.Creation, Input: usage.Input + other.Input}
}

// Row is one virtual session on one real session.
type Row struct {
	Virtual  string  `json:"v"`
	Agent    string  `json:"agent"`
	Real     string  `json:"r"`
	Model    string  `json:"model"`
	Turns    int     `json:"turns"`
	Usage    Usage   `json:"usage"`
	HitRatio float64 `json:"h"`
	// FirstTurn is the usage of the first turn on this real session: where
	// a cold start pays for the whole prefix.
	FirstTurn Usage `json:"first_turn"`
}

// Report is the fleet's cache measurement: the dashboard's JSON.
type Report struct {
	Virtual  int     `json:"n_virtual"`
	Real     int     `json:"m_real"`
	Turns    int     `json:"turns"`
	Usage    Usage   `json:"usage"`
	HitRatio float64 `json:"h_fleet"`
	// FirstTurnCreationShare is the share of all cache creation paid by
	// first turns.
	FirstTurnCreationShare float64 `json:"first_turn_creation_share"`
	// Offenders are the rows that wrote the most cache, most first.
	Offenders []Row `json:"offenders"`
	Rows      []Row `json:"rows"`
}

// Measure reads every run's result events under the state directory.
func Measure(state string) (Report, error) {
	paths, err := filepath.Glob(filepath.Join(state, "*", session.EventsFile))
	if err != nil {
		return Report{}, err
	}
	report := Report{Rows: []Row{}, Offenders: []Row{}}
	reals := map[string]bool{}
	var firstCreation int64
	for _, path := range paths {
		rows, err := measureRun(filepath.Dir(path))
		if err != nil {
			return Report{}, err
		}
		if len(rows) > 0 {
			report.Virtual++
		}
		for _, row := range rows {
			reals[row.Real] = true
			report.Turns += row.Turns
			report.Usage = report.Usage.add(row.Usage)
			firstCreation += row.FirstTurn.Creation
			report.Rows = append(report.Rows, row)
		}
	}
	report.Real = len(reals)
	report.HitRatio = report.Usage.HitRatio()
	if report.Usage.Creation > 0 {
		report.FirstTurnCreationShare = float64(firstCreation) / float64(report.Usage.Creation)
	}
	slices.SortStableFunc(report.Rows, func(left Row, right Row) int {
		switch {
		case left.Usage.Creation > right.Usage.Creation:
			return -1
		case left.Usage.Creation < right.Usage.Creation:
			return 1
		}
		return 0
	})
	report.Offenders = report.Rows[:min(Offenders, len(report.Rows))]
	return report, nil
}

// measureRun is one run's rows, one per real session its turns ran on, in
// the order the run first used them, labeled with the run's agent and model
// when its run record can be read.
func measureRun(directory string) ([]Row, error) {
	path := filepath.Join(directory, session.EventsFile)
	virtual := filepath.Base(directory)
	var agent, model string
	if recorded, err := session.ReadRunState(directory); err == nil {
		agent, model = recorded.AgentID, recorded.Model
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	rows := []Row{}
	index := map[string]int{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1<<16), maxRecordBytes)
	for scanner.Scan() {
		var record struct {
			EventType string `json:"event_type"`
			Event     struct {
				Type      string `json:"type"`
				SessionID string `json:"session_id"`
				Usage     *Usage `json:"usage"`
			} `json:"event"`
		}
		// Records are slog lines of several shapes; only result events with
		// usage count.
		if json.Unmarshal(scanner.Bytes(), &record) != nil || record.EventType != resultEvent || record.Event.Usage == nil {
			continue
		}
		position, found := index[record.Event.SessionID]
		if !found {
			position = len(rows)
			index[record.Event.SessionID] = position
			rows = append(rows, Row{Virtual: virtual, Agent: agent, Real: record.Event.SessionID, Model: model, FirstTurn: *record.Event.Usage})
		}
		rows[position].Turns++
		rows[position].Usage = rows[position].Usage.add(*record.Event.Usage)
		rows[position].HitRatio = rows[position].Usage.HitRatio()
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("harness routing: read %s: %w", path, err)
	}
	return rows, nil
}

// DecisionKey is one series of the decisions counter.
type DecisionKey struct {
	Arm    Arm
	Reason Reason
}

// CountDecisions counts the logged routing decisions by arm and reason; no
// log yet is no decisions.
func CountDecisions(state string) (map[DecisionKey]int, error) {
	counts := map[DecisionKey]int{}
	file, err := os.Open(filepath.Join(state, Directory, DecisionsFile))
	if errors.Is(err, os.ErrNotExist) {
		return counts, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var decision Decision
		if err := json.Unmarshal(scanner.Bytes(), &decision); err != nil {
			return nil, fmt.Errorf("harness routing: decode decision: %w", err)
		}
		counts[DecisionKey{Arm: decision.Arm, Reason: decision.Reason}]++
	}
	return counts, scanner.Err()
}

// rowLabels name a measurement row: never a host, a path or a user.
var rowLabels = []string{labelScope, labelAssignment, labelAgent, labelReal, labelModel}

// Collector exports the measurement as Prometheus gauges, measured afresh
// at each scrape.
type Collector struct {
	state     string
	hit       *prometheus.Desc
	creation  *prometheus.Desc
	sessions  *prometheus.Desc
	share     *prometheus.Desc
	decisions *prometheus.Desc
}

// NewCollector measures the runs under the state directory at each scrape.
func NewCollector(state string) *Collector {
	return &Collector{
		state:     state,
		hit:       prometheus.NewDesc(metricPrefix+"hit_ratio", "Share of submitted input tokens read from the prompt cache.", rowLabels, nil),
		creation:  prometheus.NewDesc(metricPrefix+"creation_tokens", "Input tokens written to the prompt cache.", rowLabels, nil),
		sessions:  prometheus.NewDesc(metricPrefix+"sessions", "Virtual and real sessions measured.", []string{labelScope}, nil),
		share:     prometheus.NewDesc(metricPrefix+"first_turn_creation_share", "Share of cache creation paid by first turns.", nil, nil),
		decisions: prometheus.NewDesc(decisionsMetric, "Routing decisions recorded, by arm and reason.", []string{labelArm, labelReason}, nil),
	}
}

// Describe sends the collector's descriptors.
func (collector *Collector) Describe(output chan<- *prometheus.Desc) {
	for _, descriptor := range []*prometheus.Desc{collector.hit, collector.creation, collector.sessions, collector.share, collector.decisions} {
		output <- descriptor
	}
}

// Collect measures and sends the gauges; a failed measurement sends an
// invalid metric, which the scrape reports.
func (collector *Collector) Collect(output chan<- prometheus.Metric) {
	report, err := Measure(collector.state)
	if err != nil {
		output <- prometheus.NewInvalidMetric(collector.hit, err)
		return
	}
	output <- prometheus.MustNewConstMetric(collector.hit, prometheus.GaugeValue, report.HitRatio, scopeFleet, "", "", "", "")
	output <- prometheus.MustNewConstMetric(collector.creation, prometheus.GaugeValue, float64(report.Usage.Creation), scopeFleet, "", "", "", "")
	output <- prometheus.MustNewConstMetric(collector.sessions, prometheus.GaugeValue, float64(report.Virtual), scopeVirtual)
	output <- prometheus.MustNewConstMetric(collector.sessions, prometheus.GaugeValue, float64(report.Real), scopeReal)
	output <- prometheus.MustNewConstMetric(collector.share, prometheus.GaugeValue, report.FirstTurnCreationShare)
	counts, err := CountDecisions(collector.state)
	if err != nil {
		output <- prometheus.NewInvalidMetric(collector.decisions, err)
	}
	for key, count := range counts {
		output <- prometheus.MustNewConstMetric(collector.decisions, prometheus.CounterValue, float64(count), string(key.Arm), string(key.Reason))
	}
	for _, row := range report.Rows {
		output <- prometheus.MustNewConstMetric(collector.hit, prometheus.GaugeValue, row.HitRatio, scopeReal, row.Virtual, row.Agent, row.Real, row.Model)
		output <- prometheus.MustNewConstMetric(collector.creation, prometheus.GaugeValue, float64(row.Usage.Creation), scopeReal, row.Virtual, row.Agent, row.Real, row.Model)
	}
}

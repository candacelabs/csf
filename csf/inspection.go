package csf

import (
	"context"
	"strings"
	"time"

	"github.com/candacelabs/csf/pkg/core"
	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	inspectionPrefix          = "candace_holygrail_"
	inspectionRoute           = "/metrics"
	checkPassed               = "passed"
	checkFailed               = "failed"
	projectionTasksMetric     = "projection_tasks"
	projectionWorkersMetric   = "projection_workers"
	projectionActiveMetric    = "projection_active_workers"
	projectionReadableMetric  = "projection_queue_readable"
	projectionStatePrefix     = "PROJECTION_STATE_"
	projectionStateLabel      = "state"
	browserConnectionsMetric  = "browser_connections"
)

// inspectionMetricDefinition is one inspection series: its name, its help
// text, and its label names.
type inspectionMetricDefinition struct {
	name   string
	help   string
	labels []string
}

// Inspection mounts observations in the caller's router and registry. It owns
// no listener or background task. Receipts remain the event ledger; their
// retained counts are gauges because retention can remove records.
type Inspection struct {
	simulations *Simulations
	registry    *prometheus.Registry
	snapshot    func() FarmView
	receipts    string
	descriptors map[string]*prometheus.Desc
	workers     *ProjectionWorkers
	telemetry   func(ctx context.Context) (api.TelemetrySnapshot, error)
	connections func() int
}

// InspectionOption configures an Inspection.
type InspectionOption func(inspection *Inspection)

// WithInspectionRegistry shares the host's registry with optional capabilities.
// The host owns this registry and must not register Go/process collectors twice.
func WithInspectionRegistry(registry *prometheus.Registry) InspectionOption {
	return func(inspection *Inspection) {
		if registry != nil {
			inspection.registry = registry
		}
	}
}

// WithInspectionBrowserConnections samples the live board's connection registry.
// Without a source the series is absent, rather than reporting a measured zero.
func WithInspectionBrowserConnections(source func() int) InspectionOption {
	return func(inspection *Inspection) { inspection.connections = source }
}

func WithInspectionProjectionWorkers(workers *ProjectionWorkers) InspectionOption {
	return func(inspection *Inspection) { inspection.workers = workers }
}

func WithInspectionSnapshot(snapshot func() FarmView) InspectionOption {
	return func(inspection *Inspection) { inspection.snapshot = snapshot }
}

func WithInspectionReceipts(path string) InspectionOption {
	return func(inspection *Inspection) { inspection.receipts = path }
}

func NewInspection(options ...InspectionOption) *Inspection {
	inspection := &Inspection{registry: prometheus.NewRegistry(), descriptors: make(map[string]*prometheus.Desc)}
	for _, option := range options {
		if option != nil {
			option(inspection)
		}
	}
	definitions := []inspectionMetricDefinition{
		{projectionTasksMetric, "Durable projection tasks by current state; absent when the queue cannot be read.", []string{projectionStateLabel}},
		{projectionWorkersMetric, "Configured projection goroutines in this process.", nil},
		{projectionActiveMetric, "Projection goroutines currently executing a claimed task.", nil},
		{projectionReadableMetric, "One when the durable projection queue was read successfully, otherwise zero.", nil},
		{"runtime_count", "Application Go processes exporting this target; excludes supporting infrastructure and other machines.", nil},
		{browserConnectionsMetric, "Registered live-board WebSocket connections, including cleanup; excludes chat and terminal streams. Absent when the board is not mounted.", nil},
		{"work_report_readable", "One when the work report and priority order can be read, otherwise zero.", nil},
		{"work_report_timestamp_seconds", "Unix timestamp reported by the work producer; absent when unknown or invalid.", nil},
		{"agent_reports", "Reported agents by current state; reports older than 120 seconds are unknown, not active.", []string{"state"}},
		{"receipt_collection_readable", "One when all retained command receipts were read within collection bounds, otherwise zero.", nil},
		{"check_receipts", "Retained command receipt count, not a lifetime total or a count of proven product requirements.", []string{"result"}},
		{"last_check_timestamp_seconds", "Most recent command completion timestamp by result; absent when there is no observation.", []string{"result"}},
	}
	definitions = append(definitions, telemetryMetricDefinitions...)
	definitions = append(definitions, simulationMetricDefinitions...)
	for _, definition := range definitions {
		inspection.descriptors[definition.name] = prometheus.NewDesc(inspectionPrefix+definition.name, definition.help, definition.labels, nil)
	}
	inspection.registry.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}), inspection)
	return inspection
}

func (inspection *Inspection) Register(router gin.IRouter) {
	router.GET(inspectionRoute, gin.WrapH(promhttp.HandlerFor(inspection.registry, promhttp.HandlerOpts{})))
}

func (inspection *Inspection) Describe(output chan<- *prometheus.Desc) {
	for _, descriptor := range inspection.descriptors {
		output <- descriptor
	}
}

func (inspection *Inspection) collectConnections(emit func(name string, value float64, labels ...string)) {
	if inspection.connections != nil {
		emit(browserConnectionsMetric, float64(inspection.connections()))
	}
}

func (inspection *Inspection) Collect(output chan<- prometheus.Metric) {
	emit := func(name string, value float64, labels ...string) {
		output <- prometheus.MustNewConstMetric(inspection.descriptors[name], prometheus.GaugeValue, value, labels...)
	}
	emit("runtime_count", 1)
	inspection.collectConnections(emit)
	inspection.collectTelemetry(emit)
	inspection.collectSimulations(emit)
	if inspection.workers == nil {
		emit(projectionWorkersMetric, 0)
		emit(projectionActiveMetric, 0)
		emit(projectionReadableMetric, 0)
	} else {
		emit(projectionWorkersMetric, float64(inspection.workers.Configured()))
		emit(projectionActiveMetric, float64(inspection.workers.Active()))
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		counts, err := inspection.workers.Counts(ctx)
		cancel()
		emit(projectionReadableMetric, core.BoolToFloat64(err == nil))
		if err == nil {
			for _, count := range counts {
				emit(projectionTasksMetric, float64(count.Count), strings.ToLower(strings.TrimPrefix(count.State.String(), projectionStatePrefix)))
			}
		}
	}
	if inspection.snapshot != nil {
		view := inspection.snapshot()
		readable := view.SourceError == ""
		emit("work_report_readable", core.BoolToFloat64(readable))
		if readable {
			if timestamp, err := time.Parse(time.RFC3339, view.UpdatedAt); err == nil && !timestamp.After(time.Now()) {
				emit("work_report_timestamp_seconds", float64(timestamp.Unix()))
			}
			states := map[string]int{"running": 0, "idle": 0, "unknown": 0, "failed": 0, "completed": 0}
			for _, agent := range view.Agents {
				state := agent.State
				if _, exists := states[state]; !exists {
					state = "unknown"
				}
				states[state]++
			}
			for state, count := range states {
				emit("agent_reports", float64(count), state)
			}
		}
	} else {
		emit("work_report_readable", 0)
	}
	receipts, err := readInspectionReceipts(inspection.receipts)
	emit("receipt_collection_readable", core.BoolToFloat64(err == nil))
	if err != nil {
		return
	}
	emitResult := func(result string, summary inspectionReceiptSummary) {
		emit("check_receipts", float64(summary.count), result)
		if !summary.latest.IsZero() {
			emit("last_check_timestamp_seconds", float64(summary.latest.Unix()), result)
		}
	}
	emitResult(checkPassed, receipts.passed)
	emitResult(checkFailed, receipts.failed)
}

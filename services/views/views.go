// Copyright 2026 Candace Labs

// Package views is CSF's measurements and the Prometheus and Grafana that
// show them: CSF owns both and uses no other project's.
//
// [Views] exports every family of catalog.json at /metrics. The per-run
// families (turns, tokens by kind, spend, gate decisions, reply-gate
// refusals, merge checks) and the struggle rate are folded from the state
// directory's run records by one goroutine, which refolds only a run whose
// event log changed and publishes each measurement whole; the live families
// (sessions by phase, admission, the dispatcher's slices and limits) are read
// from their sources at each scrape; merges and the ontology alignment score
// come from main's merged pull requests.
//
// Given a [Stack], Start provisions Prometheus and Grafana through the
// container capability, with the dashboard in dashboard/csf.json, and
// backfills the history the records reconstruct. /views/panels/<metric>
// redirects to the panel that shows a family, on the host the browser used,
// which is how the Workbench links its tiles.
//
// No metric without a panel, no panel without a metric: [CheckPanels] is the
// check tools/metricpanels runs on the merge path.
package views

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/candacelabs/csf/io/kernel/clock"
	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/runtime"
)

const (
	// MetricsPath is where the families are served.
	MetricsPath = "/metrics"
	// PanelPath is the prefix of a family's panel link: PanelPath + name.
	PanelPath = "/views/panels/"
	// DashboardPath redirects to the dashboard.
	DashboardPath = "/views"

	metricParameter = "metric"
	// The goroutines the views own, by name.
	ownerFold      = "fold"
	ownerCosts     = "costs"
	ownerPulls     = "pulls"
	dashboardRoute = "/d/" + DashboardUID
	viewPanelQuery = "viewPanel"
	// panelReference is how Grafana's scenes address a panel by its id.
	panelReference = "panel-"
	// PullRefresh is how often main's merge history is read again: merges
	// land minutes apart, and each read lists every merged pull request.
	PullRefresh = 15 * time.Minute
)

var (
	// ErrNoGrafana is the answer to a panel link while no Grafana is
	// provisioned.
	ErrNoGrafana = errors.New("views: no Grafana is provisioned on this host")
	// ErrStarted reports a second Start: the views own their goroutines once.
	ErrStarted = errors.New("views: already started")
)

// Views exports CSF's measurements and serves the panel links.
type Views struct {
	files      iofs.IFiles
	clock      clock.IClock
	logger     *slog.Logger
	sessions   SessionSource
	admission  AdmissionSource
	dispatch   DispatchSource
	pullSource PullSource
	// corrections is AFFECT's typed corrections (#124), once it lands.
	corrections CorrectionSource
	collectors  []prometheus.Collector
	stack       *Stack
	registry    *prometheus.Registry

	started     atomic.Bool
	measurement atomic.Pointer[Measurement]
	pulls       atomic.Pointer[[]MergedPull]
	costs       atomic.Pointer[[]CostEvent]
	grafana     atomic.Pointer[StackRecord]
	// measured, pulled and costed close once the fold, the merge history and
	// the cost events have each been read once, which the backfill waits for.
	measured chan struct{}
	pulled   chan struct{}
	costed   chan struct{}
}

// Option configures [Views].
type Option func(views *Views) error

// WithCorpus grants the state directory whose run records are folded.
// Required.
func WithCorpus(files iofs.IFiles) Option {
	return func(views *Views) error {
		if files == nil {
			return fmt.Errorf("%w: nil corpus", ErrInvalidOption)
		}
		views.files = files
		return nil
	}
}

// WithClock replaces the clock the refreshes are paced by.
func WithClock(source clock.IClock) Option {
	return func(views *Views) error {
		if source == nil {
			return fmt.Errorf("%w: nil clock", ErrInvalidOption)
		}
		views.clock = source
		return nil
	}
}

// WithLogger names the logger refresh failures are reported to.
func WithLogger(logger *slog.Logger) Option {
	return func(views *Views) error {
		if logger == nil {
			return fmt.Errorf("%w: nil logger", ErrInvalidOption)
		}
		views.logger = logger
		return nil
	}
}

// WithSessions grants the session list csf_sessions is read from.
func WithSessions(source SessionSource) Option {
	return func(views *Views) error {
		if source == nil {
			return fmt.Errorf("%w: nil session source", ErrInvalidOption)
		}
		views.sessions = source
		return nil
	}
}

// WithAdmission grants the launch check the admission families are read from.
func WithAdmission(source AdmissionSource) Option {
	return func(views *Views) error {
		if source == nil {
			return fmt.Errorf("%w: nil admission source", ErrInvalidOption)
		}
		views.admission = source
		return nil
	}
}

// WithDispatch grants the dispatcher's snapshot, which the dispatch families
// and every slice label are read from.
func WithDispatch(source DispatchSource) Option {
	return func(views *Views) error {
		if source == nil {
			return fmt.Errorf("%w: nil dispatch source", ErrInvalidOption)
		}
		views.dispatch = source
		return nil
	}
}

// WithPulls grants main's merge history, which the merge and ontology
// families are read from.
func WithPulls(source PullSource) Option {
	return func(views *Views) error {
		if source == nil {
			return fmt.Errorf("%w: nil pull source", ErrInvalidOption)
		}
		views.pullSource = source
		return nil
	}
}

// WithCorrections grants the typed operator corrections AFFECT (#124) finds,
// which export beside the hand-labeled window as source affect.
func WithCorrections(source CorrectionSource) Option {
	return func(views *Views) error {
		if source == nil {
			return fmt.Errorf("%w: nil correction source", ErrInvalidOption)
		}
		views.corrections = source
		return nil
	}
}

// WithCollectors adds collectors another package owns, whose families the
// catalog also lists, to /metrics.
func WithCollectors(collectors ...prometheus.Collector) Option {
	return func(views *Views) error {
		for _, added := range collectors {
			if added == nil {
				return fmt.Errorf("%w: nil collector", ErrInvalidOption)
			}
		}
		views.collectors = append(views.collectors, collectors...)
		return nil
	}
}

// WithStack provisions Prometheus and Grafana when the views start.
func WithStack(stack *Stack) Option {
	return func(views *Views) error {
		if stack == nil {
			return fmt.Errorf("%w: nil stack", ErrInvalidOption)
		}
		views.stack = stack
		return nil
	}
}

// NewViews validates the whole option set and returns the views, their
// registry holding the collector and every added one.
func NewViews(options ...Option) (*Views, error) {
	views := &Views{clock: clock.NewSystemClock(), logger: slog.Default(), registry: prometheus.NewRegistry(),
		measured: make(chan struct{}), pulled: make(chan struct{}), costed: make(chan struct{})}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("%w: nil option", ErrInvalidOption)
		}
		if err := option(views); err != nil {
			return nil, err
		}
	}
	if views.files == nil {
		return nil, ErrMissingCapability
	}
	for _, registered := range append([]prometheus.Collector{newCollector(views)}, views.collectors...) {
		if err := views.registry.Register(registered); err != nil {
			return nil, fmt.Errorf("views: register collector: %w", err)
		}
	}
	return views, nil
}

// Registry is the registry /metrics serves.
func (views *Views) Registry() *prometheus.Registry { return views.registry }

// Register mounts /metrics, the dashboard link and the panel links.
func (views *Views) Register(router gin.IRouter) {
	router.GET(MetricsPath, gin.WrapH(promhttp.HandlerFor(views.registry, promhttp.HandlerOpts{EnableOpenMetrics: true})))
	router.GET(DashboardPath, func(request *gin.Context) { views.redirect(request, 0) })
	router.GET(PanelPath+":"+metricParameter, func(request *gin.Context) {
		panel, found := PanelFor(request.Param(metricParameter))
		if !found {
			request.String(http.StatusNotFound, "no panel shows %s", request.Param(metricParameter))
			return
		}
		views.redirect(request, panel.ID)
	})
}

// redirect sends the browser to the dashboard, or one panel of it, on the
// host it reached csf serve by: Grafana is published on each address csf
// serve listens on.
func (views *Views) redirect(request *gin.Context, panel int) {
	record := views.grafana.Load()
	if record == nil {
		request.String(http.StatusServiceUnavailable, "%s", ErrNoGrafana.Error())
		return
	}
	host := request.Request.Host
	if name, _, err := net.SplitHostPort(host); err == nil {
		host = name
	}
	target := record.GrafanaURL(host) + dashboardRoute
	if panel != 0 {
		target += "?" + url.Values{viewPanelQuery: {panelReference + strconv.Itoa(panel)}}.Encode()
	}
	request.Redirect(http.StatusFound, target)
}

// Start runs the fold and, given a pull source, the merge history reader,
// each on the goroutine that owns it; given a stack, it provisions
// Prometheus and Grafana and backfills once both have published.
func (views *Views) Start(scope *runtime.Scope) error {
	if !views.started.CompareAndSwap(false, true) {
		return ErrStarted
	}
	folding := newCorpus(views.files)
	if err := scope.GoOwner(ownerFold, func(ctx context.Context) error {
		return refresh(ctx, views, ScrapeInterval, views.measured, &views.measurement, func(ctx context.Context) (Measurement, error) {
			measurement, err := folding.measure(ctx, views.clock.Now())
			if err != nil {
				return Measurement{}, err
			}
			return *measurement, nil
		})
	}); err != nil {
		return err
	}
	if err := scope.GoOwner(ownerCosts, func(ctx context.Context) error {
		return refresh(ctx, views, CostRefresh, views.costed, &views.costs, func(ctx context.Context) ([]CostEvent, error) {
			return DeriveCosts(views.files)
		})
	}); err != nil {
		return err
	}
	if views.pullSource == nil {
		close(views.pulled)
	} else if err := scope.GoOwner(ownerPulls, func(ctx context.Context) error {
		return refresh(ctx, views, PullRefresh, views.pulled, &views.pulls, views.pullSource)
	}); err != nil {
		return err
	}
	if views.stack == nil {
		return nil
	}
	return scope.Go(views.provision)
}

// refresh owns one published value: it reads, publishes and waits period,
// until ctx ends. A failed read is logged and keeps the last value
// published; attempted closes after the first read, failed or not, which is
// what the backfill waits for.
func refresh[Value any](ctx context.Context, views *Views, period time.Duration, attempted chan struct{},
	published *atomic.Pointer[Value], read func(ctx context.Context) (Value, error)) error {
	first := true
	for {
		value, err := read(ctx)
		switch {
		case err != nil && ctx.Err() != nil:
			return ctx.Err()
		case err != nil:
			views.logger.Warn("views: a measurement was not refreshed", "period", period, "error", err)
		default:
			published.Store(&value)
		}
		if first {
			close(attempted)
			first = false
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-views.clock.After(period):
		}
	}
}

// provision brings Prometheus and Grafana up, publishes Grafana's record for
// the panel links, and backfills what the records reconstruct since the last
// backfill. A failure is logged and leaves /metrics serving: the dashboard
// is how the measurements are seen, not how they are taken.
func (views *Views) provision(ctx context.Context) error {
	record, err := views.stack.Ensure(ctx)
	if err != nil {
		views.logger.Warn("views: Prometheus and Grafana not provisioned", "error", err)
		return nil
	}
	shown := record
	shown.Grafana.Settings = GrafanaAccess{}
	views.grafana.Store(&shown)
	views.logger.Info("views: Grafana provisioned", "addresses", record.Addresses, "port", record.Grafana.Port,
		"prometheus_port", record.Prometheus.Port, "retention_bytes", record.Prometheus.Settings.Bytes)
	for _, ready := range []chan struct{}{views.measured, views.pulled, views.costed} {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ready:
		}
	}
	history := History{Measurement: views.measurement.Load()}
	if pulls := views.pulls.Load(); pulls != nil {
		history.Pulls = *pulls
	}
	if events := views.costs.Load(); events != nil {
		history.Costs = *events
	}
	if views.dispatch != nil {
		if dispatch, err := views.dispatch(ctx); err == nil {
			history.Slices = dispatch.Slices
		}
	}
	samples, err := views.stack.Backfill(ctx, record, history, views.clock.Now())
	if err != nil {
		views.logger.Warn("views: history not backfilled", "error", err)
		return nil
	}
	views.logger.Info("views: history backfilled", "samples", samples, "after", record.BackfilledThrough)
	return nil
}

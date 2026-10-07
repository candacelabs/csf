// Package metrics exposes a warden node's cluster state as Prometheus metrics.
//
// Metrics are collected on scrape: a custom prometheus.Collector reads a fresh
// ClusterView snapshot via ViewSource.View() each time /metrics is scraped, so
// there is no background goroutine and no cached state to keep in sync. The
// collector holds only immutable metric descriptors, making it safe for
// concurrent scrapes without any locking of its own.
//
// A private *prometheus.Registry is used rather than the global default
// registry to avoid collisions with anything else linked into the binary. The
// Go runtime and process collectors are registered on that same private
// registry, which is also why a host embedding warden alongside its own
// instrumentation cannot have its default registry disturbed by this package.
//
// The metric and label names in names.go are the scrape contract. They are
// what an operator's dashboards and alert rules bind to, so they are treated
// like any other wire name: additive growth is fine, renaming one is a
// breaking change to every downstream query.
package metrics

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/candacelabs/csf/services/warden"
)

// Metrics serves the warden Prometheus endpoint from a private registry.
type Metrics struct {
	registry *prometheus.Registry
	handler  http.Handler
}

// New builds Metrics containing the warden view collector plus the standard Go
// runtime and process collectors. Without WithRegistry it creates a private
// registry, preserving the standalone default.
func New(view warden.IViewSource, options ...Option) *Metrics {
	config := metricsConfig{}
	for _, option := range options {
		if option != nil {
			option(&config)
		}
	}
	registry := config.registry
	if registry == nil {
		registry = prometheus.NewRegistry()
	}
	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		newViewCollector(view),
	)
	return &Metrics{
		registry: registry,
		handler:  promhttp.HandlerFor(registry, promhttp.HandlerOpts{}),
	}
}

// Register mounts the metrics endpoint (GET only) on r. The promhttp handler is
// adapted with gin.WrapH so its response (including its own Content-Type) is
// served unchanged; the engine's HandleMethodNotAllowed answers other methods
// with 405.
func (m *Metrics) Register(r gin.IRouter) {
	r.GET(warden.PathMetrics, gin.WrapH(m.handler))
}

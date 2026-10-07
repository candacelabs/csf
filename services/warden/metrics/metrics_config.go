package metrics

import "github.com/prometheus/client_golang/prometheus"

type metricsConfig struct {
	registry *prometheus.Registry
}

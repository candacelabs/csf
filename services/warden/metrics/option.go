package metrics

import "github.com/prometheus/client_golang/prometheus"

// Option configures Metrics before construction.
type Option func(config *metricsConfig)

// WithRegistry makes Metrics register and serve from registry. A nil registry
// retains the private-registry default.
func WithRegistry(registry *prometheus.Registry) Option {
	return func(config *metricsConfig) { config.registry = registry }
}

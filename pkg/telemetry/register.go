package telemetry

import (
	"errors"
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
)

// RegisterOnce registers collector with registerer and returns the collector
// that ends up registered. When a collector is already registered under the
// same name and type, RegisterOnce returns that existing collector, so two
// builders of the same metric share one instance instead of the second
// registration failing. A name already held by a different concrete type is an
// error, so two collectors never silently share a name.
func RegisterOnce[T prometheus.Collector](registerer prometheus.Registerer, collector T) (T, error) {
	if err := registerer.Register(collector); err != nil {
		var already prometheus.AlreadyRegisteredError
		if !errors.As(err, &already) {
			var zero T
			return zero, fmt.Errorf("telemetry: register collector: %w", err)
		}
		existing, ok := already.ExistingCollector.(T)
		if !ok {
			var zero T
			return zero, errors.New("telemetry: collector already registered under the same name with a different type")
		}
		return existing, nil
	}
	return collector, nil
}

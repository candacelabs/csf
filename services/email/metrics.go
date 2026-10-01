package email

import (
	"errors"
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	metricOutcomeAccepted = "accepted"
	metricOutcomeFailed   = "failed"
	metricOutcomeUnknown  = "unknown"
)

type mailerMetrics struct {
	sends    *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

func newMailerMetrics(registerer prometheus.Registerer) (*mailerMetrics, error) {
	if registerer == nil {
		return nil, nil
	}
	sends := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "csf_email_send_total",
		Help: "Total bounded CSF email send outcomes; accepted is not inbox delivery.",
	}, []string{"outcome"})
	duration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "csf_email_send_duration_seconds",
		Help:    "Duration of CSF email send attempts by bounded delivery outcome.",
		Buckets: prometheus.DefBuckets,
	}, []string{"outcome"})

	registeredSends, err := registerCounter(registerer, sends)
	if err != nil {
		return nil, err
	}
	registeredDuration, err := registerHistogram(registerer, duration)
	if err != nil {
		return nil, err
	}
	return &mailerMetrics{sends: registeredSends, duration: registeredDuration}, nil
}

func registerCounter(registerer prometheus.Registerer, collector *prometheus.CounterVec) (*prometheus.CounterVec, error) {
	if err := registerer.Register(collector); err != nil {
		var already prometheus.AlreadyRegisteredError
		if !errors.As(err, &already) {
			return nil, fmt.Errorf("registering csf_email_send_total: %w", err)
		}
		existing, ok := already.ExistingCollector.(*prometheus.CounterVec)
		if !ok {
			return nil, errors.New("email: csf_email_send_total registered with incompatible type")
		}
		return existing, nil
	}
	return collector, nil
}

func registerHistogram(registerer prometheus.Registerer, collector *prometheus.HistogramVec) (*prometheus.HistogramVec, error) {
	if err := registerer.Register(collector); err != nil {
		var already prometheus.AlreadyRegisteredError
		if !errors.As(err, &already) {
			return nil, fmt.Errorf("registering csf_email_send_duration_seconds: %w", err)
		}
		existing, ok := already.ExistingCollector.(*prometheus.HistogramVec)
		if !ok {
			return nil, errors.New("email: csf_email_send_duration_seconds registered with incompatible type")
		}
		return existing, nil
	}
	return collector, nil
}

func (metrics *mailerMetrics) observe(outcome string, seconds float64) {
	if metrics == nil {
		return
	}
	metrics.sends.WithLabelValues(outcome).Inc()
	metrics.duration.WithLabelValues(outcome).Observe(seconds)
}

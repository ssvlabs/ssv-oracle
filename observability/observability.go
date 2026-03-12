package observability

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/prometheus"
	metric_sdk "go.opentelemetry.io/otel/sdk/metric"

	"github.com/ssvlabs/ssv-oracle/logger"
)

// Setup initializes the OTel meter provider with a Prometheus exporter
// and sets it as the global provider. Must be called once per process.
// Returns a shutdown function that must be called on application exit.
func Setup() (func(context.Context) error, error) {
	exporter, err := prometheus.New()
	if err != nil {
		return nil, fmt.Errorf("create prometheus exporter: %w", err)
	}

	provider := metric_sdk.NewMeterProvider(
		metric_sdk.WithReader(exporter),
	)
	otel.SetMeterProvider(provider)

	return provider.Shutdown, nil
}

// NewMetric wraps OTel meter instrument constructors that return (T, error).
// Logs the error but returns the instrument regardless — a metric creation
// failure should not crash the oracle.
func NewMetric[T any](metric T, err error) T {
	if err != nil {
		logger.Errorw("Failed to create metric", "error", err)
	}
	return metric
}

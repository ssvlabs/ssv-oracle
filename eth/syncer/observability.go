package syncer

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"

	"github.com/ssvlabs/ssv-oracle/observability"
)

var syncerMeter = otel.Meter("github.com/ssvlabs/ssv-oracle/eth/syncer")

var syncerLastBlock = observability.NewMetric(syncerMeter.Int64Gauge(
	observability.MetricNamespace+".syncer.last_block",
	metric.WithDescription("Last synced block number."),
))

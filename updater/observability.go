package updater

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/ssvlabs/ssv-oracle/observability"
)

const (
	clusterOutcomeUpdated = "updated"
	clusterOutcomeSkipped = "skipped"
	clusterOutcomeFailed  = "failed"
)

var updaterMeter = otel.Meter("github.com/ssvlabs/ssv-oracle/updater")

var updaterClustersCounter = observability.NewMetric(updaterMeter.Int64Counter(
	observability.MetricNamespace+".updater.clusters",
	metric.WithDescription("Total cluster update results by outcome."),
))

func recordClusterUpdates(ctx context.Context, stats processStats) {
	updaterClustersCounter.Add(ctx, int64(stats.updated), metric.WithAttributes(attribute.String("outcome", clusterOutcomeUpdated)))
	updaterClustersCounter.Add(ctx, int64(stats.skipped), metric.WithAttributes(attribute.String("outcome", clusterOutcomeSkipped)))
	updaterClustersCounter.Add(ctx, int64(stats.failed), metric.WithAttributes(attribute.String("outcome", clusterOutcomeFailed)))
}

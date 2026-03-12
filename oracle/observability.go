package oracle

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/ssvlabs/ssv-oracle/observability"
)

const (
	outcomeSuccess          = "success"
	outcomeMissed           = "missed"
	outcomeAlreadyCommitted = "already_committed"
	outcomeConflict         = "conflict"
	outcomeReverted         = "reverted"
	outcomeError            = "error"
)

var meter = otel.Meter("github.com/ssvlabs/ssv-oracle/oracle")

var (
	commitCounter = observability.NewMetric(meter.Int64Counter(
		observability.MetricNamespace+".commit",
		metric.WithDescription("Oracle commit cycle outcomes."),
	))

	nextTargetGauge = observability.NewMetric(meter.Int64Gauge(
		observability.MetricNamespace+".next_target_epoch",
		metric.WithDescription("Next scheduled commit target epoch."),
	))

	clusterGauge = observability.NewMetric(meter.Int64Gauge(
		observability.MetricNamespace+".cluster_count",
		metric.WithDescription("Number of clusters in the latest merkle tree."),
	))

	validatorGauge = observability.NewMetric(meter.Int64Gauge(
		observability.MetricNamespace+".validator_count",
		metric.WithDescription("Number of active validators at last commit."),
	))

	totalEffBalanceGauge = observability.NewMetric(meter.Int64Gauge(
		observability.MetricNamespace+".total_effective_balance",
		metric.WithUnit("{ETH}"),
		metric.WithDescription("Total effective balance across all clusters."),
	))
)

func recordCommit(ctx context.Context, outcome string) {
	commitCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", outcome)))
}

func recordNextTarget(ctx context.Context, epoch uint64) {
	nextTargetGauge.Record(ctx, int64(epoch))
}

func recordCommitStats(ctx context.Context, clusters, validators int, totalEffBalance uint64) {
	clusterGauge.Record(ctx, int64(clusters))
	validatorGauge.Record(ctx, int64(validators))
	totalEffBalanceGauge.Record(ctx, int64(totalEffBalance))
}

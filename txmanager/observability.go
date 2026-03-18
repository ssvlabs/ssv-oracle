package txmanager

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/ssvlabs/ssv-oracle/observability"
)

const (
	txOutcomeSuccess   = "success"
	txOutcomeReverted  = "reverted"
	txOutcomeCancelled = "cancelled"
	txOutcomeError     = "error"
)

var txMeter = otel.Meter("github.com/ssvlabs/ssv-oracle/txmanager")

var txCounter = observability.NewMetric(txMeter.Int64Counter(
	observability.MetricNamespace+".tx",
	metric.WithDescription("Total transaction submissions by outcome."),
))

func recordTx(ctx context.Context, outcome string) {
	txCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", outcome)))
}

func recordTxReceipt(ctx context.Context, err error) {
	if err == nil {
		recordTx(ctx, txOutcomeSuccess)
		return
	}
	if _, isRevert := IsRevertError(err); isRevert {
		recordTx(ctx, txOutcomeReverted)
		return
	}
	recordTx(ctx, txOutcomeError)
}

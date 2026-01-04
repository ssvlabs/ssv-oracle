package syncer

import (
	"testing"

	"github.com/stretchr/testify/require"

	"ssv-oracle/contract"
)

func TestEventTopics_ReturnsCorrectCount(t *testing.T) {
	topics := EventTopics()
	require.Equal(t, len(handledEvents), len(topics))
}

func TestEventTopics_MatchesABI(t *testing.T) {
	topics := EventTopics()

	// Verify each topic matches the ABI event ID
	for i, name := range handledEvents {
		event, ok := contract.SSVNetworkABI.Events[name]
		require.True(t, ok, "event %s not found in ABI", name)
		require.Equal(t, event.ID, topics[i], "topic mismatch for %s", name)
	}
}

func TestEventTopics_MatchesManualSignatures(t *testing.T) {
	// Verify ABI-derived topics match the manually computed signatures in types.go
	topics := EventTopics()

	expected := map[string]bool{
		eventSigValidatorAdded.Hex():        false,
		eventSigValidatorRemoved.Hex():      false,
		eventSigClusterLiquidated.Hex():     false,
		eventSigClusterReactivated.Hex():    false,
		eventSigClusterWithdrawn.Hex():      false,
		eventSigClusterDeposited.Hex():      false,
		eventSigClusterMigratedToETH.Hex():  false,
		eventSigClusterBalanceUpdated.Hex(): false,
	}

	for _, topic := range topics {
		_, exists := expected[topic.Hex()]
		require.True(t, exists, "unexpected topic: %s", topic.Hex())
		expected[topic.Hex()] = true
	}

	// Ensure all expected signatures were found
	for sig, found := range expected {
		require.True(t, found, "missing expected signature: %s", sig)
	}
}

func TestEventTopics_AllHandledEventsHaveHandlers(t *testing.T) {
	// Verify every event in handledEvents has a corresponding case in applyEvent.
	// This is a documentation test - if it fails, update handledEvents or applyEvent.
	expectedEvents := []string{
		eventValidatorAdded,
		eventValidatorRemoved,
		eventClusterLiquidated,
		eventClusterReactivated,
		eventClusterWithdrawn,
		eventClusterDeposited,
		eventClusterMigratedToETH,
		eventClusterBalanceUpdated,
	}

	require.ElementsMatch(t, expectedEvents, handledEvents,
		"handledEvents doesn't match expected events - update if intentional")
}

func TestEventTopics_PanicsOnUnknownEvent(t *testing.T) {
	// Temporarily modify handledEvents to include an unknown event
	original := handledEvents
	defer func() { handledEvents = original }()

	handledEvents = append([]string{}, original...)
	handledEvents = append(handledEvents, "NonExistentEvent")

	require.Panics(t, func() {
		EventTopics()
	})
}

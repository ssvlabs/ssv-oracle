package syncer

import (
	"github.com/ethereum/go-ethereum/common"

	"ssv-oracle/contract"
)

// handledEvents lists event names that the syncer processes.
// These must match the handler cases in applyEvent().
// Order does not matter - topics are used as an OR filter.
var handledEvents = []string{
	eventValidatorAdded,
	eventValidatorRemoved,
	eventClusterLiquidated,
	eventClusterReactivated,
	eventClusterWithdrawn,
	eventClusterDeposited,
	eventClusterMigratedToETH,
	eventClusterBalanceUpdated,
}

// EventTopics returns topic0 hashes for all events the syncer handles.
// Topics are derived from the contract ABI to avoid signature drift.
// Panics if handledEvents contains a name not in the ABI.
func EventTopics() []common.Hash {
	topics := make([]common.Hash, 0, len(handledEvents))
	for _, name := range handledEvents {
		event, ok := contract.SSVNetworkABI.Events[name]
		if !ok {
			panic("unknown event in handledEvents: " + name)
		}
		topics = append(topics, event.ID)
	}
	return topics
}

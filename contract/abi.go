package contract

import (
	_ "embed"
	"encoding/hex"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
)

//go:embed SSVNetwork.abi
var ssvNetworkABIJSON string

//go:embed SSVNetworkViews.abi
var ssvNetworkViewsABIJSON string

// SSVNetworkABI is the parsed ABI for the SSV Network contract.
var SSVNetworkABI abi.ABI

// SSVNetworkViewsABI is the parsed ABI for the SSV Network Views contract.
var SSVNetworkViewsABI abi.ABI

// ErrorSelectors maps 4-byte error selectors to human-readable names.
var ErrorSelectors map[string]string

func init() {
	var err error
	SSVNetworkABI, err = abi.JSON(strings.NewReader(ssvNetworkABIJSON))
	if err != nil {
		panic("failed to parse SSVNetwork ABI: " + err.Error())
	}

	SSVNetworkViewsABI, err = abi.JSON(strings.NewReader(ssvNetworkViewsABIJSON))
	if err != nil {
		panic("failed to parse SSVNetworkViews ABI: " + err.Error())
	}

	ErrorSelectors = make(map[string]string)
	for name, abiError := range SSVNetworkABI.Errors {
		selector := hex.EncodeToString(abiError.ID[:4])
		ErrorSelectors[selector] = name
	}
}

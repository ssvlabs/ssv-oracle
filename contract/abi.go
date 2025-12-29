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

var ssvNetworkViewsABI abi.ABI

// errorSelectors maps 4-byte error selectors to error names for revert decoding.
var errorSelectors map[string]string

func init() {
	var err error
	SSVNetworkABI, err = abi.JSON(strings.NewReader(ssvNetworkABIJSON))
	if err != nil {
		panic("failed to parse SSVNetwork ABI: " + err.Error())
	}

	ssvNetworkViewsABI, err = abi.JSON(strings.NewReader(ssvNetworkViewsABIJSON))
	if err != nil {
		panic("failed to parse SSVNetworkViews ABI: " + err.Error())
	}

	errorSelectors = make(map[string]string)
	for name, abiError := range SSVNetworkABI.Errors {
		selector := hex.EncodeToString(abiError.ID[:4])
		errorSelectors[selector] = name
	}
}

package contract

import (
	_ "embed"
	"encoding/hex"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
)

//go:embed SSVNetwork.abi
var ssvNetworkABIJSON string

// SSVNetworkABI is the parsed ABI, loaded once at startup.
// Used by both contract client and event parser.
var SSVNetworkABI abi.ABI

// ErrorSelectors maps custom error selectors to human-readable names.
// Built dynamically from the SSVNetwork ABI.
var ErrorSelectors map[string]string

func init() {
	var err error
	SSVNetworkABI, err = abi.JSON(strings.NewReader(ssvNetworkABIJSON))
	if err != nil {
		panic("failed to parse SSVNetwork ABI: " + err.Error())
	}

	// Build error selectors from ABI
	ErrorSelectors = make(map[string]string)
	for name, abiError := range SSVNetworkABI.Errors {
		selector := hex.EncodeToString(abiError.ID[:4])
		ErrorSelectors[selector] = name
	}
}

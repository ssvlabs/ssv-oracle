package contract

import (
	_ "embed"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
)

//go:embed SSVNetwork.abi
var ssvNetworkABIJSON string

// SSVNetworkABI is the parsed ABI, loaded once at startup.
// Used by both contract client and event parser.
var SSVNetworkABI abi.ABI

func init() {
	var err error
	SSVNetworkABI, err = abi.JSON(strings.NewReader(ssvNetworkABIJSON))
	if err != nil {
		panic("failed to parse SSVNetwork ABI: " + err.Error())
	}
}

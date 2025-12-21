package merkle

import (
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/crypto"
)

var leafArguments abi.Arguments

func init() {
	bytes32Type, err := abi.NewType("bytes32", "", nil)
	if err != nil {
		panic("failed to create bytes32 ABI type: " + err.Error())
	}
	uint32Type, err := abi.NewType("uint32", "", nil)
	if err != nil {
		panic("failed to create uint32 ABI type: " + err.Error())
	}
	leafArguments = abi.Arguments{
		{Type: bytes32Type},
		{Type: uint32Type},
	}
}

// EncodeMerkleLeaf encodes a cluster balance into a Merkle leaf hash.
// Uses Solidity abi.encode(bytes32 clusterId, uint32 effectiveBalance).
func EncodeMerkleLeaf(clusterID [32]byte, effectiveBalance uint32) [32]byte {
	encoded, err := leafArguments.Pack(clusterID, effectiveBalance)
	if err != nil {
		panic("failed to encode merkle leaf: " + err.Error())
	}
	return crypto.Keccak256Hash(encoded)
}

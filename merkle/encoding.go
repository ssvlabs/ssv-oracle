package merkle

import (
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/crypto"
)

// Pre-computed ABI types for leaf encoding (initialized once at package load).
var leafArguments abi.Arguments

func init() {
	bytes32Type, err := abi.NewType("bytes32", "", nil)
	if err != nil {
		panic("failed to create bytes32 ABI type: " + err.Error())
	}
	uint64Type, err := abi.NewType("uint64", "", nil)
	if err != nil {
		panic("failed to create uint64 ABI type: " + err.Error())
	}
	leafArguments = abi.Arguments{
		{Type: bytes32Type},
		{Type: uint64Type},
	}
}

// EncodeMerkleLeaf encodes a cluster balance into a Merkle leaf.
// Uses Solidity abi.encode(bytes32 clusterId, uint64 effectiveBalance).
//
// This produces 64 bytes:
// - 32 bytes: clusterId (bytes32)
// - 32 bytes: effectiveBalance (uint64 padded to 32 bytes)
//
// Then returns keccak256 of those 64 bytes.
func EncodeMerkleLeaf(clusterID [32]byte, effectiveBalance uint64) [32]byte {
	encoded, err := leafArguments.Pack(clusterID, effectiveBalance)
	if err != nil {
		panic("failed to encode merkle leaf: " + err.Error())
	}
	return crypto.Keccak256Hash(encoded)
}

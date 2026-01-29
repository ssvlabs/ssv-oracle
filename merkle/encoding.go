package merkle

import (
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/crypto"
)

var leafABI abi.Arguments

func init() {
	leafABI = abi.Arguments{
		{Type: mustType("bytes32")},
		{Type: mustType("uint32")},
	}
}

// HashLeaf computes the double-hashed leaf for OpenZeppelin StandardMerkleTree.
// Returns keccak256(keccak256(abi.encode(clusterID, effectiveBalance))).
func HashLeaf(clusterID [32]byte, effectiveBalance uint32) [32]byte {
	data, err := leafABI.Pack(clusterID, effectiveBalance)
	if err != nil {
		panic(err)
	}
	return crypto.Keccak256Hash(crypto.Keccak256(data))
}

func mustType(t string) abi.Type {
	typ, err := abi.NewType(t, "", nil)
	if err != nil {
		panic(err)
	}
	return typ
}

package merkle

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGoldenVector_OpenZeppelin validates our implementation against OpenZeppelin StandardMerkleTree.
//
// To verify these values independently, install dependencies and run:
//
//	npm install @openzeppelin/merkle-tree viem
//	node -e "
//	const { StandardMerkleTree } = require('@openzeppelin/merkle-tree');
//	const values = [
//	  ['0x0000000000000000000000000000000000000000000000000000000000000001', 32],
//	  ['0x0000000000000000000000000000000000000000000000000000000000000002', 64],
//	];
//	const tree = StandardMerkleTree.of(values, ['bytes32', 'uint32']);
//	console.log('Root:', tree.root);
//	for (const [i, v] of tree.entries()) {
//	  console.log('Leaf', i, ':', tree.leafHash(v));
//	  console.log('Proof:', tree.getProof(i));
//	}
//	"
//
// Expected output should match the values in this test.
func TestGoldenVector_OpenZeppelin(t *testing.T) {
	// Test data: two clusters with simple IDs and balances
	cluster1 := [32]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	cluster2 := [32]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2}

	clusters := map[[32]byte]uint32{
		cluster1: 32,
		cluster2: 64,
	}

	tree := NewTree(clusters)

	// Expected leaf hashes (double-hashed: keccak256(keccak256(abi.encode(bytes32, uint32))))
	// ABI encoding for (bytes32, uint32):
	//   cluster1: 0x0...01 || 0x0...20 (32 as uint256 = 0x20)
	//   cluster2: 0x0...02 || 0x0...40 (64 as uint256 = 0x40)
	expectedLeaf1, _ := hex.DecodeString("a705cc086e204a80e7ca233181d1a0107b3a78ca619149dde90edd19aa2cfce6")
	expectedLeaf2, _ := hex.DecodeString("a423cb0da1ad54d486070305ffce6a6469821ec5d45850764860bede0ee3a70d")

	// Verify leaf hashes
	leaf1Hash := HashLeaf(cluster1, 32)
	leaf2Hash := HashLeaf(cluster2, 64)

	require.Equal(t, expectedLeaf1, leaf1Hash[:], "cluster1 leaf hash mismatch")
	require.Equal(t, expectedLeaf2, leaf2Hash[:], "cluster2 leaf hash mismatch")

	// Expected root (computed from leaves sorted by hash)
	// Sort order: a423... < a705..., so [cluster2, cluster1]
	// Root = keccak256(concat(min, max)) = keccak256(a423... || a705...)
	expectedRoot, _ := hex.DecodeString("deebf47271c6875e1487fbba2d3f982b35d883ce213b7dceb4139765ac2821c7")

	require.Equal(t, expectedRoot, tree.Root[:], "root mismatch")

	// Verify proofs work
	proof1, err := tree.GetProof(cluster1)
	require.NoError(t, err)
	require.Len(t, proof1, 1)

	proof2, err := tree.GetProof(cluster2)
	require.NoError(t, err)
	require.Len(t, proof2, 1)

	// Each proof contains the sibling's hash
	require.Equal(t, expectedLeaf2, proof1[0][:], "proof1 should contain leaf2 hash")
	require.Equal(t, expectedLeaf1, proof2[0][:], "proof2 should contain leaf1 hash")
}

func TestGoldenVector_EmptyTree(t *testing.T) {
	tree := NewTree(nil)

	// keccak256("") = 0xc5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470
	expectedRoot, _ := hex.DecodeString("c5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470")

	require.Equal(t, expectedRoot, tree.Root[:], "empty tree root mismatch")
}

func TestGoldenVector_SingleLeaf(t *testing.T) {
	cluster := [32]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	clusters := map[[32]byte]uint32{cluster: 32}

	tree := NewTree(clusters)

	// Single leaf = root (double hash of abi.encode(0x...01, 32))
	expectedRoot, _ := hex.DecodeString("a705cc086e204a80e7ca233181d1a0107b3a78ca619149dde90edd19aa2cfce6")

	require.Equal(t, expectedRoot, tree.Root[:], "single leaf root mismatch")
}

// TestGoldenVector_ThreeLeaves tests an odd number of leaves (requires duplication).
func TestGoldenVector_ThreeLeaves(t *testing.T) {
	cluster1 := [32]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	cluster2 := [32]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2}
	cluster3 := [32]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 3}

	clusters := map[[32]byte]uint32{
		cluster1: 32,
		cluster2: 64,
		cluster3: 96,
	}

	tree := NewTree(clusters)

	// Verify all proofs are valid
	for _, leaf := range tree.Leaves() {
		proof, err := tree.GetProof(leaf.ClusterID)
		require.NoError(t, err)
		require.NotEmpty(t, proof)

		// Verify proof by recomputing root
		computed := leaf.Hash
		for _, sibling := range proof {
			computed = hashPairForTest(computed, sibling)
		}
		require.Equal(t, tree.Root, computed, "proof verification failed for cluster %x", leaf.ClusterID)
	}
}

func hashPairForTest(a, b [32]byte) [32]byte {
	if bytes.Compare(a[:], b[:]) > 0 {
		a, b = b, a
	}
	return hashPair(a, b)
}

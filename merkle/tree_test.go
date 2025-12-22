package merkle

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

func TestNewTree_Empty(t *testing.T) {
	tree := NewTree(nil)
	expected, _ := hex.DecodeString("c5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470")
	require.Equal(t, expected, tree.Root[:])
}

func TestNewTree_SingleCluster(t *testing.T) {
	clusterID := [32]byte{0x12, 0x34}
	clusters := map[[32]byte]uint32{clusterID: 32}

	tree := NewTree(clusters)
	require.Equal(t, HashLeaf(clusterID, 32), tree.Root)
}

func TestNewTree_Deterministic(t *testing.T) {
	clusters := map[[32]byte]uint32{
		{0x11}: 32,
		{0x22}: 31,
		{0x33}: 32,
	}

	root1 := NewTree(clusters).Root
	root2 := NewTree(clusters).Root
	require.Equal(t, root1, root2)
}

func TestNewTree_VariousSizes(t *testing.T) {
	for _, n := range []int{2, 3, 4, 5, 7, 8, 100} {
		t.Run("", func(t *testing.T) {
			clusters := make(map[[32]byte]uint32)
			for i := 0; i < n; i++ {
				clusters[[32]byte{byte(i)}] = 32
			}

			root1 := NewTree(clusters).Root
			root2 := NewTree(clusters).Root
			require.Equal(t, root1, root2)
			require.NotEqual(t, root1, [32]byte{})
		})
	}
}

func TestNewTree_LeavesSortedByHash(t *testing.T) {
	clusters := map[[32]byte]uint32{
		{0x11}: 32,
		{0x22}: 31,
		{0x33}: 32,
	}

	tree := NewTree(clusters)
	require.Len(t, tree.Leaves, 3)

	for i := 1; i < len(tree.Leaves); i++ {
		require.True(t, bytes.Compare(tree.Leaves[i-1].Hash[:], tree.Leaves[i].Hash[:]) < 0)
	}
}

func TestGetProof_Verify(t *testing.T) {
	clusters := map[[32]byte]uint32{
		{0x11}: 32,
		{0x22}: 31,
		{0x33}: 32,
	}

	tree := NewTree(clusters)

	for _, leaf := range tree.Leaves {
		proof, err := tree.GetProof(leaf.ClusterID)
		require.NoError(t, err)

		computed := verifyProof(leaf.Hash, proof)
		require.Equal(t, tree.Root, computed)
	}
}

func TestGetProof_SingleCluster(t *testing.T) {
	clusters := map[[32]byte]uint32{{0x11}: 32}
	tree := NewTree(clusters)

	proof, err := tree.GetProof([32]byte{0x11})
	require.NoError(t, err)
	require.Empty(t, proof)

	computed := verifyProof(tree.Leaves[0].Hash, proof)
	require.Equal(t, tree.Root, computed)
}

func TestGetProof_NotFound(t *testing.T) {
	tree := NewTree(map[[32]byte]uint32{{0x11}: 32})

	_, err := tree.GetProof([32]byte{0xFF})
	require.Error(t, err)
}

func TestGetProof_LargeTree(t *testing.T) {
	clusters := make(map[[32]byte]uint32)
	for i := 0; i < 100; i++ {
		clusters[[32]byte{byte(i), byte(i >> 8)}] = uint32(32 + i)
	}

	tree := NewTree(clusters)

	for _, leaf := range tree.Leaves {
		proof, err := tree.GetProof(leaf.ClusterID)
		require.NoError(t, err)

		computed := verifyProof(leaf.Hash, proof)
		require.Equal(t, tree.Root, computed)
	}
}

func verifyProof(leafHash [32]byte, proof [][32]byte) [32]byte {
	h := leafHash
	for _, sibling := range proof {
		h = hashPairTest(h, sibling)
	}
	return h
}

func hashPairTest(a, b [32]byte) [32]byte {
	if bytes.Compare(a[:], b[:]) > 0 {
		a, b = b, a
	}
	var buf [64]byte
	copy(buf[:32], a[:])
	copy(buf[32:], b[:])
	return crypto.Keccak256Hash(buf[:])
}

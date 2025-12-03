package merkle

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
)

func TestBuildMerkleTree_Empty(t *testing.T) {
	clusters := map[[32]byte]uint64{}

	root := BuildMerkleTree(clusters)

	// Empty tree should return keccak256([])
	// This is the standard approach (not using empty leaf)
	expected := "c5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470"
	expectedBytes, _ := hex.DecodeString(expected)
	var expectedHash [32]byte
	copy(expectedHash[:], expectedBytes)

	if root != expectedHash {
		t.Errorf("Empty tree root mismatch")
		t.Logf("Expected: 0x%x", expectedHash)
		t.Logf("Got: 0x%x", root)
	}

	t.Logf("Empty tree root: 0x%x", root)
}

func TestBuildMerkleTree_SingleCluster(t *testing.T) {
	clusterID := [32]byte{0x12, 0x34}
	clusters := map[[32]byte]uint64{
		clusterID: 32000000000,
	}

	root := BuildMerkleTree(clusters)

	// Single cluster should be: keccak256(leaf || emptyLeaf)
	leaf := EncodeMerkleLeaf(clusterID, 32000000000)
	emptyLeaf := EncodeEmptyLeaf()

	combined := make([]byte, 64)
	copy(combined[0:32], leaf[:])
	copy(combined[32:64], emptyLeaf[:])

	// This is NOT the final root - just for reference
	// expectedRoot := crypto.Keccak256Hash(combined)

	t.Logf("Single cluster root: 0x%x", root)
	t.Logf("Leaf: 0x%x", leaf)
	t.Logf("Empty leaf: 0x%x", emptyLeaf)

	// TODO: Verify against Solidity test
}

func TestBuildMerkleTree_TwoClusters(t *testing.T) {
	cluster1 := [32]byte{0x11, 0x11}
	cluster2 := [32]byte{0x22, 0x22}

	clusters := map[[32]byte]uint64{
		cluster1: 32000000000,
		cluster2: 31000000000,
	}

	root := BuildMerkleTree(clusters)

	t.Logf("Two clusters root: 0x%x", root)

	// Verify determinism
	root2 := BuildMerkleTree(clusters)
	if root != root2 {
		t.Error("BuildMerkleTree is not deterministic")
	}

	// TODO: Verify against Solidity test
}

func TestBuildMerkleTree_ThreeClusters(t *testing.T) {
	// Three clusters should trigger empty leaf rule
	cluster1 := [32]byte{0x11}
	cluster2 := [32]byte{0x22}
	cluster3 := [32]byte{0x33}

	clusters := map[[32]byte]uint64{
		cluster1: 32000000000,
		cluster2: 31000000000,
		cluster3: 32000000000,
	}

	root := BuildMerkleTree(clusters)

	t.Logf("Three clusters root: 0x%x", root)

	// Verify determinism
	root2 := BuildMerkleTree(clusters)
	if root != root2 {
		t.Error("BuildMerkleTree is not deterministic")
	}

	// TODO: Verify against Solidity test
}

func TestBuildMerkleTree_Sorting(t *testing.T) {
	// Create clusters with IDs in different order
	cluster1 := [32]byte{0xAA} // Higher
	cluster2 := [32]byte{0x11} // Lower

	clusters := map[[32]byte]uint64{
		cluster1: 32000000000,
		cluster2: 32000000000,
	}

	root := BuildMerkleTree(clusters)

	// Should be same regardless of iteration order
	// (Go maps have random iteration order)
	root2 := BuildMerkleTree(clusters)

	if root != root2 {
		t.Error("BuildMerkleTree sorting is not working correctly")
	}

	t.Logf("Root with sorting: 0x%x", root)
}

func TestBuildMerkleTree_TestDataFixtures(t *testing.T) {
	// Use our test data cluster IDs (computed with keccak256)
	cluster1Hex := "b183c42279b4dc3eb381352db3458ae66a3439765e4f880a027f62ac2c4edba9"
	cluster2Hex := "44ab2ba437cef9cb17bb6bc6af7d87715b9a3e245fbb153e66b09bb79697d316"
	cluster3Hex := "a1ea9fe3f3ba4ba4d19a681b639f7033740e55371ee0f00c2e32c15b4fe3c468"

	cluster1Bytes, _ := hex.DecodeString(cluster1Hex)
	cluster2Bytes, _ := hex.DecodeString(cluster2Hex)
	cluster3Bytes, _ := hex.DecodeString(cluster3Hex)

	var cluster1, cluster2, cluster3 [32]byte
	copy(cluster1[:], cluster1Bytes)
	copy(cluster2[:], cluster2Bytes)
	copy(cluster3[:], cluster3Bytes)

	clusters := map[[32]byte]uint64{
		cluster1: 64000000000, // 64 ETH
		cluster2: 31000000000, // 31 ETH
		cluster3: 32000000000, // 32 ETH
	}

	root := BuildMerkleTree(clusters)

	t.Log("Merkle tree from test data:")
	t.Logf("  Cluster 1: 0x%s (64 ETH)", cluster1Hex)
	t.Logf("  Cluster 2: 0x%s (31 ETH)", cluster2Hex)
	t.Logf("  Cluster 3: 0x%s (32 ETH)", cluster3Hex)
	t.Logf("  Merkle Root: 0x%x", root)

	// TODO: Verify against Solidity test with same data
}

func TestBuildMerkleTree_DifferentOrderSameRoot(t *testing.T) {
	cluster1 := [32]byte{0x11}
	cluster2 := [32]byte{0x22}
	cluster3 := [32]byte{0x33}

	// Create same clusters in different orders
	clusters1 := map[[32]byte]uint64{
		cluster1: 32000000000,
		cluster2: 31000000000,
		cluster3: 32000000000,
	}

	clusters2 := map[[32]byte]uint64{
		cluster3: 32000000000,
		cluster1: 32000000000,
		cluster2: 31000000000,
	}

	root1 := BuildMerkleTree(clusters1)
	root2 := BuildMerkleTree(clusters2)

	if root1 != root2 {
		t.Error("Different cluster insertion order produced different roots")
		t.Logf("Root 1: 0x%x", root1)
		t.Logf("Root 2: 0x%x", root2)
	}
}

func TestBuildMerkleTree_PowerOfTwo(t *testing.T) {
	// Test with 2, 4, 8 clusters (powers of 2 - no empty leaf needed except for initial pairing)
	testCases := []int{2, 4, 8}

	for _, numClusters := range testCases {
		t.Run(string(rune(numClusters))+" clusters", func(t *testing.T) {
			clusters := make(map[[32]byte]uint64)

			for i := 0; i < numClusters; i++ {
				var clusterID [32]byte
				clusterID[0] = byte(i)
				clusters[clusterID] = 32000000000
			}

			root := BuildMerkleTree(clusters)

			t.Logf("%d clusters root: 0x%x", numClusters, root)

			// Verify determinism
			root2 := BuildMerkleTree(clusters)
			if root != root2 {
				t.Error("Not deterministic")
			}
		})
	}
}

func TestBuildMerkleTree_NonPowerOfTwo(t *testing.T) {
	// Test with 3, 5, 7 clusters (requires empty leaf padding)
	testCases := []int{3, 5, 7}

	for _, numClusters := range testCases {
		t.Run(string(rune(numClusters))+" clusters", func(t *testing.T) {
			clusters := make(map[[32]byte]uint64)

			for i := 0; i < numClusters; i++ {
				var clusterID [32]byte
				clusterID[0] = byte(i)
				clusters[clusterID] = 32000000000
			}

			root := BuildMerkleTree(clusters)

			t.Logf("%d clusters root: 0x%x", numClusters, root)

			// Verify determinism
			root2 := BuildMerkleTree(clusters)
			if root != root2 {
				t.Error("Not deterministic")
			}
		})
	}
}

func TestBuildMerkleTreeWithProofs_Structure(t *testing.T) {
	cluster1 := [32]byte{0x11}
	cluster2 := [32]byte{0x22}
	cluster3 := [32]byte{0x33}

	clusters := map[[32]byte]uint64{
		cluster1: 32000000000,
		cluster2: 31000000000,
		cluster3: 32000000000,
	}

	tree := BuildMerkleTreeWithProofs(clusters)

	// Verify tree structure
	if tree == nil {
		t.Fatal("Tree is nil")
	}

	// Should have 3 leaves
	if len(tree.Leaves) != 3 {
		t.Errorf("Expected 3 leaves, got %d", len(tree.Leaves))
	}

	// Leaves should be sorted by clusterID
	for i := 1; i < len(tree.Leaves); i++ {
		if tree.Leaves[i-1].ClusterID[0] > tree.Leaves[i].ClusterID[0] {
			t.Error("Leaves are not sorted by clusterID")
		}
	}

	// Should have multiple layers
	if len(tree.Layers) < 2 {
		t.Errorf("Expected at least 2 layers, got %d", len(tree.Layers))
	}

	// Layer 0 should have 3 hashes (leaves)
	if len(tree.Layers[0]) != 3 {
		t.Errorf("Layer 0 should have 3 hashes, got %d", len(tree.Layers[0]))
	}

	// Last layer should have 1 hash (root)
	if len(tree.Layers[len(tree.Layers)-1]) != 1 {
		t.Errorf("Last layer should have 1 hash (root), got %d", len(tree.Layers[len(tree.Layers)-1]))
	}

	// Root should match
	if tree.Root != tree.Layers[len(tree.Layers)-1][0] {
		t.Error("Root doesn't match last layer")
	}

	t.Logf("Tree with %d leaves, %d layers", len(tree.Leaves), len(tree.Layers))
	t.Logf("Root: 0x%x", tree.Root)
}

func TestBuildMerkleTreeWithProofs_MatchesBuildMerkleTree(t *testing.T) {
	cluster1 := [32]byte{0x11}
	cluster2 := [32]byte{0x22}
	cluster3 := [32]byte{0x33}

	clusters := map[[32]byte]uint64{
		cluster1: 32000000000,
		cluster2: 31000000000,
		cluster3: 32000000000,
	}

	// Both functions should produce the same root
	rootSimple := BuildMerkleTree(clusters)
	tree := BuildMerkleTreeWithProofs(clusters)

	if rootSimple != tree.Root {
		t.Error("BuildMerkleTree and BuildMerkleTreeWithProofs produce different roots")
		t.Logf("BuildMerkleTree: 0x%x", rootSimple)
		t.Logf("BuildMerkleTreeWithProofs: 0x%x", tree.Root)
	}
}

func TestGetProof_VerifyProof(t *testing.T) {
	cluster1 := [32]byte{0x11}
	cluster2 := [32]byte{0x22}
	cluster3 := [32]byte{0x33}

	clusters := map[[32]byte]uint64{
		cluster1: 32000000000,
		cluster2: 31000000000,
		cluster3: 32000000000,
	}

	tree := BuildMerkleTreeWithProofs(clusters)

	// Get proof for each cluster and verify
	for _, leaf := range tree.Leaves {
		proof, err := tree.GetProof(leaf.ClusterID)
		if err != nil {
			t.Errorf("Failed to get proof for cluster %x: %v", leaf.ClusterID[:8], err)
			continue
		}

		// Verify proof by reconstructing root
		computedRoot := verifyProof(leaf.Hash, proof)
		if computedRoot != tree.Root {
			t.Errorf("Proof verification failed for cluster %x", leaf.ClusterID[:8])
			t.Logf("Expected root: 0x%x", tree.Root)
			t.Logf("Computed root: 0x%x", computedRoot)
		}

		t.Logf("Cluster %x: proof has %d siblings", leaf.ClusterID[:8], len(proof))
	}
}

func TestGetProof_NotFound(t *testing.T) {
	cluster1 := [32]byte{0x11}

	clusters := map[[32]byte]uint64{
		cluster1: 32000000000,
	}

	tree := BuildMerkleTreeWithProofs(clusters)

	// Try to get proof for non-existent cluster
	nonExistent := [32]byte{0xFF}
	_, err := tree.GetProof(nonExistent)
	if err == nil {
		t.Error("Expected error for non-existent cluster")
	}
}

func TestGetProof_SingleCluster(t *testing.T) {
	cluster1 := [32]byte{0x11}

	clusters := map[[32]byte]uint64{
		cluster1: 32000000000,
	}

	tree := BuildMerkleTreeWithProofs(clusters)

	proof, err := tree.GetProof(cluster1)
	if err != nil {
		t.Fatalf("Failed to get proof: %v", err)
	}

	// Single cluster tree should have 1 proof element (the duplicate)
	t.Logf("Single cluster proof length: %d", len(proof))

	// Verify proof
	leaf := tree.Leaves[0]
	computedRoot := verifyProof(leaf.Hash, proof)
	if computedRoot != tree.Root {
		t.Error("Proof verification failed for single cluster")
		t.Logf("Expected root: 0x%x", tree.Root)
		t.Logf("Computed root: 0x%x", computedRoot)
	}
}

func TestGetProof_LargeTree(t *testing.T) {
	// Test with 100 clusters
	clusters := make(map[[32]byte]uint64)
	for i := 0; i < 100; i++ {
		var clusterID [32]byte
		clusterID[0] = byte(i)
		clusterID[1] = byte(i >> 8)
		clusters[clusterID] = uint64(32000000000 + i*1000000000)
	}

	tree := BuildMerkleTreeWithProofs(clusters)

	// Verify proof for each cluster
	for _, leaf := range tree.Leaves {
		proof, err := tree.GetProof(leaf.ClusterID)
		if err != nil {
			t.Errorf("Failed to get proof for cluster %x: %v", leaf.ClusterID[:8], err)
			continue
		}

		computedRoot := verifyProof(leaf.Hash, proof)
		if computedRoot != tree.Root {
			t.Errorf("Proof verification failed for cluster %x", leaf.ClusterID[:8])
		}
	}

	t.Logf("Verified proofs for %d clusters", len(clusters))
	t.Logf("Tree has %d layers", len(tree.Layers))
}

// verifyProof reconstructs the root from a leaf hash and proof.
// Uses OpenZeppelin-style sorted sibling hashing.
func verifyProof(leafHash [32]byte, proof [][32]byte) [32]byte {
	computedHash := leafHash

	for _, sibling := range proof {
		computedHash = hashPair(computedHash, sibling)
	}

	return computedHash
}

// hashPair hashes two nodes using OpenZeppelin sorting (smaller first).
func hashPair(a, b [32]byte) [32]byte {
	// OpenZeppelin-style: sort siblings before hashing
	if bytes.Compare(a[:], b[:]) > 0 {
		a, b = b, a
	}

	combined := make([]byte, 64)
	copy(combined[0:32], a[:])
	copy(combined[32:64], b[:])

	return crypto.Keccak256Hash(combined)
}

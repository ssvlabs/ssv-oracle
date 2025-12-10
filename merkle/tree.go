package merkle

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/ethereum/go-ethereum/crypto"
)

// Leaf represents a single leaf in the merkle tree.
type Leaf struct {
	ClusterID        [32]byte
	EffectiveBalance uint64
	Hash             [32]byte
}

// MerkleTree holds the full tree structure for proof generation.
type MerkleTree struct {
	Root   [32]byte
	Leaves []Leaf
	Layers [][][32]byte
}

// BuildMerkleTree constructs a Merkle tree and returns the root.
func BuildMerkleTree(clusters map[[32]byte]uint64) [32]byte {
	tree := BuildMerkleTreeWithProofs(clusters)
	return tree.Root
}

// BuildMerkleTreeWithProofs constructs a Merkle tree preserving all layers.
// Uses Bitcoin/OpenZeppelin standard: duplicate odd leaves, sort siblings before hashing.
func BuildMerkleTreeWithProofs(clusters map[[32]byte]uint64) *MerkleTree {
	if len(clusters) == 0 {
		return &MerkleTree{
			Root:   crypto.Keccak256Hash([]byte{}),
			Leaves: nil,
			Layers: nil,
		}
	}

	leaves := encodeLeaves(clusters)
	sortLeavesByClusterID(leaves)

	hashes := extractHashes(leaves)
	layers := buildLayers(hashes)

	return &MerkleTree{
		Root:   layers[len(layers)-1][0],
		Leaves: leaves,
		Layers: layers,
	}
}

// GetProof returns the merkle proof for a cluster (sibling hashes from leaf to root).
func (t *MerkleTree) GetProof(clusterID [32]byte) ([][32]byte, error) {
	leafIndex := t.findLeafIndex(clusterID)
	if leafIndex == -1 {
		return nil, fmt.Errorf("cluster %x not found in tree", clusterID)
	}

	var proof [][32]byte
	index := leafIndex

	for layer := 0; layer < len(t.Layers)-1; layer++ {
		sibling := t.getSibling(layer, index)
		proof = append(proof, sibling)
		index /= 2
	}

	return proof, nil
}

func (t *MerkleTree) findLeafIndex(clusterID [32]byte) int {
	for i, leaf := range t.Leaves {
		if leaf.ClusterID == clusterID {
			return i
		}
	}
	return -1
}

func (t *MerkleTree) getSibling(layer, index int) [32]byte {
	levelHashes := t.Layers[layer]

	var siblingIndex int
	if index%2 == 0 {
		siblingIndex = index + 1
	} else {
		siblingIndex = index - 1
	}

	if siblingIndex < len(levelHashes) {
		return levelHashes[siblingIndex]
	}
	return levelHashes[index]
}

func buildLayers(hashes [][32]byte) [][][32]byte {
	var layers [][][32]byte
	layers = append(layers, hashes)

	current := hashes
	for len(current) > 1 {
		next := buildNextLayer(current)
		layers = append(layers, next)
		current = next
	}

	return layers
}

func buildNextLayer(current [][32]byte) [][32]byte {
	next := make([][32]byte, 0, (len(current)+1)/2)

	for i := 0; i < len(current); i += 2 {
		left := current[i]
		right := left
		if i+1 < len(current) {
			right = current[i+1]
		}

		// OpenZeppelin: sort siblings before hashing
		if bytes.Compare(left[:], right[:]) > 0 {
			left, right = right, left
		}

		combined := make([]byte, 64)
		copy(combined[0:32], left[:])
		copy(combined[32:64], right[:])

		next = append(next, crypto.Keccak256Hash(combined))
	}

	return next
}

func encodeLeaves(clusters map[[32]byte]uint64) []Leaf {
	leaves := make([]Leaf, 0, len(clusters))
	for clusterID, balance := range clusters {
		leaves = append(leaves, Leaf{
			ClusterID:        clusterID,
			EffectiveBalance: balance,
			Hash:             EncodeMerkleLeaf(clusterID, balance),
		})
	}
	return leaves
}

func extractHashes(leaves []Leaf) [][32]byte {
	hashes := make([][32]byte, len(leaves))
	for i, leaf := range leaves {
		hashes[i] = leaf.Hash
	}
	return hashes
}

func sortLeavesByClusterID(leaves []Leaf) {
	sort.Slice(leaves, func(i, j int) bool {
		return bytes.Compare(leaves[i].ClusterID[:], leaves[j].ClusterID[:]) < 0
	})
}

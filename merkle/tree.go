package merkle

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/ethereum/go-ethereum/crypto"
)

// MerkleTree holds the full tree structure for proof generation.
type MerkleTree struct {
	Root   [32]byte
	Leaves []Leaf       // Sorted leaves (by clusterID)
	Layers [][][32]byte // All tree layers [0]=leaves, [len-1]=root
}

// Leaf represents a single leaf in the merkle tree.
type Leaf struct {
	ClusterID        [32]byte
	EffectiveBalance uint64
	Hash             [32]byte
}

// BuildMerkleTree constructs a Merkle tree from cluster balances.
// Returns the Merkle root.
func BuildMerkleTree(clusters map[[32]byte]uint64) [32]byte {
	tree := BuildMerkleTreeWithProofs(clusters)
	return tree.Root
}

// BuildMerkleTreeWithProofs constructs a Merkle tree and preserves all layers for proof generation.
//
// Algorithm (following Bitcoin/OpenZeppelin standard):
// 1. Encode each (clusterID, balance) as a leaf
// 2. Sort leaves by clusterID ascending
// 3. Build binary tree bottom-up with duplicate strategy
// 4. Use OpenZeppelin sibling sorting (sort siblings before hashing)
func BuildMerkleTreeWithProofs(clusters map[[32]byte]uint64) *MerkleTree {
	// Special case: empty tree
	if len(clusters) == 0 {
		return &MerkleTree{
			Root:   crypto.Keccak256Hash([]byte{}),
			Leaves: nil,
			Layers: nil,
		}
	}

	// 1. Encode leaves
	leaves := make([]Leaf, 0, len(clusters))
	for clusterID, balance := range clusters {
		leaves = append(leaves, Leaf{
			ClusterID:        clusterID,
			EffectiveBalance: balance,
			Hash:             EncodeMerkleLeaf(clusterID, balance),
		})
	}

	// 2. Sort leaves by clusterID ascending
	sort.Slice(leaves, func(i, j int) bool {
		return bytes.Compare(leaves[i].ClusterID[:], leaves[j].ClusterID[:]) < 0
	})

	// 3. Extract hashes for tree construction
	hashes := make([][32]byte, len(leaves))
	for i, leaf := range leaves {
		hashes[i] = leaf.Hash
	}

	// 4. Build tree layers (for proof generation)
	var layers [][][32]byte
	layers = append(layers, hashes) // Layer 0 = leaf hashes

	currentLevel := hashes
	for len(currentLevel) > 1 {
		nextLevel := make([][32]byte, 0, (len(currentLevel)+1)/2)

		for i := 0; i < len(currentLevel); i += 2 {
			left := currentLevel[i]
			var right [32]byte

			// Duplicate last node if odd count (standard approach)
			if i+1 < len(currentLevel) {
				right = currentLevel[i+1]
			} else {
				right = left
			}

			// OpenZeppelin sibling sorting: sort siblings before hashing
			// This ensures h(a,b) = h(b,a) for verification
			if bytes.Compare(left[:], right[:]) > 0 {
				left, right = right, left
			}

			// Concatenate and hash: keccak256(left || right)
			combined := make([]byte, 64)
			copy(combined[0:32], left[:])
			copy(combined[32:64], right[:])

			parent := crypto.Keccak256Hash(combined)
			nextLevel = append(nextLevel, parent)
		}

		layers = append(layers, nextLevel)
		currentLevel = nextLevel
	}

	return &MerkleTree{
		Root:   currentLevel[0],
		Leaves: leaves,
		Layers: layers,
	}
}

// GetProof returns the merkle proof for a cluster (sibling hashes from leaf to root).
// Returns error if cluster is not in the tree.
func (t *MerkleTree) GetProof(clusterID [32]byte) ([][32]byte, error) {
	leafIndex := -1
	for i, leaf := range t.Leaves {
		if leaf.ClusterID == clusterID {
			leafIndex = i
			break
		}
	}
	if leafIndex == -1 {
		return nil, fmt.Errorf("cluster %x not found in tree", clusterID)
	}

	var proof [][32]byte
	index := leafIndex

	for layer := 0; layer < len(t.Layers)-1; layer++ {
		levelHashes := t.Layers[layer]

		var siblingIndex int
		if index%2 == 0 {
			siblingIndex = index + 1
		} else {
			siblingIndex = index - 1
		}

		var sibling [32]byte
		if siblingIndex < len(levelHashes) {
			sibling = levelHashes[siblingIndex]
		} else {
			sibling = levelHashes[index]
		}

		proof = append(proof, sibling)
		index = index / 2
	}

	return proof, nil
}

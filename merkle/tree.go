package merkle

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/ethereum/go-ethereum/crypto"
)

// Leaf represents a merkle tree leaf with cluster data and its hash.
type Leaf struct {
	ClusterID        [32]byte
	EffectiveBalance uint32
	Hash             [32]byte
}

// Tree holds the structure for root computation and proof generation.
type Tree struct {
	Root   [32]byte
	Leaves []Leaf
	layers [][][32]byte
}

// NewTree builds a merkle tree from cluster balances.
// Implements OpenZeppelin StandardMerkleTree: double-hashed leaves, sorted by hash,
// with sibling pairs sorted before hashing.
func NewTree(clusters map[[32]byte]uint32) *Tree {
	if len(clusters) == 0 {
		return &Tree{Root: crypto.Keccak256Hash(nil)}
	}

	leaves := makeLeaves(clusters)
	sort.Slice(leaves, func(i, j int) bool {
		return bytes.Compare(leaves[i].Hash[:], leaves[j].Hash[:]) < 0
	})

	if len(leaves) == 1 {
		return &Tree{Root: leaves[0].Hash, Leaves: leaves}
	}

	layers := buildLayers(leaves)
	return &Tree{
		Root:   layers[len(layers)-1][0],
		Leaves: leaves,
		layers: layers,
	}
}

// GetProof returns the merkle proof (sibling hashes from leaf to root).
func (t *Tree) GetProof(clusterID [32]byte) ([][32]byte, error) {
	idx := t.findLeaf(clusterID)
	if idx < 0 {
		return nil, fmt.Errorf("cluster %x not found", clusterID)
	}
	if len(t.layers) == 0 {
		return nil, nil
	}

	proof := make([][32]byte, 0, len(t.layers)-1)
	for layer := 0; layer < len(t.layers)-1; layer++ {
		proof = append(proof, t.sibling(layer, idx))
		idx /= 2
	}
	return proof, nil
}

func (t *Tree) findLeaf(clusterID [32]byte) int {
	for i, leaf := range t.Leaves {
		if leaf.ClusterID == clusterID {
			return i
		}
	}
	return -1
}

func (t *Tree) sibling(layer, idx int) [32]byte {
	hashes := t.layers[layer]
	if idx%2 == 0 {
		if idx+1 < len(hashes) {
			return hashes[idx+1]
		}
		return hashes[idx]
	}
	return hashes[idx-1]
}

func makeLeaves(clusters map[[32]byte]uint32) []Leaf {
	leaves := make([]Leaf, 0, len(clusters))
	for id, balance := range clusters {
		leaves = append(leaves, Leaf{
			ClusterID:        id,
			EffectiveBalance: balance,
			Hash:             HashLeaf(id, balance),
		})
	}
	return leaves
}

func buildLayers(leaves []Leaf) [][][32]byte {
	hashes := make([][32]byte, len(leaves))
	for i, leaf := range leaves {
		hashes[i] = leaf.Hash
	}

	layers := [][][32]byte{hashes}
	for len(hashes) > 1 {
		hashes = hashLevel(hashes)
		layers = append(layers, hashes)
	}
	return layers
}

func hashLevel(hashes [][32]byte) [][32]byte {
	next := make([][32]byte, (len(hashes)+1)/2)
	for i := 0; i < len(hashes); i += 2 {
		left, right := hashes[i], hashes[i]
		if i+1 < len(hashes) {
			right = hashes[i+1]
		}
		next[i/2] = hashPair(left, right)
	}
	return next
}

func hashPair(a, b [32]byte) [32]byte {
	if bytes.Compare(a[:], b[:]) > 0 {
		a, b = b, a
	}
	var buf [64]byte
	copy(buf[:32], a[:])
	copy(buf[32:], b[:])
	return crypto.Keccak256Hash(buf[:])
}

package merkle

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHashLeaf(t *testing.T) {
	clusterID := [32]byte{0x12, 0x34, 0x56}

	hash1 := HashLeaf(clusterID, 32)
	hash2 := HashLeaf(clusterID, 32)
	require.Equal(t, hash1, hash2, "should be deterministic")

	hash3 := HashLeaf(clusterID, 64)
	require.NotEqual(t, hash1, hash3, "different balance should produce different hash")

	require.NotEqual(t, hash1, [32]byte{}, "should not produce zero hash")
}

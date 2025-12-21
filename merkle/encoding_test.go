package merkle

import (
	"encoding/hex"
	"testing"
)

func TestEncodeMerkleLeaf(t *testing.T) {
	tests := []struct {
		name             string
		clusterID        string
		effectiveBalance uint32
	}{
		{
			name:             "cluster with 32 ETH",
			clusterID:        "b82eab112a1f680c7e7dd45c0ca182aa2904ec64422dccbc0c5855925cdf43a7",
			effectiveBalance: 32,
		},
		{
			name:             "cluster with 64 ETH",
			clusterID:        "b82eab112a1f680c7e7dd45c0ca182aa2904ec64422dccbc0c5855925cdf43a7",
			effectiveBalance: 64,
		},
		{
			name:             "cluster with 320 ETH (10 validators)",
			clusterID:        "2d850ebc13f5434cb9b72638555561ab569c5ef9c0f321805a25ff545b3d7430",
			effectiveBalance: 320,
		},
		{
			name:             "zero balance",
			clusterID:        "b82eab112a1f680c7e7dd45c0ca182aa2904ec64422dccbc0c5855925cdf43a7",
			effectiveBalance: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clusterIDBytes, err := hex.DecodeString(tt.clusterID)
			if err != nil {
				t.Fatalf("Failed to decode cluster ID: %v", err)
			}
			var clusterID [32]byte
			copy(clusterID[:], clusterIDBytes)

			result := EncodeMerkleLeaf(clusterID, tt.effectiveBalance)

			t.Logf("Cluster ID: 0x%s", tt.clusterID)
			t.Logf("Balance: %d ETH", tt.effectiveBalance)
			t.Logf("Leaf Hash: 0x%x", result)

			zeroHash := [32]byte{}
			if tt.effectiveBalance > 0 && result == zeroHash {
				t.Error("Got zero hash for non-zero balance")
			}
		})
	}
}

func TestEncodeMerkleLeaf_Deterministic(t *testing.T) {
	clusterID := [32]byte{0x12, 0x34, 0x56}
	balance := uint32(32)

	result1 := EncodeMerkleLeaf(clusterID, balance)
	result2 := EncodeMerkleLeaf(clusterID, balance)

	if result1 != result2 {
		t.Error("EncodeMerkleLeaf is not deterministic")
	}
}

func TestEncodeMerkleLeaf_DifferentBalancesDifferentHashes(t *testing.T) {
	clusterID := [32]byte{0x12, 0x34, 0x56}

	hash1 := EncodeMerkleLeaf(clusterID, 32)
	hash2 := EncodeMerkleLeaf(clusterID, 64)

	if hash1 == hash2 {
		t.Error("Different balances should produce different leaf hashes")
	}

	t.Logf("Hash (32 ETH): 0x%x", hash1)
	t.Logf("Hash (64 ETH): 0x%x", hash2)
}

package merkle

import (
	"encoding/hex"
	"testing"
)

func TestEncodeMerkleLeaf(t *testing.T) {
	tests := []struct {
		name             string
		clusterID        string
		effectiveBalance uint64
		expectedHash     string
	}{
		{
			name:             "cluster with 32 ETH",
			clusterID:        "b82eab112a1f680c7e7dd45c0ca182aa2904ec64422dccbc0c5855925cdf43a7",
			effectiveBalance: 32000000000,
			expectedHash:     "b1c5e1274eb963ff1a526b955ea87ce78b0feb3cffe1b963339e8b15e389325a",
		},
		{
			name:             "cluster with 64 ETH",
			clusterID:        "b82eab112a1f680c7e7dd45c0ca182aa2904ec64422dccbc0c5855925cdf43a7",
			effectiveBalance: 64000000000,
			expectedHash:     "08dfc4a0b2ac00493a66fcebc50479a3842d56a8bc4dfbeb7cd02d2d0dbf7dcd",
		},
		{
			name:             "on-chain verified cluster (320 ETH)",
			clusterID:        "2d850ebc13f5434cb9b72638555561ab569c5ef9c0f321805a25ff545b3d7430",
			effectiveBalance: 320000000000,
			expectedHash:     "1085ad33da55ab193a1224ad64aa69ee6aa972ade2b004a228b9f975b3de58a8",
		},
		{
			name:             "zero balance",
			clusterID:        "b82eab112a1f680c7e7dd45c0ca182aa2904ec64422dccbc0c5855925cdf43a7",
			effectiveBalance: 0,
			expectedHash:     "a7c8dd319b659f58cec7c4323b8b4ada867efee7941841d0dfcbc046d1d9e9f2",
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
			t.Logf("Balance: %d Gwei (%.2f ETH)", tt.effectiveBalance, float64(tt.effectiveBalance)/1e9)
			t.Logf("Leaf Hash: 0x%x", result)

			// Verify non-zero hash
			zeroHash := [32]byte{}
			if result == zeroHash {
				t.Error("Got zero hash")
			}

			// Verify expected hash if provided
			if tt.expectedHash != "" {
				expectedBytes, _ := hex.DecodeString(tt.expectedHash)
				var expected [32]byte
				copy(expected[:], expectedBytes)
				if result != expected {
					t.Errorf("Hash mismatch:\n  got:      0x%x\n  expected: 0x%s", result, tt.expectedHash)
				}
			}
		})
	}
}

func TestEncodeMerkleLeaf_Deterministic(t *testing.T) {
	clusterID := [32]byte{0x12, 0x34, 0x56}
	balance := uint64(32000000000)

	result1 := EncodeMerkleLeaf(clusterID, balance)
	result2 := EncodeMerkleLeaf(clusterID, balance)

	if result1 != result2 {
		t.Error("EncodeMerkleLeaf is not deterministic")
	}
}

func TestEncodeMerkleLeaf_DifferentBalancesDifferentHashes(t *testing.T) {
	clusterID := [32]byte{0x12, 0x34, 0x56}

	hash1 := EncodeMerkleLeaf(clusterID, 32000000000)
	hash2 := EncodeMerkleLeaf(clusterID, 64000000000)

	if hash1 == hash2 {
		t.Error("Different balances should produce different leaf hashes")
	}

	t.Logf("Hash (32 ETH): 0x%x", hash1)
	t.Logf("Hash (64 ETH): 0x%x", hash2)
}

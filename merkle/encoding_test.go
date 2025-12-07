package merkle

import (
	"encoding/hex"
	"testing"
)

func TestEncodeMerkleLeaf(t *testing.T) {
	tests := []struct {
		name             string
		clusterID        string // hex string
		effectiveBalance uint64
		// expectedHash will be filled in after Solidity verification
		expectedHash string
	}{
		{
			name:             "cluster with 32 ETH",
			clusterID:        "b82eab112a1f680c7e7dd45c0ca182aa2904ec64422dccbc0c5855925cdf43a7",
			effectiveBalance: 32000000000,
			expectedHash:     "", // TODO: Get from Solidity
		},
		{
			name:             "cluster with 64 ETH",
			clusterID:        "b82eab112a1f680c7e7dd45c0ca182aa2904ec64422dccbc0c5855925cdf43a7",
			effectiveBalance: 64000000000,
			expectedHash:     "", // TODO: Get from Solidity
		},
		{
			name:             "different cluster with 31 ETH",
			clusterID:        "000790198eb3653b70bf9af567e804b99636997014a17e1470c4ee8430e46bd2",
			effectiveBalance: 31000000000,
			expectedHash:     "", // TODO: Get from Solidity
		},
		{
			name:             "zero balance",
			clusterID:        "b82eab112a1f680c7e7dd45c0ca182aa2904ec64422dccbc0c5855925cdf43a7",
			effectiveBalance: 0,
			expectedHash:     "", // TODO: Get from Solidity
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Decode cluster ID
			clusterIDBytes, err := hex.DecodeString(tt.clusterID)
			if err != nil {
				t.Fatalf("Failed to decode cluster ID: %v", err)
			}
			var clusterID [32]byte
			copy(clusterID[:], clusterIDBytes)

			// Encode leaf
			result := EncodeMerkleLeaf(clusterID, tt.effectiveBalance)

			t.Logf("Cluster ID: 0x%s", tt.clusterID)
			t.Logf("Balance: %d Gwei", tt.effectiveBalance)
			t.Logf("Leaf Hash: 0x%x", result)

			// For now, just verify we got a non-zero hash
			// Will add exact verification after Solidity tests
			zeroHash := [32]byte{}
			if result == zeroHash {
				t.Error("Got zero hash")
			}

			// TODO: Uncomment after getting Solidity test vectors
			// if tt.expectedHash != "" {
			//     expectedBytes, _ := hex.DecodeString(tt.expectedHash)
			//     var expected [32]byte
			//     copy(expected[:], expectedBytes)
			//     if result != expected {
			//         t.Errorf("Hash mismatch: expected 0x%s, got 0x%x", tt.expectedHash, result)
			//     }
			// }
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

func TestEncodeMerkleLeaf_TestDataFixtures(t *testing.T) {
	// Use cluster IDs from our test data
	fixtures := []struct {
		name             string
		clusterID        string
		effectiveBalance uint64
	}{
		{
			name:             "Cluster 1 - 64 ETH",
			clusterID:        "b183c42279b4dc3eb381352db3458ae66a3439765e4f880a027f62ac2c4edba9",
			effectiveBalance: 64000000000,
		},
		{
			name:             "Cluster 2 - 31 ETH",
			clusterID:        "44ab2ba437cef9cb17bb6bc6af7d87715b9a3e245fbb153e66b09bb79697d316",
			effectiveBalance: 31000000000,
		},
		{
			name:             "Cluster 3 - 32 ETH",
			clusterID:        "a1ea9fe3f3ba4ba4d19a681b639f7033740e55371ee0f00c2e32c15b4fe3c468",
			effectiveBalance: 32000000000,
		},
	}

	for _, tt := range fixtures {
		t.Run(tt.name, func(t *testing.T) {
			clusterIDBytes, _ := hex.DecodeString(tt.clusterID)
			var clusterID [32]byte
			copy(clusterID[:], clusterIDBytes)

			leafHash := EncodeMerkleLeaf(clusterID, tt.effectiveBalance)

			t.Logf("%s:", tt.name)
			t.Logf("  Cluster ID: 0x%s", tt.clusterID)
			t.Logf("  Balance: %d Gwei (%.2f ETH)", tt.effectiveBalance, float64(tt.effectiveBalance)/1e9)
			t.Logf("  Leaf Hash: 0x%x", leafHash)
		})
	}
}
